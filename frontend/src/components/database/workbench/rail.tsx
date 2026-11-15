"use client"

import Link from "next/link"
import { ChevronDown, ChevronRight, MoreHorizontal } from "@/components/icons"
import { cn } from "@/lib/utils"
import { rowReveal } from "@/components/icon-action"
import { SearchInput } from "@/components/search-input"
import { EmptyNote } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { VerbMenu, type Verb } from "@/components/verbs"
import {
  RAIL_ROW,
  railSkeletonRows,
  type RailFigure,
} from "@/components/database/workbench/rail-model"

export {
  RAIL_ROW,
  RAIL_ROW_PX,
  railSkeletonRows,
  type RailFigure,
} from "@/components/database/workbench/rail-model"

/**
 * The parts every database workbench's rail is built from: its head, the band
 * it is searched in, the head of a group of rows, an object's row, the shape
 * it holds while it is read, and the line it says when a search matches
 * nothing.
 *
 * The rails do different jobs and stay apart — Data lists what holds rows,
 * Schema folds every kind of object, Query keeps saved statements and a tree
 * of columns, MongoDB lists collections and Redis walks a keyspace — but
 * their chrome was five copies that had drifted: the find band 28px in two
 * and 36px in two, five skeletons with five silhouettes (Data's rows ~24px
 * against its real 28px, Redis's 20px), the same "Nothing here is called
 * that." pasted by hand in four, and one object drawn two ways one click
 * apart. Each part here is the one copy.
 */

/** The rail's 40px head on the header ground: which container, and the rail's verbs. */
export function RailHead({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="rail-head"
      className={cn(
        "flex h-10 shrink-0 items-center gap-1 border-b border-hairline bg-surface-header pr-1.5 pl-2",
        className,
      )}
      {...props}
    />
  )
}

/** The rail's name in its head, where there is no container to pick. */
export function RailTitle({ className, ...props }: React.ComponentProps<"span">) {
  return (
    <span
      className={cn("min-w-0 flex-1 truncate px-1 text-body font-medium", className)}
      {...props}
    />
  )
}

/** An icon verb in a rail's head: 28px, 32px under a finger. */
export const RAIL_ACTION = "size-7 max-sm:size-8"

/**
 * The band a rail is searched in, under its head. The box is the dense 28px
 * one from `sm` too (the grid toolbar's height beside it on Data), which the
 * field's own `sm:h-9` used to override in the rails that did not say so;
 * under a finger it is the full 44px field.
 */
export function RailFind({
  value,
  onChange,
  placeholder,
  trailing,
  className,
}: {
  value: string
  onChange: (value: string) => void
  /** The box's words, which are its name too: "Find a table". */
  placeholder: string
  /** A control beside the box, in the band: a verb that reads the list again. */
  trailing?: React.ReactNode
  className?: string
}) {
  return (
    <div
      data-slot="rail-find"
      className={cn("flex shrink-0 items-center gap-1 border-b border-hairline p-1", className)}
    >
      <SearchInput
        dense
        value={value}
        placeholder={placeholder}
        aria-label={placeholder}
        className="sm:h-7"
        containerClassName="sm:w-full"
        onChange={(event) => onChange(event.target.value)}
      />
      {trailing}
    </div>
  )
}

/**
 * The head of a run of rows: its name in the eyebrow and how many it holds at
 * the row's end, where the rows' own figures end. Foldable when `onToggle` is
 * given: the head is then the button, with a chevron before the name.
 *
 * It is not a heading. A rail stands before the page's own h2, so its group
 * heads as h3s broke the outline on every workbench (A11Y-12); the group
 * around it carries the name (`aria-label` on its section).
 */
export function RailGroupHead({
  label,
  mark,
  count,
  open,
  onToggle,
  className,
}: {
  label: React.ReactNode
  /** The group drawn as itself before its name. */
  mark?: React.ReactNode
  count?: React.ReactNode
  /** Whether the rows are shown, for a group that folds. */
  open?: boolean
  onToggle?: () => void
  className?: string
}) {
  const body = (
    <>
      {onToggle &&
        (open ? (
          <ChevronDown aria-hidden className="size-3 shrink-0 text-muted-foreground" />
        ) : (
          <ChevronRight aria-hidden className="size-3 shrink-0 text-muted-foreground" />
        ))}
      {mark}
      {/* The caps and the figure share a baseline; centred, the figure stood
          a pixel above the word it counts. */}
      <span className="flex min-w-0 flex-1 items-baseline gap-1.5">
        <span className="eyebrow min-w-0 flex-1 truncate">{label}</span>
        {count !== undefined && (
          <span className="numeric shrink-0 text-hint text-muted-foreground">{count}</span>
        )}
      </span>
    </>
  )
  const head = "flex h-7 w-full min-w-0 items-center gap-1.5 rounded-md pr-2 pl-2"
  if (!onToggle) {
    return (
      <div data-slot="rail-group-head" className={cn(head, className)}>
        {body}
      </div>
    )
  }
  return (
    <button
      type="button"
      data-slot="rail-group-head"
      aria-expanded={open}
      onClick={onToggle}
      className={cn(
        head,
        "pl-1.5 text-left focus-ring-inset transition-colors hover:bg-row-hover max-sm:h-9",
        className,
      )}
    >
      {body}
    </button>
  )
}

/**
 * One object of a rail: its kind's mark, its name in mono, a staged-edits
 * count, and its figures at the end. A row is a link — it opens in a new tab,
 * and choosing one is a step of history.
 *
 * The row's menu lies over its figures rather than taking a column of its
 * own: reserved, that column left every figure a button's width short of the
 * edge the group heads end on. The figures give way while the pointer is on
 * the row or the menu is open. Under a finger the menu is always there and
 * the figures stand clear of it.
 */
export function RailRow({
  href,
  current,
  title,
  mark,
  name,
  staged,
  figures = [],
  verbs,
  menuLabel,
  triggerProps,
  className,
}: {
  href: string
  current: boolean
  title?: string
  mark?: React.ReactNode
  name: string
  /** Edits made here and not yet applied: "~3", in the hue of a change. */
  staged?: { count: number; label: string; title?: string; slot?: string }
  figures?: readonly RailFigure[]
  verbs?: readonly Verb[]
  /** The menu's name: "Actions for orders". */
  menuLabel?: string
  /** Extra attributes for the menu's trigger, which a spec or a shortcut finds it by. */
  triggerProps?: Record<string, string | undefined>
  className?: string
}) {
  const menu = verbs !== undefined && verbs.length > 0
  return (
    <li
      data-current={current || undefined}
      className={cn(
        "group grid grid-cols-1 items-center rounded-md transition-colors",
        RAIL_ROW,
        current ? "bg-accent" : "hover:bg-row-hover",
        className,
      )}
    >
      <Link
        href={href}
        aria-current={current ? "page" : undefined}
        title={title}
        className={cn(
          "col-start-1 row-start-1 flex h-full min-w-0 items-center gap-1.5 rounded-md pr-2 pl-2 focus-ring-inset",
          menu && "[@media(hover:none)]:pr-10",
        )}
      >
        {mark}
        <span className="min-w-0 flex-1 truncate font-mono text-xs">{name}</span>
        {staged && staged.count > 0 && (
          <span
            data-slot={staged.slot}
            title={staged.title}
            className="shrink-0 font-mono text-hint text-(--git-modified)"
          >
            ~{staged.count}
            <span className="sr-only"> {staged.label}</span>
          </span>
        )}
        {figures.length > 0 && (
          <span
            className={cn(
              "flex shrink-0 items-baseline gap-1.5 transition-opacity",
              // As wide as the menu at least, so the menu never covers the name.
              menu && "min-w-5 justify-end",
              menu &&
                "group-hover:opacity-0 [@media(hover:hover)]:group-has-[[data-state=open]]:opacity-0 [@media(hover:hover)]:group-has-[button:focus-visible]:opacity-0",
            )}
          >
            {figures.map((figure) => (
              <span
                key={figure.key}
                title={figure.title}
                className={cn(
                  "shrink-0 truncate text-right text-hint",
                  figure.mono ? "font-mono" : "numeric",
                  figure.quiet ? "text-faint" : "text-muted-foreground",
                  figure.className,
                )}
              >
                {figure.text}
              </span>
            ))}
          </span>
        )}
      </Link>
      {menu && (
        <VerbMenu
          verbs={[...verbs]}
          label={menuLabel}
          trigger={
            <Button
              size="icon-xs"
              variant="ghost"
              aria-label={menuLabel}
              {...triggerProps}
              className={cn(
                "col-start-1 row-start-1 mr-0.5 justify-self-end max-sm:size-8",
                rowReveal(),
              )}
            >
              <MoreHorizontal className="size-3.5" />
            </Button>
          }
        />
      )}
    </li>
  )
}

/**
 * The rail's shape while it is read: a group head, then rows at the real
 * row's pitch (28px, 36px on a phone) with a mark, a name and a figure — so
 * the rail does not move when its list lands. `lines={2}` is a list of two-line
 * entries (saved queries, history): a name over a statement, at their 44px.
 */
export function RailSkeleton({
  lines = 1,
  groups = 1,
  className,
}: {
  lines?: 1 | 2
  /** How many group heads the silhouette has; 0 for a list with none. */
  groups?: number
  className?: string
}) {
  const rows = railSkeletonRows(groups, lines)
  return (
    <div aria-hidden data-slot="rail-skeleton" className={cn("px-0.5 pt-1.5", className)}>
      {rows.map((row, index) =>
        row.kind === "head" ? (
          <div key={index} className={cn("flex h-7 items-center px-2", index > 0 && "mt-1.5")}>
            <Skeleton className="h-2.5 w-14" />
            <Skeleton className="ml-auto h-2.5 w-4" />
          </div>
        ) : lines === 2 ? (
          <div key={index} className="flex min-h-11 flex-col justify-center gap-1.5 px-2">
            <Skeleton className={cn("h-3", row.width)} />
            <Skeleton className="h-2.5 w-44 max-w-full" />
          </div>
        ) : (
          <div key={index} className={cn("flex items-center gap-1.5 px-2", RAIL_ROW)}>
            <Skeleton className="size-3.5 shrink-0 rounded-sm" />
            <Skeleton className={cn("h-3", row.width)} />
            <Skeleton className="ml-auto h-2.5 w-6" />
          </div>
        ),
      )}
    </div>
  )
}

/** A search of the rail that matched nothing, and the one way out of it. */
export function RailNoMatch({
  onClear,
  children = "Nothing here is called that.",
  clearLabel = "Clear the search",
  as,
}: {
  onClear: () => void
  children?: React.ReactNode
  clearLabel?: string
  as?: "p" | "li" | "div"
}) {
  return (
    <EmptyNote dense as={as} action={{ label: clearLabel, onClick: onClear }}>
      {children}
    </EmptyNote>
  )
}
