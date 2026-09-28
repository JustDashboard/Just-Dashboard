import { describe, expect, test } from "bun:test"
import {
  autoDeployReading,
  groupedState,
  projectProduct,
  runActor,
  runRevision,
  runTriggerLine,
  sourceLine,
  sourceProduct,
  stepStateLabel,
} from "./vocabulary"

const summary = (overrides) => ({
  id: 7,
  name: "api",
  profile: "web",
  sourceKind: "git",
  buildMethod: "recipe",
  ...overrides,
})

const run = (overrides) => ({ trigger: "manual", actor: "operator", metadata: {}, ...overrides })

describe("projectProduct", () => {
  test("a template is its own product, by the id before its version", () => {
    expect(projectProduct(summary({ sourceKind: "blueprint", sourceRef: "n8n@1.2.3" }))).toBe("n8n")
    expect(
      projectProduct(summary({ sourceKind: "blueprint", sourceRef: "minecraft-java@2.0.0" })),
    ).toBe("minecraft-java")
  })

  test("a template with no logo falls back to its image, and not to the whale", () => {
    const unknown = summary({ sourceKind: "blueprint", sourceRef: "acme-tool@1.0.0" })
    expect(projectProduct({ ...unknown, sourceRepository: "grafana/grafana:11" })).toBe("grafana")
    expect(projectProduct({ ...unknown, sourceRepository: "acme/tool:1" })).toBeUndefined()
  })

  test("an image is the product it is, and Docker when nothing names it", () => {
    const image = summary({ sourceKind: "image", buildMethod: "image" })
    expect(projectProduct({ ...image, sourceRepository: "ghcr.io/acme/grafana:11" })).toBe(
      "grafana",
    )
    expect(projectProduct({ ...image, sourceRepository: "acme/api:1" })).toBe("docker")
    expect(projectProduct(image)).toBe("docker")
  })

  test("a Compose stack is Compose", () => {
    expect(projectProduct(summary({ sourceKind: "compose", buildMethod: "compose" }))).toBe(
      "docker-compose",
    )
  })

  test("an adopted workload is the first image anything names", () => {
    const adopted = summary({ sourceKind: "import", buildMethod: "none" })
    expect(projectProduct({ ...adopted, images: ["acme/api:1", "postgres:16"] })).toBe("postgres")
    expect(projectProduct({ ...adopted, images: ["acme/api:1"] })).toBeUndefined()
    expect(projectProduct(adopted)).toBeUndefined()
  })

  test("a repository is its framework, then what its build makes of it", () => {
    expect(projectProduct(summary({ framework: "nextjs", recipe: "node" }))).toBe("nextjs")
    expect(projectProduct(summary({ framework: "parcel", recipe: "node" }))).toBe("nodejs")
    expect(projectProduct(summary({ recipe: "python" }))).toBe("python")
    expect(projectProduct(summary({ buildMethod: "dockerfile" }))).toBe("docker")
    expect(projectProduct(summary({ buildMethod: "static" }))).toBe("nginx")
    expect(projectProduct(summary({ buildMethod: "legacy_compose" }))).toBe("docker-compose")
    expect(projectProduct(summary({}))).toBeUndefined()
    expect(projectProduct(summary({ buildMethod: "none" }))).toBeUndefined()
  })

  test("the configuration and the template say it more precisely when they are in hand", () => {
    const configuration = {
      build: { method: "recipe", recipe: "node", packageManager: "bun" },
      source: { kind: "git", mode: "url" },
    }
    expect(projectProduct(summary({ recipe: "node" }), configuration)).toBe("bun")
    expect(
      projectProduct(summary({ sourceKind: "blueprint", sourceRef: "" }), undefined, {
        id: "grafana",
        image: "grafana/grafana:11",
      }),
    ).toBe("grafana")
  })
})

describe("sourceProduct", () => {
  test("a repository is the forge its remote is on, and git when none is named", () => {
    expect(sourceProduct(summary({ sourceRemote: "https://github.com/acme/api" }))).toBe("github")
    expect(sourceProduct(summary({ sourceRemote: "git@gitlab.com:acme/api.git" }))).toBe("gitlab")
    expect(sourceProduct(summary({ sourceRemote: "https://git.example.com/acme/api" }))).toBe("git")
    expect(sourceProduct(summary({}), { provider: "gitea" })).toBe("gitea")
    expect(sourceProduct(summary({}), { mode: "connected_repository" })).toBe("github")
    expect(sourceProduct(summary({ sourceKind: "local" }))).toBe("git")
  })

  test("the other kinds are the product their source is", () => {
    expect(sourceProduct(summary({ sourceKind: "image", sourceRepository: "redis:7" }))).toBe(
      "redis",
    )
    expect(sourceProduct(summary({ sourceKind: "compose" }))).toBe("docker-compose")
    expect(sourceProduct(summary({ sourceKind: "blueprint", sourceRef: "n8n@1.0.0" }))).toBe("n8n")
    expect(
      sourceProduct(summary({ sourceKind: "blueprint", sourceRef: "acme@1.0.0" })),
    ).toBeUndefined()
    expect(sourceProduct(summary({ sourceKind: "import" }))).toBe("docker")
  })
})

describe("runTriggerLine", () => {
  test("a person is named, and a machine never is", () => {
    expect(runTriggerLine(run({}))).toBe("by operator")
    expect(runTriggerLine(run({ actor: "" }))).toBe("manual")
    expect(runTriggerLine(run({ trigger: "rollback", actor: "ion" }))).toBe("by ion")
    expect(runTriggerLine(run({ trigger: "git_push", actor: "git-monitor" }))).toBe("on push")
    expect(runTriggerLine(run({ trigger: "github", actor: "webhook" }))).toBe("GitHub push")
    expect(runTriggerLine(run({ trigger: "gitlab", actor: "webhook" }))).toBe("GitLab push")
    expect(runTriggerLine(run({ trigger: "generic_hook", actor: "webhook" }))).toBe("by webhook")
    expect(runTriggerLine(run({ trigger: "api", actor: "webhook" }))).toBe("by API")
    expect(runTriggerLine(run({ trigger: "preview", actor: "webhook" }))).toBe("on pull request")
    expect(runTriggerLine(run({ trigger: "preview", actor: "ion" }))).toBe("by ion")
  })

  test("a schedule is named by its own name when the run recorded one", () => {
    const scheduled = run({ trigger: "schedule", actor: "scheduler:4" })
    expect(runTriggerLine({ ...scheduled, metadata: { scheduleName: "nightly" } })).toBe(
      "on schedule · nightly",
    )
    expect(runTriggerLine(scheduled)).toBe("on schedule")
  })

  test("runActor draws the forge, the watcher and the person", () => {
    expect(runActor(run({ trigger: "bitbucket", actor: "webhook" }))).toEqual({
      kind: "provider",
      product: "bitbucket",
    })
    expect(runActor(run({ trigger: "git_push", actor: "git-monitor" }))).toEqual({
      kind: "push",
      product: "git",
    })
    expect(runActor(run({}))).toEqual({ kind: "person", name: "operator" })
    expect(runActor(run({ trigger: "migration", actor: "system" }))).toEqual({ kind: "system" })
  })
})

describe("runRevision", () => {
  test("a run's own revision, its recorded commit, then its release — never the project's", () => {
    expect(runRevision({ sourceRevision: "abc", metadata: {} })).toBe("abc")
    expect(runRevision({ metadata: { commit: { sha: "def" } } }, { sourceRevision: "999" })).toBe(
      "def",
    )
    expect(runRevision({ metadata: {} }, { sourceRevision: "999" })).toBe("999")
    expect(runRevision({ sourceRevision: "", metadata: {} })).toBeUndefined()
  })
})

describe("groupedState", () => {
  const step = (key, state) => ({ key, state })

  test("a stage the run has no step for is not part of it, rather than waiting", () => {
    const steps = [step("resolve_source", "passed"), step("build_artifact", "passed")]
    expect(groupedState(steps, ["provision_certificate"])).toBe("absent")
    expect(stepStateLabel("absent")).toBe("Not part of this run")
  })

  test("before the steps are read, every stage is still to come", () => {
    expect(groupedState([], ["provision_certificate"])).toBe("pending")
  })

  test("a stage takes its worst step, and is done once all of them are", () => {
    const keys = ["prepare_context", "build_artifact"]
    expect(
      groupedState([step("prepare_context", "passed"), step("build_artifact", "running")], keys),
    ).toBe("running")
    expect(
      groupedState([step("prepare_context", "passed"), step("build_artifact", "skipped")], keys),
    ).toBe("passed")
    expect(groupedState([step("prepare_context", "pending")], keys)).toBe("pending")
  })
})

describe("autoDeployReading", () => {
  const watch = (overrides) => ({
    automatic: true,
    status: "watching",
    intervalSeconds: 5,
    ...overrides,
  })

  test("one reading, with how often it looks only while it is on", () => {
    expect(autoDeployReading(watch({}))).toEqual({
      tone: "running",
      label: "Auto-deploy on",
      interval: 5,
    })
    expect(autoDeployReading(watch({ automatic: false }))).toEqual({
      tone: "stopped",
      label: "Manual deployments",
    })
    expect(autoDeployReading(watch({ policy: { automatic: false } }))?.label).toBe(
      "Manual deployments",
    )
    expect(autoDeployReading(watch({ status: "stale" }))?.tone).toBe("warning")
    expect(autoDeployReading(watch({ status: "awaiting_first_deployment" }))?.label).toBe(
      "Auto-deploy after first deployment",
    )
    expect(autoDeployReading(watch({ status: "not_applicable" }))).toBeUndefined()
  })

  test("a branch it could not read says why when git said", () => {
    expect(
      autoDeployReading(
        watch({ status: "unavailable", reason: "ref_not_found", branch: "master" }),
      ),
    ).toEqual({ tone: "warning", label: "Auto-deploy stopped: master no longer exists" })
    expect(
      autoDeployReading(watch({ status: "unavailable", reason: "source_auth_failed" }))?.label,
    ).toBe("Auto-deploy stopped: the credential was refused")
    expect(
      autoDeployReading(watch({ status: "unavailable", reason: "source_unavailable" }))?.label,
    ).toBe("Auto-deploy needs attention")
    expect(autoDeployReading(watch({ status: "stale", reason: "ref_not_found" }))?.label).toBe(
      "Auto-deploy needs attention",
    )
  })
})

describe("sourceLine", () => {
  test("an image is named by its reference, not its digest", () => {
    const image = summary({
      sourceKind: "image",
      sourceRepository: "ghcr.io/acme/api:1",
      sourceRevision: "sha256:0123456789abcdef0123",
    })
    expect(sourceLine(image).primary).toBe("ghcr.io/acme/api:1")
  })
})
