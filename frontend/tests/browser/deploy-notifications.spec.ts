import { expect, test } from "@playwright/test"
import { json, mockProject, now } from "./deploy-fixture"

/**
 * Notification channel editing. The shared fixture only wires up creation,
 * enable/pause, test-delivery and delete; the plain "edit and save" route
 * (`PUT /deploy/notifications/{id}`) is answered here per test, and each
 * test seeds its own channel by overriding the list read — the fixture's own
 * `notificationChannels` starts empty and there is no option to preload it.
 */

test("editing an e-mail channel waits for its host, sender and recipients, then sends them", async ({
  page,
}) => {
  await mockProject(page)
  const channel = {
    id: 90,
    name: "Ops email",
    kind: "email",
    url: "",
    target: "ops@example.test",
    events: ["run.succeeded", "run.failed"],
    enabled: true,
    createdAt: now,
    updatedAt: now,
  }
  await page.route("**/api/v1/deploy/notifications", async (route) => {
    if (route.request().method() !== "GET") return route.fallback()
    await json(route, [channel])
  })
  const writes: Record<string, unknown>[] = []
  await page.route("**/api/v1/deploy/notifications/90", async (route) => {
    if (route.request().method() !== "PUT") return route.fallback()
    writes.push(route.request().postDataJSON() as Record<string, unknown>)
    await json(route, { ...channel, name: "Ops email renamed" })
  })

  await page.goto("/deploy/notifications")
  await expect(page.getByText("Ops email", { exact: true })).toBeVisible()
  await page.getByRole("button", { name: "Actions for Ops email" }).click()
  await page.getByRole("menuitem", { name: "Edit channel" }).click()

  // The list read has no `config` at all, and the server keeps only the
  // password when it is blank — the host, the sender and the recipients are
  // required on every write, so the form says so and the button waits.
  await expect(page.getByText("Enter the delivery settings again")).toBeVisible()
  await page.getByLabel("Name", { exact: true }).fill("Ops email renamed")
  const save = page.getByRole("button", { name: "Save channel" })
  await expect(save).toBeDisabled()
  await page.locator("#notification-smtp-host").fill("smtp.example.test")
  await page.locator("#notification-from").fill("Deploys <deploys@example.test>")
  await expect(save).toBeDisabled()
  await page.locator("#notification-to").fill("ops@example.test")
  await expect(save).toBeEnabled()
  await save.click()
  await expect(page.getByText("Notification channel saved")).toBeVisible()

  const body = writes.at(-1) as { config: Record<string, unknown> }
  expect(body.config.smtpHost).toBe("smtp.example.test")
  expect(body.config.from).toBe("Deploys <deploys@example.test>")
  expect(body.config.to).toEqual(["ops@example.test"])
  // A blank password keeps the stored one, so none is sent.
  expect(body.config.smtpPassword).toBeUndefined()
})

test("editing a Discord channel keeps its webhook hidden until Replace delivery settings is opened", async ({
  page,
}) => {
  await mockProject(page)
  const channel = {
    id: 91,
    name: "Release pings",
    kind: "discord",
    url: "https://discord.com/api/webhooks/123456/••••",
    target: "https://discord.com/api/webhooks/123456/••••",
    events: [],
    enabled: true,
    createdAt: now,
    updatedAt: now,
  }
  await page.route("**/api/v1/deploy/notifications", async (route) => {
    if (route.request().method() !== "GET") return route.fallback()
    await json(route, [channel])
  })

  await page.goto("/deploy/notifications")
  await page.getByRole("button", { name: "Actions for Release pings" }).click()
  await page.getByRole("menuitem", { name: "Edit channel" }).click()

  // Blank is safe here (the update route keeps the stored URL when the field
  // is omitted), so the stored address is shown as a reading, masked, and
  // nothing takes input until it is replaced.
  const dialog = page.getByRole("dialog")
  await expect(dialog.getByText("https://discord.com/api/webhooks/123456/••••")).toBeVisible()
  await expect(page.getByLabel("Discord webhook URL")).toHaveCount(0)
  await page.getByRole("button", { name: "Replace delivery settings" }).click()
  await expect(page.getByLabel("Discord webhook URL")).toBeVisible()
  // A Slack address in the Discord field is named for what it is.
  await page.getByLabel("Discord webhook URL").fill("https://hooks.slack.com/services/T0/B0/x")
  await expect(dialog.getByText(/That is a Slack address/)).toBeVisible()
  await page.getByLabel("Discord webhook URL").fill("https://discord.com/api/webhooks/1/abc")
  await expect(dialog.getByText("Discord webhook", { exact: true })).toBeVisible()
})

/**
 * The readings, the cards and the channel's sheet all answer one question —
 * are the messages arriving — from the list's own last delivery and strip and
 * from each channel's log.
 */
test("each channel says how its last message went, and its sheet reads every attempt", async ({
  page,
}) => {
  await mockProject(page)
  const at = (hours: number) => new Date(Date.now() - hours * 3_600_000).toISOString()
  const failing = {
    id: 92,
    name: "Status hook",
    kind: "webhook",
    url: "https://n8n.acme.test/webhook/deploys",
    target: "https://n8n.acme.test/webhook/deploys",
    events: ["run.started", "run.failed"],
    enabled: true,
    createdAt: now,
    updatedAt: now,
    lastDelivery: {
      status: "failed",
      event: "run.failed",
      responseClass: "5xx",
      createdAt: at(2),
    },
    recent: [
      { status: "delivered", createdAt: at(5), test: false },
      { status: "failed", createdAt: at(2), test: false },
    ],
  }
  const paused = {
    id: 93,
    name: "Release pings",
    kind: "slack",
    url: "https://hooks.slack.com/services/T0/B0/••••",
    target: "https://hooks.slack.com/services/T0/B0/••••",
    events: ["run.succeeded"],
    enabled: false,
    createdAt: now,
    updatedAt: now,
  }
  await page.route("**/api/v1/deploy/notifications", async (route) => {
    if (route.request().method() !== "GET") return route.fallback()
    await json(route, [paused, failing])
  })
  await page.route("**/api/v1/deploy/notifications/*/deliveries**", async (route) => {
    const id = Number(new URL(route.request().url()).pathname.split("/").at(-2))
    const delivery = (extra: Record<string, unknown>) => ({
      channelId: id,
      attempt: 1,
      responseClass: "2xx",
      createdAt: at(1),
      ...extra,
    })
    await json(
      route,
      id === 92
        ? [
            delivery({
              id: 5,
              event: "run.started",
              runId: 41,
              status: "pending",
              responseClass: "",
            }),
            delivery({
              id: 4,
              event: "run.failed",
              runId: 40,
              status: "failed",
              attempt: 2,
              responseClass: "5xx",
              createdAt: at(2),
            }),
            delivery({
              id: 3,
              event: "run.succeeded",
              runId: 39,
              status: "failed",
              responseClass: "network",
              nextAttemptAt: new Date(Date.now() + 10 * 60_000).toISOString(),
            }),
            delivery({ id: 2, event: "test", status: "delivered", createdAt: at(5) }),
          ]
        : [],
    )
  })

  await page.goto("/deploy/notifications")
  const tiles = page.locator("[data-slot=stat-tile]")
  await expect(tiles).toHaveCount(4)
  await expect(tiles.nth(0)).toContainText("1 paused")
  await expect(tiles.nth(1)).toContainText("1")
  // One message given up on in the last day, one still being retried.
  await expect(tiles.nth(2).locator(".text-destructive")).toHaveText("1")
  await expect(tiles.nth(2)).toContainText("Status hook · server error")
  await expect(tiles.nth(2)).toContainText("1 retrying")
  await expect(tiles.nth(3)).toContainText("Run failed to Status hook")

  // The picture is a picture, not a second list of the same channels.
  await expect(page.getByRole("group", { name: "Where deployment events go" })).toBeVisible()
  await expect(page.getByRole("listitem").filter({ hasText: "Status hook" })).toHaveCount(1)

  const cards = page.getByRole("list", { name: "Notification channels" }).getByRole("listitem")
  // The channel that gave up on its last message comes first.
  await expect(cards.first()).toContainText("Status hook")
  const hook = cards.filter({ hasText: "Status hook" })
  await expect(hook.getByText(/^failed 2h/)).toBeVisible()
  await expect(hook.getByRole("img", { name: "Last 2 deliveries: 1 failed" })).toBeVisible()
  await expect(hook).toContainText("Started, Failed")
  // A paused channel refuses every delivery, so the test is not offered.
  const pings = cards.filter({ hasText: "Release pings" })
  await expect(pings.getByText("Paused", { exact: true })).toBeVisible()
  await expect(pings.getByRole("button", { name: "Send test", exact: true })).toBeDisabled()

  await page.getByRole("button", { name: "Open Status hook" }).click()
  const sheet = page.getByRole("dialog", { name: "Deliveries · Status hook" })
  // One given up on; the one with a retry ahead is counted as retrying.
  await expect(sheet.getByText("1 failed", { exact: false })).toBeVisible()
  await expect(sheet.getByText("1 retrying", { exact: false })).toBeVisible()
  // A pending attempt is sending, not failed; one with a retry scheduled says so.
  await expect(sheet.getByText("Sending", { exact: true })).toBeVisible()
  await expect(sheet.getByText(/^retrying in/)).toBeVisible()
  await expect(sheet.getByText("Failed", { exact: true })).toHaveCount(1)
  await expect(sheet.getByText("Delivered", { exact: true })).toHaveCount(1)
  await expect(sheet.getByText(/Run failed · run 40/)).toBeVisible()
  await expect(sheet.getByText(/attempt 2 · server error/)).toBeVisible()
  await expect(sheet.getByText("Test message", { exact: true })).toBeVisible()
})
