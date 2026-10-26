import { plural } from "@/lib/format"
import { ssoPreset } from "@/lib/sso-providers"
import type { DeploymentForwardAuth, DeploymentPlannedDomain } from "@/lib/types"

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
