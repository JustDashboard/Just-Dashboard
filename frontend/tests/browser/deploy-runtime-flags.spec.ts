import { expect, test } from "@playwright/test"
import { expectSaved, mockProject, saveSettings } from "./deploy-fixture"

/**
 * The process flags (`deploy/runtime_flags.go`) on Runtime settings: the
 * user, a read-only root with its writable paths, ulimits and sysctls under
 * *Inside the container*, the GPUs under *Container access* — typed as
 * `docker run` writes them, saved with the runtime plan, and refused under
 * the field a line or the server's refusal is about.
 */

const configuration = "/environments/12/configuration"

test("the process flags are typed as docker run writes them and saved with the plan", async ({
  page,
}) => {
  await mockProject(page)
  await page.goto("/deploy/7/settings/runtime")
  const process = page.locator("#process")
  await expect(process.getByText("Inside the container", { exact: true })).toBeVisible()

  await process.getByLabel("Run as").fill("1000:1000")
  await process.getByRole("switch", { name: "Read-only root filesystem" }).click()
  await expect(process.getByLabel("Writable paths")).toHaveValue("/tmp")
  await process.getByLabel("Writable paths").fill("/tmp 128 MiB\n/app/cache")
  await process.getByText("Ulimits and kernel parameters").click()
  await process.getByLabel("Ulimits").fill("nofile=65536\nmemlock=unlimited")
  await process.getByLabel("Kernel parameters").fill("net.core.somaxconn=1024")
  await expect(process).toContainText(
    "runs as 1000:1000 · read-only root · 2 ulimits · 1 kernel parameter",
  )

  const access = page.locator("#access")
  await access.getByRole("radio", { name: "A number" }).click()
  await access.getByLabel("How many").fill("2")
  await access.getByRole("switch", { name: "Pull request previews get them too" }).click()
  await expect(access.getByText("2 GPUs", { exact: true })).toBeVisible()

  const saved = page.waitForRequest(
    (request) => request.method() === "PUT" && request.url().endsWith(configuration),
  )
  await saveSettings(page)
  const body = (await saved).postDataJSON() as { runtime: Record<string, unknown> }
  expect(body.runtime.user).toBe("1000:1000")
  expect(body.runtime.readOnlyRootfs).toBe(true)
  expect(body.runtime.tmpfs).toEqual([{ path: "/tmp", sizeMb: 128 }, { path: "/app/cache" }])
  expect(body.runtime.ulimits).toEqual([
    { name: "nofile", soft: 65536, hard: 65536 },
    { name: "memlock", soft: -1, hard: -1 },
  ])
  expect(body.runtime.sysctls).toEqual([{ name: "net.core.somaxconn", value: "1024" }])
  expect(body.runtime.gpus).toEqual({ mode: "count", count: 2, previews: true })
  await expectSaved(page)
  await expect(process.getByText("Unsaved changes")).toHaveCount(0)
  await expect(access.getByText("Unsaved changes")).toHaveCount(0)
})

test("a line the form cannot read is refused before a save, and the server's under its field", async ({
  page,
}) => {
  await mockProject(page)
  await page.route(`**/api/v1/deploy/7${configuration}`, async (route) => {
    if (route.request().method() !== "PUT") return route.fallback()
    await route.fulfill({
      status: 422,
      contentType: "application/json",
      body: JSON.stringify({
        error: {
          code: "invalid_plan",
          field: "runtime.sysctls[0]",
          message:
            "vm.swappiness is the server's, not the container's; a container may set net.*, fs.mqueue.* and kernel.msgmax, kernel.msgmnb, kernel.msgmni, kernel.sem, kernel.shmall, kernel.shmmax, kernel.shmmni, kernel.shm_rmid_forced",
        },
      }),
    })
  })
  await page.goto("/deploy/7/settings/runtime")
  const process = page.locator("#process")
  await process.getByText("Ulimits and kernel parameters").click()
  await process.getByLabel("Ulimits").fill("nofile 1024")
  let sent = false
  page.on("request", (request) => {
    if (request.method() === "PUT" && request.url().endsWith(configuration)) sent = true
  })
  await saveSettings(page)
  await expect(
    process.getByText(
      "“nofile 1024” is not a limit: write name=soft:hard, such as nofile=65536:65536.",
    ),
  ).toBeVisible()
  expect(sent).toBe(false)

  await process.getByLabel("Ulimits").fill("nofile=1024")
  await process.getByLabel("Kernel parameters").fill("vm.swappiness=10")
  await saveSettings(page)
  await expect(
    process.getByText(/vm\.swappiness is the server's, not the container's/),
  ).toBeVisible()
  await expect(process.getByLabel("Kernel parameters")).toHaveAttribute("aria-invalid", "true")
})
