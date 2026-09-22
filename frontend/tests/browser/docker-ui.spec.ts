import { expect, test, type Page, type Route } from "@playwright/test"

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
 * attention are separate tiles, and neither claims to be the other.
 */
test("the overview separates runtime health from attention", async ({ page }) => {
  await mockDocker(page)
  await page.goto("/docker")

  await expect(page.getByText("Runtime health")).toBeVisible()
  await expect(page.getByText("2 / 2 running")).toBeVisible()
  await expect(page.getByText("2 running, 1 without a health check")).toBeVisible()

  // And, at the same time, that something needs attention.
  await expect(page.getByText("Attention", { exact: true }).first()).toBeVisible()
  await expect(page.getByText("2 issues").first()).toBeVisible()

  // The word that used to sit above a page of warnings must not appear.
  await expect(page.getByText("All good")).toHaveCount(0)
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

  await expect(page.getByText("Not deployed · 3 services defined").first()).toBeVisible()
  await expect(page.getByText("Running · 2/2 services").first()).toBeVisible()
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

test("a volume in use offers no delete button", async ({ page }) => {
  await mockDocker(page)
  await page.goto("/docker/volumes")

  const inUse = page.getByRole("row").filter({ hasText: "app-data" })
  await expect(inUse.getByText("1 container")).toBeVisible()
  await expect(inUse.getByRole("button", { name: "Remove", exact: true })).toHaveCount(0)

  // The unattached one still can be removed — the gate is usage, not caution.
  const free = page.getByRole("row").filter({ hasText: "orphaned" })
  await expect(free.getByRole("button", { name: "Remove", exact: true })).toHaveCount(1)
  // And an unmeasured size says which of the three things a dash used to mean.
  await expect(free.getByText("not measured")).toBeVisible()
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
  await expect(page.getByRole("columnheader", { name: "Issues" })).toBeVisible()
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
  await expect(db.getByText(/no limit/)).toBeVisible()

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
  // because the wide table is still in the document with `display: none` and
  // a bare text query would match its cells too.
  const list = page.getByRole("list").filter({ hasText: "nginx:alpine" })
  await expect(list.getByText("nginx:alpine")).toBeVisible()
  await expect(list.getByText("postgres:16")).toBeVisible()
  await expect(list.getByText("Running").first()).toBeVisible()
  await expect(list.getByText("443 → 443")).toBeVisible()
  // The memory figure is the one that used to be truncated to "100.0…" when
  // the readings shared the row with the action cluster.
  await expect(list.getByText("512.0 MB")).toBeVisible()
  await expect(list.getByText(/no limit/)).toBeVisible()
  await expect(list.getByText("1 issue").first()).toBeVisible()

  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  )
  expect(overflow).toBeLessThanOrEqual(1)
})

/**
 * The containers page was the only one that replaced its table on a phone; the
 * image, volume and network tables were still the remains of one: columns
 * dropped until a wide first cell and two stubs were left. The same test
 * applies to them as to the containers list — the table is replaced, not
 * squeezed, and nothing the table carried is lost on the way.
 */
test("the image, volume and network lists read down the row on a phone", async ({ page }) => {
  await mockDocker(page)
  await page.setViewportSize({ width: 390, height: 844 })

  await page.goto("/docker/images")
  await expect(page.getByRole("columnheader")).toHaveCount(0)
  const imageList = page.getByRole("list").filter({ hasText: "nginx:alpine" })
  await expect(imageList.getByText("nginx:alpine")).toBeVisible()
  // The dangling image has no name and is reachable by its id — "untagged" is
  // the only honest thing the row can say, so that is what it says.
  await expect(imageList.getByText("untagged")).toBeVisible()
  await expect(imageList.getByRole("button", { name: "Remove image" })).toBeVisible()

  await page.goto("/docker/volumes")
  await expect(page.getByRole("columnheader")).toHaveCount(0)
  const volumeList = page.getByRole("list").filter({ hasText: "app-data" })
  await expect(volumeList.getByRole("button", { name: "app-data" })).toBeVisible()
  await expect(volumeList.getByText("not measured")).toBeVisible()
  // The volume Docker's own prune would delete while calling it unused.
  await expect(volumeList.getByText("unused")).toBeVisible()

  await page.goto("/docker/networks")
  await expect(page.getByRole("columnheader")).toHaveCount(0)
  const networkList = page.getByRole("list").filter({ hasText: "172.17.0.0/16" })
  await expect(networkList.getByRole("button", { name: "bridge" })).toBeVisible()
  await expect(networkList.getByText("172.17.0.0/16")).toBeVisible()
  await expect(networkList.getByText("2 containers")).toBeVisible()
  await expect(networkList.getByText("Docker system")).toBeVisible()
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
 * a menu where each verb carries a line of plain English.
 */
test("the destructive verbs are words with a sentence, not glyphs", async ({ page }) => {
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
  await expect(menu.getByText(/Pulls a newer nginx:alpine/)).toBeVisible()
  await expect(menu.getByText("Remove")).toBeVisible()
  await expect(menu.getByText(/Named volumes and their data are kept/)).toBeVisible()
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

  // Nothing is stopped on this host, so that chip is not offered at all —
  // a filter that can only ever return nothing is furniture.
  await expect(page.getByRole("button", { name: /^Not running/ })).toHaveCount(0)

  await page.getByRole("button", { name: /^Needs attention/ }).click()
  await expect(page.getByRole("row").filter({ hasText: "nginx:alpine" })).toBeVisible()
})

/**
 * Runtime health states four numbers and the relationship between them.
 *
 * It was a 2×4 grid of figures, which cannot show that half the estate is
 * unwatched. The bar can, and every count — including the zeroes — is still
 * printed, because a category that stops being mentioned the moment it goes
 * right is a category nobody learns to check.
 */
test("runtime health keeps every count, including the zeroes", async ({ page }) => {
  await mockDocker(page)
  await page.goto("/docker/containers")

  await expect(page.getByText("passing a health check")).toBeVisible()
  await expect(page.getByText("failing one")).toBeVisible()
  await expect(page.getByText("without one")).toBeVisible()
  await expect(page.getByText("2 of 2 running")).toBeVisible()
  await expect(page.getByText("still starting")).toBeVisible()
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
 * Every column, on a screen with room for them.
 *
 * `CPU · 1h` waits for `2xl` rather than `xl`: it is the ninth column, and at
 * 1280 with the sidebar open it is the one that pushes Issues and the row's
 * actions past the panel's right edge.
 */
test("the containers table keeps every column on a wide desktop", async ({ page }) => {
  await mockDocker(page)
  await page.setViewportSize({ width: 1600, height: 900 })
  await page.goto("/docker/containers")

  for (const name of [
    "Container",
    "Image",
    "Status",
    "CPU",
    "Memory",
    "CPU · 1h",
    "Ports",
    "Issues",
  ]) {
    await expect(page.getByRole("columnheader", { name, exact: true })).toBeVisible()
  }
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
 * The Storage tab named every mount and showed the contents of none.
 *
 * A row opens onto what is in it. A tmpfs row does not, and must not grow a
 * control that could only fail: it is memory in the container's namespace and
 * there is nothing on this filesystem to list.
 */
test("the storage tab opens onto what is in a mount", async ({ page }) => {
  await mockDocker(page)
  await mockVolumeFiles(page)
  await page.route("**/api/v1/docker/containers/1111111111111111", (route) => json(route, detail))
  await page.goto("/docker/containers/1111111111111111")
  await page.getByRole("tab", { name: "Storage" }).click()

  const volume = page.getByRole("button").filter({ hasText: "/usr/share/nginx/html" })
  await expect(volume).toHaveAttribute("aria-expanded", "false")
  await volume.click()
  await expect(page.getByRole("button", { name: "postgresql.conf" })).toBeVisible()

  // Temporary memory names itself and offers nothing to open.
  await expect(page.getByRole("button").filter({ hasText: "Temporary memory" })).toHaveCount(0)
  await expect(page.getByText("Temporary memory")).toBeVisible()
})
