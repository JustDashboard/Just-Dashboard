import { expect, test, type Page } from "@playwright/test"
import { risen } from "./risen"
import {
  PENDING,
  failure,
  iso,
  logins,
  manySessions,
  mockLogins,
  narrowed,
  viewer,
  type Mutation,
} from "./security-logins-fixture"

/**
 * The Logins page and the Security overview against a mocked API: each opens
 * on its identity line and a picture with no row of tiles, holds its shape
 * while its reads are in flight, keeps every row one height, and still does
 * everything it did — end a session, block an address, look one up, switch to
 * the failed record, filter it, hold new records back — with nothing offered
 * to an account that may not use it.
 */

const LOGINS = "/security/logins"
const WIDTHS = [
  { width: 1280, height: 900 },
  { width: 390, height: 844 },
] as const

const unreadable = failure(503, "unavailable", "the host could not be read")
const noRecord = failure(
  503,
  "login_history_unavailable",
  "the host's login record could not be read on this system",
)

async function loaded(page: Page) {
  await page.waitForSelector("[data-slot=page]")
  await page.waitForFunction(() => !document.querySelector("[data-slot=page] [data-slot=skeleton]"))
  await risen(page)
}

/** Where each named block stands, to the pixel. */
async function places(page: Page, selectors: string[]) {
  await risen(page)
  return page.evaluate(
    (selectors) =>
      selectors.map((selector) => {
        const box = document.querySelector(selector)?.getBoundingClientRect()
        return box ? { top: Math.round(box.top), height: Math.round(box.height) } : null
      }),
    selectors,
  )
}

/** Where each named block starts. */
async function tops(page: Page, selectors: string[]) {
  return (await places(page, selectors)).map((box) => box?.top)
}

/** The heights of a run of rows and the steps between them. */
async function rhythm(page: Page, selector: string) {
  return page.evaluate((selector) => {
    const boxes = Array.from(document.querySelectorAll(selector))
      .map((el) => el.getBoundingClientRect())
      .filter((box) => box.height > 0)
    return {
      heights: [...new Set(boxes.map((box) => Math.round(box.height)))],
      steps: [...new Set(boxes.slice(1).map((box, i) => Math.round(box.top - boxes[i].top)))],
      count: boxes.length,
    }
  }, selector)
}

const overflow = (page: Page) =>
  page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)

test.describe("logins", () => {
  test("opens on the server and a picture of who is in and who is trying", async ({ page }) => {
    await mockLogins(page)
    await page.goto(LOGINS)
    await loaded(page)

    await expect(page.locator("[data-slot=stat-grid]")).toHaveCount(0)
    const line = page.getByRole("region", { name: "Logins on this server" })
    await expect(line).toHaveAttribute("data-layout", "project")
    await expect(line).toContainText("atlas")
    await expect(line).toContainText("1 signed in")
    await expect(line).toContainText("3 addresses trying")
    await expect(line).toContainText("last login")
    await expect(line).toContainText("ubuntu")
    await expect(line).toContainText("1 over ssh")
    await expect(line).toContainText("2,330 failed attempts in 7 days")

    // The picture stands directly under the line, before any list.
    const picture = page.locator("[data-slot=logins-picture]")
    const [lineBox, pictureBox, sessionsBox] = await places(page, [
      "[data-slot=host-identity]",
      "[data-slot=logins-picture]",
      "#sessions",
    ])
    expect(pictureBox!.top).toBeGreaterThan(lineBox!.top)
    expect(sessionsBox!.top).toBeGreaterThan(pictureBox!.top)

    const origin = picture.locator('[data-node="origin:100.110.34.9"]')
    await expect(origin).toContainText("Tailscale")
    await expect(origin).toContainText("ubuntu")
    await expect(origin.locator('img[src="/logos/tailscale.svg"]')).toHaveCount(1)
    await expect(picture.locator('[data-node="attacker:198.51.100.4"]')).toContainText("Attacking")
    await expect(picture.locator('[data-node="attacker:198.51.100.4"]')).toContainText(
      "112 attempts",
    )
    // The one the firewall already turns away is drawn as that.
    await expect(picture.locator('[data-node="attacker:203.0.113.9"]')).toContainText("Blocked")
    await expect(picture.locator('[data-node="account:ubuntu"]')).toContainText("Signed in")
    await expect(picture.locator('[data-node="account:deploy"]')).toContainText("last in")
    await expect(picture).toContainText("1 session open")
    await expect(picture).toContainText("2,330 refused in 7 days")

    // A node is the way to the list that names it.
    await picture.getByRole("link", { name: /^198\.51\.100\.4/ }).click()
    await expect(page).toHaveURL(/#attackers$/)
  })

  for (const viewport of WIDTHS) {
    test(`holds its shape while its reads are in flight at ${viewport.width}`, async ({ page }) => {
      await page.setViewportSize(viewport)
      // The line and the picture are the same box waiting and read; every
      // list under them starts where it did.
      const opening = ["[data-slot=host-identity]", "[data-slot=logins-picture]"]
      const lists = ["#sessions", "#attackers", "#recent"]
      await mockLogins(page, [], {
        overrides: { "/ssh-sessions": PENDING, "/logins": PENDING, "/logins/attackers": PENDING },
      })
      await page.goto(LOGINS)
      await page.waitForSelector("[data-slot=logins-picture] [data-slot=wire-flow][data-loading]")
      const waiting = [await places(page, opening), await tops(page, lists)]

      await page.unrouteAll({ behavior: "ignoreErrors" })
      await mockLogins(page)
      await page.goto(LOGINS)
      await loaded(page)
      await expect(page.locator("#sessions [data-slot=choice-row]")).toHaveCount(1)
      expect([await places(page, opening), await tops(page, lists)]).toEqual(waiting)
      expect(await overflow(page)).toBeLessThanOrEqual(0)
    })

    test(`every row is one height with and without its optional parts at ${viewport.width}`, async ({
      page,
    }) => {
      await page.setViewportSize(viewport)
      // A session with a process and an idle time beside one with neither;
      // an attacker with names tried, one with none and one already blocked;
      // a login beside the machine's own boot line.
      await mockLogins(page, [], { overrides: { "/ssh-sessions": manySessions } })
      await page.goto(LOGINS)
      await loaded(page)

      const sessions = await rhythm(page, "#sessions [data-slot=choice-row]")
      expect(sessions.count).toBe(3)
      expect(sessions.heights).toHaveLength(1)
      expect(sessions.steps).toHaveLength(1)

      const attackers = await rhythm(page, "#attackers [data-slot=rank-row]")
      expect(attackers.count).toBe(3)
      expect(attackers.heights).toEqual([32])
      expect(attackers.steps).toEqual([32])

      const records = await rhythm(
        page,
        viewport.width < 640 ? "#recent ul > li" : "#recent tbody tr",
      )
      expect(records.count).toBe(3)
      expect(records.heights).toHaveLength(1)
      expect(records.steps).toHaveLength(1)
      expect(await overflow(page)).toBeLessThanOrEqual(0)

      // The picture is as tall with six things to draw as with four: the
      // rest are counted on its last row.
      if (viewport.width >= 1024) {
        const picture = page.locator("[data-slot=logins-picture]")
        await expect(picture).toContainText("3 more in the lists")
        const busy = await tops(page, ["[data-slot=logins-picture]", "#sessions"])
        await page.unrouteAll({ behavior: "ignoreErrors" })
        await mockLogins(page)
        await page.goto(LOGINS)
        await loaded(page)
        expect(await tops(page, ["[data-slot=logins-picture]", "#sessions"])).toEqual(busy)
      }
    })
  }

  test("a session is ended from its menu, after a confirmation", async ({ page }) => {
    const mutations: Mutation[] = []
    await mockLogins(page, mutations, { overrides: { "/ssh-sessions": manySessions } })
    await page.goto(LOGINS)
    await loaded(page)

    const row = page.locator("#sessions [data-slot=choice-row]").filter({ hasText: "ubuntu" })
    await expect(row).toContainText("100.110.34.9")
    await expect(row).toContainText("pts/0")
    await expect(row).toContainText("active now")
    // A session the host gave no process for has no verb to offer.
    await expect(
      page.getByRole("button", { name: "Actions for root on tty1", exact: true }),
    ).toHaveCount(0)

    await page.getByRole("button", { name: "Actions for ubuntu on pts/0", exact: true }).click()
    await page.getByRole("menuitem", { name: "End session", exact: true }).click()
    const dialog = page.getByRole("dialog")
    await expect(dialog).toContainText("End ubuntu's session")
    await expect(dialog).toContainText("pts/0")
    expect(mutations).toEqual([])
    await dialog.getByRole("button", { name: "End session", exact: true }).click()
    await expect
      .poll(() => mutations.map((m) => `${m.method} ${m.path}`))
      .toEqual(["POST /ssh-sessions/4242/disconnect"])
  })

  test("an attacker is blocked from its menu, and one already blocked is not offered it", async ({
    page,
  }) => {
    const mutations: Mutation[] = []
    await mockLogins(page, mutations)
    await page.goto(LOGINS)
    await loaded(page)

    const panel = page.locator("#attackers")
    await expect(panel).toContainText("2,330 attempts from 3 addresses")
    await expect(panel.locator("[data-slot=split-bar], [data-slot=meter]")).toHaveCount(0)
    const first = panel.locator("[data-slot=rank-row]").filter({ hasText: "203.0.113.9" })
    await expect(first).toContainText("tried root, admin, ubuntu")
    await expect(first.locator("[data-slot=rank-value]")).toHaveText("2,200")
    await expect(first.locator("[data-slot=rank-share]")).toHaveText("94%")
    await expect(first).toContainText("blocked")
    await page.getByRole("button", { name: "Actions for 203.0.113.9", exact: true }).click()
    await expect(page.getByRole("menuitem", { name: "Block at the firewall" })).toHaveCount(0)
    await expect(page.getByRole("menuitem", { name: "Reverse lookup" })).toBeVisible()
    await page.keyboard.press("Escape")

    const second = panel.locator("[data-slot=rank-row]").filter({ hasText: "198.51.100.4" })
    await expect(second).not.toContainText("blocked")
    await page.getByRole("button", { name: "Actions for 198.51.100.4", exact: true }).click()
    await page.getByRole("menuitem", { name: "Block at the firewall", exact: true }).click()
    await expect
      .poll(() => mutations.find((m) => m.path === "/firewall/rules")?.body)
      .toEqual({ action: "deny", direction: "in", from: "198.51.100.4", comment: "failed logins" })
    // It says so at once, and is not offered a second rule.
    await expect(second).toContainText("blocked")
    await page.getByRole("button", { name: "Actions for 198.51.100.4", exact: true }).click()
    await expect(page.getByRole("menuitem", { name: "Block at the firewall" })).toHaveCount(0)

    // The lookups go to the Tools page with the address filled in.
    await page.getByRole("menuitem", { name: "Who owns this address", exact: true }).click()
    await expect(page).toHaveURL(/\/network\/tools\?tool=asn&target=198\.51\.100\.4$/)
    expect(mutations.filter((m) => m.path === "/firewall/rules")).toHaveLength(1)
  })

  test("the record switches to failed attempts and filters, both in the address", async ({
    page,
  }) => {
    await mockLogins(page)
    await page.goto(LOGINS)
    await loaded(page)

    const panel = page.locator("#recent")
    await expect(panel.getByRole("heading", { name: "Recent logins" })).toBeVisible()
    await expect(panel).toContainText("3 records")
    const rows = panel.locator("tbody tr")
    await expect(rows).toHaveCount(3)
    await expect(rows.nth(0)).toContainText("ubuntu")
    await expect(rows.nth(0)).toContainText("pts/0")
    await expect(rows.nth(0)).toContainText("still open")
    await expect(rows.nth(1)).toContainText("1 hour")
    // The machine's own line carries the machine's mark, not a person's.
    await expect(rows.nth(2)).toContainText("boot")
    await expect(rows.nth(2).locator('img[src="/logos/linux.svg"]')).toHaveCount(1)

    await panel.getByRole("textbox", { name: "Filter login records" }).fill("deploy")
    await expect(rows).toHaveCount(1)
    await expect(page).toHaveURL(/[?&]q=deploy/)
    await panel.getByRole("textbox", { name: "Filter login records" }).fill("nobody-by-this-name")
    await expect(panel).toContainText("Nothing matches “nobody-by-this-name”.")
    await panel.getByRole("textbox", { name: "Filter login records" }).fill("")

    await panel.getByRole("radio", { name: "Failed", exact: true }).click()
    await expect(page).toHaveURL(/[?&]failed=1/)
    await expect(panel.getByRole("heading", { name: "Failed login attempts" })).toBeVisible()
    await expect(rows).toHaveCount(2)
    await expect(rows.nth(0)).toContainText("root")
    await expect(rows.nth(0)).toContainText("203.0.113.9")
    await expect(panel.getByRole("columnheader", { name: "Lasted" })).toHaveCount(0)

    // The address alone opens the same view.
    await page.goto(`${LOGINS}?failed=1&q=admin`)
    await loaded(page)
    await expect(panel.getByRole("heading", { name: "Failed login attempts" })).toBeVisible()
    await expect(rows).toHaveCount(1)
    await expect(rows.nth(0)).toContainText("admin")
  })

  test("new records wait to be shown, and Refresh asks again", async ({ page }) => {
    await mockLogins(page)
    await page.goto(LOGINS)
    await loaded(page)
    const rows = page.locator("#recent tbody tr")
    await expect(rows).toHaveCount(3)

    await page.unrouteAll({ behavior: "ignoreErrors" })
    await mockLogins(page, [], {
      overrides: {
        "/logins": [
          {
            kind: "login",
            user: "mira",
            tty: "pts/3",
            from: "100.110.34.12",
            loginTime: iso(1),
            active: true,
          },
          ...logins,
        ],
      },
    })
    await page.getByRole("button", { name: "Logins actions", exact: true }).click()
    await page.getByRole("menuitem", { name: "Refresh", exact: true }).click()
    const show = page.getByRole("button", { name: "Show 1 new logins", exact: true })
    await expect(show).toBeVisible()
    await expect(rows).toHaveCount(3)
    await show.click()
    await expect(rows).toHaveCount(4)
    await expect(rows.nth(0)).toContainText("mira")

    // The page's keys are listed from the line's own button.
    await page.getByRole("button", { name: "Logins shortcuts", exact: true }).click()
    await expect(page.getByRole("dialog")).toContainText("Refresh data")
  })

  test("a read-only account reads the page and is offered nothing it cannot use", async ({
    page,
  }) => {
    let askedForFailed = false
    page.on("request", (request) => {
      if (/\/api\/v1\/logins\/(attackers|failed)/.test(request.url())) askedForFailed = true
    })
    await mockLogins(page, [], { session: viewer })
    await page.goto(LOGINS)
    await loaded(page)

    await expect(page.getByRole("region", { name: "Logins on this server" })).toContainText(
      "failed attempts: administrators only",
    )
    await expect(page.locator("#sessions [data-slot=choice-row]")).toHaveCount(1)
    await expect(page.locator("#recent tbody tr")).toHaveCount(3)
    await expect(page.locator("#attackers")).toHaveCount(0)
    await expect(page.locator("[data-slot=logins-picture] [data-node^='attacker:']")).toHaveCount(0)
    await expect(page.getByRole("radio", { name: "Failed", exact: true })).toHaveCount(0)
    await expect(page.getByRole("button", { name: /^Actions for / })).toHaveCount(0)
    expect(askedForFailed).toBe(false)
  })

  test("an administrator who may not end a session is not offered it", async ({ page }) => {
    await mockLogins(page, [], { session: narrowed })
    await page.goto(LOGINS)
    await loaded(page)
    await expect(page.locator("#sessions [data-slot=choice-row]")).toHaveCount(1)
    await expect(
      page.getByRole("button", { name: "Actions for ubuntu on pts/0", exact: true }),
    ).toHaveCount(0)
    // Blocking is an administrator's and not destructive: it stays.
    await page.getByRole("button", { name: "Actions for 198.51.100.4", exact: true }).click()
    await expect(page.getByRole("menuitem", { name: "Block at the firewall" })).toBeVisible()
  })

  test("nobody in and nobody trying is said in the rows' own place", async ({ page }) => {
    await mockLogins(page, [], {
      overrides: {
        "/ssh-sessions": [],
        "/logins": [],
        "/logins/attackers": {
          attempts: 0,
          addresses: 0,
          windowHours: 168,
          capped: false,
          attackers: [],
        },
      },
    })
    await page.goto(LOGINS)
    await loaded(page)
    const line = page.getByRole("region", { name: "Logins on this server" })
    await expect(line).toContainText("Nobody signed in")
    await expect(line).toContainText("0 failed attempts in 7 days")
    await expect(line).not.toContainText("trying")
    await expect(page.locator("[data-slot=logins-picture]")).toContainText("no session open")
    await expect(page.locator("#sessions")).toContainText("Nobody holds a shell on this server.")
    await expect(page.locator("#attackers")).toContainText("No failed attempt in the last 7 days.")
    await expect(page.locator("#recent")).toContainText("No login on record.")
  })

  test("a server that cannot read its records says which tool is missing", async ({ page }) => {
    await mockLogins(page, [], {
      overrides: { "/logins": noRecord, "/logins/attackers": noRecord },
    })
    await page.goto(LOGINS)
    await loaded(page)
    await expect(page.locator("[data-slot=error-state]")).toHaveCount(0)
    await expect(page.locator("#attackers")).toContainText("cannot read its failed-login record")
    await expect(page.locator("#attackers")).toContainText("lastb")
    await expect(page.locator("#recent")).toContainText("cannot read its login record")
    await expect(page.locator("#recent").getByRole("link", { name: "Packages" })).toHaveAttribute(
      "href",
      "/packages",
    )
    // The sessions are another read, and are still drawn.
    await expect(page.locator("#sessions [data-slot=choice-row]")).toHaveCount(1)
  })

  test("a read that failed says so and is asked again from where it stands", async ({ page }) => {
    await mockLogins(page, [], { overrides: { "/ssh-sessions": unreadable } })
    await page.goto(LOGINS)
    await loaded(page)
    await expect(page.getByRole("region", { name: "Logins on this server" })).toContainText(
      "Sessions unread",
    )
    await expect(page.locator("[data-slot=logins-picture]")).toContainText("sessions not read")
    await expect(page.locator("[data-slot=logins-picture]")).not.toContainText("no session open")
    const failed = page.locator("#sessions [data-slot=error-state]")
    await expect(failed).toBeVisible()

    await page.unrouteAll({ behavior: "ignoreErrors" })
    await mockLogins(page)
    await failed.getByRole("button", { name: "Try again" }).click()
    await expect(page.locator("#sessions [data-slot=choice-row]")).toHaveCount(1)
    await expect(page.locator("[data-slot=logins-picture]")).toContainText("1 session open")
  })
})

test.describe("security overview", () => {
  const node = (page: Page, name: string) =>
    page.getByRole("list", { name: "The ways onto this machine" }).locator(`[data-node=${name}]`)

  test("opens on how it is reached and the ways in, whose marks are the ways to its pages", async ({
    page,
  }) => {
    await mockLogins(page)
    await page.goto("/security")
    await loaded(page)

    await expect(page.locator("[data-slot=stat-grid]")).toHaveCount(0)
    await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
    // The picture is the block directly under the identity line.
    const order = await page.evaluate(() =>
      Array.from(document.querySelector("[data-slot=page]")!.children)
        // Past the page's name, which is there for a screen reader alone.
        .filter((el) => el.getBoundingClientRect().height > 1)
        .map((el) =>
          el.matches("[data-slot=host-identity]")
            ? "identity"
            : el.querySelector("ol[aria-label='The ways onto this machine']")
              ? "picture"
              : el.matches("[data-slot=panel]")
                ? "panel"
                : "other",
        ),
    )
    expect(order.slice(0, 2)).toEqual(["identity", "picture"])

    for (const [name, label, href] of [
      ["connections", "Connections", "/network/connections"],
      ["firewall", "Firewall", "/network/firewall"],
      ["intrusion", "Intrusion", "/security/intrusion"],
      ["ssh", "SSH", "/security/ssh"],
      ["logins", "Logins", "/security/logins"],
    ] as const) {
      await expect(
        node(page, name).getByRole("link", { name: label, exact: true }),
      ).toHaveAttribute("href", href)
    }

    // Each reading a tile carried is on its node.
    await expect(node(page, "connections")).toContainText("1 connected now")
    await expect(node(page, "connections")).toContainText("2 addresses · 14 sockets")
    await expect(node(page, "connections").locator('img[src="/logos/caddy.svg"]')).toHaveCount(1)
    await expect(node(page, "firewall")).toContainText("ufw")
    await expect(node(page, "firewall")).toContainText("deny inbound · 3 rules")
    await expect(node(page, "firewall")).toContainText(":22")
    await expect(node(page, "intrusion")).toContainText("2 jails watching")
    await expect(node(page, "intrusion")).toContainText("2 banned now")
    await expect(node(page, "ssh")).toContainText("Passwords allowed")
    await expect(node(page, "ssh")).toContainText("port 22")
    await expect(node(page, "ssh").getByText("Passwords allowed", { exact: true })).toHaveClass(
      /text-warning/,
    )
    await expect(node(page, "logins")).toContainText("ubuntu")
    await expect(node(page, "logins")).toContainText("1 session · 1 over ssh")
    await expect(node(page, "logins").locator('img[src="/logos/tailscale.svg"]')).toHaveCount(1)

    await node(page, "logins").getByRole("link", { name: "Logins", exact: true }).click()
    await expect(page).toHaveURL(/\/security\/logins$/)
  })

  test("a read that failed is unreadable on its node, not a missing layer", async ({ page }) => {
    await mockLogins(page, [], {
      overrides: {
        "/fail2ban/": unreadable,
        "/connections": unreadable,
        "/ssh-sessions": unreadable,
        "/ssh/config": unreadable,
      },
    })
    await page.goto("/security")
    await loaded(page)
    await expect(node(page, "intrusion")).toContainText("unreadable")
    await expect(node(page, "intrusion")).not.toContainText("not installed")
    await expect(node(page, "connections")).toContainText("unreadable")
    await expect(node(page, "ssh")).toContainText("Unreadable")
    await expect(node(page, "logins")).toContainText("Unreadable")
    await expect(node(page, "logins")).not.toContainText("Nobody")
  })

  test("an unread firewall is not drawn as no firewall, and the picture keeps its place", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 900 })
    const picture = "ol[aria-label='The ways onto this machine']"
    await mockLogins(page, [], {
      overrides: {
        "/firewall/": PENDING,
        "/fail2ban/": PENDING,
        "/connections": PENDING,
        "/ssh-sessions": PENDING,
        "/ssh/config": PENDING,
      },
    })
    await page.goto("/security")
    await page.waitForSelector(picture)
    await expect(node(page, "firewall")).toContainText("not read yet")
    await expect(node(page, "firewall")).not.toContainText("nothing filters")
    await expect(node(page, "firewall")).not.toContainText("None")
    await expect(node(page, "intrusion")).toContainText("not read yet")
    const waiting = await places(page, [picture])

    await page.unrouteAll({ behavior: "ignoreErrors" })
    await mockLogins(page)
    await page.goto("/security")
    await loaded(page)
    await expect(node(page, "firewall")).toContainText("deny inbound")
    expect(await places(page, [picture])).toEqual(waiting)
  })

  test("reads on a phone without scrolling sideways, every word kept", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await mockLogins(page)
    await page.goto("/security")
    await loaded(page)
    await expect(node(page, "firewall")).toContainText("deny inbound · 3 rules")
    await expect(node(page, "ssh")).toContainText("Passwords allowed")
    await expect(node(page, "logins")).toContainText("1 session · 1 over ssh")
    expect(await overflow(page)).toBeLessThanOrEqual(0)
  })
})
