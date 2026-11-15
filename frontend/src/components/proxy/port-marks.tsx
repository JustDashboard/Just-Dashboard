"use client"

import { Router } from "@/components/icons"
import type { Listener } from "@/lib/types"
import { cn } from "@/lib/utils"
import { ProductLogo } from "@/components/product-logo"
import { DashboardMark } from "@/components/backups/marks"
import { useProjectFavicon } from "@/components/deploy/project-mark"
import { Status } from "@/components/status-dot"
import {
  dangerousService,
  listenerProduct,
  networkWords,
  onUplink,
  pastFirewallWords,
  reachVerdict,
  socketAddresses,
  UPLINK_CAVEAT,
  type Socket,
} from "@/components/proxy/ports"

/** Said of a port Docker publishes through its NAT rules, where the command would be. */
export const NAT_WORDS = "Forwarded by Docker's NAT rules; no process listens"

/**
 * The owner as the product it is, on the tile the identity line draws a host's
 * facts with — a deployment's release by its project's website icon first.
 */
export function ProcessMark({ listener, className }: { listener: Listener; className?: string }) {
  const favicon = useProjectFavicon(listener.container?.deployment?.projectId)
  if (listener.self) return <DashboardMark />
  return (
    <ProductLogo
      id={listenerProduct(listener)}
      favicon={favicon}
      size="sm"
      fallback={Router}
      className={className}
    />
  )
}

/** A socket's addresses on one line, IPv4 first, as a reader would type them. */
export function addressLine(socket: Socket): string {
  return socketAddresses(socket).join(", ")
}

/**
 * Where a socket answers, coloured by who can connect as the posture levels
 * the same socket: a database on a public address is critical, sshd on the
 * tailnet a notice, loopback nothing. One line; what a database's firewall
 * default and the way past it say is the tooltip here and a line of its own
 * on the port's page.
 */
export function ReachWord({ socket, className }: { socket: Socket; className?: string }) {
  const verdict = reachVerdict(socket)
  const words = networkWords(socket)
  const service = dangerousService(socket)
  const notes = reachNotes(socket)
  return (
    <span
      className={cn("inline-flex min-w-0 items-center gap-2", className)}
      title={notes.length > 0 ? notes.join("\n") : undefined}
    >
      {verdict ? (
        <Status
          verdict={verdict}
          label={service ? `${service} · ${words}` : words}
          icon={Router}
          className="min-w-0 [&>span:last-child]:truncate"
        />
      ) : (
        <span className="truncate text-xs text-muted-foreground">{words}</span>
      )}
      {socket.interface && (
        <span className="shrink-0 font-mono text-hint text-muted-foreground">
          {socket.interface}
        </span>
      )}
    </span>
  )
}

/**
 * What else is true of where a socket answers: the provider may map a public
 * address onto the private uplink, and a database's firewall default — or
 * the way past it.
 */
export function reachNotes(socket: Socket): string[] {
  const notes: string[] = []
  const service = dangerousService(socket)
  if (onUplink(socket)) notes.push(UPLINK_CAVEAT)
  if (service && socket.inboundDefault) {
    notes.push(`Firewall's inbound default: ${socket.inboundDefault}`)
  }
  const past = service ? pastFirewallWords(socket) : undefined
  if (past) notes.push(past)
  return notes
}
