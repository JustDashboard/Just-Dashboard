"use client"

import type { NetworkLink } from "@/lib/types"
import { observationAge } from "@/lib/network-traffic"
import { ChartActivity } from "@/components/icons"
import { useNow } from "@/components/deploy/vocabulary"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Status } from "@/components/status-dot"
import { linkGlyph, linkProduct } from "@/components/network/marks"
import type { LiveTraffic } from "@/components/network/use-live-traffic"
import { carryingWords } from "./words"

/**
 * What the page is about, on one line: the uplink as the thing it is, what it
 * carries right now in words, how fresh that reading is, and whether the
 * server keeps a history of it.
 *
 * The page opened on a section title and a tile reading "3s old". The age is
 * the state here, because a chart that keeps drawing its last answer looks
 * exactly like a quiet link: past three missed steps the line says stale, in
 * amber, and says why.
 *
 * The line is drawn in every state — reading, read, failed — so nothing under
 * it moves when the first answer lands.
 */
export function TrafficIdentity({
  live,
  links,
  linksError,
  recording,
}: {
  live: LiveTraffic
  links: NetworkLink[] | undefined
  linksError?: Error
  /** Whether the metrics retention keeps any history; undefined until it is known. */
  recording: boolean | undefined
}) {
  const now = useNow(1000) / 1000
  const uplink = links?.find((l) => l.role === "uplink")
  const step = live.stepSeconds ?? 2
  const age = observationAge(live.sampledAt, now, step)
  const carrying = carryingWords(uplink ? live.series[uplink.name]?.at(-1) : undefined)
  const state = live.error ? (
    <Status tone="warning" label="read failed" />
  ) : !live.sampledAt ? (
    <Status tone="unknown" label="reading" />
  ) : age.stale ? (
    <Status tone="warning" label={`stale, ${age.seconds}s old`} />
  ) : (
    <Status state="running" label={`${age.seconds}s old`} />
  )
  return (
    <HostIdentity
      label="About the uplink"
      mark={uplink ? linkProduct(uplink) : undefined}
      fallback={uplink ? linkGlyph(uplink.kind) : ChartActivity}
      title={uplink ? <span className="font-mono">{uplink.name}</span> : "Traffic"}
      state={state}
      facts={
        <>
          <span>
            {uplink
              ? "Uplink"
              : links
                ? "No uplink found"
                : linksError
                  ? "The devices could not be read"
                  : "Reading the devices…"}
          </span>
          {carrying && (
            <>
              <FactDot />
              <span className="numeric text-foreground">{carrying}</span>
            </>
          )}
          <FactDot />
          {live.error ? (
            <span className="text-warning">the last poll failed; figures are the last answer</span>
          ) : age.stale ? (
            <span className="text-warning">the sampler has not stepped; figures are stale</span>
          ) : (
            <span>sampled every {step}s</span>
          )}
          {recording !== undefined && (
            <>
              <FactDot />
              <span>{recording ? "history recorded" : "no history kept"}</span>
            </>
          )}
        </>
      }
    />
  )
}
