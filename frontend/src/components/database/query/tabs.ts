/**
 * The editor's tabs, as a value: which statements are open, which one is in
 * front, and what each is called.
 *
 * A tab is a draft. It is kept for the browser tab, per connection, so a
 * reload or a walk to another page and back brings the same statements back.
 * Nothing here ever replaces the text of a tab that holds some: a statement
 * handed over from another page, a history entry, a snippet and a saved query
 * each open in a tab of their own, or fill one that is still blank.
 */

export interface QueryTab {
  id: string
  title: string
  sql: string
  /** The saved query this tab is the draft of. */
  saved?: number
  /** That saved query's text when it was opened or last saved: the draft differs when the text does. */
  savedSql?: string
}

export interface TabsState {
  tabs: QueryTab[]
  /** The id of the tab in front. */
  active: string
  /** The number the next untitled tab takes. */
  next: number
}

export const NO_TABS: TabsState = { tabs: [], active: "", next: 1 }

/** How many tabs a connection keeps open; past it the oldest blank one makes room, or none opens. */
export const MAX_TABS = 24

const blank = (tab: QueryTab) => tab.sql.trim() === "" && tab.saved === undefined

/** The state with a tab to show: a store that holds nothing yet, or holds something else, opens on one blank tab. */
export function withTab(state: TabsState | undefined): TabsState {
  const kept = Array.isArray(state?.tabs)
    ? state.tabs.filter(
        (tab) => tab && typeof tab.id === "string" && typeof tab.sql === "string" && tab.id !== "",
      )
    : []
  // The same list when nothing was dropped from it, so a state in order is handed back as it is.
  const tabs = state && kept.length === state.tabs.length ? state.tabs : kept
  const next = typeof state?.next === "number" && state.next > 0 ? state.next : 1
  if (tabs.length === 0) {
    return {
      tabs: [{ id: `q${next}`, title: `Query ${next}`, sql: "" }],
      active: `q${next}`,
      next: next + 1,
    }
  }
  const active = tabs.some((tab) => tab.id === state?.active) ? state!.active : tabs[0].id
  if (tabs === state?.tabs && active === state.active && next === state.next) return state
  return { tabs, active, next }
}

export function activeTab(state: TabsState): QueryTab {
  const shown = withTab(state)
  return shown.tabs.find((tab) => tab.id === shown.active) ?? shown.tabs[0]
}

export interface Opening {
  sql: string
  /** What the tab is called; untitled tabs are numbered. */
  title?: string
  saved?: number
}

/**
 * Opens a statement: in the tab in front when that one is blank, else in a
 * tab of its own. A saved query that is already open comes to the front
 * instead of opening twice.
 */
export function openTab(state: TabsState, opening: Opening): TabsState {
  const shown = withTab(state)
  if (opening.saved !== undefined) {
    const open = shown.tabs.find((tab) => tab.saved === opening.saved)
    if (open) return { ...shown, active: open.id }
  }
  const bound =
    opening.saved !== undefined ? { saved: opening.saved, savedSql: opening.sql } : undefined
  const front = shown.tabs.find((tab) => tab.id === shown.active)
  if (front && blank(front)) {
    const filled: QueryTab = { ...front, sql: opening.sql, ...bound }
    if (opening.title) filled.title = opening.title
    return { ...shown, tabs: shown.tabs.map((tab) => (tab.id === front.id ? filled : tab)) }
  }
  const tabs = roomFor(shown.tabs)
  if (tabs.length >= MAX_TABS) return shown
  const id = `q${shown.next}`
  return {
    tabs: [
      ...tabs,
      { id, title: opening.title ?? `Query ${shown.next}`, sql: opening.sql, ...bound },
    ],
    active: id,
    next: shown.next + 1,
  }
}

/** At the limit, a blank tab that is not in front gives its place up. */
function roomFor(tabs: QueryTab[]): QueryTab[] {
  if (tabs.length < MAX_TABS) return tabs
  const spare = tabs.find(blank)
  return spare ? tabs.filter((tab) => tab !== spare) : tabs
}

/**
 * A statement handed over by another page. A tab that already holds exactly
 * that statement comes to the front — the same hand-over read twice (a
 * reload before the address was cleaned) is one tab, not two.
 */
export function handOver(state: TabsState, sql: string): TabsState {
  const shown = withTab(state)
  const same = shown.tabs.find((tab) => tab.sql === sql)
  if (same) return { ...shown, active: same.id }
  return openTab(shown, { sql })
}

/** A new blank tab, in front. */
export function newTab(state: TabsState): TabsState {
  const shown = withTab(state)
  const tabs = roomFor(shown.tabs)
  if (tabs.length >= MAX_TABS) return shown
  const id = `q${shown.next}`
  return {
    tabs: [...tabs, { id, title: `Query ${shown.next}`, sql: "" }],
    active: id,
    next: shown.next + 1,
  }
}

/** Closes a tab. The one beside it comes to the front, and the last one closed leaves a blank one. */
export function closeTab(state: TabsState, id: string): TabsState {
  const shown = withTab(state)
  const at = shown.tabs.findIndex((tab) => tab.id === id)
  if (at < 0) return shown
  const tabs = shown.tabs.filter((tab) => tab.id !== id)
  if (tabs.length === 0) return withTab({ tabs, active: "", next: shown.next })
  const active = shown.active === id ? tabs[Math.min(at, tabs.length - 1)].id : shown.active
  return { ...shown, tabs, active }
}

/** Puts a closed tab back where it was, in front. */
export function restoreTab(state: TabsState, tab: QueryTab, at: number): TabsState {
  const shown = withTab(state)
  if (shown.tabs.some((open) => open.id === tab.id)) return { ...shown, active: tab.id }
  const tabs = [...shown.tabs]
  tabs.splice(Math.min(Math.max(at, 0), tabs.length), 0, tab)
  return { ...shown, tabs, active: tab.id }
}

export function showTab(state: TabsState, id: string): TabsState {
  const shown = withTab(state)
  return shown.tabs.some((tab) => tab.id === id) ? { ...shown, active: id } : shown
}

const change = (state: TabsState, id: string, patch: (tab: QueryTab) => QueryTab): TabsState => {
  const shown = withTab(state)
  return { ...shown, tabs: shown.tabs.map((tab) => (tab.id === id ? patch(tab) : tab)) }
}

export function setTabSql(state: TabsState, id: string, sql: string): TabsState {
  return change(state, id, (tab) => (tab.sql === sql ? tab : { ...tab, sql }))
}

export function renameTab(state: TabsState, id: string, title: string): TabsState {
  const name = title.trim()
  return name ? change(state, id, (tab) => ({ ...tab, title: name })) : withTab(state)
}

/** The tab is now the draft of this saved query, as it was just written. */
export function bindSaved(
  state: TabsState,
  id: string,
  saved: { id: number; name: string; sql: string },
): TabsState {
  return change(state, id, (tab) => ({
    ...tab,
    title: saved.name,
    saved: saved.id,
    savedSql: saved.sql,
  }))
}

/** A saved query was renamed or deleted elsewhere: its tab follows the name, or becomes a plain draft. */
export function followSaved(
  state: TabsState,
  saved: { id: number; name?: string; gone?: boolean },
): TabsState {
  const shown = withTab(state)
  return {
    ...shown,
    tabs: shown.tabs.map((tab) => {
      if (tab.saved !== saved.id) return tab
      if (saved.gone) return { id: tab.id, title: tab.title, sql: tab.sql }
      return saved.name ? { ...tab, title: saved.name } : tab
    }),
  }
}

/** A tab on a saved query whose text has moved on from what is saved. */
export function isChanged(tab: QueryTab): boolean {
  return tab.saved !== undefined && tab.savedSql !== undefined && tab.savedSql !== tab.sql
}
