import { plural } from "@/lib/format"
import type { DeploymentEnvironmentConfiguration, DeploymentRouteTailnet } from "@/lib/types"

/**
 * A tailnet-only environment's allow-list, read the way the server reads it
 * (`internal/tailnetgate/allow.go`): Tailscale logins, compared without case
 * and kept in lower case, and ACL tags, compared exactly. The part of a
 * login after the @ need not be a domain with a dot — GitHub accounts are
 * alice@github — so a login is anything shaped user@provider.
 */

export const TAILNET_MAX_ALLOW = 64

const LOGIN = /^[A-Za-z0-9._%+'=-]{1,128}@[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$/
const TAG = /^tag:[A-Za-z][A-Za-z0-9-]{0,62}$/

export type TailnetEntry = { entry: string; tag: boolean } | { error: string }

/** One typed entry, as the list would keep it, or why the server would refuse it. */
export function readTailnetEntry(raw: string, existing: readonly string[] = []): TailnetEntry {
  const value = raw.trim()
  if (!value) return { error: "Type a Tailscale login or a tag" }
  if (existing.length >= TAILNET_MAX_ALLOW)
    return { error: `At most ${TAILNET_MAX_ALLOW} logins and tags` }
  let entry: string
  let tag = false
  if (value.toLowerCase().startsWith("tag:")) {
    if (!TAG.test(value))
      return {
        error: "A tag is tag: followed by a letter, then letters, digits and dashes",
      }
    entry = value
    tag = true
  } else if (LOGIN.test(value)) {
    entry = value.toLowerCase()
  } else {
    return {
      error: `${value} is neither a Tailscale login (user@example.com) nor a tag (tag:name)`,
    }
  }
  if (existing.includes(entry)) return { error: `${entry} is already on the list` }
  return { entry, tag }
}

/** Whether an entry of the list is a tag rather than a login. */
export function isTailnetTag(entry: string) {
  return entry.startsWith("tag:")
}

/** Who opens the environment, as the Tailnet section's state. */
export function tailnetAccessState(tailnet: DeploymentRouteTailnet | undefined): string {
  if (!tailnet) return "Off · answers on its public domains"
  const allow = tailnet.allow ?? []
  if (allow.length === 0) return "Everyone on the tailnet"
  if (allow.length === 1) return `Only ${allow[0]}`
  return `Only ${plural(allow.length, "login or tag", "logins and tags")}`
}

type Configuration = Pick<DeploymentEnvironmentConfiguration, "domains" | "runtime">

/**
 * What turning tailnet-only on takes away when it is saved: everything that
 * only a public route has. The server refuses a tailnet-only plan that keeps
 * any of it, so the switch clears it rather than leaving the operator to
 * find each one.
 */
export function tailnetSwitchClears(configuration: Configuration): string[] {
  const { domains, runtime } = configuration
  const out: string[] = []
  const names = domains.map((domain) => domain.hostname)
  if (names.length === 1) out.push(`the domain ${names[0]}`)
  else if (names.length > 1) out.push(`the domains ${names.join(", ")}`)
  if (runtime.forwardAuth) out.push("the sign-in")
  if (runtime.allowFrom?.length) out.push("the allowed addresses")
  if (runtime.responseHeaders?.length) out.push("the response headers")
  if (runtime.hsts) out.push("HSTS")
  if (runtime.maintenance) out.push("the maintenance settings")
  if (runtime.errorPages?.length || runtime.interceptErrors) out.push("the error pages")
  if (runtime.bindAddress === "0.0.0.0" || runtime.bindAddress === "::")
    out.push(`the bind address ${runtime.bindAddress}, so the container is published on loopback`)
  return out
}

/**
 * What a tailnet-only environment cannot have and the switch does not take
 * away, because each was chosen on purpose under Runtime: they publish the
 * container past the tailnet.
 */
export function tailnetBlockers(runtime: Configuration["runtime"]): string[] {
  const out: string[] = []
  if (runtime.hostNetwork) out.push("it runs on the host's network")
  if (runtime.hostPort) out.push(`it publishes host port ${runtime.hostPort}`)
  if (runtime.ports?.length)
    out.push(`it publishes ${plural(runtime.ports.length, "extra port", "extra ports")}`)
  return out
}

/**
 * The runtime the switch saves: tailnet-only with the list, without what
 * only a public route carries, published on loopback — or, switched off,
 * the plan without the publication.
 */
export function tailnetRuntime(
  runtime: Configuration["runtime"],
  allow: string[] | undefined,
): Configuration["runtime"] {
  if (allow === undefined) return { ...runtime, tailnet: undefined }
  const bindAddress =
    runtime.bindAddress === "0.0.0.0" || runtime.bindAddress === "::"
      ? "127.0.0.1"
      : runtime.bindAddress
  return {
    ...runtime,
    bindAddress,
    tailnet: allow.length > 0 ? { allow } : {},
    forwardAuth: undefined,
    allowFrom: undefined,
    responseHeaders: undefined,
    hsts: undefined,
    maintenance: undefined,
    errorPages: undefined,
    interceptErrors: undefined,
  }
}
