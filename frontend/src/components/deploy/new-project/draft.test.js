import { expect, test } from "bun:test"
import { defaultConfiguration, detectedVariableDeclarations } from "../deployment-defaults"
import { synchronizePrimaryDomain } from "./domain-bindings"
import {
  configurationForSave,
  landingStep,
  mergeDetectedConfiguration,
  persistableFlow,
  railsDatabaseRows,
  withHeldDomainVariables,
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
