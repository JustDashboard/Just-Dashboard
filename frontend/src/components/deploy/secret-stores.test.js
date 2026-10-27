import { describe, expect, test } from "bun:test"
import {
  credentialFields,
  parseStoreReference,
  parseStoreTarget,
  storeAddressProblem,
  storeHealth,
  storeLocation,
  storeReference,
} from "./secret-stores"

describe("store references", () => {
  test("build and read back the literal a variable holds", () => {
    const reference = { store: "vault-prod", path: "apps/web", key: "DATABASE_URL" }
    expect(storeReference(reference)).toBe("${{store.vault-prod/apps/web#DATABASE_URL}}")
    expect(parseStoreReference("${{store.vault-prod/apps/web#DATABASE_URL}}")).toEqual(reference)
    expect(storeReference({ store: "infisical", path: "", key: "API_KEY", version: 4 })).toBe(
      "${{store.infisical#API_KEY@4}}",
    )
    expect(parseStoreTarget("infisical#API_KEY@4")).toEqual({
      store: "infisical",
      path: "",
      key: "API_KEY",
      version: 4,
    })
    expect(storeReference({ store: "bao", path: "/apps/web/", key: "K" })).toBe(
      "${{store.bao/apps/web#K}}",
    )
  })
  test("refuse what the server refuses", () => {
    for (const target of [
      "vault/apps",
      "Vault/apps#K",
      "vault/../etc#K",
      "vault//apps#K",
      "vault/apps#K@0",
      "vault/apps#K@01",
      "vault/apps#K#L",
      "vault/apps#K Y",
    ]) {
      expect(parseStoreTarget(target)).toBeUndefined()
    }
    expect(parseStoreReference("${{server.API_URL}}")).toBeUndefined()
  })
  test("say where a reference reads, without the store", () => {
    expect(storeLocation({ store: "v", path: "apps/web", key: "K", version: 2 })).toBe(
      "apps/web#K@2",
    )
    expect(storeLocation({ store: "d", path: "", key: "K" })).toBe("K")
  })
})

describe("storeAddressProblem", () => {
  test("a credential never goes over plain http to another machine", () => {
    expect(storeAddressProblem("http://vault.lan:8200")).toBe("credential-over-http")
    expect(storeAddressProblem("http://127.0.0.1:8200")).toBeUndefined()
    expect(storeAddressProblem("http://localhost:8200")).toBeUndefined()
    expect(storeAddressProblem("http://[::1]:8200")).toBeUndefined()
    expect(storeAddressProblem("https://vault.example.com")).toBeUndefined()
    expect(storeAddressProblem("http://127.example.com")).toBe("credential-over-http")
  })
})

describe("credentialFields", () => {
  test("ask for what each way of signing in takes", () => {
    expect(credentialFields("vault", "token").map((field) => field.name)).toEqual(["token"])
    expect(credentialFields("openbao", "approle").map((field) => field.name)).toEqual([
      "roleId",
      "secretId",
    ])
    expect(credentialFields("infisical", undefined).map((field) => field.name)).toEqual([
      "clientId",
      "clientSecret",
    ])
    expect(credentialFields("doppler", undefined).map((field) => field.name)).toEqual(["token"])
  })
})

describe("storeHealth", () => {
  const now = Date.now()
  const at = (minutes) => new Date(now - minutes * 60_000).toISOString()
  test("the newest attempt speaks, a failure in the store's words", () => {
    const failed = storeHealth({
      test: { at: at(10), ok: true, message: "Signed in" },
      lastRead: { at: at(1), ok: false, message: "permission denied (403)" },
    })
    expect(failed.tone).toBe("warning")
    expect(failed.line).toContain("A run's read failed")
    expect(failed.line).toContain("permission denied (403)")
    const tested = storeHealth({ test: { at: at(1), ok: true, message: "Signed in as approle" } })
    expect(tested.tone).toBe("muted")
    expect(tested.line).toContain("Signed in as approle")
    expect(storeHealth({}).line).toBe("Not tested yet")
  })
})
