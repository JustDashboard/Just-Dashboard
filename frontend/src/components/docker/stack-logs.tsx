"use client"

import { useMemo, useState } from "react"
import { get } from "@/lib/api"
import { cn } from "@/lib/utils"
import type { LogSearchResult, StackDetail } from "@/lib/types"
import { stackSource } from "@/lib/log-sources"
import { usePoll } from "@/hooks/use-poll"
import { containerEventsView } from "@/components/docker/container-events"
import { Sparkline } from "@/components/metrics/sparkline"
import { ServiceLogs, type LogAsk, type ServiceLogSource } from "@/components/logs/service-logs"
import { StatusDot } from "@/components/status-dot"
import { ServiceLabel } from "@/components/docker/stack-diff"
import {
  bucketTone,
  serviceLane,
  type ServiceReading,
} from "@/components/docker/stack-service-readings"

/** How far back the strip counts, and how often it counts again. */
const WINDOW_MS = 60 * 60_000
const REFRESH_MS = 60_000

type Activity = {
  lines: Map<string, number>
  errors: Map<string, number>
  shape: Map<string, number[]>
}

/**
 * Every container in the stack, as one log.
 *
 * It was a socket of its own that followed the running services only and
 * wrote `db | ` in front of each line — so a crashed service's last words
 * were never in it, a JSON line stopped being JSON, and the only way to read
 * one service was to type its name into the filter. The stack is a log
 * source now (`stack:<project>`): every container's output merged by time,
 * the exited ones included, each line read through its own container's lens
 * and carrying its service as the lane down the left and as a field to
 * narrow by. Events is what Docker did to them, beside it.
 *
 * Over it, the services that write into it, the way the log page's rail
 * lists its sources: each in its lane, with its state, how much it wrote in
 * the last hour and in what shape, and its errors in red — so the service
 * that is shouting is found before a line is read, and a press narrows the
 * log to it.
 */
export function StackLogs({
  stack,
  readings,
  productOf,
}: {
  stack: StackDetail
  readings: ServiceReading[]
  productOf: (service: string) => string | undefined
}) {
  // As one string: the stack's poll hands a new array every ten seconds.
  const images = stack.services
    .map((service) => service.image)
    .filter(Boolean)
    .join(",")
  const running = stack.services.some((service) => service.state === "running")
  const source = stackSource(stack.name)
  const sources = useMemo<ServiceLogSource[]>(
    () => [
      {
        id: source,
        label: stack.name,
        kind: "stack",
        status: running ? "running" : "exited",
        images: images ? images.split(",") : undefined,
        product: "docker-compose",
      },
    ],
    [source, stack.name, running, images],
  )
  const views = useMemo(() => [containerEventsView({ stack: stack.name })], [stack.name])

  const names = readings.filter((r) => r.bucket !== "missing" || r.orphan).map((r) => r.key)
  const activity = useActivity(source, names.slice(0, 12).join(","))
  const [narrowed, setNarrowed] = useState<{ service: string; key: number }>()
  const ask: (LogAsk & { key: string }) | undefined = narrowed && {
    fields: narrowed.service ? { service: [narrowed.service] } : {},
    key: `${narrowed.service}:${narrowed.key}`,
  }
  const narrow = (service: string) =>
    setNarrowed((prev) => ({ service, key: (prev?.key ?? 0) + 1 }))
  const chosen = narrowed?.service ?? ""

  return (
    <div className="flex h-full min-h-0 min-w-0 animate-rise flex-col gap-4 pb-4">
      <nav aria-label="Services in this log" className="min-w-0">
        {/* A row that scrolls on a phone, where a column of six took the first screen. */}
        <ul className="flex min-w-0 [scrollbar-width:none] gap-1.5 overflow-x-auto sm:grid sm:grid-cols-[repeat(auto-fill,minmax(10rem,1fr))] sm:overflow-visible [&::-webkit-scrollbar]:hidden [&>li]:w-40 [&>li]:shrink-0 sm:[&>li]:w-auto">
          <li>
            <ServiceButton
              pressed={chosen === ""}
              onClick={() => narrow("")}
              label="Every service"
              lines={sum(activity?.lines)}
              errors={sum(activity?.errors)}
            >
              <span className="flex min-w-0 items-center gap-2">
                <span aria-hidden className="flex h-4 gap-px">
                  {names.slice(0, 6).map((name) => (
                    <span
                      key={name}
                      className="w-0.5 rounded-full"
                      style={{ background: serviceLane(name) }}
                    />
                  ))}
                </span>
                <span className="truncate text-body font-medium">Every service</span>
              </span>
            </ServiceButton>
          </li>
          {readings
            .filter((r) => names.includes(r.key))
            .map((reading) => (
              <li key={reading.key}>
                <ServiceButton
                  pressed={chosen === reading.key}
                  onClick={() => narrow(chosen === reading.key ? "" : reading.key)}
                  label={`Only ${reading.key}`}
                  lines={activity?.lines.get(reading.key)}
                  errors={activity?.errors.get(reading.key)}
                  shape={activity?.shape.get(reading.key)}
                  lane={reading.lane}
                >
                  <span className="flex min-w-0 items-center gap-2">
                    <ServiceLabel name={reading.key} product={productOf(reading.key)} />
                    <StatusDot
                      tone={bucketTone(reading.bucket)}
                      live={reading.state === "running"}
                      className="shrink-0"
                    />
                  </span>
                </ServiceButton>
              </li>
            ))}
        </ul>
      </nav>
      <ServiceLogs
        sources={sources}
        storageKey={`docker.stack.${stack.name}.logs`}
        views={views}
        ask={ask}
        readings
        className="min-h-0 flex-1"
        paneClassName="min-h-[30rem]"
      />
    </div>
  )
}

function sum(counts: Map<string, number> | undefined) {
  return counts ? [...counts.values()].reduce((a, b) => a + b, 0) : undefined
}

/**
 * What each service wrote in the last hour: its lines and their shape over
 * the hour, and its errors — two counts the log's own search answers, one
 * faceted and bucketed by service, one of errors faceted by service.
 */
function useActivity(source: string, services: string) {
  const { data } = usePoll<Activity>(
    async (signal) => {
      const since = new Date(Date.now() - WINDOW_MS).toISOString()
      const [all, errors] = await Promise.all([
        get<LogSearchResult>(
          "/logs/search",
          {
            source,
            since,
            facets: "service",
            facetLimit: 50,
            histogramBy: "service",
            histogramValues: services || undefined,
            limit: 1,
          },
          signal,
        ),
        get<LogSearchResult>(
          "/logs/search",
          { source, since, levels: "critical,error", facets: "service", facetLimit: 50, limit: 1 },
          signal,
        ),
      ])
      const counts = (result: LogSearchResult) =>
        new Map((result.facets?.service?.values ?? []).map((v) => [v.value, v.count]))
      const shape = new Map(
        services
          .split(",")
          .filter(Boolean)
          .map((name) => [name, (all.histogram ?? []).map((b) => b.counts?.[name] ?? 0)]),
      )
      return { lines: counts(all), errors: counts(errors), shape }
    },
    REFRESH_MS,
    [source, services],
  )
  return data
}

function ServiceButton({
  pressed,
  onClick,
  label,
  lines,
  errors,
  shape,
  lane,
  children,
}: {
  pressed: boolean
  onClick: () => void
  label: string
  lines?: number
  errors?: number
  shape?: number[]
  lane?: string
  children: React.ReactNode
}) {
  return (
    <button
      type="button"
      aria-pressed={pressed}
      aria-label={label}
      onClick={onClick}
      className={cn(
        "flex h-full w-full min-w-0 flex-col gap-1 rounded-lg border px-2.5 py-2 text-left focus-ring transition-colors",
        pressed
          ? "border-border-strong bg-accent"
          : "border-hairline bg-card hover:border-border-strong",
      )}
    >
      {children}
      <span className="flex min-w-0 items-end justify-between gap-2">
        <span className="numeric flex min-w-0 items-baseline gap-2 text-hint text-muted-foreground">
          <span>{lines === undefined ? "—" : `${lines.toLocaleString()} lines`}</span>
          {errors !== undefined && errors > 0 && (
            <span className="font-medium text-destructive">
              {errors.toLocaleString()} {errors === 1 ? "error" : "errors"}
            </span>
          )}
        </span>
        {shape && shape.some((v) => v > 0) && (
          <Sparkline
            values={shape}
            width={44}
            height={14}
            color={lane}
            label={`${label}: lines over the last hour`}
            className="shrink-0"
          />
        )}
      </span>
    </button>
  )
}
