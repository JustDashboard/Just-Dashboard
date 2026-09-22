import { expect, test } from "bun:test"
import { defaultConfiguration } from "../deployment-defaults"
import { landingStep } from "./draft"

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
