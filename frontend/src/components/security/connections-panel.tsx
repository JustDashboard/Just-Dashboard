"use client"

import { useMemo } from "react"
import { NetworkDevice, Shield } from "@/components/icons"
import { notify } from "@/lib/toast"
import { get, post } from "@/lib/api"
import type { Connections } from "@/lib/types"
import { useViewState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { Metric, MetricStrip } from "@/components/page"
import { Panel, PanelBody, PanelFooter, PanelHeader, PanelToolbar } from "@/components/panel"
import { EmptyState, ErrorState, LoadingPanel } from "@/components/state"
import { IconAction, RowActions } from "@/components/icon-action"
import { Reach } from "@/components/security/reach"
import { Status } from "@/components/status-dot"
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
 * two hundred connections underneath four hundred rows of noise.
 */
export function ConnectionsPanel() {
  const { can } = useAuth()
  const [scope, setScope] = useViewState<"all" | "public">("security.connections.scope", "all")
  const { data, error, loading, refresh } = usePoll<Connections>(
    (signal) => get("/connections", undefined, signal),
    10000,
  )

  const peers = useMemo(
    () => (data?.peers ?? []).filter((p) => scope === "all" || !p.private),
    [data?.peers, scope],
  )
  const fromInternet = (data?.peers ?? []).filter((p) => !p.private).length

  if (loading) return <LoadingPanel />
  if (error) return <ErrorState error={error} />

  const block = async (ip: string) => {
    try {
      await post("/firewall/rules", {
        action: "deny",
        direction: "in",
        from: ip,
        comment: "blocked from connections",
      })
      notify.success(`${ip} blocked`)
      refresh()
    } catch (err) {
      notify.error("Could not add the rule", err)
    }
  }

  return (
    <Panel>
      <PanelHeader
        title="Live connections"
        actions={
          <Status
            verdict={fromInternet > 0 ? "notice" : "ok"}
            label={
              fromInternet > 0 ? `${fromInternet} from the internet` : "none from the internet"
            }
          />
        }
      />
      {/* One strip, not two. The figures and the filter that changes which
          rows they describe belong on the same line: stacked, the filter read
          as a second, unrelated header and the panel grew three horizontal
          rules before its first row of data. */}
      <PanelToolbar className="gap-x-6">
        <MetricStrip>
          <Metric label="remote addresses" value={data?.peers.length ?? 0} />
          <Metric label="sockets" value={data?.total ?? 0} />
          <Metric label="listening" value={data?.listening ?? 0} />
          <Metric label="loopback" value={data?.loopback ?? 0} hint="never left the machine" />
        </MetricStrip>
        <span className="flex-1" />
        <ToggleGroup
          type="single"
          value={scope}
          onValueChange={(next) => next && setScope(next as "all" | "public")}
          variant="outline"
          size="sm"
          className="self-end"
          aria-label="Which peers to show"
        >
          <ToggleGroupItem value="all" className="px-2.5 text-hint">
            Everything
          </ToggleGroupItem>
          <ToggleGroupItem value="public" className="px-2.5 text-hint">
            From the internet {fromInternet}
          </ToggleGroupItem>
        </ToggleGroup>
      </PanelToolbar>
      <PanelBody flush>
        {peers.length === 0 ? (
          <EmptyState
            icon={NetworkDevice}
            title={scope === "public" ? "Nothing connected from the internet" : "No connections"}
            className="border-0"
          />
        ) : (
          <Table containerClassName="max-h-[calc(100svh-26rem)]">
            <TableHeader className={stickyTableHeader}>
              <TableRow>
                <TableHead>Remote address</TableHead>
                <TableHead>Origin</TableHead>
                <TableHead>Sockets</TableHead>
                <TableHead>Reaching</TableHead>
                <TableHead className="w-full">Process</TableHead>
                <TableHead className="w-px" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {peers.map((peer) => (
                <TableRow key={peer.address} className="group">
                  <TableCell className="font-mono">{peer.address}</TableCell>
                  <TableCell>
                    <Reach scope={peer.private ? "private" : "internet"} />
                  </TableCell>
                  <TableCell className="numeric">
                    {peer.established}
                    {peer.count !== peer.established && (
                      <span className="text-muted-foreground"> / {peer.count}</span>
                    )}
                  </TableCell>
                  <TableCell>
                    <span className="font-mono">{peer.ports.slice(0, 4).join(", ")}</span>
                    {peer.service && (
                      <span className="ml-1.5 text-muted-foreground">{peer.service}</span>
                    )}
                  </TableCell>
                  <TableCell className="text-muted-foreground">
                    {peer.processes.join(", ") || "—"}
                  </TableCell>
                  <TableCell>
                    {can("system.admin") && !peer.private && (
                      <RowActions className="justify-end">
                        <IconAction
                          label="Block this address at the firewall"
                          className="text-destructive"
                          onClick={() => block(peer.address)}
                        >
                          <Shield />
                        </IconAction>
                      </RowActions>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </PanelBody>
      <PanelFooter className="text-hint text-muted-foreground">
        Most of a healthy host&rsquo;s connections are private, which is what makes the public ones
        worth looking at.
      </PanelFooter>
    </Panel>
  )
}
