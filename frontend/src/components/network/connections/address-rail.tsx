"use client"

import { useSearchParams } from "next/navigation"
import { networkOf } from "@/lib/clients"
import { plural } from "@/lib/format"
import type { Peer } from "@/lib/types"
import { Rail, RailFigure, RailLine, RailName, RailRow } from "@/components/rail"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { AddressMark } from "@/components/network/connections/rows"
import { addressHref, reached, shortAddress } from "@/components/network/connections/model"

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
  error,
  onRetry,
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
  /** The list could not be read: said in place of the rows. */
  error?: Error
  onRetry?: () => void
  query: string
  onQuery: (value: string) => void
  scope: "all" | "public"
  onScope: (scope: "all" | "public") => void
  /** Undefined until the list is read: the chip has no count to show before. */
  fromInternet: number | undefined
  /** Opens an address carrying the page's view; the row's link does the same without it. */
  onOpen?: (address: string) => void
  className?: string
}) {
  const search = useSearchParams()
  const view = { tab: search.get("tab") ?? undefined, hour: search.get("hour") ?? undefined }
  return (
    <Rail<{ address: string; peer?: Peer }>
      slot="address-rail"
      label="Remote addresses"
      listLabel="Addresses to open"
      search={{
        label: "Search the addresses",
        placeholder: "Address, port or process",
        query,
        onQuery,
      }}
      filters={
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
      }
      caption={
        loading && rows.length === 0
          ? "Reading the addresses…"
          : plural(rows.length, "address", "addresses")
      }
      rows={rows.map((peer) => ({ address: peer.address, peer }))}
      keyOf={(row) => row.address}
      current={(row) => row.address === current}
      pinned={{ address: current }}
      row={(row, on) => (
        <AddressRow
          address={row.address}
          peer={row.peer}
          current={on}
          href={addressHref(row.address, view)}
          onOpen={onOpen}
        />
      )}
      loading={loading}
      loadingRows={8}
      error={error}
      onRetry={onRetry}
      empty="No address matches."
      className={className}
    />
  )
}

function AddressRow({
  address,
  peer,
  current,
  href,
  onOpen,
}: {
  address: string
  peer?: Peer
  current: boolean
  href: string
  onOpen?: (address: string) => void
}) {
  return (
    <RailRow
      item={address}
      name={address}
      title={address}
      current={current}
      href={href}
      onOpen={onOpen && (() => onOpen(address))}
      mark={<AddressMark address={address} className="size-7 rounded-md [&_svg]:size-3.5" />}
      end={<RailFigure>{peer ? peer.count : "—"}</RailFigure>}
    >
      <RailName current={current} mono>
        {/* The column holds some twenty characters; an IPv6 address keeps both ends. */}
        {shortAddress(address, 21)}
      </RailName>
      <RailLine>{peer ? reached(peer) : networkOf(address).label}</RailLine>
    </RailRow>
  )
}
