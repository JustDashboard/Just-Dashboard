"use client"

import { Download, MoreHorizontal, Pencil } from "@/components/icons"
import { downloadUrl } from "@/lib/api"
import { bytes, relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { FileEntry } from "@/lib/types"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { Checkbox } from "@/components/ui/checkbox"
import { IconAction, RowActions } from "@/components/icon-action"
import { TableCell, TableRow } from "@/components/ui/table"
import { DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import { FileIcon } from "@/components/files/file-icon"
import { FileActionsMenu, type FileActions, type RowCaps } from "@/components/files/file-actions"

export { isArchive, type RowCaps } from "@/components/files/file-actions"

/**
 * One row of the file listing.
 *
 * The click model is the one every desktop file manager uses and this page did
 * not: **one click looks, two clicks open.** Selecting a row asks the server
 * what the thing is and shows it in the preview beside the list, which is the
 * question a click is usually asking — the old page answered every click by
 * loading the file into a code editor, which is wrong for an image, a tarball
 * and a two-gigabyte log alike.
 *
 * The common actions stay as quick icons; the rest live in the shared menu, so
 * the row is not a wall of buttons and the grid offers exactly the same list.
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
  actions,
}: {
  entry: FileEntry
  selected: boolean
  /** The row the preview pane is currently showing. */
  active?: boolean
  /** Faded because it is on the clipboard waiting to be moved. */
  dimmed?: boolean
  caps: RowCaps
  onToggle: (checked: boolean) => void
  onSelect: () => void
  onOpen: () => void
  actions: FileActions
}) {
  const onKeyDown = (e: React.KeyboardEvent<HTMLTableRowElement>) => {
    if (e.target !== e.currentTarget) return
    if (e.key === "Enter") {
      e.preventDefault()
      onOpen()
    } else if (e.key === " ") {
      e.preventDefault()
      onSelect()
    }
  }

  return (
    <TableRow
      className={cn(
        "group cursor-pointer select-none",
        dimmed && "opacity-50",
        active && !selected && "bg-accent",
      )}
      data-state={selected ? "selected" : undefined}
      tabIndex={0}
      onClick={onSelect}
      onDoubleClick={onOpen}
      onKeyDown={onKeyDown}
    >
      <TableCell onClick={(e) => e.stopPropagation()} onDoubleClick={(e) => e.stopPropagation()}>
        <Checkbox
          checked={selected}
          onCheckedChange={(v) => onToggle(v === true)}
          aria-label={`Select ${entry.name}`}
        />
      </TableCell>
      <TableCell>
        <div className="max-w-[26rem] min-w-0">
          <button
            className="flex max-w-full items-center gap-2 text-left text-body hover:underline"
            onClick={(e) => {
              e.stopPropagation()
              onOpen()
            }}
            title={entry.name}
          >
            <FileIcon entry={entry} className="size-4" />
            <span className="truncate">{entry.name}</span>
          </button>
          {entry.isSymlink && (
            <p className="ml-6 truncate font-mono text-hint text-muted-foreground">
              → {entry.linkTarget}
              {entry.linkBroken && <span className="ml-1 text-destructive">broken</span>}
            </p>
          )}
        </div>
      </TableCell>
      <TableCell className="numeric text-right font-mono text-muted-foreground">
        {entry.isDir ? "—" : bytes(entry.size)}
      </TableCell>
      <TableCell className="text-muted-foreground">{relativeTime(entry.modified)}</TableCell>
      <TableCell className="text-muted-foreground">
        {entry.owner}:{entry.group}
      </TableCell>
      <TableCell className="font-mono text-muted-foreground">{entry.modeOctal}</TableCell>
      <TableCell onClick={(e) => e.stopPropagation()} onDoubleClick={(e) => e.stopPropagation()}>
        <RowActions className="justify-end">
          {!entry.isDir && (
            <>
              <IconAction label="Edit" onClick={onOpen}>
                <Pencil />
              </IconAction>
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
