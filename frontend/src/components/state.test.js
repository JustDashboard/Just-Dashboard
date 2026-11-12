import { describe, expect, test } from "bun:test"
import { createElement } from "react"
import { renderToStaticMarkup } from "react-dom/server"
import { ApiError } from "@/lib/api"
import { failureReason } from "@/lib/toast"
import { ErrorState } from "./state"

// What a failure is drawn as, read from the markup of a first render. The
// pause a throttled read waits before it asks again needs a clock and a
// document, and is held in tests/browser/app-states.spec.ts.

const draw = (error, props = {}) =>
  renderToStaticMarkup(createElement(ErrorState, { error, ...props }))
const retry = () => {}

describe("ErrorState", () => {
  test("an item that is not there is said calmly, as a sentence", () => {
    const html = draw(new ApiError(404, "not_found", "not found"))
    expect(html).toContain('data-kind="missing"')
    expect(html).toContain(">Not found</p>")
    expect(html).not.toContain(">not found<")
    expect(html).toContain("It may have been removed or renamed since this link was made.")
    // Not the red box of something broken.
    expect(html).not.toContain("border-rule-danger")
    expect(html).not.toContain("text-destructive")
  })

  test("a server that asked for a pause offers another try and says it will ask by itself", () => {
    const error = new ApiError(429, "rate_limited", "too many requests, slow down", undefined, {
      retryAfterMs: 30_000,
    })
    const html = draw(error, { onRetry: retry })
    expect(html).toContain('data-kind="throttled"')
    expect(html).toContain(">Too many requests, slow down</p>")
    expect(html).toContain("This is asked again by itself in 32 seconds.")
    expect(html).toContain("Try again")
    expect(html).not.toContain("border-rule-danger")
    // With nothing to call there is no button, and no promise to ask again.
    const alone = draw(error)
    expect(alone).not.toContain("Try again")
    expect(alone).toContain("It answers again in a moment.")
  })

  test("any other message is the headline with its capital, in the box of a failure", () => {
    const html = draw(new ApiError(500, "internal", "database is locked"))
    expect(html).toContain('data-kind="failed"')
    expect(html).toContain(">Database is locked</p>")
    expect(html).toContain("border-rule-danger")
    // A tool's own words are not rewritten.
    expect(
      draw(new ApiError(422, "invalid_config", 'nginx: [emerg] unknown directive "foo"')),
    ).toContain("nginx: [emerg] unknown directive &quot;foo&quot;")
  })

  test("what was attempted is still the headline, with the reason as a sentence under it", () => {
    const html = draw(
      new ApiError(502, "docker_failed", "could not reach the daemon", undefined, {
        operation: "list",
        resource: "containers",
        reason: "the socket refused the connection",
      }),
    )
    expect(html).toContain(">Could not list containers</p>")
    expect(html).toContain("Could not reach the daemon.")
    expect(html).toContain("The socket refused the connection.")
  })

  test("a failure that is not the API's keeps the fallback headline", () => {
    const html = draw(new Error("Failed to fetch"))
    expect(html).toContain(">Something went wrong</p>")
    expect(html).toContain("Failed to fetch.")
  })

  test("a module this host does not have is still information, whatever its status", () => {
    const html = draw(new ApiError(404, "not_installed", "nginx is not installed"))
    expect(html).toContain('data-kind="unavailable"')
    expect(html).toContain("Not available on this host")
    expect(html).not.toContain("It may have been removed")
  })

  test("Try again is offered when the server or the caller says the read is worth repeating", () => {
    const failed = new ApiError(503, "unavailable", "upstream timed out", undefined, {
      retryable: true,
    })
    expect(draw(failed, { onRetry: retry })).toContain("Try again")
    expect(draw(new ApiError(500, "internal", "boom"), { onRetry: retry })).not.toContain(
      "Try again",
    )
    expect(draw(new Error("offline"), { onRetry: retry, retryable: true })).toContain("Try again")
  })
})

describe("a toast's reason", () => {
  test("is the error's message as a sentence, without the exception's class", () => {
    expect(failureReason(new ApiError(404, "not_found", "not found"))).toBe("Not found.")
    expect(failureReason(new ApiError(429, "rate_limited", "too many requests, slow down"))).toBe(
      "Too many requests, slow down.",
    )
    expect(failureReason(new Error("path is outside the permitted roots"))).toBe(
      "Path is outside the permitted roots.",
    )
    expect(failureReason("pg_dump: server version mismatch")).toBe(
      "pg_dump: server version mismatch.",
    )
  })
})
