import { describe, expect, test } from "bun:test"
import { pickView, viewSearch } from "./use-view-tab"

// The order a view is chosen in and what the address becomes, without a
// router. The strips themselves are held by the pages' browser specs.

const VIEWS = ["overview", "journal", "settings"]

describe("pickView", () => {
  test("the address wins when the page has the view it names", () => {
    expect(pickView(VIEWS, "journal", "settings")).toBe("journal")
  })

  test("a bare address opens the remembered view, then the first", () => {
    expect(pickView(VIEWS, null, "settings")).toBe("settings")
    expect(pickView(VIEWS, null, null)).toBe("overview")
    expect(pickView(VIEWS, undefined)).toBe("overview")
  })

  test("an unknown value falls back to the first view, not to nothing", () => {
    expect(pickView(VIEWS, "logs")).toBe("overview")
    expect(pickView(VIEWS, "", null)).toBe("overview")
  })

  test("an unknown value still lets a remembered one stand", () => {
    expect(pickView(VIEWS, "logs", "journal")).toBe("journal")
  })

  test("a remembered view the page no longer has is ignored", () => {
    expect(pickView(VIEWS, null, "metrics")).toBe("overview")
  })
})

describe("viewSearch", () => {
  test("writes the view under the parameter it was given: tab or view", () => {
    expect(viewSearch("", "tab", "journal", "overview")).toBe("?tab=journal")
    expect(viewSearch("", "view", "journal", "overview")).toBe("?view=journal")
  })

  test("the first view is the bare address", () => {
    expect(viewSearch("?tab=journal", "tab", "overview", "overview")).toBe("")
    expect(viewSearch("?view=journal", "view", "overview", "overview")).toBe("")
  })

  test("every other parameter is kept, and only the named one moves", () => {
    expect(viewSearch("?q=nginx&tab=journal", "tab", "settings", "overview")).toBe(
      "?q=nginx&tab=settings",
    )
    expect(viewSearch("?q=nginx&tab=journal", "tab", "overview", "overview")).toBe("?q=nginx")
    expect(viewSearch("?tab=journal", "view", "settings", "overview")).toBe(
      "?tab=journal&view=settings",
    )
  })
})
