"use client"

import { useEffect, useState } from "react"
import { errorMessage, get } from "@/lib/api"
import { plural, relativeTime } from "@/lib/format"
import { fieldPredicates } from "@/lib/log-filter"
import { eventMeta } from "@/lib/log-lenses"
import type { LogFacet, LogFacetValue, LogSearchResult } from "@/lib/types"
import { cn } from "@/lib/utils"
import { EVENT_WORD } from "@/components/logs/log-text"
import { BarList } from "@/components/bar-list"
import { Facet } from "@/components/deploy/traffic-facets"

/** The ways an application fails to start, as the app lens names them. */
const STARTUP_FAILURES = ["port_in_use", "env_missing", "db_unreachable", "schema_missing"]

/** How many of each list are drawn. */
const SHOWN = 8

type Answer = { exceptions: LogFacet | undefined; startup: LogFacet | undefined; lens?: string }

/**
 * What the containers wrote over the window the requests above are read
 * over, ranked: the exceptions it threw by what they say — the class and the
 * first line, so a thousand of one error are one row — and the ways it failed
 * to start. The Output view's quick views name the same lines; this is where
 * a burst of 5xx on the chart meets the exception behind it, on one Insights
 * rather than two.
 *
 * A row opens Output on the minute around the last time it was written, on
 * the container that wrote it, which is the line a reader wants next.
 */
export function OutputInsights({
  sourceId,
  label,
  window,
  onOpen,
}: {
  /** The live container, or the live release's stack as a whole. */
  sourceId: string
  /** What that is, in words, for the section's reading. */
  label: string
  window: { since?: string; until?: string }
  /** Output around an instant, on the container a line's attrs name (a stack's short id). */
  onOpen: (at: string, container?: string) => void
}) {
  const [state, setState] = useState<{ key: string; answer?: Answer; error?: string }>()
  // A preset window is resolved again by every poll of the requests above,
  // ten seconds apart; the containers' logs are read again once a minute.
  const since = window.since ? minuteOf(window.since) : undefined
  const until = window.until
  const key = JSON.stringify({ sourceId, since, until })

  useEffect(() => {
    const controller = new AbortController()
    const common = { source: sourceId, since, until, limit: 1, facetLimit: SHOWN }
    Promise.all([
      get<LogSearchResult>(
        "/logs/search",
        {
          ...common,
          f: fieldPredicates({ event: ["exception"] }),
          facets: "error",
          sample: "component,container",
        },
        controller.signal,
      ),
      get<LogSearchResult>(
        "/logs/search",
        {
          ...common,
          f: fieldPredicates({ event: STARTUP_FAILURES }),
          facets: "event",
          sample: "error,container",
        },
        controller.signal,
      ),
    ]).then(
      ([exceptions, startup]) =>
        setState({
          key,
          answer: {
            exceptions: exceptions.facets?.error,
            startup: startup.facets?.event,
            lens: exceptions.lens ?? startup.lens,
          },
        }),
      (err) => {
        if (!controller.signal.aborted) setState({ key, error: errorMessage(err) })
      },
    )
    return () => controller.abort()
    // The key is the question: the source and the window's two instants.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key])

  const current = state?.key === key ? state : undefined
  const answer = current?.answer
  const exceptions = answer?.exceptions?.values ?? []
  const startup = answer?.startup?.values ?? []
  const open = (value: LogFacetValue) =>
    value.last ? () => onOpen(value.last!, value.samples?.container) : undefined

  return (
    <section aria-label="Output" className="flex flex-col gap-6 border-t border-hairline px-5 py-5">
      <div className="flex min-w-0 items-baseline justify-between gap-3">
        <h3 className="text-title font-semibold">Output</h3>
        <span className="truncate text-hint text-muted-foreground">
          what {label} wrote over this window
        </span>
      </div>
      {current?.error ? (
        <p className="text-hint text-muted-foreground">
          The containers&apos; output could not be read: {current.error}
        </p>
      ) : !answer ? (
        <p className="text-hint text-muted-foreground">Reading what the containers wrote…</p>
      ) : exceptions.length === 0 && startup.length === 0 ? (
        <p className="text-hint text-muted-foreground">
          No exception and no failed start in what {label} wrote over this window.
        </p>
      ) : (
        <div className="grid grid-cols-1 gap-x-10 gap-y-8 lg:grid-cols-2">
          <Facet title="Exceptions" reading={tally(answer.exceptions)} index={1}>
            <BarList
              items={exceptions.slice(0, SHOWN).map((value) => ({
                key: value.value,
                label: value.value,
                value: value.count.toLocaleString(),
                share: value.count / peak(exceptions),
                hint: seen(value, value.samples?.component),
                title: "Read the output around the last time this was thrown",
                onClick: open(value),
              }))}
              emptyLabel="Nothing thrown in this window."
            />
          </Facet>
          <Facet title="Startup failures" reading={tally(answer.startup)} index={2}>
            <BarList
              items={startup.slice(0, SHOWN).map((value) => {
                const meta = eventMeta(answer.lens ?? "app", value.value)
                return {
                  key: value.value,
                  mono: false,
                  label: (
                    <span className={cn(EVENT_WORD[meta?.tone ?? "danger"])}>
                      {meta?.label ?? value.value.replace(/_/g, " ")}
                    </span>
                  ),
                  value: value.count.toLocaleString(),
                  share: value.count / peak(startup),
                  hint: seen(value, value.samples?.error),
                  title: "Read the output around the last time it failed to start",
                  onClick: open(value),
                }
              })}
              emptyLabel="It started cleanly every time in this window."
            />
          </Facet>
        </div>
      )}
    </section>
  )
}

function minuteOf(instant: string) {
  const at = Date.parse(instant)
  return Number.isFinite(at) ? new Date(at - (at % 60_000)).toISOString() : instant
}

function tally(facet: LogFacet | undefined) {
  if (!facet || facet.distinct === 0) return undefined
  return facet.distinct > SHOWN
    ? `top ${SHOWN} of ${facet.distinct.toLocaleString()}`
    : plural(facet.distinct, "kind")
}

function peak(values: LogFacetValue[]) {
  return Math.max(...values.map((value) => value.count), 1)
}

/** When it last happened, and the one detail that says where. */
function seen(value: LogFacetValue, detail: string | undefined) {
  const when = value.last ? `last ${relativeTime(value.last)}` : undefined
  return [detail, when].filter(Boolean).join(" · ") || undefined
}
