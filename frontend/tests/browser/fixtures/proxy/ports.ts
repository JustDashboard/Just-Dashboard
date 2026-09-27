import { json, type ProxyRoutes } from "./shared"

export const ports = [
  {
    protocol: "tcp",
    address: "0.0.0.0",
    port: 443,
    pid: 812,
    process: "nginx",
    cmdline: "nginx: master process",
    user: "root",
    exposed: true,
  },
  {
    protocol: "tcp",
    address: "127.0.0.1",
    port: 3000,
    pid: 1400,
    process: "node",
    cmdline: "node server.js",
    user: "app",
    exposed: false,
  },
  {
    protocol: "tcp",
    address: "0.0.0.0",
    port: 5432,
    pid: 900,
    process: "postgres",
    cmdline: "postgres -D /var/lib/postgresql",
    user: "postgres",
    exposed: true,
  },
]

export const routes: ProxyRoutes = {
  "/ports": (route) => json(route, ports),
}

export const showcase: ProxyRoutes = {}
