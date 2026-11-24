"use client"

import { useCallback, useLayoutEffect, useRef, useState } from "react"
import { Tabs as TabsPrimitive } from "radix-ui"
import { cn } from "@/lib/utils"
import { ChipCount } from "@/components/chips"
import { StatusDot, type DotTone } from "@/components/status-dot"

/**
 * One interaction language for every switcher in the product.
 *
 * The problem this fixes is small and everywhere: selection and keyboard focus
 * were drawn the same way. A filter chip that is *active* and one that merely
 * has the caret on it both got the accent outline, so a keyboard user could not
 * tell which filters were applied, and a mouse user tabbing away found the
 * selection had apparently moved.
 *
 * The three states are given three different mechanisms, which is the only way
 * they stay distinguishable:
 *
 *   selected — filled surface and foreground text. A property of the data.
 *   hover    — a faint background. A property of the pointer.
 *   focus    — a ring, drawn outside the control. A property of the keyboard.
 *
 * A control can be all three at once and still be readable, which is the test.
 *
 * Everything in this file switches between views of one page, never between
 * pages: the rail drills into a section and lists its pages there.
 */

// The quiet tab Just Hub draws (its `.stitch/DESIGN.md` 4, Tabs and chips):
// the body size (§8) in the muted ink, and the view on screen in the
// foreground on a control face. No underline: a blue strip under the chosen
// word was a colour bar, the mark this product draws nowhere else.
//
// The face sits 4px in from every side of the tab: a transparent 4px border
// with the fill clipped inside it. The tab keeps its strip's height (40px, 36
// in a pane) as its press box, so no strip moved, and the inset ring lands in
// that border, 2px clear of the face. That is the Hub's ring, and a scroller
// cannot clip it. The face takes the control's corner, so the box's corner is
// that plus the border. Forced colours paint a transparent border, so it comes
// off there.
const TAB_BASE = cn(
  "inline-flex shrink-0 items-center gap-1.5 px-2 text-body font-medium whitespace-nowrap focus-ring-inset transition-colors",
  "rounded-[calc(var(--radius-md)+4px)] border-4 border-transparent bg-clip-padding forced-colors:border-0 forced-colors:px-3",
)

/**
 * The quiet-tab look, for a switcher between views of the page you are
 * already on.
 *
 * @deprecated use `ViewTabs`, which draws the strip, the roles and the
 * scrolling with it. The wave-2 passes move the other callers.
 */
export function tabClasses(selected: boolean | undefined, height: string) {
  return cn(
    TAB_BASE,
    height,
    selected
      ? // A count inside the chosen tab takes the label's ink, not the faint one.
        "bg-control text-foreground [--count-ink:currentColor]"
      : "text-muted-foreground hover:bg-accent hover:text-foreground",
  )
}

// Keyed on Radix's own state, so a strip that stands inside a caller's `Tabs`
// is drawn right without being told which tab is chosen.
const TRIGGER = cn(
  TAB_BASE,
  "data-[state=active]:bg-control data-[state=active]:text-foreground data-[state=active]:[--count-ink:currentColor]",
  "data-[state=inactive]:text-muted-foreground data-[state=inactive]:hover:bg-accent data-[state=inactive]:hover:text-foreground",
  "disabled:pointer-events-none disabled:text-faint",
)

const HEIGHT = { md: "h-10", sm: "h-9" } as const

/** One view in the strip. */
export type TabView<T extends string> = {
  key: T
  label: React.ReactNode
  /** Beside the label; nothing at zero, so an empty view does not shout "0". */
  count?: number
  /** A mark that something in the view wants a look: an unsaved edit, a failure. */
  dot?: { tone: DotTone; label: string }
  disabled?: boolean
}

type Fade = "none" | "start" | "end" | "both"

// A shade where the strip runs on past the edge, so a cut tab reads as more
// rather than as a word cut short ('Stora', 'U'). Whole class names: Tailwind
// reads the source.
const FADE: Record<Fade, string | undefined> = {
  none: undefined,
  end: "[mask-image:linear-gradient(to_right,black_calc(100%-2rem),transparent)]",
  start: "[mask-image:linear-gradient(to_left,black_calc(100%-2rem),transparent)]",
  both: "[mask-image:linear-gradient(to_right,transparent,black_2rem,black_calc(100%-2rem),transparent)]",
}

// The room kept clear of the shade when the chosen tab is scrolled to, so it
// lands in full ink rather than under the fade.
const FADE_ROOM = 32

function fadeOf(node: HTMLElement): Fade {
  const before = node.scrollLeft > 1
  const after = node.scrollLeft + node.clientWidth < node.scrollWidth - 1
  return before && after ? "both" : before ? "start" : after ? "end" : "none"
}

/** Scrolls the strip sideways only: a strip below the fold must not move the page. */
function reveal(list: HTMLElement, tab: HTMLElement) {
  if (list.scrollWidth <= list.clientWidth) return
  const box = list.getBoundingClientRect()
  const at = tab.getBoundingClientRect()
  if (at.left < box.left + FADE_ROOM) list.scrollLeft -= box.left + FADE_ROOM - at.left
  else if (at.right > box.right - FADE_ROOM) list.scrollLeft += at.right - (box.right - FADE_ROOM)
}

type StripProps<T extends string> = {
  /** The strip's accessible name: "Views of nginx.service". */
  label: string
  views: TabView<T>[]
  /** The view on screen; the strip scrolls it into view when it changes. */
  value: T
  /** `md` is a page's strip (40px); `sm` a pane's or a workbench's (36px). */
  size?: keyof typeof HEIGHT
  /** The labels are names a machine wrote: units, keys, files. */
  mono?: boolean
  /** Hold the strip at the top of the scroller it is in. */
  sticky?: boolean
  /** Controls at the strip's right end, outside the tab list: a search, a verb. */
  trailing?: React.ReactNode
  /** Draw nothing when there is one view or none: one tab is a title, not a choice. */
  hideWhenSingle?: boolean
  className?: string
}

/**
 * The strip of a page's views, standing inside a `Tabs` the caller holds
 * (`ui/tabs`), whose `TabsContent` are the panels. `ViewTabs` with
 * `onValueChange` holds the `Tabs` itself.
 */
export function ViewTabList<T extends string>({
  label,
  views,
  value,
  size = "md",
  mono,
  sticky,
  trailing,
  hideWhenSingle,
  className,
}: StripProps<T>) {
  const list = useRef<HTMLDivElement>(null)
  const [fade, setFade] = useState<Fade>("none")

  const measure = useCallback(() => {
    const node = list.current
    if (node) setFade(fadeOf(node))
  }, [])

  // Whether the strip overflows changes with the window, the labels and the
  // counts, so it is measured whenever the strip changes size.
  useLayoutEffect(() => {
    const node = list.current
    if (!node) return
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(node)
    for (const child of node.children) observer.observe(child)
    return () => observer.disconnect()
  }, [measure, views.length])

  // The chosen tab is brought into view at mount and on every change: a
  // `?tab=` link to the fifth view opened with it off the screen.
  useLayoutEffect(() => {
    const node = list.current
    const tab = node?.querySelector<HTMLElement>('[role="tab"][data-state="active"]')
    if (node && tab) {
      reveal(node, tab)
      setFade(fadeOf(node))
    }
  }, [value, views.length])

  if (hideWhenSingle && views.length < 2) return null

  return (
    <div
      data-slot="view-tabs"
      className={cn(
        "flex min-w-0 shrink-0 items-stretch border-b border-hairline",
        sticky && "sticky top-0 z-sticky [background-color:var(--panel-ground,var(--background))]",
        className,
      )}
    >
      <TabsPrimitive.List
        ref={list}
        aria-label={label}
        onScroll={measure}
        className={cn(
          "flex min-w-0 flex-1 [scrollbar-width:none] gap-1 overflow-x-auto [&::-webkit-scrollbar]:hidden",
          FADE[fade],
        )}
      >
        {views.map((view) => (
          <TabsPrimitive.Trigger
            key={view.key}
            value={view.key}
            disabled={view.disabled}
            className={cn(TRIGGER, HEIGHT[size], mono && "font-mono")}
          >
            {view.label}
            {view.count !== undefined && view.count > 0 && <ChipCount>{view.count}</ChipCount>}
            {view.dot && (
              <span title={view.dot.label} className="flex items-center">
                <StatusDot tone={view.dot.tone} />
                <span className="sr-only">, {view.dot.label}</span>
              </span>
            )}
          </TabsPrimitive.Trigger>
        ))}
      </TabsPrimitive.List>
      {trailing && <div className="flex shrink-0 items-center gap-1.5 pl-2">{trailing}</div>}
    </div>
  )
}

/**
 * The strip of a page's views, with real tab semantics: Radix Tabs,
 * so the arrow keys move along it, Home and End reach its ends, and each tab
 * names the panel it shows.
 *
 * Given `onValueChange` it holds the `Tabs` and its `children` are the panels
 * (`TabsContent` from `ui/tabs`); without, it is the strip alone and stands
 * inside the caller's `Tabs`. Pair it with `useViewTab` to keep the view in
 * the address.
 *
 * The strip scrolls sideways when the views do not fit, shades the edge they
 * run under, and brings the chosen tab into view. A strip that changes route
 * is links, not tabs; a closable, renamable strip is the editor's `TabStrip`.
 */
export function ViewTabs<T extends string>({
  onValueChange,
  children,
  rootClassName,
  ...strip
}: StripProps<T> & {
  onValueChange?: (value: T) => void
  /** The panels, when the strip holds the `Tabs`. */
  children?: React.ReactNode
  /** The `Tabs` it holds: its place in the page's layout. */
  rootClassName?: string
}) {
  if (!onValueChange) return <ViewTabList {...strip} />
  return (
    <TabsPrimitive.Root
      value={strip.value}
      onValueChange={(next) => onValueChange(next as T)}
      className={cn("flex min-w-0 flex-col", rootClassName)}
    >
      <ViewTabList {...strip} />
      {children}
    </TabsPrimitive.Root>
  )
}
