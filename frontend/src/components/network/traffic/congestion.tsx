"use client"

import { useState } from "react"
import { get, post } from "@/lib/api"
import { bytes, plural, relativeTime } from "@/lib/format"
import { latencyHead, latencyTerm, latencyWords } from "@/lib/latency-words"
import { notify } from "@/lib/toast"
import type { BBRState } from "@/lib/types"
import {
  millis,
  shareLabel,
  type CongestionComparison,
  type CongestionView,
} from "@/lib/network-traffic"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { OptionList, OptionRow } from "@/components/form"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, ErrorState } from "@/components/state"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

/**
 * How TCP paces itself on this host: the one switch the kernel has for it,
 * and whether the algorithm it chooses is doing better.
 *
 * BBR is the host's, not a device's. A socket keeps the algorithm it opened
 * with, so after a switch the host runs both: the live sockets are grouped by
 * algorithm and compared on the same traffic at the same moment, and the
 * groups as they stood just before the last switch are kept beside them.
 * Flipping BBR proves nothing on its own; these are the figures that might,
 * with the caveat that each group is whatever its sockets happened to be
 * carrying.
 *
 * The two comparisons are tables because they are read down their columns —
 * one algorithm against another — and framed because they are tables (§2).
 * The round trip is in the product's words for latency: "half within" and
 * "9 in 10 within", never a median or a percentile.
 */
export function CongestionPanel({
  bbr,
  onChanged,
  stale,
}: {
  /** Undefined until the shaping read lands. */
  bbr: BBRState | undefined
  onChanged: () => void
  stale: boolean
}) {
  const view = usePoll<CongestionView>(
    (signal) => get("/network/shaping/congestion", undefined, signal),
    30_000,
  )
  const last = view.data?.snapshots[0]
  return (
    <Panel plain aria-label="Congestion control on live sockets">
      <PanelHeader
        title="Congestion control"
        actions={
          view.data && (
            <span className="numeric text-hint text-muted-foreground">
              new sockets get <span className="font-mono">{view.data.now.default || "?"}</span> ·
              read {relativeTime(view.data.now.at)}
            </span>
          )
        }
      />
      <PanelBody className="flex flex-col gap-5">
        {bbr ? (
          <BBRSwitch bbr={bbr} onChanged={onChanged} stale={stale} />
        ) : (
          <div aria-hidden className="flex h-[59px] items-center justify-between max-sm:h-[77px]">
            <Skeleton className="h-3.5 w-56" />
            <Skeleton className="h-[18px] w-8 rounded-full" />
          </div>
        )}
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
            <ErrorState error={view.error} onRetry={view.refresh} retryable />
          ) : (
            <GroupsGhost />
          )
        ) : (
          <>
            <Groups comparison={view.data.now} label="Now" title="On live sockets now" />
            {last ? (
              <Groups
                comparison={last.comparison}
                label="Before the last switch"
                title={
                  <>
                    Before the last switch, <span className="font-mono">{last.before}</span> →{" "}
                    <span className="font-mono">{last.after}</span>
                  </>
                }
                detail={`kept ${relativeTime(last.at)}${last.actor ? ` when ${last.actor} switched` : ""}`}
              />
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

/** The kernel's congestion control as the switch it is, with what is in force and what the host offers. */
function BBRSwitch({
  bbr,
  onChanged,
  stale,
}: {
  bbr: BBRState
  onChanged: () => void
  stale: boolean
}) {
  const { can } = useAuth()
  const [busy, setBusy] = useState(false)
  const toggle = async (on: boolean) => {
    setBusy(true)
    try {
      await post("/network/shaping/bbr", { on })
      notify.success(on ? "BBR is on" : "Back to the kernel's usual congestion control")
      onChanged()
    } catch (err) {
      notify.error("BBR was not changed", err)
    } finally {
      setBusy(false)
    }
  }
  return (
    <OptionList>
      <OptionRow
        title="Use BBR congestion control"
        hint={
          bbr.available ? (
            <>
              Paces by measured bandwidth instead of waiting for loss. Now{" "}
              <span className="font-mono">{bbr.congestion}</span> over{" "}
              <span className="font-mono">{bbr.defaultQdisc}</span>.
            </>
          ) : (
            <>
              This kernel offers <span className="font-mono">{bbr.algorithms.join(" ")}</span>. Load
              it on the host with <span className="font-mono">modprobe tcp_bbr</span> to turn it on
              here.
            </>
          )
        }
        checked={bbr.active}
        disabled={!can("system.admin") || !bbr.available || busy || stale}
        onCheckedChange={(on) => void toggle(on)}
      />
    </OptionList>
  )
}

const FRAME = "min-w-0 overflow-hidden rounded-xl border bg-card"

function Groups({
  comparison,
  label,
  title,
  detail,
}: {
  comparison: CongestionComparison
  label: string
  title: React.ReactNode
  detail?: string
}) {
  const left = [
    comparison.loopback > 0 && `${comparison.loopback} loopback sockets left out`,
    comparison.truncated && "the host had more sockets than are read",
  ].filter(Boolean)
  return (
    <div className="flex min-w-0 flex-col gap-2" aria-label={label} role="group">
      <p className="flex min-w-0 flex-wrap items-baseline justify-between gap-x-4 text-body font-medium">
        <span>{title}</span>
        {(detail || left.length > 0) && (
          <span className="numeric text-hint font-normal text-muted-foreground">
            {[detail, ...left].filter(Boolean).join(" · ")}
          </span>
        )}
      </p>
      {comparison.error ? (
        <EmptyNote>{comparison.error}</EmptyNote>
      ) : comparison.groups.length === 0 ? (
        <EmptyNote>No established non-loopback TCP socket to compare.</EmptyNote>
      ) : (
        <div className={FRAME}>
          {/* A phone has no room for seven columns, nor for four with the
              other three under the first: there an algorithm is three lines. */}
          <ul className="divide-y divide-hairline px-4 sm:hidden">
            {comparison.groups.map((g) => (
              <li key={g.algorithm} className="flex min-w-0 flex-col gap-0.5 py-3">
                <span className="flex items-baseline justify-between gap-3">
                  <span className="font-mono text-body font-medium">{g.algorithm}</span>
                  <span className="numeric text-hint text-muted-foreground">
                    {plural(g.sockets, "socket")}
                  </span>
                </span>
                <span className="numeric text-hint">
                  round trips {latencyWords("p50", millis(g.medianRttMs))},{" "}
                  {latencyWords("p90", millis(g.p90RttMs))}
                </span>
                <span className="numeric text-hint text-muted-foreground">
                  {shareLabel(g.retransmitShare)} resent
                  {g.medianDeliveryMbit > 0 && ` · ${g.medianDeliveryMbit} Mbit/s`} ·{" "}
                  {bytes(g.bytesSent)} sent
                </span>
              </li>
            ))}
          </ul>
          <Table containerClassName="max-sm:hidden">
            <TableHeader>
              <TableRow>
                <TableHead>Algorithm</TableHead>
                <TableHead className="text-right">Sockets</TableHead>
                <TableHead
                  className="text-right"
                  title={latencyTerm("p50", "round trips came back")}
                >
                  Round trip, {latencyHead("p50").toLowerCase()}
                </TableHead>
                <TableHead
                  className="text-right max-md:hidden"
                  title={latencyTerm("p90", "round trips came back")}
                >
                  {latencyHead("p90")}
                </TableHead>
                <TableHead className="text-right">Resent</TableHead>
                <TableHead className="text-right max-md:hidden">Delivery rate</TableHead>
                <TableHead className="text-right max-md:hidden">Sent</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {comparison.groups.map((g) => (
                <TableRow key={g.algorithm}>
                  <TableCell>
                    <span className="font-mono">{g.algorithm}</span>
                    {/* Between a phone and a laptop there is room for four
                        columns: the other three are said under the algorithm
                        rather than dropped. */}
                    <span className="numeric block text-hint text-muted-foreground md:hidden">
                      {latencyHead("p90").toLowerCase()} {millis(g.p90RttMs)}
                      {g.medianDeliveryMbit > 0 && ` · ${g.medianDeliveryMbit} Mbit/s`} ·{" "}
                      {bytes(g.bytesSent)} sent
                    </span>
                  </TableCell>
                  <TableCell className="numeric text-right">{g.sockets}</TableCell>
                  <TableCell className="numeric text-right">{millis(g.medianRttMs)}</TableCell>
                  <TableCell className="numeric text-right max-md:hidden">
                    {millis(g.p90RttMs)}
                  </TableCell>
                  <TableCell className="numeric text-right">
                    {shareLabel(g.retransmitShare)}
                  </TableCell>
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
        </div>
      )}
    </div>
  )
}

/** The two comparisons before they are read, at about the height they land. */
function GroupsGhost() {
  return (
    <div aria-hidden className="flex flex-col gap-5">
      {[2, 1].map((rows) => (
        <div key={rows} className="flex flex-col gap-2">
          <Skeleton className="my-1 h-3.5 w-48" />
          <div className={FRAME}>
            {Array.from({ length: rows + 1 }, (_, i) => (
              <div key={i} className="flex h-10 items-center gap-6 border-b border-hairline px-4">
                <Skeleton className="h-3 w-20" />
                <Skeleton className="ml-auto h-3 w-40" />
              </div>
            ))}
          </div>
        </div>
      ))}
      <Skeleton className="h-3 w-3/4" />
    </div>
  )
}
