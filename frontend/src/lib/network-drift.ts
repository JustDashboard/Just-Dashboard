import type { NetworkChangeStatus } from "./types"

export type DriftStatus =
  "matching" | "missing" | "drift" | "conflict" | "unreadable" | "unknown" | "not_required"

export type DriftObservation = {
  id: string
  domain: string
  resource: string
  status: DriftStatus
  coverage: string
  reason?: string
  expected?: Record<string, string>
  observed?: Record<string, string>
  owned: boolean
  repairable: boolean
  dependencies?: string[]
}

export type OwnedRepair = {
  id: string
  observationId: string
  domain: string
  resource: string
  action: string
  reason: string
  preconditions: string[]
  dependencies?: string[]
  executable?: boolean
  blocker?: string
  reviewToken?: string
  before?: string
  after?: string
  effect?: "boot_files" | "runtime"
}

export type DriftReport = {
  checkedAt: string
  finishedAt: string
  status: "matching" | "drift" | "unknown"
  consistent: boolean
  savedGeneration?: string
  canonicalGeneration?: string
  spec: DriftObservation
  journal: DriftObservation
  change?: NetworkChangeStatus
  files: DriftObservation[]
  runtime: DriftObservation[]
  boot: {
    status: DriftStatus
    reason?: string
    unit: string
    loadState?: string
    unitFileState?: string
    activeState?: string
    fragmentPath?: string
    dropInPaths?: string
    needDaemonReload?: string
    owned: boolean
    repairable: boolean
    execution: {
      status: "unknown" | "unrecorded" | "running" | "failed" | "succeeded"
      source: string
      scope: string
      bootTrigger: string
      bootId?: string
      invocationId?: string
      startedAt?: string
      finishedAt?: string
      startedMonotonicUs?: number
      finishedMonotonicUs?: number
      result?: string
      exitStatus?: number
      reason?: string
      commands: {
        path: string
        ignoreErrors: boolean
        startedAt?: string
        finishedAt?: string
        code?: string
        exitStatus?: number
      }[]
    }
  }
  blocklists: {
    id: number
    name: string
    enabled: boolean
    enforcement: string
    renderedGeneration: string
    cache: { status: string; count: number; generation: string; error?: string }
    runtime: { status: string; count: number | null; generation: string; error?: string }
  }[]
  repairPlan: {
    generation?: string
    createdAt: string
    status: string
    executable: boolean
    blockers: string[]
    items: OwnedRepair[]
    excluded: { observationId: string; reason: string }[]
    preconditions: string[]
  }
}

export function driftReading(status: string) {
  switch (status) {
    case "matching":
      return { label: "Matches", tone: "running" as const }
    case "missing":
      return { label: "Missing", tone: "warning" as const }
    case "drift":
      return { label: "Changed", tone: "warning" as const }
    case "conflict":
      return { label: "Ownership conflict", tone: "danger" as const }
    case "unreadable":
      return { label: "Could not read", tone: "unknown" as const }
    case "not_required":
      return { label: "Not required", tone: "stopped" as const }
    default:
      return { label: "Incomplete comparison", tone: "unknown" as const }
  }
}

export function driftCounts(observations: { status: string }[]) {
  return {
    differences: observations.filter((o) => ["missing", "drift", "conflict"].includes(o.status))
      .length,
    unknown: observations.filter((o) => ["unreadable", "unknown"].includes(o.status)).length,
    matching: observations.filter((o) => o.status === "matching").length,
  }
}

export type DriftDomain = "files" | "kernel" | "boot" | "blocklists"

/** The order the picture draws the domains in: what is saved, then where it is written, then what runs. */
export const DRIFT_DOMAINS: { key: DriftDomain; label: string }[] = [
  { key: "files", label: "Rendered files" },
  { key: "kernel", label: "Kernel objects" },
  { key: "boot", label: "Boot unit" },
  { key: "blocklists", label: "Blocklists" },
]

/** One compared thing, whichever list of the report it came from. */
export type DriftRow = {
  id: string
  domain: DriftDomain | "config"
  resource: string
  status: DriftStatus
  reason?: string
  /** The comparison behind it, for the observations the backend compared field by field. */
  observation?: DriftObservation
}

function blocklistStatus(list: DriftReport["blocklists"][number]): DriftStatus {
  if (!list.enabled) return "not_required"
  if (list.enforcement === "verified") return "matching"
  return list.enforcement === "degraded" ? "drift" : "unknown"
}

function activationStatus(report: DriftReport): DriftStatus {
  const status = report.boot.execution?.status
  return status === "failed" ? "drift" : status === "succeeded" ? "matching" : "unknown"
}

/**
 * Everything the report compares, as one list with the same members the counts
 * are taken over: the saved configuration and journal, each owned file and
 * kernel object, the boot unit and its last measured activation, and each
 * blocklist's generations.
 */
export function driftRows(report: DriftReport): DriftRow[] {
  const observed = (domain: DriftRow["domain"], item: DriftObservation): DriftRow => ({
    id: item.id,
    domain,
    resource: item.resource,
    status: item.status,
    reason: item.reason,
    observation: item,
  })
  return [
    observed("config", report.spec),
    observed("config", report.journal),
    ...report.files.map((item) => observed("files", item)),
    ...report.runtime.map((item) => observed("kernel", item)),
    {
      id: "boot:unit",
      domain: "boot",
      resource: report.boot.unit,
      status: report.boot.status,
      reason: report.boot.reason,
    },
    {
      id: "boot:activation",
      domain: "boot",
      resource: "Last measured activation",
      status: activationStatus(report),
      reason: report.boot.execution?.reason,
    },
    ...report.blocklists.map((list): DriftRow => ({
      id: `blocklist:${list.id}`,
      domain: "blocklists",
      resource: list.name,
      status: blocklistStatus(list),
      reason: list.cache.error ?? list.runtime.error,
    })),
  ]
}

export type DriftSummary = {
  key: DriftDomain | "config"
  total: number
  matching: number
  differences: number
  unknown: number
  /** The worst reading in the domain, which is the colour of its line in the picture. */
  tone: "success" | "warning" | "danger" | "default"
}

/** A domain's counts, taken over the same rows the table lists. */
export function driftSummary(rows: DriftRow[], key: DriftSummary["key"]): DriftSummary {
  // A thing that is not required has nothing to compare, so it is not one of the readings.
  const own = rows.filter((row) => row.domain === key && row.status !== "not_required")
  const counts = driftCounts(own)
  const conflict = own.some((row) => row.status === "conflict")
  return {
    key,
    total: own.length,
    ...counts,
    tone: conflict
      ? "danger"
      : counts.differences
        ? "warning"
        : counts.unknown || counts.matching === 0
          ? "default"
          : "success",
  }
}

/** The verdict at the end of the identity line. */
export function driftVerdict(rows: DriftRow[], consistent: boolean, stale: boolean) {
  const counts = driftCounts(rows)
  if (stale) return { tone: "warning" as const, label: "Last known evidence" }
  if (!consistent) return { tone: "warning" as const, label: "Changed during inspection" }
  if (counts.differences)
    return {
      tone: rows.some((row) => row.status === "conflict")
        ? ("danger" as const)
        : ("warning" as const),
      label: `${counts.differences} ${counts.differences === 1 ? "difference" : "differences"}`,
    }
  if (counts.unknown)
    return {
      tone: "unknown" as const,
      label: `${counts.unknown} ${counts.unknown === 1 ? "reading" : "readings"} incomplete`,
    }
  return { tone: "running" as const, label: "Everything matches" }
}

export type DriftMove = {
  id: string
  resource: string
  from: DriftStatus
  to: DriftStatus
  at: number
}

/**
 * What changed between two inspections of the same report: a row whose status
 * is not the one it had, and a row that appeared or went. The first inspection
 * of a page has nothing to compare with and reports nothing.
 */
export function driftMoves(
  before: Map<string, DriftStatus> | undefined,
  rows: DriftRow[],
  at: number,
): DriftMove[] {
  if (!before) return []
  return rows.flatMap((row) => {
    const was = before.get(row.id)
    return was && was !== row.status
      ? [{ id: row.id, resource: row.resource, from: was, to: row.status, at }]
      : []
  })
}

export function selectedDriftRepairRequest(report: DriftReport, selected: string[]) {
  if (
    !report.consistent ||
    report.repairPlan.status === "blocked" ||
    !report.repairPlan.executable ||
    !report.savedGeneration ||
    report.savedGeneration !== report.repairPlan.generation ||
    !selected.length ||
    new Set(selected).size !== selected.length
  )
    return undefined
  const items = report.repairPlan.items.filter((item) => selected.includes(item.id))
  if (
    items.length !== selected.length ||
    items.some((item) => !item.executable || !item.reviewToken)
  )
    return undefined
  return {
    generation: report.savedGeneration,
    selections: items.map(({ id, reviewToken }) => ({ id, reviewToken })),
  }
}

export function driftRepairOutcome(change: NetworkChangeStatus) {
  const pending = change.phase === "awaiting_confirmation" && change.watchdog === "armed"
  const completed = ["saved", "confirmed"].includes(change.phase) && change.watchdog === "completed"
  const runtimeApplied = change.runtime === "applied"
  const filesSaved = change.persistence === "written" && change.boot === "not_verified"
  const runtimeOnly =
    runtimeApplied && change.persistence === "not_applicable" && change.boot === "not_applicable"
  if (
    (!pending && !completed) ||
    (change.recoveryErrors?.length ?? 0) > 0 ||
    (!filesSaved && !runtimeOnly) ||
    !["applied", "not_applied"].includes(change.runtime)
  )
    return {
      tone: "warning" as const,
      title: "Inspect the repair outcome",
      description:
        "The returned change does not establish a completed repair. Inspect its current status before retrying.",
    }
  return {
    tone: "success" as const,
    title: pending ? "Repairs await reconnection confirmation" : "Selected repairs applied",
    description: runtimeOnly
      ? "Runtime applied; not saved for boot."
      : runtimeApplied
        ? "Selected runtime rules applied and boot inputs saved; boot execution remains unverified."
        : "Selected boot inputs saved; boot execution remains unverified.",
  }
}

// Keep a selection only while its reviewed evidence is unchanged. Poll time is
// excluded: a fresh read of the same facts should not interrupt an operator.
export function driftReviewKey(report: DriftReport) {
  return JSON.stringify({
    generation: report.savedGeneration,
    consistent: report.consistent,
    phase: report.change?.phase,
    journalId: report.change?.id,
    journalGeneration: report.change?.generation,
    boot: {
      status: report.boot.status,
      owned: report.boot.owned,
      activeState: report.boot.activeState,
      bootId: report.boot.execution?.bootId,
      fragmentPath: report.boot.fragmentPath,
      dropInPaths: report.boot.dropInPaths,
      needDaemonReload: report.boot.needDaemonReload,
      unitFileState: report.boot.unitFileState,
    },
    plan: {
      generation: report.repairPlan.generation,
      status: report.repairPlan.status,
      executable: report.repairPlan.executable,
      blockers: report.repairPlan.blockers,
      items: report.repairPlan.items,
      excluded: report.repairPlan.excluded,
      preconditions: report.repairPlan.preconditions,
    },
    observations: [report.spec, report.journal, ...report.files, ...report.runtime].map(
      ({ id, status, expected, observed }) => ({
        id,
        status,
        expected,
        observed,
      }),
    ),
  })
}
