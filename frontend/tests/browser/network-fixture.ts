import type { Page, Route } from "@playwright/test"

/**
 * The Network section's API, mocked from the shapes `backend/internal/netx`
 * writes. The machine it describes is a small VPS like the one the section
 * was built on: an uplink with a public address in each family, Tailscale, a
 * WireGuard server with a phone and an office on it, five Docker networks
 * with the products a self-hosted box runs, and a bridge and a VLAN the
 * dashboard made. Addresses are documentation ranges and made-up tailnet
 * ones; nothing here is a real host's.
 *
 * The live ring is generated rather than recorded so a screenshot always has
 * fifteen minutes of shape to draw, and its newest point is "now".
 */

export type Mutation = { method: string; path: string; body: unknown }

const now = new Date()
export const iso = (minutesAgo: number) =>
  new Date(now.getTime() - minutesAgo * 60_000).toISOString()

export const admin = {
  authenticated: true,
  needsTotp: false,
  needsEnrollment: false,
  require2fa: false,
  capabilities: ["read", "service.control", "file.write", "terminal", "destructive", "system.admin"],
  user: {
    id: 1,
    username: "operator",
    role: "admin",
    totpEnabled: true,
    disabled: false,
    mustChangePassword: false,
    lastLoginAt: iso(60),
    createdAt: iso(60 * 24 * 30),
  },
}

export const exposure = {
  grade: "tailscale",
  summary: "Reachable only from your tailnet. Nothing is exposed to the internet.",
  allowlist: ["100.64.0.0/10"],
  interfaces: ["tailscale0"],
  tailscaleIp: "100.110.34.31",
  client: "100.110.34.9",
}

export const posture = {
  status: "ok",
  checkedAt: iso(2),
  checks: 7,
  skipped: [],
  findings: [],
}

export const firewall = {
  backend: "ufw",
  available: true,
  enabled: true,
  defaultPolicy: "deny (incoming), allow (outgoing), deny (routed)",
  policy: { incoming: "deny", outgoing: "allow", routed: "deny" },
  logging: "on (low)",
  capabilities: {
    editable: true,
    toggle: true,
    defaultPolicy: true,
    logging: true,
    reset: true,
    profiles: true,
  },
  rules: [
    {
      number: 1,
      action: "ALLOW",
      direction: "IN",
      to: "22/tcp",
      from: "Anywhere",
      port: "22",
      protocol: "tcp",
      service: "SSH",
      raw: "22/tcp ALLOW IN Anywhere",
    },
    {
      number: 2,
      action: "ALLOW",
      direction: "IN",
      to: "443/tcp",
      from: "Anywhere",
      port: "443",
      protocol: "tcp",
      service: "HTTPS",
      raw: "443/tcp ALLOW IN Anywhere",
    },
    {
      number: 3,
      action: "ALLOW",
      direction: "IN",
      to: "51820/udp",
      from: "Anywhere",
      port: "51820",
      protocol: "udp",
      comment: "WireGuard wg0",
      raw: "51820/udp ALLOW IN Anywhere # WireGuard wg0",
    },
  ],
}

const counters = (rx: number, tx: number, errors = 0, dropped = 0) => ({
  rxBytes: rx,
  txBytes: tx,
  rxPackets: Math.round(rx / 900),
  txPackets: Math.round(tx / 900),
  rxErrors: errors,
  txErrors: 0,
  rxDropped: dropped,
  txDropped: 0,
})

const v6ll = (n: number) => ({
  cidr: `fe80::${n.toString(16)}:ff:fe00:1/64`,
  family: "inet6",
  scope: "link",
})

type Link = Record<string, unknown> & { name: string; rxRate: number; txRate: number }

/** The devices, with the rates the live ring's newest point carries. */
export const links: Link[] = [
  {
    name: "ens3",
    index: 2,
    kind: "physical",
    role: "uplink",
    owner: "system",
    state: "up",
    adminUp: true,
    carrier: true,
    mtu: 1500,
    mac: "fa:16:3e:00:1a:48",
    qdisc: "fq_codel",
    speedMbps: 1000,
    addresses: [
      {
        cidr: "203.0.113.20/32",
        family: "inet",
        scope: "global",
        dynamic: true,
        public: true,
        guard:
          "The uplink's own address: the provider assigned it and the internet reaches this server at it.",
      },
      {
        cidr: "2001:db8:2005:100::13/128",
        family: "inet6",
        scope: "global",
        public: true,
        guard:
          "The uplink's own address: the provider assigned it and the internet reaches this server at it.",
      },
      v6ll(1),
    ],
    uplink: true,
    counters: counters(184_000_000_000, 96_000_000_000, 2, 41),
    rxRate: 2_840_000,
    txRate: 1_120_000,
    managed: false,
    guard: "ens3 carries the default route: it is how this server reaches the internet.",
  },
  {
    name: "tailscale0",
    index: 4,
    kind: "tun",
    role: "tunnel",
    owner: "tailscale",
    state: "unknown",
    adminUp: true,
    carrier: true,
    mtu: 1280,
    qdisc: "fq_codel",
    addresses: [
      {
        cidr: "100.110.34.31/32",
        family: "inet",
        scope: "global",
        guard: "Your browser is answered from this address.",
      },
      { cidr: "fd7a:115c:a1e0::4c01:2220/128", family: "inet6", scope: "global" },
    ],
    uplink: false,
    clientPath: true,
    counters: counters(4_200_000_000, 9_100_000_000),
    rxRate: 48_200,
    txRate: 312_000,
    managed: false,
    guard: "Your browser is reached through tailscale0.",
  },
  {
    name: "wg0",
    index: 7,
    kind: "wireguard",
    role: "tunnel",
    owner: "wireguard",
    state: "unknown",
    adminUp: true,
    carrier: true,
    mtu: 1420,
    addresses: [{ cidr: "10.8.0.1/24", family: "inet", scope: "global" }],
    uplink: false,
    counters: counters(2_900_000_000, 31_000_000_000),
    rxRate: 21_000,
    txRate: 640_000,
    managed: false,
  },
  {
    name: "jd-lab",
    index: 8,
    kind: "bridge",
    role: "bridge",
    owner: "just-dashboard",
    state: "up",
    adminUp: true,
    carrier: true,
    mtu: 1500,
    mac: "02:00:00:aa:bb:08",
    qdisc: "noqueue",
    members: ["vlan30"],
    addresses: [{ cidr: "192.168.50.1/24", family: "inet", scope: "global", managed: true }],
    uplink: false,
    counters: counters(12_000_000, 9_000_000),
    rxRate: 3_400,
    txRate: 2_900,
    managed: true,
  },
  {
    name: "vlan30",
    index: 9,
    kind: "vlan",
    role: "vlan",
    owner: "just-dashboard",
    state: "up",
    adminUp: true,
    carrier: true,
    mtu: 1500,
    parent: "ens3",
    vlanId: 30,
    master: "jd-lab",
    addresses: [],
    uplink: false,
    counters: counters(1_000_000, 800_000),
    rxRate: 0,
    txRate: 0,
    managed: true,
  },
  ...dockerBridge("docker0", 5, "bridge", "10.0.0.1/24", 410_000, 1_900_000),
  ...dockerBridge("br-93e5e9c9442b", 10, "web", "10.0.4.1/24", 1_180_000, 2_260_000),
  ...dockerBridge("br-b17d23d019f2", 11, "automation", "10.0.1.1/24", 96_000, 54_000),
  ...dockerBridge("br-041b209e03b1", 12, "monitoring", "10.0.5.1/24", 8_400, 22_000),
  {
    name: "lo",
    index: 1,
    kind: "loopback",
    role: "loopback",
    owner: "kernel",
    state: "unknown",
    adminUp: true,
    carrier: true,
    mtu: 65536,
    addresses: [
      { cidr: "127.0.0.1/8", family: "inet", scope: "host" },
      { cidr: "::1/128", family: "inet6", scope: "host" },
    ],
    uplink: false,
    counters: counters(90_000_000_000, 90_000_000_000),
    rxRate: 880_000,
    txRate: 880_000,
    managed: false,
    guard: "Loopback carries this machine's own traffic, the dashboard's included.",
  },
  {
    name: "gre0",
    index: 3,
    kind: "gre",
    role: "tunnel",
    owner: "kernel",
    state: "down",
    adminUp: false,
    carrier: false,
    mtu: 1476,
    addresses: [],
    uplink: false,
    counters: counters(0, 0),
    rxRate: 0,
    txRate: 0,
    managed: false,
    guard:
      "The kernel made this when the tunnel module loaded; it carries nothing and goes when the module does.",
  },
  ...veth("veth6e4f828", 66193, "docker0", "postgres", "postgres:17-alpine", 120_000, 340_000),
  ...veth("veth7a16418", 66194, "docker0", "redis", "redis:7", 40_000, 22_000),
  ...veth("veth78bb67b", 66201, "br-93e5e9c9442b", "proxy", "caddy:2-alpine", 1_100_000, 2_100_000),
  ...veth("vethd366621", 66202, "br-93e5e9c9442b", "website", "ghcr.io/acme/website:main", 80_000, 160_000),
  ...veth("veth634b694", 66211, "br-b17d23d019f2", "n8n", "n8nio/n8n:1.94", 96_000, 54_000),
  ...veth("vethc7e574f", 66212, "br-041b209e03b1", "uptime", "louislam/uptime-kuma:2.5.5", 8_400, 22_000),
]

function dockerBridge(
  name: string,
  index: number,
  network: string,
  cidr: string,
  rx: number,
  tx: number,
): Link[] {
  return [
    {
      name,
      index,
      kind: "bridge",
      role: "bridge",
      owner: "docker",
      state: "up",
      adminUp: true,
      carrier: true,
      mtu: 1500,
      qdisc: "noqueue",
      addresses: [{ cidr, family: "inet", scope: "global" }, v6ll(index)],
      uplink: false,
      counters: counters(rx * 40_000, tx * 40_000),
      rxRate: rx,
      txRate: tx,
      dockerNetwork: network,
      managed: false,
      guard: "Docker owns this device and recreates it as it needs; change it through the Docker pages.",
    },
  ]
}

function veth(
  name: string,
  index: number,
  master: string,
  container: string,
  image: string,
  rx: number,
  tx: number,
): Link[] {
  return [
    {
      name,
      index,
      kind: "veth",
      role: "container",
      owner: "docker",
      state: "up",
      adminUp: true,
      carrier: true,
      mtu: 1500,
      master,
      addresses: [v6ll(index)],
      uplink: false,
      counters: counters(rx * 30_000, tx * 30_000),
      rxRate: tx,
      txRate: rx,
      container,
      containerImage: image,
      managed: false,
      guard: "Docker owns this device and recreates it as it needs; change it through the Docker pages.",
    },
  ]
}

export const overview = {
  hostname: "vps-edge-01",
  client: {
    address: "100.110.34.9",
    device: "tailscale0",
    source: "100.110.34.31",
  },
  defaults: [
    { family: "inet", device: "ens3", gateway: "203.0.113.1", metric: 100 },
    { family: "inet6", device: "ens3", gateway: "2001:db8:2005:100::", metric: 1024 },
  ],
  publicAddresses: ["203.0.113.20/32", "2001:db8:2005:100::13/128"],
  links,
  dockerNetworks: [
    {
      id: "93e5e9c9442b5f1a",
      name: "web",
      bridge: "br-93e5e9c9442b",
      subnets: ["10.0.4.0/24"],
      containers: [
        { name: "proxy", image: "caddy:2-alpine" },
        { name: "website", image: "ghcr.io/acme/website:main" },
      ],
    },
    {
      id: "docker0bridge000",
      name: "bridge",
      bridge: "docker0",
      subnets: ["10.0.0.0/24"],
      containers: [
        { name: "postgres", image: "postgres:17-alpine" },
        { name: "redis", image: "redis:7" },
      ],
    },
    {
      id: "b17d23d019f2aa00",
      name: "automation",
      bridge: "br-b17d23d019f2",
      subnets: ["10.0.1.0/24"],
      containers: [{ name: "n8n", image: "n8nio/n8n:1.94" }],
    },
    {
      id: "041b209e03b1bb00",
      name: "monitoring",
      bridge: "br-041b209e03b1",
      subnets: ["10.0.5.0/24"],
      containers: [{ name: "uptime", image: "louislam/uptime-kuma:2.5.5" }],
    },
  ],
  firewall: { backend: "ufw", available: true, enabled: true, incoming: "deny", rules: 3 },
  connections: { total: 214, peers: 38, fromInternet: 11, listening: 27 },
  forwarding: { ipv4: true, ipv6: true },
  persistence: { unit: "enabled", made: 7, dir: "/etc/just-dashboard/network" },
  made: {
    links: 2,
    routes: 1,
    rules: 1,
    forwards: 2,
    nat: 1,
    limits: 1,
    blocklists: 2,
    shaping: 1,
    namespaces: 0,
  },
  findings: [
    {
      id: "link.errors.ens3",
      level: "warning",
      title: "ens3 dropped or failed 43 packets in the last hour",
      detail:
        "Errors on the uplink are a cable, a driver or the provider; drops are a queue too short for the traffic.",
      href: "/network/interfaces",
    },
    {
      id: "dns.plain",
      level: "notice",
      title: "Name lookups leave this server unencrypted",
      detail:
        "Every upstream resolver is asked in plain DNS, which anyone on the path can read and rewrite. DNS over TLS is a setting away.",
      href: "/network/dns",
    },
  ],
}

/** Fifteen minutes of two-second readings, ending now, shaped by a few slow waves and a burst. */
export function liveSeries(nowSeconds = Math.floor(Date.now() / 1000)) {
  const series: Record<string, { t: number; rx: number; tx: number }[]> = {}
  for (const link of links) {
    if (link.kind === "veth" && !link.container) continue
    const points: { t: number; rx: number; tx: number }[] = []
    const phase = (link.index as number) * 0.7
    for (let i = 449; i >= 0; i--) {
      const t = nowSeconds - i * 2
      const wave = 0.65 + 0.25 * Math.sin(i / 23 + phase) + 0.1 * Math.sin(i / 5 + phase * 2)
      const burst = i > 120 && i < 150 ? 2.4 : 1
      const jitter = 0.9 + ((i * 7919 + (link.index as number) * 104729) % 100) / 500
      points.push({
        t,
        rx: Math.round(link.rxRate * wave * burst * jitter),
        tx: Math.round(link.txRate * wave * (i > 300 && i < 330 ? 1.8 : 1) * jitter),
      })
    }
    series[link.name] = points
  }
  return series
}

export function json(route: Route, body: unknown, status = 200) {
  return route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
}

/**
 * Every route the Network pages read. A test narrows or replaces one through
 * `overrides`, keyed by path; every mutation is recorded and answered with
 * success, so a test reads back exactly what a form sent.
 */
export async function mockNetwork(
  page: Page,
  mutations: Mutation[] = [],
  options: { overrides?: Record<string, unknown>; session?: typeof admin } = {},
) {
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    if (request.method() !== "GET") {
      mutations.push({ method: request.method(), path, body: request.postDataJSON() })
      return json(route, { output: "ok" })
    }
    const overrides = options.overrides ?? {}
    if (path in overrides) return json(route, overrides[path])
    switch (path) {
      case "/auth/session":
        return json(route, options.session ?? admin)
      case "/exposure":
        return json(route, exposure)
      case "/security/posture":
        return json(route, posture)
      case "/firewall/":
        return json(route, firewall)
      case "/network/overview":
        return json(route, overview)
      case "/network/links":
        return json(route, links)
      case "/network/traffic/live": {
        const since = Number(url.searchParams.get("since") ?? 0)
        const series = liveSeries()
        const out: Record<string, { t: number; rx: number; tx: number }[]> = {}
        for (const [name, points] of Object.entries(series)) {
          out[name] = points.filter((p) => p.t > since)
        }
        return json(route, { now: Math.floor(Date.now() / 1000), series: out })
      }
      case "/jobs":
      case "/jobs/":
        return json(route, [])
      case "/updates/self":
        return json(route, { current: "0.7.1", latest: "0.7.1" })
      default:
        return json(route, [])
    }
  })
}
