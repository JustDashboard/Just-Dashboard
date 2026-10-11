"use client"

import { memo, useId } from "react"
import { motion, useReducedMotion } from "motion/react"
import { boundsOf, center, wrapWords, type StickyPreview } from "@/lib/stickyboard"
import { cn } from "@/lib/utils"
import { linkStroke } from "@/components/stickyboard/link-edge"

type PreviewNote = StickyPreview["notes"][number]

/** Above this many notes a board simply appears: sixty landing one after another is enough. */
const LAND_LIMIT = 60

/**
 * A board drawn small: its sections, notes, text and links in their own
 * colours, fitted to the card, with what each note says written on it. The
 * server derives the shapes and the words from the board on every save
 * (`stickyPreviewOf`), so the list never reads a whole board to draw it. Words
 * too small to read at this size are drawn as soft bars the length of their
 * lines — on a board of two hundred notes it is the shape that is recognised,
 * not what any one of them says.
 *
 * The notes stand on the board's dot grid, and on arrival they land on it
 * one after another — the *arrived* of §11, as `BlurFade` staggers the cards
 * around them; reduced motion, a large board or `land={false}` simply draws
 * them.
 */
export const BoardPreview = memo(function BoardPreview({
  preview,
  className,
  land = true,
}: {
  preview: StickyPreview
  className?: string
  land?: boolean
}) {
  const uid = useId().replace(/:/g, "")
  const reduced = useReducedMotion()
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
  const w = Math.max(bounds.w + pad * 2, 640)
  const h = Math.max(bounds.h + pad * 2, 360)
  const x = bounds.x + bounds.w / 2 - w / 2
  const y = bounds.y + bounds.h / 2 - h / 2
  const radius = Math.max(w, h) / 160
  // Below about seven screen pixels a line of words is broken glyphs; a bar
  // says the same thing more cleanly. A card's picture is about 400 by 176
  // pixels, and the board is fitted inside it by whichever side is tighter.
  const scale = Math.min(400 / w, 176 / h)
  const legible = (size: number) => size * scale >= 7
  // The editor's grid, at a spacing that reads as a grid at any zoom.
  const gap = Math.max(w, h) / 36
  const landing = land && !reduced && preview.notes.length <= LAND_LIMIT

  return (
    <svg
      aria-hidden
      viewBox={`${x} ${y} ${w} ${h}`}
      preserveAspectRatio="xMidYMid meet"
      className={className}
    >
      <defs>
        <pattern
          id={`${uid}-dots`}
          patternUnits="userSpaceOnUse"
          x={x}
          y={y}
          width={gap}
          height={gap}
        >
          <circle cx={gap / 2} cy={gap / 2} r={gap * 0.09} style={{ fill: "var(--grid-dot)" }} />
        </pattern>
      </defs>
      {/* Past the view box on every side, so the letterbox a card's own shape
          leaves around the board is grid too. */}
      <rect x={x - w} y={y - h} width={w * 3} height={h * 3} fill={`url(#${uid}-dots)`} />
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
      {preview.notes.map((note, i) => (
        <Land key={i} index={i} rise={h * 0.02} on={landing}>
          <Note note={note} radius={radius} legible={legible} />
        </Land>
      ))}
    </svg>
  )
})

/** A note arriving on the wall: from a little above, into place, a beat after the last. */
function Land({
  index,
  rise,
  on,
  children,
}: {
  index: number
  rise: number
  on: boolean
  children: React.ReactNode
}) {
  if (!on) return <>{children}</>
  return (
    <motion.g
      initial={{ opacity: 0, y: -rise }}
      animate={{ opacity: 1, y: 0 }}
      transition={{
        delay: 0.12 + Math.min(index * 0.04, 0.6),
        duration: 0.35,
        ease: [0.16, 1, 0.3, 1],
      }}
    >
      {children}
    </motion.g>
  )
}

function Note({
  note,
  radius,
  legible,
}: {
  note: PreviewNote
  radius: number
  legible: (size: number) => boolean
}) {
  if (note.kind === "section") {
    const ink = linkStroke(note.color)
    const size = Math.min(Math.max(Math.min(note.w, note.h) * 0.06, 18), 44)
    const title = note.text?.split("\n")[0]
    return (
      <g>
        <rect
          x={note.x}
          y={note.y}
          width={note.w}
          height={note.h}
          rx={radius * 2}
          style={{ fill: `color-mix(in oklab, ${ink} 7%, transparent)`, stroke: ink }}
          strokeOpacity={0.4}
          strokeWidth={radius * 0.5}
        />
        {title && legible(size) && (
          <text
            x={note.x + size * 0.9}
            y={note.y + size * 1.5}
            fontSize={size}
            fontWeight={600}
            style={{ fill: ink }}
          >
            {wrapWords(title, note.w / (size * 0.55) - 2, 1)[0]}
          </text>
        )}
      </g>
    )
  }
  if (note.kind === "text") {
    const size = Math.min(note.h * 0.55, note.w * 0.09)
    if (note.text && legible(size)) {
      const lines = wrapWords(note.text, note.w / (size * 0.52), 2)
      return (
        <text fontSize={size} fontWeight={600} style={{ fill: linkStroke(note.color) }}>
          {lines.map((line, j) => (
            <tspan key={j} x={note.x} y={note.y + size * (1 + j * 1.25)}>
              {line}
            </tspan>
          ))}
        </text>
      )
    }
    return (
      <rect
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
  return <Sticky note={note} radius={radius} legible={legible} />
}

function Sticky({
  note,
  radius,
  legible,
}: {
  note: PreviewNote
  radius: number
  legible: (size: number) => boolean
}) {
  const inset = Math.min(note.w, note.h) * 0.1
  const size = Math.max(note.w * 0.075, 12)
  const room = Math.floor((note.h - inset * 2) / (size * 1.3))
  const lines = note.text
    ? wrapWords(note.text, (note.w - inset * 2) / (size * 0.5), Math.max(room, 1))
    : []
  const readable = legible(size)
  return (
    <g data-note-color={note.color} data-note-style={note.style}>
      <rect
        x={note.x}
        y={note.y}
        width={note.w}
        height={note.h}
        rx={radius}
        style={{ fill: "var(--note-ground)", stroke: "var(--note-edge)" }}
        strokeWidth={radius * 0.4}
      />
      {!readable &&
        lines
          .slice(0, 4)
          .map((line, j) => (
            <rect
              key={j}
              x={note.x + inset}
              y={note.y + inset + size * (0.4 + j * 1.3)}
              width={Math.min(line.length * size * 0.5, note.w - inset * 2)}
              height={size * 0.62}
              rx={size * 0.2}
              style={{ fill: "var(--note-ink-soft)" }}
              fillOpacity={j === 0 ? 0.55 : 0.32}
            />
          ))}
      {readable && lines.length > 0 && (
        <text fontSize={size} style={{ fill: "var(--note-ink)" }}>
          {lines.map((line, j) => (
            <tspan
              key={j}
              x={note.x + inset}
              y={note.y + inset + size * (0.95 + j * 1.3)}
              fontWeight={j === 0 ? 600 : 400}
            >
              {line}
            </tspan>
          ))}
        </text>
      )}
    </g>
  )
}
