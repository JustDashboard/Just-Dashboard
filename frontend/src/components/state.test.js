import { describe, expect, test } from "bun:test"
import { createElement } from "react"
import { renderToStaticMarkup } from "react-dom/server"
import { ApiError } from "@/lib/api"
import { failureReason } from "@/lib/toast"
import { readFileSync } from "node:fs"
import { join } from "node:path"
import { EmptyNote, EmptyState, ErrorState, NeedsAdmin, Notice, needsVerb } from "./state"
import { Hold, LoadingPanel, LoadingRows, Spinner, Sweep, SWEEP_STILL } from "./loading"
import { absentFromError } from "./missing-thing-model"

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

  test("a module this host does not have is the designed absence, whatever its status", () => {
    const html = draw(
      new ApiError(404, "not_installed", "nginx is not installed on this server.", undefined, {
        resource: "nginx",
      }),
    )
    expect(html).toContain('data-slot="missing-thing"')
    expect(html).toContain('data-size="block"')
    expect(html).toContain(">nginx is not installed</p>")
    expect(html).toContain('href="/packages?q=nginx"')
    expect(html).not.toContain("error-state")
    expect(html).not.toContain("Not available on this host")
    expect(html).not.toContain("It may have been removed")
    // Docker that did not answer is the Docker screen, with Try again after its step.
    const docker = draw(
      new ApiError(503, "docker_unavailable", "docker did not answer", undefined, {
        retryable: true,
      }),
      { onRetry: retry },
    )
    expect(docker).toContain(">Docker is not answering</p>")
    expect(docker).toContain('href="/docker"')
    expect(docker).toContain("Try again")
    expect(draw(new ApiError(503, "no_proxy", "the proxy is unavailable on this host"))).toContain(
      ">No proxy on this server</p>",
    )
  })

  test("a read names itself: its headline, the server's words under it, and always the retry", () => {
    const html = draw(new ApiError(502, "bad_gateway", "upstream closed the connection"), {
      what: "the busiest statements",
      onRetry: retry,
    })
    expect(html).toContain(">Could not read the busiest statements</p>")
    expect(html).toContain("Upstream closed the connection.")
    expect(html).toContain("Try again")
    expect(html).toContain('role="alert"')
    expect(html).toContain("border-rule-danger")
    // What the server said it was doing still wins the headline.
    const named = draw(
      new ApiError(502, "docker_failed", "timeout", undefined, {
        operation: "list",
        resource: "containers",
      }),
      { what: "the containers", onRetry: retry },
    )
    expect(named).toContain(">Could not list containers</p>")
    // A 404 stays calm under a read's name.
    const gone = draw(new ApiError(404, "not_found", "not found"), { what: "the dumps" })
    expect(gone).toContain(">Could not read the dumps</p>")
    expect(gone).toContain('data-kind="missing"')
  })

  test("every says how often the page asks by itself, beside the retry it goes with", () => {
    const html = draw(new Error("offline"), {
      onRetry: retry,
      retryable: true,
      every: "30 seconds",
    })
    expect(html).toContain("It is asked again by itself every 30 seconds.")
    // Without a retry on offer there is nothing for the sentence to sit beside.
    expect(draw(new ApiError(500, "internal", "boom"), { every: "30 seconds" })).not.toContain(
      "asked again by itself every",
    )
  })

  test("the root takes the caller's props", () => {
    const html = draw(new Error("x"), { id: "sessions-failed", "data-testid": "f" })
    expect(html).toContain('id="sessions-failed"')
    expect(html).toContain('data-slot="error-state"')
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
    expect(
      draw(new ApiError(503, "unavailable", "x", undefined, { retryable: true }), {
        onRetry: retry,
        retryable: false,
      }),
    ).not.toContain("Try again")
  })
})

describe("the absences a failed read names", () => {
  test("adds a program and a missing proxy to what missingFromError knows", () => {
    expect(
      absentFromError({ code: "not_installed", message: "git is not installed" }),
    ).toMatchObject({
      reason: "not-installed",
      title: "Git is not installed",
      steps: [{ href: "/packages" }],
    })
    expect(absentFromError({ code: "not_installed", resource: "systemd" })?.reason).toBe(
      "no-systemd",
    )
    expect(absentFromError({ code: "no_proxy" })).toMatchObject({
      reason: "no-proxy",
      steps: [{ kind: "link", label: "Open Proxy", href: "/proxy" }],
    })
    expect(absentFromError({ code: "internal" })).toBeUndefined()
    expect(absentFromError(undefined)).toBeUndefined()
  })
})

const html = (component, props = {}, ...children) =>
  renderToStaticMarkup(createElement(component, props, ...children))

describe("EmptyNote", () => {
  test("is one sentence, as a paragraph unless told otherwise", () => {
    const note = html(EmptyNote, {}, "No tables yet.")
    expect(note).toStartWith('<p data-slot="empty-note"')
    expect(note).toContain("py-6 text-body")
    expect(html(EmptyNote, { as: "li" }, "x")).toStartWith("<li ")
    expect(html(EmptyNote, { as: "div" }, "x")).toStartWith("<div ")
  })

  test("dense is the rail's size", () => {
    const note = html(EmptyNote, { dense: true }, "Nothing matches.")
    expect(note).toContain('data-dense=""')
    expect(note).toContain("py-3 text-hint")
    expect(note).not.toContain("py-6")
  })

  test("action is the way out, under the sentence", () => {
    const note = html(EmptyNote, { action: { label: "Clear the search", onClick: () => {} } }, "x")
    expect(note).toContain("<button")
    expect(note).toContain("Clear the search")
    expect(html(EmptyNote, {}, "x")).not.toContain("<button")
  })
})

describe("EmptyState", () => {
  test("draws its glyph bare, with no plate under it", () => {
    const empty = html(EmptyState, { title: "No channels" })
    expect(empty).not.toContain("size-10")
    expect(empty).not.toContain("bg-surface-header")
    expect(empty).toMatch(/<svg [^>]*class="size-5 shrink-0 text-muted-foreground"/)
    expect(html(EmptyState, { title: "x", id: "e" })).toContain('id="e"')
  })
})

describe("NeedsAdmin", () => {
  test("one phrase and the lock, under any subject", () => {
    const door = html(NeedsAdmin, { what: "Packet captures" })
    expect(door).toContain('data-slot="needs-admin"')
    expect(door).toContain(">Packet captures need an administrator</p>")
    expect(html(NeedsAdmin, { what: "VPN" })).toContain(">VPN needs an administrator</p>")
    expect(html(NeedsAdmin, { what: "x", why: "Only they read bytes." })).toContain(
      "Only they read bytes.",
    )
  })

  test("says need after a plural and needs after anything else", () => {
    expect(needsVerb("Server diagnostics")).toBe("need")
    expect(needsVerb("External checks")).toBe("need")
    expect(needsVerb("Socket history")).toBe("needs")
    expect(needsVerb("Access")).toBe("needs")
    expect(needsVerb("Status")).toBe("needs")
  })
})

describe("Notice", () => {
  test("is a live region only when asked, and takes the caller's props", () => {
    expect(html(Notice, { title: "x" })).not.toContain("role=")
    expect(html(Notice, { title: "x", live: "alert" })).toContain('role="alert"')
    const notice = html(Notice, { title: "x", tone: "danger", id: "n" })
    expect(notice).toContain('data-slot="notice"')
    expect(notice).toContain('data-tone="danger"')
    expect(notice).toContain('id="n"')
  })
})

describe("what a wait says", () => {
  test("a spinner alone is silent, and with a label says it once as a status", () => {
    expect(html(Spinner)).not.toContain("role=")
    const said = html(Spinner, { label: "Loading the containers" })
    expect(said).toContain('role="status"')
    expect(said).toContain('class="sr-only"')
    expect(said).toContain("Loading the containers")
  })

  test("a skeleton with a label is busy and says what it stands for", () => {
    for (const skeleton of [LoadingRows, LoadingPanel]) {
      expect(html(skeleton)).not.toContain("role=")
      const said = html(skeleton, { label: "Loading the accounts" })
      expect(said).toContain('aria-busy="true"')
      expect(said).toContain('role="status"')
      expect(said).toContain("Loading the accounts")
    }
  })
})

describe("Sweep", () => {
  test("is a named progressbar with a label, and decoration without", () => {
    const named = html(Sweep, { label: "Pulling" })
    expect(named).toContain('role="progressbar"')
    expect(named).toContain('aria-label="Pulling"')
    expect(named).not.toContain("aria-valuenow")
    expect(html(Sweep)).toContain('aria-hidden="true"')
    expect(html(Sweep)).toContain("bg-meter-track")
    expect(html(Sweep, { track: false })).not.toContain("bg-meter-track")
    expect(html(Sweep, { tone: "quiet" })).toContain("bg-foreground/20")
  })

  test("has one reduced-motion rule, for the system and for the product's pause", () => {
    const bar = html(Sweep)
    expect(bar).toContain("animate-sweep")
    expect(bar).toContain(SWEEP_STILL.replaceAll("&", "&amp;"))
    // The bar stops, fills the track, and takes the faint ink.
    for (const variant of ["motion-reduce:", "[:root[data-motion=paused]_&]:"]) {
      for (const rule of ["w-full", "animate-none", "bg-faint"]) {
        expect(SWEEP_STILL.split(" ")).toContain(`${variant}${rule}`)
      }
    }
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

// A page arrives from the top: what is written under a block that still waits
// for its first answer is not drawn until the answer lands, so a block that
// lands taller than its skeleton has nothing under it to push. The marker is
// what the stylesheet reads, and eight pages moved while they loaded for the
// want of it (the audit's UI-18).
const MARK = '<span data-hold="" hidden=""></span>'

test("a hold leaves its marker, and what stands meanwhile, until the read has answered", () => {
  expect(renderToStaticMarkup(createElement(Hold, { until: false }))).toBe(MARK)
  expect(renderToStaticMarkup(createElement(Hold, { until: false }, createElement("i")))).toBe(
    `<i></i>${MARK}`,
  )
  // Answered: neither the marker nor the skeleton it stood behind.
  expect(renderToStaticMarkup(createElement(Hold, { until: true }, createElement("i")))).toBe("")
})

test("a skeleton holds what is under it only where it is asked to", () => {
  for (const skeleton of [LoadingRows, LoadingPanel]) {
    expect(renderToStaticMarkup(createElement(skeleton, {}))).not.toContain("data-hold")
    const held = renderToStaticMarkup(createElement(skeleton, { hold: true }))
    // After the skeleton, so that the skeleton itself is drawn.
    expect(held.endsWith(MARK)).toBe(true)
  }
})

test("the stylesheet takes what follows a marker, and the page's blocks under it, off the page", () => {
  const css = readFileSync(join(import.meta.dir, "../app/globals.css"), "utf8")
  const rule = css.slice(css.indexOf("[data-hold] ~ *"))
  expect(rule.slice(0, rule.indexOf("}") + 1).replace(/\s+/g, " ")).toBe(
    '[data-hold] ~ *, [data-slot="page"] > :has([data-hold]) ~ * { display: none !important; }',
  )
})
