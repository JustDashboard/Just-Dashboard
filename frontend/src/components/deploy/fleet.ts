import { percent, relativeTime } from "@/lib/format"
import { perMinute } from "@/lib/requests"
import type { DeploymentSummary, TrafficPulse } from "@/lib/types"
import type { Finding } from "@/components/finding-list"
import type { Tone } from "@/components/tone"
import { imageProducts } from "@/components/product-logo"
import {
  RECIPE_SHORT,
  RUN_LABELS,
  frameworkLabel,
  projectProduct,
  projectState,
  runCommit,
  runTitle,
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

/** A request share this high is a site failing, not a site having a bad minute. */
export const FAILING_SHARE = 0.05

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

export type FleetFilter = "all" | "deploying" | "failed" | "attention" | "pending"

export const FLEET_FILTERS: { key: FleetFilter; label: string }[] = [
  { key: "all", label: "All" },
  { key: "deploying", label: "Deploying" },
  { key: "failed", label: "Failed" },
  { key: "attention", label: "Attention" },
  { key: "pending", label: "Changes pending" },
]

function pulseOf(deployment: DeploymentSummary, pulses: FleetPulses) {
  const pulse = pulses?.[String(deployment.id)]
  return pulse?.status === "available" ? pulse : undefined
}

/**
 * The projects the Attention list names: a deploy that failed, a live release
 * failing its health check, a site failing a twentieth of its requests.
 *
 * Health `unavailable` is not among them. It means no check has been observed —
 * every project without a health check reads that way — and a chip counting
 * six of those on a healthy fleet teaches the reader to stop looking at it.
 */
export function needsAttention(deployment: DeploymentSummary, pulses: FleetPulses) {
  return (
    projectState(deployment) === "failed" ||
    deployment.health === "unhealthy" ||
    deployment.health === "failed" ||
    (pulseOf(deployment, pulses)?.errorRate ?? 0) >= FAILING_SHARE
  )
}

export function matchesFilter(
  deployment: DeploymentSummary,
  filter: FleetFilter,
  pulses: FleetPulses,
) {
  switch (filter) {
    case "deploying":
      return Boolean(deployment.activeRun)
    case "failed":
      return projectState(deployment) === "failed"
    case "attention":
      return needsAttention(deployment, pulses)
    case "pending":
      return deployment.pendingChanges
    default:
      return true
  }
}

/** How many projects each chip would leave, for the count it carries. */
export function fleetCounts(deployments: DeploymentSummary[], pulses: FleetPulses) {
  return Object.fromEntries(
    FLEET_FILTERS.map(({ key }) => [
      key,
      deployments.filter((deployment) => matchesFilter(deployment, key, pulses)).length,
    ]),
  ) as Record<FleetFilter, number>
}

const RANK: Record<ProjectState, number> = {
  failed: 0,
  unhealthy: 1,
  deploying: 2,
  ready: 4,
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

/** A finding whose remedy is a page to open, so the list stays free of a router. */
export type FleetFinding = Omit<Finding, "action"> & { action: { label: string; href: string } }

const LEVEL_ORDER: Record<Finding["level"], number> = { critical: 0, warning: 1, notice: 2 }

/** One thing wrong with a project, before the project's things are drawn as one finding. */
type Fact = {
  level: Finding["level"]
  title: string
  detail: string
  /** The same fact as a sentence under another fact's title. */
  also: string
  advice?: string
  meta: string
  action: FleetFinding["action"]
}

function sentence(text: string) {
  return /[.!?…]$/.test(text) ? text : `${text}.`
}

/** What is wrong with one project, worst first. */
function projectFacts(deployment: DeploymentSummary, pulses: FleetPulses): Fact[] {
  const facts: Fact[] = []
  const base = `/deploy/${deployment.id}`
  const live = Boolean(deployment.liveReleaseId)
  const last = deployment.lastRun
  if (projectState(deployment) === "failed" && last) {
    const failed = `${runTitle(last)} failed ${relativeTime(last.endedAt ?? last.requestedAt)}`
    const reason = last.terminalReason ?? RUN_LABELS[last.state]
    facts.push({
      level: live ? "warning" : "critical",
      title: `${deployment.name}: ${failed}`,
      detail: reason,
      also: sentence(`${failed}: ${reason}`),
      advice: live
        ? "The live release is still serving. Open the run to read the step that failed."
        : "Nothing is serving yet. Open the run to read the step that failed.",
      meta: "deploy failed",
      action: { label: "Open run", href: `${base}/runs/${last.id}` },
    })
  }
  if (live && (deployment.health === "unhealthy" || deployment.health === "failed")) {
    const detail = "The live release's last readiness check did not pass."
    facts.push({
      level: "critical",
      title: `${deployment.name} is failing its health check`,
      detail,
      also: detail,
      meta: "unhealthy",
      action: { label: "Open runtime", href: `${base}/runtime` },
    })
  }
  const pulse = pulseOf(deployment, pulses)
  if (pulse && pulse.errorRate >= FAILING_SHARE) {
    const share = percent(pulse.errorRate * 100, 1)
    const rate = perMinute(pulse.perMinute)
    facts.push({
      level: "warning",
      title: `${deployment.name} is failing ${share} of requests`,
      detail: `${rate} requests a minute over the last hour.`,
      also: `${share} of its ${rate} requests a minute failed over the last hour.`,
      meta: "traffic",
      action: { label: "Open requests", href: `${base}/logs` },
    })
  }
  return facts.sort((a, b) => LEVEL_ORDER[a.level] - LEVEL_ORDER[b.level])
}

/**
 * What somebody has to act on, in the engine's own words: the reason a deploy
 * stopped, the live release failing its readiness check, the site answering
 * errors. Critical first — a deploy that failed with nothing live is a site
 * that never came up, where the same failure beside a serving release is a
 * change that did not land.
 *
 * One finding per project, as the Attention chip counts them: a project
 * failing three ways is one thing to go and look at, titled by the worst of
 * them, with the rest said in its body and named at its edge. Three rows each
 * opening on the same name beside a chip reading "Attention 1" was two
 * answers to one question.
 */
export function fleetAttention(
  deployments: DeploymentSummary[],
  pulses: FleetPulses,
): FleetFinding[] {
  const findings: FleetFinding[] = []
  for (const deployment of deployments) {
    const [worst, ...rest] = projectFacts(deployment, pulses)
    if (!worst) continue
    findings.push({
      id: `project-${deployment.id}`,
      level: worst.level,
      title: worst.title,
      detail: [worst.detail, ...rest.map((fact) => fact.also)].join(" "),
      advice: worst.advice,
      meta: [worst, ...rest].map((fact) => fact.meta).join(" · "),
      action: worst.action,
    })
  }
  return findings.sort((a, b) => LEVEL_ORDER[a.level] - LEVEL_ORDER[b.level])
}

/**
 * The fleet's live projects, what is not running, and what the running ones
 * are — each product once, the most common first, as the Overview's Docker
 * tile names its images.
 *
 * Live is what is serving: a release that is up, whatever its last deploy
 * did. A site whose newest deploy failed, or which is deploying its next
 * release, is still answering on the one before, and a tile that left it out
 * read "4 of 7" beside a Requests tile counting five sites answering.
 */
export function fleetLive(deployments: DeploymentSummary[]) {
  const states = deployments.map((deployment) => projectState(deployment))
  const serving = deployments.filter(
    (deployment) => deployment.liveReleaseId && !deployment.stopped,
  )
  const counts = new Map<string, number>()
  for (const deployment of serving) {
    const product = projectProduct(deployment)
    if (product) counts.set(product, (counts.get(product) ?? 0) + 1)
  }
  return {
    serving: serving.length,
    stopped: states.filter((state) => state === "stopped").length,
    notDeployed: states.filter((state) => state === "not_deployed").length,
    products: [...counts.keys()].sort((a, b) => (counts.get(b) ?? 0) - (counts.get(a) ?? 0)),
  }
}

/**
 * The fleet's last hour at the ingress, summed: requests a minute and their
 * shape, how many page views, what share failed, which site failed most and
 * how many are failing outright. Undefined when no project has routed traffic
 * to read.
 *
 * Each pulse is the same hour in one-minute buckets, so the lines add bucket
 * by bucket; they are aligned on their newest bucket, where every one of
 * them ends.
 */
export function fleetTraffic(deployments: DeploymentSummary[], pulses: FleetPulses) {
  const answering = deployments.flatMap((deployment) => {
    const pulse = pulseOf(deployment, pulses)
    return pulse ? [{ deployment, pulse }] : []
  })
  if (answering.length === 0) return undefined
  const length = Math.max(...answering.map(({ pulse }) => pulse.points.length))
  const points = Array.from({ length }, (_, index) =>
    answering.reduce((sum, { pulse }) => {
      const at = index - (length - pulse.points.length)
      return sum + (at >= 0 ? pulse.points[at] : 0)
    }, 0),
  )
  const total = answering.reduce((sum, { pulse }) => sum + pulse.perMinute, 0)
  const failed = answering.reduce((sum, { pulse }) => sum + pulse.errorRate * pulse.perMinute, 0)
  const worst = answering.reduce((a, b) => (b.pulse.errorRate > a.pulse.errorRate ? b : a))
  return {
    sites: answering.length,
    perMinute: total,
    pages: answering.reduce((sum, { pulse }) => sum + pulse.pages, 0),
    points,
    share: total > 0 ? failed / total : 0,
    worst: { name: worst.deployment.name, errorRate: worst.pulse.errorRate },
    failingSites: answering.filter(({ pulse }) => pulse.errorRate >= FAILING_SHARE).length,
  }
}
