"use client"

import { Fragment } from "react"
import Link from "next/link"
import { Check, MoreHorizontal } from "@/components/icons"
import { cn } from "@/lib/utils"
import { DimActions, IconAction, RowActions } from "@/components/icon-action"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import {
  ContextMenuItem,
  ContextMenuLabel,
  ContextMenuSeparator,
  ContextMenuShortcut,
  ContextMenuSub,
  ContextMenuSubContent,
  ContextMenuSubTrigger,
} from "@/components/ui/context-menu"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuShortcut,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
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
 * out and still carries its own copy, and Files has a second model of a verb
 * (`files/file-actions.tsx`); both move onto this one in their page passes.
 * The process, PM2, unit and cron rows draw theirs through this file so that
 * four kinds of row do not arrive at four menus.
 *
 * A verb that opens something rather than doing it has an `href` and is drawn
 * as a link; a choice among several is `checked`; a verb with more under it
 * has a `submenu`. `VerbItems` draws a list of them into either kind of menu.
 */
export type VerbSpec = {
  key: string
  /** The word on the button and in the menu. */
  label: string
  /**
   * A shorter word for a bar with little room — "Logs" for "Open logs" —
   * where the verb is drawn as a named button. The menu always says `label`.
   */
  short?: string
  icon: React.ComponentType<{ className?: string }>
  /** What pressing it does. A verb with `href` or `submenu` leaves it out. */
  run?: () => void
  /**
   * Where it goes, for a verb that opens something rather than doing it: drawn
   * as a link, so it can be opened in a new tab or copied, and a middle click
   * works. `internal` sends it through the router; `download` saves it.
   */
  href?: string
  /** The link is a route in this app, and goes through the router. */
  internal?: boolean
  /** The link is a file to save, under this name when one is given. */
  download?: boolean | string
  /** Drawn inline in a row; the rest go behind the overflow menu. */
  inline?: boolean
  /** The present participle a row reports while this is in flight. */
  progressive?: string
  danger?: boolean
  disabled?: boolean
  /**
   * Why a disabled verb is off, said on hover and focus of its menu item. A
   * held verb with a reason stays focusable (it is `aria-disabled` rather
   * than taken out of the menu), so the keyboard reaches the reason too.
   */
  reason?: string
  /**
   * The part of a long menu this verb belongs to — "Run", "Project". A menu
   * of eleven words in a row is a wall; the same eleven under two names are
   * two short lists. Verbs of one group are declared next to each other.
   */
  group?: string
  /** The key that does the same from the keyboard, said at the item's end. */
  shortcut?: string
  /** The current one of a set of choices — a folder's colour, a sort. */
  checked?: boolean
  /**
   * A drawing in front of the word in a menu, which otherwise draws none: a
   * colour's own swatch. Only for what the item is, never for the verb.
   */
  mark?: React.ReactNode
  /** Verbs one level down, behind this one. */
  submenu?: VerbSpec[]
}

/**
 * A verb that does something when pressed: the form every row declared
 * before verbs could be links, and the one a caller that presses a verb
 * itself (`verb.run()`) holds. Every component here takes the wider
 * `VerbSpec`, which this is one of.
 */
export type Verb = VerbSpec & { run: () => void }

/**
 * A danger verb drawn as a control of its own, in a row or a bar: the muted
 * ink at rest at every width, the destructive ink only under the pointer or
 * the keyboard. A phone has no hover to keep a row's reveal grey, so a list
 * of dumps showed a red bin on every card, and the danger hue was the
 * loudest colour on the page; the red is for the moment of pressing, and for
 * the confirmation that follows.
 */
export const DANGER_AT_REST =
  "text-muted-foreground hover:text-destructive focus-visible:text-destructive"

/**
 * How a row's controls are drawn: `reveal` for a row whose last column is
 * shared with something else (they appear with the pointer); `dim` for a row
 * whose controls own their column (always drawn, quiet until the pointer is
 * on the row); `plain` for controls that are always simply there.
 */
export type VerbActionsMode = "plain" | "reveal" | "dim"

const WRAPPERS = { plain: PlainActions, reveal: RowActions, dim: DimActions } as const

/** The inline verbs as icons, everything else behind one menu. */
export function VerbActions({
  verbs,
  mode,
  reveal,
  dim,
  menuLabel,
  className,
}: {
  verbs: VerbSpec[]
  mode?: VerbActionsMode
  /** @deprecated use `mode="reveal"` */
  reveal?: boolean
  /** @deprecated use `mode="dim"` */
  dim?: boolean
  /** Names the menu button after its row, where a surface draws more than one. */
  menuLabel?: string
  className?: string
}) {
  const inline = verbs.filter((v) => v.inline)
  const rest = verbs.filter((v) => !v.inline)
  const Wrapper = WRAPPERS[mode ?? (reveal ? "reveal" : dim ? "dim" : "plain")]

  return (
    <Wrapper className={className}>
      {inline.map((verb) =>
        verb.href ? (
          <IconAction
            key={verb.key}
            asChild
            label={verb.label}
            className={cn(verb.danger && DANGER_AT_REST)}
          >
            <VerbLink verb={verb} onClick={(event) => event.stopPropagation()}>
              <verb.icon />
            </VerbLink>
          </IconAction>
        ) : (
          <IconAction
            key={verb.key}
            label={verb.label}
            disabled={verb.disabled}
            className={cn(verb.danger && DANGER_AT_REST)}
            onClick={(event) => {
              event.stopPropagation()
              verb.run?.()
            }}
          >
            <verb.icon />
          </IconAction>
        ),
      )}
      {rest.length > 0 && <VerbMenu verbs={rest} label={menuLabel} />}
    </Wrapper>
  )
}

/**
 * A verb with an address, as the element its kind of address wants. Every
 * other prop is passed on: as a menu item's or a button's child (`asChild`)
 * it is handed the item's role, keys and focus, and a link that dropped them
 * would be a menu item the arrow keys skip.
 */
function VerbLink({ verb, children, ...props }: { verb: VerbSpec } & React.ComponentProps<"a">) {
  if (verb.internal && !verb.download)
    return (
      <Link href={verb.href!} {...props}>
        {children}
      </Link>
    )
  return (
    <a
      href={verb.href}
      download={verb.download === true ? "" : verb.download || undefined}
      {...props}
    >
      {children}
    </a>
  )
}

function PlainActions({ className, ...props }: React.ComponentProps<"div">) {
  return <div className={cn("flex shrink-0 items-center gap-0.5", className)} {...props} />
}

/**
 * A detail surface's controls: the inline verbs as named buttons, because a
 * sheet has the width for a word (`short`, where the verb has one), and the
 * rest behind the same menu.
 */
export function VerbBar({
  verbs,
  menuLabel,
  className,
}: {
  verbs: VerbSpec[]
  menuLabel?: string
  className?: string
}) {
  const inline = verbs.filter((v) => v.inline)
  const rest = verbs.filter((v) => !v.inline)
  return (
    <div className={cn("flex flex-wrap items-center gap-1.5", className)}>
      {inline.map((verb) => {
        const face = (
          <>
            <verb.icon className="size-3" />
            {verb.short ?? verb.label}
          </>
        )
        return verb.href ? (
          <Button
            key={verb.key}
            asChild
            size="xs"
            variant="outline"
            className={cn(verb.danger && DANGER_AT_REST)}
          >
            <VerbLink verb={verb}>{face}</VerbLink>
          </Button>
        ) : (
          <Button
            key={verb.key}
            size="xs"
            variant="outline"
            disabled={verb.disabled}
            aria-label={verb.short ? verb.label : undefined}
            className={cn(verb.danger && DANGER_AT_REST)}
            onClick={verb.run}
          >
            {face}
          </Button>
        )
      })}
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
  verbs: VerbSpec[]
  align?: "start" | "end"
  label?: string
  /** A named trigger instead of the ellipsis, for a menu that stands alone in a header. */
  trigger?: React.ReactNode
}) {
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
        <VerbItems verbs={verbs} kind="dropdown" />
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

/** The menu parts a kind of menu is drawn with. */
const PARTS = {
  dropdown: {
    Item: DropdownMenuItem,
    Label: DropdownMenuLabel,
    Separator: DropdownMenuSeparator,
    Shortcut: DropdownMenuShortcut,
    Sub: DropdownMenuSub,
    SubTrigger: DropdownMenuSubTrigger,
    SubContent: DropdownMenuSubContent,
  },
  context: {
    Item: ContextMenuItem,
    Label: ContextMenuLabel,
    Separator: ContextMenuSeparator,
    Shortcut: ContextMenuShortcut,
    Sub: ContextMenuSub,
    SubTrigger: ContextMenuSubTrigger,
    SubContent: ContextMenuSubContent,
  },
} as const

/**
 * The parts a list of verbs is drawn with: either kind of menu's, or — for a
 * test that reads the markup without opening a menu — stand-ins of the same
 * props.
 */
export type VerbMenuParts = {
  [K in keyof (typeof PARTS)["dropdown"]]: React.ComponentType<any> // eslint-disable-line @typescript-eslint/no-explicit-any -- two libraries' item props, read through one name
}

/**
 * A list of verbs as menu items, for either kind of menu: the overflow
 * menu's dropdown (`VerbMenu` draws it) and a right-click menu, whose root
 * and trigger stay the caller's — one root for a whole listing, say. A group
 * opens under its name, a danger verb under a hairline, a verb with `href` is
 * a link, a `checked` one carries a tick, and a `submenu` opens one level
 * down.
 */
export function VerbItems({ verbs, kind }: { verbs: VerbSpec[]; kind: "dropdown" | "context" }) {
  return <VerbItemList verbs={verbs} parts={PARTS[kind]} />
}

/** `VerbItems` drawn with parts of the caller's. */
export function VerbItemList({ verbs, parts }: { verbs: VerbSpec[]; parts: VerbMenuParts }) {
  return verbs.map((verb, i) => {
    const previous = verbs[i - 1]
    const regroups = i > 0 && verb.group !== previous.group
    return (
      <Fragment key={verb.key}>
        {i > 0 && (regroups || (verb.danger && !previous.danger)) && <parts.Separator />}
        {verb.group && (i === 0 || regroups) && <parts.Label>{verb.group}</parts.Label>}
        <VerbItem verb={verb} parts={parts} />
      </Fragment>
    )
  })
}

/**
 * One verb as a menu item: its word, and no glyph in front of it. The operator
 * asked for menus without them, first on the Databases control center (24 Nov
 * 2026) and then everywhere (27 Nov 2026): a glyph beside every word was noise.
 * A `mark` is still drawn, being what the item says (a colour's swatch), and
 * so is the tick on the current choice.
 */
function VerbItem({ verb, parts }: { verb: VerbSpec; parts: VerbMenuParts }) {
  const { Item, Shortcut, Sub, SubTrigger, SubContent } = parts
  if (verb.submenu) {
    return (
      <Sub>
        <SubTrigger disabled={verb.disabled} className="gap-2 text-body">
          {verb.mark}
          <span className="min-w-0 flex-1 truncate">{verb.label}</span>
        </SubTrigger>
        <SubContent className="min-w-44">
          {verb.submenu.map((inner) => (
            <VerbItem key={inner.key} verb={inner} parts={parts} />
          ))}
        </SubContent>
      </Sub>
    )
  }
  const face = (
    <>
      {verb.mark}
      <span className="min-w-0 flex-1 truncate">{verb.label}</span>
      {verb.checked && <Check aria-hidden className="size-3.5" />}
      {verb.shortcut && <Shortcut>{verb.shortcut}</Shortcut>}
    </>
  )
  // One of a set of choices says which is current to a screen reader too,
  // not only with the tick.
  const choice =
    verb.checked !== undefined
      ? { role: "menuitemcheckbox" as const, "aria-checked": verb.checked }
      : {}
  if (verb.href) {
    return (
      <Item asChild variant={verb.danger ? "destructive" : "default"} disabled={verb.disabled}>
        <VerbLink verb={verb}>{face}</VerbLink>
      </Item>
    )
  }
  if (verb.disabled && verb.reason) {
    return (
      <Tooltip>
        <TooltipTrigger asChild>
          <Item
            aria-disabled
            aria-description={verb.reason}
            data-held=""
            className="text-muted-foreground focus:text-muted-foreground"
            onSelect={(event: Event) => event.preventDefault()}
          >
            {face}
          </Item>
        </TooltipTrigger>
        <TooltipContent side="left" className="max-w-xs">
          {verb.reason}
        </TooltipContent>
      </Tooltip>
    )
  }
  // A choice closes the menu. Preventing the item's default, which the
  // Docker copy of this menu still does, kept every menu drawn through here
  // open after a choice until 0.6.7.
  return (
    <Item
      {...choice}
      variant={verb.danger ? "destructive" : "default"}
      disabled={verb.disabled}
      onSelect={() => verb.run?.()}
    >
      {face}
    </Item>
  )
}
