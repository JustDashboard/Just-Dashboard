import { expect, test } from "@playwright/test"
import { iso, json, mockHost, now } from "./host-fixture"

/**
 * The host's Alerts page under Metrics: rules over the machine's own readings,
 * drawn as the proxy's Alerts page is. Served by the host fixture, with the
 * alert routes answered here.
 */

const slack = {
  id: 1,
  name: "#ops",
  kind: "slack",
  url: "",
  target: "hooks.slack.com/services/T0",
  events: [],
  enabled: true,
  createdAt: iso(now),
  updatedAt: iso(now),
}

test("an administrator reaches Alerts from Metrics and adds a disk and a container rule", async ({
  page,
}) => {
  await mockHost(page)
  const rules: Array<Record<string, unknown>> = []
  const created: Array<Record<string, unknown>> = []
  await page.route("**/api/v1/system/alerts", (route) =>
    json(route, { rules, subjects: [], history: [], intervalSeconds: 60, metrics: true }),
  )
  await page.route("**/api/v1/deploy/notifications", (route) => json(route, []))
  await page.route("**/api/v1/system/alerts/rules", async (route) => {
    const body = route.request().postDataJSON() as Record<string, unknown>
    created.push(body)
    const rule = { id: rules.length + 1, createdAt: iso(now), updatedAt: iso(now), ...body }
    rules.push(rule)
    await route.fulfill({
      status: 201,
      contentType: "application/json",
      body: JSON.stringify(rule),
    })
  })

  await page.goto("/metrics")
  await page.locator("[data-slot='host-identity']").getByRole("link", { name: "Alerts" }).click()
  await expect(page).toHaveURL(/\/metrics\/alerts$/)
  const sheet = page.getByRole("region", { name: "Rules" })
  await expect(sheet.getByText(/checked every minute/)).toBeVisible()

  await page.getByRole("button", { name: "Add rule" }).click()
  let form = page.getByRole("dialog", { name: "Add alert" })
  await form.getByRole("button", { name: /Disk filling up/ }).click()
  // A disk has a line and no minutes.
  await expect(form.getByLabel("Averaged over")).toHaveCount(0)
  await form.getByLabel("At or above").fill("101")
  await form.getByRole("button", { name: "Add alert" }).click()
  await expect(form.getByText("The line is a percentage between 10 and 99.")).toBeVisible()
  expect(created).toHaveLength(0)
  await form.getByLabel("At or above").fill("85")
  await form.getByRole("button", { name: "Add alert" }).click()
  await expect(sheet.getByText("Disk filling up", { exact: true })).toBeVisible()
  expect(created.at(-1)).toMatchObject({
    kind: "disk_filling",
    params: { threshold: 85 },
    channels: [],
  })

  await page.getByRole("button", { name: "Add rule" }).click()
  form = page.getByRole("dialog", { name: "Add alert" })
  await form.getByRole("button", { name: /Container down/ }).click()
  await expect(form.getByLabel("At or above")).toHaveCount(0)
  await form.getByLabel("Down for").fill("5")
  await form.getByRole("button", { name: "Add alert" }).click()
  await expect(sheet.getByText("Container down", { exact: true })).toBeVisible()
  expect(created.at(-1)).toMatchObject({ kind: "container_down", params: { minutes: 5 } })
})

test("the page wires what is watched to each channel, leads with what fires and says what it cannot judge", async ({
  page,
}) => {
  await mockHost(page)
  const tested: string[] = []
  const rules = [
    {
      id: 1,
      kind: "container_down",
      params: { minutes: 2 },
      channels: [],
      enabled: true,
      createdAt: iso(now),
      updatedAt: iso(now),
    },
    {
      id: 2,
      kind: "cpu_high",
      params: { threshold: 90, minutes: 10 },
      channels: [1],
      enabled: true,
      createdAt: iso(now),
      updatedAt: iso(now),
      unjudged: "Only 3 of the 10 minutes it averages are recorded so far.",
    },
  ]
  await page.route("**/api/v1/system/alerts", (route) =>
    json(route, {
      intervalSeconds: 60,
      metrics: true,
      lastPass: iso(now),
      rules,
      subjects: [
        {
          ruleId: 1,
          kind: "container_down",
          subject: "shop-api-1",
          label: "shop-api-1",
          detail: "It exited with code 1.",
          state: "firing",
          since: iso(now - 300_000),
          muted: false,
        },
      ],
      history: [
        {
          id: 1,
          ruleId: 1,
          kind: "container_down",
          subject: "shop-api-1",
          label: "shop-api-1",
          event: "host.alert.firing",
          detail: "It exited with code 1.",
          delivered: 1,
          failed: 0,
          muted: false,
          test: false,
          createdAt: iso(now - 240_000),
        },
      ],
    }),
  )
  await page.route("**/api/v1/deploy/notifications", (route) => json(route, [slack]))
  await page.route("**/api/v1/system/alerts/rules/*/test", async (route) => {
    tested.push(new URL(route.request().url()).pathname)
    await json(route, { delivered: 1, failed: 0 })
  })

  // Metrics counts what fires beside the way to it.
  await page.goto("/metrics")
  await expect(page.getByRole("link", { name: "Alerts, 1 firing" })).toBeVisible()

  await page.goto("/metrics/alerts")
  const watched = page.getByRole("group", { name: "What the rules watch" })
  await expect(watched).toContainText("1 firing")
  // Containers are drawn as Docker, the channel as its own service.
  await expect(watched.locator("img[src='/logos/docker.svg']")).toHaveCount(1)
  await expect(
    page.getByRole("group", { name: "Who is told" }).locator("img[src='/logos/slack.svg']"),
  ).toHaveCount(1)
  const firing = page.getByRole("region", { name: "Firing" })
  await expect(firing.getByText("It exited with code 1.")).toBeVisible()
  await expect(firing.getByRole("button", { name: "Mute shop-api-1" })).toBeVisible()
  await expect(page.getByRole("list", { name: "Alert history" })).toContainText("told 1 channel")

  // A rule that cannot be judged says so, rather than looking quiet.
  const rulesList = page.getByRole("list", { name: "Alert rules" })
  await expect(rulesList.getByText("Cannot judge")).toBeVisible()
  await expect(rulesList.getByText(/Only 3 of the 10 minutes/)).toBeVisible()

  // A rule's test goes to its channels from its menu.
  await page.getByRole("button", { name: /Actions for CPU high/ }).click()
  await page.getByRole("menuitem", { name: "Send test" }).click()
  await expect.poll(() => tested).toEqual(["/api/v1/system/alerts/rules/2/test"])

  // A rule is a row that opens its form.
  await page.getByRole("button", { name: "Edit Container down" }).click()
  const form = page.getByRole("dialog", { name: "Edit alert" })
  await expect(form.getByLabel("Down for")).toHaveValue("2")
})
