"use client"

import { useMemo, useState } from "react"
import { ClockRewind, MagnifyingGlassMinus, RefreshClockwise } from "@/components/icons"
import { get } from "@/lib/api"
import { plural } from "@/lib/format"
import type { LogLine, LogSearchResult, RequestEntry } from "@/lib/types"
import { EMPTY_FILTER, filterQuery, type LogLevel } from "@/lib/log-filter"
import { lensFor } from "@/lib/log-lenses"
import { usePoll } from "@/hooks/use-poll"
import type { LogFields } from "@/components/logs/types"
import { ServiceLogs, type LogWindow } from "@/components/logs/service-logs"
import { LineDetail } from "@/components/logs/line-detail"
import { LogConsole } from "@/components/logs/log-console"
import { Pane } from "@/components/panel"
import { EmptyState, ErrorState } from "@/components/state"
import { ChipCount, FilterChip, tabClasses } from "@/components/tabs"
import { IconAction } from "@/components/icon-action"
import { Button } from "@/components/ui/button"
import { RequestsWorkspace, type RequestQuery } from "@/components/deploy/requests-workspace"
import { OutputLines } from "@/components/deploy/output-lines"
import { PROXY_SILENT, proxyWindow } from "@/components/deploy/request-lines"
import { ProductGlyph } from "@/components/product-logo"
import type { SiteErrorLog, SiteLogPlan } from "@/components/proxy/site-log-plan"

/** What a failure in the error log is: an error, or the warnings nginx writes beside one. */
const FAILURES: LogLevel[] = ["critical", "error", "warn"]

/** The Errors view's window: the last day. */
const ERRORS_WINDOW = 24 * 3_600_000

/** The most lines the Errors view reads of one kind: the newest, as a search keeps them. */
const ERRORS_LIMIT = 1000

/** The error log opened on a failed request: a minute either side, to see what led to it. */
const AROUND_OPEN = 60_000

const iso = (ms: number) => new Date(ms).toISOString()

/** What the log a site's failures are read from is called: Caddy writes one log, not an error log. */
const logName = (errors: SiteErrorLog) =>
  errors.lens === "caddy" ? "Caddy's log" : "the error log"

/** Whether a log other sites share is narrowed to this one's names at all. */
const narrowed = (errors: SiteErrorLog) => Object.keys(errors.fields).length > 0

/** The pane's views, in the order its strip draws them. */
export type SiteLogView = "requests" | "insights" | "errors" | "files"

const VIEW_LABEL: Record<SiteLogView, string> = {
  requests: "Requests",
  insights: "Insights",
  errors: "Errors",
  files: "Log files",
}

/** The views a site's plan can answer: Requests and Insights need its own request record. */
export function siteLogViews(plan: SiteLogPlan): SiteLogView[] {
  const views: SiteLogView[] = []
  if (plan.requests) views.push("requests", "insights")
  if (plan.errors) views.push("errors")
  if (plan.sources.length > 0) views.push("files")
  return views
}

/**
 * The pane's height, the window's less the page's chrome, as the deployment
 * Logs page sizes its own: a pane that sized itself to its rows made a live
 * tail the length of the page, with nothing to follow. A floor under it,
 * lower on a phone.
 */
const PANE_SIZE = "h-[max(28rem,calc(100dvh-6rem))] sm:h-[max(40rem,calc(100dvh-6rem))]"

/**
 * A site's logs, on the site's page, in the pane the deployment Logs page
 * reads a deployment's in: one strip of views across the top and each view
 * scrolling inside it, so a site and a deployment are read the same way.
 *
 * **Requests** is the site's request record — the chart, the families, the
 * rows opening in place — the deployment's workspace over the site's route.
 * A failed request shows, in its opened row, what the proxy wrote in its
 * error log in the second around it, which is where "502" turns into "the
 * application refused the connection". **Insights** is what the same window
 * adds up to. **Errors** is that error log's failures over the last day, by
 * kind, counted on its tab. **Log files** is the site's logs themselves,
 * live and back through History, read through the lens of what wrote them.
 *
 * The figures that stood over the pane went where they are said better: the
 * hour's rate and the probes refused are the route's first node, the failed
 * requests the identity line's verdict — a press of which narrows Requests to
 * them, as the tile's did — and the upstream failures the Errors tab's count.
 */
export function SiteLogs({
  name,
  plan,
  engine,
  view,
  onViewChange,
  query,
  onQueryChange,
}: {
  name: string
  plan: SiteLogPlan
  /** The engine's name, for who wrote the error lines: "nginx wrote no error…". */
  engine: string
  view: SiteLogView
  onViewChange: (view: SiteLogView) => void
  query: RequestQuery
  onQueryChange: (query: RequestQuery) => void
}) {
  const base = `/proxy/sites/${encodeURIComponent(name)}`
  const errors = plan.errors
  const views = siteLogViews(plan)
  const [source, setSource] = useState<string | null>(plan.requests ?? errors?.source ?? null)
  const [kind, setKind] = useState("")
  const [around, setAround] = useState<LogWindow | undefined>()
  const failures = useDayCount(errors)

  // A window opened on the log files is a question about one moment, and
  // goes when the reader leaves for another view, or the strip would come
  // back on it with nothing to say what it was.
  const choose = (next: SiteLogView) => {
    if (next !== "files") setAround(undefined)
    onViewChange(next)
  }
  const openFiles = (window: LogWindow, at: string) => {
    setSource(at)
    setAround(window)
    onViewChange("files")
  }
  // The minute either side, every level, narrowed as the lines under the
  // request were: a log other sites share is this site's names in it.
  const openAround = (entry: RequestEntry) => {
    if (!errors) return
    const at = Date.parse(entry.time)
    if (!Number.isFinite(at)) return
    openFiles(
      {
        since: iso(at - AROUND_OPEN),
        until: iso(at + AROUND_OPEN),
        label: "Around a failed request",
        fields: requestFields(entry, errors),
        levels: [],
        q: "",
      },
      errors.source,
    )
  }

  return (
    <Pane className={PANE_SIZE}>
      <div className="flex min-h-10 shrink-0 items-stretch border-b border-hairline pr-1 pl-2">
        <nav aria-label="Log view" className="flex min-w-0 flex-1 items-stretch overflow-x-auto">
          {views.map((id) => (
            <button
              key={id}
              type="button"
              aria-pressed={view === id}
              className={tabClasses(view === id, "h-10")}
              onClick={() => choose(id)}
            >
              {VIEW_LABEL[id]}
              {/* The day's failures, the number the Errors view's All chip
                  counts, so the tab and the rows under it agree. */}
              {id === "errors" && failures !== undefined && failures > 0 && (
                <span
                  aria-hidden
                  title={`${plural(failures, "error or warning", "errors or warnings")} in the last day`}
                  className="numeric text-hint text-warning"
                >
                  {failures.toLocaleString()}
                </span>
              )}
            </button>
          ))}
        </nav>
      </div>

      {(view === "requests" || view === "insights") && plan.requests && (
        <RequestsWorkspace
          base={base}
          subject={`site ${name}`}
          emptyTitle="No request record for this site"
          view={view}
          query={query}
          onQueryChange={onQueryChange}
          markers={[]}
          renderInline={
            errors
              ? (entry) =>
                  entry.status >= 500 ? (
                    <FailedRequestLines
                      entry={entry}
                      errors={errors}
                      onOpen={() => openAround(entry)}
                    />
                  ) : undefined
              : undefined
          }
        />
      )}
      {view === "errors" && errors && (
        <div className="flex min-h-0 flex-1 flex-col overflow-auto">
          <SiteErrors
            errors={errors}
            engine={engine}
            kind={kind}
            onKindChange={setKind}
            onOpenHistory={(window) => openFiles(window, errors.source)}
          />
        </div>
      )}
      {view === "files" && (
        <ServiceLogs
          sources={plan.sources}
          source={source}
          onSourceChange={(id) => {
            setSource(id)
            setAround(undefined)
          }}
          storageKey={`proxy.site.${name}`}
          modes={["live", "search"]}
          window={around}
          onLeaveWindow={() => setAround(undefined)}
          pickerLabel="Site log"
          flush
          className="min-h-0 flex-1"
        />
      )}
    </Pane>
  )
}

/**
 * How many errors and warnings the Errors view will hold, for its tab: the
 * same day and the same narrowing, read once as the page opens rather than on
 * a timer — each read is a pass over a day of a log, and on a Caddy ingress
 * that log is every site's. A log every site shares and nothing narrows is
 * not counted: its number would be everybody's.
 */
function useDayCount(errors: SiteErrorLog | undefined): number | undefined {
  const counted = errors && (!errors.shared || narrowed(errors)) ? errors : undefined
  const certificates = counted ? certificateFields(counted) : undefined
  const all = useFailures(counted, counted?.fields, 1, "event")
  const certified = useFailures(counted, certificates, 1)
  if (!all.data) return undefined
  return all.data.res.matched + (certified.data?.res.matched ?? 0)
}

/**
 * What in the error log is about one request's site: Caddy records the host
 * it answered for; nginx's combined line does not, and a shared log is
 * narrowed by the site's names instead.
 */
function requestFields(entry: RequestEntry, errors: SiteErrorLog): LogFields {
  return entry.host && errors.fields.host ? { ...errors.fields, host: [entry.host] } : errors.fields
}

/**
 * What the proxy wrote in its error log while a failed request was in flight
 * and the second either side — "connect() failed (111: Connection refused)
 * while connecting to upstream" under the 502 it explains. The error log's
 * lines carry the host they were about, so a log other sites share is
 * narrowed to this one's. Drawn as the deployment's opened request draws
 * the same answer (`RequestLines`), with the same window and the same
 * sentence when there is none: the failure came from behind the proxy.
 */
export function FailedRequestLines({
  entry,
  errors,
  onOpen,
}: {
  entry: RequestEntry
  errors: SiteErrorLog
  onOpen: () => void
}) {
  const at = Date.parse(entry.time)
  if (!Number.isFinite(at)) return null
  return (
    <OutputLines
      title="Proxy said"
      facts={
        <>
          <ProductGlyph id={errors.lens === "caddy" ? "caddy" : "nginx"} className="size-3" />
          <span className="truncate">
            {logName(errors)}
            {errors.shared && !narrowed(errors) ? ", which every site shares" : ""}
          </span>
        </>
      }
      query={{
        source: errors.source,
        lens: errors.lens,
        ...proxyWindow(entry, at),
        order: "asc",
        limit: 20,
        ...filterQuery({ ...EMPTY_FILTER, levels: FAILURES, fields: requestFields(entry, errors) }),
      }}
      empty={PROXY_SILENT}
      action={
        <Button size="xs" variant="ghost" className="h-6 px-1.5" onClick={onOpen}>
          <ClockRewind className="size-3" />
          Open in {logName(errors)}
        </Button>
      }
    />
  )
}

/**
 * The kinds of failure the Errors view counts: the lens's own quick views
 * that name events — nginx's Upstream, Rate limited, TLS, Config; Caddy's
 * Upstream and Certificates — so its chips are the words the Log files'
 * quick views and the Fields popover already use.
 */
function failureKinds(lensId: string): { id: string; label: string; events: string[] }[] {
  return (lensFor(lensId)?.views ?? []).flatMap((view) => {
    const keys = Object.keys(view.fields ?? {})
    if (view.levels || view.q || keys.length !== 1 || keys[0] !== "event") return []
    return [{ id: view.id, label: view.label, events: view.fields!.event }]
  })
}

/**
 * What narrows Caddy's log to this site's certificate lines, where it is
 * Caddy's: those lines name the domain rather than the host a request asked
 * for, so the kind that counts them is narrowed by that instead — and asked
 * on its own, since one search cannot ask for either.
 */
function certificateFields(errors: SiteErrorLog): LogFields | undefined {
  const certificates = errors.certificates
    ? failureKinds(errors.lens).find((k) => k.id === "certificates")
    : undefined
  return certificates && { ...errors.certificates, event: certificates.events }
}

/**
 * The last day's failures in the error log that `fields` narrow it to, or
 * nothing asked while there is no such question.
 *
 * Read when the view opens and when its question changes, and again when the
 * reader asks — not on a timer. Each read is a pass over a day of a log that
 * on a Caddy ingress is every site's, two or three at once, and a view left
 * open would keep them running; a poll also answered with new lines, which
 * closed the one the reader had opened. Live is where fresh lines arrive.
 */
function useFailures(
  errors: SiteErrorLog | undefined,
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
          source: errors!.source,
          lens: errors!.lens,
          since: iso(since),
          facets,
          limit,
          ...filterQuery({ ...EMPTY_FILTER, levels: FAILURES, fields: fields ?? {} }),
        },
        signal,
      )
      return { res, since, until }
    },
    0,
    [errors?.source, errors?.lens, JSON.stringify(fields), limit, facets],
    { enabled: errors !== undefined && fields !== undefined },
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
  errors,
  engine,
  kind,
  onKindChange,
  onOpenHistory,
}: {
  errors: SiteErrorLog
  engine: string
  kind: string
  onKindChange: (kind: string) => void
  /** The log files' History over a window, narrowed as the lines here are. */
  onOpenHistory: (window: LogWindow) => void
}) {
  const kinds = useMemo(() => failureKinds(errors.lens), [errors.lens])
  const chosen = kinds.find((k) => k.id === kind)
  const certificates = errors.certificates ? kinds.find((k) => k.id === "certificates") : undefined
  const kindFields =
    chosen && chosen !== certificates ? { ...errors.fields, event: chosen.events } : undefined

  const all = useFailures(errors, errors.fields, chosen ? 1 : ERRORS_LIMIT, "event")
  const certified = useFailures(errors, certificateFields(errors), ERRORS_LIMIT)
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
  // A read asked for is pending until it answers, which replaces the answer
  // it was asked over.
  const [asked, setAsked] = useState<{ data: unknown; error: unknown }>()
  const pending = asked !== undefined && asked.data === all.data && asked.error === all.error
  const again = () => {
    setAsked({ data: all.data, error: all.error })
    all.refresh()
    certified.refresh()
    picked.refresh()
  }

  const narrowedTo: LogFields = chosen ? { event: chosen.events } : {}
  const openHistory = (fields: LogFields = narrowedTo) =>
    onOpenHistory({
      label: "Errors in the last day",
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
        <span className="ml-auto flex items-center gap-1">
          <IconAction
            label="Read the errors again"
            className="size-5"
            pending={pending}
            disabled={!all.data && !all.error}
            onClick={again}
          >
            <RefreshClockwise />
          </IconAction>
          <Button size="xs" variant="ghost" className="h-5 px-1.5" onClick={() => openHistory()}>
            <ClockRewind className="size-3" />
            Open in History
          </Button>
        </span>
      }
      empty={
        failure && !shown ? (
          <ErrorState error={failure} onRetry={again} className="max-w-lg" />
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
