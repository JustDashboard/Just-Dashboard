import { expect, test, type Page } from "@playwright/test"
import { json, mockProject, now } from "./deploy-fixture"

/**
 * The Log drains page (`/deploy/drains`): where deployments' output and
 * requests are copied to. Each test stubs `/deploy/log-drains` itself.
 */

const failing = {
  id: 4,
  name: "Grafana Cloud",
  kind: "otlp",
  endpoint: "https://otlp-gateway.example.test/otlp",
  enabled: true,
  streams: ["output", "requests"],
  projects: [],
  headerNames: ["Authorization"],
  createdAt: now,
  updatedAt: now,
  status: {
    shipped: 1240,
    dropped: 0,
    batches: 31,
    lastSuccessAt: new Date(Date.now() - 10 * 60_000).toISOString(),
    lastErrorAt: new Date(Date.now() - 60_000).toISOString(),
    lastError: "the destination refused the credentials (401): invalid token",
    queued: 86,
  },
}

const healthy = {
  ...failing,
  id: 5,
  name: "Local Loki",
  kind: "loki",
  endpoint: "http://127.0.0.1:3100",
  streams: ["requests"],
  projects: [7],
  headerNames: [],
  status: {
    shipped: 52,
    dropped: 0,
    batches: 4,
    lastSuccessAt: new Date(Date.now() - 5000).toISOString(),
    queued: 0,
  },
}

async function withDrains(page: Page, drains: unknown[]) {
  await mockProject(page)
  const writes: { method: string; url: string; body: unknown }[] = []
  await page.route("**/api/v1/deploy/log-drains**", async (route) => {
    const request = route.request()
    if (request.method() === "GET") return json(route, drains)
    writes.push({ method: request.method(), url: request.url(), body: request.postDataJSON() })
    if (request.url().endsWith("/test")) return json(route, { ok: true })
    return json(route, drains[0] ?? {})
  })
  return writes
}

test("an empty page offers the three formats, and a drain is added with its sealed header", async ({
  page,
}) => {
  const writes = await withDrains(page, [])
  await page.goto("/deploy/drains")
  await expect(page.getByRole("heading", { name: "Log drains", exact: true })).toBeVisible()
  await page.getByRole("button", { name: "Add OpenTelemetry" }).click()

  const sheet = page.getByRole("dialog", { name: "Add drain" })
  const save = sheet.getByRole("button", { name: "Add drain" })
  await expect(save).toBeDisabled()
  await sheet.getByLabel("Name", { exact: true }).fill("Grafana Cloud")
  await sheet.getByLabel("Endpoint", { exact: true }).fill("https://otlp-gateway.example.test/otlp")
  await sheet.getByRole("button", { name: "Add header" }).click()
  await expect(sheet.getByLabel("Header 1 name")).toHaveValue("Authorization")
  await sheet.getByLabel("Header 1 value").fill("Basic fixture-header-secret")
  await save.click()
  await expect.poll(() => writes.length).toBe(1)
  expect(writes[0].body).toEqual({
    name: "Grafana Cloud",
    kind: "otlp",
    endpoint: "https://otlp-gateway.example.test/otlp",
    enabled: true,
    streams: ["output", "requests"],
    projects: [],
    headers: [{ name: "Authorization", value: "Basic fixture-header-secret" }],
  })
  const storage = await page.evaluate(() =>
    JSON.stringify({ local: { ...localStorage }, session: { ...sessionStorage } }),
  )
  expect(storage).not.toContain("fixture-header-secret")
})

test("a failing drain says what the destination answered, and its verbs reach the server", async ({
  page,
}) => {
  const writes = await withDrains(page, [failing, healthy])
  await page.goto("/deploy/drains")
  const list = page.getByRole("list", { name: "Log drains" })
  const cloud = list.locator("[data-slot=choice-row]", { hasText: "Grafana Cloud" })
  await expect(
    cloud.getByText(
      /Failing since .*refused the credentials \(401\): invalid token · 86 records waiting/,
    ),
  ).toBeVisible()
  await expect(cloud.locator(".text-warning")).toHaveCount(1)
  await expect(cloud.getByText("output and requests of every project")).toBeVisible()
  const loki = list.locator("[data-slot=choice-row]", { hasText: "Local Loki" })
  await expect(loki.getByText(/52 records shipped · last delivery/)).toBeVisible()
  await expect(loki.getByText("requests of api-production")).toBeVisible()

  await cloud.getByRole("button", { name: "Send test" }).click()
  await expect(page.getByText("Grafana Cloud took the test record")).toBeVisible()
  await cloud.getByRole("button", { name: "Actions for Grafana Cloud" }).click()
  await page.getByRole("menuitem", { name: "Pause" }).click()
  await expect
    .poll(() => writes.map((write) => `${write.method} ${new URL(write.url).pathname}`))
    .toEqual(["POST /api/v1/deploy/log-drains/4/test", "PUT /api/v1/deploy/log-drains/4/enabled"])
  expect(writes[1].body).toEqual({ enabled: false })
})

test("an edit keeps the sealed headers unless they are replaced, and a header never goes over plain http", async ({
  page,
}) => {
  const writes = await withDrains(page, [failing])
  await page.goto("/deploy/drains")
  await page.getByRole("button", { name: "Edit Grafana Cloud" }).click()
  const sheet = page.getByRole("dialog", { name: "Edit drain" })
  await expect(sheet.getByText("Sends Authorization, sealed.")).toBeVisible()

  // Plain http to another machine with the saved header: refused before it
  // is asked.
  const endpoint = sheet.getByLabel("Endpoint", { exact: true })
  await endpoint.fill("http://collector.lan:4318")
  await expect(sheet.getByText(/this address would send it in the clear/)).toBeVisible()
  await expect(sheet.getByRole("button", { name: "Save drain" })).toBeDisabled()
  await endpoint.fill("https://otlp-gateway.example.test/otlp")

  // Narrow it to one project.
  await sheet.getByRole("switch", { name: "Every project" }).click()
  await sheet.getByRole("switch", { name: /api-production/ }).click()
  await sheet.getByRole("button", { name: "Save drain" }).click()
  await expect.poll(() => writes.length).toBe(1)
  const body = writes[0].body as Record<string, unknown>
  expect(body.headers).toBeUndefined()
  expect(body.projects).toEqual([7])
})

test("plain http without a header is allowed and says the records travel in the clear", async ({
  page,
}) => {
  await withDrains(page, [])
  await page.goto("/deploy/drains")
  await page.getByRole("button", { name: "Add JSON lines over HTTP" }).click()
  const sheet = page.getByRole("dialog", { name: "Add drain" })
  // The formats in the sheet are named cards, and pressing one switches.
  await sheet.getByRole("button", { name: /^Grafana Loki/ }).click()
  await expect(sheet.getByLabel("Endpoint", { exact: true })).toHaveAttribute(
    "placeholder",
    "http://loki:3100",
  )
  await sheet.getByRole("button", { name: /^JSON lines over HTTP/ }).click()
  await sheet.getByLabel("Name", { exact: true }).fill("Vector")
  await sheet.getByLabel("Endpoint", { exact: true }).fill("http://10.0.0.5:8080")
  await expect(sheet.getByText(/cross the network unencrypted/)).toBeVisible()
  await expect(sheet.getByRole("button", { name: "Add drain" })).toBeEnabled()
})
