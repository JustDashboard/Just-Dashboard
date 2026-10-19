import { expect, test } from "bun:test"
import { dashboardResource, embeddable } from "./board-embeds"

test("a dashboard link names the thing it opens", () => {
  expect(dashboardResource("/")).toEqual({ kind: "server" })
  expect(dashboardResource("/deploy/4")).toEqual({ kind: "project", id: 4 })
  expect(dashboardResource("/databases/12")).toEqual({ kind: "database", id: 12 })
  expect(dashboardResource("/databases/12/tables")).toBeNull()
  expect(dashboardResource("https://example.com/deploy/4")).toBeNull()
  expect(dashboardResource(null)).toBeNull()
})

test("only players the page may frame, and dashboard cards, embed", () => {
  expect(embeddable("/deploy/4")).toBe(true)
  expect(embeddable("https://www.youtube.com/watch?v=abc")).toBe(true)
  expect(embeddable("https://youtu.be/abc")).toBe(true)
  expect(embeddable("https://vimeo.com/123")).toBe(true)
  expect(embeddable("https://www.figma.com/file/abc")).toBe(true)
  expect(embeddable("http://www.youtube.com/watch?v=abc")).toBe(false)
  expect(embeddable("https://twitter.com/x/status/1")).toBe(false)
  expect(embeddable("https://gist.github.com/a/b")).toBe(false)
  expect(embeddable("https://youtube.com.evil.test/")).toBe(false)
  expect(embeddable("/files")).toBe(false)
})
