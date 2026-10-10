"use client"

import { useEffect, useRef, useState } from "react"
import { ChevronLeft, ChevronRight, Cross, Plus } from "@/components/icons"
import { cn } from "@/lib/utils"
import type { TerminalActivity, TerminalWindow as Window } from "@/lib/types"
import { windowActivity, windowLabel, windowProgram } from "@/lib/terminal-activity"
import { TERMINAL_WINDOW_DRAG } from "@/lib/terminal-layout"
import { Button } from "@/components/ui/button"
import { IconAction, rowReveal } from "@/components/icon-action"
import { ActivityMark, ProgramMark } from "@/components/terminal/activity-mark"

/** Compact direct-PTY windows for the terminal title bar. */
export function WindowStrip({
  windows,
  activeId,
  activity,
  disconnected,
  onSelect,
  onNew,
  newDisabled,
  onClose,
  onReorder,
  onDragStart,
  onDragEnd,
}: {
  windows: Window[]
  activeId: string | null
  /** The state each window's own socket last pushed, keyed by window id. */
  activity: Record<string, TerminalActivity>
  /** The windows whose socket in this browser has dropped, by window id. */
  disconnected: ReadonlySet<string>
  onSelect: (id: string) => void
  onNew: () => void
  newDisabled?: boolean
  onClose: (id: string) => void
  onReorder: (id: string, position: number) => void
  onDragStart: (id: string) => void
  onDragEnd: () => void
}) {
  const [dropAt, setDropAt] = useState<number | null>(null)
  const scrollerRef = useRef<HTMLDivElement>(null)
  // Which ends of the strip have tabs scrolled past them. The strip draws no
  // scrollbar — a ten-pixel bar across the tabs' labels in a 40px title bar
  // was the loudest thing on the page — so a fade at an edge, and a pair of
  // chevrons while anything is out of view, say there is more that way.
  const [overflow, setOverflow] = useState({ start: false, end: false })
  const scrolling = overflow.start || overflow.end
  const scrollBy = (direction: 1 | -1) => {
    const el = scrollerRef.current
    el?.scrollBy({ left: direction * el.clientWidth * 0.8, behavior: "smooth" })
  }
  useEffect(() => {
    const el = scrollerRef.current
    if (!el) return
    const measure = () => {
      const start = el.scrollLeft > 1
      const end = el.scrollLeft + el.clientWidth < el.scrollWidth - 1
      setOverflow((previous) =>
        previous.start === start && previous.end === end ? previous : { start, end },
      )
    }
    // A mouse wheel has no horizontal axis; without this the hidden overflow
    // was reachable only from a trackpad or by dragging a tab into it.
    const onWheel = (event: WheelEvent) => {
      if (Math.abs(event.deltaY) <= Math.abs(event.deltaX)) return
      if (el.scrollWidth <= el.clientWidth) return
      event.preventDefault()
      el.scrollLeft += event.deltaY
    }
    const observer = new ResizeObserver(measure)
    observer.observe(el)
    for (const tab of el.children) observer.observe(tab)
    el.addEventListener("scroll", measure, { passive: true })
    el.addEventListener("wheel", onWheel, { passive: false })
    measure()
    return () => {
      observer.disconnect()
      el.removeEventListener("scroll", measure)
      el.removeEventListener("wheel", onWheel)
    }
  }, [windows])
  const mask = `linear-gradient(to right, ${
    overflow.start ? "transparent, black 1.5rem" : "black"
  }, ${overflow.end ? "black calc(100% - 1.5rem), transparent" : "black"})`
  return (
    <div className="flex min-w-0 flex-1 items-center gap-0.5">
      {scrolling && (
        <IconAction
          label="Scroll tabs left"
          className="size-6 shrink-0 text-muted-foreground"
          disabled={!overflow.start}
          onClick={() => scrollBy(-1)}
        >
          <ChevronLeft />
        </IconAction>
      )}
      <div
        ref={scrollerRef}
        className="flex min-w-0 [scrollbar-width:none] items-center gap-0.5 overflow-x-auto [&::-webkit-scrollbar]:hidden"
        style={{ maskImage: mask }}
        aria-label="Terminal windows"
        data-overflow-start={overflow.start || undefined}
        data-overflow-end={overflow.end || undefined}
        onDragLeave={(event) => {
          if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setDropAt(null)
        }}
      >
        {windows.map((window, position) => (
          <WindowTab
            key={window.id}
            window={window}
            live={activity[window.id]}
            disconnected={disconnected.has(window.id)}
            active={window.id === activeId}
            inserting={dropAt === position}
            onSelect={() => onSelect(window.id)}
            onClose={() => onClose(window.id)}
            onDragOver={(event) => {
              if (!event.dataTransfer.types.includes(TERMINAL_WINDOW_DRAG)) return
              event.preventDefault()
              event.dataTransfer.dropEffect = "move"
              setDropAt(position)
            }}
            onDrop={(event) => {
              event.preventDefault()
              setDropAt(null)
              const id = event.dataTransfer.getData(TERMINAL_WINDOW_DRAG)
              if (id && id !== window.id) onReorder(id, window.index)
              onDragEnd()
            }}
            onDragStart={() => onDragStart(window.id)}
            onDragEnd={() => {
              setDropAt(null)
              onDragEnd()
            }}
          />
        ))}
      </div>
      {scrolling && (
        <IconAction
          label="Scroll tabs right"
          className="size-6 shrink-0 text-muted-foreground"
          disabled={!overflow.end}
          onClick={() => scrollBy(1)}
        >
          <ChevronRight />
        </IconAction>
      )}
      {/* Outside the scroller, so it stays beside the last tab rather than
          scrolling away with the tabs it would add to. */}
      <IconAction
        label="New window"
        className="size-7 shrink-0"
        disabled={newDisabled}
        onClick={onNew}
      >
        <Plus />
      </IconAction>
    </div>
  )
}

function WindowTab({
  window,
  live,
  disconnected,
  active,
  inserting,
  onSelect,
  onClose,
  onDragOver,
  onDrop,
  onDragStart,
  onDragEnd,
}: {
  window: Window
  live?: TerminalActivity
  disconnected: boolean
  active: boolean
  inserting: boolean
  onSelect: () => void
  onClose: () => void
  onDragOver: (event: React.DragEvent) => void
  onDrop: (event: React.DragEvent) => void
  onDragStart: () => void
  onDragEnd: () => void
}) {
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (active) ref.current?.scrollIntoView({ block: "nearest", inline: "nearest" })
  }, [active])
  // The tab reads like a desktop terminal's title bar: the program's own
  // title while one runs and the directory at the prompt. The dot at the end,
  // beside the close, says whether it is working, idle or has lost its
  // connection — the reason to glance at a tab you are not in — and sits
  // where the session rail puts it, so the two read the same way.
  const label = windowLabel(window, live)
  const state = windowActivity(window, live)
  const working = Boolean(state.working)
  const hint = disconnected
    ? `${label} — disconnected`
    : working
      ? `${label} — working`
      : state.busy && state.process
        ? `${label} — running ${state.process}`
        : label
  return (
    <div
      ref={ref}
      draggable
      data-window={window.id}
      data-active={active}
      data-busy={state.busy || undefined}
      data-working={working || undefined}
      data-disconnected={disconnected || undefined}
      onDragStart={(event) => {
        event.dataTransfer.effectAllowed = "move"
        event.dataTransfer.setData(TERMINAL_WINDOW_DRAG, window.id)
        onDragStart()
      }}
      onDragOver={onDragOver}
      onDrop={onDrop}
      onDragEnd={onDragEnd}
      className={cn(
        // Tabs give up width before the strip scrolls, the way a browser's do,
        // but only down to a width that still reads a word of the label: a
        // row of "cl…" tabs that all fit says less than one that scrolls.
        "group flex h-7 max-w-44 min-w-32 shrink items-center rounded-md border border-transparent pr-0.5 pl-2.5 transition-colors",
        active
          ? "bg-accent text-foreground"
          : "text-muted-foreground hover:bg-row-hover hover:text-foreground",
        inserting && "border-l-primary",
      )}
    >
      <button
        aria-current={active ? "page" : undefined}
        title={hint}
        className="flex h-full min-w-0 flex-1 items-center gap-1.5 rounded-sm focus-ring-inset"
        onClick={onSelect}
      >
        <ProgramMark process={windowProgram(window, live)} />
        <span className="min-w-0 flex-1 truncate text-xs font-medium">{label}</span>
        <ActivityMark working={working} disconnected={disconnected} />
      </button>
      {/* Close sits on the tab itself rather than in a menu, and appears under
          the pointer, as a browser's does; the active tab keeps it. */}
      <Button
        size="icon-sm"
        variant="ghost"
        aria-label={`Close window ${label}`}
        className={cn(
          "size-6 shrink-0 text-muted-foreground hover:text-destructive",
          !active && rowReveal(),
        )}
        onClick={onClose}
      >
        <Cross className="size-3" />
      </Button>
    </div>
  )
}
