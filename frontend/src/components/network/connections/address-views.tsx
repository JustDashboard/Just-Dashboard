"use client"

import Link from "next/link"
import { useMemo } from "react"
import { get } from "@/lib/api"
import { bytes, duration, plural, relativeTime } from "@/lib/format"
import type { FirewallStatus, NetworkPath, Peer, PortRange } from "@/lib/types"
import { flowBytes, flowOwnerName, type FlowReport, type FlowRow } from "@/lib/network-flows"
import {
  foldHistoricalFlows,
  hourBounds,
  millis,
  sourceCovers,
  type AddressBlock,
  type ConnectionDetail,
} from "@/lib/network-traffic"
import { usePoll, type PollState } from "@/hooks/use-poll"
import { useNow } from "@/components/deploy/vocabulary"
import { FormFact, FormFacts } from "@/components/form"
import { Panel, PanelBody, PanelFooter, PanelHeader, PanelToolbar } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { Status } from "@/components/status-dot"
import { EmptyNote, ErrorState, LoadingRows, Notice } from "@/components/state"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { toolHref } from "@/components/security/address-verbs"
import { RX, TX } from "@/components/network/rate-pair"
import { ProcessList } from "@/components/security/marks"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { foldedWords } from "@/components/network/connections/model"
import { PortLinks, ProgramGlyph } from "@/components/network/connections/rows"

const endpoint = (addr: string, port: number) =>
  addr.includes(":") ? `[${addr}]:${port}` : `${addr}:${port}`

/**
 * An address's live view, for an administrator: what the summary folded, then
 * every live tuple with how long it has been seen, its byte counters and TCP's
 * round trip; the tuples seen closing in the last fifteen minutes; and the
 * layers it crosses — what the firewall says about it, the route this server
 * answers it by, what the socket history kept of it in the last day — with
 * the next steps handed to the pages that own them. Everything is read,
 * nothing probes the address.
 *
 * It was the peer sheet over the table until 0.8.7.
 */
export function AddressLive({
  address,
  peer,
  detail,
  firewall,
  block,
  range,
}: {
  address: string
  peer?: Peer
  detail: PollState<ConnectionDetail>
  firewall: FirewallStatus | undefined
  block?: AddressBlock
  range?: PortRange | null
}) {
  const now = useNow(1000)
  const d = detail.data
  const tcp = (d?.sockets ?? []).filter((s) => s.rxBytes !== undefined)
  const rx = tcp.reduce((n, s) => n + (s.rxBytes ?? 0), 0)
  const tx = tcp.reduce((n, s) => n + (s.txBytes ?? 0), 0)
  const oldest = (d?.sockets ?? []).reduce<string | undefined>(
    (min, s) => (s.firstSeen && (!min || s.firstSeen < min) ? s.firstSeen : min),
    undefined,
  )

  if (!d && detail.error) return <ErrorState error={detail.error} onRetry={detail.refresh} />

  return (
    <div className="flex min-w-0 flex-col gap-8">
      {d && (
        <NetworkReadWarning
          error={detail.error}
          refresh={detail.refresh}
          lastSuccess={detail.lastSuccess}
          reading="connection detail"
        />
      )}
      <FormFacts>
        <FormFact label="Sockets" mono>
          {d ? d.sockets.length : "…"}
        </FormFact>
        <FormFact label="Seen since" mono>
          {!d ? "…" : oldest ? relativeTime(oldest) : "—"}
        </FormFact>
        <FormFact label="TCP carried" mono>
          {d ? `↓ ${bytes(rx)} · ↑ ${bytes(tx)}` : "…"}
        </FormFact>
        <FormFact label="Closed lately" mono>
          {d ? d.closed.length : "…"}
        </FormFact>
      </FormFacts>
      {peer && <Folded peer={peer} range={range} />}
      {d?.countersError && (
        <Notice title="TCP counters could not be read">{d.countersError}</Notice>
      )}

      <Panel aria-label="Live sockets">
        <PanelHeader
          title="Live sockets"
          actions={
            d && (
              <span className="numeric text-hint text-muted-foreground">
                {plural(d.sockets.length, "socket")}
              </span>
            )
          }
        />
        <PanelBody flush>
          {!d ? (
            <LoadingRows rows={3} />
          ) : d.sockets.length === 0 ? (
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
                  <TableRow key={`${s.protocol}-${s.localAddress}-${s.localPort}-${s.remotePort}`}>
                    <TableCell className="font-mono uppercase">{s.protocol}</TableCell>
                    <TableCell className="font-mono">
                      {endpoint(s.localAddress, s.localPort)}
                    </TableCell>
                    <TableCell className="numeric font-mono">{s.remotePort}</TableCell>
                    <TableCell className="font-mono text-hint">{s.status || "connected"}</TableCell>
                    <TableCell className="max-md:hidden">{s.process || "—"}</TableCell>
                    <TableCell className="numeric text-right">
                      {s.firstSeen ? `≥ ${duration((now - Date.parse(s.firstSeen)) / 1000)}` : "—"}
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
        </PanelBody>
      </Panel>

      <Panel plain>
        <PanelHeader
          title="Closed in the last 15 minutes"
          actions={
            d && <span className="numeric text-hint text-muted-foreground">{d.closed.length}</span>
          }
        />
        <PanelBody flush>
          {!d ? (
            <LoadingRows rows={1} />
          ) : d.closed.length === 0 ? (
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
        </PanelBody>
      </Panel>

      <Layers address={address} firewall={firewall} block={block} />

      {d && (
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
      )}
    </div>
  )
}

/**
 * What the summary folded of the address: the program it reached, the ports,
 * the transports, the far end's ports and the states — the lines the list's
 * row once carried under the address. Every role reads this; it is all a role
 * without the detail's capability has of the address.
 */
export function Folded({ peer, range }: { peer: Peer; range?: PortRange | null }) {
  const words = foldedWords(peer)
  return (
    <dl
      aria-label="What the summary folded"
      className="grid min-w-0 grid-cols-[auto_minmax(0,1fr)] gap-x-6 gap-y-2 text-body"
    >
      <dt className="text-muted-foreground">Reached</dt>
      <dd className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
        <ProgramGlyph name={peer.processes[0] || peer.service || ""} />
        <span className="font-medium">
          {peer.processes[0] || peer.service || "Unknown program"}
        </span>
        <span className="font-mono text-hint text-muted-foreground">
          <PortLinks ports={peer.ports} range={range} />
          {peer.service && ` · ${peer.service}`}
        </span>
      </dd>
      {peer.processes.length > 1 && (
        <>
          <dt className="text-muted-foreground">Also held by</dt>
          <dd className="min-w-0">
            <ProcessList names={peer.processes.slice(1)} />
          </dd>
        </>
      )}
      {words && (
        <>
          <dt className="text-muted-foreground">Sockets</dt>
          <dd className="min-w-0 font-mono text-hint">{words}</dd>
        </>
      )}
    </dl>
  )
}

/**
 * The layers the address crosses: the firewall's word on it, the route this
 * server answers it by, and what socket history kept of it in the last day,
 * with the pages that go further.
 */
function Layers({
  address,
  firewall,
  block,
}: {
  address: string
  firewall: FirewallStatus | undefined
  block?: AddressBlock
}) {
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
  // Rules for any source cover every address; only the ones that name this
  // address or its network are about it.
  const covering = (firewall?.rules ?? []).filter((r) => sourceCovers(r.from, address))
  const rules = covering
    .filter((r) => !sourceCovers(r.from, "0.0.0.0") && !sourceCovers(r.from, "::"))
    .sort((a, b) => Number(/deny|reject/i.test(b.action)) - Number(/deny|reject/i.test(a.action)))
  const general = covering.length - rules.length
  const retained = useMemo(
    () =>
      foldHistoricalFlows(history.data?.rows ?? [], (row) => flowOwnerName(row.socket.owner)).find(
        (p) => p.address === address,
      ),
    [history.data?.rows, address],
  )
  return (
    <Panel plain aria-label="Across the layers">
      <PanelHeader title="Across the layers" />
      <PanelBody flush>
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
        <p className="flex flex-wrap gap-x-4 gap-y-1 px-1 pt-3 text-hint">
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
      </PanelBody>
    </Panel>
  )
}

/**
 * What socket history kept of the address in one hour: the rows it holds for
 * it, each a socket with its owner, its two ends, when it was first and last
 * seen and what TCP carried — the list's past hour, for one address. With
 * the history off there is no past to read, and it says where to turn it on.
 */
export function AddressPast({
  address,
  hours,
  hour,
  onHour,
}: {
  address: string
  hours: { from: string; to: string; label: string }[]
  hour: string
  onHour: (from: string) => void
}) {
  const chosen = hours.find((h) => h.from === hour) ?? hours[0]
  const report = usePoll<FlowReport>(
    (signal) =>
      get("/network/flows/", { address, from: chosen.from, to: chosen.to, limit: 500 }, signal),
    60_000,
    [address, chosen.from],
  )
  const rows = useMemo(
    () => (report.data?.rows ?? []).filter((row) => row.socket.remoteAddress === address),
    [report.data?.rows, address],
  )
  const folded = useMemo(
    () => foldHistoricalFlows(rows, (row) => flowOwnerName(row.socket.owner))[0],
    [rows],
  )
  const coverage = report.data?.coverageHours.find((c) =>
    c.hour.startsWith(chosen.from.slice(0, 13)),
  )
  return (
    <Panel aria-label="Recorded sockets">
      <PanelHeader
        title="Recorded sockets"
        actions={
          report.data && (
            <span className="numeric text-hint text-muted-foreground">
              {plural(rows.length, "socket")}
              {folded &&
                ` · ↓ ${flowBytes(folded.rxBytes)} ↑ ${flowBytes(folded.txBytes)} · ${folded.owners.join(", ")}`}
            </span>
          )
        }
      />
      <PanelToolbar>
        <Select value={chosen.from} onValueChange={onHour}>
          <SelectTrigger className="w-72 max-w-full font-mono" size="sm" aria-label="Which hour">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {hours.map((h) => (
              <SelectItem key={h.from} value={h.from} className="font-mono">
                {h.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </PanelToolbar>
      <PanelBody flush>
        {report.data && (
          <div className="px-4 pt-3 empty:hidden">
            <NetworkReadWarning
              error={report.error}
              refresh={report.refresh}
              lastSuccess={report.lastSuccess}
              reading="socket history"
            />
          </div>
        )}
        {!report.data ? (
          report.error ? (
            <ErrorState error={report.error} onRetry={report.refresh} />
          ) : (
            <LoadingRows rows={3} />
          )
        ) : !report.data.settings.enabled && report.data.rows.length === 0 ? (
          <div className="p-4">
            <Notice title="Socket history is off">
              Nothing is kept of connections once they close, so there is no past hour to read.{" "}
              <Link href="/network/flows" className="underline underline-offset-4">
                Turn on socket history
              </Link>{" "}
              to record from now on.
            </Notice>
          </div>
        ) : rows.length === 0 ? (
          <EmptyNote>Socket history kept nothing of {address} in this hour.</EmptyNote>
        ) : (
          <Table aria-label={`Recorded sockets with ${address}`}>
            <TableHeader>
              <TableRow>
                <TableHead>Protocol</TableHead>
                <TableHead>This server</TableHead>
                <TableHead>Remote port</TableHead>
                <TableHead>Owner</TableHead>
                <TableHead className="max-md:hidden">Seen</TableHead>
                <TableHead className="text-right">↓ / ↑</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((row) => (
                <RecordedSocket key={row.id} row={row} />
              ))}
            </TableBody>
          </Table>
        )}
      </PanelBody>
      {report.data && (
        <PanelFooter className="text-hint text-muted-foreground">
          {coverage
            ? `${plural(coverage.samples, "sample")} in this hour${coverage.failedSources ? `, ${plural(coverage.failedSources, "failed source")}` : ""}${coverage.discardedIntervals ? `, ${plural(coverage.discardedIntervals, "discarded interval")}` : ""}. `
            : "No sample was taken in this hour. "}
          {report.data.truncated && "More rows were kept than are shown. "}
          Sockets that opened and closed between two samples are not in the record; UDP has no byte
          counters.
        </PanelFooter>
      )}
    </Panel>
  )
}

function RecordedSocket({ row }: { row: FlowRow }) {
  const s = row.socket
  return (
    <TableRow>
      <TableCell className="font-mono uppercase">{s.protocol}</TableCell>
      <TableCell className="font-mono">{endpoint(s.localAddress, s.localPort)}</TableCell>
      <TableCell className="numeric font-mono">{s.remotePort ?? "—"}</TableCell>
      <TableCell>{flowOwnerName(s.owner)}</TableCell>
      <TableCell className="numeric text-hint text-muted-foreground max-md:hidden">
        {relativeTime(row.firstSeen)} to {relativeTime(row.lastSeen)}
      </TableCell>
      <TableCell className="numeric text-right font-mono text-hint whitespace-nowrap">
        <span style={{ color: RX }}>{flowBytes(row.rxBytes)}</span> /{" "}
        <span style={{ color: TX }}>{flowBytes(row.txBytes)}</span>
      </TableCell>
    </TableRow>
  )
}
