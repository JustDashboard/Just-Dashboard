"use client"

import { StatusDot, type DotTone } from "@/components/status-dot"
import { cn } from "@/lib/utils"
import { ProductGlyph, programProduct } from "@/components/product-logo"

/**
 * The mark beside a window tab or a session row: one dot, whose colour is the
 * state it is in. Red when this browser's connection to it has dropped, green
 * and breathing while something in it is working, green and still once that
 * has finished — until the finish has been seen — and yellow when it is idle,
 * which includes a program that is merely open: an editor, or an agent waiting
 * for its next message, holds the terminal and does nothing.
 *
 * It used to be a breathing dot for working and a check for finished, with
 * nothing at all otherwise, so a tab said something only while it was busy —
 * and a row and a tab could carry the two different marks for the same moment.
 */
export function ActivityMark({
  working,
  finished,
  disconnected,
  className,
}: {
  working: boolean
  finished: boolean
  disconnected: boolean
  className?: string
}) {
  const [state, tone, label]: [string, DotTone, string | undefined] = disconnected
    ? ["disconnected", "danger", "Disconnected"]
    : working
      ? ["working", "running", "Working"]
      : finished
        ? ["finished", "running", "Finished"]
        : // Idle is the resting state, so a screen reader is not told it on
          // every tab: the three that are news are named, as before.
          ["idle", "warning", undefined]
  return (
    <span
      role={label ? "img" : undefined}
      aria-label={label}
      aria-hidden={label ? undefined : true}
      data-activity={state}
      className={cn("flex size-3 shrink-0 items-center justify-center", className)}
    >
      <StatusDot tone={tone} live={working && !disconnected} />
    </span>
  )
}

/**
 * The program a terminal is running, as the product it is — Claude, Neovim,
 * Node, psql — in a slot the width of the line's height, so a column of tabs
 * and rows says what each is doing before any of them is read. A shell at its
 * prompt, or a program with no mark of its own (`htop`, Codex, OpenCode), is
 * drawn as a terminal: left empty, an agent the list had no logo for read as
 * a window with nothing in it, and a tab with no mark as a different kind of tab.
 */
export function ProgramMark({ process }: { process?: string }) {
  const product = programProduct(process)
  if (product) return <ProductGlyph id={product} />
  return (
    // eslint-disable-next-line @next/next/no-img-element
    <img src="/logos/terminal.svg" alt="" aria-hidden="true" className="size-3.5 shrink-0" />
  )
}
