import { expect, test, type Page, type Route } from "@playwright/test"

/**
 * Settings → just-dashboard.com against a mocked API: not linked, waiting for approval, linked,
 * and linking switched off, plus the three things done to a link — change its scope, sync it,
 * unlink it. The mock is a small state machine rather than canned answers, because the page's
 * whole job is following the link from one state to the next.
 *
 * Reading register, no frame anywhere (design-system §15), so the structural checks other specs
 * make of their pages are made here too.
 */

const now = Date.now()
const iso = (ms: number) => new Date(ms).toISOString()

const user = {
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
    role: "admin",
    totpEnabled: true,
    disabled: false,
    mustChangePassword: false,
    lastLoginAt: iso(now),
    createdAt: iso(now),
  },
}

type Scope = "observe" | "operate" | "maintain"

const base = {
  enabled: true,
  siteUrl: "https://just-dashboard.com",
  fingerprint: "21fe:31df:a154:a261",
}

const unlinked = { ...base, state: "unlinked" }

const pending = (scope: Scope) => ({
  ...base,
  state: "pending",
  request: {
    userCode: "ABCD-1234",
    approveUrl: "https://just-dashboard.com/link?code=ABCD-1234",
    expiresAt: iso(Date.now() + 14 * 60_000 + 30_000),
    scope,
  },
})

// What the dashboard last sent, as docs/server-linking-plan.md §4.4 describes it.
const lastSnapshot = {
  protocol: 1,
  sentAt: Math.floor((now - 20_000) / 1000),
  server: {
    name: "atlas",
    version: "0.8.2",
    updateAvailable: false,
    scope: "operate",
    uptimeSeconds: 864_000,
    os: "Ubuntu 24.04",
    kernel: "6.14.0-37-generic",
    arch: "amd64",
  },
  access: {
    kind: "tailscale",
    endpoint: "https://atlas.tail1234.ts.net:8443",
    tailscale: { hostname: "atlas.tail1234.ts.net", ip4: "100.64.0.7", running: true },
  },
  metrics: { cpuPercent: 23.4, memPercent: 61, diskMount: "/", diskPercent: 71.2 },
  containers: {
    running: 12,
    list: [{ id: "3f2", name: "shop-web", image: "ghcr.io/acme/shop", state: "running" }],
  },
  alerts: [],
}

const linked = (scope: Scope, extra: Record<string, unknown> = {}) => ({
  ...base,
  state: "linked",
  link: {
    serverId: "srv_01",
    owner: "alice@example.com",
    scope,
    linkedAt: iso(now - 3 * 86_400_000),
    linkedBy: "operator",
    lastHeartbeatAt: iso(now - 20_000),
    lastCommand: {
      id: "cmd_1",
      kind: "container.restart",
      status: "succeeded",
      issuedBy: "account:alice@example.com",
      at: iso(now - 120_000),
    },
    siteKeys: [
      { kid: "k1", fingerprint: "9a0c:77b2:0e41:d5f3", notAfter: iso(now + 90 * 86_400_000) },
    ],
    updatePolicy: { auto: "patch", window: { start: "03:00", end: "05:00" } },
    lastSnapshot,
    ...extra,
  },
})

type Calls = { method: string; path: string; body?: unknown; csrf?: string }[]

/**
 * `link` is the state the dashboard reports and is replaced by the mutations, as the real routes
 * replace it; a test sets it directly to play the other tab approving the request.
 */
async function mock(page: Page, initial: Record<string, unknown>) {
  const world = { link: initial, calls: [] as Calls, startRefusal: undefined as undefined | object }
  const respond = (route: Route, body: unknown, status = 200) =>
    route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })

  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname.replace(/^\/api\/v1/, "")
    const method = request.method()
    if (path === "/auth/session") return respond(route, user)
    if (path === "/dashboard/update") {
      return respond(route, {
        version: "0.8.2",
        latest: "0.8.2",
        available: false,
        releases: [],
        history: [],
        breaking: false,
        check: { enabled: true, checkedAt: iso(now), repo: "wayy/just-dashboard", ref: "main" },
        install: { supported: true, dir: "/opt/just-dashboard", dirty: [] },
      })
    }
    if (!path.startsWith("/dashboard/link")) return respond(route, [])

    if (method !== "GET") {
      world.calls.push({
        method,
        path,
        body: request.postDataJSON() ?? undefined,
        csrf: request.headers()["x-jd-csrf"],
      })
    }
    const current = world.link as { link?: Record<string, unknown> }

    if (path === "/dashboard/link" && method === "GET") return respond(route, world.link)
    if (path === "/dashboard/link" && method === "POST") {
      if (world.startRefusal) return respond(route, world.startRefusal, 502)
      world.link = pending(request.postDataJSON().scope)
      return respond(route, world.link)
    }
    if (path === "/dashboard/link/request" && method === "DELETE") {
      world.link = unlinked
      return route.fulfill({ status: 204 })
    }
    if (path === "/dashboard/link/scope" && method === "PUT") {
      world.link = linked(request.postDataJSON().scope)
      return respond(route, world.link)
    }
    if (path === "/dashboard/link/sync" && method === "POST") return respond(route, current)
    if (path === "/dashboard/link" && method === "DELETE") {
      world.link = unlinked
      return route.fulfill({ status: 204 })
    }
    return respond(route, { error: { code: "not_found", message: "no such route" } }, 404)
  })
  return world
}

/** Every `Panel` on the page is plain: the redesign's one structural promise. */
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

const scopeCard = (page: Page, scope: string) =>
  page.getByRole("button", { name: `Choose the ${scope} scope` })

test("not linked: three scopes that name what they share, observe chosen, and a terminal way", async ({
  page,
}) => {
  await mock(page, unlinked)
  await page.goto("/dashboard/link")

  await expect(page.getByRole("heading", { name: "just-dashboard.com", level: 1 })).toHaveClass(
    /sr-only/,
  )
  await expect(page.locator("[data-slot=page]")).toHaveAttribute("data-register", "reading")
  await expect(page.getByText("Not linked", { exact: true }).first()).toBeVisible()
  await expect(page.getByText("21fe:31df:a154:a261")).toBeVisible()

  // The least access is what pressing Link with no other choice gives.
  const scopes = page.getByRole("group", { name: "Scope" })
  await expect(scopeCard(page, "Observe")).toHaveAttribute("aria-pressed", "true")
  await expect(scopeCard(page, "Operate")).toHaveAttribute("aria-pressed", "false")
  await expect(scopeCard(page, "Maintain")).toHaveAttribute("aria-pressed", "false")

  // Each card says exactly what it shares and unlocks.
  await expect(scopes.getByText("Access path (tailnet address or SSH tunnel port)")).toBeVisible()
  await expect(scopes.getByText("Refresh the snapshot.")).toBeVisible()
  await expect(
    scopes.getByText(/Everything in Observe, plus container, stack/).first(),
  ).toBeVisible()
  await expect(scopes.getByText(/restart a stack and run a backup job/)).toBeVisible()
  await expect(
    scopes.getByText(/install a dashboard update, set the automatic-update policy/),
  ).toBeVisible()

  // What would leave, for the scope in question, and what never does.
  const sends = page.locator("section", { hasText: "What this server will send" })
  await expect(sends.getByText("At Observe")).toBeVisible()
  await expect(sends.getByText("Alerts firing")).toBeVisible()
  await expect(sends.getByText(/project names/)).toHaveCount(0)
  await scopeCard(page, "Operate").click()
  await expect(sends.getByText("At Operate")).toBeVisible()
  await expect(
    sends.getByText("Container, stack, backup-job and last-deployed project names"),
  ).toBeVisible()
  await expect(sends.getByText("API keys", { exact: false })).toBeVisible()

  await expect(page.getByText("sudo ./scripts/manage.sh link")).toBeVisible()
  await expect(page.getByRole("button", { name: "Copy command" })).toBeVisible()

  expect(await framedPanels(page)).toEqual([])
  expect(await scrollsSideways(page)).toBe(false)
})

test("linking asks for the chosen scope, shows the code to approve, and follows the approval", async ({
  page,
}) => {
  const world = await mock(page, unlinked)
  await page.goto("/dashboard/link")

  await scopeCard(page, "Operate").click()
  await page.getByRole("button", { name: "Link", exact: true }).click()

  // A request is waiting: the code large and copyable, the way to approve it, the clock.
  await expect(page.getByText("ABCD-1234", { exact: true })).toBeVisible()
  expect(world.calls).toEqual([
    { method: "POST", path: "/dashboard/link", body: { scope: "operate" }, csrf: "1" },
  ])
  const approve = page.getByRole("link", { name: "Approve on just-dashboard.com" })
  await expect(approve).toHaveAttribute("href", "https://just-dashboard.com/link?code=ABCD-1234")
  await expect(approve).toHaveAttribute("target", "_blank")
  await expect(approve).toHaveAttribute("rel", "noopener noreferrer")
  await expect(page.getByText(/^1[34]:\d\d$/)).toBeVisible()
  await expect(page.getByRole("button", { name: "Copy code" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Cancel" })).toBeVisible()
  await expect(page.getByText("Waiting for approval").first()).toBeVisible()
  // The form is gone while the request stands; the fingerprint to check is on the page.
  await expect(page.getByRole("button", { name: "Link", exact: true })).toHaveCount(0)
  await expect(page.getByText(/shows this server's fingerprint/)).toContainText(
    "21fe:31df:a154:a261",
  )
  await expect(page.locator("section", { hasText: "What this server will send" })).toContainText(
    "At Operate",
  )
  expect(await framedPanels(page)).toEqual([])

  // Approving in the other tab: the page asks every five seconds and flips to linked.
  world.link = linked("operate")
  await expect(page.getByRole("button", { name: "Sync now" })).toBeVisible({ timeout: 15_000 })
  await expect(page.getByText("ABCD-1234")).toHaveCount(0)
  await expect(page.getByText("Linked to alice@example.com")).toBeVisible()
  await expect(page.getByRole("button", { name: "Unlink" })).toBeVisible()
})

test("a pending request can be cancelled", async ({ page }) => {
  const world = await mock(page, pending("maintain"))
  await page.goto("/dashboard/link")
  await expect(page.getByText("ABCD-1234", { exact: true })).toBeVisible()
  await expect(page.getByText("Maintain", { exact: true })).toBeVisible()

  await page.getByRole("button", { name: "Cancel" }).click()
  await expect(scopeCard(page, "Observe")).toBeVisible()
  await expect(page.getByText("ABCD-1234")).toHaveCount(0)
  expect(world.calls.map((call) => `${call.method} ${call.path}`)).toEqual([
    "DELETE /dashboard/link/request",
  ])
})

test("a refused start says what failed beside the button", async ({ page }) => {
  const world = await mock(page, unlinked)
  world.startRefusal = {
    error: { code: "site_unreachable", message: "just-dashboard.com did not answer within 15s" },
  }
  await page.goto("/dashboard/link")
  await page.getByRole("button", { name: "Link", exact: true }).click()

  // Next.js keeps its own route announcer in an alert; this is the one holding the refusal.
  const alert = page.getByRole("alert").filter({ hasText: "Could not start linking" })
  await expect(alert).toContainText("Could not start linking")
  await expect(alert).toContainText("just-dashboard.com did not answer within 15s")
  // Nothing was started, so the form stays and can be tried again.
  await expect(scopeCard(page, "Observe")).toBeVisible()
  await expect(page.getByRole("button", { name: "Link", exact: true })).toBeEnabled()
})

test("the outcome of the last request is said once", async ({ page }) => {
  await mock(page, {
    ...unlinked,
    lastOutcome: { result: "denied", at: iso(now - 60_000) },
  })
  await page.goto("/dashboard/link")
  await expect(page.getByText("The link request was denied")).toBeVisible()

  await page.getByRole("button", { name: "Dismiss" }).click()
  await expect(page.getByText("The link request was denied")).toHaveCount(0)
  await page.reload()
  await expect(scopeCard(page, "Observe")).toBeVisible()
  await expect(page.getByText("The link request was denied")).toHaveCount(0)
})

for (const [result, message, title] of [
  ["expired", undefined, "The link request expired"],
  ["failed", "the site refused the key", "Linking failed"],
] as const) {
  test(`a request that ${result} says so`, async ({ page }) => {
    await mock(page, { ...unlinked, lastOutcome: { result, at: iso(now - 60_000), message } })
    await page.goto("/dashboard/link")
    await expect(page.getByText(title)).toBeVisible()
    if (message) await expect(page.getByText(message)).toBeVisible()
  })
}

test("linking switched off explains the switch and offers nothing", async ({ page }) => {
  await mock(page, { ...unlinked, enabled: false })
  await page.goto("/dashboard/link")

  await expect(page.getByText("Linking is turned off")).toBeVisible()
  await expect(page.getByText("JD_ACCOUNT_LINK=false")).toBeVisible()
  await expect(page.getByRole("button", { name: "Link", exact: true })).toHaveCount(0)
  await expect(page.locator("[data-slot=choice-card]")).toHaveCount(0)
  await expect(page.getByText("sudo ./scripts/manage.sh link")).toHaveCount(0)
  await expect(page.locator("[data-slot=page] button, [data-slot=page] a")).toHaveCount(0)
})

test("linked: the link's state, what is shared as it was sent, the policy and the keys", async ({
  page,
}) => {
  await mock(page, linked("operate"))
  await page.goto("/dashboard/link")

  await expect(page.getByText("Linked", { exact: true }).first()).toBeVisible()
  await expect(page.getByText("alice@example.com").first()).toBeVisible()
  await expect(page.getByText("Operate scope")).toBeVisible()

  // Three readings of the link.
  const tiles = page.locator("[data-slot=stat-tile]")
  await expect(tiles).toHaveCount(3)
  await expect(tiles.nth(0)).toContainText("by operator")
  await expect(tiles.nth(1)).toContainText("just now")
  await expect(tiles.nth(2)).toContainText("container.restart")
  await expect(tiles.nth(2)).toContainText("succeeded")

  // The scope in force is the chosen card.
  await expect(scopeCard(page, "Operate")).toHaveAttribute("aria-pressed", "true")
  await expect(page.getByRole("button", { name: /^Change to/ })).toHaveCount(0)

  // What was actually sent, grouped as it was.
  const shared = page.locator("section", { hasText: "What this server shares" }).first()
  for (const group of ["Heartbeat", "Server", "Access", "Metrics", "Containers"]) {
    await expect(shared.getByRole("heading", { name: group, exact: true })).toBeVisible()
  }
  await expect(shared.getByText("Ubuntu 24.04")).toBeVisible()
  await expect(shared.getByText("23.4%")).toBeVisible()
  await expect(shared.getByText("atlas.tail1234.ts.net", { exact: true })).toBeVisible()
  await expect(shared.getByText(/shop-web · ghcr.io\/acme\/shop/)).toBeVisible()
  await expect(shared.getByText(/Never sent/)).toBeVisible()

  // The policy is a mirror, set elsewhere.
  const policy = page.locator("section", { hasText: "Automatic updates" })
  await expect(policy.getByText("Patch releases")).toBeVisible()
  await expect(policy.getByText("between 03:00 and 05:00, server time")).toBeVisible()
  await expect(policy.getByText(/Set from just-dashboard.com/)).toBeVisible()
  await expect(policy.getByRole("button")).toHaveCount(0)

  // Both ends of the pinned trust.
  const keys = page.locator("section", { hasText: "Keys" }).last()
  await expect(keys.getByText("21fe:31df:a154:a261")).toBeVisible()
  await expect(keys.getByText("9a0c:77b2:0e41:d5f3")).toBeVisible()

  expect(await framedPanels(page)).toEqual([])
  expect(await scrollsSideways(page)).toBe(false)
})

test("linked: a failed exchange is a warning on the link, not a dead page", async ({ page }) => {
  await mock(
    page,
    linked("observe", {
      lastError: "just-dashboard.com answered 503",
      lastHeartbeatAt: iso(now - 10 * 60_000),
      lastCommand: {
        id: "cmd_2",
        kind: "dashboard.update.install",
        status: "refused",
        issuedBy: "account:alice@example.com",
        at: iso(now - 600_000),
      },
    }),
  )
  await page.goto("/dashboard/link")

  await expect(page.getByText("Linked, not reaching the site")).toBeVisible()
  await expect(page.getByText("The last exchange with just-dashboard.com failed")).toBeVisible()
  await expect(page.getByText("just-dashboard.com answered 503")).toBeVisible()
  await expect(page.locator("[data-slot=stat-tile]").nth(1)).toContainText(/10m (\d+s )?ago/)
  await expect(page.locator("[data-slot=stat-tile]").nth(2)).toContainText("refused")
})

test("linked: a scope change is a choice and then a command, and can be dropped", async ({
  page,
}) => {
  const world = await mock(page, linked("observe"))
  await page.goto("/dashboard/link")
  await expect(scopeCard(page, "Observe")).toHaveAttribute("aria-pressed", "true")

  await scopeCard(page, "Maintain").click()
  await expect(scopeCard(page, "Maintain")).toHaveAttribute("aria-pressed", "true")
  expect(world.calls).toEqual([])
  await page.getByRole("button", { name: "Discard" }).click()
  await expect(scopeCard(page, "Observe")).toHaveAttribute("aria-pressed", "true")
  await expect(page.getByRole("button", { name: /^Change to/ })).toHaveCount(0)

  await scopeCard(page, "Maintain").click()
  const apply = page.getByRole("button", { name: "Change to Maintain" })
  // The one brand-faced command on the page, as the decision it finishes.
  await expect(apply).toHaveAttribute("data-variant", "default")
  await apply.click()

  await expect(page.getByText("Scope is now Maintain")).toBeVisible()
  await expect(page.getByText("Maintain scope")).toBeVisible()
  await expect(scopeCard(page, "Maintain")).toHaveAttribute("aria-pressed", "true")
  await expect(page.getByRole("button", { name: /^Change to/ })).toHaveCount(0)
  expect(world.calls).toEqual([
    { method: "PUT", path: "/dashboard/link/scope", body: { scope: "maintain" }, csrf: "1" },
  ])
})

test("linked: sync now sends a heartbeat and says so", async ({ page }) => {
  const world = await mock(page, linked("operate"))
  await page.goto("/dashboard/link")

  await page.getByRole("button", { name: "Sync now" }).click()
  await expect(page.getByText("Heartbeat sent to just-dashboard.com")).toBeVisible()
  expect(world.calls).toEqual([
    { method: "POST", path: "/dashboard/link/sync", body: undefined, csrf: "1" },
  ])
})

test("linked: unlinking asks first, and the page goes back to the choice", async ({ page }) => {
  const world = await mock(page, linked("operate"))
  await page.goto("/dashboard/link")

  await page.getByRole("button", { name: "Unlink", exact: true }).click()
  const dialog = page.getByRole("dialog", { name: "Unlink this server" })
  await expect(dialog).toBeVisible()
  await expect(dialog.getByText("stops sending snapshots to just-dashboard.com")).toBeVisible()
  await expect(dialog.getByText(/alice@example.com/)).toBeVisible()
  // Nothing has happened yet.
  expect(world.calls).toEqual([])

  await dialog.getByRole("button", { name: "Cancel" }).click()
  await expect(dialog).toHaveCount(0)
  expect(world.calls).toEqual([])

  await page.getByRole("button", { name: "Unlink", exact: true }).click()
  await page
    .getByRole("dialog", { name: "Unlink this server" })
    .getByRole("button", { name: "Unlink", exact: true })
    .click()

  await expect(scopeCard(page, "Observe")).toBeVisible()
  await expect(page.getByText("Not linked", { exact: true }).first()).toBeVisible()
  await expect(page.getByRole("button", { name: "Unlink" })).toHaveCount(0)
  expect(world.calls).toEqual([
    { method: "DELETE", path: "/dashboard/link", body: undefined, csrf: "1" },
  ])
})

test("the page does not scroll sideways, in any state, on a phone or a wide screen", async ({
  page,
}) => {
  const world = await mock(page, unlinked)
  for (const [width, height] of [
    [390, 900],
    [1280, 1800],
    [1720, 1800],
  ]) {
    await page.setViewportSize({ width, height })
    for (const [name, state] of [
      ["unlinked", unlinked],
      ["pending", pending("maintain")],
      ["linked", linked("operate", { lastError: "just-dashboard.com answered 503" })],
    ] as const) {
      world.link = state
      await page.goto("/dashboard/link")
      await expect(page.locator("[data-slot=page]")).toHaveClass(/animate-rise/)
      await expect(
        page.locator("[data-slot=choice-card], [data-slot=stat-tile]").first(),
      ).toBeVisible()
      expect(await scrollsSideways(page), `${name} at ${width}`).toBe(false)
      await page.screenshot({
        path: `test-results/account-link-${name}-${width}.png`,
        fullPage: true,
      })
    }
  }
})

test("Settings lists the page beside Configuration, for an administrator", async ({ page }) => {
  await mock(page, unlinked)
  await page.goto("/dashboard/link")
  const settings = page.locator("[data-slot=sidebar]")
  await expect(settings.locator("a[href='/dashboard/configuration']")).toBeVisible()
  await expect(settings.locator("a[href='/dashboard/link']")).toContainText("just-dashboard.com")
})
