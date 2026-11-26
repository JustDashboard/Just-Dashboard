"use client"

import { useCallback, useState } from "react"
import { useSearchParams } from "next/navigation"
import { ChevronDown, ChevronUp } from "@/components/icons"
import { useViewState } from "@/lib/view-state"
import { IconAction } from "@/components/icon-action"
import { Page, PageContext, type Back } from "@/components/page"
import { Workspace, WorkspaceHelp } from "@/components/workspace/workspace"
import type { WorkspaceCommand } from "@/components/workspace/commands"

/**
 * An item's detail as a place of its own (D9): its own route with a way back,
 * its views in the address as `?tab=`, and from `xl` the list it was opened
 * from as a rail beside it, so the next one is one press away and Alt+↑ and
 * Alt+↓ walk it. On a phone the page is the one pane and scrolls as a whole,
 * with the way back and the same two arrows.
 *
 * Two dozen pages wrote this frame by hand: the Workspace with the two
 * commands, `Page fill="xl"`, the row holding the rail, a `PageContext` whose
 * eyebrow was a hand-made back link, the arrows and the help, and a column
 * that scrolls under a head that stays put. This is the one frame; a page
 * hands it its rail (`components/rail.tsx`), its identity line and its views.
 * The recipe is `docs/internal/frontend/recipes/detail-with-rail.md`.
 *
 * The item comes before the list in the document, though the list is drawn
 * first: Tab from the shell reaches the item's own way back, views and
 * controls, and the rail's search and every one of its rows come after them
 * rather than standing between a keyboard and the page it opened.
 */
export type Step = { disabled: boolean; run: () => void }

/**
 * The one before and the one after `current` in the rail's order. With no
 * current (an item the list does not hold), Alt+↓ opens the first and Alt+↑
 * the last.
 */
export function siblingSteps<K>(
  keys: readonly K[],
  current: K | null | undefined,
  open: (key: K) => void,
): { previous: Step; next: Step } {
  const at = current === null || current === undefined ? -1 : keys.indexOf(current)
  const go = (direction: number) => {
    const next = at >= 0 ? keys[at + direction] : keys[direction > 0 ? 0 : keys.length - 1]
    if (next !== undefined) open(next)
  }
  return {
    previous: { disabled: at <= 0, run: () => go(-1) },
    next: { disabled: keys.length === 0 || at >= keys.length - 1, run: () => go(1) },
  }
}

/** `siblingSteps` for a page: the rail's keys, the one on screen and how to open another. */
export function useSiblingSteps<K>(
  keys: readonly K[],
  current: K | null | undefined,
  open: (key: K) => void,
) {
  return siblingSteps(keys, current, open)
}

/** The two Workspace commands Alt+↑ and Alt+↓ run, titled by what they walk. */
export function stepCommands(
  what: string,
  { previous, next }: { previous: Step; next: Step },
): WorkspaceCommand[] {
  return [
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
  ]
}

/**
 * The view on screen, kept in the address as `?tab=` (or `param`). The first
 * view is the bare address. It is written to the history at once, replaced
 * rather than pushed: a view is not a place Back steps through, and a write
 * held back by a debounce would cancel a navigation the router has pending.
 * An address that changes under the page (a link to another view of the same
 * thing, or to the bare page) is followed, by the same rules it opens on.
 *
 * With `remember`, the last view is kept under that key and a page opened
 * without `?tab=` opens on it. With `keepMounted`, the third value is the
 * views visited so far, so their panes can stay mounted and keep their state.
 */
export function useDetailTab<T extends string>(
  tabs: readonly T[],
  { param = "tab", remember }: { param?: string; remember?: string; keepMounted?: boolean } = {},
): [T, (next: string) => void, ReadonlySet<T>] {
  const search = useSearchParams()
  const first = tabs[0]
  const [remembered, keep] = useViewState<string>(remember ?? "detail-page.tab", first)
  const asked = search.get(param)
  // A bare address opens on the remembered view, or the first.
  const bare = remember ? remembered : first
  const [chosen, setChosen] = useState<string>(asked ?? bare)
  const [seen, setSeen] = useState(asked)
  if (asked !== seen) {
    setSeen(asked)
    // The bare address is followed too, by the rule it opens on: a page
    // left mounted on `?tab=ports` and handed its bare address showed Ports.
    setChosen(asked ?? bare)
  }
  const tab = (tabs as readonly string[]).includes(chosen) ? (chosen as T) : first
  const [visited, setVisited] = useState<ReadonlySet<T>>(() => new Set([tab]))
  if (!visited.has(tab)) setVisited(new Set([...visited, tab]))
  const setTab = useCallback(
    (next: string) => {
      setChosen(next)
      if (remember) keep(next)
      const url = new URL(window.location.href)
      if (next === first) url.searchParams.delete(param)
      else url.searchParams.set(param, next)
      window.history.replaceState(null, "", `${url.pathname}${url.search}`)
    },
    [first, param, remember, keep],
  )
  return [tab, setTab, visited]
}

/** The two arrows a phone walks the list with, and the shortcuts' help. */
export function StepArrows({
  what,
  previous,
  next,
  help = true,
}: {
  /** What is being walked: "container", "account". */
  what: string
  previous: Step
  next: Step
  /** The `?` that opens the page's shortcuts, beside the arrows from `sm`. */
  help?: boolean
}) {
  return (
    <span className="flex items-center">
      <IconAction label={`Previous ${what}`} disabled={previous.disabled} onClick={previous.run}>
        <ChevronUp />
      </IconAction>
      <IconAction label={`Next ${what}`} disabled={next.disabled} onClick={next.run}>
        <ChevronDown />
      </IconAction>
      {help && (
        <span className="max-sm:hidden">
          <WorkspaceHelp compact />
        </span>
      )}
    </span>
  )
}

/**
 * The detail-with-rail frame. The head (the way back with the arrows, then the
 * identity line) stays put from `xl` while the body scrolls under it; narrower
 * the page scrolls as a whole. `name` and `stateKey` are the Workspace's: the
 * help sheet is titled by the one and the remembered place kept under the
 * other, so a page moving onto this frame keeps both as they were. `escape`
 * is the Workspace's too: what Escape lets go of on this page (a picked run),
 * answering whether it took the key.
 */
export function DetailPage({
  name,
  what,
  stateKey,
  refresh,
  escape,
  steps,
  rail,
  back,
  title,
  actions,
  head,
  identity,
  children,
}: {
  /** The workspace's name, as its shortcuts are titled: "Container". */
  name: string
  /** What the arrows step through, in the singular: "container". */
  what: string
  stateKey: string
  refresh?: () => void
  /** What Escape lets go of here; `true` when it took the key. */
  escape?: () => boolean
  steps: { previous: Step; next: Step }
  /** The sibling rail (`Rail`), shown from `xl`. */
  rail: React.ReactNode
  /** The way back to the list: where it leads and the list's name. */
  back?: Back
  /** The page's name for the tab and the screen reader's heading. */
  title?: React.ReactNode
  /** Verbs beside the arrows, where a page has them in its context line. */
  actions?: React.ReactNode
  /** A head the page draws itself, in place of the way back and the arrows. */
  head?: React.ReactNode
  /** The identity line under the way back. */
  identity?: React.ReactNode
  /** The views and their panes: the part that scrolls. */
  children: React.ReactNode
}) {
  return (
    <Workspace
      name={name}
      stateKey={stateKey}
      refresh={refresh}
      escape={escape}
      commands={stepCommands(what, steps)}
    >
      {/* Held to the window where the rail stands beside it, so the list and
          the identity line stay put while the page scrolls; narrower, the
          page is the one pane and scrolls as a whole. */}
      <Page fill="xl" className="gap-0 md:gap-0">
        <div className="flex min-w-0 flex-1 gap-6 xl:min-h-0">
          {/* First in the document, second on screen: `order` puts the rail,
              which follows it, on the leading edge from `xl`. */}
          <div className="flex min-w-0 flex-1 flex-col gap-5 xl:order-last xl:min-h-0">
            <div className="flex min-w-0 shrink-0 flex-col gap-5">
              {head ?? (
                <PageContext
                  back={back}
                  title={title ?? name}
                  actions={
                    <>
                      {actions}
                      <StepArrows what={what} previous={steps.previous} next={steps.next} />
                    </>
                  }
                />
              )}
              {identity}
            </div>
            <div className="flex min-w-0 flex-col gap-8 xl:min-h-0 xl:flex-1 xl:overflow-y-auto xl:pr-1">
              {children}
            </div>
          </div>
          {rail}
        </div>
      </Page>
    </Workspace>
  )
}
