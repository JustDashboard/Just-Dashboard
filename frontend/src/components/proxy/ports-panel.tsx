"use client"

import { Fragment, Suspense, useEffect, useMemo, useState } from "react"
import { useRouter, useSearchParams } from "next/navigation"
import {
  AcronymCsv,
  AcronymJson,
  Box,
  ChartActivity,
  ChevronDown,
  ChevronRight,
  ChevronUp,
  Clipboard,
  Copy,
  Download,
  GridMasonry,
  ListOrdered,
  Pause,
  Play,
  RefreshClockwise,
  Router,
  Servers,
  Shield,
  Warning,
} from "@/components/icons"
import { errorMessage, get } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { plural } from "@/lib/format"
import { downloadText } from "@/lib/metrics-export"
import type { Listener, PortsMeta } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useSessionState, useViewState } from "@/lib/view-state"
import { useMediaQuery } from "@/hooks/use-mobile"
import { usePoll } from "@/hooks/use-poll"
import { useNow } from "@/components/deploy/vocabulary"
import { InfoTip } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Page, PageContext, SearchInput, Toolbar } from "@/components/page"
import { Panel, PanelBody, PanelFooter, PanelHeader } from "@/components/panel"
import {
  ProductLogo,
  imageProduct,
  portProduct,
  processProduct,
  unitProduct,
} from "@/components/product-logo"
import { ROW_BLEED } from "@/components/row-list"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { EmptyState, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { VerbActions, VerbMenu, type Verb } from "@/components/verbs"
import {
  dangerousPorts,
  dangerousService,
  foldDualStack,
  internetHint,
  networkWords,
  onUplink,
  ownerLabel,
  ownerLinks,
  ownerTitle,
  pastFirewallWords,
  privateHint,
  reachVerdict,
  reachWords,
  socketAddresses,
  socketPids,
  tallyReach,
  UPLINK_CAVEAT,
  type OwnerLink,
  type Socket,
} from "@/components/proxy/ports"
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

const header = <PageContext eyebrow="Proxy" title="Listening ports" />

/**
 * Every listening socket on the host, and whether it faces off the machine.
 *
 * "What is listening on 8080 and who started it" is the first question during
 * an incident, and the answer to the second half is one click away: a row's
 * process opens under Processes with its connections and parent chain. The
 * view — search, reach, protocol and sort — is in the address bar, so the
 * overview's attention list and the Connections page can link into it
 * already narrowed.
 */
export function PortsPage() {
  // The view is read from the address bar, which the server does not have.
  return (
    <Suspense
      fallback={
        <Page>
          {header}
          <LoadingPanel />
        </Page>
      }
    >
      <PortsView />
    </Suspense>
  )
}

function PortsView() {
  const router = useRouter()
  const arrival = viewFromParams(useSearchParams())
  const [query, setQuery] = useSessionState("proxy.ports.query", "", arrival?.q)
  const [reachValue, setReach] = useSessionState<string>("proxy.ports.reach", "all", arrival?.reach)
  const [protoValue, setProto] = useSessionState<string>("proxy.ports.proto", "all", arrival?.proto)
  const [sortValue, setSort] = useSessionState(
    "proxy.ports.sort",
    sortParam(DEFAULT_SORT),
    arrival ? sortParam(arrival.sort) : null,
  )
  // Values another version of the page remembered read as the default.
  const reach = parseReachFilter(reachValue) ?? "all"
  const proto = parseProtoFilter(protoValue) ?? "all"
  const sort = useMemo(() => parseSort(sortValue) ?? DEFAULT_SORT, [sortValue])
  const [grouped, setGrouped] = useViewState("proxy.ports.grouped", false)
  const [hideEphemeral, setHideEphemeral] = useViewState("proxy.ports.hideEphemeral", false)
  const [open, setOpen] = useState<ReadonlySet<string>>(new Set())
  const [paused, setPaused] = useState(false)
  const [refreshing, setRefreshing] = useState(false)
  const wide = useMediaQuery("(min-width: 1024px)")

  const { data, error, loading, refresh } = usePoll(
    async (signal) => {
      try {
        return { listeners: await get<Listener[]>("/ports", undefined, signal), at: Date.now() }
      } finally {
        // A refresh that aborted a poll in flight is still refreshing.
        if (!signal.aborted) setRefreshing(false)
      }
    },
    POLL_MS,
    [],
    { enabled: !paused },
  )
  const meta = usePoll((signal) => get<PortsMeta>("/ports/meta", undefined, signal), 0)
  const range = meta.data?.ephemeralRange ?? null

  useEffect(() => {
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
  }, [query, reach, proto, sort])

  const listeners = useMemo(() => data?.listeners ?? [], [data])
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
  const facets = useMemo(() => facetCounts(all, filters, range), [all, filters, range])
  const visible = useMemo(
    () => sortSockets(visibleSockets(all, filters), sort),
    [all, filters, sort],
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

  const filtered = query.trim() !== "" || reach !== "all" || proto !== "all"
  const clearFilters = () => {
    setQuery("")
    setReach("all")
    setProto("all")
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
    setRefreshing(true)
    if (paused) setPaused(false)
    else refresh()
  }
  const exportRows = (format: "csv" | "json") => {
    const stamp = new Date().toISOString().slice(0, 16).replace(/[:T]/g, "-")
    if (format === "csv") downloadText(`listening-sockets-${stamp}.csv`, toCsv(visible))
    else downloadText(`listening-sockets-${stamp}.json`, toJson(visible), "application/json")
  }

  const verbsFor = (l: Socket): Verb[] => {
    const endpoint = formatEndpoint(l.address, l.port)
    // The owner's own page first: the container, the deployment it runs
    // for, the PM2 app or the unit, where its remedy usually is.
    const verbs: Verb[] = ownerLinks(l).map((link, i) => ({
      key: link.key,
      label: link.label,
      icon: OWNER_ICON[link.key],
      inline: i === 0,
      run: () => router.push(link.href),
    }))
    // Init holding a socket unit's socket is systemd listening, whose page
    // is the unit's; docker-proxy is a container's plumbing, kept in the menu.
    if (l.pid > 1 || (l.pid === 1 && !l.socketUnit)) {
      verbs.push({
        key: "process",
        label: "Process",
        icon: ListOrdered,
        inline: !l.container,
        run: () => router.push(`/processes?pid=${l.pid}`),
      })
    }
    verbs.push({
      key: "endpoint",
      label: "Copy endpoint",
      icon: Copy,
      run: () => void copyText(endpoint, `Copied ${endpoint}`),
    })
    if (l.cmdline) {
      const cmdline = l.cmdline
      verbs.push({
        key: "command",
        label: "Copy command",
        icon: Clipboard,
        run: () => void copyText(cmdline, "Copied the command"),
      })
    }
    if (l.exposed) {
      verbs.push({
        key: "firewall",
        label: "Firewall",
        icon: Shield,
        run: () => router.push("/security/firewall"),
      })
    }
    return verbs
  }
  // A row's menu is named by the endpoint it acts on; two programs can share
  // a port on two addresses, never an address and a port.
  const menuLabel = (l: Socket) => `Actions for ${l.protocol} ${formatEndpoint(l.address, l.port)}`

  if (loading && !data) {
    return (
      <Page>
        {header}
        <LoadingPanel />
      </Page>
    )
  }
  if (error && !data) {
    return (
      <Page>
        {header}
        <ErrorState error={error} onRetry={refresh} />
      </Page>
    )
  }

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

      <StatGrid columns={4} dense>
        <StatTile
          label="Listening"
          value={counts.all}
          hint={`${counts.tcp} TCP · ${counts.udp} UDP`}
        />
        <StatTile
          label="Internet-facing"
          value={counts.internet}
          // Nothing is sure to face the internet while a socket is on the
          // private uplink, but nothing is sure not to either.
          tone={counts.internet > 0 ? "warning" : onTheUplink > 0 ? "default" : "success"}
          hint={internetHint(all)}
        />
        <StatTile label="Private networks" value={counts.private} hint={privateHint(all)} />
        <StatTile
          label="Databases exposed"
          value={dangerous.length}
          tone={
            dangerous.some((d) => d.level === "critical")
              ? "danger"
              : dangerous.length > 0
                ? "warning"
                : "default"
          }
          hint={dangerous.length > 0 ? "counted once per port" : "none off the machine"}
        />
      </StatGrid>

      <Panel plain>
        <PanelHeader
          title="Listening sockets"
          actions={
            <>
              {data && (
                <Freshness
                  at={data.at}
                  paused={paused}
                  refreshing={refreshing}
                  stale={Boolean(error)}
                />
              )}
              {!paused && (
                <IconAction label="Refresh now" pending={refreshing} onClick={refreshNow}>
                  <RefreshClockwise />
                </IconAction>
              )}
              <IconAction
                label={paused ? "Resume refreshing" : "Pause refreshing"}
                aria-pressed={paused}
                onClick={() => {
                  if (paused) return refreshNow()
                  setRefreshing(false)
                  setPaused(true)
                }}
              >
                {paused ? <Play /> : <Pause />}
              </IconAction>
              <VerbMenu
                label="Export"
                verbs={[
                  { key: "csv", label: "CSV", icon: AcronymCsv, run: () => exportRows("csv") },
                  { key: "json", label: "JSON", icon: AcronymJson, run: () => exportRows("json") },
                ]}
                trigger={
                  <Button variant="outline" size="xs" disabled={visible.length === 0}>
                    <Download />
                    Export
                  </Button>
                }
              />
            </>
          }
        />
        {error && data && (
          // The rows below are the last list that arrived; saying nothing
          // would let an incident be read off a list minutes old.
          <Notice
            tone="warning"
            icon={Warning}
            title="Refreshing failed, so this is the last list that arrived"
            className="my-3"
          >
            <p>
              {errorMessage(error)}
              {paused
                ? " Refreshing is paused."
                : ` The page tries again every ${POLL_MS / 1000} seconds.`}
            </p>
            <Button variant="outline" size="xs" className="mt-2" onClick={refreshNow}>
              {paused ? "Resume" : "Try again"}
            </Button>
          </Notice>
        )}
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
            <Select value={sortParam(sort)} onValueChange={setSort}>
              <SelectTrigger size="sm" className="ml-auto w-48" aria-label="Sort">
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
          )}
        </Toolbar>
        <PanelBody flush>
          {visible.length === 0 ? (
            empty
          ) : wide ? (
            // The grid scrolls independently, so its border marks that boundary.
            <Table
              className="table-fixed"
              containerClassName="max-h-[calc(100svh-24rem)] rounded-xl border bg-card"
            >
              <TableHeader className={stickyTableHeader}>
                <TableRow>
                  <SortHead
                    label="Endpoint"
                    column="port"
                    sort={sort}
                    onSort={chooseSort}
                    className="w-[20%]"
                  />
                  <SortHead label="Application" column="process" sort={sort} onSort={chooseSort} />
                  <SortHead
                    label="Reach"
                    column="reach"
                    sort={sort}
                    onSort={chooseSort}
                    className="w-56"
                  />
                  <TableHead className="w-28">
                    <span className="sr-only">Actions</span>
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {entries.map((entry) =>
                  entry.kind === "group" ? (
                    <TableRow
                      key={`group:${entry.group.key}`}
                      // The row is the pointer's target; the keyboard's is the
                      // one button in it, so the row is not a second tab stop.
                      className="group cursor-pointer"
                      onClick={(event) => {
                        if ((event.target as HTMLElement).closest("button")) return
                        toggleGroup(entry.group.key)
                      }}
                    >
                      <TableCell className="py-4">
                        <p className="text-body font-medium">
                          {plural(entry.group.sockets.length, "socket")}
                        </p>
                        <p className="mt-1 font-mono text-hint wrap-anywhere text-muted-foreground">
                          {groupPorts(entry.group)}
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
                    </TableRow>
                  ) : (
                    <TableRow key={socketKey(entry.socket)} className="group">
                      <TableCell className="py-4">
                        {/* A group's sockets stand behind a rule under its
                            line, as an option's revealed fields do. */}
                        <div className={cn(entry.member && "border-l border-hairline pl-4")}>
                          <div className="flex items-baseline gap-2">
                            <span className="numeric font-mono text-title font-semibold">
                              {entry.socket.port}
                            </span>
                            <span className="text-hint text-muted-foreground uppercase">
                              {entry.socket.protocol}
                            </span>
                          </div>
                          <p className="mt-1 font-mono text-hint wrap-anywhere text-muted-foreground">
                            <Addresses socket={entry.socket} />
                          </p>
                        </div>
                      </TableCell>
                      <TableCell>
                        <Owner socket={entry.socket} />
                      </TableCell>
                      <TableCell>
                        <ReachStatus socket={entry.socket} />
                      </TableCell>
                      <TableCell>
                        <VerbActions
                          dim
                          className="justify-end"
                          verbs={verbsFor(entry.socket)}
                          menuLabel={menuLabel(entry.socket)}
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
                    className={cn("flex min-w-0 items-start gap-3 py-3", ROW_BLEED)}
                  >
                    <div className="w-14 shrink-0">
                      <p className="numeric font-mono text-title font-semibold">
                        {entry.group.sockets.length}
                      </p>
                      <p className="text-hint text-muted-foreground">sockets</p>
                    </div>
                    <div className="min-w-0 flex-1">
                      <GroupTitle
                        group={entry.group}
                        open={entry.open}
                        onToggle={() => toggleGroup(entry.group.key)}
                      />
                      <p className="mt-1 font-mono text-hint wrap-anywhere text-muted-foreground">
                        {groupPorts(entry.group)}
                      </p>
                      <div className="mt-1.5">
                        <GroupReach group={entry.group} />
                      </div>
                    </div>
                  </li>
                ) : (
                  <li
                    key={socketKey(entry.socket)}
                    className={cn(
                      "group flex min-w-0 items-start gap-3 py-3 transition-colors hover:bg-row-hover",
                      ROW_BLEED,
                    )}
                  >
                    {entry.member && (
                      <span
                        aria-hidden
                        className="w-2 shrink-0 self-stretch border-l border-hairline"
                      />
                    )}
                    {/* The protocol under the port, as the table pairs them:
                        a line of its own that nothing can push off the row. */}
                    <div className="w-14 shrink-0">
                      <p className="numeric font-mono text-title font-semibold">
                        {entry.socket.port}
                      </p>
                      <p className="text-hint text-muted-foreground uppercase">
                        {entry.socket.protocol}
                      </p>
                    </div>
                    <div className="min-w-0 flex-1">
                      <div className="flex min-w-0 items-center gap-2 text-body">
                        <ProcessMark listener={entry.socket} />
                        <span className="truncate">{ownerTitle(entry.socket)}</span>
                        {entry.socket.user && (
                          <span className="max-w-[45%] shrink-0 truncate text-hint text-muted-foreground">
                            · {entry.socket.user}
                          </span>
                        )}
                      </div>
                      {/* The tag waits a line, so the owner's name keeps the
                          width a phone has for it. */}
                      <OwnerLine socket={entry.socket} tagged />
                      {entry.socket.source === "docker-nat" && (
                        <p className="text-hint text-muted-foreground">{NAT_WORDS}</p>
                      )}
                      {/* Which address a port is bound to is the whole reason
                          this page exists — it must not be the line that gets
                          cut on a narrow screen. */}
                      <p className="font-mono text-hint wrap-anywhere text-muted-foreground">
                        <Addresses socket={entry.socket} />
                      </p>
                      <div className="mt-1.5">
                        <ReachStatus socket={entry.socket} />
                      </div>
                    </div>
                    <VerbActions
                      className="shrink-0"
                      verbs={verbsFor(entry.socket)}
                      menuLabel={menuLabel(entry.socket)}
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
    </Page>
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
    <span className="text-hint whitespace-nowrap text-muted-foreground tabular-nums">
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
 * An application's folded sockets, named as the process and account, with the
 * control that unfolds them.
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
    <div className="flex min-w-0 items-center gap-3">
      <ProcessMark listener={group.sockets[0]} />
      <div className="min-w-0">
        <button
          type="button"
          aria-expanded={open}
          aria-label={`${group.process}, ${plural(group.sockets.length, "socket")}`}
          onClick={(event) => {
            event.stopPropagation()
            onToggle()
          }}
          className="flex max-w-full min-w-0 items-center gap-1 rounded-sm text-left text-body font-medium focus-ring"
        >
          <Chevron aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />
          <span className="truncate">{group.process}</span>
        </button>
        <p className="mt-1 text-hint text-muted-foreground">
          {group.user || "unknown user"}
          {pids.length === 1 && ` · PID ${pids[0]}`}
          {pids.length > 1 && ` · ${pids.length} PIDs`}
        </p>
      </div>
    </div>
  )
}

/** A group's reach is its worst socket's, with how many networks it spans. */
function GroupReach({ group }: { group: SocketGroup }) {
  const networks = new Set(group.sockets.map((socket) => reachWords(socket))).size
  return (
    <div className="min-w-0">
      <ReachStatus socket={worstSocket(group)} />
      {networks > 1 && (
        <p className="mt-0.5 pl-5 text-hint text-muted-foreground">worst of {networks} networks</p>
      )}
    </div>
  )
}

const OWNER_ICON: Record<OwnerLink["key"], Verb["icon"]> = {
  container: Box,
  deployment: GridMasonry,
  unit: Servers,
  app: ChartActivity,
}

/** Said of a port Docker publishes through its NAT rules, where the command would be. */
const NAT_WORDS = "Forwarded by Docker's NAT rules; no process listens"

/**
 * Who a socket answers for — a container, a PM2 app, the program — with the
 * dashboard's own marked, how the owner runs, the command, and the account
 * and process holding the socket.
 */
function Owner({ socket }: { socket: Socket }) {
  const nat = socket.source === "docker-nat"
  return (
    <div className="flex min-w-0 items-center gap-3">
      <ProcessMark listener={socket} />
      <div className="min-w-0">
        <p className="flex min-w-0 items-center gap-2">
          <span className="truncate text-body font-medium">{ownerTitle(socket)}</span>
          {socket.self && <Tag>This dashboard</Tag>}
        </p>
        <OwnerLine socket={socket} />
        {nat ? (
          <p className="text-hint text-muted-foreground">{NAT_WORDS}</p>
        ) : (
          <>
            <p
              className="truncate font-mono text-hint text-muted-foreground"
              title={socket.cmdline}
            >
              {socket.cmdline || "No command reported"}
            </p>
            <p className="mt-1 text-hint text-muted-foreground">
              {socket.user || "unknown user"}
              <Pids socket={socket} />
            </p>
          </>
        )}
      </div>
    </div>
  )
}

/**
 * How the owner runs: its container's image and stack or deployment, the
 * socket unit and the service it starts, the unit, the PM2 app's program —
 * after the "This dashboard" tag where the title's line has no room for it.
 */
function OwnerLine({ socket, tagged }: { socket: Socket; tagged?: boolean }) {
  const label = ownerLabel(socket)
  const tag = tagged && socket.self
  if (!label && !tag) return null
  return (
    <p className="flex min-w-0 items-center gap-2 text-hint text-muted-foreground">
      {tag && <Tag>This dashboard</Tag>}
      {label && (
        <span className="truncate" title={label}>
          {label}
        </span>
      )}
    </p>
  )
}

/**
 * The owner as the product it is, bare at the line's height — Postgres on
 * 5432, nginx on 443, Caddy for a container of its image — the way the
 * identity line draws a host's facts. Read from the container's image or the
 * program first and the port second, so a `postgres` on an unusual port is
 * still Postgres and a `python` on 5432 is not.
 */
function ProcessMark({ listener }: { listener: Listener }) {
  const unit = listener.activates || (listener.manager === "systemd" ? listener.managerName : "")
  const product = listener.container
    ? imageProduct(listener.container.image)
    : (processProduct(listener.displayName || listener.process || "") ??
      (unit ? unitProduct(unit) : undefined) ??
      portProduct(listener.port))
  return <ProductLogo id={product} size="sm" fallback={Router} />
}

/**
 * A socket's addresses one to a line, for its parent to wrap rather than cut:
 * the address is what says where the port answers. The comma between them
 * stays for a screen reader and a search of the page.
 */
function Addresses({ socket }: { socket: Socket }) {
  return socketAddresses(socket).map((address, i) => (
    <Fragment key={address}>
      {i > 0 && (
        <>
          <span className="sr-only">, </span>
          <br />
        </>
      )}
      {address}
    </Fragment>
  ))
}

/** The process behind a row, or both when Docker holds each family in its own proxy. */
function Pids({ socket }: { socket: Socket }) {
  const pids = socketPids(socket)
  if (pids.length === 0) return null
  return <>{` · ${pids.length > 1 ? "PIDs" : "PID"} ${pids.join(", ")}`}</>
}

/**
 * Where the socket answers, coloured by who can connect as the posture levels
 * the same socket, with the interface the address is on beneath it. The
 * label wraps rather than running into the next column: "the Docker API ·
 * Private uplink" is wider than the column. A database the firewall's
 * inbound default holds to a warning says so, as the posture's finding does,
 * and so does one that gets past the default.
 */
function ReachStatus({ socket }: { socket: Socket }) {
  const verdict = reachVerdict(socket)
  const words = networkWords(socket)
  if (!verdict) return <span className="text-xs text-muted-foreground">{words}</span>
  const service = dangerousService(socket)
  const past = pastFirewallWords(socket)
  return (
    <div className="min-w-0" title={onUplink(socket) ? UPLINK_CAVEAT : undefined}>
      <Status
        verdict={verdict}
        label={service ? `${service} · ${words}` : words}
        icon={Router}
        className="max-w-full whitespace-normal"
      />
      {socket.interface && (
        <p
          className="mt-0.5 truncate pl-5 font-mono text-hint text-muted-foreground"
          title={socket.interface}
        >
          {socket.interface}
        </p>
      )}
      {service && socket.inboundDefault && (
        <p className="mt-0.5 pl-5 text-hint text-muted-foreground">
          Firewall&apos;s inbound default: {socket.inboundDefault}
        </p>
      )}
      {service && past && <p className="mt-0.5 pl-5 text-hint text-muted-foreground">{past}</p>}
    </div>
  )
}
