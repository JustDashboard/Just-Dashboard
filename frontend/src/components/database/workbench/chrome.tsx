"use client"

import {
  createContext,
  useContext,
  useId,
  useLayoutEffect,
  useMemo,
  useState,
  useSyncExternalStore,
} from "react"
import { createPortal } from "react-dom"
import Link from "next/link"
import {
  ArrowLeft,
  MoreHorizontal,
  RefreshClockwise,
  SidebarLeftClose,
  SidebarLeftOpen,
} from "@/components/icons"
import { useBreakpoint } from "@/lib/breakpoints"
import { usePanelSize } from "@/lib/panel-size"
import { cn } from "@/lib/utils"
import { ResizeHandle } from "@/components/resize-handle"
import { useConfirm } from "@/components/confirm-dialog"
import { Fact, FactLine } from "@/components/fact-line"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { VerbMenu, type Verb } from "@/components/verbs"
import { useLifecycleVerbs } from "@/components/database/home/verbs"
import type { SectionId } from "@/components/database/engine"
import { EngineMark } from "@/components/database/kit/engine-mark"
import { EnvironmentTag, ProtectedTag } from "@/components/database/kit/environment-tag"
import { useOperateVerbs } from "@/components/database/ops/verbs"
import { ConnectPopover } from "@/components/database/shell/connect-popover"
import { ConnectionSwitcher } from "@/components/database/shell/connection-switcher"
import { useDatabase } from "@/components/database/shell/database-context"
import { useOpenVerbs } from "@/components/database/shell/database-verbs"
import { connectionFacts } from "@/components/database/shell/facts"
import { DatabaseStatusMark } from "@/components/database/shell/status"
import type { DatabaseVerbTools } from "@/components/database/shell/types"
import { menuStore, type MenuStore } from "@/components/database/workbench/menu-store"
import { RAIL_SIZE, clamp, sideBeside, sideMax } from "@/components/database/workbench/side-model"

/**
 * The one head of a database's workbench, and the frame it heads.
 *
 * Data, Query, Schema and the Diagram are each one frame held to the window.
 * They used to stand under the database's strip and open on a toolbar of
 * their own, so there were two bars before the work: which database, then
 * what can be done here. The Git workspace has one — back, the mark, the
 * name over a line of facts, the verbs, a menu — and this is that bar for
 * the four of them, drawn once here so they cannot drift apart.
 *
 * The head is above every part of the page and the page's verbs belong to
 * parts far below it (the open table's Insert row is the grid's), so the
 * parts hand them up rather than the page being rebuilt around the head:
 *
 * - `HeadVerbs` draws its children in the head's verb slot, wherever in the
 *   page it is rendered. They stay that part's own elements: its state, its
 *   handlers, its focus.
 * - `useHeadMenu` puts verbs in the head's one `⋯` menu, above the
 *   database's own. The icon-only controls of a workbench live there —
 *   reading again, showing and hiding a side column — as words.
 *
 * It keeps the strip's slot name: it is the strip, moved into the frame, and
 * whatever asks a database's page which database it is finds it here.
 */

type Chrome = {
  /** The page's name in the engine's own word: the heading of its verbs in the menu. */
  title: string
  /** Where the page's verbs are drawn, once the head is on the page. */
  slot: HTMLElement | null
  menu: MenuStore
}

const ChromeContext = createContext<Chrome | null>(null)

/** The workbench's frame: the head, the columns under it, and a phone's bar at the foot. */
export function WorkbenchFrame({
  ref,
  section,
  slot,
  style,
  className,
  bar,
  verbs,
  menu,
  children,
}: {
  ref?: React.Ref<HTMLDivElement>
  section: SectionId
  /** The frame's `data-slot`, by which a page's own styles and tests find it. */
  slot?: string
  style?: React.CSSProperties
  className?: string
  /** The `PaneBar` of a page that shows one pane at a time on a phone. */
  bar?: React.ReactNode
  /** Verbs of the page itself, drawn in the head before those its parts hand up. */
  verbs?: React.ReactNode
  /** Verbs of the page itself for the head's menu, after those its parts hand up. */
  menu?: readonly Verb[]
  children: React.ReactNode
}) {
  const { engine } = useDatabase()
  const title = engine.section(section)?.title ?? ""
  const [node, setSlot] = useState<HTMLElement | null>(null)
  const [store] = useState(menuStore)
  const chrome = useMemo<Chrome>(() => ({ title, slot: node, menu: store }), [title, node, store])
  return (
    <ChromeContext.Provider value={chrome}>
      <div
        ref={ref}
        data-slot={slot}
        style={style}
        // Edge to edge below the break, as the Git workspace is: a phone's
        // bar at the foot is the screen's, not a card's.
        className={cn(
          "relative flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden bg-card lg:rounded-xl lg:border",
          className,
        )}
      >
        <FrameHead onSlot={setSlot} />
        <div data-slot="workbench-columns" className="relative flex min-h-0 min-w-0 flex-1">
          {children}
        </div>
        {verbs && <HeadVerbs>{verbs}</HeadVerbs>}
        {menu && <FrameMenu verbs={menu} />}
        {bar}
      </div>
    </ChromeContext.Provider>
  )
}

/** The gutters of a page whose frame is a `WorkbenchFrame`: none below the break. */
export const WORKBENCH_PAGE = "max-lg:gap-0 max-lg:p-0"

/**
 * Draws its children among the head's verbs. Rendered by whichever part of
 * the page owns them; outside a workbench frame it draws nothing.
 */
export function HeadVerbs({ children }: { children: React.ReactNode }) {
  const chrome = useContext(ChromeContext)
  return chrome?.slot ? createPortal(children, chrome.slot) : null
}

/**
 * Puts verbs in the head's menu for as long as the caller is mounted. They
 * are drawn in the order their owners first asked, under the page's name.
 */
export function useHeadMenu(verbs: readonly Verb[]) {
  const chrome = useContext(ChromeContext)
  const owner = useId()
  const menu = chrome?.menu
  // Every render: the closures are this render's.
  useLayoutEffect(() => {
    menu?.set(owner, verbs)
  })
  useLayoutEffect(() => () => menu?.drop(owner), [menu, owner])
}

/** The page's own verbs, handed over from inside the frame they are for. */
function FrameMenu({ verbs }: { verbs: readonly Verb[] }) {
  useHeadMenu(verbs)
  return null
}

function FrameHead({
  onSlot,
}: {
  /** Told the element the page's verbs are drawn in. */
  onSlot: (node: HTMLElement | null) => void
}) {
  const { conn, summary, engine, status, readOnly, href } = useDatabase()
  const labelled = Boolean(conn.environment?.trim()) || readOnly
  const facts = connectionFacts(conn, engine, summary)
  return (
    <div
      data-slot="database-strip"
      data-head="workbench"
      className="@container shrink-0 border-b border-hairline bg-surface-header"
    >
      {/* Narrower than one row holds it folds on purpose, as the Git head
          does: who, with the menu at its end, then the page's verbs on a line
          of their own — which is not there at all for a page with none. */}
      <div className="grid min-h-12 w-full min-w-0 grid-cols-main-end items-center gap-x-2 gap-y-1.5 px-2 py-1.5 @4xl:flex">
        <div className="flex min-w-0 items-center gap-2 @4xl:flex-1">
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                size="icon-sm"
                variant="ghost"
                className="shrink-0 text-muted-foreground hover:text-foreground"
                aria-label={`Back to ${conn.name}'s Home`}
                asChild
              >
                <Link href={href("home")}>
                  <ArrowLeft className="size-4" />
                </Link>
              </Button>
            </TooltipTrigger>
            <TooltipContent>Back to the database&rsquo;s Home</TooltipContent>
          </Tooltip>
          <EngineMark engine={engine} size="sm" />
          <div className="min-w-0 shrink">
            {/* Whether it answers stands beside the name, so the line under
                them has the block's whole width for the two words a
                workbench must not lose on a phone. */}
            <div className="flex min-w-0 items-center gap-2.5">
              <ConnectionSwitcher inset className="font-semibold" />
              <DatabaseStatusMark status={status} />
            </div>
            {/* What it is for comes before what it is: "production" and
                "protected" stay, and the facts after them give way first. */}
            <FactLine nowrap className="text-micro text-muted-foreground">
              {labelled && (
                <Fact className="shrink-0! overflow-visible!">
                  <span
                    data-slot="database-labels"
                    className="inline-flex items-center gap-1.5 align-top"
                  >
                    <EnvironmentTag environment={conn.environment} className="max-w-32" />
                    {readOnly && <ProtectedTag />}
                  </span>
                </Fact>
              )}
              {facts.map((fact, index) => (
                <Fact
                  key={fact.key}
                  title={fact.title ?? fact.text}
                  mono={fact.mono}
                  className={cn(
                    // The address and the database go before the engine does.
                    index > 0 && "@max-md:hidden",
                    labelled && "@max-xl:hidden",
                  )}
                >
                  {fact.text}
                </Fact>
              ))}
            </FactLine>
          </div>
        </div>

        <div
          ref={onSlot}
          data-slot="workbench-verbs"
          className="order-1 col-span-2 flex min-w-0 flex-wrap items-center gap-1.5 empty:hidden @4xl:order-none @4xl:flex-nowrap"
        />

        <div className="flex shrink-0 items-center gap-1.5">
          <ConnectPopover />
          <HeadMenu />
        </div>
      </div>
    </div>
  )
}

/**
 * The head's one menu: the page's verbs under the page's name, then the
 * database's — the same ones, in the same order, as the strip's menu on
 * every other page of the database (`shell/database-verbs.tsx`).
 */
function HeadMenu() {
  const { menu, title } = useContext(ChromeContext)!
  const { conn, status } = useDatabase()
  const { confirm, dialog } = useConfirm()
  const tools: DatabaseVerbTools = { confirm }
  const handed = useSyncExternalStore(menu.subscribe, menu.verbs, menu.verbs)
  const page = handed.map((verb) => ({ ...verb, group: verb.group ?? title }))
  const database: Verb[] = [
    ...useOpenVerbs(),
    { key: "check", label: "Check connection", icon: RefreshClockwise, run: status.refresh },
    ...useLifecycleVerbs(tools),
    ...useOperateVerbs(tools),
  ]
  return (
    <>
      <VerbMenu
        verbs={[...page, ...database]}
        label={`Actions for ${conn.name}`}
        trigger={
          <Button size="icon-sm" variant="outline" aria-label={`Actions for ${conn.name}`}>
            <MoreHorizontal className="size-4" />
          </Button>
        }
      />
      {dialog}
    </>
  )
}

/* ------------------------------------------------------------ side column */

/**
 * A side column's width and whether it stands beside the work: one rule for
 * every database workbench (`side-model.ts`). The width is the reader's, kept
 * under `key` — each page keeps its own key, so a width saved before this
 * survives — and held to the one scale and to what the frame leaves the work.
 */
export function useSideColumn(
  key: string,
  frameWidth: number,
  size: { base: number; min: number; max: number } = RAIL_SIZE,
) {
  const windowWide = useBreakpoint("lg")
  const [stored, setStored, reset] = usePanelSize(key, size.base)
  const max = sideMax(frameWidth, size)
  const width = clamp(stored, size.min, max)
  const beside = sideBeside(frameWidth, clamp(stored, size.min, size.max), windowWide)
  return {
    beside,
    width,
    min: size.min,
    max,
    setWidth: (px: number, commit?: boolean) => setStored(clamp(px, size.min, max), commit),
    reset,
  }
}

/** What `useSideColumn` hands its column. */
export type SideColumn = ReturnType<typeof useSideColumn>

/**
 * The head menu's word for a side column beside the work: "Hide the tables",
 * "Show the keys". Over the work the phone's bar is how it is reached, so
 * there is no word for it then.
 */
export function sideVerbs(
  column: { beside: boolean },
  shown: boolean,
  noun: string,
  toggle: () => void,
): Verb[] {
  if (!column.beside) return []
  return [
    {
      key: "rail",
      label: `${shown ? "Hide" : "Show"} the ${noun}`,
      icon: shown ? SidebarLeftClose : SidebarLeftOpen,
      run: toggle,
    },
  ]
}

/**
 * A side column of the workbench: beside the work with a hairline and a
 * handle to widen it, or laid over the whole of the work where the frame has
 * no room for both (a phone's pane). The rails of Data, Query, Schema,
 * MongoDB and Redis, and the assistant beside the editor.
 *
 * Over the work it is on the card's ground at one layer (`z-30`): the engines
 * had laid theirs at `z-20` and the Redis columns on no ground at all.
 */
export function WorkbenchSide({
  column,
  side = "left",
  label,
  className,
  onClick,
  children,
}: {
  column: Pick<SideColumn, "beside" | "width" | "min" | "max" | "setWidth" | "reset">
  /** Which edge of the frame it stands at. */
  side?: "left" | "right"
  /** The handle's name: "Tables width". */
  label: string
  className?: string
  onClick?: React.MouseEventHandler<HTMLDivElement>
  children: React.ReactNode
}) {
  const handle = column.beside && (
    <ResizeHandle
      side={side}
      label={label}
      value={column.width}
      min={column.min}
      max={column.max}
      onChange={column.setWidth}
      onReset={column.reset}
      className={cn("absolute inset-y-0 z-sticky", side === "left" ? "-right-1" : "-left-1")}
    />
  )
  return (
    // A click handler only to hear a link inside it act, which the keyboard
    // reaches as the link itself.
    // eslint-disable-next-line jsx-a11y/no-static-element-interactions, jsx-a11y/click-events-have-key-events
    <div
      data-slot="workbench-side"
      data-side={side}
      data-over={column.beside ? undefined : ""}
      style={column.beside ? { width: column.width } : undefined}
      onClick={onClick}
      className={cn(
        "flex min-h-0 min-w-0 flex-col border-hairline bg-card",
        column.beside
          ? cn("relative shrink-0", side === "left" ? "border-r" : "border-l")
          : "absolute inset-0 z-pane",
        className,
      )}
    >
      {side === "right" && handle}
      {children}
      {side === "left" && handle}
    </div>
  )
}

/* ------------------------------------------------------------ object head */

/**
 * The object head's band alone — its ground, hairline and 40px — for a strip
 * that lays out its own contents (`WorkbenchHead`, a pipeline's head).
 */
export const OBJECT_HEAD =
  "flex min-h-10 shrink-0 flex-wrap items-center gap-x-2 gap-y-1 border-b border-hairline bg-surface-header px-3 py-1"

/**
 * The strip at the top of the work: the object that is open — a table, a
 * collection, a key, a database while nothing in it is — and the views of it.
 *
 * Six of these were built by hand across Data, Schema, MongoDB and Redis, the
 * same 40px band with three grounds (the pipeline's and the key's had none),
 * two name sizes and two ways of offering the views (a tablist on SQL, a row
 * of pressed buttons on MongoDB). One band: the header ground with a hairline
 * under it, 40px with the hairline so it lines up with the rail's head beside
 * it; the kind's mark, the name in mono at the body size (the schema before
 * it giving way by truncating, never leaving), one kind tag, the views as a
 * tablist (`ViewTabList` inside the caller's `Tabs`), then the verbs at the
 * end. `facts` is a second line under it, for an object whose facts are what
 * it is read for (a key's type, size and expiry).
 */
export function ObjectHead({
  mark,
  name,
  prefix,
  kind,
  title,
  views,
  verbs,
  facts,
  identityClassName,
  className,
  ...props
}: {
  mark?: React.ReactNode
  name: string
  /** The container it is in, drawn before the name with a dot: a table's schema. */
  prefix?: string
  /** What sort of object, where it is not the ordinary one: a Tag. */
  kind?: React.ReactNode
  title?: string
  /** The views of the object: a `ViewTabList`. */
  views?: React.ReactNode
  verbs?: React.ReactNode
  /** A second line of what the object is. */
  facts?: React.ReactNode
  /** The identity's own room, where the views beside it need a share of a narrow strip. */
  identityClassName?: string
  className?: string
} & Omit<React.ComponentProps<"div">, "title">) {
  const full = prefix ? `${prefix}.${name}` : name
  return (
    <div
      data-slot="object-head"
      className={cn(
        "@container/object shrink-0 border-b border-hairline bg-surface-header",
        className,
      )}
      {...props}
    >
      {/* 40px with its hairline, as the rail's head beside it is: tabs of a
          full 40 over the rule put the strip a pixel below the rail's. */}
      <div className="flex min-h-[39px] items-stretch px-1.5 @max-[34rem]/object:flex-wrap">
        <div
          className={cn(
            "flex min-w-0 items-center gap-1.5 py-1 pr-2 pl-1.5",
            !views && "flex-1",
            identityClassName,
          )}
        >
          {mark}
          {/* Named whole: read part by part, its two halves are two words. */}
          <h2
            aria-label={prefix ? full : undefined}
            title={title ?? full}
            className="flex min-w-0 items-baseline font-mono text-body font-medium"
          >
            {prefix && (
              <>
                <span className="min-w-[3ch] shrink-[2] truncate text-muted-foreground">
                  {prefix}
                </span>
                <span className="text-muted-foreground">.</span>
              </>
            )}
            <span className="min-w-[4ch] truncate">{name}</span>
          </h2>
          {kind && <span className="flex shrink-0 items-center">{kind}</span>}
        </div>
        {views && (
          <div className="flex min-w-0 shrink-0 items-stretch [&_[role=tab]]:h-[39px]">{views}</div>
        )}
        {verbs && <div className="ml-auto flex shrink-0 items-center gap-1 py-1 pl-2">{verbs}</div>}
      </div>
      {facts && (
        <div className="flex min-w-0 items-center gap-2 px-3 pb-1.5 text-hint text-muted-foreground">
          {facts}
        </div>
      )}
    </div>
  )
}
