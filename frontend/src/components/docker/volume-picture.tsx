"use client"

import { Archive, Box, Database } from "@/components/icons"
import { bytes, relativeTime } from "@/lib/format"
import type { BackupJob, VolumeDetail, VolumeUser } from "@/lib/types"
import { cn } from "@/lib/utils"
import { DestinationGlyph, destinations } from "@/components/backups/marks"
import { WireMark, WirePlaceholder } from "@/components/deploy/wire"
import {
  VOLUME_HUE,
  holderHue,
  holderOf,
  shortName,
  sizeReading,
  standing,
  volumeProduct,
  type Backup,
} from "@/components/docker/volumes"
import { ProductGlyph, hasProductLogo } from "@/components/product-logo"
import { Skeleton } from "@/components/ui/skeleton"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { WireFlow, type WireFlowNode } from "@/components/wire-flow"

/**
 * More containers or jobs than this are two and a count. Three rows is what
 * the hub's cell holds open, so the picture is one height whatever a volume
 * has on either side, and nothing under it moves when it lands.
 */
const DRAWN = 2

const UP = new Set(["running", "restarting", "paused"])

/**
 * A volume between who mounts it and where it is backed up, in the wiring
 * vocabulary every picture in the product speaks (`WireFlow`).
 *
 * Left, the containers that mount it, each as the product it runs with the
 * path it sees the volume at, a link to that container; a stopped one's line
 * is dashed. A volume nothing mounts has a dashed ring there saying whose it
 * was. Right, the backup jobs that cover it, each as the place its archives
 * go; a paused job's line is dashed, a failed one's red, and a line carries a
 * pulse only while that job is running. A volume no job covers has a ring
 * there too, which is the way to the Backups form for whoever may write one.
 *
 * On a phone the marks stand in rows over and under the volume, the lines
 * running down between them: the Mounts and Backups views name them again.
 */
export function VolumePicture({
  volume,
  productOf,
  coverage,
  backup,
  jobs,
  stacks,
  onTab,
  onBackUp,
}: {
  /** Undefined until it is read: the picture is drawn waiting, at its final height. */
  volume?: VolumeDetail
  productOf: Map<string, string>
  /** Whether the Backups page has answered what covers this volume. */
  coverage: "reading" | "unreadable" | "read"
  backup?: Backup
  /** The jobs, for where each one writes; a job not found here is drawn by name alone. */
  jobs?: BackupJob[]
  /** The stacks this dashboard can open, so a stack named here is a link only when it exists. */
  stacks: Set<string>
  onTab: (tab: string) => void
  /** Opens the Backups form on this volume; undefined when the reader may not write a job. */
  onBackUp?: () => void
}) {
  // Drawn waiting with the nodes it will most often have, one on each side,
  // rather than through `WireFlow`'s own `loading`: that holds the wide
  // picture's height, and on a phone gives the hub one line of words where
  // the volume has two, so the views under it moved when the volume landed.
  if (!volume)
    return (
      <WireFlow
        phone="marks"
        className="py-0"
        start={{ title: "Mounted by", nodes: [waiting("start")] }}
        end={{ title: "Backed up by", nodes: [waiting("end")] }}
        hub={{
          mark: <HubMark />,
          eyebrow: "Volume",
          title: <Waiting className="w-28" />,
          hint: (
            <span className="block">
              <Waiting className="w-36" />
            </span>
          ),
          className: HUB_ROOM,
        }}
      />
    )

  const size = sizeReading(volume)
  const product = volumeProduct(volume, productOf)
  return (
    <WireFlow
      phone="marks"
      className="animate-rise py-0"
      start={{
        title: "Mounted by",
        nodes:
          volume.usedBy.length > 0
            ? volume.usedBy.map((user) => mounter(user, productOf))
            : [unmounted(volume, stacks)],
        max: DRAWN,
        more: {
          onPress: () => onTab("mounts"),
          words: (hidden) => `${hidden} more containers`,
        },
      }}
      end={{
        title: "Backed up by",
        nodes: copies(volume, coverage, backup, jobs, onBackUp),
        max: DRAWN,
        more: { onPress: () => onTab("backups"), words: (hidden) => `${hidden} more jobs` },
      }}
      hub={{
        mark: <HubMark product={product} />,
        eyebrow: "Volume",
        title: (
          <span className="block truncate font-mono" title={volume.name}>
            {shortName(volume)}
          </span>
        ),
        hint: (
          <span className="numeric block truncate">
            {size.bytes !== undefined ? bytes(size.bytes) : size.word}
            {" · "}
            {volume.driver === "local"
              ? volume.mountType
                ? `${volume.mountType} mount`
                : "on this server"
              : `kept by ${volume.driver}`}
          </span>
        ),
        className: HUB_ROOM,
      }}
    />
  )
}

/**
 * Room for three rows beside the hub and for its words under it. The picture's
 * own padding is dropped for it: the hub's cell already stands clear of the
 * identity line above and the views below.
 */
const HUB_ROOM = "lg:py-16"

/** The volume as the product whose data it holds, else a disk in the Volumes hue. */
function HubMark({ product }: { product?: string }) {
  if (hasProductLogo(product))
    return (
      <WireMark tone="logo" size="lg">
        <ProductGlyph id={product} />
      </WireMark>
    )
  return (
    <WireMark hue={VOLUME_HUE} size="lg">
      <Database aria-hidden />
    </WireMark>
  )
}

function mounter(user: VolumeUser, productOf: Map<string, string>): WireFlowNode {
  const product = productOf.get(user.id)
  const up = UP.has(user.state)
  return {
    id: `c:${user.id}:${user.destination}`,
    mark: (size) => (
      <WireMark tone="logo" size={size}>
        {hasProductLogo(product) ? <ProductGlyph id={product} /> : <Box aria-hidden />}
      </WireMark>
    ),
    // What the path under the name cannot say goes here, so the path is whole.
    eyebrow: (
      <span className="block truncate">
        {user.stack ? (
          <span style={{ color: holderHue({ kind: "stack", name: user.stack }) }}>
            {user.stack}
          </span>
        ) : (
          "Container"
        )}
        {!up && " · stopped"}
        {user.readOnly && " · read-only"}
      </span>
    ),
    title: (
      <span className="block truncate" title={user.name}>
        {user.name}
      </span>
    ),
    name: `${user.name}, ${user.state}, at ${user.destination}`,
    label: `Open ${user.name}`,
    hint: (
      <span className="block truncate font-mono" title={user.destination}>
        {user.destination}
      </span>
    ),
    href: `/docker/containers/${user.id}`,
    // A mount is a hold, not traffic: the line stands, and is dashed for a
    // container that is not up, as the container's own picture draws it.
    line: { state: up ? "up" : "down" },
  }
}

/** The ring where a container would be, saying whose data this was. */
function unmounted(volume: VolumeDetail, stacks: Set<string>): WireFlowNode {
  const s = standing(volume)
  const holder = holderOf(volume)
  const open = s === "down" && stacks.has(holder.name)
  return {
    id: "nothing",
    placeholder: true,
    mark: (size) => (
      <WirePlaceholder size={size}>
        <Box aria-hidden />
      </WirePlaceholder>
    ),
    eyebrow:
      s === "down" ? (
        <span style={{ color: holderHue(holder) }}>{holder.name}</span>
      ) : s === "anonymous" ? (
        "Anonymous"
      ) : (
        "Not mounted"
      ),
    title: <span className="text-muted-foreground">Nothing mounts it</span>,
    name: "Nothing mounts it",
    hint:
      s === "down"
        ? "the stack was taken down"
        : s === "anonymous"
          ? "its container was removed"
          : "no container has it",
    href: open ? `/docker/stacks/${encodeURIComponent(holder.name)}` : undefined,
    label: open ? `Open ${holder.name}` : undefined,
    line: { state: "idle" },
  }
}

/** The jobs that cover it, or the ring that says none does. */
function copies(
  volume: VolumeDetail,
  coverage: "reading" | "unreadable" | "read",
  backup: Backup | undefined,
  jobs: BackupJob[] | undefined,
  onBackUp: (() => void) | undefined,
): WireFlowNode[] {
  if (coverage !== "read" || !backup) {
    return [
      {
        id: "backup:unknown",
        placeholder: true,
        mark: (size) => (
          <WirePlaceholder size={size}>
            <Archive aria-hidden />
          </WirePlaceholder>
        ),
        eyebrow: "Backup",
        title:
          coverage === "reading" ? (
            <TextShimmer>Reading the backups…</TextShimmer>
          ) : (
            <span className="text-muted-foreground">Coverage not readable</span>
          ),
        name: coverage === "reading" ? "Reading the backups" : "Backup coverage not readable",
        line: { state: "idle" },
      },
    ]
  }
  if (backup.jobs.length === 0) {
    return [
      {
        id: "backup:none",
        placeholder: true,
        mark: (size) =>
          onBackUp ? (
            <WirePlaceholder size={size} fallback={Archive} />
          ) : (
            <WirePlaceholder size={size}>
              <Archive aria-hidden />
            </WirePlaceholder>
          ),
        eyebrow: "Backup",
        title: <span className="text-muted-foreground">No job covers it</span>,
        name: "No backup job covers it",
        // Only a plain local volume is known to be on this disk and nowhere else.
        hint:
          volume.driver === "local" && !volume.mountType
            ? "it exists only on this disk"
            : "nothing here copies it",
        onPress: onBackUp,
        label: onBackUp ? "Back up…" : undefined,
        line: { state: "idle" },
      },
    ]
  }
  return backup.jobs.map((covering) => {
    const job = jobs?.find((j) => j.id === covering.id)
    const destination = job ? destinations([job])[0] : undefined
    const paused = job ? !job.enabled : !covering.enabled
    const failed = job?.lastRun?.status === "failed"
    const running = job?.lastRun?.status === "running"
    const last = job?.lastSuccessAt ?? backup.lastAt
    return {
      id: `b:${covering.id}`,
      mark: (size) => (
        <WireMark tone={failed && !paused ? "danger" : "logo"} size={size}>
          {destination ? <DestinationGlyph destination={destination} /> : <Archive aria-hidden />}
        </WireMark>
      ),
      eyebrow: (
        <span className="block truncate">{destination ? destination.service : "Backup job"}</span>
      ),
      title: (
        <span className="block truncate" title={covering.name}>
          {covering.name}
        </span>
      ),
      name: `${covering.name}, ${paused ? "paused" : last ? `backed up ${relativeTime(last)}` : "never ran"}`,
      label: `Open ${covering.name}`,
      hint: (
        <span className="block truncate">
          {paused ? (
            <span className="text-warning">paused</span>
          ) : running ? (
            <TextShimmer>backing up…</TextShimmer>
          ) : failed ? (
            <span className="text-destructive">last run failed</span>
          ) : last ? (
            <span className="text-success">backed up {relativeTime(last)}</span>
          ) : (
            "never ran"
          )}
        </span>
      ),
      href: `/backups/${covering.id}`,
      line: paused
        ? { state: "idle" }
        : failed
          ? { state: "danger" }
          : { state: "up", moving: running },
    } satisfies WireFlowNode
  })
}

const TILE = { lg: "size-14", md: "size-11", sm: "size-9" } as const

/** A grey bar the height of a line's words, so the line is as tall as it will be. */
function Waiting({ className }: { className?: string }) {
  return (
    <span
      data-slot="skeleton"
      className={cn(
        "inline-block h-[0.7em] max-w-full animate-pulse rounded-md bg-muted align-middle",
        className,
      )}
    />
  )
}

/** A node not read yet: a grey tile and three grey lines, on a dashed wire. */
function waiting(id: string): WireFlowNode {
  return {
    id: `waiting:${id}`,
    placeholder: true,
    mark: (size) => <Skeleton className={cn("rounded-xl", TILE[size])} />,
    eyebrow: <Waiting className="w-16" />,
    title: <Waiting className="w-28" />,
    hint: <Waiting className="w-20" />,
    line: { state: "idle" },
  }
}
