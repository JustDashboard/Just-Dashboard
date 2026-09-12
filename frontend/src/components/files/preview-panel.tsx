"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import {
  ArrowUpRight,
  Calculator,
  Clipboard,
  Cross,
  Download,
  Fingerprint,
  FolderOpen,
  Image,
  Pencil,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { downloadUrl, get } from "@/lib/api"
import { bytes, relativeTime, timestamp } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { FileChecksum, FileEntry, FilePreview, FileUsage } from "@/lib/types"
import { Button } from "@/components/ui/button"
import { Detail, DetailList } from "@/components/page"
import { PanelBody, PanelHeader, Well } from "@/components/panel"
import { EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { IconAction } from "@/components/icon-action"
import { FileIcon, kindOfEntry } from "@/components/files/file-icon"
import { Meter } from "@/components/meter"
import { copyText } from "@/lib/clipboard"

/** The raw URL for a file, cache-busted by its own modification time. */
export function rawUrl(path: string, modified?: string) {
  return downloadUrl("/files/raw", { path, v: modified ? Date.parse(modified) : undefined })
}

/**
 * What one click gets you.
 *
 * Opening a file used to mean loading it into the editor — the right answer
 * for a config file and the wrong one for a 200 MB log, a JPEG, a tarball and
 * a binary, three of which arrived at the same sheet only to be refused. A
 * single click now asks the server what the thing *is* and shows that: the
 * first hundred lines, the picture, the contents of the archive, the size of
 * the directory. Opening it — the editor, the image editor, the download — is
 * the deliberate second action.
 *
 * Nothing here loads a whole file. The text is a head the server trimmed, the
 * image is a URL the browser fetches itself, and the recursive size of a
 * directory is asked for rather than computed on hover.
 */
export function PreviewPanel({
  entry,
  canWrite,
  onOpen,
  onEditImage,
  onNavigate,
  onClose,
  className,
}: {
  entry: FileEntry | null
  canWrite: boolean
  onOpen: (path: string) => void
  onEditImage: (path: string) => void
  onNavigate: (path: string) => void
  onClose: () => void
  className?: string
}) {
  if (!entry) {
    return (
      <div className={cn("flex min-h-0 flex-col", className)}>
        <EmptyState
          icon={Image}
          title="Nothing selected"
          description="Click a row to see what it is — the first lines of a file, a picture, what is inside an archive."
        />
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
  onEditImage,
  onNavigate,
  onClose,
  className,
}: {
  entry: FileEntry
  canWrite: boolean
  onOpen: (path: string) => void
  onEditImage: (path: string) => void
  onNavigate: (path: string) => void
  onClose: () => void
  className?: string
}) {
  const [preview, setPreview] = useState<FilePreview>()
  const [error, setError] = useState<Error>()
  const path = entry.path

  useEffect(() => {
    const controller = new AbortController()
    get<FilePreview>("/files/preview", { path }, controller.signal)
      .then(setPreview)
      .catch((err) => !controller.signal.aborted && setError(err))
    return () => controller.abort()
  }, [path])

  const kind = kindOfEntry(entry)
  return (
    <div className={cn("flex min-h-0 flex-col", className)}>
      <PanelHeader className="gap-2">
        <div className="flex min-w-0 items-center gap-2.5">
          <FileIcon entry={entry} className="size-7" />
          <div className="min-w-0">
            <h2 className="truncate text-body leading-tight font-medium" title={entry.name}>
              {entry.name}
            </h2>
            <p className="truncate text-xs leading-tight text-muted-foreground">
              {preview?.kind === "dir" ? "Folder" : kind.label}
              {preview?.width ? ` · ${preview.width}×${preview.height}` : ""}
            </p>
          </div>
        </div>
        <IconAction label="Close the preview" onClick={onClose}>
          <Cross />
        </IconAction>
      </PanelHeader>

      <PanelBody scroll className="space-y-3 p-3">
        {error && <ErrorState error={error} />}
        {!preview && !error && <LoadingRows rows={3} />}
        {preview && (
          <>
            <PreviewBody
              entry={entry}
              preview={preview}
              canWrite={canWrite}
              onOpen={onOpen}
              onEditImage={onEditImage}
              onNavigate={onNavigate}
            />
            <Facts entry={entry} preview={preview} />
            <Actions entry={entry} preview={preview} canWrite={canWrite} onOpen={onOpen} />
          </>
        )}
      </PanelBody>
    </div>
  )
}

function PreviewBody({
  entry,
  preview,
  canWrite,
  onOpen,
  onEditImage,
  onNavigate,
}: {
  entry: FileEntry
  preview: FilePreview
  canWrite: boolean
  onOpen: (path: string) => void
  onEditImage: (path: string) => void
  onNavigate: (path: string) => void
}) {
  switch (preview.kind) {
    case "image":
      return (
        <div className="space-y-2">
          <div className="flex max-h-72 items-center justify-center overflow-hidden rounded-lg border border-hairline checkerboard p-2">
            {/* eslint-disable-next-line @next/next/no-img-element */}
            <img
              src={rawUrl(entry.path, preview.modified)}
              alt={entry.name}
              className="max-h-64 max-w-full object-contain"
            />
          </div>
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
      return (
        <div className="space-y-1.5">
          <p className="text-hint text-muted-foreground">
            {preview.archiveError
              ? `Could not read the archive: ${preview.archiveError}`
              : preview.entries?.length
                ? `${preview.entryCount}${preview.moreEntries ? "+" : ""} entries inside`
                : "This archive's format cannot be listed here — download it to unpack."}
          </p>
          {preview.entries && preview.entries.length > 0 && (
            <Well className="max-h-64 space-y-0.5 p-2">
              {preview.entries.map((item) => (
                <div key={item.name} className="flex items-center justify-between gap-3">
                  <span className="truncate" title={item.name}>
                    {item.name}
                  </span>
                  {!item.isDir && (
                    <span className="numeric shrink-0 text-muted-foreground">
                      {bytes(item.size)}
                    </span>
                  )}
                </div>
              ))}
            </Well>
          )}
        </div>
      )
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

/** The head, with line numbers, because "line 42" is how errors are reported. */
function TextHead({ text }: { text: string }) {
  const lines = useMemo(() => text.replace(/\n$/, "").split("\n"), [text])
  return (
    <div className="grid grid-cols-[auto_1fr] gap-x-3 p-2">
      <div className="numeric shrink-0 text-right text-muted-foreground/60 select-none">
        {lines.map((_, i) => (
          <div key={i}>{i + 1}</div>
        ))}
      </div>
      <div className="min-w-0">
        {lines.map((line, i) => (
          <div key={i} className="whitespace-pre">
            {line || " "}
          </div>
        ))}
      </div>
    </div>
  )
}

/**
 * A PDF through a blob rather than straight from the API.
 *
 * The API's own responses carry `X-Frame-Options: DENY`, which is what keeps
 * the dashboard out of somebody else's iframe and which a browser applies to
 * the PDF viewer too. Fetching the bytes and framing a blob URL keeps that
 * header exactly as strict as it is and still shows the document.
 */
function PdfPreview({ path, modified }: { path: string; modified: string }) {
  const [url, setUrl] = useState<string>()
  const [failed, setFailed] = useState(false)

  useEffect(() => {
    let objectUrl: string | undefined
    const controller = new AbortController()
    fetch(rawUrl(path, modified), { credentials: "include", signal: controller.signal })
      .then((res) => (res.ok ? res.blob() : Promise.reject(new Error(res.statusText))))
      .then((blob) => {
        objectUrl = URL.createObjectURL(blob)
        setUrl(objectUrl)
      })
      .catch(() => !controller.signal.aborted && setFailed(true))
    return () => {
      controller.abort()
      if (objectUrl) URL.revokeObjectURL(objectUrl)
    }
  }, [path, modified])

  if (failed) {
    return (
      <p className="rounded-lg border border-dashed border-hairline p-4 text-center text-xs text-muted-foreground">
        This PDF could not be loaded for preview. Download it to read it.
      </p>
    )
  }
  if (!url) return <LoadingRows rows={2} />
  return (
    <iframe
      src={url}
      title="PDF preview"
      className="h-72 w-full rounded-lg border border-hairline"
    />
  )
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
}: {
  entry: FileEntry
  preview: FilePreview
  canWrite: boolean
  onOpen: (path: string) => void
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
