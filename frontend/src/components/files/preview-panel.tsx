"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import {
  ArrowUpRight,
  Calculator,
  Clipboard,
  Cross,
  Download,
  Eye,
  Fingerprint,
  FolderOpen,
  Pencil,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { downloadUrl, get } from "@/lib/api"
import { bytes, relativeTime, timestamp } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { FileChecksum, FileEntry, FilePreview, FileUsage } from "@/lib/types"
import { Button } from "@/components/ui/button"
import { Detail, DetailList } from "@/components/page"
import { PaneHeader, Well } from "@/components/panel"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import { IconAction } from "@/components/icon-action"
import { FileIcon, kindOfEntry } from "@/components/files/file-icon"
import { Meter } from "@/components/meter"
import { copyText } from "@/lib/clipboard"
import { rawUrl } from "@/components/files/media"
import { ArchiveListing, PdfPreview, TextHead } from "@/components/files/preview-bits"

export { rawUrl } from "@/components/files/media"

/**
 * The inspector beside the listing: what one click gets you.
 *
 * Opening a file used to mean loading it into the editor — the right answer
 * for a config file and the wrong one for a 200 MB log, a JPEG, a tarball and
 * a binary, three of which arrived at the same sheet only to be refused. A
 * single click now asks the server what the thing *is* and shows that: the
 * first hundred lines, the picture, the contents of the archive, the size of
 * the directory. Opening it — the viewer, the editor, the image editor, the
 * download — is the deliberate second action.
 *
 * Nothing here loads a whole file. The text is a head the server trimmed, the
 * image is a URL the browser fetches itself, and the recursive size of a
 * directory is asked for rather than computed on hover.
 */
export function PreviewPanel({
  entry,
  canWrite,
  onOpen,
  onView,
  onEditImage,
  onNavigate,
  onClose,
  className,
}: {
  entry: FileEntry | null
  canWrite: boolean
  /** The editor. */
  onOpen: (path: string) => void
  /** The full-screen viewer. */
  onView: (entry: FileEntry) => void
  onEditImage: (path: string) => void
  onNavigate: (path: string) => void
  onClose: () => void
  className?: string
}) {
  if (!entry) {
    return (
      <div className={cn("flex min-h-0 flex-col", className)}>
        <PaneHeader className="gap-2 pr-1.5">
          <span className="min-w-0 flex-1 truncate text-body font-medium">Details</span>
          <IconAction label="Hide the details" className="size-7" onClick={onClose}>
            <Cross />
          </IconAction>
        </PaneHeader>
        <EmptyNote className="px-5 py-10 leading-relaxed">
          Click a file to see what it is — the first lines, the picture, what is inside an archive —
          without opening it.
        </EmptyNote>
      </div>
    )
  }
  // Keyed on the path so the fetch, the checksum and everything else about
  // one entry is thrown away by React when the selection moves, rather than
  // by an effect that clears four pieces of state on the way past.
  return (
    <Preview
      key={entry.path}
      entry={entry}
      canWrite={canWrite}
      onOpen={onOpen}
      onView={onView}
      onEditImage={onEditImage}
      onNavigate={onNavigate}
      onClose={onClose}
      className={className}
    />
  )
}

function Preview({
  entry,
  canWrite,
  onOpen,
  onView,
  onEditImage,
  onNavigate,
  onClose,
  className,
}: {
  entry: FileEntry
  canWrite: boolean
  onOpen: (path: string) => void
  onView: (entry: FileEntry) => void
  onEditImage: (path: string) => void
  onNavigate: (path: string) => void
  onClose: () => void
  className?: string
}) {
  const [preview, setPreview] = useState<FilePreview>()
  const [error, setError] = useState<Error>()
  const path = entry.path
  const modified = entry.modified

  // Re-read when the entry's own modification time moves — a saved edit, a
  // replaced picture — so the head and the facts are the file as it is now.
  useEffect(() => {
    const controller = new AbortController()
    get<FilePreview>("/files/preview", { path }, controller.signal)
      .then(setPreview)
      .catch((err) => !controller.signal.aborted && setError(err))
    return () => controller.abort()
  }, [path, modified])

  const kind = kindOfEntry(entry)
  return (
    <div className={cn("flex min-h-0 flex-col", className)}>
      <PaneHeader className="gap-2 pr-1.5">
        <FileIcon entry={entry} className="size-5 shrink-0" />
        <div className="min-w-0 flex-1">
          <h2 className="truncate text-body leading-tight font-medium" title={entry.name}>
            {entry.name}
          </h2>
          <p className="truncate text-hint leading-tight text-muted-foreground">
            {preview?.kind === "dir" ? "Folder" : kind.label}
            {preview?.width ? ` · ${preview.width}×${preview.height}` : ""}
          </p>
        </div>
        <IconAction label="Hide the details" className="size-7" onClick={onClose}>
          <Cross />
        </IconAction>
      </PaneHeader>

      <div className="min-h-0 flex-1 space-y-3 overflow-auto p-3">
        {error && <ErrorState error={error} />}
        {!preview && !error && <LoadingRows rows={3} />}
        {preview && (
          <>
            <PreviewBody
              entry={entry}
              preview={preview}
              canWrite={canWrite}
              onOpen={onOpen}
              onView={onView}
              onEditImage={onEditImage}
              onNavigate={onNavigate}
            />
            <Facts entry={entry} preview={preview} />
            <Actions
              entry={entry}
              preview={preview}
              canWrite={canWrite}
              onOpen={onOpen}
              onView={onView}
            />
          </>
        )}
      </div>
    </div>
  )
}

function PreviewBody({
  entry,
  preview,
  canWrite,
  onOpen,
  onView,
  onEditImage,
  onNavigate,
}: {
  entry: FileEntry
  preview: FilePreview
  canWrite: boolean
  onOpen: (path: string) => void
  onView: (entry: FileEntry) => void
  onEditImage: (path: string) => void
  onNavigate: (path: string) => void
}) {
  switch (preview.kind) {
    case "image":
      return (
        <div className="space-y-2">
          <button
            type="button"
            className="flex max-h-72 w-full items-center justify-center overflow-hidden rounded-lg border border-hairline checkerboard p-2 focus-ring"
            onClick={() => onView(entry)}
            title="View full screen"
          >
            {/* eslint-disable-next-line @next/next/no-img-element */}
            <img
              src={rawUrl(entry.path, preview.modified)}
              alt={entry.name}
              className="max-h-64 max-w-full object-contain"
            />
          </button>
          {canWrite && (
            <Button
              size="sm"
              variant="outline"
              className="w-full"
              onClick={() => onEditImage(entry.path)}
            >
              <Pencil className="size-3.5" />
              Crop, rotate, resize
            </Button>
          )}
        </div>
      )
    case "video":
      return (
        <video
          src={rawUrl(entry.path, preview.modified)}
          controls
          preload="metadata"
          className="max-h-72 w-full rounded-lg border border-hairline bg-black"
        />
      )
    case "audio":
      return <audio src={rawUrl(entry.path, preview.modified)} controls className="w-full" />
    case "pdf":
      return <PdfPreview path={entry.path} modified={preview.modified} />
    case "text":
      return (
        <div className="space-y-2">
          <Well className="max-h-72 p-0 whitespace-pre">
            <TextHead text={preview.text ?? ""} />
          </Well>
          <div className="flex items-center justify-between text-hint text-muted-foreground">
            <span>
              {preview.truncated
                ? `First ${preview.lines} lines of ${bytes(preview.size)}`
                : `${preview.lines} line${preview.lines === 1 ? "" : "s"}`}
            </span>
            <Button size="xs" variant="ghost" onClick={() => onOpen(entry.path)}>
              {preview.editable && canWrite ? "Open in editor" : "Open"}
              <ArrowUpRight className="size-3" />
            </Button>
          </div>
        </div>
      )
    case "archive":
      return <ArchiveListing preview={preview} />
    case "dir":
      return <DirectoryPreview entry={entry} preview={preview} onNavigate={onNavigate} />
    default:
      return (
        <div className="rounded-lg border border-dashed border-hairline p-4 text-center text-xs text-muted-foreground">
          {bytes(preview.size)} of binary data. Download it to open it locally.
        </div>
      )
  }
}

/** A folder's own row says "—" for size. This is the button that answers it. */
function DirectoryPreview({
  entry,
  preview,
  onNavigate,
}: {
  entry: FileEntry
  preview: FilePreview
  onNavigate: (path: string) => void
}) {
  const [usage, setUsage] = useState<FileUsage>()
  const [busy, setBusy] = useState(false)

  const measure = useCallback(async () => {
    setBusy(true)
    try {
      setUsage(await get<FileUsage>("/files/usage", { path: entry.path }))
    } catch (err) {
      notify.error("Could not measure this folder", err)
    } finally {
      setBusy(false)
    }
  }, [entry.path])

  return (
    <div className="space-y-2">
      <div className="flex gap-2">
        <Button
          size="sm"
          variant="outline"
          className="flex-1"
          onClick={() => onNavigate(entry.path)}
        >
          <FolderOpen className="size-3.5" />
          Open
        </Button>
        <Button size="sm" variant="outline" className="flex-1" onClick={measure} pending={busy}>
          <Calculator className="size-3.5" />
          Measure
        </Button>
      </div>
      <p className="text-hint text-muted-foreground">
        {preview.childCount ?? 0} item{preview.childCount === 1 ? "" : "s"} directly inside —{" "}
        {preview.dirCount ?? 0} folder{preview.dirCount === 1 ? "" : "s"}, {preview.fileCount ?? 0}{" "}
        file
        {preview.fileCount === 1 ? "" : "s"}
      </p>
      {usage && (
        <div className="space-y-1.5 rounded-lg border border-hairline p-2">
          <p className="text-xs">
            <b className="numeric">{bytes(usage.bytes)}</b>{" "}
            <span className="text-muted-foreground">
              over {usage.files.toLocaleString()} files in {usage.dirs.toLocaleString()} folders
            </span>
          </p>
          {usage.truncated && (
            <p className="text-hint text-warning">
              The walk stopped at its budget, so this is a floor rather than the total.
            </p>
          )}
          {usage.largest?.map((item) => (
            <button
              key={item.path}
              className="flex w-full items-center gap-2 rounded-sm px-1 py-0.5 text-left text-hint hover:bg-accent"
              onClick={() => item.isDir && onNavigate(item.path)}
            >
              <span className="w-24 shrink-0 truncate" title={item.name}>
                {item.name}
              </span>
              <Meter
                value={usage.bytes > 0 ? Math.max(2, (item.bytes / usage.bytes) * 100) : 0}
                label={`${item.name} share of the total`}
                className="flex-1"
              />
              <span className="numeric w-14 shrink-0 text-right text-muted-foreground">
                {bytes(item.bytes)}
              </span>
            </button>
          ))}
        </div>
      )}
    </div>
  )
}

function Facts({ entry, preview }: { entry: FileEntry; preview: FilePreview }) {
  return (
    <DetailList>
      <Detail label="Size">
        <span className="numeric">
          {preview.kind === "dir" ? `${preview.childCount ?? 0} items` : bytes(preview.size)}
        </span>
      </Detail>
      <Detail label="Modified">
        <span title={timestamp(preview.modified)}>{relativeTime(preview.modified)}</span>
      </Detail>
      <Detail label="Owner">
        {preview.owner}:{preview.group}
      </Detail>
      <Detail label="Mode" className="font-mono">
        {preview.modeOctal}
      </Detail>
      {preview.language && preview.language !== "plaintext" && (
        <Detail label="Language">{preview.language}</Detail>
      )}
      {preview.isSymlink && (
        <Detail label="Links to" className="font-mono break-all">
          {preview.symlinkTarget}
          {preview.linkBroken && <span className="ml-1 text-destructive">broken</span>}
        </Detail>
      )}
      <Detail label="Path" className="font-mono text-hint break-all">
        {entry.path}
      </Detail>
    </DetailList>
  )
}

function Actions({
  entry,
  preview,
  canWrite,
  onOpen,
  onView,
}: {
  entry: FileEntry
  preview: FilePreview
  canWrite: boolean
  onOpen: (path: string) => void
  onView: (entry: FileEntry) => void
}) {
  const [sum, setSum] = useState<FileChecksum>()
  const [hashing, setHashing] = useState(false)
  const abort = useRef<AbortController>(null)

  // Nothing resets `sum` on a new selection because nothing has to: the whole
  // panel is keyed on the path, so a different entry is a different component.
  // This only abandons a hash still running when the panel goes away.
  useEffect(() => () => abort.current?.abort(), [])

  const checksum = async () => {
    setHashing(true)
    abort.current = new AbortController()
    try {
      setSum(await get<FileChecksum>("/files/checksum", { path: entry.path }, abort.current.signal))
    } catch (err) {
      if (!abort.current.signal.aborted) notify.error("Could not hash this file", err)
    } finally {
      setHashing(false)
    }
  }

  const copy = (text: string, what: string) => void copyText(text, `${what} copied`)

  if (preview.kind === "dir") {
    return (
      <div className="flex flex-wrap gap-1.5">
        <Button size="xs" variant="outline" onClick={() => copy(entry.path, "Path")}>
          <Clipboard className="size-3" />
          Copy path
        </Button>
      </div>
    )
  }

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap gap-1.5">
        <Button size="xs" variant="outline" onClick={() => onView(entry)}>
          <Eye className="size-3" />
          View
        </Button>
        {preview.editable && (
          <Button size="xs" variant="outline" onClick={() => onOpen(entry.path)}>
            <Pencil className="size-3" />
            {canWrite ? "Edit" : "Open"}
          </Button>
        )}
        <Button size="xs" variant="outline" asChild>
          <a href={downloadUrl("/files/download", { path: entry.path })} download>
            <Download className="size-3" />
            Download
          </a>
        </Button>
        <Button size="xs" variant="outline" onClick={() => copy(entry.path, "Path")}>
          <Clipboard className="size-3" />
          Copy path
        </Button>
        <Button size="xs" variant="outline" onClick={checksum} pending={hashing}>
          <Fingerprint className="size-3" />
          Checksum
        </Button>
      </div>
      {sum && (
        <button
          className="w-full rounded-md border border-hairline bg-surface-sunken p-2 text-left font-mono text-micro break-all hover:border-primary"
          onClick={() => copy(sum.sum, "Checksum")}
          title="Copy the checksum"
        >
          <span className="mr-1 text-muted-foreground">{sum.algo}</span>
          {sum.sum}
        </button>
      )}
      {preview.kind === "binary" && (
        <p className="flex items-center gap-1 text-hint text-muted-foreground">
          <Fingerprint className="size-3" />
          Compare the checksum against the one you were given, rather than trusting the size.
        </p>
      )}
    </div>
  )
}
