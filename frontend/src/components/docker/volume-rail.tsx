"use client"

import { useSearchParams } from "next/navigation"
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
  volumeHref,
  volumeProduct,
} from "@/components/docker/volumes"
import { Rail, RailFigure, RailLine, RailName, RailRow } from "@/components/rail"
import { ProductLogo } from "@/components/product-logo"

/**
 * The Volumes table, folded into a column beside the volume it opened: its
 * search, its filters and its order, so the next volume to check is one press
 * away. Each row is the volume as the product whose data it holds with where
 * it stands as a dot in the logo's corner, its name, who holds it or what a
 * prune would do to it, and its size; the one on screen is filled.
 *
 * The column is the shared `Rail`; this file is the volume's row.
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
  /** Opens a volume carrying the page's view; the row's link does the same without it. */
  onOpen?: (name: string) => void
  className?: string
}) {
  const tab = useSearchParams().get("tab") ?? undefined
  return (
    <Rail
      slot="volume-rail"
      label="Volumes"
      search={{ placeholder: "Volume, container or path", query, onQuery }}
      rows={volumes}
      keyOf={(volume) => volume.name}
      current={(volume) => volume.name === current}
      pinned={currentVolume}
      row={(volume, on) => (
        <VolumeRow
          volume={volume}
          current={on}
          productOf={productOf}
          href={volumeHref(volume.name, tab)}
          onOpen={onOpen}
        />
      )}
      loading={loading}
      empty="No volume matches."
      filtered={filtered}
      onClearFilters={onClearFilters}
      caption={caption}
      className={className}
    />
  )
}

function VolumeRow({
  volume,
  current,
  productOf,
  href,
  onOpen,
}: {
  volume: VolumeDetail
  current: boolean
  productOf: Map<string, string>
  href: string
  onOpen?: (name: string) => void
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
    <RailRow
      item={volume.name}
      name={volume.name}
      title={volume.name}
      current={current}
      href={href}
      onOpen={onOpen && (() => onOpen(volume.name))}
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
        <RailFigure>
          {size.bytes !== undefined ? bytes(size.bytes) : size.word === "empty" ? "empty" : "—"}
        </RailFigure>
      }
    >
      <RailName current={current} mono className={cn(anonymous && "text-muted-foreground")}>
        {anonymous ? (
          `${shortName(volume)}…`
        ) : (
          <>
            {prefix && <span className="text-muted-foreground">{prefix}</span>}
            {rest}
          </>
        )}
      </RailName>
      <RailLine
        ink={line.warning ? "text-warning" : undefined}
        style={line.color ? { color: line.color } : undefined}
      >
        {line.text}
      </RailLine>
    </RailRow>
  )
}
