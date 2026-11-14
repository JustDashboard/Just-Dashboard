"use client"

import { useEffect, useRef } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { Tabs as TabsPrimitive } from "radix-ui"
import { ArrowLeft, ChevronDown, ChevronUp } from "@/components/icons"
import { cn } from "@/lib/utils"
import { IconAction } from "@/components/icon-action"
import { Page, PageContext, SearchInput } from "@/components/page"
import { LoadingRows } from "@/components/state"
import { ChipCount, tabClasses } from "@/components/tabs"
import { Tabs, TabsContent } from "@/components/ui/tabs"
import { BlockedState } from "@/components/database/kit"
import { ServerDown, isDown } from "@/components/database/ops/performance-parts"
import { useDatabase } from "@/components/database/shell/database-context"

/**
 * The shape of a page Performance opens for one thing — a statement, a table,
 * a session — which is the process page's (`procs/process-page`): a way back,
 * the identity line, the views as an underlined strip with the open one in
 * the address, and from `xl` the list it was opened from as a column on the
 * left, so the next one is a press away and not a trip back to the list.
 *
 * Each of the three was a sheet over its list until 0.8.7. A statement's
 * seven figures, its whole text and a form for its plan in a sheet's width,
 * over a table nobody could read behind it, is a page crammed into a drawer.
 */

/** One row of the rail: a sibling of the thing on screen. */
export type RailRow = {
  key: string
  href: string
  /** The row's accessible name: "Session 52". */
  label: string
  mark?: React.ReactNode
  title: React.ReactNode
  sub?: React.ReactNode
  figure?: React.ReactNode
  figureSub?: React.ReactNode
}

/**
 * The frame: the rail beside the page from `xl`, held to the window there so
 * the list and the identity line stay put while a view scrolls. Narrower, the
 * page is the one pane and scrolls as a whole.
 */
export function DetailFrame({
  rail,
  children,
}: {
  rail: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <Page fill="xl" className="gap-0 md:gap-0">
      <div className="flex min-w-0 flex-1 gap-6 xl:min-h-0">
        {rail}
        <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-5">{children}</div>
      </div>
    </Page>
  )
}

/** Which of its siblings a page steps to, and the keys that do it. */
export type Step = { previous?: string; next?: string }

export function stepOf(rows: readonly RailRow[], current: string | null): Step {
  const at = rows.findIndex((row) => row.key === current)
  // A thing the list does not hold steps into the list from its ends.
  return at < 0
    ? { previous: rows[rows.length - 1]?.href, next: rows[0]?.href }
    : { previous: rows[at - 1]?.href, next: rows[at + 1]?.href }
}

/** Alt+↑ and Alt+↓ walk the rail, from anywhere on the page but a field being typed in. */
function useStepKeys(step: Step) {
  const router = useRouter()
  const held = useRef(step)
  useEffect(() => {
    held.current = step
  })
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (!event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) return
      if (event.key !== "ArrowUp" && event.key !== "ArrowDown") return
      const target = event.target
      if (
        target instanceof HTMLElement &&
        (target.isContentEditable || target.closest("input, textarea, select, .monaco-editor"))
      ) {
        return
      }
      const href = event.key === "ArrowUp" ? held.current.previous : held.current.next
      if (!href) return
      event.preventDefault()
      router.push(href)
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [router])
}

/**
 * The line above the identity: the way back to the list, and the two arrows
 * that step along it — the only way to the next one on a phone, where there
 * is no rail.
 */
export function DetailHead({
  title,
  back,
  noun,
  step,
}: {
  /** The page's name for assistive technology and the tab. */
  title: string
  back: { href: string; label: string }
  /** What the arrows step to: "statement". */
  noun: string
  step: Step
}) {
  const router = useRouter()
  useStepKeys(step)
  const go = (href: string | undefined) => () => href && router.push(href)
  return (
    <PageContext
      eyebrow={
        <Link
          href={back.href}
          className="inline-flex items-center gap-1 rounded-sm focus-ring hover:underline"
        >
          <ArrowLeft className="size-3" /> {back.label}
        </Link>
      }
      title={title}
      actions={
        <span className="flex items-center" data-slot="detail-step">
          <IconAction
            label={`Previous ${noun}`}
            title={`Previous ${noun} (Alt+↑)`}
            disabled={!step.previous}
            onClick={go(step.previous)}
          >
            <ChevronUp />
          </IconAction>
          <IconAction
            label={`Next ${noun}`}
            title={`Next ${noun} (Alt+↓)`}
            disabled={!step.next}
            onClick={go(step.next)}
          >
            <ChevronDown />
          </IconAction>
        </span>
      }
    />
  )
}

export type DetailView<T extends string> = { key: T; label: string; count?: number }

/**
 * The page's view, as the address has it: `?tab=`, absent for the first. It
 * is replaced and not pushed — a view is not a place Back should step
 * through — and a tab the page does not have is its first.
 */
export function useDetailTab<T extends string>(views: readonly DetailView<T>[]) {
  const { param, select } = useDatabase()
  const asked = param("tab")
  const first = views[0]?.key
  const tab = (views.find((view) => view.key === asked)?.key ?? first) as T
  const setTab = (next: string) => select({ tab: next === first ? null : next })
  return [tab, setTab] as const
}

/** The underlined strip and the open view under it, which scrolls by itself where the page is held to the window. */
export function DetailTabs<T extends string>({
  label,
  views,
  tab,
  onTab,
  children,
}: {
  label: string
  views: readonly DetailView<T>[]
  tab: T
  onTab: (tab: string) => void
  children: React.ReactNode
}) {
  return (
    <Tabs value={tab} onValueChange={onTab} className="min-h-0 flex-1 gap-0">
      <TabsPrimitive.List
        aria-label={label}
        className="flex shrink-0 [scrollbar-width:none] gap-1 overflow-x-auto border-b border-hairline [&::-webkit-scrollbar]:hidden"
      >
        {views.map((view) => (
          <TabsPrimitive.Trigger
            key={view.key}
            value={view.key}
            className={tabClasses(tab === view.key, "h-10")}
          >
            {view.label}
            {view.count !== undefined && view.count > 0 && <ChipCount>{view.count}</ChipCount>}
          </TabsPrimitive.Trigger>
        ))}
      </TabsPrimitive.List>
      <TabsContent
        value={tab}
        data-slot="detail-view"
        className="min-h-0 min-w-0 flex-1 pt-6 xl:overflow-y-auto"
      >
        {children}
      </TabsContent>
    </Tabs>
  )
}

/**
 * The list a thing was opened from, folded into a column beside it: the
 * list's own order and its own filter, the one on screen filled. A thing the
 * list no longer holds — a session that ended, a statement that fell out of
 * the top — is drawn first, apart, so the page is never off its own rail.
 */
export function DetailRail({
  label,
  placeholder,
  query,
  onQuery,
  caption,
  rows,
  current,
  pinned,
  loading,
  empty,
  className,
}: {
  /** What the rail lists: "Statements". */
  label: string
  placeholder: string
  query: string
  onQuery: (value: string) => void
  caption: React.ReactNode
  rows: readonly RailRow[]
  current: string | null
  /** The thing on screen, where the list does not hold it. */
  pinned?: RailRow
  loading: boolean
  empty: string
  className?: string
}) {
  const list = useRef<HTMLOListElement>(null)
  // The one on screen is kept in view: opened from a link, or stepped to past
  // the column's edge, it would otherwise be off its own list.
  useEffect(() => {
    list.current
      ?.querySelector<HTMLElement>("[aria-current='page']")
      ?.scrollIntoView({ block: "nearest" })
  }, [current, rows.length])
  const apart = pinned && !rows.some((row) => row.key === pinned.key) ? pinned : undefined
  return (
    <nav
      aria-label={label}
      data-slot="detail-rail"
      className={cn(
        "hidden min-h-0 w-72 shrink-0 flex-col border-r border-hairline pr-4 xl:flex",
        className,
      )}
    >
      <div className="flex shrink-0 flex-col gap-2 pb-3">
        <SearchInput
          value={query}
          onChange={(event) => onQuery(event.target.value)}
          placeholder={placeholder}
          aria-label={`Filter the ${label.toLowerCase()}`}
          containerClassName="sm:w-full"
        />
        <p className="truncate text-hint text-muted-foreground">{caption}</p>
      </div>
      <ol
        ref={list}
        aria-label={`${label} to open`}
        className="-mx-2 min-h-0 flex-1 [scrollbar-width:thin] space-y-0.5 overflow-y-auto px-2 pb-2"
      >
        {apart && <RailItem row={apart} current />}
        {apart && rows.length > 0 && (
          <li aria-hidden className="mx-3 my-1.5 border-t border-hairline" />
        )}
        {rows.map((row) => (
          <RailItem key={row.key} row={row} current={row.key === current} />
        ))}
        {loading && rows.length === 0 && <LoadingRows rows={8} />}
        {!loading && rows.length === 0 && !apart && (
          <li className="px-3 py-4 text-hint text-muted-foreground">{empty}</li>
        )}
      </ol>
    </nav>
  )
}

function RailItem({ row, current }: { row: RailRow; current: boolean }) {
  return (
    <li className="min-w-0">
      <Link
        href={row.href}
        aria-current={current ? "page" : undefined}
        aria-label={row.label}
        className={cn(
          "flex h-12 w-full min-w-0 items-center gap-2.5 rounded-lg px-2 text-left focus-ring-inset transition-colors",
          current ? "bg-accent" : "hover:bg-row-hover",
        )}
      >
        {row.mark && <span className="flex shrink-0">{row.mark}</span>}
        <span className="min-w-0 flex-1">
          <span className={cn("block truncate text-xs", current ? "font-semibold" : "font-medium")}>
            {row.title}
          </span>
          <span className="block truncate text-hint text-muted-foreground">{row.sub ?? " "}</span>
        </span>
        {(row.figure !== undefined || row.figureSub !== undefined) && (
          <span className="flex shrink-0 flex-col items-end">
            <span className="numeric text-hint font-medium">{row.figure}</span>
            <span className="numeric text-micro text-muted-foreground">{row.figureSub ?? " "}</span>
          </span>
        )}
      </Link>
    </li>
  )
}

/**
 * The gate every one of these pages stands behind: its engine has the thing,
 * and its server is there to ask. An address typed for an engine without
 * statements says so in the engine's terms, with the way back, and asks the
 * server nothing.
 */
export function DetailGate({
  has,
  thing,
  children,
}: {
  /** The engine keeps what this page is about. */
  has: boolean
  /** What the page would have shown: "a statement's figures". */
  thing: string
  children: React.ReactNode
}) {
  const { engine, status, href } = useDatabase()
  if (!has) {
    return (
      <Page>
        <PageContext eyebrow="Databases" title="Performance" />
        <BlockedState
          engine={engine}
          thing={thing}
          href={href("performance")}
          action="Open Performance"
        >
          {engine.label} has nothing of the kind for this page to read.
        </BlockedState>
      </Page>
    )
  }
  if (isDown(status.state)) {
    return (
      <Page>
        <PageContext eyebrow="Databases" title="Performance" />
        <ServerDown what="Performance" />
      </Page>
    )
  }
  return <>{children}</>
}
