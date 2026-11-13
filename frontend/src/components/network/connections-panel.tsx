"use client"

import { useEffect, useMemo, useState } from "react"
import Link from "next/link"
import { useRouter, useSearchParams } from "next/navigation"
import { NetworkDevice } from "@/components/icons"
import { get } from "@/lib/api"
import { plural } from "@/lib/format"
import type { Connections, PortsMeta } from "@/lib/types"
import { cn } from "@/lib/utils"
import { Workspace, WorkspaceHelp } from "@/components/workspace/workspace"
import { useFilterHistory } from "@/components/workspace/history"
import { useHeldList } from "@/components/workspace/held-list"
import { usePoll } from "@/hooks/use-poll"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { useAuth } from "@/hooks/use-auth"
import { PageContext, SearchInput } from "@/components/page"
import { ChoiceList } from "@/components/flow"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { EmptyNote, EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { Status } from "@/components/status-dot"
import { tabClasses } from "@/components/tabs"
import { addressVerbs } from "@/components/security/address-verbs"
import { Button } from "@/components/ui/button"
import { portsHref } from "@/components/proxy/ports-list"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { recentHours, type AddressBlock } from "@/lib/network-traffic"
import { BlockDialog } from "@/components/network/connections/block-dialog"
import { BlocksPanel } from "@/components/network/connections/blocks-panel"
import { ConnectionsPicture } from "@/components/network/connections/picture"
import { PeerRow } from "@/components/network/connections/rows"
import { RecordedConnections } from "@/components/network/connections/recorded"
import {
  CONNECTIONS_FILTER_DEFAULTS,
  CONNECTIONS_FILTER_KEY,
  addressHref,
  blockContext,
  blockable,
  matchPeers,
  parseAddress,
} from "@/components/network/connections/model"

/**
 * Who is talking to this machine right now.
 *
 * The ports view answers what is listening; this answers who took it up on the
 * offer, which is the question during an incident. Folded by remote address
 * rather than listed one socket per row: a busy host holds thousands, and
 * forty of them are one client — a raw table buries the single address with
 * two hundred connections underneath four hundred rows of noise.
 *
 * It opens on the server's line — its sockets, how many addresses hold them
 * and how many of those are from the internet — and the picture of where the
 * callers are and what they reached (`connections/picture.tsx`). Four tiles
 * stood there until 0.8.7, every one a figure the page already said: the
 * list's count, the "From the internet" chip, the picture's centre.
 *
 * Under them each address is one card of one height: the network it is on,
 * the program it reached as that product with the ports it reached it on,
 * its sockets and active ones in columns of their own. A card opens the
 * address's own page (`connections/address-page.tsx`), which holds what the
 * peer sheet held — the tuples, what closed, the layers, a past hour, the
 * block. A past hour of the whole list is read from the socket history in
 * the same shape. A block asks why and until when, and can open an incident;
 * the blocks made here are listed under the list with their end.
 */
export function ConnectionsPanel() {
  const { can } = useAuth()
  const router = useRouter()
  const params = useSearchParams()
  const [filters, setFilters] = useFilterHistory(
    CONNECTIONS_FILTER_KEY,
    CONNECTIONS_FILTER_DEFAULTS,
  )
  const query = filters.q
  const scope = filters.scope === "public" ? "public" : "all"
  const admin = can("system.admin")
  const when = filters.when === "recorded" && admin ? "recorded" : "live"
  const setQuery = (q: string) => setFilters((previous) => ({ ...previous, q }))
  const setScope = (scope: string) => setFilters((previous) => ({ ...previous, scope }), true)
  const setWhen = (when: string) => setFilters((previous) => ({ ...previous, when }), true)
  const setHour = (hour: string) => setFilters((previous) => ({ ...previous, hour }), true)
  const hours = useMemo(() => recentHours(new Date(), 24), [])
  const [blockFor, setBlockFor] = useState<{ address: string; context: string }>()

  // One address named in the link is that address's page: the sheet it
  // would have opened is a page now.
  const peerParam = parseAddress(params.get("peer") ?? undefined)
  useEffect(() => {
    if (peerParam) router.replace(addressHref(peerParam))
  }, [peerParam, router])

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

  const matches = useMemo(
    () => matchPeers(data?.peers ?? [], scope, query),
    [data?.peers, scope, query],
  )
  const held = useHeldList(
    data ? matches : undefined,
    (peer) => peer.address,
    JSON.stringify(filters),
  )
  const peers = held.rows
  const fromInternet = (data?.peers ?? []).filter((p) => !p.private).length
  const blocked = new Set(
    (blocks.data ?? []).filter((b) => b.state === "active").map((b) => b.address),
  )

  const search = (
    <SearchInput
      dense
      data-workspace-search
      aria-label="Filter connections"
      placeholder="Address, port or process"
      value={query}
      onChange={(e) => setQuery(e.target.value)}
      containerClassName="sm:w-64"
    />
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
      <PageContext eyebrow="Network" title="Connections" />
      <HostIdentity
        className="animate-rise"
        fallback={NetworkDevice}
        title="Connections"
        facts={
          data ? (
            <>
              <span className="numeric text-foreground">{plural(data.total, "socket")}</span>
              <FactDot />
              <span className="numeric">
                {plural(data.peers.length, "remote address", "remote addresses")},{" "}
                <span className={cn(fromInternet > 0 && "text-warning")}>
                  {fromInternet} from the internet
                </span>
              </span>
            </>
          ) : (
            <span>
              {error ? "The connection table could not be read" : "Reading the connection table…"}
            </span>
          )
        }
        // Drawn in every state, so the line has its one shape before the
        // first read lands.
        state={
          error ? (
            <Status tone="warning" label="read failed" />
          ) : data ? (
            <Status state="running" label="read every 10s" />
          ) : (
            <Status tone="unknown" label="reading" />
          )
        }
      />

      <NetworkReadWarning
        error={data ? error : undefined}
        refresh={refresh}
        lastSuccess={lastSuccess}
      />

      {/* A first read that failed has no shape to draw: the Live view says
          so where its list would be, and a past hour is still a press away. */}
      {(data || !error) && (
        <ConnectionsPicture
          data={data}
          loading={loading}
          internetOnly={scope === "public"}
          onInternet={() => setScope(scope === "public" ? "all" : "public")}
          onProgram={(name) => {
            setFilters((previous) => ({ ...previous, q: name, when: "live" }), true)
          }}
        />
      )}

      <section aria-label="Remote addresses" className="flex min-w-0 flex-col gap-3">
        {/* The section's title line: the two views as the strip, the held
            rows and the shortcuts at its end. */}
        <div className="flex min-w-0 flex-wrap items-center justify-between gap-x-4 gap-y-2 border-b border-hairline">
          {admin ? (
            <div role="tablist" aria-label="Which connections" className="flex gap-1">
              {(
                [
                  ["live", "Live", data ? matches.length : undefined],
                  ["recorded", "A past hour", undefined],
                ] as const
              ).map(([value, label, count]) => (
                <button
                  key={value}
                  type="button"
                  role="tab"
                  aria-selected={when === value}
                  onClick={() => setWhen(value)}
                  className={tabClasses(when === value, "h-10")}
                >
                  {label}
                  {count !== undefined && (
                    <span className="numeric text-micro tabular-nums opacity-80">{count}</span>
                  )}
                </button>
              ))}
            </div>
          ) : (
            <p className="flex h-10 items-center text-title font-medium">Live connections</p>
          )}
          <span className="flex items-center gap-3 pb-1">
            {when === "live" && held.pending > 0 && (
              <Button
                size="xs"
                aria-label={`Show ${held.pending} new addresses`}
                onClick={held.reveal}
              >
                <span role="status">{held.pending} new addresses</span> · Show
              </Button>
            )}
            <WorkspaceHelp compact />
          </span>
        </div>

        {when === "recorded" ? (
          <RecordedConnections
            hours={hours}
            hour={filters.hour}
            onHour={setHour}
            query={query}
            search={search}
          />
        ) : !data && error ? (
          <ErrorState error={error} onRetry={refresh} retryable />
        ) : (
          <>
            <div className="flex min-w-0 flex-wrap items-center gap-2">
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
                  From the internet {data && fromInternet}
                </ToggleGroupItem>
              </ToggleGroup>
              <span className="flex-1" />
              {search}
            </div>
            {!data ? (
              <LoadingRows rows={4} />
            ) : peers.length === 0 ? (
              query.trim() ? (
                <EmptyNote>No connection matches &ldquo;{query.trim()}&rdquo;.</EmptyNote>
              ) : (
                <EmptyState
                  icon={NetworkDevice}
                  title={
                    scope === "public" ? "Nothing connected from the internet" : "No connections"
                  }
                  description={
                    scope === "public"
                      ? "Every caller is on a private network or the tailnet."
                      : undefined
                  }
                  action={
                    <Link
                      href={portsHref({})}
                      className="text-body font-medium text-brand hover:underline"
                    >
                      What is listening
                    </Link>
                  }
                />
              )
            ) : (
              <ChoiceList aria-label="Live addresses">
                {peers.map((peer) => (
                  <PeerRow
                    key={peer.address}
                    peer={peer}
                    blocked={blocked.has(peer.address)}
                    range={range}
                    verbs={addressVerbs({
                      ip: peer.address,
                      block:
                        admin && blockable(peer.address, peer.private) && !blocked.has(peer.address)
                          ? () =>
                              setBlockFor({ address: peer.address, context: blockContext(peer) })
                          : undefined,
                      blocking: blockFor?.address === peer.address,
                      navigate: (href) => router.push(href),
                    }).map((verb) => ({ ...verb, inline: false }))}
                  />
                ))}
              </ChoiceList>
            )}
            {data && (
              <p className="text-hint text-muted-foreground">
                {data.peers.length > 0 && (
                  <>
                    {peers.length} of {data.peers.length} addresses · grouped by remote address ·
                    socket counts include connections still opening or closing.
                  </>
                )}
                {data.quality && (
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
              </p>
            )}
          </>
        )}
      </section>

      <BlocksPanel
        blocks={blocks.data}
        error={blocks.error}
        lastSuccess={blocks.lastSuccess}
        refresh={blocks.refresh}
      />

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
