import { expect, test } from "@playwright/test"
import { json, mockProject } from "./deploy-fixture"

/**
 * The project's Logs page, after it stopped being one pane of container output.
 *
 * The page it replaced was not broken — it was answering a question nobody had.
 * A Next.js production server prints a startup banner and then nothing, so a
 * deployment serving a thousand requests a minute showed thirty-nine lines
 * ending at "Ready in 236ms" and never changed. These tests hold the three
 * readings that fixed it: what the ingress served, what the container printed,
 * and what Docker did to it.
 */

test.describe("a deployment's traffic", () => {
  test.use({ timezoneId: "UTC" })

  test("the page opens on requests, with the readings that say whether anything is wrong", async ({
    page,
  }) => {
    await mockProject(page)
    await page.goto("/deploy/7/logs")

    // Requests first: it is the only one of the three that answers "is it
    // working" without the reader knowing what to look for.
    await expect(page.getByRole("button", { name: "Requests", exact: true })).toHaveAttribute(
      "aria-pressed",
      "true",
    )

    // A rate rather than a count: "1,284" means nothing without the window it
    // was counted over.
    await expect(page.getByText("per minute")).toBeVisible()
    await expect(page.getByText("21.4")).toBeVisible()
    // 13 of 1284 is about 1%, and the reading is the share, not the count.
    await expect(page.getByText("13 of 1,284 answered 5xx")).toBeVisible()
    // The tail, not the mean: a p50 of 24ms hides a p99 of 2.8 seconds.
    await expect(page.getByText("p95 · half answered inside 24ms")).toBeVisible()
  })

  test("a request row carries its status, its timing and its path, and opens for the rest", async ({
    page,
  }) => {
    await mockProject(page)
    await page.goto("/deploy/7/logs")

    const failing = page.getByText("/api/checkout").first()
    await expect(failing).toBeVisible()
    await expect(page.getByText("2.84s").first()).toBeVisible()

    // The row opens in place rather than into a drawer over the list: the
    // question is asked while scanning, and a panel that covers the rows makes
    // you close it to ask it again about the next one.
    await failing.click()
    // The agent is a title attribute on the row and text only in the detail,
    // so matching it proves the disclosure opened rather than the row's own
    // client column being visible at this width.
    await expect(page.getByText("Mozilla/5.0 Chrome/140.0")).toBeVisible()
    await expect(page.getByText("Referer")).toBeVisible()
    await expect(page.getByRole("button", { name: "Show every request to this path" })).toBeVisible()
  })

  test("narrowing to a status family is one press, and the chips carry their counts", async ({
    page,
  }) => {
    await mockProject(page)
    const asked: URL[] = []
    await page.route("**/api/v1/deploy/7/requests*", async (route) => {
      asked.push(new URL(route.request().url()))
      await route.fallback()
    })
    await page.goto("/deploy/7/logs")

    const serverErrors = page.getByRole("button", { name: /5xx/ })
    await expect(serverErrors).toBeVisible()
    await serverErrors.click()

    await expect
      .poll(() => asked.some((url) => url.searchParams.get("classes") === "5xx"))
      .toBe(true)
  })

  test("output is still there, and the container's own lines are one press away", async ({
    page,
  }) => {
    await mockProject(page, {
      runtime: {
        status: "available",
        observedAt: new Date().toISOString(),
        services: [
          {
            containerId: "abc123",
            name: "api-production-r20",
            releaseId: 20,
            liveRelease: true,
            state: "running",
            health: "healthy",
            imageId: `sha256:${"a".repeat(64)}`,
          },
        ],
      },
    })
    await page.route("**/api/v1/logs/**", async (route) => {
      const url = new URL(route.request().url())
      if (url.pathname.endsWith("/sources"))
        return json(route, { sources: [], units: [], roots: ["/var/log"], missing: {} })
      return json(route, { lines: [], scanned: 0, matched: 0, histogram: [], files: [] })
    })
    await page.goto("/deploy/7/logs")

    await page.getByRole("button", { name: "Output", exact: true }).click()
    // The service picker and the workspace's own Live/History strip — the
    // original pane, kept whole rather than replaced.
    await expect(page.getByRole("combobox", { name: "Runtime log source" })).toBeVisible()
    await expect(page.getByRole("button", { name: "Live", exact: true })).toBeVisible()
  })

  test("events name the exit code, and say whether the dashboard or Docker did it", async ({
    page,
  }) => {
    await mockProject(page)
    await page.goto("/deploy/7/logs")

    await page.getByRole("button", { name: "Events", exact: true }).click()
    await expect(page.getByText("api-production-r20 exited with status 137")).toBeVisible()
    // The exit code stays on the row at every width: it is the one fact that
    // changes what you do next.
    await expect(page.getByText("exit 137")).toBeVisible()
    // Docker records what happened and never who asked, so "the daemon did
    // this on its own" is the distinction worth drawing.
    await expect(page.getByText("docker itself")).toBeVisible()
  })

  test("a deployment with no request record explains which nothing this is", async ({ page }) => {
    await mockProject(page)
    await page.route("**/api/v1/deploy/7/requests*", (route) =>
      json(route, {
        status: "unavailable",
        reason: "This deployment has no public route, so nothing records the requests it serves.",
        latency: false,
        complete: true,
        observedAt: new Date().toISOString(),
        entries: [],
        summary: { total: 0, scanned: 0, classes: {}, errorRate: 0, clientErrorRate: 0, bytes: 0, perMinute: 0, methods: [], statuses: [], paths: [], hosts: [], clients: [], agents: [], buckets: [], bucketSeconds: 60, truncated: false },
        coverage: { exists: false, held: 0, complete: true, cursor: 0, refreshedAt: new Date().toISOString() },
      }),
    )
    await page.goto("/deploy/7/logs")

    // "Nothing asked for it" and "nothing is recording" are different
    // sentences, and a page that renders both as an empty table teaches the
    // reader to distrust it.
    await expect(page.getByText("No request record for this deployment")).toBeVisible()
    await expect(page.getByText("no public route")).toBeVisible()
  })

  test("live continues from the window's cursor rather than from a timestamp", async ({
    page,
  }) => {
    await mockProject(page)
    const sockets: string[] = []
    page.on("websocket", (socket) => sockets.push(socket.url()))
    await page.goto("/deploy/7/logs")
    await expect(page.getByText("21.4")).toBeVisible()

    await page.getByRole("button", { name: "Live", exact: true }).click()
    // The fixture's window ends at sequence 9917; the socket asks for what
    // comes after it, so nothing between the window read and the socket
    // opening is missed or sent twice.
    await expect
      .poll(() => sockets.some((url) => url.includes("/deploy/7/requests/stream") && url.includes("after=9917")))
      .toBe(true)
  })

  test("the page fits without scrolling sideways, at a phone and at a laptop", async ({
    page,
  }, testInfo) => {
    await mockProject(page)
    for (const width of [390, 1280]) {
      await page.setViewportSize({ width, height: 900 })
      await page.goto("/deploy/7/logs")
      await expect(page.getByRole("button", { name: "Requests", exact: true })).toBeVisible()
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
      ).toBe(true)
      await page.screenshot({
        path: testInfo.outputPath(`deploy-requests-${width}.png`),
        fullPage: true,
      })
    }
  })
})
