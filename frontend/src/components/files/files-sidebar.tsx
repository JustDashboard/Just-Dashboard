"use client"

import { ChevronDown, Cross } from "@/components/icons"
import { cn } from "@/lib/utils"
import type { FileBookmark, FilePlaces } from "@/lib/types"
import { useViewState } from "@/lib/view-state"
import { LoadingRows } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { rowReveal } from "@/components/icon-action"
import {
  PlaceMark,
  placeSections,
  type Destination,
  type PlaceSectionKey,
} from "@/components/files/places-menu"
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
 * listing beside it. So this is that list — the homes, what this operator
 * starred, the server's own folders, what was visited lately — drawn once and
 * left alone. The browsed folder is marked when it is one of them.
 *
 * A row is one line: the folder drawn as what it is and the name of what it
 * holds. Each used to carry a second line — its path, or a caption such as
 * "Locally installed software" — which doubled the column's height and made
 * a list of ten shortcuts read as a page of small print; the path is the
 * row's tooltip, and the strip shows it the moment the place is open. A
 * section folds away under its heading, remembered, so a column somebody
 * never uses part of can be as short as they want it.
 *
 * Every row takes a drop: a row dragged from the listing onto one moves
 * there, and a file from the desktop uploads there.
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
  const [folded, setFolded] = useViewState<PlaceSectionKey[]>("files.sidebar.folded", [])
  const bookmarks = places?.bookmarks ?? []

  return (
    <nav
      aria-label="Places"
      className={cn("flex min-h-0 min-w-0 flex-1 flex-col overflow-auto px-2 py-2", className)}
    >
      {places === undefined ? (
        <LoadingRows rows={6} className="p-1" />
      ) : (
        placeSections(places, recent).map((section) => {
          const open = !folded.includes(section.key)
          const id = `files-places-${section.key}`
          return (
            <section key={section.key} className="pb-2">
              <button
                type="button"
                aria-expanded={open}
                aria-controls={id}
                onClick={() =>
                  setFolded(
                    open ? [...folded, section.key] : folded.filter((key) => key !== section.key),
                  )
                }
                className="group/heading flex h-7 w-full items-center gap-1 rounded-md px-2 text-left focus-ring"
              >
                <span className="eyebrow flex-1 transition-colors group-hover/heading:text-foreground">
                  {section.title}
                </span>
                <ChevronDown
                  aria-hidden
                  className={cn(
                    "size-3 text-muted-foreground/70 transition-transform",
                    !open && "-rotate-90",
                  )}
                />
              </button>
              {open && (
                <ul id={id} className="space-y-px">
                  {section.rows.map((row) => (
                    <PlaceRow
                      key={row.path}
                      destination={row}
                      active={row.path === path}
                      onNavigate={onNavigate}
                      onDropPaths={onDropPaths}
                      onDropFiles={onDropFiles}
                      trailing={
                        canWrite &&
                        row.bookmark && (
                          <Tooltip>
                            <TooltipTrigger asChild>
                              <Button
                                size="icon-xs"
                                variant="ghost"
                                className={cn("text-muted-foreground", rowReveal("row"))}
                                aria-label={`Unstar ${row.name}`}
                                onClick={(e) => {
                                  e.stopPropagation()
                                  onBookmarksChange(bookmarks.filter((b) => b.path !== row.path))
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
                </ul>
              )}
            </section>
          )
        })
      )}
    </nav>
  )
}

function PlaceRow({
  destination,
  active,
  trailing,
  onNavigate,
  onDropPaths,
  onDropFiles,
}: {
  destination: Destination
  active: boolean
  trailing?: React.ReactNode
  onNavigate: (path: string) => void
  onDropPaths: (paths: string[], dir: string, mode: DropMode) => void
  onDropFiles: (transfer: DataTransfer, dir: string) => void
}) {
  const dir = destination.path
  const drop = useDropTarget({ dir, onDropPaths, onDropFiles })
  // Any trailing control is a sibling of the row rather than a child of it: a
  // <button> inside a <button> is invalid HTML, and React says so as a
  // hydration error on every render of the rail.
  return (
    <li
      {...drop.handlers}
      className={cn(
        "group/row flex h-8 items-center gap-1 rounded-md pr-1 text-body transition-colors hover:bg-row-hover",
        active && "bg-accent text-accent-foreground hover:bg-accent",
        drop.over && "bg-wash-brand",
      )}
    >
      <button
        type="button"
        title={dir}
        aria-current={active ? "location" : undefined}
        onClick={() => onNavigate(dir)}
        className="flex h-full min-w-0 flex-1 items-center gap-2.5 rounded-md pl-2 text-left focus-ring-inset"
      >
        <PlaceMark destination={destination} className="size-4.5" />
        <span className={cn("min-w-0 flex-1 truncate", active && "font-medium")}>
          {destination.name}
        </span>
      </button>
      {trailing}
    </li>
  )
}
