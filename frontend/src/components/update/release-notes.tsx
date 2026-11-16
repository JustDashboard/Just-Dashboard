"use client"

import { dateFormatter } from "@/lib/format"
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
  added: { label: "New", className: "text-tag-green", bar: "bg-tag-green" },
  changed: { label: "Changed", className: "text-tag-blue", bar: "bg-tag-blue" },
  fixed: { label: "Fixed", className: "text-tag-cyan", bar: "bg-tag-cyan" },
  removed: { label: "Removed", className: "text-tag-slate", bar: "bg-tag-slate" },
  security: { label: "Security", className: "text-tag-red", bar: "bg-tag-red" },
  deprecated: {
    label: "Ageing",
    className: "text-tag-amber",
    bar: "bg-tag-amber",
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

const utcDay = dateFormatter({ day: "numeric", month: "short", year: "numeric", timeZone: "UTC" })

/** A calendar day as a person writes one. The value is YYYY-MM-DD, not a time. */
export function releaseDay(date: string): string {
  const parsed = new Date(`${date}T00:00:00Z`)
  if (Number.isNaN(parsed.getTime())) return date
  return utcDay(parsed)
}
