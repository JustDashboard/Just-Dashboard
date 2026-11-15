"use client"

import { ArrowRight } from "@/components/icons"
import { useChoicePress } from "@/components/choice-row"
import { BlurFade } from "@/components/ui/blur-fade"
import { SpotlightBorder } from "@/components/ui/spotlight-border"
import { cn } from "@/lib/utils"

/**
 * A large card you open: a repository on its shelf, a drawing with its
 * preview. The card is pressable anywhere, and its name is the control.
 *
 * Five cards had copied this shell by hand — the `BlurFade`, the lit edge, a
 * `<div>` taking the press behind the same `jsx-a11y` comment, a filter for
 * presses on the card's own controls, and a `<button>` on the name for the
 * keyboard — and lost what `ChoiceCard` had learnt on the way: a press that
 * ends a text selection is a selection, so selecting a repository's path no
 * longer opens it, and Cmd, Ctrl or Shift opens `href` in a new tab. Both
 * come from `useChoicePress`, the rule `ChoiceRow` and `ChoiceCard` take.
 *
 * The anatomy, top to bottom: `media` (a preview across the card's top), the
 * head — `mark`, the name with `aside` at the end of its line, `subtitle`
 * under it, then the arrow and the `menu` last (→ then ⋯, the order every card
 * and `ChoiceRow` ends in) — then `children`, the body, and `foot` on the
 * card's floor, so a run of cards of different bodies still lines its feet up.
 *
 * `ChoiceCard` is not this: it *picks*, and its selection is its edge. This
 * opens, and carries no selection.
 */
export function OpenCard({
  index = 0,
  onOpen,
  href,
  title,
  titleHint,
  mark,
  aside,
  subtitle,
  media,
  menu,
  foot,
  primary,
  children,
  className,
}: {
  /** Position on the shelf, for the arrival stagger (capped). */
  index?: number
  onOpen: () => void
  /** The address the card stands for, which a modified press opens in a new tab. */
  href?: string
  /** The name: the control's text and its accessible name. */
  title: string
  /** The name's native tooltip: the full path a truncated name stands for. */
  titleHint?: string
  /** What it is, drawn as itself, before the name. */
  mark?: React.ReactNode
  /** A reading at the end of the name's line: how far a branch has drifted. */
  aside?: React.ReactNode
  /** The lines under the name. */
  subtitle?: React.ReactNode
  /** A preview across the top of the card. */
  media?: React.ReactNode
  /** The card's `VerbMenu`, drawn after the arrow. */
  menu?: React.ReactNode
  /** The card's floor: what it links, what is waiting on it. */
  foot?: React.ReactNode
  /** Marks the name as the item's primary control for the workspace's own reads. */
  primary?: boolean
  children?: React.ReactNode
  className?: string
}) {
  const press = useChoicePress({ href, onSelect: onOpen })
  return (
    <BlurFade delay={Math.min(index, 11) * 0.03} className="h-full">
      <SpotlightBorder className="h-full">
        {/* eslint-disable-next-line jsx-a11y/click-events-have-key-events, jsx-a11y/no-static-element-interactions -- the pointer's larger target: the card's name is the button a keyboard opens it by */}
        <div
          data-slot="open-card"
          onClick={press.onClick}
          className={cn(
            "group group/choice flex h-full min-w-0 cursor-pointer flex-col rounded-xl",
            className,
          )}
        >
          {media}
          <div className={cn("flex min-w-0 items-start gap-3 px-3.5", media ? "py-3" : "pt-3.5")}>
            {mark}
            <div className="min-w-0 flex-1">
              <div className="flex min-w-0 items-center gap-2">
                <button
                  type="button"
                  data-workspace-primary={primary ? "" : undefined}
                  title={titleHint}
                  // The card's own handler already answers the pointer; this
                  // one is for the keyboard and must not open it twice.
                  onClick={(event) => {
                    event.stopPropagation()
                    press.go()
                  }}
                  className="block max-w-full min-w-0 truncate rounded-sm text-left text-title leading-5 font-semibold focus-ring"
                >
                  {title}
                </button>
                {aside && (
                  <>
                    <span className="flex-1" />
                    {aside}
                  </>
                )}
              </div>
              {subtitle}
            </div>
            {/* A menu's button is taller than the name's line, so with one the
                pair is drawn up and out to centre on it. */}
            <div
              className={cn("flex shrink-0 items-center", menu ? "-mt-1 -mr-1.5 gap-0.5" : "h-5")}
            >
              <ArrowRight
                aria-hidden
                className="size-3.5 text-muted-foreground transition-colors group-hover/choice:text-foreground"
              />
              {menu}
            </div>
          </div>
          {children}
          {foot && <div className="mt-auto min-w-0">{foot}</div>}
        </div>
      </SpotlightBorder>
    </BlurFade>
  )
}
