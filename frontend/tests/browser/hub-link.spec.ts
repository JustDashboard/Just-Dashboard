import { expect, test, type Page, type Route } from "@playwright/test"

/**
 * The hub-aware dashboard against a mocked API: Settings → Hub not connected, waiting for
 * approval and connected, and the shell a connected server grows — the server switcher in the
 * rail and the phone's strip, the palette's Servers, the server's name in the tab's title and the
 * hub's notice card. The JSON is hub-link-protocol.md §8.2 and §8.3, field for field.
 *
 * The mock is a small state machine, as account-link.spec.ts's is: `/auth/session` carries the
 * `hub` field exactly while a link is stored, which is what the backend does, so connecting and
 * disconnecting here moves the shell the way they move it for real.
 *
 * With no link, nothing hub-shaped may render and nothing may be asked for: the first test is the
 * one that keeps an unlinked server's shell what it was.
 */

const now = Date.now()
const iso = (ms: number) => new Date(ms).toISOString()

const HUB = "https://hub.tailed39ba.ts.net"
const OTHER = "https://vmi3690758.tailed39ba.ts.net:8443"
const FINGERPRINT = "21fe:31df:a154:a261"
// The hub's machine, as WhoIs names it (§2.4).
const MACHINE = {
  id: "nK7f2QbPaa21CNTRL",
  name: "hub.tailed39ba.ts.net",
  owner: "wayy01@github",
  tags: [],
}
const MACHINE_LINE = "The hub's machine: hub.tailed39ba.ts.net, a machine of wayy01@github"
// Another machine at the hub's name, as the backend says it (§2.4).
const CHANGED =
  "the hub's machine changed: hub.tailed39ba.ts.net is now held by hub-2.tailed39ba.ts.net (nB2c0000CNTRL), not by hub.tailed39ba.ts.net (nK7f2QbPaa21CNTRL)"

const admin = {
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
  user: {
    id: 1,
    username: "operator",
    displayName: "operator",
    role: "admin",
    totpEnabled: true,
    disabled: false,
    mustChangePassword: false,
    lastLoginAt: iso(now),
    createdAt: iso(now),
    hiddenSections: [],
  },
}

const reader = { ...admin, capabilities: ["read"], user: { ...admin.user, role: "viewer" } }

type Scope = "observe" | "operate" | "maintain"
type Status = Record<string, unknown> & { state: string; reachable?: boolean }

// The hub sends a version for this server only; every other entry's is "" (§6.1).
const fleet = [
  {
    serverId: "5b1f9a20-0000-4000-8000-000000000001",
    name: "vmi3690758",
    endpoint: OTHER,
    state: "late",
    version: "",
    self: false,
    lastSeenAt: iso(now - 60_000),
  },
  {
    serverId: "7a0d3c1e-0000-4000-8000-000000000002",
    name: "vps-07749119-vps-ovh-net",
    endpoint: "https://vps-07749119-vps-ovh-net.tailed39ba.ts.net:8443",
    state: "online",
    version: "0.8.5",
    self: true,
    lastSeenAt: iso(now),
  },
  {
    serverId: "3c2e0f11-0000-4000-8000-000000000003",
    name: "atlas",
    state: "offline",
    version: "",
    self: false,
  },
]

const notices = [
  {
    id: "update-0.8.6",
    level: "info",
    title: "Update ready on 1 server",
    body: "0.8.6 is out. <b>vmi3690758</b> can install it from Just Hub.",
    href: `${HUB}/updates`,
    at: iso(now - 120_000),
  },
  {
    id: "offline:atlas",
    level: "critical",
    title: "atlas is offline",
    body: "No report for 4 minutes.",
    href: "https://evil.example.com/phish",
    at: iso(now - 240_000),
  },
]

const unlinked: Status = { enabled: true, state: "unlinked", admin: { fingerprint: FINGERPRINT } }

const pending = (scope: Scope, url = HUB, testOrigin?: string): Status => ({
  enabled: true,
  state: "pending",
  hubUrl: url,
  admin: {
    fingerprint: FINGERPRINT,
    ...(testOrigin ? { testOrigin } : {}),
    request: {
      hubUrl: url,
      userCode: "K7QM-29XD",
      // The approval page names the request, never the code: the owner types that (§4.1).
      approveUrl: `${url}/servers/add/?request=2c9e41d7-0000-4000-8000-000000000001`,
      expiresAt: iso(Date.now() + 14 * 60_000 + 30_000),
      scope,
      // No machine is asked about at the test origin (§2.4).
      ...(url === testOrigin ? {} : { hubMachine: MACHINE }),
    },
  },
})

// What the dashboard last sent (§5), at maintain.
const lastSnapshot = {
  protocol: 1,
  sentAt: Math.floor((now - 10_000) / 1000),
  server: {
    name: "vps-07749119-vps-ovh-net",
    version: "0.8.5",
    updateAvailable: false,
    updateBreaking: false,
    scope: "maintain",
    uptimeSeconds: 864_000,
    os: "Ubuntu 24.04",
    arch: "amd64",
  },
  access: {
    kind: "tailscale",
    endpoint: "https://vps-07749119-vps-ovh-net.tailed39ba.ts.net:8443",
    tailscale: { hostname: "vps-07749119-vps-ovh-net.tailed39ba.ts.net", running: true },
  },
  metrics: { cpuPercent: 12.5, memPercent: 40, diskPercent: 55, diskMount: "/" },
  containers: { running: 3, stopped: 0, unhealthy: 0 },
  alerts: [],
}

const linked = (scope: Scope, extra: Record<string, unknown> = {}): Status => ({
  enabled: true,
  state: "linked",
  hubUrl: HUB,
  hubName: "hub",
  scope,
  reachable: true,
  lastHeartbeatAt: iso(now - 10_000),
  fleet,
  notices: [],
  signIn: { enabled: false, offered: false, users: [] },
  admin: {
    fingerprint: FINGERPRINT,
    link: {
      serverId: "7a0d3c1e-0000-4000-8000-000000000002",
      linkedAt: iso(now - 86_400_000),
      linkedBy: "operator",
      hubMachine: MACHINE,
      lastCommand: {
        id: "01JB0000000000000000000011",
        kind: "container.restart",
        status: "succeeded",
        issuedBy: "owner",
        at: iso(now - 60_000),
      },
      hubKeys: [{ kid: "hub-2026-11", fingerprint: "39f7:13d0:a644:253f" }],
      updatePolicy: { auto: "patch", window: { start: "03:00", end: "05:00" } },
      lastSnapshot,
    },
    grants: [],
  },
  ...extra,
})

/**
 * A link this server stopped using (§2.2, §2.4). The status has no field for it: an idle link's
 * reason is its last error, in the backend's words (hublink/status_test.go), and it is not reached.
 */
const idleLink = (reason: string): Status => {
  const base = linked("maintain")
  return {
    ...base,
    reachable: false,
    admin: {
      ...(base.admin as object),
      link: {
        ...(base.admin as { link: object }).link,
        lastError: `this server no longer accepts the hub's address: ${reason}; disconnect, then connect again`,
      },
    },
  }
}

/** What a non-administrator is sent: no `admin` part (§8.2). */
const forReader = (status: Status): Status =>
  Object.fromEntries(Object.entries(status).filter(([key]) => key !== "admin")) as Status

type Call = { method: string; path: string; body?: unknown; csrf?: string }

async function mock(page: Page, initial: Status, auth = admin) {
  const world = {
    hub: initial,
    calls: [] as Call[],
    reads: [] as string[],
    connectRefusal: undefined as undefined | { status: number; body: object },
  }
  const respond = (route: Route, body: unknown, status = 200) =>
    route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
  const shown = () => (auth === admin ? world.hub : forReader(world.hub))

  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname.replace(/^\/api\/v1/, "")
    const method = request.method()
    if (path === "/auth/session") {
      // The `hub` field exists exactly while a link is stored (§8.3).
      const stored = world.hub.enabled && world.hub.state === "linked"
      return respond(route, {
        ...auth,
        ...(stored
          ? {
              hub: {
                linked: true,
                name: world.hub.hubName,
                url: world.hub.hubUrl,
                reachable: world.hub.reachable,
              },
            }
          : {}),
      })
    }
    if (path === "/account/profile") return respond(route, auth.user)
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
    if (!path.startsWith("/dashboard/hub")) return respond(route, [])

    if (method === "GET") world.reads.push(path)
    else {
      world.calls.push({
        method,
        path,
        body: request.postDataJSON() ?? undefined,
        csrf: request.headers()["x-jd-csrf"],
      })
    }

    if (path === "/dashboard/hub" && method === "GET") return respond(route, shown())
    if (path === "/dashboard/hub/check" && method === "GET") {
      // The URL rule, nothing started (§8.2): a name off the tailnet is refused, the test
      // origin has no machine, and the hub's name is its machine.
      const asked = new URL(request.url()).searchParams.get("url") ?? ""
      const testOrigin = (world.hub.admin as { testOrigin?: string } | undefined)?.testOrigin
      if (asked.includes("example.com")) {
        return respond(
          route,
          {
            error: {
              code: "hub_url_not_tailnet",
              message:
                "the hub must be a machine on this server's tailnet, named like hub.tailed39ba.ts.net",
            },
          },
          400,
        )
      }
      if (testOrigin && asked === testOrigin) {
        return respond(route, { hubUrl: testOrigin, testOrigin: true })
      }
      return respond(route, { hubUrl: HUB, machine: MACHINE, testOrigin: false })
    }
    if (path === "/dashboard/hub" && method === "POST") {
      if (world.connectRefusal) {
        return respond(route, world.connectRefusal.body, world.connectRefusal.status)
      }
      const { url, scope } = request.postDataJSON()
      const testOrigin = (world.hub.admin as { testOrigin?: string } | undefined)?.testOrigin
      world.hub = pending(scope, url, testOrigin)
      return respond(route, world.hub)
    }
    if (path === "/dashboard/hub/request" && method === "DELETE") {
      world.hub = unlinked
      return route.fulfill({ status: 204 })
    }
    if (path === "/dashboard/hub/scope" && method === "PUT") {
      world.hub = linked(request.postDataJSON().scope)
      return respond(route, world.hub)
    }
    if (path === "/dashboard/hub/sync" && method === "POST") return respond(route, world.hub)
    if (path === "/dashboard/hub" && method === "DELETE") {
      world.hub = unlinked
      return route.fulfill({ status: 204 })
    }
    return respond(route, { error: { code: "not_found", message: "no such route" } }, 404)
  })

  // Another server and the hub are other origins, opened in tabs of their own; answering them
  // for the whole context is what lets a test see a tab arrive there without anything leaving
  // the machine.
  for (const origin of [OTHER, HUB]) {
    await page
      .context()
      .route(`${origin}/**`, (route) =>
        route.fulfill({ status: 200, contentType: "text/html", body: "<title>elsewhere</title>" }),
      )
  }
  return world
}

/** The tab a click opens, and the click. */
async function newTab(page: Page, click: () => Promise<void>) {
  const [tab] = await Promise.all([page.context().waitForEvent("page"), click()])
  await tab.waitForLoadState()
  return tab
}

const rail = (page: Page) => page.locator("[data-slot=sidebar]")
const switcher = (page: Page) => page.locator("[data-slot=server-switcher]:visible")
const servers = (page: Page) => page.getByRole("menu", { name: "Servers" })
const scopeCard = (page: Page, scope: string) =>
  page.getByRole("button", { name: `Choose the ${scope} scope` })
const palette = (page: Page) => page.getByRole("dialog", { name: "Command palette" })

function framedPanels(page: Page) {
  return page.evaluate(() =>
    Array.from(
      document.querySelectorAll("[data-slot=panel]:not([data-plain]):not(.border-rule-danger)"),
    ).map((el) => el.outerHTML.slice(0, 120)),
  )
}

function scrollsSideways(page: Page) {
  return page.evaluate(
    () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
  )
}

/** Every visible button with no text of its own and no name from anywhere else. */
function unnamedControls(page: Page) {
  return page.evaluate(() => {
    const bad: string[] = []
    for (const el of document.querySelectorAll<HTMLElement>("button, [role='button']")) {
      if (el.offsetParent === null) continue
      if ((el.textContent ?? "").trim()) continue
      if (el.getAttribute("aria-label") || el.getAttribute("aria-labelledby")) continue
      if (el.querySelector(".sr-only")) continue
      bad.push(el.outerHTML.slice(0, 160))
    }
    return bad
  })
}

/** Fully rounded, filled labels: design-system §4's pill, which a status dot with no text is not. */
function filledPills(page: Page) {
  return page.evaluate(() => {
    const bad: string[] = []
    for (const el of document.querySelectorAll<HTMLElement>("span, div")) {
      if (el.dataset.slot === "user-avatar") continue
      const s = getComputedStyle(el)
      const h = el.getBoundingClientRect().height
      if (!h || h > 32 || parseFloat(s.borderTopLeftRadius) < h / 2) continue
      const filled = s.backgroundColor !== "rgba(0, 0, 0, 0)" && s.backgroundColor !== "transparent"
      if (filled && (el.textContent ?? "").trim()) bad.push(el.outerHTML.slice(0, 140))
    }
    return bad
  })
}

test("unlinked: the rail, the palette and the tab are what they were, and nothing is asked", async ({
  page,
}) => {
  const world = await mock(page, unlinked)
  await page.goto("/account")
  await expect(page.locator("[data-slot=page]").first()).toBeVisible()

  await expect(page).toHaveTitle("Profile · Just Dashboard")
  await expect(rail(page).getByRole("link", { name: "Just Dashboard" })).toHaveAttribute(
    "href",
    "/",
  )
  await expect(page.locator("[data-slot=server-switcher]")).toHaveCount(0)
  await expect(page.locator("[data-slot=hub-notice]")).toHaveCount(0)

  await page.keyboard.press("Control+k")
  await expect(palette(page)).toBeVisible()
  await expect(palette(page).getByRole("button", { name: /^Servers/ })).toHaveCount(0)
  await palette(page).getByRole("combobox").fill("server: vmi")
  await expect(palette(page).getByRole("button", { name: "Everything" })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await page.keyboard.press("Escape")
  await page.keyboard.press("Escape")

  // Settings keeps its three pages; Hub is drawn only on a linked server.
  await page.goto("/dashboard/configuration")
  await expect(rail(page).locator("a[href='/dashboard/link']")).toHaveText("Connect")
  await expect(rail(page).locator("a[href='/dashboard/hub']")).toHaveCount(0)

  // An unlinked server never asks for the hub's status outside the hub's own page.
  expect(world.reads).toEqual([])
})

test("?connect= fills in the hub, says so, shows its machine, chooses no scope, and waits for Connect", async ({
  page,
}) => {
  const world = await mock(page, unlinked)
  await page.goto("/dashboard/hub?connect=hub.tailed39ba.ts.net")

  await expect(page.getByRole("heading", { name: "Hub", level: 1 })).toHaveClass(/sr-only/)
  await expect(page.locator("[data-slot=page]")).toHaveAttribute("data-register", "reading")
  await expect(
    page.getByRole("heading", { name: "Connect this server to Just Hub at hub.tailed39ba.ts.net" }),
  ).toBeVisible()
  await expect(page.getByLabel("Hub address")).toHaveValue(HUB)
  await expect(page.getByText(`this server ${FINGERPRINT}`)).toBeVisible()
  // The address came from a link anyone on the tailnet could send (§8.1), and whose machine it
  // is shows before anything is sent to it (§8.2).
  await expect(page.getByText("This address came from a link")).toBeVisible()
  await expect(page.locator("[data-slot=hub-link-machine]")).toHaveText(MACHINE_LINE)
  expect(world.reads).toContain("/dashboard/hub/check")

  // No scope is chosen for the person, and Connect waits for one; each card says what the hub
  // may then do.
  for (const scope of ["Observe", "Operate", "Maintain"]) {
    await expect(scopeCard(page, scope)).toHaveAttribute("aria-pressed", "false")
  }
  await expect(page.getByRole("button", { name: "Connect", exact: true })).toBeDisabled()
  const scopes = page.getByRole("group", { name: "Scope" })
  await expect(scopes.getByText("Refresh the snapshot.")).toBeVisible()
  await expect(scopes.getByText(/install a dashboard update/)).toBeVisible()
  await expect(page.getByText(/No scope gives the hub a shell, files or accounts/)).toBeVisible()
  const sends = page.locator("section", { hasText: "What this server will send" })
  await expect(sends).toHaveCount(0)

  // What would leave, at the chosen scope, and what never does.
  await scopeCard(page, "Maintain").click()
  await expect(page.getByRole("button", { name: "Connect", exact: true })).toBeEnabled()
  await expect(sends.getByText("At Maintain")).toBeVisible()
  await expect(sends.getByText(/project names/)).toBeVisible()
  await scopeCard(page, "Observe").click()
  await expect(sends.getByText("At Observe")).toBeVisible()
  await expect(sends.getByText(/project names/)).toHaveCount(0)
  await expect(sends.getByText("Never sent")).toBeVisible()
  await expect(
    page.getByText("sudo ./scripts/manage.sh hub connect https://hub.tailed39ba.ts.net"),
  ).toBeVisible()

  // Arriving from a link has done nothing.
  expect(world.calls).toEqual([])
  expect(await framedPanels(page)).toEqual([])
  expect(await unnamedControls(page)).toEqual([])
  expect(await scrollsSideways(page)).toBe(false)

  await scopeCard(page, "Maintain").click()
  await page.getByRole("button", { name: "Connect", exact: true }).click()
  expect(world.calls).toEqual([
    { method: "POST", path: "/dashboard/hub", body: { url: HUB, scope: "maintain" }, csrf: "1" },
  ])

  // The code to type into the hub, which never shows it, and the fingerprint its screen shows.
  await expect(page.locator("[data-slot=hub-link-code]")).toHaveText("K7QM-29XD")
  await expect(page.locator("[data-slot=hub-link-fingerprint]")).toHaveText(FINGERPRINT)
  await expect(page.getByText(/Type this code into the hub when it asks for it/)).toBeVisible()
  await expect(page.locator("[data-slot=hub-link-machine]")).toHaveText(MACHINE_LINE)
  const approve = page.getByRole("link", { name: "Approve in Just Hub" })
  await expect(approve).toHaveAttribute(
    "href",
    `${HUB}/servers/add/?request=2c9e41d7-0000-4000-8000-000000000001`,
  )
  await expect(approve).toHaveAttribute("target", "_blank")
  await expect(approve).toHaveAttribute("rel", "noopener noreferrer")
  await expect(page.getByRole("meter", { name: "Time left to approve" })).toBeVisible()
  await expect(page.getByText(/^1[34]:\d\d$/)).toBeVisible()
  await expect(page.getByRole("button", { name: "Connect", exact: true })).toHaveCount(0)
  // Still no hub in the shell: nothing is linked until the hub approves.
  await expect(page.locator("[data-slot=server-switcher]")).toHaveCount(0)

  // The hub owner approves in the other tab; the page flips and the shell becomes hub-aware.
  world.hub = linked("maintain")
  await expect(page.getByText("Connected to Just Hub (hub)")).toBeVisible({ timeout: 15_000 })
  await expect(switcher(page)).toContainText("vps-07749119-vps-ovh-net")
  await expect(page).toHaveTitle("Hub · vps-07749119-vps-ovh-net · Just Dashboard")
  await expect(rail(page).locator("a[href='/dashboard/hub']")).toHaveText("Hub")
  await expect(page.getByRole("button", { name: "Disconnect" })).toBeVisible()
})

test("a link someone else wrote fills nothing in, and a refused hub says why", async ({ page }) => {
  const world = await mock(page, unlinked)
  await page.goto("/dashboard/hub?connect=javascript:alert(1)")
  await expect(page.getByRole("heading", { name: "Connect this server to Just Hub" })).toBeVisible()
  await expect(page.getByLabel("Hub address")).toHaveValue("")
  await expect(page.getByRole("button", { name: "Connect", exact: true })).toBeDisabled()

  world.connectRefusal = {
    status: 400,
    body: {
      error: {
        code: "hub_url_not_tailnet",
        message: "hub.example.com is not one machine on this server's tailnet (tailed39ba.ts.net)",
      },
    },
  }
  await page.getByLabel("Hub address").fill("https://hub.example.com")
  await expect(
    page.getByText("sudo ./scripts/manage.sh hub connect https://hub.example.com"),
  ).toBeVisible()
  // The rule's refusal shows as it is typed, before Connect.
  await expect(page.locator("[data-slot=hub-link-machine]")).toContainText(
    "the hub must be a machine on this server's tailnet",
  )
  // Arrived by a link, so the scope is the person's to choose before Connect is offered.
  await expect(page.getByRole("button", { name: "Connect", exact: true })).toBeDisabled()
  await scopeCard(page, "Observe").click()
  await page.getByRole("button", { name: "Connect", exact: true }).click()
  const alert = page.getByRole("alert").filter({ hasText: "Could not connect" })
  await expect(alert).toContainText("is not one machine on this server's tailnet")
  await expect(page.getByRole("button", { name: "Connect", exact: true })).toBeEnabled()
})

test("a request another machine answered for ends as failed and says why", async ({ page }) => {
  // The approval is where the hub key is pinned, so a different machine before it ends the
  // request with nothing stored (§2.4 "While a request is pending").
  await mock(page, {
    ...unlinked,
    admin: {
      fingerprint: FINGERPRINT,
      lastOutcome: { result: "failed", at: iso(now - 60_000), message: CHANGED },
    },
  })
  await page.goto("/dashboard/hub")
  await expect(page.getByText("Connecting failed")).toBeVisible()
  await expect(
    page.getByText(
      "The hub's machine changed: hub.tailed39ba.ts.net is now held by hub-2.tailed39ba.ts.net",
    ),
  ).toBeVisible()
  await expect(page.getByText(/^Ended 1m/)).toBeVisible()
  // Nothing was linked: the form is there to try again.
  await expect(page.getByLabel("Hub address")).toBeVisible()
})

test("a waiting request survives a reload and can be cancelled", async ({ page }) => {
  const world = await mock(page, pending("operate"))
  await page.goto("/dashboard/hub")
  await expect(page.locator("[data-slot=hub-link-code]")).toHaveText("K7QM-29XD")
  await expect(page.getByText("Waiting for approval").first()).toBeVisible()
  await expect(page.locator("section", { hasText: "What this server will send" })).toContainText(
    "At Operate",
  )
  await page.getByRole("button", { name: "Cancel" }).click()
  await expect(page.getByLabel("Hub address")).toBeVisible()
  expect(world.calls.map((call) => `${call.method} ${call.path}`)).toEqual([
    "DELETE /dashboard/hub/request",
  ])
})

test("the outcome of the last request is said once", async ({ page }) => {
  await mock(page, {
    ...unlinked,
    admin: {
      fingerprint: FINGERPRINT,
      lastOutcome: { result: "denied", at: iso(now - 60_000) },
    },
  })
  await page.goto("/dashboard/hub")
  await expect(page.getByText("The request was declined")).toBeVisible()
  await page.getByRole("button", { name: "Dismiss" }).click()
  await expect(page.getByText("The request was declined")).toHaveCount(0)
})

test("linked: the switcher lists the fleet as the hub's, this one first, and another opens in a new tab", async ({
  page,
}) => {
  await mock(page, linked("maintain"))
  await page.goto("/account")

  await expect(page).toHaveTitle("Profile · vps-07749119-vps-ovh-net · Just Dashboard")
  await expect(switcher(page)).toContainText("vps-07749119-vps-ovh-net")
  await switcher(page).click()
  const menu = servers(page)
  await expect(menu).toBeVisible()
  // The names are the hub's words, and the menu says whose (§8.10).
  await expect(menu.locator("[data-slot=hub-attribution]")).toHaveText("Listed by Just Hub (hub)")
  const items = menu.getByRole("menuitem")
  // The hub sends no version for another server (§6.1), so none is shown for one.
  await expect(items).toHaveText([
    /^vps-07749119-vps-ovh-net, Online\s*[\d.]+\s*this one$/,
    /^atlas, Offline\s*no address$/,
    /^vmi3690758, Late, opens in a new tab$/,
    /^Open Just Hub, opens in a new tab$/,
  ])
  await expect(items.nth(0)).toHaveAttribute("href", "/")
  await expect(items.nth(1)).toHaveAttribute("aria-disabled", "true")
  // An address the hub handed over opens in a tab of its own, with no opener and no referrer.
  for (const [n, href] of [
    [2, OTHER],
    [3, `${HUB}/`],
  ] as const) {
    await expect(items.nth(n)).toHaveAttribute("href", href)
    await expect(items.nth(n)).toHaveAttribute("target", "_blank")
    await expect(items.nth(n)).toHaveAttribute("rel", "noopener noreferrer")
  }
  // A state is a dot and a word (§4), never a filled pill.
  expect(await filledPills(page)).toEqual([])

  const tab = await newTab(page, () => items.nth(2).click())
  await expect(tab).toHaveURL(`${OTHER}/`)
  await expect(tab).toHaveTitle("elsewhere")
  expect(await tab.evaluate(() => window.opener)).toBeNull()
  // This tab stays on this server.
  await expect(page).toHaveURL(/\/account$/)
})

test("linked: the palette's Servers open another server in a new tab", async ({ page }) => {
  await mock(page, linked("maintain"))
  await page.goto("/account")
  await expect(switcher(page)).toContainText("vps-07749119-vps-ovh-net")

  await page.keyboard.press("Control+k")
  const dialog = palette(page)
  await expect(dialog.getByRole("button", { name: /^Servers/ })).toBeVisible()
  await dialog.getByRole("combobox").fill("server: ")
  const results = dialog.getByRole("listbox")
  await expect(results.getByRole("option")).toHaveCount(4)
  await expect(results.getByRole("option").first()).toContainText("vps-07749119-vps-ovh-net")
  await expect(results.getByRole("option").first()).toContainText("This server")
  await expect(results.getByRole("option", { name: /Just Hub/ })).toBeVisible()

  await dialog.getByRole("combobox").fill("server: vmi")
  await expect(results.getByRole("option")).toHaveCount(1)
  await expect(results.getByRole("option")).toContainText("Late")
  // Who listed it, and where it opens, beside the list.
  const selected = dialog.locator("aside[aria-label='Selected result']")
  await expect(selected).toContainText("Just Hub (hub)")
  await expect(selected).toContainText("In a new tab")
  const tab = await newTab(page, () => page.keyboard.press("Enter"))
  await expect(tab).toHaveURL(`${OTHER}/`)
  expect(await tab.evaluate(() => window.opener)).toBeNull()
  await expect(page).toHaveURL(/\/account$/)
})

test("linked: the hub's notices, newest first, as text, dismissed per browser", async ({
  page,
}) => {
  await mock(page, linked("maintain", { notices }))
  await page.goto("/account")

  const card = page.locator("[data-slot=hub-notice]")
  await expect(card).toContainText("Update ready on 1 server")
  // Who says it comes first: the hub's words are the hub's (§8.10).
  await expect(card).toContainText(/Just Hub \(hub\) says · 2m/)
  // The body came from another machine: it is text, never markup.
  await expect(card).toContainText("0.8.6 is out. <b>vmi3690758</b> can install it")
  await expect(card.locator("b")).toHaveCount(0)
  const open = card.getByRole("link", { name: "Open in Just Hub" })
  await expect(open).toHaveAttribute("href", `${HUB}/updates`)
  await expect(open).toHaveAttribute("target", "_blank")
  await expect(open).toHaveAttribute("rel", "noopener noreferrer")
  // A notice's link names the host it opens.
  await expect(open).toContainText("hub.tailed39ba.ts.net")
  await expect(card.getByRole("link", { name: "1 more from Just Hub" })).toHaveAttribute(
    "href",
    "/dashboard/hub#notices",
  )

  expect(await filledPills(page)).toEqual([])
  expect(await unnamedControls(page)).toEqual([])

  await card.getByRole("button", { name: "Dismiss Update ready on 1 server" }).click()
  await expect(card).toContainText("atlas is offline")
  await expect(card).toHaveClass(/border-rule-danger/)
  // A link off the hub's origin is dropped (§6.2).
  await expect(card.getByRole("link", { name: "Open in Just Hub" })).toHaveCount(0)

  await page.reload()
  await expect(card).toContainText("atlas is offline")
  await card.getByRole("button", { name: "Dismiss atlas is offline" }).click()
  await expect(card).toHaveCount(0)

  // Settings → Hub lists them all, whatever this browser dismissed.
  await page.goto("/dashboard/hub#notices")
  const listed = page.locator("#notices")
  await expect(listed.getByText("Update ready on 1 server")).toBeVisible()
  await expect(listed.getByText("atlas is offline")).toBeVisible()
  await expect(listed.getByRole("link", { name: "Open in Just Hub" })).toHaveCount(1)
})

test("a notice that reads like the dashboard's own prompt is drawn as the hub's", async ({
  page,
}) => {
  await mock(
    page,
    linked("maintain", {
      notices: [
        {
          id: "session",
          level: "critical",
          title: "Your session expired",
          body: "Sign in again to keep working.",
          at: iso(now - 30_000),
        },
      ],
    }),
  )
  await page.goto("/account")
  const card = page.locator("[data-slot=hub-notice]")
  await expect(card).toContainText("Your session expired")
  // Inside the hub's card, under its name, before its words (§8.10).
  const said = await card.innerText()
  expect(said.indexOf("Just Hub (hub) says")).toBeGreaterThanOrEqual(0)
  expect(said.indexOf("Just Hub (hub) says")).toBeLessThan(said.indexOf("Your session expired"))
  // Nothing of the dashboard's own sign-in is drawn for it.
  await expect(page.locator("input[type=password]")).toHaveCount(0)

  await page.goto("/dashboard/hub#notices")
  const listed = await page.locator("#notices").innerText()
  expect(listed.indexOf("Just Hub (hub) says")).toBeGreaterThanOrEqual(0)
  expect(listed.indexOf("Just Hub (hub) says")).toBeLessThan(listed.indexOf("Your session expired"))
})

test("linked, hub unreachable: the switcher says so and the others read unknown", async ({
  page,
}) => {
  await mock(
    page,
    linked("maintain", {
      reachable: false,
      admin: {
        ...(linked("maintain").admin as object),
        link: {
          ...(linked("maintain").admin as { link: object }).link,
          lastError: "the heartbeat failed: the hub answered HTTP 503",
        },
      },
    }),
  )
  await page.goto("/dashboard/hub")
  await expect(switcher(page)).toContainText("Hub unreachable")
  await expect(page.getByText("Connected, hub unreachable")).toBeVisible()
  await expect(page.getByText("The last exchange with hub.tailed39ba.ts.net failed")).toBeVisible()
  await expect(
    page.locator("section", { hasText: "Activity" }).first().getByText("Not reaching the hub"),
  ).toBeVisible()

  await switcher(page).click()
  const menu = servers(page)
  await expect(menu.getByText("Hub unreachable")).toBeVisible()
  await expect(menu.getByRole("menuitem").nth(0)).toContainText("Not reaching the hub")
  await expect(menu.getByRole("menuitem").nth(2)).toContainText("Unknown")
})

test("an idle link says why, offers no sync, and a disconnect tells no one", async ({ page }) => {
  // Another machine took the hub's name (§2.4), in the words hublink/node.go gives it.
  const world = await mock(page, idleLink(CHANGED))
  await page.goto("/dashboard/hub")

  await expect(page.getByText("Idle", { exact: true })).toBeVisible()
  const notice = page.locator("[data-slot=hub-link-idle]")
  await expect(notice).toContainText("The link is idle")
  await expect(notice).toContainText(
    "The hub's machine changed: hub.tailed39ba.ts.net is now held by hub-2.tailed39ba.ts.net (nB2c0000CNTRL), not by hub.tailed39ba.ts.net (nK7f2QbPaa21CNTRL).",
  )
  await expect(notice).toContainText(
    "Nothing is sent to hub.tailed39ba.ts.net and nothing it asks is run until you disconnect and connect again.",
  )
  // Said once, at the top: not again as a failed exchange.
  await expect(page.getByText("The last exchange with hub.tailed39ba.ts.net failed")).toHaveCount(0)
  const activity = page.locator("section", { hasText: "Activity" }).first()
  await expect(activity.getByText("Not sent", { exact: true })).toBeVisible()
  await expect(page.getByText("Idle, nothing is sent")).toBeVisible()
  // A sync would be refused (409 hub_url_refused), so none is offered.
  await expect(page.getByRole("button", { name: "Sync now" })).toHaveCount(0)
  expect(await unnamedControls(page)).toEqual([])
  expect(await scrollsSideways(page)).toBe(false)

  await page.getByRole("button", { name: "Disconnect", exact: true }).click()
  const dialog = page.getByRole("dialog", { name: "Disconnect from Just Hub" })
  await expect(dialog).toContainText("forgets its idle link to hub.tailed39ba.ts.net")
  await expect(dialog).toContainText("the hub is not told")
  await dialog.getByRole("button", { name: "Disconnect", exact: true }).click()
  await expect(page.getByLabel("Hub address")).toBeVisible()
  expect(world.calls.map((call) => `${call.method} ${call.path}`)).toEqual([
    "DELETE /dashboard/hub",
  ])
})

test("to anyone but an administrator an idle link is one that cannot reach its hub", async ({
  page,
}) => {
  await mock(page, idleLink("the hub's machine changed"), reader)
  await page.goto("/dashboard/hub")
  await expect(page.getByText("Connected, hub unreachable")).toBeVisible()
  await expect(page.getByText("The link is idle")).toHaveCount(0)
  await expect(page.getByText(/hub's machine changed/)).toHaveCount(0)
})

test("linked: the page reads the link, changes its scope and disconnects after asking", async ({
  page,
}) => {
  const world = await mock(page, linked("maintain"))
  await page.goto("/dashboard/hub")

  await expect(page.getByRole("link", { name: "hub.tailed39ba.ts.net" })).toHaveAttribute(
    "href",
    `${HUB}/`,
  )
  await expect(page.getByText("Connected", { exact: true })).toBeVisible()
  // The hub's name is its own word, said as the hub's (§8.10).
  await expect(page.locator("[data-slot=host-identity]").getByText("Just Hub (hub)")).toBeVisible()
  await expect(page.getByText("Maintain scope")).toBeVisible()
  await expect(page.getByText("3 servers", { exact: true })).toBeVisible()
  await expect(page.getByText(/connected 1d .*by operator/)).toBeVisible()

  // What the hub has run here, and where every command is.
  const activity = page.locator("section", { hasText: "Activity" }).first()
  const command = activity.locator("[data-slot=row]", { hasText: "Restart a container" })
  await expect(command).toContainText("asked by owner")
  await expect(command).toContainText("Succeeded")
  await expect(
    activity.getByRole("link", { name: /Every command in the audit log/ }),
  ).toHaveAttribute("href", "/audit?action=hub.command.")

  // The last report, as it was sent.
  const shared = page.locator("section", { hasText: "What this server shares" }).first()
  await expect(shared.getByRole("heading", { name: "Server", exact: true })).toBeVisible()
  await expect(shared.getByText("Ubuntu 24.04")).toBeVisible()
  await expect(page.locator("section", { hasText: "Automatic updates" })).toContainText(
    "Set from hub.tailed39ba.ts.net",
  )
  const keys = page.locator("section", { hasText: "Keys" }).last()
  await expect(keys.getByText("Hub key")).toBeVisible()
  await expect(keys.getByText("39f7:13d0:a644:253f")).toBeVisible()
  await expect(keys.getByText(FINGERPRINT)).toBeVisible()
  // The pinned key and machine, to compare with the hub's own Settings page (§8.2).
  await expect(
    page.getByText(
      "Compare the hub key's fingerprint with the key fingerprint on the hub's Settings page.",
    ),
  ).toBeVisible()
  await expect(page.locator("[data-slot=hub-link-machine]")).toHaveText(MACHINE_LINE)

  // A scope change is a choice and then a command.
  await scopeCard(page, "Observe").click()
  expect(world.calls).toEqual([])
  await page.getByRole("button", { name: "Change to Observe" }).click()
  await expect(page.getByText("Scope is now Observe")).toBeVisible()
  await expect(page.getByText("Observe scope")).toBeVisible()

  await page.getByRole("button", { name: "Sync now" }).click()
  await expect(page.getByText("Report sent to hub.tailed39ba.ts.net")).toBeVisible()

  expect(await framedPanels(page)).toEqual([])
  expect(await unnamedControls(page)).toEqual([])
  expect(await scrollsSideways(page)).toBe(false)

  // Disconnect asks first, and nothing happens until it is confirmed.
  await page.getByRole("button", { name: "Disconnect", exact: true }).click()
  const dialog = page.getByRole("dialog", { name: "Disconnect from Just Hub" })
  await expect(dialog).toContainText("stops reporting to hub.tailed39ba.ts.net")
  await dialog.getByRole("button", { name: "Cancel" }).click()
  expect(world.calls.map((call) => `${call.method} ${call.path}`)).toEqual([
    "PUT /dashboard/hub/scope",
    "POST /dashboard/hub/sync",
  ])
  await page.getByRole("button", { name: "Disconnect", exact: true }).click()
  await page
    .getByRole("dialog", { name: "Disconnect from Just Hub" })
    .getByRole("button", { name: "Disconnect", exact: true })
    .click()

  await expect(page.getByText("Disconnected from hub.tailed39ba.ts.net")).toBeVisible()
  await expect(page.getByLabel("Hub address")).toBeVisible()
  // The shell goes back to what an unlinked server draws.
  await expect(page.locator("[data-slot=server-switcher]")).toHaveCount(0)
  await expect(rail(page).getByRole("link", { name: "Just Dashboard" })).toBeVisible()
  await expect(page).toHaveTitle("Hub · Just Dashboard")
  expect(world.calls.at(-1)).toEqual({
    method: "DELETE",
    path: "/dashboard/hub",
    body: undefined,
    csrf: "1",
  })
})

test("a reader sees which hub and what it says, and none of the controls", async ({ page }) => {
  await mock(page, linked("operate", { notices }), reader)
  await page.goto("/dashboard/hub")
  await expect(page.getByText("Operate scope")).toBeVisible()
  await expect(page.locator("#notices")).toContainText("Update ready on 1 server")
  await expect(page.getByRole("button", { name: "Disconnect" })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Sync now" })).toHaveCount(0)
  await expect(page.locator("[data-slot=choice-card]")).toHaveCount(0)
  // The switcher is every role's: it is how a reader moves between servers too.
  await expect(switcher(page)).toContainText("vps-07749119-vps-ovh-net")
})

test("linking switched off explains the switch and offers nothing", async ({ page }) => {
  await mock(page, { enabled: false, state: "unlinked" })
  await page.goto("/dashboard/hub")
  await expect(page.getByText("Connecting to a hub is turned off")).toBeVisible()
  await expect(page.getByText("JD_HUB_LINK=false")).toBeVisible()
  await expect(page.getByLabel("Hub address")).toHaveCount(0)
  await expect(page.locator("[data-slot=page] button, [data-slot=page] a")).toHaveCount(0)
})

test("a test origin is said out loud", async ({ page }) => {
  await mock(page, {
    ...unlinked,
    admin: { fingerprint: FINGERPRINT, testOrigin: "http://127.0.0.1:43180" },
  })
  await page.goto("/dashboard/hub?connect=127.0.0.1:43180")
  await expect(page.getByText("A test hub address is allowed")).toBeVisible()
  await expect(page.getByLabel("Hub address")).toHaveValue("http://127.0.0.1:43180")
  await expect(page.locator("[data-slot=hub-link-machine]")).toHaveText(
    "The test origin: no machine is checked.",
  )
  // A development hub names its own origin in the link; that exact origin fills in too.
  await page.goto("/dashboard/hub?connect=http://127.0.0.1:43181")
  await expect(page.getByLabel("Hub address")).toHaveValue("")
  await page.goto("/dashboard/hub?connect=http://127.0.0.1:43180")
  await expect(page.getByLabel("Hub address")).toHaveValue("http://127.0.0.1:43180")

  // Its request has no machine, and says why rather than "not checked".
  await scopeCard(page, "Observe").click()
  await page.getByRole("button", { name: "Connect", exact: true }).click()
  await expect(page.locator("[data-slot=hub-link-code]")).toHaveText("K7QM-29XD")
  await expect(page.locator("[data-slot=hub-link-machine]")).toHaveText(
    "The test origin: no machine is checked.",
  )
})

test("on a phone the strip names the server and opens the same menu", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mock(page, linked("maintain", { notices }))
  await page.goto("/dashboard/hub")

  const strip = switcher(page)
  await expect(strip).toHaveCount(1)
  await expect(strip).toContainText("vps-07749119-vps-ovh-net")
  await strip.click()
  const menu = servers(page)
  await expect(menu.getByRole("menuitem")).toHaveCount(4)
  const box = await menu.boundingBox()
  expect(box!.x).toBeGreaterThanOrEqual(0)
  expect(box!.x + box!.width).toBeLessThanOrEqual(390)
  await page.keyboard.press("Escape")
  expect(await scrollsSideways(page)).toBe(false)

  for (const [name, state] of [
    ["unlinked", unlinked],
    ["pending", pending("maintain")],
    ["linked", linked("maintain", { notices })],
    ["idle", idleLink("the hub's machine changed")],
  ] as const) {
    for (const width of [390, 1280, 1720]) {
      await page.setViewportSize({ width, height: 1600 })
      await mock(page, state)
      await page.goto("/dashboard/hub")
      await expect(page.locator("[data-slot=page]")).toHaveClass(/animate-rise/)
      expect(await scrollsSideways(page), `${name} at ${width}`).toBe(false)
      await page.screenshot({ path: `test-results/hub-link-${name}-${width}.png`, fullPage: true })
    }
  }
})
