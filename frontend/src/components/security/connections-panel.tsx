"use client"

import { useMemo, useState } from "react"
import { useRouter } from "next/navigation"
import { NetworkDevice } from "@/components/icons"
import { notify } from "@/lib/toast"
import { get } from "@/lib/api"
import type { Connections } from "@/lib/types"
import { useSessionState, useViewState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { PageContext, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelFooter, PanelHeader, PanelToolbar } from "@/components/panel"
import { StatGrid, StatLink, StatTile } from "@/components/stat-tile"
import { EmptyNote, EmptyState, ErrorState, LoadingPanel } from "@/components/state"
import { Reach } from "@/components/security/reach"
import { Address, ProcessList } from "@/components/security/marks"
import { addressVerbs, blockAddress } from "@/components/security/address-verbs"
import { AreaFindings } from "@/components/security/posture-panel"
import { useSecurity } from "@/components/security/security-context"
import { VerbActions } from "@/components/verbs"
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
  const [query, setQuery] = useSessionState("security.connections.query", "")
  const [blocking, setBlocking] = useState<string | null>(null)
  const { data, error, loading, refresh } = usePoll<Connections>(
    (signal) => get("/connections", undefined, signal),
    10000,
  )

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
        <StatLink href="/proxy/ports" label="Listening ports">
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
        <PanelHeader title="Live connections" />
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
              <Table containerClassName="max-h-[calc(100svh-28rem)]">
                <TableHeader className={stickyTableHeader}>
                  <TableRow>
                    <TableHead>Remote address</TableHead>
                    <TableHead>Origin</TableHead>
                    <TableHead>Sockets</TableHead>
                    <TableHead className="hidden sm:table-cell">Reaching</TableHead>
                    <TableHead className="hidden w-full md:table-cell">Process</TableHead>
                    <TableHead className="w-px" />
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {peers.map((peer) => (
                    <TableRow key={peer.address} className="group">
                      <TableCell>
                        <Address ip={peer.address} />
                      </TableCell>
                      <TableCell>
                        <Reach scope={peer.private ? "private" : "internet"} />
                      </TableCell>
                      <TableCell className="numeric">
                        {peer.established}
                        {peer.count !== peer.established && (
                          <span className="text-muted-foreground"> / {peer.count}</span>
                        )}
                      </TableCell>
                      <TableCell className="hidden sm:table-cell">
                        <span className="font-mono">{peer.ports.slice(0, 4).join(", ")}</span>
                        {peer.service && (
                          <span className="ml-1.5 text-muted-foreground">{peer.service}</span>
                        )}
                      </TableCell>
                      <TableCell className="hidden text-muted-foreground md:table-cell">
                        <ProcessList names={peer.processes} />
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
          Most of a healthy host&rsquo;s connections are private, which is what makes the public
          ones worth looking at.
        </PanelFooter>
      </Panel>
    </>
  )
}
