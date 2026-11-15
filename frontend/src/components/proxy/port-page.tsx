"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import Link from "next/link"
import { useParams, useRouter, useSearchParams } from "next/navigation"
import { Tabs as TabsPrimitive } from "radix-ui"
import { ArrowLeft, ArrowUpRight, ChevronDown, ChevronUp, Router } from "@/components/icons"
import { get } from "@/lib/api"
import { plural, timestamp } from "@/lib/format"
import type { PortsFirewall, PortsMeta, SeenListener } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useSessionState, useViewState } from "@/lib/view-state"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { useConfirm } from "@/components/confirm-dialog"
import { IconAction } from "@/components/icon-action"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Page, PageContext, SearchInput } from "@/components/page"
import { EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { ChipCount, tabClasses } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { VerbBar } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import { Tabs, TabsContent } from "@/components/ui/tabs"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { Workspace, WorkspaceHelp } from "@/components/workspace/workspace"
import { processHref } from "@/components/procs/process-facts"
import {
  findPort,
  foldDualStack,
  ownerLabel,
  ownerLinks,
  ownerTitle,
  parsePortKey,
  portHref,
  PORTS_VIEW_KEYS,
  socketClients,
  socketPids,
  type Socket,
} from "@/components/proxy/ports"
import {
  DEFAULT_SORT,
  parseProtoFilter,
  parseQuery,
  parseReachFilter,
  parseSort,
  sortSockets,
  visibleSockets,
} from "@/components/proxy/ports-list"
import { firstSeen, isNew } from "@/components/proxy/ports-history"
import { NewMark, PortChanges } from "@/components/proxy/port-changes"
import { addressLine, ProcessMark, ReachWord } from "@/components/proxy/port-marks"
import { proxyWords } from "@/components/proxy/ports-proxy"
import { PortClients, PortOwner, PortReach } from "@/components/proxy/port-views"
import { PortPath } from "@/components/proxy/port-path"
import { socketMenuLabel, useSocketVerbs } from "@/components/proxy/port-verbs"
import { useProxy } from "@/components/proxy/proxy-context"
import type { Reading } from "@/components/proxy/freshness"

const TABS = ["reach", "owner", "clients", "history"] as const
type Tab = (typeof TABS)[number]

const BACK = (
  <Link
    href="/proxy/ports"
    className="inline-flex items-center gap-1 rounded-sm focus-ring hover:underline"
  >
    <ArrowLeft className="size-3" /> Ports
  </Link>
)

/**
 * One listening port, as a place of its own (`/proxy/ports/tcp-443`).
 *
 * It was a side sheet over the list until 0.8.7: the owner, the reach, the
 * firewall, the clients, the proof from outside and the identify form in a
 * sheet's width, over a list nobody could read behind it, and no history at
 * all. So a port is a destination with a way back, built the way a process
 * is (`procs/process-page.tsx`): the identity line — the owner as its
 * product, the port and who holds it, where it answers as the state — with
 * the socket's verbs above it, and its views as the underlined strip under
 * it: Reach (the path to it drawn, then why), Owner, Clients, History.
 *
 * What the sheet did well, the list beside the thing being read, is the rail
 * from `xl`: the list's own search, reach, protocol and sort, so the next
 * port is one press away, Alt+↑ and Alt+↓ walk it, and the view on screen
 * goes with you. On a phone the page is one pane that scrolls as a whole.
 *
 * A port that stops listening keeps its page: it says so, and its history
 * says when.
 */
export function PortPage() {
  const params = useParams<{ port: string }>()
  const search = useSearchParams()
  const router = useRouter()
  const key = useMemo(() => parsePortKey(decodeURIComponent(params.port ?? "")), [params.port])
  const requested = search.get("tab")
  const [remembered, remember] = useViewState<string>("proxy.port.tab", "reach")
  const [chosen, setChosen] = useState<string>(requested ?? remembered)
  const tab: Tab = (TABS as readonly string[]).includes(chosen) ? (chosen as Tab) : "reach"
  const setTab = useCallback(
    (next: string) => {
      setChosen(next)
      remember(next)
      // The address says which view, so a link to a port's clients opens on
      // them; replaced rather than pushed, because a view is not a place.
      const url = new URL(window.location.href)
      if (next === "reach") url.searchParams.delete("tab")
      else url.searchParams.set("tab", next)
      window.history.replaceState(null, "", `${url.pathname}${url.search}`)
    },
    [remember],
  )

  // The section's own read of the list, which the list page draws from too.
  const shared = useProxy().reads.ports
  const reading = shared.data as Reading<SeenListener[]> | undefined
  const all = useMemo(() => foldDualStack(reading?.value ?? []), [reading])
  const socket = key ? findPort(all, key) : undefined
  const rail = useRail(all)
  const open = useCallback(
    (next: Socket) => router.push(portHref(next, all, tab)),
    [router, all, tab],
  )
  const at = socket ? rail.rows.indexOf(socket) : -1
  const adjacent = (direction: number) => {
    const next = at >= 0 ? rail.rows[at + direction] : rail.rows[direction > 0 ? 0 : -1]
    if (next) open(next)
  }
  const previous = { disabled: at <= 0, run: () => adjacent(-1) }
  const next = {
    disabled: rail.rows.length === 0 || at >= rail.rows.length - 1,
    run: () => adjacent(1),
  }

  return (
    <Workspace
      name="Port"
      stateKey="proxy.port.page"
      refresh={shared.refresh}
      commands={[
        {
          id: "previous",
          label: "Previous port",
          keys: "Alt+↑",
          chord: "Alt+ArrowUp",
          disabled: previous.disabled,
          run: previous.run,
        },
        {
          id: "next",
          label: "Next port",
          keys: "Alt+↓",
          chord: "Alt+ArrowDown",
          disabled: next.disabled,
          run: next.run,
        },
      ]}
    >
      {/* Held to the window where the rail stands beside it, so the list and
          the identity line stay put while a view scrolls; narrower, the page
          is the one pane and scrolls as a whole. */}
      <Page fill="xl" className="gap-0 md:gap-0">
        <div className="flex min-w-0 flex-1 gap-6 xl:min-h-0">
          <PortRail
            className="hidden w-72 shrink-0 xl:flex"
            rows={rail.rows}
            all={all}
            current={socket}
            loading={!reading}
            query={rail.query}
            onQuery={rail.setQuery}
            filtered={rail.filtered}
            onClearFilters={rail.clearFilters}
            listedAt={reading?.at ?? 0}
            onOpen={open}
          />
          <PortMain
            key={key ? `${key.protocol}-${key.port}-${key.address ?? ""}` : "none"}
            portKey={key}
            socket={socket}
            loading={!reading}
            error={reading ? undefined : shared.error}
            listedAt={reading?.at ?? 0}
            onRetry={shared.refresh}
            tab={tab}
            setTab={setTab}
            previous={previous}
            next={next}
          />
        </div>
      </Page>
    </Workspace>
  )
}

/**
 * The list's rows, read for the rail: the same search, reach, protocol and
 * sort the list was left on (they are its session's), over the same reading.
 */
function useRail(all: Socket[]) {
  const [query, setQuery] = useSessionState(PORTS_VIEW_KEYS.query, "")
  const [reachValue, setReach] = useSessionState<string>(PORTS_VIEW_KEYS.reach, "all")
  const [protoValue, setProto] = useSessionState<string>(PORTS_VIEW_KEYS.proto, "all")
  const [sortValue] = useSessionState(PORTS_VIEW_KEYS.sort, "")
  const [hideEphemeral] = useViewState(PORTS_VIEW_KEYS.hideEphemeral, false)
  const meta = usePoll((signal) => get<PortsMeta>("/ports/meta", undefined, signal), 0, [], {
    enabled: hideEphemeral,
  })
  const reach = parseReachFilter(reachValue) ?? "all"
  const proto = parseProtoFilter(protoValue) ?? "all"
  const sort = parseSort(sortValue) ?? DEFAULT_SORT
  const range = hideEphemeral ? (meta.data?.ephemeralRange ?? null) : null
  const rows = useMemo(
    () =>
      sortSockets(
        visibleSockets(all, { terms: parseQuery(query), reach, proto, hide: range }),
        sort,
      ),
    // `sort` is rebuilt each render from its string.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [all, query, reach, proto, range, sortValue],
  )
  return {
    rows,
    query,
    setQuery,
    filtered: reach !== "all" || proto !== "all",
    clearFilters: () => {
      setReach("all")
      setProto("all")
    },
  }
}

type Step = { disabled: boolean; run: () => void }

function PortMain({
  portKey,
  socket,
  loading,
  error,
  listedAt,
  onRetry,
  tab,
  setTab,
  previous,
  next,
}: {
  portKey: ReturnType<typeof parsePortKey>
  socket: Socket | undefined
  loading: boolean
  error: Error | undefined
  listedAt: number
  onRetry: () => void
  tab: Tab
  setTab: (tab: string) => void
  previous: Step
  next: Step
}) {
  const { can } = useAuth()
  const admin = can("system.admin")
  const { confirm, dialog } = useConfirm()
  const proxy = useProxy()
  const firewall = usePoll(
    (signal) => get<PortsFirewall>("/ports/firewall", undefined, signal),
    15_000,
    [],
    { enabled: admin },
  )
  const verbsFor = useSocketVerbs({
    firewall: firewall.data,
    confirm,
    onDone: proxy.reads.ports.refresh,
  })
  const name = portKey ? `${portKey.port}/${portKey.protocol}` : "Port"
  const steps = (
    <span className="flex items-center">
      <IconAction label="Previous port" disabled={previous.disabled} onClick={previous.run}>
        <ChevronUp />
      </IconAction>
      <IconAction label="Next port" disabled={next.disabled} onClick={next.run}>
        <ChevronDown />
      </IconAction>
      <span className="max-sm:hidden">
        <WorkspaceHelp compact />
      </span>
    </span>
  )

  if (!portKey) {
    return (
      <div className="flex min-w-0 flex-1 flex-col gap-5">
        <PageContext eyebrow={BACK} title="Port" />
        <EmptyState
          icon={Router}
          title="That is not a port"
          description="A port's page is named by its protocol and number, such as tcp-443."
          action={
            <Button size="sm" variant="outline" asChild>
              <Link href="/proxy/ports">All ports</Link>
            </Button>
          }
        />
      </div>
    )
  }

  if (!socket) {
    return (
      <div className="flex min-w-0 flex-1 flex-col gap-5">
        <PageContext eyebrow={BACK} title={name} actions={steps} />
        <HostIdentity
          className="animate-rise"
          fallback={Router}
          title={<PortTitle port={portKey.port} protocol={portKey.protocol} />}
          facts={
            loading ? (
              <TextShimmer>Reading the sockets…</TextShimmer>
            ) : error ? (
              <span>couldn&apos;t read the sockets</span>
            ) : (
              <span>
                {portKey.address ? (
                  <span className="font-mono">{portKey.address}</span>
                ) : (
                  "any address"
                )}
              </span>
            )
          }
          aside={!loading && !error && <span className="text-body font-medium">not listening</span>}
        />
        {loading ? (
          <LoadingRows />
        ) : error ? (
          <ErrorState error={error} onRetry={onRetry} />
        ) : (
          <>
            <EmptyState
              icon={Router}
              title="Nothing listens there now"
              description={`No socket in the last listing is ${portKey.protocol.toUpperCase()} port ${portKey.port}${portKey.address ? ` on ${portKey.address}` : ""}. Its changes are below.`}
              action={
                <Button size="sm" variant="outline" asChild>
                  <Link href="/proxy/ports">All ports</Link>
                </Button>
              }
            />
            <PortChanges port={portKey} />
          </>
        )}
      </div>
    )
  }

  const clients = socketClients(socket)
  const views: { key: Tab; label: string; count?: number }[] = [
    { key: "reach", label: "Reach" },
    { key: "owner", label: "Owner" },
    { key: "clients", label: "Clients", count: clients?.count },
    { key: "history", label: "History" },
  ]

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-5">
      <div className="flex min-w-0 shrink-0 flex-col gap-5">
        <PageContext
          eyebrow={BACK}
          title={`${name} ${ownerTitle(socket)}`}
          actions={
            <>
              <VerbBar verbs={verbsFor(socket)} menuLabel={socketMenuLabel(socket)} />
              {steps}
            </>
          }
        />
        <PortIdentity socket={socket} listedAt={listedAt} />
      </div>

      <Tabs value={tab} onValueChange={setTab} className="min-h-0 flex-1 gap-0">
        <TabsPrimitive.List
          aria-label={`Views of port ${socket.port}`}
          className="flex shrink-0 [scrollbar-width:none] gap-1 overflow-x-auto border-b border-hairline [&::-webkit-scrollbar]:hidden"
        >
          {views.map((view) => (
            <TabsPrimitive.Trigger
              key={view.key}
              value={view.key}
              className={tabClasses(tab === view.key, "h-10")}
            >
              {view.label}
              {view.count !== undefined && view.count > 0 && <ChipCount>{view.count}</ChipCount>}
            </TabsPrimitive.Trigger>
          ))}
        </TabsPrimitive.List>

        <TabsContent value="reach" className="min-h-0 flex-1 space-y-8 pt-6 xl:overflow-y-auto">
          {tab === "reach" && (
            <>
              <PortPath socket={socket} />
              <PortReach socket={socket} admin={admin} />
            </>
          )}
        </TabsContent>
        <TabsContent value="owner" className="min-h-0 flex-1 pt-6 xl:overflow-y-auto">
          {tab === "owner" && <PortOwner socket={socket} />}
        </TabsContent>
        <TabsContent value="clients" className="min-h-0 flex-1 pt-6 xl:overflow-y-auto">
          {tab === "clients" && <PortClients socket={socket} />}
        </TabsContent>
        <TabsContent value="history" className="min-h-0 flex-1 pt-6 xl:overflow-y-auto">
          {tab === "history" && <PortChanges port={socket} />}
        </TabsContent>
      </Tabs>
      {dialog}
    </div>
  )
}

function PortTitle({ port, protocol }: { port: number; protocol: string }) {
  return (
    <span className="inline-flex min-w-0 items-baseline gap-2">
      <span className="numeric font-mono">{port}</span>
      <span className="text-body font-normal text-muted-foreground uppercase">{protocol}</span>
    </span>
  )
}

/**
 * The line the page opens on: the owner as its product, the port and who
 * holds it, then the facts a port is opened to check — where it is bound,
 * the account and process, how the owner runs (a link to its page), what the
 * proxy does with it and when it was first seen — and where it answers at
 * the line's end, coloured as the list colours it.
 */
function PortIdentity({ socket, listedAt }: { socket: Socket; listedAt: number }) {
  const pids = socketPids(socket)
  const label = ownerLabel(socket)
  const link = ownerLinks(socket)[0]
  const proxy = proxyWords(socket)
  const seen = firstSeen(socket)
  return (
    <HostIdentity
      className="animate-rise border-b-0 pb-0"
      logo={<ProcessMark listener={socket} className="size-12 rounded-xl [&_img]:size-7" />}
      title={
        <span className="inline-flex min-w-0 items-baseline gap-2">
          <PortTitle port={socket.port} protocol={socket.protocol} />
          <span className="truncate">{ownerTitle(socket)}</span>
          {socket.self && <Tag>This dashboard</Tag>}
          <NewMark socket={socket} now={listedAt} />
        </span>
      }
      facts={
        <>
          <span className="font-mono">{addressLine(socket)}</span>
          {socket.source !== "docker-nat" && (
            <>
              <FactDot />
              <span className="font-mono">{socket.user || "unknown user"}</span>
              {pids.length > 0 && (
                <>
                  <FactDot />
                  <span className="numeric">
                    {pids.length > 1 ? "PIDs " : "PID "}
                    {pids.map((pid, i) => (
                      <span key={pid}>
                        {i > 0 && ", "}
                        <Link
                          href={processHref(pid)}
                          className="rounded-sm text-foreground focus-ring hover:underline"
                        >
                          {pid}
                        </Link>
                      </span>
                    ))}
                  </span>
                </>
              )}
            </>
          )}
          {label && (
            <>
              <FactDot />
              {link ? (
                <Link
                  href={link.href}
                  className="inline-flex min-w-0 items-center gap-1 rounded-sm text-foreground focus-ring hover:underline"
                >
                  <span className="truncate">{label}</span>
                  <ArrowUpRight aria-hidden className="size-3 shrink-0" />
                </Link>
              ) : (
                <span className="truncate">{label}</span>
              )}
            </>
          )}
          {proxy && (
            <>
              <FactDot />
              <span>{proxy}</span>
            </>
          )}
          {seen && (
            <>
              <FactDot />
              <span title={timestamp(seen)}>
                {isNew(socket, listedAt)
                  ? "first seen today"
                  : `listening since ${timestamp(seen)}`}
              </span>
            </>
          )}
        </>
      }
      aside={<ReachWord socket={socket} className="text-body" />}
    />
  )
}

/**
 * The list, folded into a column beside the port it opened: the same search,
 * reach, protocol and sort, so the next port is one press away. Each row is
 * the owner as its product, the port and protocol, the owner's name and where
 * it answers; the one on screen is filled.
 */
function PortRail({
  rows,
  all,
  current,
  loading,
  query,
  onQuery,
  filtered,
  onClearFilters,
  listedAt,
  onOpen,
  className,
}: {
  rows: Socket[]
  all: Socket[]
  current: Socket | undefined
  loading: boolean
  query: string
  onQuery: (value: string) => void
  filtered: boolean
  onClearFilters: () => void
  listedAt: number
  onOpen: (socket: Socket) => void
  className?: string
}) {
  const listed = current !== undefined && rows.includes(current)
  const pinned = !listed ? current : undefined
  const list = useRef<HTMLOListElement>(null)
  // The port on screen is kept in view: opened from a link or by Alt+↓ past
  // the column's edge, it would otherwise be off its own list.
  useEffect(() => {
    const row = list.current?.querySelector<HTMLElement>("[aria-current='page']")
    row?.scrollIntoView({ block: "nearest" })
  }, [current])
  const caption =
    rows.length === all.length
      ? plural(all.length, "socket")
      : `${rows.length} of ${plural(all.length, "socket")}`
  return (
    <nav
      aria-label="Ports"
      data-slot="port-rail"
      className={cn("min-h-0 flex-col border-r border-hairline pr-4", className)}
    >
      <div className="flex shrink-0 flex-col gap-2 pb-3">
        <SearchInput
          value={query}
          onChange={(event) => onQuery(event.target.value)}
          placeholder="Port, process, user or address"
          aria-label="Search the ports"
          containerClassName="sm:w-full"
        />
        <p className="flex min-w-0 items-center justify-between gap-2 text-hint text-muted-foreground">
          <span className="truncate">{loading ? "Reading the sockets…" : caption}</span>
          {filtered && (
            <button
              type="button"
              onClick={onClearFilters}
              className="shrink-0 rounded-sm font-medium focus-ring hover:text-foreground"
            >
              Clear filters
            </button>
          )}
        </p>
      </div>
      <ol
        ref={list}
        aria-label="Ports to open"
        className="-mx-2 min-h-0 flex-1 [scrollbar-width:thin] space-y-0.5 overflow-y-auto px-2 pb-2"
      >
        {pinned && <RailRow socket={pinned} current listedAt={listedAt} onOpen={onOpen} />}
        {pinned && rows.length > 0 && (
          <li aria-hidden className="mx-3 my-1.5 border-t border-hairline" />
        )}
        {rows.map((row) => (
          <RailRow
            key={`${row.protocol}-${row.family}-${row.address}-${row.port}`}
            socket={row}
            current={row === current}
            listedAt={listedAt}
            onOpen={onOpen}
          />
        ))}
        {loading && rows.length === 0 && <LoadingRows rows={8} />}
        {!loading && rows.length === 0 && !pinned && (
          <li className="px-3 py-4 text-hint text-muted-foreground">No socket matches.</li>
        )}
      </ol>
    </nav>
  )
}

function RailRow({
  socket,
  current,
  onOpen,
}: {
  socket: Socket
  current: boolean
  listedAt: number
  onOpen: (socket: Socket) => void
}) {
  return (
    <li className="min-w-0">
      <button
        type="button"
        data-workspace-item={`${socket.protocol}-${socket.address}-${socket.port}`}
        data-workspace-name={`${socket.port} ${ownerTitle(socket)}`}
        aria-current={current ? "page" : undefined}
        aria-label={`${socket.port}/${socket.protocol}, ${ownerTitle(socket)}`}
        onClick={() => onOpen(socket)}
        className={cn(
          "flex h-12 w-full min-w-0 items-center gap-2.5 rounded-lg px-2 text-left focus-ring-inset transition-colors",
          current ? "bg-accent" : "hover:bg-row-hover",
        )}
      >
        <ProcessMark listener={socket} className="size-7 rounded-md [&_img]:size-4" />
        <span className="min-w-0 flex-1">
          <span className="flex min-w-0 items-baseline gap-1.5">
            <span
              className={cn(
                "numeric shrink-0 font-mono text-body",
                current ? "font-semibold" : "font-medium",
              )}
            >
              {socket.port}
            </span>
            <span className="shrink-0 text-micro text-muted-foreground uppercase">
              {socket.protocol}
            </span>
            <span className="truncate text-body">{ownerTitle(socket)}</span>
          </span>
          <ReachWord socket={socket} className="max-w-full [&_.font-mono]:hidden" />
        </span>
      </button>
    </li>
  )
}
