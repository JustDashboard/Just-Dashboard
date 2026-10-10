"use client"

import Link from "next/link"
import { useMemo } from "react"
import { get } from "@/lib/api"
import { plural, relativeTime } from "@/lib/format"
import { flowBytes, flowOwnerName, type FlowReport } from "@/lib/network-flows"
import { foldHistoricalFlows } from "@/lib/network-traffic"
import { usePoll } from "@/hooks/use-poll"
import { Panel, PanelBody, PanelFooter, PanelHeader, PanelToolbar } from "@/components/panel"
import { EmptyNote, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { PeerIdentity } from "@/components/security/marks"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { RX, TX } from "@/components/network/rate-pair"
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

/**
 * Who was connected during a past hour, from the opt-in socket history,
 * folded by remote address in the live table's shape. It says what the hour's
 * record could see: how many samples were taken, which sources failed, and
 * whether more rows were kept than are shown. With the history off there is
 * no past to choose from, and the panel says so and where to turn it on.
 */
export function RecordedConnections({
  hours,
  hour,
  onHour,
  query,
  onInspect,
}: {
  hours: { from: string; to: string; label: string }[]
  hour: string
  onHour: (from: string) => void
  query: string
  onInspect: (address: string) => void
}) {
  const chosen = hours.find((h) => h.from === hour) ?? hours[0]
  const report = usePoll<FlowReport>(
    (signal) => get("/network/flows/", { from: chosen.from, to: chosen.to, limit: 1000 }, signal),
    60_000,
    [chosen.from],
  )
  const peers = useMemo(() => {
    const folded = foldHistoricalFlows(report.data?.rows ?? [], (row) =>
      flowOwnerName(row.socket.owner),
    )
    const q = query.trim().toLowerCase()
    return q
      ? folded.filter((p) =>
          `${p.address} ${p.owners.join(" ")} ${p.localPorts.join(" ")}`.toLowerCase().includes(q),
        )
      : folded
  }, [report.data?.rows, query])
  const coverage = report.data?.coverageHours.find((c) =>
    c.hour.startsWith(chosen.from.slice(0, 13)),
  )
  return (
    <Panel aria-label="Recorded connections">
      <PanelHeader
        title="Recorded connections"
        actions={
          report.data && (
            <span className="numeric text-hint text-muted-foreground">
              {plural(peers.length, "address", "addresses")} · from socket history
            </span>
          )
        }
      />
      <PanelToolbar>
        <Select value={chosen.from} onValueChange={onHour}>
          <SelectTrigger className="w-72 font-mono" size="sm" aria-label="Which hour">
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
          <div className="px-4 pt-3">
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
            <LoadingPanel />
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
        ) : peers.length === 0 ? (
          <EmptyNote>No remote address was recorded in this hour.</EmptyNote>
        ) : (
          <Table containerClassName="max-h-[36rem]">
            <TableHeader className={stickyTableHeader}>
              <TableRow>
                <TableHead>Remote address</TableHead>
                <TableHead className="w-full">Owners</TableHead>
                <TableHead className="text-right">Sockets</TableHead>
                <TableHead className="text-right">TCP ↓ / ↑</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {peers.map((p) => (
                <TableRow key={p.address} data-recorded-item={p.address}>
                  <TableCell className="py-3">
                    <button
                      type="button"
                      onClick={() => onInspect(p.address)}
                      aria-label={`Inspect ${p.address}`}
                      className="rounded-sm text-left focus-ring"
                    >
                      <PeerIdentity ip={p.address} />
                    </button>
                  </TableCell>
                  <TableCell className="whitespace-normal">
                    <span className="block text-body">{p.owners.join(", ")}</span>
                    <span className="block font-mono text-hint text-muted-foreground">
                      {p.protocols.join(", ")} · ports {p.localPorts.join(", ")} · seen{" "}
                      {relativeTime(p.firstSeen)} to {relativeTime(p.lastSeen)}
                    </span>
                  </TableCell>
                  <TableCell className="numeric text-right">{p.sockets}</TableCell>
                  <TableCell className="numeric text-right font-mono text-hint whitespace-nowrap">
                    <span style={{ color: RX }}>{flowBytes(p.rxBytes)}</span> /{" "}
                    <span style={{ color: TX }}>{flowBytes(p.txBytes)}</span>
                  </TableCell>
                </TableRow>
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
