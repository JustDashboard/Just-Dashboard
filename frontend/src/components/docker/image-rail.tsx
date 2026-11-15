"use client"

import { useMemo } from "react"
import { get } from "@/lib/api"
import { bytes, plural } from "@/lib/format"
import type { Container, DockerImage, ImageUpdateStatus } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useSessionState, useViewState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { ImageLogo, ReferenceWords } from "@/components/docker/image-marks"
import {
  FRESHNESS_COLOR,
  FRESHNESS_LABEL,
  imageFreshness,
  imageHref,
  primaryTag,
  shortId,
  visibleImages,
  type Freshness,
  type ImageSort,
  type ImageTab,
  type Use,
} from "@/components/docker/images"
import { DockerRail, DockerRailRow } from "@/components/docker/item-rail"
import { StatusDot } from "@/components/status-dot"

const SORT_WORDS: Record<ImageSort, string> = {
  size: "largest first",
  created: "newest first",
  name: "by name",
}

/**
 * The table's list, read for the rail: the search, the chips, the registry
 * filter and the order the table was left on (they are its session's and its
 * view's), at the table's cadence. The reads carry the table's names, so the
 * rail is drawn at once from what the table last read.
 */
export function useImageRail() {
  const [query, setQuery] = useSessionState("docker.images.query", "")
  const [use, setUse] = useViewState<Use>("docker.images.use", "all")
  const [state, setState] = useSessionState<Freshness | "">("docker.images.state", "")
  const [sort] = useViewState<ImageSort>("docker.images.sort", "size")
  const images = usePoll(
    (signal) => get<DockerImage[]>("/docker/images/", undefined, signal),
    30_000,
    [],
    { key: "/docker/images/" },
  )
  // One registry request per tag in use, kept half an hour on the server.
  const updates = usePoll(
    (signal) => get<Record<string, ImageUpdateStatus>>("/docker/images/updates", undefined, signal),
    15 * 60_000,
    [],
    { key: "/docker/images/updates" },
  )
  const containers = usePoll(
    (signal) => get<Container[]>("/docker/containers/", undefined, signal),
    30_000,
    [],
    { key: "/docker/containers/" },
  )
  const list = useMemo(() => images.data ?? [], [images.data])
  const states = useMemo(
    () => new Map(list.map((image) => [image.id, imageFreshness(image, updates.data)] as const)),
    [list, updates.data],
  )
  const rows = useMemo(
    () => visibleImages(list, { query, use, state, sort, states }),
    [list, query, use, state, sort, states],
  )
  // Whether any container made from an image runs, for the dot on its tile.
  const running = useMemo(() => {
    const map = new Map<string, boolean>()
    for (const c of containers.data ?? []) {
      map.set(c.imageId, (map.get(c.imageId) ?? false) || c.state === "running")
    }
    return map
  }, [containers.data])
  const filtered = use !== "all" || state !== ""
  const caption = images.data
    ? `${rows.length < list.length ? `${rows.length} of ${plural(list.length, "image")}` : plural(list.length, "image")} · ${SORT_WORDS[sort]}`
    : images.error
      ? "The images could not be read"
      : "Reading the images…"
  const refreshImages = images.refresh
  const refreshUpdates = updates.refresh
  const refreshContainers = containers.refresh
  return {
    rows,
    /** Every image, whatever the filters leave out of the rows. */
    images: list,
    states,
    running,
    updates: updates.data,
    loading: images.loading && !images.data,
    query,
    setQuery,
    filtered,
    clearFilters: () => {
      setUse("all")
      setState("")
    },
    caption,
    refresh: () => {
      refreshImages()
      refreshUpdates()
      refreshContainers()
    },
  }
}

export type ImageRailState = ReturnType<typeof useImageRail>

/**
 * The table, folded into a column beside the image it opened: the same order,
 * the same filters and the same search, so the next image to look at is one
 * press away rather than a trip back to the table. Each row is the image as
 * the product it is, its registry's answer and its size; the one on screen is
 * filled, and the view on screen goes with the reader to the next.
 */
export function ImageRail({
  rail,
  current,
  currentRow,
  tab,
  className,
}: {
  rail: ImageRailState
  /** The id of the image on screen. */
  current?: string
  /** The image on screen, drawn first when the filters leave it out of the list. */
  currentRow?: Pick<DockerImage, "id" | "repoTags" | "size">
  tab: ImageTab
  className?: string
}) {
  return (
    <DockerRail<Pick<DockerImage, "id" | "repoTags" | "size">>
      slot="image-rail"
      label="Images"
      search={{ label: "Search the images", placeholder: "Name, version or id" }}
      rows={rail.rows}
      keyOf={(image) => image.id}
      current={(image) => image.id === current}
      pinned={currentRow}
      row={(image, on) => (
        <RailRow
          image={image}
          current={on}
          tab={tab}
          state={rail.states.get(image.id)}
          running={rail.running.get(image.id)}
        />
      )}
      loading={rail.loading}
      empty="No image matches."
      query={rail.query}
      onQuery={rail.setQuery}
      filtered={rail.filtered}
      onClearFilters={rail.clearFilters}
      caption={rail.caption}
      className={className}
    />
  )
}

function RailRow({
  image,
  current,
  tab,
  state,
  running,
}: {
  image: Pick<DockerImage, "id" | "repoTags" | "size">
  current: boolean
  tab: ImageTab
  state?: Freshness
  /** A container made from it runs (true), or all of them have stopped (false). */
  running?: boolean
}) {
  const tag = primaryTag(image)
  const coloured = state === "outdated" || state === "pinned" || state === "local"
  return (
    <DockerRailRow
      item={image.id}
      name={tag ?? shortId(image.id)}
      label={tag ?? `untagged ${shortId(image.id)}`}
      current={current}
      href={imageHref(image.id, tab)}
      mark={<ImageLogo image={image} className="size-7 rounded-md [&_img]:size-4" />}
      dot={
        running === undefined ? null : (
          <StatusDot state={running ? "running" : "exited"} className="size-1.5" />
        )
      }
      end={
        <span className="numeric shrink-0 font-mono text-hint text-muted-foreground">
          {bytes(image.size)}
        </span>
      }
    >
      <span
        className={cn(
          "block truncate font-mono text-xs",
          current ? "font-semibold" : "font-medium",
          !tag && "font-sans font-normal text-muted-foreground italic",
        )}
      >
        {tag ? <ReferenceWords reference={tag} /> : "untagged"}
      </span>
      <span
        className="block truncate text-hint text-muted-foreground"
        style={state && coloured ? { color: FRESHNESS_COLOR[state] } : undefined}
      >
        {!tag ? shortId(image.id) : state ? FRESHNESS_LABEL[state].toLowerCase() : " "}
      </span>
    </DockerRailRow>
  )
}
