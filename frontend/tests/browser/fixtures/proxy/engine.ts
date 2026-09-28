import type { Route } from "@playwright/test"
import { json, now, type ProxyRoutes } from "./shared"

/** What the backend answers once it parses `nginx -v` rather than storing it. */
export const availability = {
  nginx: true,
  nginxVersion: "nginx/1.26.3",
  caddy: false,
  caddyVersion: "",
  nginxDir: "/etc/nginx",
  caddyFile: "/etc/caddy/Caddyfile",
  certbot: true,
}

/** `nginx -t` passing, as the config test and a reload's test answer it. */
export const passingTest = {
  valid: true,
  output:
    "nginx: the configuration file /etc/nginx/nginx.conf syntax is ok\nnginx: configuration file /etc/nginx/nginx.conf test is successful",
  command: "nginx -t",
  diagnostics: [],
  warnings: 0,
}

/** The engine's service, answered as the proxy's own route answers it. */
const engineAction = (action: string) => (route: Route) =>
  json(route, { action, unit: "nginx.service", output: "" })

/** nginx.service running for two hours and enabled at boot, as `systemctl show` reads it. */
export const nginxUnit = {
  name: "nginx.service",
  description: "A high performance web server",
  loadState: "loaded",
  activeState: "active",
  subState: "running",
  unitFileState: "enabled",
  enabled: true,
  activeSince: Math.floor(Date.now() / 1000) - 7200,
}

/**
 * The last lines of a failed nginx.service's journal: a start that could not
 * bind port 80 because something else holds it.
 */
export const failedJournal = [
  {
    timestamp: "2026-09-27T21:04:11.120Z",
    message: "Starting nginx.service - A high performance web server and a reverse proxy server...",
    priority: 6,
    unit: "nginx.service",
  },
  {
    timestamp: "2026-09-27T21:04:11.180Z",
    message: "nginx: [emerg] bind() to 0.0.0.0:80 failed (98: Address already in use)",
    priority: 3,
    unit: "nginx.service",
  },
  {
    timestamp: "2026-09-27T21:04:13.690Z",
    message: "nginx.service: Failed with result 'exit-code'.",
    priority: 4,
    unit: "nginx.service",
  },
]

const RELOAD_CYCLE = [
  "Reloading nginx.service - A high performance web server and a reverse proxy server...",
  "nginx.service: Sent signal SIGHUP to main process 1187 (nginx) on client request.",
  "Reloaded nginx.service - A high performance web server and a reverse proxy server.",
]

/**
 * All thirty lines the fold reads, oldest first as journalctl gives them: a
 * week of reloads, then the start that could not bind, whose reason is the
 * third line from the bottom.
 */
export const longJournal = [
  ...Array.from({ length: 27 }, (_, index) => ({
    timestamp: new Date(Date.parse("2026-09-20T08:00:00Z") + index * 6 * 3_600_000).toISOString(),
    message: RELOAD_CYCLE[index % RELOAD_CYCLE.length],
    priority: 6,
    unit: "nginx.service",
  })),
  ...failedJournal,
]

/** The log search route's answer for the unit journal. */
export const journalSearch = (entries: typeof failedJournal) => ({
  lines: entries.map((entry) => ({
    timestamp: entry.timestamp,
    text: entry.message,
    level: entry.priority <= 3 ? "error" : "info",
  })),
})

const entry = (path: string, extra: Record<string, unknown> = {}) => ({
  path: `/etc/nginx/${path}`,
  kind: "file",
  size: 1200,
  modified: now,
  included: false,
  managed: false,
  protected: false,
  ...extra,
})

/**
 * A Debian nginx directory as GET /proxy/files lists it: two enabled sites
 * through their links, one disabled, a snippet a site includes and one
 * nothing does, a stream nginx.conf does not include, a module outside the
 * directory, and the dashboard's own password file.
 */
export const configFiles = {
  root: "/etc/nginx",
  main: "/etc/nginx/nginx.conf",
  includesKnown: true,
  truncated: false,
  files: [
    entry("conf.d/gzip.conf", {
      included: true,
      includedBy: { file: "/etc/nginx/nginx.conf", line: 60 },
    }),
    entry("jd-auth/shop", { kind: "password", protected: true, managed: true, size: 64 }),
    entry("mime.types", {
      included: true,
      includedBy: { file: "/etc/nginx/nginx.conf", line: 20 },
      size: 5527,
    }),
    entry("modules-enabled/50-mod-http-geoip2.conf", {
      kind: "link",
      included: true,
      includedBy: { file: "/etc/nginx/nginx.conf", line: 4 },
      target: "/usr/share/nginx/modules-available/mod-http-geoip2.conf",
      outside: true,
    }),
    entry("nginx.conf", { kind: "main", included: true, size: 1547 }),
    entry("sites-available/app.example.com", {
      included: true,
      managed: true,
      includedBy: {
        file: "/etc/nginx/nginx.conf",
        line: 61,
        via: "/etc/nginx/sites-enabled/app.example.com",
      },
    }),
    entry("sites-available/legacy.example.com", {
      included: true,
      includedBy: {
        file: "/etc/nginx/nginx.conf",
        line: 61,
        via: "/etc/nginx/sites-enabled/legacy.example.com",
      },
      size: 400,
    }),
    entry("sites-available/old-site", { size: 380 }),
    entry("sites-enabled/app.example.com", {
      kind: "link",
      included: true,
      includedBy: { file: "/etc/nginx/nginx.conf", line: 61 },
      target: "/etc/nginx/sites-available/app.example.com",
    }),
    entry("sites-enabled/legacy.example.com", {
      kind: "link",
      included: true,
      includedBy: { file: "/etc/nginx/nginx.conf", line: 61 },
      target: "/etc/nginx/sites-available/legacy.example.com",
    }),
    entry("snippets/ssl-params.conf", {
      included: true,
      includedBy: { file: "/etc/nginx/sites-available/app.example.com", line: 12 },
    }),
    entry("snippets/unused.conf", { size: 90 }),
    entry("stream.d/postgres.conf", { managed: true, size: 240 }),
  ],
}

const appSite = [
  "# Managed by Just Dashboard.",
  "server {",
  "    listen 443 ssl;",
  "    server_name app.example.com;",
  "    location / {",
  "        proxy_pass http://127.0.0.1:3000;",
  "    }",
  "}",
  "",
].join("\n")

/** What GET /proxy/effective answers for that directory: nginx -T, file by file, placed. */
export const effectiveConfig = {
  checkedAt: now,
  files: [
    {
      path: "/etc/nginx/nginx.conf",
      content:
        "events {}\nhttp {\n    include /etc/nginx/conf.d/*.conf;\n    include /etc/nginx/sites-enabled/*;\n}\n",
    },
    { path: "/etc/nginx/conf.d/gzip.conf", content: "gzip on;\ngzip_types text/css;\n" },
    {
      path: "/etc/nginx/sites-enabled/app.example.com",
      target: "/etc/nginx/sites-available/app.example.com",
      content: appSite,
    },
    {
      path: "/etc/nginx/sites-enabled/legacy.example.com",
      target: "/etc/nginx/sites-available/legacy.example.com",
      content:
        "server {\n    listen 80;\n    server_name legacy.example.com;\n    location / {\n        proxy_pass http://127.0.0.1:8080;\n    }\n}\n",
    },
  ],
  directives: [
    { name: "events", args: [], file: "/etc/nginx/nginx.conf", line: 1, within: [], opens: true },
    { name: "http", args: [], file: "/etc/nginx/nginx.conf", line: 2, within: [], opens: true },
    {
      name: "gzip",
      args: ["on"],
      file: "/etc/nginx/conf.d/gzip.conf",
      line: 1,
      within: ["http"],
      opens: false,
    },
    {
      name: "gzip_types",
      args: ["text/css"],
      file: "/etc/nginx/conf.d/gzip.conf",
      line: 2,
      within: ["http"],
      opens: false,
    },
    ...site("/etc/nginx/sites-enabled/app.example.com", "app.example.com", "443", "3000", 1),
    ...site("/etc/nginx/sites-enabled/legacy.example.com", "legacy.example.com", "80", "8080", 0),
  ],
}

/** A site's directives as the tree places them, from its server line. */
function site(file: string, name: string, port: string, upstream: string, offset: number) {
  const server = `server ${name}`
  return [
    { name: "server", args: [], file, line: 1 + offset, within: ["http"], opens: true },
    {
      name: "listen",
      args: port === "443" ? ["443", "ssl"] : [port],
      file,
      line: 2 + offset,
      within: ["http", server],
      opens: false,
    },
    {
      name: "server_name",
      args: [name],
      file,
      line: 3 + offset,
      within: ["http", server],
      opens: false,
    },
    {
      name: "location",
      args: ["/"],
      file,
      line: 4 + offset,
      within: ["http", server],
      opens: true,
    },
    {
      name: "proxy_pass",
      args: [`http://127.0.0.1:${upstream}`],
      file,
      line: 5 + offset,
      within: ["http", server, "location /"],
      opens: false,
    },
  ]
}

export const routes: ProxyRoutes = {
  "/proxy/status": (route) => json(route, availability),
  "/systemd/nginx.service": (route) => json(route, { unit: nginxUnit, properties: {} }),
  "/logs/search": (route) => json(route, journalSearch(failedJournal)),
  "/proxy/test": (route) => json(route, passingTest),
  // No test since the dashboard started.
  "/proxy/test/last": (route) => route.fulfill({ status: 204 }),
  "/proxy/reload": (route) => json(route, { validation: passingTest, reloaded: true, output: "" }),
  "/proxy/engine/start": engineAction("start"),
  "/proxy/engine/restart": engineAction("restart"),
  "/proxy/engine/stop": engineAction("stop"),
  "/proxy/engine/enable": engineAction("enable"),
  "/proxy/engine/reset-failed": engineAction("reset-failed"),
  "/proxy/files": (route) => json(route, configFiles),
  "/proxy/effective": (route) => json(route, effectiveConfig),
}

export const showcase: ProxyRoutes = {}
