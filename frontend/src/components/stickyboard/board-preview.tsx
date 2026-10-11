"use client"

import { memo } from "react"
import { boundsOf, center, type StickyPreview } from "@/lib/stickyboard"
import { cn } from "@/lib/utils"
import { linkStroke } from "@/components/stickyboard/link-edge"

/**
 * A board drawn small: its sections, notes, text and links in their own
 * colours, fitted to the card. The server derives the shapes from the board on
 * every save (`stickyPreviewOf`), so the list never reads a whole board to
 * draw it. Text is a bar where the words would be — at this size it is the
 * shape of the board that is recognised, not what is written on it.
 */
export const BoardPreview = memo(function BoardPreview({
  preview,
  className,
}: {
  preview: StickyPreview
  className?: string
}) {
  const bounds = boundsOf(preview.notes.map((n) => ({ x: n.x, y: n.y, w: n.w, h: n.h })))
  if (!bounds) {
    return (
      <div
        aria-hidden
        className={cn(
          "flex items-center justify-center text-hint text-muted-foreground",
          className,
        )}
      >
        Empty board
      </div>
    )
  }
  // Room around the drawing, and a floor on its size so one note on a board
  // is a note on a board rather than a slab filling the card.
  const pad = Math.max(bounds.w, bounds.h) * 0.08 + 40
  const w = Math.max(bounds.w + pad * 2, 900)
  const h = Math.max(bounds.h + pad * 2, 520)
  const x = bounds.x + bounds.w / 2 - w / 2
  const y = bounds.y + bounds.h / 2 - h / 2
  const radius = Math.max(w, h) / 160

  return (
    <svg
      aria-hidden
      viewBox={`${x} ${y} ${w} ${h}`}
      preserveAspectRatio="xMidYMid meet"
      className={className}
    >
      {preview.links.map(([from, to], i) => {
        const a = preview.notes[from]
        const b = preview.notes[to]
        if (!a || !b) return null
        const p = center(a)
        const q = center(b)
        return (
          <line
            key={`l${i}`}
            x1={p.x}
            y1={p.y}
            x2={q.x}
            y2={q.y}
            stroke="var(--muted-foreground)"
            strokeOpacity={0.55}
            strokeWidth={radius * 0.8}
          />
        )
      })}
      {preview.notes.map((note, i) => {
        if (note.kind === "section") {
          const ink = linkStroke(note.color)
          return (
            <rect
              key={i}
              x={note.x}
              y={note.y}
              width={note.w}
              height={note.h}
              rx={radius * 2}
              style={{ fill: `color-mix(in oklab, ${ink} 7%, transparent)`, stroke: ink }}
              strokeOpacity={0.4}
              strokeWidth={radius * 0.5}
            />
          )
        }
        if (note.kind === "text") {
          return (
            <rect
              key={i}
              x={note.x}
              y={note.y + note.h * 0.25}
              width={note.w * 0.8}
              height={Math.max(note.h * 0.5, radius * 3)}
              rx={radius}
              style={{ fill: linkStroke(note.color) }}
              fillOpacity={0.55}
            />
          )
        }
        return (
          <rect
            key={i}
            data-note-color={note.color}
            data-note-style={note.style}
            x={note.x}
            y={note.y}
            width={note.w}
            height={note.h}
            rx={radius}
            style={{ fill: "var(--note-ground)", stroke: "var(--note-edge)" }}
            strokeWidth={radius * 0.4}
          />
        )
      })}
    </svg>
  )
})
