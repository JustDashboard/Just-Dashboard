"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import {
  ArrowUpRight,
  Calculator,
  Clipboard,
  Download,
  Eye,
  Fingerprint,
  FolderOpen,
  Pencil,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { downloadUrl, get } from "@/lib/api"
import { bytes, plural, relativeTime, timestamp } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { FileChecksum, FileEntry, FilePreview, FileUsage } from "@/lib/types"
import { Button } from "@/components/ui/button"
import { Detail, DetailList } from "@/components/page"
import { Well } from "@/components/panel"
import { ErrorState, LoadingRows } from "@/components/state"
import {
  FileIcon,
  kindOfEntry,
  useFolderColour,
  type FolderColour,
} from "@/components/files/file-icon"
import { FolderColourSwatches } from "@/components/files/folder-colour"
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
 * It opens on the thing itself, large — the picture, the video, or the folder
 * or page it is drawn as — with its name and kind under it, the way a desktop's
 * preview column does; a folder carries its colour there too. With nothing
 * chosen it describes the folder being browsed rather than asking to be
 * clicked: that column was a sentence of instructions on every visit.
 *
 * Nothing here loads a whole file. The text is a head the server trimmed, the
 * image is a URL the browser fetches itself, and the recursive size of a
 * directory is asked for rather than computed on hover.
 */
export function PreviewPanel({
  entry,
  folder,
  canWrite,
  onColour,
  onOpen,
  onView,
  onEditImage,
  onNavigate,
  className,
}: {
  entry: FileEntry | null
  /** The folder being browsed, shown while nothing in it is chosen. */
  folder: FileEntry | null
  canWrite: boolean
  onColour: (entry: FileEntry, colour: FolderColour) => void
  /** The editor. */
  onOpen: (path: string) => void
  /** The full-screen viewer. */
  onView: (entry: FileEntry) => void
  onEditImage: (path: string) => void
  onNavigate: (path: string) => void
  className?: string
}) {
  const shown = entry ?? folder
  if (!shown) {
    return (
      <div className={cn("flex min-h-0 flex-col", className)}>
        <LoadingRows rows={3} className="p-4" />
      </div>
    )
  }
  // Keyed on the path so the fetch, the checksum and everything else about
  // one entry is thrown away by React when the selection moves, rather than
  // by an effect that clears four pieces of state on the way past.
  return (
    <Preview
      key={shown.path}
      entry={shown}
      current={!entry}
      canWrite={canWrite}
      onColour={onColour}
      onOpen={onOpen}
      onView={onView}
      onEditImage={onEditImage}
      onNavigate={onNavigate}
      className={className}
    />
  )
}

function Preview({
  entry,
  current,
  canWrite,
  onColour,
  onOpen,
  onView,
  onEditImage,
  onNavigate,
  className,
}: {
  entry: FileEntry
  /** The folder being browsed, rather than a row in it. */
  current: boolean
  canWrite: boolean
  onColour: (entry: FileEntry, colour: FolderColour) => void
  onOpen: (path: string) => void
  onView: (entry: FileEntry) => void
  onEditImage: (path: string) => void
  onNavigate: (path: string) => void
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

  return (
    <div className={cn("flex min-h-0 flex-col overflow-auto", className)}>
      <Hero
        entry={entry}
        preview={preview}
        current={current}
        canWrite={canWrite}
        onColour={onColour}
        onView={onView}
      />
      <div className="space-y-4 p-4">
        {error && <ErrorState error={error} />}
        {!preview && !error && <LoadingRows rows={3} />}
        {preview && (
          <>
            <PreviewBody
              entry={entry}
              preview={preview}
              current={current}
              canWrite={canWrite}
              onOpen={onOpen}
              onEditImage={onEditImage}
              onNavigate={onNavigate}
            />
            <Actions
              entry={entry}
              preview={preview}
              canWrite={canWrite}
              onOpen={onOpen}
              onView={onView}
            />
            <Facts entry={entry} preview={preview} />
          </>
        )}
      </div>
    </div>
  )
}

/**
 * The thing, large, and what it is called. A picture or a video is itself;
 * everything else is the folder or the page the listing draws it as, at a
 * size where the page can carry its extension.
 */
function Hero({
  entry,
  preview,
  current,
  canWrite,
  onColour,
  onView,
}: {
  entry: FileEntry
  preview: FilePreview | undefined
  current: boolean
  canWrite: boolean
  onColour: (entry: FileEntry, colour: FolderColour) => void
  onView: (entry: FileEntry) => void
}) {
  const colour = useFolderColour(entry.path, entry.name)
  const kind = kindOfEntry(entry)
  const facts = [
    current ? "This folder" : kind.label,
    preview?.width ? `${preview.width}×${preview.height}` : null,
    preview?.kind === "dir"
      ? plural(preview.childCount ?? 0, "item")
      : preview && !entry.isDir
        ? bytes(preview.size)
        : null,
  ].filter(Boolean)

  return (
    <div className="flex flex-col items-center gap-3 border-b border-hairline px-4 pt-6 pb-4 text-center">
      {preview?.kind === "image" ? (
        <button
          type="button"
          className="flex max-h-56 w-full items-center justify-center overflow-hidden rounded-lg border border-hairline checkerboard p-2 focus-ring"
          onClick={() => onView(entry)}
          title="View full screen"
        >
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img
            src={rawUrl(entry.path, preview.modified)}
            alt={entry.name}
            className="max-h-52 max-w-full object-contain"
          />
        </button>
      ) : preview?.kind === "video" ? (
        <video
          src={rawUrl(entry.path, preview.modified)}
          controls
          preload="metadata"
          className="max-h-56 w-full rounded-lg border border-hairline bg-black"
        />
      ) : (
        <FileIcon entry={entry} detail className="size-24" />
      )}
      <div className="w-full min-w-0 space-y-1">
        <h2
          className="line-clamp-2 text-title leading-snug font-semibold break-all"
          title={entry.name}
        >
          {entry.name}
        </h2>
        <p className="numeric truncate text-hint text-muted-foreground">{facts.join(" · ")}</p>
      </div>
      {entry.isDir && canWrite && (
        <FolderColourSwatches
          value={colour}
          onPick={(next) => onColour(entry, next)}
          className="justify-center"
        />
      )}
    </div>
  )
}

function PreviewBody({
  entry,
  preview,
  current,
  canWrite,
  onOpen,
  onEditImage,
  onNavigate,
}: {
  entry: FileEntry
  preview: FilePreview
  current: boolean
  canWrite: boolean
  onOpen: (path: string) => void
  onEditImage: (path: string) => void
  onNavigate: (path: string) => void
}) {
  switch (preview.kind) {
    case "image":
      return canWrite ? (
        <Button
          size="sm"
          variant="outline"
          className="w-full"
          onClick={() => onEditImage(entry.path)}
        >
          <Pencil className="size-3.5" />
          Crop, rotate, resize
        </Button>
      ) : null
    case "video":
      return null
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
      return (
        <DirectoryPreview
          entry={entry}
          preview={preview}
          current={current}
          onNavigate={onNavigate}
        />
      )
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
  current,
  onNavigate,
}: {
  entry: FileEntry
  preview: FilePreview
  /** The folder being browsed, which there is no opening. */
  current: boolean
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
        {!current && (
          <Button
            size="sm"
            variant="outline"
            className="flex-1"
            onClick={() => onNavigate(entry.path)}
          >
            <FolderOpen className="size-3.5" />
            Open
          </Button>
        )}
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
