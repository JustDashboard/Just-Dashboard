"use client"

import { Database, Filter, Trash } from "@/components/icons"
import { rowReveal } from "@/components/icon-action"
import { LiveBytes } from "@/components/overview/readings"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ShareBar } from "@/components/procs/workloads"
import { ProductGlyph, ProductGlyphs } from "@/components/product-logo"
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
 * Who keeps their data on this disk, beside whether anything still mounts it
 * — the two questions the old page left to be added up a card at a time.
 *
 * The left half is the Processes band asked of storage: the stacks and lone
 * containers holding the most, as spans of one bar the size of every volume,
 * each in the lane hue the Stacks and Containers pages give that name. The
 * right half is every volume by where it stands, the part a prune would take
 * hatched inside its span, as the Images band hatches what a prune gives
 * back. A line in either narrows the table to what it counts.
 */
export function VolumeBand({
  volumes,
  productOf,
  holder,
  onHolder,
  selected,
  onSelect,
  pruning,
  onPrune,
}: {
  volumes: VolumeDetail[]
  productOf: Map<string, string>
  holder: string
  onHolder: (key: string) => void
  selected: Standing | ""
  onSelect: (standing: Standing | "") => void
  pruning: boolean
  /** Undefined when the reader may not prune. */
  onPrune?: () => void
}) {
  return (
    <div
      data-slot="volume-band"
      className="grid min-w-0 animate-rise gap-x-10 gap-y-6 lg:grid-cols-2"
    >
      <HolderShare volumes={volumes} productOf={productOf} selected={holder} onSelect={onHolder} />
      <MountShare
        volumes={volumes}
        productOf={productOf}
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
  productOf,
  selected,
  onSelect,
}: {
  volumes: VolumeDetail[]
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
    <Panel plain aria-label="Who holds the data">
      <PanelHeader
        title="Held by"
        className="min-h-10"
        actions={
          // As tall as the Mounts head's button, so the two hairlines meet.
          <span className="numeric flex h-6 items-center text-body font-medium">
            <LiveBytes value={total} />
          </span>
        }
      />
      <PanelBody className="space-y-3 pt-4">
        {total === 0 ? (
          <p className="py-2 text-body text-muted-foreground">
            Docker has measured nothing in these volumes yet.
          </p>
        ) : (
          <>
            <ShareBar
              label="Held by"
              capacity={total}
              rest={restSize}
              parts={shown.map((h) => ({
                key: h.key,
                value: h.size,
                color: holderHue(h),
                label: `${h.name} ${bytes(h.size)}`,
              }))}
              format={(v) => bytes(v)}
            />
            <ul className="-mx-2">
              {shown.map((h) => {
                const pressed = selected === h.key
                const products = holderProducts(h, productOf)
                return (
                  <li key={h.key}>
                    <button
                      type="button"
                      aria-pressed={pressed}
                      aria-label={`Only the volumes ${h.kind === "none" ? "nothing mounts" : `${h.name} holds`}`}
                      onClick={() => onSelect(pressed ? "" : h.key)}
                      className={cn(
                        "group flex h-9 w-full min-w-0 items-center gap-2.5 rounded-md px-2 text-left text-body focus-ring-inset transition-colors",
                        pressed ? "bg-accent" : "hover:bg-row-hover",
                      )}
                    >
                      <span
                        aria-hidden
                        className="h-3 w-0.5 shrink-0 rounded-full"
                        style={{ background: holderHue(h) }}
                      />
                      <span className="flex size-4 shrink-0 items-center justify-center">
                        {products[0] ? (
                          <ProductGlyph id={products[0]} className="size-3.5" />
                        ) : (
                          <Database aria-hidden className="size-3.5 text-muted-foreground" />
                        )}
                      </span>
                      <span
                        className={cn(
                          "min-w-0 truncate font-medium",
                          h.kind === "none" && "text-muted-foreground",
                        )}
                        style={h.kind === "none" ? undefined : { color: holderHue(h) }}
                      >
                        {h.kind === "none" ? "Nothing mounts them" : h.name}
                      </span>
                      <span className="numeric shrink-0 truncate text-hint text-muted-foreground">
                        {plural(h.volumes.length, "volume")}
                        {h.kind === "stack" && h.down && (
                          <span className="text-warning"> · stack is down</span>
                        )}
                      </span>
                      {products.length > 1 && (
                        <span className="hidden min-w-0 truncate sm:inline">
                          <ProductGlyphs ids={products.slice(1)} max={3} />
                        </span>
                      )}
                      <span className="numeric ml-auto shrink-0 font-medium">
                        <LiveBytes value={h.size} />
                      </span>
                      <Filter
                        aria-hidden
                        className={cn(
                          "size-3.5 shrink-0 text-muted-foreground",
                          pressed ? "text-foreground" : rowReveal(),
                        )}
                      />
                    </button>
                  </li>
                )
              })}
            </ul>
            {rest.length > 0 && (
              <p className="numeric text-hint text-muted-foreground">
                and {plural(rest.length, "more holder")}
                {restSize > 0 && `, ${bytes(restSize)} between them`}
              </p>
            )}
          </>
        )}
      </PanelBody>
    </Panel>
  )
}

/* ---------------------------------------------------------------- mounts -- */

/** The part of a span a prune would take: the kind's own colour, hatched, as the Images band draws it. */
const hatched = (color: string) =>
  `repeating-linear-gradient(-45deg, ${color} 0 2px, color-mix(in oklab, ${color} 22%, transparent) 2px 5px)`

function MountShare({
  volumes,
  productOf,
  selected,
  onSelect,
  pruning,
  onPrune,
}: {
  volumes: VolumeDetail[]
  productOf: Map<string, string>
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
  const total = volumes.length

  return (
    <Panel plain aria-label="Where each volume stands">
      <PanelHeader
        title="Mounts"
        className="min-h-10"
        actions={
          pruning ? (
            <span className="flex h-6 items-center text-hint">
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
      />
      <PanelBody className="space-y-3 pt-4">
        {total === 0 ? (
          <p className="py-2 text-body text-muted-foreground">No volumes to stand anywhere.</p>
        ) : (
          <>
            <div
              role="img"
              aria-label={`Mounts: ${groups
                .map((g) => `${STANDING_LABEL[g.standing]} ${g.volumes.length}`)
                .join(", ")}`}
              className="relative flex h-2.5 w-full overflow-hidden rounded-sm bg-meter-track"
            >
              {groups.map((g) => (
                <span
                  key={g.standing}
                  title={`${STANDING_LABEL[g.standing]}: ${g.volumes.length}`}
                  className="flex h-full shrink-0 border-r border-background transition-[width] duration-700 ease-out last:border-r-0 motion-reduce:transition-none"
                  style={{ width: `${(g.volumes.length / total) * 100}%` }}
                >
                  {g.taken < g.volumes.length && (
                    <span
                      className="h-full flex-1"
                      style={{ background: STANDING_COLOR[g.standing] }}
                    />
                  )}
                  {g.taken > 0 && (
                    <span
                      className="h-full"
                      style={{
                        width: `${(g.taken / g.volumes.length) * 100}%`,
                        background: hatched(STANDING_COLOR[g.standing]),
                      }}
                    />
                  )}
                </span>
              ))}
              {/* A prune in flight sweeps the bar, as a registry check sweeps
                  the Images band and Health's segment its check. */}
              {pruning && (
                <span
                  aria-hidden
                  className="absolute inset-y-0 left-0 w-1/3 animate-sweep bg-foreground/20"
                />
              )}
            </div>
            <ul className="-mx-2">
              {groups.map((g) => {
                const pressed = selected === g.standing
                const products = [
                  ...new Set(
                    g.volumes
                      .map((v) => volumeProduct(v, productOf))
                      .filter((id): id is string => id !== undefined),
                  ),
                ]
                return (
                  <li key={g.standing}>
                    <button
                      type="button"
                      aria-pressed={pressed}
                      aria-label={`Only volumes: ${STANDING_LABEL[g.standing].toLowerCase()}`}
                      onClick={() => onSelect(pressed ? "" : g.standing)}
                      className={cn(
                        "group flex h-9 w-full min-w-0 items-center gap-2.5 rounded-md px-2 text-left text-body focus-ring-inset transition-colors",
                        pressed ? "bg-accent" : "hover:bg-row-hover",
                      )}
                    >
                      <span
                        aria-hidden
                        className="h-3 w-0.5 shrink-0 rounded-full"
                        style={{ background: STANDING_COLOR[g.standing] }}
                      />
                      <span
                        className={cn(
                          "shrink-0 font-medium",
                          g.standing === "down" && "text-warning",
                        )}
                      >
                        {STANDING_LABEL[g.standing]}
                      </span>
                      <span className="min-w-0 truncate">
                        <ProductGlyphs ids={products} max={4} />
                      </span>
                      <span className="numeric ml-auto shrink-0 text-hint text-muted-foreground">
                        {g.size > 0 && bytes(g.size)}
                      </span>
                      <span className="numeric w-5 shrink-0 text-right font-medium">
                        {g.volumes.length}
                      </span>
                      <Filter
                        aria-hidden
                        className={cn(
                          "size-3.5 shrink-0 text-muted-foreground",
                          pressed ? "text-foreground" : rowReveal(),
                        )}
                      />
                    </button>
                  </li>
                )
              })}
            </ul>
            <p className="text-hint text-muted-foreground">
              {share.volumes.length > 0
                ? `Hatched is what a prune deletes: ${plural(share.volumes.length, "volume")} no container mounts, running or stopped.`
                : "A prune would delete nothing: a stopped container still holds what it mounts."}
            </p>
          </>
        )}
      </PanelBody>
    </Panel>
  )
}
