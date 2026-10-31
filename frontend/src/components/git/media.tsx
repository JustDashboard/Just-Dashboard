"use client"

import { createContext, useContext, useEffect, useState } from "react"
import { Dialog as DialogPrimitive } from "radix-ui"
import { ChevronLeft, ChevronRight, Cross, External } from "@/components/icons"
import { forgeMediaUrl } from "@/hooks/use-github"
import { isForgeMedia } from "@/lib/markdown"
import { cn } from "@/lib/utils"
import { Button } from "@/components/ui/button"

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
    if (index >= 0) {
      setZoomed(false)
      setAt(index)
    }
  }
  // Full size, panned: a wide screenshot fitted to a phone is too small to read.
  const [zoomed, setZoomed] = useState(false)
  const current = at === undefined ? undefined : media[at]
  const step = (by: number) => {
    setZoomed(false)
    setAt((index) =>
      index === undefined ? index : Math.min(media.length - 1, Math.max(0, index + by)),
    )
  }

  useEffect(() => {
    if (at === undefined) return
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "ArrowLeft") step(-1)
      if (event.key === "ArrowRight") step(1)
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
    // `step` only reads through the setter.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [at === undefined])

  return (
    <Lightbox.Provider value={open}>
      {children}
      <DialogPrimitive.Root open={!!current} onOpenChange={(next) => !next && setAt(undefined)}>
        <DialogPrimitive.Portal>
          <DialogPrimitive.Overlay className="fixed inset-0 z-50 bg-black/95" />
          <DialogPrimitive.Content
            aria-describedby={undefined}
            className="fixed inset-0 z-50 flex flex-col outline-none"
          >
            <div className="flex h-12 shrink-0 items-center gap-2 px-3 text-white">
              <DialogPrimitive.Title className="min-w-0 flex-1 truncate text-body">
                {current?.alt || "Image"}
              </DialogPrimitive.Title>
              {media.length > 1 && at !== undefined && (
                <span className="numeric shrink-0 text-hint text-white/70">
                  {at + 1} of {media.length}
                </span>
              )}
              {current && (
                <Button
                  size="icon-sm"
                  variant="ghost"
                  asChild
                  className="text-white hover:bg-white/10 hover:text-white"
                >
                  <a
                    href={current.src}
                    target="_blank"
                    rel="noreferrer noopener"
                    aria-label="Open the original"
                  >
                    <External className="size-4" />
                  </a>
                </Button>
              )}
              <DialogPrimitive.Close asChild>
                <Button
                  size="icon-sm"
                  variant="ghost"
                  aria-label="Close"
                  className="text-white hover:bg-white/10 hover:text-white"
                >
                  <Cross className="size-4" />
                </Button>
              </DialogPrimitive.Close>
            </div>
            <div className="relative min-h-0 flex-1">
              <div
                className={cn(
                  "absolute inset-0 flex px-2 pb-2",
                  zoomed ? "overflow-auto" : "items-center justify-center",
                )}
              >
                {current && (
                  // eslint-disable-next-line @next/next/no-img-element -- our own route, already sized
                  <img
                    src={forgeMediaUrl(current.src)}
                    alt={current.alt}
                    onClick={() => setZoomed((value) => !value)}
                    className={cn(
                      zoomed
                        ? "m-auto max-w-none cursor-zoom-out"
                        : "max-h-full max-w-full cursor-zoom-in object-contain",
                    )}
                  />
                )}
              </div>
              {media.length > 1 && at !== undefined && (
                <>
                  <Button
                    size="icon"
                    variant="ghost"
                    aria-label="Previous image"
                    disabled={at === 0}
                    onClick={() => step(-1)}
                    className="absolute top-1/2 left-2 -translate-y-1/2 bg-black/50 text-white hover:bg-black/70 hover:text-white"
                  >
                    <ChevronLeft className="size-5" />
                  </Button>
                  <Button
                    size="icon"
                    variant="ghost"
                    aria-label="Next image"
                    disabled={at === media.length - 1}
                    onClick={() => step(1)}
                    className="absolute top-1/2 right-2 -translate-y-1/2 bg-black/50 text-white hover:bg-black/70 hover:text-white"
                  >
                    <ChevronRight className="size-5" />
                  </Button>
                </>
              )}
            </div>
          </DialogPrimitive.Content>
        </DialogPrimitive.Portal>
      </DialogPrimitive.Root>
    </Lightbox.Provider>
  )
}
