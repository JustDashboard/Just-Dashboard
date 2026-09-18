import { expect, test } from "@playwright/test"
import { json, mockProject, now } from "./deploy-fixture"

/**
 * Notification channel editing. The shared fixture only wires up creation,
 * enable/pause, test-delivery and delete; the plain "edit and save" route
 * (`PUT /deploy/notifications/{id}`) is answered here per test, and each
 * test seeds its own channel by overriding the list read — the fixture's own
 * `notificationChannels` starts empty and there is no option to preload it.
 */

test("editing an e-mail channel to only rename it sends no empty to or smtpHost", async ({
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
    await json(route, { ...channel, name: "Ops email (renamed)" })
  })

  await page.goto("/deploy/notifications")
  await expect(page.getByText("Ops email", { exact: true })).toBeVisible()
  await page.getByRole("button", { name: "Actions for Ops email" }).click()
  await page.getByRole("menuitem", { name: "Edit channel" }).click()

  // The list read has no `config` at all, so a required field the operator
  // never re-typed must be said plainly rather than shown blank as if it
  // were optional.
  await expect(page.getByText("Enter the delivery settings again")).toBeVisible()

  await page.getByLabel("Name", { exact: true }).fill("Ops email (renamed)")
  await page.getByRole("button", { name: "Save channel" }).click()
  await expect(page.getByText("Notification channel saved")).toBeVisible()

  const body = writes.at(-1) as { config: Record<string, unknown> }
  expect(body.config.to).toBeUndefined()
  expect(body.config.smtpHost).toBeUndefined()
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
  // is omitted), so it stays behind a disclosure instead of showing blank.
  await expect(page.getByLabel("Discord webhook URL")).toHaveCount(0)
  await page.getByRole("button", { name: "Replace delivery settings" }).click()
  await expect(page.getByLabel("Discord webhook URL")).toBeVisible()
})
