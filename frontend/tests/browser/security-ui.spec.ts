import { expect, test, type Page, type Route } from "@playwright/test"
import { HOST_LOG_SOURCES, mockHostLogs } from "./host-logs-fixture"
import { loaded } from "./network-fixture"

/**
 * The Security section against a mocked API: every page renders its readings
 * from the shapes the backend sends, the joins between pages hold (an address
 * in one list is a block, a ban or a lookup in another), and the design
 * system's structural rules — framed tables and workspaces, every icon-only
 * control named, row controls reachable without a pointer — hold on all eight.
 * SSH, Firewall and Intrusion each read their service's log in place, through
 * its lens, with the day's counts in the page's one grid; the host's logs are
 * `host-logs-fixture.ts`'s.
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
    {
      number: 1,
      action: "ALLOW",
      direction: "IN",
      to: "22/tcp",
      from: "Anywhere",
      port: "22",
      protocol: "tcp",
      service: "SSH",
      raw: "22/tcp ALLOW IN Anywhere",
    },
    {
      number: 2,
      action: "ALLOW",
      direction: "IN",
      to: "443/tcp",
      from: "Anywhere",
      port: "443",
      protocol: "tcp",
      service: "HTTPS",
      raw: "443/tcp ALLOW IN Anywhere",
    },
    {
      number: 3,
      action: "DENY",
      direction: "IN",
      to: "Anywhere",
      from: "203.0.113.9",
      comment: "repeat offender",
      raw: "Anywhere DENY IN 203.0.113.9 # repeat offender",
    },
    {
      number: 4,
      action: "ALLOW",
      direction: "IN",
      to: "22/tcp",
      from: "Anywhere",
      port: "22",
      protocol: "tcp",
      ipv6: true,
      raw: "22/tcp (v6) ALLOW IN Anywhere (v6)",
    },
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

const connections = {
  total: 14,
  listening: 6,
  loopback: 4,
  peers: [
    {
      address: "203.0.113.50",
      count: 3,
      established: 3,
      ports: [443],
      processes: ["caddy"],
      private: false,
      service: "HTTPS",
    },
    {
      address: "100.110.34.9",
      count: 2,
      established: 1,
      ports: [8443],
      processes: ["caddy"],
      private: true,
    },
  ],
}

const sessions = [
  {
    user: "ubuntu",
    tty: "pts/0",
    from: "100.110.34.9",
    loginTime: iso(40),
    idle: "active",
    pid: 4242,
    isSsh: true,
  },
]

const logins = [
  {
    kind: "login",
    user: "ubuntu",
    tty: "pts/0",
    from: "100.110.34.9",
    loginTime: iso(40),
    active: true,
  },
  {
    kind: "boot",
    user: "reboot",
    tty: "",
    from: "6.14.0-37-generic",
    loginTime: iso(60 * 24),
    duration: "1 day",
  },
]

const attackers = {
  attempts: 2312,
  addresses: 2,
  windowHours: 168,
  capped: false,
  since: iso(60 * 24 * 6),
  attackers: [
    {
      address: "203.0.113.9",
      attempts: 2200,
      users: ["root", "admin", "ubuntu"],
      first: iso(60 * 24 * 6),
      last: iso(12),
    },
    {
      address: "198.51.100.4",
      attempts: 112,
      users: ["deploy"],
      first: iso(60 * 8),
      last: iso(70),
    },
  ],
}

const network = {
  interfaces: [
    {
      name: "eth0",
      addresses: ["203.0.113.20/24"],
      mtu: 1500,
      up: true,
      loopback: false,
      kind: "physical",
      bytesSent: 1e9,
      bytesRecv: 4e9,
      public: true,
    },
    {
      name: "tailscale0",
      addresses: ["100.110.34.31/32"],
      mtu: 1280,
      up: true,
      loopback: false,
      kind: "tunnel",
      bytesSent: 1e7,
      bytesRecv: 2e7,
      public: false,
    },
    {
      name: "docker0",
      addresses: ["172.17.0.1/16"],
      mtu: 1500,
      up: true,
      loopback: false,
      kind: "bridge",
      bytesSent: 0,
      bytesRecv: 0,
      public: false,
    },
    {
      name: "veth1a2b",
      addresses: [],
      mtu: 1500,
      up: true,
      loopback: false,
      kind: "virtual",
      bytesSent: 0,
      bytesRecv: 0,
      public: false,
    },
  ],
  routes: [
    {
      destination: "default",
      gateway: "203.0.113.1",
      interface: "eth0",
      metric: "100",
      family: "ipv4",
      raw: "default via 203.0.113.1 dev eth0 metric 100",
    },
    {
      destination: "172.17.0.0/16",
      interface: "docker0",
      family: "ipv4",
      raw: "172.17.0.0/16 dev docker0",
    },
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
    {
      key: "permitrootlogin",
      label: "Root login",
      value: "prohibit-password",
      recommended: "prohibit-password",
      secure: true,
      detail: "Whether root may log in over SSH at all.",
      options: ["no", "prohibit-password", "forced-commands-only", "yes"],
      kind: "choice",
    },
    {
      key: "passwordauthentication",
      label: "Password authentication",
      value: "yes",
      recommended: "no",
      secure: false,
      detail: "Whether a password alone is enough to get a shell.",
      risk: "With this on, your server's security is whatever the weakest password on it is.",
      options: ["no", "yes"],
      kind: "choice",
    },
    {
      key: "maxauthtries",
      label: "Attempts per connection",
      value: "6",
      recommended: "3 or fewer",
      secure: false,
      detail: "How many guesses one connection gets.",
      risk: "Six passwords per handshake instead of one.",
      kind: "number",
    },
    {
      key: "port",
      label: "Port",
      value: "22",
      recommended: "any",
      secure: true,
      detail: "Where sshd listens.",
      kind: "number",
    },
    {
      key: "allowusers",
      label: "Only these accounts may log in",
      value: "",
      recommended: "",
      secure: true,
      detail: "A space-separated list.",
      kind: "list",
    },
  ],
}

const services = [
  { key: "ssh", name: "SSH", port: "22", protocol: "tcp", detail: "Remote shell." },
  {
    key: "redis",
    name: "Redis",
    port: "6379",
    protocol: "tcp",
    detail: "Cache.",
    danger: "Never open this to the world.",
  },
]

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
}

type Mutation = { method: string; path: string; body: unknown }

/** An account that may read the section and change nothing in it. */
const readonly = {
  ...admin,
  capabilities: ["read"],
  user: { ...admin.user, username: "viewer", role: "readonly" },
}

/**
 * The whole Security API, answered from the fixtures above; every write is
 * recorded. The session, the firewall's status and the host's log files can
 * be swapped for the case under test, and any route answered with a body of
 * its own; what the logs routes were asked is returned.
 */
async function mockSecurity(
  page: Page,
  mutations: Mutation[] = [],
  options: {
    session?: typeof admin
    firewall?: typeof firewall
    sources?: typeof HOST_LOG_SOURCES
    outside?: string[]
    overrides?: Record<string, unknown>
  } = {},
) {
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    if (request.method() !== "GET") {
      mutations.push({ method: request.method(), path, body: request.postDataJSON() })
      return json(route, { output: "ok" })
    }
    const overrides = options.overrides ?? {}
    if (path in overrides) return json(route, overrides[path])
    switch (path) {
      case "/auth/session":
        return json(route, options.session ?? admin)
      case "/exposure":
        return json(route, exposure)
      case "/security/posture":
        return json(route, posture)
      case "/security/services":
        return json(route, services)
      case "/firewall/":
        return json(route, options.firewall ?? firewall)
      case "/firewall/apps":
        return json(route, [{ name: "OpenSSH", ports: ["22/tcp"] }])
      case "/fail2ban/":
        return json(route, jails)
      case "/fail2ban/offenders":
        return json(route, offenders)
      case "/fail2ban/sshd/config":
        return json(route, {
          name: "sshd",
          banTime: 600,
          findTime: 600,
          maxRetry: 5,
          ignoreIp: ["127.0.0.0/8"],
          actions: ["iptables-multiport"],
        })
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
  return mockHostLogs(page, { sources: options.sources, outside: options.outside })
}

const PAGES = [
  "/security",
  "/network/firewall",
  "/security/ssh",
  "/security/intrusion",
  "/network/connections",
  "/security/logins",
  "/network/tools",
] as const

/**
 * §2: a Panel frames a table's scroll region, so a row's offscreen actions
 * remain visibly contained. Findings and forms stay plain; pictures and the
 * Tools panes own their separate edges under §7.
 *
 * Asserted structurally rather than as a count, so the rule keeps holding as
 * pages gain and lose tables.
 */
async function framedNonTables(page: Page) {
  return page.evaluate(() =>
    Array.from(document.querySelectorAll("[data-slot=page] [data-slot=panel]:not([data-plain])"))
      .filter((el) => !el.querySelector("[data-slot=table-container]"))
      .map((el) => el.outerHTML.slice(0, 120)),
  )
}

test("the overview reads as readings, findings and how the panel is reached", async ({ page }) => {
  await mockSecurity(page)
  await page.goto("/security")
  await loaded(page)

  // The identity line: the way in drawn as itself, the grade, the allowlist,
  // the address this browser came from, and the posture's verdict at its end.
  const identity = page.locator("[data-slot=host-identity]")
  // Twice: the way in on the tile, and the network this browser is on.
  await expect(identity.locator('img[src="/logos/tailscale.svg"]')).toHaveCount(2)
  await expect(identity.getByText("Tailscale only")).toBeVisible()
  await expect(identity.getByText("100.110.34.9", { exact: true })).toBeVisible()
  await expect(identity.getByText("Needs attention")).toBeVisible()
  await expect(identity).toContainText("100.64.0.0/10")

  // The seven checks the verdict is made of: a warning on SSH, a notice on
  // the firewall, security updates not run, and the rest passed.
  const checks = page.getByRole("region", { name: "Checks" })
  await expect(checks).toContainText("4 of 7 checks passed")
  await expect(checks.getByRole("button", { name: "SSH: 1 warning" })).toBeEnabled()
  await expect(checks.getByRole("button", { name: "Updates: Not checked" })).toBeDisabled()
  await expect(checks.getByRole("button", { name: "Exposure: Passed" })).toBeDisabled()

  // Five tiles, each a destination with a figure read from its own poll.
  // Scoped to the grid: the sidebar and the section strip link to the same
  // pages under the same names, and this is about the tiles.
  const grid = page.locator("[data-slot=stat-grid]")
  const tiles = grid.locator("a")
  await expect(tiles).toHaveCount(5)
  // One width and one inset for all five, the first included: the first column
  // used to give up its left padding, which put the hover wash flush against
  // its name.
  const cells = await tiles.evaluateAll((links) =>
    links.map((a) => {
      const tile = a.querySelector("[data-slot=stat-tile]")!
      return `${Math.round(a.getBoundingClientRect().width)} ${getComputedStyle(tile).paddingLeft}`
    }),
  )
  expect(new Set(cells).size, cells.join(" | ")).toBe(1)
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

  // The ways onto the machine: the internet's through the firewall, fail2ban
  // and sshd, and this browser's through the allowlist.
  const ways = page.getByRole("list", { name: "The ways onto this machine" })
  await expect(ways).toContainText("ufw")
  await expect(ways).toContainText("deny inbound")
  await expect(ways).toContainText(":22")
  await expect(ways).toContainText(":443")
  await expect(ways).toContainText("2 banned now")
  await expect(ways).toContainText("SSH accepts passwords")
  await expect(ways).toContainText("Tailscale only")
  await expect(ways.locator('img[src="/logos/fail2ban.webp"]')).toHaveCount(1)

  // A check's segment narrows the findings to its area, and lets it go.
  await expect(page.getByRole("button", { name: /The firewall is not logging/ })).toBeVisible()
  await checks.getByRole("button", { name: "SSH: 1 warning" }).click()
  await expect(checks.getByRole("button", { name: "SSH: 1 warning" })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await expect(page.getByRole("button", { name: /The firewall is not logging/ })).toHaveCount(0)
  await page.getByRole("button", { name: "Show every area" }).click()
  await expect(page.getByRole("button", { name: /The firewall is not logging/ })).toBeVisible()

  // The findings, with the remedy the dashboard can carry out as a button.
  // The finding's row, not the SSH tile's hint, which quotes the same title.
  await page.getByRole("button", { name: /SSH accepts passwords/ }).click()
  await expect(page.getByRole("button", { name: "Turn off passwords" })).toBeVisible()
})

for (const path of PAGES) {
  test(`${path} keeps non-table Panels plain`, async ({ page }) => {
    await mockSecurity(page)
    await page.goto(path)
    await loaded(page)
    // A dialog or sheet may frame itself, and so may a table (§2); any other
    // block in the page's flow may not.
    expect(await framedNonTables(page), `framed non-table blocks on ${path}`).toEqual([])
  })

  test(`every icon-only control on ${path} has an accessible name`, async ({ page }) => {
    await mockSecurity(page)
    await page.goto(path)
    await loaded(page)
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
  await page.goto("/network/firewall")
  await loaded(page)

  const grid = page.locator("[data-slot=stat-grid]")
  await expect(grid.getByText("Rules", { exact: true })).toBeVisible()
  // Three rules, not four: the IPv6 twin is folded away and said so.
  await expect(grid.locator("[data-slot=stat-tile]").first()).toContainText("3")
  await expect(page.getByText("1 IPv6 twin folded away", { exact: true })).toBeVisible()
  await expect(page.getByRole("combobox", { name: "Inbound default" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Add rule" })).toBeVisible()
  await expect(page.getByRole("switch", { name: "Firewall enabled" })).toBeChecked()
  await expect(page.getByRole("row").filter({ hasText: "repeat offender" })).toBeVisible()

  // The rules folded by where they lead: each port admitted, and the default
  // for everything else. The rule list ends on that default as well.
  const inbound = page.getByRole("list", { name: "How a connection from outside is answered" })
  await expect(inbound).toContainText("SSH")
  await expect(inbound).toContainText(":22")
  await expect(inbound).toContainText("HTTPS")
  await expect(inbound).toContainText("1 address denied by name")
  await expect(inbound).toContainText("deny by default")
  await expect(page.getByRole("row").last()).toContainText("Everything else")
  await page.getByRole("textbox", { name: "Filter rules" }).fill("offender")
  await expect(page.getByRole("row").filter({ hasText: "Everything else" })).toHaveCount(0)
})

test("ssh changes are staged as a page state and applied together", async ({ page }) => {
  await mockSecurity(page)
  await page.goto("/security/ssh")
  await loaded(page)

  // The doors a login can take, drawn from the draft.
  const doors = page.getByRole("list", { name: "How a login reaches a shell" })
  await expect(doors).toContainText(":22")
  await expect(doors).toContainText("accepted — a guessed one is a shell")
  await expect(doors).toContainText("keys only")
  await expect(doors).toContainText("2 keys")
  await expect(page.getByText("held by ssh.socket")).toBeVisible()
  await expect(page.getByRole("button", { name: "Test and apply" })).toHaveCount(0)

  await page
    .getByRole("radiogroup", { name: "Password authentication" })
    .getByRole("radio", { name: "no" })
    .click()
  await expect(page.getByText("edited", { exact: true })).toBeVisible()
  await expect(doors.getByText("accepted — a guessed one is a shell")).toHaveCount(0)
  // The section holding it says so, and the apply bar names what changed.
  await expect(page.getByText("Edited", { exact: true }).first()).toBeVisible()
  await expect(page.getByText("1 unsaved change", { exact: true })).toBeVisible()
  await page.getByRole("button", { name: /^Edited/ }).click()
  await expect(page.getByRole("radiogroup", { name: "Password authentication" })).toBeVisible()
  await expect(page.getByLabel("Attempts per connection")).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Test and apply" })).toBeVisible()
  await page.getByRole("button", { name: "Discard" }).click()
  await expect(page.getByRole("button", { name: "Test and apply" })).toHaveCount(0)
  await expect(doors).toContainText("accepted — a guessed one is a shell")
})

test("a jail's sheet bans an address by hand and the tuning offers the caller's own", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await mockSecurity(page, mutations)
  await page.goto("/security/intrusion")
  await loaded(page)

  await expect(page.locator("[data-slot=stat-grid]")).toContainText("Banned now")
  // fail2ban as its own mark, and each jail a card that opens its sheet.
  await expect(
    page.locator("[data-slot=host-identity]").locator('img[src="/logos/fail2ban.webp"]'),
  ).toHaveCount(1)
  const jail = page.locator("[data-slot=choice-row]").filter({ hasText: "sshd" })
  await expect(jail).toHaveCount(1)
  await expect(jail).toContainText("banned now")
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
  await loaded(page)

  const row = page.getByRole("row").filter({ hasText: "203.0.113.9" }).first()
  await row.getByRole("button", { name: "Block at the firewall" }).click()
  await expect
    .poll(() => mutations.find((m) => m.path === "/firewall/rules")?.body)
    .toEqual({ action: "deny", direction: "in", from: "203.0.113.9", comment: "repeat offender" })

  await row.getByRole("button", { name: "More actions" }).click()
  await page.getByRole("menuitem", { name: /Who owns this address/ }).click()
  await expect(page).toHaveURL(/\/network\/tools\?tool=asn&target=203\.0\.113\.9/)
  // Arriving narrows the page to the one tool, with the address filled in.
  await expect(page.getByRole("textbox", { name: "Target" })).toHaveValue("203.0.113.9")
  await expect(page.getByRole("textbox", { name: "Target" })).toHaveCount(1)
  await page.getByRole("button", { name: "Every tool" }).click()
  await page.getByRole("button", { name: "DNS", exact: true }).click()
  await expect(page.getByRole("textbox", { name: "Target" })).toHaveCount(1)
  await expect(page.getByRole("textbox", { name: "Target" })).toBeEmpty()
  await page.getByRole("button", { name: "Ownership", exact: true }).click()
  await expect(page.getByRole("textbox", { name: "Target" })).toHaveValue("203.0.113.9")
})

test("connections offers a block only for an address that is not private", async ({ page }) => {
  await mockSecurity(page)
  await page.goto("/network/connections")
  await loaded(page)

  await expect(tile(page, "From the internet")).toContainText(/From the internet1\D/)
  const internet = page.getByRole("row").filter({ hasText: "203.0.113.50" })
  await expect(internet.getByRole("button", { name: "Block at the firewall" })).toBeVisible()
  const tailnet = page.getByRole("row").filter({ hasText: "100.110.34.9" })
  await expect(tailnet.getByRole("button", { name: "Block at the firewall" })).toHaveCount(0)
  await expect(tailnet.getByRole("button", { name: "More actions" })).toBeVisible()
  // The address drawn as the network it is on, and the process as its product.
  await expect(tailnet.locator('img[src="/logos/tailscale.svg"]')).toHaveCount(1)
  await expect(internet.locator('img[src="/logos/tailscale.svg"]')).toHaveCount(0)
  await expect(internet.locator('img[src="/logos/caddy.svg"]')).toHaveCount(1)
})

test("the logins page folds the failed record into attackers", async ({ page }) => {
  const mutations: Mutation[] = []
  await mockSecurity(page, mutations)
  await page.goto("/security/logins")
  await loaded(page)

  await expect(page.locator("[data-slot=stat-grid]")).toContainText("2312")
  const row = page.getByRole("row").filter({ hasText: "203.0.113.9" })
  await expect(row).toContainText("2200")
  await expect(row).toContainText("root")
  await row.getByRole("button", { name: "Block at the firewall" }).click()
  await expect
    .poll(() => mutations.find((m) => m.path === "/firewall/rules")?.body)
    .toEqual({ action: "deny", direction: "in", from: "203.0.113.9", comment: "failed logins" })
})

/** A figure in the page's one grid, by its name. */
function tile(page: Page, label: string) {
  return page
    .locator("[data-slot=stat-grid] [data-slot=stat-tile]")
    .filter({ has: page.getByText(label, { exact: true }) })
}

test("the ssh page reads its auth log through the lens, the day's counts in its one grid", async ({
  page,
}) => {
  const logs = await mockSecurity(page)
  await page.goto("/security/ssh")
  await loaded(page)

  // One grid (§15): what the log says the last day made of the doors drawn
  // above it, whose first line carries the same day's failures.
  await expect(page.locator("[data-slot=stat-grid]")).toHaveCount(1)
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(4)
  await expect(page.getByRole("list", { name: "How a login reaches a shell" })).toContainText(
    "8 failed attempts today",
  )
  await expect(tile(page, "Accepted logins")).toContainText("2")
  // Wrong passwords, unknown accounts and a connection that ran out of tries.
  await expect(tile(page, "Failed attempts")).toContainText("8")
  await expect(tile(page, "Invalid users")).toContainText("2")
  await expect(tile(page, "Attackers")).toContainText("3")

  // The lines as what they record, the lens's noise hidden as a chip.
  const lines = page.getByLabel("Log lines")
  await expect(lines.getByText("invalid user", { exact: true }).first()).toBeVisible()
  await expect(lines.getByText("too many attempts", { exact: true })).toBeVisible()
  await expect(
    page.getByRole("button", { name: "Clear the scans and cron sessions hidden filter" }),
  ).toBeVisible()
  await expect(lines.getByText(/203\.0\.113\.250/)).toHaveCount(0)
  const socket = logs.sockets.at(-1)!
  expect(socket.get("source")).toBe("file:/var/log/auth.log")
  // auth.log is the auth lens's on the server too, and the page hands the
  // pane the server's own description of it: no lens of its own, nothing
  // under More, and the file is not asked after a second time.
  expect(socket.has("lens")).toBe(false)
  await expect(page.getByRole("button", { name: "More", exact: true })).toBeVisible()
  expect(socket.getAll("f")).toEqual(["event:!cron_session", "event:!ssh_scan"])
  // Found by asking after the two files, not by listing everything the
  // logs page could open.
  expect(logs.requests).not.toContain("/logs/sources")
  expect(logs.requests.filter((path) => path === "/logs/source")).toHaveLength(2)

  // The grid's readings are the last day's, through the auth lens.
  const readings = logs.searches.filter((search) => search.get("lens") === "auth")
  expect(readings.length).toBeGreaterThan(0)
  for (const search of readings) {
    expect(search.get("source")).toBe("file:/var/log/auth.log")
    const since = Date.parse(search.get("since")!)
    expect(Math.abs(Date.now() - since - 24 * 3_600_000)).toBeLessThan(5 * 60_000)
  }

  // A quick view is the server's question, not a filter over what arrived.
  await page.getByRole("button", { name: /^Failed\b/ }).click()
  await expect
    .poll(() => logs.sockets.at(-1)?.getAll("f"))
    .toEqual(["event:ssh_failed", "event:ssh_invalid_user", "event:ssh_max_attempts"])
  await expect(lines.getByText("sudo", { exact: true })).toHaveCount(0)

  // A figure in the page's grid is a press away from the lines it counts,
  // asked of the pane further down.
  await page.getByRole("button", { name: "Show the lines behind accepted logins" }).click()
  await expect.poll(() => logs.sockets.at(-1)?.getAll("f")).toEqual(["event:ssh_accepted"])
  await expect(page.getByRole("button", { name: /^Accepted\b/ })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
})

test("an attacker in the auth log is blocked from its line; a tailnet address is not", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await mockSecurity(page, mutations)
  await page.goto("/security/ssh")
  const lines = page.getByLabel("Log lines")

  await lines.getByText(/Failed password for root from 203\.0\.113\.197 port 9051/).click()
  await page.getByRole("button", { name: "Block at the firewall" }).click()
  await expect
    .poll(() => mutations.find((m) => m.path === "/firewall/rules")?.body)
    .toEqual({
      action: "deny",
      direction: "in",
      from: "203.0.113.197",
      comment: "blocked from the auth log",
    })

  // The operator's own session over the tailnet: nothing at the edge to block.
  await lines.getByText(/Accepted password for ubuntu from 100\.64\.12\.7/).click()
  await expect(page.getByRole("button", { name: "Only this event" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Block at the firewall" })).toHaveCount(0)

  // A login from a public address is somebody with a key — a deploy, the
  // operator at home — and one press would lock them out: its address can be
  // looked up, not blocked.
  await lines.getByText(/Accepted publickey for deploy from 198\.51\.100\.20/).click()
  await expect(page.getByRole("button", { name: "Only this event" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Block at the firewall" })).toHaveCount(0)
  await page.getByRole("button", { name: "More actions for this line" }).click()
  await expect(page.getByRole("menuitem", { name: "Who owns this address" })).toBeVisible()
})

test("without the admin capability the ssh page says so and reads no auth data", async ({
  page,
}) => {
  const logs = await mockSecurity(page, [], { session: readonly })
  await page.goto("/security/ssh")
  await loaded(page)

  await expect(page.getByText("SSH needs the admin capability")).toBeVisible()
  await expect(page.getByLabel("Log lines")).toHaveCount(0)
  // Not asked and refused: not asked. The server refuses auth data to this
  // account too (`handlers_logs.go`), but the page never makes it say so.
  expect(logs.requests).toEqual([])
})

test("a host with no auth.log reads sshd and sudo through the journal", async ({ page }) => {
  const logs = await mockSecurity(page, [], {
    sources: HOST_LOG_SOURCES.filter((s) => s.id !== "file:/var/log/auth.log"),
  })
  await page.goto("/security/ssh")
  await expect(
    page.getByLabel("Log lines").getByText("invalid user", { exact: true }).first(),
  ).toBeVisible()
  expect(logs.sockets.at(-1)!.get("source")).toBe(
    "journal-id:sshd,sshd-session,sshd-auth,sudo,su,systemd-logind",
  )
  await expect(page.getByText(/This host keeps no auth\.log/)).toBeVisible()
})

test("an auth.log outside the log roots is said to be, not said to be missing", async ({
  page,
}) => {
  const logs = await mockSecurity(page, [], {
    sources: HOST_LOG_SOURCES.filter((s) => s.id !== "file:/var/log/auth.log"),
    outside: ["/var/log/auth.log"],
  })
  await page.goto("/security/ssh")
  await expect(
    page.getByLabel("Log lines").getByText("invalid user", { exact: true }).first(),
  ).toBeVisible()
  expect(logs.sockets.at(-1)!.get("source")).toMatch(/^journal-id:sshd,/)
  await expect(page.getByText(/auth\.log is outside JD_LOG_ROOTS/)).toBeVisible()
  await expect(page.getByText(/keeps no auth\.log/)).toHaveCount(0)
})

test("the firewall page reads what its rules did, with the day's counts in its grid", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  const logs = await mockSecurity(page, mutations, {
    firewall: { ...firewall, logging: "on (low)" },
  })
  await page.goto("/network/firewall")
  await loaded(page)

  await expect(page.locator("[data-slot=stat-grid]")).toHaveCount(1)
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(7)
  await expect(tile(page, "Blocked")).toContainText("5")
  await expect(tile(page, "Sources")).toContainText("2")
  await expect(tile(page, "Rate-limited")).toContainText("2")

  const lines = page.getByLabel("Log lines")
  await expect(lines.getByText("rate limited", { exact: true }).first()).toBeVisible()
  expect(logs.sockets.at(-1)!.get("source")).toBe("file:/var/log/ufw.log")
  // ufw.log is the firewall lens's on the server too.
  expect(logs.sockets.at(-1)!.has("lens")).toBe(false)

  // A drop is already the firewall's answer, and an allowed connection may
  // be the operator's own; a rate limit is where a deny is the next one.
  await lines.getByText(/SRC=203\.0\.113\.11 .*DPT=4448/).click()
  await expect(page.getByRole("button", { name: "More actions for this line" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Block at the firewall" })).toHaveCount(0)
  await lines.getByText(/\[UFW ALLOW\] .*SRC=198\.51\.100\.20 /).click()
  await expect(page.getByRole("button", { name: "Only this event" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Block at the firewall" })).toHaveCount(0)
  // A packet this host sent names the host as its source: no verbs for it.
  await lines.getByText(/\[UFW AUDIT\] IN= OUT=ens3 SRC=198\.51\.100\.87/).click()
  await page.getByRole("button", { name: "More actions for this line" }).click()
  await expect(page.getByRole("menuitem", { name: "Copy line" })).toBeVisible()
  await expect(page.getByRole("menuitem", { name: "Who owns this address" })).toHaveCount(0)
  await page.keyboard.press("Escape")
  await lines.getByText(/SRC=203\.0\.113\.197 /).click()
  await page.getByRole("button", { name: "Block at the firewall" }).click()
  await expect
    .poll(() => mutations.find((m) => m.path === "/firewall/rules")?.body)
    .toMatchObject({ from: "203.0.113.197", comment: "blocked from the firewall log" })

  await page.getByRole("button", { name: /^Blocked\b/ }).click()
  await expect.poll(() => logs.sockets.at(-1)?.getAll("f")).toEqual(["event:block"])
})

test("with no ufw.log the firewall's drops are read from kern.log, then the kernel ring", async ({
  page,
}) => {
  const logs = await mockSecurity(page, [], {
    firewall: { ...firewall, logging: "on (low)" },
    sources: HOST_LOG_SOURCES.filter((s) => s.id !== "file:/var/log/ufw.log"),
  })
  await page.goto("/network/firewall")
  const lines = page.getByLabel("Log lines")
  await expect(lines.getByText("rate limited", { exact: true }).first()).toBeVisible()
  // kern.log is the kernel lens's on the server, and the page asks the
  // firewall's questions of it — the one place it names a lens.
  expect(logs.sockets.at(-1)!.get("source")).toBe("file:/var/log/kern.log")
  expect(logs.sockets.at(-1)!.get("lens")).toBe("firewall")
  await expect(page.getByRole("button", { name: /^Blocked\b/ })).toBeVisible()

  // The page's lens is where the pane starts, not a setting the reader
  // changed: More is not lit for it, and Auto under Read as lets it go.
  const more = page.getByRole("button", { name: "More", exact: true })
  await expect(more).toBeVisible()
  await more.click()
  await page.getByRole("combobox", { name: "Read as" }).click()
  await page.getByRole("option", { name: /^Auto/ }).click()
  await expect.poll(() => logs.sockets.at(-1)?.has("lens")).toBe(false)
  await expect(page.getByRole("button", { name: /^More\s*1$/ })).toBeVisible()
})

test("a host with neither file reads the firewall's drops from the kernel ring", async ({
  page,
}) => {
  const logs = await mockSecurity(page, [], {
    firewall: { ...firewall, logging: "on (low)" },
    sources: HOST_LOG_SOURCES.filter(
      (s) => s.id !== "file:/var/log/ufw.log" && s.id !== "file:/var/log/kern.log",
    ),
  })
  await page.goto("/network/firewall")
  await expect(
    page.getByLabel("Log lines").getByText("rate limited", { exact: true }).first(),
  ).toBeVisible()
  expect(logs.sockets.at(-1)!.get("source")).toBe("kernel:")
  expect(logs.sockets.at(-1)!.get("lens")).toBe("firewall")
  await expect(page.getByText(/This host keeps no ufw\.log or kern\.log/)).toBeVisible()
})

test("with logging off the firewall log points at the control that turns it on", async ({
  page,
}) => {
  const logs = await mockSecurity(page)
  await page.goto("/network/firewall")
  await loaded(page)

  await expect(page.getByText("ufw is not logging")).toBeVisible()
  // Zeroes that mean "not recorded" are not drawn as readings.
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(4)
  await page.getByRole("button", { name: "Choose a logging level" }).click()
  await expect(page.getByRole("combobox", { name: "Logging default" })).toBeFocused()
  expect(logs.sockets).toHaveLength(0)
  expect(logs.searches).toHaveLength(0)
})

test("intrusion's activity is fail2ban's own log, strikes and repeat offenders included", async ({
  page,
}) => {
  const logs = await mockSecurity(page)
  await page.goto("/security/intrusion")
  await loaded(page)

  await expect(page.locator("[data-slot=stat-grid]")).toHaveCount(1)
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(8)
  await expect(tile(page, "Strikes")).toContainText("8")
  await expect(tile(page, "Bans")).toContainText("3")

  const activity = page
    .locator("[data-slot=panel]")
    .filter({ has: page.getByRole("heading", { name: "Activity", exact: true }) })
  const lines = activity.getByLabel("Log lines")
  await expect(lines.getByText("strike", { exact: true }).first()).toBeVisible()
  await expect(lines.getByText("ban increased", { exact: true })).toBeVisible()
  expect(logs.sockets.at(-1)!.get("source")).toBe("file:/var/log/fail2ban.log")
  expect(logs.sockets.at(-1)!.has("lens")).toBe(false)

  // Every strike and ban is an address a deny answers; one in ignoreip is
  // one the operator trusts.
  await lines.getByText(/\[sshd\] Ignore 198\.51\.100\.20/).click()
  await expect(activity.getByRole("button", { name: "Only this event" })).toBeVisible()
  await expect(activity.getByRole("button", { name: "Block at the firewall" })).toHaveCount(0)
  await lines.getByText(/\[sshd\] Found 198\.51\.100\.61/).click()
  await expect(activity.getByRole("button", { name: "Block at the firewall" })).toBeVisible()

  await activity.getByRole("button", { name: "Insights", exact: true }).click()
  await expect(activity.getByRole("heading", { name: "Strikes by jail" })).toBeVisible()
  await expect(activity.getByRole("heading", { name: "Repeat offenders" })).toBeVisible()
  const offenders = logs.searches.find((s) => s.get("facets") === "client")!
  expect(offenders.getAll("f")).toEqual(["event:ban"])
})

test("with no fail2ban.log the activity is read from its unit's journal", async ({ page }) => {
  const logs = await mockSecurity(page, [], {
    sources: HOST_LOG_SOURCES.filter((s) => s.id !== "file:/var/log/fail2ban.log"),
  })
  await page.route("**/api/v1/fail2ban/offenders**", (route) =>
    json(route, { ...offenders, bans: 0, unbans: 0, offenders: [], perDay: [], since: undefined }),
  )
  await page.goto("/security/intrusion")
  await expect(
    page.getByLabel("Log lines").getByText("strike", { exact: true }).first(),
  ).toBeVisible()
  expect(logs.sockets.at(-1)!.get("source")).toBe("journal:fail2ban.service")
  // The fold above reads the file, which this host does not keep: it points
  // at Activity rather than saying nothing was ever banned.
  await expect(page.getByText("No bans in fail2ban.log")).toBeVisible()
  await expect(page.getByText("Nothing has been banned yet")).toHaveCount(0)
  // With no table to hold, the fold draws no frame (§2).
  expect(await framedNonTables(page), "a framed block that is not a table").toEqual([])
})

test.describe("with no hover available", () => {
  test.use({ hasTouch: true, viewport: { width: 390, height: 844 } })

  for (const path of ["/network/connections", "/security/logins", "/security/intrusion"] as const) {
    test(`row controls on ${path} are reachable without a pointer`, async ({ page }) => {
      await mockSecurity(page)
      await page.goto(path)
      await loaded(page)
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

  for (const width of [390, 1280, 1720]) {
    test.describe(`${width} wide`, () => {
      test.use({ viewport: { width, height: 1000 } })
      for (const path of PAGES) {
        test(`${path} at ${width}`, async ({ page }) => {
          await mockSecurity(page)
          await page.goto(path)
          await loaded(page)
          await page.waitForTimeout(400)
          const name = path.replace(/^\/(security|network)\/?/, "") || "overview"
          await page.screenshot({ path: `${dir}/${name}-${width}.png`, fullPage: true })
          // The shell owns scrolling, so a full-page shot captures only the
          // first screen. Walk the remaining content without changing layout.
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
            await page.screenshot({ path: `${dir}/${name}-${width}-scroll-${part}.png` })
          }
        })
      }
      test(`rule and jail dialogs at ${width}`, async ({ page }) => {
        await mockSecurity(page)
        await page.goto("/network/firewall")
        await page.getByRole("button", { name: "Add rule", exact: true }).click()
        await expect(page.getByRole("dialog")).toBeVisible()
        await expect(page.getByRole("button", { name: "Add rule", exact: true })).toBeInViewport()
        await page.screenshot({ path: `${dir}/rule-dialog-${width}.png` })
        await page.getByRole("button", { name: "Use Tailnet", exact: true }).click()
        const source = page.getByRole("textbox", { name: "Address or CIDR", exact: true })
        await source.scrollIntoViewIfNeeded()
        await expect(source).toHaveValue("100.64.0.0/10")
        await page.screenshot({ path: `${dir}/rule-source-${width}.png` })
        await page.getByRole("button", { name: "Cancel", exact: true }).click()
        await page.goto("/security/intrusion")
        await page.getByRole("button", { name: "Tune sshd", exact: true }).click()
        await expect(page.getByRole("textbox", { name: "Failures", exact: true })).toHaveValue("5")
        await expect(page.getByRole("button", { name: "Apply", exact: true })).toBeInViewport()
        await page.screenshot({ path: `${dir}/jail-dialog-${width}.png` })
      })
    })
  }
})

test("jail policy inputs keep edits and send all three values together", async ({ page }) => {
  const mutations: Mutation[] = []
  await mockSecurity(page, mutations)
  await page.goto("/security/intrusion")
  await page.getByRole("button", { name: "Tune sshd", exact: true }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog.getByRole("textbox", { name: "Failures", exact: true })).toHaveValue("5")
  await dialog.getByRole("textbox", { name: "Failures", exact: true }).fill("3")
  await dialog.getByRole("textbox", { name: "Window", exact: true }).fill("900")
  await dialog.getByRole("textbox", { name: "Ban for", exact: true }).fill("1800")
  await expect(dialog.getByRole("textbox", { name: "Failures", exact: true })).toHaveValue("3")
  await expect(dialog.getByRole("textbox", { name: "Window", exact: true })).toHaveValue("900")
  await dialog.getByRole("button", { name: "Apply", exact: true }).click()
  await expect
    .poll(() => mutations.find((entry) => entry.path === "/fail2ban/sshd/config")?.body)
    .toEqual({ params: { maxretry: 3, findtime: 900, bantime: 1800 } })
})

test("reverting an SSH setting removes the pending change", async ({ page }) => {
  await mockSecurity(page)
  await page.goto("/security/ssh")
  const password = page.getByRole("radiogroup", { name: "Password authentication" })
  await password.getByRole("radio", { name: "no", exact: true }).click()
  await expect(page.getByRole("button", { name: "Test and apply", exact: true })).toBeVisible()
  await password.getByRole("radio", { name: "yes", exact: true }).click()
  await expect(page.getByRole("button", { name: "Test and apply", exact: true })).toHaveCount(0)
})

test("a source-only firewall deny stays editable and preserves its source", async ({ page }) => {
  const mutations: Mutation[] = []
  await mockSecurity(page, mutations)
  await page.goto("/network/firewall")
  await page
    .getByRole("row")
    .filter({ hasText: "repeat offender" })
    .getByRole("button", { name: "Edit rule", exact: true })
    .click()
  const dialog = page.getByRole("dialog")
  await expect(dialog.getByRole("textbox", { name: "Address or CIDR", exact: true })).toHaveValue(
    "203.0.113.9",
  )
  await dialog.getByRole("button", { name: "Save changes", exact: true }).click()
  await expect
    .poll(() => mutations.find((entry) => entry.path === "/firewall/rules/3")?.body)
    .toMatchObject({ action: "deny", direction: "in", from: "203.0.113.9", port: "" })
})

test("the rule builder shows the service warning and requires a deliberate source choice", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await mockSecurity(page, mutations)
  await page.goto("/network/firewall")
  await page.getByRole("button", { name: "Add rule", exact: true }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByRole("combobox", { name: "Service", exact: true }).click()
  await page.getByRole("option", { name: /Redis/ }).click()
  await expect(dialog.getByText("Redis open to the internet", { exact: true })).toBeVisible()
  await dialog.getByRole("button", { name: "Use Tailnet", exact: true }).click()
  await expect(dialog.getByRole("textbox", { name: "Address or CIDR", exact: true })).toHaveValue(
    "100.64.0.0/10",
  )
  await expect(dialog.getByText("Redis open to the internet", { exact: true })).toHaveCount(0)
  await dialog.getByRole("button", { name: "Add rule", exact: true }).click()
  await expect
    .poll(() => mutations.find((entry) => entry.path === "/firewall/rules")?.body)
    .toMatchObject({ action: "allow", port: "6379", from: "100.64.0.0/10" })
})

test("limited readers see no SSH, diagnostic, firewall or failed-login mutations", async ({
  page,
}) => {
  await mockSecurity(page, [], {
    overrides: {
      "/auth/session": {
        ...admin,
        capabilities: ["read"],
        user: { ...admin.user, role: "viewer" },
      },
    },
  })
  await page.goto("/network/firewall")
  await expect(page.getByRole("button", { name: "Add rule", exact: true })).toHaveCount(0)
  await expect(page.getByRole("switch", { name: "Firewall enabled" })).toHaveCount(0)
  await page.goto("/security/logins")
  await expect(page.getByRole("radio", { name: "Failed", exact: true })).toHaveCount(0)
  await page.goto("/security/ssh")
  await expect(page.getByText("SSH needs the admin capability", { exact: true })).toBeVisible()
  await page.goto("/network/tools")
  await expect(
    page.getByText("Diagnostics need the admin capability", { exact: true }),
  ).toBeVisible()
})

for (const [path, endpoint, reply, message] of [
  ["firewall", "/firewall/", { available: false }, "No firewall on this host"],
  [
    "ssh",
    "/ssh/config",
    { available: false, settings: [], ports: [], keyedAccounts: [] },
    "No SSH server on this host",
  ],
  ["intrusion", "/fail2ban/", { available: false }, "fail2ban is not installed"],
] as const) {
  test(`${path} keeps its unavailable state distinct from a healthy reading`, async ({ page }) => {
    await mockSecurity(page, [], { overrides: { [endpoint]: reply } })
    await page.goto(`/security/${path}`)
    await expect(page.getByText(message, { exact: true })).toBeVisible()
    await expect(page.locator("[data-slot=stat-grid]")).toHaveCount(0)
  })
}

for (const width of [390, 1280, 1720]) {
  for (const path of PAGES) {
    test(`${path} keeps its content in the viewport at ${width}`, async ({ page }) => {
      await page.setViewportSize({ width, height: 1000 })
      await mockSecurity(page)
      await page.goto(path)
      await loaded(page)
      const overflow = await page.evaluate(
        () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
      )
      expect(overflow, path).toBeLessThanOrEqual(0)
      // Tables may scroll on a phone; at desktop every action column must fit.
      if (width >= 1280) {
        const clipped = await page
          .locator('[data-slot="table-container"]')
          .evaluateAll((elements) =>
            elements
              .filter((element) => element.scrollWidth > element.clientWidth + 1)
              .map((element) => element.textContent?.slice(0, 100)),
          )
        expect(clipped, `${path} clips a table at ${width}`).toEqual([])
      }
    })
  }
}

test("diagnostics retain drafts and finish requests while another tool is selected", async ({
  page,
}) => {
  await mockSecurity(page)
  const requests: string[] = []
  let finishDNS: (() => void) | undefined
  const dnsFinished = new Promise<void>((resolve) => {
    finishDNS = resolve
  })
  await page.route("**/api/v1/network/probe", async (route) => {
    const body = route.request().postDataJSON()
    requests.push(body.tool)
    if (body.tool === "dns") await dnsFinished
    await json(route, {
      ok: true,
      tool: body.tool,
      target: body.target,
      duration: "12ms",
      output: `${body.tool} result for ${body.target}`,
    })
  })
  await page.goto("/network/tools")
  await page.getByRole("textbox", { name: "Target", exact: true }).fill("example.com")
  expect(requests).toEqual([])
  await page.getByRole("button", { name: "Run", exact: true }).click()
  await expect.poll(() => requests).toEqual(["dns"])
  await page.getByRole("button", { name: "Ping", exact: true }).click()
  await page.getByRole("textbox", { name: "Target", exact: true }).fill("other.example.com")
  await page.getByRole("button", { name: "Run", exact: true }).click()
  await expect(page.getByText("ping result for other.example.com", { exact: true })).toBeVisible()
  finishDNS!()
  await page.getByRole("button", { name: "DNS", exact: true }).click()
  await expect(page.getByRole("textbox", { name: "Target", exact: true })).toHaveValue(
    "example.com",
  )
  await expect(page.getByText("dns result for example.com", { exact: true })).toBeVisible()
  await page.getByRole("button", { name: "Ping", exact: true }).click()
  await expect(page.getByRole("textbox", { name: "Target", exact: true })).toHaveValue(
    "other.example.com",
  )
})

test("a new diagnostic deep link overrides only that tool's saved input and never runs itself", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await mockSecurity(page, mutations)
  await page.goto("/network/tools?tool=dns&target=first.example.com")
  await expect(page.getByRole("textbox", { name: "Target", exact: true })).toHaveValue(
    "first.example.com",
  )
  await page.evaluate(() =>
    window.history.pushState(
      null,
      "",
      "/network/tools?tool=dns&target=second.example.com&record=MX",
    ),
  )
  await expect(page.getByRole("textbox", { name: "Target", exact: true })).toHaveValue(
    "second.example.com",
  )
  await expect(page.getByRole("combobox", { name: "Record type", exact: true })).toContainText("MX")
  await page.getByRole("textbox", { name: "Target", exact: true }).fill("saved.example.com")
  await page.evaluate(() =>
    window.history.pushState(null, "", "/network/tools?tool=ping&target=third.example.com"),
  )
  await expect(page.getByRole("textbox", { name: "Target", exact: true })).toHaveValue(
    "third.example.com",
  )
  await page.getByRole("button", { name: "DNS", exact: true }).click()
  await expect(page.getByRole("textbox", { name: "Target", exact: true })).toHaveValue(
    "saved.example.com",
  )
  await expect(page.getByRole("combobox", { name: "Record type", exact: true })).toContainText("MX")
  expect(mutations).toEqual([])
})

test("firewall and SSH changes use ordinary confirmation", async ({ page }) => {
  const mutations: Mutation[] = []
  await mockSecurity(page, mutations)
  await page.goto("/network/firewall")
  await page.getByRole("switch", { name: "Firewall enabled" }).click()
  await expect(page.getByRole("dialog").getByPlaceholder("Type the phrase above")).toHaveCount(0)
  await expect(
    page.getByRole("dialog").getByRole("button", { name: "Disable", exact: true }),
  ).toBeEnabled()
  expect(mutations).toEqual([])
  await page.getByRole("dialog").getByRole("button", { name: "Cancel", exact: true }).click()
  await page.goto("/security/ssh")
  await page
    .getByRole("radiogroup", { name: "Password authentication" })
    .getByRole("radio", { name: "no", exact: true })
    .click()
  await page.getByRole("button", { name: "Test and apply", exact: true }).click()
  await expect(page.getByRole("dialog").getByPlaceholder("Type the phrase above")).toHaveCount(0)
  await expect(
    page.getByRole("dialog").getByRole("button", { name: "Test and apply", exact: true }),
  ).toBeEnabled()
  expect(mutations).toEqual([])
})

test("workspace: security filters are shareable and Back restores the previous question", async ({
  page,
}) => {
  await mockSecurity(page)
  await page.goto("/network/connections")
  const query = page.getByRole("textbox", { name: "Filter connections" })
  await query.fill("203.0.113.50")
  await expect(page).toHaveURL(/q=203\.0\.113\.50/)
  await query.fill("100.110.34.9")
  await expect(page).toHaveURL(/q=100\.110\.34\.9/)
  await page.goBack()
  await expect(query).toHaveValue("203.0.113.50")
  await expect(page.locator("[data-workspace-item]")).toHaveCount(1)
  await page.reload()
  await expect(query).toHaveValue("203.0.113.50")
  await query.press("Escape")
  await expect(query).toHaveValue("")
  await expect(page.locator("[data-workspace-item]").first()).toBeFocused()
})
