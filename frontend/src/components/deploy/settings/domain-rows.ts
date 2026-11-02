import type { DeploymentEnvironmentConfiguration } from "@/lib/types"
import type { TypedPasswords } from "@/components/deploy/settings/domain-passwords"

type DomainValue = DeploymentEnvironmentConfiguration["domains"][number]

/** The server's rule for a planned hostname (`plannedDomainRE`). */
const HOSTNAME =
  /^(?:\*\.)?[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$/
const PROTECTION_USER = /^[A-Za-z0-9._@-]{1,64}$/

export const PASSWORD_MIN = 8
/** bcrypt reads no further than this many bytes, so a longer password would quietly be a shorter one. */
export const PASSWORD_MAX_BYTES = 72

export function isWildcard(hostname: string) {
  return hostname.startsWith("*.")
}

/**
 * The name in what was typed or pasted. An address copied from a browser's bar
 * carries a scheme, a path and sometimes a port, none of which is the name.
 */
export function hostnameFromInput(raw: string) {
  return raw
    .trim()
    .toLowerCase()
    .replace(/^[a-z][a-z0-9+.-]*:\/\//, "")
    .replace(/[/?#].*$/, "")
    .replace(/:\d+$/, "")
    .replace(/\.$/, "")
}

/** Why the server would refuse a new name, said before it is asked. */
export function hostnameProblem(hostname: string, domains: Pick<DomainValue, "hostname">[]) {
  if (!hostname) return undefined
  if (!HOSTNAME.test(hostname))
    return "A hostname is letters, digits and dashes with at least one dot, like app.example.com."
  if (domains.some((domain) => domain.hostname.toLowerCase() === hostname))
    return `${hostname} is already on this environment.`
  return undefined
}

/** Whether the password field is one the server would accept: empty, or the right length. */
export function passwordProblem(password: string) {
  if (!password) return undefined
  if (password.length < PASSWORD_MIN) return `At least ${PASSWORD_MIN} characters`
  if (new TextEncoder().encode(password).length > PASSWORD_MAX_BYTES)
    return `At most ${PASSWORD_MAX_BYTES} characters`
  return undefined
}

/** Whether a user name is one the server would keep; an empty one is merely not typed yet. */
export function protectionUserProblem(username: string) {
  if (!username || PROTECTION_USER.test(username)) return undefined
  return "Letters, digits, dots, dashes, underscores and @, up to 64 characters."
}

/**
 * Whether a name asking for a password can be saved. An empty form is the
 * worst of these: the server reads a protection with nothing in it as none,
 * so it would say Saved and leave the site open.
 */
export function protectionIncomplete(domain: DomainValue, password: string) {
  const protection = domain.protection
  if (!protection) return false
  if (!protection.username.trim() || protectionUserProblem(protection.username)) return true
  if (passwordProblem(password)) return true
  return !password && !protection.hash
}

/** Whether any name in the list cannot be saved as it stands. */
export function protectionsIncomplete(domains: DomainValue[], typed: TypedPasswords) {
  return domains.some((domain) => protectionIncomplete(domain, typed[domain.hostname] ?? ""))
}

/**
 * The other names asking for the same user name with a different password.
 * The route holds one password per user, the first name's in list order, so
 * the other's would never be asked for.
 */
export function sharedUser(domains: DomainValue[], typed: TypedPasswords, hostname: string) {
  const mine = domains.find((domain) => domain.hostname === hostname)
  if (!mine?.protection?.username) return []
  const secret = (domain: DomainValue) => typed[domain.hostname] || domain.protection?.hash
  return domains
    .filter(
      (other) =>
        other.hostname !== hostname &&
        other.protection?.username === mine.protection?.username &&
        secret(other) !== secret(mine),
    )
    .map((other) => other.hostname)
}

/**
 * The names this one may redirect to: those serving the application, other
 * than itself and other than a wildcard, which is not an address to land on.
 */
export function redirectTargets(domains: DomainValue[], hostname: string) {
  return domains
    .filter(
      (domain) =>
        !domain.redirectTo && domain.hostname !== hostname && !isWildcard(domain.hostname),
    )
    .map((domain) => domain.hostname)
}

/**
 * The list with one name redirecting to the first name it may. A name already
 * redirecting here moves with it, since the server refuses a chain and the
 * visitor would otherwise be sent round twice.
 */
export function withRedirect(domains: DomainValue[], hostname: string) {
  const target = redirectTargets(domains, hostname)[0]
  if (!target) return domains
  return domains.map((domain) => {
    if (domain.hostname === hostname)
      return { ...domain, redirectTo: target, redirectCode: undefined }
    return domain.redirectTo === hostname ? { ...domain, redirectTo: target } : domain
  })
}

/** The list without one name. Whatever redirected to it goes back to serving. */
export function withoutDomain(domains: DomainValue[], hostname: string) {
  return domains
    .filter((domain) => domain.hostname !== hostname)
    .map((domain) =>
      domain.redirectTo === hostname
        ? { ...domain, redirectTo: undefined, redirectCode: undefined }
        : domain,
    )
}

/** What the HTTPS segment does on this name, as its tooltip says it. */
export function httpsTip({
  https,
  tunnel,
  gated,
}: {
  https: boolean
  tunnel: boolean
  /** The sign-in is on and this name serves, so it cannot go plain. */
  gated: boolean
}) {
  if (tunnel) return "Always on through the tunnel: Cloudflare's edge answers over HTTPS."
  if (gated) return "Stays on while sign-in is required, so the session cookie is never in clear."
  return https
    ? "Served with a certificate. Plain HTTP is redirected to HTTPS."
    : "Served over plain HTTP, with no certificate and no redirect."
}

/** What the Password segment does, which is more than this one name. */
export function passwordTip({ guarded, gated }: { guarded: boolean; gated: boolean }) {
  if (gated && !guarded)
    return "Not while sign-in is required: the browser would send the password to the auth server too."
  return "Asks for a user name and password before every name this environment serves, whichever one it is set on."
}

/** What the Tunnel segment does, or why it cannot be pressed. */
export function tunnelTip({
  tunnel,
  wildcard,
  fenced,
}: {
  tunnel: boolean
  wildcard: boolean
  /** The route has a saved address list. */
  fenced: boolean
}) {
  if (!tunnel && wildcard)
    return "A wildcard is not routed through a tunnel: each name is a route and a record of its own at Cloudflare."
  if (!tunnel && fenced)
    return "Not with Only these addresses set: every request would come from the tunnel's connector. Empty that list first."
  return "Cloudflare's edge answers this name and the connector on this host brings the requests here: no record points at this server and no port is opened."
}

/** What ownership means, as the select's tooltip: both answers, since the other is the choice. */
export function ownershipTip(words: { word: string; hint: string }[]) {
  return words.map(({ word, hint }) => `${word}: ${hint}.`).join(" ")
}
