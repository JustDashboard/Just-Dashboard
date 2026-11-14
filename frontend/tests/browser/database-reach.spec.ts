import { expect as baseExpect, test, type Page, type Route } from "@playwright/test"
import { hold, mockDatabases, type DatabaseMock } from "./database-fixture"
import { risen } from "./risen"
import { tallShot } from "./database-settings-shots"

/**
 * Where a database can be reached from, on its Network settings page: the
 * four answers, and the two that take a value — one of this machine's own
 * addresses, or a list of the addresses that may connect.
 *
 * The claims are the ones the page makes to an operator who is not a network
 * engineer. The answer is a field of the page, saved by its bar. A list is
 * built and taken apart as a draft, says why an entry it will not take is
 * wrong, offers the address the reader is on, and names the networks that get
 * in whatever it says. Nothing runs until the page is saved, the server has
 * said what it would run (the gateway's own rules among it) and the reader
 * has confirmed that; a confirmation that is cancelled sends nothing, and a
 * change that is refused leaves the answer as it was chosen. The picture says
 * who may reach the port and who is refused, and carries the warning for a
 * port only a password guards. An answer that this machine cannot give is
 * drawn with the reason, not offered. And none of it is drawn for a role that
 * may not read it.
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
  reach(page).getByRole("radiogroup", { name: "Where it is reachable from" })
const choice = (page: Page, name: string) => choices(page).getByRole("radio", { name, exact: true })
const picture = (page: Page) => reach(page).getByRole("list", { name: "Who can reach it" })
const editor = (page: Page) => reach(page).locator("[data-slot=reach-allowlist]")
const picker = (page: Page) => reach(page).locator("[data-slot=reach-address]")
const dialog = (page: Page) => page.getByRole("dialog")
const saveBar = (page: Page) => page.getByRole("region", { name: "Save changes" })
const save = (page: Page) => saveBar(page).getByRole("button", { name: "Save", exact: true })

test("a list is built as a draft, reviewed with the gateway's own rules, and then kept", async ({
  page,
}) => {
  const server = await mockReach(page)
  await visit(page, NETWORK)
  await expect(choice(page, "Anywhere")).toBeChecked()
  await expect(editor(page)).toHaveCount(0)

  await choice(page, "Allowed").click()
  await expect(editor(page)).toContainText("No address is on the list yet")
  // An answer is an edit of the page; a list with nobody on it cannot be saved.
  await expect(saveBar(page)).toContainText("1 unsaved change")
  await expect(save(page)).toBeDisabled()
  const add = editor(page).getByLabel("Add an address or a network")

  // What it will not take is said before anything is sent.
  await add.fill("the office")
  await expect(editor(page)).toContainText("Not an address or a network")
  await expect(editor(page).getByRole("button", { name: "Add", exact: true })).toBeDisabled()
  await add.fill("0.0.0.0/0")
  await expect(editor(page)).toContainText("choose Anywhere")

  await add.fill("203.0.113.50")
  await editor(page).getByRole("button", { name: "Add", exact: true }).click()
  await add.fill("198.51.100.0/24")
  // Enter adds the entry: it does not save the page.
  await add.press("Enter")
  await expect(dialog(page)).toHaveCount(0)
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

  await save(page).click()
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

  // The page reads it back: the list in force, and on the picture who is
  // refused, how often, and what the firewall now lets in.
  await expect(choice(page, "Allowed")).toBeChecked()
  await expect(picture(page)).toContainText("Allowed addresses")
  await expect(picture(page)).toContainText("refused by the gateway")
  await expect(picture(page)).toContainText("1,284 attempts so far")
  await expect(picture(page)).toContainText(
    "ufw lets port 5432 in from 198.51.100.0/24 and 203.0.113.50",
  )
  await expect(save(page)).toHaveCount(0)
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
  // This machine's own addresses are beside "This server" until it is published to anywhere.
  await expect(picture(page)).toContainText("This server · 203.0.113.7")
  await choice(page, "One address").click()
  const addresses = picker(page).getByRole("group", { name: "This machine's addresses" })
  await expect(addresses.getByRole("button")).toHaveCount(3)
  await expect(addresses.getByRole("button", { name: /100\.101\.12\.4/ })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await expect(picker(page)).toContainText("Every device on your tailnet reaches it.")
  await addresses.getByRole("button", { name: /10\.20\.0\.8/ }).click()
  await expect(picker(page)).toContainText("if wg0 is down when the container starts")
  expect(server.sent).toEqual([])

  await save(page).click()
  await expect(dialog(page)).toContainText("Publish shop on one address")
  await expect(dialog(page)).toContainText("published on 10.20.0.8 (wg0)")
  await expect(dialog(page)).toContainText("Every open session is dropped")
  await dialog(page).getByRole("button", { name: "Publish on this address" }).click()
  await expect
    .poll(() => server.sent.find((one) => one.request === "PUT /access")?.body)
    .toEqual({ exposure: "private", address: "10.20.0.8" })
  await expect(picture(page)).toContainText("10.20.0.8")
  await expect(picture(page)).toContainText("published on wg0")
  await expect(choice(page, "One address")).toBeChecked()
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
  await expect(choice(page, "Allowed")).toBeDisabled()
  await expect(reach(page)).toContainText("nftables is not installed on this host")
  await expect(choice(page, "One address")).toBeDisabled()
  await expect(reach(page)).toContainText("no tailnet, VPN or local-network address")
})

test("restricting a published server is asked too, and sends only the answer", async ({ page }) => {
  const server = await mockReach(page, {
    change: {
      access: { ...ACCESS, exposure: "local", firewall: { ...ACCESS.firewall, open: false } },
      firewall: "closed",
      gateway: "unchanged",
    },
  })
  await visit(page, NETWORK)
  // The picture carries the warning: only the password guards a port published to anywhere.
  await expect(reach(page).locator("[data-slot=reach-warning]")).toHaveText(
    "only the password is in the way",
  )
  await expect(picture(page)).toContainText("ufw lets port 5432 in from anywhere")
  await choice(page, "This server").click()
  await save(page).click()
  await expect(dialog(page)).toContainText("Restrict shop")
  await expect(dialog(page)).toContainText("bound to this machine only")
  await expect(dialog(page)).toContainText("Every open session is dropped")
  await dialog(page).getByRole("button", { name: "Restrict", exact: true }).click()
  await expect
    .poll(() => server.sent.map((one) => [one.request, one.body]))
    .toEqual([["PUT /access", { exposure: "local" }]])
  await expect(choice(page, "This server")).toBeChecked()
  await expect(reach(page).locator("[data-slot=reach-warning]")).toHaveText("cannot reach it")
  await expect(picture(page)).toContainText("ufw keeps port 5432 closed")
})

test("a confirmation that is cancelled sends nothing, and a refused change keeps the answer chosen", async ({
  page,
}) => {
  const server = await mockReach(page, {
    access: { ...ACCESS, exposure: "local", firewall: { ...ACCESS.firewall, open: false } },
    change: refusal("docker_failed", "the container could not be recreated", 502),
  })
  await visit(page, NETWORK)
  await choice(page, "Anywhere").click()
  await save(page).click()
  await expect(dialog(page)).toContainText("Publish shop to anywhere")
  await dialog(page).getByRole("button", { name: "Cancel" }).click()
  await expect(dialog(page)).toHaveCount(0)
  // Nothing was sent, and the page still holds the answer, unsaved.
  expect(server.sent).toEqual([])
  await expect(choice(page, "Anywhere")).toBeChecked()
  await expect(saveBar(page)).toContainText("1 unsaved change")

  await save(page).click()
  await dialog(page).getByRole("button", { name: "Publish", exact: true }).click()
  // The refusal is under the form, in the server's words, with the bar still up beside it.
  await expect(
    page.getByRole("form", { name: "Where it is reachable from" }).getByRole("alert"),
  ).toHaveText("It was not changed. the container could not be recreated")
  await expect(reach(page)).toContainText("Not saved")
  expect(server.sent.map((one) => one.request)).toEqual(["PUT /access"])
  // What the server has is unchanged, and what the reader chose is still there to save again.
  await expect(reach(page)).toContainText("This server only")
  await expect(choice(page, "Anywhere")).toBeChecked()
  await expect(saveBar(page)).toContainText("1 unsaved change")
  // Discard puts the answer back to the one in force.
  await saveBar(page).getByRole("button", { name: "Discard" }).click()
  await expect(choice(page, "This server")).toBeChecked()
})

test("a list not in force says so on the picture, and a refused review sends nothing further", async ({
  page,
}) => {
  const server = await mockReach(page, {
    access: { ...LISTED, gateway: { ...LISTED.gateway, enforced: false } },
    preview: refusal("gateway_unavailable", "this host cannot keep a port to a list", 409),
  })
  await visit(page, NETWORK)
  await expect(reach(page).locator("[data-slot=reach-warning]")).toContainText(
    "not refused: the gateway’s table is not loaded",
  )
  // The list in force is the field's starting draft.
  const list = editor(page).getByRole("list", { name: "Allowed addresses" })
  await expect(list.getByRole("listitem")).toHaveCount(2)
  await editor(page).getByLabel("Add an address or a network").fill("192.0.2.80")
  await editor(page).getByLabel("Add an address or a network").press("Enter")
  await expect(list.getByRole("listitem")).toHaveCount(3)
  await save(page).click()
  await expect(
    page.getByRole("form", { name: "Where it is reachable from" }).getByRole("alert"),
  ).toContainText(
    "Could not work out what the change would do. this host cannot keep a port to a list",
  )
  await expect(dialog(page)).toHaveCount(0)
  expect(server.sent.map((one) => one.request)).toEqual(["POST /access/preview"])

  // Discard puts the draft back to what is kept.
  await saveBar(page).getByRole("button", { name: "Discard" }).click()
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

test("the picture has its shape before the binding is read, and the read landing moves nothing", async ({
  page,
}) => {
  const local = { ...ACCESS, exposure: "local", firewall: { ...ACCESS.firewall, open: false } }
  for (const width of [1280, 390]) {
    await page.setViewportSize({ width, height: 1100 })
    await page.unrouteAll({ behavior: "ignoreErrors" })
    const read = hold()
    await mockReach(page, { summaries: { 1: { container: { id: "c1", name: "shop-db" } } } })
    await page.route("**/api/v1/databases/1/access", async (route) => {
      if (route.request().method() !== "GET") return route.fallback()
      await read.until
      return route.fulfill({ json: local })
    })
    await visit(page, NETWORK)
    // The container the summary names is drawn while the binding is read, and
    // the answers' place is kept.
    const container = picture(page).getByRole("link", { name: "shop-db" })
    await expect(container).toBeVisible()
    await expect(reach(page).locator("[data-slot=reach-loading]")).toBeVisible()
    await risen(page)
    const below = page.locator("section#tls")
    const before = { container: await container.boundingBox(), below: await below.boundingBox() }
    read.release()
    await expect(choices(page)).toBeVisible()
    await risen(page)
    expect((await container.boundingBox())?.x, `the container at ${width}`).toBe(
      before.container?.x,
    )
    expect((await below.boundingBox())?.y, `the section under it at ${width}`).toBe(before.below?.y)
  }
})

test("at a phone's width the list and its picture fit without scrolling sideways", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockReach(page, { access: LISTED })
  await visit(page, NETWORK)
  await expect(editor(page).getByRole("list", { name: "Allowed addresses" })).toBeVisible()
  await expect(reach(page)).toContainText("refused by the gateway")
  // Four answers fit the width: none is cut off.
  for (const name of ["This server", "One address", "Allowed", "Anywhere"]) {
    const box = await choice(page, name).boundingBox()
    expect(box && box.x >= 0 && box.x + box.width <= 390, `${name} is inside the screen`).toBe(true)
  }
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= document.documentElement.clientWidth,
    ),
  ).toBe(true)
})

/**
 * Clears what a shot should not be of: the toast a change left up, which lies
 * over the foot of the section, and the focus ring a click leaves on a
 * control, which reads as a second selection.
 */
async function settle(page: Page) {
  await page.addStyleTag({ content: "[data-sonner-toaster]{display:none!important}" })
  await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur())
}

test("the reach states, for the record @shots", async ({ page }) => {
  test.skip(!process.env.JD_SHOTS, "set JD_SHOTS to a directory to take the screenshots")
  test.setTimeout(240_000)
  const shots = process.env.JD_SHOTS
  const shot = async (name: string, width: number) => {
    await risen(page)
    await tallShot(page, `${shots}/after-network-${name}-${width}.png`)
  }

  for (const width of [1280, 390]) {
    await page.setViewportSize({ width, height: width === 390 ? 844 : 900 })
    await page.unrouteAll({ behavior: "ignoreErrors" })
    await mockReach(page)
    await visit(page, NETWORK)
    await expect(reach(page).locator("[data-slot=reach-warning]")).toBeVisible()
    await shot("anywhere", width)

    await choice(page, "Allowed").click()
    const add = editor(page).getByLabel("Add an address or a network")
    for (const entry of ["198.51.100.0/24", "203.0.113.50"]) {
      await add.fill(entry)
      await add.press("Enter")
    }
    await add.fill("office.example")
    await expect(editor(page)).toContainText("Not an address or a network")
    await shot("list-draft", width)
    await add.fill("")

    await save(page).click()
    await expect(dialog(page)).toContainText("The gateway's rules")
    await page.screenshot({ path: `${shots}/after-network-list-review-${width}.png` })
    await dialog(page).getByRole("button", { name: "Allow these addresses" }).click()
    await expect(picture(page)).toContainText("1,284 attempts so far")
    await settle(page)
    await shot("listed", width)

    await choice(page, "One address").click()
    await expect(picker(page)).toBeVisible()
    await settle(page)
    await shot("one-address", width)
  }
})
