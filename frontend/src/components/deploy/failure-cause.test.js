import { describe, expect, test } from "bun:test"
import {
  causeHeadline,
  causeTitle,
  deployWithCurrentSettings,
  driftLine,
  failureLabel,
  failureCause,
  fixTarget,
  isReleaseTaskFailure,
  variableFixLabel,
  runPlanIsStale,
  settingsChangeText,
} from "./failure-cause"

const step = (overrides) => ({
  id: 7,
  runId: 3,
  key: "build_artifact",
  ordinal: 4,
  state: "passed",
  attempt: 1,
  timeoutSeconds: 0,
  evidence: {},
  lastSeq: 0,
  ...overrides,
})

describe("causeTitle", () => {
  test("names a known cause and reads any other code as a sentence", () => {
    expect(causeTitle("build_lockfile_out_of_sync")).toBe("Lockfile out of sync")
    expect(causeTitle("runtime_port_mismatch")).toBe("Application listens on another port")
    expect(causeTitle("health_gate_failed")).toBe("Health gate failed")
  })
  test("names the causes read from state, secrets and the kernel's sockets too", () => {
    expect(causeTitle("runtime_sqlite_not_writable")).toBe("SQLite database not writable")
    expect(causeTitle("runtime_auth_untrusted_host")).toBe("Host not trusted by Auth.js")
    expect(causeTitle("runtime_loopback_bind")).toBe("Application listens on localhost only")
  })
})

describe("causeHeadline", () => {
  test("names the object the evidence proved", () => {
    expect(causeHeadline({ code: "build_lockfile_out_of_sync", detail: "package-lock.json" })).toBe(
      "package-lock.json is out of sync",
    )
    expect(causeHeadline({ code: "build_command_not_found", subjects: ["npm"] })).toBe(
      "`npm` not found",
    )
    expect(causeHeadline({ code: "build_env_missing", subjects: ["DATABASE_URL"] })).toBe(
      "DATABASE_URL missing at build",
    )
    expect(causeHeadline({ code: "schema_missing", table: "public.users" })).toBe(
      "Table public.users does not exist",
    )
    expect(causeHeadline({ code: "build_command_not_found" })).toBe("Command not found")
  })
})

describe("failureCause", () => {
  test("reads a build's cause, a health gate's diagnostics and nothing from a passing run", () => {
    const build = failureCause([
      step({ key: "analyze_plan" }),
      step({
        state: "failed",
        evidence: { cause: { code: "build_lockfile_out_of_sync", lineSeq: 42 } },
      }),
    ])
    expect(build.step.key).toBe("build_artifact")
    expect(build.cause).toEqual({ code: "build_lockfile_out_of_sync", lineSeq: 42 })

    const gate = failureCause([
      step({
        key: "verify_readiness",
        state: "failed",
        evidence: { health: {}, diagnostics: { cause: { code: "runtime_env_missing" } } },
      }),
    ])
    expect(gate.cause.code).toBe("runtime_env_missing")

    expect(failureCause([step({ state: "failed", evidence: { cause: { code: "" } } })]).cause).toBe(
      undefined,
    )
    expect(failureCause([step({})])).toBe(undefined)
  })
})

describe("fixTarget", () => {
  test("opens the field the fix targets, with a variable's editor filled in", () => {
    expect(
      fixTarget(5, {
        kind: "set_build",
        field: "configuration.build.packageManager",
        value: "bun",
      }),
    ).toEqual({ href: "/deploy/5/settings/build#build", label: "Build with Bun" })
    expect(
      fixTarget(5, {
        kind: "add_variable",
        field: "variables.NODE_OPTIONS",
        value: "--max-old-space-size=3072",
        scope: "build",
      }),
    ).toEqual({
      href: "/deploy/5/settings/variables?variable=NODE_OPTIONS&scope=build&value=--max-old-space-size%3D3072",
      label: "Add NODE_OPTIONS=--max-old-space-size=3072",
    })
    expect(
      fixTarget(5, {
        kind: "variable_scope",
        field: "variables.DATABASE_URL",
        value: "build",
        scope: "build",
      }),
    ).toEqual({
      href: "/deploy/5/settings/variables?variable=DATABASE_URL&scope=build",
      label: "Give DATABASE_URL the build scope",
    })
    expect(
      fixTarget(5, {
        kind: "remove_variable_scope",
        field: "variables.DATABASE_URL",
        scope: "build",
      }),
    ).toEqual({
      href: "/deploy/5/settings/variables?variable=DATABASE_URL&without=build",
      label: "Remove DATABASE_URL's build scope",
    })
    expect(
      variableFixLabel({
        kind: "remove_variable_scope",
        field: "variables.DATABASE_URL",
        scope: "build",
      }),
    ).toBe("Remove DATABASE_URL's build scope")
    expect(
      fixTarget(5, { kind: "set_runtime", field: "runtime.internalPort", value: "3000" }),
    ).toEqual({ href: "/deploy/5/settings/runtime#runtime", label: "Set the port to 3000" })
    expect(fixTarget(5, { kind: "review", field: "configuration.build.rootDirectory" })).toEqual({
      href: "/deploy/5/settings/build#commands",
      label: "Review the root directory",
    })
    expect(fixTarget(5, { kind: "review", field: "dependencies" }).href).toBe(
      "/deploy/5/settings/databases",
    )
    expect(fixTarget(5, { kind: "review", field: "runtime.mounts" })).toEqual({
      href: "/deploy/5/settings/storage",
      label: "Review the storage",
    })
    expect(fixTarget(5, { kind: "review", field: "somewhere.else" })).toBe(undefined)
  })
})

describe("driftLine", () => {
  test("says what changed since the run in one line", () => {
    const drift = {
      runId: 3,
      planRevision: 4,
      desiredRevision: 6,
      changed: true,
      changes: [
        { kind: "build", field: "packageManager", change: "changed", before: "npm", after: "bun" },
        {
          kind: "variable",
          field: "DATABASE_URL",
          change: "scope",
          before: "runtime",
          after: "build,runtime",
        },
        { kind: "variable", field: "NODE_OPTIONS", change: "added", after: "build" },
        {
          kind: "runtime",
          field: "internalPort",
          change: "changed",
          before: "8080",
          after: "3000",
        },
      ],
    }
    expect(driftLine(drift)).toBe(
      "Settings changed since this run: packageManager npm → bun; DATABASE_URL now available at build; NODE_OPTIONS added; and 1 more.",
    )
    expect(driftLine({ ...drift, changed: false })).toBe(undefined)
    expect(driftLine(undefined)).toBe(undefined)
    expect(settingsChangeText({ kind: "variable", field: "TOKEN", change: "changed" })).toBe(
      "TOKEN value changed",
    )
    expect(settingsChangeText({ kind: "source", field: "source", change: "changed" })).toBe(
      "the source",
    )
  })
})

describe("deploying with current settings", () => {
  test("pins the run's commit only where a revision applies", () => {
    const run = { sourceRevision: "a".repeat(40), planRevision: 2, environmentId: 1 }
    const git = { sourceKind: "git", sourceRemote: "https://github.com/a/b" }
    const drift = { changes: [{ kind: "build", field: "packageManager", change: "changed" }] }
    expect(deployWithCurrentSettings(run, git, drift)).toEqual({
      operation: "deploy",
      sourceRevision: "a".repeat(40),
    })
    expect(deployWithCurrentSettings(run, { sourceKind: "local" }, drift)).toEqual({
      operation: "deploy",
    })
    expect(deployWithCurrentSettings({}, { sourceKind: "git", sourceRemote: "x" }, drift)).toEqual({
      operation: "deploy",
    })
  })

  test("never asks a changed or unknown source for the run's old commit", () => {
    const run = { sourceRevision: "a".repeat(40) }
    const git = { sourceKind: "git", sourceRemote: "https://github.com/a/b" }
    const moved = {
      changes: [
        { kind: "build", field: "packageManager", change: "changed" },
        { kind: "source", field: "source", change: "changed" },
      ],
    }
    expect(deployWithCurrentSettings(run, git, moved)).toEqual({ operation: "deploy" })
    expect(deployWithCurrentSettings(run, git, undefined)).toEqual({ operation: "deploy" })
  })

  test("a run is stale once a later plan is saved for its environment", () => {
    const deployment = { desiredRevision: 3, environmentId: 1 }
    expect(runPlanIsStale({ planRevision: 2, environmentId: 1 }, deployment)).toBe(true)
    expect(runPlanIsStale({ planRevision: 3, environmentId: 1 }, deployment)).toBe(false)
    expect(runPlanIsStale({ planRevision: 2, environmentId: 9 }, deployment)).toBe(false)
    expect(runPlanIsStale({ planRevision: 2, environmentId: 1 }, undefined)).toBe(false)
  })
})

describe("isReleaseTaskFailure", () => {
  test("reads every release task cause and no other release_ fault", () => {
    expect(isReleaseTaskFailure("release_task_failed")).toBe(true)
    expect(isReleaseTaskFailure("release_migration_failed")).toBe(true)
    expect(isReleaseTaskFailure("release_env_missing")).toBe(true)
    expect(isReleaseTaskFailure("release_pointer_mismatch")).toBe(false)
    expect(isReleaseTaskFailure("build_failed")).toBe(false)
    expect(isReleaseTaskFailure(undefined)).toBe(false)
  })
})

describe("failureLabel", () => {
  test("names a cause and only the place for a code that says where", () => {
    expect(failureLabel("build_lockfile_out_of_sync")).toBe("Lockfile out of sync")
    expect(failureLabel("build_failed")).toBe("Build")
    expect(failureLabel("health_gate_failed")).toBe("Health gate")
    expect(failureLabel("activation_failed")).toBe("Activation")
  })
})
