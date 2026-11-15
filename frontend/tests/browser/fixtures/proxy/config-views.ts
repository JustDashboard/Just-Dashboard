import type { Page } from "@playwright/test"
import { json, now } from "./shared"

/**
 * The Configuration page's administrator views that the shared engine table
 * does not answer: nginx's server-wide settings, the linter, and the history
 * of the files the dashboard changed — with one site's revisions. Kept apart
 * from the shared tables so the page's own spec is the only one that reads
 * them; `mockConfigViews` registers them over `mockProxy`.
 */

const unix = Math.floor(Date.parse(now) / 1000)

export const settingsReport = {
  settings: [
    {
      name: "server_tokens",
      context: "http",
      value: "on",
      set: false,
      overrides: 0,
      default: "on",
      choices: ["on", "off", "build"],
    },
    {
      name: "gzip",
      context: "http",
      value: "on",
      set: true,
      file: "/etc/nginx/conf.d/gzip.conf",
      line: 1,
      overrides: 0,
      default: "off",
      choices: ["on", "off"],
    },
    {
      name: "client_max_body_size",
      context: "http",
      value: "1m",
      set: false,
      overrides: 2,
      default: "1m",
    },
    {
      name: "keepalive_timeout",
      context: "http",
      value: "65",
      set: true,
      file: "/etc/nginx/nginx.conf",
      line: 21,
      overrides: 0,
      default: "75s",
    },
    {
      name: "ssl_protocols",
      context: "http",
      value: "TLSv1.2 TLSv1.3",
      set: true,
      file: "/etc/nginx/nginx.conf",
      line: 34,
      overrides: 0,
      default: "TLSv1.2 TLSv1.3",
    },
    {
      name: "server_names_hash_bucket_size",
      context: "http",
      value: "64",
      set: false,
      overrides: 0,
      default: "32, 64 or 128 (the CPU's cache line)",
    },
    {
      name: "worker_connections",
      context: "events",
      value: "768",
      set: true,
      file: "/etc/nginx/nginx.conf",
      line: 7,
      overrides: 0,
      default: "512",
    },
  ],
  openFiles: 1024,
  openFilesFrom: "the master's soft limit",
  target: "/etc/nginx/conf.d/jd-http.conf",
}

export const lintReport = {
  checkedAt: now,
  findings: [
    {
      id: "add-header-dropped:/etc/nginx/sites-available/app.example.com:6",
      rule: "add-header-dropped",
      level: "warning",
      title: "A location drops the server's headers",
      detail:
        "add_header in location / replaces every add_header the server block set, including HSTS.",
      file: "/etc/nginx/sites-available/app.example.com",
      line: 6,
    },
  ],
}

const revision = (id: number, action: string, ago: number, existed = true) => ({
  id,
  path: "/etc/nginx/sites-available/app.example.com",
  sha256: `sha-${id}`,
  size: 220 + id,
  existed,
  action,
  actor: "operator",
  createdAt: unix - ago,
})

export const historyFiles = {
  files: [
    {
      path: "/etc/nginx/sites-available/app.example.com",
      revisions: 3,
      latest: revision(3, "write", 120),
      current: { exists: true, sha256: "sha-3", drift: false },
    },
    {
      path: "/etc/nginx/sites-available/gone.example.com",
      revisions: 2,
      latest: {
        ...revision(5, "delete", 600, false),
        path: "/etc/nginx/sites-available/gone.example.com",
      },
      current: { exists: false, drift: false },
    },
  ],
}

export const appHistory = {
  path: "/etc/nginx/sites-available/app.example.com",
  revisions: [revision(3, "write", 120), revision(2, "write", 3600), revision(1, "baseline", 7200)],
  current: { exists: true, sha256: "sha-3", drift: false },
}

const site = (upstream: string) =>
  `server {\n    listen 443 ssl;\n    server_name app.example.com;\n    location / {\n        proxy_pass http://127.0.0.1:${upstream};\n    }\n}\n`

export function revisionDetail(id: number) {
  const contents: Record<number, string> = { 1: site("3000"), 2: site("3001"), 3: site("3002") }
  const rev = appHistory.revisions.find((r) => r.id === id) ?? appHistory.revisions[0]
  const previous = appHistory.revisions.find((r) => r.id === id - 1)
  return {
    revision: rev,
    content: contents[rev.id],
    previous: previous ? { revision: previous, content: contents[previous.id] } : null,
    current: appHistory.current,
    currentContent: contents[3],
  }
}

/** Registers the views' reads (and what the page writes, recorded) over `mockProxy`. */
export async function mockConfigViews(page: Page) {
  const writes: { path: string; body: unknown }[] = []
  await page.route("**/api/v1/proxy/settings", async (route) => {
    if (route.request().method() === "PUT") {
      writes.push({ path: "/proxy/settings", body: route.request().postDataJSON() })
      return json(route, {
        edits: [],
        validation: { valid: true, output: "", command: "nginx -t" },
      })
    }
    return json(route, settingsReport)
  })
  await page.route("**/api/v1/proxy/settings/preview", async (route) => {
    const body = route.request().postDataJSON() as { changes: { name: string; value: string }[] }
    const change = body.changes[0]
    const setting = settingsReport.settings.find((s) => s.name === change.name)
    return json(route, {
      edits: [
        {
          name: change.name,
          value: change.value,
          file: setting?.file ?? settingsReport.target,
          line: setting?.line ?? 1,
          before: setting?.set ? `    ${change.name} ${setting.value};` : "",
          after: `    ${change.name} ${change.value};`,
          created: !setting?.file,
          conffile: setting?.file === "/etc/nginx/nginx.conf",
        },
      ],
    })
  })
  await page.route("**/api/v1/proxy/lint", (route) => json(route, lintReport))
  await page.route("**/api/v1/proxy/history/files", (route) => json(route, historyFiles))
  await page.route("**/api/v1/proxy/history?**", (route) => json(route, appHistory))
  await page.route(/\/api\/v1\/proxy\/history\/\d+$/, (route) => {
    const id = Number(new URL(route.request().url()).pathname.split("/").pop())
    return json(route, revisionDetail(id))
  })
  await page.route(/\/api\/v1\/proxy\/history\/\d+\/restore$/, (route) => {
    writes.push({
      path: new URL(route.request().url()).pathname.replace(/^\/api\/v1/, ""),
      body: route.request().postDataJSON(),
    })
    return json(route, { validation: { valid: true, output: "", command: "nginx -t" } })
  })
  await page.route("**/api/v1/proxy/config?**", (route) =>
    json(route, { content: site("3002"), revision: "rev-3" }),
  )
  return writes
}
