"use client"

import { useEffect, useRef, useState, type RefObject } from "react"
import { External } from "@/components/icons"
import { API_BASE } from "@/lib/api"
import { cn } from "@/lib/utils"
import { deploymentURL, hostOf, WorkloadMark } from "@/components/deploy/vocabulary"
import type { DeploymentSummary } from "@/lib/types"

/** The width the site is laid out at before it is shrunk into the tile. */
const DESKTOP_WIDTH = 1280
/**
 * The tile's screen: sixteen by ten, the shape of the laptop the site is
 * being looked at on. It is also what keeps the tile the height of the
 * wiring column beside it — at four by three the preview stood twice as
 * tall as the facts it was meant to sit level with.
 */
const ASPECT = 16 / 10

/**
 * The one framed block on the overview: a tile that is the website, seen
 * from across the room. The site is laid out at desktop width inside a frame
 * and scaled down to fit, the way Vercel's deployment thumbnail is a whole
 * page made small, and the tile itself is one link that opens the site — no
 * controls of its own, because a preview is something you look at and then
 * go to. A project without a public address, or without a release yet,
 * shows the same tile with nothing behind the glass.
 */
export function SitePreview({ deployment }: { deployment: DeploymentSummary }) {
  const url = deploymentURL(deployment.endpoint)
  const live = Boolean(url && deployment.liveReleaseId)
  const screen = useRef<HTMLDivElement>(null)
  const width = useWidth(screen)
  const scale = width / DESKTOP_WIDTH

  const tile = (
    <>
      <div className="flex h-8 items-center gap-3 border-b border-hairline bg-surface-header px-3">
        <span aria-hidden="true" className="flex shrink-0 gap-1">
          <span className="size-1.5 rounded-full bg-muted-foreground/40" />
          <span className="size-1.5 rounded-full bg-muted-foreground/40" />
          <span className="size-1.5 rounded-full bg-muted-foreground/40" />
        </span>
        <span className="min-w-0 flex-1 truncate text-center font-mono text-hint text-muted-foreground">
          {url ? hostOf(url) : deployment.name}
        </span>
        {url && (
          <span className="inline-flex shrink-0 items-center gap-1 text-hint text-muted-foreground transition-colors group-hover:text-foreground">
            Open <External aria-hidden className="size-3" />
          </span>
        )}
      </div>
      <div
        ref={screen}
        className="relative w-full overflow-hidden bg-background"
        style={{ aspectRatio: `${ASPECT}` }}
      >
        {live && scale > 0 ? (
          <iframe
            src={`${API_BASE}/deploy/${deployment.id}/preview-frame`}
            title={`Website preview for ${deployment.name}`}
            aria-hidden="true"
            tabIndex={-1}
            referrerPolicy="no-referrer"
            loading="lazy"
            className="pointer-events-none absolute top-0 left-0 border-0 bg-white"
            style={{
              width: DESKTOP_WIDTH,
              height: DESKTOP_WIDTH / ASPECT,
              transform: `scale(${scale})`,
              transformOrigin: "top left",
            }}
          />
        ) : (
          <div className="absolute inset-0 flex flex-col items-center justify-center gap-4 px-6 text-center">
            <WorkloadMark profile={deployment.profile} />
            <p className="text-body font-medium">
              {url
                ? "Deploy your project to bring it to life here"
                : deployment.liveReleaseId
                  ? "Running on your server"
                  : "Deploy your project to bring it to life here"}
            </p>
          </div>
        )}
      </div>
    </>
  )

  const frame =
    "group block min-w-0 overflow-hidden rounded-lg border border-hairline bg-background transition-colors"
  if (!url) {
    return (
      <section aria-label="Website preview" className={frame}>
        {tile}
      </section>
    )
  }
  return (
    <a
      href={url}
      target="_blank"
      rel="noopener noreferrer"
      aria-label={`Open ${hostOf(url)} in a new tab`}
      className={cn(frame, "focus-ring hover:border-border-strong")}
    >
      {tile}
    </a>
  )
}

/** The element's rendered width, kept current as it is resized. */
function useWidth(ref: RefObject<HTMLDivElement | null>) {
  const [width, setWidth] = useState(0)
  useEffect(() => {
    const element = ref.current
    if (!element) return
    const observer = new ResizeObserver(([entry]) => setWidth(entry.contentRect.width))
    observer.observe(element)
    setWidth(element.getBoundingClientRect().width)
    return () => observer.disconnect()
  }, [ref])
  return width
}
