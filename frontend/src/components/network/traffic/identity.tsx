"use client"

import type { NetworkLink } from "@/lib/types"
import { observationAge } from "@/lib/network-traffic"
import { ChartActivity } from "@/components/icons"
import { useNow } from "@/components/deploy/vocabulary"
import { HostIdentity } from "@/components/metrics/host-identity"
import { Status } from "@/components/status-dot"
import { linkGlyph, linkProduct } from "@/components/network/marks"
import type { LiveTraffic } from "@/components/network/use-live-traffic"
import { FactGroups } from "./facts"
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
  className,
}: {
  live: LiveTraffic
  links: NetworkLink[] | undefined
  linksError?: Error
  /** Whether the metrics retention keeps any history; undefined until it is known. */
  recording: boolean | undefined
  className?: string
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
    <Status tone="warning" label={`stale, read ${age.seconds}s ago`} />
  ) : (
    <Status state="running" label={`read ${age.seconds}s ago`} />
  )
  return (
    <HostIdentity
      label="About the uplink"
      className={className}
      mark={uplink ? linkProduct(uplink) : undefined}
      fallback={uplink ? linkGlyph(uplink.kind) : ChartActivity}
      title={uplink ? <span className="font-mono">{uplink.name}</span> : "Traffic"}
      state={state}
      facts={
        <FactGroups
          groups={[
            [
              <span key="what">
                {uplink
                  ? "Uplink"
                  : links
                    ? "No uplink found"
                    : linksError
                      ? "The devices could not be read"
                      : "Reading the devices…"}
              </span>,
              carrying && (
                <span key="carrying" className="numeric text-foreground">
                  {carrying}
                </span>
              ),
            ],
            [
              live.error ? (
                <span key="fresh" className="text-warning">
                  the last poll failed; figures are the last answer
                </span>
              ) : age.stale ? (
                <span key="fresh" className="text-warning">
                  the sampler has not stepped; figures are stale
                </span>
              ) : live.sampledAt === undefined ? (
                // Said before the first answer too, so the line is the
                // height it will be: on a phone this group is its second row.
                <span key="fresh">waiting for the first reading</span>
              ) : (
                <span key="fresh">sampled every {step}s</span>
              ),
              recording !== undefined && (
                <span key="history">{recording ? "history recorded" : "no history kept"}</span>
              ),
            ],
          ]}
        />
      }
    />
  )
}
