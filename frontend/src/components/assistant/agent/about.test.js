import { describe, expect, test } from "bun:test"
import {
  ABOUT_PATH,
  ABOUT_TITLE,
  askAboutHref,
  boundAbout,
  isAssistantPage,
  readAbout,
} from "./about"

describe("Ask from a page", () => {
  test("names the page and its title in the assistant's address, and reads them back", () => {
    const href = askAboutHref("/databases/3/logs?source=server", "shop · Logs")
    expect(href.startsWith("/assistant?")).toBe(true)
    const params = new URLSearchParams(href.split("?")[1])
    expect(readAbout(params)).toEqual({
      path: "/databases/3/logs?source=server",
      title: "shop · Logs",
    })
  })

  test("is never a link out of the dashboard", () => {
    for (const path of [
      "//evil.example/x",
      "/\\evil.example",
      "https://evil.example",
      "javascript:alert(1)",
      "logs",
      "/a b",
      "",
    ]) {
      expect(boundAbout({ path, title: "x" })).toBeUndefined()
    }
    expect(askAboutHref("//evil.example", "x")).toBe("/assistant")
  })

  test("keeps within what the server takes: the query string goes before the page does", () => {
    const long = `/files?path=${"a".repeat(ABOUT_PATH)}`
    expect(boundAbout({ path: long, title: "Files" })).toEqual({ path: "/files", title: "Files" })
    expect(boundAbout({ path: `/${"a".repeat(ABOUT_PATH)}`, title: "x" })).toBeUndefined()
    const title = boundAbout({ path: "/", title: "é".repeat(200) }).title
    expect([...title]).toHaveLength(ABOUT_TITLE)
    expect(title.endsWith("…")).toBe(true)
    // A page with no title is named by its address.
    expect(boundAbout({ path: "/docker?x=1", title: "  " }).title).toBe("/docker")
  })

  test("is not offered on the assistant's own pages", () => {
    expect(isAssistantPage("/assistant")).toBe(true)
    expect(isAssistantPage("/deploy/7/assistant")).toBe(true)
    expect(isAssistantPage("/databases/3/assistant")).toBe(true)
    expect(isAssistantPage("/databases/3/query")).toBe(false)
    expect(isAssistantPage("/deploy/7")).toBe(false)
  })
})
