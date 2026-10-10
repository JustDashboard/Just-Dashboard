"use client"

import Link from "next/link"
import { useState } from "react"
import { Route as RouteGlyph } from "@/components/icons"
import { get } from "@/lib/api"
import { duration } from "@/lib/format"
import type { BGPPolicy, BGPRoutesView, BGPView } from "@/lib/types"
import { EmptyNote, EmptyState } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Field } from "@/components/form"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
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

/**
 * BGP and OSPF through FRR, where it runs: each address family's router id
 * and AS, every neighbour with its state, uptime, prefixes and the filters
 * FRR names for it, the OSPF adjacencies, and a route browser over a
 * bounded table or one prefix. Read only — FRR's own configuration is a file
 * the operator edits, and the dashboard does not pretend to be a second
 * editor for it, nor a failover controller.
 *
 * Without FRR the block says what BGP needs here, which is a provider that
 * peers with customers (Vultr, Hetzner's dedicated servers, Equinix Metal,
 * most colocation): installing FRR on a VPS whose provider offers no session
 * gets a daemon with nobody to talk to.
 */
export function BGPBlock({ bgp }: { bgp: BGPView | undefined }) {
  if (!bgp) return null
  if (!bgp.installed) {
    return (
      <EmptyState
        icon={RouteGlyph}
        title="FRR is not installed"
        description={
          <>
            BGP needs a provider that offers a session to its customers — most colocation, Vultr,
            Equinix Metal, Hetzner&rsquo;s dedicated servers. With FRR installed its neighbours,
            their state and their prefixes appear here.
          </>
        }
        action={
          <Button asChild size="sm" variant="outline">
            <Link href="/packages?q=frr">Find FRR in Packages</Link>
          </Button>
        }
      />
    )
  }
  const peers = bgp.families.flatMap((f) =>
    f.peers.map((p) => ({ ...p, family: f.name, localAs: f.localAs, routerId: f.routerId })),
  )
  return (
    <div className="space-y-6">
      {bgp.error && <p className="text-body text-destructive">{bgp.error}</p>}
      {!bgp.error && peers.length === 0 && (
        <p className="text-body text-muted-foreground">
          FRR is {bgp.running ? "running" : "installed but not running"} with no BGP neighbours
          configured.
        </p>
      )}
      {peers.length > 0 && (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Neighbour</TableHead>
              <TableHead>AS</TableHead>
              <TableHead>State</TableHead>
              <TableHead className="text-right">Up for</TableHead>
              <TableHead className="text-right">Prefixes in</TableHead>
              <TableHead className="text-right">Prefixes out</TableHead>
              <TableHead>Filters</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {peers.map((p) => (
              <TableRow key={`${p.family}:${p.address}`}>
                <TableCell>
                  <span className="block font-mono text-xs">{p.address}</span>
                  <span className="text-hint text-muted-foreground">
                    {p.description ?? p.hostname ?? p.family}
                  </span>
                </TableCell>
                <TableCell className="numeric font-mono text-xs">
                  {p.remoteAs} <span className="text-muted-foreground">← {p.localAs}</span>
                </TableCell>
                <TableCell>
                  <Status
                    tone={
                      p.state === "Established"
                        ? "running"
                        : p.state === "Idle"
                          ? "danger"
                          : "warning"
                    }
                    label={p.state}
                  />
                </TableCell>
                <TableCell className="numeric text-right text-xs">
                  {p.uptimeSeconds ? duration(p.uptimeSeconds) : "—"}
                </TableCell>
                <TableCell className="numeric text-right font-mono text-xs">
                  {p.prefixesReceived}
                </TableCell>
                <TableCell className="numeric text-right font-mono text-xs">
                  {p.prefixesSent}
                </TableCell>
                <TableCell className="text-xs">
                  <PolicyNames policy={p.policy} />
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      {(bgp.ospf?.length ?? 0) > 0 && (
        <section aria-label="OSPF neighbours" className="space-y-2">
          <p className="text-body font-medium">OSPF neighbours</p>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Router ID</TableHead>
                <TableHead>Version</TableHead>
                <TableHead>State</TableHead>
                <TableHead>Interface</TableHead>
                <TableHead className="text-right">Up for</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {bgp.ospf!.map((n) => (
                <TableRow key={`${n.version}:${n.routerId}:${n.interface}`}>
                  <TableCell>
                    <span className="block font-mono text-xs">{n.routerId}</span>
                    {n.address && (
                      <span className="font-mono text-hint text-muted-foreground">{n.address}</span>
                    )}
                  </TableCell>
                  <TableCell className="text-xs">OSPFv{n.version}</TableCell>
                  <TableCell>
                    <Status
                      tone={n.state === "Full" ? "running" : "warning"}
                      label={n.role ? `${n.state} · ${n.role}` : n.state}
                    />
                  </TableCell>
                  <TableCell className="font-mono text-xs">{n.interface ?? "—"}</TableCell>
                  <TableCell className="numeric text-right text-xs">
                    {n.uptimeSeconds ? duration(n.uptimeSeconds) : "—"}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </section>
      )}
      {peers.length > 0 && <RouteBrowser families={bgp.families.map((f) => f.name)} />}
      {bgp.readOnly && <p className="text-hint text-muted-foreground">{bgp.readOnly}</p>}
    </div>
  )
}

function PolicyNames({ policy }: { policy?: BGPPolicy }) {
  if (!policy) return <span className="text-muted-foreground">none named</span>
  const parts = [
    policy.routeMapIn && `route-map in ${policy.routeMapIn}`,
    policy.routeMapOut && `route-map out ${policy.routeMapOut}`,
    policy.prefixListIn && `prefix-list in ${policy.prefixListIn}`,
    policy.prefixListOut && `prefix-list out ${policy.prefixListOut}`,
    policy.filterListIn && `filter-list in ${policy.filterListIn}`,
    policy.filterListOut && `filter-list out ${policy.filterListOut}`,
  ].filter(Boolean) as string[]
  return (
    <span className="flex flex-wrap gap-1">
      {parts.map((part) => (
        <Tag key={part} mono>
          {part}
        </Tag>
      ))}
    </span>
  )
}

/**
 * The BGP table, read on request: a family whole when it is small, or one
 * prefix's paths. A full internet table is never pulled into the page.
 */
function RouteBrowser({ families }: { families: string[] }) {
  const supported = families.filter((f) => f === "ipv4Unicast" || f === "ipv6Unicast")
  const [family, setFamily] = useState<string>(supported[0] ?? "ipv4Unicast")
  const [prefix, setPrefix] = useState("")
  const [busy, setBusy] = useState(false)
  const [view, setView] = useState<BGPRoutesView>()
  const [error, setError] = useState<string>()
  if (supported.length === 0) return null
  const read = async () => {
    setBusy(true)
    setError(undefined)
    try {
      setView(
        await get<BGPRoutesView>("/network/bgp/routes", {
          family,
          prefix: prefix.trim() || undefined,
        }),
      )
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <section aria-label="BGP routes" className="space-y-3">
      <p className="text-body font-medium">Routes</p>
      <div className="flex flex-wrap items-end gap-3">
        <Field label="Family" htmlFor="bgp-family">
          <Select value={family} onValueChange={setFamily}>
            <SelectTrigger id="bgp-family" className="w-40">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {supported.map((f) => (
                <SelectItem key={f} value={f}>
                  {f === "ipv4Unicast" ? "IPv4 unicast" : "IPv6 unicast"}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        <Field label="Prefix" htmlFor="bgp-prefix" hint="Optional: one prefix's paths">
          <Input
            id="bgp-prefix"
            value={prefix}
            onChange={(event) => setPrefix(event.target.value)}
            placeholder="10.10.0.0/24"
            className="w-48 font-mono"
          />
        </Field>
        <Button size="sm" variant="outline" onClick={read} pending={busy} disabled={busy}>
          Read routes
        </Button>
      </div>
      {error && <p className="text-body text-destructive">{error}</p>}
      {view?.error && <p className="text-hint text-warning">{view.error}</p>}
      {view && !view.error && view.routes.length === 0 && (
        <EmptyNote>No path {view.prefix ? `to ${view.prefix}` : "in this table"}.</EmptyNote>
      )}
      {view && view.routes.length > 0 && (
        <Table containerClassName="max-h-[24rem]">
          <TableHeader>
            <TableRow>
              <TableHead>Prefix</TableHead>
              <TableHead>Next hop</TableHead>
              <TableHead>AS path</TableHead>
              <TableHead className="text-right">Local pref</TableHead>
              <TableHead className="text-right">MED</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {view.routes.map((r, index) => (
              <TableRow key={`${r.prefix}:${r.peer}:${index}`}>
                <TableCell>
                  <span className="inline-flex flex-wrap items-center gap-2">
                    <span className="font-mono text-xs">{r.prefix}</span>
                    {r.best && <Tag tone="success">best</Tag>}
                    {r.multipath && <Tag>multipath</Tag>}
                    {!r.valid && <Tag tone="warning">invalid</Tag>}
                  </span>
                </TableCell>
                <TableCell className="font-mono text-xs">{r.nexthops.join(", ") || "—"}</TableCell>
                <TableCell className="font-mono text-xs">{r.path || "local"}</TableCell>
                <TableCell className="numeric text-right text-xs">{r.localPref ?? "—"}</TableCell>
                <TableCell className="numeric text-right text-xs">{r.med ?? "—"}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </section>
  )
}
