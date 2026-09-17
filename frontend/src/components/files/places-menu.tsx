"use client"

import { Clock, FolderClosed, Home, Servers, Star } from "@/components/icons"
import { truncateMiddle } from "@/lib/format"
import type { FilePlaces } from "@/lib/types"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"

/**
 * Everywhere the file manager can jump to, behind one button.
 *
 * The rail used to list all of this permanently — home, the configured roots,
 * every account under /home, /etc, /var/www, /var/log, /opt, /srv, /usr/local,
 * /tmp — above a tree that then started at "/". Twelve rows of system
 * directories over a second scrolling region, on a page whose whole subject is
 * usually the one folder the operator's own work lives in. The system is
 * still one click away; it is just no longer the furniture.
 *
 * Three kinds of destination, and they are deliberately different things:
 * **places** are what the machine says about itself and come from the server;
 * **starred** is what this operator says about this server, stored there so a
 * phone sees it too; **recent** is the last few minutes at this screen and
 * lives only in the browser.
 */
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
  const listed = new Set<string>()
  const rows = places?.places ?? []
  for (const place of rows) listed.add(place.path)
  const starred = (places?.bookmarks ?? []).filter((b) => !listed.has(b.path))
  for (const b of starred) listed.add(b.path)
  const recentRows = recent.filter((p) => !listed.has(p)).slice(0, 6)

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>{children}</DropdownMenuTrigger>
      <DropdownMenuContent align={align} className="w-64">
        <DropdownMenuLabel className="eyebrow py-1">Places</DropdownMenuLabel>
        {rows.map((place) => (
          <DropdownMenuItem
            key={place.path}
            onSelect={() => onPick(place.path)}
            className="items-start gap-2.5 py-1.5"
          >
            {place.kind === "home" ? (
              <Home className="mt-0.5 size-3.5 text-brand" />
            ) : place.kind === "root" ? (
              <Servers className="mt-0.5 size-3.5" />
            ) : (
              <FolderClosed className="mt-0.5 size-3.5" />
            )}
            <span className="min-w-0 flex-1">
              <span className="block truncate text-body">
                {place.kind === "home" ? "Home" : place.kind === "user" ? place.name : place.path}
              </span>
              <span className="block truncate font-mono text-hint text-muted-foreground">
                {place.kind === "home" || place.kind === "user" ? place.path : place.hint}
              </span>
            </span>
            {place.path === current && (
              <span className="mt-1 size-1.5 shrink-0 rounded-full bg-brand" aria-hidden />
            )}
          </DropdownMenuItem>
        ))}
        {starred.length > 0 && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuLabel className="eyebrow py-1">Starred</DropdownMenuLabel>
            {starred.map((bookmark) => (
              <DropdownMenuItem
                key={bookmark.path}
                onSelect={() => onPick(bookmark.path)}
                className="gap-2.5"
              >
                <Star className="size-3.5 text-warning" />
                <span className="min-w-0 flex-1 truncate text-body">
                  {bookmark.name || bookmark.path}
                </span>
              </DropdownMenuItem>
            ))}
          </>
        )}
        {recentRows.length > 0 && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuLabel className="eyebrow py-1">Recent</DropdownMenuLabel>
            {recentRows.map((path) => (
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
