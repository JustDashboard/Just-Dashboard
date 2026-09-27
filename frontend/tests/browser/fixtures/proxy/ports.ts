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
    scope: "all",
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
    scope: "loopback",
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
    scope: "all",
    exposed: true,
  },
]

/**
 * Sockets as a real host lists them: sshd on every interface in both
 * families, caddy on the tailnet address, the DHCP client on the public
 * address, Redis on a public address of its own, and two on loopback.
 */
export const hostPorts = [
  {
    protocol: "tcp",
    address: "0.0.0.0",
    port: 22,
    pid: 2450808,
    process: "sshd",
    cmdline: "sshd: /usr/sbin/sshd -D [listener] 0 of 10-100 startups",
    user: "root",
    scope: "all",
    exposed: true,
  },
  {
    protocol: "tcp",
    address: "::",
    port: 22,
    pid: 2450808,
    process: "sshd",
    cmdline: "sshd: /usr/sbin/sshd -D [listener] 0 of 10-100 startups",
    user: "root",
    scope: "all",
    exposed: true,
  },
  {
    protocol: "udp",
    address: "127.0.0.53",
    port: 53,
    pid: 4412,
    process: "systemd-resolve",
    cmdline: "/usr/lib/systemd/systemd-resolved",
    user: "systemd-resolve",
    scope: "loopback",
    exposed: false,
  },
  {
    protocol: "udp",
    address: "57.131.21.87",
    port: 68,
    pid: 998,
    process: "systemd-network",
    cmdline: "/usr/lib/systemd/systemd-networkd",
    user: "systemd-network",
    scope: "interface",
    exposed: true,
  },
  {
    protocol: "tcp",
    address: "127.0.0.1",
    port: 5432,
    pid: 3100,
    process: "postgres",
    cmdline: "postgres -D /var/lib/postgresql/17/main",
    user: "postgres",
    scope: "loopback",
    exposed: false,
  },
  {
    protocol: "tcp",
    address: "203.0.113.5",
    port: 6379,
    pid: 3200,
    process: "redis-server",
    cmdline: "/usr/bin/redis-server 203.0.113.5:6379",
    user: "redis",
    scope: "interface",
    exposed: true,
  },
  {
    protocol: "tcp",
    address: "100.110.34.31",
    port: 8443,
    pid: 2066,
    process: "caddy",
    cmdline: "/usr/bin/caddy run --config /etc/caddy/Caddyfile",
    user: "caddy",
    scope: "interface",
    exposed: true,
  },
]

export const routes: ProxyRoutes = {
  "/ports": (route) => json(route, ports),
}

export const showcase: ProxyRoutes = {
  "/ports": (route) => json(route, hostPorts),
}
