import { bytes, plural, rate, relativeTime } from "@/lib/format"
import { LATENCY_SHARE, latencyWords } from "@/lib/latency-words"
import {
  millis,
  packetRate,
  retransmitShare,
  shareLabel,
  type InterfaceQuota,
  type Percentiles,
  type TCPLatency,
  type TCPPoint,
} from "@/lib/network-traffic"
import type { EBPFView, ShapeDevice } from "@/lib/types"
import type { DotTone } from "@/components/status-dot"
import { formatKbit } from "./shaping-math"

/**
 * The Traffic page's readings as the sentences it prints. They were figures
 * on tiles ("3.0%" over "of segments in the last minute"), and a tile leaves
 * the reader to join the two; here each is joined once, and tested as words.
 */

/** What a device carries right now: "2.6 MB/s in, 1.0 MB/s out". */
export function carryingWords(point: { rx: number; tx: number } | undefined): string | undefined {
  if (!point) return undefined
  return `${rate(point.rx)} in, ${rate(point.tx)} out`
}

/** Packets a second on a device, both directions, and each apart. */
export function packetWords(point: { rxp?: number; txp?: number } | undefined): {
  reading?: string
  hint: string
} {
  if (point?.rxp === undefined) return { hint: "no uplink reading yet" }
  return {
    reading: `${packetRate(point.rxp + (point.txp ?? 0)).replace("/s", "")} packets a second`,
    hint: `${packetRate(point.rxp)} in · ${packetRate(point.txp)} out`,
  }
}

/** Past this share of segments sent again, TCP is struggling and the line says so in amber. */
export const RESENT_WARNING = 0.02

/**
 * TCP's health over the last minute as one sentence and one line of detail:
 * the share of segments sent again and the round trip half of them came back
 * inside, then the rest. The round trip is in the product's words for
 * latency (`lib/latency-words.ts`), never as a median or a percentile.
 */
export function tcpWords({
  tcp,
  tcpError,
  latency,
  stepSeconds = 2,
}: {
  tcp: TCPPoint[]
  tcpError?: string
  latency?: TCPLatency
  stepSeconds?: number
}): { reading?: string; hint: string; warning: boolean } {
  if (tcpError) return { hint: `TCP counters unreadable: ${tcpError}`, warning: true }
  const share = retransmitShare(tcp, 30)
  const resets = Math.round(tcp.slice(-30).reduce((n, p) => n + p.resets, 0) * stepSeconds)
  const timed = latency && !latency.error && latency.sockets > 0 ? latency : undefined
  // What is missing is said where the reading would be: the line is short on
  // a phone, and its first words are the ones that are never cut.
  const untimed = !latency
    ? "round trip being measured"
    : latency.error
      ? latency.error
      : "no non-loopback TCP socket to time"
  const hint = [
    timed ? latencyWords("p90", millis(timed.p90Ms)) : untimed,
    timed && plural(timed.sockets, "socket"),
    share !== undefined && plural(resets, "reset"),
    timed && `read ${relativeTime(timed.at)}`,
  ]
    .filter(Boolean)
    .join(" · ")
  if (share === undefined) return { hint, warning: false }
  return {
    reading: [
      `${shareLabel(share)} resent`,
      timed && `round trips ${latencyWords("p50", millis(timed.medianMs))}`,
    ]
      .filter(Boolean)
      .join(" · "),
    hint,
    warning: share > RESENT_WARNING,
  }
}

/**
 * A recorded window's throughput spread in words. These are percentiles of
 * how much moved, not of how long anything took, but "p95" asks the same
 * learning of the reader either way: the shares are said as the latency
 * figures say theirs.
 */
export function throughputWords(p: Percentiles): string {
  const pair = (rx: number, tx: number) => `↓ ${rate(rx)} ↑ ${rate(tx)}`
  return [
    `${LATENCY_SHARE.p50} of the time under ${pair(p.rxP50, p.txP50)}`,
    `${LATENCY_SHARE.p95.toLowerCase()} under ${pair(p.rxP95, p.txP95)}`,
    `${LATENCY_SHARE.p99.toLowerCase()} under ${pair(p.rxP99, p.txP99)}`,
    `over ${p.samples} ${p.basisSeconds}s intervals`,
  ].join(" · ")
}

/** The line a chart draws at the rate a direction stayed under 19 times in 20. */
export function thresholdWords(direction: "in" | "out", value: number): string {
  return `${direction} ${LATENCY_SHARE.p95.toLowerCase()} under ${rate(value)}`
}

const DIRECTION_WORD: Record<InterfaceQuota["direction"], string> = {
  both: "in and out",
  rx: "in",
  tx: "out",
}

/** A budget as its sentence and the line under its band. */
export function budgetWords(q: InterfaceQuota): { reading: string; hint: string } {
  const recorded = `${Math.round(q.coverage * 100)}% of the ${q.period} so far was recorded`
  const short = q.retentionShort
    ? " · the retention keeps less than the period, so its start is gone"
    : ""
  if (q.state === "unmeasured") {
    return {
      reading: `Not measured against ${bytes(q.limitBytes)} ${DIRECTION_WORD[q.direction]} a ${q.period}`,
      hint: `The metrics retention keeps nothing · ${recorded}${short}`,
    }
  }
  const estimated =
    q.estimatedBytes > 0 ? ` · ${bytes(q.estimatedBytes)} estimated from older rows` : ""
  return {
    reading: `${bytes(q.usedBytes + q.estimatedBytes)} of ${bytes(q.limitBytes)} ${DIRECTION_WORD[q.direction]} this ${q.period}`,
    hint: `${recorded}${estimated}${short}`,
  }
}

/** A limit on a queue's card: "18 Mbit/s", or "no limit". */
export function limitWords(kbit: number): string {
  return kbit > 0 ? formatKbit(kbit) : "no limit"
}

/**
 * What a queue's card says of the device, worst first: a foreign queue tree
 * that refuses changes, saved limits the kernel no longer holds, limits that
 * could not be checked, limits in force, and a device nobody shaped.
 */
export function queueState(device: ShapeDevice): { label: string; tone: DotTone } {
  if (!device.shapeable) return { label: "cannot be shaped", tone: "unknown" }
  if (device.ownership?.verdict === "refused" && !device.managed) {
    return { label: "changes refused", tone: "warning" }
  }
  switch (device.verification?.status) {
    case "drift":
      return { label: "differs from saved limits", tone: "warning" }
    case "unknown":
      return { label: "could not be verified", tone: "notice" }
    case "verified":
      return { label: "matches saved limits", tone: "running" }
  }
  if (device.managed) return { label: "shaped", tone: "running" }
  return { label: "not shaped", tone: "unknown" }
}

/** The mono line under a queue's name: the queue it runs and what the device is to this connection. */
export function queueLine(device: ShapeDevice): string {
  return [
    device.root?.kind ?? "no queue",
    device.uplink && "uplink",
    device.clientPath && "carries your connection",
    device.sqm && "download SQM",
  ]
    .filter(Boolean)
    .join(" · ")
}

/** The eBPF inventory's figures as one line: what the four tiles said. */
export function ebpfWords(view: EBPFView): string {
  const memlock = view.programs.reduce((n, p) => n + p.memlock, 0)
  const devices = [...new Set(view.attachments.map((a) => a.device))]
  return [
    `${plural(view.total, "program")} of ${plural(view.byType.length, "type")}`,
    `${bytes(memlock)} of locked memory`,
    devices.length > 0
      ? `${plural(view.attachments.length, "attachment")} on ${devices.join(", ")}`
      : "attached to no network device",
    view.cgroupAttachments === undefined
      ? "the cgroup tree could not be read"
      : `${plural(view.cgroupAttachments, "cgroup attachment")}`,
  ].join(" · ")
}
