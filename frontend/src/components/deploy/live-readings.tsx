"use client"

import Link from "next/link"
import { CheckCircle, Cpu, Globe, Layers, Warning } from "@/components/icons"
import { get } from "@/lib/api"
import { bytes, percent } from "@/lib/format"
import { perMinute } from "@/lib/requests"
import { useContainerLive } from "@/hooks/use-container-live"
import { usePoll } from "@/hooks/use-poll"
import type { ContainerHistory, TrafficPulse } from "@/lib/types"
import { utilisationTone } from "@/components/meter"
import { StatusDot } from "@/components/status-dot"
import { TileTrend } from "@/components/metrics/sparkline"
import { HUE } from "@/components/overview/readings"
import { NumberTicker } from "@/components/ui/number-ticker"
import { WireMark } from "@/components/deploy/wire"
import { failingTone } from "@/components/deploy/fleet"

/**
 * The four readings under Production, as sentences on the wiring's own marks:
 * a tinted glyph, what the reading is, what it says in words, and its last hour
 * at the line's end. They were four `StatTile`s, a figure with a unit and a
 * caption the reader had to put together ("0.1 %", "100% is one core"), under a
 * picture that says everything else in words. Each line is a way to the page
 * that has the rest, and every state of a line is the same height, so a reading
 * landing moves nothing under it.
 */
function ReadingLine({
  href,
  label,
  mark,
  name,
  children,
  hint,
  trend,
}: {
  href: string
  /** Where the line goes, as its accessible name. */
  label: string
  mark: React.ReactNode
  name: React.ReactNode
  children: React.ReactNode
  hint: React.ReactNode
  trend?: React.ReactNode
}) {
  return (
    <Link
      href={href}
      aria-label={label}
      data-slot="reading-line"
      className="flex min-w-0 items-center gap-3.5 rounded-lg px-2 py-2.5 focus-ring transition-colors hover:bg-row-hover"
    >
      {mark}
      <span className="block min-w-0 flex-1">
        <span className="eyebrow flex items-center gap-1.5">{name}</span>
        <span className="block truncate text-body leading-snug font-medium">{children}</span>
        <span className="block truncate text-hint leading-snug text-muted-foreground">{hint}</span>
      </span>
      <span className="hidden w-32 shrink-0 sm:block 2xl:w-48">{trend}</span>
    </Link>
  )
}

function NoReading({ children }: { children: React.ReactNode }) {
  return <span className="text-muted-foreground">{children}</span>
}

/**
 * The last hour at the ingress, from the record the server holds, content with
 * a reading a minute old. With no record yet the lines stay and say so. The
 * failing share takes the fleet's colour for it, so one share is one colour on
 * every deploy page.
 */
export function TrafficReadings({ projectId }: { projectId: number }) {
  const pulse = usePoll<Record<string, TrafficPulse>>(
    (signal) => get<Record<string, TrafficPulse>>("/deploy/traffic", undefined, signal),
    30000,
    [],
    { key: "/deploy/traffic" },
  )
  const mine = pulse.data?.[String(projectId)]
  const ready = mine?.status === "available" ? mine : undefined
  const waiting = pulse.data ? "No requests recorded yet" : "Reading the request record…"
  const base = `/deploy/${projectId}/logs`
  const tone = ready ? failingTone(ready.errorRate) : "default"
  const rate = Number(ready?.perMinute ? perMinute(ready.perMinute) : 0)
  return (
    <>
      <ReadingLine
        href={base}
        label="Requests on Logs"
        mark={
          <WireMark size="md" hue={HUE.net}>
            <Globe aria-hidden />
          </WireMark>
        }
        name="Traffic"
        hint={ready ? `${ready.pages.toLocaleString()} page views in the last hour` : waiting}
        trend={
          ready && (
            <TileTrend
              values={ready.points}
              label="Requests per minute, last hour"
              color={HUE.net}
            />
          )
        }
      >
        {ready ? (
          <>
            <NumberTicker
              key="requests"
              value={rate}
              decimalPlaces={rate < 10 ? 2 : rate < 100 ? 1 : 0}
            />{" "}
            {rate === 1 ? "request" : "requests"} a minute
          </>
        ) : (
          <NoReading>No reading yet</NoReading>
        )}
      </ReadingLine>
      <ReadingLine
        href={`${base}?view=insights`}
        label="Failing requests on Logs"
        mark={
          ready ? (
            <WireMark
              size="md"
              tone={tone === "default" ? "success" : tone === "danger" ? "danger" : "warning"}
            >
              {tone === "default" ? <CheckCircle aria-hidden /> : <Warning aria-hidden />}
            </WireMark>
          ) : (
            <WireMark size="md">
              <CheckCircle aria-hidden className="text-muted-foreground" />
            </WireMark>
          )
        }
        name="Errors"
        hint="Answered with a 5xx in the last hour"
      >
        {ready ? failingWords(ready.errorRate) : <NoReading>No reading yet</NoReading>}
      </ReadingLine>
    </>
  )
}

function failingWords(rate: number) {
  if (rate <= 0) return "No requests failed"
  const share = rate * 100
  return share < 0.1 ? "Under 0.1% of requests failed" : `${percent(share, 1)} of requests failed`
}

/**
 * What the container is using, from the live stats socket — the reading
 * Docker's own page shows — with the last hour from the recorded history at the
 * line's end. With no frame yet the last recorded minute is the figure, said as
 * that and never drawn as live (§11). The socket is `useContainerLive`'s, so
 * the minutes read here are already on Runtime's live charts when the reader
 * follows the line.
 */
export function UsageReadings({
  containerId,
  name,
  reason,
  href,
}: {
  containerId?: string
  /** The service the container is, said under the processor reading. */
  name?: string
  /** Why there is no container to read. */
  reason?: string
  href: string
}) {
  const feed = useContainerLive(containerId)
  const history = usePoll(
    (signal) =>
      get<ContainerHistory>(
        `/docker/containers/${encodeURIComponent(containerId ?? "")}/stats/history`,
        { points: 60 },
        signal,
      ),
    60000,
    [containerId],
    { enabled: Boolean(containerId) },
  )
  const stats = feed.stats
  const points = history.data?.points ?? []
  const recorded = stats ? undefined : points.at(-1)

  const streamedCpu = stats?.cpuReady ? stats.cpuPercent : undefined
  const cpuShare =
    streamedCpu !== undefined && stats?.cpuLimit ? streamedCpu / stats.cpuLimit : undefined
  const cpu = streamedCpu ?? recorded?.cpu ?? undefined
  const memory = stats?.memUsage ?? recorded?.memBytes
  const mib = memory !== undefined ? memory / 1024 / 1024 : 0
  const gib = mib >= 1024
  const memoryShare = stats?.memLimited ? stats.memPercent : undefined
  const mark = (share: number | undefined, hue: string, glyph: React.ReactNode) => {
    const tone = share === undefined ? "default" : utilisationTone(share)
    return tone === "default" ? (
      <WireMark size="md" hue={hue}>
        {glyph}
      </WireMark>
    ) : (
      <WireMark size="md" tone={tone === "danger" ? "danger" : "warning"}>
        {glyph}
      </WireMark>
    )
  }

  return (
    <>
      <ReadingLine
        href={href}
        label="CPU on Runtime"
        mark={mark(cpuShare, HUE.cpu, <Cpu aria-hidden />)}
        name={
          <>
            CPU
            {feed.live && <StatusDot tone="running" live />}
          </>
        }
        hint={
          !containerId
            ? reason
            : recorded
              ? "Last minute, not live"
              : cpuShare !== undefined
                ? `${percent(cpuShare)} of its limit`
                : name
        }
        trend={
          cpuShare === undefined && (
            <TileTrend
              values={points.flatMap((point) => (point.cpuPeak === null ? [] : [point.cpuPeak]))}
              label="CPU over the last hour"
              color={HUE.cpu}
            />
          )
        }
      >
        {cpu === undefined ? (
          <NoReading>No reading yet</NoReading>
        ) : cpu >= 100 ? (
          <span className="inline-block animate-rise">
            <NumberTicker value={cpu / 100} decimalPlaces={1} /> cores busy
          </span>
        ) : (
          <span className="inline-block animate-rise">
            <NumberTicker value={cpu} decimalPlaces={1} />% of one core
          </span>
        )}
      </ReadingLine>
      <ReadingLine
        href={href}
        label="Memory on Runtime"
        mark={mark(memoryShare, HUE.mem, <Layers aria-hidden />)}
        name="Memory"
        hint={
          !containerId
            ? "Starts with the container"
            : !stats
              ? recorded
                ? "Last minute, not live"
                : "Waiting for Docker's first reading"
              : stats.memLimited
                ? `${percent(stats.memPercent)} of its ${bytes(stats.memLimit)} limit`
                : stats.memHostPercent !== undefined
                  ? `No limit, ${percent(stats.memHostPercent)} of the host`
                  : "No limit"
        }
        trend={
          memoryShare === undefined && (
            <TileTrend
              values={points.map((point) => point.memBytesPeak)}
              label="Memory over the last hour"
              color={HUE.mem}
            />
          )
        }
      >
        {memory === undefined ? (
          <NoReading>No reading yet</NoReading>
        ) : (
          <span className="inline-block animate-rise">
            {/* Keyed by the unit, so crossing a gibibyte counts up in the new
                unit rather than springing from 1,023 down to 1.00. */}
            <NumberTicker
              key={gib ? "gib" : "mib"}
              value={gib ? mib / 1024 : mib}
              decimalPlaces={gib ? 2 : 0}
            />{" "}
            {gib ? "GB" : "MB"} in use
          </span>
        )}
      </ReadingLine>
    </>
  )
}
