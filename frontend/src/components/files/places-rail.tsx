"use client"

import { Clock, Cross, Home, Servers, Star, StarFill } from "@/components/icons"
import { cn } from "@/lib/utils"
import { truncateMiddle } from "@/lib/format"
import type { FileBookmark, FilePlaces } from "@/lib/types"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { FileIcon } from "@/components/files/file-icon"
import { rowReveal } from "@/components/icon-action"

const baseOf = (p: string) => p.replace(/\/+$/, "").split("/").pop() || "/"

/**
 * The shortcuts down the left-hand side.
 *
 * A file manager that opens at "/" and offers no way back but the breadcrumb
 * makes you walk the same three directories every visit. Three kinds of
 * shortcut earn their space, and they are deliberately different things:
 *
 *  - **Places** are what the machine says about itself — home, the configured
 *    roots, the account directories, and the handful of paths a server keeps
 *    its work in. They come from the server because it is the side that can
 *    check whether /var/www exists and is reachable.
 *  - **Starred** is what this operator says about *this* server, and it is
 *    stored on the server for that reason: which directory matters is a fact
 *    about the box, and it should be there from another browser.
 *  - **Recent** is what you did in the last few minutes, and it is the only
 *    one kept in the browser — it is a property of this sitting, and syncing
 *    it would mean a phone reordering a laptop's list.
 */
export function PlacesRail({
  places,
  path,
  recent,
  canWrite,
  onNavigate,
  onBookmarksChange,
  className,
}: {
  places?: FilePlaces
  path: string
  recent: string[]
  canWrite: boolean
  onNavigate: (path: string) => void
  onBookmarksChange: (next: FileBookmark[]) => void
  className?: string
}) {
  const bookmarks = places?.bookmarks ?? []
  const starred = bookmarks.some((b) => b.path === path)

  const toggleStar = () => {
    onBookmarksChange(
      starred
        ? bookmarks.filter((b) => b.path !== path)
        : [...bookmarks, { path, name: baseOf(path) }],
    )
  }

  return (
    <nav className={cn("flex min-h-0 flex-col gap-3 overflow-y-auto p-2", className)}>
      <Group label="Places">
        {places?.places.map((place) => (
          <Row
            key={place.path}
            active={place.path === path}
            title={place.hint ? `${place.path} — ${place.hint}` : place.path}
            onClick={() => onNavigate(place.path)}
            icon={
              place.kind === "home" ? (
                <Home className="size-3.5 text-primary" />
              ) : place.kind === "root" ? (
                <Servers className="size-3.5 text-muted-foreground" />
              ) : (
                <FileIcon
                  entry={{ name: baseOf(place.path), isDir: true, isSymlink: false } as never}
                  className="size-3.5"
                />
              )
            }
          >
            {place.kind === "notable" ? place.name : baseOf(place.path) || place.name}
          </Row>
        ))}
      </Group>

      <Group
        label="Starred"
        action={
          canWrite && (
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  size="icon-xs"
                  variant="ghost"
                  className={cn("text-muted-foreground", starred && "text-warning")}
                  onClick={toggleStar}
                  aria-label={starred ? "Remove this folder from Starred" : "Star this folder"}
                >
                  {starred ? <StarFill /> : <Star />}
                </Button>
              </TooltipTrigger>
              <TooltipContent>
                {starred ? "Remove this folder from Starred" : "Star this folder"}
              </TooltipContent>
            </Tooltip>
          )
        }
      >
        {bookmarks.length === 0 && (
          <p className="px-2 py-1 text-hint leading-snug text-muted-foreground">
            Star a folder to keep it here — on this server, for every browser.
          </p>
        )}
        {bookmarks.map((bookmark) => (
          <Row
            key={bookmark.path}
            active={bookmark.path === path}
            title={bookmark.path}
            onClick={() => onNavigate(bookmark.path)}
            icon={<Star className="size-3.5 fill-warning/30 text-warning" />}
            trailing={
              canWrite && (
                <Button
                  size="icon-xs"
                  variant="ghost"
                  className={rowReveal("row")}
                  aria-label={`Remove ${bookmark.name ?? bookmark.path}`}
                  onClick={(e) => {
                    e.stopPropagation()
                    onBookmarksChange(bookmarks.filter((b) => b.path !== bookmark.path))
                  }}
                >
                  <Cross />
                </Button>
              )
            }
          >
            {bookmark.name || baseOf(bookmark.path)}
          </Row>
        ))}
      </Group>

      {recent.length > 0 && (
        <Group label="Recent">
          {recent.map((entry) => (
            <Row
              key={entry}
              active={entry === path}
              title={entry}
              onClick={() => onNavigate(entry)}
              icon={<Clock className="size-3.5 text-muted-foreground" />}
            >
              {truncateMiddle(entry.replace(/^\/+/, ""), 28)}
            </Row>
          ))}
        </Group>
      )}
    </nav>
  )
}

function Group({
  label,
  action,
  children,
}: {
  label: string
  action?: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <div className="space-y-0.5">
      <div className="flex items-center justify-between gap-2 px-2">
        <p className="eyebrow">{label}</p>
        {action}
      </div>
      {children}
    </div>
  )
}

function Row({
  active,
  icon,
  trailing,
  title,
  onClick,
  children,
}: {
  active?: boolean
  icon: React.ReactNode
  trailing?: React.ReactNode
  title?: string
  onClick: () => void
  children: React.ReactNode
}) {
  // The remove control is a sibling of the row rather than a child of it: a
  // <button> inside a <button> is invalid HTML, and React says so as a
  // hydration error on every render of the rail.
  return (
    <div
      className={cn(
        "group/row flex w-full items-center gap-1 rounded-md pr-1 transition-colors",
        // A selected row is a solid `bg-primary`, so every glyph inside it has
        // to come from the row rather than carry its own colour: `--file-icon-colour`
        // reaches FileIcon's inline style, and `text-current` overrides the
        // fixed `text-primary`/`text-muted-foreground` on the place icons.
        active
          ? "bg-primary text-primary-foreground [--file-icon-colour:currentColor] [&_svg]:text-current"
          : "hover:bg-accent hover:text-accent-foreground",
      )}
    >
      <button
        type="button"
        title={title}
        onClick={onClick}
        className="flex min-w-0 flex-1 items-center gap-2 px-2 py-1 text-left text-xs"
      >
        <span className="flex size-3.5 shrink-0 items-center justify-center">{icon}</span>
        <span className="min-w-0 flex-1 truncate">{children}</span>
      </button>
      {trailing}
    </div>
  )
}
