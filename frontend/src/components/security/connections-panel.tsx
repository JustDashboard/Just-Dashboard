"use client"

import { Fragment, useMemo, useState } from "react"
import Link from "next/link"
import { useRouter, useSearchParams } from "next/navigation"
import { NetworkDevice, Servers } from "@/components/icons"
import { notify } from "@/lib/toast"
import { get } from "@/lib/api"
import type { Connections, PortsMeta } from "@/lib/types"
import { useSessionState, useViewState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { PageContext, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelFooter, PanelHeader, PanelToolbar } from "@/components/panel"
import { StatGrid, StatLink, StatTile } from "@/components/stat-tile"
import { EmptyNote, EmptyState, ErrorState, LoadingPanel } from "@/components/state"
import { PeerIdentity, ProcessList } from "@/components/security/marks"
import { addressVerbs, blockAddress } from "@/components/security/address-verbs"
import { AreaFindings } from "@/components/security/posture-panel"
import { useSecurity } from "@/components/security/security-context"
import { ProductLogo, processProduct } from "@/components/product-logo"
import { Meter } from "@/components/meter"
import { VerbActions } from "@/components/verbs"
import { chosenPort, portsHref } from "@/components/proxy/ports-list"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
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
 */
export function ConnectionsPanel() {
  const { can } = useAuth()
  const { posture, applyFix } = useSecurity()
  const router = useRouter()
  const [scope, setScope] = useViewState<"all" | "public">("security.connections.scope", "all")
  // A link from a socket's clients arrives narrowed to its port or a peer.
  const [query, setQuery] = useSessionState(
    "security.connections.query",
    "",
    useSearchParams().get("q"),
  )
  const [blocking, setBlocking] = useState<string | null>(null)
  const { data, error, loading, refresh } = usePoll<Connections>(
    (signal) => get("/connections", undefined, signal),
    10000,
  )
  // The kernel's ephemeral range tells this host's side of a connection that
  // somebody chose — a listener's port, which the ports page can name — from
  // one the kernel picked for an outgoing call, which nothing listens on.
  const range = usePoll((signal) => get<PortsMeta>("/ports/meta", undefined, signal), 0).data
    ?.ephemeralRange

  const peers = useMemo(() => {
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
  const most = Math.max(1, ...(data?.peers ?? []).map((p) => p.count))
  const fromInternet = (data?.peers ?? []).filter((p) => !p.private).length

  const header = <PageContext eyebrow="Security" title="Connections" />

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
        <ErrorState error={error} />
      </>
    )
  }

  const block = async (ip: string) => {
    setBlocking(ip)
    try {
      await blockAddress(ip, "blocked from connections")
      notify.success(`${ip} blocked`, {
        description: "The deny rule sits in front of every allow and does not expire.",
      })
      refresh()
    } catch (err) {
      notify.error("Could not add the rule", err)
    } finally {
      setBlocking(null)
    }
  }

  return (
    <>
      {header}

      {/* The figures, then the addresses they describe. Listening goes to the
          page that names each socket: this page is who arrived, that one is
          what was open for them. */}
      <StatGrid columns={4}>
        <StatTile
          label="Remote addresses"
          value={data?.peers.length ?? 0}
          tone={fromInternet > 0 ? "warning" : "default"}
          hint={fromInternet > 0 ? `${fromInternet} from the internet` : "all private or loopback"}
        />
        <StatTile label="Sockets" value={data?.total ?? 0} hint="every connection counted" />
        <StatLink href={portsHref({})} label="Listening ports">
          <StatTile
            className="h-full transition-colors group-hover:bg-row-hover"
            label="Listening"
            value={data?.listening ?? 0}
            hint="sockets waiting for a caller"
          />
        </StatLink>
        <StatTile label="Loopback" value={data?.loopback ?? 0} hint="never left the machine" />
      </StatGrid>

      {/* The "ports" findings are about listeners, which live on the proxy's
          Ports page; they are shown here because this is the page somebody
          opens to ask what is reachable. */}
      <AreaFindings posture={posture} area="ports" onFix={applyFix} />

      <Panel>
        <PanelHeader
          title="Live connections"
          actions={
            <span className="numeric text-hint text-muted-foreground">
              {peers.length} addresses · refreshes every 10s
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
                    <TableRow key={peer.address} className="group">
                      <TableCell className="py-4">
                        <PeerIdentity ip={peer.address} />
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
                              can("system.admin") && !peer.private
                                ? () => void block(peer.address)
                                : undefined,
                            blocking: blocking === peer.address,
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
          {peers.length} of {data?.peers.length ?? 0} addresses · grouped by remote address · socket
          counts include connections still opening or closing.
        </PanelFooter>
      </Panel>
    </>
  )
}
