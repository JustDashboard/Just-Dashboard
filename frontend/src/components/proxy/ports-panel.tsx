"use client"

import { Suspense, useEffect, useMemo, useState } from "react"
import Link from "next/link"
import { useRouter, useSearchParams } from "next/navigation"
import {
  AcronymCsv,
  AcronymJson,
  ChevronDown,
  ChevronRight,
  ChevronUp,
  Pause,
  Play,
  RefreshClockwise,
  Router,
  Warning,
} from "@/components/icons"
import { errorMessage, get } from "@/lib/api"
import { plural } from "@/lib/format"
import { downloadText } from "@/lib/metrics-export"
import type { PortsFirewall, PortsMeta, SeenListener } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useSessionState, useViewState } from "@/lib/view-state"
import { useMediaQuery } from "@/hooks/use-mobile"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { useNow } from "@/components/deploy/vocabulary"
import { useConfirm } from "@/components/confirm-dialog"
import { InfoTip } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Page, PageContext, SearchInput, Toolbar } from "@/components/page"
import { Panel, PanelBody, PanelFooter, PanelHeader } from "@/components/panel"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { EmptyState, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { VerbActions, VerbMenu } from "@/components/verbs"
import {
  dangerousPorts,
  foldDualStack,
  internetHint,
  keyHref,
  onUplink,
  ownerLabel,
  ownerTitle,
  parseSocketParam,
  PORTS_VIEW_KEYS,
  portHref,
  privateHint,
  socketClients,
  socketPids,
  tallyReach,
  type Socket,
} from "@/components/proxy/ports"
import { firewallWords, noFirewallWords, OrphanRules } from "@/components/proxy/ports-firewall"
import { FreePortFinder, proxyWords } from "@/components/proxy/ports-proxy"
import {
  DEFAULT_SORT,
  facetCounts,
  FIRST_DIRECTION,
  formatEndpoint,
  groupByOwner,
  groupPids,
  groupPorts,
  isLoopbackEphemeral,
  parseProtoFilter,
  parseQuery,
  parseReachFilter,
  parseSort,
  sinceWords,
  sortParam,
  sortSockets,
  toCsv,
  toJson,
  viewFromParams,
  visibleSockets,
  withView,
  worstSocket,
  type PortSort,
  type ProtoFilter,
  type ReachFilter,
  type SocketGroup,
  type SortKey,
} from "@/components/proxy/ports-list"
import { isNew } from "@/components/proxy/ports-history"
import { NewMark, PortChanges } from "@/components/proxy/port-changes"
import { addressLine, NAT_WORDS, ProcessMark, ReachWord } from "@/components/proxy/port-marks"
import { socketMenuLabel, useSocketVerbs } from "@/components/proxy/port-verbs"
import { PortsPicture } from "@/components/proxy/ports-picture"
import { useProxy } from "@/components/proxy/proxy-context"
import type { Reading } from "@/components/proxy/freshness"
import { Button } from "@/components/ui/button"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  stickyTableHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

/** The firewall's verdicts are read at the list's own cadence. */
const POLL_MS = 15_000

const REACH_LABEL: Record<ReachFilter, string> = {
  all: "All",
  internet: "Internet-facing",
  private: "Private networks",
  local: "This server",
}

const PROTO_LABEL: Record<Exclude<ProtoFilter, "all">, string> = { tcp: "TCP", udp: "UDP" }

/** The phone's sort choices: the table's three columns, each way round. */
const SORT_CHOICES: { value: string; label: string }[] = [
  { value: "-reach", label: "Worst first" },
  { value: "port", label: "Port, low to high" },
  { value: "-port", label: "Port, high to low" },
  { value: "process", label: "Application, A to Z" },
  { value: "-process", label: "Application, Z to A" },
  { value: "reach", label: "Safest first" },
]

/** A row of the list: one socket, or one application's sockets folded under its name. */
type Entry =
  | { kind: "socket"; socket: Socket; member: boolean }
  | { kind: "group"; group: SocketGroup; open: boolean }

const header = <PageContext title="Listening ports" />

/**
 * Every listening socket on the host, and who can reach it.
 *
 * It opens on what it is about — the line of what listens and what faces the
 * internet — then the picture of who reaches what: each network a socket
 * answers on, wired to the programs listening there, a database the
 * internet reaches in red. Under it the list, read down its columns (who
 * owns each socket, who can reach it, what the firewall does, who is
 * connected), one line per socket with the port as the way to its page
 * (`port-page.tsx`), which holds the rest: the reach in full, the owner, the
 * clients, and its history. Under the list, what opened and closed.
 *
 * It opened on four tiles — listening, internet-facing, private networks,
 * dangerous services exposed — that the reach chips repeated one row down.
 * Their figures are the identity line's facts now, and the chips keep theirs.
 *
 * The view — search, reach, protocol and sort — is in the address bar, so the
 * overview, Connections and the TLS report link into it already narrowed. The
 * side sheet's old address, `?socket=tcp:0.0.0.0:443`, opens the port's page.
 */
export function PortsPage() {
  // The view is read from the address bar, which the server does not have.
  return (
    <Suspense fallback={<PortsLoading />}>
      <PortsView />
    </Suspense>
  )
}

/** The page's shape while the list is read: the identity line where it will stand. */
function PortsLoading() {
  return (
    <Page>
      {header}
      <HostIdentity
        fallback={Router}
        title="Listening ports"
        facts={<TextShimmer>Reading the sockets…</TextShimmer>}
      />
      <LoadingRows rows={7} />
    </Page>
  )
}

function PortsView() {
  const router = useRouter()
  const params = useSearchParams()
  // The side sheet's address, from before a port had a page of its own.
  const legacy = params.get("socket")
  useEffect(() => {
    if (legacy === null) return
    const key = parseSocketParam(legacy)
    if (key) router.replace(keyHref(key))
  }, [legacy, router])

  const arrival = viewFromParams(params)
  const [query, setQuery] = useSessionState(PORTS_VIEW_KEYS.query, "", arrival?.q)
  const [reachValue, setReach] = useSessionState<string>(
    PORTS_VIEW_KEYS.reach,
    "all",
    arrival?.reach,
  )
  const [protoValue, setProto] = useSessionState<string>(
    PORTS_VIEW_KEYS.proto,
    "all",
    arrival?.proto,
  )
  const [sortValue, setSort] = useSessionState(
    PORTS_VIEW_KEYS.sort,
    sortParam(DEFAULT_SORT),
    arrival ? sortParam(arrival.sort) : null,
  )
  // Values another version of the page remembered read as the default.
  const reach = parseReachFilter(reachValue) ?? "all"
  const proto = parseProtoFilter(protoValue) ?? "all"
  const sort = useMemo(() => parseSort(sortValue) ?? DEFAULT_SORT, [sortValue])
  const [grouped, setGrouped] = useViewState("proxy.ports.grouped", false)
  const [hideEphemeral, setHideEphemeral] = useViewState(PORTS_VIEW_KEYS.hideEphemeral, false)
  const [onlyNew, setOnlyNew] = useSessionState(PORTS_VIEW_KEYS.onlyNew, false)
  const [open, setOpen] = useState<ReadonlySet<string>>(new Set())
  const wide = useMediaQuery("(min-width: 1024px)")

  // The section's own read of the list, which the overview and the rail
  // answer from too: the page asking again on a timer of its own was a
  // second read of the same list every fifteen seconds. Paused, the rows on
  // screen are the reading the pause was pressed on.
  const shared = useProxy().reads.ports
  const live = shared.data as Reading<SeenListener[]> | undefined
  const [paused, setPaused] = useState(false)
  const [held, setHeld] = useState<Reading<SeenListener[]>>()
  // A refresh is under way until a new reading or a new failure answers it.
  const [asked, setAsked] = useState<{ at?: number; error?: Error } | null>(null)
  const refreshing =
    asked !== null && asked.at === live?.at && asked.error === shared.error && !paused
  const data = paused ? (held ?? live) : live
  const error = paused ? undefined : shared.error
  const meta = usePoll((signal) => get<PortsMeta>("/ports/meta", undefined, signal), 0)
  const firewall = usePoll(
    (signal) => get<PortsFirewall>("/ports/firewall", undefined, signal),
    POLL_MS,
    [],
    { enabled: !paused },
  )
  const { can } = useAuth()
  const admin = can("system.admin")
  const { confirm, dialog } = useConfirm()
  const range = meta.data?.ephemeralRange ?? null

  useEffect(() => {
    if (legacy !== null) return
    // Written a moment after the last keystroke: Safari refuses a page more
    // than a hundred history writes in thirty seconds.
    const timer = setTimeout(() => {
      const url = new URL(window.location.href)
      const next = withView(url.search, { q: query, reach, proto, sort })
      if (next !== url.search) {
        window.history.replaceState(null, "", `${url.pathname}${next}${url.hash}`)
      }
    }, 250)
    return () => clearTimeout(timer)
  }, [query, reach, proto, sort, legacy])

  const listeners = useMemo(() => data?.value ?? [], [data])
  // A service on 0.0.0.0 and :: is one row and one count.
  const all = useMemo(() => foldDualStack(listeners), [listeners])
  const counts = useMemo(() => tallyReach(all), [all])
  const onTheUplink = useMemo(() => all.filter(onUplink).length, [all])
  const dangerous = useMemo(() => dangerousPorts(listeners), [listeners])
  const ephemeralOnHost = useMemo(
    () => all.filter((socket) => isLoopbackEphemeral(socket, range)).length,
    [all, range],
  )
  const filters = useMemo(
    () => ({ terms: parseQuery(query), reach, proto, hide: hideEphemeral ? range : null }),
    [query, reach, proto, hideEphemeral, range],
  )
  // What the ports history first saw open in the last day, dated when the
  // list arrived; "New in 24 hours" narrows every chip's count to them.
  const listedAt = data?.at ?? 0
  const fresh = useMemo(() => all.filter((socket) => isNew(socket, listedAt)), [all, listedAt])
  const pool = onlyNew ? fresh : all
  const newCount = useMemo(() => visibleSockets(fresh, filters).length, [fresh, filters])
  const facets = useMemo(() => facetCounts(pool, filters, range), [pool, filters, range])
  const visible = useMemo(
    () => sortSockets(visibleSockets(pool, filters), sort),
    [pool, filters, sort],
  )
  const hidden = filters.hide ? facets.ephemeral : 0
  const entries = useMemo<Entry[]>(() => {
    if (!grouped) return visible.map((socket) => ({ kind: "socket", socket, member: false }))
    return groupByOwner(visible).flatMap((group): Entry[] => {
      if (group.sockets.length === 1) {
        return [{ kind: "socket", socket: group.sockets[0], member: false }]
      }
      const isOpen = open.has(group.key)
      const members = isOpen
        ? group.sockets.map((socket): Entry => ({ kind: "socket", socket, member: true }))
        : []
      return [{ kind: "group", group, open: isOpen }, ...members]
    })
  }, [grouped, visible, open])

  const filtered = query.trim() !== "" || reach !== "all" || proto !== "all" || onlyNew
  const clearFilters = () => {
    setQuery("")
    setReach("all")
    setProto("all")
    setOnlyNew(false)
  }
  const toggleGroup = (key: string) =>
    setOpen((previous) => {
      const next = new Set(previous)
      if (!next.delete(key)) next.add(key)
      return next
    })
  const chooseSort = (key: SortKey) =>
    setSort(
      sortParam(
        sort.key === key
          ? { key, dir: sort.dir === "asc" ? "desc" : "asc" }
          : { key, dir: FIRST_DIRECTION[key] },
      ),
    )
  const refreshNow = () => {
    setAsked({ at: live?.at, error: shared.error })
    setPaused(false)
    shared.refresh()
    firewall.refresh()
  }
  const pause = () => {
    setHeld(live)
    setAsked(null)
    setPaused(true)
  }
  const exportRows = (format: "csv" | "json") => {
    const stamp = new Date().toISOString().slice(0, 16).replace(/[:T]/g, "-")
    if (format === "csv") downloadText(`listening-sockets-${stamp}.csv`, toCsv(visible))
    else downloadText(`listening-sockets-${stamp}.json`, toJson(visible), "application/json")
  }
  const verbsFor = useSocketVerbs({ firewall: firewall.data, confirm, onDone: refreshNow })
  const open_ = (socket: Socket) => router.push(portHref(socket, all))
  // A click anywhere on a row opens it, except on a control of its own.
  const rowClick = (socket: Socket) => (event: React.MouseEvent) => {
    if ((event.target as HTMLElement).closest("button, a, [role=menuitem]")) return
    open_(socket)
  }

  if (legacy !== null || (!data && (shared.loading || !shared.error))) return <PortsLoading />
  if (!data) {
    return (
      <Page>
        {header}
        <Identity facts={<span>couldn&apos;t read the sockets</span>} />
        <ErrorState
          error={shared.error ?? new Error("The sockets could not be read.")}
          onRetry={refreshNow}
        />
      </Page>
    )
  }

  const internetTone =
    counts.internet > 0 ? "text-warning" : onTheUplink > 0 ? undefined : "text-success"
  const dangerTone = dangerous.some((d) => d.level === "critical")
    ? "font-medium text-destructive"
    : "text-warning"

  const empty = (() => {
    if (all.length === 0) {
      return (
        <EmptyState
          icon={Router}
          title="Nothing is listening"
          description="The kernel's socket tables list no listening TCP socket and no bound UDP socket."
          className="mt-4"
        />
      )
    }
    const showHidden = hidden > 0 && (
      <Button variant="outline" size="sm" onClick={() => setHideEphemeral(false)}>
        Show {plural(hidden, "hidden socket")}
      </Button>
    )
    if (!filtered) {
      return (
        <EmptyState
          icon={Router}
          title="Only loopback ephemeral ports are listening"
          description="Every socket is on loopback, on a port the kernel handed out."
          action={showHidden}
          className="mt-4"
        />
      )
    }
    return (
      <EmptyState
        icon={Router}
        title="No sockets match"
        description={
          hidden > 0
            ? `${plural(hidden, "loopback socket")} on ephemeral ports would, but ${hidden === 1 ? "it is" : "they are"} hidden.`
            : undefined
        }
        action={
          <div className="flex flex-wrap justify-center gap-2">
            <Button variant="outline" size="sm" onClick={clearFilters}>
              Clear filters
            </Button>
            {showHidden}
          </div>
        }
        className="mt-4"
      />
    )
  })()

  return (
    <Page className="animate-rise">
      {header}

      <Identity
        facts={
          <>
            <span className="numeric text-foreground">{counts.all} listening</span>
            <span className="numeric">
              {counts.tcp} TCP, {counts.udp} UDP
            </span>
            <FactDot />
            <span className={cn("numeric", internetTone)} title={internetHint(all)}>
              {counts.internet > 0
                ? `${counts.internet} internet-facing`
                : onTheUplink > 0
                  ? `${onTheUplink} internet-facing if the uplink is mapped`
                  : "nothing the internet can reach"}
            </span>
            {counts.private > 0 && (
              <>
                <FactDot />
                <span className="numeric" title={privateHint(all)}>
                  {counts.private} on private networks
                </span>
              </>
            )}
            {dangerous.length > 0 && (
              <>
                <FactDot />
                <span className={cn("numeric", dangerTone)} title="Counted once per port">
                  {plural(dangerous.length, "dangerous service")} exposed
                </span>
              </>
            )}
            <FactDot />
            <Freshness
              at={data.at}
              paused={paused}
              refreshing={refreshing}
              stale={Boolean(shared.error)}
            />
          </>
        }
        aside={
          <div className="flex flex-wrap items-center gap-x-2 gap-y-2">
            <span className="flex items-center">
              {!paused && (
                <IconAction label="Refresh now" pending={refreshing} onClick={refreshNow}>
                  <RefreshClockwise />
                </IconAction>
              )}
              <IconAction
                label={paused ? "Resume refreshing" : "Pause refreshing"}
                aria-pressed={paused}
                onClick={paused ? refreshNow : pause}
              >
                {paused ? <Play /> : <Pause />}
              </IconAction>
              <VerbMenu
                label="More actions for the list"
                verbs={[
                  {
                    key: "csv",
                    label: "Export as CSV",
                    icon: AcronymCsv,
                    disabled: visible.length === 0,
                    run: () => exportRows("csv"),
                  },
                  {
                    key: "json",
                    label: "Export as JSON",
                    icon: AcronymJson,
                    disabled: visible.length === 0,
                    run: () => exportRows("json"),
                  },
                ]}
              />
            </span>
            {admin && <FreePortFinder />}
          </div>
        }
      />

      {error && (
        // The rows below are the last list that arrived; saying nothing
        // would let an incident be read off a list minutes old.
        <Notice
          tone="warning"
          icon={Warning}
          title="Refreshing failed, so this is the last list that arrived"
        >
          <p>
            {errorMessage(error)} The page tries again every {POLL_MS / 1000} seconds.
          </p>
          <Button variant="outline" size="xs" className="mt-2" onClick={refreshNow}>
            Try again
          </Button>
        </Notice>
      )}

      {all.length > 0 && (
        <PortsPicture
          sockets={all}
          all={all}
          reach={reach}
          onReach={(next) => {
            setReach(next)
            setOnlyNew(false)
          }}
        />
      )}

      <Panel plain>
        <PanelHeader
          title="Listening sockets"
          actions={
            <span className="numeric text-hint text-muted-foreground">
              {visible.length === all.length ? all.length : `${visible.length} of ${all.length}`}
            </span>
          }
        />
        <Toolbar className="justify-between gap-x-4">
          <SearchInput
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Port, process, user or address"
            aria-label="Search sockets"
            className="pr-8"
            trailing={
              <span className="flex size-7 items-center justify-center">
                <InfoTip label="Search syntax">
                  Words match the port, process, command, user, address and reach. Narrow by field
                  with <code>:443</code>, <code>[::]:22</code>, <code>0.0.0.0:80</code>,{" "}
                  <code>port:8000-8100</code>, <code>port:5432,6379</code>, <code>proto:udp</code>,{" "}
                  <code>user:postgres</code> or <code>pid:812</code>.
                </InfoTip>
              </span>
            }
          />
          <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2">
            <ChipStrip role="group" aria-label="Reach">
              {(Object.keys(REACH_LABEL) as ReachFilter[])
                .filter((key) => key === "all" || key === reach || facets.reach[key] > 0)
                .map((key) => (
                  <FilterChip key={key} selected={reach === key} onClick={() => setReach(key)}>
                    {REACH_LABEL[key]} <ChipCount>{facets.reach[key]}</ChipCount>
                  </FilterChip>
                ))}
            </ChipStrip>
            <ChipStrip role="group" aria-label="Protocol">
              {(Object.keys(PROTO_LABEL) as Exclude<ProtoFilter, "all">[])
                .filter((key) => key === proto || facets.proto[key] > 0)
                .map((key) => (
                  // A second press on the chosen protocol lists both again.
                  <FilterChip
                    key={key}
                    selected={proto === key}
                    onClick={() => setProto(proto === key ? "all" : key)}
                  >
                    {PROTO_LABEL[key]} <ChipCount>{facets.proto[key]}</ChipCount>
                  </FilterChip>
                ))}
            </ChipStrip>
          </div>
        </Toolbar>
        <Toolbar className="mt-2 gap-x-3">
          <FilterChip selected={grouped} onClick={() => setGrouped(!grouped)}>
            Group by application
          </FilterChip>
          {(fresh.length > 0 || onlyNew) && (
            <FilterChip
              selected={onlyNew}
              onClick={() => setOnlyNew(!onlyNew)}
              title="Sockets the ports history first saw listening in the last 24 hours"
            >
              New in 24 hours <ChipCount>{newCount}</ChipCount>
            </FilterChip>
          )}
          {range && ephemeralOnHost > 0 && (
            <FilterChip
              selected={hideEphemeral}
              onClick={() => setHideEphemeral(!hideEphemeral)}
              title={`Loopback sockets on ports ${range.low}–${range.high}, which the kernel hands to a program that asks for any port`}
            >
              Hide loopback ephemeral ports <ChipCount>{facets.ephemeral}</ChipCount>
            </FilterChip>
          )}
          {!wide && (
            <div className="ml-auto">
              <Select value={sortParam(sort)} onValueChange={setSort}>
                <SelectTrigger size="sm" className="w-48" aria-label="Sort">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {SORT_CHOICES.map((choice) => (
                    <SelectItem key={choice.value} value={choice.value}>
                      {choice.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          )}
        </Toolbar>
        <PanelBody flush>
          {visible.length === 0 ? (
            empty
          ) : wide ? (
            // The grid scrolls independently, so its border marks that boundary.
            <Table
              className="table-fixed"
              containerClassName="max-h-[calc(100svh-12rem)] rounded-xl border bg-card"
            >
              <TableHeader className={stickyTableHeader}>
                <TableRow>
                  <SortHead
                    label="Endpoint"
                    column="port"
                    sort={sort}
                    onSort={chooseSort}
                    className="w-[24%]"
                  />
                  <SortHead label="Application" column="process" sort={sort} onSort={chooseSort} />
                  <SortHead
                    label="Reach"
                    column="reach"
                    sort={sort}
                    onSort={chooseSort}
                    className="w-[22%]"
                  />
                  <TableHead className="w-[17%]">Firewall</TableHead>
                  <TableHead className="w-16 text-right">Clients</TableHead>
                  <TableHead className="w-24">
                    <span className="sr-only">Actions</span>
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {entries.map((entry) =>
                  entry.kind === "group" ? (
                    <TableRow
                      key={`group:${entry.group.key}`}
                      data-slot="port-row"
                      // The row is the pointer's target; the keyboard's is the
                      // one button in it, so the row is not a second tab stop.
                      className="group h-12 cursor-pointer"
                      onClick={(event) => {
                        if ((event.target as HTMLElement).closest("button")) return
                        toggleGroup(entry.group.key)
                      }}
                    >
                      <TableCell>
                        <p className="flex min-w-0 items-baseline gap-2">
                          <span className="shrink-0 text-body font-medium">
                            {plural(entry.group.sockets.length, "socket")}
                          </span>
                          <span className="truncate font-mono text-hint text-muted-foreground">
                            {groupPorts(entry.group)}
                          </span>
                        </p>
                      </TableCell>
                      <TableCell>
                        <GroupTitle
                          group={entry.group}
                          open={entry.open}
                          onToggle={() => toggleGroup(entry.group.key)}
                        />
                      </TableCell>
                      <TableCell>
                        <GroupReach group={entry.group} />
                      </TableCell>
                      <TableCell />
                      <TableCell />
                      <TableCell />
                    </TableRow>
                  ) : (
                    <TableRow
                      key={socketKey(entry.socket)}
                      data-slot="port-row"
                      className="group h-12 cursor-pointer"
                      onClick={rowClick(entry.socket)}
                    >
                      <TableCell>
                        {/* A group's sockets stand behind a rule under its line. */}
                        <div
                          className={cn(
                            "flex min-w-0 items-baseline gap-2",
                            entry.member && "border-l border-hairline pl-4",
                          )}
                        >
                          <PortLink socket={entry.socket} all={all} />
                          <span className="shrink-0 text-hint text-muted-foreground uppercase">
                            {entry.socket.protocol}
                          </span>
                          <NewMark socket={entry.socket} now={listedAt} />
                          <span
                            className="min-w-0 truncate font-mono text-hint text-muted-foreground"
                            title={addressLine(entry.socket)}
                          >
                            {addressLine(entry.socket)}
                          </span>
                        </div>
                      </TableCell>
                      <TableCell>
                        <Owner socket={entry.socket} />
                      </TableCell>
                      <TableCell>
                        <ReachWord socket={entry.socket} className="max-w-full" />
                      </TableCell>
                      <TableCell>
                        <FirewallWord socket={entry.socket} />
                      </TableCell>
                      <TableCell className="text-right">
                        <ClientsCell socket={entry.socket} />
                      </TableCell>
                      <TableCell>
                        <VerbActions
                          dim
                          className="justify-end"
                          verbs={verbsFor(entry.socket)}
                          menuLabel={socketMenuLabel(entry.socket)}
                        />
                      </TableCell>
                    </TableRow>
                  ),
                )}
              </TableBody>
            </Table>
          ) : (
            <ul aria-label="Listening sockets" className="divide-y divide-hairline">
              {entries.map((entry) =>
                entry.kind === "group" ? (
                  <li
                    key={`group:${entry.group.key}`}
                    data-slot="port-row"
                    className="flex h-16 min-w-0 items-center gap-3"
                  >
                    <div className="min-w-0 flex-1">
                      <GroupTitle
                        group={entry.group}
                        open={entry.open}
                        onToggle={() => toggleGroup(entry.group.key)}
                      />
                      <p className="mt-1 truncate font-mono text-hint text-muted-foreground">
                        {plural(entry.group.sockets.length, "socket")} · {groupPorts(entry.group)}
                      </p>
                    </div>
                  </li>
                ) : (
                  // eslint-disable-next-line jsx-a11y/click-events-have-key-events, jsx-a11y/no-noninteractive-element-interactions -- the pointer's larger target: the port is the link a keyboard opens it by
                  <li
                    key={socketKey(entry.socket)}
                    data-slot="port-row"
                    className={cn(
                      "group flex h-16 min-w-0 cursor-pointer items-center gap-3 transition-colors hover:bg-row-hover",
                      entry.member && "border-l border-hairline pl-3",
                    )}
                    onClick={rowClick(entry.socket)}
                  >
                    <ProcessMark listener={entry.socket} />
                    <div className="min-w-0 flex-1">
                      <p className="flex min-w-0 items-baseline gap-2">
                        <PortLink socket={entry.socket} all={all} />
                        <span className="shrink-0 text-hint text-muted-foreground uppercase">
                          {entry.socket.protocol}
                        </span>
                        <NewMark socket={entry.socket} now={listedAt} />
                        <span className="min-w-0 truncate text-body">
                          {ownerTitle(entry.socket)}
                        </span>
                        {entry.socket.self && <Tag>This dashboard</Tag>}
                      </p>
                      <p className="mt-1 flex min-w-0 items-center gap-2">
                        <ReachWord socket={entry.socket} className="shrink-0" />
                        <span className="min-w-0 truncate font-mono text-hint text-muted-foreground">
                          {addressLine(entry.socket)}
                        </span>
                      </p>
                    </div>
                    <VerbActions
                      className="shrink-0"
                      verbs={verbsFor(entry.socket).map((verb) => ({ ...verb, inline: false }))}
                      menuLabel={socketMenuLabel(entry.socket)}
                    />
                  </li>
                ),
              )}
            </ul>
          )}
        </PanelBody>
        <PanelFooter className="mt-3 text-hint text-muted-foreground">
          <span className="numeric">
            {visible.length === all.length
              ? plural(all.length, "socket")
              : `${visible.length} of ${plural(all.length, "socket")}`}
          </span>
          {hidden > 0 && range && (
            <>
              <span className="text-muted-foreground/40">·</span>
              <span className="numeric">
                {hidden} on loopback ports {range.low}–{range.high} hidden
              </span>
            </>
          )}
        </PanelFooter>
      </Panel>

      <OrphanRules firewall={firewall.data} admin={admin} onChange={firewall.refresh} />
      <PortChanges query={query} onClearQuery={() => setQuery("")} />
      {dialog}
    </Page>
  )
}

/** The line the page opens on: what it is about, how much, and the page's commands. */
function Identity({ facts, aside }: { facts: React.ReactNode; aside?: React.ReactNode }) {
  return <HostIdentity fallback={Router} title="Listening ports" facts={facts} aside={aside} />
}

/** The port, as the keyboard's way to its page; the pointer's is the whole row. */
function PortLink({ socket, all }: { socket: Socket; all: Socket[] }) {
  return (
    <Link
      href={portHref(socket, all)}
      aria-label={`Open ${socket.protocol} ${formatEndpoint(socket.address, socket.port)}`}
      className="numeric shrink-0 rounded-sm font-mono text-body font-semibold focus-ring hover:underline"
    >
      {socket.port}
    </Link>
  )
}

/**
 * Who a socket answers for, on one line: the owner as its product, its name,
 * and after it how it runs or the command — the rest is the port's page.
 */
function Owner({ socket }: { socket: Socket }) {
  const nat = socket.source === "docker-nat"
  const label = ownerLabel(socket)
  const proxy = proxyWords(socket)
  const pids = socketPids(socket)
  // How it runs says more than the command where something supervises it:
  // "ssh.socket → ssh.service" over "/lib/systemd/systemd".
  const command = nat ? NAT_WORDS : socket.cmdline || "No command reported"
  const detail = label ?? command
  const who = [socket.user || "unknown user", pids.length > 0 && `PID ${pids.join(", ")}`]
    .filter(Boolean)
    .join(" · ")
  return (
    <div
      className="flex min-w-0 items-center gap-2.5"
      title={[label, proxy, command, nat ? undefined : who].filter(Boolean).join("\n")}
    >
      <ProcessMark listener={socket} className="size-7 rounded-md [&_img]:size-4" />
      <span className="max-w-[70%] shrink-0 truncate text-body font-medium">
        {ownerTitle(socket)}
      </span>
      {socket.self && <Tag>This dashboard</Tag>}
      <span
        className={cn("min-w-0 truncate text-hint text-muted-foreground", !label && "font-mono")}
      >
        {detail}
      </span>
    </div>
  )
}

/** What the firewall does with a socket, as one coloured line; why is its tooltip and the port's page. */
function FirewallWord({ socket }: { socket: Socket }) {
  const fw = socket.firewall
  if (!fw) return <span className="text-xs text-muted-foreground">{noFirewallWords(socket)}</span>
  const words = firewallWords(fw)
  const tone =
    words.verdict === "warning"
      ? "text-warning"
      : words.verdict === "ok"
        ? "text-success"
        : "text-muted-foreground"
  return (
    <span
      className={cn("block truncate text-xs font-medium", tone)}
      title={`${words.label}. ${words.conclusion}`}
    >
      {words.label}
    </span>
  )
}

/** How many are connected to a TCP socket; the busiest of them is its tooltip. */
function ClientsCell({ socket }: { socket: Socket }) {
  const clients = socketClients(socket)
  if (!clients) return <span className="text-xs text-muted-foreground">—</span>
  return (
    <span
      className={cn("numeric text-body", clients.count === 0 && "text-muted-foreground")}
      title={clients.peers[0] ? `Busiest: ${clients.peers[0].address}` : undefined}
    >
      {clients.count}
    </span>
  )
}

/** The kernel keeps one socket per protocol, family, address and port. */
function socketKey(socket: Socket) {
  return `${socket.protocol}-${socket.family}-${socket.address}-${socket.port}`
}

/**
 * How old the rows are, ticking on its own so the table under it does not
 * redraw every second.
 */
function Freshness({
  at,
  paused,
  refreshing,
  stale,
}: {
  at: number
  paused: boolean
  refreshing: boolean
  stale: boolean
}) {
  const now = useNow(1000)
  const ago = sinceWords(now - at)
  return (
    <span className="whitespace-nowrap tabular-nums">
      {refreshing
        ? "Refreshing…"
        : paused
          ? `Paused · updated ${ago}`
          : stale
            ? `Last updated ${ago}`
            : `Updated ${ago}`}
    </span>
  )
}

/** A column heading that sorts by its column, saying so to a screen reader. */
function SortHead({
  label,
  column,
  sort,
  onSort,
  className,
}: {
  label: string
  column: SortKey
  sort: PortSort
  onSort: (key: SortKey) => void
  className?: string
}) {
  const active = sort.key === column
  const Arrow = sort.dir === "asc" ? ChevronUp : ChevronDown
  return (
    <TableHead
      className={className}
      aria-sort={active ? (sort.dir === "asc" ? "ascending" : "descending") : "none"}
    >
      <button
        type="button"
        onClick={() => onSort(column)}
        className={cn(
          "-mx-1 inline-flex items-center gap-1 rounded-sm px-1 focus-ring transition-colors hover:text-foreground",
          active && "text-foreground",
        )}
      >
        {label}
        {active && <Arrow aria-hidden className="size-3" />}
      </button>
    </TableHead>
  )
}

/**
 * An application's folded sockets on one line: the control that unfolds
 * them, the owner as its product, and its account and processes.
 */
function GroupTitle({
  group,
  open,
  onToggle,
}: {
  group: SocketGroup
  open: boolean
  onToggle: () => void
}) {
  const Chevron = open ? ChevronDown : ChevronRight
  const pids = groupPids(group)
  return (
    <div className="flex min-w-0 items-center gap-2.5">
      <ProcessMark listener={group.sockets[0]} className="size-7 rounded-md [&_img]:size-4" />
      <button
        type="button"
        aria-expanded={open}
        aria-label={`${group.process}, ${plural(group.sockets.length, "socket")}`}
        onClick={(event) => {
          event.stopPropagation()
          onToggle()
        }}
        className="flex min-w-0 shrink-0 items-center gap-1 rounded-sm text-left text-body font-medium focus-ring"
      >
        <Chevron aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />
        <span className="truncate">{group.process}</span>
      </button>
      <span className="min-w-0 truncate text-hint text-muted-foreground">
        {group.user || "unknown user"}
        {pids.length === 1 && ` · PID ${pids[0]}`}
        {pids.length > 1 && ` · ${pids.length} PIDs`}
      </span>
    </div>
  )
}

/** A group's reach is its worst socket's; how many networks it spans is the tooltip. */
function GroupReach({ group }: { group: SocketGroup }) {
  const networks = new Set(group.sockets.map((socket) => socket.network)).size
  return (
    <span title={networks > 1 ? `Worst of ${networks} networks` : undefined} className="flex">
      <ReachWord socket={worstSocket(group)} className="max-w-full" />
    </span>
  )
}
