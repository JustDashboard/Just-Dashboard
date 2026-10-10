"use client"

import { Filter, RefreshClockwise, Trash } from "@/components/icons"
import { rowReveal } from "@/components/icon-action"
import { LiveBytes } from "@/components/overview/readings"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductGlyphs, imageProducts } from "@/components/product-logo"
import { ErrorState, LoadingRows } from "@/components/state"
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
 * registries say about the images in use — the Packages band's two questions,
 * asked of Docker.
 *
 * The disk breakdown was four grey lines of text over the image list, each
 * with its own Reclaim, and the one question a reader brings to it — what
 * shape is it, and what can I get back — needed all four read and added up.
 * It is one bar now, a span per kind in the colour the Docker overview draws
 * that kind in, with the part a prune would give back drawn faint inside its
 * own span, so the reclaimable share of each is seen before a figure is read.
 */
export function ImageBand({
  usage,
  usageLoading,
  images,
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
  images: DockerImage[]
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
      <DiskShare usage={usage} loading={usageLoading} confirm={confirm} onPruned={onPruned} />
      <RegistryShare
        images={images}
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
    unit: "container",
    definition: "writableLayer",
    lineOf: (d) => d.containers,
    sizeOf: (d) => d.containersSize,
  },
  {
    key: "volumes",
    label: "Volumes",
    color: "var(--chart-5)",
    unit: "volume",
    definition: "volumes",
    lineOf: (d) => d.volumes,
    sizeOf: (d) => d.volumesSize,
  },
]

/**
 * The part of a span a prune would give back: the kind's own colour, hatched,
 * so it reads as part of that kind rather than as a fifth one beside it.
 */
const hatched = (color: string) =>
  `repeating-linear-gradient(-45deg, ${color} 0 2px, color-mix(in oklab, ${color} 22%, transparent) 2px 5px)`

function DiskShare({
  usage,
  loading,
  confirm,
  onPruned,
}: {
  usage?: DockerDiskUsage
  loading: boolean
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
    else notify.success(message)
    onPruned()
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
          action: async () => report([await pruneContainers()]),
        })
    }
    return undefined
  }

  return (
    <Panel plain aria-label="Docker on disk">
      <PanelHeader
        title="On disk"
        className="min-h-10"
        actions={
          total > 0 && (
            // As tall as the Registry head's button, so the two hairlines meet.
            <span className="numeric flex h-6 items-center text-body font-medium">
              <LiveBytes value={total} />
            </span>
          )
        }
      />
      <PanelBody className="space-y-3 pt-4">
        {loading && !usage ? (
          <LoadingRows rows={4} />
        ) : !usage || total === 0 ? (
          <p className="py-2 text-body text-muted-foreground">Docker reports nothing on disk.</p>
        ) : (
          <>
            <div
              role="img"
              aria-label={`On disk: ${kinds
                .filter((k) => k.size > 0)
                .map((k) => `${k.label} ${bytes(k.size)}, ${bytes(k.reclaimable)} reclaimable`)
                .join("; ")}`}
              className="flex h-3 w-full overflow-hidden rounded-sm bg-meter-track"
            >
              {kinds
                .filter((k) => k.size > 0)
                .map((kind) => (
                  <span
                    key={kind.key}
                    title={`${kind.label}: ${bytes(kind.size)}, ${bytes(kind.reclaimable)} reclaimable`}
                    className="flex h-full shrink-0 border-r border-background transition-[width] duration-700 ease-out last:border-r-0 motion-reduce:transition-none"
                    style={{ width: `${(kind.size / total) * 100}%` }}
                  >
                    <span className="h-full flex-1" style={{ background: kind.color }} />
                    {kind.reclaimable > 0 && (
                      <span
                        className="h-full"
                        style={{
                          width: `${(kind.reclaimable / kind.size) * 100}%`,
                          background: hatched(kind.color),
                        }}
                      />
                    )}
                  </span>
                ))}
            </div>
            <ul className="-mx-2">
              {kinds.map((kind) => (
                <DiskLine
                  key={kind.key}
                  kind={kind}
                  definition={usage.definitions?.find((d) => d.key === kind.definition)}
                  onReclaim={reclaim(kind.key)}
                />
              ))}
            </ul>
            <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2 pt-1">
              {can("destructive") && safe > 0 && (
                <Button
                  size="sm"
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
                            {bytes(usage.buildCache.reclaimable)}), along with stopped containers
                            and unused networks.
                          </p>
                          <p>
                            Nothing a running container needs is touched, and <b>no volume is</b> —
                            the images come back from their registries and the cache rebuilds
                            itself, more slowly, on the next build.
                          </p>
                        </>
                      ),
                      action: async () => report(await prune(RECLAIM_SAFE)),
                    })
                  }
                >
                  <Trash className="size-4" />
                  Reclaim {bytes(safe)}
                </Button>
              )}
              {/* The reason two honest figures on this page look like a
                  contradiction: every image's own size, added up, counts a
                  shared layer once per image. */}
              <p className="min-w-0 text-hint text-muted-foreground">
                Hatched is what a prune gives back.
                {usage.sharedLayers > 0 &&
                  ` ${bytes(usage.sharedLayers)} of layers are shared between images and stored once.`}
              </p>
            </div>
          </>
        )}
      </PanelBody>
    </Panel>
  )
}

function DiskLine({
  kind,
  definition,
  onReclaim,
}: {
  kind: Kind & { line: DockerDiskUsageLine; size: number; reclaimable: number }
  definition?: DiskDefinition
  onReclaim?: () => void
}) {
  const label = definition ? (
    <HoverCard openDelay={200}>
      <HoverCardTrigger asChild>
        <button
          type="button"
          className="cursor-help rounded-sm font-medium underline decoration-muted-foreground/40 decoration-dotted underline-offset-4 focus-ring"
        >
          {kind.label}
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
  ) : (
    <span className="font-medium">{kind.label}</span>
  )
  return (
    <li className="flex min-h-9 min-w-0 items-center gap-2.5 rounded-md px-2 text-body">
      <span
        aria-hidden
        className="h-3 w-0.5 shrink-0 rounded-full"
        style={{ background: kind.color }}
      />
      <span className="shrink-0">{label}</span>
      <span className="numeric min-w-0 truncate text-hint text-muted-foreground">
        {plural(kind.line.total, kind.unit, kind.many)}
        {kind.line.total > 0 && ` · ${kind.line.active} in use`}
      </span>
      <span className="ml-auto flex shrink-0 flex-col items-end leading-tight">
        <span className="numeric font-medium">
          <LiveBytes value={kind.size} />
        </span>
        <span className="numeric text-hint text-muted-foreground">
          {kind.reclaimable > 0 ? `${bytes(kind.reclaimable)} reclaimable` : "nothing to reclaim"}
        </span>
      </span>
      {/* No control on Volumes: a volume is the data rather than a copy of
          something fetchable, so it is reclaimed one at a time from the
          Volumes page, where what each one holds can be seen first. */}
      <span className="flex w-18 shrink-0 justify-end">
        {onReclaim ? (
          <Button size="xs" variant="outline" onClick={onReclaim}>
            {kind.key === "containers" ? "Remove" : "Reclaim"}
          </Button>
        ) : kind.key === "volumes" ? (
          <span className="text-hint text-muted-foreground">holds data</span>
        ) : null}
      </span>
    </li>
  )
}

/* -------------------------------------------------------------- registry -- */

/**
 * What the registries say about every image, as one bar of the images and a
 * line per answer. The tags in use are checked; an image nothing runs is not,
 * and is counted as such rather than as a question nobody asked. A line
 * narrows the table to its images, drawn as the products they are.
 */
function RegistryShare({
  images,
  states,
  checkedAt,
  checking,
  error,
  onCheck,
  selected,
  onSelect,
}: {
  images: DockerImage[]
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
  const total = images.length
  const outdated = groups.find((g) => g.state === "outdated")?.images.length ?? 0

  return (
    <Panel plain aria-label="What the registries say">
      <PanelHeader
        title="Registry"
        className="min-h-10"
        actions={
          <span className="flex items-center gap-2 text-hint text-muted-foreground">
            {checking ? (
              <TextShimmer>Asking the registries…</TextShimmer>
            ) : (
              checkedAt && <span>checked {relativeTime(checkedAt)}</span>
            )}
            <Button size="xs" variant="ghost" disabled={checking} onClick={onCheck}>
              <RefreshClockwise
                className={cn(checking && "animate-spin motion-reduce:animate-none")}
              />
              Check now
            </Button>
          </span>
        }
      />
      <PanelBody className="space-y-3 pt-4">
        {error && !states.size ? (
          <ErrorState error={error} onRetry={onCheck} />
        ) : total === 0 ? (
          <p className="py-2 text-body text-muted-foreground">No images to ask about.</p>
        ) : (
          <>
            <div
              role="img"
              aria-label={`Registry: ${groups
                .map((g) => `${FRESHNESS_LABEL[g.state]} ${g.images.length}`)
                .join(", ")}`}
              className="relative flex h-3 w-full overflow-hidden rounded-sm bg-meter-track"
            >
              {groups.map((g) => (
                <span
                  key={g.state}
                  title={`${FRESHNESS_LABEL[g.state]}: ${g.images.length}`}
                  className="h-full shrink-0 border-r border-background transition-[width] duration-700 ease-out last:border-r-0 motion-reduce:transition-none"
                  style={{
                    width: `${(g.images.length / total) * 100}%`,
                    background: FRESHNESS_COLOR[g.state],
                  }}
                />
              ))}
              {/* A check the reader asked for sweeps the bar, as Health's
                  segment does while its check runs. */}
              {checking && (
                <span
                  aria-hidden
                  className="absolute inset-y-0 left-0 w-1/3 animate-sweep bg-foreground/20"
                />
              )}
            </div>
            <ul className="-mx-2">
              {groups.map((g) => {
                const pressed = selected === g.state
                return (
                  <li key={g.state}>
                    <button
                      type="button"
                      aria-pressed={pressed}
                      aria-label={`Only images that are ${FRESHNESS_LABEL[g.state].toLowerCase()}`}
                      onClick={() => onSelect(pressed ? "" : g.state)}
                      className={cn(
                        "group flex min-h-9 w-full min-w-0 items-center gap-2.5 rounded-md px-2 text-left text-body focus-ring-inset transition-colors",
                        pressed ? "bg-accent" : "hover:bg-row-hover",
                      )}
                    >
                      <span
                        aria-hidden
                        className="h-3 w-0.5 shrink-0 rounded-full"
                        style={{ background: FRESHNESS_COLOR[g.state] }}
                      />
                      <span
                        className={cn(
                          "shrink-0 font-medium",
                          g.state === "outdated" && "text-warning",
                        )}
                      >
                        {FRESHNESS_LABEL[g.state]}
                      </span>
                      <span className="min-w-0 truncate">
                        <ProductGlyphs
                          ids={imageProducts(
                            g.images.map((i) => primaryTag(i)).filter((t): t is string => !!t),
                          )}
                        />
                      </span>
                      <span className="numeric ml-auto shrink-0 font-medium">
                        {g.images.length}
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
              {outdated > 0
                ? `Pulling moves this server to the new ${outdated === 1 ? "copy" : "copies"}; the containers keep the old one until they are recreated.`
                : "Only images a container runs are checked — one registry request per tag, kept half an hour."}
            </p>
          </>
        )}
      </PanelBody>
    </Panel>
  )
}
