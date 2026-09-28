"use client"

import { Cross } from "@/components/icons"
import { cn } from "@/lib/utils"
import type { FileBookmark, FilePlaces } from "@/lib/types"
import { LoadingRows } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { rowReveal } from "@/components/icon-action"
import {
  bookmarkName,
  destinations,
  PlaceMark,
  placeHint,
  placeName,
} from "@/components/files/places-menu"
import { FolderIcon } from "@/components/files/file-icon"
import { baseOf, parentOf } from "@/components/files/media"
import { useDropTarget, type DropMode } from "@/components/files/dnd"

/**
 * The column down the left of the file manager: the places to jump to, and
 * nothing that moves.
 *
 * It was a folder tree for a while, rooted at whatever place the browsed
 * folder lived in, and it kept re-drawing itself as the operator walked
 * around — open a folder and the column changed under the pointer. A desktop
 * file manager's sidebar does not do that: it is a fixed list of the drives,
 * the home folder and the pinned places, and *walking* happens in the
 * listing beside it. So this is that list — the server's places, what this
 * operator starred, what was visited lately — drawn once and left alone.
 * The browsed folder is marked when it is one of them.
 *
 * Every row takes a drop: a row dragged from the listing onto one moves
 * there, and a file from the desktop uploads there.
 *
 * It has no header of its own. Its name and its close button were a strip of
 * chrome above a list whose three headings already say what it is, and the
 * toggle that shows and hides it lives in the workbench's strip with the rest
 * of the page's controls. Each row is drawn as what it is — the folder in its
 * colour with what it holds pressed into it, the machine's own distribution
 * for `/` — so the column is found by looking, the way the listing is.
 */
export function FilesSidebar({
  places,
  path,
  recent,
  canWrite,
  onNavigate,
  onBookmarksChange,
  onDropPaths,
  onDropFiles,
  className,
}: {
  places: FilePlaces | undefined
  /** The folder being browsed. */
  path: string
  recent: string[]
  canWrite: boolean
  onNavigate: (path: string) => void
  onBookmarksChange: (next: FileBookmark[]) => void
  onDropPaths: (paths: string[], dir: string, mode: DropMode) => void
  onDropFiles: (transfer: DataTransfer, dir: string) => void
  className?: string
}) {
  const rows = destinations(places, recent)
  const bookmarks = places?.bookmarks ?? []

  return (
    <nav
      aria-label="Places"
      className={cn("flex min-h-0 min-w-0 flex-1 flex-col overflow-auto", className)}
    >
      {places === undefined ? (
        <LoadingRows rows={6} className="p-3" />
      ) : (
        <>
          <div className="py-2">
            <p className="eyebrow px-3 pt-1 pb-1.5">Places</p>
            {rows.places.map((place) => (
              <PlaceRow
                key={place.path}
                dir={place.path}
                active={place.path === path}
                icon={<PlaceMark place={place} className="size-5" />}
                name={placeName(place)}
                hint={placeHint(place)}
                onNavigate={onNavigate}
                onDropPaths={onDropPaths}
                onDropFiles={onDropFiles}
              />
            ))}
          </div>
          {rows.starred.length > 0 && (
            <div className="border-t border-hairline py-2">
              <p className="eyebrow px-3 pt-1 pb-1.5">Starred</p>
              {rows.starred.map((bookmark) => (
                <PlaceRow
                  key={bookmark.path}
                  dir={bookmark.path}
                  active={bookmark.path === path}
                  icon={
                    <FolderIcon
                      name={baseOf(bookmark.path)}
                      path={bookmark.path}
                      className="size-5"
                    />
                  }
                  name={bookmarkName(bookmark)}
                  onNavigate={onNavigate}
                  onDropPaths={onDropPaths}
                  onDropFiles={onDropFiles}
                  trailing={
                    canWrite && (
                      <Tooltip>
                        <TooltipTrigger asChild>
                          <Button
                            size="icon-xs"
                            variant="ghost"
                            className={cn("text-muted-foreground", rowReveal("row"))}
                            aria-label={`Unstar ${bookmarkName(bookmark)}`}
                            onClick={(e) => {
                              e.stopPropagation()
                              onBookmarksChange(bookmarks.filter((b) => b.path !== bookmark.path))
                            }}
                          >
                            <Cross />
                          </Button>
                        </TooltipTrigger>
                        <TooltipContent>Unstar</TooltipContent>
                      </Tooltip>
                    )
                  }
                />
              ))}
            </div>
          )}
          {rows.recent.length > 0 && (
            <div className="border-t border-hairline py-2">
              <p className="eyebrow px-3 pt-1 pb-1.5">Recent</p>
              {rows.recent.map((p) => (
                <PlaceRow
                  key={p}
                  dir={p}
                  active={p === path}
                  icon={<FolderIcon name={baseOf(p)} path={p} className="size-5" />}
                  name={baseOf(p) || "/"}
                  hint={p === "/" ? undefined : parentOf(p)}
                  onNavigate={onNavigate}
                  onDropPaths={onDropPaths}
                  onDropFiles={onDropFiles}
                />
              ))}
            </div>
          )}
        </>
      )}
    </nav>
  )
}

function PlaceRow({
  dir,
  active,
  icon,
  name,
  hint,
  trailing,
  onNavigate,
  onDropPaths,
  onDropFiles,
}: {
  dir: string
  active: boolean
  icon: React.ReactNode
  name: string
  hint?: string
  trailing?: React.ReactNode
  onNavigate: (path: string) => void
  onDropPaths: (paths: string[], dir: string, mode: DropMode) => void
  onDropFiles: (transfer: DataTransfer, dir: string) => void
}) {
  const drop = useDropTarget({ dir, onDropPaths, onDropFiles })
  // Any trailing control is a sibling of the row rather than a child of it: a
  // <button> inside a <button> is invalid HTML, and React says so as a
  // hydration error on every render of the rail.
  return (
    <div
      {...drop.handlers}
      className={cn(
        "group/row flex items-center gap-1 pr-1 text-body transition-colors hover:bg-row-hover",
        active && "bg-accent",
        drop.over && "bg-wash-brand",
      )}
    >
      <button
        type="button"
        title={dir}
        aria-current={active ? "location" : undefined}
        onClick={() => onNavigate(dir)}
        className={cn(
          "flex min-w-0 flex-1 items-center gap-2.5 pl-3 text-left",
          hint ? "py-1.5" : "py-1",
        )}
      >
        {icon}
        <span className="min-w-0 flex-1">
          <span className={cn("block truncate", active && "font-medium")}>{name}</span>
          {hint && (
            <span className="block truncate font-mono text-hint text-muted-foreground">{hint}</span>
          )}
        </span>
        {active && <span className="size-1.5 shrink-0 rounded-full bg-brand" aria-hidden />}
      </button>
      {trailing}
    </div>
  )
}
