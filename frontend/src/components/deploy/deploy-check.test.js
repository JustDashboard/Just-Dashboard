import { describe, expect, test } from "bun:test"
import {
  attentionFindings,
  cachedDeploymentCheck,
  confirmationSignature,
  needsConfirmation,
  rememberDeploymentCheck,
  settingsPathForField,
  stopsDeployment,
} from "./deploy-check-state"

const finding = (code, severity, overrides = {}) => ({
  code,
  severity,
  title: code,
  ...overrides,
})

const check = (findings, overrides = {}) => ({
  findings,
  planRevision: 4,
  sourceRevision: "a".repeat(40),
  checkedAt: "2026-09-24T10:00:00Z",
  ...overrides,
})

describe("what stops a deployment", () => {
  test("blocked findings and the decisions analyze_plan cannot pass", () => {
    expect(stopsDeployment(finding("recipe_unsupported", "blocked"))).toBe(true)
    expect(stopsDeployment(finding("readiness_missing", "decision"))).toBe(true)
    expect(stopsDeployment(finding("go_main_ambiguous", "decision"))).toBe(true)
    expect(stopsDeployment(finding("git_submodules", "decision"))).toBe(false)
    expect(stopsDeployment(finding("plan_drift_framework", "warning"))).toBe(false)
  })

  test("attention lists what stops the deployment first and drops passes", () => {
    const listed = attentionFindings([
      finding("plan_drift_framework", "warning"),
      finding("source_identity", "pass"),
      finding("git_submodules", "decision"),
      finding("dns_unverified", "unavailable"),
      finding("recipe_unsupported", "blocked"),
    ])
    expect(listed.map((item) => item.code)).toEqual([
      "recipe_unsupported",
      "git_submodules",
      "plan_drift_framework",
      "dns_unverified",
    ])
  })
})

describe("Ready to deploy?", () => {
  test("a clean check, or none, deploys at once", () => {
    expect(needsConfirmation(undefined)).toBe(false)
    expect(needsConfirmation(check([finding("source_identity", "pass")]))).toBe(false)
    expect(needsConfirmation(check([finding("dns_unverified", "unavailable")]))).toBe(false)
  })

  test("something that stops the deployment always asks", () => {
    const blocked = check([finding("package_manager_lockfile_missing", "blocked")])
    expect(needsConfirmation(blocked, confirmationSignature(blocked.findings))).toBe(true)
  })

  test("warnings ask until exactly these ones are confirmed", () => {
    const warned = check([
      finding("public_bind", "warning", { measured: "0.0.0.0" }),
      finding("plan_drift_variables", "warning", { measured: "SENTRY_DSN" }),
    ])
    expect(needsConfirmation(warned)).toBe(true)
    const confirmed = confirmationSignature(warned.findings)
    expect(needsConfirmation(warned, confirmed)).toBe(false)
    // The same warnings listed in another order are the same warnings.
    expect(needsConfirmation(check([...warned.findings].reverse()), confirmed)).toBe(false)
    // A warning about something new asks again.
    const moved = check([
      finding("public_bind", "warning", { measured: "0.0.0.0" }),
      finding("plan_drift_variables", "warning", { measured: "SENTRY_DSN, NEW_KEY" }),
    ])
    expect(needsConfirmation(moved, confirmed)).toBe(true)
  })
})

describe("where a finding's field lives", () => {
  test.each([
    ["configuration.build.packageManager", "/settings/build"],
    ["configuration.build.startCommand", "/settings/build#commands"],
    ["configuration.build.rootDirectory", "/settings/build#commands"],
    ["configuration.build.goPackage", "/settings/build"],
    ["build.startCommand", "/settings/build#commands"],
    ["source.ref", "/settings/general#source"],
    ["runtime.mounts", "/settings/storage"],
    ["runtime.hostPort", "/settings/runtime"],
    ["checks", "/settings/runtime#health-checks"],
    ["domains.app.example.test", "/settings/domains"],
    ["variables.DATABASE_URL", "/settings/variables"],
    ["dependencies.database.12", "/settings/databases"],
  ])("%s opens %s", (field, path) => {
    expect(settingsPathForField(field)).toBe(path)
  })

  test("a field no page owns opens nothing", () => {
    expect(settingsPathForField(undefined)).toBeUndefined()
    expect(settingsPathForField("runtimeish")).toBeUndefined()
  })
})

describe("the check a card reuses", () => {
  test("only for the revision it was about, and only while recent", () => {
    const result = check([finding("public_bind", "warning")], { planRevision: 7 })
    rememberDeploymentCheck(91, result, 1_000)
    expect(cachedDeploymentCheck(91, 7, 2_000)).toBe(result)
    expect(cachedDeploymentCheck(91, undefined, 2_000)).toBe(result)
    expect(cachedDeploymentCheck(91, 8, 2_000)).toBeUndefined()
    expect(cachedDeploymentCheck(91, 7, 1_000 + 5 * 60 * 1000 + 1)).toBeUndefined()
    expect(cachedDeploymentCheck(92, 7, 2_000)).toBeUndefined()
  })
})
