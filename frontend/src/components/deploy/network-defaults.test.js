import { describe, expect, test } from "bun:test"
import {
  defaultConfiguration,
  discoveredEnvironmentRows,
  networkVariables,
  validateConfiguration,
} from "./deployment-defaults"
import { synchronizePrimaryDomain } from "./new-project/domain-bindings"

const candidate = (overrides) => ({
  id: "fixture",
  name: "Fixture",
  root: "",
  profile: "web",
  buildMethod: "recipe",
  confidence: "high",
  evidence: [],
  needsDecision: [],
  port: 3000,
  ...overrides,
})

describe("variables the proxy decides", () => {
  const auth = candidate({
    networkVariables: [
      { name: "AUTH_TRUST_HOST", value: "true", reason: "next-auth 5" },
      { name: "NEXTAUTH_URL", domainTemplate: "{{scheme}}://{{hostname}}", reason: "next-auth 4" },
    ],
    variables: [
      { name: "AUTH_TRUST_HOST", sources: [".env.example"] },
      { name: "AUTH_SECRET", sources: [".env.example"] },
    ],
  })

  test("arrive as plain, removable plan variables", () => {
    expect(networkVariables(auth)).toEqual([
      { name: "AUTH_TRUST_HOST", sensitivity: "plain", scopes: ["runtime"], value: "true" },
      {
        name: "NEXTAUTH_URL",
        sensitivity: "plain",
        scopes: ["runtime"],
        value: "",
        domainTemplate: "{{scheme}}://{{hostname}}",
      },
    ])
    expect(defaultConfiguration("web", auth).variables.map((variable) => variable.name)).toEqual([
      "AUTH_TRUST_HOST",
      "NEXTAUTH_URL",
    ])
    expect(networkVariables(candidate({}))).toEqual([])
  })

  test("a public URL follows the primary domain", () => {
    const plan = synchronizePrimaryDomain(defaultConfiguration("web", auth), [
      { hostname: "app.example.com", https: true, ownership: "managed" },
    ])
    expect(plan.variables.find((variable) => variable.name === "NEXTAUTH_URL")?.value).toBe(
      "https://app.example.com",
    )
  })

  test("are not asked for again as environment rows", () => {
    expect(discoveredEnvironmentRows(auth).map((row) => row.name)).toEqual(["AUTH_SECRET"])
  })
})

describe("request body limit", () => {
  test("zero keeps the default and the ceiling bounds a typo", () => {
    const plan = defaultConfiguration("web", candidate({}))
    expect(plan.runtime.maxRequestBodyMb).toBeUndefined()
    for (const [limit, refused] of [
      [undefined, false],
      [512, false],
      [10240, false],
      [10241, true],
      [-1, true],
    ]) {
      const errors = validateConfiguration(
        { ...plan, runtime: { ...plan.runtime, maxRequestBodyMb: limit } },
        "web",
      )
      expect(Boolean(errors.maxRequestBodyMb)).toBe(refused)
    }
  })
})
