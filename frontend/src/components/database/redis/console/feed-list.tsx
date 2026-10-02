"use client"

import { useCallback, useEffect, useRef } from "react"
import { cn } from "@/lib/utils"
import { useRowWindow } from "@/components/database/redis/use-row-window"

const ROW_HEIGHT = 24

/**
 * A feed's lines, newest last, held to the bottom while they arrive.
 *
 * The list follows its last line for as long as the reader is at it: scroll
 * up to read something and it stays where it was put, scroll back down and
 * it follows again. Only the lines on screen are drawn — a feed is thousands
 * of them.
 */
export function FeedList<R>({
  label,
  rows,
  row,
  className,
}: {
  label: string
  rows: readonly R[]
  /** One line, drawn inside a row of fixed height. */
  row: (entry: R) => React.ReactNode
  className?: string
}) {
  const { attach, onScroll, slice, height, offset } = useRowWindow(rows.length, ROW_HEIGHT)
  const scroller = useRef<HTMLDivElement | null>(null)
  const following = useRef(true)
  // One callback for the element's life: a new one each render would be
  // detached and attached again each render.
  const hold = useCallback(
    (node: HTMLDivElement | null) => {
      scroller.current = node
      attach(node)
    },
    [attach],
  )

  useEffect(() => {
    const el = scroller.current
    if (el && following.current) el.scrollTop = el.scrollHeight
  }, [rows.length])

  return (
    <div
      ref={hold}
      onScroll={(event) => {
        const el = event.currentTarget
        following.current = el.scrollHeight - el.scrollTop - el.clientHeight < ROW_HEIGHT * 2
        onScroll()
      }}
      role="log"
      aria-label={label}
      tabIndex={0}
      className={cn(
        "min-h-0 flex-1 overflow-auto bg-surface-sunken font-mono text-xs focus-ring-inset",
        className,
      )}
    >
      <div style={{ height }} className="relative">
        <div style={{ transform: `translateY(${offset}px)` }}>
          {rows.slice(slice.start, slice.end).map((entry, index) => (
            <div
              key={slice.start + index}
              className="flex h-6 min-w-0 items-center gap-3 px-3 whitespace-nowrap hover:bg-row-hover"
            >
              {row(entry)}
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}
