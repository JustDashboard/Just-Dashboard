import { expect, test } from "@playwright/test"
import {
  crowdsec,
  crowdsecFlushed,
  mockIntrusion,
  sshd,
  suricata,
  viewer,
  type Mutation,
} from "./security-intrusion-fixture"
import { loaded } from "./network-fixture"

const JUMP_KEYS = [
  "allowtcpforwarding",
  "allowagentforwarding",
  "gatewayports",
  "permittunnel",
  "maxsessions",
]

/**
 * Intrusion's CrowdSec and Suricata sections and SSH's jump host, against a
 * mocked API (`security-intrusion-fixture.ts`): the forms send exactly what
 * the routes take, a release asks before it deletes, the jump host's preset
 * only stages, and a tool that is not installed hands off to its package.
 * fail2ban's own half of the page, and the rest of SSH, are covered in
 * `security-ui.spec.ts`.
 */

const PAGES = ["/security/intrusion", "/security/ssh"]

/** Neither tool is installed: the page's three sections are all that remain of them. */
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

test("CrowdSec reads its decisions, alerts and bouncers", async ({ page }) => {
  await mockIntrusion(page)
  await page.goto("/security/intrusion")
  const section = page.getByRole("region", { name: "CrowdSec" })

  await expect(section.getByText("8 decisions in force")).toBeVisible()
  // The three readings: what is in force, what fired in the last day (the
  // alerts older than a day are on record and not counted), and who enforces.
  const tiles = section.locator("[data-slot=stat-tile]")
  await expect(tiles.filter({ hasText: "Decisions in force" })).toContainText("8")
  await expect(tiles.filter({ hasText: "Alerts, last day" })).toContainText("5")
  await expect(tiles.filter({ hasText: "Enforcement" })).toContainText("Enforcing")
  await expect(tiles.filter({ hasText: "Enforcement" })).toContainText("2 of 2 pulled within 3 min")

  // The origin is said in words, and the community list is the one CAPI means.
  const rows = section.getByRole("row")
  await expect(rows.filter({ hasText: "203.0.113.141" })).toContainText("the community list")
  await expect(rows.filter({ hasText: "192.0.2.77" })).toContainText("added by hand")
  await expect(rows.filter({ hasText: "203.0.113.9" })).toContainText("detected on this server")
  await expect(rows.filter({ hasText: "203.0.113.141" })).toContainText("🇨🇳")
  await expect(rows.filter({ hasText: "203.0.113.0/24" })).toContainText("🇳🇱")

  await section.getByRole("button", { name: /^Decided here/ }).click()
  await expect(section.getByRole("row")).toHaveCount(1 + 3)
  await section.getByRole("button", { name: /^Community list/ }).click()
  await expect(section.getByRole("row")).toHaveCount(1 + 5)

  await expect(section.getByText("firewall-bouncer-nftables", { exact: true })).toBeVisible()
  await expect(section.getByText("caddy-bouncer", { exact: true })).toBeVisible()
  // The claim is the verdict's: the engine running is not what the head says.
  await expect(section.getByText("enforcing", { exact: true })).toBeVisible()
  const kernel = section.getByLabel("Kernel sets")
  await expect(kernel).toContainText("nftables")
  await expect(kernel).toContainText("crowdsec-blacklists-crowdsec")
  await expect(kernel).toContainText("5 addresses")
  await expect(kernel).toContainText("dropped on the input hook")
})

test("a bouncer pulling into a flushed kernel table is not called protection", async ({ page }) => {
  await mockIntrusion(page, [], { overrides: { "/security/crowdsec/": crowdsecFlushed } })
  await page.goto("/security/intrusion")
  const section = page.getByRole("region", { name: "CrowdSec" })
  await expect(
    section.getByText("The bouncer is pulling and nothing is dropped", { exact: true }),
  ).toBeVisible()
  await expect(section.getByText("no CrowdSec set exists in nftables or ipset")).toBeVisible()
  await expect(section.getByText("not dropping", { exact: true }).first()).toBeVisible()
  await expect(section.getByText(/enforced by/)).toHaveCount(0)
  await expect(section.getByLabel("Kernel sets")).toContainText("No CrowdSec set exists")
})

test("a CrowdSec ban sends exactly the value, the duration and the reason", async ({ page }) => {
  const mutations: Mutation[] = []
  await mockIntrusion(page, mutations)
  await page.goto("/security/intrusion")

  await page.getByRole("button", { name: "Ban an address" }).click()
  const dialog = page.getByRole("dialog", { name: "Ban an address" })
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
  await page.goto("/security/intrusion")
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
  await page.goto("/security/intrusion")

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
  await page.goto("/security/intrusion")
  const section = page.getByRole("region", { name: "CrowdSec" })
  await expect(section.getByText("No bouncer enforces the decisions")).toBeVisible()
  await expect(section.getByText(/nothing drops the traffic it names/)).toBeVisible()
  await expect(
    section.locator("[data-slot=stat-tile]").filter({ hasText: "Enforcement" }),
  ).toContainText("0 of 0 pulled")
})

test("every engine's refused addresses are folded into one list by address", async ({ page }) => {
  await mockIntrusion(page)
  await page.goto("/security/intrusion")
  const panel = page.locator("[data-slot=panel]").filter({ hasText: "Blocked across engines" })
  await expect(panel).toContainText("9 addresses")
  const held = panel.getByRole("row").filter({ hasText: "203.0.113.9" })
  await expect(held).toContainText("fail2ban sshd")
  await expect(held).toContainText("CrowdSec #11")
  await expect(held).toContainText("held 2 times")
  await expect(held).toContainText("203.0.113.0/24")

  await panel.getByRole("button", { name: /^Held more than once/ }).click()
  await expect(panel.getByRole("row")).toHaveCount(1 + 2)
  await panel.getByRole("button", { name: /^Inside a broader block/ }).click()
  await expect(panel.getByRole("row")).toHaveCount(1 + 1)
  await expect(panel).toContainText("4 community decisions touch nothing else")
})

test("both ban forms say which engine already holds the address", async ({ page }) => {
  await mockIntrusion(page)
  await page.goto("/security/intrusion")
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
  await dialog.getByRole("button", { name: "Cancel" }).click()

  await page.getByRole("button", { name: "sshd", exact: true }).click()
  const sheet = page.getByRole("dialog", { name: "sshd" })
  await sheet.getByLabel("Ban an address now").fill("198.51.100.4")
  await expect(sheet.getByText(/already held by fail2ban sshd, CrowdSec #12/)).toBeVisible()
})

test("the sshd jail's policy says its ban misses the port sshd moved to", async ({ page }) => {
  await mockIntrusion(page)
  await page.goto("/security/intrusion")
  await page.getByRole("button", { name: "sshd", exact: true }).click()
  const policy = page.getByRole("dialog", { name: "sshd" }).getByRole("region", { name: "Policy" })
  await expect(policy).toContainText("5 failures within 10 minutes earn a 2-hour ban.")
  await expect(policy).toContainText("Bans here do not stop the traffic")
  await expect(policy).toContainText("sshd listens on 2222, and the ban drops only ssh")
  await expect(policy).toContainText("journal: _SYSTEMD_UNIT=ssh.service + _COMM=sshd")
  await expect(policy).toContainText("2222 not covered")
  await expect(policy).toContainText("blocks nothing")
  await expect(policy).toContainText("restart loads 3")
})

test("Suricata reads its mode, its severities, its signatures and its latest alerts", async ({
  page,
}) => {
  await mockIntrusion(page)
  await page.goto("/security/intrusion")
  const section = page.getByRole("region", { name: "Suricata" })

  await expect(section.getByText("only reads")).toBeVisible()
  await expect(section.getByText("47,213 rules loaded")).toBeVisible()
  await expect(section.getByText("from /etc/default/suricata")).toBeVisible()

  const tiles = section.locator("[data-slot=stat-tile]")
  await expect(tiles.filter({ hasText: "Alerts scanned" })).toContainText("214")
  await expect(tiles.filter({ hasText: "High · severity 1" })).toContainText("6")
  await expect(tiles.filter({ hasText: "Medium · severity 2" })).toContainText("58")
  await expect(tiles.filter({ hasText: "Low · severity 3" })).toContainText("150")

  // Ranked by count, the likeliest noise first.
  const signatures = section.locator("[data-slot=bar-list] li")
  await expect(signatures).toHaveCount(6)
  await expect(signatures.first()).toContainText("ET SCAN Suspicious inbound to MSSQL port 1433")

  const alerts = section.getByRole("row")
  await expect(alerts).toHaveCount(1 + 20)
  const first = alerts.nth(1)
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
  await page.goto("/security/intrusion")
  const section = page.getByRole("region", { name: "Suricata" })
  await expect(section.getByText("can drop what it matches")).toBeVisible()
  await expect(section.getByRole("row").nth(1)).toContainText("blocked")
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
  await page.goto("/security/intrusion")
  const section = page.getByRole("region", { name: "Suricata" })
  await expect(section.getByText("Suricata's alert log could not be read")).toBeVisible()
  await expect(section).toContainText("/var/log/suricata/eve.json")
  await expect(section).toContainText("outside the log roots")
  await expect(section.locator("[data-slot=stat-tile]")).toHaveCount(0)
})

test("tools that are not installed hand off to their packages", async ({ page }) => {
  const mutations: Mutation[] = []
  await mockIntrusion(page, mutations, { overrides: BARE })
  await page.goto("/security/intrusion")

  const crowd = page.getByRole("region", { name: "CrowdSec" })
  await expect(crowd.getByText("CrowdSec is not installed")).toBeVisible()
  await expect(crowd).toContainText("community list")
  const suri = page.getByRole("region", { name: "Suricata" })
  await expect(suri.getByText("Suricata is not installed")).toBeVisible()
  await expect(suri).toContainText("IPS mode")
  await expect(suri).toContainText("configuration")

  await crowd.getByRole("button", { name: "Install crowdsec" }).click()
  await expect
    .poll(() => mutations.find((m) => m.path === "/packages/install")?.body)
    .toEqual({ packages: ["crowdsec"] })
  // Neither handoff hides fail2ban, which is still running above them.
  await expect(page.getByText("fail2ban", { exact: true }).first()).toBeVisible()
})

test("a reader without the admin capability cannot ban, release or read Suricata", async ({
  page,
}) => {
  await mockIntrusion(page, [], { session: viewer })
  await page.goto("/security/intrusion")
  await expect(page.getByRole("region", { name: "CrowdSec" }).getByRole("row").nth(1)).toBeVisible()
  await expect(page.getByRole("button", { name: "Ban an address" })).toHaveCount(0)
  await expect(page.getByRole("button", { name: /^Release 203/ })).toHaveCount(0)
  await expect(page.getByText("Suricata needs the admin capability")).toBeVisible()
})

test("the jump host's preset stages four values and applies nothing until Test and apply", async ({
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
  await expect(page.getByRole("button", { name: "Test and apply" })).toHaveCount(0)

  await page.getByRole("button", { name: "Use it as a jump host" }).click()

  await expect(page.getByText("4 unsaved changes", { exact: true })).toBeVisible()
  await expect(
    page.getByText(/TCP forwarding · Agent forwarding · Gateway ports · Tunnel devices/),
  ).toBeVisible()
  // The picture follows the draft, and the section's settings hold the values.
  await expect(doors).toContainText("may reach other hosts through it")
  await expect(page.getByRole("combobox", { name: "TCP forwarding" })).toHaveText("yes")
  await expect(page.getByRole("combobox", { name: "Gateway ports" })).toHaveText("no")
  await expect(page.getByText("Jumping through this server is refused")).toHaveCount(0)
  expect(mutations).toHaveLength(0)

  await page.getByRole("button", { name: "Test and apply" }).click()
  await page
    .getByRole("dialog", { name: "Apply SSH changes" })
    .getByRole("button", { name: "Test and apply" })
    .click()
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

test("a staged jump-host setting goes through the same apply bar and can be discarded", async ({
  page,
}) => {
  await mockIntrusion(page)
  await page.goto("/security/ssh")
  await page.getByRole("textbox", { name: "Sessions per connection" }).fill("4")
  await expect(page.getByText("1 unsaved change", { exact: true })).toBeVisible()
  await expect(page.getByText("Sessions per connection", { exact: true }).last()).toBeVisible()
  await page.getByRole("button", { name: "Discard" }).click()
  await expect(page.getByRole("button", { name: "Test and apply" })).toHaveCount(0)
  await expect(page.getByRole("textbox", { name: "Sessions per connection" })).toHaveValue("10")
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
