"use client"

import { useRouter } from "next/navigation"
import { DetailPage } from "@/components/detail-page"
import { Page, PageContext } from "@/components/page"
import { Rail, RailFigures, RailLine, RailName, RailRow as RailItem } from "@/components/rail"
import { ViewTabList } from "@/components/tabs"
import { Tabs, TabsContent } from "@/components/ui/tabs"
import { BlockedState } from "@/components/database/kit"
import { capitalize } from "@/components/database/kit/figures"
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

/** Which of its siblings a page steps to: the addresses of the one before and the one after. */
export type Step = { previous?: string; next?: string }

export function stepOf(rows: readonly RailRow[], current: string | null): Step {
  const at = rows.findIndex((row) => row.key === current)
  // A thing the list does not hold steps into the list from its ends.
  return at < 0
    ? { previous: rows[rows.length - 1]?.href, next: rows[0]?.href }
    : { previous: rows[at - 1]?.href, next: rows[at + 1]?.href }
}

/** The line above the identity: the page's name, the way back to the list and what the arrows walk. */
export type DetailHeadSpec = {
  /** The page's name for assistive technology and the tab. */
  title: string
  back: { href: string; label: string }
  /** What the arrows step to: "statement". */
  noun: string
  step: Step
}

/**
 * The frame: the product's one detail page (`DetailPage`), with the rail
 * beside it from `xl`, the way back, the two arrows and the shortcuts' help
 * on the line above the identity, and Alt+↑ and Alt+↓ as the Workspace's
 * commands, so the help sheet lists them. These pages had a window listener
 * of their own for the keys and named them only in the arrows' titles.
 */
export function DetailFrame({
  rail,
  head,
  identity,
  children,
}: {
  rail: React.ReactNode
  head: DetailHeadSpec
  /** The identity line, held with the head while the views scroll. */
  identity?: React.ReactNode
  children: React.ReactNode
}) {
  const { id } = useDatabase()
  const router = useRouter()
  const go = (href: string | undefined) => () => {
    if (href) router.push(href, { scroll: false })
  }
  const { noun, step } = head
  return (
    <DetailPage
      name={capitalize(noun)}
      what={noun}
      stateKey={`databases.${id}.performance.${noun}`}
      steps={{
        previous: { disabled: !step.previous, run: go(step.previous) },
        next: { disabled: !step.next, run: go(step.next) },
      }}
      rail={rail}
      back={head.back}
      title={head.title}
      identity={identity}
    >
      {children}
    </DetailPage>
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
      <ViewTabList label={label} views={[...views]} value={tab} />
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
 * shared `Rail`, in the list's own order and its own filter, the one on
 * screen filled. A thing the list no longer holds — a session that ended, a
 * statement that fell out of the top — is drawn first, apart, so the page is
 * never off its own rail. A list that could not be read says so in its
 * place, with the way to ask again.
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
  error,
  onRetry,
  empty,
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
  error?: Error
  onRetry?: () => void
  empty: string
}) {
  return (
    <Rail
      slot="detail-rail"
      label={label}
      search={{ label: `Filter the ${label.toLowerCase()}`, placeholder, query, onQuery }}
      caption={caption}
      rows={rows}
      keyOf={(row) => row.key}
      current={(row) => row.key === current}
      pinned={pinned}
      loading={loading}
      loadingRows={8}
      error={error}
      onRetry={onRetry}
      empty={empty || undefined}
      row={(row, on) => (
        <RailItem
          item={row.key}
          name={row.label}
          current={on}
          href={row.href}
          mark={row.mark}
          end={
            (row.figure !== undefined || row.figureSub !== undefined) && (
              <RailFigures top={row.figure} bottom={row.figureSub ?? " "} />
            )
          }
        >
          <RailName current={on} className="text-xs">
            {row.title}
          </RailName>
          <RailLine>{row.sub ?? " "}</RailLine>
        </RailItem>
      )}
    />
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
