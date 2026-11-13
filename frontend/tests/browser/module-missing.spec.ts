import { expect, test, type Page, type Route } from "@playwright/test"
import { user } from "./host-fixture"
import { admin, mockNetwork, vpn } from "./network-fixture"
import { risen } from "./risen"

/**
 * What a page offers where the thing it reads is not there.
 *
 * Four pages installed a missing tool in place; git, Tailscale and PM2
 * printed a sentence, and Docker printed the client's own question — "Is the
 * docker daemon running?" — for a stopped daemon and for a socket the
 * dashboard may not open alike, which are fixed in different places.
 */

function json(route: Route, body: unknown, status = 200) {
  return route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
}

const refusedConnection =
  "Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?"
const deniedSocket =
  'permission denied while trying to connect to the Docker daemon socket at unix:///var/run/docker.sock: Get "http://%2Fvar%2Frun%2Fdocker.sock/_ping": dial unix /var/run/docker.sock: connect: permission denied'

const dockerUnit = (activeState: string) => ({
  unit: {
    name: "docker.service",
    description: "Docker Application Container Engine",
    loadState: "loaded",
    activeState,
    subState: activeState === "active" ? "running" : "dead",
    unitFileState: "enabled",
    enabled: true,
  },
  properties: {},
})

type Shell = {
  session?: unknown
  /** What `/docker/ping` answers, read on every request. */
  ping?: () => unknown
  /** What systemd says of docker.service; a status answers that instead. */
  unit?: () => unknown | number
  routes?: Record<string, (route: Route) => unknown>
}

/** A signed-in shell over an API with nothing in it. */
async function mockShell(page: Page, shell: Shell = {}) {
  const posts: { path: string; body: unknown }[] = []
  await page.route("**/api/v1/**", (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname.replace(/^\/api\/v1/, "")
    if (request.method() !== "GET") {
      posts.push({ path, body: request.postData() ? request.postDataJSON() : undefined })
    }
    const own = shell.routes?.[path]
    if (own) return own(route)
    if (path === "/auth/session") return json(route, shell.session ?? user)
    if (path === "/docker/ping") {
      return json(route, shell.ping?.() ?? { available: true, serverVersion: "27.0.0" })
    }
    if (path === "/systemd/docker.service" && request.method() === "GET") {
      const unit = shell.unit?.()
      if (typeof unit === "number") {
        return json(
          route,
          { error: { code: "not_installed", message: "systemd is not here" } },
          unit,
        )
      }
      return json(route, unit ?? dockerUnit("active"))
    }
    return json(route, [])
  })
  return posts
}

const state = (page: Page) => page.locator("[data-slot=empty-state]")
/** The screen for a thing that is not there (`MissingThing`): Docker's, here. */
const missing = (page: Page) => page.locator("[data-slot=missing-thing]")

/** The block is inside the page's column and does not push it sideways. */
async function expectHeld(page: Page, width: number, block = state(page)) {
  await risen(page)
  const box = await block.boundingBox()
  expect(box).not.toBeNull()
  expect(box!.x).toBeGreaterThanOrEqual(0)
  expect(box!.x + box!.width).toBeLessThanOrEqual(width)
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
    ),
  ).toBeLessThanOrEqual(0)
}

for (const width of [1280, 390]) {
  test(`a stopped daemon is said as one and started from the page at ${width}`, async ({
    page,
  }, testInfo) => {
    await page.setViewportSize({ width, height: 844 })
    let running = false
    const posts = await mockShell(page, {
      ping: () =>
        running
          ? { available: true, serverVersion: "27.0.0" }
          : { available: false, host: "unix:///var/run/docker.sock", error: refusedConnection },
      unit: () => dockerUnit(running ? "active" : "inactive"),
      routes: {
        "/systemd/docker.service/start": (route) => {
          running = true
          return json(route, { exitCode: 0 })
        },
      },
    })
    await page.goto("/docker")
    await expect(missing(page)).toContainText("Docker is stopped")
    await expect(missing(page)).toContainText(
      "docker.service is inactive on this server, so nothing answers at /var/run/docker.sock.",
    )
    // The client's question is one press away, not the description.
    await expect(missing(page).getByText(refusedConnection)).toBeHidden()
    await missing(page).getByText("What the server said").click()
    await expect(missing(page).getByText(refusedConnection)).toBeVisible()
    await expect(missing(page).getByRole("link", { name: "Service details" })).toHaveAttribute(
      "href",
      "/processes/services/docker.service",
    )
    await expectHeld(page, width, missing(page))
    await page.screenshot({ path: testInfo.outputPath(`docker-stopped-${width}.png`) })

    await missing(page).getByRole("button", { name: "Start Docker" }).click()
    await expect
      .poll(() => posts.map((post) => post.path))
      .toContain("/systemd/docker.service/start")
    // The daemon answers now, and the page under the block is drawn.
    await expect(page.getByText("Docker is stopped")).toHaveCount(0)
  })

  test(`a socket the dashboard may not open is not a stopped daemon at ${width}`, async ({
    page,
  }, testInfo) => {
    await page.setViewportSize({ width, height: 844 })
    await mockShell(page, {
      ping: () => ({
        available: false,
        host: "unix:///var/run/docker.sock",
        error: deniedSocket,
      }),
    })
    await page.goto("/docker")
    await expect(missing(page)).toContainText("The dashboard may not open Docker's socket")
    await expect(missing(page)).toContainText("/var/run/docker.sock is there and refused")
    await expect(missing(page)).toContainText("docker")
    // Starting a daemon that runs would fix nothing.
    await expect(missing(page).getByRole("button", { name: "Start Docker" })).toHaveCount(0)
    await expect(missing(page).getByRole("button", { name: "Check again" })).toBeVisible()
    await expectHeld(page, width, missing(page))
    await page.screenshot({ path: testInfo.outputPath(`docker-refused-${width}.png`) })
  })
}

test("a daemon that runs but does not answer is at another address", async ({ page }) => {
  await mockShell(page, {
    ping: () => ({
      available: false,
      host: "unix:///var/run/docker.sock",
      error: refusedConnection,
    }),
    unit: () => dockerUnit("active"),
  })
  await page.goto("/docker")
  await expect(missing(page)).toContainText("Docker is running, but not where the dashboard asks")
  await expect(missing(page)).toContainText("JD_DOCKER_HOST")
  await expect(missing(page).getByRole("button", { name: "Start Docker" })).toHaveCount(0)
})

test("where systemd cannot be asked the block claims neither, and offers no start", async ({
  page,
}) => {
  await mockShell(page, {
    ping: () => ({
      available: false,
      host: "unix:///var/run/docker.sock",
      error: refusedConnection,
    }),
    unit: () => 503,
  })
  await page.goto("/docker")
  await expect(missing(page)).toContainText("Docker is not answering")
  await expect(missing(page).getByRole("button", { name: "Start Docker" })).toHaveCount(0)
  await expect(missing(page).getByRole("button", { name: "Check again" })).toBeVisible()
})

test("an account that may not control services is told, and not offered a start", async ({
  page,
}) => {
  await mockShell(page, {
    session: { ...user, capabilities: ["read"] },
    ping: () => ({
      available: false,
      host: "unix:///var/run/docker.sock",
      error: refusedConnection,
    }),
    unit: () => dockerUnit("failed"),
  })
  await page.goto("/docker")
  await expect(missing(page)).toContainText("docker.service is failed on this server")
  await expect(missing(page).getByRole("button", { name: "Start Docker" })).toHaveCount(0)
})

test("a ping the dashboard could not ask is its own failure, not a reading of Docker", async ({
  page,
}) => {
  let failing = true
  await mockShell(page, {
    routes: {
      "/docker/ping": (route) =>
        failing
          ? json(route, { error: { code: "unavailable", message: "backend restarting" } }, 503)
          : json(route, { available: true, serverVersion: "27.0.0" }),
    },
    unit: () => dockerUnit("active"),
  })
  await page.goto("/docker")
  const failure = page.getByRole("alert").filter({ hasText: "backend restarting" })
  await expect(failure).toBeVisible()
  await expect(missing(page)).toHaveCount(0)
  failing = false
  await failure.getByRole("button", { name: "Try again" }).click()
  await expect(failure).toHaveCount(0)
})

const job = (pkg: string) => ({
  id: "job-9",
  kind: "packages.install",
  title: `Installing ${pkg}`,
  target: pkg,
  status: "running",
  exitCode: 0,
  startedAt: new Date().toISOString(),
  lines: 0,
})

for (const width of [1280, 390]) {
  test(`a server without git is offered it in place at ${width}`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 844 })
    let installed = false
    const posts = await mockShell(page, {
      routes: {
        "/git/": (route) => json(route, { available: installed, repos: [] }),
        "/git/github/": (route) => json(route, { available: false }),
        "/packages/install": (route) => json(route, job("git")),
      },
    })
    await page.routeWebSocket("**/api/v1/jobs/job-9/stream**", (socket) => {
      installed = true
      socket.send(
        JSON.stringify({
          type: "job",
          data: { ...job("git"), status: "succeeded", endedAt: new Date().toISOString() },
        }),
      )
    })
    await page.goto("/git")
    await expect(state(page)).toContainText("git is not installed on this host")
    await expectHeld(page, width)
    await page.screenshot({ path: testInfo.outputPath(`git-missing-${width}.png`) })
    await state(page).getByRole("button", { name: "Install git" }).click()
    await expect
      .poll(() => posts.find((post) => post.path === "/packages/install")?.body)
      .toEqual({
        packages: ["git"],
      })
    // The page reads again once the job is through, and git is there: no
    // hand-off, and no reading of phases the server does not keep for git.
    await expect(page.getByText("git is not installed on this host")).toHaveCount(0)
    await expect(page.getByText("No repositories found")).toBeVisible()
    await expect(page.getByText(/no hand-off reads/)).toHaveCount(0)
  })
}

test("a reader is told who can install git, with nothing to press", async ({ page }) => {
  await mockShell(page, {
    session: { ...user, capabilities: ["read"] },
    routes: {
      "/git/": (route) => json(route, { available: false, repos: [] }),
      "/git/github/": (route) => json(route, { available: false }),
    },
  })
  await page.goto("/git")
  await expect(state(page)).toContainText("An administrator can install git")
  await expect(state(page).getByRole("button", { name: "Install git" })).toHaveCount(0)
})

const withoutTailscale = {
  ...vpn,
  tailscale: {
    ...vpn.tailscale,
    installed: false,
    running: false,
    backendState: "",
    tailnet: "",
    self: null,
    peers: [],
  },
}
const tailscaleBlock = (page: Page) =>
  page.locator("[data-slot=empty-state]").filter({ hasText: "Tailscale is not installed" })

test("Tailscale is installed in place where the package manager carries it", async ({ page }) => {
  const mutations: { method: string; path: string; body: unknown }[] = []
  await mockNetwork(page, mutations, {
    session: admin,
    overrides: {
      "/network/vpn": withoutTailscale,
      "/packages/tailscale": { name: "tailscale", version: "1.90.4", installed: false },
    },
  })
  await page.goto("/network/vpn")
  await expect(tailscaleBlock(page)).toBeVisible()
  await tailscaleBlock(page).getByRole("button", { name: "Install tailscale" }).click()
  await expect
    .poll(() => mutations.find((m) => m.path === "/packages/install")?.body)
    .toEqual({ packages: ["tailscale"] })
})

for (const width of [1280, 390]) {
  test(`Tailscale the package manager does not carry is pointed at, not offered, at ${width}`, async ({
    page,
  }, testInfo) => {
    await page.setViewportSize({ width, height: 844 })
    await mockNetwork(page, [], { session: admin, overrides: { "/network/vpn": withoutTailscale } })
    // Held back, so the block is measured before and after the answer.
    let answer: () => void = () => undefined
    const asked = new Promise<void>((resolve) => (answer = resolve))
    await page.route("**/api/v1/packages/tailscale", async (route) => {
      await asked
      await json(route, { error: { code: "not_found", message: "not found" } }, 404)
    })
    await page.goto("/network/vpn")
    const block = tailscaleBlock(page)
    await expect(block).toBeVisible()
    await risen(page)
    const before = await block.boundingBox()
    await expect(block.getByRole("link", { name: "tailscale.com/download" })).toBeHidden()

    answer()
    const link = block.getByRole("link", { name: "tailscale.com/download" })
    await expect(link).toBeVisible()
    await expect(link).toHaveAttribute("href", "https://tailscale.com/download/linux")
    await expect(block.getByRole("button", { name: "Install tailscale" })).toHaveCount(0)
    await expect(block).toContainText("This server's package manager does not carry it")
    // The block held its place while the package manager was asked: the
    // button's room was kept, and at most a line of the sentence changed.
    const after = await block.boundingBox()
    expect(after!.y).toBe(before!.y)
    expect(Math.abs(after!.height - before!.height)).toBeLessThanOrEqual(20)
    await block.scrollIntoViewIfNeeded()
    await page.screenshot({ path: testInfo.outputPath(`tailscale-missing-${width}.png`) })
  })
}

test("a server without PM2 names the command and opens the terminal it is typed in", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockShell(page, {
    routes: { "/pm2/": (route) => json(route, { available: false, processes: null }) },
  })
  await page.goto("/processes/pm2")
  await expect(state(page)).toContainText("PM2 is not installed")
  await expect(state(page)).toContainText("npm install -g pm2")
  await expect(state(page).getByRole("link", { name: "Open a terminal" })).toHaveAttribute(
    "href",
    "/terminal",
  )
  await expectHeld(page, 390)
})

test("an account without the terminal is not sent to one", async ({ page }) => {
  await mockShell(page, {
    session: { ...user, capabilities: ["read"] },
    routes: { "/pm2/": (route) => json(route, { available: false, processes: null }) },
  })
  await page.goto("/processes/pm2")
  await expect(state(page)).toContainText("PM2 is not installed")
  await expect(state(page).getByRole("link", { name: "Open a terminal" })).toHaveCount(0)
})
