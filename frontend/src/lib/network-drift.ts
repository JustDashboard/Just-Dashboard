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
