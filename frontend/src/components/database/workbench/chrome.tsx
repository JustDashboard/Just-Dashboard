"use client"

import {
  Fragment,
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
import { ArrowLeft, MoreHorizontal, RefreshClockwise } from "@/components/icons"
import { cn } from "@/lib/utils"
import { useConfirm } from "@/components/confirm-dialog"
import { FactDot } from "@/components/metrics/host-identity"
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
import { connectionFacts } from "@/components/database/shell/facts"
import { DatabaseStatusMark } from "@/components/database/shell/status"
import type { DatabaseVerbTools } from "@/components/database/shell/types"
import { menuStore, type MenuStore } from "@/components/database/workbench/menu-store"

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
  full,
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
  /** The frame is the whole window (the diagram's full screen): the head goes. */
  full?: boolean
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
        <FrameHead hidden={full} onSlot={setSlot} />
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
  hidden,
  onSlot,
}: {
  hidden?: boolean
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
      className={cn(
        "@container shrink-0 border-b border-hairline bg-surface-header",
        hidden && "hidden",
      )}
    >
      {/* Narrower than one row holds it folds on purpose, as the Git head
          does: who, with the menu at its end, then the page's verbs on a line
          of their own — which is not there at all for a page with none. */}
      <div className="grid min-h-12 w-full min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-x-2 gap-y-1.5 px-2 py-1.5 @4xl:flex">
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
            <ConnectionSwitcher inset className="font-semibold" />
            {/* What it is for comes before what it is: "production" and
                "protected" are the two words a workbench must not lose, and
                the facts after them give way first. */}
            <div className="flex min-w-0 items-center gap-1.5 text-micro text-muted-foreground">
              {labelled && (
                <span data-slot="database-labels" className="flex min-w-0 items-center gap-1.5">
                  <EnvironmentTag environment={conn.environment} />
                  {readOnly && <ProtectedTag />}
                </span>
              )}
              <span
                className={cn(
                  "flex min-w-0 shrink-[1000] items-center gap-1.5 overflow-hidden",
                  labelled && "@max-xl:hidden",
                )}
              >
                {facts.map((fact, index) => (
                  <Fragment key={fact.key}>
                    {(index > 0 || labelled) && <FactDot />}
                    <span
                      title={fact.title ?? fact.text}
                      className={cn(
                        "truncate",
                        fact.mono && "font-mono",
                        // The address and the database go before the engine does.
                        index === 0 ? "shrink-0" : "min-w-0",
                        index > 0 && "@max-md:hidden",
                      )}
                    >
                      {fact.text}
                    </span>
                  </Fragment>
                ))}
              </span>
            </div>
          </div>
          <DatabaseStatusMark status={status} className="ml-1" />
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
