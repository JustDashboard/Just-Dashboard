import { describe, expect, test } from "bun:test"
import {
  credentialFields,
  parseStoreReference,
  parseStoreTarget,
  storeAddressProblem,
  storeHealth,
  storeLocation,
  storeReference,
  storeSettingsProblem,
  validRegion,
  validVaultName,
} from "./secret-stores"

describe("store references", () => {
  test("build and read back the literal a variable holds", () => {
    const reference = { store: "vault-prod", path: "apps/web", key: "DATABASE_URL" }
    expect(storeReference(reference)).toBe("${{store.vault-prod/apps/web#DATABASE_URL}}")
    expect(parseStoreReference("${{store.vault-prod/apps/web#DATABASE_URL}}")).toEqual(reference)
    expect(storeReference({ store: "infisical", path: "", key: "API_KEY", version: "4" })).toBe(
      "${{store.infisical#API_KEY@4}}",
    )
    expect(parseStoreTarget("infisical#API_KEY@4")).toEqual({
      store: "infisical",
      path: "",
      key: "API_KEY",
      version: "4",
    })
    expect(storeReference({ store: "bao", path: "/apps/web/", key: "K" })).toBe(
      "${{store.bao/apps/web#K}}",
    )
  })
  test("read a whole secret, a field and a store's own version", () => {
    // Secrets Manager and Key Vault keep whole secrets: no key reads the
    // whole value, and a version is theirs — a staging label, a version id.
    expect(storeReference({ store: "kv", path: "database-url", key: "" })).toBe(
      "${{store.kv/database-url}}",
    )
    expect(parseStoreTarget("kv/database-url@0123456789abcdef0123456789abcdef")).toEqual({
      store: "kv",
      path: "database-url",
      key: "",
      version: "0123456789abcdef0123456789abcdef",
    })
    expect(parseStoreTarget("aws/prod/db#DATABASE_URL@AWSPREVIOUS")).toEqual({
      store: "aws",
      path: "prod/db",
      key: "DATABASE_URL",
      version: "AWSPREVIOUS",
    })
    expect(parseStoreTarget("aws/prod/db")).toEqual({ store: "aws", path: "prod/db", key: "" })
  })
  test("refuse what the server refuses", () => {
    for (const target of [
      "vault",
      "vault@3",
      "Vault/apps#K",
      "vault/../etc#K",
      "vault//apps#K",
      "vault/apps#",
      "vault/apps#K@",
      "vault/apps@",
      "vault/apps#K@a b",
      "vault/apps#K@a@b",
      "vault/apps#K#L",
      "vault/apps#K Y",
    ]) {
      expect(parseStoreTarget(target)).toBeUndefined()
    }
    expect(parseStoreReference("${{server.API_URL}}")).toBeUndefined()
  })
  test("say where a reference reads, without the store", () => {
    expect(storeLocation({ store: "v", path: "apps/web", key: "K", version: "2" })).toBe(
      "apps/web#K@2",
    )
    expect(storeLocation({ store: "d", path: "", key: "K" })).toBe("K")
    expect(storeLocation({ store: "aws", path: "prod/db", key: "", version: "AWSPREVIOUS" })).toBe(
      "prod/db@AWSPREVIOUS",
    )
    expect(storeLocation({ store: "kv", path: "database-url", key: "" })).toBe("database-url")
  })
})

describe("the hosted kinds' settings", () => {
  test("hold a region and a vault name to the shapes the server does", () => {
    for (const region of [
      "us-east-1",
      "EU-West-2",
      "us-gov-west-1",
      "cn-northwest-1",
      "ap-southeast-3",
    ]) {
      expect(validRegion(region)).toBe(true)
    }
    for (const region of [
      "",
      "us_east_1",
      "us-east",
      "useast1",
      "us-east-1.amazonaws.com",
      "us east 1",
    ]) {
      expect(validRegion(region)).toBe(false)
    }
    for (const name of ["my-vault", "Shop-Prod", "kv1"]) {
      expect(validVaultName(name)).toBe(true)
    }
    for (const name of ["", "kv", "1vault", "my.vault", "my--vault", "-vault", "a".repeat(25)]) {
      expect(validVaultName(name)).toBe(false)
    }
    expect(storeSettingsProblem("aws", {})).toContain("region")
    expect(storeSettingsProblem("aws", { region: "eu-west-1" })).toBeUndefined()
    expect(storeSettingsProblem("azure", { vaultName: "my-vault" })).toContain("tenant")
    expect(storeSettingsProblem("azure", { vaultName: "my-vault", tenantId: "t" })).toBeUndefined()
    expect(storeSettingsProblem("infisical", { project: "p" })).toContain("environment")
    expect(storeSettingsProblem("doppler", {})).toBeUndefined()
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
    const aws = credentialFields("aws", undefined)
    expect(aws.map((field) => field.name)).toEqual([
      "accessKeyId",
      "secretAccessKey",
      "sessionToken",
    ])
    // A session token belongs only to temporary credentials.
    expect(aws.map((field) => Boolean(field.optional))).toEqual([false, false, true])
    expect(credentialFields("azure", undefined).map((field) => field.name)).toEqual([
      "clientId",
      "clientSecret",
    ])
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
