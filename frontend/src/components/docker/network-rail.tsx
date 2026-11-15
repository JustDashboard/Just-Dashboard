"use client"

import { membersKnown } from "@/lib/docker-networks"
import type { Container, DockerNetwork } from "@/lib/types"
import { cn } from "@/lib/utils"
import { ProductLogo } from "@/components/product-logo"
import { linkGlyph } from "@/components/network/marks"
import { DockerRail, DockerRailRow } from "@/components/docker/item-rail"
import { HueKey, memberProducts } from "@/components/docker/network-band"
import { isUnused, networkHue, networkOwner } from "@/components/docker/networks"

/**
 * The Networks table, folded into a column beside the network it opened: its
 * search, its owner chip and its order, so the next network to check is one
 * press away. Each row is the network in its own colour, as the product most
 * of its members run, with who made it and how many are on it; the one on
 * screen is filled.
 *
 * The column itself is the one every Docker item's page has (`item-rail.tsx`);
 * this file is the network's row.
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
  onOpen: (name: string) => void
  className?: string
}) {
  return (
    <DockerRail
      slot="network-rail"
      label="Networks"
      search={{ label: "Find networks", placeholder: "Network, subnet or container" }}
      rows={networks}
      keyOf={(network) => network.id}
      current={(network) => network.name === current}
      pinned={currentNetwork}
      row={(network, on) => (
        <RailRow network={network} current={on} containers={containers} onOpen={onOpen} />
      )}
      loading={loading}
      empty="No network matches."
      query={query}
      onQuery={onQuery}
      filtered={filtered}
      onClearFilters={onClearFilters}
      caption={caption}
      className={className}
    />
  )
}

function RailRow({
  network,
  current,
  containers,
  onOpen,
}: {
  network: DockerNetwork
  current: boolean
  containers: Map<string, Container>
  onOpen: (name: string) => void
}) {
  const unused = isUnused(network)
  const unread = !membersKnown(network)
  const count = network.usedBy.length
  const bridged = network.driver === "bridge" || network.driver === "overlay"
  return (
    <DockerRailRow
      item={network.id}
      name={network.name}
      title={network.name}
      current={current}
      onOpen={() => onOpen(network.name)}
      mark={
        <ProductLogo
          id={memberProducts(network, containers)[0] ?? (bridged ? "docker" : undefined)}
          size="sm"
          fallback={linkGlyph(network.driver)}
          className={cn("size-7 rounded-md [&_img]:size-4 [&_svg]:size-4", unused && "opacity-60")}
        />
      }
      end={
        <span
          className={cn(
            "numeric shrink-0 text-hint",
            unused || unread ? "text-warning" : "text-muted-foreground",
          )}
        >
          {unread ? "unread" : unused ? "unused" : count === 0 ? "—" : count}
        </span>
      }
    >
      <span className="flex min-w-0 items-center gap-1.5">
        <HueKey color={networkHue(network.name)} />
        <span
          className={cn(
            "truncate text-body",
            current ? "font-semibold" : "font-medium",
            unused && "text-muted-foreground",
          )}
        >
          {network.name}
        </span>
      </span>
      <span className="block truncate text-hint text-muted-foreground">
        {networkOwner(network).label}
      </span>
    </DockerRailRow>
  )
}
