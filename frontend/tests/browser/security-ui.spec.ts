import { expect, test, type Page, type Route } from "@playwright/test"

/**
 * The Security section against a mocked API: every page renders its readings
 * from the shapes the backend sends, the joins between pages hold (an address
 * in one list is a block, a ban or a lookup in another), and the design
 * system's structural rules — no framed block on the page, every icon-only
 * control named, row controls reachable without a pointer — hold on all eight.
 *
 * Nothing here needs a firewall, an sshd or a fail2ban: the checks are about
 * what the page does with the answers, which is the part a Go test cannot see.
 */

const now = new Date()
const iso = (minutesAgo: number) => new Date(now.getTime() - minutesAgo * 60_000).toISOString()

const admin = {
  authenticated: true,
  needsTotp: false,
  needsEnrollment: false,
  require2fa: false,
  capabilities: ["read", "service.control", "file.write", "terminal", "destructive", "system.admin"],
  user: {
    id: 1,
    username: "operator",
    role: "admin",
    totpEnabled: true,
    disabled: false,
    mustChangePassword: false,
    lastLoginAt: iso(60),
    createdAt: iso(60 * 24 * 30),
  },
}

const exposure = {
  grade: "tailscale",
  summary: "Reachable only from your tailnet. Nothing is exposed to the internet.",
  allowlist: ["100.64.0.0/10"],
  interfaces: ["tailscale0"],
  tailscaleIp: "100.110.34.31",
  client: "100.110.34.9",
}

const posture = {
  status: "warning",
  checkedAt: iso(2),
  checks: 7,
  skipped: ["security updates"],
  findings: [
    {
      id: "ssh.password-auth",
      level: "warning",
      area: "ssh",
      title: "SSH accepts passwords",
      detail: "PasswordAuthentication is yes, and 1 account(s) already have an authorized key.",
      advice: "Turn it off and use keys.",
      fix: "ssh.passwordauthentication=no",
      fixLabel: "Turn off passwords",
    },
    {
      id: "firewall.logging-off",
      level: "notice",
      area: "firewall",
      title: "The firewall is not logging",
      detail: "ufw logging is off, so refused connections leave no record.",
      advice: "Turn logging on at low.",
    },
  ],
}

const firewall = {
  backend: "ufw",
  available: true,
  enabled: true,
  defaultPolicy: "deny (incoming), allow (outgoing), disabled (routed)",
  policy: { incoming: "deny", outgoing: "allow", routed: "disabled" },
  logging: "off",
  capabilities: {
    editable: true,
    toggle: true,
    defaultPolicy: true,
    logging: true,
    reset: true,
    profiles: true,
  },
  rules: [
    { number: 1, action: "ALLOW", direction: "IN", to: "22/tcp", from: "Anywhere", port: "22", protocol: "tcp", service: "SSH", raw: "22/tcp ALLOW IN Anywhere" },
    { number: 2, action: "ALLOW", direction: "IN", to: "443/tcp", from: "Anywhere", port: "443", protocol: "tcp", service: "HTTPS", raw: "443/tcp ALLOW IN Anywhere" },
    { number: 3, action: "DENY", direction: "IN", to: "Anywhere", from: "203.0.113.9", comment: "repeat offender", raw: "Anywhere DENY IN 203.0.113.9 # repeat offender" },
    { number: 4, action: "ALLOW", direction: "IN", to: "22/tcp", from: "Anywhere", port: "22", protocol: "tcp", ipv6: true, raw: "22/tcp (v6) ALLOW IN Anywhere (v6)" },
  ],
}

const jails = {
  available: true,
  running: true,
  jails: [
    {
      name: "sshd",
      currentlyFailed: 3,
      totalFailed: 812,
      currentlyBanned: 2,
      totalBanned: 41,
      bannedIps: ["203.0.113.9", "198.51.100.4"],
      fileList: ["/var/log/auth.log"],
    },
    {
      name: "nginx-http-auth",
      currentlyFailed: 0,
      totalFailed: 0,
      currentlyBanned: 0,
      totalBanned: 0,
      bannedIps: [],
      fileList: ["/var/log/nginx/error.log"],
    },
  ],
}

const offenders = {
  total: 60,
  bans: 41,
  unbans: 19,
  offenders: [
    { ip: "203.0.113.9", bans: 11, jails: ["sshd"], first: iso(60 * 24 * 6), last: iso(30) },
    { ip: "198.51.100.4", bans: 2, jails: ["sshd"], first: iso(60 * 5), last: iso(90) },
  ],
  byJail: { sshd: 41 },
  perDay: [
    { day: "2026-09-15", count: 12 },
    { day: "2026-09-16", count: 20 },
    { day: "2026-09-17", count: 9 },
  ],
  since: iso(60 * 24 * 6),
}

const history = [
  { action: "ban", jail: "sshd", ip: "203.0.113.9", at: iso(30) },
  { action: "unban", jail: "sshd", ip: "192.0.2.77", at: iso(45) },
]

const connections = {
  total: 14,
  listening: 6,
  loopback: 4,
  peers: [
    { address: "203.0.113.50", count: 3, established: 3, ports: [443], processes: ["caddy"], private: false, service: "HTTPS" },
    { address: "100.110.34.9", count: 2, established: 1, ports: [8443], processes: ["caddy"], private: true },
  ],
}

const sessions = [
  { user: "ubuntu", tty: "pts/0", from: "100.110.34.9", loginTime: iso(40), idle: "active", pid: 4242, isSsh: true },
]

const logins = [
  { kind: "login", user: "ubuntu", tty: "pts/0", from: "100.110.34.9", loginTime: iso(40), active: true },
  { kind: "boot", user: "reboot", tty: "", from: "6.14.0-37-generic", loginTime: iso(60 * 24), duration: "1 day" },
]

const attackers = {
  attempts: 2312,
  addresses: 2,
  windowHours: 168,
  capped: false,
  since: iso(60 * 24 * 6),
  attackers: [
    { address: "203.0.113.9", attempts: 2200, users: ["root", "admin", "ubuntu"], first: iso(60 * 24 * 6), last: iso(12) },
    { address: "198.51.100.4", attempts: 112, users: ["deploy"], first: iso(60 * 8), last: iso(70) },
  ],
}

const network = {
  interfaces: [
    { name: "eth0", addresses: ["203.0.113.20/24"], mtu: 1500, up: true, loopback: false, kind: "physical", bytesSent: 1e9, bytesRecv: 4e9, public: true },
    { name: "tailscale0", addresses: ["100.110.34.31/32"], mtu: 1280, up: true, loopback: false, kind: "tunnel", bytesSent: 1e7, bytesRecv: 2e7, public: false },
    { name: "docker0", addresses: ["172.17.0.1/16"], mtu: 1500, up: true, loopback: false, kind: "bridge", bytesSent: 0, bytesRecv: 0, public: false },
    { name: "veth1a2b", addresses: [], mtu: 1500, up: true, loopback: false, kind: "virtual", bytesSent: 0, bytesRecv: 0, public: false },
  ],
  routes: [
    { destination: "default", gateway: "203.0.113.1", interface: "eth0", metric: "100", family: "ipv4", raw: "default via 203.0.113.1 dev eth0 metric 100" },
    { destination: "172.17.0.0/16", interface: "docker0", family: "ipv4", raw: "172.17.0.0/16 dev docker0" },
  ],
  resolvers: ["127.0.0.53"],
  search: ["example.internal"],
}

const sshd = {
  available: true,
  source: "/usr/sbin/sshd -T",
  ports: ["22"],
  managedFile: "/etc/ssh/sshd_config.d/99-just-dashboard.conf",
  keyedAccounts: [{ user: "ubuntu", keys: 2 }],
  hasMatchBlocks: false,
  socket: { unit: "ssh.socket", ports: ["22"] },
  settings: [
    { key: "permitrootlogin", label: "Root login", value: "prohibit-password", recommended: "prohibit-password", secure: true, detail: "Whether root may log in over SSH at all.", options: ["no", "prohibit-password", "forced-commands-only", "yes"], kind: "choice" },
    { key: "passwordauthentication", label: "Password authentication", value: "yes", recommended: "no", secure: false, detail: "Whether a password alone is enough to get a shell.", risk: "With this on, your server's security is whatever the weakest password on it is.", options: ["no", "yes"], kind: "choice" },
    { key: "maxauthtries", label: "Attempts per connection", value: "6", recommended: "3 or fewer", secure: false, detail: "How many guesses one connection gets.", risk: "Six passwords per handshake instead of one.", kind: "number" },
    { key: "port", label: "Port", value: "22", recommended: "any", secure: true, detail: "Where sshd listens.", kind: "number" },
    { key: "allowusers", label: "Only these accounts may log in", value: "", recommended: "", secure: true, detail: "A space-separated list.", kind: "list" },
  ],
}

const services = [
  { key: "ssh", name: "SSH", port: "22", protocol: "tcp", detail: "Remote shell." },
  { key: "redis", name: "Redis", port: "6379", protocol: "tcp", detail: "Cache.", danger: "Never open this to the world." },
]

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
}

type Mutation = { method: string; path: string; body: unknown }

/** The whole Security API, answered from the fixtures above; every write is recorded. */
async function mockSecurity(page: Page, mutations: Mutation[] = []) {
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    if (request.method() !== "GET") {
      mutations.push({ method: request.method(), path, body: request.postDataJSON() })
      return json(route, { output: "ok" })
    }
    switch (path) {
      case "/auth/session":
        return json(route, admin)
      case "/exposure":
        return json(route, exposure)
      case "/security/posture":
        return json(route, posture)
      case "/security/services":
        return json(route, services)
      case "/firewall/":
        return json(route, firewall)
      case "/firewall/apps":
        return json(route, [{ name: "OpenSSH", ports: ["22/tcp"] }])
      case "/fail2ban/":
        return json(route, jails)
      case "/fail2ban/offenders":
        return json(route, offenders)
      case "/fail2ban/history":
        return json(route, history)
      case "/fail2ban/sshd/config":
        return json(route, { name: "sshd", banTime: 600, findTime: 600, maxRetry: 5, ignoreIp: ["127.0.0.0/8"], actions: ["iptables-multiport"] })
      case "/connections":
        return json(route, connections)
      case "/ssh-sessions":
        return json(route, sessions)
      case "/logins":
        return json(route, logins)
      case "/logins/failed":
        return json(route, [])
      case "/logins/attackers":
        return json(route, attackers)
      case "/network":
        return json(route, network)
      case "/ssh/config":
        return json(route, sshd)
      case "/jobs":
      case "/jobs/":
        return json(route, [])
      case "/updates/self":
        return json(route, { current: "0.6.7", latest: "0.6.7" })
      default:
        return json(route, [])
    }
  })
}

const PAGES = [
  "/security",
  "/security/firewall",
  "/security/ssh",
  "/security/intrusion",
  "/security/connections",
  "/security/logins",
  "/security/network",
  "/security/tools",
] as const

/**
 * §2: the only block on a page that may draw a frame is a table. A grid owns a
 * scroll region, and an edge is what says where it ends — a row whose actions
 * sit past the boundary otherwise reads as a row with no actions. Everything
 * else in the page's flow stays plain, which is what the frame is read against.
 *
 * Asserted structurally rather than as a count, so the rule keeps holding as
 * pages gain and lose tables.
 */
async function framedNonTables(page: Page) {
  return page.evaluate(() =>
    Array.from(
      document.querySelectorAll("[data-slot=page] [data-slot=panel]:not([data-plain])"),
    )
      .filter((el) => !el.querySelector("[data-slot=table-container]"))
      .map((el) => el.outerHTML.slice(0, 120)),
  )
}

test("the overview reads as readings, findings and how the panel is reached", async ({ page }) => {
  await mockSecurity(page)
  await page.goto("/security")
  await page.waitForLoadState("networkidle")

  // The facts row: the grade, the allowlist and the address this browser came from.
  await expect(page.getByText("Tailscale only")).toBeVisible()
  await expect(page.getByText("100.110.34.9", { exact: true })).toBeVisible()

  // Five tiles, each a destination with a figure read from its own poll.
  // Scoped to the grid: the sidebar and the section strip link to the same
  // pages under the same names, and this is about the tiles.
  const grid = page.locator("[data-slot=stat-grid]")
  const tiles = grid.locator("a")
  await expect(tiles).toHaveCount(5)
  await expect(grid.getByRole("link", { name: "Firewall", exact: true })).toContainText("ufw")
  await expect(grid.getByRole("link", { name: "SSH", exact: true })).toContainText("1 finding")
  await expect(grid.getByRole("link", { name: "Intrusion", exact: true })).toContainText(
    "2 banned now",
  )
  // The figure and its unit are two spans, so the text runs together.
  await expect(grid.getByRole("link", { name: "Connections", exact: true })).toContainText(
    /1\s*from the internet/,
  )
  await expect(grid.getByRole("link", { name: "Logins", exact: true })).toContainText("1 session")

  // The findings, with the remedy the dashboard can carry out as a button.
  // The finding's row, not the SSH tile's hint, which quotes the same title.
  await page.getByRole("button", { name: /SSH accepts passwords/ }).click()
  await expect(page.getByRole("button", { name: "Turn off passwords" })).toBeVisible()
})

for (const path of PAGES) {
  test(`${path} draws no framed block on the page`, async ({ page }) => {
    await mockSecurity(page)
    await page.goto(path)
    await page.waitForLoadState("networkidle")
    // A dialog or sheet may frame itself, and so may a table (§2); any other
    // block in the page's flow may not.
    expect(await framedNonTables(page), `framed non-table blocks on ${path}`).toEqual([])
  })

  test(`every icon-only control on ${path} has an accessible name`, async ({ page }) => {
    await mockSecurity(page)
    await page.goto(path)
    await page.waitForLoadState("networkidle")
    const unnamed = await page.evaluate(() => {
      const bad: string[] = []
      for (const el of document.querySelectorAll<HTMLElement>("button, [role='button']")) {
        if (el.offsetParent === null && el.getAttribute("aria-hidden") !== "true") continue
        if ((el.textContent ?? "").trim().length > 0) continue
        const named =
          el.getAttribute("aria-label") ||
          el.getAttribute("aria-labelledby") ||
          el.querySelector(".sr-only")
        if (!named) bad.push(el.outerHTML.slice(0, 160))
      }
      return bad
    })
    expect(unnamed, `unlabelled icon-only controls on ${path}`).toEqual([])
  })
}

test("the firewall page opens on its defaults and offers the controls that set them", async ({
  page,
}) => {
  await mockSecurity(page)
  await page.goto("/security/firewall")
  await page.waitForLoadState("networkidle")

  const grid = page.locator("[data-slot=stat-grid]")
  await expect(grid.getByText("Rules", { exact: true })).toBeVisible()
  // Three rules, not four: the IPv6 twin is folded away and said so.
  await expect(grid.locator("[data-slot=stat-tile]").first()).toContainText("3")
  await expect(page.getByText("1 IPv6 twin folded away", { exact: true })).toBeVisible()
  await expect(page.getByRole("combobox", { name: "Inbound default" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Add rule" })).toBeVisible()
  await expect(page.getByRole("switch", { name: "Firewall enabled" })).toBeChecked()
  await expect(page.getByRole("row").filter({ hasText: "repeat offender" })).toBeVisible()
})

test("ssh changes are staged as a page state and applied together", async ({ page }) => {
  await mockSecurity(page)
  await page.goto("/security/ssh")
  await page.waitForLoadState("networkidle")

  await expect(page.locator("[data-slot=stat-grid]")).toContainText("Passwords")
  await expect(page.getByText("held by ssh.socket")).toBeVisible()
  await expect(page.getByRole("button", { name: "Test and apply" })).toHaveCount(0)

  await page
    .getByRole("radiogroup", { name: "Password authentication" })
    .getByRole("radio", { name: "no" })
    .click()
  await expect(page.getByText("pending", { exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: "Test and apply" })).toBeVisible()
  await page.getByRole("button", { name: "Discard" }).click()
  await expect(page.getByRole("button", { name: "Test and apply" })).toHaveCount(0)
})

test("a jail's sheet bans an address by hand and the tuning offers the caller's own", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await mockSecurity(page, mutations)
  await page.goto("/security/intrusion")
  await page.waitForLoadState("networkidle")

  await expect(page.locator("[data-slot=stat-grid]")).toContainText("Banned now")
  await page.getByRole("button", { name: "sshd", exact: true }).click()
  await page.getByLabel("Ban an address now").fill("192.0.2.200")
  await page.getByRole("button", { name: "Ban", exact: true }).click()
  await expect
    .poll(() => mutations.find((m) => m.path === "/fail2ban/sshd/ban")?.body)
    .toEqual({ ip: "192.0.2.200" })

  await page.getByRole("button", { name: "Tune", exact: true }).click()
  await page.getByRole("button", { name: "Never ban my address" }).click()
  await expect
    .poll(() => mutations.find((m) => m.path === "/fail2ban/sshd/ignore")?.body)
    .toEqual({ ip: "100.110.34.9", add: true })
})

test("an offender is blocked with a plain deny and looked up on the tools page", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await mockSecurity(page, mutations)
  await page.goto("/security/intrusion")
  await page.waitForLoadState("networkidle")

  const row = page.getByRole("row").filter({ hasText: "203.0.113.9" }).first()
  await row.getByRole("button", { name: "Block at the firewall" }).click()
  await expect
    .poll(() => mutations.find((m) => m.path === "/firewall/rules")?.body)
    .toEqual({ action: "deny", direction: "in", from: "203.0.113.9", comment: "repeat offender" })

  await row.getByRole("button", { name: "More actions" }).click()
  await page.getByRole("menuitem", { name: /Who owns this address/ }).click()
  await expect(page).toHaveURL(/\/security\/tools\?tool=asn&target=203\.0\.113\.9/)
  // Arriving narrows the page to the one tool, with the address filled in.
  await expect(page.getByRole("textbox", { name: "Target" })).toHaveValue("203.0.113.9")
  await expect(page.getByRole("textbox", { name: "Target" })).toHaveCount(1)
  await page.getByRole("button", { name: "Every tool" }).click()
  expect(await page.getByRole("textbox", { name: "Target" }).count()).toBeGreaterThan(10)
})

test("connections offers a block only for an address that is not private", async ({ page }) => {
  await mockSecurity(page)
  await page.goto("/security/connections")
  await page.waitForLoadState("networkidle")

  await expect(page.locator("[data-slot=stat-grid]")).toContainText("1 from the internet")
  const internet = page.getByRole("row").filter({ hasText: "203.0.113.50" })
  await expect(internet.getByRole("button", { name: "Block at the firewall" })).toBeVisible()
  const tailnet = page.getByRole("row").filter({ hasText: "100.110.34.9" })
  await expect(tailnet.getByRole("button", { name: "Block at the firewall" })).toHaveCount(0)
  await expect(tailnet.getByRole("button", { name: "More actions" })).toBeVisible()
})

test("the logins page folds the failed record into attackers", async ({ page }) => {
  const mutations: Mutation[] = []
  await mockSecurity(page, mutations)
  await page.goto("/security/logins")
  await page.waitForLoadState("networkidle")

  await expect(page.locator("[data-slot=stat-grid]")).toContainText("2312")
  const row = page.getByRole("row").filter({ hasText: "203.0.113.9" })
  await expect(row).toContainText("2200")
  await expect(row).toContainText("root")
  await row.getByRole("button", { name: "Block at the firewall" }).click()
  await expect
    .poll(() => mutations.find((m) => m.path === "/firewall/rules")?.body)
    .toEqual({ action: "deny", direction: "in", from: "203.0.113.9", comment: "failed logins" })
})

test("the network page leads with what faces the internet", async ({ page }) => {
  await mockSecurity(page)
  await page.goto("/security/network")
  await page.waitForLoadState("networkidle")

  await expect(page.locator("[data-slot=stat-grid]")).toContainText("eth0")
  await expect(page.getByText("1 on a public address")).toBeVisible()
  // Docker's devices are folded away until asked for.
  await expect(page.getByRole("row").filter({ hasText: "veth1a2b" })).toHaveCount(0)
  await page.getByRole("radio", { name: /Everything/ }).click()
  await expect(page.getByRole("row").filter({ hasText: "veth1a2b" })).toBeVisible()
})

test.describe("with no hover available", () => {
  test.use({ hasTouch: true, viewport: { width: 390, height: 844 } })

  for (const path of ["/security/connections", "/security/logins", "/security/intrusion"] as const) {
    test(`row controls on ${path} are reachable without a pointer`, async ({ page }) => {
      await mockSecurity(page)
      await page.goto(path)
      await page.waitForLoadState("networkidle")
      const hidden = await page.evaluate(() => {
        const bad: string[] = []
        for (const el of document.querySelectorAll<HTMLElement>(
          "[data-slot='table-row'] button, [data-slot='table-row'] a",
        )) {
          if (parseFloat(getComputedStyle(el).opacity) < 0.1) {
            bad.push(el.getAttribute("aria-label") ?? el.outerHTML.slice(0, 120))
          }
        }
        return bad
      })
      expect(hidden, `controls hidden behind hover on ${path}`).toEqual([])
      // The page never scrolls sideways on a phone.
      const overflow = await page.evaluate(
        () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
      )
      expect(overflow, `horizontal overflow on ${path}`).toBeLessThanOrEqual(0)
    })
  }
})

/**
 * Review screenshots, for the eyes the checks above do not have. Written only
 * when asked for, into the directory named — never into the tree.
 */
test.describe("screenshots", () => {
  const dir = process.env.JD_SECURITY_SHOTS
  test.skip(!dir, "set JD_SECURITY_SHOTS to a directory to capture them")

  for (const width of [1280, 1720]) {
    test.describe(`${width} wide`, () => {
      test.use({ viewport: { width, height: 1000 } })
      for (const path of PAGES) {
        test(`${path} at ${width}`, async ({ page }) => {
          await mockSecurity(page)
          await page.goto(path)
          await page.waitForLoadState("networkidle")
          await page.waitForTimeout(400)
          const name = path.replace(/^\/security\/?/, "") || "overview"
          await page.screenshot({ path: `${dir}/${name}-${width}.png`, fullPage: true })
        })
      }
    })
  }
})
