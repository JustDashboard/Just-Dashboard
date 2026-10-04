import type { Page } from "@playwright/test"
import { mockProxy, json, user, vhosts } from "./proxy-fixtures"

export { user, json }
export const CONTAINER_ID = "cafebabe".padEnd(64, "1")
export const searchContainer = {
  id: CONTAINER_ID,
  names: ["shop-web"],
  name: "shop-web",
  image: "nginx:alpine",
  imageId: "sha256:abc",
  command: "nginx",
  state: "running",
  status: "Up 2 hours",
  health: "healthy",
  createdAt: "2026-10-04T10:00:00Z",
  uptimeSeconds: 7200,
  ports: [],
  labels: {},
  networks: ["bridge"],
  exposure: [],
  hasHealthcheck: true,
  restartPolicy: "unless-stopped",
  privileged: false,
  inspected: true,
  mounts: [],
  env: [],
  networkDetails: [],
  restartCount: 0,
  exitCode: 0,
}

export const searchSites = vhosts.map((site) =>
  site.kind === "caddy" ? { ...site, name: "shop-ingress" } : site,
)

/** A realistic shop host; the recording explicitly labels these API fixtures. */
export async function mockCommandSearch(page: Page, auth = user) {
  await mockProxy(page, { included: true })
  const mutations: string[] = []
  const reads: string[] = []
  const inventories: Record<string, unknown> = {
    "/auth/session": auth,
    "/account/profile": auth.user,
    "/deploy/": {
      deployments: [
        {
          id: 7,
          name: "shop",
          endpoint: "https://shop.example.com",
          environmentName: "Production",
          environmentKind: "production",
          health: "healthy",
        },
      ],
      activeWork: [],
      slots: { heavyUsed: 0, heavyCapacity: 2, lightUsed: 0, lightCapacity: 4 },
    },
    "/proxy/vhosts": searchSites,
    "/docker/ping": { available: true, serverVersion: "29.0.0" },
    "/docker/containers/": [searchContainer],
    [`/docker/containers/${CONTAINER_ID}`]: searchContainer,
    [`/docker/containers/${CONTAINER_ID}/health`]: {
      status: "healthy",
      checkedAt: new Date().toISOString(),
      findings: [],
      checks: [],
    },
    "/docker/stacks/": [
      {
        name: "shop-stack",
        summary: "Running · 2/2 services",
        workingDir: "/srv/shop",
        declared: ["web", "db"],
      },
    ],
    "/git/": {
      available: true,
      repos: [
        {
          name: "shop",
          path: "/srv/shop",
          branch: "main",
          remote: "https://github.com/example/shop.git",
          upstream: "origin/main",
          head: "a".repeat(40),
          subject: "Serve the shop",
          author: "Operator",
          commitAt: "2026-10-04T10:00:00Z",
          dirty: false,
          changes: 0,
          staged: 0,
          untracked: 0,
          conflicts: 0,
          ahead: 0,
          behind: 0,
          detached: false,
        },
      ],
    },
    "/systemd/": {
      available: true,
      units: [{ name: "caddy.service", description: "Caddy web server", activeState: "active" }],
    },
    "/pm2/": {
      available: true,
      processes: [
        {
          id: 0,
          daemonId: "deploy",
          user: "deploy",
          name: "shop-worker",
          namespace: "shop",
          status: "online",
        },
        {
          id: 0,
          daemonId: "ubuntu",
          user: "ubuntu",
          name: "shop-worker",
          namespace: "shop",
          status: "stopped",
        },
      ],
    },
    "/backups/": [
      {
        id: 4,
        name: "shop-nightly",
        targetKind: "local",
        schedule: "0 2 * * *",
        sources: ["/srv/shop"],
      },
    ],
    "/boards/": [{ id: 2, name: "Shop architecture" }],
    "/databases/": [
      {
        id: 1,
        name: "shop-main",
        driver: "postgres",
        database: "shop",
        host: "shop-db",
        environment: "production",
      },
    ],
  }
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname.replace(/^\/api\/v1/, "")
    if (route.request().method() !== "GET") mutations.push(path)
    else reads.push(path)
    if (Object.hasOwn(inventories, path)) return json(route, inventories[path])
    return route.fallback()
  })
  return { mutations, reads }
}
