import { isRecordedWindow, writeSpan, type RecordedWindow, type TrafficSpan } from "./span"

/**
 * The Traffic page's addresses. The page was one scroll of four subjects with
 * its window, its sheets and its filters in component state; each is in the
 * address now, so any of them can be linked, reloaded and left with Back.
 */
export const TRAFFIC_TABS = [
  { key: "bandwidth", label: "Bandwidth" },
  { key: "budgets", label: "Budgets" },
  { key: "shaping", label: "Shaping" },
  { key: "ebpf", label: "eBPF programs" },
] as const
export type TrafficTab = (typeof TRAFFIC_TABS)[number]["key"]

type Query = { get: (name: string) => string | null }

/** The view an address asks for; an unknown one is the page's first. */
export function tabFromParams(params: Query): TrafficTab {
  const asked = params.get("tab")
  return TRAFFIC_TABS.find((tab) => tab.key === asked)?.key ?? "bandwidth"
}

/** The page on a view, with the window and the program filter it should keep. */
export function trafficHref({
  tab = "bandwidth",
  span = "live",
  type,
}: {
  tab?: TrafficTab
  span?: TrafficSpan
  /** The eBPF program type the list is narrowed to. */
  type?: string
} = {}): string {
  const params = new URLSearchParams()
  if (tab !== "bandwidth") params.set("tab", tab)
  writeSpan(params, span)
  if (type) params.set("type", type)
  const query = params.toString()
  return query ? `/network/traffic?${query}` : "/network/traffic"
}

/** The window a container's page reads: the hour, unless the address names a longer one. */
export function containerWindow(params: Query): RecordedWindow {
  const asked = params.get("window")
  return isRecordedWindow(asked) ? asked : "1h"
}

/** One container's traffic over a window. */
export function containerHref(name: string, window: RecordedWindow = "1h"): string {
  const path = `/network/traffic/containers/${encodeURIComponent(name)}`
  return window === "1h" ? path : `${path}?window=${window}`
}

/** One loaded eBPF program, with the type its list was narrowed to. */
export function programHref(id: number, type?: string): string {
  const path = `/network/traffic/ebpf/${id}`
  return type ? `${path}?type=${encodeURIComponent(type)}` : path
}

/** A program's id from its address, or null where it is not one. */
export function programId(segment: string | undefined): number | null {
  return segment && /^\d{1,10}$/.test(segment) ? Number(segment) : null
}
