import { expect, test, type Page } from "@playwright/test"
import { mockDockerHost } from "./docker-fixture"
import { mockDatabases } from "./database-fixture"
import { mockProject } from "./deploy-fixture"
import { mockShowcase } from "./proxy-fixtures"
import { N8N, mockContainerPage } from "./container-page-fixture"

const retired =
  /\/docker\/health|\/containers\/[^/]+\/(failure|anomalies)|\/security\/posture|\/databases\/[^/]+\/advisor|\/environments\/[^/]+\/check|\/certificates\/findings|\/proxy\/findings\/snoozes/

function advisoryRequests(page: Page) {
  const requests: string[] = []
  page.on("request", (request) => {
    if (retired.test(new URL(request.url()).pathname)) requests.push(request.url())
  })
  return requests
}

async function expectNoAttention(page: Page) {
  await expect(
    page.getByRole("heading", {
      name: /^(Attention|Attention needed|Needs attention|Action required|What to change|Before you deploy)$/,
    }),
  ).toHaveCount(0)
  await expect(
    page.getByRole("region", {
      name: /^(Attention|Attention needed|Needs attention|Action required|What to change)$/,
    }),
  ).toHaveCount(0)
}

for (const width of [390, 1280]) {
  test(`Docker pages keep inventory without advisory reads at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    await mockDockerHost(page)
    const requests = advisoryRequests(page)
    for (const route of ["/docker", "/docker/containers"]) {
      await page.goto(route)
      await expect(page.locator("[data-workspace-item]").first()).toBeVisible()
      await expectNoAttention(page)
    }
    await mockContainerPage(page)
    await page.goto(`/docker/containers/${N8N}`)
    await expect(page.getByRole("tab", { name: "Inspect", exact: true })).toBeVisible()
    await expectNoAttention(page)
    expect(requests).toEqual([])
  })

  test(`database Home keeps its work pages without an Advisor at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    await mockDatabases(page)
    const requests = advisoryRequests(page)
    await page.goto("/databases/1")
    await expect(page.getByRole("region", { name: "How it is used", exact: true })).toBeVisible()
    await expectNoAttention(page)
    await expect(page.getByRole("link", { name: "Advisor", exact: true })).toHaveCount(0)
    expect(requests).toEqual([])
  })

  test(`project Home shows the runtime without advisory checks at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    await mockProject(page, { normalized: true })
    const requests = advisoryRequests(page)
    await page.goto("/deploy/7")
    await expect(page.getByRole("heading", { name: "api-production", exact: true })).toBeVisible()
    await expectNoAttention(page)
    expect(requests).toEqual([])
  })

  test(`proxy overview keeps the engine and sites without advisory reads at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 })
    await mockShowcase(page)
    const requests = advisoryRequests(page)
    await page.goto("/proxy")
    await expect(page.locator("[data-slot='host-identity']")).toBeVisible()
    await expectNoAttention(page)
    expect(requests).toEqual([])
  })
}
