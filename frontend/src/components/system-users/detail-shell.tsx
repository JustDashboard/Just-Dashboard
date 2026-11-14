"use client"

import { useCallback, useState } from "react"
import { useSearchParams } from "next/navigation"
import { Tabs as TabsPrimitive } from "radix-ui"
import { ChevronDown, ChevronUp } from "@/components/icons"
import { IconAction } from "@/components/icon-action"
import { ChipCount, tabClasses } from "@/components/tabs"
import { WorkspaceHelp } from "@/components/workspace/workspace"

/**
 * What an account's page and a group's page share with a service's
 * (`procs/service-page.tsx`), whose shape they are built on: the view in the
 * address, the underlined strip that picks it, and the two arrows that walk the
 * column of siblings.
 */

/**
 * The view on screen, kept in the address as `?tab=`. The first view is the
 * bare address. It is replaced rather than pushed, because a view is not a
 * place Back should step through, and an address that changes under the page
 * (a link to another view of the same thing) is followed.
 */
export function useAddressTab<T extends string>(tabs: readonly T[]): [T, (next: string) => void] {
  const search = useSearchParams()
  const requested = search.get("tab") ?? tabs[0]
  const [chosen, setChosen] = useState<string>(requested)
  const [seen, setSeen] = useState(requested)
  if (requested !== seen) {
    setSeen(requested)
    setChosen(requested)
  }
  const tab = (tabs as readonly string[]).includes(chosen) ? (chosen as T) : tabs[0]
  const first = tabs[0]
  const setTab = useCallback(
    (next: string) => {
      setChosen(next)
      const url = new URL(window.location.href)
      if (next === first) url.searchParams.delete("tab")
      else url.searchParams.set("tab", next)
      window.history.replaceState(null, "", `${url.pathname}${url.search}`)
    },
    [first],
  )
  return [tab, setTab]
}

export type View<T extends string> = { key: T; label: string; count?: number }

/** The underlined strip of a thing's views. It stands inside a `Tabs`. */
export function ViewTabs<T extends string>({
  label,
  views,
  shown,
}: {
  label: string
  views: View<T>[]
  shown: T
}) {
  return (
    <TabsPrimitive.List
      aria-label={label}
      className="flex shrink-0 [scrollbar-width:none] gap-1 overflow-x-auto border-b border-hairline [&::-webkit-scrollbar]:hidden"
    >
      {views.map((view) => (
        <TabsPrimitive.Trigger
          key={view.key}
          value={view.key}
          className={tabClasses(shown === view.key, "h-10")}
        >
          {view.label}
          {view.count !== undefined && view.count > 0 && <ChipCount>{view.count}</ChipCount>}
        </TabsPrimitive.Trigger>
      ))}
    </TabsPrimitive.List>
  )
}

export type Step = { disabled: boolean; run: () => void }

/** Up and down the column of siblings, and what the keys are. */
export function StepArrows({
  noun,
  previous,
  next,
}: {
  /** What is being walked: "account", "group". */
  noun: string
  previous: Step
  next: Step
}) {
  return (
    <span className="flex items-center">
      <IconAction label={`Previous ${noun}`} disabled={previous.disabled} onClick={previous.run}>
        <ChevronUp />
      </IconAction>
      <IconAction label={`Next ${noun}`} disabled={next.disabled} onClick={next.run}>
        <ChevronDown />
      </IconAction>
      <span className="max-sm:hidden">
        <WorkspaceHelp compact />
      </span>
    </span>
  )
}

/** The route's segment as a name; decoding a name twice is harmless. */
export function decodeSegment(segment: string | undefined): string {
  if (!segment) return ""
  try {
    return decodeURIComponent(segment)
  } catch {
    return segment
  }
}
