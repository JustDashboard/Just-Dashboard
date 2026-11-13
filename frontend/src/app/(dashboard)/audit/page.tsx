"use client"

import { Fragment, useMemo, useState } from "react"
import { Workspace, WorkspaceHelp } from "@/components/workspace/workspace"
import { useFilterHistory } from "@/components/workspace/history"
import { useHeldList } from "@/components/workspace/held-list"
import { CrossCircle, FileText, Notes, RefreshClockwise } from "@/components/icons"
import { ApiError, get } from "@/lib/api"
import { calendarDate, clock, plural, relativeTime, timestamp } from "@/lib/format"
import type { MetricsState } from "@/lib/metrics-store"
import type { AuditEntry } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { useMetricsValue } from "@/hooks/use-metrics"
import { usePoll } from "@/hooks/use-poll"
import { useArrivals } from "@/hooks/use-arrivals"
import { dayReading, dayStart, failedWords, peopleWords } from "@/components/audit/day"
import { DayStrip } from "@/components/audit/day-strip"
import { EntryDetail, EntryResult } from "@/components/audit/entry-detail"
import {
  AUDIT_SECTIONS,
  ActionMark,
  ActionName,
  ActorMark,
  actorName,
  isMachine,
  keyTitle,
  keyWords,
} from "@/components/audit/marks"
import { FactDot, HostFact, HostIdentity } from "@/components/metrics/host-identity"
import { Page, PageContext, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelFooter, PanelHeader, PanelToolbar } from "@/components/panel"
import { ProductGlyph, platformProduct } from "@/components/product-logo"
import { ROW_BLEED } from "@/components/row-list"
import { Address } from "@/components/security/marks"
import { EmptyState, ErrorState } from "@/components/state"
import { Status } from "@/components/status-dot"
import { FaceRun } from "@/components/system-users/marks"
import { ChipStrip, FilterChip } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { Skeleton } from "@/components/ui/skeleton"
import {
  stickyTableHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { VerbMenu, type Verb } from "@/components/verbs"

const PAGE_SIZE = 100
const DAY_MS = 24 * 60 * 60 * 1000

/** The sections worth a chip: the ones a trail is most often narrowed to. */
const CHIPS = ["signin", "docker", "deploy", "database", "files", "terminal", "git"]

type AuditPage = { entries: AuditEntry[]; total: number }

/**
 * The line of facts as one block. On a phone it wraps to three lines, so it
 * holds that room there from the start and the strip under it does not move
 * when the day's read lands.
 */
const FACTS = "flex min-w-0 flex-wrap content-start items-center gap-x-3 gap-y-1 max-sm:min-h-14"

const hostOf = (state: MetricsState) => state.host

/**
 * Who changed this server, and what they changed.
 *
 * The page opens on what it is about, as a project's page does: an identity
 * line saying how the last day went (how much was changed, how much of it the
 * server refused, who did it, drawn as their faces, and where the last sign-in
 * came from). Under it the day is one strip,
 * an hour to a stretch in the colour of how its changes went
 * (`audit/day-strip`). Both are read from their own query, so a filter narrows
 * the trail without narrowing the day above it. Four tiles said this until
 * 0.8.7, three of them bare figures.
 *
 * The trail is a table of readings (§16), so it keeps its frame and its
 * hairlines: each entry is one line, drawn with the mark of the part of the
 * product it touched (Docker's whale, Git's branch, the sidebar's glyph for
 * the rest), its actor as a face in their own hue, the address as the network
 * it is on and the outcome in its family's colour. The action is the row's
 * button: it opens the entry whole, with the method and path, the key's record
 * and whatever a one-line cell cut short. The rows are shelved by day, and a
 * row that arrives on a poll rises into place (§11 *arrived*). The sections a
 * trail is usually narrowed to are one row of chips over it.
 */
export default function AuditPage() {
  const { can } = useAuth()
  const admin = can("system.admin")
  // `?action=` is how the Docker event feed hands off: an event correlated to
  // an audit entry offers a link, and a link that lands on an unfiltered list
  // of everything the dashboard has ever done is not the entry it promised.
  //
  // Settled filters are complete questions; Back restores them without racing the input.
  const [filters, setFilters] = useFilterHistory("audit.filters", {
    username: "",
    action: "",
    failed: "",
    offset: "0",
  })
  const { username, action } = filters
  const onlyFailed = filters.failed === "1"
  const offset =
    /^\d+$/.test(filters.offset) && Number.isSafeInteger(Number(filters.offset))
      ? Math.floor(Number(filters.offset) / PAGE_SIZE) * PAGE_SIZE
      : 0
  const setUsername = (username: string) => setFilters((previous) => ({ ...previous, username }))
  const setAction = (action: string) => setFilters((previous) => ({ ...previous, action }))
  const setOnlyFailed = (next: boolean) =>
    setFilters((previous) => ({ ...previous, failed: next ? "1" : "" }))
  const setOffset = (offset: number | ((value: number) => number)) =>
    setFilters(
      (previous) => ({
        ...previous,
        offset: String(typeof offset === "function" ? offset(Number(previous.offset)) : offset),
      }),
      true,
    )

  const {
    data,
    error,
    loading,
    refresh: trailRefresh,
  } = usePoll(
    (signal) =>
      get<AuditPage>(
        "/audit/",
        { username, action, failed: onlyFailed, limit: PAGE_SIZE, offset },
        signal,
      ),
    15000,
    [username, action, onlyFailed, offset],
    { enabled: admin },
  )

  // The last day, unfiltered: what the opening is about. Five hundred is the
  // most the server hands back in one page, and past it the strip and the
  // faces are read from the newest five hundred while the counts stay exact.
  const day = usePoll(
    async (signal) => {
      const start = dayStart(Date.now())
      const since = new Date(start).toISOString()
      const [recent, failed] = await Promise.all([
        get<AuditPage>("/audit/", { since, limit: 500 }, signal),
        get<AuditPage>("/audit/", { since, failed: true, limit: 1 }, signal),
      ])
      return { ...recent, failed: failed.total, start }
    },
    60000,
    [],
    { enabled: admin },
  )
  const reading = useMemo(
    () => (day.data ? dayReading(day.data.entries, day.data.start) : undefined),
    [day.data],
  )

  // The header's figure outlives a filter change: a new filter empties `data`
  // while it loads, and a total that blinked out on every keystroke would
  // read as the log emptying. Adjusted during render rather than in an
  // effect, so the figure never paints a frame behind the rows.
  const [total, setTotal] = useState<number>()
  if (data && data.total !== total) setTotal(data.total)

  const filtered = username !== "" || action !== "" || onlyFailed
  const held = useHeldList(data?.entries, (entry) => String(entry.id), JSON.stringify(filters))
  const entries = held.rows
  const arrived = useArrivals(entries.map((entry) => String(entry.id)))
  const days = useMemo(() => byDay(entries), [entries])

  const narrow = (next: () => void) => {
    next()
    setFilters((previous) => ({ ...previous, offset: "0" }))
  }
  const refresh = () => {
    held.reveal()
    trailRefresh()
    day.refresh()
  }

  const verbs: Verb[] = [{ key: "refresh", label: "Refresh", icon: RefreshClockwise, run: refresh }]

  // The trail is an administrator's: it names every account, address and key.
  // The server refuses anyone else, so the page says that in place of a toolbar
  // of filters over a refusal.
  const refused = (failure: Error | null | undefined) =>
    failure instanceof ApiError && failure.status === 403
  if (!admin || refused(error) || refused(day.error)) {
    return (
      <Page className="animate-rise">
        <PageContext eyebrow="Advanced" title="Audit log" />
        <EmptyState
          icon={Notes}
          title="The audit log is for administrators"
          description="It names every account, address and key that changed this server."
        />
      </Page>
    )
  }

  return (
    <Workspace
      name="Audit"
      refresh={refresh}
      escape={() => {
        if (!filtered) return false
        setFilters({ username: "", action: "", failed: "", offset: "0" }, true)
        return true
      }}
    >
      <Page className="animate-rise">
        <PageContext eyebrow="Advanced" title="Audit log" />

        {/* The line and the day under it are one block: what happened, then
            when. Both stand at their final height before the day is read. */}
        <div className="flex min-w-0 flex-col gap-4">
          <AuditIdentity
            day={day.data}
            reading={reading}
            failed={Boolean(day.error && !day.data)}
            verbs={verbs}
          />
          <DayStrip day={reading} error={day.error} onRetry={day.refresh} />
        </div>

        {/* The table is the whole of the trail, and it is framed: a grid that
          owns its own scrolling takes an edge (§2). The filters stay mounted
          across a filter change so the box being typed into never loses its
          caret to a skeleton. */}
        <Panel>
          <PanelHeader title="Recorded requests" className="gap-x-3">
            <span className="mr-auto flex min-w-0 items-center gap-3">
              {total !== undefined && (
                <span
                  key={total}
                  className="numeric animate-rise text-hint text-muted-foreground"
                  aria-label={
                    filtered
                      ? `${total.toLocaleString()} matching`
                      : `${total.toLocaleString()} recorded`
                  }
                >
                  {total.toLocaleString()}
                  {filtered && " matching"}
                </span>
              )}
              {held.pending > 0 && (
                <Button
                  size="xs"
                  aria-label={`Show ${held.pending} new audit ${held.pending === 1 ? "entry" : "entries"}`}
                  onClick={held.reveal}
                >
                  <span role="status">
                    {held.pending} new {held.pending === 1 ? "entry" : "entries"}
                  </span>{" "}
                  · Show
                </Button>
              )}
            </span>
            <div className="flex w-full min-w-0 items-center gap-2 sm:w-auto">
              <SearchInput
                containerClassName="max-sm:basis-0 max-sm:flex-1 sm:w-40"
                value={username}
                onChange={(e) => narrow(() => setUsername(e.target.value))}
                placeholder="User"
                aria-label="User"
              />
              <Input
                value={action}
                onChange={(e) => narrow(() => setAction(e.target.value))}
                data-workspace-search
                placeholder="Action, e.g. docker.container"
                aria-label="Action"
                className="h-10 min-w-0 flex-[1.4] text-body sm:h-8 sm:w-60 sm:flex-none"
              />
            </div>
          </PanelHeader>
          <PanelToolbar>
            <ChipStrip>
              <FilterChip
                selected={onlyFailed}
                onClick={() => narrow(() => setOnlyFailed(!onlyFailed))}
              >
                <CrossCircle aria-hidden className="size-3.5 text-destructive" />
                Failures only
              </FilterChip>
              <span aria-hidden className="h-4 w-px shrink-0 bg-border" />
              <div role="group" aria-label="Sections" className="contents">
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
              </div>
            </ChipStrip>
          </PanelToolbar>

          <PanelBody flush>
            {loading && !data && <TrailSkeleton />}
            {error && !data && (
              <div className="p-4">
                <ErrorState error={error} onRetry={trailRefresh} retryable />
              </div>
            )}
            {data && (
              <div key="rows" className="animate-rise">
                {/* The outer columns take the gutter from their own cell padding,
                  so the first column starts in the title's column; the `-mx`
                  bleed that does the same on a plain panel is gated to it (§2). */}
                <div className="hidden min-w-0 group-data-[plain]/panel:-mx-4 lg:block">
                  <Table containerClassName="max-h-[calc(100svh-21rem)]">
                    <TableHeader className={stickyTableHeader}>
                      <TableRow>
                        <TableHead className="w-24">When</TableHead>
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
                              data-workspace-item={String(entry.id)}
                              data-workspace-name={entry.action}
                              className={cn(arrived.has(String(entry.id)) && "animate-rise")}
                            >
                              <TableCell
                                className="numeric font-mono text-xs"
                                title={`${timestamp(entry.ts)} · ${relativeTime(entry.ts)}`}
                              >
                                {clock(entry.ts)}
                              </TableCell>
                              <TableCell>
                                <Actor entry={entry} />
                              </TableCell>
                              <TableCell>
                                <EntryButton
                                  entry={entry}
                                  className="max-w-[15rem] 2xl:max-w-[20rem]"
                                >
                                  <ActionMark action={entry.action} />
                                  <ActionName action={entry.action} className="text-xs" />
                                </EntryButton>
                              </TableCell>
                              {/* The column that yields: `max-w-0` beside `w-full` lets it
                                shrink to what the others leave and truncate, where a
                                path longer than that pushed Result past the frame. */}
                              <TableCell className="max-w-0">
                                <p className="flex min-w-0 items-baseline gap-2.5">
                                  {entry.target && (
                                    <span
                                      className="min-w-0 truncate font-mono text-xs"
                                      title={entry.target}
                                    >
                                      {entry.target}
                                    </span>
                                  )}
                                  {entry.detail && (
                                    <span
                                      className="min-w-12 flex-1 truncate text-hint text-muted-foreground"
                                      title={entry.detail}
                                    >
                                      {entry.detail}
                                    </span>
                                  )}
                                </p>
                              </TableCell>
                              <TableCell>
                                <EntryResult entry={entry} />
                              </TableCell>
                            </TableRow>
                          ))}
                        </Fragment>
                      ))}
                    </TableBody>
                  </Table>
                </div>
                {/* Below `lg` an entry is two lines, the same two whatever it
                  carries: what was done and when, then who did it to what. The
                  rest is in the entry the row opens. */}
                <div className="px-4 group-data-[plain]/panel:px-0 lg:hidden">
                  {days.map(({ label, entries: rows }) => (
                    <section key={label} className="pt-4">
                      <DayLabel label={label} count={rows.length} />
                      <ul className="mt-1.5 divide-y divide-hairline">
                        {rows.map((entry) => (
                          <li
                            key={entry.id}
                            data-workspace-item={String(entry.id)}
                            data-workspace-name={entry.action}
                            className={cn(
                              "flex min-w-0 items-center gap-3 py-2.5",
                              ROW_BLEED,
                              arrived.has(String(entry.id)) && "animate-rise",
                            )}
                          >
                            <EntryButton entry={entry} className="flex-1">
                              <ActionMark action={entry.action} />
                              <span className="block min-w-0 flex-1">
                                <span className="flex min-w-0 items-baseline gap-2">
                                  <ActionName
                                    action={entry.action}
                                    className="text-xs font-medium"
                                  />
                                  <span className="numeric shrink-0 text-hint text-muted-foreground">
                                    {clock(entry.ts)}
                                  </span>
                                </span>
                                <span className="block truncate text-hint text-muted-foreground">
                                  <span className="text-foreground">{actorName(entry)}</span>
                                  {(entry.target || entry.detail) && (
                                    <>
                                      {" · "}
                                      <span className={cn(entry.target && "font-mono")}>
                                        {entry.target || entry.detail}
                                      </span>
                                    </>
                                  )}
                                </span>
                              </span>
                            </EntryButton>
                            <EntryResult entry={entry} className="shrink-0" />
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
                    className="m-4"
                  />
                )}
              </div>
            )}
          </PanelBody>

          <PanelFooter className="justify-between">
            <span className="numeric text-hint text-muted-foreground">
              {!data
                ? error
                  ? "Not read"
                  : "Loading…"
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
    </Workspace>
  )
}

type Day = AuditPage & { failed: number; start: number }

/**
 * What the page is about, as a project's header says what a project is: the
 * log's own mark and name, how the last day went as a state beside it (what
 * the server refused, in red only when it refused something; a sign-in turned
 * away, in amber), then the day's facts in a line: this server, how much was
 * changed, who changed it, and where the last sign-in came from.
 */
function AuditIdentity({
  day,
  reading,
  failed,
  verbs,
}: {
  day: Day | undefined
  reading: ReturnType<typeof dayReading> | undefined
  /** The day could not be read, and there is no earlier reading to show. */
  failed: boolean
  verbs: Verb[]
}) {
  const host = useMetricsValue(hostOf)
  const who = reading && peopleWords(reading.people, reading.machines)
  const last = reading?.signIns.last
  return (
    <HostIdentity
      label="About the audit log"
      className="border-b-0 pb-0"
      fallback={Notes}
      title="Audit log"
      state={
        failed ? (
          <Status tone="danger" label="Not read" />
        ) : !day || !reading ? (
          <Skeleton className="h-4 w-28" />
        ) : (
          <>
            <Status
              tone={day.failed > 0 ? "danger" : day.total > 0 ? "running" : "stopped"}
              label={failedWords(day.failed, day.total)}
            />
            {reading.signIns.refused > 0 && (
              <Status
                tone="warning"
                label={`${plural(reading.signIns.refused, "sign-in")} refused`}
              />
            )}
          </>
        )
      }
      facts={
        failed ? (
          <span className={FACTS}>The last day could not be read.</span>
        ) : !day || !reading ? (
          <span className={FACTS}>
            <Skeleton className="my-0.5 h-3 w-96 max-w-full" />
          </span>
        ) : (
          <span className={FACTS}>
            {host?.hostname && (
              <>
                <HostFact product={platformProduct(host.platform)}>{host.hostname}</HostFact>
                <FactDot />
              </>
            )}
            <span className="numeric">
              <span className="text-foreground">{day.total.toLocaleString()}</span>{" "}
              {day.total === 1 ? "change" : "changes"} in the last day
            </span>
            {who && (
              <>
                <FactDot />
                <span className="inline-flex min-w-0 items-center gap-1.5">
                  <FaceRun names={reading.people} />
                  <span className="truncate">{who}</span>
                </span>
              </>
            )}
            <FactDot />
            {last ? (
              <span className="inline-flex min-w-0 items-center gap-1.5">
                <span className="shrink-0">
                  {plural(reading.signIns.ok, "sign-in")}, the last by{" "}
                  <span className="text-foreground">{last.username || "someone"}</span>
                  {last.ip && " from"}
                </span>
                {last.ip && <Address ip={last.ip} className="min-w-0" />}
                <span className="numeric shrink-0">{relativeTime(last.ts)}</span>
              </span>
            ) : (
              <span>nobody signed in</span>
            )}
          </span>
        )
      }
      command={
        <span className="max-sm:hidden">
          <WorkspaceHelp compact />
        </span>
      }
      menu={<VerbMenu verbs={verbs} label="Audit log actions" />}
    />
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

/**
 * Who acted, as themselves, and where they acted from, on one line: the face,
 * the name, the address as the network it is on, and the key or the session
 * they came through.
 *
 * The column is as wide as its widest row, and what it takes it takes from the
 * target. So below `2xl` a row that came through a key, the assistant or a hub
 * session says that in the place of its address's digits and keeps the
 * network's mark: which key it was tells more than which address the key was
 * used from, and the address is in the entry the row opens.
 */
function Actor({ entry }: { entry: AuditEntry }) {
  const key = keyWords(entry)
  return (
    <div className="flex max-w-[15rem] min-w-0 items-center gap-2.5 2xl:max-w-[24rem]">
      <ActorMark entry={entry} />
      <span
        className={cn(
          "max-w-[9rem] shrink-0 truncate text-body",
          !entry.username && !isMachine(entry) && "text-muted-foreground",
        )}
      >
        {actorName(entry)}
      </span>
      <span className="flex min-w-0 items-center gap-1.5 text-hint text-muted-foreground">
        {entry.ip && (
          <Address
            ip={entry.ip}
            // The digits are `Address`'s second child, after the network's mark.
            className={cn("shrink-0", key && "max-2xl:[&>:nth-child(2)]:hidden")}
          />
        )}
        {key && (
          <span className="min-w-0 truncate" title={keyTitle(entry)}>
            · {key}
          </span>
        )}
      </span>
    </div>
  )
}

/**
 * A row's name as its button: the keyboard's place on the row (the arrows walk
 * from one to the next) and the way to the entry whole, in a popover beside
 * the row it is about, so the trail under it keeps its place.
 */
function EntryButton({
  entry,
  className,
  children,
}: {
  entry: AuditEntry
  className?: string
  children: React.ReactNode
}) {
  return (
    <Popover>
      <PopoverTrigger asChild>
        <button
          type="button"
          data-workspace-primary
          className={cn(
            "flex min-w-0 items-center gap-2.5 rounded-md text-left underline-offset-2 focus-ring hover:underline",
            className,
          )}
        >
          {children}
        </button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-[26rem]">
        <EntryDetail entry={entry} />
      </PopoverContent>
    </Popover>
  )
}

/** The trail before it is read: the table's own rows, at their height, in grey. */
function TrailSkeleton() {
  return (
    <div aria-busy className="divide-y divide-hairline" data-testid="audit-loading">
      {Array.from({ length: 8 }, (_, index) => (
        <div key={index} className="flex h-14 items-center gap-4 px-4">
          <Skeleton className="h-3 w-14 max-lg:hidden" />
          <Skeleton className="size-8 shrink-0 rounded-full max-lg:hidden" />
          <Skeleton className="h-3 w-24 max-lg:hidden" />
          <Skeleton className="size-8 shrink-0 rounded-md" />
          <Skeleton className="h-3 flex-1" style={{ maxWidth: `${30 + ((index * 13) % 30)}%` }} />
          <Skeleton className="ml-auto h-3 w-12" />
        </div>
      ))}
    </div>
  )
}
