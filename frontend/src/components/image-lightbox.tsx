"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { Dialog as DialogPrimitive } from "radix-ui"
import { ChevronLeft, ChevronRight, Cross, Download, External } from "@/components/icons"
import { Spinner } from "@/components/state"
import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"

/** One picture the lightbox can show. */
export interface LightboxImage {
  /** The picture, as large as there is of it. */
  src: string
  alt: string
  /**
   * A small copy already on the page, drawn at once while `src` arrives, and in
   * its place if `src` cannot be had.
   */
  placeholder?: string
  /** Its size in pixels, when known: room is made for it before it arrives. */
  width?: number
  height?: number
  /** An address to open it on its own, in a tab. */
  original?: string
  /** A file name to save it under, which offers Download. */
  download?: string
}

const STRIP_BUTTON = "text-white hover:bg-white/10 hover:text-white"

/**
 * A picture as large as the window allows, over a dark ground: a screenshot in a
 * 56-pixel tile, or a 400-pixel column, is a thumbnail of what was meant. Arrows
 * (the keys, the buttons, or a swipe) walk the rest; a click on a picture larger
 * than the window shows it pixel for pixel, panned, and another click fits it
 * again. Escape, the cross or the ground closes it, and focus goes back to the
 * picture that opened it.
 *
 * It opens with the small copy the page already drew, at the size the picture
 * will take, and the picture fades in over it as it arrives, so nothing jumps.
 * One that cannot be had leaves its small copy, and says so.
 */
export function ImageLightbox({
  images,
  at,
  onAt,
  onClose,
  missingNote = "Only a small copy of this image is kept.",
}: {
  images: LightboxImage[]
  /** The one shown, or none for closed. */
  at: number | undefined
  onAt: (index: number) => void
  onClose: () => void
  missingNote?: string
}) {
  // The one shown last stays drawn while the lightbox fades out.
  const [last, setLast] = useState(at)
  if (at !== undefined && at !== last) setLast(at)
  const open = at !== undefined && at < images.length
  const index = open ? at : last
  const current = index === undefined ? undefined : images[index]
  const many = images.length > 1 && index !== undefined
  const step = (by: number) => {
    if (!open) return
    const next = at + by
    if (next >= 0 && next < images.length) onAt(next)
  }
  // The arrow keys are read where the dialog is, not on the window, so a key
  // pressed in a field behind it is never taken.
  const onKeyDown = (event: React.KeyboardEvent) => {
    if (event.key === "ArrowLeft") {
      event.preventDefault()
      step(-1)
    } else if (event.key === "ArrowRight") {
      event.preventDefault()
      step(1)
    }
  }
  // A swipe on a touch screen walks them too, and is not a press on the ground.
  const swipe = useRef<{ x: number; y: number } | null>(null)
  const swiped = useRef(false)
  // What had focus when it opened, the thumbnail pressed, has it back after: the
  // dialog has no trigger of its own to return to.
  const opener = useRef<HTMLElement | null>(null)

  return (
    <DialogPrimitive.Root open={open} onOpenChange={(next) => !next && onClose()}>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay
          data-slot="lightbox-overlay"
          className="fixed inset-0 z-50 bg-black/95 backdrop-blur-sm duration-200 data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:animate-in data-[state=open]:fade-in-0"
        />
        <DialogPrimitive.Content
          data-slot="lightbox"
          aria-describedby={undefined}
          onKeyDown={onKeyDown}
          onOpenAutoFocus={() => {
            opener.current =
              document.activeElement instanceof HTMLElement ? document.activeElement : null
          }}
          onCloseAutoFocus={(event) => {
            const back = opener.current
            opener.current = null
            if (back?.isConnected) {
              event.preventDefault()
              back.focus({ preventScroll: true })
            }
          }}
          className="group/lightbox fixed inset-0 z-50 flex flex-col duration-200 outline-none data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:animate-in data-[state=open]:fade-in-0"
        >
          <div className="flex h-12 shrink-0 items-center gap-2 px-3 text-white">
            <DialogPrimitive.Title className="min-w-0 flex-1 truncate text-body">
              {current?.alt || "Image"}
            </DialogPrimitive.Title>
            {many && (
              <span data-slot="lightbox-count" className="numeric shrink-0 text-hint text-white/70">
                {index + 1} of {images.length}
              </span>
            )}
            {current?.download && (
              <Button
                size="icon-sm"
                variant="ghost"
                asChild
                className={STRIP_BUTTON}
                title="Download"
              >
                <a href={current.src} download={current.download} aria-label="Download">
                  <Download className="size-4" />
                </a>
              </Button>
            )}
            {current?.original && (
              <Button
                size="icon-sm"
                variant="ghost"
                asChild
                className={STRIP_BUTTON}
                title="Open the original"
              >
                <a
                  href={current.original}
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
                type="button"
                size="icon-sm"
                variant="ghost"
                aria-label="Close"
                title="Close (Esc)"
                className={STRIP_BUTTON}
              >
                <Cross className="size-4" />
              </Button>
            </DialogPrimitive.Close>
          </div>
          <div
            className="relative min-h-0 flex-1"
            onPointerDown={(event) => {
              if (event.pointerType === "touch")
                swipe.current = { x: event.clientX, y: event.clientY }
            }}
            onPointerUp={(event) => {
              const from = swipe.current
              swipe.current = null
              if (!from) return
              const dx = event.clientX - from.x
              if (Math.abs(dx) > 56 && Math.abs(dx) > 2 * Math.abs(event.clientY - from.y)) {
                swiped.current = true
                step(dx < 0 ? 1 : -1)
              }
            }}
            onPointerCancel={() => (swipe.current = null)}
            onClick={(event) => {
              // The dark ground round the picture closes it, as a press outside a
              // dialog does.
              if (swiped.current) {
                swiped.current = false
                return
              }
              const target = event.target as HTMLElement
              if (!target.closest("img, button, a, [data-slot='lightbox-note']")) onClose()
            }}
          >
            {current && index !== undefined && (
              <Picture key={`${index}:${current.src}`} image={current} missingNote={missingNote} />
            )}
            {many && (
              <>
                <Button
                  type="button"
                  size="icon"
                  variant="ghost"
                  aria-label="Previous image"
                  disabled={index === 0}
                  onClick={() => step(-1)}
                  className="absolute top-1/2 left-2 -translate-y-1/2 bg-black/50 text-white hover:bg-black/70 hover:text-white disabled:opacity-0"
                >
                  <ChevronLeft className="size-5" />
                </Button>
                <Button
                  type="button"
                  size="icon"
                  variant="ghost"
                  aria-label="Next image"
                  disabled={index === images.length - 1}
                  onClick={() => step(1)}
                  className="absolute top-1/2 right-2 -translate-y-1/2 bg-black/50 text-white hover:bg-black/70 hover:text-white disabled:opacity-0"
                >
                  <ChevronRight className="size-5" />
                </Button>
              </>
            )}
          </div>
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  )
}

/**
 * One picture on the stage. Its box is the size the picture will be drawn at,
 * worked out from its size before it arrives — fitted to the stage, never past
 * its own pixels — so the small copy fills that box and the picture lands on it.
 */
function Picture({ image, missingNote }: { image: LightboxImage; missingNote: string }) {
  const [state, setState] = useState<"loading" | "ready" | "missing">("loading")
  const [zoomed, setZoomed] = useState(false)
  // Whether the picture is larger than it is drawn: only then is there a
  // pixel-for-pixel to zoom to.
  const [larger, setLarger] = useState(false)
  const picture = useRef<HTMLImageElement>(null)
  const sized = !!image.width && !!image.height
  const shown = state === "missing" ? image.placeholder : image.src

  // Read while it is fitted; zoomed, the fitted picture is not on the stage.
  const measure = useCallback(() => {
    const img = picture.current
    if (!img || img.naturalWidth === 0) return
    setLarger(img.naturalWidth > img.clientWidth + 1 || img.naturalHeight > img.clientHeight + 1)
  }, [])
  useEffect(() => {
    window.addEventListener("resize", measure)
    return () => window.removeEventListener("resize", measure)
  }, [measure])

  const toggle = () => {
    if (zoomed || larger) setZoomed((value) => !value)
  }
  const cursor = zoomed ? "cursor-zoom-out" : larger ? "cursor-zoom-in" : "cursor-default"

  if (zoomed && shown) {
    return (
      <div className="absolute inset-0 flex overflow-auto px-2 pb-2" data-slot="lightbox-stage">
        {/* eslint-disable-next-line @next/next/no-img-element -- a data address or our own route, already sized */}
        <img
          src={shown}
          alt={image.alt}
          onClick={toggle}
          className={cn("m-auto max-w-none rounded-sm ring-1 ring-white/10", cursor)}
        />
      </div>
    )
  }
  return (
    <div
      data-slot="lightbox-stage"
      className="absolute inset-0 flex items-center justify-center px-2 pb-2"
      style={{ containerType: "size" }}
    >
      <div
        data-slot="lightbox-picture"
        data-state={state}
        className={cn(
          "relative animate-in duration-200 ease-out fade-in-0 zoom-in-95",
          !sized && "flex size-full items-center justify-center",
        )}
        style={
          sized
            ? {
                // Fitted to the stage and never past its own pixels, as
                // object-fit: contain would, before there is a picture to fit.
                width: `min(${image.width}px, 100cqw, calc(100cqh * ${image.width! / image.height!}))`,
                aspectRatio: `${image.width} / ${image.height}`,
              }
            : undefined
        }
      >
        {sized && image.placeholder && state === "loading" && (
          // eslint-disable-next-line @next/next/no-img-element -- the small copy the page drew
          <img
            src={image.placeholder}
            alt=""
            aria-hidden
            data-slot="lightbox-placeholder"
            className="absolute inset-0 size-full rounded-sm object-contain"
          />
        )}
        {shown && (
          // eslint-disable-next-line @next/next/no-img-element -- a data address or our own route, already sized
          <img
            ref={picture}
            src={shown}
            alt={image.alt}
            onLoad={() => {
              setState((now) => (now === "loading" ? "ready" : now))
              measure()
            }}
            onError={() => setState("missing")}
            onClick={toggle}
            className={cn(
              // A hairline of light round it: a dark screenshot on the dark ground
              // still has an edge.
              "relative rounded-sm ring-1 ring-white/10 transition-opacity duration-300 ease-out",
              sized ? "size-full object-contain" : "max-h-full max-w-full object-contain",
              state === "loading" && sized && "opacity-0",
              cursor,
            )}
          />
        )}
        {state === "loading" && (
          // Centred by the wrapper: the spinner's own transform is its turn.
          <span className="pointer-events-none absolute inset-0 grid place-items-center">
            <Spinner className="size-5 text-white/70" />
          </span>
        )}
      </div>
      {state === "missing" && (
        <p
          role="status"
          data-slot="lightbox-note"
          className="absolute bottom-3 left-1/2 -translate-x-1/2 rounded-md bg-black/70 px-2.5 py-1 text-hint text-white/80"
        >
          {image.placeholder ? missingNote : "This image could not be opened."}
        </p>
      )}
    </div>
  )
}
