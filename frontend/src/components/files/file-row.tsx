"use client"

import { memo, useState } from "react"
import { Download, Eye, Pencil } from "@/components/icons"
import { downloadUrl } from "@/lib/api"
import { bytes, relativeTime, timestamp } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { FileEntry } from "@/lib/types"
import { FileSelection } from "@/components/files/file-selection"
import { Button } from "@/components/ui/button"
import { useNearViewport } from "@/hooks/use-near-viewport"
import { IconAction, RowActions } from "@/components/icon-action"
import { TableCell, TableRow } from "@/components/ui/table"
import { Thumbnail } from "@/components/files/thumbnail"
import type { RowCaps } from "@/components/files/file-actions"
import type { EntryHandlers } from "@/components/files/entry-handlers"
import { mediaKind } from "@/components/files/media"
import { useDropTarget } from "@/components/files/dnd"

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
 *
 * `memo`, over props that change only when the row does: the entry, four
 * booleans, and the page's `handlers`, which are one object for its life. A
 * click moves `active` on two rows and those two are drawn; the other five
 * hundred of a large folder are not.
 */
export const FileRow = memo(function FileRow({
  entry,
  selected,
  active,
  dimmed,
  dragging,
  caps,
  handlers,
}: {
  entry: FileEntry
  selected: boolean
  /** The row the inspector is currently showing. */
  active?: boolean
  /** Faded because it is on the clipboard waiting to be moved. */
  dimmed?: boolean
  dragging?: boolean
  caps: RowCaps
  handlers: EntryHandlers
}) {
  const [viewportRef, near] = useNearViewport<HTMLTableRowElement>()
  const [focusedControls, setFocusedControls] = useState<boolean | null>(null)
  // A viewport update must not replace the button the keyboard is on.
  const fullControls = focusedControls ?? (near || active)
  const drop = useDropTarget({
    dir: entry.isDir ? entry.path : null,
    onDropPaths: caps.write ? handlers.onDropPaths : undefined,
    onDropFiles: caps.write ? handlers.onDropFiles : undefined,
  })
  const media = !entry.isDir && mediaKind(entry.name)
  const onOpen = () => handlers.onOpen(entry)
  const onSelect = (event: React.MouseEvent) => handlers.onSelect(entry, event)
  // The row's verbs are made when one is pressed: a set per row per render was
  // five hundred objects of fifteen closures that nobody had asked for yet.
  const onView = () => handlers.actionsFor(entry).onView()
  const onEdit = () => handlers.actionsFor(entry).onEdit()

  const onKeyDown = (e: React.KeyboardEvent<HTMLTableRowElement>) => {
    if (e.target !== e.currentTarget) return
    if (e.key === "Enter") {
      e.preventDefault()
      onOpen()
    }
  }

  return (
    <TableRow
      ref={viewportRef}
      onFocusCapture={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget as Node)) {
          handlers.onFocusEntry(entry)
          setFocusedControls(!!(near || active))
        }
      }}
      onBlurCapture={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget as Node)) setFocusedControls(null)
      }}
      data-entry-path={entry.path}
      data-state={selected ? "selected" : undefined}
      data-dragging={dragging || undefined}
      draggable={caps.write}
      onDragStart={caps.write ? (event) => handlers.onDragStart(entry, event) : undefined}
      {...drop.handlers}
      className={cn(
        "group cursor-pointer focus-ring-inset transition-opacity duration-150 select-none",
        dimmed && "opacity-50",
        dragging && "opacity-40",
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
        <FileSelection
          checked={selected}
          onCheckedChange={(v) => handlers.onToggle(entry, v === true)}
          aria-label={`Select ${entry.name}`}
        />
      </TableCell>
      <TableCell className="py-1.5">
        <div className="flex max-w-[30rem] min-w-0 items-center gap-2.5">
          {near || active ? (
            <Thumbnail entry={entry} size="row" />
          ) : (
            <span aria-hidden className="size-7 shrink-0" />
          )}
          <div className="min-w-0">
            <button
              data-file-name
              className="flex max-w-full items-center text-left text-body hover:underline"
              onClick={(e) => {
                e.stopPropagation()
                onSelect(e)
              }}
              onDoubleClick={(e) => {
                e.stopPropagation()
                onOpen()
              }}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  e.preventDefault()
                  onOpen()
                }
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
              {fullControls ? (
                media ? (
                  <IconAction label="View" onClick={onView}>
                    <Eye />
                  </IconAction>
                ) : (
                  <IconAction label={caps.write ? "Edit" : "Open"} onClick={onEdit}>
                    <Pencil />
                  </IconAction>
                )
              ) : (
                <Button
                  size="icon-sm"
                  variant="ghost"
                  aria-label={media ? "View" : caps.write ? "Edit" : "Open"}
                  onClick={media ? onView : onEdit}
                >
                  {media ? <Eye className="size-3.5" /> : <Pencil className="size-3.5" />}
                </Button>
              )}
              {fullControls ? (
                <IconAction label="Download" asChild>
                  <a href={downloadUrl("/files/download", { path: entry.path })} download>
                    <Download />
                  </a>
                </IconAction>
              ) : (
                <Button size="icon-sm" variant="ghost" aria-label="Download" asChild>
                  <a href={downloadUrl("/files/download", { path: entry.path })} download>
                    <Download className="size-3.5" />
                  </a>
                </Button>
              )}
            </>
          )}
        </RowActions>
      </TableCell>
    </TableRow>
  )
})
