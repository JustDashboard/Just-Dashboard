import type { Route } from "@playwright/test"
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
  "/proxy/test": (route) => json(route, passingTest),
  "/proxy/reload": (route) => json(route, { validation: passingTest, reloaded: true, output: "" }),
  "/proxy/engine/start": engineAction("start"),
  "/proxy/engine/restart": engineAction("restart"),
  "/proxy/engine/stop": engineAction("stop"),
}

export const showcase: ProxyRoutes = {}
