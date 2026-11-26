import { expect, test } from "bun:test"
import { templateDraftSource, templateInputErrors } from "./template-inputs"

test("numeric template inputs respect integer bounds, including advanced defaults", () => {
  const inputs = [{ name: "memory", kind: "memory", minimum: 512, maximum: 8192, default: "2048" }]
  expect(templateInputErrors(inputs, {})).toEqual({})
  expect(templateInputErrors(inputs, { memory: "128" })).toEqual({ memory: "Use at least 512." })
  expect(templateInputErrors(inputs, { memory: "9999" })).toEqual({ memory: "Use at most 8192." })
  expect(templateInputErrors(inputs, { memory: "1024.5" })).toEqual({
    memory: "Enter a whole number.",
  })
  expect(templateInputErrors(inputs, { memory: "Infinity" })).toEqual({
    memory: "Enter a whole number.",
  })
})

test("secret inputs are deferred while required domains and explicit acceptance remain required", () => {
  const inputs = [
    { name: "domain", kind: "domain", required: true },
    { name: "eula", kind: "accept", required: true },
    { name: "password", kind: "secret", required: true },
  ]
  expect(templateInputErrors(inputs, {})).toEqual({
    domain: "Required.",
    eula: "Accept this agreement to continue.",
  })
  expect(templateInputErrors(inputs, { domain: "example.test", eula: "true" })).toEqual({})
})

test("a text input's pattern is checked as typed, and an empty optional one is left alone", () => {
  const inputs = [
    {
      name: "subtitle-languages",
      kind: "text",
      default: "en",
      pattern: "^$|^[a-z]{2}(,[a-z]{2}){0,9}$",
    },
  ]
  expect(templateInputErrors(inputs, {})).toEqual({})
  expect(templateInputErrors(inputs, { "subtitle-languages": "en,fr" })).toEqual({})
  expect(templateInputErrors(inputs, { "subtitle-languages": "" })).toEqual({})
  expect(templateInputErrors(inputs, { "subtitle-languages": "english" })).toEqual({
    "subtitle-languages": "This is not in the expected format.",
  })
  expect(templateInputErrors(inputs, { "subtitle-languages": "en, fr" })).toEqual({
    "subtitle-languages": "This is not in the expected format.",
  })
})

test("a pattern this engine cannot read is left to the server", () => {
  const inputs = [{ name: "name", kind: "text", pattern: "(?i)^[a-z]+$" }]
  expect(templateInputErrors(inputs, { name: "Anything 1" })).toEqual({})
})

test("a template of several services is drafted as a Compose source the server renders", () => {
  const definition = {
    id: "media-complete",
    version: "1.0.0",
    profile: "compose",
    services: [{ name: "jellyfin" }, { name: "seerr" }],
    inputs: [
      { name: "domain", kind: "domain" },
      { name: "quality", kind: "choice", default: "1080p" },
      { name: "token", kind: "secret" },
    ],
  }
  expect(
    templateDraftSource(definition, { domain: "media.example.test", token: "never sent" }),
  ).toEqual({
    profile: "compose",
    source: {
      kind: "compose",
      mode: "blueprint",
      blueprintId: "media-complete",
      blueprintVersion: "1.0.0",
      // Quality was not touched, so the definition's default stands.
      blueprintInputs: { domain: "media.example.test" },
    },
  })
})

test("one reviewed image is drafted as a blueprint source, and a game keeps its profile", () => {
  expect(templateDraftSource({ id: "uptime-kuma", version: "1.0.0", profile: "web" }, {})).toEqual({
    profile: "service",
    source: {
      kind: "blueprint",
      mode: "blueprint",
      blueprintId: "uptime-kuma",
      blueprintVersion: "1.0.0",
      blueprintInputs: {},
    },
  })
  expect(
    templateDraftSource({ id: "minecraft-java", version: "1.0.0", profile: "game" }, {}).profile,
  ).toBe("game")
})
