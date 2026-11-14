import { expect, test, type Locator, type Page } from "@playwright/test"
import {
  crowdsec,
  crowdsecFlushed,
  mockIntrusion,
  sshd,
  sshdPolicy,
  suricata,
  viewer,
  type Mutation,
} from "./security-intrusion-fixture"
import { loaded } from "./network-fixture"
import { risen } from "./risen"

const JUMP_KEYS = [
  "allowtcpforwarding",
  "allowagentforwarding",
  "gatewayports",
  "permittunnel",
  "maxsessions",
]

/**
 * Intrusion and SSH against a mocked API (`security-intrusion-fixture.ts`).
 *
 * Intrusion opens on its identity line and its picture, with one engine at a
 * time as a view in the address: the forms send exactly what the routes
 * take, a release asks before it deletes, a jail is a page of its own, rows
 * are one height whatever they hold, the opening block is where it will be
 * before the engines answer, and an engine that is not installed hands off
 * to its package. SSH's settings are one column behind the product's save
 * bar, and the jump host's preset only stages. fail2ban's log and the auth
 * log are covered in `security-ui.spec.ts`.
 */

const PAGES = ["/security/intrusion", "/security/ssh"]

/** Neither CrowdSec nor Suricata is installed: fail2ban is the one engine there. */
const BARE = {
  "/security/crowdsec/": {
    installed: false,
    active: false,
    decisions: [],
    alerts: [],
    bouncers: [],
  },
  "/security/suricata/": {
    installed: false,
    active: false,
    mode: "ids",
    alerts: [],
    scanned: 0,
    bySeverity: [],
    topSignatures: [],
    logPath: "/var/log/suricata/eve.json",
  },
}

/**
 * Review screenshots, for the eyes the checks do not have. Written only when
 * asked for, into the directory named — never into the tree.
 */
test.describe("screenshots", () => {
  const dir = process.env.JD_NETWORK_SHOTS
  test.skip(!dir, "set JD_NETWORK_SHOTS to a directory to capture them")

  for (const width of [390, 1440]) {
    test.describe(`${width} wide`, () => {
      test.use({ viewport: { width, height: 1000 } })

      const shoot = async (page: import("@playwright/test").Page, name: string) => {
        await loaded(page)
        await page.waitForTimeout(1500)
        await page.screenshot({ path: `${dir}/${name}-${width}.png`, fullPage: true })
        for (let part = 1; part <= 8; part++) {
          const moved = await page.locator("[data-slot=page]").evaluate((element) => {
            let parent = element.parentElement
            while (parent && !/auto|scroll/.test(getComputedStyle(parent).overflowY)) {
              parent = parent.parentElement
            }
            if (!parent || parent.scrollTop + parent.clientHeight >= parent.scrollHeight - 1)
              return false
            parent.scrollTop += parent.clientHeight - 80
            return true
          })
          if (!moved) break
          await page.waitForTimeout(300)
          await page.screenshot({ path: `${dir}/${name}-${width}-scroll-${part}.png` })
        }
      }

      for (const path of PAGES) {
        test(`${path} at ${width}`, async ({ page }) => {
          await mockIntrusion(page)
          await page.goto(path)
          await shoot(page, path.replace(/^\/security\//, ""))
        })
      }

      test(`intrusion without CrowdSec and Suricata at ${width}`, async ({ page }) => {
        await mockIntrusion(page, [], { overrides: BARE })
        await page.goto("/security/intrusion")
        await shoot(page, "intrusion-bare")
      })

      test(`ssh as a jump host, staged, at ${width}`, async ({ page }) => {
        await mockIntrusion(page)
        await page.goto("/security/ssh")
        await page.getByRole("button", { name: "Use it as a jump host" }).click()
        await page.getByRole("button", { name: /^build-box/ }).click()
        await page.getByRole("button", { name: /^office-nas/ }).click()
        await page.getByLabel("Another host").fill("10.0.0.5")
        await page.getByRole("button", { name: "Add" }).click()
        await shoot(page, "ssh-jump-host")
      })
    })
  }
})

/** No engine at all, and nothing refused. */
const NONE = {
  ...BARE,
  "/fail2ban/": { available: false, running: false, jails: [] },
  "/security/blocks": {
    entries: [],
    distinct: 0,
    duplicated: 0,
    covered: 0,
    communityOnly: 0,
    truncated: false,
    engines: [],
  },
}

const ENGINE_READS = [
  "/fail2ban/",
  "/security/crowdsec/",
  "/security/suricata/",
  "/security/blocks",
]

/** Holds the named reads back until `release` is called, then lets the mock answer them. */
async function gate(page: Page, paths: string[]) {
  let release = () => {}
  const open = new Promise<void>((resolve) => {
    release = resolve
  })
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname.replace(/^\/api\/v1/, "")
    if (paths.includes(path)) await open
    return route.fallback()
  })
  return release
}

const view = (page: Page) => page.getByRole("tabpanel")
const heights = async (rows: Locator) =>
  new Set(
    (await Promise.all((await rows.all()).map((row) => row.boundingBox()))).map((box) =>
      Math.round(box!.height),
    ),
  )

test("the page opens on its line, its picture and the first engine installed", async ({ page }) => {
  await mockIntrusion(page)
  await page.goto("/security/intrusion")
  await loaded(page)

  // No row of figures opens the page, or any view of it.
  await expect(page.locator("[data-slot=stat-grid]")).toHaveCount(0)
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)

  const line = page.locator("[data-slot=intrusion-identity]")
  await expect(line).toContainText("Intrusion prevention")
  await expect(line).toContainText("3 engines watching")
  await expect(line).toContainText("9 addresses refused")
  await expect(line).toContainText("2 held more than once")
  await expect(line.getByRole("button", { name: "Ban an address" })).toBeVisible()

  // The picture: who knocks, this server, and each engine with what it holds.
  const picture = page.locator("[data-slot=wire-flow]")
  await expect(picture).toContainText("The internet")
  await expect(picture).toContainText("3 attempts inside a jail’s window")
  await expect(picture).toContainText("9 addresses refused")
  await expect(picture).toContainText("fail2ban 2 · CrowdSec 7 · firewall 1")
  await expect(picture.locator("[data-node=fail2ban]")).toContainText("2 banned now · running")
  await expect(picture.locator("[data-node=crowdsec]")).toContainText("8 decisions · enforcing")
  await expect(picture.locator("[data-node=suricata]")).toContainText(
    "214 alerts scanned · only reads",
  )

  // One engine at a time, the first installed one without a view in the address.
  await expect(page.getByRole("tab", { name: "fail2ban" })).toHaveAttribute("aria-selected", "true")
  await expect(page.locator("[data-slot=engine-head]")).toHaveCount(1)
  await expect(view(page)).toContainText("1 jail")
  await expect(view(page).getByText("Decisions", { exact: true })).toHaveCount(0)

  await page.getByRole("tab", { name: "CrowdSec" }).click()
  await expect(page).toHaveURL(/\/security\/intrusion\?tab=crowdsec$/)
  await expect(view(page).locator("[data-slot=engine-head]")).toContainText("CrowdSec")
  // An engine's node in the picture opens its view too.
  await picture.getByRole("button", { name: "Open Suricata" }).click()
  await expect(page).toHaveURL(/tab=suricata$/)
  await expect(view(page).locator("[data-slot=engine-head]")).toContainText("Suricata")
  await page.getByRole("tab", { name: /^Blocked/ }).click()
  await expect(view(page)).toContainText("Blocked across engines")
})

test("an address that names an engine opens on it, installed or not", async ({ page }) => {
  await mockIntrusion(page, [], { overrides: BARE })
  await page.goto("/security/intrusion?tab=suricata")
  await expect(page.getByRole("tab", { name: "Suricata" })).toHaveAttribute("aria-selected", "true")
  await expect(view(page).getByText("Suricata is not installed")).toBeVisible()
  // The picture keeps a place for it: a ring that opens the view that installs it.
  await expect(page.locator("[data-slot=wire-flow] [data-node=suricata]")).toContainText(
    "not installed",
  )
  await expect(page.locator("[data-slot=intrusion-identity]")).toContainText("1 engine watching")
})

test("without a view in the address the page skips engines that are not installed", async ({
  page,
}) => {
  await mockIntrusion(page, [], {
    overrides: { "/fail2ban/": { available: false, running: false, jails: [] } },
  })
  await page.goto("/security/intrusion")
  await expect(page.getByRole("tab", { name: "CrowdSec" })).toHaveAttribute("aria-selected", "true")
  await expect(view(page).locator("[data-slot=engine-head]")).toContainText("CrowdSec")
  // A view that is not one falls back the same way.
  await page.goto("/security/intrusion?tab=jails")
  await expect(page.getByRole("tab", { name: "CrowdSec" })).toHaveAttribute("aria-selected", "true")
})

for (const width of [1280, 390]) {
  test(`the opening block is where it will be before the engines answer, at ${width}`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 })
    await mockIntrusion(page)
    const release = await gate(page, ENGINE_READS)
    await page.goto("/security/intrusion")
    const line = page.locator("[data-slot=intrusion-identity]")
    const picture = page.locator("[data-slot=wire-flow]")
    const tabs = page.getByRole("tablist", { name: "Engines" })
    await expect(picture).toHaveAttribute("data-loading", "true")
    await risen(page)
    const before = await Promise.all([line, picture, tabs].map((part) => part.boundingBox()))

    release()
    await expect(line).toContainText("3 engines watching")
    await expect(picture).not.toHaveAttribute("data-loading", "true")
    await expect(view(page)).toContainText("1 jail")
    await risen(page)
    const after = await Promise.all([line, picture, tabs].map((part) => part.boundingBox()))
    expect(after.map((box) => [box!.y, box!.height])).toEqual(
      before.map((box) => [box!.y, box!.height]),
    )
    // Nothing on the page runs off its side.
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(
      width,
    )
  })
}

test("CrowdSec reads its decisions, alerts and bouncers", async ({ page }) => {
  await mockIntrusion(page)
  await page.goto("/security/intrusion?tab=crowdsec")
  const section = view(page)

  const head = section.locator("[data-slot=engine-head]")
  await expect(head).toContainText("8 decisions in force")
  await expect(head).toContainText("5 from the community list")
  // The claim is the verdict's: the engine running is not what the head says.
  await expect(head.getByText("enforcing", { exact: true })).toBeVisible()
  // The two readings: whether decisions are dropped, and what fired in the
  // last day (the alerts older than a day are on record and not counted).
  const lines = section.locator("[data-slot=reading-line]")
  await expect(lines.filter({ hasText: "Enforcement" })).toContainText(
    "Decisions are being dropped",
  )
  await expect(lines.filter({ hasText: "Enforcement" })).toContainText("2 of 2 pulled within 3 min")
  await expect(lines.filter({ hasText: "Alerts" })).toContainText(
    "5 scenarios fired in the last day",
  )
  await expect(lines.filter({ hasText: "Alerts" })).toContainText("6 on record")

  // The origin is said in words, and the community list is the one CAPI means.
  const decisions = section.getByRole("list", { name: "Decisions" })
  const rows = decisions.locator("[data-slot=line-row]")
  await expect(rows.filter({ hasText: "203.0.113.141" })).toContainText("the community list")
  await expect(rows.filter({ hasText: "192.0.2.77" })).toContainText("added by hand")
  await expect(rows.filter({ hasText: "203.0.113.9" })).toContainText("detected on this server")
  await expect(rows.filter({ hasText: "203.0.113.141" })).toContainText("🇨🇳")
  await expect(rows.filter({ hasText: "203.0.113.0/24" })).toContainText("🇳🇱")

  await section.getByRole("button", { name: /^Decided here/ }).click()
  await expect(rows).toHaveCount(3)
  await section.getByRole("button", { name: /^Community list/ }).click()
  await expect(rows).toHaveCount(5)

  await expect(section.getByText("firewall-bouncer-nftables", { exact: true })).toBeVisible()
  await expect(section.getByText("caddy-bouncer", { exact: true })).toBeVisible()
  const kernel = section.getByLabel("Kernel sets")
  await expect(kernel).toContainText("nftables")
  await expect(kernel).toContainText("crowdsec-blacklists-crowdsec")
  await expect(kernel).toContainText("5 addresses")
  await expect(kernel).toContainText("dropped on the input hook")
})

for (const width of [1280, 390]) {
  test(`a decision, a blocked address and an alert are one height whatever they hold, at ${width}`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 })
    await mockIntrusion(page)
    await page.goto("/security/intrusion?tab=crowdsec")
    await loaded(page)
    await risen(page)
    // 192.0.2.77 was added by hand and has no country or network owner.
    const decisions = view(page).getByRole("list", { name: "Decisions" })
    const rows = decisions.locator("[data-slot=line-row]")
    await expect(rows).toHaveCount(8)
    expect((await heights(rows)).size).toBe(1)
    const tops = (await Promise.all((await rows.all()).map((row) => row.boundingBox()))).map(
      (box) => box!.y,
    )
    const steps = new Set(tops.slice(1).map((y, i) => Math.round(y - tops[i])))
    expect(steps.size).toBe(1)
    expect(
      (await heights(view(page).getByRole("list", { name: "Alerts" }).locator("li"))).size,
    ).toBe(1)
    // The bouncers: one with a unit beside its kind, one without.
    expect(
      (
        await heights(
          view(page)
            .locator("[data-slot=panel]")
            .filter({ hasText: "Bouncers" })
            .locator("ul")
            .first()
            .locator("> li"),
        )
      ).size,
    ).toBe(1)

    // The merged list: an address held twice and inside a broader block, and one held once.
    await page.getByRole("tab", { name: /^Blocked/ }).click()
    const blocked = view(page).getByRole("list", { name: "Blocked addresses" })
    await expect(blocked.locator("[data-slot=line-row]")).toHaveCount(4)
    await risen(page)
    expect((await heights(blocked.locator("[data-slot=line-row]"))).size).toBe(1)

    await page.getByRole("tab", { name: "Suricata" }).click()
    const alerts = view(page).getByRole("list", { name: "Latest alerts" })
    await expect(alerts.locator("[data-slot=line-row]")).toHaveCount(20)
    await risen(page)
    expect((await heights(alerts.locator("[data-slot=line-row]"))).size).toBe(1)
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(
      width,
    )
  })
}

test("a bouncer pulling into a flushed kernel table is not called protection", async ({ page }) => {
  await mockIntrusion(page, [], { overrides: { "/security/crowdsec/": crowdsecFlushed } })
  await page.goto("/security/intrusion?tab=crowdsec")
  const section = view(page)
  await expect(
    section.getByText("The bouncer is pulling and nothing is dropped", { exact: true }).first(),
  ).toBeVisible()
  // The cause, named beside the bouncer it is about, as well as under the kernel sets.
  await expect(
    section
      .getByRole("listitem")
      .filter({ hasText: "no CrowdSec set exists in nftables or ipset" }),
  ).toContainText("firewall-bouncer-nftables")
  await expect(section.getByText("not dropping", { exact: true }).first()).toBeVisible()
  await expect(section.getByText(/enforced by/)).toHaveCount(0)
  await expect(section.getByLabel("Kernel sets")).toContainText("No CrowdSec set exists")
  // The page's own state and the picture say it before the view is read.
  await expect(page.locator("[data-slot=intrusion-identity]")).toContainText(
    "CrowdSec not dropping",
  )
  await expect(page.locator("[data-slot=wire-flow] [data-node=crowdsec]")).toContainText(
    "not dropping",
  )
})

test("a CrowdSec ban sends exactly the value, the duration and the reason", async ({ page }) => {
  const mutations: Mutation[] = []
  await mockIntrusion(page, mutations)
  await page.goto("/security/intrusion?tab=crowdsec")

  await page.getByRole("button", { name: "Ban an address" }).click()
  const dialog = page.getByRole("dialog", { name: "Ban an address" })
  // Opened from CrowdSec's view, the form is CrowdSec's.
  await expect(dialog.getByRole("combobox", { name: "Banned by" })).toHaveText("CrowdSec")
  await expect(dialog.getByRole("button", { name: "Ban", exact: true })).toBeDisabled()
  await dialog.getByLabel("Address or range").fill("203.0.113.77")
  await dialog.getByRole("combobox", { name: "For how long" }).click()
  await page.getByRole("option", { name: "24 hours" }).click()
  await dialog.getByLabel("Reason").fill("scanning the admin panel")
  await dialog.getByRole("button", { name: "Ban", exact: true }).click()

  await expect
    .poll(() => mutations.find((m) => m.path === "/security/crowdsec/decisions"))
    .toEqual({
      method: "POST",
      path: "/security/crowdsec/decisions",
      body: { value: "203.0.113.77", duration: "24h", reason: "scanning the admin panel" },
    })
  await expect(dialog).toHaveCount(0)
})

test("the one ban form bans through a jail as well, with only the address", async ({ page }) => {
  const mutations: Mutation[] = []
  await mockIntrusion(page, mutations)
  await page.goto("/security/intrusion")
  await loaded(page)

  await page.getByRole("button", { name: "Ban an address" }).click()
  const dialog = page.getByRole("dialog", { name: "Ban an address" })
  // Opened from fail2ban's view, it is the jail's: no length and no reason to give.
  await expect(dialog.getByRole("combobox", { name: "Banned by" })).toHaveText("fail2ban · sshd")
  await expect(dialog.getByLabel("Reason")).toHaveCount(0)
  await dialog.getByLabel("Address", { exact: true }).fill("192.0.2.200")
  await dialog.getByRole("button", { name: "Ban", exact: true }).click()
  await expect
    .poll(() => mutations.find((m) => m.path === "/fail2ban/sshd/ban")?.body)
    .toEqual({ ip: "192.0.2.200" })
  await expect(dialog).toHaveCount(0)
})

test("a ban that would lock the operator out is refused beside its field", async ({ page }) => {
  await mockIntrusion(page, [], {
    refuse: {
      "POST /security/crowdsec/decisions": {
        status: 409,
        code: "would_lock_you_out",
        message: "100.64.0.0/10 covers your own address, 100.110.34.9.",
      },
    },
  })
  await page.goto("/security/intrusion?tab=crowdsec")
  await page.getByRole("button", { name: "Ban an address" }).click()
  const dialog = page.getByRole("dialog", { name: "Ban an address" })
  await dialog.getByLabel("Address or range").fill("100.64.0.0/10")
  await dialog.getByRole("button", { name: "Ban", exact: true }).click()

  const refusal = dialog.getByRole("alert")
  await expect(refusal).toContainText("covers your own address")
  // The dialog stays open on the field it is about, with what was typed.
  await expect(dialog.getByLabel("Address or range")).toHaveValue("100.64.0.0/10")
})

test("releasing a decision asks first, then deletes that decision", async ({ page }) => {
  const mutations: Mutation[] = []
  await mockIntrusion(page, mutations)
  await page.goto("/security/intrusion?tab=crowdsec")

  await page.getByRole("button", { name: "Release 203.0.113.9" }).click()
  const dialog = page.getByRole("dialog", { name: "Release 203.0.113.9" })
  await expect(dialog).toBeVisible()
  expect(mutations.filter((m) => m.method === "DELETE")).toHaveLength(0)
  await dialog.getByRole("button", { name: "Release", exact: true }).click()

  await expect
    .poll(() => mutations.find((m) => m.method === "DELETE")?.path)
    .toBe("/security/crowdsec/decisions/11")
})

test("with no valid bouncer CrowdSec says nothing is enforcing its decisions", async ({ page }) => {
  await mockIntrusion(page, [], {
    overrides: {
      "/security/crowdsec/": {
        ...crowdsec,
        bouncers: [],
        enforcement: {
          ...crowdsec.enforcement,
          state: "unenforced",
          summary:
            "No bouncer is registered. Every decision is recorded and nothing drops the traffic it names.",
          bouncers: [],
          kernel: undefined,
          enforcedBy: [],
        },
      },
    },
  })
  await page.goto("/security/intrusion?tab=crowdsec")
  const section = view(page)
  await expect(section.getByText("No bouncer enforces the decisions").first()).toBeVisible()
  await expect(section.getByText(/nothing drops the traffic it names/)).toBeVisible()
  await expect(
    section.locator("[data-slot=reading-line]").filter({ hasText: "Enforcement" }),
  ).toContainText("0 of 0 pulled")
})

test("every engine's refused addresses are folded into one list by address", async ({ page }) => {
  await mockIntrusion(page)
  await page.goto("/security/intrusion?tab=blocked")
  const panel = view(page)
  await expect(panel).toContainText("9 addresses · fail2ban 2 · CrowdSec 7 · Firewall 1")
  const rows = panel
    .getByRole("list", { name: "Blocked addresses" })
    .locator("[data-slot=line-row]")
  const held = rows.filter({ hasText: "203.0.113.9" })
  await expect(held).toContainText("fail2ban sshd")
  await expect(held).toContainText("CrowdSec #11")
  await expect(held).toContainText("held 2 times")
  await expect(held).toContainText("203.0.113.0/24")

  await panel.getByRole("button", { name: /^Held more than once/ }).click()
  await expect(rows).toHaveCount(2)
  await panel.getByRole("button", { name: /^Inside a broader block/ }).click()
  await expect(rows).toHaveCount(1)
  await expect(panel).toContainText("4 community decisions touch nothing else")
})

test("the ban form says which engine already holds the address, for either engine", async ({
  page,
}) => {
  await mockIntrusion(page)
  await page.goto("/security/intrusion?tab=crowdsec")
  await page.getByRole("button", { name: "Ban an address" }).click()
  const dialog = page.getByRole("dialog", { name: "Ban an address" })
  await dialog.getByLabel("Address or range").fill("203.0.113.9")
  await expect(dialog.getByText(/already held by fail2ban sshd, CrowdSec #11/)).toBeVisible()
  await dialog.getByLabel("Address or range").fill("203.0.113.200")
  await expect(
    dialog.getByText("203.0.113.200 is inside 203.0.113.0/24 (the firewall)."),
  ).toBeVisible()
  await dialog.getByLabel("Address or range").fill("198.51.100.99")
  await expect(dialog.getByText(/already held|is inside/)).toHaveCount(0)

  // The same form with a jail chosen keeps what was typed and says the same.
  await dialog.getByRole("combobox", { name: "Banned by" }).click()
  await page.getByRole("option", { name: "fail2ban · sshd" }).click()
  await dialog.getByLabel("Address", { exact: true }).fill("198.51.100.4")
  await expect(dialog.getByText(/already held by fail2ban sshd, CrowdSec #12/)).toBeVisible()
})

test("a jail is a page of its own: its line, what it holds and its policy", async ({ page }) => {
  const mutations: Mutation[] = []
  await mockIntrusion(page, mutations, {
    overrides: {
      "/fail2ban/sshd/config": {
        maxRetry: 5,
        findTime: 600,
        banTime: 7200,
        ignoreIp: ["127.0.0.0/8"],
      },
    },
  })
  await page.goto("/security/intrusion")
  await loaded(page)

  // The jail's row is one line that goes to its page; nothing opens over the list.
  const row = view(page).locator("[data-slot=choice-row]").filter({ hasText: "sshd" })
  await expect(row).toContainText("2 banned now")
  await expect(row).toContainText("/var/log/auth.log")
  await row.getByRole("link", { name: "sshd" }).click()
  await expect(page).toHaveURL(/\/security\/intrusion\/jails\/sshd$/)
  await expect(page.getByRole("dialog")).toHaveCount(0)

  const line = page.locator("[data-slot=jail-identity]")
  await expect(line).toContainText("sshd")
  await expect(line).toContainText("2 banned now")
  await expect(page.locator("[data-slot=reading-line]").first()).toContainText(
    "3 attempts inside the window",
  )

  const policy = page.getByRole("region", { name: "Policy" })
  await expect(policy).toContainText("5 failures within 10 minutes earn a 2-hour ban.")
  await expect(policy).toContainText("Bans here do not stop the traffic")
  await expect(policy).toContainText("sshd listens on 2222, and the ban drops only ssh")
  await expect(policy).toContainText("journal: _SYSTEMD_UNIT=ssh.service + _COMM=sshd")
  await expect(policy).toContainText("2222 not covered")
  await expect(policy).toContainText("blocks nothing")
  await expect(policy).toContainText("restart loads 3")

  // What it holds, each address with its two verbs on its line.
  const banned = page
    .getByRole("list", { name: "Banned addresses" })
    .locator("[data-slot=line-row]")
  await expect(banned).toHaveCount(2)
  await page.getByRole("button", { name: "Unban 203.0.113.9" }).click()
  await expect
    .poll(() => mutations.find((m) => m.path === "/fail2ban/sshd/unban")?.body)
    .toEqual({ ip: "203.0.113.9" })
  await page.getByRole("button", { name: "Never ban 198.51.100.4 again" }).click()
  await expect
    .poll(() => mutations.find((m) => m.path === "/fail2ban/sshd/ignore")?.body)
    .toEqual({ ip: "198.51.100.4", add: true })

  // The rest of its verbs are behind the line's menu.
  await page.getByRole("button", { name: "Actions for sshd" }).click()
  await page.getByRole("menuitem", { name: "Release every ban" }).click()
  await expect.poll(() => mutations.some((m) => m.path === "/fail2ban/sshd/unban-all")).toBe(true)
  await page.getByRole("button", { name: "Actions for sshd" }).click()
  await page.getByRole("menuitem", { name: "Tune" }).click()
  const tuning = page.getByRole("dialog", { name: "Tune sshd" })
  await expect(tuning.getByRole("textbox", { name: "Failures" })).toHaveValue("5")
  await tuning.getByRole("button", { name: "Cancel" }).click()

  // The way back opens the engine the jail belongs to.
  await page.locator("[data-slot=page-context]").getByRole("link", { name: "Intrusion" }).click()
  await expect(page).toHaveURL(/\/security\/intrusion\?tab=fail2ban$/)
})

test("a jail's page lists the other jails beside it and walks them", async ({ page }) => {
  const jail = (name: string, banned: string[]) => ({
    name,
    currentlyFailed: 0,
    totalFailed: 12,
    currentlyBanned: banned.length,
    totalBanned: 4,
    bannedIps: banned,
    fileList: [`/var/log/${name}.log`],
  })
  await mockIntrusion(page, [], {
    overrides: {
      "/fail2ban/": {
        available: true,
        running: true,
        jails: [jail("sshd", ["203.0.113.9"]), jail("nginx-http-auth", []), jail("recidive", [])],
      },
      "/fail2ban/nginx-http-auth/policy": { ...sshdPolicy, name: "nginx-http-auth", findings: [] },
    },
  })
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/security/intrusion/jails/nginx-http-auth")
  const rail = page.getByRole("navigation", { name: "Jails" })
  await expect(rail.getByRole("link")).toHaveCount(3)
  await expect(rail.getByRole("link", { name: /nginx-http-auth/ })).toHaveAttribute(
    "aria-current",
    "page",
  )
  // No finding, no notice: the policy is its rule and what it reads.
  await expect(page.getByRole("region", { name: "Policy" })).toContainText(
    "5 failures within 10 minutes earn a 2-hour ban.",
  )
  // A jail with nothing banned says so in the same place a list would stand.
  await expect(page.locator("[data-slot=jail-identity]")).toContainText("Watching")
  await expect(page.getByText("Nothing is banned right now.")).toBeVisible()

  await page.keyboard.press("Alt+ArrowDown")
  await expect(page).toHaveURL(/\/jails\/recidive$/)
  // A jail whose policy did not come back says so and keeps the rest of its page.
  await expect(page.getByRole("region", { name: "Policy" })).toContainText(
    "The policy could not be read.",
  )
  await expect(page.getByRole("button", { name: "Next jail" })).toBeDisabled()
  await page.getByRole("button", { name: "Previous jail" }).click()
  await expect(page).toHaveURL(/\/jails\/nginx-http-auth$/)
  await rail.getByRole("link", { name: /sshd/ }).click()
  await expect(page).toHaveURL(/\/jails\/sshd$/)

  // A name fail2ban does not have is said, with the way back.
  await page.goto("/security/intrusion/jails/postfix")
  await expect(page.getByText("fail2ban has no jail named postfix")).toBeVisible()
  await expect(page.getByRole("link", { name: "Back to Intrusion" })).toBeVisible()
})

test("a ban across the access boundary is shown first and sent acknowledged", async ({ page }) => {
  const mutations: Mutation[] = []
  const impacts = [
    {
      boundary: "allowlist",
      level: "affects",
      text: "100.64.0.12 overlaps 100.64.0.0/10, which the allowlist admits: a ban refuses the dashboard's port to anyone else arriving from it.",
    },
  ]
  await mockIntrusion(page, mutations, { overrides: { "/security/boundary/check": { impacts } } })
  await page.goto("/security/intrusion?tab=crowdsec")
  await page.getByRole("button", { name: "Ban an address" }).click()
  const dialog = page.getByRole("dialog", { name: "Ban an address" })
  await dialog.getByLabel("Address or range").fill("100.64.0.12")
  await expect(dialog.getByText("This crosses the dashboard's access boundary")).toBeVisible()
  await expect(dialog.getByText(/which the allowlist admits/)).toBeVisible()
  await dialog.getByRole("button", { name: "Ban", exact: true }).click()
  await expect
    .poll(() => mutations.find((m) => m.path === "/security/crowdsec/decisions")?.body)
    .toEqual({ value: "100.64.0.12", duration: "4h", acknowledgeBoundary: true })
})

test("a ban that would cut this session cannot be sent, from a jail's page either", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await mockIntrusion(page, mutations, {
    overrides: {
      "/security/boundary/check": {
        impacts: [
          {
            boundary: "allowlist",
            level: "cuts",
            text: "100.110.34.0/24 covers 100.110.34.9, the address this session arrives from: a ban would end it.",
          },
        ],
      },
    },
  })
  await page.goto("/security/intrusion/jails/sshd")
  await page.getByRole("button", { name: "Ban an address" }).click()
  const dialog = page.getByRole("dialog", { name: "Ban an address" })
  // On a jail's page the form is that jail's, with no engine to pick.
  await expect(dialog.getByRole("combobox", { name: "Banned by" })).toHaveCount(0)
  await dialog.getByLabel("Address", { exact: true }).fill("100.110.34.0/24")
  await expect(dialog.getByText("This would cut your way in")).toBeVisible()
  await expect(dialog.getByRole("button", { name: "Ban", exact: true })).toBeDisabled()
  expect(mutations).toHaveLength(0)
})

test("a stopped fail2ban is a state with Start, and its client's error is under a fold", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  const said =
    "ERROR Failed to access socket path: /var/run/fail2ban/fail2ban.sock. Is fail2ban running?"
  await mockIntrusion(page, mutations, {
    overrides: {
      "/fail2ban/": { available: true, running: false, jails: [], error: said },
      "/systemd/fail2ban.service": {
        unit: { name: "fail2ban.service", activeState: "inactive", subState: "dead" },
        properties: {},
      },
    },
  })
  await page.goto("/security/intrusion")
  const section = view(page)
  // Its own line stays, saying what is wrong with it.
  await expect(section.locator("[data-slot=engine-head]")).toContainText("not running")
  // Its own state, not the log pane's under it.
  const state = section.locator("[data-slot=empty-state]").filter({ hasText: "fail2ban" })
  await expect(state).toContainText("fail2ban is stopped")
  await expect(state.locator('img[src="/logos/fail2ban.webp"]')).toHaveCount(1)
  // The daemon's own words are evidence, never the headline.
  await expect(state.getByText(said)).toBeHidden()
  await state.getByText("What fail2ban’s client said").click()
  await expect(state.getByText(said)).toBeVisible()
  await expect(page.locator("[data-slot=intrusion-identity]")).toContainText("fail2ban not running")
  // Nothing can take a ban from fail2ban, and CrowdSec still can.
  await expect(page.getByRole("button", { name: "Ban an address" })).toBeVisible()

  await state.getByRole("button", { name: "Start fail2ban" }).click()
  await expect
    .poll(() => mutations.some((m) => m.path === "/systemd/fail2ban.service/start"))
    .toBe(true)
  await expect(state.getByRole("link", { name: "Service details" })).toHaveAttribute(
    "href",
    "/processes/services/fail2ban.service",
  )
})

test("a fail2ban that runs and does not answer is not offered Start", async ({ page }) => {
  await mockIntrusion(page, [], {
    overrides: {
      "/fail2ban/": { available: true, running: false, jails: [], error: "timed out" },
      "/systemd/fail2ban.service": {
        unit: { name: "fail2ban.service", activeState: "active", subState: "running" },
        properties: {},
      },
    },
  })
  await page.goto("/security/intrusion")
  const state = view(page).locator("[data-slot=empty-state]").filter({ hasText: "fail2ban" })
  await expect(state).toContainText("fail2ban is running and not answering")
  await expect(state.getByRole("button", { name: "Start fail2ban" })).toHaveCount(0)
  await expect(state.getByRole("button", { name: "Check again" })).toBeVisible()
})

test("an engine whose read fails says so in its own view, and the page still opens", async ({
  page,
}) => {
  await mockIntrusion(page)
  await page.route("**/api/v1/security/crowdsec/", (route) =>
    route.fulfill({
      status: 500,
      contentType: "application/json",
      body: JSON.stringify({ error: { code: "internal", message: "cscli did not answer" } }),
    }),
  )
  await page.goto("/security/intrusion")
  await expect(page.locator("[data-slot=intrusion-identity]")).toContainText("CrowdSec not read")
  await expect(page.locator("[data-slot=wire-flow] [data-node=crowdsec]")).toContainText("not read")
  await expect(view(page)).toContainText("1 jail")
  await page.getByRole("tab", { name: "CrowdSec" }).click()
  await expect(view(page).getByText("Cscli did not answer")).toBeVisible()
  await expect(view(page).getByRole("button", { name: "Try again" })).toBeVisible()
})

test("Suricata's setup moves the capture, fetches rules and reads the queue it depends on", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await mockIntrusion(page, mutations, {
    overrides: { "/security/suricata/": { ...suricata, active: false } },
  })
  await page.goto("/security/intrusion?tab=suricata")
  const section = view(page)
  const setup = section.locator("[data-slot=panel]").filter({ hasText: "Setup" })
  await expect(setup).toContainText("af-packet in suricata.yaml · eth0")
  await expect(setup).toContainText("1,824,113 packets captured")
  await expect(setup).toContainText("capturing")
  await expect(setup).toContainText("47,213 rules enabled")
  await expect(setup).toContainText("et/open")

  await setup.getByRole("combobox", { name: "Capture interface" }).click()
  await page.getByRole("option", { name: "ens4" }).click()
  await setup.getByRole("button", { name: "Move capture" }).click()
  const dialog = page.getByRole("dialog", { name: "Capture on ens4" })
  await expect(dialog).toContainText("suricata -T")
  expect(mutations).toHaveLength(0)
  await dialog.getByRole("button", { name: "Test and move" }).click()
  await expect
    .poll(() => mutations.find((m) => m.path === "/security/suricata/interface")?.body)
    .toEqual({ interface: "ens4" })

  await setup.getByRole("button", { name: "Update rules" }).click()
  await expect
    .poll(() => mutations.some((m) => m.path === "/security/suricata/rules/update"))
    .toBe(true)
  await setup.getByRole("button", { name: "Start Suricata" }).click()
  await expect.poll(() => mutations.some((m) => m.path === "/security/suricata/start")).toBe(true)

  const inline = section.locator("[data-slot=panel]").filter({ hasText: "Inline queue" })
  await expect(inline).toContainText("A queue fails closed")
  await expect(inline).toContainText("FORWARD → queue 0")
  await expect(inline).toContainText("fails closed")
  // That the rules are only read is behind the section's ⓘ, not a paragraph under it.
  await inline.getByRole("button", { name: "About the inline queue" }).hover()
  await expect(page.getByRole("tooltip")).toContainText("never changed")
  // A stopped Suricata is the page's state and its node's word.
  await expect(page.locator("[data-slot=intrusion-identity]")).toContainText("Suricata not running")
})

test("Suricata reads its mode, its severities, its signatures and its latest alerts", async ({
  page,
}) => {
  await mockIntrusion(page)
  await page.goto("/security/intrusion?tab=suricata")
  const section = view(page)

  await expect(section.getByText("only reads")).toBeVisible()
  await expect(section.getByText("47,213 rules loaded")).toBeVisible()
  await expect(section.getByText("from /etc/default/suricata")).toBeVisible()

  // The severities are one band with its figure, not four tiles.
  await expect(section.locator("[data-slot=stat-tile]")).toHaveCount(0)
  const band = section.locator("[data-slot=bar-reading]")
  await expect(band).toContainText("214")
  await expect(band).toContainText("scanned from the tail of eve.json")
  await expect(
    band.getByRole("img", { name: "Alerts by severity: High 6, Medium 58, Low 150" }),
  ).toBeVisible()
  await expect(band.locator("[data-slot=reading-span]")).toHaveCount(3)

  // Ranked by count, the likeliest noise first, as figures in columns: no
  // bar under a name, every row one height, and the counts and their shares
  // of the alerts scanned ending on one edge down the list.
  await expect(section.locator("[data-slot=bar-list]")).toHaveCount(0)
  const signatures = section.locator("[data-slot=rank-row]")
  await expect(signatures).toHaveCount(6)
  await expect(signatures.first()).toContainText("ET SCAN Suspicious inbound to MSSQL port 1433")
  await risen(page)
  const rows = await Promise.all((await signatures.all()).map((row) => row.boundingBox()))
  expect(new Set(rows.map((box) => box!.height))).toEqual(new Set([32]))
  const shares = await Promise.all(
    (await section.locator("[data-slot=rank-share]").all()).map((cell) => cell.boundingBox()),
  )
  expect(shares).toHaveLength(6)
  expect(new Set(shares.map((box) => Math.round(box!.x + box!.width))).size).toBe(1)

  const alerts = section
    .getByRole("list", { name: "Latest alerts" })
    .locator("[data-slot=line-row]")
  await expect(alerts).toHaveCount(20)
  const first = alerts.first()
  await expect(first).toContainText("203.0.113.141")
  await expect(first).toContainText(":443")
  await expect(first).toContainText("ET EXPLOIT Apache Log4j RCE Attempt")
  await expect(first).toContainText("High")
  await expect(first).toContainText("allowed")
})

test("an IPS Suricata says it can drop, and a blocked alert says it was", async ({ page }) => {
  await mockIntrusion(page, [], {
    overrides: {
      "/security/suricata/": {
        ...suricata,
        mode: "ips",
        modeSource: "suricata.yaml (nfq)",
        alerts: suricata.alerts.map((a, i) => (i === 0 ? { ...a, action: "blocked" } : a)),
      },
    },
  })
  await page.goto("/security/intrusion?tab=suricata")
  const section = view(page)
  await expect(section.getByText("can drop what it matches")).toBeVisible()
  await expect(
    section.getByRole("list", { name: "Latest alerts" }).locator("[data-slot=line-row]").first(),
  ).toContainText("blocked")
  await expect(page.locator("[data-slot=wire-flow] [data-node=suricata]")).toContainText("can drop")
})

test("a Suricata log it may not read names the path and why", async ({ page }) => {
  await mockIntrusion(page, [], {
    overrides: {
      "/security/suricata/": {
        ...suricata,
        alerts: [],
        scanned: 0,
        bySeverity: [],
        topSignatures: [],
        logRefused: "it is outside the log roots this dashboard may read",
      },
    },
  })
  await page.goto("/security/intrusion?tab=suricata")
  const section = view(page)
  await expect(section.getByText("Suricata's alert log could not be read")).toBeVisible()
  await expect(section).toContainText("/var/log/suricata/eve.json")
  await expect(section).toContainText("outside the log roots")
  await expect(section.getByText("Alerts by severity")).toHaveCount(0)
})

test("engines that are not installed hand off to their packages, fail2ban included", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await mockIntrusion(page, mutations, { overrides: NONE })
  await page.goto("/security/intrusion")

  // With nothing installed the page says so and opens where the first install is.
  await expect(page.locator("[data-slot=intrusion-identity]")).toContainText("No engine installed")
  await expect(page.getByRole("button", { name: "Ban an address" })).toHaveCount(0)
  await expect(page.getByRole("tab", { name: "fail2ban" })).toHaveAttribute("aria-selected", "true")
  await expect(view(page).getByText("fail2ban is not installed")).toBeVisible()
  await view(page).getByRole("button", { name: "Install fail2ban" }).click()
  await expect
    .poll(() => mutations.find((m) => m.path === "/packages/install")?.body)
    .toEqual({ packages: ["fail2ban"] })

  await page.getByRole("tab", { name: "CrowdSec" }).click()
  await expect(view(page).getByText("CrowdSec is not installed")).toBeVisible()
  await expect(view(page)).toContainText("bouncer")
  await expect(view(page).getByRole("button", { name: "Install crowdsec" })).toBeVisible()
  await page.getByRole("tab", { name: "Suricata" }).click()
  await expect(view(page).getByText("Suricata is not installed")).toBeVisible()
  await expect(view(page)).toContainText("IPS mode")
  await expect(view(page)).toContainText("configuration")

  await page.getByRole("tab", { name: /^Blocked/ }).click()
  await expect(view(page)).toContainText("No blocked addresses are listed in this reading.")
})

test("a reader without the admin capability cannot ban, release or read Suricata", async ({
  page,
}) => {
  await mockIntrusion(page, [], { session: viewer })
  await page.goto("/security/intrusion")
  await loaded(page)
  // Three engines are still read about; Suricata is not asked after, and the
  // page does not wait on it.
  await expect(page.locator("[data-slot=intrusion-identity]")).toContainText("2 engines watching")
  await expect(page.locator("[data-slot=wire-flow] [data-node=suricata]")).toContainText(
    "administrators only",
  )
  await expect(page.getByRole("button", { name: "Ban an address" })).toHaveCount(0)
  // A jail still opens, with nothing to press on it.
  await expect(view(page).getByRole("button", { name: /^Actions for/ })).toHaveCount(0)
  await expect(view(page).getByRole("button", { name: "Block at the firewall" })).toHaveCount(0)

  await page.getByRole("tab", { name: "CrowdSec" }).click()
  await expect(
    view(page).getByRole("list", { name: "Decisions" }).locator("[data-slot=line-row]").first(),
  ).toBeVisible()
  await expect(page.getByRole("button", { name: /^Release 203/ })).toHaveCount(0)
  await page.getByRole("tab", { name: "Suricata" }).click()
  await expect(page.getByText("Suricata needs the admin capability")).toBeVisible()

  await page.goto("/security/intrusion/jails/sshd")
  await expect(page.locator("[data-slot=jail-identity]")).toContainText("2 banned now")
  await expect(page.getByRole("button", { name: "Ban an address" })).toHaveCount(0)
  await expect(page.getByRole("button", { name: /^Unban / })).toHaveCount(0)
  await expect(page.getByRole("button", { name: /^Never ban / })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Actions for sshd" })).toHaveCount(0)
})

test("an administrator who may not destroy is not offered a release", async ({ page }) => {
  await mockIntrusion(page, [], {
    session: { ...viewer, capabilities: ["read", "service.control", "system.admin"] },
  })
  await page.goto("/security/intrusion?tab=crowdsec")
  await expect(
    view(page).getByRole("list", { name: "Decisions" }).locator("[data-slot=line-row]").first(),
  ).toBeVisible()
  await expect(page.getByRole("button", { name: "Ban an address" })).toBeVisible()
  await expect(page.getByRole("button", { name: /^Release 203/ })).toHaveCount(0)
})

/* SSH */

const bar = (page: Page) => page.getByRole("region", { name: "Save changes" })

test("the SSH page opens on sshd's line, the doors and four lines of readings", async ({
  page,
}) => {
  await mockIntrusion(page)
  await page.goto("/security/ssh")
  await loaded(page)

  await expect(page.locator("[data-slot=stat-grid]")).toHaveCount(0)
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
  const line = page.locator("[data-slot=ssh-identity]")
  await expect(line).toContainText("sshd")
  // How a login gets in is the line's state: the fixture accepts passwords.
  await expect(line).toContainText("Passwords allowed")
  await expect(line.getByRole("link", { name: "Add a key" })).toHaveAttribute(
    "href",
    "/system-users",
  )
  await line.getByRole("button", { name: "SSH actions" }).click()
  await expect(page.getByRole("menuitem", { name: "Use it as a jump host" })).toBeVisible()
  await page.keyboard.press("Escape")

  // The auth log's last day, each count on its own line.
  const lines = page.locator("[data-slot=reading-line]")
  await expect(lines).toHaveCount(4)
  for (const name of ["Accepted logins", "Failed attempts", "Invalid users", "Attackers"])
    await expect(lines.filter({ hasText: name })).toContainText("in the last 24 hours")

  // The settings are one centred column, every row a directive at one height.
  const column = page.locator("#ssh-settings")
  const box = (await column.boundingBox())!
  expect(box.width).toBeLessThanOrEqual(770)
  const settings = column.locator("[data-slot=ssh-setting]")
  await expect(settings).toHaveCount(10)
  const lefts = new Set(
    (await Promise.all((await settings.all()).map((row) => row.boundingBox()))).map((b) =>
      Math.round(b!.x),
    ),
  )
  expect(lefts.size).toBe(1)
  // A yes-or-no directive is a switch.
  await expect(page.getByRole("switch", { name: "Password authentication" })).toBeChecked()
})

for (const width of [1280, 390]) {
  test(`a directive's row is one height edited or not, and the next row does not move, at ${width}`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 })
    await mockIntrusion(page)
    await page.goto("/security/ssh")
    await loaded(page)
    await risen(page)
    const row = page
      .locator("[data-slot=ssh-setting]")
      .filter({ hasText: "Attempts per connection" })
    const next = page.locator("[data-slot=ssh-setting]").filter({ hasText: "TCP forwarding" })
    // Measured against each other: typing scrolls the field into view.
    const measure = async () => {
      const [a, b] = [(await row.boundingBox())!, (await next.boundingBox())!]
      return [a.height, b.height, b.y - a.y]
    }
    const before = await measure()
    await page.getByRole("textbox", { name: "Attempts per connection" }).fill("3")
    await expect(row).toContainText("sshd runs 6")
    await expect(row).toHaveAttribute("data-edited", "true")
    expect(await measure()).toEqual(before)
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(
      width,
    )
  })
}

test("an SSH change applies until confirmed, with what it does to the tunnel shown first", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  const headers: Record<string, string>[] = []
  page.on("request", (request) => {
    if (request.method() === "POST" && request.url().endsWith("/api/v1/ssh/config"))
      headers.push(request.headers())
  })
  await mockIntrusion(page, mutations, {
    overrides: {
      "/network/changes/current": { available: true, owned: false, change: null },
      "/security/boundary/check": {
        impacts: [
          {
            boundary: "ssh",
            level: "affects",
            text: "sshd moves from 22 to 2222: every SSH tunnel to the dashboard must be opened on 2222.",
          },
        ],
      },
    },
  })
  await page.goto("/security/ssh")
  await page.getByRole("textbox", { name: "Port" }).fill("2222")
  // The product's one save bar, and nothing sent until the review is answered.
  await expect(bar(page)).toContainText("1 unsaved change")
  await bar(page).getByRole("button", { name: "Save" }).click()
  const dialog = page.getByRole("dialog", { name: "Apply SSH changes" })
  await expect(dialog.getByRole("list", { name: "Staged changes" })).toContainText("Port")
  await expect(dialog.getByRole("list", { name: "Staged changes" })).toContainText("22 → 2222")
  await expect(
    dialog.getByRole("switch", { name: "Restore unless confirmed after reconnecting" }),
  ).toBeChecked()
  await expect(dialog).toContainText("sshd moves from 22 to 2222")
  await expect(dialog).toContainText("the host restores them and reloads sshd")
  expect(mutations).toHaveLength(0)
  await dialog.getByRole("button", { name: "Test and apply" }).click()
  await expect
    .poll(() => mutations.find((m) => m.path === "/ssh/config")?.body)
    .toEqual({ settings: { port: "2222" }, acknowledgeBoundary: true })
  expect(headers[0]["x-jd-network-apply"]).toBe("pending")
  await expect(dialog).toHaveCount(0)
})

test("the review's recovery can be turned off, and closing it applies nothing", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  const headers: Record<string, string>[] = []
  page.on("request", (request) => {
    if (request.method() === "POST" && request.url().endsWith("/api/v1/ssh/config"))
      headers.push(request.headers())
  })
  await mockIntrusion(page, mutations, {
    overrides: { "/network/changes/current": { available: true, owned: false, change: null } },
  })
  await page.goto("/security/ssh")
  await page.getByRole("textbox", { name: "Port" }).fill("2222")
  await bar(page).getByRole("button", { name: "Save" }).click()
  const dialog = page.getByRole("dialog", { name: "Apply SSH changes" })
  await dialog.getByRole("button", { name: "Cancel" }).click()
  await expect(dialog).toHaveCount(0)
  // The edit is still staged.
  await expect(bar(page)).toContainText("1 unsaved change")
  await expect(page.getByRole("textbox", { name: "Port" })).toHaveValue("2222")
  expect(mutations.filter((m) => m.path === "/ssh/config")).toHaveLength(0)

  await bar(page).getByRole("button", { name: "Save" }).click()
  await dialog.getByRole("switch", { name: "Restore unless confirmed after reconnecting" }).click()
  await expect(dialog).toContainText("confirm you can still log in from a second terminal")
  await dialog.getByRole("button", { name: "Test and apply" }).click()
  await expect.poll(() => headers.length).toBe(1)
  expect(headers[0]["x-jd-network-apply"]).toBeUndefined()
})

test("an SSH change that would cut the way in cannot be applied", async ({ page }) => {
  const mutations: Mutation[] = []
  await mockIntrusion(page, mutations, {
    overrides: {
      "/security/boundary/check": {
        impacts: [
          {
            boundary: "ssh",
            level: "cuts",
            text: "AllowUsers leaves out ubuntu, the account this tunnel signs in as.",
          },
        ],
      },
    },
  })
  await page.goto("/security/ssh")
  await page.getByRole("textbox", { name: "Only these accounts may log in" }).fill("deploy")
  await bar(page).getByRole("button", { name: "Save" }).click()
  const dialog = page.getByRole("dialog", { name: "Apply SSH changes" })
  await expect(dialog.getByText("This would cut your way in")).toBeVisible()
  await expect(dialog.getByRole("button", { name: "Test and apply" })).toBeDisabled()
  expect(mutations.filter((m) => m.path === "/ssh/config")).toHaveLength(0)
})

test("an SSH change on a host without the watchdog applies immediately as before", async ({
  page,
}) => {
  const headers: Record<string, string>[] = []
  page.on("request", (request) => {
    if (request.method() === "POST" && request.url().endsWith("/api/v1/ssh/config"))
      headers.push(request.headers())
  })
  await mockIntrusion(page)
  await page.goto("/security/ssh")
  await page.getByRole("textbox", { name: "Sessions per connection" }).fill("4")
  await bar(page).getByRole("button", { name: "Save" }).click()
  const dialog = page.getByRole("dialog", { name: "Apply SSH changes" })
  await expect(
    dialog.getByRole("switch", { name: "Restore unless confirmed after reconnecting" }),
  ).toHaveCount(0)
  await expect(dialog).toContainText("confirm you can still log in from a second terminal")
  await dialog.getByRole("button", { name: "Test and apply" }).click()
  await expect.poll(() => headers.length).toBe(1)
  expect(headers[0]["x-jd-network-apply"]).toBeUndefined()
})

test("the jump host's preset stages four values and applies nothing until the review", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await mockIntrusion(page, mutations)
  await page.goto("/security/ssh")
  await loaded(page)

  // The saved file refuses forwarding, so the door is drawn shut and the
  // snippet area says jumping is refused.
  const doors = page.getByRole("list", { name: "How a login reaches a shell" })
  await expect(doors).toContainText("Forwarding")
  await expect(doors).toContainText("refused")
  await expect(page.getByText("Jumping through this server is refused")).toBeVisible()
  await expect(bar(page)).toHaveCount(0)

  await page.getByRole("button", { name: "Use it as a jump host" }).click()

  await expect(bar(page)).toContainText("4 unsaved changes")
  // The section that holds them says so, and the Edited chip counts them.
  const section = page.locator("[data-slot=setting]").filter({ hasText: "Jump host" })
  await expect(section.getByText("Unsaved changes")).toBeVisible()
  await expect(page.getByRole("button", { name: /^Edited/ })).toContainText("4")
  // The picture follows the draft, and the section's settings hold the values.
  await expect(doors).toContainText("may reach other hosts through it")
  await expect(page.getByRole("combobox", { name: "TCP forwarding" })).toHaveText("yes")
  await expect(page.getByRole("combobox", { name: "Gateway ports" })).toHaveText("no")
  await expect(page.getByRole("switch", { name: "Agent forwarding" })).not.toBeChecked()
  await expect(page.getByText("Jumping through this server is refused")).toHaveCount(0)
  expect(mutations).toHaveLength(0)

  await bar(page).getByRole("button", { name: "Save" }).click()
  const dialog = page.getByRole("dialog", { name: "Apply SSH changes" })
  // The review names what changed, each with the value sshd runs and the one it would.
  const staged = dialog.getByRole("list", { name: "Staged changes" })
  await expect(staged.getByRole("listitem")).toHaveCount(4)
  await expect(staged).toContainText("TCP forwarding")
  await expect(staged).toContainText("no → yes")
  await expect(staged).toContainText("Tunnel devices")
  await expect(staged).toContainText("ethernet → no")
  await dialog.getByRole("button", { name: "Test and apply" }).click()
  await expect
    .poll(() => mutations.find((m) => m.path === "/ssh/config")?.body)
    .toEqual({
      settings: {
        allowtcpforwarding: "yes",
        allowagentforwarding: "no",
        gatewayports: "no",
        permittunnel: "no",
      },
    })
})

test("a staged jump-host setting goes through the same save bar and can be discarded", async ({
  page,
}) => {
  await mockIntrusion(page)
  await page.goto("/security/ssh")
  await page.getByRole("textbox", { name: "Sessions per connection" }).fill("4")
  await expect(bar(page)).toContainText("1 unsaved change")
  await expect(bar(page)).toContainText("sshd settings")
  // Only what is edited, where the reader asks for it.
  await page.getByRole("button", { name: /^Edited/ }).click()
  await expect(page.locator("[data-slot=ssh-setting]")).toHaveCount(1)
  await bar(page).getByRole("button", { name: "Discard" }).click()
  await expect(bar(page)).toHaveCount(0)
  await expect(page.locator("[data-slot=ssh-setting]")).toHaveCount(10)
  await expect(page.getByRole("textbox", { name: "Sessions per connection" })).toHaveValue("10")
})

test("a link to a directive lands on it with the keyboard", async ({ page }) => {
  await mockIntrusion(page)
  await page.goto("/security/ssh?setting=maxauthtries")
  await expect(page.getByRole("textbox", { name: "Attempts per connection" })).toBeFocused()
})

for (const width of [1280, 390]) {
  test(`sshd's line is where it will be before its settings are read, at ${width}`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 })
    await mockIntrusion(page)
    const release = await gate(page, ["/ssh/config"])
    await page.goto("/security/ssh")
    const line = page.locator("[data-slot=ssh-identity]")
    await expect(line).toContainText("reading")
    await risen(page)
    const before = (await line.boundingBox())!
    release()
    await expect(line).toContainText("Passwords allowed")
    await risen(page)
    const after = (await line.boundingBox())!
    expect([after.y, after.x]).toEqual([before.y, before.x])
  })
}

test("a reader without the admin capability is told so and sees no control", async ({ page }) => {
  await mockIntrusion(page, [], { session: viewer })
  await page.goto("/security/ssh")
  await expect(page.getByText("SSH needs the admin capability")).toBeVisible()
  await expect(page.locator("[data-slot=ssh-setting]")).toHaveCount(0)
  await expect(page.getByRole("link", { name: "Add a key" })).toHaveCount(0)
  await expect(page.getByRole("switch")).toHaveCount(0)
})

test("the snippet names the jump through this server and each host picked", async ({
  page,
  context,
}) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"])
  await mockIntrusion(page)
  await page.goto("/security/ssh")
  await page.getByRole("button", { name: "Use it as a jump host" }).click()

  await expect(page.getByText("Pick or type a host and its block appears here.")).toBeVisible()
  // Suggested from the tailnet and from WireGuard's single-address peers; the
  // site's whole network is not a host and is not offered.
  await expect(page.getByRole("button", { name: /^branch-office/ })).toHaveCount(0)
  await page.getByRole("button", { name: /^build-box/ }).click()
  await page.getByRole("button", { name: /^office-nas/ }).click()
  await page.getByLabel("Another host").fill("db.internal")
  await page.getByLabel("Another host").press("Enter")

  const config = page.locator("[data-slot=snippet]").first()
  await expect(config).toContainText(
    [
      "Host build-box",
      "  HostName 100.64.0.12",
      "  User ubuntu",
      "  ProxyJump ubuntu@100.110.34.31",
    ].join("\n"),
  )
  await expect(config).toContainText("HostName 10.8.0.2")
  await expect(config).toContainText("Host db.internal")
  const command = page.locator("[data-slot=snippet]").nth(1)
  await expect(command).toHaveText("ssh -J ubuntu@100.110.34.31 ubuntu@100.64.0.12")

  await page.getByRole("button", { name: "Copy the ssh command for 100.64.0.12" }).click()
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(
    "ssh -J ubuntu@100.110.34.31 ubuntu@100.64.0.12",
  )
  await page.getByRole("button", { name: "Copy the ~/.ssh/config block" }).click()
  expect(await page.evaluate(() => navigator.clipboard.readText())).toContain("ProxyJump")

  // The address and users are the reader's to correct, and a value that could
  // not be pasted into a shell config safely draws no snippet at all.
  await page.getByLabel("User on those hosts").fill("deploy")
  await expect(config).toContainText("User deploy")
  await page.getByLabel("This server", { exact: true }).fill("bastion.example.net")
  await expect(config).toContainText("ProxyJump ubuntu@bastion.example.net")
  await page.getByLabel("User on those hosts").fill("de ploy")
  await expect(page.getByText("Letters, digits, dot, dash and underscore")).toBeVisible()
  await expect(page.locator("[data-slot=snippet]")).toHaveCount(0)
})

test("a typed host that is not a host is refused beside its field", async ({ page }) => {
  await mockIntrusion(page)
  await page.goto("/security/ssh")
  await page.getByRole("button", { name: "Use it as a jump host" }).click()
  await page.getByLabel("Another host").fill("10.0.0.5; rm -rf")
  await page.getByRole("button", { name: "Add" }).click()
  await expect(page.getByText("A host name or an address, with no spaces")).toBeVisible()
  await expect(page.getByRole("button", { name: "Hosts in the snippet" })).toHaveCount(0)
})

test("a server that sends no forwarding directives has no jump host section", async ({ page }) => {
  const forwarding = [...JUMP_KEYS]
  await mockIntrusion(page, [], {
    overrides: {
      "/ssh/config": {
        ...sshd,
        settings: sshd.settings.filter((s) => !forwarding.includes(s.key)),
      },
    },
  })
  await page.goto("/security/ssh")
  await expect(page.getByRole("heading", { name: "Jump host" })).toHaveCount(0)
  await expect(page.getByRole("list", { name: "How a login reaches a shell" })).not.toContainText(
    "Forwarding",
  )
})

test.describe("without Tailscale or Docker data", () => {
  test("the suggestions are quiet and typing still works", async ({ page }) => {
    await mockIntrusion(page, [], { forbidden: ["/network/vpn", "/network/overview"] })
    await page.goto("/security/ssh")
    await page.getByRole("button", { name: "Use it as a jump host" }).click()
    await page.getByLabel("Another host").fill("10.0.0.5")
    await page.getByRole("button", { name: "Add" }).click()
    await expect(page.locator("[data-slot=snippet]").first()).toContainText("HostName 10.0.0.5")
  })
})
