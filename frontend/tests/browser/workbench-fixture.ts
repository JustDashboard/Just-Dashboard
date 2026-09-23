import type { Page, Route } from "@playwright/test"

/**
 * A server with three backup jobs (one failing, one paused), a coverage report
 * across every kind of thing, the containers the volumes and stacks are drawn
 * from, and four terminal sessions each running something different beside a
 * repository with staged, unstaged, untracked and deleted work. Shared by the
 * Backups and terminal Diff specs.
 */

const now = Date.now()
const iso = (ms: number) => new Date(ms).toISOString()
const H = 3_600_000
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
    username: "wayy",
    role: "admin",
    totpEnabled: true,
    disabled: false,
    mustChangePassword: false,
    lastLoginAt: iso(now),
    createdAt: iso(now),
  },
}
const run = (id: number, jobId: number, ago: number, status: string, size: number) => ({
  id,
  jobId,
  startedAt: iso(now - ago),
  endedAt: iso(now - ago + 48_000),
  status,
  artifact: `/var/backups/just-dashboard/${jobId}-${id}.tar.zst`,
  sizeBytes: size,
  log:
    status === "failed"
      ? "dumping postgres\nFAILED: pg_dump: connection refused"
      : "archived 1,204 files",
  trigger: "schedule",
  duration: "48s",
})
const jobs = [
  {
    id: 1,
    name: "Nginx configuration",
    sources: ["/etc/nginx"],
    excludes: [],
    targetKind: "local",
    target: { path: "/var/backups/just-dashboard" },
    schedule: "0 3 * * *",
    retention: 7,
    retentionDays: 0,
    enabled: true,
    createdAt: iso(now - 30 * 24 * H),
    hasCredentials: false,
    lastRun: run(70, 1, 3.9 * H, "success", 7_200),
    nextRun: iso(now + 20 * H),
    lastSuccessAt: iso(now - 3.9 * H),
    overdue: false,
    stored: { runs: 7, bytes: 51_500 },
  },
  {
    id: 2,
    name: "Databases nightly",
    sources: [],
    excludes: [],
    databaseDumps: [1, 2],
    targetKind: "b2",
    target: { bucket: "wayy-backups", prefix: "db/" },
    schedule: "30 2 * * *",
    retention: 14,
    retentionDays: 30,
    enabled: true,
    createdAt: iso(now - 20 * 24 * H),
    hasCredentials: true,
    lastRun: run(71, 2, 5 * H, "failed", 0),
    nextRun: iso(now + 19 * H),
    lastSuccessAt: iso(now - 29 * H),
    overdue: false,
    stored: { runs: 12, bytes: 1_850_000_000 },
  },
  {
    id: 3,
    name: "n8n data",
    sources: ["/var/lib/docker/volumes/n8n_data/_data"],
    excludes: [],
    pauseContainers: ["n8n"],
    targetKind: "s3",
    target: { bucket: "offsite", region: "eu-central-1" },
    schedule: "0 */6 * * *",
    retention: 10,
    retentionDays: 0,
    enabled: false,
    createdAt: iso(now - 10 * 24 * H),
    hasCredentials: true,
    lastRun: run(60, 3, 50 * H, "success", 220_000_000),
    nextRun: undefined,
    lastSuccessAt: iso(now - 50 * H),
    overdue: false,
    stored: { runs: 10, bytes: 2_100_000_000 },
  },
]
const runsFor = (id: number) =>
  Array.from({ length: 14 }, (_, i) =>
    run(
      100 + i,
      id,
      (i + 1) * 24 * H,
      id === 2 && (i === 0 || i === 4) ? "failed" : "success",
      1000 * (i + 1),
    ),
  )
const res = (
  kind: string,
  id: string,
  name: string,
  detail: string,
  extra: Record<string, unknown> = {},
) => ({
  kind,
  id,
  name,
  detail,
  suggest: { name, sources: [] },
  coveredBy: [],
  protected: false,
  ...extra,
})
const resources = {
  resources: [
    res(
      "dashboard",
      "dashboard",
      "Just Dashboard",
      "Settings, accounts, saved connections and deployment records",
      { paths: ["/var/lib/just-dashboard"] },
    ),
    res("proxy", "nginx", "Nginx configuration", "/etc/nginx", {
      paths: ["/etc/nginx"],
      protected: true,
      coveredBy: [{ jobId: 1, jobName: "Nginx configuration", enabled: true }],
      lastBackupAt: iso(now - 3.9 * H),
    }),
    res("proxy", "caddy", "Caddy configuration", "/etc/caddy", { paths: ["/etc/caddy"] }),
    res("database", "LOL", "LOL", "postgres · LOL", { connectionId: 1 }),
    res("database", "Main", "Main", "postgres · app", { connectionId: 2 }),
    res("database", "cache", "cache", "redis · 127.0.0.1", { connectionId: 3 }),
    res("volume", "n8n_data", "n8n_data", "Mounted by n8n", {
      paths: ["/var/lib/docker/volumes/n8n_data/_data"],
      coveredBy: [{ jobId: 3, jobName: "n8n data", enabled: false }],
    }),
    res("volume", "LOL-data", "LOL-data", "Mounted by LOL", {
      paths: ["/var/lib/docker/volumes/LOL-data/_data"],
    }),
    res("volume", "trilium-data", "trilium-data", "Mounted by t-trilium", {
      paths: ["/var/lib/docker/volumes/trilium-data/_data"],
    }),
    res("stack", "epgjauto", "epgjauto", "4 services", { paths: ["/home/ubuntu/epgjauto"] }),
    res("stack", "just-dashboard", "just-dashboard", "3 services", {
      paths: ["/home/ubuntu/Just-Dashboard"],
    }),
    res("deployment", "shop", "shop", "/srv/deploy/shop", { paths: ["/srv/deploy/shop"] }),
    res(
      "repository",
      "/home/ubuntu/Just-Dashboard",
      "Just-Dashboard",
      "/home/ubuntu/Just-Dashboard",
      { paths: ["/home/ubuntu/Just-Dashboard"] },
    ),
  ],
  unavailable: {},
}
const containers = [
  ["n8n", "n8nio/n8n:1.110.1", ""],
  ["LOL", "postgres:16", ""],
  ["t-trilium", "ghcr.io/triliumnext/trilium:v0.105.0", ""],
  ["epgjauto-proxy-1", "caddy:2-alpine", "epgjauto"],
  ["epgjauto-db-1", "postgres:16-alpine", "epgjauto"],
  ["epgjauto-app-1", "epgjauto-app", "epgjauto"],
  ["just-dashboard-proxy-1", "caddy:2-alpine", "just-dashboard"],
  ["just-dashboard-backend-1", "just-dashboard-backend:latest", "just-dashboard"],
].map(([name, image, stack], i) => ({
  id: `c${i}`,
  names: [name],
  name,
  image,
  imageId: "",
  command: "",
  state: "running",
  status: "Up",
  createdAt: iso(now),
  uptimeSeconds: 1,
  ports: [],
  labels: {},
  networks: [],
  composeStack: stack || undefined,
  exposure: [],
}))
const sessions = [
  {
    id: "s1",
    title: "Just-Dashboard",
    live: true,
    windows: 3,
    createdAt: iso(now - H),
    attached: 1,
    favourite: true,
    cwd: "/home/ubuntu/Just-Dashboard",
    busy: true,
    working: true,
    current: { id: "w1", name: "claude", busy: true, process: "claude", working: true },
  },
  {
    id: "s2",
    title: "api server",
    live: true,
    windows: 1,
    createdAt: iso(now - 2 * H),
    attached: 0,
    favourite: false,
    cwd: "/srv/deploy/shop",
    busy: true,
    working: false,
    current: { id: "w4", name: "node", busy: true, process: "node" },
  },
  {
    id: "s3",
    title: "db",
    live: true,
    windows: 1,
    createdAt: iso(now - 3 * H),
    attached: 0,
    favourite: false,
    busy: true,
    current: { id: "w5", name: "psql", busy: true, process: "psql" },
  },
  {
    id: "s4",
    title: "ubuntu",
    live: true,
    windows: 1,
    createdAt: iso(now - 4 * H),
    attached: 0,
    favourite: false,
    finishedAt: now - 60_000,
    current: { id: "w6", name: "bash" },
  },
]
const windows: Record<string, object[]> = {
  s1: [
    {
      id: "w1",
      name: "claude",
      index: 0,
      busy: true,
      process: "claude",
      working: true,
      cwd: "/home/ubuntu/Just-Dashboard",
    },
    { id: "w2", name: "vim", index: 1, busy: true, process: "nvim" },
    { id: "w3", name: "bash", index: 2 },
  ],
  s2: [{ id: "w4", name: "node", index: 0, busy: true, process: "node" }],
  s3: [{ id: "w5", name: "psql", index: 0, busy: true, process: "psql" }],
  s4: [{ id: "w6", name: "bash", index: 0 }],
}
const status = {
  repo: {
    name: "Just-Dashboard",
    path: "/home/ubuntu/Just-Dashboard",
    branch: "patch/0.7.0",
    ahead: 2,
    behind: 0,
    dirty: true,
    detached: false,
  },
  clean: false,
  stashes: 0,
  identity: { name: "Wayy" },
  files: [
    {
      path: "frontend/src/app/(dashboard)/backups/page.tsx",
      index: " ",
      worktree: "M",
      label: "modified",
      staged: false,
      unstaged: true,
    },
    {
      path: "frontend/src/components/backups/job-card.tsx",
      index: "?",
      worktree: "?",
      label: "untracked",
      staged: false,
      unstaged: true,
    },
    {
      path: "frontend/src/components/product-logo.tsx",
      index: "M",
      worktree: " ",
      label: "modified",
      staged: true,
      unstaged: false,
    },
    {
      path: "docs/old-notes.md",
      index: " ",
      worktree: "D",
      label: "deleted",
      staged: false,
      unstaged: true,
    },
  ],
}
const diff = `diff --git a/frontend/src/app/(dashboard)/backups/page.tsx b/frontend/src/app/(dashboard)/backups/page.tsx
index 1111111..2222222 100644
--- a/frontend/src/app/(dashboard)/backups/page.tsx
+++ b/frontend/src/app/(dashboard)/backups/page.tsx
@@ -144,20 +144,6 @@ export default function BackupsPage() {
       />
 
-      <StatGrid columns={4}>
-        <StatTile
-          label="Jobs"
-          value={jobs.data ? String(list.length) : "—"}
-        />
-      </StatGrid>
+      <JobCards jobs={list} />
 
       {(findings.length > 0 || list.length > 0) && (
diff --git a/docs/old-notes.md b/docs/old-notes.md
deleted file mode 100644
index 3333333..0000000
--- a/docs/old-notes.md
+++ /dev/null
@@ -1,3 +0,0 @@
-# Old notes
-
-Nothing here any more.
`
const staged = `diff --git a/frontend/src/components/product-logo.tsx b/frontend/src/components/product-logo.tsx
index 4444444..5555555 100644
--- a/frontend/src/components/product-logo.tsx
+++ b/frontend/src/components/product-logo.tsx
@@ -52,6 +52,8 @@ const LOGOS: Record<string, string> = {
   freshrss: "freshrss.svg",
+  git: "git.svg",
   gitea: "gitea.svg",
+  go: "go.svg",
   gotify: "gotify.svg",
`
const untracked = `diff --git a/frontend/src/components/backups/job-card.tsx b/frontend/src/components/backups/job-card.tsx
new file mode 100644
--- /dev/null
+++ b/frontend/src/components/backups/job-card.tsx
@@ -0,0 +1,4 @@
+export function JobCard() {
+  return null
+}
+
`
async function json(route: Route, body: unknown) {
  await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) })
}
export async function mockWorkbench(page: Page, tab = "git") {
  await page.addInitScript((tab) => {
    localStorage.setItem(
      "jd.view.state",
      JSON.stringify({ "terminal.rail": true, "terminal.tools": true, "terminal.tools.tab": tab }),
    )
  }, tab)
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    if (path === "/auth/session") return json(route, user)
    if (path === "/backups/") return json(route, jobs)
    if (path === "/backups/resources") return json(route, resources)
    const m = path.match(/^\/backups\/(\d+)\/runs$/)
    if (m) return json(route, { runs: runsFor(Number(m[1])), running: false })
    if (path === "/docker/containers/") return json(route, containers)
    if (path === "/terminal/")
      return json(route, {
        enabled: true,
        login: { user: "ubuntu", home: "/home/ubuntu", shell: "/bin/bash" },
        folders: [],
        sessions,
      })
    const w = path.match(/^\/terminal\/([^/]+)\/windows$/)
    if (w) return json(route, windows[w[1]] ?? [])
    if (path === "/git/detect")
      return json(route, {
        available: true,
        inRoots: true,
        repo: { path: "/home/ubuntu/Just-Dashboard", name: "Just-Dashboard" },
      })
    if (path === "/git/status") return json(route, status)
    if (path === "/git/diff") {
      const file = url.searchParams.get("file")
      if (file?.endsWith("job-card.tsx")) return json(route, { diff: untracked })
      return json(route, {
        diff: url.searchParams.get("staged")
          ? staged
          : file
            ? (diff
                .split("diff --git")
                .filter(Boolean)
                .map((d) => "diff --git" + d)
                .find((d) => d.includes(file)) ?? "")
            : diff,
      })
    }
    if (path === "/files/list" || path === "/files/")
      return json(route, { path: "/home/ubuntu/Just-Dashboard", entries: [] })
    if (path === "/dashboard/update")
      return json(route, {
        version: "0.7.0",
        available: false,
        releases: [],
        history: [],
        breaking: false,
        check: { enabled: true, repo: "a/b", ref: "main" },
        install: { supported: true },
      })
    return json(route, [])
  })
  await page.routeWebSocket("**/api/v1/**", (socket) => {
    if (!/\/terminal\/[^/]+\/attach$/.test(new URL(socket.url()).pathname)) return
    socket.send(
      Buffer.from(
        "\x1b[1;32mubuntu@vps\x1b[0m:\x1b[1;34m~/Just-Dashboard\x1b[0m$ claude\r\n\r\n\x1b[38;5;173m✻\x1b[0m Welcome to Claude Code\r\n\r\n> redesign the backups page\r\n",
      ),
    )
  })
}
