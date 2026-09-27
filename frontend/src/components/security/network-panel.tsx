"use client"

import { useMemo } from "react"
import { get } from "@/lib/api"
import { bytes } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { NetworkInfo } from "@/lib/types"
import { useViewState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { Detail, DetailList, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { EmptyNote, ErrorState, LoadingPanel } from "@/components/state"
import { Reach } from "@/components/security/reach"
import { InterfaceMark, interfaceProduct } from "@/components/security/marks"
import { Meter } from "@/components/meter"
import { ProductGlyph } from "@/components/product-logo"
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
 *
 * Every device is drawn as what made it — Tailscale's tunnel and Docker's
 * bridges as their marks, a physical port as a glyph for its kind — so a list
 * of nine devices is scanned for the two that are not the container network
 * before a name is read. The devices, routes and resolvers are readings, so
 * they stay tables.
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

  const exposed = data?.interfaces.filter((i) => i.public && i.up) ?? []
  const header = <PageContext eyebrow="Security" title="Network" />

  if (loading && !data) {
    return (
      <>
        {header}
        <LoadingPanel />
      </>
    )
  }
  if (error && !data) {
    return (
      <>
        {header}
        <ErrorState error={error} />
      </>
    )
  }
  if (!data) return header

  const virtual = data.interfaces.filter((i) => VIRTUAL.includes(i.kind)).length
  const defaultRoute = data.routes.find((r) => r.destination === "default")

  return (
    <>
      {header}

      <StatGrid columns={4}>
        <StatTile
          label="Devices"
          value={data.interfaces.length}
          hint={
            virtual > 0 ? `${virtual} virtual or bridge devices` : "no virtual or bridge devices"
          }
        />
        <StatTile
          label="Up"
          value={data.interfaces.filter((i) => i.up).length}
          hint={`${data.interfaces.filter((i) => !i.up).length} down`}
        />
        <StatTile
          label="Default route"
          value={defaultRoute?.interface ?? "—"}
          hint={
            defaultRoute?.gateway
              ? `via ${defaultRoute.gateway}`
              : "where the internet reaches this host"
          }
        />
        <StatTile
          label="Public addresses"
          value={exposed.length}
          tone={exposed.length > 0 ? "warning" : "default"}
          hint={
            exposed.length > 0
              ? exposed.map((i) => i.name).join(", ")
              : "nothing faces the internet directly"
          }
        />
      </StatGrid>

      <Panel>
        <PanelHeader
          title="Interfaces"
          actions={
            <span className="text-hint text-muted-foreground">
              {interfaces.length} shown · refreshes every minute
            </span>
          }
        />
        <PanelToolbar>
          <ToggleGroup
            type="single"
            value={scope}
            onValueChange={(next) => next && setScope(next as "real" | "all")}
            variant="outline"
            size="sm"
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
          {interfaces.length === 0 ? (
            <EmptyNote>No devices match.</EmptyNote>
          ) : (
            <div className="min-w-0 group-data-[plain]/panel:-mx-4">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Device</TableHead>
                    <TableHead className="w-full">Addresses</TableHead>
                    <TableHead>Link</TableHead>
                    <TableHead>Transferred</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {interfaces.map((ifc) => (
                    <TableRow key={ifc.name} className={cn(!ifc.up && "opacity-60")}>
                      <TableCell className="py-4">
                        <div className="flex items-center gap-3">
                          <InterfaceMark device={ifc} />
                          <div className="space-y-1">
                            <span className="block font-mono text-body font-medium">
                              {ifc.name}
                            </span>
                            <Tag>{ifc.kind}</Tag>
                          </div>
                        </div>
                      </TableCell>
                      <TableCell className="min-w-44 whitespace-normal">
                        <div className="space-y-1.5">
                          {ifc.addresses.length ? (
                            ifc.addresses.map((address) => (
                              <span key={address} className="block font-mono text-body break-all">
                                {address}
                              </span>
                            ))
                          ) : (
                            <span className="text-muted-foreground">No address</span>
                          )}
                          <Reach
                            scope={ifc.public ? "internet" : ifc.loopback ? "local" : "private"}
                          />
                        </div>
                      </TableCell>
                      <TableCell>
                        <div className="space-y-1.5">
                          <Status
                            state={ifc.up ? "active" : "stopped"}
                            label={ifc.up ? "Up" : "Down"}
                          />
                          <span className="numeric block text-hint text-muted-foreground">
                            MTU {ifc.mtu}
                          </span>
                        </div>
                      </TableCell>
                      <TableCell>
                        <div className="w-32 space-y-2">
                          <div className="flex justify-between gap-3 text-hint">
                            <span className="text-muted-foreground">Received</span>
                            <span className="numeric">{bytes(ifc.bytesRecv)}</span>
                          </div>
                          <Meter
                            value={
                              ifc.bytesRecv + ifc.bytesSent
                                ? (ifc.bytesRecv / (ifc.bytesRecv + ifc.bytesSent)) * 100
                                : 0
                            }
                            size="thin"
                            label={`${bytes(ifc.bytesRecv)} received, ${bytes(ifc.bytesSent)} sent`}
                          />
                          <div className="flex justify-between gap-3 text-hint">
                            <span className="text-muted-foreground">Sent</span>
                            <span className="numeric">{bytes(ifc.bytesSent)}</span>
                          </div>
                        </div>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
        </PanelBody>
      </Panel>

      {/* Routes own a bounded table; resolver facts stay on the page beside it. */}
      <div className="grid items-start gap-x-8 gap-y-6 lg:grid-cols-[2fr_1fr] [&>*]:min-w-0">
        <Panel>
          <PanelHeader title="Routes" />
          <PanelBody flush>
            <div className="min-w-0 group-data-[plain]/panel:-mx-4">
              <Table containerClassName="max-h-[22rem]">
                <TableHeader>
                  <TableRow>
                    <TableHead>Destination</TableHead>
                    <TableHead>Gateway</TableHead>
                    <TableHead>Device</TableHead>
                    <TableHead className="hidden sm:table-cell">Metric</TableHead>
                    <TableHead className="w-full" />
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {data.routes.map((route, i) => (
                    <TableRow
                      key={`${route.family}-${route.destination}-${i}`}
                      className={cn(route.destination === "default" && "bg-row-hover/40")}
                    >
                      <TableCell className="py-4 font-mono">
                        <span className="block font-medium">{route.destination}</span>
                        <Tag>{route.family}</Tag>
                      </TableCell>
                      <TableCell className="font-mono text-hint text-muted-foreground">
                        {route.gateway || "on-link"}
                      </TableCell>
                      <TableCell className="font-mono text-hint">
                        <span className="inline-flex items-center gap-1.5">
                          {route.interface && interfaceProduct(route.interface) && (
                            <ProductGlyph id={interfaceProduct(route.interface)!} />
                          )}
                          {route.interface || "—"}
                        </span>
                      </TableCell>
                      <TableCell className="numeric hidden text-hint text-muted-foreground sm:table-cell">
                        {route.metric ?? "—"}
                      </TableCell>
                      <TableCell />
                    </TableRow>
                  ))}
                  {data.routes.length === 0 && (
                    <TableRow>
                      <TableCell colSpan={5} className="p-0">
                        <EmptyNote>No routes could be read.</EmptyNote>
                      </TableCell>
                    </TableRow>
                  )}
                </TableBody>
              </Table>
            </div>
          </PanelBody>
        </Panel>

        <Panel plain>
          <PanelHeader
            title="DNS resolvers"
            actions={
              <span className="numeric text-hint text-muted-foreground">
                {data.resolvers.length}
              </span>
            }
          />
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
    </>
  )
}
