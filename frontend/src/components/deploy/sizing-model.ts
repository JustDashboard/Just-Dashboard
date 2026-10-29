import type { DotTone } from "@/components/status-dot"
import type { DeploymentConfiguration } from "@/lib/types"

/**
 * Right-sizing as the server answers it (`api/handlers_deploy_sizing.go`,
 * the maths in `metrics/sizing.go`), and the words the card and the fleet
 * panel say it in. Nothing here re-derives a suggestion: the server decides,
 * and the page explains how.
 */

export type SizingVerdict =
  "too_tight" | "oversized" | "unlimited" | "right" | "insufficient_history"

export type MemorySizing = {
  verdict: SizingVerdict
  limitMb: number
  liveLimitMb: number
  p95Mb: number
  peakMb: number
  suggestedMb: number
  oomKills: number
  offBy: number
  reasons: string[]
}

export type CpuSizing = {
  verdict: SizingVerdict
  limit: number
  liveLimit: number
  p95: number
  peak: number
  suggested: number
  throttledShare: number | null
  offBy: number
  reasons: string[]
}

export type SizingWindow = {
  from: string | null
  to: string | null
  samples: number
  containers: number
  intervalSeconds: number
  coveredSeconds: number
  neededSeconds: number
}

/** What "Apply suggested limits" writes: only the limits that are off. */
export type SizingApply = { memoryMb?: number; cpus?: number }

export type EnvironmentSizing = {
  projectId: number
  projectName: string
  environmentId: number
  environmentName: string
  environmentKind: string
  status: "available" | "compose" | "unavailable"
  reason?: string
  verdict: SizingVerdict
  offBy: number
  window: SizingWindow
  memory: MemorySizing | null
  cpu: CpuSizing | null
  apply: SizingApply | null
  revision: number
  pending: boolean
  oom: { watching: boolean; since: string | null; kills: number }
}

export type FleetSizing = {
  status: "available" | "unavailable"
  reason?: string
  retentionSeconds: number
  environments: EnvironmentSizing[]
}

export const VERDICT_LABEL: Record<SizingVerdict, string> = {
  too_tight: "Too tight",
  oversized: "Oversized",
  unlimited: "No limit",
  right: "Sized right",
  insufficient_history: "Not enough history",
}

/**
 * A limit killing or throttling the application is the red one; a server's
 * worth of memory set aside for nothing is worth a look; an unlimited
 * container is the default and stays quiet, as does what is fine.
 */
export function verdictTone(verdict: SizingVerdict): DotTone {
  switch (verdict) {
    case "too_tight":
      return "danger"
    case "oversized":
      return "warning"
    case "right":
      return "running"
    default:
      return "unknown"
  }
}

/** A verdict the fleet panel lists: one the operator could act on. */
export function isOff(sizing: EnvironmentSizing) {
  return (
    sizing.status === "available" &&
    sizing.apply !== null &&
    (sizing.verdict === "too_tight" ||
      sizing.verdict === "oversized" ||
      sizing.verdict === "unlimited")
  )
}

/** "512 MiB", "1.5 GiB". */
export function mib(value: number) {
  if (value >= 1024) {
    const gib = value / 1024
    return `${Number.isInteger(gib) ? gib : gib.toFixed(1)} GiB`
  }
  return `${Math.round(value)} MiB`
}

/** "0.35 cores", "1 core". */
export function cores(value: number) {
  const text = value >= 10 ? value.toFixed(0) : String(Math.round(value * 100) / 100)
  return `${text} ${value === 1 ? "core" : "cores"}`
}

/** A limit as the plan holds it, zero being none. */
export function memoryLimit(limitMb: number) {
  return limitMb > 0 ? mib(limitMb) : "no limit"
}

export function cpuLimit(limit: number) {
  return limit > 0 ? cores(limit) : "no limit"
}

/** "2 days", "18 h", "45 min" for a covered span. */
export function span(seconds: number) {
  if (seconds >= 2 * 86400) return `${Math.floor(seconds / 86400)} days`
  if (seconds >= 3600) return `${Math.floor(seconds / 3600)} h`
  return `${Math.max(0, Math.floor(seconds / 60))} min`
}

/**
 * What the judgement rests on, in one line: the samples and how much history
 * they are, or how much more it needs.
 */
export function historyLine(window: SizingWindow) {
  if (window.coveredSeconds < window.neededSeconds)
    return `${span(window.coveredSeconds)} of the ${span(window.neededSeconds)} of history a suggestion needs`
  return `${window.samples.toLocaleString()} samples over ${span(window.coveredSeconds)}`
}

/**
 * The readings behind one limit, after the suggestion they produced: the
 * limit it is measured against first, then what was used.
 */
export function memoryReadings(memory: MemorySizing) {
  const limit = memory.limitMb > 0 ? `limit ${mib(memory.limitMb)}` : "no limit"
  return `${limit} · p95 ${mib(memory.p95Mb)} · peak ${mib(memory.peakMb)}`
}

export function cpuReadings(cpu: CpuSizing) {
  const limit = cpu.limit > 0 ? `limit ${cores(cpu.limit)}` : "no limit"
  const parts = [limit, `p95 ${cores(cpu.p95)}`, `peak ${cores(cpu.peak)}`]
  if (cpu.throttledShare !== null && cpu.throttledShare > 0)
    parts.push(`throttled ${Math.round(cpu.throttledShare * 100)}%`)
  return parts.join(" · ")
}

/** "512 MiB → 640 MiB" for the limits an apply would change. */
export function applyLines(sizing: EnvironmentSizing) {
  const lines: string[] = []
  if (sizing.apply?.memoryMb !== undefined && sizing.memory)
    lines.push(`memory ${memoryLimit(sizing.memory.limitMb)} → ${mib(sizing.apply.memoryMb)}`)
  if (sizing.apply?.cpus !== undefined && sizing.cpu)
    lines.push(`CPU ${cpuLimit(sizing.cpu.limit)} → ${cores(sizing.apply.cpus)}`)
  return lines
}

/** How the two suggestions are computed, as the page says it. */
export const MEMORY_RULE =
  "Memory is the 95th percentile of the samples plus a quarter, never under the peak plus a tenth, rounded up to a 64 MiB step. After an OOM kill it is at least half again the limit it happened under."
export const CPU_RULE =
  "CPU is the 95th percentile plus half, at least a quarter core so a start-up is not throttled into its readiness timeout, rounded up. After throttling in 5% of periods it is at least half again the limit."

/** The plan's runtime with the suggested limits written into it. */
export function applyToRuntime(
  runtime: DeploymentConfiguration["runtime"],
  apply: SizingApply,
): DeploymentConfiguration["runtime"] {
  return {
    ...runtime,
    ...(apply.memoryMb !== undefined ? { memoryMb: apply.memoryMb } : {}),
    ...(apply.cpus !== undefined ? { cpus: apply.cpus } : {}),
  }
}

/**
 * What the fleet panel says about the environments it does not list: how many
 * are sized right and how many are still gathering history.
 */
export function fleetSummary(environments: EnvironmentSizing[]) {
  const right = environments.filter((sizing) => sizing.verdict === "right").length
  const measuring = environments.filter(
    (sizing) => sizing.status === "available" && sizing.verdict === "insufficient_history",
  ).length
  const parts: string[] = []
  if (right) parts.push(`${right} sized right`)
  if (measuring) parts.push(`${measuring} still gathering history`)
  return parts.join(" · ")
}
