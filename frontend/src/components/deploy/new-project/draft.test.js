import { expect, test } from "bun:test"
import { defaultConfiguration, detectedVariableDeclarations } from "../deployment-defaults"
import { synchronizePrimaryDomain } from "./domain-bindings"
import {
  automaticCloneOptions,
  candidateStanding,
  configurationForSave,
  firstDeployRevision,
  landingStep,
  lfsFilesForRoot,
  mergeDetectedConfiguration,
  persistableFlow,
  railsDatabaseRows,
  submodulesForRoot,
  withHeldDomainVariables,
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

test("variables detection answered itself do not stop the import on the variables screen", () => {
  const flow = flowFor({
    candidate: candidate({
      port: 3000,
      variables: [
        {
          name: "AUTH_SECRET",
          sources: [".env.example"],
          setup: "generate",
          generateLength: 32,
          generateFormat: "base64",
        },
        {
          name: "AUTH_URL",
          sources: [".env.example"],
          setup: "domain",
          domainTemplate: "{{scheme}}://{{hostname}}",
        },
      ],
    }),
  })
  expect(landingStep(flow)).toBe("review")
  const paste = flowFor({
    candidate: candidate({
      port: 3000,
      variables: [
        { name: "RAILS_MASTER_KEY", sources: ["config/credentials.yml.enc"], setup: "paste" },
      ],
    }),
  })
  expect(landingStep(paste)).toBe("variables")
})

test("an address that follows the domain takes the suggested hostname and waits without one", () => {
  const configuration = defaultConfiguration(
    "web",
    candidate({
      port: 3000,
      variables: [
        {
          name: "AUTH_URL",
          sources: [".env.example"],
          setup: "domain",
          domainTemplate: "{{scheme}}://{{hostname}}",
        },
      ],
    }),
  )
  // Nothing to follow yet: committed empty it would be "", which an
  // application reads as a value, so it waits in the form.
  expect(configurationForSave(configuration).variables).toEqual([])
  const hosted = withSuggestedHostname(
    configuration,
    "web",
    { kind: "git" },
    { hostname: "App.example.test", method: "sslip" },
  )
  expect(hosted.variables[0].value).toBe("https://app.example.test")
  expect(configurationForSave(hosted).variables).toHaveLength(1)
})

test("Rails' further databases follow the link as their own database names", () => {
  expect(railsDatabaseRows(7, "shop", ["CACHE_DATABASE_URL", "QUEUE_DATABASE_URL"])).toEqual([
    { name: "CACHE_DATABASE_URL", value: "${{database.7.url.shop_cache}}" },
    { name: "QUEUE_DATABASE_URL", value: "${{database.7.url.shop_queue}}" },
  ])
  expect(railsDatabaseRows(7, "", ["CACHE_DATABASE_URL"])).toEqual([])
  expect(railsDatabaseRows(7, "a-b", ["CACHE_DATABASE_URL"])).toEqual([])
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

test("an address held back while no domain is planned binds to a domain added after the check", () => {
  const origin = {
    name: "ORIGIN",
    sources: ["package.json"],
    setup: "domain",
    domainTemplate: "{{scheme}}://{{hostname}}",
  }
  const configuration = defaultConfiguration("web", candidate({ port: 3000, variables: [origin] }))
  // Review's automatic check saves without the address, and the server hands
  // back what it saved.
  const toSave = configurationForSave(configuration)
  expect(toSave.variables).toEqual([])
  const canonical = withHeldDomainVariables(toSave, configuration.variables)
  expect(configurationForSave(canonical)).toEqual(toSave)
  const hosted = synchronizePrimaryDomain(canonical, [
    { hostname: "shop.example.test", https: true, ownership: "managed" },
  ])
  expect(hosted.variables.find((variable) => variable.name === "ORIGIN")?.value).toBe(
    "https://shop.example.test",
  )
  // A saved draft read back later gets it from detection, bound to its domain.
  const resumed = withHeldDomainVariables(
    { ...toSave, domains: [{ hostname: "app.example.test", https: true, ownership: "managed" }] },
    detectedVariableDeclarations(candidate({ variables: [origin] }), "web"),
  )
  expect(resumed.variables.find((variable) => variable.name === "ORIGIN")?.value).toBe(
    "https://app.example.test",
  )
})

/**
 * The clone options detection has already answered. A theme submodule on the
 * repository's own host and LFS files under the build root are turned on at
 * inspection; a submodule elsewhere stays the operator's question.
 */
test("clone options detection answered are switched on for the chosen root", () => {
  const git = { kind: "git", mode: "git_url" }
  const requirements = (overrides) => ({
    candidates: [],
    gitRequirements: {
      submodules: true,
      lfs: true,
      submodulesChecked: true,
      lfsChecked: true,
      submoduleList: [{ path: "themes/ananke", sameSource: true }],
      lfsFiles: 2,
      lfsPaths: ["site/images/a.png", "assets/b.bin"],
      ...overrides,
    },
  })
  expect(automaticCloneOptions(requirements(), candidate({ root: "" }), git)).toEqual({
    includeSubmodules: true,
    includeLfs: true,
  })
  expect(automaticCloneOptions(requirements(), candidate({ root: "site" }), git)).toEqual({
    includeLfs: true,
  })
  expect(
    automaticCloneOptions(
      requirements({ submoduleList: [{ path: "vendor/x", sameSource: false }], lfsFiles: 0 }),
      candidate({ root: "" }),
      git,
    ),
  ).toBeUndefined()
  expect(
    automaticCloneOptions(requirements(), candidate({ root: "" }), {
      ...git,
      includeSubmodules: true,
      includeLfs: true,
    }),
  ).toBeUndefined()
  expect(
    automaticCloneOptions(
      requirements({ submodulesChecked: false, lfsChecked: false }),
      candidate({ root: "" }),
      git,
    ),
  ).toBeUndefined()
  expect(
    automaticCloneOptions(requirements(), candidate({ root: "" }), {
      kind: "image",
      mode: "image",
    }),
  ).toBeUndefined()
})

/**
 * The step and preflight judge a build root's submodules and LFS files the
 * same way: a root inside a submodule needs it, and a nested root's LFS count
 * is unknown, not zero, when the listed paths were cut short.
 */
test("submodules and LFS files are counted for the build root", () => {
  const requirements = {
    submodules: true,
    lfs: true,
    submoduleList: [
      { path: "themes/ananke", sameSource: true },
      { path: "site", sameSource: false },
    ],
    lfsFiles: 300,
    lfsPaths: Array.from({ length: 256 }, (_, index) => `assets/${index}.png`),
  }
  expect(submodulesForRoot(requirements, "site/public").map((item) => item.path)).toEqual(["site"])
  expect(submodulesForRoot(requirements, "themes").map((item) => item.path)).toEqual([
    "themes/ananke",
  ])
  expect(submodulesForRoot(requirements, "").length).toBe(2)
  expect(lfsFilesForRoot(requirements, "")).toBe(300)
  expect(lfsFilesForRoot(requirements, "assets")).toBe(256)
  expect(lfsFilesForRoot(requirements, "site")).toBeUndefined()
  expect(lfsFilesForRoot({ ...requirements, lfsFiles: 256 }, "site")).toBe(0)
  expect(
    automaticCloneOptions(
      { candidates: [], gitRequirements: { ...requirements, lfsChecked: true } },
      candidate({ root: "site" }),
      { kind: "git", mode: "git_url" },
    ),
  ).toBeUndefined()
})

test("the candidate chooser says why a candidate is not the application", () => {
  expect(candidateStanding(candidate({ notDeployable: "library" }))).toBe(
    "Not a service: a library",
  )
  expect(candidateStanding(candidate({ demotion: "examples/basic is an example" }))).toBe(
    "examples/basic is an example",
  )
  expect(candidateStanding(candidate({ recipeIssue: "Haskell has no automatic recipe" }))).toBe(
    "No automatic recipe",
  )
  expect(candidateStanding(candidate({ recipe: "node" }))).toBeUndefined()
})

test("a source that is not a service, or has a template, opens on the project step", () => {
  expect(landingStep(flowFor({ candidate: candidate({ notDeployable: "library" }) }))).toBe(
    "project",
  )
  expect(
    landingStep(
      flowFor({
        candidate: candidate(),
        detection: {
          alternatives: [{ kind: "template", ref: "n8n", label: "n8n", evidence: "reviewed" }],
        },
      }),
    ),
  ).toBe("project")
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
