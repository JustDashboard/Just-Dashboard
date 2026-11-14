"use client"

import Link from "next/link"
import { useState } from "react"
import { Route as RouteGlyph } from "@/components/icons"
import { get } from "@/lib/api"
import { duration } from "@/lib/format"
import type { BGPPolicy, BGPRoutesView, BGPView } from "@/lib/types"
import type { PollState } from "@/hooks/use-poll"
import { useMediaQuery } from "@/hooks/use-mobile"
import { InfoTip } from "@/components/form"
import { Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { WireHost, WirePlaceholder } from "@/components/deploy/wire"
import { EmptyNote, EmptyState, ErrorState, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

type Peer = BGPView["families"][number]["peers"][number] & { family: string; localAs: number }

/**
 * BGP and OSPF through FRR, where it runs: every neighbour with its state,
 * uptime, prefixes and the filters FRR names for it, the OSPF adjacencies,
 * and a route browser over a bounded table or one prefix, each a framed
 * table of one-line rows. Read only — FRR's own configuration is a file the
 * operator edits, and the dashboard does not pretend to be a second editor
 * for it, nor a failover controller; FRR's sentence saying so is behind the
 * ⓘ beside its state.
 *
 * Without FRR the view says what BGP needs here, which is a provider that
 * peers with customers (Vultr, Hetzner's dedicated servers, Equinix Metal,
 * most colocation): installing FRR on a VPS whose provider offers no session
 * gets a daemon with nobody to talk to.
 */
export function BGPPanel({ read }: { read: PollState<BGPView> }) {
  const bgp = read.data
  if (!bgp)
    return (
      <Panel plain aria-label="BGP" aria-busy={!read.error}>
        <PanelHeader title="BGP" />
        <PanelBody>
          {read.error ? (
            <ErrorState error={read.error} onRetry={read.refresh} retryable />
          ) : (
            <Skeleton className="h-56 w-full rounded-xl" />
          )}
        </PanelBody>
      </Panel>
    )
  if (!bgp.installed)
    return (
      <Panel plain aria-label="BGP">
        <PanelHeader title="BGP" actions={<Status tone="unknown" label="FRR not installed" />} />
        <PanelBody>
          <EmptyState
            className="h-56 py-0"
            mark={
              <span aria-hidden className="flex items-center">
                <WireHost size="sm" />
                <span className="w-14 border-t border-dashed border-border-strong" />
                <WirePlaceholder size="sm" fallback={RouteGlyph} />
              </span>
            }
            title="FRR is not installed"
            description={
              <>
                BGP needs a provider that offers a session to its customers: most colocation, Vultr,
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
        </PanelBody>
      </Panel>
    )
  const peers: Peer[] = bgp.families.flatMap((f) =>
    f.peers.map((p) => ({ ...p, family: f.name, localAs: f.localAs })),
  )
  const up = peers.filter((p) => p.state === "Established").length
  return (
    <div className="min-w-0 space-y-8">
      <Panel plain aria-label="BGP">
        <PanelHeader
          title="BGP"
          actions={
            <>
              <Status
                tone={bgp.running ? "running" : "stopped"}
                label={
                  !bgp.running
                    ? "FRR installed, not running"
                    : peers.length > 0
                      ? `FRR running · ${up} of ${peers.length} established`
                      : "FRR running"
                }
              />
              {bgp.readOnly && <InfoTip label="Where BGP is configured">{bgp.readOnly}</InfoTip>}
            </>
          }
        />
        {(bgp.error || peers.length === 0) && (
          <PanelBody>
            {bgp.error ? (
              <Notice tone="danger" title="FRR could not be read">
                {bgp.error}
              </Notice>
            ) : (
              <EmptyState
                icon={RouteGlyph}
                title="No BGP neighbours"
                description={`FRR is ${bgp.running ? "running" : "installed but not running"} with no BGP neighbours configured.`}
              />
            )}
          </PanelBody>
        )}
      </Panel>
      {peers.length > 0 && <Neighbours peers={peers} />}
      {(bgp.ospf?.length ?? 0) > 0 && <Adjacencies neighbours={bgp.ospf!} />}
      {peers.length > 0 && <RouteBrowser families={bgp.families.map((f) => f.name)} />}
    </div>
  )
}

function peerTone(state: string) {
  return state === "Established" ? "running" : state === "Idle" ? "danger" : "warning"
}

function Neighbours({ peers }: { peers: Peer[] }) {
  const wide = useMediaQuery("(min-width: 1100px)")
  return (
    <Panel aria-label="BGP neighbours">
      <PanelHeader title={<Counted title="Neighbours" count={peers.length} />} />
      <PanelBody flush>
        {wide ? (
          <Table className="table-fixed">
            <TableHeader>
              <TableRow>
                <TableHead className="w-[26%]">Neighbour</TableHead>
                <TableHead className="w-[14%]">AS</TableHead>
                <TableHead className="w-[13%]">State</TableHead>
                <TableHead className="w-[9%] text-right">Up for</TableHead>
                <TableHead className="w-[7%] text-right">In</TableHead>
                <TableHead className="w-[7%] text-right">Out</TableHead>
                <TableHead>Filters</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {peers.map((p) => (
                <TableRow key={`${p.family}:${p.address}`} className="h-11">
                  <TableCell className="py-0">
                    <Named name={p.address} detail={p.description ?? p.hostname ?? p.family} />
                  </TableCell>
                  <TableCell className="numeric py-0 font-mono whitespace-nowrap">
                    {p.remoteAs} <span className="text-muted-foreground">← {p.localAs}</span>
                  </TableCell>
                  <TableCell className="py-0">
                    <Status tone={peerTone(p.state)} label={p.state} />
                  </TableCell>
                  <TableCell className="numeric py-0 text-right whitespace-nowrap">
                    {p.uptimeSeconds ? duration(p.uptimeSeconds) : "—"}
                  </TableCell>
                  <TableCell className="numeric py-0 text-right font-mono">
                    {p.prefixesReceived}
                  </TableCell>
                  <TableCell className="numeric py-0 text-right font-mono">
                    {p.prefixesSent}
                  </TableCell>
                  <TableCell className="py-0">
                    <PolicyNames policy={p.policy} />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        ) : (
          <ul aria-label="BGP neighbours" className="divide-y divide-hairline">
            {peers.map((p) => (
              <li
                key={`${p.family}:${p.address}`}

                className="grid h-[4.5rem] min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 px-4 text-xs"
              >
                <div className="min-w-0 space-y-1">
                  <Named name={p.address} detail={p.description ?? p.hostname ?? p.family} />
                  <p className="numeric truncate font-mono text-muted-foreground">
                    AS {p.remoteAs} ← {p.localAs} · {p.prefixesReceived} in · {p.prefixesSent} out
                  </p>
                  <PolicyNames policy={p.policy} />
                </div>
                <div className="flex flex-col items-end gap-1">
                  <Status tone={peerTone(p.state)} label={p.state} />
                  <span className="numeric text-muted-foreground">
                    {p.uptimeSeconds ? duration(p.uptimeSeconds) : "—"}
                  </span>
                </div>
              </li>
            ))}
          </ul>
        )}
      </PanelBody>
    </Panel>
  )
}

function Adjacencies({ neighbours }: { neighbours: NonNullable<BGPView["ospf"]> }) {
  return (
    <Panel aria-label="OSPF neighbours">
      <PanelHeader title={<Counted title="OSPF neighbours" count={neighbours.length} />} />
      <PanelBody flush>
        <ul aria-label="OSPF neighbours" className="divide-y divide-hairline">
          {neighbours.map((n) => (
            <li
              key={`${n.version}:${n.routerId}:${n.interface}`}
              className="grid h-11 min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-x-4 px-4 text-xs sm:grid-cols-[minmax(0,1.4fr)_5rem_minmax(0,1fr)_minmax(0,1fr)_5rem] sm:px-5"
            >
              <Named name={n.routerId} detail={n.address} mono />
              <span className="text-muted-foreground max-sm:hidden">OSPFv{n.version}</span>
              <Status
                tone={n.state === "Full" ? "running" : "warning"}
                label={n.role ? `${n.state} · ${n.role}` : n.state}
              />
              <span className="truncate font-mono max-sm:hidden">{n.interface ?? "—"}</span>
              <span className="numeric text-right whitespace-nowrap max-sm:hidden">
                {n.uptimeSeconds ? duration(n.uptimeSeconds) : "—"}
              </span>
            </li>
          ))}
        </ul>
      </PanelBody>
    </Panel>
  )
}

function Counted({ title, count }: { title: string; count: number }) {
  return (
    <>
      {title}
      <span className="numeric ml-2 text-body font-normal text-muted-foreground">{count}</span>
    </>
  )
}

/** A name in mono with what it is called after it, on one line. */
function Named({ name, detail, mono }: { name: string; detail?: string; mono?: boolean }) {
  return (
    <span className="flex min-w-0 items-baseline gap-2 whitespace-nowrap">
      <span className="font-mono">{name}</span>
      {detail && (
        <span
          className={
            mono
              ? "truncate font-mono text-muted-foreground"
              : "truncate text-hint text-muted-foreground"
          }
        >
          {detail}
        </span>
      )}
    </span>
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
    <span className="flex min-w-0 gap-1 overflow-hidden" title={parts.join(", ")}>
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
  const wide = useMediaQuery("(min-width: 768px)")
  if (supported.length === 0) return null
  const read = async () => {
    if (busy) return
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
    <Panel aria-label="BGP routes">
      <PanelHeader
        title={
          view && !view.error ? <Counted title="Routes" count={view.routes.length} /> : "Routes"
        }
      />
      <PanelToolbar>
        <form
          className="flex min-w-0 flex-1 flex-wrap items-center gap-2"
          onSubmit={(event) => {
            event.preventDefault()
            void read()
          }}
        >
          <Select value={family} onValueChange={setFamily}>
            <SelectTrigger size="sm" aria-label="Family" className="w-36 max-sm:flex-1">
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
          <Input
            aria-label="Prefix"
            value={prefix}
            onChange={(event) => setPrefix(event.target.value)}
            placeholder="One prefix, or the whole table"
            className="h-10 min-w-0 font-mono placeholder:font-sans max-sm:order-first max-sm:basis-full sm:h-8 sm:max-w-64 sm:flex-1"
          />
          <Button type="submit" size="sm" variant="outline" pending={busy} disabled={busy}>
            Read routes
          </Button>
        </form>
      </PanelToolbar>
      <PanelBody flush>
        {error ? (
          <p role="alert" className="px-5 py-4 text-body text-destructive">
            {error}
          </p>
        ) : view?.error ? (
          <p className="px-5 py-4 text-body text-warning">{view.error}</p>
        ) : !view ? (
          <EmptyNote>Not read yet.</EmptyNote>
        ) : view.routes.length === 0 ? (
          <EmptyNote>No path {view.prefix ? `to ${view.prefix}` : "in this table"}.</EmptyNote>
        ) : !wide ? (
          <ul
            aria-label="BGP routes"
            className="max-h-[24rem] divide-y divide-hairline overflow-y-auto"
          >
            {view.routes.map((r, index) => (
              <li
                key={`${r.prefix}:${r.peer}:${index}`}
                className="flex h-14 min-w-0 flex-col justify-center gap-1 px-4 text-xs"
              >
                <span className="flex items-center gap-2 whitespace-nowrap">
                  <span className="font-mono">{r.prefix}</span>
                  {r.best && <Tag tone="success">best</Tag>}
                  {r.multipath && <Tag>multipath</Tag>}
                  {!r.valid && <Tag tone="warning">invalid</Tag>}
                </span>
                <span className="truncate font-mono text-muted-foreground">
                  via {r.nexthops.join(", ") || "—"} · path {r.path || "local"}
                  {r.localPref !== undefined && ` · pref ${r.localPref}`}
                  {r.med !== undefined && ` · MED ${r.med}`}
                </span>
              </li>
            ))}
          </ul>
        ) : (
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
                <TableRow key={`${r.prefix}:${r.peer}:${index}`} className="h-11">
                  <TableCell className="py-0">
                    <span className="flex items-center gap-2 whitespace-nowrap">
                      <span className="font-mono">{r.prefix}</span>
                      {r.best && <Tag tone="success">best</Tag>}
                      {r.multipath && <Tag>multipath</Tag>}
                      {!r.valid && <Tag tone="warning">invalid</Tag>}
                    </span>
                  </TableCell>
                  <TableCell className="py-0 font-mono whitespace-nowrap">
                    {r.nexthops.join(", ") || "—"}
                  </TableCell>
                  <TableCell className="py-0 font-mono whitespace-nowrap">
                    {r.path || "local"}
                  </TableCell>
                  <TableCell className="numeric py-0 text-right">{r.localPref ?? "—"}</TableCell>
                  <TableCell className="numeric py-0 text-right">{r.med ?? "—"}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </PanelBody>
    </Panel>
  )
}
