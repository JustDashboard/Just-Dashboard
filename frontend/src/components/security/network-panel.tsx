"use client"

import { useMemo } from "react"
import { get } from "@/lib/api"
import { bytes } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { NetworkInfo } from "@/lib/types"
import { useViewState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { Detail, DetailList, Metric, MetricStrip } from "@/components/page"
import { Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { EmptyNote, ErrorState, LoadingPanel } from "@/components/state"
import { Reach } from "@/components/security/reach"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

/** A device that carries traffic of its own, rather than one Docker made. */
const VIRTUAL = ["virtual", "bridge"]

/**
 * The shape of the machine's network, which is what "exposed" means.
 *
 * An address on tailscale0 and the same address on eth0 are completely
 * different security propositions, and until the interfaces are on screen the
 * operator has to take the dashboard's word for which one they have. The
 * routing table answers the other half: which interface carries the default
 * route is what "the internet reaches this box here" means.
 */
export function NetworkPanel() {
  const [scope, setScope] = useViewState<"real" | "all">("security.network.devices", "real")
  const { data, error, loading } = usePoll<NetworkInfo>(
    (signal) => get("/network", undefined, signal),
    60000,
  )

  const interfaces = useMemo(() => {
    const all = data?.interfaces ?? []
    // The header promised the real devices read first and nothing sorted them,
    // so a host running Docker opened on four veths. Public first, then the
    // rest of the real devices, then everything Docker made.
    const rank = (kind: string, isPublic: boolean) =>
      isPublic ? 0 : VIRTUAL.includes(kind) ? 2 : 1
    const sorted = [...all].sort(
      (a, b) => rank(a.kind, a.public) - rank(b.kind, b.public) || a.name.localeCompare(b.name),
    )
    return scope === "all" ? sorted : sorted.filter((i) => !VIRTUAL.includes(i.kind))
  }, [data?.interfaces, scope])

  if (loading) return <LoadingPanel />
  if (error) return <ErrorState error={error} />
  if (!data) return null

  const virtual = data.interfaces.filter((i) => VIRTUAL.includes(i.kind)).length
  const exposed = data.interfaces.filter((i) => i.public && i.up)
  const defaultRoute = data.routes.find((r) => r.destination === "default")

  return (
    <div className="flex min-w-0 flex-col gap-4">
      <Panel>
        <PanelHeader
          title="Interfaces"
          actions={
            <Status
              verdict={exposed.length > 0 ? "warning" : "ok"}
              label={
                exposed.length > 0 ? `${exposed.length} on a public address` : "no public address"
              }
            />
          }
        />
        <PanelToolbar className="gap-x-6">
          <MetricStrip>
            <Metric
              label="devices"
              value={data.interfaces.length}
              hint={`${virtual} made by Docker`}
            />
            <Metric label="up" value={data.interfaces.filter((i) => i.up).length} />
            <Metric
              label="default route"
              value={defaultRoute?.interface ?? "—"}
              hint="where the internet reaches this host"
            />
          </MetricStrip>
          <span className="flex-1" />
          <ToggleGroup
            type="single"
            value={scope}
            onValueChange={(next) => next && setScope(next as "real" | "all")}
            variant="outline"
            size="sm"
            className="self-end"
            aria-label="Which devices to show"
          >
            <ToggleGroupItem value="real" className="px-2.5 text-hint">
              Real devices
            </ToggleGroupItem>
            <ToggleGroupItem value="all" className="px-2.5 text-hint">
              Everything {data.interfaces.length}
            </ToggleGroupItem>
          </ToggleGroup>
        </PanelToolbar>
        <PanelBody flush>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Device</TableHead>
                <TableHead>Reach</TableHead>
                <TableHead>MTU</TableHead>
                <TableHead>In / out</TableHead>
                <TableHead className="w-full">Addresses</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {interfaces.map((ifc) => (
                <TableRow key={ifc.name} className={cn(!ifc.up && "opacity-60")}>
                  <TableCell>
                    <div className="flex items-center gap-2">
                      <span className="font-mono text-xs font-medium">{ifc.name}</span>
                      <Tag>{ifc.kind}</Tag>
                      {!ifc.up && <Status state="stopped" label="down" />}
                    </div>
                  </TableCell>
                  <TableCell>
                    <Reach scope={ifc.public ? "internet" : ifc.loopback ? "local" : "private"} />
                  </TableCell>
                  <TableCell className="numeric text-muted-foreground">{ifc.mtu}</TableCell>
                  <TableCell className="numeric whitespace-nowrap text-muted-foreground">
                    {bytes(ifc.bytesRecv)} / {bytes(ifc.bytesSent)}
                  </TableCell>
                  <TableCell className="font-mono text-hint">
                    {ifc.addresses.join("  ") || "—"}
                  </TableCell>
                </TableRow>
              ))}
              {interfaces.length === 0 && (
                <TableRow>
                  <TableCell colSpan={5} className="p-0">
                    <EmptyNote>No devices match.</EmptyNote>
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </PanelBody>
      </Panel>

      <div className="grid items-start gap-4 lg:grid-cols-[2fr_1fr] [&>*]:min-w-0">
        <Panel>
          <PanelHeader title="Routes" />
          <PanelBody flush>
            <Table containerClassName="max-h-[22rem]">
              <TableHeader>
                <TableRow>
                  <TableHead>Destination</TableHead>
                  <TableHead>Via</TableHead>
                  <TableHead>Device</TableHead>
                  <TableHead>Metric</TableHead>
                  <TableHead className="w-full" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {data.routes.map((route, i) => (
                  <TableRow
                    key={`${route.family}-${route.destination}-${i}`}
                    className={cn(route.destination === "default" && "bg-row-hover/40")}
                  >
                    <TableCell className="font-mono">
                      {route.destination}
                      {route.destination === "default" && (
                        <Tag className="ml-2">{route.family}</Tag>
                      )}
                    </TableCell>
                    <TableCell className="font-mono text-hint text-muted-foreground">
                      {route.gateway || "on-link"}
                    </TableCell>
                    <TableCell className="font-mono text-hint">{route.interface || "—"}</TableCell>
                    <TableCell className="numeric text-hint text-muted-foreground">
                      {route.metric || "—"}
                    </TableCell>
                    <TableCell />
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </PanelBody>
        </Panel>

        <Panel>
          <PanelHeader title="Resolvers" />
          <PanelBody className="space-y-3">
            <DetailList>
              {data.resolvers.map((server, i) => (
                <Detail key={server} label={i === 0 ? "First" : `Fallback ${i}`}>
                  <span className="font-mono">{server}</span>
                </Detail>
              ))}
              {data.search.length > 0 && (
                <Detail label="Search">
                  <span className="font-mono break-all">{data.search.join(" ")}</span>
                </Detail>
              )}
            </DetailList>
            {data.resolvers.length === 0 && <EmptyNote>None configured.</EmptyNote>}
            {data.resolvers.some((s) => s.startsWith("127.0.0.53")) && (
              <p className="border-t border-hairline pt-3 text-hint leading-relaxed text-muted-foreground">
                127.0.0.53 is systemd-resolved answering locally; the real upstream servers are the
                ones it was given.
              </p>
            )}
          </PanelBody>
        </Panel>
      </div>
    </div>
  )
}
