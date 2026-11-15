import { expect, test } from "bun:test"
import { exampleTarget, pathToolHref } from "./tool-links"

test("an old investigator address keeps every parameter on the tool", () => {
  expect(
    pathToolHref(
      new URLSearchParams("target=127.0.0.1&port=6432&protocol=tcp&family=inet&measure=1"),
    ),
  ).toBe("/network/tools?tool=path&target=127.0.0.1&port=6432&protocol=tcp&family=inet&measure=1")
  expect(pathToolHref({ container: "postgres" })).toBe(
    "/network/tools?tool=path&container=postgres",
  )
  expect(pathToolHref({})).toBe("/network/tools?tool=path")
})

test("the old address cannot choose another tool, and odd values travel encoded", () => {
  expect(pathToolHref({ tool: "ping", target: "::1", port: undefined })).toBe(
    "/network/tools?tool=path&target=%3A%3A1",
  )
  expect(pathToolHref({ target: ["a.test", "b.test"] })).toBe(
    "/network/tools?tool=path&target=a.test&target=b.test",
  )
})

test("an empty pane offers the first of the placeholder's examples", () => {
  expect(exampleTarget("example.com or 203.0.113.9")).toBe("example.com")
  expect(exampleTarget("eno1")).toBe("eno1")
  expect(exampleTarget(undefined)).toBeUndefined()
  expect(exampleTarget("  ")).toBeUndefined()
})
