import { expect, test, type Page } from "@playwright/test"
import { json, mockProxy, mockShowcase, user, vhosts } from "./proxy-fixtures"

/**
 * The Sites list and what its verbs report back.
 *
 * Each of these was a way the page told an operator something that was not
 * true: an enable nginx could not load came back as "configuration failed
 * validation" and left the card saying disabled while the link poisoned every
 * later reload; a delete or disable whose reload failed said "completed"; a
 * ?site= link handed a read-only account the editable form; a link in
 * sites-enabled to nothing — which stops every reload — was not listed at
 * all; and the password-file warning promised an nginx that would not start.
 */

const [app, legacy] = vhosts

// Monaco loads from /monaco/vs on first use, which takes longer than the
// default expectation on a busy machine.
const editorLoad = { timeout: 20_000 }

const refusal = {
  error: {
    code: "invalid_config",
    message: 'unknown directive "foo" in /etc/nginx/sites-available/off.example.com:3',
    raw:
      'nginx: [emerg] unknown directive "foo" in /etc/nginx/sites-enabled/off.example.com:3\n' +
      "nginx: configuration file /etc/nginx/nginx.conf test failed",
  },
}

const brokenTest = {
  valid: false,
  command: "nginx -t",
  output:
    'nginx: [emerg] open() "/etc/nginx/sites-enabled/ghost" failed (2: No such file or directory) in /etc/nginx/nginx.conf:60\n' +
    "nginx: configuration file /etc/nginx/nginx.conf test failed",
  diagnostics: [
    {
      level: "emerg",
      message: 'open() "/etc/nginx/sites-enabled/ghost" failed (2: No such file or directory)',
      file: "/etc/nginx/nginx.conf",
      line: 60,
    },
  ],
  warnings: 0,
}
const brokenReason =
  'nginx -t failed: open() "/etc/nginx/sites-enabled/ghost" failed (2: No such file or directory) in /etc/nginx/nginx.conf:60'

const off = {
  ...legacy,
  name: "off.example.com",
  path: "/etc/nginx/sites-available/off.example.com",
  enabledPath: "/etc/nginx/sites-enabled/off.example.com",
  enabled: false,
  serverNames: ["off.example.com"],
}

/** Every place nginx reads a site from, as the listing now reports them. */
const layouts = [
  app,
  {
    ...legacy,
    name: "ghost",
    path: "",
    layout: "sites-enabled",
    enabledPath: "/etc/nginx/sites-enabled/ghost",
    enabled: false,
    broken: "dangling",
    linkTarget: "/etc/nginx/sites-available/gone",
    formEditable: false,
    serverNames: [],
    listen: [],
    upstreams: [],
  },
  {
    ...legacy,
    name: "renamed.example.com",
    path: "/etc/nginx/sites-available/renamed.example.com",
    enabledPath: "/etc/nginx/sites-enabled/renamed.example.com",
    enabled: false,
    broken: "stale",
    linkTarget: "/etc/nginx/sites-available/old.example.com",
    serverNames: ["renamed.example.com"],
    tls: true,
  },
  {
    ...legacy,
    name: "extra.conf",
    path: "/etc/nginx/conf.d/extra.conf",
    layout: "conf.d",
    enabledPath: undefined,
    formEditable: false,
    serverNames: ["extra.example.com"],
    tls: true,
  },
  {
    ...legacy,
    name: "copied-in",
    path: "/etc/nginx/sites-enabled/copied-in",
    layout: "sites-enabled",
    enabledPath: undefined,
    formEditable: false,
    serverNames: ["copied.example.com"],
    tls: true,
  },
]

async function serveSites(page: Page, sites: unknown[]) {
  let reads = 0
  await page.route("**/api/v1/proxy/vhosts", (route) => {
    reads += 1
    return json(route, sites)
  })
  return () => reads
}

const card = (page: Page, name: string) =>
  page.locator("[data-slot='choice-row']").filter({ has: page.getByText(name, { exact: true }) })

async function openMenu(page: Page, name: string) {
  await page.getByRole("button", { name: `More actions for ${name}` }).click()
  return page.getByRole("menu")
}

test("an enable nginx refuses says why, shows nginx's output, and leaves the site off", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  const reads = await serveSites(page, [app, off])
  let posted: unknown
  await page.route("**/api/v1/proxy/vhosts/off.example.com/enabled", (route) => {
    posted = route.request().postDataJSON()
    return route.fulfill({
      status: 422,
      contentType: "application/json",
      body: JSON.stringify(refusal),
    })
  })
  await page.goto("/proxy/sites")
  await expect(card(page, "off.example.com").getByText("disabled", { exact: true })).toBeVisible()
  const before = reads()

  await (await openMenu(page, "off.example.com")).getByRole("menuitem", { name: "Enable" }).click()

  const toast = page
    .locator("[data-sonner-toast]")
    .filter({ hasText: "Could not enable off.example.com" })
  await expect(toast).toContainText(
    'unknown directive "foo" in /etc/nginx/sites-available/off.example.com:3',
  )
  expect(posted).toEqual({ enabled: true, reload: true })
  await toast.getByRole("button", { name: "Show nginx output" }).click()
  const output = page.getByRole("dialog", { name: "nginx output — off.example.com" })
  await expect(output).toContainText("configuration file /etc/nginx/nginx.conf test failed")
  await page.keyboard.press("Escape")
  // The card is read again, and still says what nginx is doing.
  await expect.poll(reads).toBeGreaterThan(before)
  await expect(card(page, "off.example.com").getByText("disabled", { exact: true })).toBeVisible()
})

test("a switch that landed but did not reload says so instead of 'completed'", async ({ page }) => {
  await mockProxy(page, { included: true })
  await serveSites(page, [app, off])
  await page.route("**/api/v1/proxy/vhosts/*/enabled", (route) => {
    const { enabled } = route.request().postDataJSON()
    const name = enabled ? "off.example.com" : "app.example.com"
    return json(route, {
      name,
      enabled,
      reloaded: false,
      reloadError: brokenReason,
      reload: { validation: brokenTest, reloaded: false, output: "" },
    })
  })
  await page.goto("/proxy/sites")

  await (await openMenu(page, "app.example.com")).getByRole("menuitem", { name: "Disable" }).click()
  const confirm = page.getByRole("dialog", { name: "Disable app.example.com" })
  await confirm.getByRole("button", { name: "Disable and reload" }).click()
  await expect(confirm).toHaveCount(0)
  const disabled = page
    .locator("[data-sonner-toast]")
    .filter({ hasText: "app.example.com disabled, not reloaded" })
  await expect(disabled).toContainText("nginx keeps serving it until a reload succeeds.")
  await expect(disabled).toContainText(brokenReason)
  await expect(page.getByText("Disable app.example.com completed")).toHaveCount(0)

  await (await openMenu(page, "off.example.com")).getByRole("menuitem", { name: "Enable" }).click()
  const enabled = page
    .locator("[data-sonner-toast]")
    .filter({ hasText: "off.example.com enabled, not reloaded" })
  await expect(enabled).toContainText("It is not serving until nginx reloads.")
  await enabled.getByRole("button", { name: "Show nginx output" }).click()
  await expect(page.getByRole("dialog", { name: "nginx output — off.example.com" })).toContainText(
    "test failed",
  )
})

test("a delete whose reload failed says the site is still served", async ({ page }) => {
  await mockProxy(page, { included: true })
  const reads = await serveSites(page, [app, legacy])
  let reload = false
  await page.route("**/api/v1/proxy/sites/*", (route) => {
    if (route.request().method() !== "DELETE") return route.fallback()
    const name = decodeURIComponent(new URL(route.request().url()).pathname.split("/").pop()!)
    return json(
      route,
      reload
        ? {
            name,
            reload: { validation: { ...brokenTest, valid: true }, reloaded: true, output: "" },
          }
        : {
            name,
            // What the delete handler answers when the test refused its reload.
            reloadError: "configuration failed validation",
            reload: { validation: brokenTest, reloaded: false, output: "" },
          },
    )
  })
  await page.goto("/proxy/sites")

  await (
    await openMenu(page, "legacy.example.com")
  )
    .getByRole("menuitem", { name: "Delete" })
    .click()
  await page.getByRole("button", { name: "Delete and reload" }).click()
  const toast = page
    .locator("[data-sonner-toast]")
    .filter({ hasText: "legacy.example.com deleted, not reloaded" })
  await expect(toast).toContainText("nginx keeps serving it until a reload succeeds.")
  await expect(toast).toContainText(brokenReason)
  await expect(page.getByText("Delete legacy.example.com completed")).toHaveCount(0)
  expect(reads()).toBeGreaterThan(1)

  reload = true
  await (await openMenu(page, "app.example.com")).getByRole("menuitem", { name: "Delete" }).click()
  await page.getByRole("button", { name: "Delete and reload" }).click()
  await expect(page.getByText("Delete app.example.com completed")).toBeVisible()
})

test("a ?site= link gives a reader the config viewer and an administrator the form", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  const asked: string[] = []
  await page.route("**/api/v1/proxy/config?**", (route) => {
    asked.push("config")
    return json(route, { content: "server {\n    server_name app.example.com;\n}\n" })
  })
  await page.route("**/api/v1/proxy/sites/app.example.com", (route) => {
    asked.push("form")
    return route.fulfill({ status: 503, contentType: "application/json", body: "{}" })
  })
  await page.route("**/api/v1/proxy/sites/preview", (route) => {
    asked.push("preview")
    return route.fulfill({ status: 403, contentType: "application/json", body: "{}" })
  })
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, { ...user, capabilities: ["read"], user: { ...user.user, role: "viewer" } }),
  )
  await page.goto("/proxy/sites?site=app.example.com")
  const viewer = page.getByRole("dialog")
  await expect(viewer.locator(".monaco-editor .view-lines")).toContainText(
    "server_name app.example.com",
    editorLoad,
  )
  await expect(viewer.getByRole("button", { name: /^Save/ })).toHaveCount(0)
  expect(asked).toEqual(["config"])
  // The card itself opens the same viewer for a reader.
  await page.keyboard.press("Escape")
  await expect(viewer).toHaveCount(0)
  await card(page, "app.example.com").getByRole("button", { name: "Open app.example.com" }).click()
  await expect(page.getByRole("dialog").locator(".monaco-editor .view-lines")).toContainText(
    "server_name",
    editorLoad,
  )

  await page.unroute("**/api/v1/auth/session")
  asked.length = 0
  await page.goto("/proxy/sites?site=app.example.com")
  await expect(page.getByRole("dialog")).toBeVisible()
  await expect.poll(() => asked.includes("form")).toBe(true)
  expect(asked).not.toContain("config")
})

test("every place nginx reads a site from is listed, a link to nothing first", async ({ page }) => {
  await mockProxy(page, { included: true })
  await serveSites(page, layouts)
  let removed: string | undefined
  await page.route("**/api/v1/proxy/vhosts/ghost/link", (route) => {
    removed = route.request().method()
    return json(route, {
      name: "ghost",
      enabled: false,
      reloaded: true,
      reload: { validation: { ...brokenTest, valid: true }, reloaded: true, output: "" },
    })
  })
  await page.route("**/api/v1/proxy/config?**", (route) => json(route, { content: "server {}\n" }))
  await page.goto("/proxy/sites")

  const cards = page
    .getByRole("list", { name: "Needs attention" })
    .locator("[data-slot='choice-row']")
  await expect(cards.first()).toContainText("ghost")
  const ghost = card(page, "ghost")
  await expect(ghost.getByText("broken link", { exact: true })).toBeVisible()
  await expect(ghost).toContainText(
    "sites-enabled/ghost points at /etc/nginx/sites-available/gone, which is missing, so nginx refuses every reload until the link is removed.",
  )
  // Nothing to open: there is no file behind it.
  await expect(ghost.getByRole("button", { name: "Raw config" })).toHaveCount(0)
  await expect(ghost.getByRole("button", { name: /^Open / })).toHaveCount(0)

  const renamed = card(page, "renamed.example.com")
  await expect(renamed.getByText("stale link", { exact: true })).toBeVisible()
  await expect(renamed).toContainText(
    "points at /etc/nginx/sites-available/old.example.com, so nginx serves that file instead of this one.",
  )
  // The enable points the link back at this file.
  await expect(
    (await openMenu(page, "renamed.example.com")).getByRole("menuitem", { name: "Enable" }),
  ).toBeVisible()
  await page.keyboard.press("Escape")

  // A conf.d file on a Debian host is read by nginx and saved by its editor,
  // not the form, which would write it to sites-available instead.
  const extra = card(page, "extra.conf")
  await expect(extra).toContainText("nginx site in conf.d")
  await expect(extra.getByRole("button", { name: "Edit" })).toHaveCount(0)
  const extraMenu = await openMenu(page, "extra.conf")
  await expect(extraMenu.getByRole("menuitem", { name: "Delete" })).toBeVisible()
  await expect(extraMenu.getByRole("menuitem", { name: "Duplicate" })).toHaveCount(0)
  await page.keyboard.press("Escape")

  // A file copied into sites-enabled is served, has its editor, and is
  // never deleted from here.
  const copied = card(page, "copied-in")
  await expect(copied).toContainText("nginx site in sites-enabled")
  await expect(copied.getByText("always on", { exact: true })).toBeVisible()
  const copiedMenu = await openMenu(page, "copied-in")
  await expect(
    copiedMenu.getByRole("menuitem", { name: /Delete|Remove link|Disable/ }),
  ).toHaveCount(0)
  await page.keyboard.press("Escape")
  await copied.getByRole("button", { name: "Open copied-in" }).click()
  await expect(page.getByRole("dialog").locator(".monaco-editor .view-lines")).toContainText(
    "server",
    editorLoad,
  )
  await page.keyboard.press("Escape")

  // Nothing is disabled, and the reading does not call that "every site serving".
  await expect(page.getByText("none, but 2 broken links")).toBeVisible()
  await page.getByRole("button", { name: /^Broken links/ }).click()
  await expect(page.locator("[data-slot='choice-row']")).toHaveCount(2)

  await (await openMenu(page, "ghost")).getByRole("menuitem", { name: "Remove link" }).click()
  const confirm = page.getByRole("dialog", { name: "Remove sites-enabled/ghost" })
  await expect(confirm).toContainText("Removing it is what lets nginx load its configuration again")
  await confirm.getByRole("button", { name: "Remove and reload" }).click()
  await expect(page.getByText("sites-enabled/ghost removed", { exact: true })).toBeVisible()
  expect(removed).toBe("DELETE")
  await expect(page.getByText("Remove sites-enabled/ghost completed")).toHaveCount(0)
})

test("the overview names a link to nothing as critical", async ({ page }) => {
  await mockProxy(page, { included: true })
  await serveSites(page, layouts)
  await page.goto("/proxy")
  await expect(page.getByText("sites-enabled/ghost points at a file that is gone")).toBeVisible()
  // Broken rather than merely "on disk but not serving".
  await expect(page.getByText("renamed.example.com is on disk but not serving")).toHaveCount(0)
  await expect(
    page.getByText("renamed.example.com is not what nginx serves under its name"),
  ).toBeVisible()
})

test("the password-file warning says what nginx really does without the file", async ({ page }) => {
  await mockShowcase(page)
  await page.goto("/proxy/sites")
  await page.getByRole("button", { name: "More actions", exact: true }).click()
  await page.getByRole("menuitem", { name: "Delete file" }).click()
  const confirm = page.getByRole("dialog", { name: "Delete staging" })
  await expect(confirm).toContainText(
    "A site still pointing at this file keeps serving and refuses every login",
  )
  await expect(confirm).not.toContainText("stops nginx from starting")
})

for (const width of [390, 1280]) {
  test(`every kind of entry fits the sites list at ${width}`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 1000 })
    await mockProxy(page, { included: true })
    await serveSites(page, layouts)
    await page.goto("/proxy/sites")
    await expect(card(page, "ghost")).toBeVisible()
    expect(
      await page
        .locator("[data-slot='page']")
        .evaluate((element) => element.scrollWidth <= element.clientWidth + 1),
    ).toBe(true)
    await page.screenshot({ path: testInfo.outputPath(`sites-${width}.png`), fullPage: true })
  })
}
