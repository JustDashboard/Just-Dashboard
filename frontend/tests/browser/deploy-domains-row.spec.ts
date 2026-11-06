import { expect, test, type Page } from "@playwright/test"
import { expectSaved, healthyOperations, mockProject, saveSettings } from "./deploy-fixture"

const row = (page: Page, name: string) =>
  page
    .getByRole("list", { name: "Domains" })
    .getByRole("listitem")
    .filter({ has: page.getByRole("combobox", { name: `Ownership of ${name}` }) })

const name = (hostname: string, rest: Record<string, unknown> = {}) => ({
  hostname,
  https: true,
  ownership: "managed",
  ...rest,
})

async function seed(page: Page, domains: ReturnType<typeof name>[], runtimePlan = {}) {
  const fixture = await mockProject(page, { runtimePlan })
  ;(fixture.configuration() as { domains: unknown[] }).domains = domains
  return fixture
}

test("a row's controls share one axis and its readings one line", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 })
  await mockProject(page)
  await page.goto("/deploy/7/settings/domains")
  const host = "api.example.test"
  const line = row(page, host)
  await expect(line.getByText("70 days left", { exact: true })).toBeVisible()
  // The consequence moved into the tooltip, and stays the select's description.
  const owner = page.getByRole("combobox", { name: `Ownership of ${host}` })
  await expect(line.getByText("removed with the project")).toHaveClass(/sr-only/)
  await expect(owner).toHaveAccessibleDescription("removed with the project")

  const middle = async (locator: ReturnType<Page["locator"]>) => {
    const box = (await locator.boundingBox())!
    return { y: box.y + box.height / 2, height: box.height }
  }
  const controls = [
    page.getByRole("button", { name: `HTTPS on ${host}` }),
    page.getByRole("button", { name: `Password on ${host}` }),
    page.getByRole("button", { name: `Cloudflare Tunnel on ${host}` }),
    owner,
    page.getByRole("button", { name: `Actions for ${host}` }),
  ]
  const [first, ...rest] = await Promise.all(controls.map(middle))
  for (const other of rest) expect(Math.abs(other.y - first.y)).toBeLessThanOrEqual(1)
  expect((await middle(owner)).height).toBe(first.height)

  // The route and the certificate read on one line, not stacked.
  const route = (await line.getByText("Routed here", { exact: true }).boundingBox())!
  const certificate = (await line.getByText("Certificate valid", { exact: true }).boundingBox())!
  expect(Math.abs(route.y - certificate.y)).toBeLessThanOrEqual(1)
  expect((await line.boundingBox())!.height).toBeLessThanOrEqual(60)

  await owner.hover()
  await expect(page.getByRole("tooltip")).toContainText(
    "Managed: removed with the project. Linked: never removed.",
  )
  // Pressing a segment keeps its pressed look under the tooltip's own state.
  await expect(controls[0]).toHaveAttribute("data-state", "on")
  await expect(controls[1]).toHaveAttribute("data-state", "off")
})

for (const width of [390, 768]) {
  test(`a row reflows without running off the page at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    await seed(page, [
      name("a-rather-long-hostname-for-a-phone.staging.example.test"),
      name("*.example.test"),
    ])
    await page.goto("/deploy/7/settings/domains")
    await expect(page.getByRole("list", { name: "Domains" })).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  })
}

test("a wildcard has no address to open, nowhere to redirect, and no tunnel", async ({ page }) => {
  await seed(page, [name("*.example.test"), name("api.example.test"), name("www.example.test")])
  await page.goto("/deploy/7/settings/domains")
  const wildcard = row(page, "*.example.test")
  await expect(wildcard.getByRole("link", { name: /Open/ })).toHaveCount(0)

  const tunnel = page.getByRole("button", { name: "Cloudflare Tunnel on *.example.test" })
  await expect(tunnel).toHaveAttribute("aria-disabled", "true")
  await tunnel.hover()
  await expect(page.getByRole("tooltip")).toContainText("A wildcard is not routed through a tunnel")
  await tunnel.click({ force: true })
  await expect(tunnel).toHaveAttribute("aria-pressed", "false")

  await page.getByRole("button", { name: "Actions for *.example.test" }).click()
  await expect(page.getByRole("menuitem", { name: "Redirect to another domain" })).toHaveCount(0)
  await page.keyboard.press("Escape")

  // Nor is a wildcard offered as somewhere to land.
  await page.getByRole("button", { name: "Actions for www.example.test" }).click()
  await page.getByRole("menuitem", { name: "Redirect to another domain" }).click()
  await expect(page.getByRole("combobox", { name: "Sends visitors to" })).toHaveText(
    "api.example.test",
  )
  await page.getByRole("combobox", { name: "Sends visitors to" }).click()
  await expect(page.getByRole("option")).toHaveText(["api.example.test"])
})

test("a name that starts redirecting takes the names pointing at it along", async ({ page }) => {
  const fixture = await seed(page, [
    name("a.example.test"),
    name("b.example.test", { redirectTo: "a.example.test" }),
    name("c.example.test"),
  ])
  await page.goto("/deploy/7/settings/domains")
  await page.getByRole("button", { name: "Actions for a.example.test" }).click()
  await page.getByRole("menuitem", { name: "Redirect to another domain" }).click()
  await saveSettings(page)
  await expectSaved(page)
  const saved = (fixture.configuration() as { domains: { redirectTo?: string }[] }).domains
  expect(saved.map((domain) => domain.redirectTo)).toEqual([
    "c.example.test",
    "c.example.test",
    undefined,
  ])
})

test("a password left empty holds the save instead of quietly saving no password", async ({
  page,
}) => {
  await mockProject(page)
  await page.goto("/deploy/7/settings/domains")
  await page.getByRole("button", { name: "Password on api.example.test" }).click()
  const bar = page.getByRole("region", { name: "Save changes" })
  await expect(bar).toContainText("fix it to save")
  await expect(bar.getByRole("button", { name: "Save", exact: true })).toBeDisabled()

  await page.getByLabel("User name").fill("two words")
  await expect(page.getByText(/^Letters, digits, dots/)).toBeVisible()
  await page.getByLabel("User name").fill("team")
  await page.locator("#domain-password-0").fill("short")
  await expect(bar.getByRole("button", { name: "Save", exact: true })).toBeDisabled()
  await page.locator("#domain-password-0").fill("a".repeat(73))
  await expect(page.getByText("At most 72 characters", { exact: true })).toBeVisible()
  await expect(bar.getByRole("button", { name: "Save", exact: true })).toBeDisabled()
  await page.locator("#domain-password-0").fill("long enough")
  await expect(bar.getByRole("button", { name: "Save", exact: true })).toBeEnabled()
})

test("the route's sign-in and address list hold the switches they rule out", async ({ page }) => {
  await mockProject(page, {
    runtimePlan: {
      forwardAuth: {
        provider: "authelia",
        verify: "http://127.0.0.1:9091/api/verify",
        signIn: "https://auth.example.test/",
      },
      allowFrom: ["203.0.113.7"],
    },
  })
  await page.goto("/deploy/7/settings/domains")
  const https = page.getByRole("button", { name: "HTTPS on api.example.test" })
  const password = page.getByRole("button", { name: "Password on api.example.test" })
  const tunnel = page.getByRole("button", { name: "Cloudflare Tunnel on api.example.test" })
  for (const [control, tip, pressed] of [
    [https, "Stays on while sign-in is required", "true"],
    [password, "Not while sign-in is required", "false"],
    [tunnel, "Not with Only these addresses set", "false"],
  ] as const) {
    // The tip first: a press closes it until the pointer has left, and a move
    // away and straight back can land in one frame, which is no leave at all.
    await control.hover()
    // By its words, because the last tip can still be fading out beside it.
    await expect(page.getByRole("tooltip").filter({ hasText: tip })).toBeVisible()
    await control.click({ force: true })
    await expect(control).toHaveAttribute("aria-pressed", pressed)
  }
  await expect(page.getByRole("region", { name: "Save changes" })).toHaveCount(0)

  await page.getByRole("button", { name: "Add domain", exact: true }).click()
  const sheet = page.getByRole("dialog", { name: "Add domain" })
  await expect(sheet.getByRole("switch", { name: "Ask visitors for a password" })).toBeDisabled()
  await expect(
    sheet.getByRole("switch", { name: "Serve through Cloudflare Tunnel" }),
  ).toBeDisabled()
})

test("a saved tunnel closes the address list, whose error would refuse every visitor", async ({
  page,
}) => {
  await seed(page, [name("api.example.test", { tunnel: true })])
  await page.goto("/deploy/7/settings/domains")
  const access = page.getByRole("form", { name: "Access and headers" })
  await expect(access.getByText(/Not with a name served through Cloudflare Tunnel/)).toBeVisible()
  await expect(access.getByRole("textbox", { name: "Only these addresses" })).toHaveCount(0)
})

test("the add sheet reads a pasted address and refuses a name it already has", async ({ page }) => {
  await mockProject(page)
  await page.goto("/deploy/7/settings/domains")
  await page.getByRole("button", { name: "Add domain", exact: true }).click()
  const sheet = page.getByRole("dialog", { name: "Add domain" })
  const field = sheet.getByRole("textbox", { name: "Hostname" })
  const add = sheet.getByRole("button", { name: "Add domain", exact: true })

  await field.fill("HTTPS://Next.Example.test:8443/some/path?x=1")
  await field.blur()
  await expect(field).toHaveValue("next.example.test")
  await expect(add).toBeEnabled()

  await field.fill("api.example.test")
  await field.blur()
  await expect(sheet.getByText("api.example.test is already on this environment.")).toBeVisible()
  await expect(add).toBeDisabled()

  await field.fill("not_a_name")
  await field.blur()
  await expect(sheet.getByText(/^A hostname is letters/)).toBeVisible()
  await expect(add).toBeDisabled()
  await expect(sheet.getByRole("button", { name: "Check this hostname" })).toBeDisabled()

  // A wildcard cannot go through a tunnel, and the switch says so as it is typed.
  await field.fill("*.example.test")
  await expect(
    sheet.getByRole("switch", { name: "Serve through Cloudflare Tunnel" }),
  ).toBeDisabled()
})

test("turning tailnet-only on takes the edge rules away too", async ({ page }) => {
  await mockProject(page, {
    runtimePlan: {
      edgeRules: [
        {
          name: "block-admin",
          match: { pathPrefix: "/admin" },
          action: { kind: "block", status: 403 },
        },
      ],
      rateLimit: { requests: 10, per: "second", burst: 20 },
      crowdSec: true,
    },
  })
  const puts: { runtime: Record<string, unknown> }[] = []
  page.on("request", (request) => {
    if (request.method() === "PUT" && request.url().endsWith("/configuration")) {
      puts.push(request.postDataJSON())
    }
  })
  await page.goto("/deploy/7/settings/domains")
  const tailnet = page.getByRole("form", { name: "Tailnet" })
  await tailnet.getByRole("switch", { name: "Tailnet only" }).click()
  await expect(tailnet.getByText(/the edge rules, the rate limit, the CrowdSec list/)).toBeVisible()
  await saveSettings(page)
  await expect.poll(() => puts.length).toBe(1)
  for (const field of ["edgeRules", "rateLimit", "crowdSec"])
    expect(puts[0].runtime[field]).toBeUndefined()
})

test("Enter in a header's name does not save the page with it half typed", async ({ page }) => {
  await mockProject(page)
  const puts: unknown[] = []
  page.on("request", (request) => {
    if (request.method() === "PUT" && request.url().endsWith("/configuration")) puts.push(request)
  })
  await page.goto("/deploy/7/settings/domains")
  const access = page.getByRole("form", { name: "Access and headers" })
  await access.getByRole("switch", { name: "HSTS" }).click()
  const header = access.getByRole("textbox", { name: "Response headers: name" })
  await header.fill("X-Robots-Tag")
  await header.press("Enter")
  await expect(access.getByText("X-Robots-Tag: (empty)")).toBeVisible()
  expect(puts).toHaveLength(0)
})

test("the maintenance page is edited from its own section", async ({ page }) => {
  await mockProject(page)
  await page.goto("/deploy/7/settings/domains")
  const pages = page.getByRole("form", { name: "Maintenance page and error pages" })
  await expect(pages.getByRole("button", { name: "Edit Maintenance page" })).toBeVisible()
  // Nothing to edit under Error pages until a code is saved.
  await expect(pages.getByRole("button", { name: /^Edit \d/ })).toHaveCount(0)
  await pages.getByRole("switch", { name: "Own 404 Not found page" }).click()
  await expect(pages.getByRole("button", { name: /^Edit \d/ })).toHaveCount(0)
})

test("an unread certificate says what is unread", async ({ page }) => {
  await mockProject(page, {
    operations: {
      ...healthyOperations,
      domains: {
        ...healthyOperations.domains,
        domains: [
          {
            ...healthyOperations.domains.domains[0],
            certificate: "unavailable",
            certificateDaysLeft: undefined,
          },
        ],
      },
    },
  })
  await page.goto("/deploy/7/settings/domains")
  await expect(page.getByText("Certificate not observed", { exact: true })).toBeVisible()
})
