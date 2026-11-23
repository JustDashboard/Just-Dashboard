import { expect, test, type Page } from "@playwright/test"
import {
  ADDRESS,
  appDomainSuggestion,
  held,
  mockAppDomain,
  notLinked,
  readyToClaim,
  ZONE,
} from "./app-domain-fixture"
import {
  expectSaved,
  gotoStep,
  healthyOperations,
  json,
  mockNewProject,
  mockProject,
  saveSettings,
} from "./deploy-fixture"

/**
 * The just-dashboard.com address where deployments meet it: a choice (and the default when
 * new deployments get it) on the new-project public address step, a quiet hint where the
 * server holds none, the control on a deployment's Domains settings that adds or removes its
 * name through the ordinary save, and the marker on a name the server no longer holds
 * (DESIGN 3.4, 3.5 and 4).
 */

const sslip = {
  hostname: "wesmokefish-a1b2c3.203-0-113-7.sslip.io",
  base: "203-0-113-7.sslip.io",
  covered: false,
  certificateMethod: "nginx",
  method: "sslip",
  address: "203.0.113.7",
  detail: "Resolves to 203.0.113.7 with no DNS record to create.",
}

/**
 * `/deploy/hostname`: `fallback` for the plain question, the app-domain name for
 * `method=app-domain`, and a check of any typed name that says only that it is a custom one.
 */
async function hostnames(page: Page, fallback: Record<string, unknown>) {
  const asked: string[] = []
  await page.route("**/api/v1/deploy/hostname**", (route) => {
    const url = new URL(route.request().url())
    asked.push(url.search)
    if (url.searchParams.get("method") === "app-domain") {
      return json(route, appDomainSuggestion(url.searchParams.get("name") ?? "app"))
    }
    // The suggestion without the address, which the step offers beside an app-domain default.
    if (url.searchParams.get("method") === "standard") return json(route, sslip)
    const typed = url.searchParams.get("hostname")
    if (typed) {
      return json(route, {
        hostname: typed,
        covered: false,
        certificateMethod: "nginx",
        method: "custom",
        address: ADDRESS,
        detail: "No certificate covers it yet; one can be issued for it from here.",
      })
    }
    return json(route, fallback)
  })
  return asked
}

async function openAddress(page: Page) {
  await page.goto("/deploy/new")
  await page.getByRole("button", { name: "Import operator/wesmokefish" }).click()
  await gotoStep(page, "runtime")
}

const hostnameField = (page: Page) => page.getByRole("textbox", { name: "Hostname", exact: true })
const choice = (page: Page) => page.getByRole("radiogroup", { name: "Address" })

test("new project: the app-domain name is the default when new deployments get it", async ({
  page,
}) => {
  const journey = await mockNewProject(page)
  await mockAppDomain(page, held("active"))
  const asked = await hostnames(page, appDomainSuggestion("wesmokefish"))
  await openAddress(page)

  const app = `wesmokefish.acme.${ZONE}`
  await expect(hostnameField(page)).toHaveValue(app)
  await expect(choice(page).getByRole("radio", { name: ZONE })).toBeChecked()
  await expect(page.getByText("HTTPS ready")).toBeVisible()
  // The default was the app-domain name: nothing else had to be asked for it.
  expect(asked.filter((query) => query.includes("method=app-domain"))).toEqual([])

  // sslip.io stays a choice: the name the backend suggests without the address
  // (`method=standard`), not one composed in the browser.
  expect(asked.filter((query) => query.includes("method=standard"))).toHaveLength(1)
  await choice(page).getByRole("radio", { name: "sslip.io" }).click()
  await expect(hostnameField(page)).toHaveValue(sslip.hostname)
  await expect(page.getByText("Certificate on deploy")).toBeVisible()

  // Your own domain empties the field and puts the cursor in it.
  await choice(page).getByRole("radio", { name: "Your domain" }).click()
  await expect(hostnameField(page)).toHaveValue("")
  await expect(hostnameField(page)).toBeFocused()
  await hostnameField(page).fill("shop.example.com")
  await expect(choice(page).getByRole("radio", { name: "Your domain" })).toBeChecked()

  await choice(page).getByRole("radio", { name: ZONE }).click()
  await expect(hostnameField(page)).toHaveValue(app)
  await gotoStep(page, "review")
  await page.getByRole("button", { name: "Save only", exact: true }).click()
  await expect.poll(() => journey.commits()).toBe(1)
  expect(journey.configuration()).toMatchObject({
    domains: [{ hostname: app, https: true, ownership: "managed" }],
  })
})

test("new project: an uncovered app-domain name says the release issues the wildcard", async ({
  page,
}) => {
  await mockNewProject(page)
  const state = held("active")
  state.certificate = { ...state.certificate!, state: "missing" }
  await mockAppDomain(page, state)
  await hostnames(page, appDomainSuggestion("wesmokefish", false))
  await openAddress(page)
  await expect(page.getByText("Certificate on deploy")).toBeVisible()
  await expect(
    page.getByText(
      `The release issues this server's wildcard certificate for *.acme.${ZONE} through ${ZONE}`,
    ),
  ).toBeVisible()
})

test("new project: with the default off, the app-domain name is offered beside sslip.io", async ({
  page,
}) => {
  await mockNewProject(page)
  await mockAppDomain(page, held("active", { autoAssign: false }))
  const asked = await hostnames(page, sslip)
  await openAddress(page)

  await expect(hostnameField(page)).toHaveValue(sslip.hostname)
  await expect(choice(page).getByRole("radio", { name: "sslip.io" })).toBeChecked()
  await choice(page).getByRole("radio", { name: ZONE }).click()
  await expect(hostnameField(page)).toHaveValue(`wesmokefish.acme.${ZONE}`)
  await expect(page.getByText("HTTPS ready")).toBeVisible()
  expect(asked.some((query) => query.includes("method=app-domain"))).toBe(true)
  // Back to the suggestion, with its own reading.
  await choice(page).getByRole("radio", { name: "sslip.io" }).click()
  await expect(hostnameField(page)).toHaveValue(sslip.hostname)
  await expect(page.getByText("Certificate on deploy")).toBeVisible()
})

for (const [name, state, words] of [
  ["not linked", notLinked, "once this server is linked and holds one"],
  ["holding none", readyToClaim(), "once this server claims one"],
] as const) {
  test(`new project: a server ${name} gets a quiet hint, not a choice`, async ({ page }) => {
    await mockNewProject(page)
    await mockAppDomain(page, state)
    await hostnames(page, sslip)
    await openAddress(page)
    await expect(hostnameField(page)).toHaveValue(sslip.hostname)
    await expect(choice(page)).toHaveCount(0)
    await expect(page.getByText(words, { exact: false })).toBeVisible()
    await expect(page.getByRole("link", { name: "Settings › Connect" })).toHaveAttribute(
      "href",
      "/dashboard/link#app-domain",
    )
    // Not a blocking state: the step goes on.
    await expect(page.getByRole("button", { name: "Continue", exact: true })).toBeEnabled()
  })
}

test("new project: a backend without app domains shows the step as it was", async ({ page }) => {
  await mockNewProject(page)
  await hostnames(page, sslip)
  await openAddress(page)
  await expect(hostnameField(page)).toHaveValue(sslip.hostname)
  await expect(choice(page)).toHaveCount(0)
  await expect(page.getByRole("link", { name: "Settings › Connect" })).toHaveCount(0)
})

for (const width of [390, 1280]) {
  test(`new project: the address choice fits at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    await mockNewProject(page)
    await mockAppDomain(page, held("active"))
    await hostnames(page, appDomainSuggestion("wesmokefish"))
    await openAddress(page)
    await expect(choice(page)).toBeVisible()
    const group = (await choice(page).boundingBox())!
    expect(group.x + group.width).toBeLessThanOrEqual(width)
    // The choice is one line above the field it fills.
    const field = (await hostnameField(page).boundingBox())!
    expect(group.y + group.height).toBeLessThan(field.y)
    expect(group.height).toBeLessThanOrEqual(44)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    await page
      .locator("#public-hostname")
      .locator("xpath=ancestor::section[1]")
      .screenshot({ path: `test-results/app-domain-new-project-${width}.png` })
  })
}

// Domains settings ---------------------------------------------------------------

const APP = `api-production.acme.${ZONE}`

async function domainsPage(
  page: Page,
  domains: Record<string, unknown>[],
  state = held("active"),
  operations = healthyOperations,
) {
  const fixture = await mockProject(page, { operations })
  ;(fixture.configuration() as { domains: unknown[] }).domains = domains
  const world = await mockAppDomain(page, state)
  await hostnames(page, sslip)
  await page.goto("/deploy/7/settings/domains")
  await expect(page.getByRole("list", { name: "Domains" })).toBeVisible()
  return { fixture, world }
}

const saved = (fixture: Awaited<ReturnType<typeof mockProject>>) =>
  (fixture.configuration() as { domains: { hostname: string }[] }).domains.map(
    (domain) => domain.hostname,
  )

const appSwitch = (page: Page) => page.getByRole("switch", { name: `${ZONE} address` })

const row = (page: Page, name: string) =>
  page
    .getByRole("list", { name: "Domains" })
    .getByRole("listitem")
    .filter({ has: page.getByRole("combobox", { name: `Ownership of ${name}` }) })

const api = { hostname: "api.example.test", https: true, ownership: "managed" }

/** The observed routes with the backend's own mark on one name: detached. */
function markedDetached(hostname: string) {
  const operations = structuredClone(healthyOperations)
  operations.domains = {
    ...operations.domains,
    status: "available",
    domains: [
      ...operations.domains.domains,
      {
        hostname,
        https: true,
        ownership: "managed",
        route: "served",
        certificate: "valid",
        appDomain: { status: "detached" },
      } as (typeof operations.domains.domains)[number],
    ],
  }
  return operations
}

test("domains: the address is switched on beside the current names, and off again", async ({
  page,
}) => {
  const { fixture } = await domainsPage(page, [api])
  await expect(appSwitch(page)).not.toBeChecked()
  await expect(page.getByText(APP)).toBeVisible()

  // On: added through the same save Add domain uses, beside the name already there.
  await appSwitch(page).click()
  await expect(page.getByText("Domains saved")).toBeVisible()
  await expect.poll(() => saved(fixture)).toEqual(["api.example.test", APP])
  await expect(appSwitch(page)).toBeChecked()
  await expect(row(page, APP)).toBeVisible()

  // A name under the address is never routed through a tunnel.
  const tunnel = page.getByRole("button", { name: `Cloudflare Tunnel on ${APP}` })
  await expect(tunnel).toHaveAttribute("aria-disabled", "true")
  await tunnel.hover()
  await expect(page.getByRole("tooltip")).toContainText("never routed through a tunnel")

  // Off: removed as the menu's Remove does, waiting for the save bar.
  await appSwitch(page).click()
  await expect(appSwitch(page)).not.toBeChecked()
  await expect(row(page, APP)).toHaveCount(0)
  expect(saved(fixture)).toEqual(["api.example.test", APP])
  await saveSettings(page)
  await expectSaved(page)
  expect(saved(fixture)).toEqual(["api.example.test"])
})

test("domains: a deployment served through the tunnel is not offered the address", async ({
  page,
}) => {
  await domainsPage(page, [{ ...api, tunnel: true }])
  await expect(row(page, "api.example.test")).toBeVisible()
  await expect(appSwitch(page)).toHaveCount(0)
})

test("domains: no address held, no control", async ({ page }) => {
  await domainsPage(page, [api], readyToClaim())
  await expect(row(page, "api.example.test")).toBeVisible()
  await expect(appSwitch(page)).toHaveCount(0)
})

test("domains: a name under a handle this server no longer holds is marked, and removable", async ({
  page,
}) => {
  // The handle this server held (acme), now detached. A name under a handle that was never
  // this server's, or the zone's own name, is an ordinary domain and never marked.
  const old = `api-production.acme.${ZONE}`
  const { fixture } = await domainsPage(
    page,
    [
      api,
      { hostname: old, https: true, ownership: "managed" },
      { hostname: "www.example.test", https: true, ownership: "managed" },
      { hostname: ZONE, https: true, ownership: "managed" },
      { hostname: `shop.other.${ZONE}`, https: true, ownership: "managed" },
    ],
    held("detached"),
  )
  const line = row(page, old)
  const marker = line.getByRole("button", { name: `${old} is detached` })
  await expect(marker).toBeVisible()
  await expect(row(page, "api.example.test").getByText("Detached")).toHaveCount(0)
  await expect(row(page, ZONE).getByText("Detached")).toHaveCount(0)
  await expect(row(page, `shop.other.${ZONE}`).getByText("Detached")).toHaveCount(0)

  // The marker shares the row's one line of readings, so the row keeps its height: what it
  // means is a layer over the row, never a band under it.
  const middle = async (locator: ReturnType<Page["locator"]>) => {
    const box = (await locator.boundingBox())!
    return box.y + box.height / 2
  }
  const beside = line.getByText("Routes on the next deployment")
  expect(Math.abs((await middle(marker)) - (await middle(beside)))).toBeLessThanOrEqual(1)
  // Against a plain name in the same place in the list (the first row has no top padding).
  const plain = (await row(page, "www.example.test").boundingBox())!.height
  expect(Math.abs((await line.boundingBox())!.height - plain)).toBeLessThanOrEqual(1)

  await marker.click()
  const layer = page.getByRole("dialog").filter({ hasText: "Not this server's address any more" })
  await expect(layer).toContainText("does not resolve")
  await expect(layer.getByRole("link", { name: "Settings › Connect" })).toHaveAttribute(
    "href",
    "/dashboard/link#app-domain",
  )
  await layer.getByRole("button", { name: "Remove" }).click()
  await expect(row(page, old)).toHaveCount(0)
  await saveSettings(page)
  await expectSaved(page)
  expect(saved(fixture)).toEqual([
    "api.example.test",
    "www.example.test",
    ZONE,
    `shop.other.${ZONE}`,
  ])
})

test("domains: the backend's own mark says detached even when the handle matches", async ({
  page,
}) => {
  const operations = structuredClone(healthyOperations)
  operations.domains = {
    ...operations.domains,
    status: "available",
    domains: [
      ...operations.domains.domains,
      {
        hostname: APP,
        https: true,
        ownership: "managed",
        route: "served",
        certificate: "valid",
        appDomain: { status: "detached" },
      } as (typeof operations.domains.domains)[number],
    ],
  }
  await domainsPage(
    page,
    [api, { hostname: APP, https: true, ownership: "managed" }],
    held("active"),
    operations,
  )
  await expect(row(page, APP).getByRole("button", { name: `${APP} is detached` })).toBeVisible()
})

for (const width of [390, 1280]) {
  test(`domains: the control and the marker fit at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    const old = `api-production.old.${ZONE}`
    await domainsPage(
      page,
      [api, { hostname: old, https: true, ownership: "managed" }],
      held("active"),
      markedDetached(old),
    )
    await expect(appSwitch(page)).toBeVisible()
    await expect(row(page, old).getByRole("button", { name: `${old} is detached` })).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    await page
      .getByRole("list", { name: "Domains" })
      .locator("xpath=ancestor::section[1]")
      .screenshot({ path: `test-results/app-domain-domains-${width}.png` })
  })
}
