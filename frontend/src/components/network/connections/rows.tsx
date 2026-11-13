"use client"

import { Fragment } from "react"
import Link from "next/link"
import { Servers } from "@/components/icons"
import { networkOf } from "@/lib/clients"
import { relativeTime } from "@/lib/format"
import { flowBytes } from "@/lib/network-flows"
import type { HistoricalPeer } from "@/lib/network-traffic"
import type { Peer, PortRange } from "@/lib/types"
import { cn } from "@/lib/utils"
import { NETWORK_GLYPH } from "@/components/client-mark"
import { ChoiceRow } from "@/components/flow"
import {
  ProductGlyph,
  ProductLogo,
  hasProductLogo,
  processProduct,
} from "@/components/product-logo"
import { chosenPort, portsHref } from "@/components/proxy/ports-list"
import { RX, TX } from "@/components/network/rate-pair"
import { VerbActions, type Verb } from "@/components/verbs"
import { addressHref, reached } from "@/components/network/connections/model"

/** An address drawn as the network it is on: Tailscale's mark for the tailnet, a glyph for the rest. */
export function AddressMark({ address, className }: { address: string; className?: string }) {
  const network = networkOf(address)
  return (
    <ProductLogo
      id={network.product}
      fallback={NETWORK_GLYPH[network.kind]}
      size="sm"
      className={className}
    />
  )
}

/** A program as the product it is, at the line's height, or a server glyph where it is none. */
export function ProgramGlyph({ name }: { name: string }) {
  const product = processProduct(name)
  return product && hasProductLogo(product) ? (
    <ProductGlyph id={product} />
  ) : (
    <Servers aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />
  )
}

/**
 * The local ports an address reached, each a link to what listens on it —
 * unless the kernel picked it for an outgoing call, where nothing listens and
 * the ports page could only say so.
 */
export function PortLinks({ ports, range }: { ports: number[]; range?: PortRange | null }) {
  if (ports.length === 0) return <>—</>
  return (
    <>
      {ports.map((port, i) => (
        <Fragment key={port}>
          {i > 0 && ", "}
          {chosenPort(port, range) ? (
            <Link
              href={portsHref({ q: `:${port}` })}
              aria-label={`What listens on port ${port}`}
              className="rounded-sm underline-offset-4 focus-ring hover:text-foreground hover:underline"
            >
              {port}
            </Link>
          ) : (
            port
          )}
        </Fragment>
      ))}
    </>
  )
}

/** A figure and its word, right-aligned in a column of its own width. */
function Figure({
  value,
  word,
  className,
}: {
  value: React.ReactNode
  word: string
  className?: string
}) {
  return (
    <span className={cn("numeric shrink-0 text-right text-hint whitespace-nowrap", className)}>
      <span className="text-body font-medium text-foreground">{value}</span>{" "}
      <span className="text-muted-foreground">{word}</span>
    </span>
  )
}

/**
 * One remote address on the live list, at one height whatever it carries:
 * the network it is on, the address over that network's name, where it
 * connects drawn as the product it reached with the ports it reached it on,
 * and its sockets and the established ones as figures in columns of their
 * own. It opens the address's page; its verbs are in the menu. What folding
 * kept besides — transports, far-end ports, states, the other programs — is
 * on that page rather than a third and fourth line here.
 */
export function PeerRow({
  peer,
  blocked,
  range,
  verbs,
}: {
  peer: Peer
  blocked: boolean
  range?: PortRange | null
  verbs: Verb[]
}) {
  const network = networkOf(peer.address)
  const program = reached(peer)
  const others = peer.processes.length - 1
  return (
    <ChoiceRow
      workspaceItem={{ id: peer.address, name: peer.address }}
      leading={<AddressMark address={peer.address} />}
      title={<span className="font-mono">{peer.address}</span>}
      verb={`Open ${peer.address}`}
      href={addressHref(peer.address)}
      description={
        <>
          {network.label}
          {blocked && <span className="text-destructive"> · blocked</span>}
          <span className="md:hidden"> · {program}</span>
        </>
      }
      trailing={
        <span className="flex items-center gap-5">
          <span
            className="hidden w-60 min-w-0 items-center gap-2 text-body md:flex lg:w-72"
            data-slot="peer-destination"
          >
            <ProgramGlyph name={program} />
            <span className="shrink-0 font-medium">{program}</span>
            {others > 0 && (
              <span
                className="numeric shrink-0 text-hint text-muted-foreground"
                title={peer.processes.slice(1).join(", ")}
              >
                +{others}
              </span>
            )}
            <span className="min-w-0 truncate font-mono text-hint text-muted-foreground">
              <PortLinks ports={peer.ports} range={range} />
              {peer.service && ` · ${peer.service}`}
            </span>
          </span>
          <Figure
            value={peer.count}
            word={peer.count === 1 ? "socket" : "sockets"}
            className="w-20"
          />
          <Figure value={peer.established} word="active" className="w-16 max-sm:hidden" />
        </span>
      }
      actions={<VerbActions dim verbs={verbs} menuLabel={`Actions for ${peer.address}`} />}
    />
  )
}

/**
 * One remote address as socket history kept it in an hour, in the live row's
 * shape: who held its sockets, how many, and what TCP carried. It opens the
 * address's page on the same hour.
 */
export function RecordedRow({ peer, hour }: { peer: HistoricalPeer; hour: string }) {
  const network = networkOf(peer.address)
  const owners = peer.owners.join(", ")
  return (
    <ChoiceRow
      workspaceItem={{ id: peer.address, name: peer.address }}
      leading={<AddressMark address={peer.address} />}
      title={<span className="font-mono">{peer.address}</span>}
      verb={`Open ${peer.address}`}
      href={addressHref(peer.address, { tab: "past", hour })}
      description={
        <>
          {network.label}
          <span className="md:hidden"> · {owners}</span>
        </>
      }
      trailing={
        <span className="flex items-center gap-5">
          <span
            className="hidden w-60 min-w-0 items-center gap-2 text-body md:flex lg:w-72"
            title={`seen ${relativeTime(peer.firstSeen)} to ${relativeTime(peer.lastSeen)}`}
          >
            <ProgramGlyph name={peer.owners[0] ?? ""} />
            <span className="min-w-0 truncate font-medium">{owners}</span>
            <span className="shrink-0 font-mono text-hint text-muted-foreground">
              {peer.protocols.join("/").toUpperCase()} {peer.localPorts.join(", ")}
            </span>
          </span>
          <Figure
            value={peer.sockets}
            word={peer.sockets === 1 ? "socket" : "sockets"}
            className="w-20"
          />
          <span className="numeric w-36 shrink-0 text-right font-mono text-hint whitespace-nowrap max-sm:hidden">
            <span style={{ color: RX }}>↓ {flowBytes(peer.rxBytes)}</span>{" "}
            <span style={{ color: TX }}>↑ {flowBytes(peer.txBytes)}</span>
          </span>
        </span>
      }
    />
  )
}
