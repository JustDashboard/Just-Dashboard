import { plural } from "@/lib/format"
import type {
  DeploymentErrorPage,
  DeploymentMaintenanceSwitch,
  DeploymentRouteMaintenance,
} from "@/lib/types"

/** The codes a route can answer with a page of its own, in the order they are listed. */
export const ERROR_PAGES: DeploymentErrorPage[] = [404, 502, 503, 504]

/** The longest wait a maintenance 503 may ask for: a day, as the server holds it. */
export const MAX_RETRY_AFTER = 86_400

/** Where the switch stands, as the Maintenance section's state under its title. */
export function maintenanceState(live: DeploymentMaintenanceSwitch | undefined): string {
  if (!live) return "Reading the live route"
  if (!live.route) return "No public route yet"
  if (!live.ready) return "Switches at once after the next deployment"
  return live.on ? "On · visitors get the maintenance page" : "Off · visitors reach the application"
}

/**
 * Why the switch cannot be turned on yet, said under it, or nothing when it
 * can. Turning it off never waits: a switch left on is always cleared.
 */
export function maintenanceUnready(live: DeploymentMaintenanceSwitch | undefined): string | null {
  if (!live || live.on) return null
  if (!live.route) return "Add a domain and deploy, and maintenance switches from here at once."
  if (!live.ready)
    return "The live release's route was written before maintenance could be switched. Deploy once, and from then on it switches at once, without a deployment."
  return null
}

/** What a visitor is told while it is on, as the Maintenance page section's state. */
export function maintenancePolicyState(policy: DeploymentRouteMaintenance | undefined): string {
  const bypass = policy?.bypassFrom?.length ?? 0
  const who = bypass > 0 ? `${plural(bypass, "address", "addresses")} let past` : "Nobody let past"
  const retry = policy?.retryAfter ? `Retry-After ${policy.retryAfter}s` : "no Retry-After"
  return `${who} · ${retry}`
}

/** Which answers get the route's own page, as the Error pages section's state. */
export function errorPagesState(codes: DeploymentErrorPage[], intercept: boolean): string {
  if (codes.length === 0) return "The proxy's own pages"
  const listed = ERROR_PAGES.filter((code) => codes.includes(code))
  const named =
    listed.length === 1
      ? String(listed[0])
      : `${listed.slice(0, -1).join(", ")} and ${listed.at(-1)}`
  return `Own ${named} ${listed.length === 1 ? "page" : "pages"}${intercept ? " · the application's too" : ""}`
}

/**
 * The Retry after field as the plan keeps it: whole seconds up to a day, or
 * nothing for an empty field or 0. `null` is a value the server would refuse.
 */
export function retryAfterOf(text: string): number | undefined | null {
  const trimmed = text.trim()
  if (trimmed === "") return undefined
  if (!/^\d+$/.test(trimmed)) return null
  const seconds = Number(trimmed)
  if (seconds > MAX_RETRY_AFTER) return null
  return seconds === 0 ? undefined : seconds
}

/**
 * The plan's maintenance from the form's fields, or nothing when it asks for
 * nothing, which is how a plan that never set it keeps its digest.
 */
export function routeMaintenanceOf(
  retryAfter: number | undefined,
  bypassFrom: string[],
): DeploymentRouteMaintenance | undefined {
  if (!retryAfter && bypassFrom.length === 0) return undefined
  return {
    ...(retryAfter ? { retryAfter } : {}),
    ...(bypassFrom.length > 0 ? { bypassFrom } : {}),
  }
}
