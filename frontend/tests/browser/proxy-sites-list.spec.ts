import { expect, test, type Page } from "@playwright/test"
import { availability, json, mockProxy, mockShowcase, user, vhosts } from "./proxy-fixtures"

/**
 * The Sites list and what its verbs report back.
 *
 * Each of these was a way the page told an operator something that was not
 * true: an enable nginx could not load came back as "configuration failed
 * validation" and left the card saying disabled while the link poisoned every
 * later reload; a delete or disable whose reload failed said "completed"; a
 * ?site= link handed a read-only account the editable form; a link in
 * sites-enabled to nothing — which stops every reload — was not listed at
 * all; and the password-file warning promised an nginx that would not start.
 */

const [app, legacy] = vhosts

// Monaco loads from /monaco/vs on first use, which takes longer than the
// default expectation on a busy machine.
const editorLoad = { timeout: 20_000 }

const refusal = {
  error: {
    code: "invalid_config",
    message: 'unknown directive "foo" in /etc/nginx/sites-available/off.example.com:3',
    raw:
      'nginx: [emerg] unknown directive "foo" in /etc/nginx/sites-enabled/off.example.com:3\n' +
      "nginx: configuration file /etc/nginx/nginx.conf test failed",
  },
}

const brokenTest = {
  valid: false,
  command: "nginx -t",
  output:
    'nginx: [emerg] open() "/etc/nginx/sites-enabled/ghost" failed (2: No such file or directory) in /etc/nginx/nginx.conf:60\n' +
    "nginx: configuration file /etc/nginx/nginx.conf test failed",
  diagnostics: [
    {
      level: "emerg",
      message: 'open() "/etc/nginx/sites-enabled/ghost" failed (2: No such file or directory)',
      file: "/etc/nginx/nginx.conf",
      line: 60,
    },
  ],
  warnings: 0,
}
const brokenReason =
  'nginx -t failed: open() "/etc/nginx/sites-enabled/ghost" failed (2: No such file or directory) in /etc/nginx/nginx.conf:60'

const off = {
  ...legacy,
  name: "off.example.com",
  path: "/etc/nginx/sites-available/off.example.com",
  enabledPath: "/etc/nginx/sites-enabled/off.example.com",
  enabled: false,
  serverNames: ["off.example.com"],
}

/** Every place nginx reads a site from, as the listing now reports them. */
const layouts = [
  app,
  {
    ...legacy,
    name: "ghost",
    path: "",
    layout: "sites-enabled",
    enabledPath: "/etc/nginx/sites-enabled/ghost",
    enabled: false,
    broken: "dangling",
    linkTarget: "/etc/nginx/sites-available/gone",
    formEditable: false,
    serverNames: [],
    listen: [],
    upstreams: [],
  },
  {
    ...legacy,
    name: "renamed.example.com",
    path: "/etc/nginx/sites-available/renamed.example.com",
    enabledPath: "/etc/nginx/sites-enabled/renamed.example.com",
    enabled: false,
    broken: "stale",
    linkTarget: "/etc/nginx/sites-available/old.example.com",
    serverNames: ["renamed.example.com"],
    tls: true,
  },
  {
    ...legacy,
    name: "extra.conf",
    path: "/etc/nginx/conf.d/extra.conf",
    layout: "conf.d",
    enabledPath: undefined,
    formEditable: false,
    serverNames: ["extra.example.com"],
    tls: true,
  },
  {
    ...legacy,
    name: "copied-in",
    path: "/etc/nginx/sites-enabled/copied-in",
    layout: "sites-enabled",
    enabledPath: undefined,
    formEditable: false,
    serverNames: ["copied.example.com"],
    tls: true,
  },
]

/** A copy in sites-enabled under the site's name: nginx serves that, not this file. */
const copied = {
  ...legacy,
  name: "copied.example.com",
  path: "/etc/nginx/sites-available/copied.example.com",
  enabledPath: "/etc/nginx/sites-enabled/copied.example.com",
  enabled: false,
  broken: "stale",
  serverNames: ["copied.example.com"],
}

/** A site file linked in from an application's repository, outside the nginx directory. */
const outside = {
  ...legacy,
  name: "outside.example.com",
  path: "/etc/nginx/sites-available/outside.example.com",
  enabledPath: "/etc/nginx/sites-enabled/outside.example.com",
  enabled: true,
  formEditable: false,
  resolvesTo: "/srv/app/deploy/nginx.conf",
  serverNames: ["outside.example.com"],
}

/** The same, kept in conf.d: nginx reads it, and the delete, which stays inside the directory, would not find it. */
const outsideConfd = {
  ...outside,
  name: "outside.conf",
  path: "/etc/nginx/conf.d/outside.conf",
  layout: "conf.d",
  enabledPath: undefined,
  resolvesTo: "/srv/app/deploy/conf.d.conf",
  serverNames: ["confd.outside.example.com"],
}

/** Served through a numbered link under another name, the way 00-default -> default is. */
const numbered = {
  ...app,
  name: "default",
  path: "/etc/nginx/sites-available/default",
  enabledPath: "/etc/nginx/sites-enabled/default",
  enabled: true,
  linkedAs: ["00-default"],
  serverNames: ["_"],
}

async function serveSites(page: Page, sites: unknown[]) {
  let reads = 0
  await page.route("**/api/v1/proxy/vhosts", (route) => {
    reads += 1
    return json(route, sites)
  })
  return () => reads
}

const card = (page: Page, name: string) =>
  page.locator("[data-slot='choice-row']").filter({ has: page.getByText(name, { exact: true }) })

async function openMenu(page: Page, name: string) {
  await page.getByRole("button", { name: `More actions for ${name}` }).click()
  return page.getByRole("menu")
}

test("an enable nginx refuses says why, shows nginx's output, and leaves the site off", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  const reads = await serveSites(page, [app, off])
  let posted: unknown
  await page.route("**/api/v1/proxy/vhosts/off.example.com/enabled", (route) => {
    posted = route.request().postDataJSON()
    return route.fulfill({
      status: 422,
      contentType: "application/json",
      body: JSON.stringify(refusal),
    })
  })
  await page.goto("/proxy/sites")
  await expect(card(page, "off.example.com").getByText("disabled", { exact: true })).toBeVisible()
  const before = reads()

  await (await openMenu(page, "off.example.com")).getByRole("menuitem", { name: "Enable" }).click()

  const toast = page
    .locator("[data-sonner-toast]")
    .filter({ hasText: "Could not enable off.example.com" })
  await expect(toast).toContainText(
    'unknown directive "foo" in /etc/nginx/sites-available/off.example.com:3',
  )
  expect(posted).toEqual({ enabled: true, reload: true })
  await toast.getByRole("button", { name: "Show nginx output" }).click()
  const output = page.getByRole("dialog", { name: "nginx output — off.example.com" })
  await expect(output).toContainText("configuration file /etc/nginx/nginx.conf test failed")
  await page.keyboard.press("Escape")
  // The card is read again, and still says what nginx is doing.
  await expect.poll(reads).toBeGreaterThan(before)
  await expect(card(page, "off.example.com").getByText("disabled", { exact: true })).toBeVisible()
})

test("a switch that landed but did not reload says so instead of 'completed'", async ({ page }) => {
  await mockProxy(page, { included: true })
  await serveSites(page, [app, off])
  await page.route("**/api/v1/proxy/vhosts/*/enabled", (route) => {
    const { enabled } = route.request().postDataJSON()
    const name = enabled ? "off.example.com" : "app.example.com"
    return json(route, {
      name,
      enabled,
      reloaded: false,
      reloadError: brokenReason,
      reload: { validation: brokenTest, reloaded: false, output: "" },
    })
  })
  await page.goto("/proxy/sites")

  await (await openMenu(page, "app.example.com")).getByRole("menuitem", { name: "Disable" }).click()
  const confirm = page.getByRole("dialog", { name: "Disable app.example.com" })
  await confirm.getByRole("button", { name: "Disable and reload" }).click()
  await expect(confirm).toHaveCount(0)
  const disabled = page
    .locator("[data-sonner-toast]")
    .filter({ hasText: "app.example.com disabled, not reloaded" })
  await expect(disabled).toContainText(
    "If nginx is running, it still serves it until a reload succeeds.",
  )
  await expect(disabled).toContainText(brokenReason)
  await expect(page.getByText("Disable app.example.com completed")).toHaveCount(0)

  await (await openMenu(page, "off.example.com")).getByRole("menuitem", { name: "Enable" }).click()
  const enabled = page
    .locator("[data-sonner-toast]")
    .filter({ hasText: "off.example.com enabled, not reloaded" })
  await expect(enabled).toContainText("It is not serving until nginx reloads.")
  await enabled.getByRole("button", { name: "Show nginx output" }).click()
  await expect(page.getByRole("dialog", { name: "nginx output — off.example.com" })).toContainText(
    "test failed",
  )
})

test("a switch keeps its card busy until the list is read again, then says what nginx serves", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  let enabled = false
  let release = () => {}
  const held = new Promise<void>((resolve) => (release = resolve))
  await page.route("**/api/v1/proxy/vhosts", async (route) => {
    // The read after the switch is held, so the page has only the answer.
    if (enabled) await held
    return json(route, [app, { ...off, enabled }])
  })
  await page.route("**/api/v1/proxy/vhosts/off.example.com/enabled", (route) => {
    enabled = true
    return json(route, {
      name: "off.example.com",
      enabled: true,
      reloaded: true,
      reload: { validation: { ...brokenTest, valid: true }, reloaded: true, output: "" },
    })
  })
  await page.goto("/proxy/sites")
  const site = card(page, "off.example.com")
  await expect(site.getByText("disabled", { exact: true })).toBeVisible()

  await (await openMenu(page, "off.example.com")).getByRole("menuitem", { name: "Enable" }).click()
  await expect(
    page.locator("[data-sonner-toast]").filter({ hasText: "off.example.com enabled" }),
  ).toBeVisible()
  // Answered, but not yet read back: the card must not fall back to the
  // state from before the switch.
  await expect(site.getByText("Enabling…", { exact: true })).toBeVisible()
  await expect(site.getByText("disabled", { exact: true })).toHaveCount(0)

  release()
  await expect(site.getByText("serving", { exact: true })).toBeVisible()
  await expect(site.getByText("Enabling…", { exact: true })).toHaveCount(0)
})

/** What `nginx -s reload` prints when there is no nginx to signal. */
const noPidFile = 'nginx: [error] open() "/run/nginx.pid" failed (2: No such file or directory)'
const notRunning = {
  reloaded: false,
  reloadError: `reload failed: ${noPidFile}`,
  reload: { validation: { ...brokenTest, valid: true }, reloaded: false, output: noPidFile },
}

/** systemd's reading of an engine's unit, as GET /systemd/{name} answers it. */
function engineUnit(activeState: string, mainPid?: number, name = "nginx.service") {
  return {
    unit: {
      name,
      description: "A high performance web server",
      loadState: "loaded",
      activeState,
      subState: activeState === "active" ? "running" : activeState === "failed" ? "failed" : "dead",
      unitFileState: "enabled",
      enabled: true,
      ...(mainPid ? { mainPid, activeSince: Math.floor(Date.now() / 1000) - 600 } : {}),
    },
    properties: {},
  }
}

/** Serves nginx's unit as `set` last left it, so a test can stop or start nginx. */
async function serveUnit(page: Page, initial: unknown) {
  let current = initial
  await page.route("**/api/v1/systemd/nginx.service", (route) => json(route, current))
  return (next: unknown) => {
    current = next
  }
}

/** A reload nginx was refused the signal for: it is running, with what it had. */
const signalRefused = "nginx: [alert] kill(812, 1) failed (1: Operation not permitted)"
const refusedSignal = {
  reloaded: false,
  reloadError: `reload failed: ${signalRefused}`,
  reload: { validation: { ...brokenTest, valid: true }, reloaded: false, output: signalRefused },
}
const reloadedOk = {
  reloaded: true,
  reload: { validation: { ...brokenTest, valid: true }, reloaded: true, output: "" },
}

test("a change with no nginx running says so, not that nginx keeps serving", async ({ page }) => {
  await mockProxy(page, { included: true })
  await serveUnit(page, engineUnit("inactive"))
  await serveSites(page, [layouts[1], app, legacy, off])
  await page.route("**/api/v1/proxy/vhosts/*/enabled", (route) => {
    const { enabled } = route.request().postDataJSON()
    return json(route, {
      name: enabled ? "off.example.com" : "app.example.com",
      enabled,
      ...notRunning,
    })
  })
  await page.route("**/api/v1/proxy/vhosts/ghost/link", (route) =>
    json(route, { name: "ghost", enabled: false, ...notRunning }),
  )
  await page.route("**/api/v1/proxy/sites/*", (route) =>
    route.request().method() === "DELETE"
      ? json(route, { name: "legacy.example.com", ...notRunning })
      : route.fallback(),
  )
  await page.goto("/proxy/sites")
  const toast = (title: string) => page.locator("[data-sonner-toast]").filter({ hasText: title })

  await (await openMenu(page, "app.example.com")).getByRole("menuitem", { name: "Disable" }).click()
  await page.getByRole("button", { name: "Disable and reload" }).click()
  const disabled = toast("app.example.com disabled; nginx is not running")
  await expect(disabled).toContainText("nginx starts without it.")
  await expect(disabled).not.toContainText(/serving|serves|not reloaded/)
  await disabled.getByRole("button", { name: "Show nginx output" }).click()
  const output = page.getByRole("dialog", { name: "nginx output — app.example.com" })
  await expect(output).toContainText(noPidFile)
  await page.keyboard.press("Escape")
  await expect(output).toHaveCount(0)

  await (await openMenu(page, "off.example.com")).getByRole("menuitem", { name: "Enable" }).click()
  await expect(toast("off.example.com enabled; nginx is not running")).toContainText(
    "It serves once nginx starts.",
  )

  await (await openMenu(page, "ghost")).getByRole("menuitem", { name: "Remove link" }).click()
  await page.getByRole("button", { name: "Remove and reload" }).click()
  const removed = toast("sites-enabled/ghost removed; nginx is not running")
  await expect(removed).toContainText("nginx starts without it.")
  await expect(removed).not.toContainText("configuration from before")

  await (
    await openMenu(page, "legacy.example.com")
  )
    .getByRole("menuitem", { name: "Delete" })
    .click()
  await page.getByRole("button", { name: "Delete and reload" }).click()
  const deleted = toast("legacy.example.com deleted; nginx is not running")
  await expect(deleted).toContainText("nginx starts without it.")
  await expect(deleted).not.toContainText(/serving|serves/)
  await expect(page.getByText("Delete legacy.example.com completed")).toHaveCount(0)
})

test("with nginx stopped no nginx site reads serving, and the page says why", async ({ page }) => {
  await mockProxy(page, { included: true })
  const setUnit = await serveUnit(page, engineUnit("inactive"))
  const confd = layouts[3]
  const shop = vhosts[2]
  const state = { off: false, app: true }
  await page.route("**/api/v1/proxy/vhosts", (route) =>
    json(route, [{ ...app, enabled: state.app }, { ...off, enabled: state.off }, confd, shop]),
  )
  await page.route("**/api/v1/proxy/vhosts/off.example.com/enabled", (route) => {
    state.off = true
    return json(route, { name: "off.example.com", enabled: true, ...notRunning })
  })
  await page.route("**/api/v1/proxy/vhosts/app.example.com/enabled", (route) => {
    state.app = false
    return json(route, { name: "app.example.com", enabled: false, ...reloadedOk })
  })
  await page.goto("/proxy/sites")

  const stopped = page.getByText(
    "systemd reports nginx.service inactive (dead), so no nginx site below is served until it starts.",
  )
  await expect(page.getByText("nginx is not running", { exact: true })).toBeVisible()
  await expect(stopped).toBeVisible()
  await expect(page.getByRole("link", { name: "Start it from the Overview" })).toHaveAttribute(
    "href",
    "/proxy",
  )
  await expect(card(page, "app.example.com").getByText("enabled", { exact: true })).toBeVisible()
  await expect(card(page, "extra.conf").getByText("enabled", { exact: true })).toBeVisible()
  await expect(card(page, "extra.conf").getByText("always on", { exact: true })).toHaveCount(0)
  await expect(card(page, "off.example.com").getByText("disabled", { exact: true })).toBeVisible()
  // The Docker ingress is a container of its own, which nginx stopping does not touch.
  await expect(
    card(page, "just-dashboard-shop").getByText("serving", { exact: true }),
  ).toBeVisible()
  await expect(
    page.locator("[data-slot='choice-row']").getByText("serving", { exact: true }),
  ).toHaveCount(1)
  await expect(page.getByRole("list", { name: "Enabled" })).toBeVisible()
  await expect(page.getByRole("list", { name: "Serving" })).toHaveCount(0)

  // An enable with nginx stopped lands, and the card says enabled, not serving.
  await (await openMenu(page, "off.example.com")).getByRole("menuitem", { name: "Enable" }).click()
  await expect(
    page
      .locator("[data-sonner-toast]")
      .filter({ hasText: "off.example.com enabled; nginx is not running" }),
  ).toContainText("It serves once nginx starts.")
  const turnedOn = card(page, "off.example.com")
  await expect(turnedOn.getByText("enabled", { exact: true })).toBeVisible()
  await expect(turnedOn.getByText("serving", { exact: true })).toHaveCount(0)
  await expect(page.getByText("every site enabled")).toBeVisible()
  await expect(page.getByText("every site serving")).toHaveCount(0)
  await expect(stopped).toBeVisible()

  // nginx is started, and the next verb's read of systemd finds it running:
  // it read the configuration as it is now.
  setUnit(engineUnit("active", 812))
  await (await openMenu(page, "app.example.com")).getByRole("menuitem", { name: "Disable" }).click()
  await page.getByRole("button", { name: "Disable and reload" }).click()
  await expect(page.getByText("app.example.com disabled", { exact: true })).toBeVisible()
  await expect(turnedOn.getByText("serving", { exact: true })).toBeVisible()
  await expect(card(page, "extra.conf").getByText("always on", { exact: true })).toBeVisible()
  await expect(page.getByText("nginx is not running", { exact: true })).toHaveCount(0)
  await expect(page.getByRole("list", { name: "Serving" })).toBeVisible()
})

test("a reader sees nginx is not running, without the way to start it", async ({ page }) => {
  await mockProxy(page, { included: true })
  await serveUnit(page, engineUnit("failed"))
  await serveSites(page, [app, legacy])
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, { ...user, capabilities: ["read"], user: { ...user.user, role: "viewer" } }),
  )
  await page.goto("/proxy/sites")
  await expect(
    page.getByText(
      "systemd reports nginx.service failed, so no nginx site below is served until it starts.",
    ),
  ).toBeVisible()
  await expect(page.getByRole("link", { name: "Start it from the Overview" })).toHaveCount(0)
  await expect(card(page, "legacy.example.com").getByText("enabled", { exact: true })).toBeVisible()
  await expect(page.getByText("every site enabled")).toBeVisible()
})

test("a switch nginx did not reload reads not reloaded until nginx has loaded it", async ({
  page,
}, testInfo) => {
  // At phone width: the longest state a card draws.
  await page.setViewportSize({ width: 390, height: 1000 })
  await mockProxy(page, { included: true })
  const setUnit = await serveUnit(page, engineUnit("active", 812))
  const state: Record<string, boolean> = { "off.example.com": false, "legacy.example.com": true }
  // On TLS, so only a change nginx has not loaded puts it among the sites
  // that need attention.
  await page.route("**/api/v1/proxy/vhosts", (route) =>
    json(route, [
      app,
      { ...off, enabled: state["off.example.com"], tls: true, listen: ["443 ssl"] },
      { ...legacy, enabled: state["legacy.example.com"] },
    ]),
  )
  let answer: "refused signal" | "reloaded" | "refused change" = "refused signal"
  await page.route("**/api/v1/proxy/vhosts/*/enabled", (route) => {
    const name = decodeURIComponent(new URL(route.request().url()).pathname.split("/").at(-2) ?? "")
    const { enabled } = route.request().postDataJSON()
    if (answer === "refused change") {
      return route.fulfill({
        status: 422,
        contentType: "application/json",
        body: JSON.stringify(refusal),
      })
    }
    state[name] = enabled
    return json(route, {
      name,
      enabled,
      ...(answer === "reloaded" ? reloadedOk : refusedSignal),
    })
  })
  await page.goto("/proxy/sites")
  const toast = (title: string) => page.locator("[data-sonner-toast]").filter({ hasText: title })
  const offCard = card(page, "off.example.com")
  await expect(offCard.getByText("disabled", { exact: true })).toBeVisible()

  await (await openMenu(page, "off.example.com")).getByRole("menuitem", { name: "Enable" }).click()
  await expect(toast("off.example.com enabled, not reloaded")).toContainText(
    "It is not serving until nginx reloads.",
  )
  await expect(offCard.getByText("enabled, not reloaded", { exact: true })).toBeVisible()
  await expect(offCard.getByText("serving", { exact: true })).toHaveCount(0)
  const inGroup = (group: string) =>
    page
      .getByRole("list", { name: group })
      .locator("[data-slot='choice-row']")
      .filter({ has: page.getByText("off.example.com", { exact: true }) })
  await expect(inGroup("Needs attention")).toBeVisible()
  await expect(page.getByText("every site enabled")).toBeVisible()
  // nginx is running: nothing says it is not.
  await expect(page.getByText("nginx is not running")).toHaveCount(0)
  expect(
    await page
      .locator("[data-slot='page']")
      .evaluate((element) => element.scrollWidth <= element.clientWidth + 1),
  ).toBe(true)
  await page.screenshot({ path: testInfo.outputPath("sites-not-reloaded-390.png"), fullPage: true })

  // Refused, a later switch changes nothing, and nginx still lacks the enable.
  answer = "refused change"
  await (await openMenu(page, "off.example.com")).getByRole("menuitem", { name: "Disable" }).click()
  await page.getByRole("button", { name: "Disable and reload" }).click()
  await expect(toast("Could not disable off.example.com")).toBeVisible()
  await expect(offCard.getByText("enabled, not reloaded", { exact: true })).toBeVisible()

  // A reload that goes through loads every change before it.
  answer = "reloaded"
  await (
    await openMenu(page, "legacy.example.com")
  )
    .getByRole("menuitem", { name: "Disable" })
    .click()
  await page.getByRole("button", { name: "Disable and reload" }).click()
  await expect(page.getByText("legacy.example.com disabled", { exact: true })).toBeVisible()
  await expect(offCard.getByText("serving", { exact: true })).toBeVisible()
  await expect(inGroup("Serving")).toBeVisible()
  const legacyCard = card(page, "legacy.example.com")
  await expect(legacyCard.getByText("disabled", { exact: true })).toBeVisible()

  // A disable nginx did not reload: a running nginx still has the site.
  answer = "refused signal"
  await (
    await openMenu(page, "legacy.example.com")
  )
    .getByRole("menuitem", { name: "Enable" })
    .click()
  await expect(toast("legacy.example.com enabled, not reloaded")).toBeVisible()
  await expect(legacyCard.getByText("enabled, not reloaded", { exact: true })).toBeVisible()
  answer = "reloaded"
  await (await openMenu(page, "off.example.com")).getByRole("menuitem", { name: "Disable" }).click()
  await page.getByRole("button", { name: "Disable and reload" }).click()
  await expect(legacyCard.getByText("serving", { exact: true })).toBeVisible()
  answer = "refused signal"
  await (
    await openMenu(page, "legacy.example.com")
  )
    .getByRole("menuitem", { name: "Disable" })
    .click()
  await page.getByRole("button", { name: "Disable and reload" }).click()
  await expect(toast("legacy.example.com disabled, not reloaded")).toBeVisible()
  await expect(legacyCard.getByText("disabled, not reloaded", { exact: true })).toBeVisible()

  // nginx is restarted: a new process read the configuration as it is now,
  // which the next read of systemd shows.
  setUnit(engineUnit("active", 944))
  answer = "refused change"
  await (await openMenu(page, "off.example.com")).getByRole("menuitem", { name: "Enable" }).click()
  await expect(toast("Could not enable off.example.com")).toBeVisible()
  await expect(legacyCard.getByText("disabled", { exact: true })).toBeVisible()
  await expect(legacyCard.getByText("disabled, not reloaded", { exact: true })).toHaveCount(0)
})

test("a reload that finds no pid file while systemd has nginx up says it did not reload", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await serveUnit(page, engineUnit("active", 812))
  let enabled = false
  await page.route("**/api/v1/proxy/vhosts", (route) => json(route, [app, { ...off, enabled }]))
  await page.route("**/api/v1/proxy/vhosts/off.example.com/enabled", (route) => {
    enabled = true
    return json(route, { name: "off.example.com", enabled: true, ...notRunning })
  })
  await page.goto("/proxy/sites")
  await (await openMenu(page, "off.example.com")).getByRole("menuitem", { name: "Enable" }).click()
  // Up, with its pid file gone: nginx serves what it had, out of the reload's reach.
  const said = page
    .locator("[data-sonner-toast]")
    .filter({ hasText: "off.example.com enabled, not reloaded" })
  await expect(said).toContainText("It is not serving until nginx reloads.")
  await expect(said).toContainText(noPidFile)
  await expect(page.getByText("nginx is not running")).toHaveCount(0)
  await expect(
    card(page, "off.example.com").getByText("enabled, not reloaded", { exact: true }),
  ).toBeVisible()
  await expect(card(page, "app.example.com").getByText("serving", { exact: true })).toBeVisible()
})

test("where nginx has no unit, a reload that found none says nginx is not running until one goes through", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await serveUnit(page, {
    unit: { ...engineUnit("inactive").unit, loadState: "not-found", activeState: "inactive" },
    properties: {},
  })
  const state = { off: false, app: true }
  await page.route("**/api/v1/proxy/vhosts", (route) =>
    json(route, [{ ...app, enabled: state.app }, { ...off, enabled: state.off }, legacy]),
  )
  await page.route("**/api/v1/proxy/vhosts/off.example.com/enabled", (route) => {
    state.off = true
    return json(route, { name: "off.example.com", enabled: true, ...notRunning })
  })
  await page.route("**/api/v1/proxy/vhosts/app.example.com/enabled", (route) => {
    state.app = false
    return json(route, { name: "app.example.com", enabled: false, ...reloadedOk })
  })
  await page.goto("/proxy/sites")
  // No unit, no reading: the page claims nothing it has not been told.
  await expect(page.getByText("nginx is not running")).toHaveCount(0)
  await expect(card(page, "app.example.com").getByText("serving", { exact: true })).toBeVisible()

  await (await openMenu(page, "off.example.com")).getByRole("menuitem", { name: "Enable" }).click()
  await expect(page.getByText("off.example.com enabled; nginx is not running")).toBeVisible()
  await expect(
    page.getByText(
      "The last reload found no nginx process to signal, so no nginx site below is served until it starts.",
    ),
  ).toBeVisible()
  await expect(page.getByRole("link", { name: "Start it from the Overview" })).toHaveCount(0)
  await expect(card(page, "app.example.com").getByText("enabled", { exact: true })).toBeVisible()
  await expect(card(page, "off.example.com").getByText("enabled", { exact: true })).toBeVisible()
  await expect(
    page.locator("[data-slot='choice-row']").getByText("serving", { exact: true }),
  ).toHaveCount(0)

  await (await openMenu(page, "app.example.com")).getByRole("menuitem", { name: "Disable" }).click()
  await page.getByRole("button", { name: "Disable and reload" }).click()
  await expect(page.getByText("app.example.com disabled", { exact: true })).toBeVisible()
  await expect(page.getByText("nginx is not running", { exact: true })).toHaveCount(0)
  await expect(card(page, "off.example.com").getByText("serving", { exact: true })).toBeVisible()
})

test("with Caddy's unit stopped, its Caddyfile sites do not read serving", async ({ page }) => {
  await mockProxy(page, { included: true })
  await page.route("**/api/v1/proxy/status", (route) =>
    json(route, {
      ...availability,
      nginx: false,
      nginxVersion: "",
      caddy: true,
      caddyVersion: "v2.8.4",
    }),
  )
  await page.route("**/api/v1/systemd/caddy.service", (route) =>
    json(route, engineUnit("failed", undefined, "caddy.service")),
  )
  await serveSites(page, [
    {
      ...vhosts[2],
      name: "shop.example.com",
      path: "/etc/caddy/Caddyfile",
      serverNames: ["shop.example.com"],
      upstreams: ["http://127.0.0.1:3000"],
    },
  ])
  await page.goto("/proxy/sites")
  await expect(page.getByText("Caddy is not running", { exact: true })).toBeVisible()
  await expect(
    page.getByText(
      "systemd reports caddy.service failed, so no Caddy site below is served until it starts.",
    ),
  ).toBeVisible()
  const shop = card(page, "shop.example.com")
  await expect(shop.getByText("enabled", { exact: true })).toBeVisible()
  await expect(shop.getByText("serving", { exact: true })).toHaveCount(0)
})

test("a switch whose list could not be read again does not show the state from before", async ({
  page,
}, testInfo) => {
  // At phone width: the notice carries the server's words, which can be long.
  await page.setViewportSize({ width: 390, height: 1000 })
  await mockProxy(page, { included: true })
  let enabled = false
  let failing = false
  await page.route("**/api/v1/proxy/vhosts", (route) =>
    failing
      ? route.fulfill({
          status: 503,
          contentType: "application/json",
          body: JSON.stringify({
            error: {
              code: "unavailable",
              message:
                "backend restarting: /var/lib/just-dashboard/run/just-dashboard-backend.sock is not accepting connections",
              retryable: true,
            },
          }),
        })
      : json(route, [app, { ...off, enabled }]),
  )
  await page.route("**/api/v1/proxy/vhosts/off.example.com/enabled", (route) => {
    enabled = true
    failing = true
    return json(route, {
      name: "off.example.com",
      enabled: true,
      reloaded: true,
      reload: { validation: { ...brokenTest, valid: true }, reloaded: true, output: "" },
    })
  })
  await page.goto("/proxy/sites")
  const site = card(page, "off.example.com")
  await expect(site.getByText("disabled", { exact: true })).toBeVisible()
  await expect(page.getByText("Could not read the sites again")).toHaveCount(0)

  await (await openMenu(page, "off.example.com")).getByRole("menuitem", { name: "Enable" }).click()
  await expect(
    page.locator("[data-sonner-toast]").filter({ hasText: "off.example.com enabled" }),
  ).toBeVisible()
  // The read after the switch failed: the page says so, and the card does
  // not fall back to the rows from before the change.
  const notice = page.getByRole("alert").filter({ hasText: "Could not read the sites again" })
  await expect(notice).toContainText("backend restarting")
  await expect(notice).toContainText("from the last read, and may be out of date")
  await expect(site.getByText("not read back", { exact: true })).toBeVisible()
  await expect(site.getByText("disabled", { exact: true })).toHaveCount(0)
  await expect(site.getByText("Enabling…", { exact: true })).toHaveCount(0)
  // The other card was not changed by anything, and keeps its reading.
  await expect(card(page, "app.example.com").getByText("serving", { exact: true })).toBeVisible()
  expect(
    await page
      .locator("[data-slot='page']")
      .evaluate((element) => element.scrollWidth <= element.clientWidth + 1),
  ).toBe(true)
  await page.screenshot({ path: testInfo.outputPath("sites-unread-390.png"), fullPage: true })

  failing = false
  await notice.getByRole("button", { name: "Try again" }).click()
  await expect(site.getByText("serving", { exact: true })).toBeVisible()
  await expect(page.getByText("Could not read the sites again")).toHaveCount(0)
  await expect(site.getByText("not read back", { exact: true })).toHaveCount(0)
})

test("a disable another site needs is refused, says so, and leaves the site serving", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  const reads = await serveSites(page, [app, legacy])
  await page.route("**/api/v1/proxy/vhosts/app.example.com/enabled", (route) =>
    route.fulfill({
      status: 422,
      contentType: "application/json",
      body: JSON.stringify({
        error: {
          code: "invalid_config",
          message:
            'nginx refuses the configuration without app.example.com: host not found in upstream "app_pool" in /etc/nginx/sites-available/legacy.example.com:7',
          raw:
            'nginx: [emerg] host not found in upstream "app_pool" in /etc/nginx/sites-enabled/legacy.example.com:7\n' +
            "nginx: configuration file /etc/nginx/nginx.conf test failed",
        },
      }),
    }),
  )
  await page.goto("/proxy/sites")
  const before = reads()

  await (await openMenu(page, "app.example.com")).getByRole("menuitem", { name: "Disable" }).click()
  const confirm = page.getByRole("dialog", { name: "Disable app.example.com" })
  await expect(confirm).toContainText("the link goes back and nothing changes")
  await confirm.getByRole("button", { name: "Disable and reload" }).click()
  await expect(confirm).toHaveCount(0)

  const refused = page
    .locator("[data-sonner-toast]")
    .filter({ hasText: "Could not disable app.example.com" })
  await expect(refused).toContainText(
    "nginx refuses the configuration without app.example.com: host not found in upstream",
  )
  await expect(refused).toContainText("/etc/nginx/sites-available/legacy.example.com:7")
  await expect(page.getByText("Disable app.example.com completed")).toHaveCount(0)
  await expect.poll(reads).toBeGreaterThan(before)
  await expect(card(page, "app.example.com").getByText("serving", { exact: true })).toBeVisible()
})

test("a delete whose reload failed says a running nginx still serves the site", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  const reads = await serveSites(page, [app, legacy])
  let reload = false
  await page.route("**/api/v1/proxy/sites/*", (route) => {
    if (route.request().method() !== "DELETE") return route.fallback()
    const name = decodeURIComponent(new URL(route.request().url()).pathname.split("/").pop()!)
    return json(
      route,
      reload
        ? {
            name,
            reload: { validation: { ...brokenTest, valid: true }, reloaded: true, output: "" },
          }
        : {
            name,
            // What the delete handler answers when the test refused its reload.
            reloadError: "configuration failed validation",
            reload: { validation: brokenTest, reloaded: false, output: "" },
          },
    )
  })
  await page.goto("/proxy/sites")

  await (
    await openMenu(page, "legacy.example.com")
  )
    .getByRole("menuitem", { name: "Delete" })
    .click()
  await page.getByRole("button", { name: "Delete and reload" }).click()
  const toast = page
    .locator("[data-sonner-toast]")
    .filter({ hasText: "legacy.example.com deleted, not reloaded" })
  await expect(toast).toContainText(
    "If nginx is running, it still serves it until a reload succeeds.",
  )
  await expect(toast).toContainText(brokenReason)
  await expect(page.getByText("Delete legacy.example.com completed")).toHaveCount(0)
  expect(reads()).toBeGreaterThan(1)

  reload = true
  await (await openMenu(page, "app.example.com")).getByRole("menuitem", { name: "Delete" }).click()
  await page.getByRole("button", { name: "Delete and reload" }).click()
  await expect(page.getByText("Delete app.example.com completed")).toBeVisible()
})

test("a ?site= link gives a reader the config viewer and an administrator the form", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  const asked: string[] = []
  await page.route("**/api/v1/proxy/config?**", (route) => {
    asked.push("config")
    return json(route, { content: "server {\n    server_name app.example.com;\n}\n" })
  })
  await page.route("**/api/v1/proxy/sites/app.example.com", (route) => {
    asked.push("form")
    return route.fulfill({ status: 503, contentType: "application/json", body: "{}" })
  })
  await page.route("**/api/v1/proxy/sites/preview", (route) => {
    asked.push("preview")
    return route.fulfill({ status: 403, contentType: "application/json", body: "{}" })
  })
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, { ...user, capabilities: ["read"], user: { ...user.user, role: "viewer" } }),
  )
  await page.goto("/proxy/sites?site=app.example.com")
  const viewer = page.getByRole("dialog")
  await expect(viewer.locator(".monaco-editor .view-lines")).toContainText(
    "server_name app.example.com",
    editorLoad,
  )
  await expect(viewer.getByRole("button", { name: /^Save/ })).toHaveCount(0)
  expect(asked).toEqual(["config"])
  // The card itself opens the same viewer for a reader.
  await page.keyboard.press("Escape")
  await expect(viewer).toHaveCount(0)
  await card(page, "app.example.com").getByRole("button", { name: "Open app.example.com" }).click()
  await expect(page.getByRole("dialog").locator(".monaco-editor .view-lines")).toContainText(
    "server_name",
    editorLoad,
  )

  await page.unroute("**/api/v1/auth/session")
  asked.length = 0
  await page.goto("/proxy/sites?site=app.example.com")
  await expect(page.getByRole("dialog")).toBeVisible()
  await expect.poll(() => asked.includes("form")).toBe(true)
  expect(asked).not.toContain("config")
})

test("every place nginx reads a site from is listed, a link to nothing first", async ({ page }) => {
  await mockProxy(page, { included: true })
  await serveSites(page, layouts)
  let removed: string | undefined
  await page.route("**/api/v1/proxy/vhosts/ghost/link", (route) => {
    removed = route.request().method()
    return json(route, {
      name: "ghost",
      enabled: false,
      reloaded: true,
      reload: { validation: { ...brokenTest, valid: true }, reloaded: true, output: "" },
    })
  })
  await page.route("**/api/v1/proxy/config?**", (route) => json(route, { content: "server {}\n" }))
  await page.goto("/proxy/sites")

  const cards = page
    .getByRole("list", { name: "Needs attention" })
    .locator("[data-slot='choice-row']")
  await expect(cards.first()).toContainText("ghost")
  const ghost = card(page, "ghost")
  await expect(ghost.getByText("broken link", { exact: true })).toBeVisible()
  await expect(ghost).toContainText(
    "sites-enabled/ghost points at /etc/nginx/sites-available/gone, which is missing, so nginx refuses every reload until the link is removed.",
  )
  // Nothing to open: there is no file behind it.
  await expect(ghost.getByRole("button", { name: "Raw config" })).toHaveCount(0)
  await expect(ghost.getByRole("button", { name: /^Open / })).toHaveCount(0)

  const renamed = card(page, "renamed.example.com")
  await expect(renamed.getByText("stale link", { exact: true })).toBeVisible()
  await expect(renamed).toContainText(
    "points at /etc/nginx/sites-available/old.example.com, so nginx serves that file instead of this one.",
  )
  // The enable points the link back at this file.
  await expect(
    (await openMenu(page, "renamed.example.com")).getByRole("menuitem", { name: "Enable" }),
  ).toBeVisible()
  await page.keyboard.press("Escape")

  // A conf.d file on a Debian host is read by nginx and saved by its editor,
  // not the form, which would write it to sites-available instead.
  const extra = card(page, "extra.conf")
  await expect(extra).toContainText("nginx site in conf.d")
  await expect(extra.getByRole("button", { name: "Edit" })).toHaveCount(0)
  const extraMenu = await openMenu(page, "extra.conf")
  await expect(extraMenu.getByRole("menuitem", { name: "Delete" })).toBeVisible()
  await expect(extraMenu.getByRole("menuitem", { name: "Duplicate" })).toHaveCount(0)
  await page.keyboard.press("Escape")

  // A file copied into sites-enabled is served, has its editor, and is
  // never deleted from here.
  const copied = card(page, "copied-in")
  await expect(copied).toContainText("nginx site in sites-enabled")
  await expect(copied.getByText("always on", { exact: true })).toBeVisible()
  const copiedMenu = await openMenu(page, "copied-in")
  await expect(
    copiedMenu.getByRole("menuitem", { name: /Delete|Remove link|Disable/ }),
  ).toHaveCount(0)
  await page.keyboard.press("Escape")
  await copied.getByRole("button", { name: "Open copied-in" }).click()
  await expect(page.getByRole("dialog").locator(".monaco-editor .view-lines")).toContainText(
    "server",
    editorLoad,
  )
  await page.keyboard.press("Escape")

  // Nothing is disabled, and the reading does not call that "every site serving".
  await expect(page.getByText("none, but 2 broken links")).toBeVisible()
  await page.getByRole("button", { name: /^Broken links/ }).click()
  await expect(page.locator("[data-slot='choice-row']")).toHaveCount(2)

  await (await openMenu(page, "ghost")).getByRole("menuitem", { name: "Remove link" }).click()
  const confirm = page.getByRole("dialog", { name: "Remove sites-enabled/ghost" })
  await expect(confirm).toContainText("Removing it is what lets nginx load its configuration again")
  await confirm.getByRole("button", { name: "Remove and reload" }).click()
  await expect(page.getByText("sites-enabled/ghost removed", { exact: true })).toBeVisible()
  expect(removed).toBe("DELETE")
  await expect(page.getByText("Remove sites-enabled/ghost completed")).toHaveCount(0)
})

test("a copy in sites-enabled opens as the served copy and is never deleted", async ({ page }) => {
  await mockProxy(page, { included: true })
  await serveSites(page, [app, copied, layouts[2]])
  const read: string[] = []
  await page.route("**/api/v1/proxy/config?**", (route) => {
    const path = new URL(route.request().url()).searchParams.get("path") ?? ""
    read.push(path)
    return json(route, {
      content: path.includes("sites-enabled")
        ? "server {\n    return 200 'served copy';\n}\n"
        : "server {\n    return 204;\n}\n",
    })
  })
  await page.goto("/proxy/sites")

  const menu = await openMenu(page, "copied.example.com")
  await expect(menu.getByRole("menuitem", { name: "Served copy" })).toBeVisible()
  // The delete would take the copy nginx serves with no backup, and an
  // enable does not replace a file.
  await expect(menu.getByRole("menuitem", { name: /^(Delete|Enable|Disable)$/ })).toHaveCount(0)
  await menu.getByRole("menuitem", { name: "Served copy" }).click()
  const served = page.getByRole("dialog", { name: /sites-enabled\/copied\.example\.com/ })
  await expect(served.locator(".monaco-editor .view-lines")).toContainText(
    "served copy",
    editorLoad,
  )
  expect(read).toEqual(["/etc/nginx/sites-enabled/copied.example.com"])
  await page.keyboard.press("Escape")
  await expect(served).toHaveCount(0)

  // A link to another site is that site's: deleting this one would take it
  // out of nginx.
  const staleMenu = await openMenu(page, "renamed.example.com")
  await expect(staleMenu.getByRole("menuitem", { name: "Enable" })).toBeVisible()
  await expect(staleMenu.getByRole("menuitem", { name: "Delete" })).toHaveCount(0)
})

test("a site file kept outside the nginx directory keeps its switch", async ({ page }) => {
  await mockProxy(page, { included: true })
  await serveSites(page, [app, outside, outsideConfd])
  let posted: unknown
  await page.route("**/api/v1/proxy/vhosts/outside.example.com/enabled", (route) => {
    posted = route.request().postDataJSON()
    return json(route, {
      name: "outside.example.com",
      enabled: false,
      reloaded: true,
      reload: { validation: { ...brokenTest, valid: true }, reloaded: true, output: "" },
    })
  })
  await page.goto("/proxy/sites")

  const site = card(page, "outside.example.com")
  await expect(site).toContainText(
    "sites-available/outside.example.com links to /srv/app/deploy/nginx.conf, outside the nginx directory, so this page does not open it.",
  )
  // Nothing to open that would only answer "outside the proxy configuration directory".
  await expect(site.getByRole("button", { name: /^(Raw config|Edit|Open outside)/ })).toHaveCount(0)
  const menu = await openMenu(page, "outside.example.com")
  await expect(menu.getByRole("menuitem", { name: /^(Delete|Duplicate)$/ })).toHaveCount(0)
  await menu.getByRole("menuitem", { name: "Disable" }).click()
  await page
    .getByRole("dialog", { name: "Disable outside.example.com" })
    .getByRole("button", { name: "Disable and reload" })
    .click()
  await expect(page.getByText("outside.example.com disabled", { exact: true })).toBeVisible()
  expect(posted).toEqual({ enabled: false, reload: true })

  // In conf.d there is no switch, and the delete could only answer "no such site".
  await expect(card(page, "outside.conf")).toContainText(
    "conf.d/outside.conf links to /srv/app/deploy/conf.d.conf, outside the nginx directory",
  )
  const confdMenu = await openMenu(page, "outside.conf")
  await expect(confdMenu.getByRole("menuitem", { name: "Open site" })).toBeVisible()
  await expect(
    confdMenu.getByRole("menuitem", { name: /^(Delete|Duplicate|Enable|Disable)$/ }),
  ).toHaveCount(0)
})

test("an enable that re-points a stale link asks first when it takes another file out of nginx", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  // sites-enabled/renamed.example.com -> old.example.com, its only way in.
  const stale = layouts[2]
  const old = {
    ...legacy,
    name: "old.example.com",
    path: "/etc/nginx/sites-available/old.example.com",
    enabledPath: "/etc/nginx/sites-enabled/old.example.com",
    enabled: true,
    linkedAs: ["renamed.example.com"],
    serverNames: ["old.example.com"],
  }
  const pointsOutside = {
    ...stale,
    name: "moved.example.com",
    path: "/etc/nginx/sites-available/moved.example.com",
    enabledPath: "/etc/nginx/sites-enabled/moved.example.com",
    linkTarget: "/srv/app/deploy/nginx.conf",
    serverNames: ["moved.example.com"],
  }
  const stillServed = {
    ...stale,
    name: "kept.example.com",
    path: "/etc/nginx/sites-available/kept.example.com",
    enabledPath: "/etc/nginx/sites-enabled/kept.example.com",
    linkTarget: "/etc/nginx/conf.d/kept.conf",
    targetServedElsewhere: true,
    serverNames: ["kept.example.com"],
  }
  await serveSites(page, [app, stale, old, pointsOutside, stillServed])
  const posted: string[] = []
  await page.route("**/api/v1/proxy/vhosts/*/enabled", (route) => {
    const name = decodeURIComponent(new URL(route.request().url()).pathname.split("/").at(-2) ?? "")
    posted.push(name)
    return json(route, {
      name,
      enabled: true,
      reloaded: true,
      reload: { validation: { ...brokenTest, valid: true }, reloaded: true, output: "" },
    })
  })
  await page.goto("/proxy/sites")

  await (
    await openMenu(page, "renamed.example.com")
  )
    .getByRole("menuitem", { name: "Enable" })
    .click()
  const ask = page.getByRole("dialog", { name: "Enable renamed.example.com" })
  await expect(ask).toContainText(
    "sites-enabled/renamed.example.com is how nginx serves old.example.com now.",
  )
  await expect(ask).toContainText(
    "old.example.com stops serving once nginx reloads, until it is enabled under its own name.",
  )
  // Nothing is sent until the operator says so.
  await ask.getByRole("button", { name: "Cancel" }).click()
  await expect(ask).toHaveCount(0)
  expect(posted).toEqual([])

  await (
    await openMenu(page, "renamed.example.com")
  )
    .getByRole("menuitem", { name: "Enable" })
    .click()
  await page
    .getByRole("dialog", { name: "Enable renamed.example.com" })
    .getByRole("button", { name: "Enable and reload" })
    .click()
  await expect(page.getByText("renamed.example.com enabled", { exact: true })).toBeVisible()
  expect(posted).toEqual(["renamed.example.com"])

  // A file no listed site owns is named by its path.
  await (
    await openMenu(page, "moved.example.com")
  )
    .getByRole("menuitem", { name: "Enable" })
    .click()
  const moved = page.getByRole("dialog", { name: "Enable moved.example.com" })
  await expect(moved).toContainText(
    "sites-enabled/moved.example.com is how nginx serves /srv/app/deploy/nginx.conf now.",
  )
  await expect(moved).toContainText("nginx stops reading that file once it reloads")
  await page.keyboard.press("Escape")
  await expect(moved).toHaveCount(0)

  // Read through conf.d as well, the target loses nothing: no question.
  await (await openMenu(page, "kept.example.com")).getByRole("menuitem", { name: "Enable" }).click()
  await expect(page.getByText("kept.example.com enabled", { exact: true })).toBeVisible()
  await expect(page.getByRole("dialog", { name: "Enable kept.example.com" })).toHaveCount(0)
  expect(posted).toEqual(["renamed.example.com", "kept.example.com"])
})

test("a site served through a link under another name reads serving and names that link", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await serveSites(page, [numbered, legacy])
  await page.goto("/proxy/sites")

  const site = card(page, "default")
  await expect(site.getByText("serving", { exact: true })).toBeVisible()
  await expect(site).toContainText("nginx serves this file through sites-enabled/00-default.")
  await expect(page.getByText("every site serving")).toBeVisible()
  const menu = await openMenu(page, "default")
  // Deleting it would leave 00-default pointing at nothing.
  await expect(menu.getByRole("menuitem", { name: "Delete" })).toHaveCount(0)
  await menu.getByRole("menuitem", { name: "Disable" }).click()
  await expect(page.getByRole("dialog", { name: "Disable default" })).toContainText(
    "Disabling removes sites-enabled/00-default, which serves it under another name.",
  )
  await page.keyboard.press("Escape")

  await page.goto("/proxy")
  await expect(page.getByText("default is on disk but not serving")).toHaveCount(0)
})

test("the overview names a link to nothing as critical", async ({ page }) => {
  await mockProxy(page, { included: true })
  await serveSites(page, layouts)
  await page.goto("/proxy")
  await expect(page.getByText("sites-enabled/ghost points at a file that is gone")).toBeVisible()
  // Broken rather than merely "on disk but not serving".
  await expect(page.getByText("renamed.example.com is on disk but not serving")).toHaveCount(0)
  await expect(
    page.getByText("renamed.example.com is not what nginx serves under its name"),
  ).toBeVisible()
})

test("the password-file warning says what nginx really does without the file", async ({ page }) => {
  await mockShowcase(page)
  await page.goto("/proxy/sites")
  await page.getByRole("button", { name: "More actions", exact: true }).click()
  await page.getByRole("menuitem", { name: "Delete file" }).click()
  const confirm = page.getByRole("dialog", { name: "Delete staging" })
  await expect(confirm).toContainText(
    "A site still pointing at this file keeps serving and refuses every login",
  )
  await expect(confirm).not.toContainText("stops nginx from starting")
})

for (const width of [390, 1280]) {
  test(`every kind of entry fits the sites list at ${width}`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 1000 })
    await mockProxy(page, { included: true })
    await serveSites(page, [...layouts, copied, outside, numbered])
    await page.goto("/proxy/sites")
    await expect(card(page, "ghost")).toBeVisible()
    await expect(card(page, "outside.example.com")).toContainText("/srv/app/deploy/nginx.conf")
    expect(
      await page
        .locator("[data-slot='page']")
        .evaluate((element) => element.scrollWidth <= element.clientWidth + 1),
    ).toBe(true)
    await page.screenshot({ path: testInfo.outputPath(`sites-${width}.png`), fullPage: true })
  })
}
