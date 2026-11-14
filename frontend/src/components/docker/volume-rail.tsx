"use client"

import { useEffect, useRef } from "react"
import { Database } from "@/components/icons"
import { bytes } from "@/lib/format"
import type { VolumeDetail } from "@/lib/types"
import { cn } from "@/lib/utils"
import {
  STANDING_COLOR,
  holderHue,
  holderOf,
  isAnonymous,
  shortName,
  sizeReading,
  splitName,
  standing,
  standingWords,
  volumeProduct,
} from "@/components/docker/volumes"
import { SearchInput } from "@/components/page"
import { ProductLogo } from "@/components/product-logo"
import { LoadingRows } from "@/components/state"

/**
 * The Volumes table, folded into a column beside the volume it opened: its
 * search, its filters and its order, so the next volume to check is one press
 * away. Each row is the volume as the product whose data it holds with where
 * it stands as a dot in the logo's corner, its name, who holds it or what a
 * prune would do to it, and its size; the one on screen is filled.
 *
 * The services and processes have the same column (`procs/service-rail`); this
 * one is kept beside the page it serves until the three Docker pages that grew
 * a rail at once are reconciled.
 */
export function VolumeRail({
  volumes,
  current,
  currentVolume,
  productOf,
  loading,
  query,
  onQuery,
  filtered,
  onClearFilters,
  caption,
  onOpen,
  className,
}: {
  volumes: VolumeDetail[]
  current: string
  /** The volume on screen, drawn first when the list does not hold it. */
  currentVolume?: VolumeDetail
  productOf: Map<string, string>
  loading: boolean
  query: string
  onQuery: (value: string) => void
  filtered: boolean
  onClearFilters: () => void
  caption: string
  onOpen: (name: string) => void
  className?: string
}) {
  const listed = volumes.some((volume) => volume.name === current)
  const pinned = !listed && currentVolume ? currentVolume : undefined
  const list = useRef<HTMLOListElement>(null)
  // The volume on screen is kept in view: opened from a link or by Alt+↓ past
  // the column's edge, it would otherwise be off its own list.
  useEffect(() => {
    const row = list.current?.querySelector<HTMLElement>("[aria-current='page']")
    row?.scrollIntoView({ block: "nearest" })
  }, [current, volumes.length])

  return (
    <nav
      aria-label="Volumes"
      data-slot="volume-rail"
      className={cn("min-h-0 flex-col border-r border-hairline pr-4", className)}
    >
      <div className="flex shrink-0 flex-col gap-2 pb-3">
        <SearchInput
          value={query}
          onChange={(event) => onQuery(event.target.value)}
          placeholder="Volume, container or path"
          aria-label="Find volumes"
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
        aria-label="Volumes to open"
        className="-mx-2 min-h-0 flex-1 [scrollbar-width:thin] space-y-0.5 overflow-y-auto px-2 pb-2"
      >
        {pinned && <RailRow volume={pinned} current productOf={productOf} onOpen={onOpen} />}
        {pinned && volumes.length > 0 && (
          <li aria-hidden className="mx-3 my-1.5 border-t border-hairline" />
        )}
        {volumes.map((volume) => (
          <RailRow
            key={volume.name}
            volume={volume}
            current={volume.name === current}
            productOf={productOf}
            onOpen={onOpen}
          />
        ))}
        {loading && volumes.length === 0 && !pinned && <LoadingRows rows={8} />}
        {!loading && volumes.length === 0 && !pinned && (
          <li className="px-3 py-4 text-hint text-muted-foreground">No volume matches.</li>
        )}
      </ol>
    </nav>
  )
}

function RailRow({
  volume,
  current,
  productOf,
  onOpen,
}: {
  volume: VolumeDetail
  current: boolean
  productOf: Map<string, string>
  onOpen: (name: string) => void
}) {
  const { prefix, rest } = splitName(volume)
  const anonymous = isAnonymous(volume)
  const holder = holderOf(volume)
  const words = standingWords(volume, false)
  const size = sizeReading(volume)
  // In use, the line says whose it is; anything else is the reason to open it.
  const line: { text: string; color?: string; warning?: boolean } =
    standing(volume) === "running"
      ? holder.kind === "stack"
        ? { text: holder.name, color: holderHue(holder) }
        : { text: volume.usedBy[0]?.name ?? words.word }
      : { text: words.word, warning: words.warning }
  return (
    <li className="min-w-0">
      <button
        type="button"
        data-workspace-item={volume.name}
        data-workspace-name={volume.name}
        aria-current={current ? "page" : undefined}
        aria-label={volume.name}
        title={volume.name}
        onClick={() => onOpen(volume.name)}
        className={cn(
          "flex h-12 w-full min-w-0 items-center gap-2.5 rounded-lg px-2 text-left focus-ring-inset transition-colors",
          current ? "bg-accent" : "hover:bg-row-hover",
        )}
      >
        <span className="relative flex shrink-0">
          <ProductLogo
            id={volumeProduct(volume, productOf)}
            size="sm"
            fallback={Database}
            className="size-7 rounded-md [&_img]:size-4"
          />
          <span
            aria-hidden
            className="absolute -right-0.5 -bottom-0.5 flex size-2.5 items-center justify-center rounded-full bg-background"
          >
            <span
              className="size-1.5 rounded-full"
              style={{ background: STANDING_COLOR[standing(volume)] }}
            />
          </span>
        </span>
        <span className="min-w-0 flex-1">
          <span
            className={cn(
              "block truncate font-mono text-body",
              current ? "font-semibold" : "font-medium",
              anonymous && "text-muted-foreground",
            )}
          >
            {anonymous ? (
              `${shortName(volume)}…`
            ) : (
              <>
                {prefix && <span className="text-muted-foreground">{prefix}</span>}
                {rest}
              </>
            )}
          </span>
          <span
            className={cn(
              "block truncate text-hint",
              line.warning ? "text-warning" : "text-muted-foreground",
            )}
            style={line.color ? { color: line.color } : undefined}
          >
            {line.text}
          </span>
        </span>
        <span className="numeric shrink-0 font-mono text-hint text-muted-foreground">
          {size.bytes !== undefined ? bytes(size.bytes) : size.word === "empty" ? "empty" : "—"}
        </span>
      </button>
    </li>
  )
}
