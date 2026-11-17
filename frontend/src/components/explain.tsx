"use client"

import * as React from "react"
import { Popover as PopoverPrimitive } from "radix-ui"

import { Information, Question } from "@/components/icons"
import { cn } from "@/lib/utils"
import { usePortalContainer } from "@/lib/portal-container"
import { keepMovedFocus } from "@/components/ui/popover"

/**
 * A short explanation one step from the thing it explains: the paragraph
 * behind a setting's ⓘ, a definition behind a dotted term, the consequence
 * behind a segment.
 *
 * There were five of these — `InfoTip` on a Tooltip, Docker's `Term` and
 * `ExplainIcon` and git's copy of them on a HoverCard, and the settings'
 * `HoverTip` — and none of them opened on a phone. A tooltip closes on a tap,
 * and a hover card ignores touch, so on a touch screen the reasoning a form
 * said it kept "behind the ⓘ" was behind nothing. This is one toggletip, on a
 * Radix Popover:
 *
 * - a mouse resting on the mark opens it, and it stays while the pointer
 *   moves onto it (WCAG 1.4.13, which the tooltip had turned off);
 * - keyboard focus opens it, and the focus moving on closes it;
 * - a press, a tap, Enter or Space pins it open until Escape, a press
 *   outside, or the mark pressed again.
 *
 * Opened by hover or focus it is a `tooltip` that describes the mark, so a
 * screen reader reads it with the mark and the focus stays where the reader
 * put it. Pinned, it is a `dialog` named by `label`: the focus moves into it
 * and back to the mark when it closes. One is open at a time, as tooltips are.
 *
 * The press box is 24px, and 44px under a coarse pointer, without the glyph or
 * its row growing.
 */

/** A titled definition, passed as a glossary keeps it. Drawn as a card. */
export type ExplainEntry = { title?: React.ReactNode; body: React.ReactNode }

type Shared = {
  /**
   * What the mark explains, for a screen reader: "What this setting does",
   * "Why the route to default stays". It names the pinned dialog too.
   */
  label?: string
  /** A definition with its title, drawn as a card rather than a bubble. */
  entry?: ExplainEntry
  side?: "top" | "right" | "bottom" | "left"
  align?: "start" | "center" | "end"
  /** On the mark, or merged onto the control `wrap` takes. */
  className?: string
  /** On the bubble or the card. */
  contentClassName?: string
}

export type ExplainProps =
  | (Shared & {
      /** An ⓘ after a label or a fact; the default. */
      face?: "icon"
      label: string
      /** `question` for a term in a header that has no sentence to hold it. */
      glyph?: "info" | "question"
      /** The explanation, when it is not an `entry`. */
      children?: React.ReactNode
    })
  | (Shared & {
      /** The words themselves, with a dotted underline, for a term inside a sentence. */
      face: "term"
      /** The explanation, when it is not an `entry`. */
      tip?: React.ReactNode
      /** The words. */
      children: React.ReactNode
    })
  | (Shared & {
      /**
       * A control that already does something — a segment, a switch — with
       * its consequence behind it. It draws no element of its own, so the
       * control stays the direct child its group reads, and it takes neither
       * the control's `data-state` nor its press: a mouse's press is the
       * control's and closes the tip, while a finger's opens it, since a
       * finger has no other way to it.
       */
      face: "wrap"
      tip?: React.ReactNode
      children: React.ReactElement
    })

type Opened = "hover" | "focus" | "press"

/** How long a resting mouse waits before it opens, and how long it is away before it closes. */
const OPEN_MS = 80
const CLOSE_MS = 150

/** The one open now: opening another closes it. */
let current: { key: string; close: () => void } | null = null

export function Explain(props: ExplainProps) {
  const { label, entry, side = "top", align = "center", className, contentClassName } = props
  const face = props.face ?? "icon"
  const key = React.useId()
  const bodyId = `${key}-explain`
  const [opened, setOpened] = React.useState<Opened | null>(null)
  // How it last opened, kept past the close: the close's focus rule reads it
  // after the state has already gone back to null.
  const how = React.useRef<Opened | null>(null)
  const timer = React.useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const mark = React.useRef<HTMLElement | null>(null)
  // The pointer of the press in progress. A focus during a press is the press,
  // not the keyboard, and a wrapped control opens only for a finger's.
  const pointer = React.useRef<string | null>(null)

  const close = React.useCallback(() => {
    clearTimeout(timer.current)
    setOpened(null)
  }, [])
  const open = React.useCallback(
    (next: Opened) => {
      clearTimeout(timer.current)
      if (current && current.key !== key) current.close()
      current = { key, close }
      how.current = next
      setOpened(next)
    },
    [key, close],
  )
  React.useEffect(
    () => () => {
      clearTimeout(timer.current)
      if (current?.key === key) current = null
    },
    [key],
  )
  // A tip a resting mouse opened is about what is under the mouse, and a
  // scroll moves that away without the pointer leaving the mark, so it
  // closes, as a tooltip does. One the keyboard opened stays: tabbing to a
  // mark below the fold scrolls the page to it (tooltip-focus.spec.ts).
  React.useEffect(() => {
    if (opened !== "hover") return
    // Only a scroll that moves the mark: a log scrolling itself beside it does not.
    const onScroll = (event: Event) => {
      const scrolled = event.target
      if (
        scrolled instanceof Document ||
        (scrolled instanceof Node && scrolled.contains(mark.current))
      )
        close()
    }
    window.addEventListener("scroll", onScroll, { capture: true, passive: true })
    return () => window.removeEventListener("scroll", onScroll, { capture: true })
  }, [opened, close])
  const after = (ms: number, run: () => void) => {
    clearTimeout(timer.current)
    timer.current = setTimeout(run, ms)
  }
  const isMouse = (event: React.PointerEvent) => event.pointerType === "mouse"

  const events = {
    onPointerEnter: (event: React.PointerEvent) => {
      if (!isMouse(event)) return
      if (opened) clearTimeout(timer.current)
      else after(OPEN_MS, () => open("hover"))
    },
    onPointerLeave: (event: React.PointerEvent) => {
      if (!isMouse(event)) return
      if (opened === "hover") after(CLOSE_MS, close)
      else if (!opened) clearTimeout(timer.current)
    },
    onPointerDown: (event: React.PointerEvent) => {
      pointer.current = event.pointerType
    },
    onFocus: (event: React.FocusEvent<HTMLElement>) => {
      if (opened) return
      // A wrapped control has its focus handed back when its own list closes,
      // which is not the reader asking, so only a visible focus opens it.
      const keyboard =
        face === "wrap" ? event.currentTarget.matches(":focus-visible") : pointer.current === null
      if (keyboard) open("focus")
    },
    onBlur: () => {
      pointer.current = null
      if (opened === "focus" || opened === "hover") close()
    },
    onClick: (event: React.MouseEvent) => {
      // Radix's own toggle would close a hovered tip on the press that means
      // "keep it"; the press is decided here instead.
      event.preventDefault()
      const by = pointer.current
      pointer.current = null
      if (face === "wrap") {
        if (by === "touch" || by === "pen") {
          if (opened) close()
          else open("press")
        } else close()
        return
      }
      if (opened === "press") close()
      else open("press")
    },
  }

  const tip = props.face === "term" || props.face === "wrap" ? props.tip : props.children
  const asTip = opened === "hover" || opened === "focus"
  const describedBy = asTip ? bodyId : undefined

  return (
    <PopoverPrimitive.Root
      open={opened !== null}
      // Radix asks only to close: Escape, a press or a focus outside.
      onOpenChange={(next) => (next ? open("press") : close())}
    >
      {props.face === "wrap" ? (
        <PopoverPrimitive.Anchor
          asChild
          ref={mark as React.Ref<HTMLDivElement>}
          aria-describedby={describedBy}
          className={className}
          {...events}
        >
          {props.children}
        </PopoverPrimitive.Anchor>
      ) : (
        <PopoverPrimitive.Trigger
          ref={mark as React.Ref<HTMLButtonElement>}
          type="button"
          data-slot="explain"
          data-face={face}
          aria-label={face === "icon" ? label : undefined}
          aria-describedby={describedBy}
          {...events}
          className={cn(
            "shrink-0 rounded-sm focus-ring transition-colors",
            face === "icon"
              ? // The box grows round the glyph and the margin gives the
                // growth back, so the press box is 24 (44 for a finger) and
                // the row it sits in keeps its height.
                "-m-1.25 inline-flex p-1.25 align-middle text-faint hover:text-foreground data-[state=open]:text-foreground pointer-coarse:-m-3.75 pointer-coarse:p-3.75"
              : "relative cursor-help underline decoration-dotted underline-offset-4 after:absolute after:inset-x-0 after:top-1/2 after:h-6 after:-translate-y-1/2 hover:text-foreground pointer-coarse:after:h-11",
            className,
          )}
        >
          {props.face === "term" ? (
            props.children
          ) : props.glyph === "question" ? (
            <Question aria-hidden className="size-3.5" />
          ) : (
            <Information aria-hidden className="size-3.5" />
          )}
        </PopoverPrimitive.Trigger>
      )}
      <PopoverPrimitive.Portal container={usePortalContainer()}>
        <PopoverPrimitive.Content
          data-slot="explain-content"
          data-opened={opened ?? undefined}
          role={asTip ? "tooltip" : "dialog"}
          aria-label={asTip ? undefined : label}
          // Radix keeps it mounted while it fades; closed, it is only paint,
          // and out of the accessibility tree at once.
          aria-hidden={opened === null ? true : undefined}
          side={side}
          align={align}
          sideOffset={entry ? 4 : 2}
          collisionPadding={8}
          // Hover and focus leave the focus where the reader put it. A press
          // hands it to the dialog, and Radix hands it back to the mark; a
          // wrapped control keeps it, since the press was its own.
          onOpenAutoFocus={(event) => {
            if (how.current !== "press" || face === "wrap") event.preventDefault()
          }}
          onCloseAutoFocus={(event) => {
            if (how.current !== "press" || face === "wrap") event.preventDefault()
            else keepMovedFocus(event)
          }}
          onInteractOutside={(event) => {
            // A press on the mark is the mark's toggle, not a press outside.
            if (mark.current?.contains(event.target as Node)) event.preventDefault()
          }}
          onPointerEnter={(event) => {
            if (isMouse(event)) clearTimeout(timer.current)
          }}
          onPointerLeave={(event) => {
            if (isMouse(event) && opened === "hover") after(CLOSE_MS, close)
          }}
          className={cn(
            "z-overlay origin-(--radix-popover-content-transform-origin) text-xs leading-relaxed outline-hidden data-[side=bottom]:slide-in-from-top-2 data-[side=left]:slide-in-from-right-2 data-[side=right]:slide-in-from-left-2 data-[side=top]:slide-in-from-bottom-2 data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=closed]:zoom-out-95 data-[state=open]:animate-in data-[state=open]:fade-in-0 data-[state=open]:zoom-in-95 motion-reduce:animate-none!",
            entry
              ? "w-80 max-w-(--radix-popover-content-available-width) rounded-md border bg-popover p-4 text-popover-foreground shadow-md"
              : "w-fit max-w-[min(20rem,var(--radix-popover-content-available-width))] rounded-md bg-foreground px-3 py-1.5 text-balance text-background",
            contentClassName,
          )}
        >
          <div id={bodyId}>
            {entry ? (
              <>
                {entry.title && <p className="mb-1 text-body font-medium">{entry.title}</p>}
                <div className="text-muted-foreground">{entry.body}</div>
              </>
            ) : (
              tip
            )}
          </div>
          {!entry && (
            <PopoverPrimitive.Arrow className="z-overlay size-2.5 translate-y-[calc(-50%_-_2px)] rotate-45 rounded-xs bg-foreground fill-foreground" />
          )}
        </PopoverPrimitive.Content>
      </PopoverPrimitive.Portal>
    </PopoverPrimitive.Root>
  )
}
