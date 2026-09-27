import { json, now, type ProxyRoutes } from "./shared"

export const vhosts = [
  {
    name: "app.example.com",
    kind: "nginx",
    path: "/etc/nginx/sites-available/app.example.com",
    enabledPath: "/etc/nginx/sites-enabled/app.example.com",
    enabled: true,
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
  "/proxy/auth-files/": (route) => json(route, []),
}

export const showcase: ProxyRoutes = {
  "/proxy/auth-files/": (route) =>
    json(route, [
      { name: "staging", path: "/etc/nginx/auth/staging", users: ["operator", "reviewer"] },
    ]),
}
