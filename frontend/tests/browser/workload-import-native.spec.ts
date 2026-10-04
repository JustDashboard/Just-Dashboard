import { expect, test } from "@playwright/test"
import { readFileSync, mkdirSync, writeFileSync } from "node:fs"
import { join } from "node:path"
import { execFileSync } from "node:child_process"
import { createHash } from "node:crypto"

const readyPath = process.env.JD_IMPORT_NATIVE_READY
test.skip(!readyPath, "requires the isolated authenticated native discovery evidence server")
test.use({ video: { mode: "on", size: { width: 1280, height: 900 } } })

type InspectedContainer = {
  Id: string
  State: { StartedAt: string }
  RestartCount: number
  Config: unknown
  HostConfig: unknown
  Mounts: unknown
  NetworkSettings: { Networks: unknown }
}

function existingStackEvidence() {
  const ids = execFileSync(
    "docker",
    ["ps", "-a", "--filter", "label=com.docker.compose.project=bet-bot", "--format", "{{.ID}}"],
    { encoding: "utf8" },
  )
    .trim()
    .split("\n")
    .filter(Boolean)
    .sort()
  if (ids.length === 0) throw new Error("The opt-in proof requires the existing bet-bot stack")
  const inspected = JSON.parse(execFileSync("docker", ["inspect", ...ids], { encoding: "utf8" }))
  return inspected.map((container: InspectedContainer) => ({
    id: container.Id,
    startedAt: container.State.StartedAt,
    restartCount: container.RestartCount,
    configurationDigest: createHash("sha256")
      .update(
        JSON.stringify({
          config: container.Config,
          host: container.HostConfig,
          mounts: container.Mounts,
          networks: container.NetworkSettings.Networks,
        }),
      )
      .digest("hex"),
  }))
}

test("imports the real existing bet-bot stack without changing its containers", async ({
  page,
  context,
}, testInfo) => {
  test.setTimeout(90000)
  const ready = JSON.parse(readFileSync(readyPath!, "utf8"))
  const output = process.env.JD_IMPORT_NATIVE_EVIDENCE ?? testInfo.outputDir
  mkdirSync(output, { recursive: true })
  await context.addCookies([
    {
      name: ready.cookieName,
      value: ready.cookieValue,
      url: testInfo.project.use.baseURL as string,
    },
  ])
  const before = existingStackEvidence()
  await page.setViewportSize({ width: 1280, height: 900 })
  await page.goto("/deploy")
  await expect(page.getByRole("link", { name: "Import existing" })).toBeVisible()
  await page.screenshot({ path: join(output, "native-before-deployments.png"), fullPage: true })
  await page.goto("/deploy/import")
  await page.getByRole("textbox", { name: "Search workloads" }).fill("bet-bot")
  await expect(page.getByRole("button", { name: "Review bet-bot" })).toBeVisible({ timeout: 30000 })
  await page.screenshot({ path: join(output, "native-discovery-1280.png"), fullPage: true })
  await page.getByRole("button", { name: "Review bet-bot" }).click()
  await expect(page.getByRole("button", { name: "Review migration" })).toBeVisible({
    timeout: 30000,
  })
  await page.screenshot({ path: join(output, "native-review-1280.png"), fullPage: true })
  await page.getByRole("button", { name: "Review migration" }).click()
  await expect(page.getByRole("heading", { name: "What does it need to run?" })).toBeVisible({
    timeout: 60000,
  })
  await page.screenshot({ path: join(output, "native-configuration-1280.png"), fullPage: true })
  await page.getByRole("button", { name: "Continue", exact: true }).click()
  await expect(page.getByRole("heading", { name: "Ready to adopt this deployment?" })).toBeVisible({
    timeout: 30000,
  })
  await expect(page.getByRole("button", { name: "Acknowledge, then adopt" })).toBeEnabled({
    timeout: 30000,
  })
  await page.getByRole("button", { name: "Acknowledge, then adopt" }).click()
  for (const checkbox of await page.getByRole("checkbox").all()) {
    if (await checkbox.isEnabled()) await checkbox.check()
  }
  await page.setViewportSize({ width: 1720, height: 1000 })
  await page.screenshot({ path: join(output, "native-review-1720.png"), fullPage: true })
  await page.setViewportSize({ width: 1280, height: 900 })
  await page.screenshot({ path: join(output, "native-adoption-review-1280.png"), fullPage: true })
  await page.getByRole("button", { name: "Adopt deployment", exact: true }).click()
  await expect(page).toHaveURL(/\/deploy\/\d+$/, { timeout: 30000 })
  await expect(page.getByRole("link", { name: "Runtime", exact: true })).toBeVisible()
  await expect(page.getByRole("link", { name: "Settings", exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: "Deploy", exact: true })).toBeVisible()
  await page.screenshot({ path: join(output, "native-project-1280.png"), fullPage: true })
  const detail = await page.request.get(
    new URL(`/api/v1${new URL(page.url()).pathname}`, page.url()).toString(),
  )
  expect(detail.ok()).toBe(true)
  const payload = await detail.json()
  expect(payload.deployment.liveReleaseId).toBeGreaterThan(0)
  const runtimeResponse = await page.request.get(
    new URL(`/api/v1${new URL(page.url()).pathname}/runtime`, page.url()).toString(),
  )
  expect(runtimeResponse.ok()).toBe(true)
  const runtime = await runtimeResponse.json()
  expect(runtime.services).toHaveLength(before.length)
  await page.getByRole("link", { name: "Runtime", exact: true }).click()
  await expect(page.getByText("high-market-tracker", { exact: true }).first()).toBeVisible({
    timeout: 30000,
  })
  await page.screenshot({ path: join(output, "native-runtime-1280.png"), fullPage: true })
  await page.getByRole("link", { name: "Settings", exact: true }).click()
  await page.screenshot({ path: join(output, "native-settings-1280.png"), fullPage: true })
  const projectUrl = page.url()
  await page.goto("/deploy")
  await expect(page.getByRole("link", { name: /bet-bot/ }).first()).toBeVisible()
  await page.screenshot({ path: join(output, "native-after-deployments.png"), fullPage: true })
  const after = existingStackEvidence()
  expect(after).toEqual(before)
  writeFileSync(
    join(output, "native-continuity.json"),
    JSON.stringify(
      {
        originalManager: "Docker Compose",
        managedDeployment: true,
        liveReleaseId: payload.deployment.liveReleaseId,
        runtimeServices: runtime.services.length,
        existingStack: "bet-bot",
        isolatedProjectUrl: projectUrl,
        existingContainerCount: before.length,
        configurationsAndStartTimesUnchanged: true,
        before,
        after,
      },
      null,
      2,
    ),
  )
})
