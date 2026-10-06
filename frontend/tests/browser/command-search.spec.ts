import { expect, test, type Page } from "@playwright/test"
import { CONTAINER_ID, json, mockCommandSearch, searchSites, user } from "./command-search-fixture"
import { mockDatabases } from "./database-fixture"

const palette = (page: Page) => page.getByRole("dialog", { name: "Command palette" })
const input = (page: Page) => palette(page).getByRole("combobox", { name: "Search dashboard" })
const selected = (page: Page) =>
  palette(page).getByRole("listbox").getByRole("option", { selected: true })

async function open(page: Page, key = "Control+k") {
  const trigger =
    page.viewportSize()!.width < 768
      ? page.getByRole("button", { name: "Toggle the sidebar", exact: true })
      : page.locator('[data-slot="sidebar-header"]').getByRole("button", { name: /Search/ })
  await trigger.focus()
  await page.keyboard.press(key)
  await expect(input(page)).toBeFocused()
}

test("find a domain, inspect its container and return to the previous site entirely by keyboard", async ({
  page,
}) => {
  const traffic = await mockCommandSearch(page)
  await page.goto("/account")
  await open(page)
  await input(page).fill("domain: shop.example.com")
  await expect(selected(page)).toContainText("shop-ingress")
  await input(page).press("Enter")
  await expect(page).toHaveURL(/\/proxy\/sites\/shop-ingress$/)
  await expect(page.getByRole("heading", { name: "shop-ingress", exact: true })).toBeAttached()
  await expect(page.getByText("shop.example.com", { exact: true }).first()).toBeVisible()
  await open(page, "Meta+k")
  await input(page).fill("container: shop-web")
  await expect(selected(page)).toContainText("shop-web")
  await input(page).press("Enter")
  await expect(page).toHaveURL(new RegExp(`/docker/containers/${CONTAINER_ID}$`))
  await expect(page.getByRole("heading", { name: "shop-web", exact: true })).toBeAttached()
  await expect(page.getByRole("tab", { name: "Overview", exact: true })).toBeVisible()
  await open(page)
  await expect(selected(page)).toContainText("Back to shop-ingress")
  await input(page).press("Enter")
  await expect(page).toHaveURL(/\/proxy\/sites\/shop-ingress$/)
  await expect(page.getByRole("heading", { name: "shop-ingress", exact: true })).toBeAttached()
  await open(page)
  await expect(selected(page)).toContainText("Back to shop-web")
  expect(traffic.mutations).toEqual([])
})

test("arrows, Home/End, Escape and shortcut toggle retain focus ownership", async ({ page }) => {
  await mockCommandSearch(page)
  await page.goto("/account")
  const trigger = page.getByRole("button", { name: /Search/ }).first()
  await trigger.focus()
  await trigger.press("Enter")
  await input(page).fill("page: docker")
  await expect(selected(page)).toContainText("Docker")
  await input(page).press("ArrowDown")
  await expect(selected(page)).toContainText("Containers")
  await input(page).press("End")
  await expect(selected(page)).toContainText("Events")
  await input(page).press("Home")
  await expect(selected(page)).toContainText("Docker")
  await expect(input(page)).toBeFocused()
  await input(page).press("Escape")
  await expect(input(page)).toHaveValue("")
  await expect(palette(page)).toBeVisible()
  await input(page).press("Escape")
  await expect(palette(page)).toHaveCount(0)
  await expect(trigger).toBeFocused()
  await open(page)
  await palette(page).getByRole("combobox", { name: "Search scope" }).focus()
  await page.keyboard.press("Control+k")
  await expect(palette(page)).toHaveCount(0)
  await page.keyboard.down("Control")
  await page.keyboard.down("k")
  await page.keyboard.down("k")
  await expect(input(page)).toBeFocused()
  await page.keyboard.up("k")
  await page.keyboard.up("Control")
  await page.keyboard.press("Control+k")
  await expect(palette(page)).toHaveCount(0)
})

test("all live inventories are scoped and duplicate PM2 names keep their daemon identity", async ({
  page,
}) => {
  await mockCommandSearch(page)
  await page.goto("/account")
  await open(page)
  for (const [query, label] of [
    ["project: shop", "shop"],
    ["domain: shop.example.com", "shop-ingress"],
    ["db: production", "shop-main"],
    ["container: cafebabe", "shop-web"],
    ["stack: shop", "shop-stack"],
    ["repo: /srv/shop", "shop"],
    ["service: caddy", "caddy.service"],
    ["backup: shop", "shop-nightly"],
    ["board: shop", "Shop architecture"],
  ]) {
    await input(page).fill(query)
    await expect(selected(page)).toContainText(label)
    await expect(palette(page).getByRole("listbox").getByRole("option")).toHaveCount(1)
  }
  await input(page).fill("pm2: shop-worker")
  await expect(palette(page).getByRole("listbox").getByRole("option")).toHaveCount(2)
  await expect(selected(page)).toContainText("deploy")
  await input(page).press("ArrowDown")
  await expect(selected(page)).toContainText("ubuntu")
  await expect(selected(page)).toHaveAttribute("data-value", "app:ubuntu:0")
  await palette(page).getByRole("combobox", { name: "Search scope" }).click()
  await page.getByRole("option", { name: "Databases", exact: true }).click()
  await expect(input(page)).toBeFocused()
  await expect(input(page)).toHaveValue("database: shop-worker")
  await expect(
    palette(page).getByText("No matches. Try a name, domain or another scope."),
  ).toBeVisible()
})

test("a failed source reports incomplete search and can recover without reopening", async ({
  page,
}) => {
  await mockCommandSearch(page)
  let unavailable = true
  await page.route("**/api/v1/proxy/vhosts", (route) =>
    unavailable
      ? route.fulfill({
          status: 503,
          contentType: "application/json",
          body: JSON.stringify({ error: { code: "unavailable", message: "Unavailable" } }),
        })
      : json(route, searchSites),
  )
  await page.goto("/account")
  await open(page)
  await input(page).fill("domain: shop")
  await expect(palette(page).getByRole("status")).toContainText("Results are incomplete")
  await expect(palette(page).getByText("No matches in the available results.")).toBeVisible()
  unavailable = false
  await palette(page).getByRole("button", { name: "Retry unavailable sources" }).click()
  await expect(palette(page).getByRole("listbox").getByRole("option")).toContainText("shop-ingress")
  await expect(palette(page).getByRole("status")).not.toContainText("incomplete")
})

test("late results do not take the selected option from the keyboard", async ({ page }) => {
  await mockCommandSearch(page)
  let release!: () => void
  const held = new Promise<void>((resolve) => {
    release = resolve
  })
  await page.route("**/api/v1/proxy/vhosts", async (route) => {
    await held
    await json(route, searchSites)
  })
  await page.goto("/account")
  await open(page)
  await input(page).fill("shop")
  await expect(
    palette(page).getByRole("listbox").getByRole("option").filter({ hasText: "shop-web" }),
  ).toBeVisible()
  await expect(palette(page).getByRole("status")).toHaveText("Loading Domains & sites…")
  await input(page).press("End")
  const identity = await selected(page).getAttribute("data-value")
  release()
  await expect(
    palette(page).getByRole("listbox").getByRole("option").filter({ hasText: "shop-ingress" }),
  ).toBeVisible()
  await expect(selected(page)).toHaveAttribute("data-value", identity!)
})

test("reopening reads additions and removes deleted resources without polling while closed", async ({
  page,
}) => {
  const traffic = await mockCommandSearch(page)
  let sites = searchSites
  await page.route("**/api/v1/proxy/vhosts", (route) => json(route, sites))
  await page.goto("/account")
  await expect(page.getByRole("heading", { name: "operator", exact: true })).toBeAttached()
  expect(traffic.reads).not.toContain("/docker/stacks/")
  await open(page)
  await input(page).fill("domain: shop")
  await expect(palette(page).getByRole("listbox").getByRole("option")).toContainText("shop-ingress")
  await page.keyboard.press("Control+k")
  sites = [{ ...searchSites[2], name: "new-shop", serverNames: ["new-shop.example.test"] }]
  await open(page)
  await input(page).fill("domain: shop")
  await expect(palette(page).getByRole("listbox").getByRole("option")).toHaveCount(1)
  await expect(palette(page).getByRole("listbox").getByRole("option")).toContainText("new-shop")
})

test("page and action permissions stay hidden for a read-only account", async ({ page }) => {
  const traffic = await mockCommandSearch(page, {
    ...user,
    capabilities: ["read"],
    user: { ...user.user, role: "readonly" },
  })
  await page.goto("/account")
  await open(page)
  for (const query of [
    "terminal",
    "system users",
    "audit log",
    "reload nginx",
    "new project",
    "credentials",
  ]) {
    await input(page).fill(query)
    await expect(palette(page).getByRole("listbox").getByRole("option")).toHaveCount(0)
  }
  await input(page).fill("domain: shop")
  await expect(palette(page).getByRole("listbox").getByRole("option")).toContainText("shop-ingress")
  expect(traffic.reads).not.toContain("/proxy/status")
  expect(traffic.mutations).toEqual([])
})

test("current database engine pages remain searchable", async ({ page }) => {
  await mockDatabases(page)
  await page.goto("/databases/1")
  await open(page)
  await input(page).fill("page: shop query")
  await expect(selected(page)).toContainText("Query")
  await input(page).press("Enter")
  await expect(page).toHaveURL(/\/databases\/1\/query/)
})

test("the page-owned Find command retains keyboard focus after the dialog closes", async ({
  page,
}) => {
  await mockCommandSearch(page)
  await page.goto("/git")
  await open(page)
  await input(page).fill("command: Find in Repositories")
  await expect(selected(page)).toContainText("Find in Repositories")
  await input(page).press("Enter")
  await expect(palette(page)).toHaveCount(0)
  const find = page.getByPlaceholder("Filter by name, path, branch or pull request")
  await expect(find).toBeFocused()
  await page.keyboard.type("shop")
  await expect(find).toHaveValue("shop")
})

test("the global shortcut opens from the SQL editor and restores the editor's focus", async ({
  page,
}) => {
  await mockDatabases(page)
  await page.goto("/databases/1/query")
  await expect(page.locator("[data-slot=sql-editor] .monaco-editor").first()).toBeVisible({
    timeout: 15_000,
  })
  const editor = page.getByRole("textbox", {
    name: "Query 1: SQL statement. Escape, then Tab, leaves the editor.",
    exact: true,
  })
  // Monaco's native EditContext receives keyboard focus in a zero-size element.
  await expect(editor).toBeAttached()
  await editor.focus()
  await expect(editor).toBeFocused()
  await page.keyboard.press("Control+k")
  await expect(input(page)).toBeFocused()
  await page.keyboard.press("Control+k")
  await expect(palette(page)).toHaveCount(0)
  await expect(editor).toBeFocused()
})

test("mobile results and controls fit inside the viewport", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockCommandSearch(page)
  await page.goto("/account")
  await open(page)
  await input(page).fill("shop")
  await expect(
    palette(page).getByRole("listbox").getByRole("option").filter({ hasText: "shop-web" }),
  ).toBeVisible()
  const box = (await palette(page).boundingBox())!
  expect(box.x).toBeGreaterThanOrEqual(0)
  expect(box.y).toBeGreaterThanOrEqual(0)
  expect(box.x + box.width).toBeLessThanOrEqual(390)
  expect(box.y + box.height).toBeLessThanOrEqual(844)
  for (const option of await palette(page).getByRole("listbox").getByRole("option").all()) {
    expect((await option.boundingBox())!.height).toBeGreaterThanOrEqual(44)
  }
})

test("Escape closes the scope picker without changing the search or closing the palette", async ({
  page,
}) => {
  await mockCommandSearch(page)
  await page.goto("/account")
  await open(page)
  await input(page).fill("domain: shop")
  await palette(page).getByRole("combobox", { name: "Search scope" }).click()
  await expect(page.locator("[data-slot=select-content]")).toBeVisible()
  await page.keyboard.press("Escape")
  await expect(page.locator("[data-slot=select-content]")).toHaveCount(0)
  await expect(palette(page)).toBeVisible()
  await expect(input(page)).toHaveValue("domain: shop")
  await expect(input(page)).toBeFocused()
})
