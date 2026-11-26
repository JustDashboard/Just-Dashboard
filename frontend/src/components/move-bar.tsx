"use client"

import { ArrowDown, ArrowLeft, ArrowRight, ArrowUp } from "@/components/icons"
import { IconAction } from "@/components/icon-action"
import type { Direction } from "@/components/map/canvas-keys"
import { cn } from "@/lib/utils"

const MOVES: { direction: Direction; word: string; Glyph: typeof ArrowLeft }[] = [
  { direction: { x: -1, y: 0 }, word: "left", Glyph: ArrowLeft },
  { direction: { x: 0, y: -1 }, word: "up", Glyph: ArrowUp },
  { direction: { x: 0, y: 1 }, word: "down", Glyph: ArrowDown },
  { direction: { x: 1, y: 0 }, word: "right", Glyph: ArrowRight },
]

/**
 * Moving what is chosen on a canvas one press at a time, for a pointer that
 * cannot drag (WCAG 2.5.7): the arrow keys answer a keyboard (2.1.1), and
 * these four answer a single click or tap. Each press is the keys' longer
 * step, kept as a drag is. The maps and a database's Diagram have them in
 * their inspector's foot, and a drawing under its canvas, where the
 * selection is. The caller says what is moved (`name`).
 */
export function MoveBar({
  name,
  onMove,
  className,
}: {
  name: string
  onMove: (direction: Direction) => void
  className?: string
}) {
  return (
    <div
      role="group"
      aria-label={`Move ${name}`}
      data-slot="move-bar"
      className={cn("flex shrink-0 items-center gap-1", className)}
    >
      <span aria-hidden className="mr-auto text-hint text-muted-foreground">
        Move
      </span>
      {MOVES.map(({ direction, word, Glyph }) => (
        <IconAction
          key={word}
          label={`Move ${name} ${word}`}
          variant="outline"
          size="icon-sm"
          onClick={() => onMove(direction)}
        >
          <Glyph />
        </IconAction>
      ))}
    </div>
  )
}
