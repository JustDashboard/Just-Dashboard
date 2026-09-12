"use client"

import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { cn } from "@/lib/utils"

/**
 * The reveal rule, in one place.
 *
 * A row's actions appear when the pointer is over the row. That sentence hides
 * four separate mechanisms, and the ones that were forgotten are the ones that
 * broke: this existed as a hand-typed class string in roughly forty places and
 * had drifted into six variants — some with `focus-within`, some with
 * `focus-visible` on a container that never takes focus itself, some with no
 * touch fallback at all, which left the actions permanently unreachable on a
 * phone. Stated once:
 *
 *   pointer     — `group-hover`, so the row above needs `group`.
 *   keyboard    — the focus lands either on the revealed control itself
 *                 (`focus-visible`) or on the row around it
 *                 (`group-focus-visible`). A decorative mark — the external-link
 *                 arrow on a pull request, the copy glyph inside a command chip
 *                 — can never be focused, so keying only on its own focus
 *                 leaves it invisible to a keyboard.
 *   open menu   — stays visible while a dropdown inside it is open, or the
 *                 menu detaches from the row it belongs to.
 *   touch       — always visible: there is no hover to reveal it with.
 *
 * `group` is the variant's default name. Rows that already own a named group —
 * a bookmark in the places rail, a segment of the path bar — name it, or the
 * rule keys off whichever ancestor happened to be plain `group`.
 *
 * The named forms are spelled out rather than interpolated: Tailwind reads the
 * source for literal class names, so a variant built from a template string at
 * runtime is a class that never gets generated.
 */
const REVEAL_BASE =
  "opacity-0 transition-opacity focus-visible:opacity-100 data-[state=open]:opacity-100 [@media(hover:none)]:opacity-100"

const REVEAL_BY_GROUP = {
  default: "group-hover:opacity-100 group-focus-visible:opacity-100",
  row: "group-hover/row:opacity-100 group-focus-visible/row:opacity-100",
  path: "group-hover/path:opacity-100 group-focus-visible/path:opacity-100",
  folder: "group-hover/folder:opacity-100 group-focus-visible/folder:opacity-100",
} as const

export type RevealGroup = keyof typeof REVEAL_BY_GROUP

export function rowReveal(group: RevealGroup = "default") {
  return `${REVEAL_BASE} ${REVEAL_BY_GROUP[group]}`
}

/** The unnamed-group form, for the callers that only need the string. */
export const ROW_REVEAL = rowReveal()

export function RowActions({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      className={cn(
        "flex shrink-0 items-center gap-0.5 opacity-0 transition-opacity group-hover:opacity-100 focus-within:opacity-100 has-[[data-state=open]]:opacity-100 [@media(hover:none)]:opacity-100",
        className,
      )}
      {...props}
    />
  )
}

/**
 * An icon-only control in a row of them: restart, download, delete.
 *
 * The label lives in a tooltip rather than a native `title`, which the browser
 * holds back for about a second — a long time to hover over an unlabelled
 * button before finding out it deletes something. It also becomes the button's
 * accessible name, which a `title` only weakly provides.
 *
 * It cannot be a `DropdownMenuTrigger asChild`: this renders a tooltip root
 * rather than an element, so there is nothing for the trigger to hand its props
 * to. A menu button is an ordinary `Button` carrying an `aria-label`.
 */
export function IconAction({
  label,
  reveal,
  revealGroup,
  className,
  ...props
}: React.ComponentProps<typeof Button> & {
  label: string
  /**
   * For the lone action that stands in a row without a `RowActions` around it:
   * the same appear-on-hover rule, so one button and a cluster of them behave
   * identically. The row above still needs `group`.
   */
  reveal?: boolean
  /** The row's group name, where it has one. */
  revealGroup?: RevealGroup
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          size="icon-sm"
          variant="ghost"
          aria-label={label}
          className={cn(
            "[&_svg:not([class*='size-'])]:size-3.5",
            reveal && rowReveal(revealGroup),
            className,
          )}
          {...props}
        />
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  )
}
