"use client"

import { useEffect, useRef } from "react"
import { membersKnown } from "@/lib/docker-networks"
import type { Container, DockerNetwork } from "@/lib/types"
import { cn } from "@/lib/utils"
import { SearchInput } from "@/components/page"
import { ProductLogo } from "@/components/product-logo"
import { LoadingRows } from "@/components/state"
import { linkGlyph } from "@/components/network/marks"
import { HueKey, memberProducts } from "@/components/docker/network-band"
import { isUnused, networkHue, networkOwner } from "@/components/docker/networks"

/**
 * The Networks table, folded into a column beside the network it opened: its
 * search, its owner chip and its order, so the next network to check is one
 * press away. Each row is the network in its own colour, as the product most
 * of its members run, with who made it and how many are on it; the one on
 * screen is filled.
 *
 * The services and processes have the same column (`procs/service-rail`); this
 * one is kept beside the page it serves until the three Docker pages that grew
 * a rail at once are reconciled.
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
  const listed = networks.some((network) => network.name === current)
  const pinned = !listed && currentNetwork ? currentNetwork : undefined
  const list = useRef<HTMLOListElement>(null)
  // The network on screen is kept in view: opened from a link or by Alt+↓
  // past the column's edge, it would otherwise be off its own list.
  useEffect(() => {
    const row = list.current?.querySelector<HTMLElement>("[aria-current='page']")
    row?.scrollIntoView({ block: "nearest" })
  }, [current, networks.length])

  return (
    <nav
      aria-label="Networks"
      data-slot="network-rail"
      className={cn("min-h-0 flex-col border-r border-hairline pr-4", className)}
    >
      <div className="flex shrink-0 flex-col gap-2 pb-3">
        <SearchInput
          value={query}
          onChange={(event) => onQuery(event.target.value)}
          placeholder="Network, subnet or container"
          aria-label="Find networks"
          containerClassName="sm:w-full"
        />
        <p className="flex min-w-0 items-center justify-between gap-2 text-hint text-muted-foreground">
          <span className="truncate">{caption}</span>
          {filtered && (
            <button
              type="button"
              onClick={onClearFilters}
              className="shrink-0 rounded-sm font-medium focus-ring hover:text-foreground"
            >
              Clear filters
            </button>
          )}
        </p>
      </div>
      <ol
        ref={list}
        aria-label="Networks to open"
        className="-mx-2 min-h-0 flex-1 [scrollbar-width:thin] space-y-0.5 overflow-y-auto px-2 pb-2"
      >
        {pinned && <RailRow network={pinned} current containers={containers} onOpen={onOpen} />}
        {pinned && networks.length > 0 && (
          <li aria-hidden className="mx-3 my-1.5 border-t border-hairline" />
        )}
        {networks.map((network) => (
          <RailRow
            key={network.id}
            network={network}
            current={network.name === current}
            containers={containers}
            onOpen={onOpen}
          />
        ))}
        {loading && networks.length === 0 && !pinned && <LoadingRows rows={8} />}
        {!loading && networks.length === 0 && !pinned && (
          <li className="px-3 py-4 text-hint text-muted-foreground">No network matches.</li>
        )}
      </ol>
    </nav>
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
    <li className="min-w-0">
      <button
        type="button"
        data-workspace-item={network.id}
        data-workspace-name={network.name}
        aria-current={current ? "page" : undefined}
        aria-label={network.name}
        title={network.name}
        onClick={() => onOpen(network.name)}
        className={cn(
          "flex h-12 w-full min-w-0 items-center gap-2.5 rounded-lg px-2 text-left focus-ring-inset transition-colors",
          current ? "bg-accent" : "hover:bg-row-hover",
        )}
      >
        <ProductLogo
          id={memberProducts(network, containers)[0] ?? (bridged ? "docker" : undefined)}
          size="sm"
          fallback={linkGlyph(network.driver)}
          className={cn("size-7 rounded-md [&_img]:size-4 [&_svg]:size-4", unused && "opacity-60")}
        />
        <span className="min-w-0 flex-1">
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
        </span>
        <span
          className={cn(
            "numeric shrink-0 text-hint",
            unused || unread ? "text-warning" : "text-muted-foreground",
          )}
        >
          {unread ? "unread" : unused ? "unused" : count === 0 ? "—" : count}
        </span>
      </button>
    </li>
  )
}
