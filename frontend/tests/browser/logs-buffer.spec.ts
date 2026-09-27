import { expect, test } from "@playwright/test"
import { json, mockHost } from "./host-fixture"

// Recording full DOM snapshots stalls this capacity fixture. Check the retained
// element directly; the smaller interaction fixtures in logs-ui retain tracing.
test.use({ trace: "off" })

test("rolling eviction retains the marked line and its DOM identity", async ({ page }) => {
  await mockHost(page)
  await page.route("**/api/v1/logs/sources", (route) =>
    json(route, {
      sources: [
        {
          id: "file:/var/log/auth.log",
          label: "auth.log",
          kind: "system",
          path: "/var/log/auth.log",
        },
      ],
      units: [],
      roots: ["/var/log"],
      missing: {},
    }),
  )
  await page.route("**/api/v1/logs/retention**", (route) => json(route, {}))
  let append: (lines: { text: string }[]) => void = () => {
    throw new Error("stream has not connected")
  }
  await page.routeWebSocket("**/api/v1/logs/stream**", (socket) => {
    append = (lines) => socket.send(JSON.stringify({ type: "logs", data: lines, ts: Date.now() }))
    append(
      Array.from({ length: 4000 }, (_, i) => ({ text: `arrival-${String(i).padStart(4, "0")}` })),
    )
  })
  await page.goto("/logs?source=file:/var/log/auth.log")
  const rows = page.locator('[class*="content-visibility:auto"]')
  await expect.poll(() => rows.count()).toBe(4000)
  const retained = rows.nth(3990)
  await retained.click()
  await retained.evaluate((element) => {
    ;(window as Window & { retainedLine?: Element }).retainedLine = element
  })
  append(Array.from({ length: 50 }, (_, i) => ({ text: `next-arrival-${i}` })))
  await expect(rows.last()).toContainText("next-arrival-49")
  await expect.poll(() => rows.count()).toBe(4000)
  const shifted = rows.nth(3940)
  await expect(shifted).toContainText("arrival-3990")
  await expect(shifted).toHaveClass(/\bbg-accent\b/)
  expect(
    await shifted.evaluate(
      (element) => element === (window as Window & { retainedLine?: Element }).retainedLine,
    ),
  ).toBe(true)
})
