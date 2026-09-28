import { expect, test, type Locator, type Page, type Route } from "@playwright/test"
import { availability, json, mockProxy, user, vhosts } from "./proxy-fixtures"
import { longJournal, nginxUnit } from "./fixtures/proxy/engine"

/**
 * The overview and the engine it drives, for the ways they told an operator
 * something untrue: a list of findings that read "all within limits" while
 * the sites could not be read, a blank page when the proxy status could not,
 * a Restart that took nginx down over a broken file, Test and Reload running
 * the host's caddy for a Docker ingress, and a Caddy "running as a container"
 * that had not been started yet.
 */

function failWith(status: number, error: Record<string, unknown>) {
  return (route: Route) =>
    route.fulfill({ status, contentType: "application/json", body: JSON.stringify({ error }) })
}

/** What GET /proxy/config answers for a file removed since the list was read. */
const fileGone = failWith(404, {
  code: "not_found",
  message: "That file is not on disk.",
  reason:
    "It may have been removed or renamed since this page last loaded — by a site change, a deploy, or somebody in a shell.",
  raw: "open /etc/nginx/sites-available/app.example.com: no such file or directory",
  retryable: true,
})

async function mockStatus(page: Page, status: Record<string, unknown>) {
  await page.route("**/api/v1/proxy/status", (route) => json(route, status))
}

test("a source the overview cannot read is reported, never cleared", async ({ page }) => {
  await mockProxy(page, { included: true })
  let sitesFail = true
  await page.route("**/api/v1/proxy/vhosts", (route) =>
    sitesFail
      ? failWith(500, {
          code: "internal",
          message: "could not determine Docker ingress ownership",
        })(route)
      : json(route, vhosts),
  )
  await page.route(
    "**/api/v1/certificates/",
    failWith(500, { code: "internal", message: "internal error" }),
  )
  await page.goto("/proxy")

  // The list says what it could not see, and never the all-clear.
  await expect(page.getByText("Sites could not be read")).toBeVisible()
  await expect(page.getByText("Certificates could not be read")).toBeVisible()
  await expect(page.getByText(/all within limits/)).toHaveCount(0)
  // The tiles say so rather than showing a bare dash.
  const sitesTile = page.locator("a[aria-label='Sites'] [data-slot='stat-tile']")
  await expect(sitesTile).toContainText("couldn't read")
  // The reason is on the hint for a pointer resting on the tile.
  await expect(sitesTile.getByText("couldn't read")).toHaveAttribute(
    "title",
    "could not determine Docker ingress ownership",
  )
  await expect(page.locator("a[aria-label='Certificates'] [data-slot='stat-tile']")).toContainText(
    "couldn't read",
  )
  // Routes and expiry show the failure, not an empty host.
  await expect(
    page.getByRole("alert").filter({ hasText: "could not determine Docker ingress ownership" }),
  ).toBeVisible()
  await expect(page.getByText("Nothing configured yet")).toHaveCount(0)
  await expect(page.getByText("No certificates were found on this host.")).toHaveCount(0)

  // The finding's own button reads the source again.
  sitesFail = false
  await page.getByText("Sites could not be read").click()
  await page.getByRole("button", { name: "Try again" }).click()
  await expect(
    page.getByRole("list", { name: "Sites" }).locator("[data-slot='choice-row']"),
  ).toHaveCount(3)
  await expect(page.getByText("Sites could not be read")).toHaveCount(0)
  await expect(sitesTile).toContainText("3")
})

test("a source that fails after answering is not judged from its last answer", async ({ page }) => {
  await mockProxy(page, { included: true })
  let sitesFail = false
  await page.route("**/api/v1/proxy/vhosts", (route) =>
    sitesFail
      ? failWith(500, {
          code: "internal",
          message: "could not determine Docker ingress ownership",
        })(route)
      : json(route, vhosts),
  )
  await page.goto("/proxy")

  const sitesTile = page.locator("a[aria-label='Sites'] [data-slot='stat-tile']")
  const plainText = page.getByText("legacy.example.com serves an application in plain text")
  await expect(plainText).toBeVisible()
  await expect(sitesTile).toContainText("3")

  // Reload reads the sites again, and this time they fail. The poll keeps its
  // last answer, and the page went on judging it: the old plain-text finding
  // and the old count stayed up beside "Sites could not be read".
  sitesFail = true
  await page.locator("[data-slot='host-identity']").getByRole("button", { name: "Reload" }).click()
  await expect(page.getByText("nginx reloaded")).toBeVisible()
  await expect(page.getByText("Sites could not be read")).toBeVisible()
  await expect(plainText).toHaveCount(0)
  await expect(sitesTile).toContainText("—")
  await expect(sitesTile).toContainText("couldn't read")
  await expect(sitesTile).not.toContainText("3")
  await expect(
    page.getByRole("alert").filter({ hasText: "could not determine Docker ingress ownership" }),
  ).toBeVisible()
})

test("the overview says why when the proxy status cannot be read", async ({ page }) => {
  await mockProxy(page, { included: true })
  let statusFails = true
  await page.route("**/api/v1/proxy/status", (route) =>
    statusFails
      ? failWith(503, {
          code: "unavailable",
          message: "The proxy status could not be read",
          retryable: true,
        })(route)
      : json(route, availability),
  )
  await page.goto("/proxy")

  // It rendered nothing at all.
  await expect(
    page.getByRole("alert").filter({ hasText: "The proxy status could not be read" }),
  ).toBeVisible()
  await expect(page.locator("[data-slot='host-identity']")).toHaveCount(0)

  statusFails = false
  await page.getByRole("button", { name: "Try again" }).click()
  await expect(page.locator("[data-slot='host-identity']").getByText("nginx/1.26.3")).toBeVisible()
  await expect(
    page.getByRole("alert").filter({ hasText: "The proxy status could not be read" }),
  ).toHaveCount(0)
})

test("a finding's button names where it leads", async ({ page }) => {
  await mockProxy(page, { included: true })
  await page.goto("/proxy")

  // One finding open at a time, so each button found is that finding's.
  const open = async (title: string, label: string) => {
    await page.getByText(title).click()
    const button = page.getByRole("button", { name: label, exact: true })
    await expect(button).toBeVisible()
    // It was `Open ${meta}`: "Open renewal", "Open stream" for a list. "Open
    // certificate" is kept for a finding that links to one certificate.
    await expect(page.getByRole("button", { name: /^Open (renewal|stream)$/ })).toHaveCount(0)
    await page.getByText(title).click()
    await expect(button).toHaveCount(0)
  }
  await open("old.example.com has expired", "Open certificates")
  await open("Nothing is scheduled to renew certbot's certificates", "Open certificates")
  await open("PostgreSQL answers on every interface", "Open ports")

  await page.getByText("legacy.example.com serves an application in plain text").click()
  await page.getByRole("button", { name: "Open site", exact: true }).click()
  await expect(page).toHaveURL(/\/proxy\/sites\?site=legacy\.example\.com$/)
})

/** What nginx 1.26 prints for a directive it does not know, and the test's reading of it. */
const brokenTest = {
  valid: false,
  output:
    'nginx: [emerg] unknown directive "frobnicate" in /etc/nginx/sites-enabled/app:3\nnginx: configuration file /etc/nginx/nginx.conf test failed',
  command: "nginx -t",
  diagnostics: [
    {
      level: "emerg",
      message: 'unknown directive "frobnicate"',
      file: "/etc/nginx/sites-available/app",
      line: 3,
    },
  ],
  warnings: 0,
}

/** A deprecation nginx places in one site ahead of the emergency in another. */
const twoFindings = {
  ...brokenTest,
  output:
    'nginx: [warn] the "listen ... http2" directive is deprecated, use the "http2" directive instead in /etc/nginx/sites-enabled/legacy:2\n' +
    brokenTest.output,
  diagnostics: [
    {
      level: "warn",
      message: 'the "listen ... http2" directive is deprecated, use the "http2" directive instead',
      file: "/etc/nginx/sites-available/legacy",
      line: 2,
    },
    ...brokenTest.diagnostics,
  ],
  warnings: 1,
}

/** A start or restart the config test turned down, as the engine route answers it. */
function refusedBy(action: "started" | "restarted", validation = brokenTest) {
  return (route: Route) =>
    route.fulfill({
      status: 422,
      contentType: "application/json",
      body: JSON.stringify({
        error: {
          code: "invalid_config",
          message: `nginx was not ${action}: its configuration test failed.\n${validation.output}`,
        },
        validation,
      }),
    })
}

/** nginx.service in the given state, answered in place of the running one. */
async function mockUnit(page: Page, overrides: Record<string, unknown>) {
  await page.route("**/api/v1/systemd/nginx.service", (route) =>
    json(route, { unit: { ...nginxUnit, activeSince: undefined, ...overrides }, properties: {} }),
  )
}

const stopped = { activeState: "inactive", subState: "dead" }
const failed = { activeState: "failed", subState: "failed", result: "exit-code", restarts: 3 }

/** Posts to the Services page's own routes, which the engine's controls must never use. */
async function watchSystemd(page: Page) {
  const posted: string[] = []
  await page.route("**/api/v1/systemd/nginx.service/*", (route) => {
    if (route.request().method() !== "POST") return route.fallback()
    posted.push(route.request().url())
    return json(route, {})
  })
  return posted
}

test("restart is refused with nginx's own reason when its configuration fails", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  const systemd = await watchSystemd(page)
  const engine: string[] = []
  await page.route("**/api/v1/proxy/engine/restart", (route) => {
    engine.push(route.request().method())
    return refusedBy("restarted")(route)
  })
  const reads: string[] = []
  await page.route("**/api/v1/proxy/config?**", (route) => {
    reads.push(new URL(route.request().url()).searchParams.get("path") ?? "")
    return json(route, { content: "server {\n    listen 80;\n    frobnicate on;\n}\n" })
  })
  await page.goto("/proxy")

  await page.getByRole("button", { name: "More nginx actions" }).click()
  await page.getByRole("menuitem", { name: "Restart nginx", exact: true }).click()
  const dialog = page.getByRole("dialog", { name: "Restart nginx" })
  await expect(dialog.getByText(/configuration is tested first/)).toBeVisible()
  await dialog.getByRole("button", { name: "Restart", exact: true }).click()

  // The dialog stays, on nginx's own line placed at its file and line; the
  // toast of the whole output it used to be is gone in twelve seconds.
  await expect(
    dialog.getByText("nginx was not restarted: its configuration test failed."),
  ).toBeVisible()
  await expect(dialog.getByText(/Nothing nginx serves was interrupted/)).toBeVisible()
  const said = dialog.getByRole("list", { name: "What the test said" })
  await expect(said.getByText('unknown directive "frobnicate"')).toBeVisible()
  await expect(said.getByText("emerg", { exact: true })).toBeVisible()
  await expect(said.getByText("/etc/nginx/sites-available/app:3")).toBeVisible()
  // The whole output is there too, folded.
  await dialog.getByText("nginx -t output", { exact: true }).click()
  await expect(
    dialog.getByText(/configuration file \/etc\/nginx\/nginx.conf test failed/),
  ).toBeVisible()
  expect(engine).toEqual(["POST"])
  expect(systemd).toEqual([])

  // The file is one press away, at the line nginx named.
  await said.getByRole("button", { name: "Open at line 3" }).click()
  await expect(page.getByRole("dialog", { name: "Restart nginx" })).toHaveCount(0)
  const editor = page.getByRole("dialog", { name: /app/ })
  await expect(editor.locator(".monaco-editor .view-lines")).toContainText("frobnicate on;", {
    timeout: 20_000,
  })
  expect(reads).toEqual(["/etc/nginx/sites-available/app"])
  await expect(editor.getByRole("button", { name: "Save and reload" })).toBeVisible()
})

test("a stopped nginx leads with Start, and Reload says why it cannot run", async ({ page }) => {
  await mockProxy(page, { included: true })
  await mockUnit(page, stopped)
  const systemd = await watchSystemd(page)
  const started: string[] = []
  let release = () => {}
  const held = new Promise<void>((resolve) => (release = resolve))
  await page.route("**/api/v1/proxy/engine/start", async (route) => {
    started.push(route.request().method())
    await held
    // Once started, the unit reads as running.
    await mockUnit(page, { activeState: "active", subState: "running" })
    return json(route, { action: "start", unit: "nginx.service", output: "" })
  })
  await page.goto("/proxy")

  const identity = page.locator("[data-slot='host-identity']")
  await expect(identity.getByText("not running", { exact: true })).toBeVisible()
  await expect(identity.getByText("inactive (dead)")).toHaveCount(0)
  const reload = identity.getByRole("button", { name: "Reload" })
  await expect(reload).toBeDisabled()
  await reload.hover({ force: true })
  await expect(page.getByRole("tooltip")).toHaveText("nginx is not running")
  // Stopped, it has nothing to restart or stop.
  await identity.getByRole("button", { name: "More nginx actions" }).click()
  await expect(page.getByRole("menuitem", { name: /Restart|Stop/ })).toHaveCount(0)
  await expect(page.getByRole("menuitem", { name: "Service details" })).toBeVisible()
  await page.keyboard.press("Escape")

  const start = identity.getByRole("button", { name: "Start nginx" })
  // The one brand command on the line.
  await expect(start).toHaveAttribute("data-variant", "default")
  await start.click()
  await expect(identity.getByText("Starting…").first()).toBeVisible()
  release()
  await expect(page.getByText("nginx started")).toBeVisible()
  await expect(identity.getByText("running", { exact: true })).toBeVisible()
  await expect(identity.getByRole("button", { name: "Reload" })).toBeEnabled()
  await expect(identity.getByRole("button", { name: "Start nginx" })).toHaveCount(0)
  expect(started).toEqual(["POST"])
  expect(systemd).toEqual([])
})

test("a start the config test refuses opens on what it said, and Start tries again", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await mockUnit(page, stopped)
  let broken = true
  const started: string[] = []
  await page.route("**/api/v1/proxy/engine/start", (route) => {
    started.push(route.request().method())
    return broken
      ? refusedBy("started")(route)
      : json(route, { action: "start", unit: "nginx.service", output: "" })
  })
  await page.goto("/proxy")

  await page.getByRole("button", { name: "Start nginx" }).click()
  const dialog = page.getByRole("dialog", { name: "Start nginx" })
  await expect(
    dialog.getByText("nginx was not started: its configuration test failed."),
  ).toBeVisible()
  await expect(dialog.getByText("Fix what the test found, then start nginx again.")).toBeVisible()
  await expect(dialog.getByText("/etc/nginx/sites-available/app:3")).toBeVisible()
  await expect(dialog.getByRole("button", { name: "Open at line 3" })).toBeVisible()
  // No toast carries the refusal: the dialog does.
  await expect(page.getByText("nginx did not start")).toHaveCount(0)

  // Fixed in another window, the dialog's own Start tries again.
  broken = false
  const again = dialog.getByRole("button", { name: "Start", exact: true })
  await expect(again).toHaveAttribute("data-variant", "default")
  await again.click()
  await expect(page.getByText("nginx started")).toBeVisible()
  await expect(dialog).toHaveCount(0)
  expect(started).toEqual(["POST", "POST"])
})

test("a file opened from a refusal closes back into it, with the keyboard on its line", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await mockUnit(page, stopped)
  let broken = true
  const started: string[] = []
  await page.route("**/api/v1/proxy/engine/start", (route) => {
    started.push(route.request().method())
    return broken
      ? refusedBy("started", twoFindings)(route)
      : json(route, { action: "start", unit: "nginx.service", output: "" })
  })
  const reads: string[] = []
  await page.route("**/api/v1/proxy/config?**", (route) => {
    reads.push(new URL(route.request().url()).searchParams.get("path") ?? "")
    return json(route, { content: "server {\n    listen 80;\n    frobnicate on;\n}\n" })
  })
  await page.goto("/proxy")

  await page.getByRole("button", { name: "Start nginx" }).click()
  const dialog = page.getByRole("dialog", { name: "Start nginx" })
  await expect(dialog.getByRole("button", { name: "Open at line 2" })).toBeVisible()
  // The second line's file, by keyboard: the first is not the one to fix.
  await dialog.getByRole("button", { name: "Open at line 3" }).focus()
  await page.keyboard.press("Enter")
  await expect(dialog).toHaveCount(0)
  const editor = page.getByRole("dialog", { name: /app/ })
  await expect(editor.locator(".monaco-editor .view-lines")).toContainText("frobnicate on;", {
    timeout: 20_000,
  })
  await page.keyboard.press("Escape")
  await expect(editor).toHaveCount(0)

  // Closing the file used to close the refusal for good and leave the
  // keyboard on the page's body; it comes back where the reader left it.
  await expect(
    dialog.getByText("nginx was not started: its configuration test failed."),
  ).toBeVisible()
  await expect(dialog.getByRole("button", { name: "Open at line 3" })).toBeFocused()
  expect(reads).toEqual(["/etc/nginx/sites-available/app"])

  // Fixed there, the dialog's own Start tries again.
  broken = false
  await dialog.getByRole("button", { name: "Start", exact: true }).click()
  await expect(page.getByText("nginx started")).toBeVisible()
  await expect(dialog).toHaveCount(0)
  expect(started).toEqual(["POST", "POST"])
})

test("a fix saved while nginx is stopped is saved, and says it was not reloaded", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await mockUnit(page, stopped)
  await page.route("**/api/v1/proxy/engine/start", refusedBy("started"))
  await page.route("**/api/v1/proxy/config?**", (route) =>
    json(route, { content: "server {\n    listen 80;\n    frobnicate on;\n}\n" }),
  )
  const saved: { reload: boolean; content: string }[] = []
  await page.route("**/api/v1/proxy/config", (route) => {
    if (route.request().method() !== "PUT") return route.fallback()
    saved.push(route.request().postDataJSON())
    // The file passed its test and was written; the reload had no nginx to signal.
    return failWith(502, {
      code: "reload_failed",
      message:
        'reload failed: nginx: [error] open() "/run/nginx.pid" failed (2: No such file or directory)',
    })(route)
  })
  await page.goto("/proxy")

  await page.getByRole("button", { name: "Start nginx" }).click()
  await page.getByRole("button", { name: "Open at line 3" }).click()
  const editor = page.getByRole("dialog", { name: /app/ })
  const lines = editor.locator(".monaco-editor .view-lines")
  await expect(lines).toContainText("frobnicate on;", { timeout: 20_000 })
  await lines.click()
  // Keys sent before Monaco has focus are lost on a busy machine.
  await expect(editor.locator(".monaco-editor.focused")).toBeVisible()
  await page.keyboard.press("End")
  await page.keyboard.type("fixed")
  await expect(lines).toContainText("fixed")
  await editor.getByRole("button", { name: "Save and reload" }).click()

  // It was "Not applied" over a file that had been written, with the buffer
  // still marked unsaved.
  await expect(page.getByText("Saved, not reloaded")).toBeVisible()
  await expect(page.getByText(/\/run\/nginx\.pid" failed/)).toBeVisible()
  await expect(page.getByText("Not applied")).toHaveCount(0)
  await expect(editor.getByRole("button", { name: "Save and reload" })).toBeDisabled()
  expect(saved).toHaveLength(1)
  expect(saved[0].reload).toBe(true)
  expect(saved[0].content).toContain("fixed")
})

test("a start systemd itself fails is said in its words", async ({ page }) => {
  await mockProxy(page, { included: true })
  await mockUnit(page, stopped)
  await page.route(
    "**/api/v1/proxy/engine/start",
    failWith(502, {
      code: "command_failed",
      message:
        "systemctl exited 1: Job for nginx.service failed because the control process exited with error code.",
    }),
  )
  await page.goto("/proxy")
  await page.getByRole("button", { name: "Start nginx" }).click()
  await expect(page.getByText("nginx did not start")).toBeVisible()
  await expect(page.getByText(/Job for nginx.service failed/)).toBeVisible()
  await expect(page.getByRole("dialog")).toHaveCount(0)
})

test("stop says what it takes offline and goes through the engine route", async ({ page }) => {
  await mockProxy(page, { included: true })
  const systemd = await watchSystemd(page)
  const stops: string[] = []
  await page.route("**/api/v1/proxy/engine/stop", (route) => {
    stops.push(route.request().method())
    return json(route, { action: "stop", unit: "nginx.service", output: "" })
  })
  await page.goto("/proxy")

  await page.getByRole("button", { name: "More nginx actions" }).click()
  await page.getByRole("menuitem", { name: "Stop nginx", exact: true }).click()
  const dialog = page.getByRole("dialog", { name: "Stop nginx" })
  await expect(dialog.getByText(/goes offline until it is started again/)).toBeVisible()
  await dialog.getByRole("button", { name: "Cancel" }).click()
  await expect(dialog).toHaveCount(0)
  expect(stops).toEqual([])

  await page.getByRole("button", { name: "More nginx actions" }).click()
  await page.getByRole("menuitem", { name: "Stop nginx", exact: true }).click()
  await dialog.getByRole("button", { name: "Stop", exact: true }).click()
  await expect(page.getByText("nginx stopped")).toBeVisible()
  await expect(dialog).toHaveCount(0)
  expect(stops).toEqual(["POST"])
  expect(systemd).toEqual([])
})

test("an engine that won't start at boot says so, and Start at boot enables it", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await mockUnit(page, { unitFileState: "disabled", enabled: false })
  const enabled: string[] = []
  await page.route("**/api/v1/proxy/engine/enable", async (route) => {
    enabled.push(route.request().method())
    await mockUnit(page, { unitFileState: "enabled", enabled: true })
    return json(route, { action: "enable", unit: "nginx.service", output: "" })
  })
  await page.goto("/proxy")

  const identity = page.locator("[data-slot='host-identity']")
  await expect(identity.getByText("won't start at boot")).toBeVisible()
  await identity.getByRole("button", { name: "Start at boot" }).click()
  await expect(page.getByText("nginx starts at boot")).toBeVisible()
  await expect(identity.getByText("starts at boot", { exact: true })).toBeVisible()
  await expect(identity.getByText("won't start at boot")).toHaveCount(0)
  await expect(identity.getByRole("button", { name: "Start at boot" })).toHaveCount(0)
  expect(enabled).toEqual(["POST"])
})

test("a masked engine is not offered a start or a boot setting that cannot work", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await mockUnit(page, { ...stopped, unitFileState: "masked", enabled: false })
  const posted: string[] = []
  await page.route("**/api/v1/proxy/engine/*", (route) => {
    posted.push(route.request().url())
    return route.fallback()
  })
  await page.goto("/proxy")

  const identity = page.locator("[data-slot='host-identity']")
  await expect(identity.getByText("masked, so it cannot start")).toBeVisible()
  await expect(identity.getByRole("button", { name: "Start at boot" })).toHaveCount(0)
  const start = identity.getByRole("button", { name: "Start nginx" })
  await expect(start).toBeDisabled()
  await start.hover({ force: true })
  await expect(page.getByRole("tooltip")).toHaveText("nginx.service is masked")
  expect(posted).toEqual([])
})

test("a failed engine says why, with its journal and a way to clear it", async ({ page }) => {
  await mockProxy(page, { included: true })
  await mockUnit(page, failed)
  const journals: string[] = []
  await page.route("**/api/v1/systemd/nginx.service/journal?**", (route) => {
    journals.push(new URL(route.request().url()).searchParams.get("lines") ?? "")
    return route.fallback()
  })
  const cleared: string[] = []
  await page.route("**/api/v1/proxy/engine/reset-failed", async (route) => {
    cleared.push(route.request().method())
    await mockUnit(page, { ...stopped, result: "success", restarts: 0 })
    return json(route, { action: "reset-failed", unit: "nginx.service", output: "" })
  })
  await page.goto("/proxy")

  const identity = page.locator("[data-slot='host-identity']")
  await expect(identity.getByText("failed", { exact: true })).toBeVisible()
  await expect(identity.getByRole("button", { name: "Start nginx" })).toBeVisible()
  await expect(identity.getByRole("button", { name: "Reload" })).toBeDisabled()

  await expect(page.getByText("nginx exited with an error")).toBeVisible()
  await expect(page.getByText("result exit-code · restarted 3 times by systemd")).toBeVisible()
  // The journal is read when the fold opens, not with the page.
  expect(journals).toEqual([])
  await page.getByRole("button", { name: "Last 30 journal lines" }).click()
  const journal = page.getByLabel("Journal")
  await expect(journal.getByText(/bind\(\) to 0\.0\.0\.0:80 failed/)).toBeVisible()
  await expect(journal.getByText(/Failed with result 'exit-code'/)).toBeVisible()
  expect(journals).toEqual(["30"])
  await page.getByRole("button", { name: "Read again" }).click()
  await expect.poll(() => journals.length).toBe(2)

  await expect(page.getByRole("link", { name: "Service details" })).toHaveAttribute(
    "href",
    "/processes/services?unit=nginx.service",
  )
  await page.getByRole("button", { name: "Clear failed state" }).click()
  await expect(page.getByText("nginx's failed state cleared")).toBeVisible()
  await expect(page.getByText("nginx exited with an error")).toHaveCount(0)
  await expect(identity.getByText("not running", { exact: true })).toBeVisible()
  expect(cleared).toEqual(["POST"])
})

test("the journal opens on its newest lines, where the reason is", async ({ page }) => {
  await mockProxy(page, { included: true })
  await mockUnit(page, failed)
  await page.route("**/api/v1/systemd/nginx.service/journal?**", (route) =>
    json(route, longJournal),
  )

  for (const width of [1280, 390]) {
    await page.setViewportSize({ width, height: 900 })
    await page.goto("/proxy")
    await page.getByRole("button", { name: "Last 30 journal lines" }).click()
    const journal = page.getByLabel("Journal")
    const lines = journal.locator("p")
    await expect(lines).toHaveCount(30)
    // Thirty lines overflow the box at either width, so where it opens matters.
    expect(await journal.evaluate((well) => well.scrollHeight > well.clientHeight)).toBe(true)
    // toBeVisible ignores clipping inside a scroll box: compare the boxes.
    const inside = async (line: Locator) => {
      const [outer, inner] = await Promise.all([journal.boundingBox(), line.boundingBox()])
      return (
        outer !== null &&
        inner !== null &&
        inner.y >= outer.y - 1 &&
        inner.y + inner.height <= outer.y + outer.height + 1
      )
    }
    const failedWith = journal.getByText(/Failed with result 'exit-code'/)
    await expect.poll(() => inside(failedWith)).toBe(true)
    expect(await inside(journal.getByText(/bind\(\) to 0\.0\.0\.0:80 failed/))).toBe(true)
    expect(await inside(lines.first())).toBe(false)

    // Read again opens on the newest lines too.
    await journal.evaluate((well) => (well.scrollTop = 0))
    expect(await inside(failedWith)).toBe(false)
    await page.getByRole("button", { name: "Read again" }).click()
    await expect.poll(() => inside(failedWith)).toBe(true)
  }
})

test("a journal that cannot be read says so and can be read again", async ({ page }) => {
  await mockProxy(page, { included: true })
  await mockUnit(page, failed)
  let unreadable = true
  await page.route("**/api/v1/systemd/nginx.service/journal?**", (route) =>
    unreadable
      ? failWith(502, { code: "command_failed", message: "journalctl exited 1: access denied" })(
          route,
        )
      : route.fallback(),
  )
  await page.goto("/proxy")
  await page.getByRole("button", { name: "Last 30 journal lines" }).click()
  const failure = page.getByRole("alert").filter({ hasText: "journalctl exited 1: access denied" })
  await expect(failure).toBeVisible()
  unreadable = false
  await page.getByRole("button", { name: "Read again" }).click()
  await expect(
    page.getByLabel("Journal").getByText(/bind\(\) to 0\.0\.0\.0:80 failed/),
  ).toBeVisible()
  await expect(failure).toHaveCount(0)
})

test("a reader sees a failed engine's reason and journal, and none of its controls", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 900 })
  await mockProxy(page, { included: true })
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, { ...user, capabilities: ["read"], user: { ...user.user, role: "viewer" } }),
  )
  await mockUnit(page, { ...failed, unitFileState: "disabled", enabled: false })
  await page.goto("/proxy")

  const identity = page.locator("[data-slot='host-identity']")
  await expect(identity.getByText("won't start at boot")).toBeVisible()
  await expect(identity.getByRole("button")).toHaveCount(0)
  await expect(page.getByText("nginx exited with an error")).toBeVisible()
  await expect(page.getByRole("button", { name: "Clear failed state" })).toHaveCount(0)
  await page.getByRole("button", { name: "Last 30 journal lines" }).click()
  await expect(
    page.getByLabel("Journal").getByText(/bind\(\) to 0\.0\.0\.0:80 failed/),
  ).toBeVisible()
  await expect(page.getByRole("link", { name: "Service details" })).toBeVisible()
  const overflow = await page
    .locator("[data-slot='page']")
    .evaluate((element) => element.scrollWidth > element.clientWidth + 1)
  expect(overflow).toBe(false)
})

test("the stopped engine's controls and a refusal fit a phone", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockProxy(page, { included: true })
  await mockUnit(page, { ...failed, unitFileState: "disabled", enabled: false })
  await page.route("**/api/v1/proxy/engine/start", refusedBy("started"))
  await page.goto("/proxy")

  await page.getByRole("button", { name: "Last 30 journal lines" }).click()
  await expect(page.getByLabel("Journal")).toBeVisible()
  const pageOverflow = await page
    .locator("[data-slot='page']")
    .evaluate((element) => element.scrollWidth > element.clientWidth + 1)
  expect(pageOverflow).toBe(false)

  await page.getByRole("button", { name: "Start nginx" }).click()
  const dialog = page.getByRole("dialog", { name: "Start nginx" })
  await expect(dialog.getByText("/etc/nginx/sites-available/app:3")).toBeVisible()
  await expect(dialog).toBeInViewport({ ratio: 1 })
  const dialogOverflow = await dialog.evaluate(
    (element) => element.scrollWidth > element.clientWidth + 1,
  )
  expect(dialogOverflow).toBe(false)
  await page.keyboard.press("Escape")
  await expect(dialog).toHaveCount(0)
})

test("a running Docker Caddy ingress is tested and reloaded inside its container", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await mockStatus(page, {
    ...availability,
    nginx: false,
    nginxVersion: "",
    caddy: true,
    caddyVersion: "v2.8.4",
    certbot: false,
    ingressContainer: "edge",
    ingressState: "running",
  })
  const kinds: string[] = []
  for (const path of ["test", "reload"]) {
    await page.route(`**/api/v1/proxy/${path}`, (route) => {
      kinds.push(`${path} ${route.request().postDataJSON().kind}`)
      return route.fallback()
    })
  }
  await page.goto("/proxy")

  const identity = page.locator("[data-slot='host-identity']")
  await expect(identity.getByText("runs as a container")).toBeVisible()
  await expect(identity.getByText("edge", { exact: true })).toBeVisible()
  // The host's Caddyfile path is not the ingress's.
  await expect(identity.getByText("/etc/caddy/Caddyfile")).toHaveCount(0)

  await identity.getByRole("button", { name: "Test config" }).click()
  await expect(page.getByText("Caddy's configuration is valid")).toBeVisible()
  await identity.getByRole("button", { name: "Reload" }).click()
  await expect(page.getByText("Caddy reloaded")).toBeVisible()
  expect(kinds).toEqual(["test caddy-ingress", "reload caddy-ingress"])
})

test("an ingress the first deployment would start is not offered controls", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 900 })
  await mockProxy(page, { included: true })
  await mockStatus(page, {
    ...availability,
    nginx: false,
    nginxVersion: "",
    caddy: false,
    certbot: false,
    ingressState: "provisionable",
  })
  await page.goto("/proxy")

  const identity = page.locator("[data-slot='host-identity']")
  await expect(identity.getByText("nothing found on this host")).toBeVisible()
  await expect(identity.getByText("a Caddy ingress starts with the first deployment")).toBeVisible()
  await expect(identity.getByText("runs as a container")).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Test config" })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Reload" })).toHaveCount(0)
  const overflow = await page
    .locator("[data-slot='page']")
    .evaluate((element) => element.scrollWidth > element.clientWidth + 1)
  expect(overflow).toBe(false)
})

/** A TLS nginx site, for lists long enough to be cut short. */
function site(name: string, overrides: Record<string, unknown> = {}) {
  return {
    ...vhosts[0],
    name,
    path: `/etc/nginx/sites-available/${name}`,
    enabledPath: `/etc/nginx/sites-enabled/${name}`,
    serverNames: [name],
    ...overrides,
  }
}

/**
 * Ten routes named so the alphabet puts the plain-HTTP site last, with a
 * Caddyfile site and a Docker ingress route among them.
 */
const tenRoutes = [
  site("alpha.example.com"),
  {
    ...vhosts[0],
    name: "blog.example.com",
    kind: "caddy",
    path: "/etc/caddy/Caddyfile",
    enabledPath: undefined,
    serverNames: ["blog.example.com"],
    certPath: undefined,
  },
  vhosts[2],
  ...["one", "two", "three", "four", "five", "six"].map((n) => site(`${n}.example.com`)),
  site("zz-plain.example.com", { tls: false, listen: ["80"], certPath: undefined }),
]

test("the routes lead with what needs attention and say how many there are", async ({ page }) => {
  await mockProxy(page, { included: true })
  await page.route("**/api/v1/proxy/vhosts", (route) => json(route, tenRoutes))
  await page.goto("/proxy")

  const routes = page.getByRole("list", { name: "Sites" })
  const cards = routes.locator("[data-slot='choice-row']")
  await expect(cards).toHaveCount(8)
  // The list was the first eight by name and never said there were more.
  await expect(page.getByText("Showing 8 of 10", { exact: true })).toBeVisible()
  await expect(cards.first()).toContainText("zz-plain.example.com")
  // A Caddyfile site and a Docker ingress route were both "Caddy route".
  await expect(cards.filter({ hasText: "blog.example.com" })).toContainText("Caddyfile")
  await expect(cards.filter({ hasText: "just-dashboard-shop" })).toContainText(
    "Docker Caddy ingress",
  )

  // An administrator opens a site on the Sites page, and an ingress route —
  // which has no file there to open — as its live TLS report.
  await expect(routes.getByRole("link", { name: "Open alpha.example.com" })).toHaveAttribute(
    "href",
    "/proxy/sites?site=alpha.example.com",
  )
  await expect(routes.getByRole("link", { name: "Open blog.example.com" })).toHaveAttribute(
    "href",
    "/proxy/sites?site=blog.example.com",
  )

  // The count and the freshness line fit a phone beside what they sit with.
  await page.setViewportSize({ width: 390, height: 900 })
  await expect(page.getByText("Showing 8 of 10", { exact: true })).toBeVisible()
  await expect(page.getByText(/^Updated (just now|\d+s ago)$/)).toBeVisible()
  const overflow = await page
    .locator("[data-slot='page']")
    .evaluate((element) => element.scrollWidth > element.clientWidth + 1)
  expect(overflow).toBe(false)

  await routes.getByRole("link", { name: "TLS report for shop.example.com" }).click()
  await expect(page).toHaveURL(/\/proxy\/tls\?domain=shop\.example\.com$/)
})

test("a read-only account reads a route's file and is never sent to the site form", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, { ...user, capabilities: ["read"], user: { ...user.user, role: "viewer" } }),
  )
  const reads: string[] = []
  await page.route("**/api/v1/proxy/config?**", (route) => {
    reads.push(new URL(route.request().url()).searchParams.get("path") ?? "")
    return json(route, { content: "server {\n    listen 443 ssl;\n    # served-from-disk\n}\n" })
  })
  await page.goto("/proxy")

  const routes = page.getByRole("list", { name: "Sites" })
  await expect(routes.locator("[data-slot='choice-row']")).toHaveCount(3)
  // Nothing on the list leads to the Sites page's form.
  await expect(routes.locator("a[href*='/proxy/sites?site=']")).toHaveCount(0)
  // The ingress route has nothing a reader can open: its report needs an administrator.
  await expect(routes.getByRole("link", { name: /TLS report/ })).toHaveCount(0)
  await expect(routes.getByRole("button", { name: /just-dashboard-shop/ })).toHaveCount(0)

  await routes.getByRole("button", { name: "View app.example.com", exact: true }).click()
  const sheet = page.getByRole("dialog")
  // The editor loads on first use, which takes a while on a busy machine.
  await expect(sheet.locator(".monaco-editor .view-lines")).toContainText("served-from-disk", {
    timeout: 20_000,
  })
  expect(reads).toEqual(["/etc/nginx/sites-available/app.example.com"])
  await expect(sheet.getByRole("button", { name: /Save/ })).toHaveCount(0)
  await expect(sheet.getByRole("button", { name: "Test config" })).toHaveCount(0)
  await expect(page).toHaveURL(/\/proxy$/)
  await page.keyboard.press("Escape")
  await expect(sheet).toHaveCount(0)
})

test("a reader's file view shows its read in flight and why it failed, never an empty file", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockProxy(page, { included: true })
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, { ...user, capabilities: ["read"], user: { ...user.user, role: "viewer" } }),
  )
  let hold = Promise.resolve()
  let release = () => {}
  let gone = true
  await page.route("**/api/v1/proxy/config?**", async (route) => {
    await hold
    return gone
      ? fileGone(route)
      : json(route, { content: "server {\n    # served-from-disk\n}\n" })
  })
  await page.goto("/proxy")

  hold = new Promise((resolve) => (release = resolve))
  const routes = page.getByRole("list", { name: "Sites" })
  await routes.getByRole("button", { name: "View app.example.com", exact: true }).click()
  const sheet = page.getByRole("dialog")
  // While the read is out there is no editor to take for an empty file.
  await expect(sheet.locator("[data-slot='pane'][aria-busy='true']")).toBeVisible()
  await expect(sheet.locator(".monaco-editor")).toHaveCount(0)

  // A file removed since the list was read says so, and nothing else is drawn.
  release()
  const failure = sheet.getByRole("alert")
  await expect(failure).toContainText("That file is not on disk.")
  await expect(failure).toContainText("removed or renamed since this page last loaded")
  await expect(sheet.locator(".monaco-editor")).toHaveCount(0)
  await expect(sheet.locator("[data-slot='pane']")).toHaveCount(0)
  const overflow = await sheet.evaluate((element) => element.scrollWidth > element.clientWidth + 1)
  expect(overflow).toBe(false)

  // Once it is back, Try again reads it.
  gone = false
  await failure.getByRole("button", { name: "Try again" }).click()
  await expect(sheet.locator(".monaco-editor .view-lines")).toContainText("served-from-disk", {
    timeout: 20_000,
  })
  await expect(failure).toHaveCount(0)
})

test("the config editor offers nothing to save over a file it could not read", async ({ page }) => {
  await mockProxy(page, { included: true })
  let gone = true
  await page.route("**/api/v1/proxy/config?**", (route) =>
    gone ? fileGone(route) : json(route, { content: "server {\n    # served-from-disk\n}\n" }),
  )
  await page.goto("/proxy/sites")
  const card = page.locator("[data-slot='choice-row']").filter({ hasText: "app.example.com" })
  await card.getByRole("button", { name: "Raw config" }).click()

  const sheet = page.getByRole("dialog")
  await expect(sheet.getByRole("alert")).toContainText("That file is not on disk.")
  // An empty buffer was one keystroke from Save and reload over the real file.
  for (const name of ["Test config", "Discard", "Save only", "Save and reload"]) {
    await expect(sheet.getByRole("button", { name, exact: true })).toHaveCount(0)
  }
  await expect(sheet.getByText("Validated before it takes effect")).toHaveCount(0)

  gone = false
  await sheet.getByRole("button", { name: "Try again" }).click()
  await expect(sheet.locator(".monaco-editor .view-lines")).toContainText("served-from-disk", {
    timeout: 20_000,
  })
  await expect(sheet.getByRole("button", { name: "Test config" })).toBeEnabled()
  await expect(sheet.getByRole("button", { name: "Save and reload" })).toBeDisabled()
})

test("the header says how old the page is, and Refresh reads every source again", async ({
  page,
}) => {
  await page.clock.install()
  await mockProxy(page, { included: true })
  const asked: string[] = []
  page.on("request", (request) => {
    const path = new URL(request.url()).pathname.replace(/^\/api\/v1/, "")
    if (request.method() === "GET") asked.push(path)
  })
  // The sites answer when told to, so the refresh can be seen in flight.
  let hold = Promise.resolve()
  let release = () => {}
  await page.route("**/api/v1/proxy/vhosts", async (route) => {
    await hold
    return json(route, vhosts)
  })
  await page.goto("/proxy")

  const header = page.locator("[data-slot='page-context']")
  // Fresh reads carry a few seconds' age at most — the clock runs on while a
  // busy machine renders.
  const fresh = header.getByText(/^Updated (just now|[5-9]s ago)$/)
  await expect(fresh).toBeVisible()
  // The age is the oldest reading's, as the clock moves.
  await page.clock.fastForward(20_000)
  await expect(header.getByText(/^Updated [2-5]\ds ago$/)).toBeVisible()

  hold = new Promise((resolve) => (release = resolve))
  asked.length = 0
  const refresh = header.getByRole("button", { name: "Refresh" })
  await refresh.click()
  await expect(header.getByText("Refreshing…")).toBeVisible()
  await expect(refresh).toHaveAttribute("aria-busy", "true")
  // Every source the page shows is asked again, not only the fast ones.
  await expect
    .poll(() => asked, { timeout: 15_000 })
    .toEqual(
      expect.arrayContaining([
        "/certificates/",
        "/certificates/certbot",
        "/ports",
        "/proxy/status",
        "/proxy/streams/",
        "/proxy/vhosts",
        "/systemd/nginx.service",
      ]),
    )
  // The one still out keeps it refreshing.
  await expect(header.getByText("Refreshing…")).toBeVisible()
  release()
  await expect(fresh).toBeVisible()
  await expect(refresh).not.toHaveAttribute("aria-busy", "true")
})

test("the age is the oldest reading's, which is the five-minute certificate list", async ({
  page,
}) => {
  await page.clock.install()
  await mockProxy(page, { included: true })
  // No certbot, so the certificate list is the only reading on a five-minute poll.
  await mockStatus(page, { ...availability, certbot: false })
  await page.goto("/proxy")
  const header = page.locator("[data-slot='page-context']")
  await expect(header.getByText(/^Updated (just now|[5-9]s ago)$/)).toBeVisible()

  // Answered, not only asked: until the answers land every reading is as
  // old as the certificates, whatever the page counts.
  const answered: string[] = []
  page.on("requestfinished", (request) => {
    if (request.method() === "GET")
      answered.push(new URL(request.url()).pathname.replace(/^\/api\/v1/, ""))
  })
  // Past every thirty- and sixty-second poll, short of the certificates' five minutes.
  await page.clock.fastForward(75_000)
  await expect
    .poll(() => answered, { timeout: 15_000 })
    .toEqual(
      expect.arrayContaining([
        "/ports",
        "/proxy/status",
        "/proxy/streams/",
        "/proxy/vhosts",
        "/systemd/nginx.service",
      ]),
    )
  expect(answered).not.toContain("/certificates/")
  // A page that took its newest reading, or left the certificates out, read
  // "just now" here: everything else was read a moment ago. Asked twice, a
  // second apart, so the moment before the answers render cannot pass it.
  const age = header.getByText(/^Updated 1m [1-5]\ds ago$/)
  await expect(age).toBeVisible()
  await page.waitForTimeout(1_000)
  await expect(age).toBeVisible()
})

test("a source that never answers a refresh is named, and Refresh asks it again", async ({
  page,
}) => {
  await page.clock.install()
  await mockProxy(page, { included: true })
  let hang = false
  let release = () => {}
  const held = new Promise<void>((resolve) => (release = resolve))
  let siteReads = 0
  await page.route("**/api/v1/proxy/vhosts", async (route) => {
    siteReads += 1
    if (hang) {
      await held
      // The page gave up on this read long ago; there is nobody to answer.
      return json(route, vhosts).catch(() => {})
    }
    return json(route, vhosts)
  })
  await page.goto("/proxy")
  const header = page.locator("[data-slot='page-context']")
  const refresh = header.getByRole("button", { name: "Refresh" })
  await expect(header.getByText(/^Updated (just now|[5-9]s ago)$/)).toBeVisible()

  hang = true
  const before = siteReads
  await refresh.click()
  await expect(header.getByText("Refreshing…")).toBeVisible()
  await expect(refresh).toHaveAttribute("aria-busy", "true")
  await expect.poll(() => siteReads).toBe(before + 1)

  // Neither the client nor the server gives up on a read, so this one would
  // have kept the line on "Refreshing…" and Refresh disabled for good.
  await page.clock.fastForward(20_000)
  const late = header.getByText("No answer from sites", { exact: true })
  await expect(late).toBeVisible()
  await expect(header.getByText("Refreshing…")).toHaveCount(0)
  await expect(refresh).toBeEnabled()
  await expect(refresh).not.toHaveAttribute("aria-busy", "true")

  // Pressing it again abandons the read that never came back and asks afresh.
  hang = false
  await refresh.click()
  await expect.poll(() => siteReads).toBe(before + 2)
  await expect(header.getByText(/^Updated (just now|[5-9]s ago)$/)).toBeVisible()
  await expect(late).toHaveCount(0)
  release()
})

test("an expiry bar leads to its certificate", async ({ page }) => {
  await mockProxy(page, { included: true })
  await page.goto("/proxy")

  await page.getByRole("button", { name: "Show old.example.com in Certificates" }).click()
  await expect(page).toHaveURL(
    "/proxy/certificates?cert=%2Fetc%2Fssl%2Fjust-dashboard%2Fold.example.com%2Ffullchain.pem",
  )
})

test("a status that fails after answering is reported rather than drawn as current", async ({
  page,
}) => {
  await page.clock.install()
  await mockProxy(page, { included: true })
  let statusFails = false
  await page.route("**/api/v1/proxy/status", (route) =>
    statusFails
      ? failWith(503, { code: "unavailable", message: "The proxy status could not be read" })(route)
      : json(route, availability),
  )
  await page.goto("/proxy")
  const identity = page.locator("[data-slot='host-identity']")
  const header = page.locator("[data-slot='page-context']")
  await expect(identity.getByText("nginx/1.26.3")).toBeVisible()
  await expect(header.getByText(/^Updated (just now|[5-9]s ago)$/)).toBeVisible()
  await page.clock.fastForward(20_000)

  statusFails = true
  await header.getByRole("button", { name: "Refresh" }).click()
  // The engine line stays — it is the last answer — and the list says so.
  await expect(page.getByText("The proxy status could not be read")).toBeVisible()
  await expect(identity.getByText("nginx/1.26.3")).toBeVisible()
  await expect(page.getByText(/all within limits/)).toHaveCount(0)
  // Every other source answered just now, but the engine line is still the
  // status's last answer, and the page is as old as that. It read "Updated
  // just now" over it.
  await expect(header.getByText(/^Updated [2-5]\ds ago$/)).toBeVisible()
  await expect(header.getByText("Updated just now")).toHaveCount(0)

  statusFails = false
  await page.getByText("The proxy status could not be read").click()
  await page.getByRole("button", { name: "Try again" }).click()
  await expect(page.getByText("The proxy status could not be read")).toHaveCount(0)
})

test("a service state that cannot be read is not drawn from its last answer", async ({ page }) => {
  await mockProxy(page, { included: true })
  let unitFails = false
  await page.route("**/api/v1/systemd/nginx.service", (route) =>
    unitFails
      ? failWith(500, { code: "internal", message: "systemd did not answer" })(route)
      : route.fallback(),
  )
  await page.goto("/proxy")
  const identity = page.locator("[data-slot='host-identity']")
  await expect(identity.getByText(/running for/)).toBeVisible()

  unitFails = true
  await page.locator("[data-slot='page-context']").getByRole("button", { name: "Refresh" }).click()
  const unread = identity.getByText("couldn't read nginx.service")
  await expect(unread).toBeVisible()
  await expect(unread).toHaveAttribute("title", "systemd did not answer")
  await expect(identity.getByText(/running for/)).toHaveCount(0)
  await expect(identity.getByText("no service unit")).toHaveCount(0)
})
