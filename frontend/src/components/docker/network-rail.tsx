"use client"

import { useSearchParams } from "next/navigation"
import { membersKnown } from "@/lib/docker-networks"
import type { Container, DockerNetwork } from "@/lib/types"
import { cn } from "@/lib/utils"
import { ProductLogo } from "@/components/product-logo"
import { linkGlyph } from "@/components/network/marks"
import { Rail, RailFigure, RailLine, RailName, RailRow } from "@/components/rail"
import { HueKey, memberProducts } from "@/components/docker/network-band"
import { isUnused, networkHref, networkHue, networkOwner } from "@/components/docker/networks"

/**
 * The Networks table, folded into a column beside the network it opened: its
 * search, its owner chip and its order, so the next network to check is one
 * press away. Each row is the network in its own colour, as the product most
 * of its members run, with who made it and how many are on it; the one on
 * screen is filled.
 *
 * The column is the shared `Rail`; this file is the network's row.
 */
export function NetworkRail({
  networks,
  current,
  currentNetwork,
  containers,
  loading,
  query,
  onQuery,
  filtered,
  onClearFilters,
  caption,
  onOpen,
  className,
}: {
  networks: DockerNetwork[]
  /** The name of the network on screen. */
  current: string
  /** The network on screen, drawn first when the list does not hold it. */
  currentNetwork?: DockerNetwork
  containers: Map<string, Container>
  loading: boolean
  query: string
  onQuery: (value: string) => void
  filtered: boolean
  onClearFilters: () => void
  caption: string
  /** Opens a network carrying the page's view; the row's link does the same without it. */
  onOpen?: (name: string) => void
  className?: string
}) {
  const tab = useSearchParams().get("tab") ?? undefined
  return (
    <Rail
      slot="network-rail"
      label="Networks"
      search={{ placeholder: "Network, subnet or container", query, onQuery }}
      rows={networks}
      keyOf={(network) => network.id}
      current={(network) => network.name === current}
      pinned={currentNetwork}
      row={(network, on) => (
        <NetworkRow
          network={network}
          current={on}
          containers={containers}
          href={networkHref(network.name, tab)}
          onOpen={onOpen}
        />
      )}
      loading={loading}
      empty="No network matches."
      filtered={filtered}
      onClearFilters={onClearFilters}
      caption={caption}
      className={className}
    />
  )
}

function NetworkRow({
  network,
  current,
  containers,
  href,
  onOpen,
}: {
  network: DockerNetwork
  current: boolean
  containers: Map<string, Container>
  href: string
  onOpen?: (name: string) => void
}) {
  const unused = isUnused(network)
  const unread = !membersKnown(network)
  const count = network.usedBy.length
  const bridged = network.driver === "bridge" || network.driver === "overlay"
  return (
    <RailRow
      item={network.id}
      name={network.name}
      title={network.name}
      current={current}
      href={href}
      onOpen={onOpen && (() => onOpen(network.name))}
      quiet={unused}
      mark={
        <ProductLogo
          id={memberProducts(network, containers)[0] ?? (bridged ? "docker" : undefined)}
          size="sm"
          fallback={linkGlyph(network.driver)}
          className="size-7 rounded-md [&_img]:size-4 [&_svg]:size-4"
        />
      }
      end={
        <RailFigure mono={false} ink={unused || unread ? "text-warning" : undefined}>
          {unread ? "unread" : unused ? "unused" : count === 0 ? "—" : count}
        </RailFigure>
      }
    >
      <span className="flex min-w-0 items-center gap-1.5">
        <HueKey color={networkHue(network.name)} />
        <RailName current={current} className={cn("min-w-0", unused && "text-muted-foreground")}>
          {network.name}
        </RailName>
      </span>
      <RailLine>{networkOwner(network).label}</RailLine>
    </RailRow>
  )
}
