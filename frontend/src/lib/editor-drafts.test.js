import { describe, expect, test } from "bun:test"
import { createEditorDraftStore } from "./editor-drafts"

describe("retained editor drafts", () => {
  test("keeps each buffer and its original revision separate until explicit discard", () => {
    const store = createEditorDraftStore()
    const owner = store.generation()
    store.set("nginx:a", { content: "unsaved", revision: "old" }, owner)
    store.set("caddy:a", { content: "other", revision: "original" }, owner)
    store.forget("caddy:a", owner)
    expect(store.get("nginx:a")).toEqual({ content: "unsaved", revision: "old" })
    expect(store.get("caddy:a")).toBeUndefined()
  })

  test("waits for the departing editor's acknowledgement before a new editor opens", async () => {
    const store = createEditorDraftStore()
    const owner = store.generation()
    let release
    const response = new Promise((resolve) => (release = resolve))
    const save = response.then(() => store.set("board:1", { revision: 2 }, owner))
    store.track("board:1", save, owner)
    let opened = false
    const opening = store.settled("board:1").then(() => (opened = true))
    await Promise.resolve()
    expect(opened).toBe(false)
    release()
    await opening
    expect(store.get("board:1")).toEqual({ revision: 2 })
  })

  test("a late save cannot resurrect or clear content after an account reset", () => {
    const store = createEditorDraftStore()
    const previous = store.generation()
    store.set("file", "old account", previous)
    store.clear()
    store.set("file", "new account", store.generation())
    store.set("file", "late response", previous)
    store.forget("file", previous)
    expect(store.get("file")).toBe("new account")
  })
})
