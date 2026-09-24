import type { DeploymentCheckResult, DeploymentPreflightFinding } from "@/lib/types"

/**
 * What the advisory check found, read the way a deployment will be.
 *
 * `analyze_plan` stops a run on a blocked finding and on three decisions a
 * run cannot go past; every other decision and every warning lets the run
 * continue. The "Ready to deploy?" dialog draws the same line: what stops the
 * deployment cannot be deployed past, what only warns is confirmed once.
 */

/** Decisions `executionDecisionMustBlock` stops a run on (normalized_executor.go). */
export const RUN_BLOCKING_DECISIONS = new Set([
  "domain_link_missing",
  "readiness_missing",
  "go_main_ambiguous",
])

/** Whether a deployment would stop on this finding before it builds. */
export function stopsDeployment(finding: DeploymentPreflightFinding) {
  return (
    finding.severity === "blocked" ||
    (finding.severity === "decision" && RUN_BLOCKING_DECISIONS.has(finding.code))
  )
}

const ORDER: Record<DeploymentPreflightFinding["severity"], number> = {
  blocked: 0,
  decision: 1,
  warning: 2,
  unavailable: 3,
  pass: 4,
}

/** The findings worth a reader's attention, what stops a deployment first. */
export function attentionFindings(findings: DeploymentPreflightFinding[]) {
  return findings
    .filter((finding) => finding.severity !== "pass")
    .map((finding, index) => ({ finding, index }))
    .sort(
      (a, b) =>
        Number(!stopsDeployment(a.finding)) - Number(!stopsDeployment(b.finding)) ||
        ORDER[a.finding.severity] - ORDER[b.finding.severity] ||
        a.index - b.index,
    )
    .map(({ finding }) => finding)
}

/** Findings that ask before a deployment: they stop it, or a reader should confirm them. */
function asking(findings: DeploymentPreflightFinding[]) {
  return findings.filter(
    (finding) =>
      finding.severity === "blocked" ||
      finding.severity === "decision" ||
      finding.severity === "warning",
  )
}

/**
 * The warnings a reader confirmed, as one string: each code with what was
 * measured, sorted, so the same warnings about the same thing read the same
 * whichever order the server listed them in, and a warning about something
 * new reads differently.
 */
export function confirmationSignature(findings: DeploymentPreflightFinding[]) {
  return asking(findings)
    .filter((finding) => !stopsDeployment(finding))
    .map((finding) => `${finding.code}|${finding.measured ?? ""}`)
    .sort()
    .join("\n")
}

/**
 * Whether pressing Deploy should open "Ready to deploy?" rather than
 * enqueue. Something that stops the deployment always asks; warnings ask
 * until the reader has confirmed exactly these ones.
 */
export function needsConfirmation(result: DeploymentCheckResult | undefined, confirmed?: string) {
  if (!result) return false
  const findings = asking(result.findings)
  if (findings.some(stopsDeployment)) return true
  return findings.length > 0 && confirmationSignature(findings) !== (confirmed ?? "")
}

/**
 * The settings page that holds a finding's field, for a project that exists:
 * the Build page for the build plan, Runtime for the container, and so on.
 * The prefixes are the plan field ids preflight names, longest first so
 * `runtime.mounts` is read as Storage rather than Runtime.
 */
const FIELD_PAGES: { prefix: string; path: string }[] = [
  { prefix: "configuration.build.releaseTasks", path: "/settings/build#release-tasks" },
  { prefix: "configuration.build.rootDirectory", path: "/settings/build#commands" },
  { prefix: "configuration.build.startCommand", path: "/settings/build#commands" },
  { prefix: "configuration.build.buildCommand", path: "/settings/build#commands" },
  { prefix: "configuration.build.outputDirectory", path: "/settings/build#commands" },
  { prefix: "configuration.build", path: "/settings/build" },
  { prefix: "build.startCommand", path: "/settings/build#commands" },
  { prefix: "build", path: "/settings/build" },
  { prefix: "source", path: "/settings/general#source" },
  { prefix: "detection", path: "/settings/build" },
  { prefix: "runtime.mounts", path: "/settings/storage" },
  { prefix: "runtime", path: "/settings/runtime" },
  { prefix: "checks", path: "/settings/runtime#health-checks" },
  { prefix: "domains", path: "/settings/domains" },
  { prefix: "variables", path: "/settings/variables" },
  { prefix: "dependencies", path: "/settings/databases" },
]

export function settingsPathForField(fieldId: string | undefined) {
  if (!fieldId) return undefined
  return FIELD_PAGES.find(
    (entry) => fieldId === entry.prefix || fieldId.startsWith(`${entry.prefix}.`),
  )?.path
}

/**
 * The last check each environment had, kept for the tab's life so a surface
 * that did not ask — a card on the projects grid — can still say what the
 * project page found. An answer is used only for the plan revision it was
 * about and only while it is recent: an old warning shown as current would
 * be worse than none, and analyze_plan checks again before building anyway.
 */
const CHECKS = new Map<number, { result: DeploymentCheckResult; at: number }>()

export const CHECK_FRESH_MS = 5 * 60 * 1000

export function rememberDeploymentCheck(
  environmentId: number,
  result: DeploymentCheckResult,
  now = Date.now(),
) {
  CHECKS.set(environmentId, { result, at: now })
}

export function cachedDeploymentCheck(
  environmentId: number,
  planRevision: number | undefined,
  now = Date.now(),
) {
  const entry = CHECKS.get(environmentId)
  if (!entry || now - entry.at > CHECK_FRESH_MS) return undefined
  if (planRevision !== undefined && entry.result.planRevision !== planRevision) return undefined
  return entry.result
}
