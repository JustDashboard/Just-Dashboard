import { expect, test, type Page, type Request, type Route } from "@playwright/test"

/**
 * Hub sign-in against a mocked API (hub-link-protocol.md §9): `/hub/enter`, where Just Hub opens
 * this server with a ticket in the fragment, and "Sign in from Just Hub" on Settings → Hub.
 *
 * `/hub/enter` is held to §9.5: its first script takes the ticket out of the address before
 * anything else runs, the only requests it makes are the session read and the redeem, the ticket
 * goes only in the redeem's body, it asks before replacing another account's session, and a
 * refusal is shown once with a way to sign in, never retried.
 */

const now = Date.now()
const iso = (ms: number) => new Date(ms).toISOString()
const HUB = "https://hub.tailed39ba.ts.net"

const b64url = (text: string) =>
  Buffer.from(text).toString("base64").replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "")

/** A ticket's shape; only the hub key can make a real one, and the server checks it, not this. */
const ticketFor = (userId: number) =>
  [
    "v1",
    "hub-2026-11",
    b64url(JSON.stringify({ v: 1, typ: "hub.signin", userId, hubUser: "owner" })),
    "c2lnbmF0dXJl",
  ].join(".")

const user = (id: number, username: string, totpEnabled = true) => ({
  id,
  username,
  displayName: username,
  role: "admin",
  totpEnabled,
  disabled: false,
  mustChangePassword: false,
  lastLoginAt: iso(now),
  createdAt: iso(now),
  hiddenSections: [],
})

const signedIn = (u: ReturnType<typeof user>) => ({
  authenticated: true,
  needsTotp: false,
  needsEnrollment: false,
  require2fa: false,
  capabilities: [
    "read",
    "service.control",
    "file.write",
    "terminal",
    "destructive",
    "system.admin",
  ],
  user: u,
})

type Redeem = { status: number; body: object }

/** /hub/enter's two calls, and every request the page makes. */
async function mockEnter(page: Page, session: object | null, redeem: Redeem) {
  const world = {
    requests: [] as Request[],
    redeems: [] as { body: unknown; csrf?: string; urlDuring: string }[],
  }
  page.on("request", (request) => world.requests.push(request))
  await page.route("**/api/v1/auth/session", (route) =>
    session
      ? route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify(session),
        })
      : route.fulfill({
          status: 401,
          contentType: "application/json",
          body: JSON.stringify({ error: { code: "unauthorized", message: "session invalid" } }),
        }),
  )
  await page.route("**/api/v1/auth/hub/enter", async (route: Route) => {
    const request = route.request()
    world.redeems.push({
      body: request.postDataJSON(),
      csrf: request.headers()["x-jd-csrf"],
      urlDuring: await page.evaluate(() => location.href),
    })
    await route.fulfill({
      status: redeem.status,
      contentType: "application/json",
      body: JSON.stringify(redeem.body),
    })
  })
  // Where a good redeem lands: a page of its own, so the test sees the navigation and nothing of
  // the dashboard's loads is counted as /hub/enter's.
  await page.route("**/landed", (route) =>
    route.fulfill({ status: 200, contentType: "text/html", body: "<p id=landed>landed</p>" }),
  )
  return world
}

const ok: Redeem = {
  status: 200,
  body: { ...signedIn(user(1, "ion")), next: "/landed" },
}

test.describe("/hub/enter", () => {
  test("the ticket leaves the address first, travels only in the redeem, and the page lands on next", async ({
    page,
  }) => {
    const ticket = ticketFor(1)
    const world = await mockEnter(page, null, ok)
    const response = await page.goto(`/hub/enter#ticket=${ticket}`)
    // The first script in the document is the inline one, with the response's CSP nonce.
    const html = (await response?.text()) ?? ""
    const csp = response?.headers()["content-security-policy"] ?? ""
    const first = /<script\b[^>]*>/.exec(html)?.[0] ?? ""
    const nonce = /nonce="([^"]+)"/.exec(first)?.[1]
    expect(nonce).toBeTruthy()
    expect(first).not.toContain("src=")
    expect(csp).toContain(`'nonce-${nonce}'`)
    expect(html.match(/<script\b/g)).toHaveLength(1)

    await expect(page).toHaveURL(/\/landed$/)
    expect(world.redeems).toHaveLength(1)
    expect(world.redeems[0].body).toEqual({ ticket })
    expect(world.redeems[0].csrf).toBe("1")
    // By the time anything was asked, the address had no fragment.
    expect(world.redeems[0].urlDuring).toMatch(/\/hub\/enter$/)

    // Nothing but the document, the session read and the redeem left /hub/enter, and nothing
    // went to another origin.
    const fromEnter = world.requests.filter((r) => !r.url().endsWith("/landed"))
    expect(fromEnter.map((r) => `${r.method()} ${new URL(r.url()).pathname}`)).toEqual([
      "GET /hub/enter",
      "GET /api/v1/auth/session",
      "POST /api/v1/auth/hub/enter",
    ])
    expect(
      world.requests.every((r) => new URL(r.url()).origin === new URL(page.url()).origin),
    ).toBe(true)
    // The ticket was never in a request's address.
    expect(world.requests.some((r) => r.url().includes(ticket))).toBe(false)
  })

  test("signed in as another account, it asks first and redeems only on Continue", async ({
    page,
  }) => {
    const world = await mockEnter(page, signedIn(user(2, "maria")), ok)
    await page.goto(`/hub/enter#ticket=${ticketFor(1)}`)
    await expect(page.getByRole("heading", { name: "Continue through Just Hub?" })).toBeVisible()
    await expect(page.getByText("You are signed in as maria.")).toBeVisible()
    expect(await page.evaluate(() => location.hash)).toBe("")
    expect(world.redeems).toHaveLength(0)
    await page.getByRole("button", { name: "Continue" }).click()
    await expect(page).toHaveURL(/\/landed$/)
    expect(world.redeems).toHaveLength(1)
  })

  test("Stay keeps the session the browser has and redeems nothing", async ({ page }) => {
    const world = await mockEnter(page, signedIn(user(2, "maria")), ok)
    await page.route("**/dashboard", (route) =>
      route.fulfill({ status: 200, contentType: "text/html", body: "<p>stayed</p>" }),
    )
    await page.goto(`/hub/enter#ticket=${ticketFor(1)}`)
    await page.getByRole("button", { name: "Stay" }).click()
    await expect(page).toHaveURL(/\/dashboard$/)
    expect(world.redeems).toHaveLength(0)
  })

  test("signed in as the same account, it goes on at once", async ({ page }) => {
    const world = await mockEnter(page, signedIn(user(1, "ion")), ok)
    await page.goto(`/hub/enter#ticket=${ticketFor(1)}`)
    await expect(page).toHaveURL(/\/landed$/)
    expect(world.redeems).toHaveLength(1)
  })

  test("a refusal says why, offers the sign-in page, and is never retried", async ({ page }) => {
    const world = await mockEnter(page, null, {
      status: 401,
      body: {
        error: {
          code: "ticket_used",
          message:
            "this sign-in link has already been used; open the server again from Just Hub, or sign in here",
        },
      },
    })
    await page.goto(`/hub/enter#ticket=${ticketFor(1)}`)
    const alert = page.getByRole("alert")
    await expect(alert.getByRole("heading", { name: "Could not sign you in" })).toBeVisible()
    await expect(alert).toContainText("this sign-in link has already been used")
    await expect(alert.getByRole("link", { name: "Sign in on this server" })).toHaveAttribute(
      "href",
      "/login",
    )
    expect(world.redeems).toHaveLength(1)
    // A reload has no ticket to send: the address no longer carries it.
    await page.reload()
    await expect(page.getByRole("alert")).toContainText("carries no sign-in link")
    expect(world.redeems).toHaveLength(1)
  })

  test("no ticket, no request", async ({ page }) => {
    const world = await mockEnter(page, null, ok)
    await page.goto("/hub/enter")
    await expect(page.getByRole("alert")).toContainText("carries no sign-in link")
    expect(world.requests.map((r) => new URL(r.url()).pathname)).toEqual(["/hub/enter"])
  })

  test("it fits a phone", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 800 })
    await mockEnter(page, signedIn(user(2, "a-rather-long-account-name")), ok)
    await page.goto(`/hub/enter#ticket=${ticketFor(1)}`)
    await expect(page.getByRole("button", { name: "Continue" })).toBeVisible()
    const width = await page.evaluate(() => document.documentElement.scrollWidth)
    expect(width).toBeLessThanOrEqual(390)
  })
})

// ------------------------------------------------------------- Settings → Hub --

type SignIn = { enabled: boolean; offered: boolean; users: string[]; mine?: object }

const status = (signIn: SignIn, grants: object[] = []) => ({
  enabled: true,
  state: "linked",
  hubUrl: HUB,
  hubName: "hub",
  scope: "maintain",
  reachable: true,
  lastHeartbeatAt: iso(now - 10_000),
  fleet: [],
  notices: [],
  signIn,
  admin: {
    fingerprint: "21fe:31df:a154:a261",
    link: {
      serverId: "7a0d3c1e-0000-4000-8000-000000000002",
      linkedAt: iso(now - 86_400_000),
      linkedBy: "ion",
      hubKeys: [{ kid: "hub-2026-11", fingerprint: "39f7:13d0:a644:253f" }],
    },
    grants,
  },
})

async function mockSettings(page: Page, first: SignIn) {
  const world = {
    signIn: first,
    grants: [] as object[],
    calls: [] as { method: string; path: string; body?: unknown }[],
  }
  const me = signedIn(user(1, "ion"))
  const respond = (route: Route, body: unknown, code = 200) =>
    route.fulfill({ status: code, contentType: "application/json", body: JSON.stringify(body) })
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname.replace(/^\/api\/v1/, "")
    const method = request.method()
    if (path === "/auth/session") {
      return respond(route, {
        ...me,
        hub: { linked: true, name: "hub", url: HUB, reachable: true },
      })
    }
    if (path === "/account/profile") return respond(route, me.user)
    if (path === "/dashboard/update") {
      return respond(route, {
        version: "0.8.5",
        latest: "0.8.5",
        available: false,
        releases: [],
        history: [],
        breaking: false,
        check: { enabled: true, checkedAt: iso(now), repo: "wayy/just-dashboard", ref: "main" },
        install: { supported: true, dir: "/opt/just-dashboard", dirty: [] },
      })
    }
    if (path === "/dashboard/hub" && method === "GET")
      return respond(route, status(world.signIn, world.grants))
    if (path === "/account/hub-signin") {
      world.calls.push({ method, path, body: request.postDataJSON() ?? undefined })
      if (method === "PUT") {
        const body = request.postDataJSON()
        if (body.password !== "right") {
          return respond(
            route,
            { error: { code: "invalid_credentials", message: "that password is incorrect" } },
            401,
          )
        }
        world.signIn = { ...world.signIn, mine: { hubUser: body.hubUser, since: iso(Date.now()) } }
        world.grants = [
          { userId: 1, username: "ion", hubUser: body.hubUser, since: iso(Date.now()) },
        ]
      } else {
        world.signIn = { ...world.signIn, mine: undefined }
        world.grants = []
      }
      return route.fulfill({ status: 204 })
    }
    return respond(route, [])
  })
  return world
}

test.describe("Settings → Hub: Sign in from Just Hub", () => {
  test("off on this server, it says how it is turned on and offers nothing", async ({ page }) => {
    await mockSettings(page, { enabled: false, offered: true, users: ["owner"] })
    await page.goto("/dashboard/hub")
    const section = page.locator("section", {
      has: page.getByRole("heading", { name: "Sign in from Just Hub" }),
    })
    await expect(section).toContainText("JD_HUB_SIGNIN=true")
    await expect(section.getByRole("button")).toHaveCount(0)
  })

  test("not offered by the hub, it says where it is turned on", async ({ page }) => {
    await mockSettings(page, { enabled: true, offered: false, users: [] })
    await page.goto("/dashboard/hub")
    await expect(page.getByText("does not offer it")).toBeVisible()
  })

  test("opting in says what it costs, asks for the password and code, and can be undone", async ({
    page,
  }) => {
    const world = await mockSettings(page, { enabled: true, offered: true, users: ["owner"] })
    await page.goto("/dashboard/hub")
    const section = page.locator("section", {
      has: page.getByRole("heading", { name: "Sign in from Just Hub" }),
    })
    // What the hub can then do, what a hub session cannot, and what turning it off does not undo.
    await expect(section).toContainText("anyone who takes over the hub could open a session as ion")
    await expect(section).toContainText("cannot create or change API keys")
    await expect(section).toContainText("it does not undo what was done in them")
    const allow = section.getByRole("button", { name: "Let Just Hub sign me in" })
    await expect(allow).toBeDisabled()
    await section.getByLabel("Your password on this server").fill("wrong")
    await section.getByLabel("Code from your authenticator").fill("123456")
    await allow.click()
    await expect(section.getByRole("alert")).toContainText("that password is incorrect")
    await section.getByLabel("Your password on this server").fill("right")
    await allow.click()
    await expect(section).toContainText("can sign you in as ion")
    expect(world.calls[world.calls.length - 1]).toEqual({
      method: "PUT",
      path: "/account/hub-signin",
      body: { hubUser: "owner", password: "right", code: "123456" },
    })
    await expect(section.getByText("Accounts that let Just Hub sign them in")).toBeVisible()
    await section.getByRole("button", { name: "Stop letting Just Hub sign me in" }).click()
    await expect(allow).toBeVisible()
    expect(world.calls[world.calls.length - 1].method).toBe("DELETE")
  })
})
