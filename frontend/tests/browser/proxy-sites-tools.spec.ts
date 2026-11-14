import { expect, test, type Page } from "@playwright/test"
import { json, mockProxy, mockShowcase, now, user } from "./proxy-fixtures"
import { accessDir, accessLists, clientAddress } from "./fixtures/proxy/sites-tools"

/**
 * The Sites page's access lists: named allow and deny lists and a password
 * file that sites include, so one edit changes every site that uses them.
 *
 * What these hold the page to: a list is saved exactly as the form shows it,
 * with what was typed but not yet added; a list that refuses the person
 * editing it says so before it is saved; nginx's refusal and a failed reload
 * are reported as what they are; a list a site still includes is not offered
 * for deletion; and only an administrator sees any of it.
 */

type List = (typeof accessLists)[number] & {
  authFile?: string
  realm?: string
  authFileMissing?: boolean
  handWritten?: string
}

const staging = [
  { name: "staging", path: "/etc/nginx/jd-auth/staging", users: ["operator"], usedBy: [] },
]

const passed = { valid: true, output: "", command: "nginx -t", diagnostics: [], warnings: 0 }

/**
 * The access-list API over a list of lists that saves and deletes take
 * effect on, so the page's read after each mutation sees it. `answer` stands
 * in for the server's reply to a save.
 */
async function accessApi(
  page: Page,
  initial: List[],
  answer?: (list: List) => { status: number; body: unknown },
) {
  let lists: List[] = structuredClone(initial)
  const requests: { method: string; name: string; body?: unknown }[] = []
  await page.route(/\/api\/v1\/proxy\/access-lists\//, async (route) => {
    const request = route.request()
    const name = decodeURIComponent(new URL(request.url()).pathname.split("/").pop() ?? "")
    if (request.method() === "GET") {
      return json(route, { dir: accessDir, clientAddress, lists })
    }
    if (request.method() === "DELETE") {
      requests.push({ method: "DELETE", name })
      lists = lists.filter((list) => list.name !== name)
      return route.fulfill({ status: 204 })
    }
    const body = request.postDataJSON()
    requests.push({ method: request.method(), name, body })
    const existing = lists.find((list) => list.name === name)
    const list: List = {
      name,
      path: `${accessDir}/${name}.conf`,
      include: `include ${accessDir}/${name}.conf;`,
      allow: body.allow,
      deny: body.deny,
      authFile: body.authFile,
      realm: body.authFile ? (body.realm ?? "Restricted") : undefined,
      satisfy: body.satisfy,
      usedBy: existing?.usedBy ?? [],
      modified: now,
    }
    const reply = answer?.(list) ?? {
      status: 200,
      body: { list, validation: passed, reloaded: true },
    }
    if (reply.status === 200) {
      lists = [...lists.filter((other) => other.name !== name), list].sort((a, b) =>
        a.name.localeCompare(b.name),
      )
    }
    return route.fulfill({
      status: reply.status,
      contentType: "application/json",
      body: JSON.stringify(reply.body),
    })
  })
  return requests
}

const section = (page: Page) =>
  page.locator("section").filter({ has: page.getByRole("heading", { name: "Access lists" }) })

const row = (page: Page, name: string) =>
  section(page)
    .getByRole("listitem")
    .filter({ has: page.getByText(`${accessDir}/${name}.conf`, { exact: true }) })

/** Opens a list's sheet, where what it holds is read and changed. */
async function open(page: Page, name: string) {
  await row(page, name)
    .getByRole("button", { name: `Open ${name}` })
    .click()
  return page.getByRole("dialog", { name: `Edit ${name}` })
}

const toast = (page: Page, title: string) =>
  page.locator("[data-sonner-toast]").filter({ hasText: title })

test("a new list is saved as the form shows it and listed with its include line", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  const requests = await accessApi(page, [])
  await page.route("**/api/v1/proxy/auth-files/", (route) => json(route, staging))
  await page.goto("/proxy/sites")

  await expect(section(page).getByText("No access lists yet")).toBeVisible()
  await section(page).getByRole("button", { name: "New list" }).click()
  const dialog = page.getByRole("dialog", { name: "New access list" })
  const save = dialog.getByRole("button", { name: "Save" })
  await expect(save).toBeDisabled()
  await expect(dialog.getByText("Add an address or a password file.")).toBeVisible()

  const name = dialog.getByLabel("Name", { exact: true })
  await name.fill("Office")
  await expect(dialog.getByText("Lowercase letters, digits, dashes or underscores")).toBeVisible()
  await name.fill("office")

  const allow = dialog.getByLabel("Allow only these", { exact: true })
  await allow.fill("10.0.0.5/8")
  await allow.press("Enter")
  await expect(
    dialog.getByText("10.0.0.5/8 has bits set past its /8 — the range is 10.0.0.0/8"),
  ).toBeVisible()
  // A pasted run of ranges goes in whole.
  await allow.fill("10.0.0.0/8, 192.168.1.0/24")
  await allow.press("Enter")
  await expect(dialog.getByRole("button", { name: "Remove 192.168.1.0/24" })).toBeVisible()
  await expect(allow).toHaveValue("")
  await allow.fill("10.0.0.0/8")
  await dialog.getByRole("button", { name: "Add to allow only these" }).click()
  await expect(dialog.getByText("10.0.0.0/8 is already on the list")).toBeVisible()
  await allow.fill("")

  // Where the dashboard sees this browser is outside the list.
  await expect(dialog.getByText("This list refuses you")).toBeVisible()
  await expect(dialog).toContainText(`The dashboard sees you at ${clientAddress}`)

  await dialog.getByLabel("Password file").click()
  await page.getByRole("option", { name: "staging" }).click()
  await expect(dialog.getByLabel("Login prompt")).toHaveAttribute("placeholder", "Restricted")
  await dialog.getByRole("radio", { name: "Either one" }).click()
  await expect(dialog.getByText("even to paths a site refuses everyone")).toBeVisible()
  await expect(dialog.getByText("This list refuses you")).toHaveCount(0)
  await expect(dialog.getByText("you would sign in with a login from staging")).toBeVisible()
  await expect(dialog.getByText("No site uses it until one includes it.")).toBeVisible()

  await save.click()
  await expect(toast(page, "office saved")).toContainText(
    "No site includes it yet: paste its include line into a site.",
  )
  await expect(dialog).toBeHidden()
  expect(requests).toEqual([
    {
      method: "PUT",
      name: "office",
      body: {
        allow: ["10.0.0.0/8", "192.168.1.0/24"],
        deny: [],
        authFile: "staging",
        satisfy: "any",
        overwrite: false,
      },
    },
  ])
  // The row says what the list is in a line; its sheet holds the rest.
  const office = row(page, "office")
  await expect(office).toContainText("2 allowed")
  await expect(office.getByTitle("staging or an allowed address")).toBeVisible()
  await expect(office).toContainText("unused")
  await expect(section(page)).toContainText("1 list")
  const listed = await open(page, "office")
  await expect(listed).toContainText(`include ${accessDir}/office.conf;`)
  await expect(listed).toContainText("No site yet")
  await page.keyboard.press("Escape")
  await expect(listed).toBeHidden()

  // A second list may not take the first one's name.
  await section(page).getByRole("button", { name: "New list" }).click()
  const second = page.getByRole("dialog", { name: "New access list" })
  await second.getByLabel("Name", { exact: true }).fill("office")
  await expect(second.getByText("There is already a list called office.")).toBeVisible()
  await expect(second.getByRole("button", { name: "Save" })).toBeDisabled()
})

test("an edit reaches every site that includes the list, with what was typed but not added", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  const requests = await accessApi(page, accessLists)
  await page.route("**/api/v1/proxy/auth-files/", (route) => json(route, staging))
  await page.goto("/proxy/sites")

  const dialog = await open(page, "office")
  await expect(dialog.getByLabel("Name", { exact: true })).toHaveCount(0)
  await expect(dialog.getByText("Saving reloads nginx for app.example.com.")).toBeVisible()
  await expect(dialog.getByLabel("Password file")).toContainText("staging")
  await expect(dialog.getByRole("radio", { name: "Either one" })).toHaveAttribute(
    "data-state",
    "on",
  )
  await dialog.getByRole("button", { name: "Remove 2001:db8::/32" }).click()
  await dialog.getByLabel("Allow only these", { exact: true }).fill("172.16.0.0/12")
  await dialog.getByRole("button", { name: "Save" }).click()

  await expect(toast(page, "office saved")).toContainText("Live on app.example.com.")
  expect(requests).toEqual([
    {
      method: "PUT",
      name: "office",
      body: {
        allow: ["10.0.0.0/8", "172.16.0.0/12"],
        deny: ["10.0.0.9"],
        authFile: "staging",
        realm: "Restricted",
        satisfy: "any",
        overwrite: true,
      },
    },
  ])
  await expect(row(page, "office")).toContainText("2 allowed · 1 denied")
  const again = await open(page, "office")
  await expect(again.getByRole("button", { name: "Remove 172.16.0.0/12" })).toBeVisible()
  await expect(again.getByRole("button", { name: "Remove 2001:db8::/32" })).toHaveCount(0)
})

test("a save nginx refuses keeps the form open and shows nginx's reason and output", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  const reason =
    'nginx refuses office where a site includes it: "auth_basic" directive is duplicate in /etc/nginx/jd-access/office.conf:7'
  const raw =
    'nginx: [emerg] "auth_basic" directive is duplicate in /etc/nginx/jd-access/office.conf:7\n' +
    "nginx: configuration file /etc/nginx/nginx.conf test failed"
  await accessApi(page, accessLists, () => ({
    status: 422,
    body: { error: { code: "invalid_config", message: reason, raw } },
  }))
  await page.route("**/api/v1/proxy/auth-files/", (route) => json(route, staging))
  await page.goto("/proxy/sites")

  const dialog = await open(page, "vpn")
  await dialog.getByLabel("Password file").click()
  await page.getByRole("option", { name: "staging" }).click()
  await dialog.getByRole("button", { name: "Save" }).click()

  await expect(toast(page, "vpn not saved")).toContainText(reason)
  // The reason stays in the form, whose dialog holds the pointer: a button
  // on the toast could not be pressed.
  const refusal = dialog.getByRole("alert").filter({ hasText: "nginx refused it" })
  await expect(refusal).toContainText(reason)
  await expect(refusal).toContainText("The list is as it was.")
  const output = refusal.getByText("configuration file /etc/nginx/nginx.conf test failed")
  await expect(output).toBeHidden()
  await refusal.getByRole("button", { name: "nginx output" }).click()
  await expect(output).toBeVisible()

  // Saving again starts from a clean slate.
  await accessApi(page, accessLists)
  await dialog.getByLabel("Password file").click()
  await page.getByRole("option", { name: "None" }).click()
  await dialog.getByRole("button", { name: "Save" }).click()
  await expect(toast(page, "vpn saved")).toBeVisible()
  await expect(dialog).toBeHidden()
})

test("a save whose reload failed says the sites keep the previous rules", async ({ page }) => {
  await mockProxy(page, { included: true })
  await accessApi(page, accessLists, (list) => ({
    status: 200,
    body: {
      list,
      validation: passed,
      reloaded: false,
      reloadError: 'reload failed: nginx: [error] invalid PID number "" in "/run/nginx.pid"',
      reload: {
        validation: passed,
        reloaded: false,
        output: 'nginx: [error] invalid PID number "" in "/run/nginx.pid"',
      },
    },
  }))
  await page.goto("/proxy/sites")

  const dialog = await open(page, "admins")
  await dialog.getByLabel("Deny", { exact: true }).fill("10.20.0.7")
  await dialog.getByRole("button", { name: "Save" }).click()

  const warning = toast(page, "admins saved, not reloaded")
  await expect(warning).toContainText(
    "If nginx is running, its sites keep the previous rules until a reload succeeds.",
  )
  await expect(warning).toContainText("invalid PID number")
  await expect(toast(page, "admins saved")).toHaveCount(1)
})

test("each list is one line, and its sheet says what it allows, who includes it and when it refuses the reader", async ({
  page,
  context,
}) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"])
  await mockShowcase(page)
  await page.goto("/proxy/sites")

  await expect(section(page)).toContainText("3 lists")
  // No sentence under the title, and every list the same height whatever it holds.
  await expect(section(page).getByText(/included by/)).toHaveCount(0)
  const heights = await section(page)
    .getByRole("listitem")
    .evaluateAll((rows) => rows.map((row) => row.getBoundingClientRect().height))
  expect(heights).toHaveLength(3)
  expect(new Set(heights).size).toBe(1)

  const office = row(page, "office")
  await expect(office).toContainText("2 allowed · 1 denied")
  await expect(office.getByTitle("staging or an allowed address")).toBeVisible()
  await expect(office).toContainText("2 sites")
  // The office list lets a password in from anywhere, so it does not lock
  // the reader out; admins has no password and does not name them.
  await expect(office.getByText("Refuses you")).toHaveCount(0)
  const admins = row(page, "admins")
  await expect(admins.getByText("Refuses you")).toBeVisible()
  const vpn = row(page, "vpn")
  await expect(vpn).toContainText("1 denied")
  await expect(vpn).toContainText("unused")
  await expect(vpn.getByText("Refuses you")).toHaveCount(0)

  await vpn.getByRole("button", { name: "More actions for vpn" }).click()
  await page.getByRole("menuitem", { name: "Copy include line" }).click()
  await expect(toast(page, "Include line copied")).toBeVisible()
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(
    `include ${accessDir}/vpn.conf;`,
  )

  // Why admins refuses the reader is in its sheet.
  const refusing = await open(page, "admins")
  await expect(refusing.getByText("This list refuses you")).toBeVisible()
  await expect(refusing).toContainText(`The dashboard sees you at ${clientAddress}`)
  await page.keyboard.press("Escape")
  await expect(refusing).toBeHidden()

  // What a list holds, and the sites that include it, each a way to that
  // site's access settings.
  const sheet = await open(page, "office")
  await expect(sheet).toContainText(`include ${accessDir}/office.conf;`)
  for (const entry of ["10.0.0.0/8", "2001:db8::/32", "10.0.0.9"]) {
    await expect(sheet.getByRole("button", { name: `Remove ${entry}` })).toBeVisible()
  }
  const used = sheet.getByRole("list", { name: "Sites using office" })
  await expect(used.getByRole("listitem").nth(1)).toContainText("legacy.example.comdisabled")
  await sheet.getByRole("button", { name: "Copy the include line for office" }).click()
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(
    `include ${accessDir}/office.conf;`,
  )
  await used.getByRole("link", { name: "app.example.com" }).click()
  await expect(page).toHaveURL(/\/proxy\/sites\/app\.example\.com\?tab=settings&view=access$/)
  await expect(page.getByRole("heading", { name: "Who may reach it" })).toBeVisible()
})

test("a list a site includes is not deleted; one no site includes is, after asking", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  const requests = await accessApi(page, accessLists)
  await page.goto("/proxy/sites")

  await row(page, "office").getByRole("button", { name: "More actions for office" }).click()
  await page.getByRole("menuitem", { name: "Delete" }).click()
  await expect(toast(page, "office is in use")).toContainText(
    "Take its include out of app.example.com and legacy.example.com before deleting it.",
  )
  await expect(page.getByRole("dialog")).toHaveCount(0)

  await row(page, "vpn").getByRole("button", { name: "More actions for vpn" }).click()
  await page.getByRole("menuitem", { name: "Delete" }).click()
  const confirm = page.getByRole("dialog", { name: "Delete vpn" })
  await expect(confirm).toContainText("No site includes vpn")
  await expect(confirm).toContainText("vpn.conf.bak")
  await confirm.getByRole("button", { name: "Delete", exact: true }).click()
  await expect(toast(page, "Delete vpn completed")).toBeVisible()
  await expect(row(page, "vpn")).toHaveCount(0)
  expect(requests).toEqual([{ method: "DELETE", name: "vpn" }])
})

test("a list whose password file is gone, or that was written by hand, says so", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  const reason =
    "its rules are written in an order or form the page would change, and nginx stops at the first rule an address matches"
  await accessApi(page, [{ ...accessLists[0], authFileMissing: true, handWritten: reason }])
  await page.route("**/api/v1/proxy/auth-files/", (route) => json(route, []))
  await page.goto("/proxy/sites")

  // The row carries the worst of the two; the sheet says both in full.
  const office = row(page, "office")
  await expect(office.getByText("Password file gone")).toBeVisible()

  const dialog = await open(page, "office")
  await expect(dialog).toContainText("Every login is refused until the list names another.")
  await expect(dialog.getByText("Written by hand")).toBeVisible()
  await expect(dialog).toContainText("Its rules are written in an order")
  await expect(dialog.getByText("staging is gone. Choose another file, or none.")).toBeVisible()
  const save = dialog.getByRole("button", { name: "Save" })
  await expect(save).toBeDisabled()
  await dialog.getByLabel("Password file").click()
  await page.getByRole("option", { name: "None" }).click()
  await expect(dialog.getByRole("radio", { name: "Either one" })).toHaveCount(0)
  await expect(save).toBeEnabled()
})

test("a list that cannot be read is an error in its own section only", async ({ page }) => {
  await mockProxy(page, { included: true })
  await page.route(/\/api\/v1\/proxy\/access-lists\//, (route) =>
    route.fulfill({
      status: 500,
      contentType: "application/json",
      body: JSON.stringify({ error: { code: "internal", message: "the lists could not be read" } }),
    }),
  )
  await page.goto("/proxy/sites")
  await expect(section(page).getByText("the lists could not be read")).toBeVisible()
  await expect(page.getByRole("heading", { name: "Password files" })).toBeVisible()
  await expect(page.getByRole("list", { name: "Sites", exact: true })).toBeVisible()
})

test("a read-only account neither sees nor asks for the access lists", async ({ page }) => {
  await mockProxy(page, { included: true })
  let asked = 0
  await page.route(/\/api\/v1\/proxy\/access-lists\//, (route) => {
    asked++
    return json(route, { dir: accessDir, clientAddress, lists: accessLists })
  })
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, { ...user, capabilities: ["read"], user: { ...user.user, role: "viewer" } }),
  )
  await page.goto("/proxy/sites")
  await expect(page.getByRole("list", { name: "Sites", exact: true })).toBeVisible()
  await expect(page.getByRole("heading", { name: "Access lists" })).toHaveCount(0)
  expect(asked).toBe(0)
})

test("the lists and their form fit a phone", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockShowcase(page)
  await page.route("**/api/v1/proxy/auth-files/", (route) => json(route, staging))
  await page.goto("/proxy/sites")
  await expect(row(page, "office")).toBeVisible()
  const overflow = await page
    .locator("[data-slot='page']")
    .evaluate((el) => el.scrollWidth - el.clientWidth)
  expect(overflow).toBeLessThanOrEqual(1)

  const dialog = await open(page, "office")
  await expect(dialog).toBeInViewport({ ratio: 1 })
  const inner = await dialog.evaluate((el) => el.scrollWidth - el.clientWidth)
  expect(inner).toBeLessThanOrEqual(1)
  await page.keyboard.press("Escape")
  await expect(dialog).toBeHidden()
})

test("the URL resolver says who may take the route from an address, layer by layer", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  const route = {
    url: "http://admin.example.com/office/",
    scheme: "http",
    host: "admin.example.com",
    port: 80,
    path: "/office/",
    outcome: "proxy",
    server: {
      names: ["admin.example.com"],
      listen: "*:80",
      match: "exact",
      file: "/etc/nginx/sites-enabled/admin",
      line: 1,
    },
    location: {
      modifier: "",
      path: "/office/",
      parents: [],
      file: "/etc/nginx/sites-enabled/admin",
      line: 9,
    },
    serves: [
      {
        text: "proxy_pass http://127.0.0.1:3000;",
        file: "/etc/nginx/sites-enabled/admin",
        line: 12,
      },
    ],
    steps: [],
    certain: true,
  }
  const asked: URLSearchParams[] = []
  await page.route("**/api/v1/proxy/resolve/access?*", (route_) => {
    const params = new URL(route_.request().url()).searchParams
    asked.push(params)
    return json(route_, {
      route,
      source: params.get("source"),
      verdict: "refused",
      summary: "Refused at Allowed addresses. Anything in front of this host is not seen here.",
      layers: [
        {
          id: "outside",
          title: "Outside this host",
          owner: "Provider, CDN or network",
          verdict: "unknown",
          detail:
            "A provider firewall, a security group, a CDN or NAT in front of this host is not visible from its configuration.",
        },
        {
          id: "firewall",
          title: "Host firewall",
          owner: "Firewall",
          ownerPath: "/network/firewall",
          verdict: "admits",
          detail: "ufw lets 10.9.0.4 reach port 80 by rule 3 (ALLOW from Anywhere).",
        },
        {
          id: "addresses",
          title: "Allowed addresses",
          owner: "Access list office",
          ownerPath: "/proxy/sites#access-lists",
          verdict: "refuses",
          detail: "10.9.0.4 is denied by the first line that matches it: nginx answers 403.",
          rules: [{ text: "deny 10.9.0.0/16;", file: "/etc/nginx/jd-access/office.conf", line: 1 }],
        },
        {
          id: "credentials",
          title: "Sign-in",
          owner: "Site form",
          verdict: "admits",
          detail: "nginx asks for no password or sign-in on this path.",
        },
      ],
    })
  })
  await page.goto("/proxy/sites")
  const resolver = page.getByTestId("route-resolver")
  await resolver.getByLabel("URL to resolve").fill("http://admin.example.com/office/")
  await resolver.getByLabel("From address").fill("10.0.0.0/8")
  await expect(
    resolver.getByText("One IPv4 or IPv6 address, as nginx sees the visitor."),
  ).toBeVisible()
  await expect(resolver.getByRole("button", { name: "Resolve" })).toBeDisabled()
  await resolver.getByLabel("From address").fill("10.9.0.4")
  await resolver.getByRole("button", { name: "Resolve" }).click()

  const access = resolver.getByRole("region", { name: "Access from 10.9.0.4" })
  await expect(access.getByText("Refused", { exact: true })).toBeVisible()
  await expect(access.getByText("Refused at Allowed addresses.", { exact: false })).toBeVisible()
  const addresses = access.getByRole("listitem").filter({ hasText: "Allowed addresses" })
  await expect(addresses).toContainText("refuses")
  await expect(addresses).toContainText("deny 10.9.0.0/16;")
  await expect(addresses.getByRole("link", { name: "Access list office" })).toHaveAttribute(
    "href",
    "/proxy/sites#access-lists",
  )
  await expect(access.getByRole("listitem").filter({ hasText: "Outside this host" })).toContainText(
    "not judged",
  )
  // The route itself is drawn below, as a plain resolve draws it.
  await expect(resolver.getByTestId("route-resolution")).toContainText(
    "proxy_pass http://127.0.0.1:3000;",
  )
  expect(asked).toHaveLength(1)
  expect(asked[0].get("source")).toBe("10.9.0.4")
})

test("backups are one line each, and a press on one is its restore", async ({ page }) => {
  await mockProxy(page, { included: true })
  const backups = [
    {
      file: "old.example.com.bak",
      layout: "sites-available",
      path: "/etc/nginx/sites-available/old.example.com.bak",
      size: 1840,
      modified: now,
      site: "old.example.com",
      siteExists: false,
      restorable: true,
    },
    {
      file: "app.example.com.bak",
      layout: "sites-available",
      path: "/etc/nginx/sites-available/app.example.com.bak",
      size: 2210,
      modified: now,
      site: "app.example.com",
      siteExists: true,
      restorable: true,
    },
    {
      file: "notes.txt~",
      layout: "conf.d",
      path: "/etc/nginx/conf.d/notes.txt~",
      size: 12,
      modified: now,
      siteExists: false,
      restorable: false,
      reason: "it holds no server block",
    },
  ]
  let deleted = ""
  await page.route(/\/api\/v1\/proxy\/site-backups/, (route) => {
    if (route.request().method() === "DELETE") {
      deleted = new URL(route.request().url()).pathname
      return route.fulfill({ status: 204 })
    }
    return json(route, backups)
  })
  await page.goto("/proxy/sites")

  const panel = page.getByRole("region", { name: "Deleted & backups" })
  await expect(panel).toContainText("3 copies")
  const rows = panel.getByRole("listitem")
  await expect(rows).toHaveCount(3)
  const heights = await rows.evaluateAll((items) =>
    items.map((item) => item.getBoundingClientRect().height),
  )
  expect(new Set(heights).size).toBe(1)
  await expect(rows.nth(0)).toContainText("restores as old.example.com")
  await expect(rows.nth(1)).toContainText("app.example.com exists")
  // One that cannot be restored has nothing to press but its menu.
  await expect(rows.nth(2)).toContainText("cannot be restored")
  await expect(
    rows.nth(2).getByTitle("Cannot be restored: it holds no server block."),
  ).toBeVisible()
  await expect(rows.nth(2).getByRole("button", { name: "Restore notes.txt~" })).toHaveCount(0)

  await rows.nth(0).getByRole("button", { name: "Restore old.example.com.bak" }).click()
  const restore = page.getByRole("dialog", { name: "Restore old.example.com.bak" })
  await expect(restore.getByLabel("Site name")).toHaveValue("old.example.com")
  await page.keyboard.press("Escape")
  await expect(restore).toBeHidden()

  await panel.getByRole("button", { name: "More actions for notes.txt~" }).click()
  await expect(page.getByRole("menuitem")).toHaveText(["Delete for good"])
  await page.getByRole("menuitem", { name: "Delete for good" }).click()
  await page
    .getByRole("dialog", { name: "Delete notes.txt~" })
    .getByRole("button", { name: "Delete", exact: true })
    .click()
  await expect.poll(() => deleted).toBe("/api/v1/proxy/site-backups/conf.d/notes.txt~")
})

test("a password file is one line, and its logins are in the sheet it opens", async ({ page }) => {
  await mockProxy(page, { included: true })
  const files = [
    {
      name: "staging",
      path: "/etc/nginx/jd-auth/staging",
      users: ["operator", "reviewer"],
      usedBy: ["app.example.com"],
    },
    { name: "empty", path: "/etc/nginx/jd-auth/empty", users: [], usedBy: [] },
  ]
  const removed: string[] = []
  await page.route(/\/api\/v1\/proxy\/auth-files\//, (route) => {
    if (route.request().method() === "DELETE") {
      removed.push(new URL(route.request().url()).pathname)
      return route.fulfill({ status: 204 })
    }
    return json(route, files)
  })
  await page.goto("/proxy/sites")

  const panel = page.getByRole("region", { name: "Password files" })
  await expect(panel).toContainText("2 files")
  // No sentence under the title, and both files one height.
  await expect(panel.getByText(/HTTP basic authentication/)).toHaveCount(0)
  const rows = panel.getByRole("listitem")
  const heights = await rows.evaluateAll((items) =>
    items.map((item) => item.getBoundingClientRect().height),
  )
  expect(heights).toHaveLength(2)
  expect(new Set(heights).size).toBe(1)
  await expect(rows.nth(0)).toContainText("2 logins")
  await expect(rows.nth(0)).toContainText("app.example.com")
  await expect(rows.nth(1)).toContainText("no logins")
  await expect(rows.nth(1)).toContainText("no site names it")

  await rows.nth(0).getByRole("button", { name: "Open staging" }).click()
  const sheet = page.getByRole("dialog", { name: "staging" })
  await expect(sheet).toContainText("/etc/nginx/jd-auth/staging")
  await expect(
    sheet.getByRole("list", { name: "Logins in staging" }).getByRole("listitem"),
  ).toHaveText(["operator", "reviewer"])
  await expect(
    sheet.getByRole("list", { name: "Sites using staging" }).getByRole("link"),
  ).toHaveAttribute("href", "/proxy/sites/app.example.com?tab=settings&view=access")

  // A login's password is changed, and a login removed, from the sheet.
  await sheet.getByRole("button", { name: "Change the password of reviewer" }).click()
  const change = page.getByRole("dialog", { name: "Change the password of reviewer" })
  await expect(change.getByLabel("New password")).toBeVisible()
  await page.keyboard.press("Escape")
  await expect(change).toBeHidden()
  await sheet.getByRole("button", { name: "Remove reviewer" }).click()
  await page
    .getByRole("dialog", { name: "Remove reviewer" })
    .getByRole("button", { name: "Remove", exact: true })
    .click()
  await expect.poll(() => removed).toEqual(["/api/v1/proxy/auth-files/staging/users/reviewer"])

  // Add login from the sheet names the file.
  await sheet.getByRole("button", { name: "Add login" }).click()
  const add = page.getByRole("dialog", { name: "Add a login" })
  await expect(add.getByLabel("File")).toHaveValue("staging")
})
