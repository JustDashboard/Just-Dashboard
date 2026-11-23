import { expect, test, type Page, type Route } from "@playwright/test"
import {
  ADDRESS,
  blogUse,
  held,
  mockAppDomain,
  notLinked,
  notOffered,
  offered,
  readyToClaim,
  shopUse,
  turnedOff,
  unreachable,
  type DomainState,
} from "./app-domain-fixture"

/**
 * Settings → Connect's "just-dashboard.com address" in every state the backend reports, the
 * refusals said as sentences, and the unlink confirmation's warning (DESIGN 4, lifecycle 3.5).
 * The link itself is a fixed answer here; `account-link.spec.ts` follows it through its states.
 */

const iso = (ms: number) => new Date(ms).toISOString()
const now = Date.now()

const session = {
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

const site = {
  enabled: true,
  siteUrl: "https://just-dashboard.com",
  fingerprint: "21fe:31df:a154:a261",
}
const unlinked = { ...site, state: "unlinked" }
const linked = {
  ...site,
  state: "linked",
  link: {
    serverId: "srv_01",
    owner: "alice@example.com",
    scope: "observe",
    linkedAt: iso(now - 86_400_000),
    linkedBy: "operator",
    lastHeartbeatAt: iso(now - 1_000),
    siteKeys: [{ kid: "k1", fingerprint: "9a0c:77b2:0e41:d5f3" }],
  },
}

async function mock(page: Page, link: Record<string, unknown>, domain: DomainState) {
  const calls: string[] = []
  const respond = (route: Route, body: unknown, status = 200) =>
    route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname.replace(/^\/api\/v1/, "")
    if (path === "/auth/session") return respond(route, session)
    if (path === "/dashboard/update") {
      return respond(route, { version: "0.9.1", latest: "0.9.1", available: false, releases: [] })
    }
    if (path === "/dashboard/link" && request.method() === "GET") return respond(route, link)
    if (path === "/dashboard/link" && request.method() === "DELETE") {
      calls.push("DELETE /dashboard/link")
      link = unlinked
      return route.fulfill({ status: 204 })
    }
    return respond(route, [])
  })
  // Registered after the catch-all, so it answers first.
  const world = await mockAppDomain(page, domain)
  return { world, calls }
}

const section = (page: Page) =>
  page.locator("section").filter({
    has: page.getByRole("heading", { name: /address$/, level: 2 }),
  })

function scrollsSideways(page: Page) {
  return page.evaluate(
    () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
  )
}

test("not linked: what the address is, and the link action above it", async ({ page }) => {
  await mock(page, unlinked, notLinked)
  await page.goto("/dashboard/link")
  const address = section(page)
  await expect(address.getByRole("heading", { name: "just-dashboard.com address" })).toBeVisible()
  await expect(address).toContainText("Link this server above to claim one.")
  await expect(address.getByText("shop.acme.just-dashboard.com")).toBeVisible()
  await expect(address.getByRole("button")).toHaveCount(0)
  // The section sits under the link's own action, which is still the one way to link.
  const link = await page
    .getByRole("button", { name: "Link this server", exact: true })
    .boundingBox()
  const here = await address.boundingBox()
  expect(link!.y).toBeLessThan(here!.y)
})

test("linked, not offered: the site's reason and nothing to press", async ({ page }) => {
  await mock(page, linked, notOffered)
  await page.goto("/dashboard/link")
  const address = section(page)
  await expect(address.getByText("just-dashboard.com is not offering addresses")).toBeVisible()
  await expect(address.getByText("Addresses are not set up on this site yet.")).toBeVisible()
  await expect(address.getByRole("button")).toHaveCount(0)
  await expect(address.getByRole("textbox")).toHaveCount(0)
})

test("linked, site unreachable: a warning with the reason", async ({ page }) => {
  await mock(page, linked, unreachable)
  await page.goto("/dashboard/link")
  const address = section(page)
  await expect(address.getByText("Could not ask just-dashboard.com")).toBeVisible()
  await expect(address.getByText(/did not answer within 15s/)).toBeVisible()
  await expect(address.getByRole("button", { name: "Claim" })).toHaveCount(0)
})

test("turned off: the switch named, nothing offered", async ({ page }) => {
  await mock(page, linked, turnedOff)
  await page.goto("/dashboard/link")
  const address = section(page)
  await expect(address.getByText("JD_APP_DOMAINS=false")).toBeVisible()
  await expect(address.getByRole("button")).toHaveCount(0)
})

test("claiming: the rules light as they are met, the preview uses the site's zone", async ({
  page,
}) => {
  // A zone that is not the default, so a hard-coded one would show.
  const { world } = await mock(page, linked, readyToClaim("apps.example.net"))
  await page.goto("/dashboard/link")
  const address = section(page)
  await expect(address.getByRole("heading", { name: "apps.example.net address" })).toBeVisible()
  const field = address.getByRole("textbox", { name: "Handle" })
  const claim = address.getByRole("button", { name: "Claim", exact: true })
  const preview = page.getByTestId("app-domain-preview")
  await expect(preview).toHaveText("app.handle.apps.example.net")
  await expect(claim).toBeDisabled()

  const rule = (label: string | RegExp) =>
    address.locator("[data-slot=field-checks] > span").filter({ hasText: label })
  await field.fill("a-")
  await expect(rule("3 to 20 characters")).toContainText("not yet")
  await expect(rule(/^a-z, 0-9 and -/)).toContainText("met")
  await expect(rule("Starts and ends")).toContainText("not yet")
  await field.fill("a--b")
  await expect(rule("No --")).toContainText("not yet")
  await expect(claim).toBeDisabled()
  // Typed in capitals, it is what the site will read.
  await field.fill("ACME")
  await expect(field).toHaveValue("acme")
  for (const label of ["3 to 20 characters", "Starts and ends", "No --"]) {
    await expect(rule(label)).toContainText(", met")
  }
  await expect(preview).toHaveText("app.acme.apps.example.net")
  await expect(claim).toBeEnabled()

  // The line about public DNS stands before the button.
  const line = address.getByText(/publishes this server's IP address/)
  await expect(line).toContainText(ADDRESS)
  expect((await line.boundingBox())!.y).toBeLessThan((await claim.boundingBox())!.y)

  await claim.click()
  expect(world.calls).toEqual([
    { method: "PUT", path: "/dashboard/link/domain", body: { handle: "acme" } },
  ])
  // Held now; the wildcard is issued in the background and the page follows it.
  await expect(address.getByText("acme.just-dashboard.com", { exact: true })).toBeVisible()
  await expect(address.getByText("Issuing")).toBeVisible()
  await expect(address.getByText(/^Valid until /)).toBeVisible({ timeout: 15_000 })
  await expect(address.getByRole("button", { name: /^Renew the certificate/ })).toBeVisible()
})

const refusals: [string, number, string, string | RegExp][] = [
  ["handle_taken", 409, "taken", "Another server holds that handle. Choose another."],
  ["handle_reserved", 409, "reserved", /reserved, or just-dashboard.com already uses the name/],
  [
    "handle_quarantined",
    409,
    "It frees up on 22 December 2026.",
    /released recently by another account.*22 December 2026/,
  ],
  ["limit_reached", 403, "limit", /already holds as many addresses as it may/],
  ["account_suspended", 403, "suspended", /account is suspended/],
  ["invalid_address", 400, "bad", /refused this server's address, 203\.0\.113\.10/],
  [
    "dns_failed",
    502,
    "Cloudflare answered 500.",
    /could not update DNS\. Cloudflare answered 500\. Try again/,
  ],
  ["domains_unavailable", 503, "off", "just-dashboard.com is not offering addresses right now."],
  ["session_required", 403, "api key", /need a person signed in/],
  ["not_linked", 409, "not linked", /not linked to a just-dashboard.com account any more/],
  ["rate_limited", 429, "slow down", /Try again in 2 minutes/],
  [
    "address_mismatch",
    409,
    "The address is not the one this request came from: the site saw 198.51.100.7.",
    /come from 198\.51\.100\.7, not 203\.0\.113\.10/,
  ],
]

for (const [code, status, message, words] of refusals) {
  test(`a claim refused with ${code} says what happened in words`, async ({ page }) => {
    const { world } = await mock(page, linked, readyToClaim())
    world.refusal = { status, code, message, retryAfter: code === "rate_limited" ? 120 : undefined }
    await page.goto("/dashboard/link")
    const address = section(page)
    await address.getByRole("textbox", { name: "Handle" }).fill("acme")
    await address.getByRole("button", { name: "Claim", exact: true }).click()
    const alert = page.getByRole("alert").filter({ hasText: "Could not claim the address" })
    await expect(alert).toContainText(words)
    await expect(alert).not.toContainText(code)
    // Nothing was claimed: the form stays to try another.
    await expect(address.getByRole("textbox", { name: "Handle" })).toHaveValue("acme")
  })
}

test("a new handle refused while in use lists the deployments, with links", async ({ page }) => {
  const { world } = await mock(page, linked, held("detached", { usedBy: [shopUse, blogUse] }))
  await page.goto("/dashboard/link")
  const address = section(page)
  const field = address.getByRole("textbox", { name: "Handle" })
  await expect(field).toHaveValue("acme")
  await field.fill("other")
  world.refusal = { status: 409, code: "domain_in_use", message: "in use" }
  await address.getByRole("button", { name: "Claim", exact: true }).click()
  const alert = page.getByRole("alert").filter({ hasText: "Could not claim the address" })
  await expect(alert).toContainText("Deployments still use this address. Remove it from them first")
  await expect(alert.getByRole("link", { name: "api-production" })).toHaveAttribute(
    "href",
    "/deploy/7/settings/domains?env=production",
  )
  await expect(alert.getByRole("link", { name: "blog" })).toHaveAttribute(
    "href",
    "/deploy/9/settings/domains?env=staging",
  )
})

test("active: the address, the certificate, the default and the deployments using it", async ({
  page,
}) => {
  const { world } = await mock(page, linked, held("active", { usedBy: [shopUse, blogUse] }))
  await page.goto("/dashboard/link")
  const address = section(page)
  await expect(address.getByText("acme.just-dashboard.com", { exact: true })).toBeVisible()
  await expect(address.getByText(ADDRESS)).toBeVisible()
  await expect(address.getByText(/^Valid until /)).toBeVisible()
  await expect(address.getByText("*.acme.just-dashboard.com")).toBeVisible()

  // Release is off while the address is in use, and the page says why.
  const release = address.getByRole("button", { name: "Release", exact: true })
  await expect(release).toBeDisabled()
  await expect(
    address.getByText("Release is off while 2 deployments use the address."),
  ).toBeVisible()

  // Used by: one row per name, each a way to its Domains settings, all one height.
  const used = address.getByRole("list", { name: "Deployments using the address" })
  const rows = used.locator("[data-slot=row]")
  await expect(rows).toHaveCount(2)
  await expect(used.getByRole("link", { name: /api-production/ })).toHaveAttribute(
    "href",
    "/deploy/7/settings/domains?env=production",
  )
  const heights = await Promise.all(
    (await rows.all()).map(async (row) => (await row.boundingBox())!.height),
  )
  expect(Math.max(...heights) - Math.min(...heights), heights.join(", ")).toBeLessThanOrEqual(1)

  // The default for new deployments is a switch that applies at once.
  const assign = address.getByRole("switch", { name: "New deployments get this address" })
  await expect(assign).toBeChecked()
  await assign.click()
  await expect(assign).not.toBeChecked()
  await expect(page.getByText("New deployments keep their usual address")).toBeVisible()
  expect(world.calls).toEqual([
    { method: "PUT", path: "/dashboard/link/domain/settings", body: { autoAssign: false } },
  ])
})

test("renewing shows progress and settles by itself", async ({ page }) => {
  const { world } = await mock(page, linked, held("active"))
  world.state = {
    ...world.state,
    certificate: { ...world.state.certificate!, state: "expiring" },
  }
  await page.goto("/dashboard/link")
  const address = section(page)
  await expect(address.getByText(/^Expires /)).toBeVisible()
  await address.getByRole("button", { name: /^Renew the certificate/ }).click()
  await expect(address.getByText("Issuing")).toBeVisible()
  await expect(address.getByRole("button", { name: /the certificate/ })).toHaveCount(0)
  await expect(address.getByText(/^Valid until /)).toBeVisible({ timeout: 15_000 })
  expect(world.calls.map((call) => `${call.method} ${call.path}`)).toEqual([
    "POST /dashboard/link/domain/certificate",
  ])
})

test("a failed certificate says why and offers Issue", async ({ page }) => {
  const state = held("active")
  state.certificate = {
    ...state.certificate!,
    state: "failed",
    error: "The TXT record did not appear within 120 seconds.",
  }
  await mock(page, linked, state)
  await page.goto("/dashboard/link")
  const address = section(page)
  await expect(address.getByText("Issuing failed")).toBeVisible()
  await expect(address.getByText("The TXT record did not appear within 120 seconds.")).toBeVisible()
  await expect(address.getByRole("button", { name: /^Issue the certificate/ })).toBeVisible()
})

test("releasing asks first, names the address, and goes back to the claim", async ({ page }) => {
  const { world } = await mock(page, linked, held("active"))
  await page.goto("/dashboard/link")
  const address = section(page)
  await address.getByRole("button", { name: "Release", exact: true }).click()
  const dialog = page.getByRole("dialog", { name: "Release address" })
  await expect(dialog).toContainText("acme.just-dashboard.com")
  await expect(dialog).toContainText("for the next 30 days")
  expect(world.calls).toEqual([])
  await dialog.getByRole("button", { name: "Release", exact: true }).click()
  await expect(page.getByText("Released acme.just-dashboard.com")).toBeVisible()
  await expect(address.getByRole("textbox", { name: "Handle" })).toBeVisible()
  expect(world.calls.map((call) => `${call.method} ${call.path}`)).toEqual([
    "DELETE /dashboard/link/domain",
  ])
})

test("suspended: the admin's reason, what it means, and who it leaves without a name", async ({
  page,
}) => {
  const state = held("suspended", { usedBy: [shopUse] })
  state.domain!.reason = "Reported for phishing."
  await mock(page, linked, state)
  await page.goto("/dashboard/link")
  const address = section(page)
  await expect(address.getByText("acme.just-dashboard.com is suspended")).toBeVisible()
  await expect(
    address.getByText(/Reported for phishing\. Its DNS records are removed/),
  ).toBeVisible()
  await expect(address.getByText(/Only a just-dashboard.com administrator can lift/)).toBeVisible()
  await expect(address.getByRole("link", { name: "api-production" })).toBeVisible()
  await expect(address.getByRole("textbox", { name: "Handle" })).toHaveCount(0)
})

test("detached: the reason, the names it affects, and the claim to take it back", async ({
  page,
}) => {
  const state = held("detached", { usedBy: [shopUse] })
  state.domain!.reason = "Released on just-dashboard.com"
  const { world } = await mock(page, linked, state)
  await page.goto("/dashboard/link")
  const address = section(page)
  await expect(
    address.getByText("acme.just-dashboard.com is no longer this server's"),
  ).toBeVisible()
  await expect(
    address.getByText(/Released on just-dashboard.com\. Apps under it do not resolve/),
  ).toBeVisible()
  await expect(address.getByRole("button", { name: "Release" })).toHaveCount(0)
  await expect(address.getByRole("textbox", { name: "Handle" })).toHaveValue("acme")
  await address.getByRole("button", { name: "Claim", exact: true }).click()
  expect(world.calls[0]).toEqual({
    method: "PUT",
    path: "/dashboard/link/domain",
    body: { handle: "acme" },
  })
  await expect(address.getByText("acme.just-dashboard.com is no longer this server's")).toHaveCount(
    0,
  )
})

test("unlinking a server whose address is in use names the names that stop resolving", async ({
  page,
}) => {
  const { calls } = await mock(page, linked, held("active", { usedBy: [shopUse, blogUse] }))
  await page.goto("/dashboard/link")
  await expect(section(page).getByText(/^Valid until /)).toBeVisible()
  await page.getByRole("button", { name: "Unlink", exact: true }).click()
  const dialog = page.getByRole("dialog", { name: "Unlink this server" })
  await expect(dialog).toContainText("just-dashboard.com also releases acme.just-dashboard.com")
  await expect(dialog).toContainText("these names stop resolving")
  const names = dialog.getByRole("list", { name: "Names that stop resolving" })
  await expect(names.getByRole("listitem")).toHaveText([
    "shop.acme.just-dashboard.com · api-production · production",
    "blog.acme.just-dashboard.com · blog · staging",
  ])
  expect(calls).toEqual([])
  await dialog.getByRole("button", { name: "Unlink", exact: true }).click()
  await expect.poll(() => calls).toEqual(["DELETE /dashboard/link"])
})

test("unlinking with an address nobody uses says nothing extra", async ({ page }) => {
  await mock(page, linked, held("active"))
  await page.goto("/dashboard/link")
  await expect(section(page).getByText(/^Valid until /)).toBeVisible()
  await page.getByRole("button", { name: "Unlink", exact: true }).click()
  const dialog = page.getByRole("dialog", { name: "Unlink this server" })
  await expect(dialog).toContainText("stops sending snapshots")
  await expect(dialog.getByText(/stop resolving/)).toHaveCount(0)
})

for (const [width, height] of [
  [390, 900],
  [1280, 1400],
]) {
  test(`every state fits the page at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height })
    const { world } = await mock(page, linked, readyToClaim())
    const states: [string, DomainState][] = [
      ["claim", readyToClaim()],
      ["active", held("active", { usedBy: [shopUse, blogUse] })],
      ["suspended", held("suspended", { usedBy: [shopUse] })],
      ["detached", held("detached", { usedBy: [shopUse] })],
      ["not-offered", notOffered],
    ]
    for (const [name, state] of states) {
      world.state = state
      await page.goto("/dashboard/link")
      await expect(section(page)).toBeVisible()
      expect(await scrollsSideways(page), `${name} at ${width}`).toBe(false)
      await section(page).screenshot({ path: `test-results/app-domain-${name}-${width}.png` })
    }
  })
}

// Claiming at link time (AUTOCLAIM.md) -------------------------------------------------------

const pending = {
  ...site,
  state: "pending",
  request: {
    userCode: "ABCD-1234",
    approveUrl: "https://just-dashboard.com/link?code=ABCD-1234",
    expiresAt: iso(Date.now() + 14 * 60_000),
    scope: "observe",
  },
}

const ADDRESS_LINE =
  "When you approve the link on just-dashboard.com, you can give this server an address for its apps there."

for (const [name, link] of [
  ["not linked", unlinked],
  ["waiting for approval", pending],
] as const) {
  test(`${name}: the link card says the address is chosen on the approval page`, async ({
    page,
  }) => {
    await mock(page, link, notLinked)
    await page.goto("/dashboard/link")
    const line = page.getByText(ADDRESS_LINE)
    await expect(line).toBeVisible()
    await expect(line).toHaveCount(1)
    // It belongs to the link card, above the address section, and is only a line: nothing to press.
    expect((await line.boundingBox())!.y).toBeLessThan((await section(page).boundingBox())!.y)
    expect((await line.boundingBox())!.height).toBeLessThanOrEqual(40)
  })
}

test("with app domains off the link card says nothing about an address", async ({ page }) => {
  await mock(page, unlinked, turnedOff)
  await page.goto("/dashboard/link")
  await expect(page.getByRole("button", { name: "Link this server", exact: true })).toBeVisible()
  await expect(page.getByText(ADDRESS_LINE)).toHaveCount(0)
})

test("an offered address is claimed by itself, and the page follows it to the end", async ({
  page,
}) => {
  const { world } = await mock(page, linked, offered())
  world.claimReads = 3
  await page.goto("/dashboard/link")
  const address = section(page)
  await expect(address.getByRole("status")).toContainText(
    "Claiming acme.just-dashboard.com, the address chosen when this server's link was approved",
  )
  // Nothing to decide while it is under way.
  await expect(address.getByRole("textbox", { name: "Handle" })).toHaveCount(0)
  await expect(address.getByRole("button")).toHaveCount(0)
  // The page asks every three seconds until it settles: held, then the wildcard valid.
  await expect(address.getByText("acme.just-dashboard.com", { exact: true })).toBeVisible({
    timeout: 15_000,
  })
  await expect(address.getByText(/^Valid until /)).toBeVisible({ timeout: 15_000 })
  await expect(address.getByText(/^Claiming/)).toHaveCount(0)
  // The dashboard claimed it; the page sent nothing.
  expect(world.calls).toEqual([])
})

test("a failed automatic claim says why, with the claim form filled in", async ({ page }) => {
  const reason = "Another server holds that handle. Choose another."
  const { world } = await mock(page, linked, offered(reason))
  await page.goto("/dashboard/link")
  const address = section(page)
  const notice = address.getByText("Could not claim acme.just-dashboard.com")
  await expect(notice).toBeVisible()
  await expect(address.getByText(reason)).toBeVisible()
  const field = address.getByRole("textbox", { name: "Handle" })
  await expect(field).toHaveValue("acme")
  // The reason stands above the form it is about.
  expect((await notice.boundingBox())!.y).toBeLessThan((await field.boundingBox())!.y)
  await expect(page.getByTestId("app-domain-preview")).toHaveText("app.acme.just-dashboard.com")

  await field.fill("acme-shop")
  await address.getByRole("button", { name: "Claim", exact: true }).click()
  expect(world.calls).toEqual([
    { method: "PUT", path: "/dashboard/link/domain", body: { handle: "acme-shop" } },
  ])
  await expect(address.getByText("acme-shop.just-dashboard.com", { exact: true })).toBeVisible()
  await expect(address.getByText("Could not claim acme.just-dashboard.com")).toHaveCount(0)
})

for (const [width, height] of [
  [390, 900],
  [1280, 1400],
]) {
  test(`both auto-claim states fit the page at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height })
    const { world } = await mock(page, linked, offered())
    world.claimReads = null
    for (const [name, state] of [
      ["claiming", offered()],
      ["claim-failed", offered("The site saw this server's request come from 198.51.100.7.")],
    ] as const) {
      world.state = state
      await page.goto("/dashboard/link")
      await expect(
        section(page)
          .getByText(/^Claiming|^Could not claim/)
          .first(),
      ).toBeVisible()
      expect(await scrollsSideways(page), `${name} at ${width}`).toBe(false)
      await section(page).screenshot({ path: `test-results/app-domain-${name}-${width}.png` })
    }
    // And the link card's line before linking.
    await page.unroute("**/api/v1/**")
    await mock(page, unlinked, notLinked)
    await page.goto("/dashboard/link")
    await expect(page.getByText(ADDRESS_LINE)).toBeVisible()
    expect(await scrollsSideways(page)).toBe(false)
    await page
      .locator("section", { hasText: "Choose what the site can do" })
      .screenshot({ path: `test-results/app-domain-link-card-${width}.png` })
  })
}
