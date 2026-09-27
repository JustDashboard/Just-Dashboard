import { json, type ProxyRoutes } from "./shared"

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

export const routes: ProxyRoutes = {
  "/proxy/status": (route) => json(route, availability),
  "/systemd/nginx.service": (route) =>
    json(route, {
      unit: {
        name: "nginx.service",
        description: "A high performance web server",
        loadState: "loaded",
        activeState: "active",
        subState: "running",
        unitFileState: "enabled",
        enabled: true,
        activeSince: Math.floor(Date.now() / 1000) - 7200,
      },
      properties: {},
    }),
}

export const showcase: ProxyRoutes = {}
