import { expect, test } from "bun:test"
import { templateInputErrors } from "./template-inputs"

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
