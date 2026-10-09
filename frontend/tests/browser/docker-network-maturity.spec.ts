import { expect, test, type Page } from "@playwright/test"
import { admin, json, mockNetwork, type Mutation } from "./network-fixture"

/**
 * Docker networks as the Engine and the dashboard both own them: who created
 * each one, what a connect, a disconnect, a removal or a prune would disturb
 * — read before it happens and refused where the backend refuses — and what
 * this Engine can create a network with. Every preview here is the backend's
 * shape; the page decides nothing the preview did not say.
 */

const now = new Date().toISOString()

const network = (over: Record<string, unknown>) => ({
  driver: "bridge",
  scope: "local",
  internal: false,
  attachable: false,
  ipv6: false,
  created: now,
  labels: {},
  subnets: [],
  containers: 0,
  usedBy: [],
  membersKnown: true,
  ...over,
})

const networks = [
  network({
    id: "n-bridge",
    name: "bridge",
    subnets: ["172.17.0.0/16"],
    usedBy: ["web"],
    owner: { kind: "system" },
  }),
  network({
    id: "n-internal",
    name: "just-dashboard_internal",
    subnets: ["10.10.0.0/24"],
    usedBy: ["jd-frontend"],
    owner: { kind: "dashboard", project: "just-dashboard" },
  }),
  network({
    id: "n-lab",
    name: "lab",
    subnets: ["10.4.0.0/24"],
    usedBy: ["api", "just-dashboard-ingress"],
    owner: { kind: "compose", project: "shop" },
  }),
  network({
    id: "n-db",
    name: "jd-e7-db-abc",
    usedBy: ["shop-web"],
    owner: { kind: "database-link", environmentId: 7, deployment: "shop · production" },
  }),
  network({ id: "n-spare", name: "spare", owner: { kind: "manual" } }),
  network({
    id: "n-managed",
    name: "jd-e9-net",
    owner: { kind: "deployment", environmentId: 9, deployment: "blog · production" },
  }),
]

const labDetail = {
  ...networks[2],
  gateway: "10.4.0.1",
  options: {},
  system: false,
  members: [
    {
      id: "api-full-id",
      name: "api",
      ipv4: "10.4.0.3/24",
      ipv6: "fd00:4::3/64",
      aliases: ["api", "app"],
      state: "running",
      stack: "shop",
      networks: ["bridge"],
    },
    {
      id: "ingress-full-id",
      name: "just-dashboard-ingress",
      ipv4: "10.4.0.4/24",
      aliases: [],
      state: "running",
      networks: ["bridge"],
      ingress: true,
    },
  ],
}

const internalDetail = {
  ...networks[1],
  gateway: "10.10.0.1",
  options: {},
  system: false,
  members: [
    {
      id: "frontend-full-id",
      name: "jd-frontend",
      ipv4: "10.10.0.2/24",
      aliases: ["frontend"],
      state: "running",
      stack: "just-dashboard",
      networks: [],
      dashboard: true,
    },
  ],
}

const candidates = [
  { id: "cache-full-id", name: "cache", state: "running" },
  { id: "batch-full-id", name: "batch", state: "exited" },
]

const preview = (over: Record<string, unknown>) => ({
  network: "lab",
  networkId: "n-lab",
  owner: { kind: "compose", project: "shop" },
  conflicts: [],
  blocked: false,
  checkedAt: now,
  ...over,
})

async function setup(page: Page, mutations: Mutation[] = [], list: unknown[] = networks) {
  await mockNetwork(page, mutations, {
    overrides: {
      "/docker/ping": { available: true },
      "/docker/networks/": list,
      "/docker/networks/n-lab": labDetail,
      "/docker/networks/n-internal": internalDetail,
      "/docker/containers/": candidates,
    },
  })
}

test("networks name their owners, and unread membership never reads as unused", async ({
  page,
}) => {
  await setup(page)
  await page.goto("/docker/networks")
  const list = page.getByRole("list", { name: "Networks" })
  await expect(list.getByText("This dashboard", { exact: true })).toBeVisible()
  await expect(list.getByText("Compose · shop", { exact: true })).toBeVisible()
  await expect(list.getByText("Database link · shop · production", { exact: true })).toBeVisible()
  await expect(list.getByText("Deployment · blog · production", { exact: true })).toBeVisible()
  await expect(list.getByText("Created by hand", { exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: "Prune", exact: true })).toBeVisible()

  // The container listing failed: the networks are listed, their members are not.
  await setup(
    page,
    [],
    networks.map((n) => ({ ...n, usedBy: [], membersKnown: false, membersError: "daemon busy" })),
  )
  await page.reload()
  await expect(page.getByText("Which containers use each network could not be read")).toBeVisible()
  await expect(list.getByText("members unread").first()).toBeVisible()
  await expect(list.getByText("0 containers")).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Prune", exact: true })).toHaveCount(0)
  await expect(list.getByRole("button", { name: "Remove", exact: true })).toHaveCount(0)
})

test("a network's detail names its owner, both families and the networks its members join", async ({
  page,
}) => {
  await setup(page)
  await page.goto("/docker/networks")
  await page.getByRole("button", { name: "lab", exact: true }).click()
  const panel = page.getByRole("dialog", { name: "lab" })
  await expect(panel.getByText("Compose · shop", { exact: true })).toBeVisible()
  await expect(
    panel.getByText("Compose recreates it the next time its project comes up."),
  ).toBeVisible()
  // Attaching depends on the scope, not on --attachable: a local bridge takes members.
  await expect(panel.getByText("yes, while running", { exact: true })).toBeVisible()
  await expect(panel.getByText("10.4.0.3/24 · fd00:4::3/64 · also on bridge")).toBeVisible()
  await expect(panel.getByText("Shared ingress", { exact: true })).toBeVisible()
  await expect(panel.getByRole("button", { name: "Detach api", exact: true })).toHaveCount(1)
  await expect(
    panel.getByRole("button", { name: "Detach just-dashboard-ingress", exact: true }),
  ).toHaveCount(0)
  await expect(panel.getByRole("button", { name: /^The shared public Caddy/ })).toHaveCount(1)
  await page.keyboard.press("Escape")

  await page.getByRole("button", { name: "just-dashboard_internal", exact: true }).click()
  const own = page.getByRole("dialog", { name: "just-dashboard_internal" })
  await expect(
    own.getByText("The dashboard's own private network takes no other containers."),
  ).toBeVisible()
  await expect(own.getByRole("button", { name: "Attach a container", exact: true })).toHaveCount(0)
  // The member's own mark, beside the owner line that says the same of the network.
  await expect(own.locator('[data-slot="tag"]', { hasText: "This dashboard" })).toBeVisible()
  await expect(own.getByRole("button", { name: "Detach jd-frontend", exact: true })).toHaveCount(0)
})

test("attaching previews shared names and refusals, and keeps the draft when the preview fails", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await setup(page, mutations)
  let answer: "warn" | "block" | "fail" = "warn"
  await page.route("**/api/v1/docker/networks/n-lab/connect?**", (route) => {
    const url = new URL(route.request().url())
    const container = url.searchParams.get("container")
    if (answer === "fail")
      return json(route, { error: { code: "dependencies_unread", message: "daemon busy" } }, 503)
    if (answer === "block")
      return json(
        route,
        preview({
          container,
          blocked: true,
          conflicts: [
            {
              code: "invalid_alias",
              level: "block",
              message: '"bad name" is not a name Docker\'s resolver can answer.',
            },
          ],
        }),
      )
    return json(
      route,
      preview({
        container,
        conflicts: [
          {
            code: "shared_name",
            level: "warn",
            message: "On lab, db already answers for postgres.",
          },
          { code: "stopped", level: "info", message: "It joins lab when it next starts." },
        ],
      }),
    )
  })
  await page.goto("/docker/networks")
  await page.getByRole("button", { name: "lab", exact: true }).click()
  await page.getByRole("button", { name: "Attach a container", exact: true }).click()
  const dialog = page.getByRole("dialog", { name: "Attach a container" })
  await dialog.getByRole("combobox").click()
  await page.getByRole("option", { name: "cache" }).click()
  await dialog.getByRole("textbox").fill("db")
  const before = dialog.getByRole("list", { name: "What this change runs into" })
  await expect(before.getByText("On lab, db already answers for postgres.")).toBeVisible()
  await expect(before.getByText("It joins lab when it next starts.")).toBeVisible()
  const attach = dialog.getByRole("button", { name: "Attach", exact: true })
  await expect(attach).toBeEnabled()

  answer = "block"
  await dialog.getByRole("textbox").fill("bad name")
  await expect(before.getByText(/is not a name Docker's resolver can answer/)).toBeVisible()
  await expect(attach).toBeDisabled()

  answer = "fail"
  await dialog.getByRole("textbox").fill("database")
  await expect(dialog.getByText(/What attaching would run into could not be read/)).toBeVisible()
  await expect(attach).toBeDisabled()
  await expect(dialog.getByRole("textbox")).toHaveValue("database")
  await expect(dialog.getByRole("combobox")).toContainText("cache")

  answer = "warn"
  await dialog.getByRole("button", { name: "Check again", exact: true }).click()
  await expect(attach).toBeEnabled()
  await attach.click()
  await expect(dialog).not.toBeVisible()
  expect(mutations).toContainEqual({
    method: "POST",
    path: "/docker/networks/n-lab/connect",
    body: { container: "cache-full-id", aliases: ["database"] },
  })
})

test("detaching names who loses the member, and a refused detach cannot be confirmed", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await setup(page, mutations)
  let blocked = false
  await page.route("**/api/v1/docker/networks/n-lab/disconnect?**", (route) =>
    json(
      route,
      blocked
        ? preview({
            container: "api-full-id",
            blocked: true,
            conflicts: [
              {
                code: "dashboard_container",
                level: "block",
                message: "api is part of the dashboard itself (just-dashboard).",
              },
            ],
          })
        : preview({
            container: "api-full-id",
            conflicts: [
              {
                code: "published_ports",
                level: "warn",
                message:
                  "Its published ports (80→8080) stop answering if Docker carries them on this network rather than on its other one.",
              },
              {
                code: "peers",
                level: "warn",
                message:
                  "worker reaches api on lab as api, app; those names stop resolving for them.",
              },
            ],
          }),
    ),
  )
  await page.goto("/docker/networks")
  await page.getByRole("button", { name: "lab", exact: true }).click()
  await page.getByRole("button", { name: "Detach api", exact: true }).click()
  const dialog = page.getByRole("dialog", { name: "Detach api" })
  await expect(dialog.getByText(/worker reaches api on lab as api, app/)).toBeVisible()
  await expect(dialog.getByText(/published ports \(80→8080\)/)).toBeVisible()
  await dialog.getByRole("button", { name: "Detach", exact: true }).click()
  await expect(dialog).not.toBeVisible()
  expect(mutations).toContainEqual({
    method: "POST",
    path: "/docker/networks/n-lab/disconnect",
    body: { container: "api-full-id" },
  })

  blocked = true
  await page.getByRole("button", { name: "Detach api", exact: true }).click()
  await expect(dialog.getByText(/part of the dashboard itself/)).toBeVisible()
  await expect(dialog.getByRole("button", { name: "Detach", exact: true })).toBeDisabled()
  await expect(dialog.getByText(/refused until what is marked first changes/)).toBeVisible()
})

test("a removal says what still names the network, and a live deployment's network is refused", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await setup(page, mutations)
  await page.route("**/api/v1/docker/networks/n-spare/removal", (route) =>
    json(
      route,
      preview({
        network: "spare",
        networkId: "n-spare",
        owner: { kind: "manual" },
        conflicts: [
          {
            code: "stopped_dependents",
            level: "warn",
            message: "batch still names this network and fails to start until it exists again.",
          },
          {
            code: "ipam_reservation",
            level: "warn",
            message:
              "The shared IPAM reservation 10.9.0.0/24 still records this network as its owner.",
          },
        ],
      }),
    ),
  )
  await page.route("**/api/v1/docker/networks/n-managed/removal", (route) =>
    json(
      route,
      preview({
        network: "jd-e9-net",
        networkId: "n-managed",
        owner: { kind: "deployment", environmentId: 9, deployment: "blog · production" },
        blocked: true,
        conflicts: [
          {
            code: "managed_network",
            level: "block",
            message:
              "jd-e9-net is the network of deployment blog · production, which still exists.",
          },
        ],
      }),
    ),
  )
  await page.goto("/docker/networks")
  const list = page.getByRole("list", { name: "Networks" })
  const spare = list.getByRole("listitem").filter({ hasText: "spare" })
  await spare.getByRole("button", { name: "Remove", exact: true }).click()
  const dialog = page.getByRole("dialog", { name: "Delete spare" })
  await expect(dialog.getByText(/batch still names this network/)).toBeVisible()
  await expect(dialog.getByText(/IPAM reservation 10.9.0.0\/24/)).toBeVisible()
  await dialog.getByRole("button", { name: "Delete", exact: true }).click()
  await expect(dialog).not.toBeVisible()
  expect(mutations).toContainEqual({
    method: "DELETE",
    path: "/docker/networks/n-spare",
    body: null,
  })

  const managed = list.getByRole("listitem").filter({ hasText: "jd-e9-net" })
  await managed.getByRole("button", { name: "Remove", exact: true }).click()
  const refused = page.getByRole("dialog", { name: "Delete jd-e9-net" })
  await expect(refused.getByText(/deployment blog · production, which still exists/)).toBeVisible()
  await expect(refused.getByRole("button", { name: "Delete", exact: true })).toBeDisabled()
})

test("the reviewed prune removes only what nothing names and says why the rest is kept", async ({
  page,
}) => {
  await setup(page)
  let pruned: unknown
  await page.route("**/api/v1/docker/networks/prune", (route) => {
    if (route.request().method() === "POST") {
      pruned = route.request().postDataJSON()
      return json(route, { kind: "networks", spaceReclaimed: 0, items: ["spare"], skipped: [] })
    }
    return json(route, {
      checkedAt: now,
      candidates: [
        { id: "n-spare", name: "spare", owner: { kind: "manual" }, conflicts: [], removable: true },
        {
          id: "n-dormant",
          name: "dormant",
          owner: { kind: "compose", project: "batch" },
          removable: false,
          conflicts: [
            {
              code: "compose_recreates",
              level: "info",
              message: "Compose project batch declares it.",
            },
            {
              code: "stopped_dependents",
              level: "warn",
              message:
                "batch-worker still names this network and fails to start until it exists again.",
            },
          ],
        },
        {
          id: "n-managed",
          name: "jd-e9-net",
          owner: { kind: "deployment", environmentId: 9, deployment: "blog · production" },
          removable: false,
          conflicts: [
            {
              code: "managed_network",
              level: "block",
              message:
                "jd-e9-net is the network of deployment blog · production, which still exists.",
            },
          ],
        },
      ],
    })
  })
  await page.goto("/docker/networks")
  await page.getByRole("button", { name: "Prune", exact: true }).click()
  const dialog = page.getByRole("dialog", { name: "Remove unused networks" })
  const removed = dialog.getByRole("list", { name: "Networks removed" })
  const kept = dialog.getByRole("list", { name: "Networks kept" })
  await expect(removed.getByText("spare", { exact: true })).toBeVisible()
  await expect(kept.getByText(/batch-worker still names this network/)).toBeVisible()
  await expect(kept.getByText(/deployment blog · production, which still exists/)).toBeVisible()
  await expect(kept.getByText("Compose project batch declares it.")).toHaveCount(0)
  await dialog.getByRole("button", { name: "Remove 1", exact: true }).click()
  await expect(dialog).not.toBeVisible()
  expect(pruned).toEqual({ ids: ["n-spare"] })
  await expect(page.getByText("Removed 1 network", { exact: true })).toBeVisible()
})

test("the creation form reads the Engine's drivers and names options Docker would ignore", async ({
  page,
}) => {
  await setup(page, [], [])
  let unread = false
  await page.route("**/api/v1/docker/networks/drivers", (route) =>
    unread
      ? json(
          route,
          { error: { code: "docker_unavailable", message: "Docker is unreachable" } },
          503,
        )
      : json(route, {
          swarm: "inactive",
          manager: false,
          pluginsRead: true,
          checkedAt: now,
          limitations: [],
          drivers: [
            {
              name: "bridge",
              source: "builtin",
              scope: "local",
              creatable: true,
              options: [{ key: "com.docker.network.driver.mtu", description: "MTU" }],
            },
            {
              name: "macvlan",
              source: "builtin",
              scope: "local",
              creatable: true,
              options: [{ key: "parent", description: "" }],
            },
            { name: "acme/fabric", source: "plugin", scope: "local", creatable: true, options: [] },
            {
              name: "overlay",
              source: "builtin",
              scope: "swarm",
              creatable: false,
              reason: "Overlay networks span a swarm; this Engine is not a swarm manager.",
              options: [],
            },
          ],
        }),
  )
  await page.goto("/docker/networks")
  await page.getByRole("button", { name: "Create network", exact: true }).click()
  const dialog = page.getByRole("dialog", { name: "Create network" })
  await dialog.getByLabel("Name", { exact: true }).fill("lab")
  await dialog.getByRole("button", { name: "Advanced settings", exact: true }).click()
  const driver = dialog.getByLabel("Network driver", { exact: true })
  const create = dialog.getByRole("button", { name: "Create", exact: true })
  await expect(dialog.getByText(/bridge is built into Docker/)).toBeVisible()

  await driver.fill("calico")
  await expect(
    dialog.getByText(
      "This Engine has no calico network driver. Install and enable its plugin first.",
    ),
  ).toBeVisible()
  await expect(create).toBeDisabled()
  await driver.fill("overlay")
  await expect(
    dialog.getByText(/overlay cannot create a network here: Overlay networks span a swarm/),
  ).toBeVisible()
  await expect(create).toBeDisabled()
  await driver.fill("acme/fabric")
  await expect(dialog.getByText(/an installed plugin's driver/)).toBeVisible()
  await expect(create).toBeEnabled()

  await driver.fill("bridge")
  await dialog
    .getByLabel("Driver options", { exact: true })
    .fill("com.docker.network.bridge.mtu=1400")
  await expect(dialog.getByText(/Docker ignores options bridge does not document/)).toBeVisible()
  await expect(dialog.getByText("com.docker.network.bridge.mtu", { exact: true })).toBeVisible()

  unread = true
  await dialog.getByRole("button", { name: "Hide advanced settings", exact: true }).click()
  await dialog.getByRole("button", { name: "Advanced settings", exact: true }).click()
  await expect(dialog.getByText(/network drivers could not be read/)).toBeVisible()
  await driver.fill("macvlan")
  await expect(create).toBeDisabled()
  await driver.fill("bridge")
  await dialog.getByLabel("Driver options", { exact: true }).fill("")
  await expect(create).toBeEnabled()
})

for (const width of [390, 1440]) {
  test(`network previews fit without page overflow at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    await setup(page)
    await page.route("**/api/v1/docker/networks/n-lab/disconnect?**", (route) =>
      json(
        route,
        preview({
          container: "api-full-id",
          conflicts: [
            {
              code: "peers",
              level: "warn",
              message:
                "worker, scheduler, reporting-service reach api on lab as api, app, the-long-alias-name; those names stop resolving for them.",
            },
          ],
        }),
      ),
    )
    await page.goto("/docker/networks")
    await page.getByRole("button", { name: "lab", exact: true }).click()
    await page.getByRole("button", { name: "Detach api", exact: true }).click()
    const dialog = page.getByRole("dialog", { name: "Detach api" })
    await expect(dialog.getByText(/reporting-service reach api/)).toBeVisible()
    const overflow = await dialog.evaluate((node) => node.scrollWidth > node.clientWidth)
    expect(overflow).toBe(false)
    await page.screenshot({
      path: `test-results/docker-network-detach-${width}.png`,
      animations: "disabled",
    })
  })
}

test("readers see owners and previews' results but are offered no change", async ({ page }) => {
  await mockNetwork(page, [], {
    session: { ...admin, capabilities: ["read"], user: { ...admin.user, role: "readonly" } },
    overrides: {
      "/docker/ping": { available: true },
      "/docker/networks/": networks,
      "/docker/networks/n-lab": labDetail,
    },
  })
  await page.goto("/docker/networks")
  await expect(page.getByText("Compose · shop", { exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: "Prune", exact: true })).toHaveCount(0)
  await page.getByRole("button", { name: "lab", exact: true }).click()
  const panel = page.getByRole("dialog", { name: "lab" })
  await expect(panel.getByText("10.4.0.3/24 · fd00:4::3/64 · also on bridge")).toBeVisible()
  await expect(panel.getByRole("button", { name: "Detach api", exact: true })).toHaveCount(0)
  await expect(panel.getByRole("button", { name: "Attach a container", exact: true })).toHaveCount(
    0,
  )
})
