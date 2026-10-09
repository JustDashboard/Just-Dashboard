"use client"

import { Fragment, useEffect, useMemo, useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { NetworkDevice, Servers } from "@/components/icons"
import { get } from "@/lib/api"
import { plural } from "@/lib/format"
import type { Connections, PortsMeta } from "@/lib/types"
import { Workspace, WorkspaceHelp } from "@/components/workspace/workspace"
import { useFilterHistory } from "@/components/workspace/history"
import { useHeldList } from "@/components/workspace/held-list"
import { usePoll } from "@/hooks/use-poll"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { useAuth } from "@/hooks/use-auth"
import { PageContext, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelFooter, PanelHeader, PanelToolbar } from "@/components/panel"
import { StatGrid, StatLink, StatTile } from "@/components/stat-tile"
import { EmptyNote, EmptyState, ErrorState, LoadingPanel } from "@/components/state"
import { PeerIdentity, ProcessList } from "@/components/security/marks"
import { addressVerbs } from "@/components/security/address-verbs"
import { AreaFindings } from "@/components/security/posture-panel"
import { useSecurity } from "@/components/security/security-context"
import { ProductLogo, processProduct } from "@/components/product-logo"
import { NumberTicker } from "@/components/ui/number-ticker"
import { TileTrend } from "@/components/metrics/sparkline"
import { ConnectionsMap } from "@/components/network/connections-map"
import { Meter } from "@/components/meter"
import { VerbActions } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import { chosenPort, portsHref } from "@/components/proxy/ports-list"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { recentHours, type AddressBlock } from "@/lib/network-traffic"
import { BlockDialog } from "@/components/network/connections/block-dialog"
import { BlocksPanel } from "@/components/network/connections/blocks-panel"
import { PeerSheet } from "@/components/network/connections/peer-sheet"
import { RecordedConnections } from "@/components/network/connections/recorded"
import {
  stickyTableHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

/**
 * Who is talking to this machine right now.
 *
 * The ports view answers what is listening; this answers who took it up on the
 * offer, which is the question during an incident. Folded by remote address
 * rather than listed one socket per row: a busy host holds thousands, and
 * forty of them are one client — a raw table buries the single address with
 * two hundred connections underneath four hundred rows of noise. Each address
 * is drawn as the network it is on (Tailscale's mark for the tailnet) and
 * each process as the product it is, so the one caller from the internet
 * talking to Caddy is found before any row is read. A reading with verbs,
 * not a destination, so the rows stay rows (§16).
 *
 * Above the table the same peers are drawn at once (`ConnectionsMap`): where
 * the callers are, this server, and the programs they reached. The figures
 * over it count up as they land and carry the counts of every read since the
 * page opened as their trend, so a burst from the internet is a shape rather
 * than a number that was briefly larger.
 *
 * Folding keeps what each address used — transports, far-end ports, states —
 * and an administrator opens an address into its full tuples, how long each
 * has been seen, what it carried, the ones seen closing, and the layers it
 * crosses. A past hour is read from the socket history in the same shape. A
 * block asks why and until when, and can open an incident; the blocks made
 * here are listed under the table with their end.
 */
export function ConnectionsPanel() {
  const { can } = useAuth()
  const { posture, applyFix, firewall } = useSecurity()
  const router = useRouter()
  const [filters, setFilters] = useFilterHistory("security.connections.filters", {
    q: "",
    scope: "all",
    when: "live",
    hour: "",
  })
  const query = filters.q
  const scope = filters.scope === "public" ? "public" : "all"
  const admin = can("system.admin")
  const when = filters.when === "recorded" && admin ? "recorded" : "live"
  const setQuery = (q: string) => setFilters((previous) => ({ ...previous, q }))
  const setScope = (scope: string) => setFilters((previous) => ({ ...previous, scope }), true)
  const setWhen = (when: string) => setFilters((previous) => ({ ...previous, when }), true)
  const setHour = (hour: string) => setFilters((previous) => ({ ...previous, hour }), true)
  const hours = useMemo(() => recentHours(new Date(), 24), [])
  const [inspecting, setInspecting] = useState<string>()
  const [blockFor, setBlockFor] = useState<{ address: string; context: string }>()
  const { data, error, loading, refresh, lastSuccess } = usePoll<Connections>(
    (signal) => get("/connections", undefined, signal),
    10000,
  )
  const blocks = usePoll<AddressBlock[]>(
    (signal) => get("/firewall/blocks", undefined, signal),
    30_000,
  )
  // The kernel's ephemeral range tells this host's side of a connection that
  // somebody chose — a listener's port, which the ports page can name — from
  // one the kernel picked for an outgoing call, which nothing listens on.
  const range = usePoll((signal) => get<PortsMeta>("/ports/meta", undefined, signal), 0).data
    ?.ephemeralRange

  const matches = useMemo(() => {
    const q = query.trim().toLowerCase()
    return (data?.peers ?? []).filter(
      (p) =>
        (scope === "all" || !p.private) &&
        (!q ||
          `${p.address} ${p.service ?? ""} ${p.processes.join(" ")} ${p.ports.join(" ")}`
            .toLowerCase()
            .includes(q)),
    )
  }, [data?.peers, scope, query])
  const held = useHeldList(
    data ? matches : undefined,
    (peer) => peer.address,
    JSON.stringify(filters),
  )
  const peers = held.rows
  const most = Math.max(1, ...(data?.peers ?? []).map((p) => p.count))
  const fromInternet = (data?.peers ?? []).filter((p) => !p.private).length
  const history = useReadings(data)

  const header = <PageContext eyebrow="Network" title="Connections" />

  if (loading && !data) {
    return (
      <>
        {header}
        <LoadingPanel />
      </>
    )
  }
  if (error && !data) {
    return (
      <>
        {header}
        <ErrorState error={error} onRetry={refresh} />
      </>
    )
  }

  const askToBlock = (peer: {
    address: string
    ports: number[]
    processes: string[]
    service?: string
  }) =>
    setBlockFor({
      address: peer.address,
      context: `connected to ${peer.service ?? peer.processes[0] ?? "this server"}${peer.ports.length ? ` on ${peer.ports.join(", ")}` : ""}`,
    })
  const blocked = new Set(
    (blocks.data ?? []).filter((b) => b.state === "active").map((b) => b.address),
  )

  return (
    <Workspace
      name="Connections"
      openItems={false}
      refresh={() => {
        held.reveal()
        refresh()
      }}
      escape={() => {
        if (!query && scope === "all") return false
        setFilters((previous) => ({ ...previous, q: "", scope: "all" }), true)
        return true
      }}
    >
      {header}

      <NetworkReadWarning error={error} refresh={refresh} lastSuccess={lastSuccess} />

      {/* The figures, then the addresses they describe. Listening goes to the
          page that names each socket: this page is who arrived, that one is
          what was open for them. */}
      <StatGrid columns={4}>
        <StatTile
          label="Remote addresses"
          value={<NumberTicker value={data?.peers.length ?? 0} />}
          trend={
            <TileTrend
              values={history.map((h) => h.peers)}
              color="var(--chart-1)"
              label="Remote addresses since the page opened"
            />
          }
          hint={`${data?.peers.filter((p) => p.private).length ?? 0} private or on the tailnet`}
        />
        <StatTile
          label="From the internet"
          value={<NumberTicker value={fromInternet} />}
          tone={fromInternet > 0 ? "warning" : "default"}
          trend={
            <TileTrend
              values={history.map((h) => h.internet)}
              color="var(--chart-3)"
              label="Addresses from the internet since the page opened"
            />
          }
          hint={fromInternet > 0 ? "neither private nor on the tailnet" : "all private or loopback"}
        />
        <StatTile
          label="Sockets"
          value={<NumberTicker value={data?.total ?? 0} />}
          trend={
            <TileTrend
              values={history.map((h) => h.total)}
              color="var(--chart-2)"
              label="Sockets since the page opened"
            />
          }
          hint={`${data?.loopback ?? 0} never left the machine`}
        />
        <StatLink href={portsHref({})} label="Listening ports">
          <StatTile
            className="h-full transition-colors group-hover:bg-row-hover"
            label="Listening"
            value={<NumberTicker value={data?.listening ?? 0} />}
            hint="sockets waiting for a caller"
          />
        </StatLink>
      </StatGrid>

      {data && data.peers.length > 0 && (
        <Panel plain>
          <PanelHeader title="Who is connected" />
          <PanelBody>
            <ConnectionsMap data={data} />
          </PanelBody>
        </Panel>
      )}

      {/* The "ports" findings are about listeners, which live on the proxy's
          Ports page; they are shown here because this is the page somebody
          opens to ask what is reachable. */}
      <AreaFindings posture={posture} area="ports" onFix={applyFix} />

      {admin && (
        <ToggleGroup
          type="single"
          value={when}
          onValueChange={(next) => next && setWhen(next)}
          variant="outline"
          size="sm"
          aria-label="Which connections"
          className="self-start"
        >
          <ToggleGroupItem value="live" className="px-2.5 text-hint">
            Live
          </ToggleGroupItem>
          <ToggleGroupItem value="recorded" className="px-2.5 text-hint">
            A past hour
          </ToggleGroupItem>
        </ToggleGroup>
      )}

      {when === "recorded" ? (
        <RecordedConnections
          hours={hours}
          hour={filters.hour}
          onHour={setHour}
          query={query}
          onInspect={setInspecting}
        />
      ) : (
        <Panel>
          <PanelHeader
            title="Live connections"
            actions={
              // The shortcuts and the held rows are the table's, so they sit in
              // its head rather than on a line of their own above the page.
              <span className="flex flex-wrap items-center gap-3">
                {held.pending > 0 && (
                  <Button
                    size="xs"
                    aria-label={`Show ${held.pending} new addresses`}
                    onClick={held.reveal}
                  >
                    <span role="status">{held.pending} new addresses</span> · Show
                  </Button>
                )}
                <span className="numeric text-hint text-muted-foreground">
                  {peers.length} addresses · refreshes every 10s
                </span>
                <WorkspaceHelp compact />
              </span>
            }
          />
          {/* One strip, not two. The filter and the search that change which
            rows are shown belong on the same line as each other. */}
          <PanelToolbar>
            <ToggleGroup
              type="single"
              value={scope}
              onValueChange={(next) => next && setScope(next as "all" | "public")}
              variant="outline"
              size="sm"
              aria-label="Which peers to show"
            >
              <ToggleGroupItem value="all" className="px-2.5 text-hint">
                Everything
              </ToggleGroupItem>
              <ToggleGroupItem value="public" className="px-2.5 text-hint">
                From the internet {fromInternet}
              </ToggleGroupItem>
            </ToggleGroup>
            <span className="flex-1" />
            <SearchInput
              dense
              data-workspace-search
              aria-label="Filter connections"
              placeholder="Address, port or process"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              containerClassName="sm:w-64"
            />
          </PanelToolbar>
          <PanelBody flush>
            {peers.length === 0 ? (
              query.trim() ? (
                <EmptyNote>No connection matches &ldquo;{query.trim()}&rdquo;.</EmptyNote>
              ) : (
                <EmptyState
                  icon={NetworkDevice}
                  title={
                    scope === "public" ? "Nothing connected from the internet" : "No connections"
                  }
                  className="mt-3"
                />
              )
            ) : (
              <div className="min-w-0 group-data-[plain]/panel:-mx-4">
                <Table containerClassName="max-h-[36rem]">
                  <TableHeader className={stickyTableHeader}>
                    <TableRow>
                      <TableHead>Remote address</TableHead>
                      <TableHead className="w-full">Destination</TableHead>
                      <TableHead className="text-right">Sockets</TableHead>
                      <TableHead className="w-px">
                        <span className="sr-only">Actions</span>
                      </TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {peers.map((peer) => (
                      <TableRow
                        key={peer.address}
                        data-workspace-item={peer.address}
                        data-workspace-name={peer.address}
                        tabIndex={0}
                        className="group focus-ring-inset"
                      >
                        <TableCell className="py-4">
                          {admin ? (
                            <button
                              type="button"
                              onClick={() => setInspecting(peer.address)}
                              aria-label={`Inspect ${peer.address}`}
                              className="rounded-sm text-left focus-ring"
                            >
                              <PeerIdentity ip={peer.address} />
                            </button>
                          ) : (
                            <PeerIdentity ip={peer.address} />
                          )}
                          {blocked.has(peer.address) && (
                            <span className="mt-1 block text-hint text-destructive">blocked</span>
                          )}
                        </TableCell>
                        <TableCell className="whitespace-normal">
                          <div className="flex items-center gap-3">
                            <ProductLogo
                              id={processProduct(peer.processes[0] ?? "")}
                              fallback={Servers}
                              size="sm"
                              className="hidden sm:flex"
                            />
                            <div className="space-y-1">
                              <span className="text-body font-medium">
                                {peer.processes[0] || peer.service || "Unknown process"}
                              </span>
                              <span className="block font-mono text-hint text-muted-foreground">
                                {peer.ports.length
                                  ? peer.ports.map((port, i) => (
                                      <Fragment key={port}>
                                        {i > 0 && ", "}
                                        {chosenPort(port, range) ? (
                                          <Link
                                            href={portsHref({ q: `:${port}` })}
                                            aria-label={`What listens on port ${port}`}
                                            className="rounded-sm underline-offset-4 focus-ring hover:underline"
                                          >
                                            {port}
                                          </Link>
                                        ) : (
                                          port
                                        )}
                                      </Fragment>
                                    ))
                                  : "—"}
                                {peer.service && ` · ${peer.service}`}
                              </span>
                              {peer.protocols && peer.protocols.length > 0 && (
                                <span className="block font-mono text-hint text-muted-foreground">
                                  {peer.protocols.join("/").toUpperCase()}
                                  {peer.remotePorts &&
                                    peer.remotePorts.length > 0 &&
                                    ` · from ${peer.remotePorts.join(", ")}${peer.morePorts ? ` +${peer.morePorts}` : ""}`}
                                  {peer.states &&
                                    ` · ${Object.entries(peer.states)
                                      .sort(([, a], [, b]) => b - a)
                                      .map(
                                        ([state, n]) =>
                                          `${n} ${state.toLowerCase().replace("_", "-")}`,
                                      )
                                      .join(", ")}`}
                                </span>
                              )}
                              {peer.processes.length > 1 && (
                                <ProcessList names={peer.processes.slice(1)} />
                              )}
                            </div>
                          </div>
                        </TableCell>
                        <TableCell>
                          <div className="ml-auto w-16 space-y-2 text-right">
                            <span className="numeric font-medium">{peer.count}</span>
                            <Meter
                              value={(peer.count / most) * 100}
                              size="thin"
                              label={`${peer.count} sockets`}
                            />
                            <span className="block text-hint text-muted-foreground">
                              {peer.established} active
                            </span>
                          </div>
                        </TableCell>
                        <TableCell>
                          <VerbActions
                            dim
                            className="justify-end"
                            verbs={addressVerbs({
                              ip: peer.address,
                              block:
                                admin && !peer.private && !blocked.has(peer.address)
                                  ? () => askToBlock(peer)
                                  : undefined,
                              blocking: blockFor?.address === peer.address,
                              navigate: (href) => router.push(href),
                            })}
                          />
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            )}
          </PanelBody>
          <PanelFooter className="text-hint text-muted-foreground">
            {peers.length} of {data?.peers.length ?? 0} addresses · grouped by remote address ·
            socket counts include connections still opening or closing.
            {data?.quality && (
              <span className="block" aria-label="What this read cannot see">
                {data.quality.intervalSeconds > 0
                  ? `Read ${Math.round(data.quality.intervalSeconds)}s after the last; connections shorter than that may be missing. `
                  : "The first read of this table; nothing is known of what came before. "}
                {data.quality.closedSinceLast > 0 &&
                  `${plural(data.quality.closedSinceLast, "connection")} closed since the last read. `}
                {data.quality.unconnectedUdp > 0 &&
                  `${plural(data.quality.unconnectedUdp, "unconnected UDP socket")} have no peer to list.`}
              </span>
            )}
          </PanelFooter>
        </Panel>
      )}

      <BlocksPanel
        blocks={blocks.data}
        error={blocks.error}
        lastSuccess={blocks.lastSuccess}
        refresh={blocks.refresh}
      />

      {inspecting && (
        <PeerSheet
          address={inspecting}
          firewall={firewall}
          blocks={blocks.data}
          onBlock={(() => {
            const peer = data?.peers.find((p) => p.address === inspecting)
            if (!admin || peer?.private) return undefined
            return () =>
              setBlockFor({
                address: inspecting,
                context: peer
                  ? `connected to ${peer.service ?? peer.processes[0] ?? "this server"}`
                  : "seen in the socket history",
              })
          })()}
          onClose={() => setInspecting(undefined)}
        />
      )}
      {blockFor && (
        <BlockDialog
          address={blockFor.address}
          context={blockFor.context}
          onClose={() => setBlockFor(undefined)}
          onBlocked={() => {
            blocks.refresh()
            refresh()
          }}
        />
      )}
    </Workspace>
  )
}

type Reading = { peers: number; internet: number; total: number }

/**
 * Every read's counts since the page opened, at most an hour of them, for the
 * tiles' trends. The backend keeps no history of connections, and a count that
 * is only ever "now" cannot show that the burst was ten minutes ago.
 */
function useReadings(data: Connections | undefined): Reading[] {
  const [history, setHistory] = useState<Reading[]>([])
  useEffect(() => {
    if (!data) return
    const next = {
      peers: data.peers.length,
      internet: data.peers.filter((p) => !p.private).length,
      total: data.total,
    }
    // eslint-disable-next-line react-hooks/set-state-in-effect -- appending a read, not deriving from props
    setHistory((previous) => [...previous.slice(-359), next])
  }, [data])
  return history
}
