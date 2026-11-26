import { expect, test, type Page } from "@playwright/test"
import { legacySpec, sitePreview, siteSpec } from "./fixtures/proxy/siteform"
import { json, mockProxy, user, vhosts } from "./proxy-fixtures"
import { risen } from "./risen"

/**
 * A site's own page: its views in the address, the line it opens on, the
 * Sites list beside it, and its Settings — the form that was a sheet over
 * the list — laid out as a deployment's settings are.
 */

const [app] = vhosts

const readOnly = (page: Page) =>
  page.route("**/api/v1/auth/session", (route) =>
    json(route, { ...user, capabilities: ["read"], user: { ...user.user, role: "viewer" } }),
  )

/** The line the page opens on. */
const identity = (page: Page) => page.getByRole("region", { name: "About app.example.com" })

const parts = (page: Page) => page.getByRole("navigation", { name: "Parts of the settings" })

const bar = (page: Page) => page.getByRole("region", { name: "Save changes" })

/** app.example.com with its file on disk exactly as the form writes it. */
async function inStep(page: Page, read: Record<string, unknown> = {}) {
  const content = "# Site: app.example.com\nserver {\n    server_name app.example.com;\n}\n"
  await page.route("**/api/v1/proxy/sites/app.example.com", (route) =>
    json(route, {
      spec: siteSpec(),
      managed: true,
      content,
      warnings: [],
      enabled: true,
      digest: "d1",
      lossless: true,
      dropped: [],
      ...read,
    }),
  )
  await page.route("**/api/v1/proxy/sites/preview", (route) =>
    json(route, sitePreview(route.request().postDataJSON().spec, { digest: "d1", ...read })),
  )
}

test("a site opens on its Overview, and each view has an address", async ({ page }) => {
  await mockProxy(page, { included: true })
  await page.goto("/proxy/sites/app.example.com")
  const views = page.getByRole("tablist", { name: "Views of app.example.com" })
  await expect(views.getByRole("tab")).toHaveText(["Overview", "Logs", "Settings"])
  await expect(views.getByRole("tab", { name: "Overview" })).toHaveAttribute(
    "aria-selected",
    "true",
  )
  // The picture of how a request reaches it leads; no row of figures does.
  await expect(page.locator('[data-slot="stat-grid"]')).toHaveCount(0)
  await expect(page.getByText("Visitors", { exact: true })).toBeVisible()

  await views.getByRole("tab", { name: "Logs" }).click()
  await expect(page).toHaveURL(/\/proxy\/sites\/app\.example\.com\?tab=logs$/)
  await expect(page.locator('[data-slot="site-logs"]')).toBeVisible()

  await views.getByRole("tab", { name: "Settings" }).click()
  await expect(page).toHaveURL(/\/proxy\/sites\/app\.example\.com\?tab=settings$/)
  await expect(page.getByRole("textbox", { name: "Domains" })).toHaveValue("app.example.com")

  // An address that names a view opens on it, and one that names none on the Overview.
  await page.goto("/proxy/sites/app.example.com?tab=logs")
  await expect(views.getByRole("tab", { name: "Logs" })).toHaveAttribute("aria-selected", "true")
  await page.goto("/proxy/sites/app.example.com?tab=requests")
  await expect(views.getByRole("tab", { name: "Overview" })).toHaveAttribute(
    "aria-selected",
    "true",
  )
})

test("the line a site opens on carries its state, one command and the rest in a menu", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await page.goto("/proxy/sites/app.example.com")
  const line = identity(page)
  await expect(line).toHaveAttribute("data-layout", "project")
  await expect(line.getByText("serving", { exact: true })).toBeVisible()
  await expect(line.getByText("reverse proxy")).toBeVisible()
  // One command with a face; everything else is a word in the menu.
  await expect(line.getByRole("button")).toHaveCount(2)
  await line.getByRole("button", { name: "More actions for app.example.com" }).click()
  // What changes the site is here as it is in the list: the switch, a
  // rename and a delete, behind the menu since none of them is the command.
  await expect(page.getByRole("menuitem")).toHaveText([
    "Raw config",
    "Open site",
    "TLS report",
    "Disable",
    "Rename",
    "Download",
    "Delete",
  ])
  await page.keyboard.press("Escape")

  // Edit is the Settings view.
  await line.getByRole("button", { name: "Edit", exact: true }).click()
  await expect(page).toHaveURL(/\?tab=settings$/)
  await expect(parts(page)).toBeVisible()
  // On Settings the form is what is on screen, so the line does not offer to
  // open it: not as its command, and not in the menu, where the rest still is.
  await expect(line.getByRole("button", { name: "Edit", exact: true })).toHaveCount(0)
  await line.getByRole("button", { name: "More actions for app.example.com" }).click()
  const offered = await page.getByRole("menuitem").allTextContents()
  expect(offered).not.toContain("Edit")
  expect(offered).toContain("Download")
  await page.keyboard.press("Escape")
  // Back on the Overview it is the command again.
  await page.getByRole("tab", { name: "Overview" }).click()
  await expect(line.getByRole("button", { name: "Edit", exact: true })).toBeVisible()
})

test("a read-only account gets a site's Overview and Logs, and nothing that changes it", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await readOnly(page)
  // Even from the address that asks for the form.
  await page.goto("/proxy/sites/app.example.com?tab=settings")
  const views = page.getByRole("tablist", { name: "Views of app.example.com" })
  await expect(views.getByRole("tab")).toHaveText(["Overview", "Logs"])
  await expect(views.getByRole("tab", { name: "Overview" })).toHaveAttribute(
    "aria-selected",
    "true",
  )
  await expect(parts(page)).toHaveCount(0)
  await expect(page.getByRole("textbox", { name: "Domains" })).toHaveCount(0)
  const line = identity(page)
  await expect(line.getByRole("button", { name: "Edit", exact: true })).toHaveCount(0)
  // The file is theirs to read.
  await expect(line.getByRole("button", { name: "View config" })).toBeVisible()
  await line.getByRole("button", { name: "More actions for app.example.com" }).click()
  await expect(page.getByRole("menuitem")).toHaveText(["Open site"])
})

test("the address that opened the site's form in a sheet lands on its Settings", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await page.goto("/proxy/sites?site=app.example.com")
  await expect(page).toHaveURL(/\/proxy\/sites\/app\.example\.com\?tab=settings$/)
  await expect(page.getByRole("textbox", { name: "Domains" })).toHaveValue("app.example.com")
  await expect(page.getByRole("dialog")).toHaveCount(0)
})

test("the Sites list stands beside a site, and the next one opens on the view on screen", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 900 })
  await mockProxy(page, { included: true })
  await page.goto("/proxy/sites/app.example.com?tab=logs")
  const rail = page.getByRole("navigation", { name: "Sites", exact: true })
  await expect(rail.getByRole("link")).toHaveCount(3)
  await expect(rail.getByRole("link", { name: "app.example.com" })).toHaveAttribute(
    "aria-current",
    "page",
  )
  await expect(rail.getByText("3 sites · worst first")).toBeVisible()
  // Every row is one height, whatever its second line says.
  const heights = await rail
    .getByRole("link")
    .evaluateAll((rows) => rows.map((row) => row.getBoundingClientRect().height))
  expect(new Set(heights).size).toBe(1)

  await rail.getByRole("link", { name: "shop.example.com" }).click()
  await expect(page).toHaveURL(/\/proxy\/sites\/just-dashboard-shop\?tab=logs$/)

  // Alt+↑ walks the list, and the list's own search narrows it.
  await page.keyboard.press("Alt+ArrowUp")
  await expect(page).toHaveURL(/\/proxy\/sites\/app\.example\.com\?tab=logs$/)
  await rail.getByLabel("Search the sites").fill("legacy")
  await expect(rail.getByRole("link")).toHaveCount(1)
  await expect(rail.getByText("1 of 3")).toBeVisible()
})

for (const width of [1280, 390]) {
  test(`a site's line is in one place while its page loads and once it has at ${width}`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 })
    await mockProxy(page, { included: true })
    let release = () => {}
    const held = new Promise<void>((resolve) => (release = resolve))
    await page.route("**/api/v1/proxy/vhosts", async (route) => {
      await held
      await json(route, vhosts)
    })
    await page.goto("/proxy/sites/app.example.com")
    const line = page.locator('[data-slot="host-identity"]')
    await expect(line).toContainText("Reading app.example.com")
    await risen(page)
    const loading = await line.boundingBox()
    release()
    await expect(line).toContainText("reverse proxy")
    await risen(page)
    const loaded = await line.boundingBox()
    expect(loaded!.y).toBe(loading!.y)
    expect(loaded!.x).toBe(loading!.x)
    // The loading line keeps a place for the state and the verbs the loaded
    // one has, so nothing under it moves when the site lands, on a phone too.
    expect(loaded!.height).toBeCloseTo(loading!.height, 0)
    // The rail is a wide screen's; on a phone the page is the one pane.
    await expect(page.getByRole("navigation", { name: "Sites", exact: true })).toHaveCount(
      width === 390 ? 0 : 1,
    )
  })
}

test("a site's Settings are one column of sections, split across parts", async ({ page }) => {
  await mockProxy(page, { included: true })
  await inStep(page)
  await page.goto("/proxy/sites/app.example.com?tab=settings")
  await expect(parts(page).getByRole("link")).toHaveText([
    "General",
    "HTTPS",
    "Access",
    "Hardening",
    "Headers & pages",
    "Paths",
    "File",
  ])
  const sections = page.locator('[data-slot="site-settings"] [data-slot="setting"] h2')
  await expect(sections).toHaveText(["Names", "What it serves", "Behaviour"])
  // What a field is for is behind its ⓘ, not a sentence under it.
  await expect(page.getByText("nginx refuses a larger request body with a 413.")).toHaveCount(0)
  await page.getByRole("button", { name: "What this setting does" }).first().hover()
  await expect(page.getByRole("tooltip")).toBeVisible()
  // In step with its file, a site has nothing to save: no bar, and no strip.
  await expect(bar(page)).toHaveCount(0)
  await expect(page.getByRole("region", { name: "Unsaved changes" })).toHaveCount(0)

  const expected: [string, RegExp, string[]][] = [
    ["HTTPS", /view=https$/, ["Encryption"]],
    ["Access", /view=access$/, ["Who may reach it", "Crawlers & hotlinking", "Visitor address"]],
    ["Hardening", /view=hardening$/, ["Hardening", "Limits", "Caching"]],
    ["Headers & pages", /view=headers$/, ["Headers", "Pages & files"]],
    ["Paths", /view=paths$/, ["Paths that go somewhere else", "Anything else"]],
    ["File", /view=file$/, ["The file"]],
  ]
  for (const [name, address, titles] of expected) {
    await parts(page).getByRole("link", { name }).click()
    await expect(page).toHaveURL(address)
    await expect(sections).toHaveText(titles)
    await expect(parts(page).getByRole("link", { name })).toHaveAttribute("aria-current", "page")
  }
  // The file a save would write, rendered by the server, with what nginx -t cannot check.
  await expect(page.getByRole("tab", { name: "nginx config" })).toBeVisible()
  await expect(page.getByRole("tab", { name: "Checks" })).toBeVisible()
  await expect(page.getByText("/etc/nginx/sites-available/app.example.com")).toBeVisible()
})

test("an edit is named where it is, counted on the bar, and saved from any part", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await inStep(page)
  const saved: Record<string, unknown>[] = []
  await page.route("**/api/v1/proxy/sites/", (route) => {
    const body = route.request().postDataJSON()
    saved.push(body)
    return json(route, {
      name: "app.example.com",
      path: "/etc/nginx/sites-available/app.example.com",
      content: "",
      warnings: [],
      enabled: true,
      reloaded: body.reload,
    })
  })
  await page.goto("/proxy/sites/app.example.com?tab=settings")
  await page.getByLabel("Send it to").fill("http://127.0.0.1:4000")
  const strip = page.getByRole("region", { name: "Unsaved changes" })
  await expect(strip).toContainText("1 unsaved change")
  await expect(bar(page)).toContainText("1 unsaved change")
  await expect(parts(page).getByRole("link", { name: "General, unsaved changes" })).toBeVisible()

  // The save is on the bar whichever part is open.
  await parts(page).getByRole("link", { name: "Access" }).click()
  await expect(strip.getByRole("link", { name: "General" })).toBeVisible()
  await bar(page).getByRole("button", { name: "Save only" }).click()
  await expect(page.getByText("app.example.com saved", { exact: true })).toBeVisible()
  expect(saved[0]).toMatchObject({
    reload: false,
    overwrite: true,
    baseDigest: "d1",
    spec: { upstream: "http://127.0.0.1:4000" },
  })
  // Saved, the page stays on the part it was on.
  await expect(page).toHaveURL(/tab=settings&view=access$/)
})

test("a failed check holds the save, and the bar leads to where it is allowed", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await inStep(page)
  await page.route("**/api/v1/proxy/sites/preflight", (route) =>
    json(route, {
      checks: [
        {
          id: "upstream:127.0.0.1:4000",
          level: "fail",
          blocking: true,
          title: "Nothing answers on 127.0.0.1:4000",
          detail: "The connection was refused.",
        },
      ],
    }),
  )
  await page.goto("/proxy/sites/app.example.com?tab=settings")
  await page.getByLabel("Send it to").fill("http://127.0.0.1:4000")
  await expect(bar(page)).toContainText("1 check failed.")
  await expect(bar(page).getByRole("button", { name: "Save and reload" })).toBeDisabled()
  await expect(parts(page).getByRole("link", { name: /^File/ })).toContainText("1")
  await bar(page).getByRole("link", { name: "allow it under Checks" }).click()
  await expect(page).toHaveURL(/view=file$/)
  await expect(page.getByText("Nothing answers on 127.0.0.1:4000")).toBeVisible()
  await page.getByRole("button", { name: "Allow saving anyway" }).click()
  await expect(bar(page).getByRole("button", { name: "Save and reload" })).toBeEnabled()
})

test("a disabled site with nothing to save can still be enabled from its Settings", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  const content = "# Site: legacy.example.com\nserver {\n    server_name legacy.example.com;\n}\n"
  await page.route("**/api/v1/proxy/vhosts", (route) =>
    json(
      route,
      vhosts.map((v) => (v.name === "legacy.example.com" ? { ...v, enabled: false } : v)),
    ),
  )
  await page.route("**/api/v1/proxy/sites/legacy.example.com", (route) =>
    json(route, {
      spec: legacySpec,
      managed: true,
      content,
      warnings: [],
      enabled: false,
      digest: "l1",
      lossless: true,
      dropped: [],
    }),
  )
  await page.route("**/api/v1/proxy/sites/preview", (route) =>
    json(route, sitePreview(route.request().postDataJSON().spec, { enabled: false, digest: "l1" })),
  )
  const saved: Record<string, unknown>[] = []
  await page.route("**/api/v1/proxy/sites/", (route) => {
    saved.push(route.request().postDataJSON())
    return json(route, {
      name: "legacy.example.com",
      path: "/etc/nginx/sites-available/legacy.example.com",
      content: "",
      warnings: [],
      enabled: true,
      reloaded: true,
    })
  })
  await page.goto("/proxy/sites/legacy.example.com?tab=settings")
  await expect(bar(page)).toContainText("Disabled")
  // Nothing to save as it is, so that is not offered.
  await expect(bar(page).getByRole("button")).toHaveCount(1)
  await bar(page).getByRole("button", { name: "Save and enable" }).click()
  await expect(page.getByText("legacy.example.com is live")).toBeVisible()
  expect(saved[0]).toMatchObject({ enable: "enable", reload: true })
})

test("a deployment's route says who writes it before its Settings open", async ({ page }) => {
  await mockProxy(page, { included: true })
  const owned = {
    ...app,
    owner: { projectId: 3, environmentId: 7, project: "shop", environment: "production" },
  }
  await page.route("**/api/v1/proxy/vhosts", (route) => json(route, [owned]))
  await page.goto("/proxy/sites/app.example.com?tab=settings")
  await expect(page.getByText("shop writes this route")).toBeVisible()
  await expect(page.getByRole("textbox", { name: "Domains" })).toHaveCount(0)
  await expect(page.getByRole("link", { name: "Open deployment" })).toHaveAttribute(
    "href",
    "/deploy/3",
  )
  await page.getByRole("button", { name: "Edit anyway" }).click()
  await expect(page.getByRole("textbox", { name: "Domains" })).toHaveValue("app.example.com")
  // Kept for the tab, so another part of the form does not ask again.
  await parts(page).getByRole("link", { name: "HTTPS" }).click()
  await expect(page.getByRole("switch", { name: "Serve over HTTPS" })).toBeVisible()
})

test("a site that keeps no log of its own says so in one line, with the way to turn it on", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await page.route("**/api/v1/proxy/sites/app.example.com", (route) =>
    json(route, {
      spec: siteSpec({ accessLog: false }),
      managed: true,
      content: "",
      warnings: [],
      enabled: true,
      digest: "d1",
    }),
  )
  await page.goto("/proxy/sites/app.example.com?tab=logs")
  const note = page.locator('[data-slot="site-unrecorded"]')
  await expect(note).toContainText("This site keeps no access log")
  const box = await note.boundingBox()
  expect(box!.height).toBeLessThan(48)
  await note.getByRole("link", { name: "Turn it on" }).click()
  await expect(page).toHaveURL(/tab=settings&view=hardening$/)
  await expect(page.getByRole("switch", { name: "Access log" })).toBeVisible()
})

test("a site's Settings and its save bar fit a phone", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockProxy(page, { included: true })
  await inStep(page)
  await page.goto("/proxy/sites/app.example.com?tab=settings")
  await page.getByLabel("Send it to").fill("http://127.0.0.1:4000")
  await expect(bar(page).getByRole("button", { name: "Save and reload" })).toBeInViewport({
    ratio: 1,
  })
  await expect(bar(page).getByRole("button", { name: "Discard" })).toBeInViewport({ ratio: 1 })
  for (const name of ["HTTPS", "Access", "Hardening", "Headers & pages", "Paths", "File"]) {
    await parts(page)
      .getByRole("link", { name: new RegExp(`^${name}`) })
      .click()
    await expect(page).toHaveURL(/view=/)
    expect(
      await page.evaluate(() => {
        const main = document.querySelector('[data-slot="sidebar-inset"]')!
        return main.scrollWidth <= main.clientWidth + 1
      }),
    ).toBe(true)
  }
})

/** A route on the Docker Caddy ingress, which has no file on the host. */
const ingressRoute = {
  kind: "caddy",
  path: "",
  enabled: true,
  formEditable: false,
  listen: ["80", "443"],
  tls: true,
  modified: "",
  size: 0,
}
/** Its project was deleted, so no environment names it. */
const leftover = {
  ...ingressRoute,
  name: "just-dashboard-env-450.conf",
  serverNames: ["jellyfin.example.com"],
  upstreams: ["http://10.0.0.10:8096"],
}
const deployed = {
  ...ingressRoute,
  name: "just-dashboard-env-175.conf",
  serverNames: ["shop.example.com"],
  upstreams: ["http://10.0.0.3:3000"],
  owner: { projectId: 3, environmentId: 175, project: "shop", environment: "production" },
}

test("a leftover route's page is titled by its domain, says what it is, and removes it", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  const sites: unknown[] = [app, leftover, deployed]
  await page.route("**/api/v1/proxy/vhosts", (route) => json(route, sites))
  const calls: string[] = []
  await page.route("**/api/v1/proxy/vhosts/*/deployment-route", (route) => {
    calls.push(`${route.request().method()} ${new URL(route.request().url()).pathname}`)
    sites.splice(1, 1)
    return json(route, { name: leftover.name, certificates: ["jellyfin.example.com"] })
  })
  await page.goto(`/proxy/sites/${leftover.name}`)

  // The domain is the title; the file is a fact on the line.
  await expect(page.getByRole("heading", { name: "jellyfin.example.com" })).toBeVisible()
  const line = page.getByRole("region", { name: "About jellyfin.example.com" })
  await expect(line.getByText("leftover", { exact: true })).toBeVisible()
  await expect(line.getByText("serving", { exact: true })).toHaveCount(0)
  await expect(line.getByText(leftover.name, { exact: true })).toBeVisible()
  await expect(line).toContainText("Leftover · its deployment was deleted")
  await expect(page.getByText("Docker Caddy ingress")).toHaveCount(0)

  // No destructive verb is the line's command; the page's one line says what
  // the route is beside the button that removes it.
  const origin = page.locator("[data-slot='site-origin']")
  await expect(origin).toContainText("Left by a deleted deployment")
  await expect(line.getByRole("button", { name: /Remove/ })).toHaveCount(0)
  await origin.getByRole("button", { name: "Remove", exact: true }).click()
  const confirm = page.getByRole("dialog", { name: "Remove jellyfin.example.com" })
  await expect(confirm).toContainText("jellyfin.example.com stops answering")
  await confirm.getByRole("button", { name: "Remove route" }).click()
  await expect
    .poll(() => calls)
    .toEqual([`DELETE /api/v1/proxy/vhosts/${leftover.name}/deployment-route`])
  await expect(page.getByText("jellyfin.example.com removed")).toBeVisible()
  // Gone, so the list is where to be.
  await expect(page).toHaveURL(/\/proxy\/sites$/)
})

test("a live deployment's route says where its domain is removed", async ({ page }) => {
  await mockProxy(page, { included: true })
  await page.route("**/api/v1/proxy/vhosts", (route) => json(route, [app, leftover, deployed]))
  await page.goto(`/proxy/sites/${deployed.name}`)

  await expect(page.getByRole("heading", { name: "shop.example.com" })).toBeVisible()
  const line = page.getByRole("region", { name: "About shop.example.com" })
  await expect(line).toContainText("shop · production")
  await expect(line.getByText("serving", { exact: true })).toBeVisible()
  const origin = page.locator("[data-slot='site-origin']")
  await expect(origin).toContainText("Written by shop on every deploy.")
  await expect(
    origin.getByRole("link", { name: "Remove the domain in shop · Domains" }),
  ).toHaveAttribute("href", "/deploy/3/settings/domains")
  // Nothing here removes it: not on the line, and not as a button in the body.
  await expect(origin.getByRole("button")).toHaveCount(0)
  await line.getByRole("button", { name: "More actions for shop.example.com" }).click()
  await expect(page.getByRole("menuitem", { name: /Remove|Delete/ })).toHaveCount(0)
})

test("a site deleted from its own page leaves for the list", async ({ page }) => {
  await mockProxy(page, { included: true })
  const sites: unknown[] = [...vhosts]
  await page.route("**/api/v1/proxy/vhosts", (route) => json(route, sites))
  let deleted: string | undefined
  await page.route("**/api/v1/proxy/sites/legacy.example.com", (route) => {
    if (route.request().method() !== "DELETE") return route.fallback()
    deleted = new URL(route.request().url()).pathname
    sites.splice(1, 1)
    return json(route, {
      name: "legacy.example.com",
      reload: { validation: { valid: true, output: "" }, reloaded: true, output: "" },
    })
  })
  await page.goto("/proxy/sites/legacy.example.com")
  await page.getByRole("button", { name: "More actions for legacy.example.com" }).click()
  await page.getByRole("menuitem", { name: "Delete" }).click()
  const confirm = page.getByRole("dialog", { name: "Delete legacy.example.com" })
  await confirm.getByRole("button", { name: "Delete and reload" }).click()
  await expect.poll(() => deleted).toBe("/api/v1/proxy/sites/legacy.example.com")
  await expect(page).toHaveURL(/\/proxy\/sites$/)
})
