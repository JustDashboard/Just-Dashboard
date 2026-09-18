"use client"

import { useEffect, useRef, useState } from "react"
import { Play } from "@/components/icons"
import { cn } from "@/lib/utils"
import type { FileEntry } from "@/lib/types"
import { FileIcon } from "@/components/files/file-icon"
import { rawUrl, thumbnailKind } from "@/components/files/media"

/**
 * What a file looks like, at the size of a row or a tile.
 *
 * A picture is drawn as itself and a video as its first frame, so a folder of
 * screenshots or recordings is recognisable without opening anything — which
 * is the whole of what a file manager is for when the files are media. Every
 * other kind keeps its category glyph.
 *
 * Nothing here is fetched until it is on screen. Images use the browser's own
 * lazy loading; a video element is not even mounted until the tile scrolls
 * into view, because a mounted `<video preload="metadata">` asks the server
 * for its header at once, and a folder of two hundred recordings would open
 * two hundred range requests for rows nobody has scrolled to.
 *
 * `hoverPlay` is the one flourish: a tile's video plays, muted, while the
 * pointer rests on it — the way a photo library previews a clip — and returns
 * to its first frame when the pointer leaves.
 */
export function Thumbnail({
  entry,
  size,
  hoverPlay,
  className,
}: {
  entry: FileEntry
  size: "row" | "sm" | "md" | "lg"
  hoverPlay?: boolean
  className?: string
}) {
  const [broken, setBroken] = useState(false)
  const kind = broken ? null : thumbnailKind(entry)
  const box =
    size === "row"
      ? "size-7 rounded-sm"
      : size === "sm"
        ? "h-16 w-full rounded-md"
        : size === "lg"
          ? "h-32 w-full rounded-md"
          : "h-24 w-full rounded-md"
  const glyph =
    size === "row" ? "size-4" : size === "sm" ? "size-8" : size === "lg" ? "size-14" : "size-11"

  return (
    <span
      className={cn(
        "relative flex shrink-0 items-center justify-center overflow-hidden",
        box,
        kind && "border border-hairline checkerboard",
        className,
      )}
    >
      {kind === "image" && (
        // eslint-disable-next-line @next/next/no-img-element
        <img
          src={rawUrl(entry.path, entry.modified)}
          alt=""
          loading="lazy"
          decoding="async"
          draggable={false}
          onError={() => setBroken(true)}
          className={cn(
            "max-h-full max-w-full",
            size === "row" ? "size-full object-cover" : "object-contain",
          )}
        />
      )}
      {kind === "video" && (
        <VideoPoster entry={entry} hoverPlay={hoverPlay} onBroken={() => setBroken(true)} />
      )}
      {!kind && <FileIcon entry={entry} className={glyph} badgeClassName="bg-background" />}
    </span>
  )
}

function VideoPoster({
  entry,
  hoverPlay,
  onBroken,
}: {
  entry: FileEntry
  hoverPlay?: boolean
  onBroken: () => void
}) {
  const ref = useRef<HTMLSpanElement>(null)
  const video = useRef<HTMLVideoElement>(null)
  const visible = useVisible(ref)

  return (
    <span
      ref={ref}
      className="flex size-full items-center justify-center bg-black"
      onMouseEnter={() => {
        if (!hoverPlay) return
        void video.current?.play().catch(() => undefined)
      }}
      onMouseLeave={() => {
        if (!hoverPlay || !video.current) return
        video.current.pause()
        video.current.currentTime = 0.1
      }}
    >
      {visible && (
        <video
          ref={video}
          src={rawUrl(entry.path, entry.modified)}
          muted
          playsInline
          loop
          preload="metadata"
          draggable={false}
          onLoadedMetadata={(e) => {
            // Some browsers paint nothing until a seek lands on a frame, so a
            // poster that is still a black box after the header arrives asks
            // for the first frame explicitly.
            if (e.currentTarget.readyState < 2) e.currentTarget.currentTime = 0.1
          }}
          onError={onBroken}
          className="max-h-full max-w-full object-contain"
        />
      )}
      <Play
        aria-hidden
        className="absolute right-1 bottom-1 size-3.5 text-white/90 drop-shadow-[0_0_2px_rgba(0,0,0,0.9)]"
      />
    </span>
  )
}

/** True once the element has scrolled within a screen of the viewport. */
function useVisible(ref: React.RefObject<HTMLElement | null>) {
  // A browser with no observer shows everything at once rather than nothing.
  const [visible, setVisible] = useState(() => typeof IntersectionObserver === "undefined")
  useEffect(() => {
    const el = ref.current
    if (!el || visible) return
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((e) => e.isIntersecting)) {
          setVisible(true)
          observer.disconnect()
        }
      },
      { rootMargin: "320px" },
    )
    observer.observe(el)
    return () => observer.disconnect()
  }, [ref, visible])
  return visible
}
