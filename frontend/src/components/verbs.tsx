"use client"

import { Fragment } from "react"
import { MoreHorizontal } from "@/components/icons"
import { cn } from "@/lib/utils"
import { DimActions, IconAction, RowActions } from "@/components/icon-action"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"

/**
 * A verb is a word.
 *
 * The rule that fell out of the Docker pass, made available to every row in
 * the product: a thing's verbs are declared once as data — label, capability
 * already applied, confirmation already attached — and a surface decides only
 * how many of them it has room to draw. Two or three go inline as icons, the
 * ones pressed daily whose glyphs are conventional; everything else goes
 * behind one menu where each verb is its word, one to a line. A verb that
 * needs a sentence to be understood needs a better word, or a confirmation
 * that carries the sentence.
 *
 * `components/docker/container-actions.tsx` is where the pattern was worked
 * out and still carries its own copy; the process, PM2, unit and cron rows
 * draw theirs through this file so that four kinds of row do not arrive at
 * four menus.
 */
export type Verb = {
  key: string
  /** The word on the button and in the menu. */
  label: string
  icon: React.ComponentType<{ className?: string }>
  run: () => void
  /** Drawn inline in a row; the rest go behind the overflow menu. */
  inline?: boolean
  /** The present participle a row reports while this is in flight. */
  progressive?: string
  danger?: boolean
  disabled?: boolean
  /**
   * The part of a long menu this verb belongs to — "Run", "Project". A menu
   * of eleven words in a row is a wall; the same eleven under two names are
   * two short lists. Verbs of one group are declared next to each other.
   */
  group?: string
}

/**
 * A row's controls: the inline verbs as icons, everything else behind one
 * menu. `reveal` for a row whose last column is shared with something else;
 * `dim` for a row whose controls own their column — always drawn, quiet until
 * the pointer is on the row.
 */
export function VerbActions({
  verbs,
  reveal,
  dim,
  menuLabel,
  className,
}: {
  verbs: Verb[]
  reveal?: boolean
  dim?: boolean
  /** Names the menu button after its row, where a surface draws more than one. */
  menuLabel?: string
  className?: string
}) {
  const inline = verbs.filter((v) => v.inline)
  const rest = verbs.filter((v) => !v.inline)
  const Wrapper = reveal ? RowActions : dim ? DimActions : PlainActions

  return (
    <Wrapper className={className}>
      {inline.map((verb) => (
        <IconAction
          key={verb.key}
          label={verb.label}
          disabled={verb.disabled}
          className={cn(verb.danger && "text-destructive")}
          onClick={(event) => {
            event.stopPropagation()
            verb.run()
          }}
        >
          <verb.icon />
        </IconAction>
      ))}
      {rest.length > 0 && <VerbMenu verbs={rest} label={menuLabel} />}
    </Wrapper>
  )
}

function PlainActions({ className, ...props }: React.ComponentProps<"div">) {
  return <div className={cn("flex shrink-0 items-center gap-0.5", className)} {...props} />
}

/**
 * A detail surface's controls: the inline verbs as named buttons, because a
 * sheet has the width for a word, and the rest behind the same menu.
 */
export function VerbBar({
  verbs,
  menuLabel,
  className,
}: {
  verbs: Verb[]
  menuLabel?: string
  className?: string
}) {
  const inline = verbs.filter((v) => v.inline)
  const rest = verbs.filter((v) => !v.inline)
  return (
    <div className={cn("flex flex-wrap items-center gap-1.5", className)}>
      {inline.map((verb) => (
        <Button
          key={verb.key}
          size="xs"
          variant="outline"
          disabled={verb.disabled}
          className={cn(verb.danger && "text-destructive")}
          onClick={verb.run}
        >
          <verb.icon className="size-3" />
          {verb.label}
        </Button>
      ))}
      {rest.length > 0 && <VerbMenu verbs={rest} label={menuLabel} />}
    </div>
  )
}

/** The overflow menu, where a verb is a word rather than a glyph. */
export function VerbMenu({
  verbs,
  align,
  label = "More actions",
  trigger,
}: {
  verbs: Verb[]
  align?: "start" | "end"
  label?: string
  /** A named trigger instead of the ellipsis, for a menu that stands alone in a header. */
  trigger?: React.ReactNode
}) {
  // A choice closes the menu. Preventing the item's default, which the
  // Docker copy of this menu still does, kept every menu drawn through here
  // open after a choice until 0.6.7.
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        {trigger ?? (
          <Button
            size="icon-sm"
            variant="ghost"
            aria-label={label}
            onClick={(event) => event.stopPropagation()}
            className="[&_svg:not([class*='size-'])]:size-3.5"
          >
            <MoreHorizontal />
          </Button>
        )}
      </DropdownMenuTrigger>
      <DropdownMenuContent align={align ?? "end"} className="min-w-44">
        {verbs.map((verb, i) => {
          const previous = verbs[i - 1]
          const regroups = i > 0 && verb.group !== previous.group
          return (
            <Fragment key={verb.key}>
              {i > 0 && (regroups || (verb.danger && !previous.danger)) && (
                <DropdownMenuSeparator />
              )}
              {verb.group && (i === 0 || regroups) && (
                <DropdownMenuLabel>{verb.group}</DropdownMenuLabel>
              )}
              <DropdownMenuItem
                variant={verb.danger ? "destructive" : "default"}
                disabled={verb.disabled}
                onSelect={() => verb.run()}
              >
                <verb.icon className="size-3.5" />
                {verb.label}
              </DropdownMenuItem>
            </Fragment>
          )
        })}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
