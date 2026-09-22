"use client"

import { useCallback, useMemo, useState } from "react"
import Link from "next/link"
import { ArrowUp, ChevronRight, FolderOpen, RefreshClockwise } from "@/components/icons"
import { ApiError, get } from "@/lib/api"
import { bytes, relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { FileEntry, FileListing } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import { IconAction } from "@/components/icon-action"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { FileEditorSheet } from "@/components/files/file-editor"
import { FileIcon } from "@/components/files/file-icon"
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

  return (
    <div className={cn("min-w-0 space-y-2", className)}>
      <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
        <nav
          aria-label="Location inside this storage"
          className="flex min-w-0 flex-1 flex-wrap items-center gap-0.5 text-hint"
        >
          <button
            type="button"
            onClick={() => setDir(base)}
            className={cn(
              "max-w-full truncate rounded-sm px-1 py-0.5 font-mono focus-ring hover:text-primary",
              dir === base ? "text-foreground" : "text-muted-foreground",
            )}
          >
            {label ?? baseOf(base)}
          </button>
          {crumbs.map((crumb, i) => (
            <span key={crumb.path} className="flex min-w-0 items-center gap-0.5">
              <ChevronRight aria-hidden className="size-3 shrink-0 text-muted-foreground" />
              <button
                type="button"
                onClick={() => setDir(crumb.path)}
                className={cn(
                  "max-w-full truncate rounded-sm px-1 py-0.5 font-mono focus-ring hover:text-primary",
                  i === crumbs.length - 1 ? "text-foreground" : "text-muted-foreground",
                )}
              >
                {crumb.name}
              </button>
            </span>
          ))}
        </nav>
        <span className="flex shrink-0 items-center gap-1">
          {parent && isWithin(parent, base) && (
            <IconAction label="Up one level" onClick={() => setDir(parent)}>
              <ArrowUp />
            </IconAction>
          )}
          <IconAction label="Refresh" onClick={refresh}>
            <RefreshClockwise />
          </IconAction>
          {/* The button the operator asked to keep: the same directory, in the
              manager that can move, delete and upload into it. */}
          <Button size="sm" variant="outline" asChild>
            <Link href={`/files?path=${encodeURIComponent(dir)}`}>
              <FolderOpen className="size-3.5" />
              Open in Files
            </Link>
          </Button>
        </span>
      </div>

      <div className="@container min-w-0 overflow-hidden rounded-md border border-hairline bg-surface-sunken">
        <div className="max-h-80 min-w-0 overflow-y-auto overscroll-contain">
          <Contents
            loading={loading}
            entries={data ? entries : undefined}
            error={error}
            single={notADirectory ? single : undefined}
            emptyNote={emptyNote}
            onOpen={open}
            onEdit={setEditing}
          />
        </div>
      </div>

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
    </div>
  )
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
  onOpen,
  onEdit,
}: {
  loading: boolean
  entries: FileEntry[] | undefined
  error: Error | undefined
  /** Set only when the path turned out to be a file. */
  single: { data: FileEntry | undefined; error: Error | undefined } | undefined
  emptyNote: React.ReactNode
  onOpen: (entry: FileEntry) => void
  onEdit: (path: string) => void
}) {
  if (single) {
    if (single.data) {
      const entry = single.data
      return (
        <ul className="min-w-0 divide-y divide-hairline">
          <EntryRow entry={entry} onOpen={() => onEdit(entry.path)} />
        </ul>
      )
    }
    if (single.error) return <ErrorState error={single.error} className="p-3" />
    return <LoadingRows rows={1} className="p-3" />
  }
  if (error) return <ErrorState error={error} className="p-3" />
  if (!entries) return loading ? <LoadingRows rows={3} className="p-3" /> : null
  if (entries.length === 0) return <EmptyNote className="px-3 py-4">{emptyNote}</EmptyNote>
  return (
    <ul className="min-w-0 divide-y divide-hairline">
      {entries.map((entry) => (
        <EntryRow key={entry.path} entry={entry} onOpen={() => onOpen(entry)} />
      ))}
    </ul>
  )
}

/**
 * One entry: what it is, what it is called, and how big and how old.
 *
 * A single click opens, where the file manager's listing wants two. There is
 * no selection here for the first click to mean, and a row that looks like a
 * link and needs a double click reads as broken.
 */
function EntryRow({ entry, onOpen }: { entry: FileEntry; onOpen: () => void }) {
  return (
    <li className="min-w-0">
      {/*
        No ROW_BLEED. That bleed is for a row sitting directly on a plain
        panel's own edge; these rows sit inside this browser's frame, and
        inside the container's Storage tab — a plain panel — they inherited it
        and shifted a step left against the border clipping them.
      */}
      <button
        type="button"
        onClick={onOpen}
        className="flex w-full min-w-0 items-center gap-2.5 px-3 py-2 text-left transition-colors focus-ring-inset hover:bg-row-hover"
      >
        <FileIcon entry={entry} className="size-4" />
        <span className="min-w-0 flex-1 truncate font-mono text-hint">{entry.name}</span>
        {entry.linkBroken && <Tag tone="warning">broken link</Tag>}
        <span className="numeric shrink-0 font-mono text-hint text-muted-foreground">
          {entry.isDir ? "—" : bytes(entry.size)}
        </span>
        <span className="hidden shrink-0 text-hint text-muted-foreground @md:inline">
          {relativeTime(entry.modified)}
        </span>
      </button>
    </li>
  )
}
