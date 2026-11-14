"use client"

import { useEffect, useMemo, useRef } from "react"
import Link from "next/link"
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
import { SearchInput } from "@/components/page"
import { LoadingRows } from "@/components/state"
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
  const listed = rail.rows.some((row) => row.id === current)
  const pinned = !listed && currentRow ? currentRow : undefined
  const list = useRef<HTMLOListElement>(null)
  // The image on screen is kept in view: opened from a link or by Alt+↓ past
  // the column's edge, it would otherwise be off its own list.
  useEffect(() => {
    const row = list.current?.querySelector<HTMLElement>("[aria-current='page']")
    row?.scrollIntoView({ block: "nearest" })
  }, [current, rail.rows.length])

  return (
    <nav
      aria-label="Images"
      data-slot="image-rail"
      className={cn("min-h-0 flex-col border-r border-hairline pr-4", className)}
    >
      <div className="flex shrink-0 flex-col gap-2 pb-3">
        <SearchInput
          value={rail.query}
          onChange={(event) => rail.setQuery(event.target.value)}
          placeholder="Name, version or id"
          aria-label="Search the images"
          containerClassName="sm:w-full"
        />
        <p className="flex min-w-0 items-center justify-between gap-2 text-hint text-muted-foreground">
          <span className="truncate">{rail.caption}</span>
          {rail.filtered && (
            <button
              type="button"
              onClick={rail.clearFilters}
              className="shrink-0 rounded-sm font-medium focus-ring hover:text-foreground"
            >
              Clear filters
            </button>
          )}
        </p>
      </div>
      <ol
        ref={list}
        aria-label="Images to open"
        className="-mx-2 min-h-0 flex-1 [scrollbar-width:thin] space-y-0.5 overflow-y-auto px-2 pb-2"
      >
        {pinned && <RailRow image={pinned} current tab={tab} />}
        {pinned && rail.rows.length > 0 && (
          <li aria-hidden className="mx-3 my-1.5 border-t border-hairline" />
        )}
        {rail.rows.map((row) => (
          <RailRow
            key={row.id}
            image={row}
            current={row.id === current}
            tab={tab}
            state={rail.states.get(row.id)}
            running={rail.running.get(row.id)}
          />
        ))}
        {rail.loading && rail.rows.length === 0 && <LoadingRows rows={8} />}
        {!rail.loading && rail.rows.length === 0 && !pinned && (
          <li className="px-3 py-4 text-hint text-muted-foreground">No image matches.</li>
        )}
      </ol>
    </nav>
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
    <li className="min-w-0">
      <Link
        href={imageHref(image.id, tab)}
        // A rail of a hundred images is not a hundred pages to fetch ahead.
        prefetch={false}
        data-workspace-item={image.id}
        data-workspace-name={tag ?? shortId(image.id)}
        aria-current={current ? "page" : undefined}
        aria-label={tag ?? `untagged ${shortId(image.id)}`}
        className={cn(
          "flex h-12 w-full min-w-0 items-center gap-2.5 rounded-lg px-2 text-left focus-ring-inset transition-colors",
          current ? "bg-accent" : "hover:bg-row-hover",
        )}
      >
        <span className="relative flex shrink-0">
          <ImageLogo image={image} className="size-7 rounded-md [&_img]:size-4" />
          {running !== undefined && (
            <span
              aria-hidden
              className="absolute -right-0.5 -bottom-0.5 flex size-2.5 items-center justify-center rounded-full bg-background"
            >
              <StatusDot state={running ? "running" : "exited"} className="size-1.5" />
            </span>
          )}
        </span>
        <span className="min-w-0 flex-1">
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
            {!tag ? shortId(image.id) : state ? FRESHNESS_LABEL[state].toLowerCase() : " "}
          </span>
        </span>
        <span className="numeric shrink-0 font-mono text-hint text-muted-foreground">
          {bytes(image.size)}
        </span>
      </Link>
    </li>
  )
}
