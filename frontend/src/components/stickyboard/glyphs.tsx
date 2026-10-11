"use client"

import { forwardRef, type Ref } from "react"
import {
  mdiArrangeBringToFront,
  mdiArrangeSendToBack,
  mdiArrowLeftRight,
  mdiArrowRight,
  mdiCursorDefaultOutline,
  mdiFormatAlignCenter,
  mdiFormatAlignLeft,
  mdiFormatText,
  mdiHandBackRightOutline,
  mdiMapOutline,
  mdiMinus,
  mdiStickerTextOutline,
  mdiVectorCurve,
  mdiVectorLine,
  mdiVectorPolyline,
  mdiVectorSquare,
} from "@mdi/js"
import type { Icon, IconProps } from "@/components/icons"

/**
 * The marks a canvas editor has and a general UI set does not: the pointer and
 * the hand, a sticky note, a section, a link's three shapes and its arrows.
 * Heroicons has none of them, and a board drawn with near-enough glyphs —
 * a grid for a section, a share glyph for a link — reads as a board that does
 * not quite know what it holds. They come from Material Design Icons through
 * the same adapter `git/glyphs.tsx` and `files/file-icon.tsx` use; anything
 * Heroicons does draw stays Heroicons.
 */
function mdi(path: string, name: string): Icon {
  function MdiGlyph({ size = 16, title, ...props }: IconProps, ref: Ref<SVGSVGElement>) {
    return (
      <svg ref={ref} viewBox="0 0 24 24" fill="currentColor" width={size} height={size} {...props}>
        {title ? <title>{title}</title> : null}
        <path d={path} />
      </svg>
    )
  }
  const Forwarded = forwardRef<SVGSVGElement, Omit<IconProps, "ref">>(MdiGlyph)
  Forwarded.displayName = name
  return Forwarded
}

export const PointerTool: Icon = mdi(mdiCursorDefaultOutline, "PointerTool")
export const HandTool: Icon = mdi(mdiHandBackRightOutline, "HandTool")
export const StickyGlyph: Icon = mdi(mdiStickerTextOutline, "StickyGlyph")
export const TextGlyph: Icon = mdi(mdiFormatText, "TextGlyph")
export const SectionGlyph: Icon = mdi(mdiVectorSquare, "SectionGlyph")
export const AlignLeft: Icon = mdi(mdiFormatAlignLeft, "AlignLeft")
export const AlignCenter: Icon = mdi(mdiFormatAlignCenter, "AlignCenter")
export const BringToFront: Icon = mdi(mdiArrangeBringToFront, "BringToFront")
export const SendToBack: Icon = mdi(mdiArrangeSendToBack, "SendToBack")
export const MinimapGlyph: Icon = mdi(mdiMapOutline, "MinimapGlyph")
export const PathCurved: Icon = mdi(mdiVectorCurve, "PathCurved")
export const PathStraight: Icon = mdi(mdiVectorLine, "PathStraight")
export const PathElbow: Icon = mdi(mdiVectorPolyline, "PathElbow")
export const ArrowNone: Icon = mdi(mdiMinus, "ArrowNone")
export const ArrowEnd: Icon = mdi(mdiArrowRight, "ArrowEnd")
export const ArrowBoth: Icon = mdi(mdiArrowLeftRight, "ArrowBoth")

/**
 * Stickyboard as a product, for where the page names itself: three notes
 * fanned out in the board's own paper colours, the front one written on.
 * Stickyboard is this dashboard's own and has no logo to borrow, so it is
 * drawn from the palette its notes are — the rail keeps the one-colour glyph.
 */
export function StickyboardMark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 32 32" aria-hidden="true" className={className}>
      <g data-note-color="blue" transform="rotate(-14 11 19)">
        <rect
          x="3.5"
          y="10"
          width="15"
          height="15"
          rx="1.5"
          style={{ fill: "var(--note)", stroke: "var(--note-edge)" }}
        />
      </g>
      <g data-note-color="pink" transform="rotate(12 21 17)">
        <rect
          x="13.5"
          y="8.5"
          width="15"
          height="15"
          rx="1.5"
          style={{ fill: "var(--note)", stroke: "var(--note-edge)" }}
        />
      </g>
      <g data-note-color="yellow">
        <rect
          x="8"
          y="4"
          width="16"
          height="16"
          rx="1.5"
          style={{ fill: "var(--note)", stroke: "var(--note-edge)" }}
        />
        <path
          d="M11 9h10M11 12.5h10M11 16h6"
          strokeWidth="1.4"
          strokeLinecap="round"
          style={{ stroke: "var(--note-ink-soft)" }}
        />
      </g>
    </svg>
  )
}
