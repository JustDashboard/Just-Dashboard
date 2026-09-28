import { expect, test, type Page } from "@playwright/test"
import { json, mockHost } from "./host-fixture"

/**
 * The logs console, coloured. The shapes themselves are unit-tested in
 * `src/lib/log-tokens.test.js`; this checks what a reader sees: the failure
 * and the status found without reading, a structured line drawn as its message
 * and fields, the line's own timestamp and this host's name not repeated
 * beside the time column, the sources drawn as their products — and all of it
 * one click away from the text exactly as it was written.
 */

const sources = {
  sources: [
    {
      id: "file:/var/log/auth.log",
      label: "auth.log",
      kind: "system",
      path: "/var/log/auth.log",
      size: 1024,
      rotated: false,
    },
    // A compose service, whose detail names its stack before its image.
    { id: "docker:api", label: "api", kind: "docker", status: "running", detail: "shop · redis:7" },
    { id: "file:/var/log/nginx/access.log", label: "access.log", kind: "nginx", rotated: false },
  ],
  units: [],
  roots: ["/var/log"],
  missing: {},
}

const ts = "2026-09-23T05:21:42.600655Z"

const LINES: Record<string, object[]> = {
  "file:/var/log/auth.log": [
    {
      text: `${ts} atlas sshd-session[3254114]: Failed password for root from 62.60.130.253 port 59026 ssh2`,
      timestamp: ts,
      level: "error",
    },
  ],
  "docker:api": [
    {
      text: '{"time":"2026-09-23T05:11:01Z","level":"INFO","msg":"audit","user":"wayy","status":204}',
      timestamp: "2026-09-23T05:11:01Z",
      level: "info",
      message: "audit",
      fields: { user: "wayy", status: "204" },
    },
  ],
  "file:/var/log/nginx/access.log": [
    {
      text: '10.0.0.12 - - [23/Sep/2026:05:20:04 +0000] "GET /api HTTP/1.1" 502 170 "-" "curl/8.5.0"',
      timestamp: "2026-09-23T05:20:04Z",
    },
  ],
}

async function mockLogs(page: Page) {
  await mockHost(page)
  await page.route("**/api/v1/logs/sources", (route) => json(route, sources))
  await page.route("**/api/v1/logs/retention**", (route) => json(route, {}))
  await page.routeWebSocket("**/api/v1/logs/stream**", (socket) => {
    const source = new URL(socket.url()).searchParams.get("source") ?? ""
    socket.send(JSON.stringify({ type: "logs", data: LINES[source] ?? [], ts: Date.now() }))
  })
}

test.beforeEach(async ({ page }) => {
  await mockLogs(page)
})

test("a failure, the address and the program are found without reading", async ({ page }) => {
  await page.goto("/logs?source=file:/var/log/auth.log")
  const failed = page.getByText("Failed", { exact: true })
  await expect(failed).toBeVisible()
  await expect(failed).toHaveClass(/text-destructive/)
  await expect(page.getByText("62.60.130.253", { exact: true })).toHaveClass(/tag-violet/)
  // An error row is washed, so it is found by scrolling rather than reading.
  await expect(page.locator(".bg-wash-danger").first()).toBeVisible()
  // The time is in its column and the host is this one: neither is drawn again.
  await expect(page.getByText(ts)).toHaveCount(0)
  await expect(page.getByText("atlas", { exact: true })).toHaveCount(0)
  await expect(page.getByText("sshd-session", { exact: true })).toBeVisible()
})

test("a status is read by its class", async ({ page }) => {
  await page.goto("/logs?source=file:/var/log/nginx/access.log")
  await expect(page.getByText("502", { exact: true })).toHaveClass(/text-destructive/)
  await expect(page.getByText("GET", { exact: true })).toBeVisible()
})

test("a structured line is its message and fields, and the raw line is one click away", async ({
  page,
}) => {
  await page.goto("/logs?source=docker:api")
  // The sentence, then its fields in the order that tells the most.
  await expect(page.getByText(/^audit\s+status=204 user=wayy$/)).toBeVisible()
  await expect(page.getByText("204", { exact: true })).toHaveClass(/text-success/)
  await expect(page.getByText('"msg"')).toHaveCount(0)

  await page.getByRole("button", { name: "Colour" }).click()
  await expect(page.getByText(/"msg":"audit"/)).toBeVisible()
  await page.getByRole("button", { name: "Colour" }).click()
  await expect(page.getByText('"msg"')).toHaveCount(0)
})

test("the sources are drawn as their products", async ({ page }) => {
  await page.goto("/logs?source=file:/var/log/auth.log")
  const rail = page.locator('[aria-label="Log sources"]')
  await expect(rail.locator('img[src="/logos/redis.svg"]')).toBeVisible()
  await expect(rail.locator('img[src="/logos/nginx.svg"]')).toBeVisible()
  await expect(rail.locator('img[src="/logos/ubuntu.svg"]')).toBeVisible()
})
