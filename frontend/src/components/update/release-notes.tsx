"use client"

import { cn } from "@/lib/utils"
import type { ChangeKind } from "@/lib/types"

/**
 * A release's vocabulary: the kind against every line, and the day it shipped.
 *
 * The changelog arrives as data rather than prose (see internal/selfupdate for
 * why), and this is what that buys: a kind against every line, in a column of
 * its own, so a release is scanned rather than read. Somebody deciding whether
 * to upgrade a root-equivalent panel on their own server is looking for two
 * things — did anything break, and is there a security fix — and both are
 * answerable at a glance from the left-hand column alone.
 *
 * The kinds are a closed set on the server, so there is no "unknown kind"
 * branch here beyond the neutral fallback: a release inventing a seventh kind
 * fails the Go test suite before it can render as an unstyled grey row.
 *
 * `bar` is the same hue as a fill, for the band the update notice and its
 * dialog draw of everything an update brings.
 */
export const KIND_STYLE: Record<ChangeKind, { label: string; className: string; bar: string }> = {
  added: { label: "New", className: "text-[var(--tag-green)]", bar: "bg-[var(--tag-green)]" },
  changed: { label: "Changed", className: "text-[var(--tag-blue)]", bar: "bg-[var(--tag-blue)]" },
  fixed: { label: "Fixed", className: "text-[var(--tag-cyan)]", bar: "bg-[var(--tag-cyan)]" },
  removed: { label: "Removed", className: "text-[var(--tag-slate)]", bar: "bg-[var(--tag-slate)]" },
  security: { label: "Security", className: "text-[var(--tag-red)]", bar: "bg-[var(--tag-red)]" },
  deprecated: {
    label: "Ageing",
    className: "text-[var(--tag-amber)]",
    bar: "bg-[var(--tag-amber)]",
  },
}

export function ChangeLabel({ kind }: { kind: ChangeKind }) {
  const style = KIND_STYLE[kind] ?? { label: kind, className: "text-muted-foreground" }
  return (
    <span className={cn("eyebrow shrink-0 text-micro leading-4 tracking-wider", style.className)}>
      {style.label}
    </span>
  )
}

/** A calendar day as a person writes one. The value is YYYY-MM-DD, not a time. */
export function releaseDay(date: string): string {
  const parsed = new Date(`${date}T00:00:00Z`)
  if (Number.isNaN(parsed.getTime())) return date
  return parsed.toLocaleDateString(undefined, {
    day: "numeric",
    month: "short",
    year: "numeric",
    timeZone: "UTC",
  })
}
