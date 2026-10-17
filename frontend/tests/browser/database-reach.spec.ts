import { expect as baseExpect, test, type Page, type Route } from "@playwright/test"
import { mockDatabases, type DatabaseMock } from "./database-fixture"

/**
 * Where a database can be reached from, on its Network settings page: the
 * four answers, and the two that take a value — one of this machine's own
 * addresses, or a list of the addresses that may connect.
 *
 * The claims are the ones the page makes to an operator who is not a network
 * engineer. A list is built and taken apart as a draft, says why an entry it
 * will not take is wrong, offers the address the reader is on, and names the
 * networks that get in whatever it says. Nothing runs until the server has
 * said what it would run — the gateway's own rules among it — and the reader
 * has confirmed that. The picture says who may reach the port and who is
 * refused. An answer that this machine cannot give is drawn with the reason,
 * not offered. And none of it is drawn for a role that may not read it.
 */

const expect = baseExpect.configure({ timeout: 15_000 })
test.describe.configure({ timeout: 60_000 })

type Reply = { status: number; body: unknown }
const isReply = (value: unknown): value is Reply =>
  typeof value === "object" && value !== null && "status" in value && "body" in value
const refusal = (code: string, message: string, status = 400): Reply => ({
  status,
  body: { error: { code, message } },
})

const ADDRESSES = [
  { address: "100.101.12.4", interface: "tailscale0", network: "tailnet" },
  { address: "10.20.0.8", interface: "wg0", network: "vpn" },
  { address: "192.168.10.24", interface: "eth1", network: "private" },
]

/** A Postgres container of the dashboard's own, published to anywhere with ufw open for it. */
const ACCESS = {
  detected: true,
  container: "shop-db",
  managed: true,
  exposure: "public",
  port: 5432,
  publicAddresses: ["203.0.113.7"],
  firewall: { backend: "ufw", active: true, open: true, allowed: [], editable: true },
  allow: [],
  gateway: { available: true, alsoAdmitted: ["100.64.0.0/10"], enforced: false, refused: 0 },
  addresses: ADDRESSES,
  client: "192.0.2.44",
}

const LIST = ["198.51.100.0/24", "203.0.113.50"]

/** The same server once its port is kept to the office and the application server. */
const LISTED = {
  ...ACCESS,
  exposure: "allowlist",
  allow: LIST,
  firewall: { ...ACCESS.firewall, open: false, allowed: LIST },
  gateway: { ...ACCESS.gateway, enforced: true, refused: 1284 },
}

const RULES = [
  'meta l4proto tcp th dport 5432 fib daddr type local ip saddr != { 198.51.100.0/24, 203.0.113.50 } counter drop comment "port-allowlist:12"',
  'meta l4proto tcp th dport 5432 fib daddr type local meta nfproto ipv6 counter drop comment "port-allowlist:12"',
]

const LIST_PREVIEW = {
  from: { exposure: "public", hostIp: "0.0.0.0" },
  to: { exposure: "allowlist", hostIp: "0.0.0.0", allow: LIST },
  recreate: false,
  steps: [
    {
      kind: "gateway",
      description:
        "The gateway table refuses new connections to port 5432 from every address not on the list.",
      lines: RULES,
    },
    { kind: "firewall", description: "ufw: Add: allow in 5432/tcp from 198.51.100.0/24" },
    { kind: "firewall", description: "ufw: Add: allow in 5432/tcp from 203.0.113.50" },
    {
      kind: "firewall",
      description: "ufw: Remove rule 4: 5432/tcp ALLOW IN Anywhere # just-dashboard database 5432",
    },
  ],
  firewall: "updated",
}

const ADDRESS_PREVIEW = {
  from: { exposure: "local", hostIp: "127.0.0.1" },
  to: { exposure: "private", hostIp: "10.20.0.8" },
  recreate: true,
  steps: [
    {
      kind: "recreate",
      description: "shop-db is recreated with port 5432 published on 10.20.0.8 (wg0).",
    },
    { kind: "firewall", description: "ufw already holds the rules this needs for port 5432." },
  ],
  firewall: "already",
}

type Sent = { request: string; body: unknown }

/**
 * The section's fixture with the reach routes of connection 1 answered over
 * it. A PUT that succeeds becomes what the next read answers, as the server's
 * would.
 */
async function mockReach(
  page: Page,
  options: DatabaseMock & {
    access?: unknown
    preview?: unknown
    change?: unknown
    capabilities?: string[]
  } = {},
) {
  const mock = await mockDatabases(page, options)
  const sent: Sent[] = []
  const asked: string[] = []
  let access: unknown = options.access ?? ACCESS
  const reply = (route: Route, value: unknown) =>
    isReply(value)
      ? route.fulfill({ status: value.status, json: value.body })
      : route.fulfill({ json: value })
  if (options.capabilities) {
    const capabilities = options.capabilities
    await page.route("**/api/v1/auth/session", (route) =>
      route.fulfill({
        json: {
          authenticated: true,
          needsTotp: false,
          needsEnrollment: false,
          require2fa: false,
          capabilities,
          user: {
            id: 2,
            username: "operator",
            displayName: "Operator",
            avatarVersion: 0,
            role: "limited",
            totpEnabled: true,
            disabled: false,
            mustChangePassword: false,
            lastLoginAt: "2026-10-01T09:00:00Z",
            createdAt: "2026-10-01T09:00:00Z",
          },
        },
      }),
    )
  }
  await page.route("**/api/v1/databases/1/access**", async (route) => {
    const url = new URL(route.request().url())
    const method = route.request().method()
    const path = url.pathname.replace(/^\/api\/v1\/databases\/1/, "")
    asked.push(`${method} ${path}`)
    if (method === "GET" && path === "/access") return reply(route, access)
    const body = route.request().postDataJSON()
    sent.push({ request: `${method} ${path}`, body })
    if (method === "POST" && path === "/access/preview") {
      return reply(route, options.preview ?? LIST_PREVIEW)
    }
    if (method === "PUT" && path === "/access") {
      const answer = options.change ?? { access: LISTED, firewall: "updated", gateway: "kept" }
      if (!isReply(answer)) access = (answer as { access: unknown }).access
      return reply(route, answer)
    }
    return route.fallback()
  })
  return { ...mock, sent, asked }
}

/** Opens a page of the section and waits for the list of connections to have answered. */
async function visit(page: Page, path: string) {
  const listed = page.waitForResponse(
    (response) => new URL(response.url()).pathname === "/api/v1/databases/",
    { timeout: 45_000 },
  )
  await page.goto(path)
  await listed
}

const NETWORK = "/databases/1/settings/network"
const reach = (page: Page) => page.locator("section#reachability")
const choices = (page: Page) =>
  reach(page).getByRole("group", { name: "Where it is reachable from" })
const editor = (page: Page) => reach(page).locator("[data-slot=reach-allowlist]")
const picker = (page: Page) => reach(page).locator("[data-slot=reach-address]")
const dialog = (page: Page) => page.getByRole("dialog")

test("a list is built as a draft, reviewed with the gateway's own rules, and then kept", async ({
  page,
}) => {
  const server = await mockReach(page)
  await visit(page, NETWORK)
  await expect(choices(page).getByRole("button", { name: /Anywhere/ })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await expect(editor(page)).toHaveCount(0)

  await choices(page)
    .getByRole("button", { name: /Allowed addresses/ })
    .click()
  await expect(editor(page)).toContainText("No address is on the list yet")
  const add = editor(page).getByLabel("Add an address or a network")
  await expect(add).toBeFocused()

  // What it will not take is said before anything is sent.
  await add.fill("the office")
  await expect(editor(page)).toContainText("Not an address or a network")
  await expect(editor(page).getByRole("button", { name: "Add", exact: true })).toBeDisabled()
  await add.fill("0.0.0.0/0")
  await expect(editor(page)).toContainText("choose Anywhere")

  await add.fill("203.0.113.50")
  await editor(page).getByRole("button", { name: "Add", exact: true }).click()
  await add.fill("198.51.100.0/24")
  await add.press("Enter")
  await add.fill("203.0.113.50")
  await expect(editor(page)).toContainText("Already on the list.")
  await add.fill("")

  // The address the reader is on is offered by name, and taken off again.
  await expect(editor(page)).toContainText("You are connected from 192.0.2.44")
  await editor(page).getByRole("button", { name: "Add it" }).click()
  const list = editor(page).getByRole("list", { name: "Allowed addresses" })
  await expect(list.getByRole("listitem")).toHaveCount(3)
  await expect(list).toContainText("the address you are on")
  await list.getByRole("button", { name: "Remove 192.0.2.44" }).click()
  await expect(list.getByRole("listitem")).toHaveCount(2)

  // Who gets in whatever the list says.
  await expect(editor(page)).toContainText("100.64.0.0/10")
  expect(server.sent).toEqual([])

  await editor(page).getByRole("button", { name: "Allow only these addresses" }).click()
  await expect(dialog(page)).toContainText("Allow only listed addresses to reach shop")
  await expect(dialog(page)).toContainText("refuses new connections to port 5432")
  await expect(dialog(page)).toContainText("ufw: Add: allow in 5432/tcp from 198.51.100.0/24")
  await expect(dialog(page)).toContainText('comment "port-allowlist:12"')
  await expect(dialog(page)).toContainText("Nothing restarts")
  expect(server.sent).toEqual([
    {
      request: "POST /access/preview",
      body: { exposure: "allowlist", allow: ["203.0.113.50", "198.51.100.0/24"] },
    },
  ])

  await dialog(page).getByRole("button", { name: "Allow these addresses" }).click()
  await expect
    .poll(() => server.sent.find((one) => one.request === "PUT /access")?.body)
    .toEqual({ exposure: "allowlist", allow: ["203.0.113.50", "198.51.100.0/24"] })

  // The page reads it back: the list in force, who is refused, and what the
  // firewall now says.
  await expect(choices(page).getByRole("button", { name: /Allowed addresses/ })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  const picture = reach(page).getByRole("list", { name: "Who can reach it" })
  await expect(picture).toContainText("Allowed addresses")
  await expect(picture).toContainText("refused by the gateway")
  await expect(reach(page)).toContainText("1,284 attempts so far")
  await expect(reach(page)).toContainText(
    "ufw is on, and lets port 5432 in from 198.51.100.0/24 and 203.0.113.50.",
  )
  await expect(editor(page).getByRole("button", { name: "Save the list" })).toBeDisabled()
})

test("one address is picked from the ones this machine has, and confirmed with the recreate", async ({
  page,
}) => {
  const server = await mockReach(page, {
    access: { ...ACCESS, exposure: "local", firewall: { ...ACCESS.firewall, open: false } },
    preview: ADDRESS_PREVIEW,
    change: {
      access: { ...ACCESS, exposure: "private", address: "10.20.0.8" },
      firewall: "closed",
      gateway: "unchanged",
    },
  })
  await visit(page, NETWORK)
  await choices(page)
    .getByRole("button", { name: /One address of this machine/ })
    .click()
  const addresses = picker(page).getByRole("group", { name: "This machine's addresses" })
  await expect(addresses.getByRole("button")).toHaveCount(3)
  await expect(addresses.getByRole("button", { name: /100\.101\.12\.4/ })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await expect(addresses).toContainText("Every device on your tailnet reaches it.")
  await addresses.getByRole("button", { name: /10\.20\.0\.8/ }).click()
  await expect(picker(page)).toContainText("if wg0 is down when the container starts")

  await picker(page).getByRole("button", { name: "Publish on 10.20.0.8" }).click()
  await expect(dialog(page)).toContainText("Publish shop on one address")
  await expect(dialog(page)).toContainText("published on 10.20.0.8 (wg0)")
  await expect(dialog(page)).toContainText("Every open session is dropped")
  await dialog(page).getByRole("button", { name: "Publish on this address" }).click()
  await expect
    .poll(() => server.sent.find((one) => one.request === "PUT /access")?.body)
    .toEqual({ exposure: "private", address: "10.20.0.8" })
  await expect(reach(page)).toContainText("Published on")
  await expect(reach(page)).toContainText("10.20.0.8")
})

test("an answer this machine cannot give is drawn with its reason, and nothing is offered empty", async ({
  page,
}) => {
  await mockReach(page, {
    access: {
      ...ACCESS,
      addresses: [],
      gateway: {
        available: false,
        reason:
          "nftables is not installed on this host, so nothing here can refuse the addresses a list leaves out.",
        alsoAdmitted: [],
        enforced: false,
        refused: 0,
      },
    },
  })
  await visit(page, NETWORK)
  await expect(choices(page).getByRole("button", { name: /Allowed addresses/ })).toBeDisabled()
  await expect(choices(page)).toContainText("nftables is not installed on this host")
  await expect(
    choices(page).getByRole("button", { name: /One address of this machine/ }),
  ).toBeDisabled()
  await expect(choices(page)).toContainText("no tailnet, VPN or local-network address")
})

test("a list not in force says so, and a refused review sends nothing further", async ({
  page,
}) => {
  const server = await mockReach(page, {
    access: { ...LISTED, gateway: { ...LISTED.gateway, enforced: false } },
    preview: refusal("gateway_unavailable", "this host cannot keep a port to a list", 409),
  })
  await visit(page, NETWORK)
  await expect(reach(page)).toContainText("The list is not in force")
  // The list in force is the field's starting draft.
  const list = editor(page).getByRole("list", { name: "Allowed addresses" })
  await expect(list.getByRole("listitem")).toHaveCount(2)
  await editor(page).getByLabel("Add an address or a network").fill("192.0.2.80")
  await editor(page).getByLabel("Add an address or a network").press("Enter")
  await editor(page).getByRole("button", { name: "Save the list" }).click()
  await expect(page.getByText("Could not work out what the change would do")).toBeVisible()
  await expect(dialog(page)).toHaveCount(0)
  expect(server.sent.map((one) => one.request)).toEqual(["POST /access/preview"])

  // Discard puts the draft back to what is kept.
  await editor(page).getByRole("button", { name: "Discard" }).click()
  await expect(list.getByRole("listitem")).toHaveCount(2)
})

test("a reach that cannot be read says so, with a way to try again", async ({ page }) => {
  await mockReach(page, { access: refusal("internal", "the gateway table could not be read", 500) })
  await visit(page, NETWORK)
  await expect(reach(page)).toContainText("Could not read where it is reachable from")
  await expect(reach(page).getByRole("button", { name: "Try again" })).toBeVisible()
  await expect(choices(page)).toHaveCount(0)
})

test("a role that may only read is told whose this is, and nothing is asked for it", async ({
  page,
}) => {
  const server = await mockReach(page, { viewer: true })
  await visit(page, NETWORK)
  await expect(reach(page)).toContainText("an administrator")
  await expect(choices(page)).toHaveCount(0)
  await page.waitForLoadState("networkidle")
  expect(server.asked).toEqual([])
})

test("an administrator who may not recreate a container reads the list and changes nothing", async ({
  page,
}) => {
  await mockReach(page, {
    access: LISTED,
    capabilities: ["read", "service.control", "system.admin"],
  })
  await visit(page, NETWORK)
  await expect(reach(page)).toContainText("Allowed addresses")
  await expect(reach(page)).toContainText("which your role may not do")
  await expect(choices(page)).toHaveCount(0)
  await expect(editor(page)).toHaveCount(0)
})

test("at a phone's width the list and its picture fit without scrolling sideways", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockReach(page, { access: LISTED })
  await visit(page, NETWORK)
  await expect(editor(page).getByRole("list", { name: "Allowed addresses" })).toBeVisible()
  await expect(reach(page)).toContainText("refused by the gateway")
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= document.documentElement.clientWidth,
    ),
  ).toBe(true)
})

test("the reach states, for the record @shots", async ({ page }) => {
  test.skip(!process.env.JD_SHOTS, "set JD_SHOTS to a directory to take the screenshots")
  const shots = process.env.JD_SHOTS
  await page.setViewportSize({ width: 1440, height: 900 })

  await mockReach(page)
  await visit(page, NETWORK)
  await expect(reach(page)).toContainText("Anything that can reach this machine can try it")
  await page.screenshot({ path: `${shots}/reach-anywhere-1440.png`, fullPage: true })

  await choices(page)
    .getByRole("button", { name: /Allowed addresses/ })
    .click()
  const add = editor(page).getByLabel("Add an address or a network")
  for (const entry of ["198.51.100.0/24", "203.0.113.50"]) {
    await add.fill(entry)
    await add.press("Enter")
  }
  await add.fill("office.example")
  await expect(editor(page)).toContainText("Not an address or a network")
  await page.screenshot({ path: `${shots}/reach-list-draft-1440.png`, fullPage: true })
  await add.fill("")

  await editor(page).getByRole("button", { name: "Allow only these addresses" }).click()
  await expect(dialog(page)).toContainText("The gateway's rules")
  await page.screenshot({ path: `${shots}/reach-list-review-1440.png` })
  await dialog(page).getByRole("button", { name: "Allow these addresses" }).click()
  await expect(reach(page)).toContainText("1,284 attempts so far")
  await page.screenshot({ path: `${shots}/reach-listed-1440.png`, fullPage: true })

  await choices(page)
    .getByRole("button", { name: /One address of this machine/ })
    .click()
  await expect(picker(page)).toBeVisible()
  await page.screenshot({ path: `${shots}/reach-one-address-1440.png`, fullPage: true })

  await page.setViewportSize({ width: 390, height: 844 })
  await page.reload()
  await expect(reach(page)).toContainText("refused by the gateway")
  await page.screenshot({ path: `${shots}/reach-listed-390.png`, fullPage: true })
})
