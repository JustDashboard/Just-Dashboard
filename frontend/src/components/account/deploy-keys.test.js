import { expect, test } from "bun:test"
import { curlExample, scopeWords } from "./api-keys"
import {
  DEPLOY_KEY_CAN,
  DEPLOY_KEY_CANNOT,
  deployKeyBody,
  deployKeyReady,
  deployReach,
  jdLogin,
  OPENAPI_DESCRIPTION,
} from "./deploy-keys"
import { matchesKey } from "./key-list"

const token = (over = {}) => ({
  id: 9,
  userId: 1,
  name: "jd-laptop",
  prefix: "vpsd_9f9f9f9f",
  role: "admin",
  scope: "deploy",
  createdAt: "2026-10-27T10:00:00Z",
  revoked: false,
  ...over,
})

test("a deploy key is used through jd login, with this dashboard's own address", () => {
  expect(jdLogin("https://dash.example.com", "vpsd_abc")).toBe(
    "jd login https://dash.example.com --key vpsd_abc",
  )
  expect(curlExample("https://dash.example.com", "vpsd_abc", "deploy")).toBe(
    "jd login https://dash.example.com --key vpsd_abc",
  )
  // The other kinds keep their own example.
  expect(curlExample("https://d", "vpsd_x")).toContain("/api/v1/system/host")
  expect(curlExample("https://d", "vpsd_x", "metrics.read")).toContain("/api/v1/metrics/databases")
})

test("the OpenAPI description the dialog links is the route jd openapi prints", () => {
  expect(OPENAPI_DESCRIPTION).toBe("/api/v1/deploy/openapi.json")
})

test("a deploy key's row names its job and which deployments it reaches", () => {
  expect(scopeWords(token())).toBe("deploy only")
  expect(scopeWords(token({ scope: "" }))).toBeUndefined()
  expect(deployReach(token())).toBe("every deployment")
  expect(deployReach(token({ deploymentIds: [] }))).toBe("every deployment")
  expect(deployReach(token({ deploymentIds: [4] }))).toBe("1 deployment")
  expect(deployReach(token({ deploymentIds: [4, 2, 9] }))).toBe("3 deployments")
})

test("the body sent to make a deploy key: trimmed, sorted, once each, empty for every deployment", () => {
  expect(deployKeyBody("  ci-shop ", "30", [7, 2, 7])).toEqual({
    name: "ci-shop",
    ttlDays: 30,
    deploymentIds: [2, 7],
  })
  expect(deployKeyBody("ci", "0", [])).toEqual({ name: "ci", ttlDays: 0, deploymentIds: [] })
})

test("a key limited to some deployments is never sent with none ticked", () => {
  expect(deployKeyReady("ci", true, new Set())).toBe(true)
  expect(deployKeyReady("ci", false, new Set())).toBe(false)
  expect(deployKeyReady("ci", false, new Set([3]))).toBe(true)
  expect(deployKeyReady("   ", true, new Set())).toBe(false)
})

test("what a deploy key cannot do names the secrets it never reads", () => {
  const words = DEPLOY_KEY_CANNOT.join(" ")
  expect(words).toContain("never a value")
  expect(words).toContain("credentials")
  expect(words).toContain("secret-store")
  expect(words).toContain("pull requests")
  expect(words).toContain("rehearsal terms")
})

test("what a deploy key can do names the verbs beside a deploy and a narrowed key's drafts", () => {
  const words = DEPLOY_KEY_CAN.join(" ")
  for (const verb of ["promote", "warm release", "maintenance", "drafts", "narrowed"]) {
    expect(words).toContain(verb)
  }
  expect(words).not.toContain("every deployment")
})

test("search finds a key by its job as well as its role", () => {
  expect(matchesKey(token(), "deploy")).toBe(true)
  expect(matchesKey(token(), "admin")).toBe(true)
  expect(matchesKey(token({ scope: "" }), "deploy")).toBe(false)
})
