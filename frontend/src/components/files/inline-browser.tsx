"use client"

import { useCallback, useMemo, useState } from "react"
import Link from "next/link"
import {
  ArrowUp,
  ChevronRight,
  FolderClosed,
  FolderOpen,
  GridSquare,
  ListUnordered,
  RefreshClockwise,
} from "@/components/icons"
import { ApiError, get } from "@/lib/api"
import { bytes, plural, relativeTime, timestamp } from "@/lib/format"
import { useSessionState } from "@/lib/view-state"
import { cn } from "@/lib/utils"
import type { FileEntry, FileListing } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import { IconAction } from "@/components/icon-action"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { FileEditorSheet } from "@/components/files/file-editor"
import { Pane, PaneFooter, PaneHeader } from "@/components/panel"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
  stickyTableHeader,
} from "@/components/ui/table"
import { Thumbnail } from "@/components/files/thumbnail"
import { COLUMN } from "@/components/files/file-row"
import { ImageEditorSheet } from "@/components/files/image-editor"
import { MediaViewer } from "@/components/files/media-viewer"
import { baseOf, cleanPath, isWithin, mediaKind, parentOf } from "@/components/files/media"

/**
 * What is actually in a directory, shown where the directory is named.
 *
 * A volume was a name, a size and a path — and the path was a link off to the
 * file manager, which is a different page with a different sidebar and no idea
 * why you went there. The question that sends anybody to a volume's row is
 * "what is in it": did the backup land, what did the container write, is this
 * the copy with the config in it. That question is answered here, in the row
 * that raised it.
 *
 * It is deliberately not the file manager. There is no selection, no clipboard,
 * no upload, no delete, no drag and drop: reading, and opening one file to read
 * or edit it. Anything beyond that is what the "Open in Files" button is for,
 * and it hands the full manager the directory you had reached rather than the
 * one you started from.
 *
 * Navigation is clamped to `root`. A volume's browser that can walk up to `/`
 * is the file manager with a worse breadcrumb, and the crumb here is relative
 * to the root so a volume reads as `LOL-data / config` rather than as six
 * levels of Docker's storage layout.
 */
export function FileBrowser({ root, ...props }: BrowserProps) {
  // Keyed on the root: a browser reused for another volume must not keep the
  // previous one's directory, or the crumb reads as this root while the
  // listing shows the last one's files.
  return <Browser key={cleanPath(root)} root={root} {...props} />
}

type BrowserProps = {
  /** The directory the browser opens at and cannot walk above. */
  root: string
  /** What to call the root in the breadcrumb. Defaults to its last segment. */
  label?: string
  className?: string
  emptyNote?: React.ReactNode
}

function Browser({ root, label, className, emptyNote = "This directory is empty." }: BrowserProps) {
  const { can } = useAuth()
  const base = cleanPath(root)
  const [dir, setDir] = useState(base)
  const [editing, setEditing] = useState<string | null>(null)
  const [editingImage, setEditingImage] = useState<string | null>(null)
  const [viewing, setViewing] = useState<number | null>(null)
  // One choice for every inline browser, the way the file manager keeps one.
  const [view, setView] = useSessionState<"list" | "grid">("files.inline.view", "list")

  const { data, error, loading, refresh } = usePoll<FileListing>(
    (signal) => get<FileListing>("/files/list", { path: dir }, signal),
    0,
    [dir],
  )

  // A bind mount can name a single file — a Caddyfile, a unit, an env file —
  // and listing one is a 400, not a directory with nothing in it. Rather than
  // showing the refusal, stat the path and offer the one row it has.
  const notADirectory = error instanceof ApiError && error.code === "wrong_type"
  const single = usePoll<FileEntry>(
    (signal) => get<FileEntry>("/files/stat", { path: dir }, signal),
    0,
    [dir],
    { enabled: notADirectory },
  )

  const entries = useMemo(() => data?.entries ?? [], [data])
  const viewable = useMemo(() => entries.filter((e) => mediaKind(e.name)), [entries])

  const open = useCallback(
    (entry: FileEntry) => {
      if (entry.isDir) {
        setViewing(null)
        setDir(cleanPath(entry.path))
        return
      }
      const index = viewable.findIndex((e) => e.path === entry.path)
      if (index >= 0) setViewing(index)
      else setEditing(entry.path)
    },
    [viewable],
  )

  // The crumb walks the segments between the root and here, so the root keeps
  // the name the operator knows it by and the depth is the depth inside it.
  const crumbs = useMemo(() => {
    const rest = dir === base ? "" : cleanPath(dir).slice(base === "/" ? 1 : base.length + 1)
    const parts = rest ? rest.split("/") : []
    return parts.map((name, i) => ({
      name,
      path: `${base === "/" ? "" : base}/${parts.slice(0, i + 1).join("/")}`,
    }))
  }, [dir, base])

  const parent = dir === base ? null : parentOf(dir)
  const upReachable = parent !== null && isWithin(parent, base)

  return (
    // The file manager's own shape, at the size of a panel: a working region
    // with its location across the top, the listing in the middle and what is
    // in it along the foot — so a volume opened from Docker reads like the
    // same directory opened in Files, with the same marks and the same words.
    <Pane className={cn("min-w-0", className)}>
      <PaneHeader className="gap-1 px-2">
        <nav
          aria-label="Location inside this storage"
          className="flex min-w-0 flex-1 items-center gap-0.5 overflow-x-auto"
        >
          <button
            type="button"
            onClick={() => setDir(base)}
            className={cn(
              "flex max-w-full shrink-0 items-center gap-1.5 truncate rounded-md px-1.5 py-0.5 text-body focus-ring transition-colors hover:bg-accent hover:text-accent-foreground",
              dir === base && "font-medium",
            )}
          >
            <FolderClosed aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />
            <span className="truncate">{label ?? baseOf(base)}</span>
          </button>
          {crumbs.map((crumb, i) => (
            <span key={crumb.path} className="flex shrink-0 items-center">
              <ChevronRight aria-hidden className="size-3 shrink-0 text-muted-foreground" />
              <button
                type="button"
                onClick={() => setDir(crumb.path)}
                className={cn(
                  "max-w-48 truncate rounded-md px-1.5 py-0.5 text-body focus-ring transition-colors hover:bg-accent hover:text-accent-foreground",
                  i === crumbs.length - 1 && "font-medium",
                )}
              >
                {crumb.name}
              </button>
            </span>
          ))}
        </nav>
        <span className="flex shrink-0 items-center gap-0.5">
          <IconAction label="Refresh" onClick={refresh}>
            <RefreshClockwise />
          </IconAction>
          {/* The two views the file manager has, and the one it remembers:
              a folder of screenshots is found by its pictures, a folder of
              config by its names. */}
          <IconAction
            label="List view"
            aria-pressed={view === "list"}
            onClick={() => setView("list")}
            className={cn(view === "list" && "bg-accent text-accent-foreground")}
          >
            <ListUnordered />
          </IconAction>
          <IconAction
            label="Grid view"
            aria-pressed={view === "grid"}
            onClick={() => setView("grid")}
            className={cn(view === "grid" && "bg-accent text-accent-foreground")}
          >
            <GridSquare />
          </IconAction>
          {/* The button the operator asked to keep: the same directory, in the
              manager that can move, delete and upload into it. */}
          <Button size="xs" variant="outline" asChild className="ml-1">
            <Link href={`/files?path=${encodeURIComponent(dir)}`}>
              <FolderOpen className="size-3" />
              Open in Files
            </Link>
          </Button>
        </span>
      </PaneHeader>

      <div className="@container max-h-96 min-h-0 min-w-0 overflow-y-auto overscroll-contain">
        <Contents
          loading={loading}
          entries={data ? entries : undefined}
          error={error}
          single={notADirectory ? single : undefined}
          emptyNote={emptyNote}
          view={view}
          onUp={upReachable ? () => setDir(parent) : undefined}
          onOpen={open}
          onEdit={setEditing}
        />
      </div>

      <PaneFooter className="justify-between px-3 text-hint text-muted-foreground">
        <span className="numeric min-w-0 truncate">{summary(data ? entries : undefined)}</span>
        <span className="min-w-0 truncate font-mono">{dir}</span>
      </PaneFooter>

      <MediaViewer
        items={viewable}
        index={viewing}
        canWrite={can("file.write")}
        onClose={() => setViewing(null)}
        onIndexChange={setViewing}
        onEditImage={(p) => {
          setViewing(null)
          setEditingImage(p)
        }}
        onOpenEditor={(p) => {
          setViewing(null)
          setEditing(p)
        }}
      />
      <FileEditorSheet
        path={editing}
        onOpenChange={(o) => !o && setEditing(null)}
        onSaved={refresh}
      />
      <ImageEditorSheet
        path={editingImage}
        onOpenChange={(o) => !o && setEditingImage(null)}
        onSaved={refresh}
      />
    </Pane>
  )
}

/** What the footer says: how many of each, and how much the files weigh. */
function summary(entries: FileEntry[] | undefined) {
  if (!entries) return "Reading…"
  if (entries.length === 0) return "Empty folder"
  const folders = entries.filter((e) => e.isDir).length
  const files = entries.length - folders
  const weight = entries.reduce((total, e) => total + (e.isDir ? 0 : e.size), 0)
  const parts = [
    folders > 0 && plural(folders, "folder"),
    files > 0 && plural(files, "file"),
  ].filter(Boolean)
  return `${parts.join(", ")}${files > 0 ? ` · ${bytes(weight)}` : ""}`
}

/**
 * The listing, or the one thing that stands in for it.
 *
 * Four answers share one frame: still loading, a refusal worth reading, a
 * directory with nothing in it, and the rows. The fifth is the bind mount that
 * names a single file rather than a directory — a Caddyfile, a unit, an env
 * file — which arrives here as a stat of one entry and is drawn as a listing
 * of one, so the row that opens it is the row it would have been.
 */
function Contents({
  loading,
  entries,
  error,
  single,
  emptyNote,
  view,
  onUp,
  onOpen,
  onEdit,
}: {
  loading: boolean
  entries: FileEntry[] | undefined
  error: Error | undefined
  /** Set only when the path turned out to be a file. */
  single: { data: FileEntry | undefined; error: Error | undefined } | undefined
  emptyNote: React.ReactNode
  view: "list" | "grid"
  /** Up one level, when there is a level above this one inside the root. */
  onUp?: () => void
  onOpen: (entry: FileEntry) => void
  onEdit: (path: string) => void
}) {
  if (single) {
    if (single.data) {
      const entry = single.data
      return <Listing entries={[entry]} onOpen={() => onEdit(entry.path)} />
    }
    if (single.error) return <ErrorState error={single.error} className="p-3" />
    return <LoadingRows rows={1} className="p-3" />
  }
  if (error) return <ErrorState error={error} className="p-3" />
  if (!entries) return loading ? <LoadingRows rows={3} className="p-3" /> : null
  if (entries.length === 0 && !onUp) {
    return <EmptyNote className="px-3 py-6 text-center">{emptyNote}</EmptyNote>
  }
  if (view === "grid") return <Tiles entries={entries} onUp={onUp} onOpen={onOpen} />
  return <Listing entries={entries} onUp={onUp} onOpen={onOpen} empty={emptyNote} />
}

/**
 * The rows, as the file manager draws them: the thumbnail or the kind's own
 * coloured mark, the name in the body face rather than as `ls` output, and the
 * columns the width can hold — the same `COLUMN` rule `FileRow` answers to, so
 * the two listings give up the same columns at the same widths.
 *
 * A single click opens, where the file manager's listing wants two. There is
 * no selection here for the first click to mean, and a row that looks like a
 * link and needs a double click reads as broken.
 */
function Listing({
  entries,
  onUp,
  onOpen,
  empty,
}: {
  entries: FileEntry[]
  onUp?: () => void
  onOpen: (entry: FileEntry) => void
  empty?: React.ReactNode
}) {
  return (
    <Table>
      <TableHeader className={stickyTableHeader}>
        <TableRow>
          <TableHead className="w-full pl-3">Name</TableHead>
          <TableHead className="text-right">Size</TableHead>
          <TableHead className={COLUMN.modified}>Modified</TableHead>
          <TableHead className={cn("pr-3", COLUMN.owner)}>Owner</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {onUp && (
          <TableRow className="select-none" onActivate={onUp}>
            <TableCell colSpan={4} className="py-2 pl-3">
              <span className="flex items-center gap-2.5 text-body text-muted-foreground">
                <span className="flex size-7 items-center justify-center">
                  <ArrowUp className="size-3.5" />
                </span>
                Parent folder
              </span>
            </TableCell>
          </TableRow>
        )}
        {entries.map((entry) => (
          <TableRow key={entry.path} className="group" onActivate={() => onOpen(entry)}>
            <TableCell className="py-1.5 pl-3">
              <span className="flex min-w-0 items-center gap-2.5">
                <Thumbnail entry={entry} size="row" />
                <span className="min-w-0">
                  <button
                    type="button"
                    onClick={() => onOpen(entry)}
                    title={entry.name}
                    className="block max-w-full truncate rounded-sm text-left text-body focus-ring hover:underline"
                  >
                    {entry.name}
                  </button>
                  {entry.isSymlink && (
                    <span className="block truncate font-mono text-hint text-muted-foreground">
                      → {entry.linkTarget}
                      {entry.linkBroken && (
                        <Tag tone="warning" className="ml-1.5">
                          broken link
                        </Tag>
                      )}
                    </span>
                  )}
                </span>
              </span>
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
            <TableCell className={cn("pr-3 whitespace-nowrap text-muted-foreground", COLUMN.owner)}>
              {entry.owner}:{entry.group}
            </TableCell>
          </TableRow>
        ))}
        {entries.length === 0 && (
          <TableRow>
            <TableCell colSpan={4} className="px-3 py-6 text-center">
              <EmptyNote>{empty}</EmptyNote>
            </TableCell>
          </TableRow>
        )}
      </TableBody>
    </Table>
  )
}

/**
 * The listing as tiles, for a folder whose files are known by what they look
 * like. Read-only like the rows: no selection to make, so one click opens.
 */
function Tiles({
  entries,
  onUp,
  onOpen,
}: {
  entries: FileEntry[]
  onUp?: () => void
  onOpen: (entry: FileEntry) => void
}) {
  const tile =
    "flex min-w-0 flex-col items-stretch gap-1.5 rounded-lg p-2 text-left transition-colors focus-ring hover:bg-row-hover"
  return (
    <div className="grid grid-cols-[repeat(auto-fill,minmax(7rem,1fr))] gap-1 p-2">
      {onUp && (
        <button type="button" onClick={onUp} className={tile}>
          <span className="flex h-16 w-full items-center justify-center rounded-md border border-dashed border-hairline">
            <ArrowUp className="size-5 text-muted-foreground" />
          </span>
          <span className="truncate text-body text-muted-foreground">Parent folder</span>
        </button>
      )}
      {entries.map((entry) => (
        <button
          key={entry.path}
          type="button"
          onClick={() => onOpen(entry)}
          title={entry.name}
          aria-label={entry.name}
          className={tile}
        >
          <Thumbnail entry={entry} size="sm" />
          <span className="truncate text-body">{entry.name}</span>
          <span className="numeric truncate text-hint text-muted-foreground">
            {entry.isDir ? relativeTime(entry.modified) : bytes(entry.size)}
          </span>
        </button>
      ))}
    </div>
  )
}
