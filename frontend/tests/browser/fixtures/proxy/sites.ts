import { json, now, type ProxyRoutes } from "./shared"

const anHourAgo = new Date(Date.now() - 3_600_000).toISOString()
const tenMinutesAgo = new Date(Date.now() - 600_000).toISOString()

/** GET /proxy/pending: nginx running what it loaded an hour ago, with nothing waiting. */
export const pendingNone = {
  running: true,
  lastReload: anHourAgo,
  generation: "1088-500000",
  files: [],
}

export const vhosts = [
  {
    name: "app.example.com",
    kind: "nginx",
    path: "/etc/nginx/sites-available/app.example.com",
    enabledPath: "/etc/nginx/sites-enabled/app.example.com",
    enabled: true,
    layout: "sites-available",
    formEditable: true,
    serverNames: ["app.example.com"],
    listen: ["443 ssl"],
    upstreams: ["http://127.0.0.1:3000"],
    tls: true,
    certPath: "/etc/letsencrypt/live/app.example.com/fullchain.pem",
    modified: now,
    size: 1200,
  },
  {
    name: "legacy.example.com",
    kind: "nginx",
    path: "/etc/nginx/sites-available/legacy.example.com",
    enabledPath: "/etc/nginx/sites-enabled/legacy.example.com",
    enabled: true,
    layout: "sites-available",
    formEditable: true,
    serverNames: ["legacy.example.com"],
    listen: ["80"],
    upstreams: ["http://127.0.0.1:8080"],
    tls: false,
    modified: now,
    size: 400,
  },
  {
    // A shared Docker Caddy route: no file on the host to edit.
    name: "just-dashboard-shop",
    kind: "caddy",
    path: "",
    enabled: true,
    formEditable: false,
    serverNames: ["shop.example.com"],
    listen: ["80", "443"],
    upstreams: ["http://172.18.0.4:3000"],
    tls: true,
    modified: now,
    size: 0,
  },
]

export const routes: ProxyRoutes = {
  "/proxy/vhosts": (route) => json(route, vhosts),
  "/proxy/pending": (route) => json(route, pendingNone),
  "/proxy/auth-files/": (route) => json(route, []),
}

export const showcase: ProxyRoutes = {
  // Files no card owns, so the strip is drawn at every width without
  // changing what any card says.
  "/proxy/pending": (route) =>
    json(route, {
      ...pendingNone,
      files: [
        { path: "/etc/nginx/nginx.conf", change: "changed", modified: tenMinutesAgo },
        {
          path: "/etc/nginx/snippets/ssl-params-for-every-site-on-this-host.conf",
          change: "changed",
          modified: tenMinutesAgo,
        },
      ],
    }),
  "/proxy/auth-files/": (route) =>
    json(route, [
      { name: "staging", path: "/etc/nginx/auth/staging", users: ["operator", "reviewer"] },
    ]),
}
