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
  capabilities: ["read", "service.control", "file.write", "terminal", "destructive", "system.admin"],
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
  await expect(page.getByText("Everything is up")).toBeVisible()
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

  // The severity filter is a real filter, and its selected state is a fill
  // rather than the outline that keyboard focus also uses — so `aria-pressed`
  // is the assertion, not a colour.
  const critical = page.getByRole("button", { name: /^Critical/ })
  await critical.click()
  await expect(critical).toHaveAttribute("aria-pressed", "true")
  await expect(page.getByText("db has no health check")).toHaveCount(0)
  await expect(page.getByText("web can control Docker itself")).toBeVisible()

  // "All" is a state of its own rather than every box ticked, and returns the
  // full list.
  await page.getByRole("button", { name: /^All/ }).click()
  await expect(page.getByText("db has no health check")).toBeVisible()
})

/** "0/0 up" is not a state. */
test("a stack that was never deployed says so", async ({ page }) => {
  await mockDocker(page)
  await page.goto("/docker/stacks")

  await expect(page.getByText("Not deployed · 3 services defined").first()).toBeVisible()
  await expect(page.getByText("Running · 2/2 services").first()).toBeVisible()
  await expect(page.getByText("0/0")).toHaveCount(0)

  // And the two counts the overview and this page used to disagree about are
  // now the same sentence.
  await expect(page.getByText(/1 active · 2 detected/)).toBeVisible()
})

test("a volume in use offers no delete button", async ({ page }) => {
  await mockDocker(page)
  await page.goto("/docker/volumes")

  const inUse = page.getByRole("row").filter({ hasText: "app-data" })
  await expect(inUse.getByText("in use by 1")).toBeVisible()
  await expect(inUse.getByRole("button", { name: "Remove" })).toHaveCount(0)

  // The unattached one still can be removed — the gate is usage, not caution.
  const free = page.getByRole("row").filter({ hasText: "orphaned" })
  await expect(free.getByRole("button", { name: "Remove" })).toHaveCount(1)
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
  await expect(page.getByRole("columnheader", { name: "CPU · 1h" })).toBeVisible()
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

test("attention collapses one problem repeated across containers into one row", async ({ page }) => {
  await mockDocker(page)
  // Registered after mockDocker, so it wins for this one path.
  await page.route("**/api/v1/docker/health", (route) => json(route, repeated))
  await page.goto("/docker")

  // Five containers, one sentence — and pluralised into "have".
  await expect(page.getByText("5 containers have no health check")).toBeVisible()
  await expect(page.getByText("5 containers have no memory limit")).toBeVisible()

  // The individual titles are not in the list until the group is opened.
  await expect(page.getByText("api has no health check")).toHaveCount(0)

  // The header says it is fewer problems than findings. It used to say so in a
  // sentence under the title; the design system has no descriptions there, so
  // the number sits beside the issue count instead.
  await expect(page.getByText(/^\d+ distinct$/)).toBeVisible()

  // Severity still wins over frequency: the one critical stays on top.
  const rows = await page.getByRole("button", { expanded: false }).allInnerTexts()
  const critical = rows.findIndex((t) => t.includes("web can control Docker itself"))
  const group = rows.findIndex((t) => t.includes("containers have no health check"))
  expect(critical).toBeGreaterThanOrEqual(0)
  expect(critical).toBeLessThan(group)

  // Opening it names the containers, and keeps the shared advice stated once.
  await page.getByRole("button", { name: /5 containers have no health check/ }).click()
  await expect(page.getByText("api has no health check")).toBeVisible()
  await expect(page.getByText("cache has no health check")).toBeVisible()
})

/**
 * The containers table on a phone.
 *
 * Ten columns with no small-screen treatment forced the page itself to scroll
 * sideways, which takes the navigation with it. The columns that go are the
 * ones a phone reader can do without; what a container is running is not one
 * of them, so the image moves into the name cell rather than disappearing.
 */
test("the containers table fits a phone without taking the page sideways", async ({ page }) => {
  await mockDocker(page)
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/docker/containers")

  await expect(page.getByRole("columnheader", { name: "Container" })).toBeVisible()
  await expect(page.getByRole("columnheader", { name: "Issues" })).toBeVisible()
  // Dropped rather than squeezed.
  await expect(page.getByRole("columnheader", { name: "CPU · 1h" })).toBeHidden()
  await expect(page.getByRole("columnheader", { name: "Ports" })).toBeHidden()
  await expect(page.getByRole("columnheader", { name: "Image" })).toBeHidden()

  // But the image is still readable, in the name cell.
  await expect(page.getByText("nginx:alpine").first()).toBeVisible()

  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  )
  expect(overflow).toBeLessThanOrEqual(1)
})

test("the containers table keeps every column on a desktop", async ({ page }) => {
  await mockDocker(page)
  await page.setViewportSize({ width: 1600, height: 900 })
  await page.goto("/docker/containers")

  for (const name of ["Container", "Image", "Status", "CPU", "Memory", "CPU · 1h", "Ports", "Issues"]) {
    await expect(page.getByRole("columnheader", { name, exact: true })).toBeVisible()
  }
})

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
  mounts: [],
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

const rawInspect = {
  Id: "1111111111111111",
  Config: {
    Image: "nginx:alpine",
    Env: ["PATH=/usr/bin", "JD_MASTER_KEY=s3cr3t-master", "JD_BOOTSTRAP_PASSWORD=hunter2"],
  },
}

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
