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
  State: { StartedAt: string; Pid: number; Running: boolean; Status: string }
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
    pid: container.State.Pid,
    running: container.State.Running,
    status: container.State.Status,
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
  // Deleted-image recovery reads the live filesystem without pausing it.
  // This opt-in recording can include two large exports on a busy host.
  test.setTimeout(300000)
  const ready = JSON.parse(readFileSync(readyPath!, "utf8"))
  expect(ready.dockerLogTailRead).toBe(true)
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
  const liveReadings = new Set<string>()
  page.on("websocket", (socket) => {
    if (!socket.url().endsWith("/stats/stream")) return
    socket.on("framereceived", ({ payload }) => {
      try {
        const message = JSON.parse(String(payload))
        if (
          message.type === "stats" &&
          message.data?.cpuReady === true &&
          Number.isFinite(message.data?.cpuPercent) &&
          Number.isFinite(message.data?.memUsage) &&
          message.data.memUsage > 0
        )
          liveReadings.add(message.data.id)
      } catch {
        // A control frame is not a resource reading.
      }
    })
  })
  const excludedServices = ["eurobet-doubles-tracker", "eurobet-high-market-tracker"]
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
  const existingScope = page.getByRole("button", { name: "Existing containers only", exact: true })
  await existingScope.click()
  await expect(existingScope).toHaveAttribute("aria-pressed", "true")
  await page.screenshot({ path: join(output, "native-review-1280.png"), fullPage: true })
  const recoveryResponse = page.waitForResponse(
    (response) =>
      response.url().endsWith("/deploy/import/recover") && response.request().method() === "POST",
    { timeout: 180000 },
  )
  await page.getByRole("button", { name: "Review migration" }).click()
  const recovered = await recoveryResponse
  expect(recovered.ok()).toBe(true)
  const recoveredDraft = await recovered.json()
  expect(recoveredDraft.data.adoption.scope).toBe("existing_services")
  expect(recoveredDraft.data.adoption.excludedServices).toEqual(excludedServices)
  await expect(page.getByRole("heading", { name: "What does it need to run?" })).toBeVisible({
    timeout: 180000,
  })
  for (const name of excludedServices) {
    await expect(page.getByRole("list", { name: "Excluded Compose services" })).toContainText(name)
  }
  await page.screenshot({ path: join(output, "native-configuration-1280.png"), fullPage: true })
  await page.getByRole("button", { name: "Continue", exact: true }).click()
  await expect(page.getByRole("heading", { name: "Ready to adopt this deployment?" })).toBeVisible({
    timeout: 30000,
  })
  for (const name of excludedServices) {
    await expect(page.getByRole("list", { name: "Excluded Compose services" })).toContainText(name)
  }
  await expect(page.getByRole("button", { name: "Acknowledge, then adopt" })).toBeEnabled({
    timeout: 30000,
  })
  await page.getByRole("button", { name: "Acknowledge, then adopt" }).click()
  const warnings = page.locator("#deployment-warning-acknowledgements")
  await expect(warnings.getByRole("checkbox").first()).toBeEnabled({ timeout: 30000 })
  for (const checkbox of await warnings.getByRole("checkbox").all()) {
    await expect(checkbox).toBeEnabled({ timeout: 30000 })
    await checkbox.check()
    await expect(checkbox).toBeChecked()
  }
  await expect(page.getByRole("button", { name: "Adopt deployment", exact: true })).toBeEnabled({
    timeout: 30000,
  })
  await page.setViewportSize({ width: 1720, height: 1000 })
  await page.screenshot({ path: join(output, "native-review-1720.png"), fullPage: true })
  await page.setViewportSize({ width: 1280, height: 900 })
  await page.screenshot({ path: join(output, "native-adoption-review-1280.png"), fullPage: true })
  await page.getByRole("button", { name: "Adopt deployment", exact: true }).click()
  await expect(page).toHaveURL(/\/deploy\/\d+$/, { timeout: 30000 })
  const projectUrl = page.url()
  const projectPath = new URL(projectUrl).pathname
  const sidebar = page.getByRole("navigation", { name: "Sidebar", exact: true })
  const runtimeLink = sidebar.locator(`a[href="${projectPath}/runtime"]`)
  const settingsLink = sidebar.locator(`a[href="${projectPath}/settings/general"]`)
  await expect(runtimeLink).toBeVisible()
  await expect(settingsLink).toBeVisible()
  await expect(page.getByRole("button", { name: "Redeploy", exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: "Deployment actions", exact: true })).toBeVisible()
  await page.screenshot({ path: join(output, "native-project-1280.png"), fullPage: true })
  const detail = await page.request.get(
    new URL(`/api/v1${new URL(page.url()).pathname}`, page.url()).toString(),
  )
  expect(detail.ok()).toBe(true)
  const payload = await detail.json()
  expect(payload.deployment.liveReleaseId).toBeGreaterThan(0)
  const runtime = payload.runtime
  expect(runtime.status).toBe("available")
  expect(runtime.services).toHaveLength(before.length)
  const managedContainerIds = runtime.services
    .map((service: { containerId: string }) => service.containerId)
    .sort()
  expect(managedContainerIds).toEqual(
    before.map((container: { id: string }) => container.id).sort(),
  )
  await runtimeLink.click()
  for (const service of runtime.services as { name: string }[]) {
    await expect(page.getByText(service.name, { exact: true }).first()).toBeVisible({
      timeout: 30000,
    })
  }
  await expect
    .poll(() => managedContainerIds.some((id: string) => liveReadings.has(id)), { timeout: 30000 })
    .toBe(true)
  const usage = page.locator('[data-slot="panel"]').filter({
    has: page.getByRole("heading", { name: "Resource usage", exact: true }),
  })
  await expect(usage.getByText("Connecting", { exact: true })).not.toBeVisible()
  await expect(usage.getByText("Waiting for Docker", { exact: true })).not.toBeVisible()
  await page.screenshot({
    path: join(output, "native-runtime-1280.png"),
    fullPage: true,
    animations: "disabled",
  })
  await settingsLink.click()
  for (const document of recoveredDraft.data.source.composeFiles as { path: string }[]) {
    await expect(
      page.getByRole("textbox", { name: `${document.path} content`, exact: true }),
    ).toBeEditable({
      timeout: 30000,
    })
  }
  await page.screenshot({
    path: join(output, "native-settings-1280.png"),
    fullPage: true,
    animations: "disabled",
  })
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
        recoveryScope: recoveredDraft.data.adoption.scope,
        excludedServices,
        originalContainersRemainLiveAuthority: true,
        managedContainerIds,
        existingStack: "bet-bot",
        isolatedProjectUrl: projectUrl,
        existingContainerCount: before.length,
        configurationsAndStartTimesUnchanged: true,
        processIdentityAndStateUnchanged: true,
        liveCpuAndMemoryReadingsVerified: true,
        dockerLogTailReadVerified: ready.dockerLogTailRead,
        noRuntimeActionsInvoked: true,
        before,
        after,
      },
      null,
      2,
    ),
  )
})
