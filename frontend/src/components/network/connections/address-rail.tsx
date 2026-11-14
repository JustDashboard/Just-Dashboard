"use client"

import { useEffect, useRef } from "react"
import { networkOf } from "@/lib/clients"
import type { Peer } from "@/lib/types"
import { cn } from "@/lib/utils"
import { SearchInput } from "@/components/page"
import { LoadingRows } from "@/components/state"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { AddressMark } from "@/components/network/connections/rows"
import { reached, shortAddress } from "@/components/network/connections/model"

/**
 * The live list, folded into a column beside the address it opened: the same
 * search and the same "From the internet" chip, so the next address to look at
 * is one press away rather than a trip back to the list. Each row is the
 * address on its network, the program it reached and its sockets; the one on
 * screen is filled, and is drawn first apart from the rest when the list's
 * filter or the last read no longer holds it.
 */
export function AddressRail({
  rows,
  current,
  loading,
  query,
  onQuery,
  scope,
  onScope,
  fromInternet,
  onOpen,
  className,
}: {
  rows: Peer[]
  current: string
  loading: boolean
  query: string
  onQuery: (value: string) => void
  scope: "all" | "public"
  onScope: (scope: "all" | "public") => void
  /** Undefined until the list is read: the chip has no count to show before. */
  fromInternet: number | undefined
  onOpen: (address: string) => void
  className?: string
}) {
  const listed = rows.some((row) => row.address === current)
  const list = useRef<HTMLOListElement>(null)
  // The address on screen is kept in view: opened from a link or by Alt+↓
  // past the column's edge, it would otherwise be off its own list.
  useEffect(() => {
    list.current?.querySelector<HTMLElement>("[aria-current='page']")?.scrollIntoView({
      block: "nearest",
    })
  }, [current])

  return (
    <nav
      aria-label="Remote addresses"
      data-slot="address-rail"
      className={cn("min-h-0 flex-col border-r border-hairline pr-4", className)}
    >
      <div className="flex shrink-0 flex-col gap-2 pb-3">
        <SearchInput
          value={query}
          onChange={(event) => onQuery(event.target.value)}
          placeholder="Address, port or process"
          aria-label="Search the addresses"
          containerClassName="sm:w-full"
        />
        <ToggleGroup
          type="single"
          value={scope}
          onValueChange={(next) => next && onScope(next as "all" | "public")}
          variant="outline"
          size="sm"
          aria-label="Which addresses to list"
          className="self-start"
        >
          <ToggleGroupItem value="all" className="px-2.5 text-hint">
            Everything
          </ToggleGroupItem>
          <ToggleGroupItem value="public" className="px-2.5 text-hint">
            From the internet {fromInternet}
          </ToggleGroupItem>
        </ToggleGroup>
      </div>
      <ol
        ref={list}
        aria-label="Addresses to open"
        className="-mx-2 min-h-0 flex-1 [scrollbar-width:thin] space-y-0.5 overflow-y-auto px-2 pb-2"
      >
        {!listed && (
          <>
            <RailRow address={current} current onOpen={onOpen} />
            {rows.length > 0 && <li aria-hidden className="mx-3 my-1.5 border-t border-hairline" />}
          </>
        )}
        {rows.map((row) => (
          <RailRow
            key={row.address}
            address={row.address}
            peer={row}
            current={row.address === current}
            onOpen={onOpen}
          />
        ))}
        {loading && rows.length === 0 && <LoadingRows rows={8} />}
        {!loading && rows.length === 0 && (
          <li className="px-3 py-4 text-hint text-muted-foreground">No address matches.</li>
        )}
      </ol>
    </nav>
  )
}

function RailRow({
  address,
  peer,
  current,
  onOpen,
}: {
  address: string
  peer?: Peer
  current: boolean
  onOpen: (address: string) => void
}) {
  return (
    <li className="min-w-0">
      <button
        type="button"
        data-workspace-item={address}
        data-workspace-name={address}
        aria-current={current ? "page" : undefined}
        aria-label={address}
        title={address}
        onClick={() => onOpen(address)}
        className={cn(
          "flex h-12 w-full min-w-0 items-center gap-2.5 rounded-lg px-2 text-left focus-ring-inset transition-colors",
          current ? "bg-accent" : "hover:bg-row-hover",
        )}
      >
        <AddressMark address={address} className="size-7 rounded-md [&_svg]:size-3.5" />
        <span className="min-w-0 flex-1">
          <span
            className={cn(
              "block truncate font-mono text-body",
              current ? "font-semibold" : "font-medium",
            )}
          >
            {/* The column holds some twenty characters; an IPv6 address keeps both ends. */}
            {shortAddress(address, 21)}
          </span>
          <span className="block truncate text-hint text-muted-foreground">
            {peer ? reached(peer) : networkOf(address).label}
          </span>
        </span>
        <span className="numeric shrink-0 font-mono text-hint text-muted-foreground">
          {peer ? peer.count : "—"}
        </span>
      </button>
    </li>
  )
}
