import { describe, expect, test } from "bun:test"
import {
  defaultConfiguration,
  discoveredEnvironmentRows,
  networkVariables,
  validateConfiguration,
} from "./deployment-defaults"
import { synchronizePrimaryDomain } from "./new-project/domain-bindings"
import { configurationForSave, withSuggestedHostname } from "./new-project/draft"

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

  test("a suggested hostname fills the public URL like a typed one", () => {
    const plan = withSuggestedHostname(
      defaultConfiguration("web", auth),
      "web",
      { kind: "git" },
      {
        hostname: "App.example.com",
        method: "sslip",
      },
    )
    expect(plan.variables.find((variable) => variable.name === "NEXTAUTH_URL")?.value).toBe(
      "https://app.example.com",
    )
  })

  // next-auth 4 reads `NEXTAUTH_URL=` as a URL and fails every sign-in request.
  test("an empty public URL is not saved", () => {
    const git = { kind: "git" }
    const saved = configurationForSave(defaultConfiguration("web", auth), git)
    expect(saved.variables.map((variable) => variable.name)).toEqual(["AUTH_TRUST_HOST"])
    const routed = configurationForSave(
      synchronizePrimaryDomain(defaultConfiguration("web", auth), [
        { hostname: "app.example.com", https: true, ownership: "managed" },
      ]),
      git,
    )
    expect(routed.variables.map((variable) => variable.name)).toEqual([
      "AUTH_TRUST_HOST",
      "NEXTAUTH_URL",
    ])
    // A blueprint's plan is its reviewed definition's, empty values included.
    const blueprint = {
      ...defaultConfiguration("web"),
      variables: [
        {
          name: "WEBHOOK_URL",
          sensitivity: "plain",
          scopes: ["runtime"],
          domainTemplate: "https://{{hostname}}",
        },
      ],
    }
    expect(
      configurationForSave(blueprint, { kind: "blueprint", mode: "blueprint" }).variables,
    ).toEqual(blueprint.variables)
  })

  test("only a trust switch stops being asked for as an environment row", () => {
    const detected = candidate({
      networkVariables: auth.networkVariables,
      variables: [...auth.variables, { name: "NEXTAUTH_URL", sources: [".env.example"] }],
    })
    expect(discoveredEnvironmentRows(detected).map((row) => row.name)).toEqual([
      "AUTH_SECRET",
      "NEXTAUTH_URL",
    ])
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
