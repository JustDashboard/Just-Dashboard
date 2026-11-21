"use client"

import { useEffect, useRef, useState } from "react"
import {
  ChevronLeft,
  ChevronRight,
  Clipboard,
  Download,
  Image as ImageIcon,
  MagnifyingGlass,
  MagnifyingGlassMinus,
  Music,
  Pause,
  Pencil,
  Play,
} from "@/components/icons"
import { downloadUrl, get } from "@/lib/api"
import { bytes, relativeTime, timestamp } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { FileEntry, FilePreview } from "@/lib/types"
import { copyText } from "@/lib/clipboard"
import { Modal } from "@/components/modal"
import { Button } from "@/components/ui/button"
import { IconAction } from "@/components/icon-action"
import { ErrorState } from "@/components/state"
import { LoadingRows } from "@/components/loading"
import { FileIcon } from "@/components/file-icon"
import { mediaKind, rawUrl } from "@/components/files/media"
import { ArchiveListing, PdfPreview } from "@/components/files/preview-bits"
import { TextStage } from "@/components/files/text-contents"
import { GalleryStrip } from "@/components/files/gallery-strip"
import { PlayableVideo } from "@/components/files/playable-video"
import { PlayerControls, useAudioQueue } from "@/components/files/audio-queue"
import {
  aheadOf,
  clockTime,
  hasSlideshow,
  nextPicture,
  positionWords,
  swipeStep,
} from "@/components/files/gallery"

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
 * A folder of pictures reads as a gallery: how far through it is, in words; a
 * strip of the folder under the stage to jump by; a swipe on a phone; the next
 * pictures fetched ahead; and a slideshow. Audio plays through the page's queue
 * (`audio-queue.tsx`), so it carries on after this closes.
 *
 * Editing is a deliberate second step from here: the image editor and the
 * code editor are offered in the strip, and opening either closes this.
 */

/** How long the slideshow holds each picture. */
const SLIDE_MS = 4000
export function MediaViewer({
  items,
  index,
  canWrite,
  onClose,
  onIndexChange,
  onEditImage,
  onOpenEditor,
  onOpenPath,
}: {
  /** The files of the folder being browsed, in the order the listing shows them. */
  items: FileEntry[]
  index: number | null
  canWrite: boolean
  onClose: () => void
  onIndexChange: (index: number) => void
  onEditImage: (path: string) => void
  onOpenEditor: (path: string) => void
  /** A link in a rendered file to another file on this server. */
  onOpenPath?: (path: string, folder: boolean) => void
}) {
  const entry = index === null ? null : items[index]
  const open = entry !== undefined && entry !== null
  const kind = entry ? mediaKind(entry.name) : null
  const [fit, setFit] = useState(true)
  const [dimensions, setDimensions] = useState<{ w: number; h: number } | null>(null)
  const [showing, setShowing] = useState(false)
  const [wasOpen, setWasOpen] = useState(open)
  if (open !== wasOpen) {
    setWasOpen(open)
    if (!open) setShowing(false)
  }
  const slideshow = showing && open && hasSlideshow(items)
  const swipe = useRef<{ x: number; y: number } | null>(null)

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

  // The next pictures are fetched while this one is on screen, so an arrow
  // press, a swipe or the slideshow lands on a picture rather than a spinner.
  useEffect(() => {
    if (index === null) return
    for (const at of aheadOf(items, index)) {
      const image = new Image()
      image.src = rawUrl(items[at].path, items[at].modified)
    }
  }, [index, items])

  // The slideshow walks the folder's pictures and stops at the last; it
  // stops too when the viewer closes, and does not start again on reopening.
  // The page hands a new callback on every render; the timer must not restart on each.
  const advance = useRef(onIndexChange)
  useEffect(() => {
    advance.current = onIndexChange
  }, [onIndexChange])
  useEffect(() => {
    if (!slideshow || index === null) return
    const timer = window.setTimeout(() => {
      const next = nextPicture(items, index)
      if (next === null) setShowing(false)
      else advance.current(next)
    }, SLIDE_MS)
    return () => window.clearTimeout(timer)
  }, [slideshow, index, items])

  const position = index === null ? null : positionWords(index, items)

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
            {position && (
              <span
                data-gallery-position
                className="numeric shrink-0 text-xs font-normal text-muted-foreground"
              >
                {position}
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
                {/* A phone walks by swipe and the strip; its header has no room for these. */}
                <IconAction
                  label="Previous"
                  disabled={index === 0}
                  onClick={() => step(-1)}
                  className="max-sm:hidden"
                >
                  <ChevronLeft />
                </IconAction>
                <IconAction
                  label="Next"
                  disabled={index === items.length - 1}
                  onClick={() => step(1)}
                  className="max-sm:hidden"
                >
                  <ChevronRight />
                </IconAction>
              </>
            )}
            {hasSlideshow(items) && (
              <IconAction
                label={slideshow ? "Stop the slideshow" : "Slideshow"}
                aria-pressed={slideshow}
                onClick={() => {
                  // Started on something that is not a picture, it begins at the next one.
                  if (!slideshow && kind !== "image" && index !== null) {
                    const next = nextPicture(items, index)
                    if (next !== null) onIndexChange(next)
                  }
                  setShowing(!slideshow)
                }}
              >
                {slideshow ? <Pause /> : <Play />}
              </IconAction>
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
                {/* A phone's header keeps its room for the picture's name. */}
                <span className="max-sm:sr-only">Edit image</span>
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
      {entry && index !== null && (
        <>
          {/* A finger's sideways swipe steps through the folder, as on a phone's own gallery. */}
          <div
            className={cn("flex min-h-0 flex-1 flex-col", fit && "touch-pan-y")}
            onPointerDown={(event) => {
              swipe.current =
                event.pointerType === "touch" ? { x: event.clientX, y: event.clientY } : null
            }}
            onPointerUp={(event) => {
              const start = swipe.current
              swipe.current = null
              if (!start) return
              const delta = swipeStep(event.clientX - start.x, event.clientY - start.y)
              if (delta) step(delta)
            }}
          >
            <Stage
              key={entry.path}
              entry={entry}
              items={items}
              fit={fit}
              onFit={setFit}
              onDimensions={setDimensions}
              onOpenPath={onOpenPath}
            />
          </div>
          {items.length > 1 && <GalleryStrip items={items} index={index} onPick={onIndexChange} />}
        </>
      )}
    </Modal>
  )
}

function Stage({
  entry,
  items,
  fit,
  onFit,
  onDimensions,
  onOpenPath,
}: {
  entry: FileEntry
  items: FileEntry[]
  fit: boolean
  onFit: (fit: boolean) => void
  onDimensions: (d: { w: number; h: number } | null) => void
  onOpenPath?: (path: string, folder: boolean) => void
}) {
  const kind = mediaKind(entry.name)
  const src = rawUrl(entry.path, entry.modified)

  useEffect(() => {
    onDimensions(null)
  }, [entry.path, onDimensions])

  switch (kind) {
    case "image":
      return (
        // eslint-disable-next-line jsx-a11y/click-events-have-key-events, jsx-a11y/no-static-element-interactions -- a press switches between fitted and full size, which is the pointer's for now (audit FE-15, left open)
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
          <PlayableVideo
            path={entry.path}
            name={entry.name}
            modified={entry.modified}
            autoPlay
            className="rounded-lg bg-card"
            videoClassName="max-h-full max-w-full"
          />
        </div>
      )
    case "audio":
      return <AudioStage entry={entry} items={items} />
    case "pdf":
      return (
        <div className="flex min-h-0 flex-1 flex-col p-3">
          <PdfPreview path={entry.path} modified={entry.modified} className="min-h-0 flex-1" />
        </div>
      )
    default:
      return <OtherStage entry={entry} onOpenPath={onOpenPath} />
  }
}

/**
 * A track: arriving on it plays it through the page's queue, with the rest of
 * the folder after it, and the stage shows what is playing and where it is.
 */
function AudioStage({ entry, items }: { entry: FileEntry; items: FileEntry[] }) {
  const queue = useAudioQueue()
  const playFrom = queue?.playFrom
  useEffect(() => {
    playFrom?.(entry, items)
    // Only on arriving at a track: a later change to the listing is not a new press.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [entry.path])
  const track = queue?.tracks[queue.index]
  const here = track?.path === entry.path
  return (
    <div className="flex min-h-0 flex-1 flex-col items-center justify-center gap-4 p-6 text-center">
      <Music className="size-16 text-muted-foreground" aria-hidden />
      {queue && here ? (
        <>
          <PlayerControls queue={queue} large />
          <p className="numeric text-hint text-muted-foreground">
            {clockTime(queue.position)}
            {queue.duration > 0 && ` of ${clockTime(queue.duration)}`}
            {queue.tracks.length > 1 && ` · track ${queue.index + 1} of ${queue.tracks.length}`}
          </p>
        </>
      ) : queue ? (
        <Button size="sm" variant="outline" onClick={() => queue.playFrom(entry, items)}>
          <Play className="size-3.5" />
          Play
        </Button>
      ) : (
        <audio src={rawUrl(entry.path, entry.modified)} controls className="w-full max-w-xl" />
      )}
    </div>
  )
}

/** Anything the browser cannot draw as itself: the head, the listing, or a note. */
function OtherStage({
  entry,
  onOpenPath,
}: {
  entry: FileEntry
  onOpenPath?: (path: string, folder: boolean) => void
}) {
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

  if (preview.kind === "text") return <TextStage preview={preview} onOpenPath={onOpenPath} />
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
