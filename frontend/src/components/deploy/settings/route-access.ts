import { plural } from "@/lib/format"
import { ssoPreset } from "@/lib/sso-providers"
import type {
  DeploymentForwardAuth,
  DeploymentPlannedDomain,
  DeploymentRouteHSTS,
} from "@/lib/types"
import {
  HSTS_DEFAULT_MAX_AGE,
  HSTS_MAX_AGES,
  HSTS_PRELOAD_MAX_AGE,
} from "@/components/proxy/site-form"

/** The auth server a route signs in through, as a sentence names it. */
export function signInThrough(gate: DeploymentForwardAuth) {
  return gate.provider === "custom" ? "an auth server" : ssoPreset(gate.provider).label
}

/**
 * Whether the sign-in is on this environment's own route: always, unless it
 * is for previews only and this is not a preview's plan.
 */
export function gatesHere(gate: DeploymentForwardAuth | undefined, preview: boolean) {
  return Boolean(gate) && (!gate?.previewsOnly || preview)
}

/** Who reaches the domains above, as the Access and headers section's state. */
export function routeAccessState(
  allowFrom: string[],
  gate: DeploymentForwardAuth | undefined,
  preview = false,
): string {
  const fence =
    allowFrom.length > 0 ? `Only ${plural(allowFrom.length, "address", "addresses")}` : ""
  if (!gate) return `${fence || "Everyone"} · every domain above`
  const through = signInThrough(gate)
  if (!gatesHere(gate, preview))
    return `${fence || "Everyone"} here · previews sign in through ${through}`
  return fence
    ? `${fence}, signed in through ${through}`
    : `Signed in through ${through} · every domain above`
}

/**
 * What the server would refuse a sign-in on this route for, said before the
 * save: a password the browser would also send the auth server, and a name
 * served over plain HTTP, whose session cookie would cross every network in
 * clear. A name that only redirects serves nothing to protect.
 */
export function signInConflicts(
  domains: DeploymentPlannedDomain[],
  gate: DeploymentForwardAuth | undefined,
  preview = false,
): string[] {
  if (!gatesHere(gate, preview)) return []
  const conflicts: string[] = []
  const guarded = domains.filter((domain) => domain.protection).map((domain) => domain.hostname)
  if (guarded.length > 0)
    conflicts.push(
      `Signing in replaces the password on ${guarded.join(", ")}. Remove it under the domain first.`,
    )
  const plain = domains
    .filter((domain) => !domain.https && !domain.redirectTo)
    .map((domain) => domain.hostname)
  if (plain.length > 0)
    conflicts.push(
      `${plain.join(", ")} ${plain.length === 1 ? "is" : "are"} served over plain HTTP. Signing in needs HTTPS, so the session cookie never crosses a network in clear.`,
    )
  return conflicts
}

/**
 * Whether the HSTS control is offered: there is a served HTTPS domain to send
 * the header on, or a policy is saved and has to stay removable after the
 * last HTTPS domain was turned off.
 */
export function hstsOffered(
  domains: DeploymentPlannedDomain[],
  hsts: DeploymentRouteHSTS | undefined,
): boolean {
  return Boolean(hsts) || domains.some((domain) => domain.https && !domain.redirectTo)
}

/** The HSTS policy as the control's state: how long, and what it covers. */
export function hstsPolicyState(hsts: DeploymentRouteHSTS | undefined): string {
  if (!hsts) return "Off"
  const maxAge = hsts.maxAge || HSTS_DEFAULT_MAX_AGE
  const listed = HSTS_MAX_AGES.find((age) => age.value === maxAge)
  const parts = [listed ? listed.label.replace(/ \(.*\)$/, "") : `${maxAge} seconds`]
  if (hsts.includeSubDomains) parts.push("subdomains")
  if (hsts.preload) parts.push("preload")
  return parts.join(" · ")
}

/**
 * The policy as the plan keeps it: a default max-age left out, the switches
 * only when on, so a saved policy that changed nothing changes no digest.
 * Nothing when the control is off.
 */
export function savedHSTS(hsts: DeploymentRouteHSTS | undefined): DeploymentRouteHSTS | undefined {
  if (!hsts) return undefined
  const maxAge = hsts.maxAge || HSTS_DEFAULT_MAX_AGE
  return {
    ...(maxAge !== HSTS_DEFAULT_MAX_AGE ? { maxAge } : {}),
    ...(hsts.includeSubDomains ? { includeSubDomains: true } : {}),
    ...(hsts.preload ? { preload: true } : {}),
  }
}

/**
 * The policy with preload turned on, as the browsers' list requires it: a
 * max-age of at least a year, and the subdomains.
 */
export function withPreload(hsts: DeploymentRouteHSTS): DeploymentRouteHSTS {
  const maxAge = hsts.maxAge || HSTS_DEFAULT_MAX_AGE
  return {
    ...hsts,
    maxAge: Math.max(maxAge, HSTS_PRELOAD_MAX_AGE),
    includeSubDomains: true,
    preload: true,
  }
}
