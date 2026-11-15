"use client"

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
import { DockerRail, DockerRailRow } from "@/components/docker/item-rail"
import { ProductLogo } from "@/components/product-logo"

/**
 * The Volumes table, folded into a column beside the volume it opened: its
 * search, its filters and its order, so the next volume to check is one press
 * away. Each row is the volume as the product whose data it holds with where
 * it stands as a dot in the logo's corner, its name, who holds it or what a
 * prune would do to it, and its size; the one on screen is filled.
 *
 * The column itself is the one every Docker item's page has (`item-rail.tsx`);
 * this file is the volume's row.
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
  return (
    <DockerRail
      slot="volume-rail"
      label="Volumes"
      search={{ label: "Find volumes", placeholder: "Volume, container or path" }}
      rows={volumes}
      keyOf={(volume) => volume.name}
      current={(volume) => volume.name === current}
      pinned={currentVolume}
      row={(volume, on) => (
        <RailRow volume={volume} current={on} productOf={productOf} onOpen={onOpen} />
      )}
      loading={loading}
      empty="No volume matches."
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
    <DockerRailRow
      item={volume.name}
      name={volume.name}
      title={volume.name}
      current={current}
      onOpen={() => onOpen(volume.name)}
      mark={
        <ProductLogo
          id={volumeProduct(volume, productOf)}
          size="sm"
          fallback={Database}
          className="size-7 rounded-md [&_img]:size-4"
        />
      }
      dot={
        <span
          className="size-1.5 rounded-full"
          style={{ background: STANDING_COLOR[standing(volume)] }}
        />
      }
      end={
        <span className="numeric shrink-0 font-mono text-hint text-muted-foreground">
          {size.bytes !== undefined ? bytes(size.bytes) : size.word === "empty" ? "empty" : "—"}
        </span>
      }
    >
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
    </DockerRailRow>
  )
}
