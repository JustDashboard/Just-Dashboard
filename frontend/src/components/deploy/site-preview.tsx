"use client"

import { useEffect, useRef, useState, type RefObject } from "react"
import { External, LockClosed } from "@/components/icons"
import { API_BASE } from "@/lib/api"
import { cn } from "@/lib/utils"
import type { DeploymentDomainRoute, DeploymentSummary } from "@/lib/types"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { ProjectMark } from "@/components/deploy/project-mark"
import { WorkloadMark, deploymentURL, hostOf } from "@/components/deploy/vocabulary"

/**
 * The widths the site is laid out at before it is shrunk into the tile: a
 * laptop's, and a phone's — the same frame re-laid, never a second one.
 */
const DEVICES = {
  desktop: { width: 1280, height: 800 },
  phone: { width: 390, height: 844 },
} as const

type Device = keyof typeof DEVICES

/**
 * The tile's screen: sixteen by ten, the shape of the laptop the site is
 * being looked at on. It is also what keeps the tile the height of the
 * wiring column beside it — at four by three the preview stood twice as
 * tall as the facts it was meant to sit level with.
 */
const ASPECT = 16 / 10

/**
 * The Overview's one framed block: a tile that is the website, seen
 * from across the room. The site is laid out at desktop width inside a frame
 * and scaled down to fit, the way Vercel's deployment thumbnail is a whole
 * page made small, and the whole tile opens the site. Its strip is a
 * browser's: the site's own icon and the lock in the certificate's colour
 * before the address, and a switch that re-lays the same frame at a phone's
 * width, centred at the tile's height — the other reading of the real site a
 * reader checks after a release.
 *
 * The frame stays invisible until the site has painted — a bare frame is a
 * white rectangle for as long as the site takes — and a bar sweeps across
 * the top of the screen meanwhile (§11 *working*), laid over it so the tile
 * does not grow and shrink by its height; then the page rises in (*arrived*).
 * A wrapper the server could not serve — an error body where the page would
 * be — is no preview, and the tile says so rather than showing the error.
 *
 * With nothing behind the glass — a private service, a project not deployed
 * yet, a stopped one — the tile draws the product the project is and says
 * which of those it is. A private service has no browser strip: it is not a
 * website, and the header and How to connect already name where it is
 * reached, so the tile keeps only the one address found nowhere else — the
 * port it is published on here. A stopped project's frame is not loaded at
 * all: there is nothing there to show.
 */
export function SitePreview({
  deployment,
  product,
  domain,
  stopped,
}: {
  deployment: DeploymentSummary
  /** What the project is, drawn when there is no site to show. */
  product?: string
  /** The route the address answers on, for the lock's colour. */
  domain?: DeploymentDomainRoute
  stopped?: boolean
}) {
  const url = deploymentURL(deployment.endpoint)
  const [failed, setFailed] = useState(false)
  const live = Boolean(url && deployment.liveReleaseId && !stopped && !failed)
  const screen = useRef<HTMLDivElement>(null)
  const size = useSize(screen)
  const [device, setDevice] = useState<Device>("desktop")
  const [loaded, setLoaded] = useState(false)
  const frame = DEVICES[device]
  const scale = device === "desktop" ? size.width / frame.width : size.height / frame.height
  const https = url?.startsWith("https:")

  const lockTone = !domain
    ? "text-muted-foreground"
    : domain.certificate === "valid"
      ? "text-success"
      : domain.certificate === "expiring"
        ? "text-warning"
        : domain.certificate === "expired" || domain.certificate === "missing"
          ? "text-destructive"
          : "text-muted-foreground"

  return (
    <section
      aria-label="Website preview"
      className={cn(
        "group relative min-w-0 overflow-hidden rounded-xl border border-hairline bg-background transition-colors",
        url && "hover:border-border-strong",
      )}
    >
      {/* The tile opens the site; it is a link laid over the whole tile rather
          than one wrapped round it, so the width switch in the strip can be a
          control of its own instead of a button inside a link. */}
      {url && (
        <a
          href={url}
          target="_blank"
          rel="noopener noreferrer"
          aria-label={`Open ${hostOf(url)} in a new tab`}
          className="absolute inset-0 z-10 rounded-xl focus-ring"
        />
      )}
      {url && (
        <div className="relative flex h-9 items-center gap-3 border-b border-hairline bg-surface-header px-3">
          <span aria-hidden="true" className="flex shrink-0 gap-1">
            <span className="size-1.5 rounded-full bg-muted-foreground/40" />
            <span className="size-1.5 rounded-full bg-muted-foreground/40" />
            <span className="size-1.5 rounded-full bg-muted-foreground/40" />
          </span>
          <span className="flex min-w-0 flex-1 items-center justify-center gap-1.5">
            <ProjectMark deployment={deployment} product={product} size="xs" />
            {https && <LockClosed aria-hidden className={cn("size-3 shrink-0", lockTone)} />}
            <span className="truncate font-mono text-hint text-muted-foreground">
              {hostOf(url)}
            </span>
          </span>
          {live && (
            <ToggleGroup
              type="single"
              size="sm"
              value={device}
              onValueChange={(value) => value && setDevice(value as Device)}
              aria-label="Preview width"
              className="relative z-20 shrink-0"
            >
              <ToggleGroupItem value="desktop" className="h-6 min-w-0 px-2 text-hint">
                Desktop
              </ToggleGroupItem>
              <ToggleGroupItem value="phone" className="h-6 min-w-0 px-2 text-hint">
                Phone
              </ToggleGroupItem>
            </ToggleGroup>
          )}
          <span
            aria-hidden="true"
            className="hidden shrink-0 items-center gap-1 text-hint text-muted-foreground transition-colors group-hover:text-foreground xl:inline-flex"
          >
            Open <External className="size-3" />
          </span>
        </div>
      )}
      <div
        ref={screen}
        className="relative w-full overflow-hidden bg-background"
        style={{ aspectRatio: `${ASPECT}` }}
      >
        {live && !loaded && (
          <div
            aria-hidden="true"
            className="absolute inset-x-0 top-0 z-10 h-0.5 overflow-hidden bg-meter-track"
          >
            <span className="absolute inset-y-0 left-0 w-1/3 animate-sweep bg-brand" />
          </div>
        )}
        {live ? (
          scale > 0 && (
            // The rise is on a wrapper: its keyframe ends on `transform: none`,
            // which on the frame itself would undo the scale that shrinks it.
            <div className={cn("absolute inset-0", loaded ? "animate-rise" : "opacity-0")}>
              <iframe
                src={`${API_BASE}/deploy/${deployment.id}/preview-frame`}
                title={`Website preview for ${deployment.name}`}
                aria-hidden="true"
                tabIndex={-1}
                referrerPolicy="no-referrer"
                loading="lazy"
                onLoad={(event) => {
                  // The wrapper is served from this origin, so its document
                  // is readable: an error body where the page would be is no
                  // preview. A frame from another origin cannot be read, and
                  // counts as loaded.
                  const page = event.currentTarget.contentDocument
                  if (page && page.contentType !== "text/html") setFailed(true)
                  else setLoaded(true)
                }}
                className={cn(
                  "pointer-events-none absolute top-0 border-0 bg-white",
                  device === "phone" && "rounded-lg ring-1 ring-hairline",
                )}
                style={{
                  width: frame.width,
                  height: frame.height,
                  left: device === "desktop" ? 0 : (size.width - frame.width * scale) / 2,
                  transform: `scale(${scale})`,
                  transformOrigin: "top left",
                }}
              />
            </div>
          )
        ) : (
          <Glass deployment={deployment} product={product} stopped={stopped} unavailable={failed} />
        )}
      </div>
    </section>
  )
}

/** What the tile says when there is no site behind it. */
function Glass({
  deployment,
  product,
  stopped,
  unavailable,
}: {
  deployment: DeploymentSummary
  product?: string
  stopped?: boolean
  /** The site has an address, but its preview could not be served. */
  unavailable?: boolean
}) {
  return (
    <div className="absolute inset-0 flex flex-col items-center justify-center gap-3 px-6 text-center">
      <WorkloadMark
        profile={deployment.profile}
        product={product}
        className="size-14 rounded-xl [&_img]:size-8 [&_svg]:size-6"
      />
      {!deployment.liveReleaseId ? (
        <div className="space-y-1">
          <p className="text-body font-medium">Not deployed yet</p>
          <p className="text-hint text-muted-foreground">The first deployment puts the site here</p>
        </div>
      ) : stopped ? (
        <div className="space-y-1">
          <p className="text-body font-medium">Nothing is serving the site</p>
          <p className="text-hint text-muted-foreground">
            Start the application to bring the site back
          </p>
        </div>
      ) : unavailable ? (
        <div className="space-y-1">
          <p className="text-body font-medium">No preview of the site</p>
          <p className="text-hint text-muted-foreground">Open it to see it live</p>
        </div>
      ) : (
        <div className="space-y-1">
          <p className="text-body font-medium">No website</p>
          <p className="text-hint text-muted-foreground">
            Reached over the Docker network
            {deployment.hostPort ? (
              <>
                {" "}
                · on this server <span className="font-mono">:{deployment.hostPort}</span>
              </>
            ) : null}
          </p>
        </div>
      )}
    </div>
  )
}

/** The element's rendered size, kept current as it is resized. */
function useSize(ref: RefObject<HTMLDivElement | null>) {
  const [size, setSize] = useState({ width: 0, height: 0 })
  useEffect(() => {
    const element = ref.current
    if (!element) return
    const observer = new ResizeObserver(([entry]) =>
      setSize({ width: entry.contentRect.width, height: entry.contentRect.height }),
    )
    observer.observe(element)
    const box = element.getBoundingClientRect()
    setSize({ width: box.width, height: box.height })
    return () => observer.disconnect()
  }, [ref])
  return size
}
