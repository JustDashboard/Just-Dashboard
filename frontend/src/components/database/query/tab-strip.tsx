"use client"

import { useRef, useState } from "react"
import { Cross, Plus } from "@/components/icons"
import { cn } from "@/lib/utils"
import { IconAction } from "@/components/icon-action"
import { Spinner } from "@/components/state"
import { tabClasses } from "@/components/tabs"
import { isChanged, type QueryTab } from "@/components/database/query/tabs"

/**
 * The editor's tabs: one statement each, the one in front underlined.
 *
 * A tab is closed with its own button and named by pressing its name twice
 * (or F2). A tab that is the draft of a saved query says when its text has
 * moved on from what is saved, in the hue of a change not yet kept; a tab
 * whose statement is still out carries the turning mark of work in flight.
 */
export function TabStrip({
  tabs,
  active,
  running,
  onShow,
  onClose,
  onNew,
  onRename,
  full,
}: {
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
  const list = useRef<HTMLDivElement>(null)

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
    if (to < 0) return
    event.preventDefault()
    onShow(tabs[to].id)
    list.current?.querySelector<HTMLElement>(`[data-tab="${tabs[to].id}"]`)?.focus()
  }

  return (
    <div className="flex min-w-0 flex-1 items-stretch">
      <div
        ref={list}
        role="tablist"
        aria-label="Open statements"
        className="flex min-w-0 [scrollbar-width:none] items-stretch overflow-x-auto [&::-webkit-scrollbar]:hidden"
        onKeyDown={onKey}
      >
        {tabs.map((tab) => {
          const selected = tab.id === active
          const changed = isChanged(tab)
          return (
            <div
              key={tab.id}
              role="presentation"
              className={cn(
                "group/tab flex shrink-0 items-stretch border-r border-hairline",
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
                    if (event.key === "Enter") event.currentTarget.blur()
                    if (event.key === "Escape") {
                      event.currentTarget.value = tab.title
                      event.currentTarget.blur()
                    }
                  }}
                />
              ) : (
                <button
                  type="button"
                  role="tab"
                  data-tab={tab.id}
                  aria-selected={selected}
                  tabIndex={selected ? 0 : -1}
                  title={`${tab.title} — press twice to rename`}
                  className={cn(tabClasses(selected, "h-10"), "max-w-48 pr-1.5")}
                  onClick={() => onShow(tab.id)}
                  onDoubleClick={() => setRenaming(tab.id)}
                >
                  {running.has(tab.id) && <Spinner className="size-3 shrink-0" />}
                  <span className="min-w-0 truncate">{tab.title}</span>
                  {changed && (
                    <span className="shrink-0 font-mono text-hint text-(--git-modified)">
                      ~<span className="sr-only"> changed since it was saved</span>
                    </span>
                  )}
                </button>
              )}
              <button
                type="button"
                aria-label={`Close ${tab.title}`}
                tabIndex={selected ? 0 : -1}
                className={cn(
                  "mr-1 flex size-5 shrink-0 items-center justify-center self-center rounded-sm text-muted-foreground focus-ring transition-colors hover:bg-control-hover hover:text-foreground max-sm:size-8",
                  !selected && "opacity-60",
                )}
                onClick={() => onClose(tab.id)}
              >
                <Cross className="size-3" />
              </button>
            </div>
          )
        })}
      </div>
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
