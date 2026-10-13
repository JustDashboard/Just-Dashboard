import { expect, test } from "@playwright/test"
import { gotoStep, json, mockNewProject, now } from "./deploy-fixture"

/**
 * `/deploy/new` — the plan a setup is building, and the four decisions that
 * used to be reachable only after the project existed: what an image is, what
 * else it answers to, whether pushes deploy themselves, and which unfinished
 * setups are still wanted.
 */

test("the plan drawing reads the setup back and opens the field that decides each step", async ({
  page,
}) => {
  await mockNewProject(page)
  await page.goto("/deploy/new")
  await page.getByRole("button", { name: "Import Wayy01/wesmokefish" }).click()
  // The drawing is beside every one of the four configure screens, so it reads
  // the setup back wherever the reader is standing in the sequence.
  await gotoStep(page, "project")
  await expect(page.getByRole("textbox", { name: "Project name" })).toBeVisible()

  const plan = page.getByRole("list", { name: "What this setup will create" })
  await expect(plan.getByText("Wayy01/wesmokefish", { exact: true })).toBeVisible()
  await expect(plan.getByText("Automatic recipe · Next.js")).toBeVisible()
  await expect(plan.getByText("Port 3000 · candidate first")).toBeVisible()

  // An unbounded container is the thing that takes the whole server down with
  // it, so the plan says so rather than leaving it inside Advanced to find.
  await expect(plan.getByText("No memory or CPU limit")).toBeVisible()

  // Pressing a step goes to the screen that decides it and opens the fold the
  // node was reading from — the limits are one press away, not a press, a
  // wall of twenty-five fields and a hunt.
  await expect(page.getByRole("spinbutton", { name: "Memory limit (MB)" })).toHaveCount(0)
  await plan.getByRole("button", { name: "Change the runtime settings" }).click()
  await expect(page.getByRole("spinbutton", { name: "Memory limit (MB)" })).toBeVisible()

  await page.getByRole("spinbutton", { name: "Memory limit (MB)" }).fill("512")
  await expect(plan.getByText("512 MB")).toBeVisible()
})

test("a second hostname can be added before the first deploy", async ({ page }) => {
  const journey = await mockNewProject(page)
  await page.goto("/deploy/new")
  await page.getByRole("button", { name: "Import Wayy01/wesmokefish" }).click()
  await gotoStep(page, "runtime")

  await page.getByRole("textbox", { name: "Hostname", exact: true }).fill("wesmokefish.test")
  await page.getByRole("button", { name: "Add another hostname" }).click()
  await page.getByRole("textbox", { name: "Also answers to 2" }).fill("www.wesmokefish.test")

  const plan = page.getByRole("list", { name: "What this setup will create" })
  await expect(plan.getByText("HTTPS · +1 more")).toBeVisible()

  await gotoStep(page, "review")
  await page.getByRole("button", { name: "Save only", exact: true }).click()
  await expect.poll(() => journey.commits()).toBe(1)
  const saved = journey.configuration() as { domains: { hostname: string }[] }
  expect(saved.domains.map((domain) => domain.hostname)).toEqual([
    "wesmokefish.test",
    "www.wesmokefish.test",
  ])
})

test("an unfinished setup can be discarded, and changing source discards the one abandoned", async ({
  page,
}) => {
  const journey = await mockNewProject(page)
  let listed = [
    {
      id: "abandoned-1",
      name: "lampino",
      source: "git · https://github.com/Wayy01/lampino.git",
      currentStep: "detection",
      updatedAt: now,
      expiresAt: "2026-10-02T09:00:00Z",
    },
  ]
  await page.route("**/api/v1/deploy/drafts", (route) =>
    route.request().method() === "GET" ? json(route, listed) : route.fallback(),
  )
  await page.goto("/deploy/new")

  await page.getByRole("button", { name: /Unfinished setups/ }).click()
  const list = page.getByRole("list", { name: "Unfinished setups" })
  await expect(list.getByRole("link", { name: /lampino/ })).toBeVisible()
  listed = []
  await list.getByRole("button", { name: "Discard lampino" }).click()
  await expect.poll(() => journey.discarded()).toContain("abandoned-1")
  await expect(page.getByRole("heading", { name: "Unfinished setups" })).toHaveCount(0)

  // Every press of Import creates a draft, so walking away from one and
  // picking a different source has to throw the first away rather than leave
  // it to expire — three tries at one repository were three rows here.
  await page.getByRole("button", { name: "Import Wayy01/wesmokefish" }).click()
  // The way out of the sequence is the way back from its first screen: there
  // is no step behind that one, so "Back" is "Change source".
  await gotoStep(page, "project")
  await page.getByRole("button", { name: "Change source" }).click()
  await expect(page.getByRole("heading", { name: "Import Git repository" })).toBeVisible()
  await expect.poll(() => journey.discarded()).toContain("journey-draft")
})
