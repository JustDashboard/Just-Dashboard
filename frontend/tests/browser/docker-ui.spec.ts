import { expect, test, type Page, type Route } from "@playwright/test"
import { PG_LINES } from "./logs-lens-fixture"

/**
 * The three claims the Docker overhaul rests on, checked in a browser.
 *
 * All three were bugs that a unit test could not have caught, because each was
 * a true statement rendered so that it read as a false one:
 *
 *   the overview said "All good" above a page of security warnings, because
 *   one label meant runtime health and posture at once;
 *
 *   a stack that existed only as a compose file reported "0/0 up", because the
 *   fraction counted containers on both sides of the slash;
 *
 *   a volume something was mounting offered a live Delete button whose only
 *   possible outcome was a 409.
 */

const now = new Date().toISOString()

const user = {
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
    lastLoginAt: now,
    createdAt: now,
  },
}

/**
 * A host where everything is running and several things need attention — the
 * exact combination the overview used to describe as "All good".
 */
const containers = [
  {
    id: "1111111111111111",
    names: ["web"],
    name: "web",
    image: "nginx:alpine",
    imageId: "sha256:aaa",
    command: "nginx",
    state: "running",
    status: "Up 2 hours",
    health: "healthy",
    createdAt: now,
    uptimeSeconds: 7200,
    ports: [],
    labels: {},
    networks: ["bridge"],
    exposure: [
      {
        hostIp: "0.0.0.0",
        hostPort: 443,
        containerPort: 443,
        protocol: "tcp",
        scope: "all",
        label: "Every interface",
        summary: "Bound to every interface on port 443.",
      },
    ],
    hasHealthcheck: true,
    inspected: true,
    memoryLimit: 0,
  },
  {
    id: "2222222222222222",
    names: ["db"],
    name: "db",
    image: "postgres:16",
    imageId: "sha256:bbb",
    command: "postgres",
    state: "running",
    status: "Up 2 hours",
    createdAt: now,
    uptimeSeconds: 7200,
    ports: [],
    labels: {},
    networks: ["bridge"],
    exposure: [
      {
        hostIp: "127.0.0.1",
        hostPort: 5432,
        containerPort: 5432,
        protocol: "tcp",
        scope: "loopback",
        label: "This server only",
        summary: "Bound to 127.0.0.1:5432. Only processes on this server can reach it.",
      },
    ],
    hasHealthcheck: false,
    inspected: true,
    memoryLimit: 0,
  },
]

const diagnosis = {
  status: "critical",
  checkedAt: now,
  checked: 2,
  // Everything is up and passing or unchecked — the runtime side is fine.
  runtime: {
    total: 2,
    running: 2,
    exited: 0,
    created: 0,
    restarting: 0,
    paused: 0,
    dead: 0,
    removing: 0,
    healthy: 1,
    unhealthy: 0,
    starting: 0,
    noHealthcheck: 1,
    status: "ok",
    summary: "2 running, 1 without a health check",
  },
  attention: { critical: 1, warning: 1, recommendations: 2, info: 0, issues: 2, total: 4 },
  findings: [
    {
      id: "container.dockersock.1111111111111111",
      level: "critical",
      severity: "critical",
      class: "security",
      title: "web can control Docker itself",
      detail: "The Docker socket is mounted into this container.",
      scope: "container",
      target: "web",
      targetId: "1111111111111111",
    },
    {
      id: "container.exposed.2222222222222222",
      level: "warning",
      severity: "warning",
      class: "exposure",
      title: "db publishes PostgreSQL on every interface",
      detail: "Ports of this kind are meant to be reached by the application in front of them.",
      scope: "container",
      target: "db",
      targetId: "2222222222222222",
    },
    {
      id: "container.nohealthcheck.2222222222222222",
      level: "notice",
      severity: "recommendation",
      class: "configuration",
      title: "db has no health check",
      detail: "Docker reports this container as up whenever its main process is alive.",
      scope: "container",
      target: "db",
      targetId: "2222222222222222",
    },
    {
      id: "container.nomemorylimit.2222222222222222",
      level: "notice",
      severity: "recommendation",
      class: "configuration",
      title: "db has no memory limit",
      detail: "It can use as much of this server's memory as it asks for.",
      scope: "container",
      target: "db",
      targetId: "2222222222222222",
    },
  ],
}

const stacks = [
  {
    name: "running-app",
    workingDir: "/srv/running-app",
    configFiles: ["/srv/running-app/docker-compose.yml"],
    services: [
      { name: "api", container: "3333", state: "running", status: "Up", image: "api", ports: [] },
      { name: "web", container: "4444", state: "running", status: "Up", image: "web", ports: [] },
    ],
    running: 2,
    total: 2,
    managed: true,
    declared: ["api", "web"],
    declaredSource: "file",
    containers: 2,
    deployed: true,
    orphans: [],
    state: "running",
    summary: "Running · 2/2 services",
  },
  {
    // The regression: a compose file on disk that has never been deployed. It
    // used to read "0/0 up".
    name: "never-deployed",
    workingDir: "/srv/never-deployed",
    configFiles: ["/srv/never-deployed/docker-compose.yml"],
    services: [],
    running: 0,
    total: 3,
    managed: true,
    declared: ["api", "db", "worker"],
    declaredSource: "file",
    containers: 0,
    deployed: false,
    orphans: [],
    state: "not-deployed",
    summary: "Not deployed · 3 services defined",
  },
]

const volumes = [
  {
    name: "app-data",
    driver: "local",
    mountpoint: "/var/lib/docker/volumes/app-data/_data",
    createdAt: now,
    scope: "local",
    labels: {},
    size: 1024 * 1024 * 512,
    refCount: 1,
    inUse: true,
    usedBy: [
      {
        id: "2222222222222222",
        name: "db",
        state: "running",
        destination: "/var/lib/postgresql/data",
        readOnly: false,
      },
    ],
  },
  {
    name: "orphaned",
    driver: "local",
    mountpoint: "/var/lib/docker/volumes/orphaned/_data",
    createdAt: now,
    scope: "local",
    labels: {},
    size: 0,
    refCount: 0,
    inUse: false,
    usedBy: [],
  },
]

/**
 * Two images and one network, enough to give the phone layouts something to
 * read: one image a container runs, one dangling and therefore removable.
 */
const images = [
  {
    id: "sha256:cccccccccccccccc",
    repoTags: ["nginx:alpine"],
    repoDigests: [],
    size: 187 * 1024 * 1024,
    created: now,
    containers: 1,
    labels: {},
    dangling: false,
  },
  {
    id: "sha256:dddddddddddddddd",
    repoTags: [],
    repoDigests: [],
    size: 12 * 1024 * 1024,
    created: now,
    containers: 0,
    labels: {},
    dangling: true,
  },
]

const networks = [
  {
    id: "net0000000000000000",
    name: "bridge",
    driver: "bridge",
    scope: "local",
    internal: false,
    attachable: false,
    ipv6: false,
    created: now,
    labels: {},
    subnets: ["172.17.0.0/16"],
    containers: 2,
    usedBy: ["web", "db"],
  },
]

/**
 * One tab hid the master key; the next one printed it.
 *
 * Environment detects credential-shaped values and puts them behind Reveal.
 * Inspect printed the same `docker inspect` document raw, including those
 * values in full — so the gesture on the first tab bought nothing against the
 * thing it defends against, which is somebody reading the screen. The server
 * already redacts for anyone below system.admin; this is the admin's own view.
 */
const detail = {
  ...containers[0],
  env: ["PATH=/usr/bin", "JD_MASTER_KEY=s3cr3t-master", "JD_BOOTSTRAP_PASSWORD=hunter2"],
  mounts: [
    {
      type: "volume",
      name: "app-data",
      source: "/var/lib/docker/volumes/app-data/_data",
      destination: "/usr/share/nginx/html",
      mode: "z",
      rw: true,
    },
    // Memory. There is nowhere on this filesystem to look, which is the case
    // the Storage tab has to draw without offering to look.
    { type: "tmpfs", name: "", source: "", destination: "/tmp", mode: "", rw: true },
  ],
  networkMode: "bridge",
  networkDetails: [],
  restartPolicy: "unless-stopped",
  privileged: false,
  capAdd: [],
  logPath: "/var/log/x.log",
  exitCode: 0,
  restartCount: 0,
  entrypoint: [],
  workingDir: "/",
  user: "root",
}

/**
 * What the file API answers for a volume's directory.
 *
 * The listing is the whole point of the storage browser, so a spec that stubs
 * the Docker half and not this one asserts an empty box.
 */
function entry(name: string, isDir: boolean, size = 0) {
  return {
    name,
    path: `/var/lib/docker/volumes/app-data/_data/${name}`,
    size,
    mode: isDir ? "drwxr-xr-x" : "-rw-r--r--",
    modeOctal: isDir ? "0755" : "0644",
    isDir,
    isSymlink: false,
    modified: now,
    owner: "root",
    group: "root",
    uid: 0,
    gid: 0,
  }
}

const volumeListing = {
  path: "/var/lib/docker/volumes/app-data/_data",
  parent: "/var/lib/docker/volumes/app-data",
  entries: [entry("base", true), entry("postgresql.conf", false, 28 * 1024)],
  roots: ["/"],
}

async function mockVolumeFiles(page: Page) {
  await page.route(/\/api\/v1\/files\/list/, (route) => json(route, volumeListing))
  await page.route(/\/api\/v1\/files\/read/, (route) =>
    json(route, {
      path: "/var/lib/docker/volumes/app-data/_data/postgresql.conf",
      content: "shared_buffers = 128MB\n",
      size: 28 * 1024,
      language: "ini",
      binary: false,
      modeOctal: "0644",
    }),
  )
}

const rawInspect = {
  Id: "1111111111111111",
  Config: {
    Image: "nginx:alpine",
    Env: ["PATH=/usr/bin", "JD_MASTER_KEY=s3cr3t-master", "JD_BOOTSTRAP_PASSWORD=hunter2"],
  },
}

async function json(route: Route, body: unknown) {
  await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) })
}

/**
 * The containers table is fed by a socket, not by the REST list.
 *
 * That is deliberate in the product — a table of live CPU and memory should
 * not be a poll — and it means a test that only stubs `/docker/containers/`
 * renders an empty table and passes for the wrong reason.
 */
async function mockContainerStream(page: Page) {
  await page.routeWebSocket(/\/api\/v1\/docker\/containers\/stream/, (ws) => {
    ws.send(JSON.stringify({ type: "containers", data: containers }))
    ws.send(
      JSON.stringify({
        type: "stats",
        data: [
          {
            id: "1111111111111111",
            name: "web",
            ts: now,
            cpuPercent: 12,
            memUsage: 100 * 1024 * 1024,
            memLimit: 512 * 1024 * 1024,
            memLimited: true,
            memPercent: 19.5,
            hostCpus: 8,
            netRx: 0,
            netTx: 0,
            blockRead: 0,
            blockWrite: 0,
            pids: 4,
            onlineCpus: 8,
            cpuTotal: 0,
            systemCpu: 0,
          },
          {
            // No limit anywhere: the cell must say "no limit" rather than
            // dividing by the machine's RAM and calling it a percentage.
            id: "2222222222222222",
            name: "db",
            ts: now,
            cpuPercent: 3,
            memUsage: 97 * 1024 * 1024,
            memLimit: 0,
            memLimited: false,
            memPercent: 0,
            memHostPercent: 0.4,
            hostCpus: 8,
            netRx: 0,
            netTx: 0,
            blockRead: 0,
            blockWrite: 0,
            pids: 9,
            onlineCpus: 8,
            cpuTotal: 0,
            systemCpu: 0,
          },
        ],
      }),
    )
  })
}

async function mockDocker(page: Page) {
  await mockContainerStream(page)
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname.replace(/^\/api\/v1/, "")
    switch (path) {
      case "/auth/session":
        return json(route, user)
      case "/dashboard/update":
        return json(route, { current: "0.6.7", latest: "0.6.7" })
      case "/docker/ping":
        return json(route, { available: true, serverVersion: "29.8.0" })
      case "/docker/containers/":
        return json(route, containers)
      case "/docker/health":
        return json(route, diagnosis)
      case "/docker/stacks/":
        return json(route, stacks)
      case "/docker/volumes/":
        return json(route, volumes)
      case "/docker/images/":
        return json(route, images)
      case "/docker/images/updates":
        return json(route, {
          "nginx:alpine": { ref: "nginx:alpine", state: "current", checkedAt: now },
        })
      case "/docker/networks/":
        return json(route, networks)
      case "/docker/events":
        return json(route, { listening: true, since: now, buffered: 0, events: [] })
      case "/docker/templates":
        return json(route, [])
      case "/docker/cleanup/preview":
        return json(route, { categories: [] })
      case "/docker/disk-usage":
        return json(route, {
          layersSize: 1000,
          imagesSize: 1500,
          containersSize: 100,
          volumesSize: 500,
          buildCacheSize: 200,
          sharedLayers: 500,
          writable: [],
          definitions: [],
          images: { total: 3, active: 2, size: 1000, reclaimable: 0 },
          containers: { total: 2, active: 2, size: 100, reclaimable: 0 },
          volumes: { total: 2, active: 1, size: 500, reclaimable: 0 },
          buildCache: { total: 1, active: 0, size: 200, reclaimable: 0 },
        })
      default:
        // Anything not named here is answered the way the deployment suite
        // answers it: a clean "not mocked" rather than an empty array, which
        // the app shell reads as a malformed host summary and crashes on.
        return route.fulfill({
          status: 503,
          contentType: "application/json",
          body: JSON.stringify({ error: { code: "not_available", message: "Not mocked" } }),
        })
    }
  })
}

/**
 * The contradiction this whole model exists to remove: runtime health and
 * attention are separate verdicts, and neither claims to be the other. They
 * were two of four tiles until the overview took §15's exit; they are the two
 * verdicts at the end of its identity line now.
 */
test("the overview separates runtime health from attention", async ({ page }) => {
  await mockDocker(page)
  await page.setViewportSize({ width: 1696, height: 992 })
  await page.goto("/docker")

  const identity = page.locator("[data-slot='host-identity']")
  await expect(identity.getByText("2 of 2 containers running")).toBeVisible()
  await expect(identity.getByText("Nothing failing")).toBeVisible()

  // And, at the same time, that something needs attention.
  await expect(identity.getByText("2 issues to look at")).toBeVisible()

  // The word that used to sit above a page of warnings must not appear, and
  // nor do the tiles.
  await expect(page.getByText("All good")).toHaveCount(0)
  await expect(page.locator("[data-slot='stat-tile']")).toHaveCount(0)
  await page.screenshot({
    path: "test-results/docker-docs.png",
    fullPage: true,
    animations: "disabled",
  })
})

test("attention lists posture findings and never calls them health", async ({ page }) => {
  await mockDocker(page)
  await page.goto("/docker")

  await expect(page.getByText("web can control Docker itself")).toBeVisible()
  await expect(page.getByText("db publishes PostgreSQL on every interface")).toBeVisible()
  // A recommendation is present and is not painted as a problem.
  await expect(page.getByText("db has no health check")).toBeVisible()

  // Docker's findings render through the one list the whole product shares
  // with Metrics and Security — see components/finding-list.tsx. The shape of
  // that list is the assertion: a row is a title, and the reasoning behind it
  // is one press away rather than printed under every entry. Docker used to
  // have its own parallel component that showed both at once, which is what
  // made this panel twice as tall and half as scannable as the identical
  // panel two pages away.
  const row = page.getByRole("button", { name: /web can control Docker itself/ })
  await expect(row).toHaveAttribute("aria-expanded", "false")
  await expect(page.getByText("The Docker socket is mounted into this container.")).toHaveCount(0)

  await row.click()
  await expect(page.getByText("The Docker socket is mounted into this container.")).toBeVisible()

  // The kind of problem is the row's right-hand column, so a reader scanning
  // the panel gets "security, exposure, configuration" in one pass.
  await expect(row.getByText("Security")).toBeVisible()
})

/** "0/0 up" is not a state. */
test("a stack that was never deployed says so", async ({ page }) => {
  await mockDocker(page)
  await page.goto("/docker/stacks")

  const never = page.locator('[data-stack-row="never-deployed"]')
  await expect(never).toContainText("Not deployed")
  await expect(never).toContainText("3 services defined")
  const running = page.locator('[data-stack-row="running-app"]')
  await expect(running).toContainText("Running")
  await expect(running).toContainText("2 of 2 services up")
  await expect(page.getByText("0/0")).toHaveCount(0)
})

/**
 * A stack is its own page since 2026-09-21: a compose editor, a merged log
 * feed and a watched command are three things you stay with, and none of them
 * wants the stack list showing behind it.
 */
test("a stack opens as its own page", async ({ page }) => {
  await mockDocker(page)
  await page.route("**/api/v1/docker/stacks/running-app", (route) => json(route, stacks[0]))
  await page.goto("/docker/stacks")
  await page.getByRole("button", { name: "running-app", exact: true }).first().click()

  await expect(page).toHaveURL(/\/docker\/stacks\/running-app$/)
  // What the stack is, as facts under the title rather than a sentence read
  // only to a screen reader.
  await expect(page.getByText("/srv/running-app").first()).toBeVisible()
  await expect(page.getByRole("tab", { name: "Compose file" })).toBeVisible()
  await expect(page.getByRole("link", { name: "Stacks" }).first()).toBeVisible()
})

/** `?stack=` was the address a compose-managed container linked to. */
test("a link to the old stack query lands on the stack", async ({ page }) => {
  await mockDocker(page)
  await page.route("**/api/v1/docker/stacks/running-app", (route) => json(route, stacks[0]))
  await page.goto("/docker/stacks?stack=running-app")

  await expect(page).toHaveURL(/\/docker\/stacks\/running-app$/)
})

test("a volume in use cannot be removed, and says what mounts it", async ({ page }) => {
  await mockDocker(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/docker/volumes")

  // Drawn and disabled, with the reason as its name: Docker refuses it.
  const inUse = page.getByRole("row", { name: /app-data/ })
  await expect(inUse.getByRole("link", { name: "db" })).toBeVisible()
  await expect(inUse.getByText("/var/lib/postgresql/data")).toBeVisible()
  await expect(
    inUse.getByRole("button", { name: "Remove — mounted by 1 container", exact: true }),
  ).toBeDisabled()

  // The unattached one still can be removed — the gate is usage, not caution.
  const free = page.getByRole("row", { name: /orphaned/ })
  await expect(free.getByRole("button", { name: "Remove", exact: true })).toBeEnabled()
  // Measured and empty says so, rather than the dash it shared with "not measured".
  await expect(free.getByText("empty", { exact: true })).toBeVisible()
})

/**
 * Two bindings one character apart and completely different in consequence,
 * drawn so that the difference is visible without hovering.
 */
test("published ports say what their binding means", async ({ page }) => {
  await mockDocker(page)
  await page.goto("/docker/containers")

  const web = page.getByRole("row").filter({ hasText: "nginx:alpine" })
  const db = page.getByRole("row").filter({ hasText: "postgres:16" })
  await expect(web.getByText("443 → 443")).toBeVisible()
  await expect(db.getByText("5432 → 5432")).toBeVisible()

  // The loopback one is a link because there is one address that is right; the
  // every-interface one deliberately is not.
  await expect(db.getByRole("link")).toHaveCount(1)
  await expect(web.getByRole("link")).toHaveCount(0)
})

test("the containers table keeps status runtime-only and counts issues apart", async ({ page }) => {
  await mockDocker(page)
  await page.goto("/docker/containers")

  const db = page.getByRole("row").filter({ hasText: "postgres:16" })
  // Status carries the runtime state and the health check's absence, and not
  // the security finding — that is a count in its own column.
  await expect(db.getByText("no health check")).toBeVisible()
  await expect(db.getByText("db publishes PostgreSQL on every interface")).toHaveCount(0)
  // The finding is a count of its own beside the name, held apart from the status.
  await expect(db.getByRole("cell").nth(1).getByText("1", { exact: true })).toHaveCount(0)
  await expect(db.getByRole("cell").first().getByText("1", { exact: true })).toBeVisible()
})

/**
 * The denominator that did not exist. Docker reports host RAM as the limit for
 * a container nobody limited, and the table showed `97 MB / 62.7 GB` as though
 * that were a budget.
 */
test("memory says no limit rather than inventing one", async ({ page }) => {
  await mockDocker(page)
  await page.goto("/docker/containers")

  const db = page.getByRole("row").filter({ hasText: "postgres:16" })
  await expect(db.getByText(/no limit/)).toBeVisible({ timeout: 15_000 })

  // A container that really is limited still shows the fraction.
  const web = page.getByRole("row").filter({ hasText: "nginx:alpine" })
  await expect(web.getByText(/512\.0 MB/)).toBeVisible()
})

/**
 * Twenty-six findings that are four habits.
 *
 * A server with eight containers none of which set a memory limit produced
 * eight separate rows saying so, and the panel read as a list of eight
 * problems. The reader had to hold every container name in their head to spot
 * that it was one. Repeats of a kind now collapse to a single row that says
 * how many, and opens to name them.
 */
const repeated = {
  ...diagnosis,
  checked: 5,
  attention: { critical: 1, warning: 0, recommendations: 10, info: 0, issues: 1, total: 11 },
  findings: [
    diagnosis.findings[0],
    ...["api", "web", "worker", "cache", "db"].flatMap((name) => [
      {
        id: `container.nohealthcheck.${name}`,
        level: "notice",
        severity: "recommendation",
        class: "configuration",
        title: `${name} has no health check`,
        detail: "Docker reports this container as up whenever its main process is alive.",
        advice: "A health check is one command the container runs against itself.",
        scope: "container",
        target: name,
        targetId: name,
      },
      {
        id: `container.nomemorylimit.${name}`,
        level: "notice",
        severity: "recommendation",
        class: "configuration",
        title: `${name} has no memory limit`,
        detail: "It can use as much of this server's memory as it asks for.",
        scope: "container",
        target: name,
        targetId: name,
      },
    ]),
  ],
}

test("attention collapses one problem repeated across containers into one row", async ({
  page,
}) => {
  await mockDocker(page)
  // Registered after mockDocker, so it wins for this one path.
  await page.route("**/api/v1/docker/health", (route) => json(route, repeated))
  await page.goto("/docker")

  // Five containers, one sentence — and pluralised into "have".
  await expect(page.getByText("5 containers have no health check")).toBeVisible()
  await expect(page.getByText("5 containers have no memory limit")).toBeVisible()

  // The individual titles are not in the list until the group is opened.
  await expect(page.getByText("api has no health check")).toHaveCount(0)

  // Severity still wins over frequency: the one critical stays on top.
  const rows = await page.getByRole("button", { expanded: false }).allInnerTexts()
  const critical = rows.findIndex((t) => t.includes("web can control Docker itself"))
  const group = rows.findIndex((t) => t.includes("containers have no health check"))
  expect(critical).toBeGreaterThanOrEqual(0)
  expect(critical).toBeLessThan(group)

  // Opening it names the containers and states the shared reasoning once. The
  // group is a summary, never a replacement for knowing which ones — but the
  // names are names rather than five repetitions of the same sentence, which
  // is what the grouping exists to stop.
  await page.getByRole("button", { name: /5 containers have no health check/ }).click()
  await expect(page.getByText("Docker reports this container as up whenever")).toBeVisible()
  await expect(page.getByText("api", { exact: true })).toBeVisible()
  await expect(page.getByText("cache", { exact: true })).toBeVisible()
  await expect(page.getByText("api has no health check")).toHaveCount(0)
})

/**
 * The containers page on a phone.
 *
 * Ten columns with no small-screen treatment forced the page itself to scroll
 * sideways, which takes the navigation with it. The first fix dropped the
 * columns a phone reader could do without — and a nine-column table with five
 * of them removed is still a table somebody is reading the remains of: a wide
 * name cell, a wedge of space, and two stubs.
 *
 * Below `lg` the same containers are laid out down the row instead of across
 * it, and the test of that layout is that *nothing was dropped to achieve it*.
 * The image, the ports, both live readings and the issue count are all what a
 * phone reader checks after a deploy, and all of them are on screen.
 */
test("on a phone the containers are a list rather than a table with columns removed", async ({
  page,
}) => {
  await mockDocker(page)
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/docker/containers")

  // No columns at all: the table is replaced here, not squeezed.
  await expect(page.getByRole("columnheader")).toHaveCount(0)

  // And every fact the columns carried is still readable. Scoped to the list,
  // because the band above it names the same containers.
  const list = page.getByRole("list").filter({ hasText: "nginx:alpine" })
  await expect(list.getByText("nginx:alpine")).toBeVisible()
  await expect(list.getByText("postgres:16")).toBeVisible()
  await expect(list.getByText("Running").first()).toBeVisible()
  await expect(list.getByText("443 → 443")).toBeVisible()
  // The memory figure is the one that used to be truncated to "100.0…" when
  // the readings shared the row with the action cluster.
  await expect(list.getByText("512.0 MB")).toBeVisible()
  await expect(list.getByText(/no limit/)).toBeVisible()
  // Each container's findings are a count beside its name.
  const db = list.getByRole("listitem").filter({ hasText: "postgres:16" })
  await expect(db.getByText("1", { exact: true })).toBeVisible()

  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  )
  expect(overflow).toBeLessThanOrEqual(1)
})

/**
 * The containers page was the only one that replaced its table on a phone; the
 * network table was still the remains of one: columns dropped until a wide first cell
 * and a stub were left. The same test applies to it as to the containers list — the table is
 * replaced, not squeezed, and nothing the table carried is lost on the way. The images and
 * volumes are tables again, with their phone shapes checked in `docker-images.spec.ts` and
 * `docker-volumes.spec.ts`.
 */
test("the network list reads down the row on a phone", async ({ page }) => {
  await mockDocker(page)
  await page.setViewportSize({ width: 390, height: 844 })

  await page.goto("/docker/networks")
  await expect(page.getByRole("columnheader")).toHaveCount(0)
  const networkList = page.getByRole("list").filter({ hasText: "172.17.0.0/16" })
  await expect(networkList.getByRole("button", { name: "bridge" })).toBeVisible()
  await expect(networkList.getByText("172.17.0.0/16")).toBeVisible()
  await expect(networkList.getByText("2 containers")).toBeVisible()
  await expect(networkList.getByText(/Docker's own/)).toBeVisible()
})

/**
 * The reveal rule's touch clause, for this page specifically: a card's verbs
 * are never hidden behind a hover that a phone cannot perform.
 */
test("a container's actions are reachable on a touch screen", async ({ page }) => {
  await mockDocker(page)
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/docker/containers")

  const hidden = await page.evaluate(() => {
    const bad: string[] = []
    for (const el of document.querySelectorAll<HTMLElement>("li button")) {
      if (parseFloat(getComputedStyle(el).opacity) < 0.1) {
        bad.push(el.getAttribute("aria-label") ?? el.outerHTML.slice(0, 120))
      }
    }
    return bad
  })
  expect(hidden, "controls hidden behind hover on the containers list").toEqual([])
})

/**
 * A glyph is not a word.
 *
 * The row used to end in five icon-only buttons, two of which — update and
 * pause — mean nothing without already knowing what they do, and a control
 * nobody dares press is a control that is not there. Start, restart and stop
 * stay as icons because they are pressed constantly; everything else moved into
 * a menu where each verb is named.
 */
test("the destructive verbs are words, not glyphs", async ({ page }) => {
  await mockDocker(page)
  await page.goto("/docker/containers")

  const row = page.getByRole("row").filter({ hasText: "nginx:alpine" })
  await row.hover()

  // The constantly-pressed ones stay on the row itself, and are asserted first:
  // an open Radix menu is modal, so it hides the rest of the page from the
  // accessibility tree while it is up.
  await expect(row.getByRole("button", { name: "Stop" })).toBeVisible()
  await expect(row.getByRole("button", { name: "Restart", exact: true })).toBeVisible()

  await row.getByRole("button", { name: "More actions" }).click()
  const menu = page.getByRole("menu")
  await expect(menu.getByText("Update to a newer image")).toBeVisible()
  await expect(menu.getByText("Remove")).toBeVisible()
})

/**
 * "Which of these is not running" was a question answered by reading a column.
 */
test("the state filters narrow the list and carry their own counts", async ({ page }) => {
  await mockDocker(page)
  await page.goto("/docker/containers")

  const running = page.getByRole("button", { name: /^Running/ })
  await expect(running).toContainText("2")
  await running.click()
  await expect(running).toHaveAttribute("aria-pressed", "true")
  await expect(page.getByRole("row").filter({ hasText: "nginx:alpine" })).toBeVisible()

  // Nothing is stopped or failing on this host, so those chips are not
  // offered at all — a filter that can only ever return nothing is furniture.
  await expect(page.getByRole("button", { name: /^Stopped/ })).toHaveCount(0)
  await expect(page.getByRole("button", { name: /^Failing/ })).toHaveCount(0)

  await page.getByRole("button", { name: /^Needs attention/ }).click()
  await expect(page.getByRole("row").filter({ hasText: "nginx:alpine" })).toBeVisible()
  await expect(page.getByRole("row").filter({ hasText: "postgres:16" })).toBeVisible()

  // Pressing it again lets it go.
  await page.getByRole("button", { name: /^Needs attention/ }).click()
  await expect(page.getByRole("button", { name: /^Needs attention/ })).toHaveAttribute(
    "aria-pressed",
    "false",
  )
})

/**
 * Whether anything is checking a container is said on every running row.
 *
 * The runtime health bar this page opened on printed every count, the zeroes
 * included, because a category that stops being mentioned the moment it goes
 * right is one nobody learns to check. The bar went to the identity line and
 * the rows: the line says how many running containers a health check vouches
 * for, and each row says its own verdict — or that it has none.
 */
test("every running container says whether a health check vouches for it", async ({ page }) => {
  await mockDocker(page)
  await page.goto("/docker/containers")

  await expect(page.getByText("1 of 2 pass a health check")).toBeVisible()
  const web = page.getByRole("row").filter({ hasText: "nginx:alpine" })
  const db = page.getByRole("row").filter({ hasText: "postgres:16" })
  await expect(web.getByText("healthy", { exact: true })).toBeVisible()
  await expect(db.getByText("no health check")).toBeVisible()
  await expect(page.getByText("Nothing failing")).toBeVisible()
})

/**
 * Opening a container to find out why it is unhappy used to mean closing it
 * again to do anything about it: the lifecycle verbs lived on the table row and
 * nowhere else.
 *
 * A container is its own page since 2026-09-21, so the row is a link and the
 * verbs are the page's own. The address is asserted because it is the thing
 * that changed: a container is somewhere you can be, not a panel over a list.
 */
test("the container page can act on the container it is describing", async ({ page }) => {
  await mockDocker(page)
  await page.route("**/api/v1/docker/containers/1111111111111111", (route) => json(route, detail))
  await page.goto("/docker/containers")
  await page.getByRole("button", { name: "web", exact: true }).first().click()

  await expect(page).toHaveURL(/\/docker\/containers\/1111111111111111$/)
  await expect(page.getByRole("button", { name: "Stop" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Restart", exact: true })).toBeVisible()
  // And the identity is a fact under the title rather than a tag in it.
  await expect(page.getByText("nginx:alpine").first()).toBeVisible()
  // The way back is the eyebrow, where a page's own breadcrumb goes.
  await expect(page.getByRole("link", { name: "Containers" }).first()).toBeVisible()
})

/**
 * `?container=` was this page's address for a detail panel for three releases.
 * Those links are in bookmarks and tickets, and they land on the container.
 */
test("a link to the old container query lands on the container", async ({ page }) => {
  await mockDocker(page)
  await page.route("**/api/v1/docker/containers/1111111111111111", (route) => json(route, detail))
  await page.goto("/docker/containers?container=1111111111111111")

  await expect(page).toHaveURL(/\/docker\/containers\/1111111111111111$/)
})

/**
 * A log button that merely selects a row is not a log button. Every route that
 * asks a particular question about a container now lands on the tab that
 * answers it.
 */
test("asking for a container's logs opens the logs", async ({ page }) => {
  await mockDocker(page)
  await page.route("**/api/v1/docker/containers/1111111111111111", (route) => json(route, detail))
  await page.goto("/docker/containers")

  const row = page.getByRole("row").filter({ hasText: "nginx:alpine" })
  await row.hover()
  await row.getByRole("button", { name: "More actions" }).click()
  await page.getByRole("menuitem", { name: /^Logs/ }).click()

  await expect(page.getByRole("tab", { name: "Logs" })).toHaveAttribute("data-state", "active")
})

/**
 * A database container whose restart policy keeps bringing it back: the
 * container a Logs tab and an Events view exist for.
 */
const DB = "2222222222222222"
const dbDetail = {
  ...detail,
  ...containers[1],
  env: [],
  mounts: [],
  restartPolicy: "always",
  restartCount: 17,
  hasHealthcheck: true,
}

const minutesAgo = (m: number) => new Date(Date.now() - m * 60_000).toISOString()

/** A loop of three restarts that exit 1, and the exit that ended it. */
const dbEvents = (() => {
  const at = (s: number) => new Date(Date.now() - 20 * 60_000 + s * 1000).toISOString()
  const ev = (s: number, action: string, exitCode?: string) => ({
    time: at(s),
    type: "container",
    action,
    name: "db",
    id: DB,
    image: "postgres:16",
    exitCode,
    message: action === "die" ? "db exited with status 1" : "db started",
    level: action === "die" ? "error" : "notice",
    source: "daemon",
  })
  return [
    ev(240, "die", "1"),
    ev(183, "start"),
    ev(180, "die", "1"),
    ev(122, "start"),
    ev(120, "die", "1"),
    ev(61, "start"),
    ev(60, "die", "1"),
    ev(0, "start"),
  ]
})()

/**
 * What the database wrote around an exit: the minute before it, and the next
 * attempt starting up a moment after — which a last-lines search, bounded at
 * the exit, must not bring back as the minute before.
 */
const lastLines = (since: number) => [
  {
    text: "2026-09-27 10:14:02.311 UTC [1] LOG:  starting PostgreSQL 16.4 on x86_64-pc-linux-gnu",
    timestamp: new Date(since + 59_000).toISOString(),
    level: "info",
    event: "startup",
  },
  {
    text: '2026-09-27 10:14:02.402 UTC [1] FATAL:  data directory "/var/lib/postgresql/data" has invalid permissions',
    timestamp: new Date(since + 59_990).toISOString(),
    level: "error",
    event: "fatal",
  },
  {
    text: "2026-09-27 10:14:02.600 UTC [1] LOG:  the next attempt, starting up",
    timestamp: new Date(since + 60_200).toISOString(),
    level: "info",
    event: "startup",
  },
]

/** How a last-lines search is told apart from the pane's own: it asks for this many. */
const LAST_LINES_LIMIT = "20"

const dbFailure = {
  containerId: DB,
  name: "db",
  checkedAt: minutesAgo(0),
  state: "looping",
  headline: "Restart loop detected: 4 starts in 4m.",
  likely: "It exits with status 1 shortly after starting.",
  confidence: "inferred",
  evidence: [],
  restarts: { count: 17, recent: 4, looping: true, window: "4m", summary: "4 starts in 4m" },
  suggestions: [],
  logWindow: {
    since: minutesAgo(18),
    until: minutesAgo(15),
    reason: "the window around the most recent start, which is where the failure repeats",
  },
}

const dbInspect = {
  Id: DB,
  Config: { Image: "postgres:16", Healthcheck: { Test: ["CMD-SHELL", "pg_isready -U postgres"] } },
  State: {
    Status: "running",
    Health: {
      Status: "unhealthy",
      FailingStreak: 2,
      Log: [
        {
          Start: "2026-09-27T10:13:30.000000001Z",
          End: "2026-09-27T10:13:35.000000001Z",
          ExitCode: 1,
          Output: "/var/run/postgresql:5432 - no response\n",
        },
        {
          Start: "2026-09-27T10:13:00.123456789Z",
          End: "2026-09-27T10:13:00.456789012Z",
          ExitCode: 0,
          Output: "/var/run/postgresql:5432 - accepting connections\n",
        },
      ],
    },
  },
}

/**
 * The stack's web service exited and came back. Its events name the
 * container as compose did, and the service as the stack's log does.
 */
const stackEvents = [
  { action: "die", at: 5, exitCode: "1", message: "running-app-web-1 exited with status 1" },
  { action: "start", at: 4.9, message: "running-app-web-1 started" },
].map(({ action, at, exitCode, message }) => ({
  time: minutesAgo(at),
  type: "container",
  action,
  name: "running-app-web-1",
  service: "web",
  id: "4444",
  image: "web",
  stack: "running-app",
  exitCode,
  message,
  level: action === "die" ? "error" : "notice",
  source: "daemon",
}))

/** A stack's merged log: each line carries the service it came from. */
const stackLines = [
  {
    text: "listening on :3000",
    timestamp: minutesAgo(3),
    source: "api",
    attrs: { service: "api", container: "3333" },
  },
  {
    text: "GET /orders 500 upstream timed out",
    timestamp: minutesAgo(2),
    source: "web",
    level: "error",
    attrs: { service: "web", container: "4444" },
  },
  {
    text: "GET /health 200",
    timestamp: minutesAgo(1),
    source: "api",
    attrs: { service: "api", container: "3333" },
  },
]

type ServiceLogMocks = {
  sockets: URLSearchParams[]
  searches: URLSearchParams[]
  events: URLSearchParams[]
  eventSockets: URLSearchParams[]
}

/** The pane's own History searches: bounded at both ends, unlike a reading's, and not a last-lines one. */
const historySearches = (mocks: ServiceLogMocks) =>
  mocks.searches.filter((s) => s.has("until") && s.get("limit") !== LAST_LINES_LIMIT)

/**
 * The service logs a container's and a stack's Logs tab embed, over the
 * database container and the running-app stack: `/logs/source` describes
 * each as the server would (the database read as Postgres), the live socket
 * sends their lines, and every search, event read and socket is recorded so
 * a spec can say which question reached the server.
 */
async function mockServiceLogs(page: Page): Promise<ServiceLogMocks> {
  const recorded: ServiceLogMocks = { sockets: [], searches: [], events: [], eventSockets: [] }
  await page.route(`**/api/v1/docker/containers/${DB}`, (route) => json(route, dbDetail))
  await page.route(`**/api/v1/docker/containers/${DB}/failure`, (route) => json(route, dbFailure))
  await page.route(`**/api/v1/docker/containers/${DB}/raw`, (route) => json(route, dbInspect))
  await page.route("**/api/v1/docker/stacks/running-app", (route) => json(route, stacks[0]))
  await page.route("**/api/v1/docker/events?**", (route) => {
    const params = new URL(route.request().url()).searchParams
    recorded.events.push(params)
    return json(route, {
      listening: true,
      since: minutesAgo(180),
      buffered: dbEvents.length,
      events:
        params.get("container") === DB
          ? dbEvents
          : params.get("stack") === "running-app"
            ? stackEvents
            : [],
    })
  })
  await page.routeWebSocket(/\/api\/v1\/docker\/events\/stream/, (socket) => {
    recorded.eventSockets.push(new URL(socket.url()).searchParams)
  })
  await page.route("**/api/v1/logs/**", (route) => {
    const url = new URL(route.request().url())
    const source = url.searchParams.get("source") ?? ""
    if (url.pathname.endsWith("/logs/source")) {
      if (source === `docker:${DB}`) {
        return json(route, { id: source, kind: "docker", status: "running", lens: "postgres" })
      }
      if (source === "stack:running-app") {
        return json(route, { id: source, kind: "stack", status: "running", detail: "2 services" })
      }
      return json(route, {})
    }
    if (url.pathname.endsWith("/logs/search")) {
      recorded.searches.push(url.searchParams)
      const before = url.searchParams.get("limit") === LAST_LINES_LIMIT
      // Up to the bound and no further, as the server reads it.
      const until = Date.parse(url.searchParams.get("until") ?? "")
      const found = before
        ? lastLines(Date.parse(url.searchParams.get("since")!)).filter(
            (line) => !(Date.parse(line.timestamp) > until),
          )
        : []
      const facet = url.searchParams.get("facets")
      return json(route, {
        facets: facet
          ? {
              [facet]: {
                values: [{ value: "web", count: 1, errors: 1 }],
                distinct: 1,
                other: 0,
                missing: 0,
              },
            }
          : undefined,
        lines: found,
        scanned: 40,
        matched: before ? found.length : 3,
        truncated: false,
        complete: true,
        files: [],
        histogram: [],
        tookMillis: 1,
        lens: source === `docker:${DB}` ? "postgres" : undefined,
      })
    }
    return json(route, {})
  })
  await page.routeWebSocket("**/api/v1/logs/stream**", (socket) => {
    const params = new URL(socket.url()).searchParams
    recorded.sockets.push(params)
    const stack = params.get("source")?.startsWith("stack:")
    socket.send(
      JSON.stringify({
        type: "meta",
        data: {
          kind: stack ? "stack" : "docker",
          label: params.get("source"),
          filtered: false,
          lens: stack ? undefined : "postgres",
        },
        ts: Date.now(),
      }),
    )
    socket.send(
      JSON.stringify({ type: "logs", data: stack ? stackLines : PG_LINES, ts: Date.now() }),
    )
  })
  return recorded
}

/**
 * A container's Logs tab is the service logs every page embeds, not a raw
 * tail: the lines are read through the lens its image names — a Postgres
 * container's as deadlocks and failed logins — with the lens's readings over
 * them, and the tab is still the page's own Radix tab.
 */
test("a container's logs read as what its image writes", async ({ page }) => {
  await mockDocker(page)
  const mocks = await mockServiceLogs(page)
  await page.goto(`/docker/containers/${DB}?tab=logs`)

  await expect(page.getByRole("tab", { name: "Logs" })).toHaveAttribute("data-state", "active")
  const lines = page.getByLabel("Log lines")
  await expect(lines.getByText("deadlock", { exact: true })).toHaveClass(/text-destructive/)
  await expect(lines.getByText("auth failed", { exact: true })).toBeVisible()
  expect(mocks.sockets.at(-1)?.get("source")).toBe(`docker:${DB}`)
  // Postgres's own questions, one press each, and its readings above the pane.
  await expect(page.getByRole("button", { name: /^Slow\b/ })).toBeVisible()
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(5)
  await expect(page.getByText("Deadlocks & lock waits")).toBeVisible()
  // A question a tile answers is counted once, by the tile over its hour:
  // its chip carries no second figure from the lines on screen. One no tile
  // asks keeps its count.
  await expect(page.getByRole("button", { name: "Errors", exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: /^Maintenance\s*\d/ })).toBeVisible()
})

/**
 * Events is what Docker did to the container, beside its lines: the health
 * check's probes first because they say why "unhealthy", a restart loop as
 * one row rather than eight, and the minute before each failed exit inline.
 * The failure's window is one press from the pane itself.
 */
test("a container's events fold its restart loop under its health check", async ({ page }) => {
  await mockDocker(page)
  const mocks = await mockServiceLogs(page)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto(`/docker/containers/${DB}?tab=logs`)

  await page.getByRole("button", { name: "Events", exact: true }).click()
  await expect(page.getByRole("button", { name: "Events", exact: true })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  expect(mocks.events.at(-1)?.get("container")).toBe(DB)
  await expect.poll(() => mocks.eventSockets.at(-1)?.get("container")).toBe(DB)

  const health = page.getByRole("region", { name: "Health check" })
  await expect(health.getByText("pg_isready -U postgres")).toBeVisible()
  await expect(health.getByText("Unhealthy")).toBeVisible()
  await expect(health.getByText("exit 1")).toBeVisible()
  await expect(health.getByText("/var/run/postgresql:5432 - no response")).toBeVisible()
  await expect(health.getByText("passed")).toBeVisible()

  const events = page.getByRole("region", { name: "Container events" })
  await expect(events.getByText(/^Restarted ×3 in 2 min · exit 1$/)).toBeVisible()
  // Three exits folded, and the one that ended the loop still its own row.
  await expect(events.getByText("db exited with status 1")).toHaveCount(1)
  await events.getByRole("button", { name: "Show the 6 events" }).click()
  await expect(events.getByText("db exited with status 1")).toHaveCount(4)

  // The minute before the last two failures, read as the pane reads them.
  await expect(events.getByText("the minute before it exited")).toBeVisible()
  await expect(events.getByText("the minute before its last exit")).toBeVisible()
  await expect(events.getByText(/has invalid permissions/)).toHaveCount(2)
  // Up to the exit and no further: the next attempt's start-up is not why it died.
  await expect(events.getByText(/the next attempt, starting up/)).toHaveCount(0)
  const before = mocks.searches.filter((s) => s.get("limit") === LAST_LINES_LIMIT)
  expect(before).toHaveLength(2)
  expect(before.map((s) => s.get("source"))).toEqual([`docker:${DB}`, `docker:${DB}`])
  // The minute runs to the exit itself, as the event wrote it.
  const exit = Date.parse(dbEvents[0].time)
  const last = before.find((s) => Date.parse(s.get("since")!) === exit - 60_000)
  expect(last?.get("until")).toBe(dbEvents[0].time)

  // The failure's window: History on it, in the pane, named as what it is.
  await page.getByRole("button", { name: "Crash window" }).click()
  await expect(page.getByRole("button", { name: "History", exact: true })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await expect
    .poll(() => mocks.searches.at(-1)?.get("since"))
    .toBe(new Date(dbFailure.logWindow.since).toISOString())
  expect(mocks.searches.at(-1)?.get("until")).toBe(
    new Date(dbFailure.logWindow.until).toISOString(),
  )
})

/**
 * The Crash window chip says what the pane is reading, and nothing else: it
 * is on while the pane reads the window, and off the moment the reader moves
 * to Live, to "Open in History" on an exit, or anywhere else — with the
 * strip's "Crash window" going with it. Pressed again, it opens the window
 * again.
 */
test("the crash window chip and the pane stay in step", async ({ page }) => {
  await mockDocker(page)
  const mocks = await mockServiceLogs(page)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto(`/docker/containers/${DB}?tab=logs`)
  const chip = page.getByRole("button", { name: "Crash window" })
  const live = page.getByRole("button", { name: "Live", exact: true })
  const history = page.getByRole("button", { name: "History", exact: true })
  const eventsView = page.getByRole("button", { name: "Events", exact: true })
  const named = page.getByText(/^Crash window · /)
  const windowSince = new Date(dbFailure.logWindow.since).toISOString()

  // On, then to Events and back to Live: Live, with the chip off.
  await chip.click()
  await expect(history).toHaveAttribute("aria-pressed", "true")
  await expect(chip).toHaveAttribute("aria-pressed", "true")
  await expect(named).toBeVisible()
  await eventsView.click()
  await live.click()
  await expect(live).toHaveAttribute("aria-pressed", "true")
  await expect(chip).toHaveAttribute("aria-pressed", "false")
  await expect(named).toHaveCount(0)

  // On, then an exit's own minutes from Events: that range, not the window's.
  await chip.click()
  await expect(chip).toHaveAttribute("aria-pressed", "true")
  await eventsView.click()
  await page
    .getByRole("region", { name: "Container events" })
    .getByRole("button", { name: "Open in History" })
    .first()
    .click()
  await expect(history).toHaveAttribute("aria-pressed", "true")
  await expect
    .poll(() => historySearches(mocks).at(-1)?.get("since"))
    .toBe(new Date(Date.parse(dbEvents[0].time) - 5 * 60_000).toISOString())
  await expect(chip).toHaveAttribute("aria-pressed", "false")
  await expect(named).toHaveCount(0)

  // And pressed again, the window again.
  await chip.click()
  await expect(chip).toHaveAttribute("aria-pressed", "true")
  await expect(named).toBeVisible()
  await expect.poll(() => historySearches(mocks).at(-1)?.get("since")).toBe(windowSince)
})

/**
 * The Overview's account of a failure reads the lines it names in one press:
 * the Logs tab, on the failure's window.
 */
test("the failure's notice opens the logs on its window", async ({ page }) => {
  await mockDocker(page)
  const mocks = await mockServiceLogs(page)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto(`/docker/containers/${DB}?tab=overview`)

  await page.getByRole("button", { name: "Read those lines" }).click()
  await expect(page.getByRole("tab", { name: "Logs" })).toHaveAttribute("data-state", "active")
  await expect(page.getByRole("button", { name: "History", exact: true })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await expect(page.getByRole("button", { name: "Crash window" })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await expect(page.getByText(/^Crash window · /)).toBeVisible()
  await expect
    .poll(() => historySearches(mocks).at(-1)?.get("since"))
    .toBe(new Date(dbFailure.logWindow.since).toISOString())
})

/**
 * The pane keeps its reading for the tab, and a window it was handed is the
 * page's: gone by the time the page is back, it is not restored as an old
 * moment with nothing naming it.
 */
test("a crash window let go of while the page was away is not what it opens on", async ({
  page,
}) => {
  await mockDocker(page)
  await mockServiceLogs(page)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto(`/docker/containers/${DB}?tab=logs`)
  const chip = page.getByRole("button", { name: "Crash window" })
  await chip.click()
  await expect(page.getByRole("button", { name: "History", exact: true })).toHaveAttribute(
    "aria-pressed",
    "true",
  )

  await page.reload()
  await expect(page.getByRole("button", { name: "Live", exact: true })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await expect(chip).toHaveAttribute("aria-pressed", "false")
  await expect(page.getByText(/^Crash window · /)).toHaveCount(0)
})

/**
 * A stack's Logs tab is one log of every container, each line in its
 * service's lane rather than behind a `web | ` prefix, from the stack's own
 * source — not a socket of the stack page's own. The service is a field to
 * narrow by, the readings above narrow the pane as a container's do, and
 * Events names each container by its service, as the lines do.
 */
test("a stack's logs are one log with a lane per service", async ({ page }) => {
  await mockDocker(page)
  const mocks = await mockServiceLogs(page)
  await page.goto("/docker/stacks/running-app")
  await page.getByRole("tab", { name: "Logs" }).click()

  const lines = page.getByLabel("Log lines")
  await expect(lines.getByText("GET /orders 500 upstream timed out")).toBeVisible()
  await expect(lines.getByText("api", { exact: true }).first()).toBeVisible()
  await expect(lines.getByText("web", { exact: true })).toBeVisible()
  await expect(lines.getByText(/\| /)).toHaveCount(0)
  expect(mocks.sockets.at(-1)?.get("source")).toBe("stack:running-app")

  await page.getByRole("button", { name: /^Fields/ }).click()
  await page.getByRole("option", { name: /^web/ }).click()
  await expect.poll(() => mocks.sockets.at(-1)?.getAll("f")).toEqual(["service:web"])
  await page.keyboard.press("Escape")
  await page.getByRole("button", { name: "Clear the service filter" }).click()
  await expect.poll(() => mocks.sockets.at(-1)?.getAll("f")).toEqual([])

  await page.getByRole("button", { name: "Show the lines behind errors", exact: true }).click()
  await expect(
    page.getByRole("button", { name: "Show every line again, not only the errors" }),
  ).toHaveAttribute("aria-pressed", "true")
  await expect.poll(() => mocks.sockets.at(-1)?.get("levels")).toMatch(/error/)

  await page.getByRole("button", { name: "Events", exact: true }).click()
  const events = page.getByRole("region", { name: "Stack events" })
  await expect(events).toBeVisible()
  expect(mocks.events.at(-1)?.get("stack")).toBe("running-app")
  await expect(events.getByText("web", { exact: true }).first()).toBeVisible()
  await expect(events.getByText("running-app-web-1", { exact: true })).toHaveCount(0)
})

/**
 * Neither Docker page takes the shell sideways, at any width anybody has.
 *
 * A horizontal scrollbar on a dashboard is never local to the thing that caused
 * it: the page scrolls, and the navigation goes with it. The widths are the
 * breakpoint boundaries plus the two extremes — a small phone in portrait and a
 * wide desktop — because an overflow introduced by a layout swap shows up
 * within a pixel or two of the breakpoint that swapped it.
 */
for (const width of [320, 390, 640, 768, 1024, 1280, 1600]) {
  test(`no Docker page scrolls sideways at ${width}px`, async ({ page }) => {
    await mockDocker(page)
    await page.setViewportSize({ width, height: 900 })

    for (const path of [
      "/docker",
      "/docker/containers",
      "/docker/containers/1111111111111111",
      "/docker/stacks",
      "/docker/stacks/running-app",
      "/docker/images",
      "/docker/volumes",
      "/docker/networks",
      "/docker/events",
    ]) {
      await page.goto(path)
      await page.waitForLoadState("networkidle")
      const overflow = await page.evaluate(
        () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
      )
      expect(overflow, `${path} overflows at ${width}px`).toBeLessThanOrEqual(1)
    }
  })

  test(`no Docker logs view scrolls sideways at ${width}px`, async ({ page }) => {
    await mockDocker(page)
    await mockServiceLogs(page)
    await page.setViewportSize({ width, height: 900 })
    const overflow = () =>
      page.evaluate(
        () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
      )

    await page.goto(`/docker/containers/${DB}?tab=logs`)
    await expect(page.getByLabel("Log lines").getByText("deadlock", { exact: true })).toBeVisible()
    expect(await overflow(), `the container's logs overflow at ${width}px`).toBeLessThanOrEqual(1)
    await page.getByRole("button", { name: "Events", exact: true }).click()
    await expect(page.getByText(/^Restarted ×3/)).toBeVisible()
    expect(await overflow(), `the container's events overflow at ${width}px`).toBeLessThanOrEqual(1)

    await page.goto("/docker/stacks/running-app")
    await page.getByRole("tab", { name: "Logs" }).click()
    await expect(page.getByLabel("Log lines").getByText("listening on :3000")).toBeVisible()
    expect(await overflow(), `the stack's logs overflow at ${width}px`).toBeLessThanOrEqual(1)
  })
}

/**
 * The live dot is a claim about the data, and the claim has to be visible.
 *
 * A running container's status dot carries a halo that breathes, because this
 * table is fed by an open socket rather than a poll. It is asserted as a
 * running animation rather than as a class name: the whole point of the token is
 * that it resolves to motion, and the root `prefers-reduced-motion` rule is what
 * turns it off — which the design-system suite checks from the other side.
 */
test("a running container's dot says the reading is live", async ({ page }) => {
  await mockDocker(page)
  await page.goto("/docker/containers")

  const row = page.getByRole("row").filter({ hasText: "nginx:alpine" })
  await expect(row.getByText("Running")).toBeVisible()

  const breathing = await row.evaluate((el) =>
    [...el.querySelectorAll("span")].some((s) => getComputedStyle(s).animationName !== "none"),
  )
  expect(breathing, "no live halo on a running container").toBe(true)
})

/**
 * Every reading, on a screen with room for them.
 *
 * The containers are a table again, as Live, PM2 and Services are: each
 * column says what it is once, in its heading, and sorts by it.
 */
test("a container's row keeps every reading on a wide desktop", async ({ page }) => {
  await mockDocker(page)
  await page.setViewportSize({ width: 1600, height: 900 })
  await page.goto("/docker/containers")

  for (const name of ["Container", "State", "CPU", "Memory", "Network", "Ports"]) {
    await expect(page.getByRole("columnheader", { name })).toBeVisible()
  }
  const web = page.getByRole("row").filter({ hasText: "nginx:alpine" })
  await expect(web.getByRole("button", { name: "web", exact: true })).toBeVisible()
  await expect(web.getByText("nginx:alpine")).toBeVisible()
  await expect(web.getByText("Running")).toBeVisible()
  await expect(web.getByText(/^up 2h/)).toBeVisible()
  await expect(web.getByText("12.0%")).toBeVisible()
  await expect(web.getByText(/100\.0 MB/)).toBeVisible()
  await expect(web.getByText("443 → 443")).toBeVisible()
  await expect(web.getByRole("button", { name: "Stop" })).toBeVisible()

  // The mark is the image's product, served from this origin.
  await expect(web.locator("img").first()).toHaveAttribute("src", "/logos/nginx.svg")
})

/**
 * The table sorts by its headings, failing first until one is pressed, and a
 * figure's column puts the heaviest first.
 */
test("the containers sort by a heading and say so", async ({ page }) => {
  await mockDocker(page)
  await page.goto("/docker/containers")

  const names = () =>
    page
      .locator("tbody tr")
      .evaluateAll((rows) => rows.map((r) => r.getAttribute("data-workspace-name")))
  await expect(page.getByRole("columnheader", { name: "State" })).toHaveAttribute(
    "aria-sort",
    "ascending",
  )
  await expect.poll(names).toEqual(["db", "web"])

  await page.getByRole("button", { name: "CPU", exact: true }).click()
  await expect(page.getByRole("columnheader", { name: "CPU" })).toHaveAttribute(
    "aria-sort",
    "descending",
  )
  await expect.poll(names).toEqual(["web", "db"])
  await expect(page.getByText("by processor")).toBeVisible()
})

/**
 * A compose project is a filter, from its chip or from any of its rows.
 */
test("a stack narrows the table from its chip or its row", async ({ page }) => {
  await mockDocker(page)
  const stacked = containers.map((c, i) => ({
    ...c,
    composeStack: i === 0 ? "shop" : undefined,
    composeService: i === 0 ? "web" : undefined,
  }))
  await page.routeWebSocket(/\/api\/v1\/docker\/containers\/stream/, (socket) =>
    socket.send(JSON.stringify({ type: "containers", data: stacked })),
  )
  await page.goto("/docker/containers")

  await page.getByRole("button", { name: "Only the shop stack" }).click()
  await expect(page.getByRole("button", { name: /^shop/ })).toHaveAttribute("aria-pressed", "true")
  await expect(page.locator("tbody tr")).toHaveCount(1)

  await page.getByRole("button", { name: /^Standalone/ }).click()
  await expect(page.locator("tbody tr")).toHaveCount(1)
  await expect(page.getByRole("row").filter({ hasText: "postgres:16" })).toBeVisible()

  await page.keyboard.press("Escape")
  await expect(page.locator("tbody tr")).toHaveCount(2)
})

/**
 * What happened lately is Docker's own event log, with a restart loop as one
 * line and a kill for memory said on the exit it caused — which also turns
 * that container's row from "stopped" into what it is.
 */
test("recent events fold a restart loop and name a kill for memory", async ({ page }) => {
  await mockDocker(page)
  const killed = {
    ...containers[1],
    state: "exited",
    status: "Exited (137) 2 minutes ago",
    exposure: [],
  }
  await page.routeWebSocket(/\/api\/v1\/docker\/containers\/stream/, (socket) =>
    socket.send(JSON.stringify({ type: "containers", data: [containers[0], killed] })),
  )
  const at = (secondsAgo: number) => new Date(Date.now() - secondsAgo * 1000).toISOString()
  const ev = (secondsAgo: number, action: string, id: string, name: string, exitCode?: string) => ({
    time: at(secondsAgo),
    type: "container",
    action,
    id,
    name,
    exitCode,
    message: "",
    level: "notice",
    source: "docker",
  })
  await page.routeWebSocket(/\/api\/v1\/docker\/events\/stream/, (socket) =>
    socket.send(
      JSON.stringify({
        type: "events",
        data: [
          ...[0, 1, 2, 3].flatMap((i) => [
            ev(300 - i * 30, "die", "1111111111111111", "web", "1"),
            ev(299 - i * 30, "start", "1111111111111111", "web"),
          ]),
          ev(120, "oom", "2222222222222222", "db"),
          ev(120, "die", "2222222222222222", "db", "137"),
        ],
      }),
    ),
  )
  await page.goto("/docker/containers")

  const recent = page.getByRole("region", { name: "Recent container events" })
  await expect(recent.getByText(/restarted ×4 in \d+ min · exit 1/)).toBeVisible()
  await expect(recent.getByText("killed for memory")).toBeVisible()
  const db = page.getByRole("row").filter({ hasText: "postgres:16" })
  await expect(db.getByText("Out of memory")).toBeVisible()
  await expect(page.getByRole("button", { name: /^Failing/ })).toContainText("1")
})

test("the inspect tab does not undo the masking the environment tab applies", async ({ page }) => {
  await mockDocker(page)
  await page.route("**/api/v1/docker/containers/1111111111111111", (route) => json(route, detail))
  await page.route("**/api/v1/docker/containers/1111111111111111/raw", (route) =>
    json(route, rawInspect),
  )
  await page.goto("/docker/containers")
  await page.getByRole("button", { name: "web", exact: true }).first().click()

  // The Environment tab's own gate still works.
  await page.getByRole("tab", { name: "Environment" }).click()
  await expect(page.getByText(/values look like a credential/)).toBeVisible()
  await expect(page.getByText("s3cr3t-master")).toHaveCount(0)

  // And one tab over, the same values are still not on screen.
  await page.getByRole("tab", { name: "Inspect" }).click()
  await expect(page.getByText(/JD_MASTER_KEY/).first()).toBeVisible()
  await expect(page.getByText("s3cr3t-master")).toHaveCount(0)
  await expect(page.getByText("hunter2")).toHaveCount(0)
  await expect(page.getByText(/are hidden here too/)).toBeVisible()

  // A deliberate reveal still shows them — an admin may read their own keys.
  await page.getByRole("button", { name: "Reveal" }).click()
  await expect(page.getByText(/s3cr3t-master/)).toBeVisible()

  // Values that are not credential-shaped were never touched.
  await expect(page.getByText(/\/usr\/bin/).first()).toBeVisible()
})

/**
 * A volume was a name, a size and a path to somewhere else.
 *
 * "Browse files" closed this panel, changed page, and asked the operator to
 * recognise the volume again as a path under /var/lib/docker — for an answer
 * ("did the backup land", "what did it write") that is four lines long. The
 * contents are in the panel now, and the button survives inside the browser
 * pointed at whichever directory was reached.
 */
test("a volume's contents are in the volume, not a page away", async ({ page }) => {
  await mockDocker(page)
  await mockVolumeFiles(page)
  await page.route("**/api/v1/docker/volumes/app-data", (route) => json(route, volumes[0]))
  await page.goto("/docker/volumes")
  await page.getByRole("button", { name: "app-data", exact: true }).first().click()

  const panel = page.getByRole("dialog")
  await expect(panel.getByRole("button", { name: "postgresql.conf" })).toBeVisible()
  await expect(panel.getByRole("button", { name: "base" })).toBeVisible()

  // The file manager is still one click away, and it is handed the directory.
  await expect(panel.getByRole("link", { name: "Open in Files" })).toHaveAttribute(
    "href",
    "/files?path=%2Fvar%2Flib%2Fdocker%2Fvolumes%2Fapp-data%2F_data",
  )

  // Postgres' own files, under a running container: reading is safe, and the
  // editor one click below this line is not.
  await expect(panel.getByText(/corrupts it in ways that only show up later/)).toBeVisible()

  // And a file opens in the editor, with its contents, over the panel rather
  // than instead of it. Both are Radix dialogs, and a nested one that
  // dismissed its parent would drop the operator back on the volumes table
  // with no idea what they had been reading. While the editor is open the
  // panel is deliberately aria-hidden behind it; closing has to land back on
  // the volume, still showing its contents.
  await page.getByRole("button", { name: "postgresql.conf" }).click()
  await expect(page.getByRole("heading", { name: "postgresql.conf" })).toBeVisible()
  await expect(page.getByText("shared_buffers")).toBeVisible()

  await page.keyboard.press("Escape")
  await expect(page.getByRole("heading", { name: "postgresql.conf" })).toHaveCount(0)
  await expect(page.getByRole("heading", { name: "app-data" })).toBeVisible()
  await expect(page.getByRole("button", { name: "postgresql.conf" })).toBeVisible()
})

/**
 * The storage browser is the file manager's listing, not a different one.
 *
 * It was a monospaced `ls` in a box: a crumb in 11px mono, bare glyphs, no
 * column names and no way to tell a folder of pictures from a folder of
 * config. It is the Files page's own shape now — named columns, the kind's
 * coloured mark, a parent row, a count along the foot, and the grid view.
 */
test("the storage browser draws a directory the way the file manager does", async ({ page }) => {
  await mockDocker(page)
  await mockVolumeFiles(page)
  await page.route("**/api/v1/docker/volumes/app-data", (route) => json(route, volumes[0]))
  await page.goto("/docker/volumes")
  await page.getByRole("button", { name: "app-data", exact: true }).first().click()

  // The sheet has a table of its own, of the containers mounting the volume.
  const panel = page.getByRole("dialog").locator("[data-slot=pane]")
  await expect(panel.getByRole("columnheader", { name: "Name" })).toBeVisible()
  await expect(panel.getByRole("columnheader", { name: "Size" })).toBeVisible()
  await expect(panel.getByText("1 folder, 1 file · 28.0 KB")).toBeVisible()
  // The root has no level above it inside the volume, so no parent row.
  await expect(panel.getByText("Parent folder")).toHaveCount(0)

  await panel.getByRole("button", { name: "base" }).click()
  const crumbs = panel.getByRole("navigation", { name: "Location inside this storage" })
  await expect(crumbs.getByRole("button", { name: "base" })).toBeVisible()
  await expect(panel.getByText("Parent folder")).toBeVisible()

  await panel.getByRole("button", { name: "Grid view" }).click()
  await expect(panel.getByRole("button", { name: "Grid view" })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await expect(panel.getByRole("columnheader")).toHaveCount(0)
  await expect(panel.getByRole("button", { name: "postgresql.conf" })).toBeVisible()
})

/**
 * The Storage tab named every mount and showed the contents of none, and then
 * showed them only once a row was expanded.
 *
 * The one mount there is to look in is open with the tab, and with nothing to
 * choose between its row is not a control. A tmpfs row never grows one either:
 * it is memory in the container's namespace and there is nothing on this
 * filesystem to list.
 */
test("the storage tab opens onto what is in a mount", async ({ page }) => {
  await mockDocker(page)
  await mockVolumeFiles(page)
  await page.route("**/api/v1/docker/containers/1111111111111111", (route) => json(route, detail))
  await page.goto("/docker/containers/1111111111111111")
  await page.getByRole("tab", { name: "Storage" }).click()

  await expect(page.getByRole("button", { name: "postgresql.conf" })).toBeVisible()
  const mounts = page.getByRole("list", { name: "Mounts" })
  await expect(mounts.getByText("/usr/share/nginx/html")).toBeVisible()
  await expect(mounts.getByRole("button", { pressed: true })).toHaveCount(0)
  await expect(mounts.getByRole("button").filter({ hasText: "/usr/share/nginx/html" })).toHaveCount(
    0,
  )

  // Temporary memory names itself and offers nothing to open.
  await expect(page.getByRole("button").filter({ hasText: "Temporary memory" })).toHaveCount(0)
  await expect(page.getByText("Temporary memory")).toBeVisible()
})

/**
 * With two mounts to look in, the rows choose which one the listing shows.
 */
test("the storage tab switches the listing between mounts", async ({ page }) => {
  await mockDocker(page)
  await mockVolumeFiles(page)
  await page.route("**/api/v1/docker/containers/1111111111111111", (route) =>
    json(route, {
      ...detail,
      mounts: [
        ...detail.mounts,
        {
          type: "bind",
          name: "",
          source: "/srv/site/config",
          destination: "/etc/nginx/conf.d",
          mode: "",
          rw: false,
        },
      ],
    }),
  )
  await page.route("**/api/v1/files/list?*", (route, request) =>
    new URL(request.url()).searchParams.get("path") === "/srv/site/config"
      ? json(route, { path: "/srv/site/config", entries: [entry("default.conf", false, 512)] })
      : route.fallback(),
  )
  await page.goto("/docker/containers/1111111111111111")
  await page.getByRole("tab", { name: "Storage" }).click()

  const mounts = page.getByRole("list", { name: "Mounts" })
  const volume = mounts.getByRole("button").filter({ hasText: "/usr/share/nginx/html" })
  const bind = mounts.getByRole("button").filter({ hasText: "/etc/nginx/conf.d" })
  await expect(volume).toHaveAttribute("aria-pressed", "true")
  await expect(page.getByRole("button", { name: "postgresql.conf" })).toBeVisible()

  await bind.click()
  await expect(bind).toHaveAttribute("aria-pressed", "true")
  await expect(page.getByRole("button", { name: "default.conf" })).toBeVisible()
  await expect(page.getByRole("button", { name: "postgresql.conf" })).toHaveCount(0)
  await expect(mounts.getByText("read-only")).toBeVisible()
})

function usageSample(second: number, received: number) {
  return {
    id: detail.id,
    name: detail.name,
    ts: new Date(Date.now() - 4000 + second * 1000).toISOString(),
    cpuPercent: 150,
    cpuReady: true,
    cpuTotal: 1000000 + second * 1000,
    systemCpu: 100000000 + second * 8000,
    hostCpus: 8,
    onlineCpus: 8,
    cpuLimit: 2,
    cpuPeriods: second * 10,
    cpuThrottledPeriods: second,
    memUsage: 100 * 1024 * 1024,
    memRaw: 128 * 1024 * 1024,
    memCache: 28 * 1024 * 1024,
    memRss: 80 * 1024 * 1024,
    memSwap: null,
    memLimit: 512 * 1024 * 1024,
    memLimited: true,
    memPercent: 19.53,
    memHostPercent: 1.22,
    pids: 12,
    pidsLimit: 256,
    netRx: received,
    netTx: received / 2,
    networkAvailable: true,
    blockAvailable: true,
    blockRead: received * 2,
    blockWrite: received * 3,
    networks: {
      eth0: {
        rxBytes: received,
        txBytes: received / 2,
        rxPackets: received / 100,
        txPackets: received / 200,
        rxErrors: 0,
        txErrors: 2,
        rxDropped: 1,
        txDropped: 0,
      },
    },
  }
}

async function mockUsage(
  page: Page,
  options: { host?: boolean; stopped?: boolean; disabled?: boolean; idle?: boolean } = {},
) {
  await mockDocker(page)
  await page.route(`**/api/v1/docker/containers/${detail.id}`, (route) =>
    json(route, {
      ...detail,
      state: options.stopped ? "exited" : "running",
      networkMode: options.host ? "host" : "bridge",
      // What the frames say it may use, so the limits and the readings agree.
      memoryLimit: 512 * 1024 * 1024,
      cpuLimit: 2,
    }),
  )
  await page.route(`**/api/v1/docker/containers/${detail.id}/anomalies`, (route) =>
    json(route, { anomalies: [] }),
  )
  await page.route("**/api/v1/system/metrics/events**", (route) => json(route, []))
  await page.route(`**/api/v1/docker/containers/${detail.id}/stats/history**`, (route) => {
    if (options.disabled)
      return route.fulfill({
        status: 503,
        contentType: "application/json",
        body: JSON.stringify({
          error: { code: "metrics_history_disabled", message: "History disabled" },
        }),
      })
    return json(route, {
      name: "web",
      from: now,
      to: now,
      stepSeconds: 150,
      sampleIntervalSeconds: 15,
      retentionSeconds: 604800,
      earliest: now,
      points: Array.from({ length: 24 }, (_, i) => ({
        ts: new Date(Date.now() - (24 - i) * 150000).toISOString(),
        samples: 10,
        cpu: 20 + Math.sin(i) * 10,
        cpuPeak: 70,
        mem: 20,
        memPeak: 25,
        memBytes: (100 + Math.sin(i) * 8) * 1024 * 1024,
        memBytesPeak: 140 * 1024 * 1024,
        memLimit: 512 * 1024 * 1024,
        pids: 12,
        netRx: options.host ? null : options.idle ? 0 : 1024 * (20 + i),
        netTx: options.host ? null : options.idle ? 0 : 1024 * (10 + i),
        blockRead: 4096,
        blockWrite: 8192,
      })),
    })
  })
  let socket: import("@playwright/test").WebSocketRoute | undefined
  let connections = 0
  await page.routeWebSocket(
    new RegExp(`/api/v1/docker/containers/${detail.id}/stats/stream`),
    (ws) => {
      socket = ws
      connections++
    },
  )
  await page.goto(`/docker/containers/${detail.id}?tab=usage`)
  const sampleTime = Date.now() - 4000
  const send = async (second: number, received: number) => {
    await expect.poll(() => !!socket).toBe(true)
    const data = usageSample(second, received)
    data.ts = new Date(sampleTime + second * 1000).toISOString()
    if (options.host) {
      data.networks = {} as typeof data.networks
      data.networkAvailable = false
    }
    socket!.send(JSON.stringify({ type: "stats", data }))
  }
  return { send, connections: () => connections }
}

/**
 * The Usage tab reads a container the way a project's Runtime reads a
 * release — each chart headed by its reading now — and breaks those
 * readings down under the charts the way the Metrics page breaks the host
 * down.
 */
test("usage heads each chart with its reading now and breaks the readings down", async ({
  page,
}) => {
  const feed = await mockUsage(page)
  const usage = page.getByTestId("container-usage")
  await feed.send(0, 1024 * 1024)
  await feed.send(2, 1024 * 1024 + 4096)
  await expect(usage.getByText("Live", { exact: true }).first()).toBeVisible()
  // The processor's share of a core heads its chart, and a rate needs two frames.
  await expect(usage.getByText("150.0%", { exact: true })).toBeVisible()
  await expect(usage.getByText("2.0 KB/s").first()).toBeVisible()

  // Where the memory is: the inactive cache apart from the working set.
  await expect(usage.getByRole("heading", { name: "Allocation", exact: true })).toBeVisible()
  await expect(usage.getByText("28.0 MB", { exact: true })).toBeVisible()
  await expect(usage.getByText("Left to its limit")).toBeVisible()

  // Of the 2-core quota, of the machine, and how often the quota bit.
  const tile = (name: RegExp) => usage.locator("[data-slot=stat-tile]").filter({ hasText: name })
  await expect(tile(/of its quota/i)).toContainText("75.0%")
  await expect(tile(/throttled/i)).toContainText("10.0%")

  // Each interface as a row with its traffic bar.
  await expect(usage.getByRole("heading", { name: "Interfaces", exact: true })).toBeVisible()
  await expect(usage.getByRole("cell", { name: "eth0", exact: true })).toBeVisible()
  await expect(usage.getByRole("img", { name: /in, .* out/ }).first()).toBeVisible()
})

test("usage keeps its figures with history disabled and says when they go stale", async ({
  page,
}) => {
  await page.clock.install()
  const feed = await mockUsage(page, { disabled: true })
  await feed.send(0, 1000)
  await feed.send(2, 3000)
  const usage = page.getByTestId("container-usage")
  await expect(usage.getByText("Live", { exact: true }).first()).toBeVisible()
  await expect(page.getByText(/History is not being recorded/)).toBeVisible()
  await expect(usage.getByText("28.0 MB", { exact: true })).toBeVisible()
  await page.clock.fastForward(12000)
  await expect(usage.getByText("Readings stale", { exact: true })).toBeVisible()
})

test("a stopped container keeps its history and opens no socket", async ({ page }) => {
  const stopped = await mockUsage(page, { stopped: true })
  const usage = page.getByTestId("container-usage")
  await expect(usage.getByText("Not running", { exact: true })).toBeVisible()
  await expect(usage.getByRole("heading", { name: "Processor", exact: true })).toBeVisible()
  await expect(usage.getByText("Not running. These are read while it runs.").first()).toBeVisible()
  expect(stopped.connections()).toBe(0)
})

test("host networking says why it has no interfaces and where the traffic is", async ({ page }) => {
  const feed = await mockUsage(page, { host: true })
  await feed.send(0, 0)
  await expect(page.getByRole("link", { name: "View host network usage" })).toHaveAttribute(
    "href",
    "/metrics",
  )
  await expect(page.getByRole("cell", { name: "eth0", exact: true })).toHaveCount(0)
})

for (const width of [1280, 1720, 390]) {
  test(`usage has readable measurements without page overflow at ${width}px`, async ({
    page,
  }, testInfo) => {
    await page.setViewportSize({ width, height: 1000 })
    const feed = await mockUsage(page)
    await feed.send(0, 1024 * 1024)
    await feed.send(2, 1024 * 1024 + 4096)
    await expect(page.getByRole("heading", { name: "Interfaces", exact: true })).toBeVisible()
    await expect(page.getByRole("heading", { name: "Since it started", exact: true })).toBeVisible()
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true)
    await page.screenshot({
      path: testInfo.outputPath(`container-usage-${width}.png`),
      fullPage: true,
    })
  })
}

test("workspace: container name typing, adjacent details and Back retain list focus", async ({
  page,
}) => {
  await mockDocker(page)
  await page.route("**/api/v1/docker/containers/1111111111111111", (route) => json(route, detail))
  await page.route("**/api/v1/docker/containers/2222222222222222", (route) =>
    json(route, { ...detail, id: "2222222222222222", name: "db" }),
  )
  await page.goto("/docker/containers")
  await expect(page.locator("[data-workspace-item]").first()).toBeVisible()
  await page.locator("[data-workspace-item]").first().focus()
  // A table's row is what the keyboard moves between, as on Live and Services.
  const web = page.locator('tr[data-workspace-item="1111111111111111"]')
  await page.keyboard.press("w")
  await expect(web).toBeFocused()
  await page.keyboard.press("Enter")
  await expect(page).toHaveURL(/1111111111111111$/)
  // Both are running, so the table lists them by name: db, then web.
  await expect(page.getByRole("button", { name: "Next container" })).toBeDisabled()
  const previous = page.getByRole("button", { name: "Previous container" })
  await expect(previous).toBeEnabled()
  await previous.click()
  await expect(page).toHaveURL(/2222222222222222/)
  await page.goBack()
  await expect(page).toHaveURL(/1111111111111111$/)
  await page.goBack()
  await expect(page).toHaveURL(/\/docker\/containers$/)
  await expect(web).toBeFocused()
})

test("workspace: Back restores a long container list's scroll and focused row", async ({
  page,
}) => {
  await mockDocker(page)
  const inventory = Array.from({ length: 80 }, (_, index) => ({
    ...containers[0],
    id: String(index + 1).padStart(16, "0"),
    name: `web-${String(index).padStart(2, "0")}`,
  }))
  await page.routeWebSocket(/\/api\/v1\/docker\/containers\/stream/, (socket) =>
    socket.send(JSON.stringify({ type: "containers", data: inventory })),
  )
  await page.route("**/api/v1/docker/containers/0000000000000051", (route) =>
    json(route, { ...detail, id: "0000000000000051", name: "web-50" }),
  )
  await page.goto("/docker/containers")
  const row = page.getByRole("button", { name: "web-50", exact: true })
  await row.scrollIntoViewIfNeeded()
  await row.focus()
  // The table is a scroll region of its own (§2), so that is where the place is kept.
  const shell = page.locator("[data-slot=table-container]:visible")
  const before = await shell.evaluate((element) => element.scrollTop)
  expect(before).toBeGreaterThan(1000)
  await page.keyboard.press("Enter")
  await expect(page).toHaveURL(/0000000000000051$/)
  await page.goBack()
  await expect(page).toHaveURL(/\/docker\/containers$/)
  const item = page.locator('tr[data-workspace-item="0000000000000051"]')
  await expect(item).toBeFocused()
  await expect.poll(() => shell.evaluate((element) => element.scrollTop)).toBe(before)
  await page.reload()
  await expect(item).toBeFocused()
  await expect.poll(() => shell.evaluate((element) => element.scrollTop)).toBe(before)
})

/**
 * A published port's verdict is the binding, the proxy and the firewall read
 * together, with the reasoning on its row; an administrator can open every
 * layer between the outside and the container — Docker's NAT, DOCKER-USER,
 * provider policy — from the Ports table, with the ones nothing on the host
 * can see said as unknown.
 */
test("an administrator traces a published port's path from outside, layer by layer", async ({
  page,
}) => {
  await mockDocker(page)
  await page.route(`**/api/v1/docker/containers/${detail.id}`, (route) => json(route, detail))
  await page.route(`**/api/v1/docker/containers/${detail.id}/routes`, (route) =>
    json(route, [
      {
        hostIp: "0.0.0.0",
        hostPort: 443,
        containerPort: 443,
        protocol: "tcp",
        public: true,
        scope: "all",
        label: "Every interface",
        binding: "Bound to every interface on port 443.",
        firewall: {
          known: true,
          backend: "ufw",
          enabled: true,
          verdict: "default",
          defaultIncoming: "deny",
          dockerBypass: true,
        },
        reach: "external",
        reasoning:
          "Bound to every interface. Docker's own NAT rules are consulted before ufw's, so the default policy does not hold it back. Provider policy in front of this host is not visible here, so reaching it from outside is unproven until an external check measures it.",
        inferred: true,
      },
    ]),
  )
  let asked: URL | undefined
  await page.route(`**/api/v1/docker/containers/${detail.id}/published/443?**`, (route) => {
    asked = new URL(route.request().url())
    return json(route, {
      request: {
        sourceKind: "external",
        containerId: detail.id,
        family: "inet",
        protocol: "tcp",
        port: 443,
        target: "",
        measure: false,
      },
      scope: {
        vantage: "published_port",
        source: "Outside this host",
        target: "web",
        address: "172.17.0.2",
        family: "inet",
        protocol: "tcp",
        port: 443,
        limitations: ["Each layer is a snapshot read in sequence; no packet was sent or traced."],
      },
      startedAt: now,
      endedAt: now,
      addresses: ["172.17.0.2"],
      comparison:
        "Docker NAT: observed. Forwarded-leg firewall prediction: unknown. Provider policy: unknown. External measurement: none retained.",
      evidence: [
        {
          id: "dnat",
          title: "Docker NAT",
          basis: "observed",
          state: "observed",
          scope: "nat table, DOCKER chain",
          owner: "Docker",
          ownerPath: "/docker",
          checkedAt: now,
          summary: "Docker's rule translates host port 443 to 172.17.0.2:443.",
          facts: [],
          limitations: [],
        },
        {
          id: "provider",
          title: "Provider policy",
          basis: "unknown",
          state: "unknown",
          scope: "upstream of this host",
          owner: "Provider",
          checkedAt: now,
          summary: "No provider adapter is configured.",
          facts: [],
          limitations: [],
        },
      ],
    })
  })
  await page.goto(`/docker/containers/${detail.id}`)
  const ports = page.getByRole("table").filter({ hasText: "Published" })
  const port = ports.getByRole("row").filter({ hasText: "443/tcp" })
  await expect(port).toHaveAttribute("title", /reaching it from outside is unproven/)
  await port.getByRole("button", { name: "Trace the path to port 443/tcp", exact: true }).click()
  const sheet = page.getByRole("dialog", { name: "Path to host port 443" })
  await expect(sheet.getByRole("heading", { name: "Outside this host → web" })).toBeVisible()
  await expect(sheet.getByText(/any outside address → 172.17.0.2/)).toBeVisible()
  await expect(sheet.getByRole("heading", { name: "Provider policy", exact: true })).toBeVisible()
  expect(asked?.searchParams.get("family")).toBe("inet")
  expect(asked?.searchParams.get("protocol")).toBe("tcp")

  await page.route("**/api/v1/auth/session", (route) =>
    json(route, {
      ...user,
      capabilities: ["read", "service.control"],
      user: { ...user.user, role: "limited" },
    }),
  )
  await page.reload()
  await expect(port).toHaveAttribute("title", /reaching it from outside is unproven/)
  await expect(page.getByRole("button", { name: /^Trace the path/ })).toHaveCount(0)
})
