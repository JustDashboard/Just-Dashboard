import { expect, test, type Locator, type Page } from "@playwright/test"
import { hold } from "./database-fixture"
import { mockFleet, refusal, type FleetMock } from "./database-fleet-fixture"

/**
 * Metrics export, opened from the control center: where the recorded database
 * activity is scraped from, the key that reads it and nothing else, and how
 * long the recorder keeps what it saved.
 *
 * The recorder's figures are believable ones for five servers: a reading is
 * about 1.5 KB and a week of them every 30 seconds is the bulk of what a
 * longer window adds to.
 */

const MB = 1_000_000
const SECRET = "vpsd_Qm3xk9TzWc1RvB7dLhYe4NfAoUs2GjP8"

function report(over: Record<string, unknown> = {}) {
  return {
    retentionDays: 7,
    options: [
      { days: 7, estimatedBytes: 171 * MB },
      { days: 14, estimatedBytes: 188 * MB },
      { days: 30, estimatedBytes: 227 * MB },
      { days: 90, estimatedBytes: 318 * MB },
    ],
    connections: 5,
    samples: 55_240,
    storedBytes: 93 * MB,
    everySeconds: 30,
    rawDays: 7,
    coarseMinutes: 5,
    endpoint: "/api/v1/metrics/databases",
    ...over,
  }
}

type ExportMock = FleetMock & {
  first?: Record<string, unknown>
  /** Held until released, then answered; for the loading state. */
  gate?: Promise<void>
  /** Failures to answer the first reads with, before one succeeds. */
  failures?: number
}

/** Mocks the fleet, then the three routes the dialog adds to it. */
async function mockExport(page: Page, options: ExportMock = {}) {
  const server = await mockFleet(page, options)
  let current = options.first ?? report()
  let failures = options.failures ?? 0
  const puts: Record<string, unknown>[] = []
  const keys: Record<string, unknown>[] = []
  await page.route("**/api/v1/databases/metrics-export", async (route) => {
    const request = route.request()
    if (request.method() === "PUT") {
      const body = request.postDataJSON() as { retentionDays: number }
      puts.push(body)
      current = { ...current, retentionDays: body.retentionDays }
      return route.fulfill({ json: current })
    }
    if (options.gate) await options.gate
    if (failures > 0) {
      failures -= 1
      const failed = refusal(503, "unavailable", "the history could not be read", true)
      return route.fulfill({ status: failed.status, json: failed.body })
    }
    return route.fulfill({ json: current })
  })
  await page.route("**/api/v1/tokens/", async (route) => {
    if (route.request().method() !== "POST") return route.fallback()
    keys.push(route.request().postDataJSON())
    return route.fulfill({
      status: 201,
      json: {
        secret: SECRET,
        token: { id: 9, name: "prometheus", scope: "metrics.read", role: "readonly" },
      },
    })
  })
  return { ...server, puts, keys }
}

const opener = (page: Page) => page.getByRole("button", { name: "Metrics export" })
const dialogOf = (page: Page) => page.getByRole("dialog", { name: "Metrics export" })
const retention = (dialog: Locator) => dialog.getByRole("radiogroup", { name: "Keep history for" })

async function open(page: Page) {
  await page.goto("/databases")
  await opener(page).click()
  const dialog = dialogOf(page)
  await expect(dialog.getByText("Metrics URL")).toBeVisible()
  return dialog
}

/** A dialog caught mid-animation shows the page through it. */
const settled = (dialog: Locator) =>
  dialog.evaluate((el) =>
    Promise.all(el.getAnimations({ subtree: true }).map((animation) => animation.finished)),
  )

test.describe("metrics export", () => {
  test("gives the endpoint, a key that reads only metrics and the scrape config to paste", async ({
    page,
  }) => {
    const server = await mockExport(page)
    const dialog = await open(page)
    const origin = new URL(page.url()).origin

    await expect(dialog).toContainText(`${origin}/api/v1/metrics/databases`)
    const config = dialog.locator("[data-slot=well]").filter({ hasText: "scrape_configs:" })
    await expect(config).toContainText("job_name: just-dashboard-databases")
    await expect(config).toContainText("metrics_path: /api/v1/metrics/databases")
    await expect(config).toContainText(`targets: ["${new URL(origin).host}"]`)
    await expect(config).toContainText("credentials: <metrics key>")
    await expect(dialog.getByText("Create a key above to fill in its value.")).toBeVisible()

    await dialog.getByRole("textbox").fill("grafana-cloud")
    await dialog.getByRole("button", { name: "Create metrics key" }).click()
    await expect(
      dialog.locator("[data-slot=well]").filter({ hasText: SECRET }).first(),
    ).toBeVisible()
    expect(server.keys).toEqual([{ name: "grafana-cloud", scope: "metrics.read", ttlDays: 0 }])
    await expect(config).toContainText(`credentials: ${SECRET}`)
    await expect(dialog.getByText("Create a key above to fill in its value.")).toHaveCount(0)
    await expect(dialog.getByText("stored hashed, so it cannot be shown again")).toBeVisible()
  })

  test("keeps history for the window chosen, and asks before a shorter one deletes some", async ({
    page,
  }) => {
    const server = await mockExport(page)
    const dialog = await open(page)
    const choose = retention(dialog)

    await expect(choose.getByRole("radio")).toHaveText(["7 days", "14 days", "30 days", "90 days"])
    await expect(choose.getByRole("radio", { name: "7 days" })).toHaveAttribute(
      "aria-checked",
      "true",
    )
    await expect(dialog).toContainText("Readings are kept whole for 7 days.")
    await expect(dialog).toContainText("Older ones are thinned to the last of every 5 minutes.")
    await expect(dialog).toContainText("About 93.0 MB in 55,240 saved readings so far.")
    await expect(dialog).toContainText("7 days for 5 servers comes to about 171.0 MB.")

    // A longer window deletes nothing, so it is taken without a question.
    await choose.getByRole("radio", { name: "90 days" }).click()
    await expect(choose.getByRole("radio", { name: "90 days" })).toHaveAttribute(
      "aria-checked",
      "true",
    )
    expect(server.puts).toEqual([{ retentionDays: 90 }])
    await expect(dialog).toContainText("90 days for 5 servers comes to about 318.0 MB.")

    // A shorter one names what it does and can be left alone.
    await choose.getByRole("radio", { name: "14 days" }).click()
    const confirm = page.getByRole("dialog", { name: "Keep 14 days of history" })
    await expect(confirm).toContainText("older than 14 days are deleted")
    await confirm.getByRole("button", { name: "Cancel" }).click()
    await expect(confirm).toHaveCount(0)
    expect(server.puts).toHaveLength(1)
    await expect(choose.getByRole("radio", { name: "90 days" })).toHaveAttribute(
      "aria-checked",
      "true",
    )

    await choose.getByRole("radio", { name: "14 days" }).click()
    await page.getByRole("button", { name: "Delete older than 14 days" }).click()
    await expect(choose.getByRole("radio", { name: "14 days" })).toHaveAttribute(
      "aria-checked",
      "true",
    )
    expect(server.puts).toEqual([{ retentionDays: 90 }, { retentionDays: 14 }])
  })

  test("says there is nothing to measure before the recorder has saved a reading", async ({
    page,
  }) => {
    await mockExport(page, {
      first: report({
        options: [7, 14, 30, 90].map((days) => ({ days, estimatedBytes: null })),
        connections: 0,
        samples: 0,
        storedBytes: null,
      }),
    })
    const dialog = await open(page)
    await expect(dialog).toContainText("Nothing is recorded yet.")
    await expect(dialog).toContainText(
      "An estimate of the space a window takes appears once readings are saved.",
    )
    await expect(dialog).not.toContainText("NaN")
  })

  test("waits in the shape it will have and offers to ask again after a failure", async ({
    page,
  }) => {
    const gate = hold()
    await mockExport(page, { gate: gate.until, failures: 1 })
    await page.goto("/databases")
    await opener(page).click()
    const dialog = dialogOf(page)
    await expect(dialog.getByRole("status", { name: "Loading metrics export" })).toBeVisible()

    gate.release()
    await expect(dialog.getByRole("alert")).toContainText("the history could not be read")
    await dialog.getByRole("button", { name: "Try again" }).click()
    await expect(dialog.getByText("Metrics URL")).toBeVisible()
    await expect(dialog.getByRole("alert")).toHaveCount(0)
  })

  test("a role that only reads sees the setup and cannot change what is kept", async ({ page }) => {
    const server = await mockExport(page, { viewer: true })
    const dialog = await open(page)
    await expect(dialog).toContainText("/api/v1/metrics/databases")
    await expect(dialog).toContainText("Only an administrator can change how long history is kept.")
    for (const radio of await retention(dialog).getByRole("radio").all()) {
      await expect(radio).toBeDisabled()
    }
    // The commands an administrator has on the page are not drawn for it.
    await dialog.getByRole("button", { name: "Done" }).click()
    await expect(page.getByRole("link", { name: "Add a database" })).toHaveCount(0)
    expect(server.puts).toEqual([])
  })

  test("fits a phone without scrolling sideways", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await mockExport(page)
    const dialog = await open(page)
    await settled(dialog)
    const box = await dialog.boundingBox()
    expect(box && box.x >= 0 && box.x + box.width <= 390).toBe(true)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    // The wide things inside it scroll in their own box, not the dialog's.
    const overflowing = await dialog.evaluate((el) => el.scrollWidth > el.clientWidth)
    expect(overflowing).toBe(false)
    await expect(retention(dialog).getByRole("radio", { name: "90 days" })).toBeVisible()
  })

  test("is reachable by keyboard from the page's commands to the choice", async ({ page }) => {
    await mockExport(page)
    await page.goto("/databases")
    await opener(page).focus()
    await page.keyboard.press("Enter")
    const dialog = dialogOf(page)
    await expect(dialog.getByText("Metrics URL")).toBeVisible()
    await retention(dialog).getByRole("radio", { name: "30 days" }).focus()
    await page.keyboard.press("Space")
    await expect(retention(dialog).getByRole("radio", { name: "30 days" })).toHaveAttribute(
      "aria-checked",
      "true",
    )
    await page.keyboard.press("Escape")
    await expect(dialog).toHaveCount(0)
    await expect(opener(page)).toBeFocused()
  })

  test("is drawn in each state it has @shots", async ({ page }) => {
    test.skip(!process.env.JD_SHOTS, "set JD_SHOTS to a directory to save screenshots")
    const shots = process.env.JD_SHOTS as string
    await page.setViewportSize({ width: 1440, height: 900 })
    await mockExport(page)
    await page.goto("/databases")
    await expect(opener(page)).toBeVisible()
    await page.screenshot({ path: `${shots}/control-center-1440.png` })

    await opener(page).click()
    const dialog = dialogOf(page)
    await expect(dialog.getByText("Metrics URL")).toBeVisible()
    await settled(dialog)
    await page.screenshot({ path: `${shots}/endpoint-1440.png` })

    await dialog.getByRole("button", { name: "Create metrics key" }).click()
    await expect(dialog.getByText("New key")).toBeVisible()
    await dialog.getByText("prometheus.yml").scrollIntoViewIfNeeded()
    await settled(dialog)
    await page.screenshot({ path: `${shots}/key-and-config-1440.png` })

    await retention(dialog).getByRole("radio", { name: "90 days" }).click()
    await expect(dialog).toContainText("90 days for 5 servers comes to about 318.0 MB.")
    await retention(dialog).scrollIntoViewIfNeeded()
    await page.screenshot({ path: `${shots}/retention-1440.png` })

    await retention(dialog).getByRole("radio", { name: "14 days" }).click()
    const confirm = page.getByRole("dialog", { name: "Keep 14 days of history" })
    await expect(confirm).toBeVisible()
    await settled(confirm)
    await page.screenshot({ path: `${shots}/confirm-shorter-1440.png` })
    await confirm.getByRole("button", { name: "Cancel" }).click()
    await expect(confirm).toHaveCount(0)

    await page.setViewportSize({ width: 390, height: 844 })
    await dialog.getByText("Metrics URL").scrollIntoViewIfNeeded()
    await settled(dialog)
    await page.screenshot({ path: `${shots}/endpoint-390.png` })
    await retention(dialog).scrollIntoViewIfNeeded()
    await page.screenshot({ path: `${shots}/retention-390.png` })

    // A role that only reads, on the same page with the choice drawn off.
    await page.setViewportSize({ width: 1440, height: 900 })
    await page.unrouteAll({ behavior: "ignoreErrors" })
    await mockExport(page, { viewer: true })
    await page.goto("/databases")
    await opener(page).click()
    const reader = dialogOf(page)
    await expect(reader.getByText("Metrics URL")).toBeVisible()
    await retention(reader).scrollIntoViewIfNeeded()
    await settled(reader)
    await page.screenshot({ path: `${shots}/read-only-1440.png` })
  })
})
