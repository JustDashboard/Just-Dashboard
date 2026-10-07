"use client"

import { useState, type FocusEvent, type PointerEvent } from "react"

const ROW = "[data-process-row], [data-workspace-item]"
const INSPECTION = `${ROW}, [role='dialog'], [role='menu'], [role='listbox']`

function inspecting(target: EventTarget | null) {
  return target instanceof Element && !!target.closest(INSPECTION)
}

export function orderedRows<T>(rows: T[], ids: string[], key: (row: T) => string) {
  const rank = new Map(ids.map((id, index) => [id, index]))
  return [...rows].sort((a, b) => (rank.get(key(a)) ?? Infinity) - (rank.get(key(b)) ?? Infinity))
}

/** Keep a moving ranking under the reader's pointer, focus or open detail. */
export function useInspectionOrder<T>(
  rows: T[],
  key: (row: T) => string,
  question = "",
  detailOpen = false,
) {
  const [held, setHeld] = useState<{
    question: string
    ids: string[]
    pointer: boolean
    focus: boolean
    detail: boolean
  } | null>(null)
  const current = held?.question === question ? held : null
  // A sibling sheet cannot bubble its focus through the list's own panel.
  if (
    detailOpen !== (current?.detail ?? false) ||
    (current && current.ids.length === 0 && rows.length > 0)
  ) {
    const next = {
      question,
      ids: current?.ids.length ? current.ids : rows.map(key),
      pointer: current?.pointer ?? false,
      focus: current?.focus ?? false,
      detail: detailOpen,
    }
    setHeld(next.pointer || next.focus || next.detail ? next : null)
  }

  const update = (source: "pointer" | "focus", active: boolean) => {
    setHeld((previous) => {
      const before = previous?.question === question ? previous : null
      if (!active && !before) return previous
      if (before?.[source] === active) return previous
      const next = {
        question,
        ids: before?.ids ?? rows.map(key),
        pointer: before?.pointer ?? false,
        focus: before?.focus ?? false,
        detail: before?.detail ?? detailOpen,
        [source]: active,
      }
      return next.pointer || next.focus || next.detail ? next : null
    })
  }

  return {
    rows: current ? orderedRows(rows, current.ids, key) : rows,
    held: current !== null,
    release: () => setHeld(null),
    bindings: {
      onPointerOverCapture: (event: PointerEvent<HTMLElement>) => {
        if (event.pointerType === "mouse") update("pointer", inspecting(event.target))
      },
      onPointerOutCapture: (event: PointerEvent<HTMLElement>) => {
        if (event.pointerType === "mouse" && !inspecting(event.relatedTarget))
          update("pointer", false)
      },
      onFocusCapture: (event: FocusEvent<HTMLElement>) => update("focus", inspecting(event.target)),
      onBlurCapture: (event: FocusEvent<HTMLElement>) => {
        if (!inspecting(event.relatedTarget)) update("focus", false)
      },
    },
  }
}
