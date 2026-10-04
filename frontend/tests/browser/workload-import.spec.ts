import { expect, test } from "@playwright/test"
import { json } from "./deploy-fixture"
import {
  betBot,
  hostApp,
  mockWorkloadImport,
  recoveredWorkloadDraft,
} from "./workload-import-fixture"

test("import inspects the four-service stack, preserves stopped services and submits only reviewed identity", async ({
  page,
}) => {
  const fixture = await mockWorkloadImport(page)
  await page.goto("/deploy/import")
  await expect(page.locator("[data-slot=page]")).toHaveAttribute("data-register", "flow")
  await expect(page.getByText("Some sources could not be checked")).toBeVisible()
  await expect(page.getByText("PM2 is unavailable for this account.")).toBeVisible()
  await expect(page.getByRole("button", { name: "Review bet-bot" })).toBeVisible()
  await expect(page.getByText("2/4 running")).toBeVisible()
  await page.getByRole("button", { name: "Review bet-bot" }).focus()
  await page.keyboard.press("Enter")
  await expect(
    page.getByRole("heading", { name: "Recover this workload's settings?" }),
  ).toBeVisible()
  expect(fixture.inspections()).toBe(1)
  const services = page.getByRole("list", { name: "Services to import" })
  await expect(services.getByRole("listitem")).toHaveCount(4)
  await expect(services.getByText("exited", { exact: true })).toHaveCount(2)
  await expect(services.getByText("unhealthy", { exact: true })).toBeVisible()
  await expect(page.getByText("/srv/bet-bot/compose.yml")).toBeVisible()
  await expect(page.getByRole("link", { name: "Open original manager" })).toHaveAttribute(
    "href",
    "/docker/stacks/bet-bot",
  )
  await expect(
    page.getByText("Recovers a draft for review. The existing workload keeps running."),
  ).toBeVisible()
  await page.getByLabel("Project name").fill("bet-bot-production")
  await page.getByRole("button", { name: "Review migration" }).click()
  await expect(page).toHaveURL(/\/deploy\/new\?draft=recovered-workload-draft/)
  await expect(page.getByRole("heading", { name: "What does it need to run?" })).toBeVisible()
  await page.getByRole("button", { name: "Continue", exact: true }).click()
  await expect(page.getByRole("heading", { name: "Ready to adopt this deployment?" })).toBeVisible()
  expect(fixture.adoptions).toEqual([])
  expect(fixture.recoveries).toEqual([
    { key: betBot.key, name: "bet-bot-production", digest: betBot.digest },
  ])
  expect(fixture.calls.filter((call) => call.method === "POST").map((call) => call.path)).toContain(
    "/deploy/import/recover",
  )
  expect(fixture.calls.some((call) => /\/runs$|\/commit$/.test(call.path))).toBe(false)
})

test("discovery searches images and ports, filters managers, and reviews a host process without configuration", async ({
  page,
}) => {
  await mockWorkloadImport(page)
  await page.goto("/deploy/import")
  await page.getByRole("textbox", { name: "Search workloads" }).fill("postgres:16")
  await expect(page.getByRole("button", { name: "Review bet-bot" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Review next-server" })).toHaveCount(0)
  await page.getByRole("textbox", { name: "Search workloads" }).fill("3001")
  await expect(page.getByRole("button", { name: "Review next-server" })).toBeVisible()
  await page.getByRole("textbox", { name: "Search workloads" }).fill("")
  await page.getByRole("button", { name: /Processes 1/ }).click()
  await expect(page.getByRole("button", { name: "Review bet-bot" })).toHaveCount(0)
  await page.getByRole("button", { name: "Review next-server" }).click()
  await expect(page.getByText("Recovery needs review", { exact: true })).toBeVisible()
  await expect(page.getByText("Could not be recovered", { exact: true })).toBeVisible()
  await expect(page.getByText("[::]:3001/tcp", { exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: "Review migration" })).toBeEnabled()
})

test("a changed workload requires another review and sends the fresh digest only after an explicit retry", async ({
  page,
}) => {
  await mockWorkloadImport(page, [betBot])
  let attempts = 0
  let inspectCount = 0
  const submitted: Record<string, unknown>[] = []
  await page.route("**/api/v1/deploy/import/inspect", (route) => {
    inspectCount += 1
    return json(
      route,
      inspectCount === 1
        ? betBot
        : {
            ...betBot,
            digest: "fresh-digest",
            running: 3,
            warnings: ["The worker started after the first review."],
          },
    )
  })
  await page.route("**/api/v1/deploy/import/recover", (route) => {
    attempts += 1
    submitted.push(route.request().postDataJSON())
    return attempts === 1
      ? route.fulfill({
          status: 409,
          contentType: "application/json",
          body: JSON.stringify({
            error: {
              code: "workload_changed",
              message: "The workload changed since inspection. Inspect it again.",
            },
          }),
        })
      : json(route, recoveredWorkloadDraft({ ...betBot, digest: "fresh-digest" }, "my-bot"))
  })
  await page.goto("/deploy/import")
  await page.getByRole("button", { name: "Review bet-bot" }).click()
  await page.getByLabel("Project name").fill("my-bot")
  await page.getByRole("button", { name: "Review migration" }).click()
  await expect(page.getByRole("button", { name: "Review migration" })).toHaveCount(0)
  await expect(page.getByText("Inspect the workload again", { exact: true })).toBeVisible()
  await page.getByRole("button", { name: "Inspect again" }).click()
  await expect(page.getByText("The worker started after the first review.")).toBeVisible()
  await expect(page.getByLabel("Project name")).toHaveValue("my-bot")
  expect(attempts).toBe(1)
  await page.getByRole("button", { name: "Review migration" }).click()
  await expect(page).toHaveURL(/\/deploy\/new\?draft=recovered-workload-draft/)
  expect(submitted.map((body) => body.digest)).toEqual(["reviewed-digest", "fresh-digest"])
})

test("an unavailable workload reports the refusal and keeps discovery available", async ({
  page,
}) => {
  const fixture = await mockWorkloadImport(page)
  await page.route("**/api/v1/deploy/import/inspect", (route) =>
    route.fulfill({
      status: 404,
      contentType: "application/json",
      body: JSON.stringify({
        error: {
          code: "workload_not_found",
          message: "The workload no longer exists. Refresh discovery.",
        },
      }),
    }),
  )
  await page.goto("/deploy/import")
  await page.getByRole("button", { name: "Review bet-bot" }).click()
  await expect(
    page
      .getByRole("alert")
      .getByText("The workload no longer exists. Refresh discovery.", { exact: true }),
  ).toBeVisible()
  await expect(page.getByRole("button", { name: "Refresh" })).toBeEnabled()
  await expect(page.getByRole("button", { name: "Review next-server" })).toBeEnabled()
  expect(fixture.recoveries).toHaveLength(0)
})

test("a taken name remains an inline correction before the recovered draft opens", async ({
  page,
}) => {
  await mockWorkloadImport(page, [betBot])
  let attempts = 0
  await page.route("**/api/v1/deploy/import/recover", (route) => {
    attempts += 1
    return attempts === 1
      ? route.fulfill({
          status: 409,
          contentType: "application/json",
          body: JSON.stringify({
            error: {
              code: "name_taken",
              field: "name",
              message: "This project name is already in use.",
            },
          }),
        })
      : json(route, recoveredWorkloadDraft(betBot, "available-name"))
  })
  await page.goto("/deploy/import")
  await page.getByRole("button", { name: "Review bet-bot" }).click()
  await page.getByRole("button", { name: "Review migration" }).click()
  const inlineError = page.locator("[data-slot=flow-panel]").getByRole("alert")
  await expect(inlineError).toHaveText("This project name is already in use.")
  await expect(page.getByLabel("Project name")).toHaveAttribute("aria-invalid", "true")
  await page.getByLabel("Project name").fill("available-name")
  await expect(inlineError).toHaveCount(0)
  await page.getByRole("button", { name: "Review migration" }).click()
  await expect(page).toHaveURL(/\/deploy\/new\?draft=recovered-workload-draft/)
})

test("the review never renders unexpected environment values or command arguments", async ({
  page,
}) => {
  await mockWorkloadImport(page)
  await page.route("**/api/v1/deploy/import/inspect", (route) =>
    json(route, {
      ...hostApp,
      environment: { DATABASE_PASSWORD: "must-stay-secret" },
      command: "node app.js --token must-stay-secret",
      compose: "password: must-stay-secret",
    }),
  )
  await page.goto("/deploy/import")
  await page.getByRole("button", { name: "Review next-server" }).click()
  await expect(
    page.getByRole("heading", { name: "Recover this workload's settings?" }),
  ).toBeVisible()
  await expect(page.getByText("must-stay-secret", { exact: false })).toHaveCount(0)
  await expect(page.getByText("DATABASE_PASSWORD", { exact: false })).toHaveCount(0)
})

for (const width of [390, 1280, 1720]) {
  test(`discovery and review stay reachable at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    await mockWorkloadImport(
      page,
      Array.from({ length: 20 }, (_, index) => ({
        ...betBot,
        key: `stack:bot-${index}`,
        name: `bet-bot-${index}`,
      })),
    )
    await page.goto("/deploy/import")
    const shellOverflow = () =>
      page.locator("[data-slot=page]").evaluate((element) => {
        const shell = element.closest<HTMLElement>("[data-workspace-shell-scroll]")!
        return {
          across: shell.scrollWidth - shell.clientWidth,
          down: shell.scrollHeight - shell.clientHeight,
        }
      })
    if (width >= 1280) await expect.poll(shellOverflow).toEqual({ across: 0, down: 0 })
    else await expect.poll(async () => (await shellOverflow()).across).toBe(0)
    await page.getByRole("button", { name: "Review bet-bot-0", exact: true }).click()
    await expect(page.getByRole("button", { name: "Review migration" })).toBeVisible()
    if (width >= 1280) await expect.poll(shellOverflow).toEqual({ across: 0, down: 0 })
    else await expect.poll(async () => (await shellOverflow()).across).toBe(0)
    await expect(page.locator("[data-slot=flow-panel]")).toHaveCount(1)
  })
}

test("the fleet offers import when it is empty", async ({ page }) => {
  await mockWorkloadImport(page)
  await page.goto("/deploy")
  await expect(page.getByRole("link", { name: "Import existing" })).toHaveAttribute(
    "href",
    "/deploy/import",
  )
})
