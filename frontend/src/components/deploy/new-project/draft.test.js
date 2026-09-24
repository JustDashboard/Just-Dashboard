import { expect, test } from "bun:test"
import { defaultConfiguration } from "../deployment-defaults"
import {
  configurationForSave,
  firstDeployRevision,
  landingStep,
  mergeDetectedConfiguration,
  persistableFlow,
  rootEditCandidate,
  withSuggestedHostname,
} from "./draft"

/**
 * Where a freshly inspected source opens, for every shape detection produces.
 *
 * The browser fixtures can only carry the shapes they hand-write, which is how
 * a `needsDecision` that every Dockerfile, Go module, Deno project and image
 * emitted went unnoticed while the two-press import it blocked was covered by
 * a candidate that emitted none. These are the real shapes, and the plan under
 * them is the real `defaultConfiguration`.
 */

const candidate = (overrides = {}) => ({
  id: "candidate-1",
  name: "candidate",
  root: "",
  profile: "web",
  buildMethod: "recipe",
  confidence: "high",
  evidence: [],
  needsDecision: [],
  ...overrides,
})

function flowFor({
  candidate: picked,
  candidates,
  detection,
  source,
  profile,
  name,
  nameTaken,
} = {}) {
  const chosen = picked ?? undefined
  const effective = chosen?.profile ?? profile ?? "web"
  const from = { kind: "git", mode: "git_url", ...source }
  const evidence =
    detection || chosen
      ? { candidates: candidates ?? (chosen ? [chosen] : []), ...detection }
      : undefined
  return {
    name: name ?? "app",
    profile: effective,
    source: from,
    draft: { id: "draft-1", revision: 1 },
    candidate: chosen,
    detection: evidence,
    configuration: defaultConfiguration(effective, chosen, from, evidence),
    sourceLabel: "source",
    hostname: nameTaken ? { nameTaken: true } : undefined,
  }
}

test("a Dockerfile whose EXPOSE gave a port is read back on Review", () => {
  expect(
    landingStep(flowFor({ candidate: candidate({ buildMethod: "dockerfile", port: 8080 }) })),
  ).toBe("review")
})

test("a Dockerfile naming no port asks for one where the field is", () => {
  expect(landingStep(flowFor({ candidate: candidate({ buildMethod: "dockerfile" }) }))).toBe(
    "runtime",
  )
})

test("a Go module asks for its port rather than for four screens", () => {
  expect(landingStep(flowFor({ candidate: candidate({ profile: "service", recipe: "go" }) }))).toBe(
    "runtime",
  )
})

test("a Deno project keeps its own default port and lands on Review", () => {
  expect(landingStep(flowFor({ candidate: candidate({ recipe: "deno", port: 8000 }) }))).toBe(
    "review",
  )
})

test("a static site at the root lands on Review", () => {
  const flow = flowFor({
    candidate: candidate({ profile: "static", buildMethod: "static", port: 80 }),
  })
  expect(landingStep(flow)).toBe("review")
})

test("a static site under public/ is its own candidate, so it lands on Review too", () => {
  // Its root is the directory holding the index.html, which is the directory
  // the static build serves — there is no public directory left to name.
  const flow = flowFor({
    candidate: candidate({ profile: "static", buildMethod: "static", root: "public", port: 80 }),
  })
  expect(landingStep(flow)).toBe("review")
})

test("an image whose exposure was read lands on Review", () => {
  const flow = flowFor({
    candidate: candidate({ profile: "image", buildMethod: "image", port: 8080 }),
    source: { kind: "image", mode: "image_reference", image: "ghcr.io/acme/app:1" },
  })
  expect(landingStep(flow)).toBe("review")
})

test("an image this host holds no copy of still owes its decision", () => {
  const flow = flowFor({
    candidate: candidate({
      profile: "image",
      buildMethod: "image",
      needsDecision: ["confirm runtime command, ports, storage, and readiness"],
    }),
    source: { kind: "image", mode: "image_reference", image: "ghcr.io/acme/app:1" },
  })
  expect(landingStep(flow)).toBe("project")
})

test("a name a live project already holds is corrected before Deploy, not by it", () => {
  expect(landingStep(flowFor({ candidate: candidate({ port: 3000 }), nameTaken: true }))).toBe(
    "project",
  )
})

test("a name that cannot be a name opens the screen that owns it", () => {
  expect(landingStep(flowFor({ candidate: candidate({ port: 3000 }), name: "-nope" }))).toBe(
    "project",
  )
})

test("a Compose stack referencing a variable opens the screen that holds it", () => {
  const flow = flowFor({
    candidate: candidate({ profile: "compose", buildMethod: "compose" }),
    source: { kind: "compose", mode: "compose_paste" },
    detection: { compose: { variables: ["API_KEY"] } },
  })
  expect(landingStep(flow)).toBe("variables")
})

test("a clean Compose stack lands on Review", () => {
  const flow = flowFor({
    candidate: candidate({ profile: "compose", buildMethod: "compose" }),
    source: { kind: "compose", mode: "compose_paste" },
    detection: { compose: { variables: [] } },
  })
  expect(landingStep(flow)).toBe("review")
})

test("a variable the source was read as needing, with nothing in it, opens the variables screen", () => {
  const flow = flowFor({
    candidate: candidate({
      port: 3000,
      variables: [{ name: "DATABASE_URL", sources: [".env.example"] }],
    }),
  })
  expect(landingStep(flow)).toBe("variables")
})

test("a worker publishes nothing, so an unset port is not a question", () => {
  expect(landingStep(flowFor({ candidate: candidate({ profile: "worker" }) }))).toBe("review")
})

test("more than one plausible root is the operator's choice", () => {
  const first = candidate({ id: "a", port: 3000 })
  const flow = flowFor({ candidate: first, candidates: [first, candidate({ id: "b" })] })
  expect(landingStep(flow)).toBe("project")
})

test("evidence detection could not reach opens the screen that names the source", () => {
  const flow = flowFor({
    candidate: candidate({ port: 3000 }),
    detection: { unavailable: "Docker is unavailable" },
  })
  expect(landingStep(flow)).toBe("project")
})

test("a static recipe with no output directory is asked where its site is built", () => {
  const flow = flowFor({ candidate: candidate({ profile: "static", buildMethod: "recipe" }) })
  expect(landingStep(flow)).toBe("project")
})

test("?mode=advanced asks for the runtime screen whatever detection answered", () => {
  expect(landingStep(flowFor({ candidate: candidate({ port: 3000 }) }), true)).toBe("runtime")
})

test("automatic hostnames are limited to HTTP workloads and preserve an explicit address", () => {
  const hostname = { hostname: "app.example.test", method: "sslip" }
  for (const profile of ["game", "service", "image", "compose", "worker"]) {
    const configuration = defaultConfiguration(profile)
    expect(
      withSuggestedHostname(configuration, profile, { kind: "blueprint" }, hostname).domains,
    ).toEqual([])
  }
  const web = defaultConfiguration("web")
  expect(withSuggestedHostname(web, "web", { kind: "git" }, hostname).domains[0].hostname).toBe(
    "app.example.test",
  )
  web.domains = [{ hostname: "custom.example.test", https: false, ownership: "managed" }]
  expect(withSuggestedHostname(web, "web", { kind: "git" }, hostname).domains).toEqual(web.domains)
})

test("re-detection changes defaults while preserving operator overrides", () => {
  const old = defaultConfiguration("web", candidate({ port: 3000, startCommand: "bun start" }))
  const edited = structuredClone(old)
  edited.build.startCommand = "bun run custom"
  edited.runtime.memoryMb = 1024
  edited.domains = [{ hostname: "custom.example.test", ownership: "managed", https: true }]
  const detected = defaultConfiguration(
    "web",
    candidate({ port: 8080, startCommand: "node server.js" }),
  )
  const merged = mergeDetectedConfiguration(old, edited, detected)
  expect(merged.runtime.internalPort).toBe(8080)
  expect(merged.runtime.memoryMb).toBe(1024)
  expect(merged.build.startCommand).toBe("bun run custom")
  expect(merged.domains).toEqual(edited.domains)
})

test("saving normalizes blank hostnames and derived readiness ports idempotently", () => {
  const configuration = defaultConfiguration("web", candidate({ port: 3000 }))
  configuration.domains = [{ hostname: " ", ownership: "managed" }]
  configuration.checks[0].config.port = 3000
  const normalized = configurationForSave(configuration)
  expect(normalized.domains).toEqual([])
  expect(normalized.checks[0].config.port).toBeUndefined()
  expect(JSON.stringify(configurationForSave(normalized))).toBe(JSON.stringify(normalized))
  expect(configuration.checks[0].config.port).toBe(3000)
})

test("persisting a flow excludes credentials from both live and server draft snapshots", () => {
  const flow = flowFor({ candidate: candidate({ port: 3000 }) })
  flow.source.url = "https://operator:git-password@example.test/repo.git"
  flow.source.composeFiles = [{ path: "compose.yml", content: "PASSWORD=compose-secret" }]
  flow.configuration.domains = [
    { hostname: "example.test", protection: { username: "reader", password: "visitor-secret" } },
  ]
  flow.draft.data = {
    source: structuredClone(flow.source),
    configuration: structuredClone(flow.configuration),
  }
  flow.detection = { compose: { preview: "# preview-private-value" } }
  flow.draft.data.detection = structuredClone(flow.detection)
  flow.draft.planPreview = JSON.stringify({ compose: { preview: "# preview-private-value" } })
  const saved = persistableFlow(flow)
  for (const secret of [
    "git-password",
    "compose-secret",
    "visitor-secret",
    "preview-private-value",
  ]) {
    expect(JSON.stringify(saved)).not.toContain(secret)
    expect(JSON.stringify(flow)).toContain(secret)
  }
  expect(saved.configuration.domains[0].protection.username).toBe("reader")
})

test("the first deployment builds the commit Review checked, for a remote repository only", () => {
  const revision = "c".repeat(40)
  const detection = { source: { kind: "git", revision }, candidates: [] }
  const passed = [{ code: "source_moved", severity: "warning" }]
  expect(
    firstDeployRevision(
      { kind: "git", mode: "git_url", url: "https://x/y.git" },
      detection,
      passed,
    ),
  ).toBe(revision)
  expect(firstDeployRevision({ kind: "git", mode: "connected_repository" }, detection, [])).toBe(
    revision,
  )
  // A local checkout, a Compose repository or an image cannot be pinned by
  // the run request, and a missing or partial revision is not a commit.
  expect(
    firstDeployRevision({ kind: "local", mode: "local_checkout" }, detection, []),
  ).toBeUndefined()
  expect(
    firstDeployRevision({ kind: "compose", mode: "compose_git" }, detection, []),
  ).toBeUndefined()
  expect(firstDeployRevision({ kind: "git", mode: "git_url" }, undefined, [])).toBeUndefined()
  expect(
    firstDeployRevision(
      { kind: "git", mode: "git_url" },
      { ...detection, source: { revision: "abc123" } },
      [],
    ),
  ).toBeUndefined()
  // A check that could not read the commit passed nothing about it: the
  // deployment follows the branch, as it did before anything was pinned.
  expect(
    firstDeployRevision({ kind: "git", mode: "git_url" }, detection, [
      { code: "source_inspection_unavailable", severity: "unavailable" },
    ]),
  ).toBeUndefined()
})

test("a root typed after detection picks the candidate detected there", () => {
  const web = { id: "web", root: "apps/web", buildMethod: "recipe", recipe: "node" }
  const api = { id: "api", root: "apps/api", buildMethod: "recipe", recipe: "python" }
  const docs = { id: "docs", root: "apps/docs", buildMethod: "static" }
  const detection = { source: { kind: "git" }, candidates: [web, api, docs], selectedId: "web" }
  const build = (rootDirectory, method = "recipe", recipe = "node") => ({
    rootDirectory,
    method,
    recipe,
  })
  expect(rootEditCandidate(detection, web, build("apps/api"))).toBe(api)
  expect(rootEditCandidate(detection, api, build("/apps/web/"))).toBe(web)
  expect(rootEditCandidate(detection, web, build("apps/docs"))).toBeUndefined()
  expect(rootEditCandidate(detection, web, build("apps/missing"))).toBeUndefined()
  expect(rootEditCandidate(undefined, web, build("apps/api"))).toBeUndefined()
  // Nothing picked means nothing was edited away from; the list decides.
  expect(rootEditCandidate(detection, undefined, build("apps/web"))).toBeUndefined()
})

test("a candidate picked at its own root is never swapped for another sharing it", () => {
  // A Django project whose asset package.json is a second candidate at the
  // same root: detection selected Django, and reaching the screen, or the
  // reader picking either one, must not flip the pick to the other.
  const assets = { id: "assets", root: "", buildMethod: "recipe", recipe: "node" }
  const django = { id: "django", root: "", buildMethod: "recipe", recipe: "python" }
  const detection = { source: { kind: "git" }, candidates: [assets, django], selectedId: "django" }
  expect(
    rootEditCandidate(detection, django, { rootDirectory: "", method: "recipe", recipe: "python" }),
  ).toBeUndefined()
  expect(
    rootEditCandidate(detection, assets, { rootDirectory: "", method: "recipe", recipe: "node" }),
  ).toBeUndefined()
  // Edited away and back, the plan's own recipe is preferred at that root.
  const moved = { id: "cli", root: "cli", buildMethod: "recipe", recipe: "go" }
  expect(
    rootEditCandidate({ ...detection, candidates: [assets, django, moved] }, moved, {
      rootDirectory: "",
      method: "recipe",
      recipe: "python",
    }),
  ).toBe(django)
})
