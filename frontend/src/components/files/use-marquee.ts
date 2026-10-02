"use client"

import { useEffect, useRef, useState } from "react"
import { intersectingPaths, selectionRect, type Point, type SelectionRect } from "./selection"

type Gesture = {
  id: number
  root: HTMLElement
  scroll: HTMLElement
  start: Point
  pointer: Point
  origin: Point
  entries: { path: string; rect: SelectionRect }[]
  initial: Set<string>
  additive: boolean
  moved: boolean
  frame: number
}

/** Background gestures leave native entry dragging and touch scrolling in charge of themselves. */
export function useMarquee({
  scope,
  selected,
  onSelect,
  onClear,
}: {
  scope: string
  selected: Set<string>
  onSelect: (paths: Set<string>) => void
  onClear: () => void
}) {
  const gesture = useRef<Gesture | null>(null)
  const callbacks = useRef({ onSelect, onClear })
  const [painted, setBox] = useState<{ scope: string; rect: SelectionRect } | null>(null)
  useEffect(() => {
    callbacks.current = { onSelect, onClear }
  }, [onSelect, onClear])

  const finish = (cancel: boolean) => {
    const current = gesture.current
    if (!current) return
    gesture.current = null
    cancelAnimationFrame(current.frame)
    if (current.root.hasPointerCapture(current.id)) current.root.releasePointerCapture(current.id)
    setBox(null)
    if (cancel) callbacks.current.onSelect(current.initial)
    else if (!current.moved && !current.additive) callbacks.current.onClear()
  }

  useEffect(() => {
    const cancel = (event: KeyboardEvent) => {
      if (event.key !== "Escape" || !gesture.current) return
      event.preventDefault()
      event.stopImmediatePropagation()
      finish(true)
    }
    const blur = () => finish(true)
    window.addEventListener("keydown", cancel, true)
    window.addEventListener("blur", blur)
    return () => {
      window.removeEventListener("keydown", cancel, true)
      window.removeEventListener("blur", blur)
      // A navigation or view switch must not keep selecting the directory left behind.
      const current = gesture.current
      gesture.current = null
      if (current) {
        cancelAnimationFrame(current.frame)
        if (current.root.hasPointerCapture(current.id))
          current.root.releasePointerCapture(current.id)
      }
    }
  }, [scope])

  const paint = (current: Gesture) => {
    const viewport = current.scroll.getBoundingClientRect()
    const root = current.root.getBoundingClientRect()
    const end = {
      x:
        Math.max(0, Math.min(current.scroll.clientWidth, current.pointer.x - viewport.left)) +
        current.scroll.scrollLeft,
      y:
        Math.max(0, Math.min(current.scroll.clientHeight, current.pointer.y - viewport.top)) +
        current.scroll.scrollTop,
    }
    const rect = selectionRect(current.start, end)
    callbacks.current.onSelect(
      intersectingPaths(rect, current.entries, current.additive ? current.initial : []),
    )
    setBox({
      scope,
      rect: {
        left: Math.max(
          viewport.left - root.left,
          rect.left - current.scroll.scrollLeft + viewport.left - root.left,
        ),
        top: Math.max(
          viewport.top - root.top,
          rect.top - current.scroll.scrollTop + viewport.top - root.top,
        ),
        right: Math.min(
          viewport.right - root.left,
          rect.right - current.scroll.scrollLeft + viewport.left - root.left,
        ),
        bottom: Math.min(
          viewport.bottom - root.top,
          rect.bottom - current.scroll.scrollTop + viewport.top - root.top,
        ),
      },
    })
  }

  const tick = (current: Gesture) => {
    if (gesture.current !== current) return
    if (current.moved) {
      const viewport = current.scroll.getBoundingClientRect()
      const speed = (value: number, lo: number, hi: number) => {
        if (value < lo + 28) return -Math.min(14, (lo + 28 - value) / 2)
        if (value > hi - 28) return Math.min(14, (value - hi + 28) / 2)
        return 0
      }
      const beforeX = current.scroll.scrollLeft
      const beforeY = current.scroll.scrollTop
      current.scroll.scrollLeft += speed(current.pointer.x, viewport.left, viewport.right)
      current.scroll.scrollTop += speed(current.pointer.y, viewport.top, viewport.bottom)
      if (beforeX !== current.scroll.scrollLeft || beforeY !== current.scroll.scrollTop)
        paint(current)
    }
    current.frame = requestAnimationFrame(() => tick(current))
  }

  const handlers = {
    onPointerDown: (event: React.PointerEvent<HTMLDivElement>) => {
      if (event.button !== 0 || event.pointerType !== "mouse" || gesture.current) return
      if (
        (event.target as HTMLElement).closest(
          "[data-entry-path], button, a, input, [role='checkbox'], thead, tr, [data-file-actions]",
        )
      )
        return
      const root = event.currentTarget
      const scroll = root.querySelector<HTMLElement>(
        "[data-file-scroll], [data-slot='table-container']",
      )
      if (!scroll || !scroll.contains(event.target as Node)) return
      event.preventDefault()
      const viewport = scroll.getBoundingClientRect()
      const offset = { x: scroll.scrollLeft - viewport.left, y: scroll.scrollTop - viewport.top }
      const current: Gesture = {
        id: event.pointerId,
        root,
        scroll,
        start: { x: event.clientX + offset.x, y: event.clientY + offset.y },
        pointer: { x: event.clientX, y: event.clientY },
        origin: { x: event.clientX, y: event.clientY },
        entries: Array.from(scroll.querySelectorAll<HTMLElement>("[data-entry-path]")).map((el) => {
          const rect = el.getBoundingClientRect()
          return {
            path: el.dataset.entryPath!,
            rect: {
              left: rect.left + offset.x,
              right: rect.right + offset.x,
              top: rect.top + offset.y,
              bottom: rect.bottom + offset.y,
            },
          }
        }),
        initial: new Set(selected),
        additive: event.shiftKey || event.ctrlKey || event.metaKey,
        moved: false,
        frame: 0,
      }
      gesture.current = current
      root.setPointerCapture(event.pointerId)
      current.frame = requestAnimationFrame(() => tick(current))
    },
    onPointerMove: (event: React.PointerEvent<HTMLDivElement>) => {
      const current = gesture.current
      if (!current || current.id !== event.pointerId) return
      current.pointer = { x: event.clientX, y: event.clientY }
      current.moved ||=
        Math.hypot(event.clientX - current.origin.x, event.clientY - current.origin.y) >= 5
      if (current.moved) paint(current)
    },
    onPointerUp: (event: React.PointerEvent<HTMLDivElement>) => {
      if (gesture.current?.id === event.pointerId) finish(false)
    },
    onPointerCancel: () => finish(true),
    onLostPointerCapture: () => finish(true),
  }
  return { box: painted?.scope === scope ? painted.rect : null, handlers }
}
