import { expect, test, type Page } from "@playwright/test"
import { availability, json, mockProxy, user } from "./proxy-fixtures"
import { mockConfigViews } from "./fixtures/proxy/config-views"
import { risen } from "./risen"

/**
 * The Configuration page (`/proxy/config`) as the overhaul left it: nginx's
 * directory as an identity line with Test config, the include picture over
 * the files, the administrator's views in the address, Settings as one
 * column with one save bar, a file's history as the editor's History view,
 * and the section's setup screen where there is no nginx.
 */

const app = "/etc/nginx/sites-available/app.example.com"

async function openPage(page: Page, path = "/proxy/config") {
  await mockProxy(page, { included: true })
  const writes = await mockConfigViews(page)
  await page.goto(path)
  return writes
}

for (const width of [1280, 390]) {
  test(`the page opens on nginx's directory and the way nginx walks it, at ${width}`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 })
    await openPage(page)
    const identity = page.locator("[data-slot='host-identity']")
    await expect(identity).toContainText("/etc/nginx")
    await expect(identity).toContainText("13 files in 8 folders")
    await expect(page.locator("[data-slot='stat-grid'], [data-slot='stat-tile']")).toHaveCount(0)

    const picture = page.getByRole("list", { name: "What nginx reads" })
    await expect(picture).toContainText("nginx.conf")
    await expect(picture).toContainText("sites-enabled")
    await expect(picture).toContainText("snippets")
    await expect(picture).toContainText("2 of 3 files read")
    await expect(picture).toContainText("stream.d")
    // The picture comes before the list.
    const pictureTop = (await picture.boundingBox())!.y
    const listTop = (await page.getByLabel("Search files").boundingBox())!.y
    expect(pictureTop).toBeLessThan(listTop)
  })

  test(`file rows are one height with and without a tag, at ${width}`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    await openPage(page)
    const available = page.getByRole("list", { name: "Files in sites-available/" })
    const rows = available.locator("[data-slot='choice-row']")
    await expect(rows).toHaveCount(3)
    await risen(page)
    const managed = await rows.filter({ hasText: "app.example.com" }).boundingBox()
    const plain = await rows.filter({ hasText: "legacy.example.com" }).boundingBox()
    const unread = await rows.filter({ hasText: "old-site" }).boundingBox()
    expect(Math.round(managed!.height)).toBe(Math.round(plain!.height))
    expect(Math.round(unread!.height)).toBe(Math.round(plain!.height))
  })

  test(`the identity line holds its place while the directory is read, at ${width}`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 })
    await mockProxy(page, { included: true })
    let release: () => void = () => {}
    const held = new Promise<void>((resolve) => (release = resolve))
    await page.route("**/api/v1/proxy/files", async (route) => {
      await held
      await route.fallback()
    })
    await page.goto("/proxy/config")
    const identity = page.locator("[data-slot='host-identity']")
    await expect(identity).toContainText("Reading the directory")
    await risen(page)
    const loading = (await identity.boundingBox())!
    release()
    await expect(identity).toContainText("13 files")
    await risen(page)
    const loaded = (await identity.boundingBox())!
    expect(Math.round(loaded.y)).toBe(Math.round(loading.y))
    // On a phone the facts wrap once they are known; wide, the line is one height.
    if (width === 1280) expect(Math.abs(loaded.height - loading.height)).toBeLessThanOrEqual(1)
  })
}

test("a folder in the picture narrows the list, and the main file opens", async ({ page }) => {
  await openPage(page)
  await page.getByRole("button", { name: "Show the files in snippets" }).first().click()
  await expect(page.getByLabel("Search files")).toHaveValue("snippets/")
  await expect(page.locator("[data-slot='choice-row']")).toHaveCount(2)
  await page.getByRole("button", { name: "Open nginx.conf" }).first().click()
  await expect(page.getByRole("dialog", { name: "nginx.conf" })).toBeVisible()
})

test("Test config is the line's command, and the menu reads again", async ({ page }) => {
  await openPage(page)
  const tested = page.waitForRequest(
    (r) => r.method() === "POST" && new URL(r.url()).pathname === "/api/v1/proxy/test",
  )
  await page
    .locator("[data-slot='host-identity']")
    .getByRole("button", { name: "Test config" })
    .click()
  await tested
  const reread = page.waitForRequest((r) => new URL(r.url()).pathname === "/api/v1/proxy/files")
  await page.getByRole("button", { name: "More configuration actions" }).click()
  await page.getByRole("menuitem", { name: "Read again" }).click()
  await reread
})

test("the views are in the address, and a reload opens on the one named", async ({ page }) => {
  await openPage(page)
  const views = page.getByRole("tablist", { name: "Configuration views" })
  await views.getByRole("tab", { name: "Settings" }).click()
  await expect(page).toHaveURL(/[?&]tab=settings/)
  await expect(page.getByRole("form", { name: "nginx settings" })).toBeVisible()
  await page.reload()
  await expect(views.getByRole("tab", { name: "Settings" })).toHaveAttribute(
    "aria-selected",
    "true",
  )
  await views.getByRole("tab", { name: "Files" }).click()
  await expect(page).not.toHaveURL(/tab=/)
})

test("settings are one column: an edit shows its line, and the one save bar writes it", async ({
  page,
}) => {
  const writes = await openPage(page, "/proxy/config?tab=settings")
  const form = page.getByRole("form", { name: "nginx settings" })
  await expect(form.getByRole("heading", { name: "Requests" })).toBeVisible()
  await expect(form).toContainText("1,024 open files per worker")
  // No Change button per setting, no Save per section.
  await expect(form.getByRole("button", { name: "Change" })).toHaveCount(0)

  await page.getByLabel("client_max_body_size").fill("64m")
  await expect(form).toContainText("Creates conf.d/jd-http.conf")
  await expect(form).toContainText("client_max_body_size 64m;")
  const bar = page.getByRole("region", { name: "Save changes" })
  await expect(bar).toContainText("1 unsaved change")
  await expect(bar).toContainText("Applies immediately")

  await page.getByRole("radio", { name: "off" }).first().click()
  await expect(bar).toContainText("2 unsaved changes")
  await page.getByRole("switch", { name: "Reload nginx after saving" }).click()
  await expect(bar).toContainText("Applies when nginx next reloads")
  await bar.getByRole("button", { name: /^Save/ }).click()
  await expect
    .poll(() => writes.map((w) => w.body))
    .toEqual([
      {
        changes: [
          { name: "server_tokens", value: "off" },
          { name: "client_max_body_size", value: "64m" },
        ],
        reload: false,
      },
    ])

  // The file and line a setting comes from opens there.
  await form.getByRole("button", { name: "Open conf.d/gzip.conf at line 1" }).click()
  await expect(page.getByRole("dialog", { name: "gzip.conf" })).toBeVisible()
})

test("a setting emptied cannot be saved, and discard puts it back", async ({ page }) => {
  await openPage(page, "/proxy/config?tab=settings")
  const field = page.getByLabel("keepalive_timeout")
  await field.fill("")
  await expect(page.getByText("Give it a value; nginx's default is 75s.")).toBeVisible()
  const bar = page.getByRole("region", { name: "Save changes" })
  await expect(bar.getByRole("button", { name: /^Save/ })).toBeDisabled()
  await bar.getByRole("button", { name: "Discard" }).click()
  await expect(field).toHaveValue("65")
})

test("lint findings open the editor at their line", async ({ page }) => {
  await openPage(page, "/proxy/config?tab=lint")
  await expect(page.getByText("A location drops the server's headers")).toBeVisible()
  await page.getByRole("button", { name: "Open at line" }).click()
  await expect(page.getByRole("dialog", { name: "app.example.com" })).toBeVisible()
})

test("a file's history is the editor's History view, and restore writes the revision", async ({
  page,
}) => {
  const writes = await openPage(page, "/proxy/config?tab=history")
  await page.getByRole("button", { name: "History of sites-available/app.example.com" }).click()
  const editor = page.getByRole("dialog", { name: "app.example.com" })
  await expect(editor.getByRole("tab", { name: "History" })).toHaveAttribute(
    "aria-selected",
    "true",
  )
  await expect(page).toHaveURL(/[?&]view=history/)
  // No second sheet beside the editor.
  await expect(page.getByRole("dialog")).toHaveCount(1)
  const revisions = editor.getByRole("list", { name: "Revisions" })
  await expect(revisions.locator("[data-slot='row']")).toHaveCount(3)
  await revisions.getByText("Before the first recorded change").click()
  await editor.getByRole("button", { name: "Restore" }).click()
  const confirm = page.getByRole("dialog", { name: /^Restore sites-available\/app\.example\.com/ })
  await confirm.getByRole("button", { name: "Restore and reload" }).click()
  await expect.poll(() => writes.map((w) => w.path)).toEqual(["/proxy/history/1/restore"])
  expect(writes[0].body).toEqual({ reload: true })

  // The file view is beside it, with the editor's own commands.
  await editor.getByRole("tab", { name: "File" }).click()
  await expect(editor.getByRole("button", { name: "Save and reload" })).toBeVisible()
  await expect(page).not.toHaveURL(/view=history/)
})

test("the old history address lands on the file's History view", async ({ page }) => {
  await openPage(page, `/proxy/config?history=${encodeURIComponent(app)}`)
  const editor = page.getByRole("dialog", { name: "app.example.com" })
  await expect(editor.getByRole("tab", { name: "History" })).toHaveAttribute(
    "aria-selected",
    "true",
  )
  await expect(page).toHaveURL(/[?&]tab=history/)
  await expect(page).not.toHaveURL(/[?&]history=/)
  // Under the sheet, the page is on its History view.
  await editor.getByRole("button", { name: "Close" }).click()
  await expect(
    page
      .getByRole("tablist", { name: "Configuration views" })
      .getByRole("tab", { name: "History" }),
  ).toHaveAttribute("aria-selected", "true")
})

test("a reader following a history link reads the file, with no history or restore", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await mockConfigViews(page)
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, { ...user, capabilities: ["read"] }),
  )
  await page.goto(`/proxy/config?history=${encodeURIComponent(app)}`)
  const editor = page.getByRole("dialog", { name: "app.example.com" })
  await expect(editor.locator(".monaco-editor .view-lines")).toContainText("server_name", {
    timeout: 20_000,
  })
  await expect(editor.getByRole("tab", { name: "History" })).toHaveCount(0)
  await expect(editor.getByRole("button", { name: "Restore" })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Test config" })).toHaveCount(0)
})

test("a server without nginx is offered the section's install screen", async ({ page }) => {
  await mockProxy(page, { included: true })
  await page.route("**/api/v1/proxy/status", (route) =>
    json(route, { ...availability, nginx: false, nginxVersion: "", certbot: false }),
  )
  await page.route("**/api/v1/packages/updates", (route) =>
    json(route, { available: true, manager: "apt", packages: [] }),
  )
  await page.goto("/proxy/config")
  await expect(
    page.getByRole("heading", { name: "Put nginx in front of this server" }),
  ).toBeVisible()
  await expect(page.getByRole("button", { name: /^Install nginx/ })).toBeVisible()
})

test("a Caddy server is sent to Sites, where its Caddyfile opens", async ({ page }) => {
  await mockProxy(page, { included: true })
  await page.route("**/api/v1/proxy/status", (route) =>
    json(route, { ...availability, nginx: false, nginxVersion: "", caddy: true }),
  )
  await page.goto("/proxy/config")
  await expect(page.getByText("This server's proxy is Caddy")).toBeVisible()
  await expect(page.getByRole("link", { name: "Open Sites" })).toHaveAttribute(
    "href",
    "/proxy/sites",
  )
})
