import { ApiError, errorMessage } from "@/lib/api"
import { calendarDate } from "@/lib/format"
import type { DotTone } from "@/components/tone"
import type { AppDomainStatus, DeploymentDomainRoute } from "@/lib/types"

/**
 * The pure half of app domains: `<app>.<handle>.<zone>` names for deployed apps. The handle
 * rules are the site's (the app-domains protocol, section 3), checked here only so a person
 * sees them met while typing; the site decides. The refusals are its section 5 and the
 * dashboard's own (`domain_in_use`, `session_required`, `not_linked`), each said as what
 * happened and what to do.
 */

/** The zone in words, for a sentence written before the site has named it. */
export function zoneWords(status: { zone?: string | null } | undefined): string {
  return status?.zone ?? "just-dashboard.com"
}

export const HANDLE_MIN = 3
export const HANDLE_MAX = 20

/** What a person typed, as the site will read it: trimmed and lowercased. */
export function normalizeHandle(value: string): string {
  return value.trim().toLowerCase()
}

export type HandleCheck = { key: string; label: string; met: boolean }

/** The four rules a handle meets, each lit as it is met. */
export function handleChecks(value: string): HandleCheck[] {
  const handle = normalizeHandle(value)
  const some = handle.length > 0
  return [
    {
      key: "length",
      label: `${HANDLE_MIN} to ${HANDLE_MAX} characters`,
      met: handle.length >= HANDLE_MIN && handle.length <= HANDLE_MAX,
    },
    { key: "characters", label: "a-z, 0-9 and -", met: some && /^[a-z0-9-]+$/.test(handle) },
    {
      key: "ends",
      label: "Starts and ends with a letter or digit",
      met: some && /^[a-z0-9]/.test(handle) && /[a-z0-9]$/.test(handle),
    },
    { key: "double", label: "No --", met: some && !handle.includes("--") },
  ]
}

export function handleValid(value: string): boolean {
  return handleChecks(value).every((check) => check.met)
}

/**
 * Whether a name is under the site's app-domain zone, and whether this server still holds the
 * handle it is under. **The one place that decides it**: the backend marks a deployment's
 * names (`DeploymentDomainRoute.appDomain`), and where it has not — a route not observed, or an
 * older backend — the name is read against the server's own app-domain state.
 */
export function appDomainStateOf(
  hostname: string,
  route: DeploymentDomainRoute | undefined,
  status: AppDomainStatus | undefined,
): "active" | "detached" | undefined {
  if (route?.appDomain) return route.appDomain.status
  const zone = status?.zone?.toLowerCase()
  const name = hostname.toLowerCase()
  if (!zone || !name.endsWith(`.${zone}`)) return undefined
  // Only `<app>.<handle>.<zone>` is an app name; the server name and the zone's own names are not.
  const labels = name.slice(0, -zone.length - 1).split(".")
  if (labels.length !== 2) return undefined
  const domain = status?.domain
  return domain?.status === "active" && labels[1] === domain.handle ? "active" : "detached"
}

const seconds = (value: number | null | undefined) =>
  value ? new Date(value * 1000).toISOString() : undefined

export type CertificateReading = {
  tone: DotTone
  label: string
  /** The one thing to do to it: issue one, or renew the one there is. */
  action?: "Issue" | "Renew"
  issuing: boolean
}

/** The wildcard's state as a dot and a word, and the verb beside it. */
export function certificateReading(
  certificate: AppDomainStatus["certificate"],
): CertificateReading {
  const until = calendarDate(seconds(certificate?.expiresAt))
  switch (certificate?.state) {
    case "issuing":
      return { tone: "notice", label: "Issuing", issuing: true }
    case "valid":
      return {
        tone: "running",
        label: certificate.expiresAt ? `Valid until ${until}` : "Valid",
        action: "Renew",
        issuing: false,
      }
    case "expiring":
      return {
        tone: "warning",
        label: certificate.expiresAt ? `Expires ${until}` : "Expiring",
        action: "Renew",
        issuing: false,
      }
    case "failed":
      return { tone: "danger", label: "Issuing failed", action: "Issue", issuing: false }
    default:
      return { tone: "warning", label: "No certificate yet", action: "Issue", issuing: false }
  }
}

/** The first IPv4 address a sentence names: the one the site says it saw. */
export function addressIn(text: string): string | undefined {
  return text.match(/\b(\d{1,3}(?:\.\d{1,3}){3})\b/)?.[1]
}

function minutes(ms: number) {
  const count = Math.max(1, Math.ceil(ms / 60_000))
  return count === 1 ? "a minute" : `${count} minutes`
}

/**
 * A refusal as what happened and what to do. `domain_in_use` is followed on the page by the
 * deployments themselves, with links, from the state read again after the refusal.
 */
export function refusalWords(
  error: unknown,
  status: Pick<AppDomainStatus, "zone" | "publicAddress"> | undefined,
): string {
  const zone = zoneWords(status)
  const message = errorMessage(error)
  if (!(error instanceof ApiError)) return message
  switch (error.code) {
    case "not_linked":
      return "This server is not linked to a just-dashboard.com account any more. Link it again, then claim the address."
    case "session_required":
      return "Claiming and releasing need a person signed in to this dashboard. An API key cannot do it."
    case "domain_in_use":
      return "Deployments still use this address. Remove it from them first:"
    case "invalid_handle":
      return `The site refused that handle. Use ${HANDLE_MIN} to ${HANDLE_MAX} characters from a-z, 0-9 and -, starting and ending with a letter or digit, with no --.`
    case "invalid_address":
      return `The site refused this server's address${status?.publicAddress ? `, ${status.publicAddress}` : ""}. It publishes only a public IPv4 address, never a private, shared or reserved one.`
    case "limit_reached":
      return "Your just-dashboard.com account already holds as many addresses as it may. Release one on another server first."
    case "account_suspended":
      return "Your just-dashboard.com account is suspended, so it cannot hold an address."
    case "domain_suspended":
      return "This server's address is suspended. Only a just-dashboard.com administrator can lift it."
    case "no_domain":
      return "This server holds no active address, so there is nothing to issue a certificate for. Claim one first."
    case "handle_reserved":
      return `That handle is reserved, or ${zone} already uses the name. Choose another.`
    case "handle_taken":
      return "Another server holds that handle. Choose another."
    case "handle_quarantined":
      // The site's sentence names the day it frees up.
      return `That handle was released recently by another account and is held for it. ${message}`
    case "address_mismatch": {
      const seen = addressIn(message)
      const ours = status?.publicAddress
      return seen
        ? `The site saw this server's request come from ${seen}${ours ? `, not ${ours}` : ""}. The address must be the one this server sends from, so a server behind NAT or an outgoing gateway cannot take one yet.`
        : `The site saw this server's request come from another address than the one it would publish. ${message}`
    }
    case "too_many_challenges":
      return "Four certificate challenges are already open. They expire within the hour; issue again after that."
    case "rate_limited":
      return error.retryAfterMs
        ? `Too many requests to just-dashboard.com. Try again in ${minutes(error.retryAfterMs)}.`
        : "Too many requests to just-dashboard.com. Try again later."
    case "dns_failed":
      return `The site could not update DNS. ${message} Try again.`
    case "domains_unavailable":
      return "just-dashboard.com is not offering addresses right now."
    case "unknown_server":
    case "server_removed":
      return "just-dashboard.com no longer knows this server. Link it again."
    case "stale_time":
      return "This server's clock is too far off for the site to accept its request. Correct the time and try again."
    default:
      return message
  }
}
