"use client"

import { motion, useReducedMotion } from "motion/react"
import { cn } from "@/lib/utils"
import { kindCounts } from "@/lib/release-kinds"
import type { Release } from "@/lib/types"
import { KIND_STYLE } from "@/components/update/release-notes"

/**
 * The wire between the version this dashboard runs and the one it can become,
 * with a pulse of light running along it toward the new one.
 *
 * It is `AnimatedBeam`'s line and pulse — the brand into `--signal`, over a
 * rule of the tone's own colour — drawn as a flex item rather than measured
 * between two elements. The notice and its dialog lay the two versions out in
 * a row, so the line is simply the space between them, and a dialog that
 * zooms in as it opens cannot leave it pointing at where its ends used to be.
 *
 * `still` is the line with nothing on it: an update that finished, or failed,
 * carries nothing any more. Reduced motion draws it still too.
 */
export function UpdateWire({
  tone = "brand",
  still = false,
  className,
}: {
  tone?: "brand" | "success" | "danger"
  still?: boolean
  className?: string
}) {
  const reduced = useReducedMotion()
  return (
    <span
      aria-hidden
      className={cn(
        "relative block h-0.5 min-w-4 flex-1 overflow-hidden rounded-full",
        tone === "brand" && "bg-rule-brand",
        tone === "success" && "bg-rule-success",
        tone === "danger" && "bg-rule-danger",
        className,
      )}
    >
      {!still && !reduced && (
        <motion.span
          className="absolute inset-y-0 left-0 w-2/3 rounded-full bg-linear-to-r from-transparent via-brand to-signal"
          initial={{ x: "-100%" }}
          animate={{ x: "160%" }}
          transition={{
            repeat: Infinity,
            duration: 1.8,
            repeatDelay: 0.5,
            ease: [0.16, 1, 0.3, 1],
          }}
        />
      )}
    </span>
  )
}

/**
 * Everything an update brings, by kind, as one band of colour and the counts
 * under it — "1 security · 6 new · 3 fixed" in the kinds' own hues, the way
 * the history counts each release. The band is what makes the notice read as
 * a delivery of things rather than as a version number: a run of green with a
 * red sliver in it says "mostly new, one security fix" before a word is read.
 * Its spans draw out one after another as the notice lands — §11's *arrived*,
 * once, the way the history's entries fade in.
 */
export function KindBand({ releases, className }: { releases: Release[]; className?: string }) {
  const reduced = useReducedMotion()
  const counts = kindCounts(releases)
  if (counts.length === 0) return null
  return (
    <div className={cn("min-w-0 space-y-1.5", className)}>
      <span aria-hidden className="flex h-1 min-w-0 gap-0.5">
        {counts.map(({ kind, n }, index) => (
          <motion.span
            key={kind}
            className={cn("block h-full min-w-1 origin-left rounded-full", KIND_STYLE[kind]?.bar)}
            style={{ flexGrow: n }}
            initial={reduced ? false : { scaleX: 0 }}
            animate={{ scaleX: 1 }}
            transition={{ delay: 0.15 + index * 0.07, duration: 0.5, ease: [0.16, 1, 0.3, 1] }}
          />
        ))}
      </span>
      <p className="flex min-w-0 flex-wrap gap-x-2.5 gap-y-0.5 text-hint leading-tight">
        {counts.map(({ kind, n }) => (
          <span key={kind} className="flex items-baseline gap-1">
            <span className="numeric text-foreground">{n}</span>
            <span className={KIND_STYLE[kind]?.className}>
              {(KIND_STYLE[kind]?.label ?? kind).toLowerCase()}
            </span>
          </span>
        ))}
      </p>
    </div>
  )
}
