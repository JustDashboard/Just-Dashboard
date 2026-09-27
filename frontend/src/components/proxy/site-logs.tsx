"use client"

import { useMemo, useState } from "react"
import { ClockRewind, MagnifyingGlassMinus } from "@/components/icons"
import { errorMessage, get } from "@/lib/api"
import { plural } from "@/lib/format"
import type { DeploymentRequests, LogLine, LogSearchResult, RequestEntry } from "@/lib/types"
import { EMPTY_FILTER, filterQuery, type LogLevel } from "@/lib/log-filter"
import { lensFor } from "@/lib/log-lenses"
import { useLogView } from "@/lib/log-view"
import { useSessionState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { useMediaQuery } from "@/hooks/use-mobile"
import type { LogFields } from "@/components/logs/types"
import {
  ServiceLogs,
  type LogWindow,
  type ServiceLogsContext,
  type ServiceLogsView,
} from "@/components/logs/service-logs"
import { ReadingTile, useLensReadings } from "@/components/logs/lens-readings"
import { LineDetail } from "@/components/logs/line-detail"
import { LogConsole, LogRow, eventColumnFor } from "@/components/logs/log-console"
import { EmptyState, ErrorState } from "@/components/state"
import { StatButton, StatGrid, StatTile } from "@/components/stat-tile"
import { ChipCount, FilterChip } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { TileTrend } from "@/components/metrics/sparkline"
import {
  EMPTY_REQUEST_QUERY,
  RequestsWorkspace,
  type RequestQuery,
} from "@/components/deploy/requests-workspace"
import type { StatusClass } from "@/lib/requests"
import type { SiteErrorLog, SiteLogPlan } from "@/components/proxy/site-log-plan"

/** What a failure in the error log is: an error, or the warnings nginx writes beside one. */
const FAILURES: LogLevel[] = ["critical", "error", "warn"]

/** The Errors view's window: the last day, as the readings' figures are the last hour. */
const ERRORS_WINDOW = 24 * 3_600_000

/** The most lines the Errors view reads of one kind: the newest, as a search keeps them. */
const ERRORS_LIMIT = 1000

/** A failed request's own lines: this far either side of the second it was answered in. */
const AROUND_REQUEST = 1_000

/** The error log opened on a failed request: a minute either side, to see what led to it. */
const AROUND_OPEN = 60_000

const iso = (ms: number) => new Date(ms).toISOString()

/** What the log a site's failures are read from is called: Caddy writes one log, not an error log. */
const logName = (errors: SiteErrorLog) =>
  errors.lens === "caddy" ? "Caddy's log" : "the error log"

/** Whether a log other sites share is narrowed to this one's names at all. */
const narrowed = (errors: SiteErrorLog) => Object.keys(errors.fields).length > 0

/**
 * A site's logs, on the site's page: what it served and what went wrong, as
 * the readings over its last hour and the service logs every page embeds.
 *
 * The two views are the site's own. **Requests** is its request record —
 * the chart, the families, the rows opening in place — which is the
 * deployment Logs page's workspace over the site's route, so a site and a
 * deployment are read the same way. A failed request shows, in its opened
 * row, what the proxy wrote in its error log in the second around it, which
 * is where "502" turns into "the application refused the connection".
 * **Errors** is that error log's failures over the last day, by kind.
 * Live, History and Insights read the log in the strip, which each view sets
 * to the one it is about, so the name beside the tabs is always the log the
 * lines under it came from.
 *
 * The readings above are the request record's and, where the site has an
 * error log of its own, its upstream failures: four figures a press away
 * from the rows behind them.
 */
export function SiteLogs({
  name,
  plan,
  engine,
}: {
  name: string
  plan: SiteLogPlan
  /** The engine's name, for what the error lines are said to be: "nginx said". */
  engine: string
}) {
  const base = `/proxy/sites/${encodeURIComponent(name)}`
  const errors = plan.errors
  const [view, setView] = useState<string | null>(
    plan.requests ? "requests" : errors ? "errors" : null,
  )
  const [source, setSource] = useState<string | null>(plan.requests ?? errors?.source ?? null)
  const [query, setQuery] = useSessionState<RequestQuery>(
    `proxy.site.${name}.requests`,
    EMPTY_REQUEST_QUERY,
  )
  const [kind, setKind] = useState("")
  const [around, setAround] = useState<LogWindow | undefined>()

  // Each view reads one log, and the strip names it: Requests beside the
  // access log, Errors beside the error log. The minute around a failed
  // request is History's, and goes when the reader does, or the strip would
  // go on naming it over the rows of another view.
  const changeView = (id: string) => {
    setView(id)
    if (id !== "search") setAround(undefined)
    if (id === "requests" && plan.requests) setSource(plan.requests)
    if (id === "errors" && errors) setSource(errors.source)
  }
  // Another log picked under a view about a different one is a question
  // about that log, which Live answers.
  const changeSource = (id: string) => {
    setSource(id)
    setAround(undefined)
    if (
      (view === "requests" && id !== plan.requests) ||
      (view === "errors" && id !== errors?.source)
    )
      setView("live")
  }
  const openAround = (entry: RequestEntry) => {
    if (!errors) return
    const at = Date.parse(entry.time)
    if (!Number.isFinite(at)) return
    setSource(errors.source)
    setView("search")
    setAround({
      since: iso(at - AROUND_OPEN),
      until: iso(at + AROUND_OPEN),
      label: "Around a failed request",
    })
  }

  const views: ServiceLogsView[] = []
  if (plan.requests) {
    views.push({
      id: "requests",
      label: "Requests",
      render: () => (
        <RequestsWorkspace
          base={base}
          subject={`site ${name}`}
          emptyTitle="No request record for this site"
          view="requests"
          query={query}
          onQueryChange={setQuery}
          markers={[]}
          renderInline={
            errors
              ? (entry) =>
                  entry.status >= 500 ? (
                    <FailedRequestLines
                      entry={entry}
                      errors={errors}
                      engine={engine}
                      onOpen={() => openAround(entry)}
                    />
                  ) : undefined
              : undefined
          }
        />
      ),
    })
  }
  if (errors) {
    views.push({
      id: "errors",
      label: "Errors",
      render: (ctx) => (
        <SiteErrors ctx={ctx} errors={errors} engine={engine} kind={kind} onKindChange={setKind} />
      ),
    })
  }

  const upstreamOn = view === "errors" && kind === "upstream"
  const showRequests = (classes: StatusClass[]) => {
    setQuery({ ...query, classes })
    changeView("requests")
  }

  return (
    <div className="flex min-w-0 flex-col gap-6">
      {plan.requests && (
        <SiteReadings
          base={base}
          errors={errors && !errors.shared ? errors : undefined}
          requestsOn={view === "requests"}
          classes={query.classes}
          upstreamOn={upstreamOn}
          onRequests={showRequests}
          onUpstream={() => {
            // Pressed again, the figure lets go of the lines it narrowed to,
            // as every reading does.
            if (upstreamOn) {
              setKind("")
              return
            }
            setKind("upstream")
            changeView("errors")
          }}
        />
      )}
      <ServiceLogs
        sources={plan.sources}
        source={source}
        onSourceChange={changeSource}
        view={view}
        onViewChange={changeView}
        storageKey={`proxy.site.${name}`}
        views={views}
        window={around}
        onLeaveWindow={() => {
          setAround(undefined)
          setView("live")
        }}
        pickerLabel="Site log"
        paneClassName="h-[min(78vh,48rem)] min-h-[28rem]"
      />
    </div>
  )
}

/**
 * The site's last hour, as four figures: how much it served, how much of it
 * failed, how much it refused, and — where it has an error log of its own —
 * how often the application behind it would not answer. Each is a press away
 * from the rows it counts. Taken over the hour rather than the view's window
 * so the figures hold still while the rows under them are narrowed.
 */
function SiteReadings({
  base,
  errors,
  requestsOn,
  classes,
  upstreamOn,
  onRequests,
  onUpstream,
}: {
  base: string
  /** The site's own error log; a shared one would count every site's failures. */
  errors?: SiteErrorLog
  requestsOn: boolean
  classes: StatusClass[]
  upstreamOn: boolean
  onRequests: (classes: StatusClass[]) => void
  onUpstream: () => void
}) {
  const hour = usePoll<DeploymentRequests>(
    (signal) =>
      get<DeploymentRequests>(
        `${base}/requests`,
        { since: iso(Date.now() - 3_600_000), limit: 1 },
        signal,
      ),
    30_000,
    [base],
  )
  // Only the figure this grid draws: each of the lens's other readings is a
  // search of the error log a minute, for a tile nobody sees.
  const lens = useMemo(() => {
    const full = lensFor(errors?.lens)
    // The lens's own hint, in a quarter of the row, was cut off before it
    // said what failed.
    const upstream = full?.readings?.find((r) => r.id === "upstream")
    return (
      full && {
        ...full,
        readings: upstream ? [{ ...upstream, hint: "Refused, timed out, closed" }] : [],
      }
    )
  }, [errors?.lens])
  const readings = useLensReadings(errors?.source ?? "", lens, {
    forcedLens: errors?.lens,
    enabled: Boolean(errors),
  })
  const upstream = readings.tiles.find((tile) => tile.reading.id === "upstream")

  const summary = hour.data?.status === "available" ? hour.data.summary : undefined
  const buckets = summary?.buckets ?? []
  const count = (klass: StatusClass) => summary?.classes[klass] ?? 0
  const probes = (summary?.probes ?? []).reduce((n, facet) => n + facet.count, 0)
  const only = (klass: StatusClass) => requestsOn && classes.length === 1 && classes[0] === klass
  const tile = "h-full transition-colors group-hover:bg-row-hover"

  return (
    <StatGrid columns={upstream ? 4 : 3} dense>
      <StatButton label="Show every request" onClick={() => onRequests([])}>
        <StatTile
          className={tile}
          label="Requests"
          value={
            summary
              ? summary.perMinute.toLocaleString(undefined, {
                  maximumFractionDigits: summary.perMinute < 10 ? 2 : 0,
                })
              : "—"
          }
          trailing={summary ? "per minute" : undefined}
          trend={
            <TileTrend
              values={buckets.map((bucket) => bucket.total)}
              label="Requests over the last hour"
            />
          }
          hint={
            summary
              ? `${plural(summary.pages, "page view")} in the last hour`
              : (hour.data?.reason && "No request record") || undefined
          }
        />
      </StatButton>
      <StatButton
        label={only("5xx") ? "Show every request again" : "Show the requests that failed"}
        pressed={only("5xx")}
        onClick={() => onRequests(only("5xx") ? [] : ["5xx"])}
      >
        <StatTile
          className={tile}
          label="Server errors"
          value={summary ? count("5xx").toLocaleString() : "—"}
          trailing={summary ? "in 1h" : undefined}
          tone={count("5xx") > 0 ? "danger" : "default"}
          trend={
            count("5xx") > 0 ? (
              <TileTrend
                values={buckets.map((bucket) => bucket.counts["5xx"] ?? 0)}
                color="var(--chart-3)"
                label="Server errors over the last hour"
              />
            ) : undefined
          }
          hint={
            summary
              ? summary.total > 0
                ? `${(summary.errorRate * 100).toFixed(1)}% of ${summary.total.toLocaleString()} answered 5xx`
                : "Nothing asked in the last hour"
              : undefined
          }
        />
      </StatButton>
      <StatButton
        label={only("4xx") ? "Show every request again" : "Show the requests refused"}
        pressed={only("4xx")}
        onClick={() => onRequests(only("4xx") ? [] : ["4xx"])}
      >
        <StatTile
          className={tile}
          label="Refused"
          value={summary ? count("4xx").toLocaleString() : "—"}
          trailing={summary ? "in 1h" : undefined}
          tone={count("4xx") > 0 ? "warning" : "default"}
          hint={
            summary
              ? probes > 0
                ? `${plural(probes, "probe")} for missing files`
                : "Not found, not allowed"
              : undefined
          }
        />
      </StatButton>
      {upstream && (
        <ReadingTile
          tile={upstream}
          window={readings.window}
          pressed={upstreamOn}
          onPick={onUpstream}
        />
      )}
    </StatGrid>
  )
}

/**
 * What the proxy wrote in its error log in the second a failed request was
 * answered in — "connect() failed (111: Connection refused) while connecting
 * to upstream" under the 502 it explains. The error log's lines carry the
 * host they were about, so a log other sites share is narrowed to this one's.
 * A second either side because nginx stamps both records to the second, and
 * the request's own time is when it was answered, after the failure it
 * explains. Nothing found says so, and what that means: the failure came
 * from behind the proxy.
 */
export function FailedRequestLines({
  entry,
  errors,
  engine,
  onOpen,
}: {
  entry: RequestEntry
  errors: SiteErrorLog
  engine: string
  onOpen: () => void
}) {
  const { time, highlight } = useLogView()
  // On a phone the time column is width the answer needs, and the opened
  // row above already says when; nginx's own stamp stays in its line.
  const wide = useMediaQuery("(min-width: 640px)")
  const at = Date.parse(entry.time)
  // Caddy records the host it answered for; nginx's combined line does not,
  // and a shared log is narrowed by the site's names instead.
  const fields: LogFields =
    entry.host && errors.fields.host ? { ...errors.fields, host: [entry.host] } : errors.fields
  const said = usePoll(
    (signal) =>
      get<LogSearchResult>(
        "/logs/search",
        {
          source: errors.source,
          lens: errors.lens,
          since: iso(at - (entry.durationMs ?? 0) - AROUND_REQUEST),
          until: iso(at + AROUND_REQUEST),
          order: "asc",
          limit: 20,
          ...filterQuery({ ...EMPTY_FILTER, levels: FAILURES, fields }),
        },
        signal,
      ),
    0,
    [errors.source, entry.time, JSON.stringify(fields)],
    { enabled: Number.isFinite(at) },
  )
  const lines = said.data?.lines ?? []
  const eventColumn = eventColumnFor(lines, errors.lens)

  return (
    <section aria-label={`What ${engine} logged`} className="mt-3 border-t border-hairline pt-2.5">
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1 font-sans text-hint">
        <span className="font-medium text-foreground">{engine} said</span>
        <span className="text-muted-foreground">
          {errors.shared && !narrowed(errors)
            ? "within a second of this request, in the log every site shares"
            : "within a second of this request"}
        </span>
        <Button size="xs" variant="ghost" className="ml-auto h-6 px-1.5" onClick={onOpen}>
          <ClockRewind className="size-3" />
          Open in {logName(errors)}
        </Button>
      </div>
      {said.error ? (
        <p className="mt-1.5 font-sans text-hint text-muted-foreground">
          Could not read {logName(errors)}: {errorMessage(said.error)}
        </p>
      ) : !said.data ? (
        <p className="mt-1.5 font-sans text-hint text-muted-foreground">
          Reading {logName(errors)}…
        </p>
      ) : lines.length === 0 ? (
        <p className="mt-1.5 font-sans text-hint text-muted-foreground">
          {engine} logged nothing then, so the {entry.status} came from the application behind it
          rather than from {engine} itself.
        </p>
      ) : (
        // Wrapped whatever the console's setting: these few lines are the
        // answer, and the part that says why is at the end of a long one.
        <div className="mt-1.5 rounded-lg bg-surface-sunken py-1">
          {lines.map((line, i) => (
            <LogRow
              key={i}
              line={line}
              time={wide ? time : "off"}
              wrap
              highlight={highlight}
              lens={errors.lens}
              eventColumn={eventColumn}
              cont={line.cont}
            />
          ))}
        </div>
      )}
    </section>
  )
}

/**
 * The kinds of failure the Errors view counts: the lens's own quick views
 * that name events — nginx's Upstream, Rate limited, TLS, Config; Caddy's
 * Upstream and Certificates — so its chips are the words the Insights and
 * the Fields popover already use, and the Upstream failures figure above
 * lands on the chip that counts the same four events.
 */
function failureKinds(lensId: string): { id: string; label: string; events: string[] }[] {
  return (lensFor(lensId)?.views ?? []).flatMap((view) => {
    const keys = Object.keys(view.fields ?? {})
    if (view.levels || view.q || keys.length !== 1 || keys[0] !== "event") return []
    return [{ id: view.id, label: view.label, events: view.fields!.event }]
  })
}

/**
 * The last day's failures in the error log that `fields` narrow it to, or
 * nothing asked while there is no such question.
 */
function useFailures(
  errors: SiteErrorLog,
  fields: LogFields | undefined,
  limit: number,
  facets?: string,
) {
  return usePoll(
    async (signal) => {
      const until = Date.now()
      const since = until - ERRORS_WINDOW
      const res = await get<LogSearchResult>(
        "/logs/search",
        {
          source: errors.source,
          lens: errors.lens,
          since: iso(since),
          facets,
          limit,
          ...filterQuery({ ...EMPTY_FILTER, levels: FAILURES, fields: fields ?? {} }),
        },
        signal,
      )
      return { res, since, until }
    },
    30_000,
    [errors.source, errors.lens, JSON.stringify(fields), limit, facets],
    { enabled: fields !== undefined },
  )
}

/** Two stretches of one log, each oldest first, as one: a line without a time keeps its place. */
function byTime(a: LogLine[], b: LogLine[]): LogLine[] {
  const out: LogLine[] = []
  let i = 0
  let j = 0
  while (i < a.length || j < b.length) {
    const left = i < a.length ? Date.parse(a[i].timestamp ?? "") : NaN
    const right = j < b.length ? Date.parse(b[j].timestamp ?? "") : NaN
    if (j >= b.length || (i < a.length && !(right < left))) out.push(a[i++])
    else out.push(b[j++])
  }
  return out
}

/**
 * The site's error log, by what failed: the last day's errors and warnings
 * in the console every log is read in, under a chip per kind with its count.
 * A chip is a question of its own to the server rather than a sieve over the
 * lines already read, since on a busy shared log the newest thousand are
 * mostly somebody else's scanners; the counts come from the day as a whole.
 * History takes the same question further back, narrowed the same way, and
 * a line opened in place asks it too.
 */
function SiteErrors({
  ctx,
  errors,
  engine,
  kind,
  onKindChange,
}: {
  ctx: ServiceLogsContext
  errors: SiteErrorLog
  engine: string
  kind: string
  onKindChange: (kind: string) => void
}) {
  const kinds = useMemo(() => failureKinds(errors.lens), [errors.lens])
  const chosen = kinds.find((k) => k.id === kind)
  // Caddy's certificate lines name the domain rather than the host a request
  // asked for, so the kind that counts them is narrowed by that instead —
  // and asked on its own, since one search cannot ask for either.
  const certificates = errors.certificates ? kinds.find((k) => k.id === "certificates") : undefined
  const certificateFields = certificates && {
    ...errors.certificates,
    event: certificates.events,
  }
  const kindFields =
    chosen && chosen !== certificates ? { ...errors.fields, event: chosen.events } : undefined

  const all = useFailures(errors, errors.fields, chosen ? 1 : ERRORS_LIMIT, "event")
  const certified = useFailures(errors, certificateFields, ERRORS_LIMIT)
  const picked = useFailures(errors, kindFields, ERRORS_LIMIT)

  const allRes = all.data?.res
  const certRes = certified.data?.res
  const counts = useMemo(() => {
    const byEvent = new Map(
      (allRes?.facets?.event?.values ?? []).map((value) => [value.value, value.count]),
    )
    return kinds.map((k) => ({
      ...k,
      count:
        k === certificates
          ? (certRes?.matched ?? 0)
          : k.events.reduce((n, event) => n + (byEvent.get(event) ?? 0), 0),
    }))
  }, [kinds, certificates, allRes, certRes])
  const total = allRes && allRes.matched + (certRes?.matched ?? 0)

  // The search the lines on screen are: the chosen kind's, or the whole
  // day's with the certificate lines woven in where they were asked apart.
  const onCertificates = chosen !== undefined && chosen === certificates
  const reading = onCertificates ? certified : chosen ? picked : all
  const readingRes = reading.data?.res
  const shown = useMemo(() => {
    if (!readingRes) return undefined
    if (chosen || !certRes)
      return {
        lines: readingRes.lines,
        matched: readingRes.matched,
        truncated: readingRes.truncated,
      }
    const lines = byTime(readingRes.lines, certRes.lines)
    return {
      lines: lines.slice(-ERRORS_LIMIT),
      matched: readingRes.matched + certRes.matched,
      truncated: readingRes.truncated || certRes.truncated || lines.length > ERRORS_LIMIT,
    }
  }, [chosen, readingRes, certRes])
  const failure = all.error ?? certified.error ?? picked.error

  const narrowedTo: LogFields = chosen ? { event: chosen.events } : {}
  const openHistory = (fields: LogFields = narrowedTo) =>
    ctx.openHistory({
      since: iso(reading.data?.since ?? Date.now() - ERRORS_WINDOW),
      until: iso(reading.data?.until ?? Date.now()),
      levels: FAILURES,
      fields: onCertificates
        ? { ...errors.certificates, ...fields }
        : { ...errors.fields, ...fields },
    })

  const renderDetail = (line: LogLine, head: LogLine | undefined) => (
    <LineDetail
      line={line}
      head={head}
      lens={errors.lens}
      forcedLens={errors.lens}
      sourceId={errors.source}
      fields={narrowedTo}
      // "Only lines like this" is a question about the whole log, which
      // History answers over the same day.
      onFieldsChange={(fields) => openHistory(fields)}
    />
  )

  const whose = !errors.shared
    ? "for this site"
    : narrowed(errors)
      ? "about this site's names"
      : "in the log every site shares"

  return (
    <LogConsole
      lines={shown?.lines ?? []}
      filter={EMPTY_FILTER}
      lens={errors.lens}
      columns={lensFor(errors.lens)?.columns}
      renderDetail={renderDetail}
      leading={
        total !== undefined && (
          <>
            <FilterChip selected={!chosen} onClick={() => onKindChange("")}>
              All <ChipCount>{total.toLocaleString()}</ChipCount>
            </FilterChip>
            {counts
              .filter((k) => k.count > 0 || k.id === chosen?.id)
              .map((k) => (
                <FilterChip
                  key={k.id}
                  selected={chosen?.id === k.id}
                  onClick={() => onKindChange(chosen?.id === k.id ? "" : k.id)}
                >
                  {k.label}
                  <ChipCount>{k.count.toLocaleString()}</ChipCount>
                </FilterChip>
              ))}
          </>
        )
      }
      status={
        shown ? (
          <span className="numeric">
            {shown.truncated
              ? `The newest ${shown.lines.length.toLocaleString()} of ${shown.matched.toLocaleString()} in the last 24 hours ·`
              : "The last 24 hours ·"}
          </span>
        ) : undefined
      }
      footer={
        <Button
          size="xs"
          variant="ghost"
          className="ml-auto h-5 px-1.5"
          onClick={() => openHistory()}
        >
          <ClockRewind className="size-3" />
          Open in History
        </Button>
      }
      empty={
        failure && !shown ? (
          <ErrorState
            error={failure}
            onRetry={() => {
              all.refresh()
              certified.refresh()
              picked.refresh()
            }}
            className="max-w-lg"
          />
        ) : !shown ? (
          <EmptyState
            icon={MagnifyingGlassMinus}
            title={`Reading ${logName(errors)}…`}
            description="The last day's errors and warnings, by what failed."
          />
        ) : (
          <EmptyState
            icon={MagnifyingGlassMinus}
            title={
              chosen
                ? `No ${chosen.label.toLowerCase()} failures in the last day`
                : "Nothing went wrong in the last day"
            }
            description={`${engine} wrote no ${chosen ? "such " : ""}error or warning ${whose} in the last 24 hours. History reaches further back.`}
          />
        )
      }
    />
  )
}
