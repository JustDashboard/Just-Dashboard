"use client"

import { useEffect, useState } from "react"
import {
  ChevronLeft,
  ChevronRight,
  Clipboard,
  Download,
  Image as ImageIcon,
  MagnifyingGlass,
  MagnifyingGlassMinus,
  Music,
  Pencil,
} from "@/components/icons"
import { downloadUrl, get } from "@/lib/api"
import { bytes, relativeTime, timestamp } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { FileEntry, FilePreview } from "@/lib/types"
import { copyText } from "@/lib/clipboard"
import { Modal } from "@/components/modal"
import { Well } from "@/components/panel"
import { Button } from "@/components/ui/button"
import { IconAction } from "@/components/icon-action"
import { ErrorState, LoadingRows } from "@/components/state"
import { FileIcon } from "@/components/files/file-icon"
import { mediaKind, rawUrl } from "@/components/files/media"
import { ArchiveListing, PdfPreview, TextHead } from "@/components/files/preview-bits"

/**
 * The full-screen look at a file.
 *
 * Double-clicking a photograph used to open a code editor that refused it;
 * now it opens this, which is what a double click means in a photo library:
 * the picture as large as the window allows, arrows to walk the folder, a
 * click to see it pixel for pixel. A video plays, a PDF renders, and anything
 * else — a script, an archive, a binary — gets the same head or listing the
 * inspector shows, at a size that can actually be read. Space in the listing
 * is the quick look; Escape is the way out.
 *
 * Editing is a deliberate second step from here: the image editor and the
 * code editor are offered in the strip, and opening either closes this.
 */
export function MediaViewer({
  items,
  index,
  canWrite,
  onClose,
  onIndexChange,
  onEditImage,
  onOpenEditor,
}: {
  /** The files of the folder being browsed, in the order the listing shows them. */
  items: FileEntry[]
  index: number | null
  canWrite: boolean
  onClose: () => void
  onIndexChange: (index: number) => void
  onEditImage: (path: string) => void
  onOpenEditor: (path: string) => void
}) {
  const entry = index === null ? null : items[index]
  const open = entry !== undefined && entry !== null
  const kind = entry ? mediaKind(entry.name) : null
  const [fit, setFit] = useState(true)
  const [dimensions, setDimensions] = useState<{ w: number; h: number } | null>(null)

  const step = (delta: number) => {
    if (index === null) return
    const next = index + delta
    if (next >= 0 && next < items.length) onIndexChange(next)
  }

  useEffect(() => {
    if (!open) return
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "ArrowLeft") {
        event.preventDefault()
        step(-1)
      } else if (event.key === "ArrowRight") {
        event.preventDefault()
        step(1)
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, index, items.length])

  // The neighbours are fetched while this one is on screen, so an arrow
  // press lands on a picture rather than on a spinner.
  useEffect(() => {
    if (index === null) return
    for (const neighbour of [items[index - 1], items[index + 1]]) {
      if (neighbour && mediaKind(neighbour.name) === "image") {
        const image = new Image()
        image.src = rawUrl(neighbour.path, neighbour.modified)
      }
    }
  }, [index, items])

  return (
    <Modal
      open={open}
      onOpenChange={(next) => !next && onClose()}
      size="full"
      title={
        entry ? (
          <span className="flex min-w-0 items-center gap-2">
            <FileIcon entry={entry} className="size-4" />
            <span className="truncate">{entry.name}</span>
            {items.length > 1 && index !== null && (
              <span className="numeric shrink-0 text-xs font-normal text-muted-foreground">
                {index + 1} / {items.length}
              </span>
            )}
          </span>
        ) : (
          "File"
        )
      }
      description={entry?.path}
      actions={
        entry && (
          <>
            {items.length > 1 && (
              <>
                <IconAction label="Previous" disabled={index === 0} onClick={() => step(-1)}>
                  <ChevronLeft />
                </IconAction>
                <IconAction
                  label="Next"
                  disabled={index === items.length - 1}
                  onClick={() => step(1)}
                >
                  <ChevronRight />
                </IconAction>
              </>
            )}
            {kind === "image" && (
              <IconAction
                label={fit ? "Actual size" : "Fit to window"}
                onClick={() => setFit((v) => !v)}
              >
                {fit ? <MagnifyingGlass /> : <MagnifyingGlassMinus />}
              </IconAction>
            )}
            <IconAction label="Download" asChild>
              <a href={downloadUrl("/files/download", { path: entry.path })} download>
                <Download />
              </a>
            </IconAction>
            {kind === "image" && canWrite && (
              <Button size="sm" variant="outline" onClick={() => onEditImage(entry.path)}>
                <ImageIcon className="size-3.5" />
                Edit image
              </Button>
            )}
            {!kind && (
              <Button size="sm" variant="outline" onClick={() => onOpenEditor(entry.path)}>
                <Pencil className="size-3.5" />
                {canWrite ? "Open in editor" : "Open"}
              </Button>
            )}
          </>
        )
      }
      footer={
        entry && (
          <div className="flex min-w-0 flex-1 flex-wrap items-center gap-x-4 gap-y-1 text-hint text-muted-foreground">
            <span className="numeric">{bytes(entry.size)}</span>
            {dimensions && (
              <span className="numeric">
                {dimensions.w}×{dimensions.h}
              </span>
            )}
            <span title={timestamp(entry.modified)}>{relativeTime(entry.modified)}</span>
            <span>
              {entry.owner}:{entry.group}
            </span>
            <span className="min-w-0 flex-1 truncate font-mono" title={entry.path}>
              {entry.path}
            </span>
            <Button
              size="xs"
              variant="ghost"
              onClick={() => void copyText(entry.path, "Path copied")}
            >
              <Clipboard className="size-3" />
              Copy path
            </Button>
          </div>
        )
      }
      initialFocus="body"
      bodyClassName="flex min-h-0 flex-1 flex-col p-0"
    >
      {entry && (
        <Stage
          key={entry.path}
          entry={entry}
          fit={fit}
          onFit={setFit}
          onDimensions={setDimensions}
        />
      )}
    </Modal>
  )
}

function Stage({
  entry,
  fit,
  onFit,
  onDimensions,
}: {
  entry: FileEntry
  fit: boolean
  onFit: (fit: boolean) => void
  onDimensions: (d: { w: number; h: number } | null) => void
}) {
  const kind = mediaKind(entry.name)
  const src = rawUrl(entry.path, entry.modified)

  useEffect(() => {
    onDimensions(null)
  }, [entry.path, onDimensions])

  switch (kind) {
    case "image":
      return (
        <div
          className={cn(
            "flex min-h-0 flex-1 overflow-auto checkerboard",
            fit ? "items-center justify-center p-4" : "p-4",
          )}
          onClick={() => onFit(!fit)}
        >
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img
            src={src}
            alt={entry.name}
            draggable={false}
            onLoad={(e) =>
              onDimensions({ w: e.currentTarget.naturalWidth, h: e.currentTarget.naturalHeight })
            }
            className={cn(
              fit
                ? "max-h-full max-w-full cursor-zoom-in object-contain"
                : "m-auto max-w-none cursor-zoom-out",
            )}
          />
        </div>
      )
    case "video":
      return (
        <div className="flex min-h-0 flex-1 items-center justify-center bg-black p-2">
          <video src={src} controls autoPlay className="max-h-full max-w-full" />
        </div>
      )
    case "audio":
      return (
        <div className="flex min-h-0 flex-1 flex-col items-center justify-center gap-6 p-6">
          <Music className="size-16 text-muted-foreground" aria-hidden />
          <audio src={src} controls autoPlay className="w-full max-w-xl" />
        </div>
      )
    case "pdf":
      return (
        <div className="flex min-h-0 flex-1 flex-col p-3">
          <PdfPreview path={entry.path} modified={entry.modified} className="min-h-0 flex-1" />
        </div>
      )
    default:
      return <OtherStage entry={entry} />
  }
}

/** Anything the browser cannot draw as itself: the head, the listing, or a note. */
function OtherStage({ entry }: { entry: FileEntry }) {
  const [preview, setPreview] = useState<FilePreview>()
  const [error, setError] = useState<Error>()

  useEffect(() => {
    const controller = new AbortController()
    get<FilePreview>("/files/preview", { path: entry.path }, controller.signal)
      .then(setPreview)
      .catch((err) => !controller.signal.aborted && setError(err))
    return () => controller.abort()
  }, [entry.path])

  if (error) return <ErrorState error={error} className="m-4" />
  if (!preview) return <LoadingRows className="p-4" />

  if (preview.kind === "text") {
    return (
      <div className="flex min-h-0 flex-1 flex-col gap-2 p-3">
        <Well className="min-h-0 flex-1 p-0 whitespace-pre">
          <TextHead text={preview.text ?? ""} />
        </Well>
        <p className="shrink-0 text-hint text-muted-foreground">
          {preview.truncated
            ? `First ${preview.lines} lines of ${bytes(preview.size)}. Open it in the editor for the rest.`
            : `${preview.lines} line${preview.lines === 1 ? "" : "s"}`}
        </p>
      </div>
    )
  }
  if (preview.kind === "archive") {
    return (
      <div className="flex min-h-0 flex-1 flex-col p-3">
        <ArchiveListing preview={preview} />
      </div>
    )
  }
  return (
    <div className="flex min-h-0 flex-1 flex-col items-center justify-center gap-3 p-6 text-center">
      <FileIcon entry={entry} detail className="size-20" />
      <p className="text-body">
        {bytes(preview.size)} of binary data. Download it to open it locally.
      </p>
    </div>
  )
}
