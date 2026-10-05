import { expect, test } from "@playwright/test"
import { readFileSync, mkdirSync, writeFileSync } from "node:fs"
import { join } from "node:path"
import { execFileSync } from "node:child_process"
import { createHash } from "node:crypto"
import { bytes, percent } from "../../src/lib/format"

const readyPath = process.env.JD_IMPORT_NATIVE_READY
const selectedStack = readyPath
  ? JSON.parse(readFileSync(readyPath, "utf8")).stackName || "bet-bot"
  : "bet-bot"
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
    [
      "ps",
      "-a",
      "--filter",
      `label=com.docker.compose.project=${selectedStack}`,
      "--format",
      "{{.ID}}",
    ],
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

test("imports the real selected stack without changing its containers", async ({
  page,
  context,
}, testInfo) => {
  // Deleted-image recovery reads the live filesystem without pausing it.
  // This opt-in recording can include two large exports on a busy host.
  test.setTimeout(300000)
  const ready = JSON.parse(readFileSync(readyPath!, "utf8"))
  expect(ready.dockerLogTailRead).toBe(true)
  expect(ready.dockerPreflightAvailable).toBe(true)
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
  const excludedServices =
    selectedStack === "bet-bot" ? ["eurobet-doubles-tracker", "eurobet-high-market-tracker"] : []
  await page.setViewportSize({ width: 1280, height: 900 })
  await page.goto("/deploy")
  await expect(page.getByRole("link", { name: "Import existing" })).toBeVisible()
  await page.screenshot({ path: join(output, "native-before-deployments.png"), fullPage: true })
  await page.goto("/deploy/import")
  await page.getByRole("textbox", { name: "Search workloads" }).fill(selectedStack)
  await expect(page.getByRole("button", { name: `Review ${selectedStack}` })).toBeVisible({
    timeout: 30000,
  })
  await page.screenshot({ path: join(output, "native-discovery-1280.png"), fullPage: true })
  await page.getByRole("button", { name: `Review ${selectedStack}` }).click()
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
  await expect(page.getByRole("heading", { name: "Ready to adopt this deployment?" })).toBeVisible({
    timeout: 180000,
  })
  for (const name of excludedServices) {
    await expect(page.getByRole("list", { name: "Excluded Compose services" })).toContainText(name)
  }
  await page.screenshot({ path: join(output, "native-configuration-1280.png"), fullPage: true })
  await page.getByRole("button", { name: "Back", exact: true }).click()
  await expect(page.getByRole("heading", { name: "What does it need to run?" })).toBeVisible()
  const inputPanel = page.locator("[data-slot=flow-panel]")
  await expect(inputPanel).toContainText("TELEGRAM_CHAT_ID")
  await expect(inputPanel).not.toContainText("JD_IMPORT_ENV_")
  await page.screenshot({ path: join(output, "native-original-inputs-1280.png"), fullPage: true })
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
  const overviewCheckResponse = page.waitForResponse(
    (response) =>
      /\/api\/v1\/deploy\/\d+\/environments\/\d+\/check$/.test(response.url()) &&
      response.request().method() === "POST",
    { timeout: 30000 },
  )
  // Record settled genuine values and settings rather than intermediate fades
  // or number springs. This changes presentation only, not returned evidence.
  await page.emulateMedia({ reducedMotion: "reduce" })
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
  const checked = await overviewCheckResponse
  expect(checked.ok()).toBe(true)
  expect(new URL(checked.url()).pathname).toMatch(
    new RegExp(`^/api/v1${projectPath}/environments/\\d+/check$`),
  )
  const overviewPreflight = await checked.json()
  const overviewFindingCodes = overviewPreflight.findings.map(
    (finding: { code: string }) => finding.code,
  )
  expect(overviewFindingCodes).toContain("docker_available")
  expect(overviewFindingCodes).toContain("compose_available")
  expect(overviewFindingCodes).not.toContain("docker_unavailable")
  expect(overviewFindingCodes).not.toContain("compose_unavailable")
  expect(overviewFindingCodes).not.toContain("runtime_unavailable")
  const runtimeReservation = overviewPreflight.findings.find(
    (finding: { fieldId?: string }) =>
      finding.fieldId === `dependencies.runtime.${recoveredDraft.data.adoption.resourceId}`,
  )
  expect(runtimeReservation).toMatchObject({ code: "dependency_available", severity: "pass" })
  await expect(page.getByText("Docker is unavailable", { exact: true })).not.toBeVisible()
  await expect(page.locator('#before-you-deploy button[aria-busy="true"]')).toHaveCount(0)
  await page.screenshot({
    path: join(output, "native-project-1280.png"),
    fullPage: true,
    animations: "disabled",
  })
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
  const operationsResponse = await page.request.get(
    new URL(`/api/v1${projectPath}/operations`, page.url()).toString(),
  )
  expect(operationsResponse.ok()).toBe(true)
  const operations = await operationsResponse.json()
  expect(operations.storage.status).toBe("available")
  if (selectedStack.startsWith("jd-import-proof-")) {
    expect(operations.storage.mounts).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ service: "worker", target: "/fixture-data", kind: "volume" }),
        expect.objectContaining({ service: "web", target: "/www", kind: "bind", readOnly: true }),
        expect.objectContaining({ service: "web", target: "/data", kind: "volume" }),
        expect.objectContaining({ service: "web", target: "/config", kind: "volume" }),
      ]),
    )
  }
  const selectedService =
    runtime.services.find(
      (service: { state: string; liveRelease: boolean }) =>
        service.state === "running" && service.liveRelease,
    ) ?? runtime.services.find((service: { state: string }) => service.state === "running")
  expect(selectedService).toBeTruthy()
  const runtimeSocketPromise = page.waitForEvent("websocket", {
    predicate: (socket) =>
      new URL(page.url()).pathname === `${projectPath}/runtime` &&
      new URL(socket.url()).pathname.endsWith(
        `/docker/containers/${selectedService.containerId}/stats/stream`,
      ),
    timeout: 30000,
  })
  await runtimeLink.click()
  const runtimeSocket = await runtimeSocketPromise
  let runtimeReading: { cpuReady: boolean; cpuPercent: number; memUsage: number } | undefined
  runtimeSocket.on("framereceived", ({ payload }) => {
    try {
      const message = JSON.parse(String(payload))
      if (
        message.type === "stats" &&
        message.data?.id === selectedService.containerId &&
        Number.isFinite(message.data.cpuPercent) &&
        Number.isFinite(message.data.memUsage) &&
        message.data.memUsage > 0
      )
        runtimeReading = message.data
    } catch {
      // A control frame is not a resource reading.
    }
  })
  for (const service of runtime.services as { name: string }[]) {
    await expect(page.getByText(service.name, { exact: true }).first()).toBeVisible({
      timeout: 30000,
    })
  }
  const usage = page.locator('[data-slot="panel"]').filter({
    has: page.getByRole("heading", { name: "Usage", exact: true }),
  })
  if (selectedStack.startsWith("jd-import-proof-")) {
    const storageList = page.getByRole("list", { name: "Persistent storage" })
    await expect(storageList).toContainText("/fixture-data")
    await expect(storageList).toContainText("worker")
    await expect(storageList).toContainText("/www")
    await expect(storageList).toContainText("read-only")
  }
  await expect(usage).toBeVisible()
  await usage.scrollIntoViewIfNeeded()
  await expect
    .poll(
      async () => {
        const reading = runtimeReading
        if (!reading?.cpuReady) return false
        return (
          (await usage.getByText(/^CPU now:/).textContent())?.startsWith(
            `CPU now: ${percent(reading.cpuPercent)}`,
          ) &&
          (await usage.getByText(/^Memory now:/).textContent())?.startsWith(
            `Memory now: ${bytes(reading.memUsage)}`,
          )
        )
      },
      { timeout: 30000 },
    )
    .toBe(true)
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
  await expect(page.getByRole("link", { name: selectedStack, exact: true }).first()).toBeVisible()
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
        existingStack: selectedStack,
        isolatedProjectUrl: projectUrl,
        existingContainerCount: before.length,
        configurationsAndStartTimesUnchanged: true,
        processIdentityAndStateUnchanged: true,
        liveCpuAndMemoryReadingsVerified: true,
        perServiceLiveStorageVerified: true,
        persistentMounts: operations.storage.mounts.map(
          (mount: { service?: string; target: string; kind: string; readOnly?: boolean }) => ({
            service: mount.service,
            target: mount.target,
            kind: mount.kind,
            readOnly: Boolean(mount.readOnly),
          }),
        ),
        selectedRuntimeReadingVerified: selectedService.containerId,
        dockerLogTailReadVerified: ready.dockerLogTailRead,
        overviewDockerPreflightVerified: true,
        runtimeReservationAvailabilityVerified: true,
        noRuntimeActionsInvoked: true,
        before,
        after,
      },
      null,
      2,
    ),
  )
})
