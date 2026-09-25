"use client"

import { Fragment, useMemo, useState } from "react"
import { useSessionState } from "@/lib/view-state"
import { useSearchParams } from "next/navigation"
import { CrossCircle, FileText } from "@/components/icons"
import { get } from "@/lib/api"
import { calendarDate, clock, plural, relativeTime } from "@/lib/format"
import type { AuditEntry } from "@/lib/types"
import { cn } from "@/lib/utils"
import { usePoll } from "@/hooks/use-poll"
import { useArrivals } from "@/hooks/use-arrivals"
import {
  AUDIT_SECTIONS,
  ActionMark,
  ActionName,
  ActorMark,
  actorName,
  auditSection,
  isMachine,
} from "@/components/audit/marks"
import { MethodWord, RequestPath, StatusCode } from "@/components/deploy/request-marks"
import { TileTrend } from "@/components/metrics/sparkline"
import { Page, PageContext, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelFooter, PanelHeader, PanelToolbar } from "@/components/panel"
import { ProductGlyph } from "@/components/product-logo"
import { ROW_BLEED } from "@/components/row-list"
import { Address } from "@/components/security/marks"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { EmptyState, ErrorState, LoadingPanel } from "@/components/state"
import { Status } from "@/components/status-dot"
import { FaceRun } from "@/components/system-users/marks"
import { ChipStrip, FilterChip } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { NumberTicker } from "@/components/ui/number-ticker"
import {
  stickyTableHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

const PAGE_SIZE = 100
const DAY_MS = 24 * 60 * 60 * 1000

/** The sections worth a chip: the ones a trail is most often narrowed to. */
const CHIPS = ["signin", "docker", "deploy", "database", "files", "terminal", "git"]

type AuditPage = { entries: AuditEntry[]; total: number }

/**
 * Who changed this server, and what they changed.
 *
 * The page opens on four readings of the last day — how much was changed and
 * when, as a trend across its hours; how much of it the server refused; who
 * did it, drawn as their faces; and how many sign-ins there were and whether
 * any were turned away — read from their own query, so a filter narrows the
 * trail without narrowing the readings above it (§15 pass 2).
 *
 * The trail is a table of readings (§16), so it keeps its frame, its density
 * and its hairlines, and gains what it could not say before (§14): each entry
 * is drawn with the mark of the part of the product it touched — Docker's
 * whale, Git's branch, the sidebar's glyph for the rest — its actor as a face
 * in their own hue, the address as the network it is on, the route's method
 * and path in the request log's hues and the outcome in its family's colour.
 * The rows are shelved by day, and a row that arrives on a poll rises into
 * place (§11 *arrived*). The sections a trail is usually narrowed to are chips
 * over it, each with the same mark.
 */
export default function AuditPage() {
  // `?action=` is how the Docker event feed hands off: an event correlated to
  // an audit entry offers a link, and a link that lands on an unfiltered list
  // of everything the dashboard has ever done is not the entry it promised.
  //
  // Read once as an initial value rather than kept in sync, like the other
  // deep links in this product — the URL is where the reader arrived, not
  // where they are now, and re-applying it on every keystroke would fight the
  // filter box.
  const initialAction = useSearchParams().get("action") ?? ""
  const [username, setUsername] = useSessionState("audit.username", "")
  const [action, setAction] = useSessionState("audit.action", "", initialAction || undefined)
  const [onlyFailed, setOnlyFailed] = useSessionState("audit.failed", false)
  const [offset, setOffset] = useSessionState("audit.offset", 0)

  const { data, error, loading } = usePoll(
    (signal) =>
      get<AuditPage>(
        "/audit/",
        { username, action, failed: onlyFailed, limit: PAGE_SIZE, offset },
        signal,
      ),
    15000,
    [username, action, onlyFailed, offset],
  )

  // The last day, unfiltered: what the readings are about. Five hundred is the
  // most the server hands back in one page, and past it the trend and the
  // faces are read from the newest five hundred while the counts stay exact.
  const day = usePoll(async (signal) => {
    const since = new Date(Date.now() - DAY_MS).toISOString()
    const [recent, failed] = await Promise.all([
      get<AuditPage>("/audit/", { since, limit: 500 }, signal),
      get<AuditPage>("/audit/", { since, failed: true, limit: 1 }, signal),
    ])
    return { ...recent, failed: failed.total, since }
  }, 60000)

  // The header's figure outlives a filter change: a new filter empties `data`
  // while it loads, and a total that blinked out on every keystroke would
  // read as the log emptying. Adjusted during render rather than in an
  // effect, so the figure never paints a frame behind the rows.
  const [total, setTotal] = useState<number>()
  if (data && data.total !== total) setTotal(data.total)

  const filtered = username !== "" || action !== "" || onlyFailed
  const entries = useMemo(() => data?.entries ?? [], [data])
  const arrived = useArrivals(entries.map((entry) => String(entry.id)))
  const days = useMemo(() => byDay(entries), [entries])

  const narrow = (next: () => void) => {
    next()
    setOffset(0)
  }

  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Advanced" title="Audit log" />

      {day.data && <DayReadings day={day.data} />}

      {/* The table is the whole of the trail, and it is framed: a grid that
          owns its own scrolling takes an edge, or a row whose actions sit past
          the right of it reads as a row with no actions (§2). The toolbar stays
          mounted across a filter change so the box being typed into never
          loses its caret to a skeleton. */}
      <Panel>
        <PanelHeader
          title="Recorded requests"
          actions={
            total !== undefined && (
              <span className="text-hint text-muted-foreground">
                {filtered ? "Matching" : "Recorded"}{" "}
                <span key={total} className="numeric inline-block animate-rise text-foreground">
                  {total.toLocaleString()}
                </span>
              </span>
            )
          }
        />
        <PanelToolbar>
          <SearchInput
            containerClassName="sm:w-48"
            value={username}
            onChange={(e) => narrow(() => setUsername(e.target.value))}
            placeholder="User"
          />
          <Input
            value={action}
            onChange={(e) => narrow(() => setAction(e.target.value))}
            placeholder="Action, e.g. docker.container"
            className="h-8 w-full text-body sm:w-64"
          />
          <FilterChip
            selected={onlyFailed}
            onClick={() => narrow(() => setOnlyFailed(!onlyFailed))}
          >
            <CrossCircle aria-hidden className="size-3.5 text-destructive" />
            Failures only
          </FilterChip>
        </PanelToolbar>
        <PanelToolbar>
          <ChipStrip role="group" aria-label="Sections">
            {AUDIT_SECTIONS.filter((section) => CHIPS.includes(section.key)).map((section) => {
              const selected = action === section.filter
              return (
                <FilterChip
                  key={section.key}
                  selected={selected}
                  onClick={() => narrow(() => setAction(selected ? "" : section.filter))}
                >
                  {section.product ? (
                    <ProductGlyph id={section.product} />
                  ) : (
                    <section.glyph aria-hidden className="size-3.5 text-muted-foreground" />
                  )}
                  {section.label}
                </FilterChip>
              )
            })}
          </ChipStrip>
        </PanelToolbar>

        <PanelBody flush>
          {loading && !data && <LoadingPanel rows={8} />}
          {error && !data && <ErrorState error={error} />}
          {data && (
            <div key="rows" className="animate-rise">
              {/* The outer columns take the gutter from their own cell padding,
                  so the first column starts in the title's column; the `-mx`
                  bleed that does the same on a plain panel is gated to it (§2). */}
              <div className="hidden min-w-0 group-data-[plain]/panel:-mx-4 lg:block">
                <Table containerClassName="max-h-[calc(100svh-22rem)]">
                  <TableHeader className={stickyTableHeader}>
                    <TableRow>
                      <TableHead className="w-28">When</TableHead>
                      <TableHead>Who</TableHead>
                      <TableHead>Action</TableHead>
                      <TableHead className="w-full">Target</TableHead>
                      <TableHead>Result</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {days.map(({ label, entries: rows }) => (
                      <Fragment key={label}>
                        <TableRow className="border-b-0 hover:bg-transparent">
                          <TableCell colSpan={5} className="pt-4 pb-1.5">
                            <DayLabel label={label} count={rows.length} />
                          </TableCell>
                        </TableRow>
                        {rows.map((entry) => (
                          <TableRow
                            key={entry.id}
                            className={cn(arrived.has(String(entry.id)) && "animate-rise")}
                          >
                            <TableCell>
                              <div className="numeric font-mono text-xs">{clock(entry.ts)}</div>
                              <p className="text-hint text-muted-foreground">
                                {relativeTime(entry.ts)}
                              </p>
                            </TableCell>
                            <TableCell>
                              <Actor entry={entry} />
                            </TableCell>
                            <TableCell>
                              <div className="flex max-w-[20rem] min-w-0 items-center gap-3">
                                <ActionMark action={entry.action} />
                                <div className="min-w-0">
                                  <ActionName action={entry.action} className="block text-xs" />
                                  {entry.path && (
                                    <p className="flex min-w-0 items-baseline gap-1.5 text-hint">
                                      <MethodWord method={entry.method} />
                                      <RequestPath path={entry.path} />
                                    </p>
                                  )}
                                </div>
                              </div>
                            </TableCell>
                            {/* The column that yields: `max-w-0` beside `w-full` lets it
                                shrink to what the others leave and truncate, where a
                                path longer than that pushed Result past the frame. */}
                            <TableCell className="max-w-0">
                              <div className="truncate font-mono text-xs" title={entry.target}>
                                {entry.target}
                              </div>
                              {entry.detail && (
                                <p
                                  className="truncate text-hint text-muted-foreground"
                                  title={entry.detail}
                                >
                                  {entry.detail}
                                </p>
                              )}
                            </TableCell>
                            <TableCell>
                              <Result entry={entry} />
                            </TableCell>
                          </TableRow>
                        ))}
                      </Fragment>
                    ))}
                  </TableBody>
                </Table>
              </div>
              {/* Below `lg` the same entries are drawn down the row instead of
                  across it, so the phone still sees who did what to what, and
                  whether it worked. */}
              <div className="px-4 group-data-[plain]/panel:px-0 lg:hidden">
                {days.map(({ label, entries: rows }) => (
                  <section key={label} className="pt-4">
                    <DayLabel label={label} count={rows.length} />
                    <ul className="mt-1.5 divide-y divide-hairline">
                      {rows.map((entry) => (
                        <li
                          key={entry.id}
                          className={cn(
                            "flex min-w-0 items-start gap-3 py-3",
                            ROW_BLEED,
                            arrived.has(String(entry.id)) && "animate-rise",
                          )}
                        >
                          <ActionMark action={entry.action} />
                          <div className="min-w-0 flex-1">
                            <div className="flex min-w-0 items-baseline gap-2">
                              <ActionName action={entry.action} className="text-xs font-medium" />
                              <span className="numeric shrink-0 text-hint text-muted-foreground">
                                {clock(entry.ts)}
                              </span>
                            </div>
                            <p className="truncate font-mono text-hint text-muted-foreground">
                              {entry.target}
                            </p>
                            <p className="flex min-w-0 items-center gap-1.5 text-hint text-muted-foreground">
                              <span className="shrink-0 text-foreground">{actorName(entry)}</span>
                              {entry.ip && <Address ip={entry.ip} className="min-w-0" />}
                            </p>
                            {entry.detail && (
                              <p className="truncate text-hint text-muted-foreground">
                                {entry.detail}
                              </p>
                            )}
                          </div>
                          <Result entry={entry} className="shrink-0" />
                        </li>
                      ))}
                    </ul>
                  </section>
                ))}
              </div>
              {entries.length === 0 && (
                <EmptyState
                  icon={FileText}
                  title={
                    data.total === 0 && !filtered ? "Nothing recorded yet" : "No entries match"
                  }
                  description={
                    data.total === 0 && !filtered
                      ? "Every request that changes something on this host is written here."
                      : "Clear a filter, or look further back with the pager."
                  }
                  className="mt-4"
                />
              )}
            </div>
          )}
        </PanelBody>

        <PanelFooter className="justify-between">
          <span className="numeric text-hint text-muted-foreground">
            {!data
              ? "Loading…"
              : data.total === 0
                ? filtered
                  ? "No matching entries"
                  : "Nothing recorded yet"
                : `${offset + 1}–${Math.min(offset + PAGE_SIZE, data.total)} of ${data.total.toLocaleString()}`}
          </span>
          <div className="flex gap-2">
            <Button
              size="sm"
              variant="outline"
              disabled={offset === 0}
              onClick={() => setOffset((o) => Math.max(0, o - PAGE_SIZE))}
            >
              Previous
            </Button>
            <Button
              size="sm"
              variant="outline"
              disabled={!data || offset + PAGE_SIZE >= data.total}
              onClick={() => setOffset((o) => o + PAGE_SIZE)}
            >
              Next
            </Button>
          </div>
        </PanelFooter>
      </Panel>
    </Page>
  )
}

/**
 * The page's entries shelved by the day they happened on, newest first, the
 * way the server orders them: "Today", "Yesterday", then the date.
 */
function byDay(entries: AuditEntry[]) {
  const today = new Date()
  const yesterday = new Date(Date.now() - DAY_MS)
  const same = (a: Date, b: Date) => a.toDateString() === b.toDateString()
  const days: { label: string; entries: AuditEntry[] }[] = []
  for (const entry of entries) {
    const at = new Date(entry.ts)
    const label = Number.isNaN(at.getTime())
      ? "Undated"
      : same(at, today)
        ? "Today"
        : same(at, yesterday)
          ? "Yesterday"
          : calendarDate(entry.ts)
    const last = days.at(-1)
    if (last?.label === label) last.entries.push(entry)
    else days.push({ label, entries: [entry] })
  }
  return days
}

function DayLabel({ label, count }: { label: string; count: number }) {
  return (
    <p className="flex items-baseline gap-2">
      <span className="eyebrow">{label}</span>
      <span className="numeric text-micro text-muted-foreground">{count}</span>
    </p>
  )
}

/** Who acted, as themselves, and where they acted from. */
function Actor({ entry }: { entry: AuditEntry }) {
  return (
    <div className="flex max-w-[14rem] min-w-0 items-center gap-3">
      <ActorMark entry={entry} />
      <div className="min-w-0">
        <div
          className={cn(
            "truncate text-body",
            !entry.username && !isMachine(entry) && "text-muted-foreground",
          )}
        >
          {actorName(entry)}
        </div>
        <p className="flex min-w-0 items-center gap-1.5 text-hint text-muted-foreground">
          {entry.ip && <Address ip={entry.ip} className="min-w-0" />}
          {entry.actor === "token" && <span className="shrink-0">· API key</span>}
        </p>
      </div>
    </div>
  )
}

type Day = AuditPage & { failed: number; since: string }

/**
 * The last day in four readings. The trend is the day's changes per hour,
 * oldest on the left; the faces are the people behind them, most active first.
 */
function DayReadings({ day }: { day: Day }) {
  const readings = useMemo(() => {
    const start = new Date(day.since).getTime()
    const hours = Array.from({ length: 24 }, () => 0)
    const people = new Map<string, number>()
    const sections = new Map<string, number>()
    let machines = 0
    const signIns = { ok: 0, refused: 0, lastFrom: "" }
    for (const entry of day.entries) {
      const hour = Math.floor((new Date(entry.ts).getTime() - start) / 3_600_000)
      if (hour >= 0 && hour < 24) hours[hour]++
      if (isMachine(entry)) machines++
      else if (entry.username && entry.actor !== "anonymous") {
        people.set(entry.username, (people.get(entry.username) ?? 0) + 1)
      }
      const section = auditSection(entry.action)
      if (section) sections.set(section.label, (sections.get(section.label) ?? 0) + 1)
      if (entry.action === "auth.login") {
        if (entry.success) {
          signIns.ok++
          signIns.lastFrom ||= entry.ip
        } else signIns.refused++
      }
    }
    const rank = (map: Map<string, number>) =>
      [...map.entries()].sort((a, b) => b[1] - a[1]).map(([name]) => name)
    return { hours, people: rank(people), sections: rank(sections), machines, signIns }
  }, [day])

  const { hours, people, sections, machines, signIns } = readings
  return (
    <StatGrid columns={4} className="animate-rise">
      <StatTile
        label="Last 24 hours"
        value={<NumberTicker value={day.total} />}
        trailing={day.total === 1 ? "change" : "changes"}
        trend={<TileTrend values={hours} label="Changes per hour over the last day" />}
        hint={sections.length > 0 ? sections.slice(0, 3).join(" · ") : "nothing was changed"}
      />
      <StatTile
        label="Failed"
        value={<NumberTicker value={day.failed} />}
        tone={day.failed > 0 ? "danger" : "success"}
        hint={
          day.failed > 0
            ? `${Math.round((day.failed / Math.max(day.total, 1)) * 100)}% of the day's requests`
            : "every change went through"
        }
      />
      <StatTile
        label="People"
        value={<NumberTicker value={people.length} />}
        hint={
          <span className="inline-flex max-w-full min-w-0 items-center gap-2">
            <FaceRun names={people} />
            <span className="truncate">
              {people.length === 0
                ? machines > 0
                  ? `only automations · ${machines}`
                  : "nobody changed anything"
                : machines > 0
                  ? `and ${plural(machines, "automated change")}`
                  : people.length === 1
                    ? people[0]
                    : people.slice(0, 2).join(", ")}
            </span>
          </span>
        }
      />
      <StatTile
        label="Sign-ins"
        value={<NumberTicker value={signIns.ok} />}
        tone={signIns.refused > 0 ? "warning" : "default"}
        hint={
          signIns.refused > 0 ? (
            `${plural(signIns.refused, "attempt")} refused`
          ) : signIns.lastFrom ? (
            <span className="inline-flex max-w-full min-w-0 items-center gap-1.5">
              <span className="shrink-0">last from</span>
              <Address ip={signIns.lastFrom} className="min-w-0" />
            </span>
          ) : (
            "none refused"
          )
        }
      />
    </StatGrid>
  )
}

/**
 * The outcome as a reading: a dot and the status code in its family's colour,
 * the way the request log draws one, with the word for a failure beside it —
 * "403 Forbidden" says the capability check refused it before the row is
 * opened. Red arrives only here, attached to the code that failed, rather
 * than washed across the whole row. An entry the dashboard wrote for itself
 * carries no code, and says whether it worked in a word.
 */
function Result({ entry, className }: { entry: AuditEntry; className?: string }) {
  return (
    <Status
      tone={entry.success ? "running" : "danger"}
      label={
        entry.status ? (
          <StatusCode status={entry.status} word={!entry.success} />
        ) : entry.success ? (
          "done"
        ) : (
          "failed"
        )
      }
      className={className}
    />
  )
}
