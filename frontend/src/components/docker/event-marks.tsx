"use client"

import Link from "next/link"
import {
  Box,
  CrossCircle,
  Heart,
  Link as LinkGlyph,
  NetworkDevice,
  Pause,
  Play,
  Plus,
  RotateClockwise,
  Stop,
  StopCircle,
  Trash,
  type Icon,
} from "@/components/icons"
import { cn } from "@/lib/utils"
import type { DockerEvent } from "@/lib/types"
import { InitialsMark } from "@/components/account/user-avatar"
import { ProductGlyph, ProductLogo, imageProduct } from "@/components/product-logo"
import { isCleanExit } from "@/components/deploy/traffic-strip"

/**
 * How a Docker event is drawn wherever one is a row: the thing it happened to
 * with what happened in its corner, and who did it. A container's and a
 * stack's Events view draw them, and so does the deployment's feed
 * (`deploy/lifecycle-feed.tsx`).
 */

/**
 * What happened, as the glyph in the corner of the thing it happened to, in
 * the tone of a reading of state: an exit that failed in red, a restart in
 * amber, a start or a passing check in green, the bookkeeping quiet.
 */
export const HAPPENED: Record<string, [Icon, string]> = {
  die: [CrossCircle, "text-destructive"],
  oom: [CrossCircle, "text-destructive"],
  kill: [Stop, "text-muted-foreground"],
  restart: [RotateClockwise, "text-warning"],
  start: [Play, "text-success"],
  unpause: [Play, "text-success"],
  healthy: [Heart, "text-success"],
  unhealthy: [Heart, "text-warning"],
  stop: [StopCircle, "text-muted-foreground"],
  pause: [Pause, "text-muted-foreground"],
  create: [Plus, "text-muted-foreground"],
  destroy: [Trash, "text-muted-foreground"],
  connect: [LinkGlyph, "text-muted-foreground"],
  disconnect: [LinkGlyph, "text-muted-foreground"],
}

function happened(event: DockerEvent, loop: boolean) {
  if (loop) return HAPPENED.restart
  if (isCleanExit(event)) return HAPPENED.stop
  if (event.action.startsWith("health_status")) {
    return HAPPENED[event.action.endsWith("unhealthy") ? "unhealthy" : "healthy"]
  }
  return HAPPENED[event.action]
}

/**
 * A container is the product it runs: the image's own mark when its reference
 * names one, otherwise the page's. A network or an image keeps a glyph on the
 * same tile, so the titles line up.
 */
export function EventMark({
  event,
  product,
  loop = false,
  badge = happened(event, loop),
}: {
  event: DockerEvent
  product?: string
  /** A restart loop folded into one row, drawn by its last exit. */
  loop?: boolean
  /** What happened, where the row says it otherwise: a loop of clean exits is no restart's amber. */
  badge?: [Icon, string]
}) {
  const named = event.image ? imageProduct(event.image) : undefined
  const id =
    event.type === "container"
      ? named && named !== "docker"
        ? named
        : (product ?? "docker")
      : undefined
  const Glyph = badge?.[0]
  return (
    <span className="relative z-10 flex shrink-0">
      <ProductLogo id={id} size="sm" fallback={event.type === "network" ? NetworkDevice : Box} />
      {Glyph && (
        <span
          aria-hidden
          className="absolute -right-1 -bottom-1 flex size-4 items-center justify-center rounded-sm border border-hairline bg-background"
        >
          <Glyph className={cn("size-2.5", badge[1])} />
        </span>
      )}
    </span>
  )
}

/** Who did it: the person whose press the audit log matched, or Docker on its own. */
export function EventWho({ event }: { event: DockerEvent }) {
  if (event.trigger) {
    return (
      <span className="flex shrink-0 items-center gap-1.5">
        <InitialsMark name={event.trigger.actor || "?"} size="xs" />
        <Link
          href={`/audit?action=${encodeURIComponent(event.trigger.action)}`}
          className="rounded-sm text-xs whitespace-nowrap text-foreground focus-ring hover:underline"
          title={`Audit entry ${event.trigger.auditId} — ${event.trigger.action} by ${
            event.trigger.actor || "an unnamed session"
          }. A name and a window, so a likely cause rather than a recorded one.`}
        >
          this dashboard
        </Link>
      </span>
    )
  }
  if (event.source !== "daemon") return null
  return (
    <span className="flex shrink-0 items-center gap-1.5">
      <ProductGlyph id="docker" />
      <span
        className="text-xs whitespace-nowrap text-muted-foreground"
        title="Docker acted on its own: a restart policy firing, a health check, or the OOM killer."
      >
        docker itself
      </span>
    </span>
  )
}
