import { expect, test, type Page } from "@playwright/test"
import { admin, json, links, mockNetwork, type Mutation } from "./network-fixture"

const path = "/network/native/profiles/ens3"
const profile = () => ({
  checkedAt: "2026-10-08T12:00:00Z",
  device: "ens3",
  kind: "physical",
  owner: "NetworkManager",
  renderer: "NetworkManager",
  version: "1.42.4",
  profile: "fixture-uplink",
  generation: "a".repeat(64),
  editable: true,
  coverage: ["ipv4", "ipv6", "dns", "routes"],
  contract: { members: [] as string[], vrfTable: undefined as number | undefined },
  intent: {
    ipv4: {
      method: "manual",
      addresses: ["192.0.2.20/24"],
      dns: ["192.0.2.53"],
      domains: ["home.arpa"],
      ignoreAutoDns: false,
      ignoreAutoRoutes: false,
      routes: [] as { destination: string; gateway?: string; metric: number; table: number }[],
    },
    ipv6: {
      method: "auto",
      addresses: [] as string[],
      dns: [] as string[],
      domains: [] as string[],
      ignoreAutoDns: false,
      ignoreAutoRoutes: false,
      routes: [] as { destination: string; gateway?: string; metric: number; table: number }[],
    },
  },
  configured: { status: "matching", reason: "Existing profile identity verified." },
  runtime: { status: "matching", reason: "Native runtime matches this profile." },
  boot: { status: "matching", reason: "Native owner enabled; no reboot measured." },
})

const sheet = (page: Page) => page.locator("[data-slot=sheet-content]")
const banner = (page: Page) =>
  page.getByRole("complementary", { name: "Network change confirmation" })

async function setup(page: Page, value = profile(), reader = false) {
  const mutations: Mutation[] = []
  await mockNetwork(page, mutations, {
    session: reader
      ? { ...admin, capabilities: ["read"], user: { ...admin.user, role: "readonly" } }
      : admin,
    overrides: {
      [path]: value,
      "/network/links": links.map((link) =>
        link.name === value.device ? { ...link, kind: value.kind } : link,
      ),
      "/network/changes/current": { available: true, owned: false, change: null },
    },
  })
  await page.goto("/network/interfaces")
  await page.getByRole("button", { name: "Open ens3", exact: true }).click()
  return mutations
}

async function edit(page: Page) {
  await sheet(page).getByRole("button", { name: "Edit native profile", exact: true }).click()
  await sheet(page).getByLabel("IPv4 static addresses", { exact: true }).fill("192.0.2.44/24")
  await sheet(page).getByLabel("IPv4 DNS servers", { exact: true }).fill("192.0.2.54\n192.0.2.55")
  await sheet(page)
    .getByLabel("IPv4 search and route domains", { exact: true })
    .fill("lab.home.arpa\n~internal.example")
  await sheet(page).getByRole("switch", { name: "Ignore automatic IPv4 DNS" }).check()
  await sheet(page).getByRole("button", { name: "Add IPv4 route", exact: true }).click()
  await sheet(page).getByLabel("IPv4 route 1 gateway", { exact: true }).fill("192.0.2.1")
  await sheet(page).getByLabel("IPv4 route 1 metric", { exact: true }).fill("45")
}

async function apply(page: Page) {
  await sheet(page)
    .getByRole("button", { name: "Apply temporary native settings", exact: true })
    .click()
  const confirmation = page.getByRole("dialog", { name: "Apply native settings to ens3" })
  await confirmation.getByRole("button", { name: "Apply temporary settings", exact: true }).click()
  return confirmation
}

async function assertDraft(page: Page) {
  await expect(sheet(page).getByLabel("IPv4 static addresses", { exact: true })).toHaveValue(
    "192.0.2.44/24",
  )
  await expect(sheet(page).getByLabel("IPv4 DNS servers", { exact: true })).toHaveValue(
    "192.0.2.54\n192.0.2.55",
  )
  await expect(
    sheet(page).getByLabel("IPv4 search and route domains", { exact: true }),
  ).toHaveValue("lab.home.arpa\n~internal.example")
  await expect(sheet(page).getByRole("switch", { name: "Ignore automatic IPv4 DNS" })).toBeChecked()
  await expect(sheet(page).getByLabel("IPv4 route 1 gateway", { exact: true })).toHaveValue(
    "192.0.2.1",
  )
  await expect(sheet(page).getByLabel("IPv4 route 1 metric", { exact: true })).toHaveValue("45")
}

test("native apply carries the exact intent and stays pending when the managed preference is off", async ({
  page,
}) => {
  await setup(page)
  await sheet(page).getByRole("button", { name: "Close", exact: true }).click()
  const preference = page.getByRole("switch", { name: "Require network reconnection confirmation" })
  await expect(preference).toBeChecked()
  await preference.uncheck()
  await page.getByRole("button", { name: "Open ens3", exact: true }).click()
  await edit(page)
  let body: unknown
  let header: string | undefined
  await page.route(`**/api/v1${path}`, async (route) => {
    if (route.request().method() === "GET") return json(route, profile())
    body = route.request().postDataJSON()
    header = route.request().headers()["x-jd-network-apply"]
    return json(route, profile())
  })
  await apply(page)
  await expect.poll(() => header).toBe("pending")
  expect(body).toEqual({
    generation: "a".repeat(64),
    intent: {
      ipv4: {
        method: "manual",
        addresses: ["192.0.2.44/24"],
        dns: ["192.0.2.54", "192.0.2.55"],
        domains: ["lab.home.arpa", "~internal.example"],
        ignoreAutoDns: true,
        ignoreAutoRoutes: false,
        routes: [{ destination: "0.0.0.0/0", gateway: "192.0.2.1", metric: 45, table: 254 }],
      },
      ipv6: profile().intent.ipv6,
    },
  })
  await expect(sheet(page).getByRole("button", { name: "Edit native profile" })).toBeVisible()
  await expect(
    sheet(page).getByText("Native owner enabled; no reboot measured.", { exact: false }),
  ).toBeVisible()
})

for (const status of [409, 500]) {
  test(`a ${status} native apply retains every field for an explicit retry`, async ({ page }) => {
    await setup(page)
    await edit(page)
    let writes = 0
    await page.route(`**/api/v1${path}`, async (route) => {
      if (route.request().method() === "GET") return json(route, profile())
      writes++
      return json(
        route,
        {
          error: {
            code: "native_refused",
            message: "The existing native owner could not be verified.",
          },
        },
        status,
      )
    })
    const confirmation = await apply(page)
    await expect.poll(() => writes).toBe(1)
    await expect(confirmation.getByRole("button", { name: "Cancel", exact: true })).toBeEnabled()
    await confirmation.getByRole("button", { name: "Cancel", exact: true }).click()
    await expect(sheet(page).getByRole("alert")).toContainText(
      "The existing native owner could not be verified.",
    )
    await assertDraft(page)
    await expect(
      sheet(page).getByRole("button", { name: "Apply temporary native settings" }),
    ).toBeEnabled()
    expect(writes).toBe(1)
  })
}

for (const failure of ["read failure", "mismatched device"] as const) {
  test(`a later ${failure} keeps the draft and blocks native apply until a valid refresh`, async ({
    page,
  }) => {
    await page.clock.install()
    const mutations = await setup(page)
    await edit(page)
    let failed = true
    await page.route(`**/api/v1${path}`, (route) =>
      failed
        ? failure === "read failure"
          ? json(
              route,
              { error: { code: "read_failed", message: "Native ownership read failed." } },
              500,
            )
          : json(route, { ...profile(), device: "ens4" })
        : json(route, profile()),
    )
    await page.clock.fastForward(31_000)
    await expect(sheet(page).getByRole("alert")).toContainText(
      failure === "read failure"
        ? "Native ownership read failed."
        : "belongs to a different device",
    )
    await expect(sheet(page).getByRole("alert")).toContainText("Last successful read:")
    await assertDraft(page)
    await expect(
      sheet(page).getByRole("button", { name: "Apply temporary native settings" }),
    ).toBeDisabled()
    expect(mutations).toEqual([])
    failed = false
    await sheet(page).getByRole("button", { name: "Refresh", exact: true }).click()
    await expect(
      sheet(page).getByRole("button", { name: "Apply temporary native settings" }),
    ).toBeEnabled()
    await assertDraft(page)
    expect(mutations).toEqual([])
  })
}

test("a changed native generation blocks apply and rebases only the generation after review", async ({
  page,
}) => {
  await page.clock.install()
  await setup(page)
  await edit(page)
  const next = { ...profile(), generation: "b".repeat(64) }
  let body: unknown
  await page.route(`**/api/v1${path}`, async (route) => {
    if (route.request().method() === "GET") return json(route, next)
    body = route.request().postDataJSON()
    return json(route, next)
  })
  await page.clock.fastForward(31_000)
  await expect(sheet(page).getByText(/The native baseline changed/)).toBeVisible()
  await expect(
    sheet(page).getByRole("button", { name: "Apply temporary native settings" }),
  ).toBeDisabled()
  await assertDraft(page)
  await sheet(page).getByRole("button", { name: "Use latest native baseline" }).click()
  await assertDraft(page)
  await apply(page)
  await expect
    .poll(() => body)
    .toMatchObject({
      generation: "b".repeat(64),
      intent: {
        ipv4: {
          addresses: ["192.0.2.44/24"],
          dns: ["192.0.2.54", "192.0.2.55"],
          domains: ["lab.home.arpa", "~internal.example"],
          ignoreAutoDns: true,
          routes: [{ gateway: "192.0.2.1", metric: 45 }],
        },
      },
    })
})

test("an initial profile for a different device is refused before draft creation", async ({
  page,
}) => {
  const mutations = await setup(page, { ...profile(), device: "ens4" })
  await expect(sheet(page).getByRole("alert")).toContainText("belongs to a different device")
  await expect(sheet(page).getByRole("button", { name: "Edit native profile" })).toHaveCount(0)
  await expect(sheet(page).getByLabel("IPv4 static addresses", { exact: true })).toHaveCount(0)
  expect(mutations).toEqual([])
})

test("new native VRF routes inherit the observed table in both families on a phone", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await setup(page, { ...profile(), kind: "vrf", contract: { members: ["eth1"], vrfTable: 1001 } })
  await sheet(page).getByRole("button", { name: "Edit native profile" }).click()
  for (const family of ["IPv4", "IPv6"]) {
    await sheet(page)
      .getByRole("button", { name: `Add ${family} route`, exact: true })
      .click()
    await expect(sheet(page).getByLabel(`${family} route 1 table`, { exact: true })).toHaveValue(
      "1001",
    )
  }
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  )
})

test("read accounts open a device without requesting its private native profile", async ({
  page,
}) => {
  let reads = 0
  await mockNetwork(page, [], {
    session: { ...admin, capabilities: ["read"], user: { ...admin.user, role: "readonly" } },
  })
  await page.route(`**/api/v1${path}`, (route) => {
    reads++
    return json(route, profile())
  })
  await page.goto("/network/interfaces")
  await page.getByRole("button", { name: "Open ens3", exact: true }).click()
  await expect(sheet(page).getByText("Native persistent profile", { exact: true })).toHaveCount(0)
  await expect(sheet(page).getByRole("button", { name: "Edit native profile" })).toHaveCount(0)
  expect(reads).toBe(0)
})

test("confirmed native cleanup remains a warning until an explicit retry succeeds", async ({
  page,
}) => {
  const change = {
    id: "native-cleanup-fixture",
    phase: "confirmed",
    cleanup: "failed",
    generation: "a".repeat(64),
    updatedAt: "2026-10-08T12:00:00Z",
    watchdog: "completed",
    runtime: "applied",
    persistence: "written",
    boot: "enabled",
  }
  await mockNetwork(page, [], {
    overrides: { "/network/changes/current": { available: true, owned: true, change } },
  })
  let retries = 0
  await page.route("**/api/v1/network/changes/native-cleanup-fixture/cleanup", async (route) => {
    retries++
    expect(route.request().method()).toBe("POST")
    expect(route.request().headers()["x-jd-network-apply"]).toBeUndefined()
    if (retries === 1)
      return json(
        route,
        { error: { code: "cleanup_failed", message: "The owned checkpoint is still retained." } },
        500,
      )
    change.cleanup = "complete"
    return json(route, change)
  })
  await page.goto("/account/keys")
  await expect(banner(page)).toContainText("Network change confirmed")
  await expect(banner(page)).toContainText("native cleanup remains incomplete")
  await expect(banner(page).getByRole("button", { name: "Dismiss network outcome" })).toHaveCount(0)
  expect(retries).toBe(0)
  await banner(page).getByRole("button", { name: "Retry native cleanup" }).click()
  await expect(banner(page).getByRole("alert")).toContainText(
    "The owned checkpoint is still retained.",
  )
  await expect(banner(page)).toContainText("native cleanup remains incomplete")
  expect(retries).toBe(1)
  await banner(page).getByRole("button", { name: "Retry native cleanup" }).click()
  await expect(banner(page)).toHaveCount(0)
  expect(retries).toBe(2)
})

test("a malformed native route is a retained read failure and never replaces the typed draft", async ({
  page,
}) => {
  await page.clock.install()
  const mutations = await setup(page)
  await edit(page)
  const current = profile()
  await page.route(`**/api/v1${path}`, (route) =>
    json(route, {
      ...current,
      intent: { ...current.intent, ipv4: { ...current.intent.ipv4, routes: [null] } },
    }),
  )
  await page.clock.fastForward(31_000)
  await expect(sheet(page).getByRole("alert")).toContainText(
    "Native family intent is incomplete or invalid.",
  )
  await expect(sheet(page).getByRole("alert")).toContainText("Last successful read:")
  await assertDraft(page)
  await expect(
    sheet(page).getByRole("button", { name: "Apply temporary native settings" }),
  ).toBeDisabled()
  expect(mutations).toEqual([])
})

for (const change of ["generation", "owner", "read failure"] as const) {
  test(`an already-open native confirmation refuses a changed ${change} without sending a write`, async ({
    page,
  }) => {
    await page.clock.install()
    let view = profile()
    let failed = false
    let writes = 0
    await mockNetwork(page)
    await page.route(`**/api/v1${path}`, (route) => {
      if (route.request().method() !== "GET") {
        writes++
        return json(route, view)
      }
      return failed
        ? json(
            route,
            { error: { code: "read_failed", message: "Native ownership read failed." } },
            500,
          )
        : json(route, view)
    })
    await page.goto("/network/interfaces")
    await page.getByRole("button", { name: "Open ens3", exact: true }).click()
    await edit(page)
    await sheet(page).getByRole("button", { name: "Apply temporary native settings" }).click()
    const confirmation = page.getByRole("dialog", { name: "Apply native settings to ens3" })
    await expect(confirmation).toBeVisible()
    if (change === "read failure") failed = true
    else
      view = {
        ...view,
        generation: "b".repeat(64),
        ...(change === "owner"
          ? { owner: "networkd", renderer: "networkd", profile: "replacement-profile" }
          : {}),
      }
    await page.clock.fastForward(31_000)
    if (change === "read failure") {
      await expect(sheet(page).locator("[role=alert]")).toContainText(
        "Native ownership read failed.",
      )
    } else {
      await expect(sheet(page).getByText(/The native baseline changed/)).toHaveCount(1)
    }
    await confirmation
      .getByRole("button", { name: "Apply temporary settings", exact: true })
      .click()
    await expect(confirmation.getByRole("button", { name: "Cancel", exact: true })).toBeEnabled()
    await confirmation.getByRole("button", { name: "Cancel", exact: true }).click()
    await expect(
      sheet(page).getByText(
        "Native ownership or the latest read changed. Review the retained draft before applying.",
        { exact: true },
      ),
    ).toBeVisible()
    await assertDraft(page)
    expect(writes).toBe(0)
    if (change === "owner") {
      await expect(
        sheet(page).getByRole("button", { name: "Use latest native baseline" }),
      ).toBeDisabled()
    }
  })
}
