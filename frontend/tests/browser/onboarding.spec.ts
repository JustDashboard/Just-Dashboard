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
  await expect(page.getByText("Welcome, Ion · Step 1 of 3")).toBeVisible()

  // The server already runs a project and four databases, so those two start
  // chosen and the head says why.
  const purposes = page.getByRole("group", { name: "Purposes" })
  await expect(page.getByText("1 project and 4 databases already on this server")).toBeVisible()
  await expect(purposes.getByRole("button", { name: "Ship apps" })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await expect(purposes.getByRole("button", { name: "Run databases" })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await purposes.getByRole("button", { name: "Run databases" }).click()
  await expect(purposes.getByRole("button", { name: "Run databases" })).toHaveAttribute(
    "aria-pressed",
    "false",
  )
  await page.getByRole("button", { name: "Continue" }).click()

  // The purposes filled the switches in; the switches decide, beside the rail
  // they will draw.
  await expect(
    page.getByRole("heading", { level: 1, name: "Which sections should your sidebar show?" }),
  ).toBeVisible()
  const preview = page.getByRole("navigation", { name: "Sidebar preview" })
  await expect(page.getByRole("switch", { name: "Deployments" })).toBeChecked()
  await expect(page.getByRole("switch", { name: "Network" })).not.toBeChecked()
  await expect(preview.getByText("Proxy & TLS")).toBeVisible()
  await page.getByRole("switch", { name: "Proxy & TLS" }).click()
  await expect(preview.getByText("Proxy & TLS")).toHaveCount(0)
  await expect(page.getByText("5 of 15 shown")).toBeVisible()
  // Overview, Monitoring and Settings cannot be switched off, and are drawn.
  for (const always of ["Overview", "Monitoring", "Settings"]) {
    await expect(preview.getByText(always, { exact: true })).toBeVisible()
  }
  await page.getByRole("button", { name: "Finish" }).click()

  await expect(page.getByRole("heading", { level: 1, name: "Your sidebar is ready" })).toBeVisible()
  await expect(page.getByText("5 of 15 sections in your sidebar")).toBeVisible()
  expect(saved).toHaveLength(1)
  expect([...saved[0]].sort()).toEqual(
    [
      "audit",
      "excalidraw",
      "files",
      "network",
      "packages",
      "proxy",
      "security",
      "stickyboard",
      "system-users",
      "terminal",
    ].sort(),
  )

  await page.getByRole("button", { name: "Open the dashboard" }).click()
  await expect(page).toHaveURL(/\/$/)
  for (const shown of ["Overview", "Deployments", "Databases", "Docker", "Git", "Backups"]) {
    await expect(rail(page).getByRole("link", { name: shown, exact: true })).toBeVisible()
  }
  for (const hidden of ["Network", "Proxy & TLS", "Security", "Terminal", "Excalidraw"]) {
    await expect(rail(page).getByRole("link", { name: hidden, exact: true })).toHaveCount(0)
  }
  // Server configuration held only hidden sections, so its row is gone, and a
  // label with nothing left under it goes with its rows. Protection keeps
  // Backups, which shipping apps uses, so it stays.
  await expect(rail(page).getByText("Server configuration")).toHaveCount(0)
  for (const label of ["Network", "Advanced"]) {
    await expect(rail(page).getByText(label, { exact: true })).toHaveCount(0)
  }
  await expect(rail(page).getByText("Protection", { exact: true })).toBeVisible()
  // The Overview drops the tiles of what was hidden, and keeps the rest.
  await expect(page.getByRole("link", { name: /^Docker/ }).first()).toBeVisible()
  await expect(page.locator("[data-slot=stat-tile]", { hasText: "Security" })).toHaveCount(0)
  await expect(page.locator("[data-slot=stat-tile]", { hasText: "Packages" })).toHaveCount(0)

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

test("Workspace keeps the same switches and saves them", async ({ page }) => {
  const { saved } = await mockOnboarding(page, {
    needsOnboarding: false,
    hiddenSections: ["network"],
  })
  await page.goto("/account/workspace")
  await expect(page.getByRole("switch", { name: "Network" })).not.toBeChecked()
  const save = page.getByRole("button", { name: "Save changes" })
  await expect(save).toBeDisabled()
  await page.getByRole("switch", { name: "Network" }).click()
  await page.getByRole("switch", { name: "Excalidraw" }).click()
  await save.click()
  await expect(save).toBeDisabled()
  expect(saved).toEqual([["excalidraw"]])
  const preview = page.getByRole("navigation", { name: "Sidebar preview" })
  await expect(preview.getByText("Network", { exact: true }).last()).toBeVisible()
  await expect(preview.getByText("Excalidraw", { exact: true })).toHaveCount(0)
  await expect(page.getByRole("link", { name: "Ask me again" })).toHaveAttribute("href", "/welcome")
})
