import { expect, test } from "bun:test"
import { synchronizePrimaryDomain } from "./domain-bindings"

const domain = { hostname: "old.example.com", https: true, ownership: "managed" }
const configuration = {
  domains: [domain],
  variables: [
    { name: "HOST", sensitivity: "plain", value: domain.hostname, domainTemplate: "{{hostname}}" },
    {
      name: "URL",
      sensitivity: "plain",
      value: "https://old.example.com/",
      domainTemplate: "{{scheme}}://{{hostname}}/",
    },
    {
      name: "CUSTOM_URL",
      sensitivity: "plain",
      value: "https://independent.example.com/",
      domainTemplate: "{{scheme}}://{{hostname}}/",
    },
    { name: "UNRELATED", sensitivity: "plain", value: domain.hostname },
  ],
}

test("reviewed host and URL defaults follow the primary hostname and scheme", () => {
  const updated = synchronizePrimaryDomain(configuration, [
    { ...domain, hostname: " New.example.com ", https: false },
  ])
  expect(updated.variables.map((variable) => variable.value)).toEqual([
    "new.example.com",
    "http://new.example.com/",
    "https://independent.example.com/",
    "old.example.com",
  ])
  expect(configuration.variables[0].value).toBe("old.example.com")
})

test("disabling publishing clears bound defaults, and re-enabling restores them", () => {
  const disabled = synchronizePrimaryDomain(configuration, [])
  expect(disabled.variables.slice(0, 2).map((variable) => variable.value)).toEqual(["", ""])
  const enabled = synchronizePrimaryDomain(disabled, [domain])
  expect(enabled.variables).toEqual(configuration.variables)
})

test("aliases and visitor protection do not rewrite values or advanced references", () => {
  const referenced = {
    ...configuration,
    variables: configuration.variables.map((variable) => ({
      ...variable,
      reference: "${{variable.OVERRIDE}}",
    })),
  }
  const changed = synchronizePrimaryDomain(referenced, [{ ...domain, hostname: "new.example.com" }])
  expect(changed.variables).toEqual(referenced.variables)
  expect(
    synchronizePrimaryDomain(configuration, [
      { ...domain, protection: { username: "viewer", password: "a-long-password" } },
      { ...domain, hostname: "alias.example.com" },
    ]).variables,
  ).toEqual(configuration.variables)
})
