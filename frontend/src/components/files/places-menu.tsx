"use client"

import { Servers } from "@/components/icons"
import type { FileBookmark, FilePlace, FilePlaces } from "@/lib/types"
import { useMetrics } from "@/hooks/use-metrics"
import { platformProduct, ProductGlyph } from "@/components/product-logo"
import { FolderIcon } from "@/components/files/file-icon"
import { baseOf } from "@/components/files/media"
import { cn } from "@/lib/utils"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"

export type PlaceSectionKey = "home" | "starred" | "system" | "recent"

/** One row of the places list: where it goes, what it is called, how it is drawn. */
export type Destination = {
  path: string
  name: string
  place?: FilePlace
  bookmark?: FileBookmark
}

/**
 * Everywhere the file manager can jump to, as one list in four sections.
 *
 * They are deliberately different things: **home** is the accounts' own
 * folders, where nearly everything an operator made lives; **starred** is what
 * this operator says about this server, stored there so a phone sees it too;
 * **this server** is the rest of what the machine says about itself — the
 * whole disk and the handful of system folders worth a shortcut; **recent** is
 * the last few minutes at this screen and lives only in the browser. A path is
 * listed once, under the first heading that claims it: a starred place is a
 * place, and a recent starred folder is starred.
 *
 * The sidebar draws this list permanently on a wide screen; on a phone the
 * same list is behind one button in the listing's strip.
 */
export function placeSections(
  places: FilePlaces | undefined,
  recent: string[],
): { key: PlaceSectionKey; title: string; rows: Destination[] }[] {
  const listed = new Set<string>()
  const rows = places?.places ?? []
  for (const place of rows) listed.add(place.path)
  const starred = (places?.bookmarks ?? []).filter((b) => !listed.has(b.path))
  for (const b of starred) listed.add(b.path)
  const fromPlace = (place: FilePlace) => ({ path: place.path, name: placeName(place), place })
  return [
    {
      key: "home" as const,
      title: "Home",
      rows: rows.filter((p) => p.kind === "home" || p.kind === "user").map(fromPlace),
    },
    {
      key: "starred" as const,
      title: "Starred",
      rows: starred.map((bookmark) => ({
        path: bookmark.path,
        name: bookmarkName(bookmark),
        bookmark,
      })),
    },
    {
      key: "system" as const,
      title: "This server",
      rows: rows.filter((p) => p.kind === "root" || p.kind === "notable").map(fromPlace),
    },
    {
      key: "recent" as const,
      title: "Recent",
      rows: recent
        .filter((p) => !listed.has(p))
        .slice(0, 6)
        .map((path) => ({ path, name: baseOf(path) || "/" })),
    },
  ].filter((section) => section.rows.length > 0)
}

/**
 * A place by what it holds, never by a caption under it: a home is its
 * account's name, `/` is the file system, and the system folders arrive from
 * the server already named for their contents. The path is the row's tooltip.
 */
export function placeName(place: FilePlace): string {
  return place.path === "/" ? "File system" : place.name
}

/**
 * A destination, drawn as what it is: a home as a folder with a house pressed
 * into it, `/` as the machine's own distribution (§14: the host is a product
 * too), and every other folder — `/etc` with its gear, `/var/log` with its
 * lines — in the colour it was labelled.
 */
export function PlaceMark({
  destination,
  className,
}: {
  destination: Destination
  className?: string
}) {
  const { host } = useMetrics()
  const { place, path } = destination
  if (place?.kind === "root") {
    const product = platformProduct(host?.platform)
    return product ? (
      <span
        aria-hidden
        className={cn("inline-flex shrink-0 items-center justify-center", className)}
      >
        <ProductGlyph id={product} className="size-[85%]" />
      </span>
    ) : (
      <Servers aria-hidden className={cn("text-muted-foreground", className)} />
    )
  }
  const home = place?.kind === "home" || place?.kind === "user"
  return <FolderIcon name={home ? "home" : baseOf(path)} path={path} className={className} />
}

export function bookmarkName(bookmark: FileBookmark): string {
  return bookmark.name || baseOf(bookmark.path) || bookmark.path
}

export function PlacesMenu({
  places,
  recent,
  current,
  onPick,
  align = "start",
  children,
}: {
  places: FilePlaces | undefined
  recent: string[]
  /** The folder being browsed, drawn as the chosen row. */
  current: string
  onPick: (path: string) => void
  align?: "start" | "end"
  children: React.ReactNode
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>{children}</DropdownMenuTrigger>
      <DropdownMenuContent align={align} className="w-64">
        {placeSections(places, recent).map((section, i) => (
          <div key={section.key}>
            {i > 0 && <DropdownMenuSeparator />}
            <DropdownMenuLabel className="eyebrow py-1">{section.title}</DropdownMenuLabel>
            {section.rows.map((row) => (
              <DropdownMenuItem
                key={row.path}
                title={row.path}
                onSelect={() => onPick(row.path)}
                className={cn("gap-2.5", row.path === current && "bg-accent font-medium")}
              >
                <PlaceMark destination={row} className="size-4" />
                <span className="min-w-0 flex-1 truncate text-body">{row.name}</span>
              </DropdownMenuItem>
            ))}
          </div>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
