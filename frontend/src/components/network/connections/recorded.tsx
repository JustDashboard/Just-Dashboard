"use client"

import Link from "next/link"
import { useMemo } from "react"
import { get } from "@/lib/api"
import { plural } from "@/lib/format"
import { flowOwnerName, type FlowReport } from "@/lib/network-flows"
import { foldHistoricalFlows } from "@/lib/network-traffic"
import { usePoll } from "@/hooks/use-poll"
import { ChoiceList } from "@/components/flow"
import { EmptyNote, ErrorState, LoadingRows, Notice } from "@/components/state"
import { NetworkReadWarning } from "@/components/network/read-warning"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { RecordedRow } from "@/components/network/connections/rows"

/**
 * Who was connected during a past hour, from the opt-in socket history,
 * folded by remote address in the live list's shape; each address opens its
 * own page on the same hour. It says what the hour's record could see: how
 * many samples were taken, which sources failed, and whether more rows were
 * kept than are shown. With the history off there is no past to choose from,
 * and it says so and where to turn it on.
 */
export function RecordedConnections({
  hours,
  hour,
  onHour,
  query,
  search,
}: {
  hours: { from: string; to: string; label: string }[]
  hour: string
  onHour: (from: string) => void
  query: string
  /** The list's search, shared with the live view, at the toolbar's end. */
  search: React.ReactNode
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
    <div aria-label="Recorded connections" role="region" className="flex min-w-0 flex-col gap-3">
      <div className="flex min-w-0 flex-wrap items-center gap-2">
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
        {report.data && (
          <span className="numeric text-hint text-muted-foreground">
            {plural(peers.length, "address", "addresses")} · from socket history
          </span>
        )}
        <span className="flex-1" />
        {search}
      </div>
      {report.data && (
        <NetworkReadWarning
          error={report.error}
          refresh={report.refresh}
          lastSuccess={report.lastSuccess}
          reading="socket history"
        />
      )}
      {!report.data ? (
        report.error ? (
          <ErrorState error={report.error} onRetry={report.refresh} />
        ) : (
          <LoadingRows rows={4} />
        )
      ) : !report.data.settings.enabled && report.data.rows.length === 0 ? (
        <Notice title="Socket history is off">
          Nothing is kept of connections once they close, so there is no past hour to read.{" "}
          <Link href="/network/flows" className="underline underline-offset-4">
            Turn on socket history
          </Link>{" "}
          to record from now on.
        </Notice>
      ) : peers.length === 0 ? (
        <EmptyNote>No remote address was recorded in this hour.</EmptyNote>
      ) : (
        <ChoiceList aria-label="Recorded addresses">
          {peers.map((p) => (
            <RecordedRow key={p.address} peer={p} hour={chosen.from} />
          ))}
        </ChoiceList>
      )}
      {report.data && (
        <p className="text-hint text-muted-foreground">
          {coverage
            ? `${plural(coverage.samples, "sample")} in this hour${coverage.failedSources ? `, ${plural(coverage.failedSources, "failed source")}` : ""}${coverage.discardedIntervals ? `, ${plural(coverage.discardedIntervals, "discarded interval")}` : ""}. `
            : "No sample was taken in this hour. "}
          {report.data.truncated && "More rows were kept than are shown. "}
          Sockets that opened and closed between two samples are not in the record; UDP has no byte
          counters.
        </p>
      )}
    </div>
  )
}
