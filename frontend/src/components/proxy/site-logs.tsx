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
import type { SiteLogPlan } from "@/components/proxy/site-log-plan"

/** What a failure in the error log is: an error, or the warnings nginx writes beside one. */
const FAILURES: LogLevel[] = ["critical", "error", "warn"]

/** The Errors view's window: the last day, as the readings' figures are the last hour. */
const ERRORS_WINDOW = 24 * 3_600_000

/** A failed request's own lines: this far either side of the second it was answered in. */
const AROUND_REQUEST = 1_000

/** The error log opened on a failed request: a minute either side, to see what led to it. */
const AROUND_OPEN = 60_000

const iso = (ms: number) => new Date(ms).toISOString()

type ErrorsPlan = NonNullable<SiteLogPlan["errors"]>

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
          detail={
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

  const showRequests = (classes: StatusClass[]) => {
    setQuery({ ...query, classes })
    changeView("requests")
  }

  return (
    <div className="flex min-w-0 flex-col gap-6">
      {plan.requests && (
        <SiteReadings
          base={base}
          errors={errors && Object.keys(errors.fields).length === 0 ? errors : undefined}
          requestsOn={view === "requests"}
          classes={query.classes}
          upstreamOn={view === "errors" && kind === "upstream"}
          onRequests={showRequests}
          onUpstream={() => {
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
  errors?: ErrorsPlan
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
  const lens = lensFor(errors?.lens)
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
              ? `${summary.pages.toLocaleString()} page views in the last hour`
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
                ? `${plural(probes, "probe")} for files this site does not have`
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
function FailedRequestLines({
  entry,
  errors,
  engine,
  onOpen,
}: {
  entry: RequestEntry
  errors: ErrorsPlan
  engine: string
  onOpen: () => void
}) {
  const { time, highlight } = useLogView()
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
        <span className="text-muted-foreground">within a second of this request</span>
        <Button size="xs" variant="ghost" className="ml-auto h-6 px-1.5" onClick={onOpen}>
          <ClockRewind className="size-3" />
          Open in the error log
        </Button>
      </div>
      {said.error ? (
        <p className="mt-1.5 font-sans text-hint text-muted-foreground">
          The error log could not be read: {errorMessage(said.error)}
        </p>
      ) : !said.data ? (
        <p className="mt-1.5 font-sans text-hint text-muted-foreground">Reading the error log…</p>
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
              time={time}
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
 * The site's error log, by what failed: the last day's errors and warnings
 * in the console every log is read in, under a chip per kind with its count.
 * A chip narrows the lines here; History takes the same question further
 * back, narrowed the same way, and a line opened in place asks it too.
 */
function SiteErrors({
  ctx,
  errors,
  engine,
  kind,
  onKindChange,
}: {
  ctx: ServiceLogsContext
  errors: ErrorsPlan
  engine: string
  kind: string
  onKindChange: (kind: string) => void
}) {
  const result = usePoll(
    async (signal) => {
      const until = Date.now()
      const since = until - ERRORS_WINDOW
      const res = await get<LogSearchResult>(
        "/logs/search",
        {
          source: errors.source,
          lens: errors.lens,
          since: iso(since),
          facets: "event",
          limit: 1000,
          ...filterQuery({ ...EMPTY_FILTER, levels: FAILURES, fields: errors.fields }),
        },
        signal,
      )
      return { res, since, until }
    },
    30_000,
    [errors.source, JSON.stringify(errors.fields)],
  )
  const res = result.data?.res
  const facet = res?.facets?.event
  const kinds = useMemo(() => {
    const counts = new Map((facet?.values ?? []).map((value) => [value.value, value.count]))
    return failureKinds(errors.lens).map((k) => ({
      ...k,
      count: k.events.reduce((n, event) => n + (counts.get(event) ?? 0), 0),
    }))
  }, [facet, errors.lens])
  const chosen = kinds.find((k) => k.id === kind)
  const lines = useMemo(() => {
    const all = res?.lines ?? []
    return chosen ? all.filter((line) => chosen.events.includes(line.event ?? "")) : all
  }, [res, chosen])
  const narrowed: LogFields = chosen ? { event: chosen.events } : {}

  const openHistory = (fields: LogFields = narrowed) =>
    ctx.openHistory({
      since: iso(result.data?.since ?? Date.now() - ERRORS_WINDOW),
      until: iso(result.data?.until ?? Date.now()),
      levels: FAILURES,
      fields: { ...errors.fields, ...fields },
    })

  const renderDetail = (line: LogLine, head: LogLine | undefined) => (
    <LineDetail
      line={line}
      head={head}
      lens={errors.lens}
      forcedLens={errors.lens}
      sourceId={errors.source}
      fields={narrowed}
      // "Only lines like this" is a question about the whole log, which
      // History answers over the same day.
      onFieldsChange={(fields) => openHistory(fields)}
    />
  )

  return (
    <LogConsole
      lines={lines}
      filter={EMPTY_FILTER}
      lens={errors.lens}
      columns={lensFor(errors.lens)?.columns}
      renderDetail={renderDetail}
      leading={
        res && (
          <>
            <FilterChip selected={!chosen} onClick={() => onKindChange("")}>
              All <ChipCount>{res.matched.toLocaleString()}</ChipCount>
            </FilterChip>
            {kinds
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
        res ? (
          <span className="numeric">
            {res.truncated
              ? `The newest ${res.lines.length.toLocaleString()} of ${res.matched.toLocaleString()} in the last 24 hours ·`
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
        result.error && !res ? (
          <ErrorState error={result.error} onRetry={result.refresh} className="max-w-lg" />
        ) : !res ? (
          <EmptyState
            icon={MagnifyingGlassMinus}
            title="Reading the error log…"
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
            description={`${engine} wrote no ${chosen ? "such " : ""}error or warning ${
              Object.keys(errors.fields).length > 0 ? "about this site's names" : "for this site"
            } in the last 24 hours. History reaches further back.`}
          />
        )
      }
    />
  )
}
