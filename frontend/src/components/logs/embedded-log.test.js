import { describe, expect, test } from "bun:test"
import { logsPageHref, sourceSummary } from "./embedded-log"

describe("logsPageHref", () => {
  test("opens the Logs page on the source", () => {
    expect(logsPageHref("file:/var/log/ufw.log")).toBe("/logs?source=file%3A%2Fvar%2Flog%2Fufw.log")
  })

  test("carries the narrowing the section was asked for", () => {
    const href = logsPageHref("journal:nginx.service", {
      q: "upstream",
      levels: ["error", "warn"],
      fields: { event: ["block"], client: ["203.0.113.9"] },
    })
    const params = new URL(href, "http://x").searchParams
    expect(params.get("source")).toBe("journal:nginx.service")
    expect(params.get("q")).toBe("upstream")
    expect(params.get("levels")).toBe("error,warn")
    expect(params.getAll("f")).toEqual(["client:203.0.113.9", "event:block"])
  })
})

describe("sourceSummary", () => {
  test("a measured file says its size and its last write", () => {
    const summary = sourceSummary({
      label: "ufw.log",
      kind: "system",
      path: "/var/log/ufw.log",
      size: 2048,
      modified: new Date().toISOString(),
    })
    expect(summary).toMatch(/^system log · 2(\.0)? KB · written /)
  })

  test("an unmeasured one says where it lives, or what it is", () => {
    expect(
      sourceSummary({ label: "error.log", kind: "nginx", path: "/var/log/nginx/error.log" }),
    ).toBe("web server · /var/log/nginx/error.log")
    expect(sourceSummary({ label: "kernel", kind: "kernel", detail: "The kernel's ring" })).toBe(
      "kernel ring · The kernel's ring",
    )
  })
})
