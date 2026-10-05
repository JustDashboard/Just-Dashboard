import { expect, test } from "@playwright/test"
import { deployment, json, mockProject, project } from "./deploy-fixture"
import { betBot } from "./workload-import-fixture"

test("an imported stack shows all services and original-manager controls without deployment execution", async ({
  page,
}) => {
  await mockProject(page)
  const posts: string[] = []
  page.on("request", (request) => {
    if (request.method() === "POST") posts.push(new URL(request.url()).pathname)
  })
  await page.route("**/api/v1/deploy/7", (route) =>
    json(route, {
      project: { ...project, profile: "imported", enabled: false, name: "bet-bot" },
      running: false,
      deployment: {
        ...deployment,
        profile: "imported",
        sourceKind: "import",
        importMode: "existing_stack",
        liveReleaseId: 0,
        lastRun: undefined,
        activeRun: undefined,
        pendingChanges: false,
        endpoint: "",
        importedWorkload: betBot,
        serviceCount: 4,
      },
      runtime: { status: "unavailable", services: [] },
    }),
  )
  await page.goto("/deploy/7")
  await expect(page.getByRole("heading", { name: "Original workload" })).toBeVisible()
  await expect(page.getByText("Partly running", { exact: true })).toBeVisible()
  await expect(page.getByText("running · unhealthy", { exact: true })).toBeVisible()
  await expect(page.getByText("exited", { exact: true })).toHaveCount(2)
  await expect(page.getByRole("link", { name: "Open Docker Compose" })).toHaveAttribute(
    "href",
    "/docker/stacks/bet-bot",
  )
  await expect(page.getByRole("button", { name: "Open original manager" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Deploy", exact: true })).toHaveCount(0)
  await expect(page.getByText("Not deployed yet", { exact: true })).toHaveCount(0)
  expect(posts.filter((path) => /\/check$|\/runs$/.test(path))).toEqual([])
})
