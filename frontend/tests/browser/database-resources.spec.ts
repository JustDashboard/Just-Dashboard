import { expect as baseExpect, test, type Page, type Route } from "@playwright/test"
import { mockDatabases, resourcesOf, SHOP, type DatabaseMock } from "./database-fixture"
import { mockFleet } from "./database-fleet-fixture"

/**
 * What a database's container may use, on its Settings page and when it is
 * made.
 *
 * The claims here are the page's to its reader. The limits are the
 * container's own, each beside what it came closest to in the last hour and
 * what the machine has, and the memory limit beside the engine's own buffer,
 * whose effect is said when a limit is set under it. A change is saved by the
 * page's one save bar and sends only what changed; lifting a limit replaces
 * the container, says so before Save, and is asked for with the container
 * named. A limit the machine cannot give, or one under what the server uses
 * now, is said before it is sent. A role that may only read is shown the
 * facts and offered nothing, and a container that is not the dashboard's to
 * change says whose it is. A new server can be limited from the start.
 */

const expect = baseExpect.configure({ timeout: 15_000 })
test.describe.configure({ timeout: 60_000 })

const MIB = 1024 * 1024
const NOW = "2026-10-01T09:00:00Z"

/** The server behind connection 1 runs in this container. */
const CONTAINER = {
  id: "c0ffee",
  name: "shop-db",
  image: "postgres:16-alpine",
  state: "running",
  status: "Up 3 days",
  memoryLimit: 1024 * MIB,
  cpuLimit: 1.5,
  restartPolicy: "unless-stopped",
}

/** An hour of the container's history: 412 MiB and a third of a core at the most. */
const HISTORY = {
  name: "shop-db",
  from: NOW,
  to: NOW,
  stepSeconds: 60,
  sampleIntervalSeconds: 15,
  retentionSeconds: 604_800,
  earliest: NOW,
  points: Array.from({ length: 60 }, (_, i) => ({
    ts: NOW,
    samples: 4,
    cpu: 12,
    cpuPeak: i === 30 ? 34 : 18,
    mem: 30,
    memPeak: 40,
    memBytes: 380 * MIB,
    memBytesPeak: i === 30 ? 412 * MIB : 390 * MIB,
    memLimit: 1024 * MIB,
    pids: 14,
    pidsPeak: 16,
  })),
}

type Options = DatabaseMock & {
  /** What `PUT /databases/1/resources` answers; the change it was asked for unless given. */
  change?: { status: number; body: unknown }
}

/**
 * The fixture's database server, with connection 1 in a container that has an
 * hour of history; `sent` is every change of limits asked for, with its body.
 */
async function mockResources(page: Page, options: Options = {}) {
  await mockDatabases(page, {
    ...options,
    summaries: { 1: { container: CONTAINER }, ...options.summaries },
  })
  const sent: unknown[] = []
  await page.route("**/api/v1/**", async (route: Route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    const method = route.request().method()
    if (method === "GET" && /^\/docker\/containers\/[^/]+\/stats\/history$/.test(path)) {
      return route.fulfill({ json: HISTORY })
    }
    if (method === "PUT" && path === "/databases/1/resources") {
      const body = route.request().postDataJSON() as { memoryMb?: number; cpus?: number }
      sent.push(body)
      if (options.change) {
        return route.fulfill({ status: options.change.status, json: options.change.body })
      }
      const lifted = body.memoryMb === 0 || body.cpus === 0
      return route.fulfill({
        json: {
          limits: {
            memoryBytes: (body.memoryMb ?? 1024) * MIB,
            cpus: body.cpus ?? 1.5,
          },
          replaced: lifted,
          warnings: [],
        },
      })
    }
    return route.fallback()
  })
  return { sent }
}

async function visit(page: Page, path = "/databases/1/settings/resources") {
  const listed = page.waitForResponse(
    (response) => new URL(response.url()).pathname === "/api/v1/databases/",
    { timeout: 45_000 },
  )
  await page.goto(path)
  await listed
}

const section = (page: Page) => page.locator("section#resources")
const form = (page: Page) => page.getByRole("form", { name: "Resource limits" })
const memory = (page: Page) => page.getByRole("spinbutton", { name: "Memory limit" })
const cpus = (page: Page) => page.getByRole("spinbutton", { name: "CPU limit" })
const saveBar = (page: Page) => page.getByRole("region", { name: "Save changes" })
const save = (page: Page) => saveBar(page).getByRole("button", { name: "Save", exact: true })

test("Resources is a page of a database's Settings: its limits beside the machine and the engine's buffer", async ({
  page,
}) => {
  await mockResources(page)
  await visit(page)
  await expect(page.getByRole("link", { name: "Resources", exact: true })).toHaveAttribute(
    "data-active",
    "true",
  )
  await expect(section(page)).toContainText("Up to 1 GB of memory and 1.5 cores")
  await expect(memory(page)).toHaveValue("1024")
  await expect(cpus(page)).toHaveValue("1.5")
  await expect(section(page)).toContainText("peak 412 MiB in the last hour")
  await expect(section(page)).toContainText("peak 0.34 cores in the last hour")
  await expect(section(page)).toContainText("this server has 16 GB")
  await expect(section(page)).toContainText("this server has 8 cores")
  await expect(section(page).locator("[data-slot=memory-buffer]")).toHaveText(
    "shared_buffers is 128 MB",
  )
  await expect(section(page)).toContainText("Docker applies a new limit to shop-db at once")
  // A clean page has nothing to save, and no button for it.
  await expect(saveBar(page)).toHaveCount(0)
})

test("a new limit is saved by the page's save bar and sends only what changed", async ({
  page,
}) => {
  const server = await mockResources(page)
  await visit(page)
  await memory(page).fill("2048")
  await expect(section(page)).toContainText("Unsaved changes")
  await expect(saveBar(page)).toContainText("1 unsaved change")
  await expect(saveBar(page)).toContainText("Resource limits · Applies immediately")
  await save(page).click()
  await expect.poll(() => server.sent).toEqual([{ memoryMb: 2048 }])
  // A change in place is not asked about.
  await expect(page.getByRole("dialog")).toHaveCount(0)
})

test("a memory limit under the engine's buffer says what it does there", async ({ page }) => {
  await mockResources(page, {
    resources: {
      1: resourcesOf(SHOP, { buffer: { setting: "shared_buffers", bytes: 2048 * MIB } }),
    },
  })
  await visit(page)
  await expect(section(page).locator("[data-slot=memory-buffer]")).toHaveText(
    "shared_buffers is 2 GB, more than this limit: the kernel stops the server once the engine fills it",
  )
  // Raised past it, the line is the fact alone.
  await memory(page).fill("4096")
  await expect(section(page).locator("[data-slot=memory-buffer]")).toHaveText(
    "shared_buffers is 2 GB",
  )
})

test("lifting a limit says it replaces the container, and is asked for with it named", async ({
  page,
}) => {
  const server = await mockResources(page)
  await visit(page)
  await memory(page).fill("0")
  await expect(section(page).locator("[data-slot=resources-replace]")).toContainText(
    "saving replaces shop-db with no memory limit",
  )
  await save(page).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog).toContainText("Replace shop-db")
  await expect(dialog).toContainText("Every open session is dropped")
  await expect(dialog).toContainText("Up to 1.5 cores, any amount of memory")
  // Cancelled, nothing is sent and the edit is still there to save.
  // The bar holds the edit while it is asked, rather than saying it is saving.
  await expect(saveBar(page)).toContainText("1 unsaved change")
  await dialog.getByRole("button", { name: "Cancel" }).click()
  await expect(dialog).toHaveCount(0)
  expect(server.sent).toEqual([])
  await expect(section(page)).toContainText("Unsaved changes")

  await save(page).click()
  await page.getByRole("dialog").getByRole("button", { name: "Replace and save" }).click()
  await expect.poll(() => server.sent).toEqual([{ memoryMb: 0 }])
})

test("a limit the machine cannot give, or one the server would die under, is said before it is sent", async ({
  page,
}) => {
  const server = await mockResources(page)
  await visit(page)
  await memory(page).fill("20000")
  await expect(form(page).getByRole("alert")).toContainText(
    "This server has 16 GB; a limit above that limits nothing.",
  )
  await expect(save(page)).toBeDisabled()
  await memory(page).fill("256")
  await expect(form(page).getByRole("alert")).toContainText(
    "It uses 412 MB now; a limit under that stops it at once.",
  )
  await memory(page).fill("1024")
  await cpus(page).fill("9")
  await expect(form(page).getByRole("alert")).toContainText("This server has 8 cores.")
  await expect(save(page)).toBeDisabled()
  expect(server.sent).toEqual([])
})

test("a refusal from the server stays under the form, and the edit is kept", async ({ page }) => {
  await mockResources(page, {
    change: {
      status: 400,
      body: {
        error: {
          code: "bad_request",
          message:
            "shop-db uses 980 MiB now, and a memory limit under that makes the kernel stop it at once",
        },
      },
    },
  })
  await visit(page)
  await memory(page).fill("768")
  await save(page).click()
  await expect(form(page)).toContainText("It was not saved. shop-db uses 980 MiB now")
  await expect(section(page)).toContainText("Not saved")
  await expect(memory(page)).toHaveValue("768")
})

test("a stopped container is not replaced to lift a limit", async ({ page }) => {
  // Stopped, the server is not dialled: its buffer is read when it runs.
  await mockResources(page, {
    resources: {
      1: resourcesOf(SHOP, { running: false, usedBytes: undefined, buffer: undefined }),
    },
  })
  await visit(page)
  await expect(section(page).locator("[data-slot=memory-buffer]")).toHaveText(
    "shared_buffers is read while it runs",
  )
  await cpus(page).fill("0")
  await expect(section(page).locator("[data-slot=resources-held]")).toContainText(
    "Not while it is stopped.",
  )
  await expect(save(page)).toBeDisabled()
})

test("a role that may only read is shown the limits and offered nothing", async ({ page }) => {
  await mockResources(page, { viewer: true })
  await visit(page)
  await expect(section(page)).toContainText("Up to 1 GB of memory and 1.5 cores")
  await expect(section(page)).toContainText("shared_buffers")
  await expect(section(page)).toContainText("What it may use is an administrator’s to change.")
  await expect(form(page)).toHaveCount(0)
  await expect(page.getByRole("spinbutton")).toHaveCount(0)
})

test("a container that is not the dashboard's to change says whose it is", async ({ page }) => {
  await mockResources(page, {
    resources: {
      1: resourcesOf(SHOP, {
        container: "stack-db-1",
        editable: false,
        refusal:
          "stack-db-1 belongs to the compose project stack, which puts back what its file says the next time it recreates the container. Set the limits in the compose file and redeploy the stack.",
      }),
    },
  })
  await visit(page)
  await expect(section(page).locator("[data-slot=resources-refusal]")).toContainText(
    "Not changed from here. stack-db-1 belongs to the compose project stack",
  )
  await expect(form(page)).toHaveCount(0)
})

test("the reading says it is being read, and one that failed can be asked again", async ({
  page,
}) => {
  let release = () => {}
  const held = new Promise<void>((resolve) => (release = resolve))
  let failing = true
  await mockResources(page)
  await page.route("**/api/v1/databases/1/resources", async (route) => {
    if (route.request().method() !== "GET") return route.fallback()
    await held
    if (failing) {
      return route.fulfill({
        status: 502,
        json: { error: { code: "docker_failed", message: "Docker did not answer" } },
      })
    }
    return route.fallback()
  })
  await visit(page)
  await expect(page.getByRole("status", { name: "Reading what it may use" })).toBeVisible()
  release()
  await expect(section(page)).toContainText("Could not read what it may use")
  failing = false
  await section(page).getByRole("button", { name: "Try again" }).click()
  await expect(memory(page)).toHaveValue("1024")
})

test("a file has no server, so Settings lists no Resources page", async ({ page }) => {
  await mockResources(page)
  await visit(page, "/databases/7/settings")
  await expect(page.getByRole("link", { name: "General", exact: true })).toBeVisible()
  await expect(page.getByRole("link", { name: "Resources", exact: true })).toHaveCount(0)
  await visit(page, "/databases/7/settings/resources")
  await expect(page.getByText("SQLite has no resources to limit")).toBeVisible()
})

test("Home's limits lead an administrator to where they are changed", async ({ page }) => {
  await mockResources(page)
  await visit(page, "/databases/1")
  // The rail lists the page too: the link meant is the one in Runs as.
  await expect(
    page.getByLabel("Runs as").getByRole("link", { name: "Change", exact: true }),
  ).toHaveAttribute("href", "/databases/1/settings/resources")
})

test("a new server can be limited from the start, with the template's limits filled in", async ({
  page,
}) => {
  const fleet = await mockFleet(page)
  await page.goto("/databases/new")
  await page.getByRole("button", { name: /^PostgreSQL/ }).click()
  const panel = page.locator("[data-slot=flow-panel]")
  const limit = panel.getByRole("switch", { name: "Limit the memory and processors it may use" })
  await expect(limit).not.toBeChecked()
  await expect(panel.getByRole("spinbutton", { name: "Memory limit" })).toHaveCount(0)
  await limit.click()
  await expect(panel.getByRole("spinbutton", { name: "Memory limit" })).toHaveValue("1024")
  await expect(panel.getByRole("spinbutton", { name: "CPU limit" })).toHaveValue("1")
  await expect(panel).toContainText("this server has 16 GB")
  await panel.getByRole("spinbutton", { name: "CPU limit" }).fill("12")
  await expect(panel.getByRole("alert")).toContainText("This server has 8 cores.")
  await expect(panel.getByRole("button", { name: "Create", exact: true })).toBeDisabled()
  await panel.getByRole("spinbutton", { name: "CPU limit" }).fill("1.5")
  await panel.getByRole("button", { name: "Create", exact: true }).click()
  await expect
    .poll(() => fleet.bodies("POST /databases/provision"))
    .toEqual([{ engine: "postgres", version: "16", exposure: "local", memoryMb: 1024, cpus: 1.5 }])
})

test("the Resources page has no sideways scroll at a phone's width", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockResources(page)
  await visit(page)
  await expect(memory(page)).toHaveValue("1024")
  await memory(page).fill("0")
  await expect(section(page).locator("[data-slot=resources-replace]")).toBeVisible()
  const sideways = await page.evaluate(
    () => document.documentElement.scrollWidth > window.innerWidth,
  )
  expect(sideways).toBe(false)
})

test("the Resources states, for the pull request @shots", async ({ page }) => {
  test.skip(!process.env.JD_SHOTS, "set JD_SHOTS to a directory to save the screenshots")
  const shot = (name: string) => page.screenshot({ path: `${process.env.JD_SHOTS}/${name}.png` })
  await page.setViewportSize({ width: 1440, height: 900 })

  await mockResources(page)
  await visit(page)
  await expect(section(page)).toContainText("peak 412 MiB in the last hour")
  await shot("resources-1440")

  await memory(page).fill("0")
  await expect(saveBar(page)).toContainText("1 unsaved change")
  await shot("resources-lift-1440")
  await save(page).click()
  await expect(page.getByRole("dialog")).toContainText("Replace shop-db")
  await shot("resources-confirm-1440")
  await page.getByRole("dialog").getByRole("button", { name: "Cancel" }).click()

  await page.setViewportSize({ width: 390, height: 844 })
  await visit(page)
  await expect(memory(page)).toHaveValue("1024")
  await shot("resources-390")

  await page.unrouteAll({ behavior: "ignoreErrors" })
  await page.setViewportSize({ width: 1440, height: 900 })
  await mockResources(page, {
    resources: {
      1: resourcesOf(SHOP, {
        buffer: { setting: "shared_buffers", bytes: 2048 * MIB },
        oomKilled: true,
      }),
    },
  })
  await visit(page)
  await expect(section(page).locator("[data-slot=memory-buffer]")).toContainText(
    "more than this limit",
  )
  await shot("resources-below-buffer-1440")

  await page.unrouteAll({ behavior: "ignoreErrors" })
  await mockResources(page, {
    viewer: true,
    resources: {
      1: resourcesOf(SHOP, {
        container: "stack-db-1",
        editable: false,
        refusal:
          "stack-db-1 belongs to the compose project stack, which puts back what its file says the next time it recreates the container. Set the limits in the compose file and redeploy the stack.",
      }),
    },
  })
  await visit(page)
  await expect(section(page).locator("[data-slot=resources-refusal]")).toBeVisible()
  await shot("resources-refusal-1440")

  await page.unrouteAll({ behavior: "ignoreErrors" })
  await mockFleet(page)
  await page.goto("/databases/new")
  await page.getByRole("button", { name: /^PostgreSQL/ }).click()
  await page
    .locator("[data-slot=flow-panel]")
    .getByRole("switch", { name: "Limit the memory and processors it may use" })
    .click()
  await expect(
    page.locator("[data-slot=flow-panel]").getByRole("spinbutton", { name: "Memory limit" }),
  ).toHaveValue("1024")
  await shot("create-limits-1440")
})
