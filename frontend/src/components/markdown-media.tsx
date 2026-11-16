"use client"

import { createContext, useContext, useState } from "react"
import { External } from "@/components/icons"
import { forgeMediaUrl } from "@/hooks/use-github"
import { isForgeMedia } from "@/lib/markdown"
import { cn } from "@/lib/utils"
import { ImageLightbox } from "@/components/image-lightbox"

type Media = { src: string; alt: string }

const Lightbox = createContext<((src: string) => void) | null>(null)

const VIDEO = /\.(mp4|webm|mov)(\?|$)/i

/**
 * One picture or video from a description, the way GitHub would have drawn it.
 *
 * Only GitHub's media hosts are drawn, through this server (`forgeMediaUrl`),
 * and everything else, or a file GitHub would not hand over, stays the link it
 * was written as: the words are never worse than they were. An upload's address
 * does not say what it is, so it is tried as a picture and then as a video.
 */
export function MediaImage({ src, alt, fill }: Media & { fill?: boolean }) {
  const open = useContext(Lightbox)
  const [kind, setKind] = useState<"image" | "video" | "link">(() =>
    !isForgeMedia(src) ? "link" : VIDEO.test(src) ? "video" : "image",
  )
  const [loaded, setLoaded] = useState(false)

  if (kind === "link") {
    return (
      <a
        href={src}
        target="_blank"
        rel="noreferrer noopener"
        className="inline-flex max-w-full items-center gap-1 text-foreground underline decoration-muted-foreground/60 underline-offset-2 hover:decoration-foreground"
      >
        <External aria-hidden className="size-3.5 shrink-0" />
        <span className="truncate">{alt || "Open the image"}</span>
      </a>
    )
  }
  if (kind === "video") {
    return (
      <video
        controls
        preload="metadata"
        aria-label={alt || "Video"}
        src={forgeMediaUrl(src)}
        onError={() => setKind("link")}
        className="block max-h-[32rem] w-full rounded-lg border border-hairline bg-surface-sunken"
      />
    )
  }
  return (
    <button
      type="button"
      aria-label={alt ? `Enlarge ${alt}` : "Enlarge the image"}
      onClick={() => open?.(src)}
      className={cn(
        "inline-block max-w-full cursor-zoom-in overflow-hidden rounded-lg border border-hairline align-top focus-ring",
        !loaded && "min-h-32 w-full animate-pulse bg-surface-sunken",
        fill && "w-full",
      )}
    >
      {/* eslint-disable-next-line @next/next/no-img-element -- our own route, already sized */}
      <img
        src={forgeMediaUrl(src)}
        alt={alt}
        loading="lazy"
        onLoad={() => setLoaded(true)}
        onError={() => setKind(VIDEO.test(src) ? "link" : "video")}
        className={cn("block h-auto max-h-[32rem] max-w-full", fill && "w-full object-contain")}
      />
    </button>
  )
}

/** Pictures alone in a paragraph: side by side where there is room, as a before and an after are. */
export function MediaGrid({ children, count }: { children: React.ReactNode; count: number }) {
  return (
    <div className="@container">
      <div className={cn("grid gap-2", count > 1 && "@lg:grid-cols-2")}>{children}</div>
    </div>
  )
}

/**
 * Enlarging a picture, and stepping through the rest of the body from it. A
 * screenshot in a 400px column is a thumbnail of what the author meant to show.
 */
export function MediaLightbox({ media, children }: { media: Media[]; children: React.ReactNode }) {
  const [at, setAt] = useState<number>()
  const open = (src: string) => {
    const index = media.findIndex((item) => item.src === src)
    if (index >= 0) setAt(index)
  }
  return (
    <Lightbox.Provider value={open}>
      {children}
      <ImageLightbox
        images={media.map((item) => ({
          src: forgeMediaUrl(item.src),
          alt: item.alt,
          original: item.src,
        }))}
        at={at}
        onAt={setAt}
        onClose={() => setAt(undefined)}
      />
    </Lightbox.Provider>
  )
}
