"use client"

import Link from "next/link"
import { Route as RouteGlyph } from "@/components/icons"
import { duration } from "@/lib/format"
import type { BGPView } from "@/lib/types"
import { EmptyState } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

/**
 * BGP through FRR, where it runs: each address family's router id and AS,
 * and every neighbour with its state, how long the session has been up and
 * how many prefixes it sends and takes. Read only — FRR's own configuration
 * is a file the operator edits, and the dashboard does not pretend to be a
 * second editor for it.
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
  if (bgp.error) {
    return <p className="text-body text-destructive">{bgp.error}</p>
  }
  const peers = bgp.families.flatMap((f) =>
    f.peers.map((p) => ({ ...p, family: f.name, localAs: f.localAs, routerId: f.routerId })),
  )
  if (peers.length === 0) {
    return (
      <p className="text-body text-muted-foreground">
        FRR is {bgp.running ? "running" : "installed but not running"} with no BGP neighbours
        configured.
      </p>
    )
  }
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Neighbour</TableHead>
          <TableHead>AS</TableHead>
          <TableHead>State</TableHead>
          <TableHead className="text-right">Up for</TableHead>
          <TableHead className="text-right">Prefixes in</TableHead>
          <TableHead className="text-right">Prefixes out</TableHead>
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
                tone={p.state === "Established" ? "running" : p.state === "Idle" ? "danger" : "warning"}
                label={p.state}
              />
            </TableCell>
            <TableCell className="numeric text-right text-xs">
              {p.uptimeSeconds ? duration(p.uptimeSeconds) : "—"}
            </TableCell>
            <TableCell className="numeric text-right font-mono text-xs">{p.prefixesReceived}</TableCell>
            <TableCell className="numeric text-right font-mono text-xs">{p.prefixesSent}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}
