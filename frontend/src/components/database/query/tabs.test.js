import { describe, expect, test } from "bun:test"
import {
  MAX_TABS,
  NO_TABS,
  activeTab,
  bindSaved,
  closeTab,
  followSaved,
  handOver,
  hasRoom,
  isChanged,
  newTab,
  openTab,
  renameTab,
  restoreTab,
  setTabSql,
  showTab,
  titleOf,
  withTab,
} from "./tabs"

const titles = (state) => state.tabs.map((tab) => tab.title)

describe("the editor's tabs", () => {
  test("a connection with nothing kept opens on one blank tab", () => {
    const state = withTab(NO_TABS)
    expect(state.tabs).toEqual([{ id: "q1", title: "Query 1", sql: "" }])
    expect(state.active).toBe("q1")
    expect(withTab(undefined).tabs).toHaveLength(1)
    // Something else under the key — an older shape — is not handed to the page.
    expect(withTab({ tabs: "nope", active: 3 }).tabs).toHaveLength(1)
  })
  test("a state that already has its tab is handed back as it is", () => {
    const state = withTab(NO_TABS)
    expect(withTab(state)).toBe(state)
  })
  test("a statement fills the blank tab in front, and the next one opens beside it", () => {
    let state = openTab(NO_TABS, { sql: "select 1" })
    expect(state.tabs).toHaveLength(1)
    expect(activeTab(state).sql).toBe("select 1")
    state = openTab(state, { sql: "select 2", title: "Largest tables" })
    expect(titles(state)).toEqual(["Query 1", "Largest tables"])
    expect(activeTab(state).sql).toBe("select 2")
  })
  test("a tab that holds text is never written over", () => {
    let state = setTabSql(withTab(NO_TABS), "q1", "delete from orders where id = 1 -- my draft")
    state = openTab(state, { sql: "select 1" })
    state = handOver(state, "select 2")
    expect(state.tabs.map((tab) => tab.sql)).toEqual([
      "delete from orders where id = 1 -- my draft",
      "select 1",
      "select 2",
    ])
  })
  test("a hand-over read twice is one tab: the tab that holds the statement comes to the front", () => {
    let state = handOver(NO_TABS, "select * from orders")
    state = newTab(state)
    const again = handOver(state, "select * from orders")
    expect(again.tabs).toHaveLength(2)
    expect(activeTab(again).sql).toBe("select * from orders")
  })
  test("a saved query opens once: asked for again, its tab comes to the front", () => {
    let state = openTab(NO_TABS, { sql: "select 1", title: "Daily", saved: 7 })
    expect(activeTab(state)).toMatchObject({ title: "Daily", saved: 7, savedSql: "select 1" })
    state = newTab(state)
    state = openTab(state, { sql: "select 1", title: "Daily", saved: 7 })
    expect(state.tabs).toHaveLength(2)
    expect(activeTab(state).saved).toBe(7)
  })
  test("closing a tab brings its neighbour forward; closing the last leaves a blank one", () => {
    let state = openTab(openTab(openTab(NO_TABS, { sql: "a" }), { sql: "b" }), { sql: "c" })
    state = showTab(state, "q2")
    state = closeTab(state, "q2")
    expect(state.tabs.map((tab) => tab.sql)).toEqual(["a", "c"])
    expect(activeTab(state).sql).toBe("c")
    state = closeTab(closeTab(state, "q3"), "q1")
    expect(state.tabs).toHaveLength(1)
    expect(activeTab(state).sql).toBe("")
    // The numbering goes on: a closed tab's name is not handed to another.
    expect(activeTab(state).title).toBe("Query 4")
  })
  test("a closed tab can be put back where it was", () => {
    let state = openTab(openTab(openTab(NO_TABS, { sql: "a" }), { sql: "b" }), { sql: "c" })
    const closed = state.tabs[1]
    state = restoreTab(closeTab(state, closed.id), closed, 1)
    expect(state.tabs.map((tab) => tab.sql)).toEqual(["a", "b", "c"])
    expect(state.active).toBe(closed.id)
  })
  test("typing changes that tab's text only", () => {
    let state = openTab(openTab(NO_TABS, { sql: "a" }), { sql: "b" })
    state = setTabSql(state, "q1", "a2")
    expect(state.tabs.map((tab) => tab.sql)).toEqual(["a2", "b"])
  })
  test("a rename takes a name, and nothing is not one", () => {
    const state = openTab(NO_TABS, { sql: "a" })
    expect(titles(renameTab(state, "q1", "  Sizes "))).toEqual(["Sizes"])
    expect(titles(renameTab(state, "q1", "   "))).toEqual(["Query 1"])
  })
  test("a tab saved becomes the draft of that saved query, and says when its text has moved on", () => {
    let state = openTab(NO_TABS, { sql: "select 1" })
    state = bindSaved(state, "q1", { id: 3, name: "One", sql: "select 1" })
    expect(isChanged(activeTab(state))).toBe(false)
    state = setTabSql(state, "q1", "select 2")
    expect(isChanged(activeTab(state))).toBe(true)
    expect(isChanged({ id: "x", title: "x", sql: "anything" })).toBe(false)
  })
  test("a saved query renamed or deleted in the rail is followed by its tab", () => {
    let state = openTab(NO_TABS, { sql: "select 1", title: "One", saved: 3 })
    state = followSaved(state, { id: 3, name: "Uno" })
    expect(activeTab(state).title).toBe("Uno")
    state = followSaved(state, { id: 3, gone: true })
    expect(activeTab(state)).toEqual({ id: "q1", title: "Uno", sql: "select 1" })
  })
  test("at the limit a blank tab makes room, and a full set opens nothing more", () => {
    let state = NO_TABS
    for (let n = 0; n < MAX_TABS; n++) state = openTab(newTab(state), { sql: `select ${n}` })
    expect(state.tabs.length).toBeLessThanOrEqual(MAX_TABS)
    const full = { ...state, tabs: state.tabs.map((tab) => ({ ...tab, sql: tab.sql || "x" })) }
    expect(full.tabs).toHaveLength(MAX_TABS)
    expect(openTab(full, { sql: "one more" }).tabs).toHaveLength(MAX_TABS)
    expect(newTab(full).tabs).toHaveLength(MAX_TABS)
  })
})

describe("opening at the limit, and what a tab is called", () => {
  const full = () => {
    let state = NO_TABS
    for (let n = 0; n < MAX_TABS; n++) state = openTab(newTab(state), { sql: `select ${n}` })
    return { ...state, tabs: state.tabs.map((tab) => ({ ...tab, sql: tab.sql || "x" })) }
  }
  test("there is room for a statement while a tab is free, blank, or already holds it", () => {
    expect(hasRoom(NO_TABS, { sql: "select 1" })).toBe(true)
    const state = full()
    expect(state.tabs).toHaveLength(MAX_TABS)
    // At the limit with every tab holding something an opening used to do nothing, silently.
    expect(hasRoom(state, { sql: "handed over at the limit" })).toBe(false)
    expect(hasRoom(state, { sql: state.tabs[3].sql })).toBe(true)
    const blanked = {
      ...state,
      tabs: state.tabs.map((tab, n) => (n === 5 ? { ...tab, sql: "" } : tab)),
    }
    expect(hasRoom(blanked, { sql: "one more" })).toBe(true)
  })
  test("a saved query has room when its own tab is open, whatever its text has become", () => {
    const state = full()
    const bound = {
      ...state,
      tabs: state.tabs.map((tab, n) => (n === 0 ? { ...tab, saved: 9, savedSql: "old" } : tab)),
    }
    expect(hasRoom(bound, { sql: "old", saved: 9 })).toBe(true)
    expect(hasRoom(bound, { sql: "old", saved: 10 })).toBe(false)
  })
  test("the same statement brought in twice is one tab, named for the statement", () => {
    let state = handOver(NO_TABS, "select 1", "select 1")
    state = handOver(
      state,
      "select * from orders where id = 7",
      titleOf("select * from orders where id = 7"),
    )
    state = handOver(state, "select 1", "select 1")
    expect(state.tabs.map((tab) => tab.title)).toEqual(["select 1", "select * from orders where…"])
    expect(activeTab(state).sql).toBe("select 1")
  })
  test("a tab's name from its statement: its first words, leading comments left out", () => {
    expect(titleOf("select 1")).toBe("select 1")
    expect(titleOf("-- sizes\n\nselect   relname,\n pg_size from pg_class")).toBe(
      "select relname, pg_size…",
    )
    expect(titleOf("x".repeat(60))).toBe(`${"x".repeat(28)}…`)
  })
})
