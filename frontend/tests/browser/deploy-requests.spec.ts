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

  test("insights say which page is failing, which client is scanning, and where visitors came from", async ({
    page,
  }) => {
    await mockProject(page)
    await page.goto("/deploy/7/logs")
    await page.getByRole("button", { name: "Insights", exact: true }).click()

    // The failing path carries its own 5xx count and its own p95, so "p95 is
    // 412ms" becomes "p95 is 412ms because of /api/checkout".
    await expect(page.getByText("13 × 5xx · p95 2.84s")).toBeVisible()
    // A client that tried a dozen doors is named as a scanner, not as a visitor.
    await expect(page.getByText("scanner · 12 probes")).toBeVisible()
    await expect(page.getByText("Scanners", { exact: true })).toBeVisible()
    await expect(page.getByText("www.google.com")).toBeVisible()
    // Bots are a share, not a guess.
    await expect(page.getByText("12% bots")).toBeVisible()
    // An admin can block the scanner from the row.
    await expect(page.getByRole("button", { name: "Block" }).first()).toBeVisible()

    // Clicking a page narrows the rows to it. Two rows name this path — the
    // Pages list and the Slowest list — and either does.
    await page.getByRole("button", { name: "Show every request to /api/checkout" }).first().click()
    await expect(page.getByLabel("Filter requests by path")).toHaveValue("/api/checkout")
  })

  test("pages only hides what a page load drags in, and the export carries the same window", async ({
    page,
  }) => {
    await mockProject(page)
    const asked: URL[] = []
    await page.route("**/api/v1/deploy/7/requests*", async (route) => {
      asked.push(new URL(route.request().url()))
      await route.fallback()
    })
    await page.goto("/deploy/7/logs")
    await page.getByRole("button", { name: "Pages only", exact: true }).click()
    await expect.poll(() => asked.some((url) => url.searchParams.get("pages") === "true")).toBe(true)
    // The export link is the window's own query, so a download is what the
    // page shows and not the whole record.
    const href = await page.getByRole("link", { name: "Export" }).getAttribute("href")
    expect(href).toContain("/api/v1/deploy/7/requests/export?")
    expect(href).toContain("pages=true")
  })

  test("a failing request opens onto the container's output and events around that minute", async ({
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
    await page.goto("/deploy/7/logs")
    await page.getByText("/api/checkout").first().click()

    // The container's lines are one press away, on the host Logs page, for the
    // minute either side — the one time anybody wants them.
    const output = page.getByRole("link", { name: "Container output around this moment" })
    await expect(output).toBeVisible()
    const href = await output.getAttribute("href")
    expect(href).toContain("/logs?source=docker%3Aabc123")
    expect(href).toContain("mode=search")
    expect(href).toContain("since=2026-09-03T11%3A58%3A31")

    await page.getByRole("button", { name: "Container events around this moment" }).click()
    await expect(page.getByRole("button", { name: "Events", exact: true })).toHaveAttribute("aria-pressed", "true")
    await expect(page.getByText("two minutes either side")).toBeVisible()
    await page.getByRole("button", { name: "Show everything" }).click()
    await expect(page.getByText("api-production-r20 exited with status 137")).toBeVisible()
  })

  test("the readings say whether anybody will be told, and the chart marks the release", async ({
    page,
  }) => {
    await mockProject(page)
    // A container exit inside the chart's window, so there is a mark to draw;
    // the fixture's own events sit at "now", outside a window fixed in the past.
    await page.route("**/api/v1/deploy/7/lifecycle*", (route) =>
      json(route, {
        status: "available",
        watching: true,
        events: [
          {
            time: "2026-09-03T11:52:00Z",
            type: "container",
            action: "die",
            name: "api-production-r20",
            exitCode: "137",
            message: "api-production-r20 exited with status 137",
            level: "error",
            source: "daemon",
            owner: { "environment-id": "12" },
          },
        ],
      }),
    )
    await page.goto("/deploy/7/logs")
    // A rule is firing: the line says so with its reading and how long.
    await expect(page.getByText("1 firing")).toBeVisible()
    await expect(page.getByText(/4\.2% failing · began/)).toBeVisible()
    await expect(page.getByRole("link", { name: "Alerts" })).toHaveAttribute(
      "href",
      "/deploy/7/settings/automation#alerts",
    )
    // Page views ride on the requests tile, and served bytes have their own.
    await expect(page.getByText("402 page views in the last hour")).toBeVisible()
    await expect(page.getByText("Served", { exact: true })).toBeVisible()
    // The chart is the house one — stacked by status family, with the live
    // release's activation drawn as a mark inside the window.
    await expect(page.getByText("Requests", { exact: true }).first()).toBeVisible()
    await expect(page.getByText("5xx server error").first()).toBeVisible()
    // The p95 chart lives on Insights, where the readings are, not over the rows.
    await expect(page.getByText("Slowest tenth", { exact: true })).toHaveCount(1)
    await page.getByRole("button", { name: "Insights", exact: true }).click()
    await expect(page.getByText("Slowest tenth", { exact: true })).toHaveCount(2)
    await expect.poll(() => page.locator(".recharts-reference-line").count()).toBeGreaterThan(0)
  })

  test("the output tab is gone", async ({ page }) => {
    await mockProject(page)
    await page.goto("/deploy/7/logs")
    await expect(page.getByRole("button", { name: "Output", exact: true })).toHaveCount(0)
    await expect(page.getByRole("button", { name: "Insights", exact: true })).toBeVisible()
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
    await expect(page.getByText("docker itself").first()).toBeVisible()

    // And the other half of that distinction, which the server can only make
    // by laying the event against the audit log. It was unreachable for as
    // long as the deployment feed did not correlate: every row said "docker
    // itself" or nothing, whoever had pressed the button.
    const trigger = page.getByRole("link", { name: "this dashboard" })
    await expect(trigger).toBeVisible()
    await expect(trigger).toHaveAttribute("href", "/audit?action=deploy.run")

    // The release is the reader's next move, so it is a link to the run that
    // put it there rather than a piece of text.
    await expect(page.getByRole("link", { name: "· release 20" }).first()).toHaveAttribute(
      "href",
      "/deploy/7/runs/84",
    )
  })

  test("the events feed can be searched, narrowed to a kind, and followed", async ({ page }) => {
    await mockProject(page)
    const sockets: string[] = []
    page.on("websocket", (socket) => sockets.push(socket.url()))
    await page.goto("/deploy/7/logs")
    await page.getByRole("button", { name: "Events", exact: true }).click()

    // A deployment owns more than its containers: a database network vanishing
    // under a running release is exactly this feed's business.
    await expect(page.getByText("deleted network jd-db-e12")).toBeVisible()
    await page.getByRole("button", { name: /^Containers/ }).click()
    await expect(page.getByText("deleted network jd-db-e12")).toHaveCount(0)
    await expect(page.getByText("api-production-r20 exited with status 137")).toBeVisible()
    await page.getByRole("button", { name: /^All/ }).click()

    await page.getByLabel("Filter container events").fill("exited")
    await expect(page.getByText("api-production-r20 exited with status 137")).toBeVisible()
    await expect(page.getByText("api-production-r20 started")).toHaveCount(0)

    await page.getByLabel("Filter container events").fill("nothing here")
    await expect(page.getByText("Nothing matches that filter")).toBeVisible()
    await page.getByLabel("Filter container events").fill("")

    // Followed rather than polled: a feed that learns about the restart ten
    // seconds after the chart beside it has drawn the 502s is not on the same
    // timeline as the chart.
    await expect
      .poll(() => sockets.some((url) => url.includes("/deploy/7/lifecycle/stream")))
      .toBe(true)
  })

  test("an empty feed says the record began when the dashboard did", async ({ page }) => {
    await mockProject(page)
    await page.route("**/api/v1/deploy/7/lifecycle*", (route) =>
      json(route, {
        status: "available",
        watching: true,
        // The buffer lives in the backend's memory, so this is a restart three
        // minutes ago rather than a deployment that has been steady all week.
        since: new Date(Date.now() - 3 * 60_000).toISOString(),
        events: [],
      }),
    )
    await page.goto("/deploy/7/logs")
    await page.getByRole("button", { name: "Events", exact: true }).click()

    await expect(page.getByText("Nothing has happened since the dashboard started")).toBeVisible()
    await expect(page.getByText(/not yet evidence that the deployment has been steady/)).toBeVisible()
  })

  test("the view and the moment it is scoped to survive a reload", async ({ page }) => {
    await mockProject(page)
    await page.goto("/deploy/7/logs")

    await page.getByRole("button", { name: "Events", exact: true }).click()
    await expect.poll(() => new URL(page.url()).searchParams.get("view")).toBe("events")

    await page.getByRole("button", { name: "Requests", exact: true }).click()
    await page.getByText("/api/checkout").first().click()
    await page.getByRole("button", { name: "Container events around this moment" }).click()
    const scoped = new URL(page.url())
    expect(scoped.searchParams.get("view")).toBe("events")
    expect(scoped.searchParams.get("moment")).toBeTruthy()

    // The point of putting it there: the link somebody pastes into a chat
    // lands on the same two minutes it was taken from.
    await page.goto(scoped.toString())
    await expect(page.getByRole("button", { name: "Events", exact: true })).toHaveAttribute(
      "aria-pressed",
      "true",
    )
    await expect(page.getByText("two minutes either side")).toBeVisible()
  })

  test("only an address worth blocking is offered the verb", async ({ page }) => {
    await mockProject(page)
    await page.goto("/deploy/7/logs")
    await page.getByRole("button", { name: "Insights", exact: true }).click()

    const row = (ip: string) =>
      page
        .locator("li")
        .filter({ has: page.getByRole("button", { name: `Show every request from ${ip}` }) })

    // A scanner is the reason the verb exists.
    await expect(row("203.0.113.55").getByRole("button", { name: "Block" })).toBeVisible()
    // 172.217 is Google. It differs from RFC 1918 space by one octet, and a
    // prefix test on "172." hid the verb for the whole of the public half.
    await expect(row("172.217.0.1").getByRole("button", { name: "Block" })).toBeVisible()
    // These two are inside the network the firewall stands at the edge of, so
    // a deny rule against them is a rule that does nothing.
    await expect(row("172.16.4.9").getByRole("button", { name: "Block" })).toHaveCount(0)
    await expect(row("127.0.0.1").getByRole("button", { name: "Block" })).toHaveCount(0)
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
      await page.getByRole("button", { name: "Insights", exact: true }).click()
      await expect(page.getByText("Pages", { exact: true })).toBeVisible()
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
      ).toBe(true)
      await page.screenshot({
        path: testInfo.outputPath(`deploy-insights-${width}.png`),
        fullPage: true,
      })
    }
    await page.goto("/deploy/7/settings/automation")
    await expect(page.getByText("Traffic alerts")).toBeVisible()
    await page.screenshot({ path: testInfo.outputPath("deploy-alerts-1280.png"), fullPage: true })
  })
})
