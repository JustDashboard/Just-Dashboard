import { expect, test, type Page } from "@playwright/test"
import { mockOnboarding } from "./onboarding-fixture"

/**
 * The first-run question and what its answer does.
 *
 * A new account is asked what the server is for straight after signing in,
 * and the answer keeps the sections it does not use out of the sidebar, the
 * Overview and the palette's empty list. What it never does is take a page
 * away: a link from a deployment's domain into a hidden proxy still opens the
 * proxy, the rail says where you are and that the section is hidden, and
 * search finds every page.
 */

const rail = (page: Page) => page.getByRole("navigation", { name: "Sidebar", exact: true })

async function signIn(page: Page) {
  await page.goto("/login")
  await page.getByLabel("Username").fill("ion")
  await page.getByLabel("Password", { exact: true }).fill("Correct-Horse-Battery-9")
  await page.getByRole("button", { name: "Continue" }).click()
}

test("a new account is asked what the server is for, and the answer shapes the shell", async ({
  page,
}) => {
  const { saved } = await mockOnboarding(page, { signedIn: false })
  await signIn(page)

  await expect(page).toHaveURL(/\/welcome$/)
  await expect(page.locator("[data-slot=page]")).toHaveAttribute("data-register", "flow")
  await expect(
    page.getByRole("heading", { level: 1, name: "What will you use this server for?" }),
  ).toBeVisible()
  await expect(page.getByText("Welcome, Ion", { exact: true })).toBeVisible()
  // The question is the cards and nothing beside them: the picture is the answer.
  await expect(page.getByRole("list", { name: "What this server is for" })).toHaveCount(0)

  // The five stable purposes start chosen; routing the network is the one
  // still being tested, says so on its card, and waits to be pressed. The
  // cards of what the server already runs say so.
  const purposes = page.getByRole("group", { name: "Purposes" })
  const purpose = (name: string) => purposes.getByRole("button", { name })
  for (const name of [
    "Ship apps",
    "Run databases",
    "Work on the server",
    "Manage containers",
    "Secure the server",
  ]) {
    await expect(purpose(name)).toHaveAttribute("aria-pressed", "true")
  }
  await expect(purpose("Route the network")).toHaveAttribute("aria-pressed", "false")
  await expect(purpose("Route the network")).toContainText("Experimental")
  await expect(purpose("Route the network")).toContainText("Working, still being tested")
  await expect(purposes.getByText("Experimental", { exact: true })).toHaveCount(1)
  await expect(purposes.getByText("1 project here")).toBeVisible()
  await expect(purposes.getByText("4 databases here")).toBeVisible()
  await purpose("Work on the server").click()
  await expect(purpose("Work on the server")).toHaveAttribute("aria-pressed", "false")
  await page.getByRole("button", { name: "Continue" }).click()

  // Continue is the answer: saved, and drawn as the server wired to each
  // purpose, with the one command under it. There is no sections screen.
  await expect(page.getByRole("heading", { level: 1, name: "Your server is ready" })).toBeVisible()
  await expect(
    page.getByRole("list", { name: "What this server is for" }).getByRole("listitem"),
  ).toHaveText(["Ship apps", "Run databases", "Manage containers", "Secure the server"])
  await expect(page.getByText(/^9 sections in your sidebar/)).toBeVisible()
  await expect(page.getByRole("button", { name: "Go to dashboard" })).toBeInViewport()
  expect(saved).toHaveLength(1)
  expect([...saved[0]].sort()).toEqual(
    ["excalidraw", "files", "git", "network", "remote-desktop", "terminal"].sort(),
  )

  await page.getByRole("button", { name: "Go to dashboard" }).click()
  await expect(page).toHaveURL(/\/$/)
  for (const shown of [
    "Overview",
    "Deployments",
    "Databases",
    "Docker",
    "Proxy & TLS",
    "Security",
    "Backups",
  ]) {
    await expect(rail(page).getByRole("link", { name: shown, exact: true })).toBeVisible()
  }
  for (const hidden of ["Network", "Terminal", "Excalidraw", "Git"]) {
    await expect(rail(page).getByRole("link", { name: hidden, exact: true })).toHaveCount(0)
  }
  // Workspace held only hidden sections, so a label with nothing left under it
  // goes with its rows, and Server configuration is in every answer.
  await expect(rail(page).getByText("Workspace", { exact: true })).toHaveCount(0)
  await expect(rail(page).getByText("Server configuration")).toBeVisible()
  // The Overview keeps the tiles and blocks of what was chosen — Packages
  // among them, because every answer keeps it — and Databases is a block.
  const tile = (name: RegExp) => page.locator("[data-slot=stat-tile]", { hasText: name })
  for (const name of [/^Docker/, /^Security/, /^Packages/]) {
    await expect(tile(name)).toBeVisible()
  }
  await expect(page.getByRole("heading", { name: "Databases", exact: true })).toBeVisible()

  // Answered once, never asked again.
  await page.reload()
  await expect(page).toHaveURL(/\/$/)
})

test("showing everything is an answer too, and nothing is hidden", async ({ page }) => {
  const { saved } = await mockOnboarding(page)
  await page.goto("/docker")
  await expect(page).toHaveURL(/\/welcome$/)
  await page.getByRole("button", { name: "Show me everything" }).click()
  await expect(page).toHaveURL(/\/$/)
  expect(saved).toEqual([[]])
  for (const name of ["Network", "Proxy & TLS", "Security", "Terminal"]) {
    await expect(rail(page).getByRole("link", { name, exact: true })).toBeVisible()
  }
})

test("on a phone the command stays in reach, and the answer fits the screen", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockOnboarding(page)
  await page.goto("/welcome")
  const purposes = page.getByRole("group", { name: "Purposes" })
  await purposes.getByRole("button", { name: "Route the network" }).click()
  // Six cards as rows leave Continue under them on the screen, without a bar
  // holding it there.
  const command = page.getByRole("button", { name: "Continue" })
  await expect(command).toBeInViewport()
  expect(await command.evaluate((el) => getComputedStyle(el.parentElement!).position)).toBe(
    "static",
  )
  await command.click()
  await expect(
    page.getByRole("list", { name: "What this server is for" }).getByRole("listitem"),
  ).toHaveCount(6)
  await expect(page.getByRole("button", { name: "Go to dashboard" })).toBeInViewport()
})

test("an account that has answered is never sent to the question", async ({ page }) => {
  await mockOnboarding(page, { needsOnboarding: false })
  await page.goto("/")
  await expect(page.getByRole("heading", { name: "atlas" })).toBeAttached()
  await expect(page).toHaveURL(/\/$/)
})

test("a link into a hidden section opens it, and the rail says it is hidden", async ({ page }) => {
  const { saved } = await mockOnboarding(page, {
    needsOnboarding: false,
    hiddenSections: ["network", "proxy"],
  })
  await page.goto("/deploy/7/settings/domains")
  const site = page.getByRole("link", { name: "just-dashboard-env-12.conf" })
  await expect(site).toBeVisible()
  await site.click()

  await expect(page).toHaveURL(/\/proxy\/sites\?site=just-dashboard-env-12\.conf$/)
  // The rail went into the section anyway, so it can say where you are.
  await expect(rail(page).getByText("Proxy & TLS", { exact: true })).toBeVisible()
  await expect(rail(page).getByRole("link", { name: "Sites" })).toHaveAttribute(
    "data-active",
    "true",
  )
  const note = page.locator("[data-slot=hidden-section-note]")
  await expect(note).toContainText("Proxy & TLS is hidden from your sidebar.")

  await note.getByRole("button", { name: "Show it" }).click()
  await expect(note).toHaveCount(0)
  expect(saved).toEqual([["network"]])
  await rail(page).getByRole("button", { name: "Back to All pages" }).click()
  await expect(rail(page).getByRole("link", { name: "Proxy & TLS" })).toBeVisible()
  await expect(rail(page).getByRole("link", { name: "Network", exact: true })).toHaveCount(0)
})

test("hidden pages stay out of the palette's list and one word away", async ({ page }) => {
  await mockOnboarding(page, { needsOnboarding: false, hiddenSections: ["network"] })
  await page.goto("/")
  await expect(rail(page).getByRole("link", { name: "Docker" })).toBeVisible()
  await page.keyboard.press("Control+k")
  const dialog = page.getByRole("dialog", { name: "Command palette" })
  const input = dialog.getByRole("combobox", { name: "Search dashboard" })
  await expect(input).toBeFocused()
  await expect(dialog.getByRole("option", { name: /^Docker/ }).first()).toBeVisible()
  await expect(dialog.getByRole("option", { name: /^Firewall/ })).toHaveCount(0)
  await input.fill("firewall")
  await expect(dialog.getByRole("option", { name: /^Firewall/ }).first()).toBeVisible()
})

test("Workspace asks the question again as cards and switches, and saves them", async ({
  page,
}) => {
  const { saved } = await mockOnboarding(page, {
    needsOnboarding: false,
    hiddenSections: ["network"],
  })
  await page.goto("/account/workspace")
  const purpose = (name: string) =>
    page.getByRole("group", { name: "Purposes" }).getByRole("button", { name })
  const toggle = (name: string) =>
    page.getByRole("group", { name: "Sidebar sections" }).getByRole("switch", { name, exact: true })
  const bar = page.getByRole("region", { name: "Save changes" })

  await expect(page.getByRole("group", { name: "Purposes" }).getByRole("button")).toHaveCount(6)
  await expect(toggle("Network")).toHaveAttribute("aria-checked", "false")
  // Network is out of the rail, so routing the network is not chosen.
  const route = purpose("Route the network")
  await expect(route).toHaveAttribute("aria-pressed", "false")
  await expect(route).toContainText("Experimental")
  await expect(purpose("Ship apps")).toHaveAttribute("aria-pressed", "true")
  // Nothing to save, so nothing asks to be saved.
  await expect(bar).toHaveCount(0)

  // A purpose throws its switches, and a switch can leave a purpose partly shown.
  await route.click()
  await expect(route).toHaveAttribute("aria-pressed", "true")
  await expect(toggle("Network")).toHaveAttribute("aria-checked", "true")
  await toggle("Excalidraw").click()
  await expect(purpose("Work on the server")).toHaveAttribute("aria-pressed", "false")
  await expect(bar).toContainText("2 unsaved changes")

  await bar.getByRole("button", { name: /^Save/ }).click()
  await expect.poll(() => saved).toEqual([["excalidraw"]])
  await expect(bar).toContainText("Saved")
  await expect(toggle("Excalidraw")).toHaveAttribute("aria-checked", "false")
  await expect(page.getByRole("link", { name: "Ask me again" })).toHaveAttribute("href", "/welcome")
})
