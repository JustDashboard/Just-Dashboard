"use client"

import { Download, Eye, MoreHorizontal, Pencil } from "@/components/icons"
import { downloadUrl } from "@/lib/api"
import { bytes, relativeTime, timestamp } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { FileEntry } from "@/lib/types"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { Checkbox } from "@/components/ui/checkbox"
import { IconAction, RowActions } from "@/components/icon-action"
import { TableCell, TableRow } from "@/components/ui/table"
import { DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import { Thumbnail } from "@/components/files/thumbnail"
import { FileActionsMenu, type FileActions, type RowCaps } from "@/components/files/file-actions"
import { mediaKind } from "@/components/files/media"
import { useDropTarget, type DropMode } from "@/components/files/dnd"

export { isArchive, type RowCaps } from "@/components/files/file-actions"

/**
 * The columns a narrow listing gives up, in the order it gives them up.
 * Shared with the header row so the two never disagree about which cell is
 * drawn. They answer to the listing's own width (`@container` on its body)
 * rather than the window's: with the sidebar and the inspector open, a
 * 1280px window leaves the listing under 400px, and a viewport breakpoint
 * would still be drawing six columns into it.
 */
export const COLUMN = {
  modified: "hidden @xl:table-cell",
  owner: "hidden @3xl:table-cell",
  mode: "hidden @4xl:table-cell",
} as const

/**
 * One row of the file listing.
 *
 * The click model is the one every desktop file manager uses: **one click
 * looks, two clicks open.** A click makes the row the active one — the
 * inspector beside the list shows what it is — and Ctrl and Shift extend a
 * selection from it, the way they do everywhere. A double click, or Enter,
 * opens it: a folder is entered, text goes to the editor, a picture to the
 * viewer.
 *
 * A folder row is also a drop target, for paths dragged from other rows and
 * for files dragged in from the desktop. The active row carries a brand-blue
 * rule at its edge: it is *where you are*, which is what the hue is for, and
 * that is how it stays distinct from the rows that are merely checked.
 */
export function FileRow({
  entry,
  selected,
  active,
  dimmed,
  caps,
  onToggle,
  onSelect,
  onOpen,
  onDragStart,
  onDropPaths,
  onDropFiles,
  actions,
}: {
  entry: FileEntry
  selected: boolean
  /** The row the inspector is currently showing. */
  active?: boolean
  /** Faded because it is on the clipboard waiting to be moved. */
  dimmed?: boolean
  caps: RowCaps
  onToggle: (checked: boolean) => void
  onSelect: (event: React.MouseEvent) => void
  onOpen: () => void
  onDragStart?: (event: React.DragEvent) => void
  onDropPaths?: (paths: string[], dir: string, mode: DropMode) => void
  onDropFiles?: (transfer: DataTransfer, dir: string) => void
  actions: FileActions
}) {
  const drop = useDropTarget({
    dir: entry.isDir ? entry.path : null,
    onDropPaths,
    onDropFiles,
  })
  const media = !entry.isDir && mediaKind(entry.name)

  const onKeyDown = (e: React.KeyboardEvent<HTMLTableRowElement>) => {
    if (e.target !== e.currentTarget) return
    if (e.key === "Enter") {
      e.preventDefault()
      onOpen()
    }
  }

  return (
    <TableRow
      data-entry-path={entry.path}
      data-state={selected ? "selected" : undefined}
      draggable={caps.write && !!onDragStart}
      onDragStart={onDragStart}
      {...drop.handlers}
      className={cn(
        "group cursor-pointer focus-ring-inset select-none",
        dimmed && "opacity-50",
        active && "bg-accent",
        drop.over && "bg-wash-brand",
      )}
      // A roving tab stop: the active row is the one the keyboard lands on,
      // rather than every row being a stop on the way to the footer.
      tabIndex={active ? 0 : -1}
      onClick={onSelect}
      onDoubleClick={onOpen}
      onKeyDown={onKeyDown}
    >
      <TableCell
        className={cn(
          "relative w-8",
          active &&
            "before:absolute before:inset-y-1.5 before:left-0 before:w-0.5 before:rounded-full before:bg-brand before:content-['']",
        )}
        onClick={(e) => e.stopPropagation()}
        onDoubleClick={(e) => e.stopPropagation()}
      >
        <Checkbox
          checked={selected}
          onCheckedChange={(v) => onToggle(v === true)}
          aria-label={`Select ${entry.name}`}
        />
      </TableCell>
      <TableCell className="py-1.5">
        <div className="flex max-w-[30rem] min-w-0 items-center gap-2.5">
          <Thumbnail entry={entry} size="row" />
          <div className="min-w-0">
            <button
              className="flex max-w-full items-center text-left text-body hover:underline"
              onClick={(e) => {
                e.stopPropagation()
                onOpen()
              }}
              title={entry.name}
            >
              <span className="truncate">{entry.name}</span>
            </button>
            {entry.isSymlink && (
              <p className="truncate font-mono text-hint text-muted-foreground">
                → {entry.linkTarget}
                {entry.linkBroken && <span className="ml-1 text-destructive">broken</span>}
              </p>
            )}
          </div>
        </div>
      </TableCell>
      <TableCell className="numeric text-right font-mono whitespace-nowrap text-muted-foreground">
        {entry.isDir ? "—" : bytes(entry.size)}
      </TableCell>
      <TableCell
        className={cn("whitespace-nowrap text-muted-foreground", COLUMN.modified)}
        title={timestamp(entry.modified)}
      >
        {relativeTime(entry.modified)}
      </TableCell>
      <TableCell className={cn("whitespace-nowrap text-muted-foreground", COLUMN.owner)}>
        {entry.owner}:{entry.group}
      </TableCell>
      <TableCell className={cn("font-mono text-muted-foreground", COLUMN.mode)}>
        {entry.modeOctal}
      </TableCell>
      <TableCell onClick={(e) => e.stopPropagation()} onDoubleClick={(e) => e.stopPropagation()}>
        <RowActions className="justify-end">
          {!entry.isDir && (
            <>
              {media ? (
                <IconAction label="View" onClick={actions.onView}>
                  <Eye />
                </IconAction>
              ) : (
                <IconAction label={caps.write ? "Edit" : "Open"} onClick={actions.onEdit}>
                  <Pencil />
                </IconAction>
              )}
              <IconAction label="Download" asChild>
                <a href={downloadUrl("/files/download", { path: entry.path })} download>
                  <Download />
                </a>
              </IconAction>
            </>
          )}
          <FileActionsMenu entry={entry} caps={caps} actions={actions}>
            <Tooltip>
              <TooltipTrigger asChild>
                <DropdownMenuTrigger asChild>
                  <Button
                    size="sm"
                    variant="ghost"
                    aria-label="More actions"
                    className="size-7 p-0 text-muted-foreground hover:text-foreground"
                  >
                    <MoreHorizontal className="size-3.5" />
                  </Button>
                </DropdownMenuTrigger>
              </TooltipTrigger>
              <TooltipContent>Rename, move, copy, permissions, delete</TooltipContent>
            </Tooltip>
          </FileActionsMenu>
        </RowActions>
      </TableCell>
    </TableRow>
  )
}
