"use client"

import { Box, Database, Layers, RefreshClockwise, Trash, Wrench } from "@/components/icons"
import { BarReading, type ReadingPart } from "@/components/bar-reading"
import { LiveBytes } from "@/components/overview/readings"
import { ProductGlyphs, imageProducts } from "@/components/product-logo"
import { Term } from "@/components/docker/explain"
import type { ConfirmFn } from "@/components/docker/shared"
import {
  FRESHNESS_COLOR,
  FRESHNESS_LABEL,
  FRESHNESS_ORDER,
  primaryTag,
  type Freshness,
} from "@/components/docker/images"
import { Button } from "@/components/ui/button"
import { HoverCard, HoverCardContent, HoverCardTrigger } from "@/components/ui/hover-card"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { useAuth } from "@/hooks/use-auth"
import {
  prune,
  pruneBuildCache,
  pruneContainers,
  pruneImages,
  pruneSummary,
  RECLAIM_SAFE,
} from "@/lib/docker-prune"
import { bytes, plural, relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import type {
  DiskDefinition,
  DockerDiskUsage,
  DockerDiskUsageLine,
  DockerImage,
  PruneReport,
} from "@/lib/types"
import { cn } from "@/lib/utils"

/**
 * What Docker holds on this disk and how much of it could go, beside what the
 * registries say about the images on it, each a bar with one line under it.
 *
 * The disk bar is a span per kind in the colour the Docker overview draws that
 * kind in, the part a prune would give back hatched inside its own span, so the
 * reclaimable share of each is seen before a figure is read. A kind's own
 * Reclaim is on the line while that kind is; the whole sweep stays in the head.
 * Both blocks are here before their readings are, saying so, so nothing moves
 * when they land.
 */
export function ImageBand({
  usage,
  usageLoading,
  usageError,
  images,
  imagesLoading,
  imagesFailed,
  states,
  checkedAt,
  checking,
  updatesError,
  onCheck,
  state,
  onState,
  confirm,
  onPruned,
}: {
  usage?: DockerDiskUsage
  usageLoading: boolean
  usageError?: Error
  images: DockerImage[]
  /** The image list has not answered yet, so an empty one means nothing. */
  imagesLoading: boolean
  imagesFailed: boolean
  states: Map<string, Freshness>
  checkedAt?: string
  checking: boolean
  updatesError?: Error
  onCheck: () => void
  state: Freshness | ""
  onState: (state: Freshness | "") => void
  confirm: ConfirmFn
  onPruned: () => void
}) {
  return (
    <div
      data-slot="image-band"
      className="grid min-w-0 animate-rise gap-x-10 gap-y-6 lg:grid-cols-2"
    >
      <DiskShare
        usage={usage}
        loading={usageLoading}
        error={usageError}
        confirm={confirm}
        onPruned={onPruned}
      />
      <RegistryShare
        images={images}
        loading={imagesLoading}
        failed={imagesFailed}
        states={states}
        checkedAt={checkedAt}
        checking={checking}
        error={updatesError}
        onCheck={onCheck}
        selected={state}
        onSelect={onState}
      />
    </div>
  )
}

/* ------------------------------------------------------------------ disk -- */

type Kind = {
  key: string
  label: string
  /** The overview's `DiskSummary` colour for the same kind, so a kind is one hue across Docker. */
  color: string
  icon: typeof Layers
  /** What its own prune is called, for a mark that has only its glyph to say so. */
  verb?: string
  unit: string
  many?: string
  definition: string
  lineOf: (d: DockerDiskUsage) => DockerDiskUsageLine
  sizeOf: (d: DockerDiskUsage) => number
}

const KINDS: Kind[] = [
  {
    key: "images",
    label: "Images",
    color: "var(--chart-1)",
    icon: Layers,
    verb: "Remove unused images",
    unit: "image",
    definition: "images",
    lineOf: (d) => d.images,
    // What the layers occupy, not the sum of every image's own size: that one
    // counts a shared layer once per image and is always the larger.
    sizeOf: (d) => d.layersSize,
  },
  {
    key: "buildCache",
    label: "Build cache",
    color: "var(--chart-2)",
    icon: Wrench,
    verb: "Empty the build cache",
    unit: "entry",
    many: "entries",
    definition: "buildCache",
    lineOf: (d) => d.buildCache,
    sizeOf: (d) => d.buildCacheSize,
  },
  {
    key: "containers",
    label: "Containers",
    color: "var(--chart-4)",
    icon: Box,
    verb: "Remove stopped containers",
    unit: "container",
    definition: "writableLayer",
    lineOf: (d) => d.containers,
    sizeOf: (d) => d.containersSize,
  },
  {
    key: "volumes",
    label: "Volumes",
    color: "var(--chart-5)",
    icon: Database,
    unit: "volume",
    definition: "volumes",
    lineOf: (d) => d.volumes,
    sizeOf: (d) => d.volumesSize,
  },
]

function DiskShare({
  usage,
  loading,
  error,
  confirm,
  onPruned,
}: {
  usage?: DockerDiskUsage
  loading: boolean
  error?: Error
  confirm: ConfirmFn
  onPruned: () => void
}) {
  const { can } = useAuth()
  const kinds = usage
    ? KINDS.map((kind) => {
        const line = kind.lineOf(usage)
        const size = Math.max(kind.sizeOf(usage), 0)
        return { ...kind, line, size, reclaimable: Math.min(line.reclaimable, size) }
      })
    : []
  const total = kinds.reduce((sum, k) => sum + k.size, 0)
  const safe = usage ? usage.images.reclaimable + usage.buildCache.reclaimable : 0

  const report = (reports: PruneReport[]) => {
    const { reclaimed, message, failed } = pruneSummary(reports)
    if (failed.length && reclaimed === 0) notify.error(message)
    else if (failed.length) notify.warning(message)
    else notify.success(message)
    onPruned()
    return "reported" as const
  }

  const reclaim = (key: string) => {
    if (!usage || !can("destructive")) return undefined
    if (key === "images" && usage.images.reclaimable > 0) {
      return () =>
        confirm({
          title: "Remove unused images",
          confirmLabel: "Remove",
          description: (
            <>
              <p>
                Removes every image no container is using —{" "}
                {usage.images.total - usage.images.active} of {usage.images.total}, freeing{" "}
                {bytes(usage.images.reclaimable)}.
              </p>
              <p>
                This is wider than <Term name="dangling">pruning untagged images</Term>: it reaches
                tagged ones too. Each comes back with a pull, and an image a container uses is never
                removed. Containers, networks and the build cache are left alone.
              </p>
            </>
          ),
          workingLabel: "Removing unused images…",
          action: async () => report([await pruneImages()]),
        })
    }
    if (key === "buildCache" && usage.buildCache.reclaimable > 0) {
      return () =>
        confirm({
          title: "Empty the build cache",
          confirmLabel: "Empty it",
          description: (
            <>
              <p>
                Frees {bytes(usage.buildCache.reclaimable)} across{" "}
                {plural(usage.buildCache.total, "entry", "entries")}.
              </p>
              <p>
                A cache holds nothing you cannot regenerate: the next build of each image starts
                from scratch and takes longer. No image, container or volume is touched.
              </p>
            </>
          ),
          workingLabel: "Reclaiming build cache…",
          action: async () => report([await pruneBuildCache()]),
        })
    }
    const stopped = usage.containers.total - usage.containers.active
    if (key === "containers" && stopped > 0) {
      return () =>
        confirm({
          title: "Remove stopped containers",
          confirmLabel: "Remove",
          description: (
            <p>
              Removes the {plural(stopped, "container")} that are not running, and whatever they had
              written to their own writable layer. Their images and volumes stay.
            </p>
          ),
          workingLabel: "Removing stopped containers…",
          action: async () => report([await pruneContainers()]),
        })
    }
    return undefined
  }

  return (
    <BarReading
      title="On disk"
      label="Docker on disk"
      list="What Docker holds"
      fixed
      figure={
        total > 0 &&
        usage && (
          <span className="flex h-7 items-center gap-2">
            <span className="numeric text-body font-medium">
              <LiveBytes value={total} />
            </span>
            {can("destructive") && safe > 0 && (
              <Button
                size="xs"
                variant="outline"
                onClick={() =>
                  confirm({
                    title: `Reclaim ${bytes(safe)}`,
                    confirmLabel: "Reclaim",
                    description: (
                      <>
                        <p>
                          Removes every image no container is using (
                          {bytes(usage.images.reclaimable)}) and the whole{" "}
                          <Term name="build cache">build cache</Term> (
                          {bytes(usage.buildCache.reclaimable)}), along with stopped containers and
                          unused networks.
                        </p>
                        <p>
                          Nothing a running container needs is touched, and <b>no volume is</b> —
                          the images come back from their registries and the cache rebuilds itself,
                          more slowly, on the next build.
                        </p>
                      </>
                    ),
                    workingLabel: "Reclaiming Docker space…",
                    action: async () => report(await prune(RECLAIM_SAFE)),
                  })
                }
              >
                <Trash />
                Reclaim {bytes(safe)}
              </Button>
            )}
          </span>
        )
      }
      message={
        usage && total > 0 ? undefined : error && !usage ? (
          "Docker's disk usage could not be read."
        ) : loading && !usage ? (
          <TextShimmer>Reading what Docker holds…</TextShimmer>
        ) : (
          "Docker reports nothing on disk."
        )
      }
      parts={kinds.flatMap((kind): ReadingPart[] => {
        const onReclaim = reclaim(kind.key)
        // A kind holding nothing has no span to draw, unless there is still
        // something of it to remove: a stopped container that wrote nothing.
        if (kind.size === 0 && !onReclaim) return []
        const held =
          kind.reclaimable > 0 ? `${bytes(kind.reclaimable)} reclaimable` : "nothing to reclaim"
        return [
          {
            key: kind.key,
            value: kind.size,
            color: kind.color,
            hatch: kind.size > 0 ? kind.reclaimable / kind.size : 0,
            mark: <KindMark kind={kind} onReclaim={onReclaim} />,
            markIsControl: true,
            name: (
              <KindName
                label={kind.label}
                definition={usage?.definitions?.find((d) => d.key === kind.definition)}
              />
            ),
            detail:
              plural(kind.line.total, kind.unit, kind.many) +
              (kind.line.total > 0 ? ` · ${kind.line.active} in use` : ""),
            figure: <LiveBytes value={kind.size} />,
            label: `${kind.label} ${bytes(kind.size)}, ${bytes(kind.reclaimable)} reclaimable`,
            // The reason two honest figures on this page look like a
            // contradiction: every image's own size, added up, counts a shared
            // layer once per image.
            title:
              `${kind.label}: ${bytes(kind.size)}, ${held}. Hatched is what a prune gives back.` +
              (kind.key === "images" && usage && usage.sharedLayers > 0
                ? ` ${bytes(usage.sharedLayers)} of layers are shared between images and stored once.`
                : ""),
            // No control on Volumes: a volume is the data rather than a copy of
            // something fetchable, so it is reclaimed one at a time from the
            // Volumes page, where what each one holds can be seen first.
            control: onReclaim ? (
              <Button size="xs" variant="outline" onClick={onReclaim}>
                {kind.key === "containers" ? "Remove" : "Reclaim"}
              </Button>
            ) : kind.key === "volumes" ? (
              <span className="text-hint text-muted-foreground">holds data</span>
            ) : undefined,
          },
        ]
      })}
    />
  )
}

/**
 * A kind's glyph in the strip, as a button of its own. A part without a press
 * cannot take focus, and a press would make the whole line a prune; and the
 * line's control comes before the strip, so a keyboard moving along the marks
 * never reaches the Reclaim of the kind it has just read. Here focus reads the
 * kind and a press does what that Reclaim does, behind the same confirmation.
 */
function KindMark({ kind, onReclaim }: { kind: Kind; onReclaim?: () => void }) {
  const Icon = kind.icon
  return (
    <button
      type="button"
      aria-label={onReclaim ? kind.verb : kind.label}
      onClick={onReclaim}
      className={cn(
        "absolute inset-0 flex items-center justify-center rounded-md pb-0.5 focus-ring-inset",
        !onReclaim && "cursor-default",
      )}
    >
      <Icon aria-hidden className="size-3.5 text-muted-foreground" />
    </button>
  )
}

function KindName({ label, definition }: { label: string; definition?: DiskDefinition }) {
  if (!definition) return label
  return (
    <HoverCard openDelay={200}>
      <HoverCardTrigger asChild>
        <button
          type="button"
          className="cursor-help rounded-sm font-medium underline decoration-muted-foreground/40 decoration-dotted underline-offset-4 focus-ring"
        >
          {label}
        </button>
      </HoverCardTrigger>
      <HoverCardContent className="w-80 space-y-1 text-xs leading-relaxed">
        <p className="text-body font-medium">{definition.label}</p>
        <p className="text-muted-foreground">{definition.measures}</p>
        {definition.excludes && (
          <p className="text-muted-foreground">
            <span className="font-medium">Not counted: </span>
            {definition.excludes}
          </p>
        )}
        <p className="text-micro text-muted-foreground">Read from {definition.source}.</p>
      </HoverCardContent>
    </HoverCard>
  )
}

/* -------------------------------------------------------------- registry -- */

/** Why a part reads as it does, where that is not its own name. */
const FRESHNESS_TITLE: Partial<Record<Freshness, string>> = {
  outdated:
    "Pulling moves this server to the new copy; the containers keep the old one until they are recreated.",
  unused:
    "Only images a container runs are checked — one registry request per tag, kept half an hour.",
}

/**
 * What the registries say about every image, as one bar of the images and a
 * part per answer. The tags in use are checked; an image nothing runs is not,
 * and is counted as such rather than as a question nobody asked. A press
 * narrows the table to its images, drawn as the products they are.
 */
function RegistryShare({
  images,
  loading,
  failed,
  states,
  checkedAt,
  checking,
  error,
  onCheck,
  selected,
  onSelect,
}: {
  images: DockerImage[]
  loading: boolean
  failed: boolean
  states: Map<string, Freshness>
  checkedAt?: string
  checking: boolean
  error?: Error
  onCheck: () => void
  selected: Freshness | ""
  onSelect: (state: Freshness | "") => void
}) {
  const groups = FRESHNESS_ORDER.map((state) => ({
    state,
    images: images.filter((i) => states.get(i.id) === state),
  })).filter((g) => g.images.length > 0)

  return (
    <BarReading
      title="Registry"
      label="What the registries say"
      list="Registry answers"
      fixed
      working={checking}
      figure={
        <span className="flex h-7 items-center gap-2 text-hint text-muted-foreground">
          {checking ? (
            <TextShimmer>Asking the registries…</TextShimmer>
          ) : (
            checkedAt && <span className="truncate">checked {relativeTime(checkedAt)}</span>
          )}
          <Button size="xs" variant="ghost" disabled={checking} onClick={onCheck}>
            <RefreshClockwise
              className={cn(checking && "animate-spin motion-reduce:animate-none")}
            />
            Check now
          </Button>
        </span>
      }
      message={
        failed ? (
          "Docker's images could not be read."
        ) : loading ? (
          <TextShimmer>Reading the images…</TextShimmer>
        ) : error && !states.size ? (
          <>
            The registries could not be asked.{" "}
            <Button size="xs" variant="outline" onClick={onCheck}>
              Try again
            </Button>
          </>
        ) : images.length === 0 ? (
          "No images to ask about."
        ) : undefined
      }
      parts={groups.map((g) => {
        const label = FRESHNESS_LABEL[g.state]
        const pressed = selected === g.state
        const products = imageProducts(
          g.images.map((i) => primaryTag(i)).filter((t): t is string => !!t),
        )
        return {
          key: g.state,
          value: g.images.length,
          color: FRESHNESS_COLOR[g.state],
          // Its count: one product's logo cannot stand for a whole answer.
          mark: <span className="numeric text-hint font-medium">{g.images.length}</span>,
          name: <span className={cn(g.state === "outdated" && "text-warning")}>{label}</span>,
          detail: <ProductGlyphs ids={products} />,
          figure: g.images.length,
          label: `${label} ${g.images.length}`,
          title: FRESHNESS_TITLE[g.state],
          press: {
            label: `Only images that are ${label.toLowerCase()}`,
            pressed,
            onPress: () => onSelect(pressed ? "" : g.state),
          },
        }
      })}
    />
  )
}
