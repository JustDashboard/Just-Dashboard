import { bytes, rate } from "@/lib/format"
import type { Series } from "@/components/metrics/metric-chart"
import { compact, perSecond } from "@/components/database/home/readings"
import { TRANSACTIONS, sourced, type Sample, type Source } from "@/components/database/home/samples"

/**
 * What the home's one chart can show, per engine family.
 *
 * A view is a group of lines that share a unit — sessions, throughput, the
 * cache — and a family is a short list of views. Each line names where its
 * numbers come from in a sample, so adding a measurement is naming a series
 * (§10) and the chart itself is `ChartPanel`, untouched.
 *
 * Everything here is a module constant on purpose: `ChartPanel` is memoised
 * on its props, and a series list or a formatter made during render would
 * redraw the plot on every poll of the page around it.
 */
export type ChartUnit = "count" | "rate" | "bytes" | "bytesRate" | "percent"

export type ChartView = {
  id: string
  label: string
  unit: ChartUnit
  series: Series[]
  sources: Source[]
}

type Line = Series & { from: Omit<Source, "key"> }

function view(id: string, label: string, unit: ChartUnit, lines: Line[]): ChartView {
  return {
    id,
    label,
    unit,
    series: lines.map(({ key, label: name, color, kind }) =>
      kind ? { key, label: name, color, kind } : { key, label: name, color },
    ),
    sources: lines.map((line) => ({ key: line.key, ...line.from }) as Source),
  }
}

const CACHE_HIT = (hits: string, misses: string): Line => ({
  key: "hit",
  label: "Hit rate",
  color: "var(--chart-2)",
  kind: "area",
  from: { share: [hits, misses] },
})

export const SQL_VIEWS: ChartView[] = [
  view("sessions", "Sessions", "count", [
    {
      key: "open",
      label: "Open",
      color: "var(--chart-1)",
      kind: "area",
      from: { gauge: "sessions" },
    },
    { key: "active", label: "Active", color: "var(--chart-5)", from: { gauge: "sessionsActive" } },
    {
      key: "waiting",
      label: "Waiting on a lock",
      color: "var(--chart-3)",
      from: { gauge: "sessionsWaiting" },
    },
  ]),
  view("throughput", "Throughput", "rate", [
    {
      key: "transactions",
      label: "Transactions",
      color: "var(--chart-1)",
      kind: "area",
      from: { rate: TRANSACTIONS },
    },
    { key: "queries", label: "Statements", color: "var(--chart-2)", from: { rate: "queries" } },
  ]),
  view("rows", "Rows", "rate", [
    {
      key: "read",
      label: "Read",
      color: "var(--chart-2)",
      kind: "area",
      from: { rate: "rowsRead" },
    },
    { key: "written", label: "Written", color: "var(--chart-5)", from: { rate: "rowsWritten" } },
  ]),
  view("cache", "Cache", "percent", [CACHE_HIT("blocksHit", "blocksRead")]),
]

export const CLICKHOUSE_VIEWS: ChartView[] = [
  view("queries", "Queries", "rate", [
    {
      key: "select",
      label: "Selects",
      color: "var(--chart-1)",
      kind: "area",
      from: { rate: "selectQueries" },
    },
    { key: "insert", label: "Inserts", color: "var(--chart-5)", from: { rate: "insertQueries" } },
    { key: "failed", label: "Failed", color: "var(--chart-3)", from: { rate: "failedQueries" } },
  ]),
  view("rows", "Rows", "rate", [
    {
      key: "inserted",
      label: "Inserted",
      color: "var(--chart-5)",
      kind: "area",
      from: { rate: "insertedRows" },
    },
    { key: "read", label: "Read", color: "var(--chart-2)", from: { rate: "selectedRows" } },
  ]),
  view("memory", "Memory", "bytes", [
    {
      key: "resident",
      label: "Resident",
      color: "var(--chart-4)",
      kind: "area",
      from: { gauge: "memoryResident" },
    },
    {
      key: "tracked",
      label: "Tracked",
      color: "var(--chart-2)",
      from: { gauge: "memoryTracking" },
    },
  ]),
  view("sessions", "Sessions", "count", [
    {
      key: "open",
      label: "Open",
      color: "var(--chart-1)",
      kind: "area",
      from: { gauge: "sessions" },
    },
    {
      key: "running",
      label: "Running queries",
      color: "var(--chart-5)",
      from: { gauge: "runningQueries" },
    },
  ]),
]

export const REDIS_VIEWS: ChartView[] = [
  view("commands", "Commands", "rate", [
    {
      key: "commands",
      label: "Commands",
      color: "var(--chart-1)",
      kind: "area",
      from: { rate: "total_commands_processed" },
    },
  ]),
  view("memory", "Memory", "bytes", [
    {
      key: "used",
      label: "Used",
      color: "var(--chart-1)",
      kind: "area",
      from: { gauge: "used_memory" },
    },
    {
      key: "resident",
      label: "Resident",
      color: "var(--chart-4)",
      from: { gauge: "used_memory_rss" },
    },
  ]),
  view("clients", "Clients", "count", [
    {
      key: "connected",
      label: "Connected",
      color: "var(--chart-2)",
      kind: "area",
      from: { gauge: "connected_clients" },
    },
    {
      key: "blocked",
      label: "Blocked",
      color: "var(--chart-3)",
      from: { gauge: "blocked_clients" },
    },
  ]),
  view("network", "Network", "bytesRate", [
    {
      key: "in",
      label: "Received",
      color: "var(--chart-2)",
      kind: "area",
      from: { rate: "total_net_input_bytes" },
    },
    {
      key: "out",
      label: "Sent",
      color: "var(--chart-5)",
      from: { rate: "total_net_output_bytes" },
    },
  ]),
  view("cache", "Cache", "percent", [CACHE_HIT("keyspace_hits", "keyspace_misses")]),
]

export const MONGO_VIEWS: ChartView[] = [
  view("operations", "Operations", "rate", [
    { key: "query", label: "Queries", color: "var(--chart-1)", from: { rate: "ops.query" } },
    { key: "insert", label: "Inserts", color: "var(--chart-5)", from: { rate: "ops.insert" } },
    { key: "update", label: "Updates", color: "var(--chart-2)", from: { rate: "ops.update" } },
    { key: "delete", label: "Deletes", color: "var(--chart-3)", from: { rate: "ops.delete" } },
    { key: "command", label: "Commands", color: "var(--chart-4)", from: { rate: "ops.command" } },
  ]),
  view("connections", "Connections", "count", [
    {
      key: "open",
      label: "Open",
      color: "var(--chart-2)",
      kind: "area",
      from: { gauge: "connections" },
    },
    {
      key: "active",
      label: "Active",
      color: "var(--chart-5)",
      from: { gauge: "connectionsActive" },
    },
  ]),
  view("cache", "Cache", "percent", [
    {
      key: "used",
      label: "Cache used",
      color: "var(--chart-4)",
      kind: "area",
      from: { gauge: "cachePercent" },
    },
  ]),
  view("network", "Network", "bytesRate", [
    {
      key: "in",
      label: "Received",
      color: "var(--chart-2)",
      kind: "area",
      from: { rate: "network.in" },
    },
    { key: "out", label: "Sent", color: "var(--chart-5)", from: { rate: "network.out" } },
  ]),
]

/**
 * The views an engine can fill: one whose every line is missing from the
 * newest sample — rows on a server that does not count them, the cache on a
 * storage engine without one — is not offered, rather than drawn empty.
 */
export function offeredViews(views: readonly ChartView[], newest: Sample | undefined): ChartView[] {
  if (!newest) return [...views]
  return views.filter((entry) => entry.sources.some((source) => sourced(newest, source)))
}

const PERCENT_DOMAIN: [number, number] = [0, 100]
const PERCENT_TICKS = [0, 25, 50, 75, 100]

/** How a unit's numbers are printed: in the tooltip and the legend, and shorter on the axis. */
export const CHART_UNITS: Record<
  ChartUnit,
  {
    unit?: string
    format?: (value: number) => string
    axisFormat?: (value: number) => string
    domain?: [number, number]
    yTicks?: number[]
  }
> = {
  count: { format: (value) => compact(value) },
  rate: { format: (value) => `${perSecond(value)}/s` },
  bytes: { format: (value) => bytes(value), axisFormat: (value) => bytes(value, 0) },
  bytesRate: { format: (value) => rate(value), axisFormat: (value) => `${bytes(value, 0)}/s` },
  percent: { unit: "%", domain: PERCENT_DOMAIN, yTicks: PERCENT_TICKS },
}
