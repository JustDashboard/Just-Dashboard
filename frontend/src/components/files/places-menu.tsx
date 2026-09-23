"use client"

import { Servers } from "@/components/icons"
import type { FileBookmark, FilePlace, FilePlaces } from "@/lib/types"
import { useMetrics } from "@/hooks/use-metrics"
import { platformProduct, ProductGlyph } from "@/components/product-logo"
import { FolderIcon } from "@/components/files/file-icon"
import { baseOf, parentOf } from "@/components/files/media"
import { cn } from "@/lib/utils"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"

/**
 * Everywhere the file manager can jump to, as one list.
 *
 * Three kinds of destination, and they are deliberately different things:
 * **places** are what the machine says about itself and come from the server;
 * **starred** is what this operator says about this server, stored there so a
 * phone sees it too; **recent** is the last few minutes at this screen and
 * lives only in the browser. A path is listed once, under the first heading
 * that claims it: a starred place is a place, and a recent starred folder is
 * starred.
 *
 * The sidebar draws this list permanently on a wide screen; on a phone the
 * same list is behind one button in the listing's strip.
 */
export function destinations(places: FilePlaces | undefined, recent: string[]) {
  const listed = new Set<string>()
  const rows = places?.places ?? []
  for (const place of rows) listed.add(place.path)
  const starred = (places?.bookmarks ?? []).filter((b) => !listed.has(b.path))
  for (const b of starred) listed.add(b.path)
  const recentRows = recent.filter((p) => !listed.has(p)).slice(0, 6)
  return { places: rows, starred, recent: recentRows }
}

export function placeName(place: FilePlace): string {
  return place.kind === "home" ? "Home" : place.kind === "user" ? place.name : place.path
}

export function placeHint(place: FilePlace): string | undefined {
  return place.kind === "home" || place.kind === "user" ? place.path : place.hint
}

/**
 * A place, drawn as what it is: a home as a folder with a house pressed into
 * it, `/` as the machine's own distribution (§14: the host is a product too),
 * and every other place as its folder — `/etc` with its gear, `/var/log` with
 * its lines — in the colour it was labelled.
 */
export function PlaceMark({ place, className }: { place: FilePlace; className?: string }) {
  const { host } = useMetrics()
  if (place.kind === "root") {
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
  const name = place.kind === "home" || place.kind === "user" ? "home" : baseOf(place.path)
  return <FolderIcon name={name} path={place.path} className={className} />
}

export function bookmarkName(bookmark: FileBookmark): string {
  return bookmark.name || bookmark.path
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
  const rows = destinations(places, recent)

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>{children}</DropdownMenuTrigger>
      <DropdownMenuContent align={align} className="w-64">
        <DropdownMenuLabel className="eyebrow py-1">Places</DropdownMenuLabel>
        {rows.places.map((place) => (
          <DropdownMenuItem
            key={place.path}
            onSelect={() => onPick(place.path)}
            className="items-start gap-2.5 py-1.5"
          >
            <PlaceMark place={place} className="mt-0.5 size-4" />
            <span className="min-w-0 flex-1">
              <span className="block truncate text-body">{placeName(place)}</span>
              <span className="block truncate font-mono text-hint text-muted-foreground">
                {placeHint(place)}
              </span>
            </span>
            {place.path === current && (
              <span className="mt-1 size-1.5 shrink-0 rounded-full bg-brand" aria-hidden />
            )}
          </DropdownMenuItem>
        ))}
        {rows.starred.length > 0 && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuLabel className="eyebrow py-1">Starred</DropdownMenuLabel>
            {rows.starred.map((bookmark) => (
              <DropdownMenuItem
                key={bookmark.path}
                onSelect={() => onPick(bookmark.path)}
                className="gap-2.5"
              >
                <FolderIcon name={baseOf(bookmark.path)} path={bookmark.path} className="size-4" />
                <span className="min-w-0 flex-1 truncate text-body">{bookmarkName(bookmark)}</span>
              </DropdownMenuItem>
            ))}
          </>
        )}
        {rows.recent.length > 0 && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuLabel className="eyebrow py-1">Recent</DropdownMenuLabel>
            {rows.recent.map((path) => (
              <DropdownMenuItem key={path} onSelect={() => onPick(path)} className="gap-2.5">
                <FolderIcon name={baseOf(path)} path={path} className="size-4" />
                <span className="min-w-0 flex-1 truncate text-body">{baseOf(path)}</span>
                <span className="max-w-[45%] shrink truncate font-mono text-hint text-muted-foreground">
                  {parentOf(path)}
                </span>
              </DropdownMenuItem>
            ))}
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
