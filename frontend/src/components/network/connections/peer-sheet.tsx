"use client"

import Link from "next/link"
import { useMemo } from "react"
import { get } from "@/lib/api"
import { bytes, duration, plural, relativeTime } from "@/lib/format"
import type { FirewallStatus, NetworkPath } from "@/lib/types"
import { flowBytes, flowOwnerName, type FlowReport } from "@/lib/network-flows"
import {
  foldHistoricalFlows,
  hourBounds,
  millis,
  sourceCovers,
  type AddressBlock,
  type ConnectionDetail,
} from "@/lib/network-traffic"
import { usePoll } from "@/hooks/use-poll"
import { useNow } from "@/components/deploy/vocabulary"
import { SidePanel } from "@/components/side-panel"
import { FormFact, FormFacts } from "@/components/form"
import { Row, RowList } from "@/components/row-list"
import { Status } from "@/components/status-dot"
import { EmptyNote, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { PeerIdentity } from "@/components/security/marks"
import { toolHref } from "@/components/security/address-verbs"
import { RX, TX } from "@/components/network/rate-pair"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

const endpoint = (addr: string, port: number) =>
  addr.includes(":") ? `[${addr}]:${port}` : `${addr}:${port}`

/**
 * One remote address in full: every live tuple with how long it has been
 * seen, its byte counters and TCP's round trip; the tuples to it seen
 * closing in the last fifteen minutes; and the layers it crosses — what the
 * firewall says about it, the route this server answers it by, what the
 * socket history kept of it in the last day — with the next steps handed to
 * the pages that own them. Everything is read, nothing probes the address.
 */
export function PeerSheet({
  address,
  firewall,
  blocks,
  onBlock,
  onClose,
}: {
  address: string
  firewall: FirewallStatus | undefined
  blocks: AddressBlock[] | undefined
  /** Absent where the reader may not block, or the address is private. */
  onBlock?: () => void
  onClose: () => void
}) {
  const now = useNow(1000)
  const detail = usePoll<ConnectionDetail>(
    (signal) => get(`/connections/${encodeURIComponent(address)}`, undefined, signal),
    10_000,
    [address],
  )
  const route = usePoll<NetworkPath>(
    (signal) => get("/network/routing/lookup", { target: address }, signal),
    0,
    [address],
  )
  const history = usePoll<FlowReport>(
    (signal) => {
      const t = Math.floor(Date.now() / 1000)
      const bounds = hourBounds(t - 86400, t)
      return get(
        "/network/flows/",
        { address, from: bounds.from, to: bounds.to, limit: 500 },
        signal,
      )
    },
    60_000,
    [address],
  )
  const d = detail.data
  // Rules for any source cover every address; only the ones that name this
  // address or its network are about it.
  const covering = (firewall?.rules ?? []).filter((r) => sourceCovers(r.from, address))
  const rules = covering
    .filter((r) => !sourceCovers(r.from, "0.0.0.0") && !sourceCovers(r.from, "::"))
    .sort((a, b) => Number(/deny|reject/i.test(b.action)) - Number(/deny|reject/i.test(a.action)))
  const general = covering.length - rules.length
  const block = (blocks ?? []).find((b) => b.address === address && b.state === "active")
  const retained = useMemo(
    () =>
      foldHistoricalFlows(history.data?.rows ?? [], (row) => flowOwnerName(row.socket.owner)).find(
        (p) => p.address === address,
      ),
    [history.data?.rows, address],
  )
  const tcp = (d?.sockets ?? []).filter((s) => s.rxBytes !== undefined)
  const rx = tcp.reduce((n, s) => n + (s.rxBytes ?? 0), 0)
  const tx = tcp.reduce((n, s) => n + (s.txBytes ?? 0), 0)
  const oldest = (d?.sockets ?? []).reduce<string | undefined>(
    (min, s) => (s.firstSeen && (!min || s.firstSeen < min) ? s.firstSeen : min),
    undefined,
  )

  return (
    <SidePanel
      open
      onOpenChange={(open) => !open && onClose()}
      width="xl"
      title={<PeerIdentity ip={address} />}
      description={`Every connection with ${address} and the layers it crosses`}
      actions={
        onBlock && !block ? (
          <Button size="sm" variant="destructive" onClick={onBlock}>
            Block at the firewall
          </Button>
        ) : undefined
      }
    >
      {!d ? (
        detail.error ? (
          <ErrorState error={detail.error} onRetry={detail.refresh} />
        ) : (
          <LoadingPanel />
        )
      ) : (
        <div className="flex flex-col gap-6">
          <NetworkReadWarning
            error={detail.error}
            refresh={detail.refresh}
            lastSuccess={detail.lastSuccess}
            reading="connection detail"
          />
          <FormFacts>
            <FormFact label="Sockets" mono>
              {d.sockets.length}
            </FormFact>
            <FormFact label="Seen since" mono>
              {oldest ? relativeTime(oldest) : "—"}
            </FormFact>
            <FormFact label="TCP carried" mono>
              ↓ {bytes(rx)} · ↑ {bytes(tx)}
            </FormFact>
            <FormFact label="Closed lately" mono>
              {d.closed.length}
            </FormFact>
          </FormFacts>
          {d.countersError && (
            <Notice title="TCP counters could not be read">{d.countersError}</Notice>
          )}

          <div className="flex min-w-0 flex-col gap-2">
            <p className="text-title font-medium">Live connections</p>
            {d.sockets.length === 0 ? (
              <EmptyNote>No live socket to {address} at this read.</EmptyNote>
            ) : (
              <Table aria-label={`Connections with ${address}`}>
                <TableHeader>
                  <TableRow>
                    <TableHead>Protocol</TableHead>
                    <TableHead>This server</TableHead>
                    <TableHead>Remote port</TableHead>
                    <TableHead>State</TableHead>
                    <TableHead className="max-md:hidden">Process</TableHead>
                    <TableHead className="text-right">Seen for</TableHead>
                    <TableHead className="text-right">↓ / ↑</TableHead>
                    <TableHead className="text-right max-md:hidden">RTT</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {d.sockets.map((s) => (
                    <TableRow
                      key={`${s.protocol}-${s.localAddress}-${s.localPort}-${s.remotePort}`}
                    >
                      <TableCell className="font-mono uppercase">{s.protocol}</TableCell>
                      <TableCell className="font-mono">
                        {endpoint(s.localAddress, s.localPort)}
                      </TableCell>
                      <TableCell className="numeric font-mono">{s.remotePort}</TableCell>
                      <TableCell className="font-mono text-hint">
                        {s.status || "connected"}
                      </TableCell>
                      <TableCell className="max-md:hidden">{s.process || "—"}</TableCell>
                      <TableCell className="numeric text-right">
                        {s.firstSeen
                          ? `≥ ${duration((now - Date.parse(s.firstSeen)) / 1000)}`
                          : "—"}
                      </TableCell>
                      <TableCell className="numeric text-right font-mono text-hint">
                        {s.rxBytes === undefined ? (
                          <span className="text-muted-foreground">
                            {s.protocol === "udp" ? "no counters" : "—"}
                          </span>
                        ) : (
                          <>
                            <span style={{ color: RX }}>{bytes(s.rxBytes)}</span> /{" "}
                            <span style={{ color: TX }}>{bytes(s.txBytes)}</span>
                          </>
                        )}
                      </TableCell>
                      <TableCell className="numeric text-right max-md:hidden">
                        {millis(s.rttMs)}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </div>

          <div className="flex min-w-0 flex-col gap-2">
            <p className="text-title font-medium">Closed in the last 15 minutes</p>
            {d.closed.length === 0 ? (
              <EmptyNote>No connection to {address} was seen closing.</EmptyNote>
            ) : (
              <RowList aria-label={`Closed connections with ${address}`}>
                {d.closed.slice(0, 20).map((c) => (
                  <Row
                    key={`${c.protocol}-${c.localPort}-${c.remotePort}-${c.goneBy}`}
                    title={
                      <span className="font-mono">
                        {c.protocol.toUpperCase()} {endpoint(c.localAddress, c.localPort)} ↔{" "}
                        {c.remotePort}
                      </span>
                    }
                    subtitle={`${c.process || "owner unknown"} · open at least ${duration(c.observedSeconds)} · closed between ${new Date(c.lastSeen).toLocaleTimeString()} and ${new Date(c.goneBy).toLocaleTimeString()}`}
                  />
                ))}
              </RowList>
            )}
          </div>

          <div className="flex min-w-0 flex-col gap-2" aria-label="Across the layers">
            <p className="text-title font-medium">Across the layers</p>
            <RowList>
              <Row
                title="Firewall"
                subtitle={
                  block
                    ? `Blocked: ${block.reason}`
                    : rules.length === 0
                      ? firewall
                        ? `No rule names this address; the default policy${general ? ` and ${plural(general, "rule")} for any source` : ""} decide${general ? "" : "s"}.`
                        : "The firewall could not be read."
                      : rules
                          .slice(0, 3)
                          .map(
                            (r) =>
                              `${r.action} ${r.direction ?? "in"} from ${r.from}${r.port ? ` to port ${r.port}` : ""}`,
                          )
                          .join(" · ")
                }
                trailing={
                  <Status
                    tone={
                      block
                        ? "danger"
                        : rules.some((r) => /deny|reject/i.test(r.action))
                          ? "warning"
                          : "unknown"
                    }
                    label={block ? "blocked" : `${plural(rules.length, "rule")}`}
                  />
                }
              />
              <Row
                title="Route"
                subtitle={
                  route.data
                    ? route.data.local
                      ? "A local address of this server."
                      : `Answered through ${route.data.device ?? "?"}${route.data.gateway ? ` via ${route.data.gateway}` : ""}${route.data.source ? ` from ${route.data.source}` : ""}.`
                    : route.error
                      ? `The kernel could not be asked: ${route.error.message}`
                      : "Asking the kernel…"
                }
              />
              <Row
                title="Socket history"
                subtitle={
                  history.error
                    ? "Socket history is an administrator's and could not be read."
                    : !history.data
                      ? "Reading…"
                      : !history.data.settings.enabled && history.data.rows.length === 0
                        ? "Socket history is off; nothing before this page is kept."
                        : retained
                          ? `${plural(retained.sockets, "socket row")} in the last day, first ${relativeTime(retained.firstSeen)} · TCP ↓ ${flowBytes(retained.rxBytes)} ↑ ${flowBytes(retained.txBytes)} · ${retained.owners.join(", ")}`
                          : "No retained socket with this address in the last day."
                }
              />
            </RowList>
            <p className="flex flex-wrap gap-x-4 gap-y-1 text-hint">
              <Link className="underline underline-offset-4" href={toolHref("traceroute", address)}>
                Trace the route
              </Link>
              <Link className="underline underline-offset-4" href={toolHref("asn", address)}>
                Who owns it
              </Link>
              <Link className="underline underline-offset-4" href="/network/investigate">
                Investigate a path
              </Link>
              <Link className="underline underline-offset-4" href="/network/captures">
                Capture packets
              </Link>
              <Link className="underline underline-offset-4" href="/network/flows">
                Socket history
              </Link>
            </p>
          </div>

          <ul
            className="list-disc space-y-1 pl-5 text-hint text-muted-foreground"
            aria-label="What this read cannot see"
          >
            {d.quality.intervalSeconds > 0 && (
              <li>
                The table was read {Math.round(d.quality.intervalSeconds)}s after the one before; a
                connection shorter than that may never have been in it.
              </li>
            )}
            {d.countersTruncated && <li>The host had more sockets than are read for counters.</li>}
            {d.quality.limits.map((l) => (
              <li key={l}>{l}</li>
            ))}
          </ul>
        </div>
      )}
    </SidePanel>
  )
}
