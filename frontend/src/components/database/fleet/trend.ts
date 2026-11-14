import type { EngineKind } from "@/components/database/engine"
import {
  historySamples,
  mongoSample,
  redisSample,
  sqlSample,
  type Sample,
} from "@/components/database/home/samples"
import type { Outcome } from "@/components/outcome-strip"
import { bytes, relativeTime } from "@/lib/format"

/**
 * What a card on the control center draws of a database's last hour and of
 * its dumps, worked out apart from the card so it is tested in a millisecond.
 */

/** A recorded snapshot as the history route answers it, whatever the engine. */
export type Snapshot = { at: number; stats: unknown; gap: boolean }

/** How each kind of engine's snapshot becomes a sample, and which gauge counts who is connected. */
const SESSIONS: Partial<
  Record<EngineKind, { sample: (stats: never) => Sample | undefined; gauge: string }>
> = {
  sql: { sample: sqlSample, gauge: "sessions" },
  keyvalue: { sample: redisSample, gauge: "connected_clients" },
  document: { sample: mongoSample, gauge: "connections" },
}

/**
 * The sessions a database had open at each recorded reading, oldest first. A
 * reading that does not say (an account that may not list sessions) is left
 * out rather than drawn as nobody connected.
 */
export function sessionsTrend(kind: EngineKind, snapshots: readonly Snapshot[]): number[] {
  const read = SESSIONS[kind]
  if (!read) return []
  return historySamples(snapshots, read.sample as (stats: unknown) => Sample | undefined).flatMap(
    (sample) => {
      const value = sample.gauges[read.gauge]
      return typeof value === "number" && Number.isFinite(value) ? [value] : []
    },
  )
}

/**
 * The scale a card's line is drawn on: a quarter above its highest reading,
 * so an hour in which nothing changed is a level line across the band and not
 * one pressed against its top, which reads as a meter that is full.
 */
export function trendCeiling(values: readonly number[]): number {
  return Math.max(1, ...values) * 1.25
}

/** One dump as the summary lists it for a strip. */
export type RecentDump = { takenAt: string; size: number }

/** How many of the newest dumps a card has room for. */
export const CARD_DUMPS = 7

/**
 * A database's newest dumps as a strip, oldest first, with one more mark in
 * the brand's blue while a dump is being taken. A dump that is listed is one
 * that was written, so every mark of the past is a success.
 */
export function dumpStrip(
  recent: readonly RecentDump[] | undefined,
  running: boolean,
  most = CARD_DUMPS,
): Outcome[] {
  const strip: Outcome[] = (recent ?? [])
    .slice(0, running ? most - 1 : most)
    .reverse()
    .map((dump) => ({
      key: dump.takenAt,
      tone: "success" as const,
      title: `${relativeTime(dump.takenAt)} · ${bytes(dump.size)}`,
    }))
  if (running) strip.push({ key: "running", tone: "running", title: "A dump is running now" })
  return strip
}
