"use client"

import { Database, Trash } from "@/components/icons"
import { BarReading } from "@/components/bar-reading"
import { LiveBytes } from "@/components/overview/readings"
import { ProductGlyph } from "@/components/product-logo"
import {
  STANDING_COLOR,
  STANDING_LABEL,
  STANDING_ORDER,
  holderHue,
  holders,
  prunable,
  pruneShare,
  standing,
  volumeProduct,
  type Holder,
  type Standing,
} from "@/components/docker/volumes"
import { Button } from "@/components/ui/button"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { bytes, plural } from "@/lib/format"
import type { VolumeDetail } from "@/lib/types"
import { cn } from "@/lib/utils"

/** How many holders the bar names; the rest are one muted span. */
const SHOWN = 5

/**
 * Who keeps their data on this disk, beside whether anything still mounts it,
 * each a bar with one line under it. Both are here before the list answers,
 * and when there are no volumes at all, so the table does not move when it does.
 *
 * The left is the Processes band asked of storage: the stacks and lone
 * containers holding the most, as spans of one bar the size of every volume,
 * each in the lane hue the Stacks and Containers pages give that name. The
 * right is every volume by where it stands, the part a prune would take hatched
 * inside its span, as the Images band hatches what a prune gives back. A press
 * in either narrows the table to what it counts.
 */
export function VolumeBand({
  volumes,
  loading,
  failed,
  productOf,
  holder,
  onHolder,
  selected,
  onSelect,
  pruning,
  onPrune,
}: {
  volumes: VolumeDetail[]
  /** The list has not answered yet, so an empty one means nothing. */
  loading: boolean
  failed: boolean
  productOf: Map<string, string>
  holder: string
  onHolder: (key: string) => void
  selected: Standing | ""
  onSelect: (standing: Standing | "") => void
  pruning: boolean
  /** Undefined when the reader may not prune. */
  onPrune?: () => void
}) {
  const unknown = failed ? (
    "Docker's volumes could not be read."
  ) : loading ? (
    <TextShimmer>Reading the volumes…</TextShimmer>
  ) : undefined
  return (
    <div
      data-slot="volume-band"
      className="grid min-w-0 animate-rise gap-x-10 gap-y-6 lg:grid-cols-2"
    >
      <HolderShare
        volumes={volumes}
        unknown={unknown}
        productOf={productOf}
        selected={holder}
        onSelect={onHolder}
      />
      <MountShare
        volumes={volumes}
        unknown={unknown}
        selected={selected}
        onSelect={onSelect}
        pruning={pruning}
        onPrune={onPrune}
      />
    </div>
  )
}

/* --------------------------------------------------------------- holders -- */

function holderProducts(holder: Holder, productOf: Map<string, string>) {
  const ids = holder.volumes
    .map((v) => volumeProduct(v, productOf))
    .filter((id): id is string => id !== undefined)
  return [...new Set(ids)]
}

function HolderShare({
  volumes,
  unknown,
  productOf,
  selected,
  onSelect,
}: {
  volumes: VolumeDetail[]
  unknown: React.ReactNode
  productOf: Map<string, string>
  selected: string
  onSelect: (key: string) => void
}) {
  const all = holders(volumes)
  const total = all.reduce((sum, h) => sum + h.size, 0)
  const shown = all.filter((h) => h.size > 0).slice(0, SHOWN)
  const rest = all.filter((h) => !shown.includes(h))
  const restSize = rest.reduce((sum, h) => sum + h.size, 0)

  return (
    <BarReading
      title="Held by"
      label="Who holds the data"
      list="Who holds the most"
      capacity={total}
      rest={restSize}
      restLabel="Other holders"
      format={(v) => bytes(v)}
      figure={
        total > 0 && (
          <span className="numeric flex h-7 items-center text-body font-medium">
            <LiveBytes value={total} />
          </span>
        )
      }
      message={
        unknown ??
        (volumes.length === 0
          ? "No volumes on this server."
          : total === 0
            ? "Docker has measured nothing in these volumes yet."
            : undefined)
      }
      note={rest.length > 0 && `and ${plural(rest.length, "more holder")}`}
      parts={shown.map((h) => {
        const pressed = selected === h.key
        const product = holderProducts(h, productOf)[0]
        return {
          key: h.key,
          value: h.size,
          color: holderHue(h),
          mark: product ? (
            <ProductGlyph id={product} className="size-3.5" />
          ) : (
            <Database aria-hidden className="size-3.5 text-muted-foreground" />
          ),
          name:
            h.kind === "none" ? (
              <span className="text-muted-foreground">Nothing mounts them</span>
            ) : (
              h.name
            ),
          detail: (
            <>
              {plural(h.volumes.length, "volume")}
              {h.kind === "stack" && h.down && (
                <span className="text-warning"> · stack is down</span>
              )}
            </>
          ),
          figure: <LiveBytes value={h.size} />,
          label: `${h.name} ${bytes(h.size)}`,
          press: {
            label: `Only the volumes ${h.kind === "none" ? "nothing mounts" : `${h.name} holds`}`,
            pressed,
            onPress: () => onSelect(pressed ? "" : h.key),
          },
        }
      })}
    />
  )
}

/* ---------------------------------------------------------------- mounts -- */

function MountShare({
  volumes,
  unknown,
  selected,
  onSelect,
  pruning,
  onPrune,
}: {
  volumes: VolumeDetail[]
  unknown: React.ReactNode
  selected: Standing | ""
  onSelect: (standing: Standing | "") => void
  pruning: boolean
  onPrune?: () => void
}) {
  const groups = STANDING_ORDER.map((s) => {
    const list = volumes.filter((v) => standing(v) === s)
    return {
      standing: s,
      volumes: list,
      size: list.reduce((sum, v) => sum + Math.max(v.size, 0), 0),
      taken: list.filter(prunable).length,
    }
  }).filter((g) => g.volumes.length > 0)
  const share = pruneShare(volumes)

  return (
    <BarReading
      title="Mounts"
      label="Where each volume stands"
      list="Where the volumes stand"
      fixed
      working={pruning}
      figure={
        pruning ? (
          <span className="flex h-7 items-center text-hint">
            <TextShimmer>Pruning…</TextShimmer>
          </span>
        ) : (
          onPrune &&
          share.volumes.length > 0 && (
            <Button size="xs" variant="outline" onClick={onPrune}>
              <Trash />
              Prune {share.size > 0 ? bytes(share.size) : plural(share.volumes.length, "volume")}
            </Button>
          )
        )
      }
      message={unknown ?? (volumes.length === 0 ? "No volumes to stand anywhere." : undefined)}
      parts={groups.map((g) => {
        const label = STANDING_LABEL[g.standing]
        const pressed = selected === g.standing
        return {
          key: g.standing,
          value: g.volumes.length,
          color: STANDING_COLOR[g.standing],
          hatch: g.taken / g.volumes.length,
          mark: <span className="numeric text-hint font-medium">{g.volumes.length}</span>,
          name: <span className={cn(g.standing === "down" && "text-warning")}>{label}</span>,
          detail: g.size > 0 ? bytes(g.size) : undefined,
          figure: g.volumes.length,
          label: `${label} ${g.volumes.length}`,
          title:
            g.taken > 0
              ? `Hatched is what a prune deletes: ${plural(g.taken, "volume")} no container mounts, running or stopped.`
              : g.standing === "stopped"
                ? "A prune would not delete these: a stopped container still holds what it mounts."
                : undefined,
          press: {
            label: `Only volumes: ${label.toLowerCase()}`,
            pressed,
            onPress: () => onSelect(pressed ? "" : g.standing),
          },
        }
      })}
    />
  )
}
