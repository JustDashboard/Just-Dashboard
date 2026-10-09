"use client"

import { get } from "@/lib/api"
import { bytes, relativeTime } from "@/lib/format"
import {
  millis,
  shareLabel,
  type CongestionComparison,
  type CongestionView,
} from "@/lib/network-traffic"
import { usePoll } from "@/hooks/use-poll"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, ErrorState, LoadingPanel } from "@/components/state"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

/**
 * Whether the congestion control is doing better, from this host's own
 * sockets. A socket keeps the algorithm it opened with, so after a switch
 * the host runs both: the live sockets are grouped by algorithm and compared
 * on the same traffic at the same moment, and the groups as they stood just
 * before the last switch are kept beside them. Flipping BBR proves nothing on
 * its own; these are the figures that might, with the caveat that each group
 * is whatever its sockets happened to be carrying.
 */
export function CongestionComparisonPanel() {
  const view = usePoll<CongestionView>(
    (signal) => get("/network/shaping/congestion", undefined, signal),
    30_000,
  )
  const last = view.data?.snapshots[0]
  return (
    <Panel plain aria-label="Congestion control on live sockets">
      <PanelHeader
        title="Congestion control on live sockets"
        actions={
          view.data && (
            <span className="numeric text-hint text-muted-foreground">
              new sockets get <span className="font-mono">{view.data.now.default || "?"}</span> ·
              read {relativeTime(view.data.now.at)}
            </span>
          )
        }
      />
      <PanelBody className="flex flex-col gap-4">
        {view.data && (
          <NetworkReadWarning
            error={view.error}
            refresh={view.refresh}
            lastSuccess={view.lastSuccess}
            reading="congestion comparison"
          />
        )}
        {!view.data ? (
          view.error ? (
            <ErrorState error={view.error} onRetry={view.refresh} />
          ) : (
            <LoadingPanel />
          )
        ) : (
          <>
            <Groups comparison={view.data.now} label="Now" />
            {last ? (
              <div className="flex flex-col gap-2">
                <p className="text-title font-medium">
                  Before the last switch, <span className="font-mono">{last.before}</span> →{" "}
                  <span className="font-mono">{last.after}</span>
                </p>
                <p className="text-hint text-muted-foreground">
                  Kept {relativeTime(last.at)}
                  {last.actor && ` when ${last.actor} switched`}.
                </p>
                <Groups comparison={last.comparison} label="Before the last switch" />
              </div>
            ) : (
              <EmptyNote>
                No switch has been made here yet, so there is no “before” to compare with.
              </EmptyNote>
            )}
            <p className="text-hint text-muted-foreground">{view.data.note}</p>
          </>
        )}
      </PanelBody>
    </Panel>
  )
}

function Groups({ comparison, label }: { comparison: CongestionComparison; label: string }) {
  if (comparison.error) return <EmptyNote>{comparison.error}</EmptyNote>
  if (comparison.groups.length === 0) {
    return <EmptyNote>No established non-loopback TCP socket to compare.</EmptyNote>
  }
  return (
    <div className="min-w-0" aria-label={label}>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Algorithm</TableHead>
            <TableHead className="text-right">Sockets</TableHead>
            <TableHead className="text-right">Median RTT</TableHead>
            <TableHead className="text-right max-sm:hidden">p90 RTT</TableHead>
            <TableHead className="text-right">Resent</TableHead>
            <TableHead className="text-right max-md:hidden">Delivery rate</TableHead>
            <TableHead className="text-right max-md:hidden">Sent</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {comparison.groups.map((g) => (
            <TableRow key={g.algorithm}>
              <TableCell className="font-mono">{g.algorithm}</TableCell>
              <TableCell className="numeric text-right">{g.sockets}</TableCell>
              <TableCell className="numeric text-right">{millis(g.medianRttMs)}</TableCell>
              <TableCell className="numeric text-right max-sm:hidden">
                {millis(g.p90RttMs)}
              </TableCell>
              <TableCell className="numeric text-right">{shareLabel(g.retransmitShare)}</TableCell>
              <TableCell className="numeric text-right max-md:hidden">
                {g.medianDeliveryMbit > 0 ? `${g.medianDeliveryMbit} Mbit/s` : "—"}
              </TableCell>
              <TableCell className="numeric text-right max-md:hidden">
                {bytes(g.bytesSent)}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      {(comparison.loopback > 0 || comparison.truncated) && (
        <p className="mt-2 text-hint text-muted-foreground">
          {comparison.loopback > 0 && `${comparison.loopback} loopback sockets left out. `}
          {comparison.truncated && "The host had more sockets than are read."}
        </p>
      )}
    </div>
  )
}
