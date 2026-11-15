"use client"

import { useEffect, useRef } from "react"
import Link from "next/link"
import { ArrowLeft, ChevronDown, ChevronUp } from "@/components/icons"
import { cn } from "@/lib/utils"
import { IconAction } from "@/components/icon-action"
import { Page, SearchInput } from "@/components/page"
import { Skeleton } from "@/components/ui/skeleton"
import { Workspace, WorkspaceHelp } from "@/components/workspace/workspace"

/**
 * The shape a container's traffic and an eBPF program share, which is the
 * process page's (`procs/process-page.tsx`): the thing as a place of its own
 * with a way back, and from `xl` the list it was opened from as a column
 * beside it, so the next one to look at is one press away, Alt+↑ and Alt+↓
 * walk it, and the list's own filter comes along. On a phone the page is the
 * one pane and scrolls as a whole, with the way back and the same two arrows.
 *
 * Both were sheets over the Traffic page. A sheet could not be linked,
 * reloaded or left with Back, and its width held a chart and a list of peers
 * (or a program's attachments and maps) in a column a third of the screen.
 */
export type Step = { disabled: boolean; run: () => void }

/** The one before and the one after `current` in the rail's order. */
export function steps<K>(keys: K[], current: K | null, open: (key: K) => void) {
  const at = current === null ? -1 : keys.indexOf(current)
  const go = (direction: number) => {
    const next = at >= 0 ? keys[at + direction] : keys[direction > 0 ? 0 : keys.length - 1]
    if (next !== undefined) open(next)
  }
  return {
    previous: { disabled: at <= 0, run: () => go(-1) },
    next: { disabled: keys.length === 0 || at >= keys.length - 1, run: () => go(1) },
  }
}

export function DetailShell({
  name,
  what,
  stateKey,
  refresh,
  previous,
  next,
  rail,
  header,
  children,
}: {
  /** The workspace's name, as its shortcuts are titled: "Container traffic". */
  name: string
  /** What the arrows step through: "container", "program". */
  what: string
  stateKey: string
  refresh: () => void
  previous: Step
  next: Step
  rail: React.ReactNode
  /** The way back and the identity line, which stay put while the rest scrolls. */
  header: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <Workspace
      name={name}
      stateKey={stateKey}
      refresh={refresh}
      commands={[
        {
          id: "previous",
          label: `Previous ${what}`,
          keys: "Alt+↑",
          chord: "Alt+ArrowUp",
          disabled: previous.disabled,
          run: previous.run,
        },
        {
          id: "next",
          label: `Next ${what}`,
          keys: "Alt+↓",
          chord: "Alt+ArrowDown",
          disabled: next.disabled,
          run: next.run,
        },
      ]}
    >
      {/* Held to the window where the rail stands beside it, so the list and
          the identity line stay put while the page scrolls; narrower, the
          page is the one pane and scrolls as a whole. */}
      <Page fill="xl" className="gap-0 md:gap-0">
        <div className="flex min-w-0 flex-1 gap-6 xl:min-h-0">
          {rail}
          <div className="flex min-w-0 flex-1 flex-col gap-5 xl:min-h-0">
            <div className="flex min-w-0 shrink-0 flex-col gap-5">{header}</div>
            <div className="flex min-w-0 flex-col gap-8 xl:min-h-0 xl:flex-1 xl:overflow-y-auto xl:pr-1">
              {children}
            </div>
          </div>
        </div>
      </Page>
    </Workspace>
  )
}

/** The way back to the list, as the page's eyebrow. */
export function BackLink({ href, children }: { href: string; children: React.ReactNode }) {
  return (
    <Link
      href={href}
      className="inline-flex items-center gap-1 rounded-sm focus-ring hover:underline"
    >
      <ArrowLeft className="size-3" /> {children}
    </Link>
  )
}

/** The two arrows a phone walks the list with, and the shortcuts' help. */
export function StepArrows({ what, previous, next }: { what: string; previous: Step; next: Step }) {
  return (
    <span className="flex items-center">
      <IconAction label={`Previous ${what}`} disabled={previous.disabled} onClick={previous.run}>
        <ChevronUp />
      </IconAction>
      <IconAction label={`Next ${what}`} disabled={next.disabled} onClick={next.run}>
        <ChevronDown />
      </IconAction>
      <span className="max-sm:hidden">
        <WorkspaceHelp compact />
      </span>
    </span>
  )
}

/**
 * The list a page was opened from, folded into a column: its search, a line
 * saying what it holds and how it is narrowed, and its rows.
 */
export function DetailRail({
  label,
  slot,
  current,
  query,
  onQuery,
  placeholder,
  caption,
  filter,
  empty,
  children,
}: {
  /** The list's accessible name: "Containers". */
  label: string
  slot: string
  /** The row on screen, kept in view. */
  current: string | number | null
  query: string
  onQuery: (value: string) => void
  placeholder: string
  caption: React.ReactNode
  /** How the list is narrowed, with the way out of it. */
  filter?: React.ReactNode
  /** Said in place of the rows when there are none. */
  empty?: React.ReactNode
  children: React.ReactNode
}) {
  const list = useRef<HTMLOListElement>(null)
  // Opened from a link or by Alt+↓ past the column's edge, the row on screen
  // would otherwise be off its own list.
  useEffect(() => {
    list.current?.querySelector<HTMLElement>("[aria-current='page']")?.scrollIntoView({
      block: "nearest",
    })
  }, [current])
  return (
    <nav
      aria-label={label}
      data-slot={slot}
      className="hidden min-h-0 w-72 shrink-0 flex-col border-r border-hairline pr-4 xl:flex"
    >
      <div className="flex shrink-0 flex-col gap-2 pb-3">
        <SearchInput
          value={query}
          onChange={(event) => onQuery(event.target.value)}
          placeholder={placeholder}
          aria-label={`Search the ${label.toLowerCase()}`}
          containerClassName="sm:w-full"
        />
        <p className="flex min-w-0 items-center justify-between gap-2 text-hint text-muted-foreground">
          <span className="truncate">{caption}</span>
          {filter}
        </p>
      </div>
      <ol
        ref={list}
        aria-label={`${label} to open`}
        className="-mx-2 min-h-0 flex-1 [scrollbar-width:thin] space-y-0.5 overflow-y-auto px-2 pb-2"
      >
        {children}
        {empty && <li className="px-3 py-4 text-hint text-muted-foreground">{empty}</li>}
      </ol>
    </nav>
  )
}

/** One sibling in the rail: its mark, its name over a line, its reading at the end. */
export function RailRow({
  id,
  name,
  label,
  current,
  onOpen,
  leading,
  detail,
  trailing,
}: {
  id: string
  name: string
  /** The row's accessible name. */
  label: string
  current: boolean
  onOpen: () => void
  leading: React.ReactNode
  detail: React.ReactNode
  trailing?: React.ReactNode
}) {
  return (
    <li className="min-w-0">
      <button
        type="button"
        data-workspace-item={id}
        data-workspace-name={name}
        aria-current={current ? "page" : undefined}
        aria-label={label}
        onClick={onOpen}
        className={cn(
          "flex h-12 w-full min-w-0 items-center gap-2.5 rounded-lg px-2 text-left focus-ring-inset transition-colors",
          current ? "bg-accent" : "hover:bg-row-hover",
        )}
      >
        <span className="flex shrink-0">{leading}</span>
        <span className="min-w-0 flex-1">
          <span
            className={cn(
              "block truncate font-mono text-body",
              current ? "font-semibold" : "font-medium",
            )}
          >
            {name}
          </span>
          <span className="block truncate text-hint text-muted-foreground">{detail}</span>
        </span>
        {trailing && (
          <span className="numeric shrink-0 font-mono text-hint text-muted-foreground">
            {trailing}
          </span>
        )}
      </button>
    </li>
  )
}

/** A rail's rows before its list is read. */
export function RailGhosts({ rows = 6 }: { rows?: number }) {
  return (
    <>
      {Array.from({ length: rows }, (_, i) => (
        <li key={i} aria-hidden className="flex h-12 items-center gap-2.5 px-2">
          <Skeleton className="size-7 shrink-0 rounded-md" />
          <Skeleton className="h-3 flex-1" style={{ maxWidth: `${50 + ((i * 17) % 40)}%` }} />
        </li>
      ))}
    </>
  )
}
