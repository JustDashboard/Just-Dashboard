import { relativeTime } from "@/lib/format"
import type { SecretStore, SecretStoreKind, SecretStoreSettings } from "@/lib/types"

/**
 * A store reference's parts: the store's name, a path in it, the key at that
 * path and, optionally, the version of that key. Its text is the target of
 * `${{store.…}}` — `name[/path]#KEY[@version]` — read by the same grammar the
 * server's `secretstore.ParseReference` holds it to.
 */
export type StoreReference = { store: string; path: string; key: string; version?: number }

const STORE_NAME = /^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$/
const SEGMENT = /^[A-Za-z0-9._-]{1,128}$/
const KEY = /^[A-Za-z0-9._-]{1,128}$/

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

/** The target `name[/path]#KEY[@version]` of a reference. */
export function storeTarget(reference: StoreReference) {
  const path = reference.path.replace(/^\/+|\/+$/g, "")
  return `${reference.store}${path ? `/${path}` : ""}#${reference.key}${reference.version ? `@${reference.version}` : ""}`
}

/** The literal a variable holds to read a store's key. */
export function storeReference(reference: StoreReference) {
  return `\${{store.${storeTarget(reference)}}}`
}

/** Reads a target back into its parts, or nothing when it is not one. */
export function parseStoreTarget(target: string): StoreReference | undefined {
  if (target.length > 300) return undefined
  const hash = target.indexOf("#")
  if (hash < 0 || target.indexOf("#", hash + 1) >= 0) return undefined
  const location = target.slice(0, hash)
  let key = target.slice(hash + 1)
  let version: number | undefined
  const at = key.indexOf("@")
  if (at >= 0) {
    const pinned = key.slice(at + 1)
    if (!/^[1-9][0-9]{0,9}$/.test(pinned)) return undefined
    version = Number(pinned)
    key = key.slice(0, at)
  }
  if (!validStoreKey(key)) return undefined
  const slash = location.indexOf("/")
  const store = slash < 0 ? location : location.slice(0, slash)
  const path = slash < 0 ? "" : location.slice(slash + 1)
  if (!validStoreName(store) || !validStorePath(path)) return undefined
  return version ? { store, path, key, version } : { store, path, key }
}

/** Reads `${{store.…}}` into its parts, or nothing when it is not one. */
export function parseStoreReference(value: string): StoreReference | undefined {
  const match = /^\$\{\{store\.([^{}\s]{1,300})\}\}$/.exec(value.trim())
  return match ? parseStoreTarget(match[1]) : undefined
}

/** Where a reference reads inside its store, as the run's transcript says it. */
export function storeLocation(reference: StoreReference) {
  const path = reference.path ? `${reference.path}#` : ""
  return `${path}${reference.key}${reference.version ? `@${reference.version}` : ""}`
}

export type StoreKindInfo = {
  kind: SecretStoreKind
  title: string
  /** One line under the title in the kind chooser. */
  short: string
  hint: string
  placeholder: string
  /** The address used when the field is left empty. */
  defaultAddress?: string
  /** What a reference's path means in this store. */
  pathHint: string
  /** Whether the store keeps versions a reference may pin. */
  versions: boolean
}

export const STORE_KINDS: StoreKindInfo[] = [
  {
    kind: "vault",
    title: "HashiCorp Vault",
    short: "KV version 2, token or AppRole",
    hint: "Reads KV version 2 secrets, signed in with a token or an AppRole, in a namespace if you use them.",
    placeholder: "https://vault.example.com:8200",
    pathHint: "The secret's path under the KV mount, such as apps/web.",
    versions: true,
  },
  {
    kind: "openbao",
    title: "OpenBao",
    short: "KV version 2, token or AppRole",
    hint: "OpenBao speaks Vault's API: KV version 2 secrets, signed in with a token or an AppRole.",
    placeholder: "https://bao.example.com:8200",
    pathHint: "The secret's path under the KV mount, such as apps/web.",
    versions: true,
  },
  {
    kind: "infisical",
    title: "Infisical",
    short: "Cloud or self-hosted, machine identity",
    hint: "Infisical Cloud or your own instance, signed in with a Universal Auth machine identity that can read the project.",
    placeholder: "https://app.infisical.com",
    defaultAddress: "https://app.infisical.com",
    pathHint: "The folder the secret is in, such as backend; empty for the top level.",
    versions: true,
  },
  {
    kind: "doppler",
    title: "Doppler",
    short: "Service token for one config",
    hint: "A service token reads one project's config, so a reference names only the secret.",
    placeholder: "https://api.doppler.com",
    defaultAddress: "https://api.doppler.com",
    pathHint: "Doppler has no paths: a service token reads one config.",
    versions: false,
  },
]

export function storeKind(kind: SecretStoreKind): StoreKindInfo {
  return STORE_KINDS.find((info) => info.kind === kind) ?? STORE_KINDS[0]
}

/** The credentials each way of signing in takes, in the order the form asks. */
export function credentialFields(
  kind: SecretStoreKind,
  auth: SecretStoreSettings["auth"],
): { name: string; label: string; secret: boolean; placeholder: string }[] {
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
  if (auth === "approle") {
    return [
      { name: "roleId", label: "Role ID", secret: false, placeholder: "" },
      { name: "secretId", label: "Secret ID", secret: true, placeholder: "" },
    ]
  }
  return [{ name: "token", label: "Token", secret: true, placeholder: "hvs.… or s.…" }]
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
