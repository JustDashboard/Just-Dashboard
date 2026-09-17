"use client"

import { ChevronDown, Cross, Home, Servers, Star } from "@/components/icons"
import { cn } from "@/lib/utils"
import type { FileBookmark, FilePlaces } from "@/lib/types"
import { LoadingRows } from "@/components/state"
import { PaneHeader } from "@/components/panel"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { IconAction, rowReveal } from "@/components/icon-action"
import { FileTree, type ConfirmRequest } from "@/components/files/file-tree"
import { PlacesMenu } from "@/components/files/places-menu"
import { useDropTarget, type DropMode } from "@/components/files/dnd"
import { baseOf, isWithin } from "@/components/files/media"

/**
 * The column down the left of the file manager: one scrolling list, not two.
 *
 * It used to be a places list over a folder tree, each in its own bounded
 * half with a rule between them, and the tree started at "/" — so the half
 * that mattered, the tree of the operator's own files, had to fit in the
 * lower half of a 256px column under a dozen system directories. Now the
 * tree is the column. It is rooted at the *place* the browsed folder lives
 * in — home, nearly always — and the strip at the top names that place and
 * opens the menu that jumps anywhere else: the roots, /etc, /var/www, the
 * other accounts, what was starred, what was just visited. Starred folders
 * sit above the tree in the same scroll, the way a desktop's quick-access
 * list sits above its drives.
 *
 * Folders in it take a drop: a row dragged from the listing onto one moves
 * there, and a file from the desktop uploads there.
 */
export function FilesSidebar({
  places,
  root,
  path,
  recent,
  showHidden,
  canWrite,
  canDestruct,
  refreshTick,
  onNavigate,
  onOpenFile,
  onBookmarksChange,
  onConfirm,
  onChanged,
  onDropPaths,
  onDropFiles,
  onClose,
  className,
}: {
  places: FilePlaces | undefined
  /** The place the tree is rooted at. */
  root: string | undefined
  /** The folder being browsed. */
  path: string
  recent: string[]
  showHidden: boolean
  canWrite: boolean
  canDestruct: boolean
  refreshTick: number
  onNavigate: (path: string) => void
  onOpenFile: (path: string) => void
  onBookmarksChange: (next: FileBookmark[]) => void
  onConfirm: (req: ConfirmRequest) => void
  onChanged: () => void
  onDropPaths: (paths: string[], dir: string, mode: DropMode) => void
  onDropFiles: (transfer: DataTransfer, dir: string) => void
  onClose: () => void
  className?: string
}) {
  const bookmarks = places?.bookmarks ?? []
  const home = places?.home
  const rootLabel = root === undefined ? "" : root === home ? "Home" : root

  return (
    <div className={cn("flex min-h-0 min-w-0 flex-1 flex-col", className)}>
      <PaneHeader className="gap-1 pr-1.5 pl-1.5">
        <PlacesMenu places={places} recent={recent} current={path} onPick={onNavigate}>
          <button
            type="button"
            aria-label="Places"
            className="flex h-7 min-w-0 flex-1 items-center gap-1.5 rounded-md px-1.5 text-left text-body font-medium focus-ring-inset transition-colors hover:bg-accent"
          >
            {root === home ? (
              <Home className="size-3.5 shrink-0 text-brand" />
            ) : (
              <Servers className="size-3.5 shrink-0 text-muted-foreground" />
            )}
            <span className="min-w-0 flex-1 truncate" title={root}>
              {rootLabel}
            </span>
            <ChevronDown className="size-3 shrink-0 text-muted-foreground" />
          </button>
        </PlacesMenu>
        <IconAction label="Hide the sidebar" className="size-7" onClick={onClose}>
          <Cross />
        </IconAction>
      </PaneHeader>

      <div className="min-h-0 flex-1 overflow-auto">
        {bookmarks.length > 0 && (
          <div className="border-b border-hairline py-1.5">
            <p className="eyebrow px-3 pt-1 pb-1">Starred</p>
            {bookmarks.map((bookmark) => (
              <StarredRow
                key={bookmark.path}
                bookmark={bookmark}
                active={bookmark.path === path}
                canWrite={canWrite}
                onNavigate={onNavigate}
                onRemove={() =>
                  onBookmarksChange(bookmarks.filter((b) => b.path !== bookmark.path))
                }
                onDropPaths={onDropPaths}
                onDropFiles={onDropFiles}
              />
            ))}
          </div>
        )}
        {root === undefined ? (
          <LoadingRows rows={6} className="p-3" />
        ) : (
          <FileTree
            // A new place is a new tree, opened fresh at its root.
            key={root}
            root={root}
            chrome={false}
            foldersOnly
            statusMap={{}}
            canWrite={canWrite}
            canDelete={canDestruct}
            activeDir={isWithin(path, root) ? path : undefined}
            hidden={showHidden}
            refreshTick={refreshTick}
            onNavigate={onNavigate}
            onOpenFile={onOpenFile}
            onConfirm={onConfirm}
            onChanged={onChanged}
            onDropPaths={onDropPaths}
            onDropFiles={onDropFiles}
          />
        )}
      </div>
    </div>
  )
}

function StarredRow({
  bookmark,
  active,
  canWrite,
  onNavigate,
  onRemove,
  onDropPaths,
  onDropFiles,
}: {
  bookmark: FileBookmark
  active: boolean
  canWrite: boolean
  onNavigate: (path: string) => void
  onRemove: () => void
  onDropPaths: (paths: string[], dir: string, mode: DropMode) => void
  onDropFiles: (transfer: DataTransfer, dir: string) => void
}) {
  const drop = useDropTarget({ dir: bookmark.path, onDropPaths, onDropFiles })
  // The remove control is a sibling of the row rather than a child of it: a
  // <button> inside a <button> is invalid HTML, and React says so as a
  // hydration error on every render of the rail.
  return (
    <div
      {...drop.handlers}
      className={cn(
        "group/row flex items-center gap-1 pr-1 text-body transition-colors hover:bg-row-hover",
        active && "bg-accent font-medium",
        drop.over && "bg-wash-brand",
      )}
    >
      <button
        type="button"
        title={bookmark.path}
        onClick={() => onNavigate(bookmark.path)}
        className="flex min-w-0 flex-1 items-center gap-2 py-[3px] pl-3 text-left"
      >
        <Star className="size-3.5 shrink-0 text-warning" />
        <span className="min-w-0 flex-1 truncate">{bookmark.name || baseOf(bookmark.path)}</span>
      </button>
      {canWrite && (
        <Tooltip>
          <TooltipTrigger asChild>
            <Button
              size="icon-xs"
              variant="ghost"
              className={cn("text-muted-foreground", rowReveal("row"))}
              aria-label={`Unstar ${bookmark.name ?? bookmark.path}`}
              onClick={(e) => {
                e.stopPropagation()
                onRemove()
              }}
            >
              <Cross />
            </Button>
          </TooltipTrigger>
          <TooltipContent>Unstar</TooltipContent>
        </Tooltip>
      )}
    </div>
  )
}
