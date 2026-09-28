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

export const routes: ProxyRoutes = {
  "/proxy/status": (route) => json(route, availability),
  "/systemd/nginx.service": (route) => json(route, { unit: nginxUnit, properties: {} }),
  "/systemd/nginx.service/journal": (route) => json(route, failedJournal),
  "/proxy/test": (route) => json(route, passingTest),
  "/proxy/reload": (route) => json(route, { validation: passingTest, reloaded: true, output: "" }),
  "/proxy/engine/start": engineAction("start"),
  "/proxy/engine/restart": engineAction("restart"),
  "/proxy/engine/stop": engineAction("stop"),
  "/proxy/engine/enable": engineAction("enable"),
  "/proxy/engine/reset-failed": engineAction("reset-failed"),
}

export const showcase: ProxyRoutes = {}
