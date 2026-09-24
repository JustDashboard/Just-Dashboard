"use client"

import type { RefObject } from "react"
import { cn } from "@/lib/utils"

/**
 * A settings section's picture of what its fields decide: two or three
 * things and the lines between them, in the wiring vocabulary the Credentials,
 * Notifications and overview pictures already speak (`deploy/wire.tsx`).
 *
 * Framed, because a picture needs an edge to read as one thing (§2's one
 * exception in the deployment section), and on the card's ground so it sits
 * as a figure among the fields rather than as another field.
 *
 * Wide, the marks stand in one row — the start edge's nodes with their words
 * before the mark, the middle one's under it, the end one's after it — so
 * every line leaves a mark over nothing. Narrow, they stand in one column with
 * the words to the right, and the lines run down the marks. `startLabel` is a
 * caption written along the first gap, from `lg` where there is a gap to
 * write it in.
 *
 * The lines are the caller's `AnimatedBeam`s, measured against
 * `containerRef`, because only the caller knows what each line carries.
 */
export function SettingPicture({
  label,
  containerRef,
  lines,
  start,
  middle,
  end,
  startLabel,
  className,
}: {
  /** What the picture shows, as the list's accessible name. */
  label: string
  containerRef: RefObject<HTMLDivElement | null>
  lines: React.ReactNode
  /** The start edge's node, or several stacked: each a `WireNode align="end"`. */
  start: React.ReactNode[]
  /** A `WireNode align="center"`, or nothing for a picture of two things. */
  middle?: React.ReactNode
  /** A `WireNode align="start"`. */
  end: React.ReactNode
  startLabel?: React.ReactNode
  className?: string
}) {
  return (
    <div
      className={cn(
        "animate-rise overflow-hidden rounded-xl border bg-card px-4 py-5 sm:px-6",
        className,
      )}
    >
      <div ref={containerRef} className="relative">
        {lines}
        <ol
          aria-label={label}
          className={cn(
            "flex flex-col gap-5 lg:grid lg:min-h-24 lg:items-center lg:gap-0",
            // Room under the row for the middle mark's words, which hang
            // beneath it rather than sharing its line. A stacked start edge
            // is already most of that height, so it needs only the overhang.
            middle && (start.length > 1 ? "lg:pb-4" : "lg:pb-10"),
            middle
              ? "lg:grid-cols-[minmax(0,1fr)_minmax(2.5rem,0.45fr)_auto_minmax(2.5rem,0.45fr)_minmax(0,1fr)]"
              : "lg:grid-cols-[minmax(0,1fr)_minmax(4rem,0.6fr)_minmax(0,1fr)]",
          )}
        >
          <li className="min-w-0">
            <ul className="flex flex-col gap-5 lg:items-end lg:gap-4">
              {start.map((node, index) => (
                <li key={index} className="min-w-0">
                  {node}
                </li>
              ))}
            </ul>
          </li>
          <li aria-hidden className="relative hidden self-stretch lg:block">
            {startLabel}
          </li>
          {middle && (
            <>
              <li className="min-w-0">{middle}</li>
              <li aria-hidden className="hidden lg:block" />
            </>
          )}
          <li className="min-w-0">{end}</li>
        </ol>
      </div>
    </div>
  )
}
