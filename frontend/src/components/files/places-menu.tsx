"use client"

import { Clock, FolderClosed, Home, Servers, Star } from "@/components/icons"
import { truncateMiddle } from "@/lib/format"
import type { FileBookmark, FilePlace, FilePlaces } from "@/lib/types"
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

export function PlaceIcon({ place, className }: { place: FilePlace; className?: string }) {
  if (place.kind === "home") return <Home className={className} />
  if (place.kind === "root") return <Servers className={className} />
  return <FolderClosed className={className} />
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
            <PlaceIcon
              place={place}
              className={place.kind === "home" ? "mt-0.5 size-3.5 text-brand" : "mt-0.5 size-3.5"}
            />
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
                <Star className="size-3.5 text-warning" />
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
                <Clock className="size-3.5" />
                <span className="min-w-0 flex-1 truncate font-mono text-xs">
                  {truncateMiddle(path, 34)}
                </span>
              </DropdownMenuItem>
            ))}
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
