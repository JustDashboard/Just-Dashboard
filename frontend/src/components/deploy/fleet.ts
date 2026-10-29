export const FAILING_SHARE = 0.05

import type { DeploymentSummary, FleetPullRequests, TrafficPulse } from "@/lib/types"
import type { Tone } from "@/components/tone"
import { imageProducts } from "@/components/product-logo"
import {
  RECIPE_SHORT,
  frameworkLabel,
  projectProduct,
  projectState,
  runCommit,
  type ProjectState,
} from "@/components/deploy/vocabulary"

/**
 * What the fleet page decides about its projects, kept apart from how it draws
 * them so each decision can be tested on its own: which chip a project falls
 * under, the order the cards are read in, what needs somebody, and the four
 * readings across the top.
 */

/** Every project's last hour at the ingress, keyed by project id as `/deploy/traffic` answers. */
export type FleetPulses = Record<string, TrafficPulse> | undefined

/** Every GitHub project's open pull requests and previews, keyed by project id as `/deploy/pull-requests` answers. */
export type FleetPulls = FleetPullRequests["projects"]

/** Below this a site's failures are noise, and its share is not drawn at all. */
export const FAILING_NOTICE = 0.01

/**
 * The one colour a failing share is drawn in, wherever it is drawn — the
 * fleet's tile, the worst site under it, a card and a row — so the same 1.0%
 * cannot be amber in one place and red a hand's width below it (§3).
 */
export function failingTone(rate: number): Tone {
  return rate >= FAILING_SHARE ? "danger" : rate >= FAILING_NOTICE ? "warning" : "default"
}

export type FleetFilter = "all" | "deploying" | "failed" | "pending" | "pulls"

export const FLEET_FILTERS: { key: FleetFilter; label: string }[] = [
  { key: "all", label: "All" },
  { key: "deploying", label: "Deploying" },
  { key: "failed", label: "Failed" },
  { key: "pending", label: "Changes pending" },
  { key: "pulls", label: "Pull requests" },
]

/**
 * `pulls` is the fleet's pull-request read, which arrives on its own clock
 * and only for GitHub projects: a project it has no entry for has none open,
 * and until it answers no project has, so the chip stays hidden rather than
 * counting a read that has not landed.
 */
export function matchesFilter(
  deployment: DeploymentSummary,
  filter: FleetFilter,
  pulses: FleetPulses,
  pulls: FleetPulls = {},
) {
  switch (filter) {
    case "deploying":
      return Boolean(deployment.activeRun)
    case "failed":
      return projectState(deployment) === "failed"
    case "pending":
      return deployment.pendingChanges
    case "pulls":
      return (pulls[String(deployment.id)]?.open ?? 0) > 0
    default:
      return true
  }
}

/** How many projects each chip would leave, for the count it carries. */
export function fleetCounts(
  deployments: DeploymentSummary[],
  pulses: FleetPulses,
  pulls: FleetPulls = {},
) {
  return Object.fromEntries(
    FLEET_FILTERS.map(({ key }) => [
      key,
      deployments.filter((deployment) => matchesFilter(deployment, key, pulses, pulls)).length,
    ]),
  ) as Record<FleetFilter, number>
}

const RANK: Record<ProjectState, number> = {
  failed: 0,
  unhealthy: 1,
  deploying: 2,
  ready: 4,
  asleep: 4,
  stopped: 5,
  not_deployed: 6,
  archived: 7,
}

/**
 * Where a project sits in the fleet: what is broken first, then what is
 * moving, then what is waiting to be deployed, then the rest. The order is the
 * page's answer to "which of these needs me", the way the Git page orders its
 * checkouts; ties go by name, so a card moves only when its state does.
 */
export function fleetRank(deployment: DeploymentSummary) {
  const state = projectState(deployment)
  return state === "ready" && deployment.pendingChanges ? 3 : RANK[state]
}

export function sortFleet(deployments: DeploymentSummary[]) {
  return [...deployments].sort(
    (a, b) => fleetRank(a) - fleetRank(b) || a.name.localeCompare(b.name),
  )
}

/**
 * Everything a project is findable by. A card draws its product, its images
 * and its last commit, so "postgres", "next" or "fix checkout" each find the
 * project they are drawn on.
 */
export function fleetHaystack(deployment: DeploymentSummary) {
  const images = deployment.images ?? []
  return [
    deployment.name,
    deployment.endpoint,
    deployment.sourceRef,
    deployment.sourceRepository,
    deployment.framework && frameworkLabel(deployment.framework),
    deployment.recipe && RECIPE_SHORT[deployment.recipe],
    projectProduct(deployment),
    ...imageProducts(images),
    ...images,
    runCommit(deployment.lastRun)?.subject,
  ]
    .filter(Boolean)
    .join(" ")
    .toLowerCase()
}
