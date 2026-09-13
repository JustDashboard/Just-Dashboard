"use client"

import { Router } from "@/components/icons"
import { get } from "@/lib/api"
import type { Listener } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ErrorState, LoadingPanel } from "@/components/state"
import { Status } from "@/components/status-dot"
import {
  stickyTableHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

/** Every listening socket on the host, and whether it faces off the machine. */
export function PortsPanel() {
  const { data, error, loading } = usePoll(
    (signal) => get<Listener[]>("/ports", undefined, signal),
    15000,
  )
  if (loading) return <LoadingPanel />
  if (error) return <ErrorState error={error} />

  return (
    <Panel>
      <PanelHeader title="Listening ports" />
      <PanelBody flush>
        <Table containerClassName="max-h-[calc(100svh-20rem)]">
          <TableHeader className={stickyTableHeader}>
            <TableRow>
              <TableHead className="w-20">Port</TableHead>
              <TableHead className="hidden w-20 sm:table-cell">Proto</TableHead>
              <TableHead className="hidden md:table-cell">Bound to</TableHead>
              <TableHead className="w-full">Process</TableHead>
              <TableHead className="hidden lg:table-cell">User</TableHead>
              <TableHead>Reach</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {data?.map((listener, i) => (
              <TableRow key={`${listener.protocol}-${listener.address}-${listener.port}-${i}`}>
                <TableCell className="numeric font-mono text-body">{listener.port}</TableCell>
                <TableCell className="hidden text-muted-foreground uppercase sm:table-cell">
                  {listener.protocol}
                </TableCell>
                <TableCell className="hidden font-mono md:table-cell">
                  {listener.address || "*"}
                </TableCell>
                <TableCell>
                  <div className="max-w-[22rem] min-w-0">
                    <div className="truncate text-body">{listener.process || "unknown"}</div>
                    <p className="truncate font-mono text-hint text-muted-foreground">
                      {listener.cmdline}
                    </p>
                    {/* Which address a port is bound to is the whole reason
                        this page exists — it must not be the column that gets
                        dropped on a narrow screen. */}
                    <p className="truncate font-mono text-hint text-muted-foreground md:hidden">
                      {listener.address || "*"}
                      <span className="uppercase sm:hidden"> · {listener.protocol}</span>
                    </p>
                  </div>
                </TableCell>
                <TableCell className="hidden lg:table-cell">{listener.user ?? "—"}</TableCell>
                <TableCell>
                  {listener.exposed ? (
                    <Status verdict="warning" label="exposed" icon={Router} />
                  ) : (
                    <span className="text-xs text-muted-foreground">loopback</span>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </PanelBody>
    </Panel>
  )
}
