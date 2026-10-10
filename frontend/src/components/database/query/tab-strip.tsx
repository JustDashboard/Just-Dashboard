"use client"

import { useEffect, useRef, useState } from "react"
import { ChevronDown, Cross, Plus } from "@/components/icons"
import { cn } from "@/lib/utils"
import { IconAction } from "@/components/icon-action"
import { Spinner } from "@/components/state"
import { ChipCount, tabClasses } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { focusSoon } from "@/components/database/query/focus"
import { StatementLine } from "@/components/database/query/messages"
import { isChanged, type QueryTab } from "@/components/database/query/tabs"

/** The element id of a tab, so the editor under the strip can say which tab it is the panel of. */
export const tabElementId = (base: string, tab: string) => `${base}-tab-${tab}`

/**
 * The editor's tabs: one statement each, the one in front underlined.
 *
 * A tab is closed with its own button (or Delete) and named by pressing its
 * name twice (or F2). A tab that is the draft of a saved query says when its
 * text has moved on from what is saved, in the hue of a change not yet kept;
 * a tab whose statement is still out carries the turning mark of work in
 * flight.
 *
 * The tabs are a tablist and only tabs are in it: the close buttons sit
 * beside it, laid between the tabs by their order, so a reader of the list
 * hears tabs and nothing else. With more tabs than fit, the strip scrolls to
 * keep the one in front in view, shades the edge it runs under, and offers
 * every tab in one menu.
 */
export function TabStrip({
  idBase,
  panelId,
  tabs,
  active,
  running,
  onShow,
  onClose,
  onNew,
  onRename,
  full,
}: {
  /** The prefix of each tab's element id (`tabElementId`). */
  idBase: string
  /** The element the tab in front is the tab of: the editor. */
  panelId: string
  tabs: QueryTab[]
  active: string
  /** The ids of the tabs whose statement has not come back. */
  running: ReadonlySet<string>
  onShow: (id: string) => void
  onClose: (id: string) => void
  onNew: () => void
  onRename: (id: string, title: string) => void
  /** No more tabs can be opened. */
  full: boolean
}) {
  const [renaming, setRenaming] = useState<string | null>(null)
  const scroller = useRef<HTMLDivElement>(null)
  const [overflows, setOverflows] = useState(false)

  // The tab in front is kept in view: one opened past the edge of the strip
  // is otherwise a tab nobody can see, with nothing underlined.
  useEffect(() => {
    const node = scroller.current
    // The tab, then its close button beside it: both ends of it in view.
    for (const part of ['[role="tab"][aria-selected="true"]', "[data-front]"]) {
      node
        ?.querySelector<HTMLElement>(part)
        ?.scrollIntoView({ block: "nearest", inline: "nearest" })
    }
  }, [active, tabs.length])

  useEffect(() => {
    const node = scroller.current
    if (!node) return
    const measure = () => setOverflows(node.scrollWidth > node.clientWidth + 1)
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(node)
    return () => observer.disconnect()
  }, [tabs])

  const focusFront = () =>
    focusSoon(() =>
      scroller.current?.querySelector<HTMLElement>('[role="tab"][aria-selected="true"]'),
    )
  // The keyboard goes to the tab that comes to the front, not to nowhere.
  const close = (id: string) => {
    onClose(id)
    focusFront()
  }

  const onKey = (event: React.KeyboardEvent) => {
    if (renaming) return
    const at = tabs.findIndex((tab) => tab.id === active)
    const to =
      event.key === "ArrowRight"
        ? (at + 1) % tabs.length
        : event.key === "ArrowLeft"
          ? (at + tabs.length - 1) % tabs.length
          : event.key === "Home"
            ? 0
            : event.key === "End"
              ? tabs.length - 1
              : -1
    if (event.key === "F2") {
      event.preventDefault()
      setRenaming(active)
      return
    }
    if (event.key === "Delete") {
      event.preventDefault()
      close(active)
      return
    }
    if (to < 0) return
    event.preventDefault()
    onShow(tabs[to].id)
    scroller.current?.querySelector<HTMLElement>(`[data-tab="${tabs[to].id}"]`)?.focus()
  }

  return (
    <div className="flex min-w-0 flex-1 items-stretch">
      <div
        ref={scroller}
        data-slot="query-tabs"
        // The shade at an edge the tabs run under is cast on the strip's own ground.
        className="scroll-affordance flex min-w-0 [scrollbar-width:none] items-stretch overflow-x-auto [--panel-ground:var(--surface-header)] [&::-webkit-scrollbar]:hidden"
      >
        <div role="tablist" aria-label="Open statements" className="contents" onKeyDown={onKey}>
          {tabs.map((tab, index) => {
            const selected = tab.id === active
            if (renaming === tab.id) return null
            return (
              <button
                key={tab.id}
                type="button"
                role="tab"
                id={tabElementId(idBase, tab.id)}
                data-tab={tab.id}
                aria-selected={selected}
                aria-controls={selected ? panelId : undefined}
                tabIndex={selected ? 0 : -1}
                title={`${tab.title} — press twice to rename`}
                style={{ order: index * 2 }}
                className={cn(
                  tabClasses(selected, "h-10"),
                  "max-w-48 shrink-0 pr-1.5",
                  selected && "bg-card",
                )}
                onClick={() => onShow(tab.id)}
                onDoubleClick={() => setRenaming(tab.id)}
                onAuxClick={(event) => {
                  if (event.button !== 1) return
                  event.preventDefault()
                  close(tab.id)
                }}
              >
                {running.has(tab.id) && <Spinner className="size-3 shrink-0" />}
                <span className="min-w-0 truncate">{tab.title}</span>
                {isChanged(tab) && (
                  <span className="shrink-0 font-mono text-hint text-(--git-modified)">
                    ~<span className="sr-only"> changed since it was saved</span>
                  </span>
                )}
              </button>
            )
          })}
        </div>
        {tabs.map((tab, index) => {
          const selected = tab.id === active
          return (
            <div
              key={tab.id}
              data-front={selected || undefined}
              style={{ order: index * 2 + 1 }}
              className={cn(
                "flex shrink-0 items-center border-r border-hairline",
                selected && "bg-card",
              )}
            >
              {renaming === tab.id ? (
                <input
                  autoFocus
                  defaultValue={tab.title}
                  maxLength={200}
                  aria-label="Name of this tab"
                  className="h-10 w-40 bg-transparent px-3 text-body font-medium focus-ring-inset"
                  onFocus={(event) => event.target.select()}
                  onBlur={(event) => {
                    onRename(tab.id, event.target.value)
                    setRenaming(null)
                  }}
                  onKeyDown={(event) => {
                    if (event.key === "Enter" || event.key === "Escape") {
                      if (event.key === "Escape") event.currentTarget.value = tab.title
                      event.currentTarget.blur()
                      focusFront()
                    }
                  }}
                />
              ) : (
                <button
                  type="button"
                  aria-label={`Close ${tab.title}`}
                  tabIndex={selected ? 0 : -1}
                  className={cn(
                    "mr-1 flex size-5 shrink-0 items-center justify-center rounded-sm text-muted-foreground focus-ring transition-colors hover:bg-control-hover hover:text-foreground max-sm:size-8",
                    !selected && "opacity-60",
                  )}
                  onClick={() => close(tab.id)}
                >
                  <Cross className="size-3" />
                </button>
              )}
            </div>
          )
        })}
      </div>
      {overflows && (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              size="xs"
              variant="ghost"
              aria-label={`All ${tabs.length} tabs`}
              className="ml-1 h-7 shrink-0 gap-1 self-center px-1.5 max-sm:h-8"
            >
              <ChipCount className="opacity-100">{tabs.length}</ChipCount>
              <ChevronDown className="size-3" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="max-h-80 w-72 overflow-y-auto">
            {tabs.map((tab) => (
              <DropdownMenuItem
                key={tab.id}
                className={cn(tab.id === active && "bg-accent")}
                onSelect={() => {
                  onShow(tab.id)
                  focusFront()
                }}
              >
                <span className="flex min-w-0 flex-1 flex-col gap-0.5">
                  <span className="flex min-w-0 items-center gap-1.5">
                    {running.has(tab.id) && <Spinner className="size-3 shrink-0" />}
                    <span className="min-w-0 truncate text-xs font-medium">{tab.title}</span>
                  </span>
                  {tab.sql.trim() !== "" && (
                    <StatementLine sql={tab.sql} className="text-hint text-muted-foreground" />
                  )}
                </span>
              </DropdownMenuItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>
      )}
      <IconAction
        label={full ? "No more tabs can be opened" : "New tab"}
        className="mx-1 size-7 shrink-0 self-center max-sm:size-8"
        disabled={full}
        onClick={onNew}
      >
        <Plus />
      </IconAction>
    </div>
  )
}
