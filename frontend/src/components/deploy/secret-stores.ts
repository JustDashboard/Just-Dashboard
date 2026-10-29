import { relativeTime } from "@/lib/format"
import type { SecretStore, SecretStoreKind, SecretStoreSettings } from "@/lib/types"

/**
 * A store reference's parts: the store's name, a path in it, the key at that
 * path and, optionally, the version of that key. Its text is the target of
 * `${{store.…}}` — `name[/path][#KEY][@version]` — read by the same grammar
 * the server's `secretstore.ParseReference` holds it to. What each part means
 * depends on the store: Vault, OpenBao, Infisical and Doppler keep keys, so a
 * key is required and a version is a number; AWS Secrets Manager and Azure Key
 * Vault keep whole secrets, so the path is the secret's name, the key names a
 * field of a JSON value or is left out for the whole value, and the version is
 * the store's own id or staging label.
 */
export type StoreReference = { store: string; path: string; key: string; version?: string }

const STORE_NAME = /^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$/
const SEGMENT = /^[A-Za-z0-9._-]{1,128}$/
const KEY = /^[A-Za-z0-9._-]{1,128}$/
const VERSION = /^[A-Za-z0-9._-]{1,64}$/
const REGION = /^[a-z]{2}(?:-[a-z]{2,16}){1,2}-[0-9]{1,2}$/
const VAULT_NAME = /^[A-Za-z][A-Za-z0-9-]{1,22}[A-Za-z0-9]$/

/** Whether a name is one a store may be saved under, as a reference says it. */
export function validStoreName(name: string) {
  return STORE_NAME.test(name)
}

/** Whether a path is one a reference may name: segments that never climb. */
export function validStorePath(path: string) {
  if (path === "") return true
  const segments = path.split("/")
  return (
    segments.length <= 16 &&
    segments.every((segment) => segment !== "." && segment !== ".." && SEGMENT.test(segment))
  )
}

export function validStoreKey(key: string) {
  return KEY.test(key)
}

/** An AWS region as the server admits it before it becomes a hostname. */
export function validRegion(region: string) {
  return REGION.test(region.trim().toLowerCase())
}

/** A Key Vault name as the server admits it before it becomes a hostname. */
export function validVaultName(name: string) {
  const trimmed = name.trim()
  return VAULT_NAME.test(trimmed) && !trimmed.includes("--")
}

/** The target `name[/path][#KEY][@version]` of a reference. */
export function storeTarget(reference: StoreReference) {
  const path = reference.path.replace(/^\/+|\/+$/g, "")
  return `${reference.store}${path ? `/${path}` : ""}${reference.key ? `#${reference.key}` : ""}${reference.version ? `@${reference.version}` : ""}`
}

/** The literal a variable holds to read a store's key. */
export function storeReference(reference: StoreReference) {
  return `\${{store.${storeTarget(reference)}}}`
}

/** Reads a target back into its parts, or nothing when it is not one. */
export function parseStoreTarget(target: string): StoreReference | undefined {
  if (target.length > 300) return undefined
  const hash = target.indexOf("#")
  if (hash >= 0 && target.indexOf("#", hash + 1) >= 0) return undefined
  let location = hash < 0 ? target : target.slice(0, hash)
  let key = hash < 0 ? "" : target.slice(hash + 1)
  let version: string | undefined
  // The version follows the key, or the path when there is no key.
  const holder = hash < 0 ? location : key
  const at = holder.indexOf("@")
  if (at >= 0) {
    version = holder.slice(at + 1)
    if (!VERSION.test(version)) return undefined
    if (hash < 0) location = holder.slice(0, at)
    else key = holder.slice(0, at)
  }
  if (hash >= 0 && !validStoreKey(key)) return undefined
  const slash = location.indexOf("/")
  const store = slash < 0 ? location : location.slice(0, slash)
  const path = slash < 0 ? "" : location.slice(slash + 1)
  if (!validStoreName(store) || !validStorePath(path)) return undefined
  if (!path && !key) return undefined
  return version ? { store, path, key, version } : { store, path, key }
}

/** Reads `${{store.…}}` into its parts, or nothing when it is not one. */
export function parseStoreReference(value: string): StoreReference | undefined {
  const match = /^\$\{\{store\.([^{}\s]{1,300})\}\}$/.exec(value.trim())
  return match ? parseStoreTarget(match[1]) : undefined
}

/** Where a reference reads inside its store, as the run's transcript says it. */
export function storeLocation(reference: StoreReference) {
  const key = reference.key ? `${reference.path ? "#" : ""}${reference.key}` : ""
  return `${reference.path}${key}${reference.version ? `@${reference.version}` : ""}`
}

export type StoreKindInfo = {
  kind: SecretStoreKind
  title: string
  /** One line under the title in the kind chooser. */
  short: string
  hint: string
  /** The name the form suggests. */
  namePlaceholder: string
  placeholder: string
  /** The address used when the field is left empty. */
  defaultAddress?: string
  /**
   * The address is derived from the settings (a region, a vault name) and
   * the field only overrides it.
   */
  addressOptional?: boolean
  addressHint?: string
  /** What a reference's path is called and means in this store. */
  pathLabel: string
  pathHint: string
  pathPlaceholder: string
  /** The reference shown beside the name while it is typed. */
  sample: { path: string; key: string }
  /** The key may be left out: the whole secret is then read. */
  keyOptional?: boolean
  keyHint?: string
  /** Whether the store keeps versions a reference may pin, and in what form. */
  versions: boolean
  versionNumeric?: boolean
  versionHint?: string
  versionPlaceholder?: string
}

export const STORE_KINDS: StoreKindInfo[] = [
  {
    kind: "vault",
    title: "HashiCorp Vault",
    short: "KV version 2, token or AppRole",
    hint: "Reads KV version 2 secrets, signed in with a token or an AppRole, in a namespace if you use them.",
    namePlaceholder: "vault-prod",
    placeholder: "https://vault.example.com:8200",
    pathLabel: "Path",
    pathHint: "The secret's path under the KV mount, such as apps/web.",
    pathPlaceholder: "apps/web",
    sample: { path: "path", key: "KEY" },
    versions: true,
    versionNumeric: true,
    versionHint: "Empty reads the newest at every run, rollbacks included.",
    versionPlaceholder: "newest",
  },
  {
    kind: "openbao",
    title: "OpenBao",
    short: "KV version 2, token or AppRole",
    hint: "OpenBao speaks Vault's API: KV version 2 secrets, signed in with a token or an AppRole.",
    namePlaceholder: "openbao-prod",
    placeholder: "https://bao.example.com:8200",
    pathLabel: "Path",
    pathHint: "The secret's path under the KV mount, such as apps/web.",
    pathPlaceholder: "apps/web",
    sample: { path: "path", key: "KEY" },
    versions: true,
    versionNumeric: true,
    versionHint: "Empty reads the newest at every run, rollbacks included.",
    versionPlaceholder: "newest",
  },
  {
    kind: "infisical",
    title: "Infisical",
    short: "Cloud or self-hosted, machine identity",
    hint: "Infisical Cloud or your own instance, signed in with a Universal Auth machine identity that can read the project.",
    namePlaceholder: "infisical-prod",
    placeholder: "https://app.infisical.com",
    defaultAddress: "https://app.infisical.com",
    pathLabel: "Path",
    pathHint: "The folder the secret is in, such as backend; empty for the top level.",
    pathPlaceholder: "backend",
    sample: { path: "folder", key: "KEY" },
    versions: true,
    versionNumeric: true,
    versionHint: "Empty reads the newest at every run, rollbacks included.",
    versionPlaceholder: "newest",
  },
  {
    kind: "doppler",
    title: "Doppler",
    short: "Service token for one config",
    hint: "A service token reads one project's config, so a reference names only the secret.",
    namePlaceholder: "doppler",
    placeholder: "https://api.doppler.com",
    defaultAddress: "https://api.doppler.com",
    pathLabel: "Path",
    pathHint: "Doppler has no paths: a service token reads one config.",
    pathPlaceholder: "",
    sample: { path: "", key: "KEY" },
    versions: false,
  },
  {
    kind: "aws",
    title: "AWS Secrets Manager",
    short: "Access key pair, one region",
    hint: "Secrets Manager in one region, signed with an IAM access key pair. A reference names a secret and, for a JSON value, one of its fields.",
    namePlaceholder: "aws",
    placeholder: "https://secretsmanager.eu-west-1.amazonaws.com",
    addressOptional: true,
    addressHint: "Empty for the region's own endpoint. Set it for a VPC endpoint or LocalStack.",
    pathLabel: "Secret name",
    pathHint: "The secret's name, such as prod/db.",
    pathPlaceholder: "prod/db",
    sample: { path: "secret-name", key: "KEY" },
    keyOptional: true,
    keyHint: "A field of a JSON value; empty reads the whole secret.",
    versions: true,
    versionHint:
      "A version id or a staging label such as AWSPREVIOUS. Empty reads AWSCURRENT at every run.",
    versionPlaceholder: "AWSCURRENT",
  },
  {
    kind: "azure",
    title: "Azure Key Vault",
    short: "Entra application, client secret",
    hint: "One Key Vault, signed in to as an Entra application with the Get permission on secrets. A reference names a secret and, for a JSON value, one of its fields.",
    namePlaceholder: "keyvault",
    placeholder: "https://my-vault.vault.azure.net",
    addressOptional: true,
    addressHint: "Empty for NAME.vault.azure.net in the cloud chosen.",
    pathLabel: "Secret name",
    pathHint: "The secret's name: letters, digits and dashes.",
    pathPlaceholder: "database-url",
    sample: { path: "secret-name", key: "" },
    keyOptional: true,
    keyHint: "A field of a JSON value; empty reads the whole secret.",
    versions: true,
    versionHint:
      "A version's 32 characters, as the vault lists it. Empty reads the current at every run.",
    versionPlaceholder: "current",
  },
]

export function storeKind(kind: SecretStoreKind): StoreKindInfo {
  return STORE_KINDS.find((info) => info.kind === kind) ?? STORE_KINDS[0]
}

/** The Azure clouds a vault can be in, as the form offers them. */
export const AZURE_CLOUDS: { value: NonNullable<SecretStoreSettings["cloud"]>; label: string }[] = [
  { value: "public", label: "Public" },
  { value: "usgov", label: "US Government" },
  { value: "china", label: "China" },
]

/** The credentials each way of signing in takes, in the order the form asks. */
export function credentialFields(
  kind: SecretStoreKind,
  auth: SecretStoreSettings["auth"],
): { name: string; label: string; secret: boolean; placeholder: string; optional?: boolean }[] {
  if (kind === "infisical") {
    return [
      {
        name: "clientId",
        label: "Client ID",
        secret: false,
        placeholder: "Universal Auth client ID",
      },
      { name: "clientSecret", label: "Client secret", secret: true, placeholder: "" },
    ]
  }
  if (kind === "doppler") {
    return [{ name: "token", label: "Service token", secret: true, placeholder: "dp.st.prd.…" }]
  }
  if (kind === "aws") {
    return [
      { name: "accessKeyId", label: "Access key ID", secret: false, placeholder: "AKIA…" },
      { name: "secretAccessKey", label: "Secret access key", secret: true, placeholder: "" },
      {
        name: "sessionToken",
        label: "Session token",
        secret: true,
        placeholder: "Only for temporary credentials",
        optional: true,
      },
    ]
  }
  if (kind === "azure") {
    return [
      {
        name: "clientId",
        label: "Client ID",
        secret: false,
        placeholder: "Application (client) ID",
      },
      { name: "clientSecret", label: "Client secret", secret: true, placeholder: "" },
    ]
  }
  if (auth === "approle") {
    return [
      { name: "roleId", label: "Role ID", secret: false, placeholder: "" },
      { name: "secretId", label: "Secret ID", secret: true, placeholder: "" },
    ]
  }
  return [{ name: "token", label: "Token", secret: true, placeholder: "hvs.… or s.…" }]
}

/**
 * Whether a connection's settings are complete enough to save: the fields a
 * kind derives its endpoint or its sign-in from.
 */
export function storeSettingsProblem(
  kind: SecretStoreKind,
  settings: SecretStoreSettings,
): string | undefined {
  if (kind === "infisical" && (!settings.project?.trim() || !settings.environment?.trim())) {
    return "Infisical needs the project ID and the environment."
  }
  if (kind === "aws" && !validRegion(settings.region ?? "")) {
    return "The region is one such as us-east-1 or eu-west-2."
  }
  if (kind === "azure") {
    if (!validVaultName(settings.vaultName ?? "")) {
      return "The vault name is 3 to 24 letters, digits and dashes, starting with a letter."
    }
    if (!settings.tenantId?.trim()) return "The tenant ID is the directory's GUID."
  }
  return undefined
}

/**
 * Whether an address would carry the credential in the clear, by the
 * server's rule: https, or plain http only to this machine.
 */
export function storeAddressProblem(address: string): "credential-over-http" | undefined {
  let url: URL
  try {
    url = new URL(address.trim())
  } catch {
    return undefined
  }
  if (url.protocol !== "http:") return undefined
  const host = url.hostname.replace(/^\[|\]$/g, "")
  const loopback = host === "localhost" || host === "::1" || /^127\.\d+\.\d+\.\d+$/.test(host)
  return loopback ? undefined : "credential-over-http"
}

/**
 * How a store is doing, in the line under its name: the newest of the last
 * run's read and the last Test, a failure in the store's own words, else
 * nothing tried yet.
 */
export function storeHealth(store: Pick<SecretStore, "test" | "lastRead">): {
  tone: "warning" | "muted"
  line: string
} {
  const read = store.lastRead
  const test = store.test
  const latest =
    read && test
      ? Date.parse(read.at) >= Date.parse(test.at)
        ? { ...read, what: "read" as const }
        : { ...test, what: "test" as const }
      : read
        ? { ...read, what: "read" as const }
        : test
          ? { ...test, what: "test" as const }
          : undefined
  if (!latest) return { tone: "muted", line: "Not tested yet" }
  const when = relativeTime(latest.at)
  if (!latest.ok) {
    return {
      tone: "warning",
      line: `${latest.what === "read" ? "A run's read failed" : "Test failed"} ${when}: ${latest.message}`,
    }
  }
  return {
    tone: "muted",
    line: latest.what === "read" ? `Read by a run ${when}` : `Tested ${when}: ${latest.message}`,
  }
}
