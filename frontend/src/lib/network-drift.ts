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

export function driftCounts(observations: DriftObservation[]) {
  return {
    differences: observations.filter((o) => ["missing", "drift", "conflict"].includes(o.status))
      .length,
    unknown: observations.filter((o) => ["unreadable", "unknown"].includes(o.status)).length,
    matching: observations.filter((o) => o.status === "matching").length,
  }
}

export function driftReportObservations(report: DriftReport): DriftObservation[] {
  return [
    report.spec,
    report.journal,
    ...report.files,
    ...report.runtime,
    { id: "boot:unit", status: report.boot.status } as DriftObservation,
    {
      id: "boot:activation",
      status:
        report.boot.execution?.status === "failed"
          ? "drift"
          : report.boot.execution?.status === "succeeded"
            ? "matching"
            : "unknown",
    } as DriftObservation,
    ...report.blocklists.map(
      (list) =>
        ({
          id: `blocklist:${list.id}`,
          status: !list.enabled
            ? "not_required"
            : list.enforcement === "verified"
              ? "matching"
              : list.enforcement === "degraded"
                ? "drift"
                : "unknown",
        }) as DriftObservation,
    ),
  ]
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
