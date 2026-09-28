"use client"

import { useMemo, useState } from "react"
import { Clock, Router, Warning } from "@/components/icons"
import { errorMessage, get } from "@/lib/api"
import { calendarDate, clockMinute, plural, timestamp } from "@/lib/format"
import type { PortEvent, PortHistory } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useSessionState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { Toolbar } from "@/components/page"
import { Panel, PanelBody, PanelFooter, PanelHeader } from "@/components/panel"
import { ProductLogo, portProduct, processProduct } from "@/components/product-logo"
import { ROW_BLEED } from "@/components/row-list"
import { EmptyState, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { Segments } from "@/components/deploy/settings/segments"
import { Button } from "@/components/ui/button"
import {
  dangerousService,
  ownerTitle,
  reachGroup,
  reachVerdict,
  reachWords,
  socketPids,
} from "@/components/proxy/ports"
import {
  matchesQuery,
  parseQuery,
  parseReachFilter,
  type ReachFilter,
} from "@/components/proxy/ports-list"
import {
  changeAddresses,
  changesByDay,
  changeWhen,
  firstSeen,
  foldChanges,
  heldFor,
  isNew,
  seenWords,
  tallyChanges,
  type DatedSocket,
  type PortChange,
} from "@/components/proxy/ports-history"

const POLL_MS = 60_000

/** How many lines the list draws before it asks to draw more. */
const PAGE = 60

const WINDOWS = [
  { value: "24", label: "24 hours" },
  { value: "168", label: "7 days" },
  { value: "720", label: "30 days" },
] as const

type Window = (typeof WINDOWS)[number]["value"]

const WINDOW_WORDS: Record<Window, string> = {
  "24": "day",
  "168": "week",
  "720": "thirty days",
}

const REACH_LABEL: Record<ReachFilter, string> = {
  all: "All",
  internet: "Internet-facing",
  private: "Private networks",
  local: "This server",
}

const KIND_WORD: Record<PortChange["kind"], string> = {
  opened: "Opened",
  closed: "Closed",
  replaced: "New owner",
}

/**
 * The "new" mark on a listening socket the history first saw open in the last
 * day, beside its port as a fixed property of the row (§4) — with the minute
 * for the pointer and for a screen reader.
 */
export function NewMark({ socket, now }: { socket: DatedSocket; now: number }) {
  const seen = firstSeen(socket)
  if (!seen || !isNew(socket, now)) return null
  return (
    <Tag title={`First seen listening ${timestamp(seen)}`}>
      New<span className="sr-only">, first seen {seenWords(seen, now)}</span>
    </Tag>
  )
}

/**
 * What opened and closed on the host, and when: the ports history, which the
 * dashboard samples once a minute whether or not anybody is looking. The list
 * above says what is listening; this says since when, and what went.
 *
 * It follows the list's search, so a port looked up above — or linked to, as
 * the overview's findings do — is the port whose history is read here. On a
 * host where programs open fixed ports all day, a week's changes are
 * hundreds of lines, and one port's are a handful.
 */
export function PortChanges({ query, onClearQuery }: { query: string; onClearQuery: () => void }) {
  const [windowValue, setWindow] = useSessionState<string>("proxy.ports.changes.window", "24")
  const [reachValue, setReach] = useSessionState<string>("proxy.ports.changes.reach", "all")
  // Values another version of the page remembered read as the default.
  const hours: Window = WINDOWS.find((w) => w.value === windowValue)?.value ?? "24"
  const reach = parseReachFilter(reachValue) ?? "all"
  const search = query.trim()
  const terms = useMemo(() => parseQuery(search), [search])
  // How far the list has been paged, for the view it was paged in: another
  // window, reach or search starts from the first page again.
  const view = JSON.stringify([hours, reach, search])
  const [paging, setPaging] = useState({ view, count: PAGE })
  const shown = paging.view === view ? paging.count : PAGE

  const { data, error, loading, refresh } = usePoll(
    async (signal) => ({
      history: await get<PortHistory>("/ports/history", { hours }, signal),
      at: Date.now(),
    }),
    POLL_MS,
    [hours],
  )
  const history = data?.history
  const changes = useMemo(() => foldChanges(history?.events ?? []), [history])
  // A program that took a port over is found by either name.
  const searched = useMemo(
    () =>
      terms.length === 0
        ? changes
        : changes.filter(
            (change) =>
              matchesQuery(change.socket, terms) ||
              (change.previous !== undefined && matchesQuery(change.previous, terms)),
          ),
    [changes, terms],
  )
  const tally = useMemo(() => tallyChanges(searched), [searched])
  const matching = useMemo(
    () =>
      reach === "all" ? searched : searched.filter((change) => reachGroup(change.socket) === reach),
    [searched, reach],
  )
  const days = useMemo(
    () => changesByDay(matching.slice(0, shown), data?.at ?? 0),
    [matching, shown, data],
  )

  const body = (() => {
    if (loading && !data) return <LoadingRows rows={3} className="py-3" />
    if (error && !data) return <ErrorState error={error} onRetry={refresh} className="mt-3" />
    if (!history) return null
    if (!history.recordingSince) {
      return (
        <EmptyState
          icon={Clock}
          title="Recording has not started"
          description="The dashboard compares the host's listening sockets once a minute. The first comparison is where this history begins."
          className="mt-4"
        />
      )
    }
    if (changes.length === 0) {
      const began = Date.parse(history.recordingSince) > Date.parse(history.since)
      return (
        <EmptyState
          icon={Router}
          title={`Nothing opened or closed in the last ${WINDOW_WORDS[hours]}`}
          description={
            began
              ? `Recording began ${calendarDate(history.recordingSince)} at ${clockMinute(history.recordingSince)}, so nothing before then is known.`
              : "The listening sockets were compared once a minute while the dashboard ran."
          }
          className="mt-4"
        />
      )
    }
    if (searched.length === 0) {
      return (
        <EmptyState
          icon={Router}
          title="No changes match the search"
          description={`Nothing that opened or closed in the last ${WINDOW_WORDS[hours]} matches “${search}”.`}
          action={
            <Button variant="outline" size="sm" onClick={onClearQuery}>
              Clear the search
            </Button>
          }
          className="mt-4"
        />
      )
    }
    if (matching.length === 0) {
      return (
        <EmptyState
          icon={Router}
          title="No changes match"
          action={
            <Button variant="outline" size="sm" onClick={() => setReach("all")}>
              Show all changes
            </Button>
          }
          className="mt-4"
        />
      )
    }
    return (
      <div className="space-y-5 pt-3">
        {days.map(([day, lines]) => (
          <section key={day} aria-label={day}>
            <h3 className="border-b border-hairline pb-1.5 text-hint font-medium text-muted-foreground">
              {day}
            </h3>
            <ul className="divide-y divide-hairline">
              {lines.map((change) => (
                <ChangeRow
                  key={change.key}
                  change={change}
                  intervalSeconds={history.intervalSeconds}
                />
              ))}
            </ul>
          </section>
        ))}
        {matching.length > shown && (
          <div className="flex flex-wrap items-center gap-3">
            <Button
              variant="outline"
              size="sm"
              onClick={() => setPaging({ view, count: shown + PAGE })}
            >
              Show {Math.min(PAGE, matching.length - shown)} more
            </Button>
            <span className="numeric text-hint text-muted-foreground">
              {shown} of {plural(matching.length, "change")} shown
            </span>
          </div>
        )}
      </div>
    )
  })()

  return (
    <Panel plain id="changes">
      <PanelHeader
        title="Changes"
        actions={
          <Segments
            label="Changes from the last"
            value={hours}
            options={WINDOWS.map((w) => ({ value: w.value, label: w.label }))}
            onChange={setWindow}
          />
        }
      />
      {history?.stalled && history.lastSample && (
        <Notice
          tone="warning"
          icon={Warning}
          title={`No sample since ${calendarDate(history.lastSample)} at ${clockMinute(history.lastSample)}`}
          className="mt-3"
        >
          <p>
            The dashboard has not been able to compare the listening sockets since then, so nothing
            that opened or closed after it is here. Its own log says why.
          </p>
        </Notice>
      )}
      {error && data && (
        <Notice
          tone="warning"
          icon={Warning}
          title="Refreshing the changes failed, so these are the last that arrived"
          className="mt-3"
        >
          <p>{errorMessage(error)}</p>
          <Button variant="outline" size="xs" className="mt-2" onClick={refresh}>
            Try again
          </Button>
        </Notice>
      )}
      {changes.length > 0 && (
        <Toolbar className="mt-3 gap-x-3">
          <ChipStrip role="group" aria-label="Changes by reach">
            {(Object.keys(REACH_LABEL) as ReachFilter[])
              .filter((key) => key === "all" || key === reach || tally[key] > 0)
              .map((key) => (
                <FilterChip key={key} selected={reach === key} onClick={() => setReach(key)}>
                  {REACH_LABEL[key]} <ChipCount>{tally[key]}</ChipCount>
                </FilterChip>
              ))}
          </ChipStrip>
          {search && (
            <span className="min-w-0 text-hint wrap-anywhere text-muted-foreground">
              Matching the search <span className="font-mono">{search}</span>
            </span>
          )}
        </Toolbar>
      )}
      <PanelBody flush>{body}</PanelBody>
      {history?.recordingSince && (
        <PanelFooter className="mt-3 text-hint text-muted-foreground">
          <span>
            Compared once a minute, kept {history.retentionDays} days, recording since{" "}
            {calendarDate(history.recordingSince)}
          </span>
          {history.truncated && (
            <>
              <span className="text-muted-foreground/40">·</span>
              <span>
                Only the newest {plural(history.events.length, "event")} of the{" "}
                {WINDOW_WORDS[hours]} are listed
              </span>
            </>
          )}
        </PanelFooter>
      )}
    </Panel>
  )
}

/**
 * One change: when, what it did, the port and addresses, the program behind
 * it, and where the socket answered. An opening takes the colour the list
 * gives the socket — the posture's for a database, a warning for anything the
 * internet reaches — and a closing none, since a port going is not a risk.
 */
function ChangeRow({ change, intervalSeconds }: { change: PortChange; intervalSeconds: number }) {
  const { socket, previous } = change
  const when = changeWhen(change, intervalSeconds)
  const closing = change.kind === "closed"
  const verdict = closing ? undefined : reachVerdict(socket)
  const service = dangerousService(socket)
  const pids = socketPids(socket)
  const held = heldFor(change)
  const owner = [
    ownerTitle(socket),
    socket.user,
    pids.length > 0 ? `${pids.length > 1 ? "PIDs" : "PID"} ${pids.join(", ")}` : undefined,
    held,
  ].filter(Boolean)
  return (
    <li
      className={cn(
        "flex min-w-0 items-start gap-3 py-2.5 transition-colors hover:bg-row-hover",
        ROW_BLEED,
      )}
    >
      <time
        dateTime={change.at}
        title={timestamp(change.at)}
        className="numeric w-11 shrink-0 pt-px font-mono text-hint text-muted-foreground"
      >
        {when.time}
      </time>
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-0.5">
          <Status
            verdict={verdict}
            tone={verdict ? undefined : closing ? "stopped" : "running"}
            label={KIND_WORD[change.kind]}
          />
          <span className="numeric font-mono text-body font-semibold">{socket.port}</span>
          <span className="text-hint text-muted-foreground uppercase">{socket.protocol}</span>
          <span className="min-w-0 font-mono text-hint wrap-anywhere text-muted-foreground">
            {changeAddresses(change).join(", ")}
          </span>
          <span className="ml-auto text-hint text-muted-foreground">
            {service ? `${service} · ${reachWords(socket)}` : reachWords(socket)}
          </span>
        </div>
        <p className="mt-1 flex min-w-0 items-center gap-2 text-hint text-muted-foreground">
          <ProcessMark listener={socket} />
          {/* Wrapped rather than cut: how long a socket listened ends the line. */}
          <span className="min-w-0 break-words" title={socket.cmdline || undefined}>
            {owner.join(" · ")}
          </span>
        </p>
        {previous && (
          <p className="mt-0.5 text-hint text-muted-foreground">
            Held before by {ownerTitle(previous)}
            {previous.user && ` · ${previous.user}`}
            {previous.pid > 0 && ` · PID ${previous.pid}`}
          </p>
        )}
        {when.span && <p className="mt-0.5 text-hint text-muted-foreground">{when.span}</p>}
      </div>
    </li>
  )
}

/** The program as the product it is, as the list of listening sockets draws it. */
function ProcessMark({ listener }: { listener: Pick<PortEvent, "process" | "port"> }) {
  const product = processProduct(listener.process ?? "") ?? portProduct(listener.port)
  return <ProductLogo id={product} size="sm" fallback={Router} />
}
