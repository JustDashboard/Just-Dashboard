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
  capabilities: [
    "read",
    "service.control",
    "file.write",
    "terminal",
    "destructive",
    "system.admin",
  ],
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
  ...veth(
    "vethd366621",
    66202,
    "br-93e5e9c9442b",
    "website",
    "ghcr.io/acme/website:main",
    80_000,
    160_000,
  ),
  ...veth("veth634b694", 66211, "br-b17d23d019f2", "n8n", "n8nio/n8n:1.94", 96_000, 54_000),
  ...veth(
    "vethc7e574f",
    66212,
    "br-041b209e03b1",
    "uptime",
    "louislam/uptime-kuma:2.5.5",
    8_400,
    22_000,
  ),
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
      guard:
        "Docker owns this device and recreates it as it needs; change it through the Docker pages.",
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
      guard:
        "Docker owns this device and recreates it as it needs; change it through the Docker pages.",
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
  vpn: {
    wireguard: {
      installed: true,
      interfaces: 1,
      peers: 4,
      online: 2,
      rx: 2_900_000_000,
      tx: 31_000_000_000,
    },
    tailscale: {
      installed: true,
      running: true,
      peers: 7,
      online: 4,
      exitNode: true,
      subnetRoutes: 1,
      selfIps: ["100.110.34.31", "fd7a:115c:a1e0::4c01:2220"],
    },
  },
  dns: {
    resolver: "systemd-resolved",
    mode: "stub",
    upstreams: ["1.1.1.1", "1.0.0.1"],
    dnsOverTLS: "no",
    dnssec: "no",
    adblock: false,
    custom: false,
  },
  gateway: {
    loaded: true,
    writable: true,
    forwards: 2,
    nat: 1,
    limits: 1,
    blocklists: 2,
    dropped: 18_422,
    conntrack: { available: true, count: 965, max: 262144, percent: 0.37, level: "ok" },
  },
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
      href: "/network/interfaces?device=ens3",
      source: "history",
    },
    {
      id: "dns.plain",
      level: "notice",
      title: "Name lookups leave this server unencrypted",
      detail:
        "Every upstream resolver is asked in plain DNS, which anyone on the path can read and rewrite. DNS over TLS is a setting away.",
      href: "/network/dns",
      source: "dns",
    },
  ],
  identity: [
    {
      family: "inet",
      forwarding: true,
      device: "ens3",
      gateway: "203.0.113.1",
      source: "203.0.113.20",
      sourceScope: "public",
      public: "nic",
      detail:
        "Traffic leaves from 203.0.113.20, the NIC's own public address. A provider firewall or 1:1 NAT in front of the NIC is not visible from this host.",
    },
    {
      family: "inet6",
      forwarding: true,
      device: "ens3",
      gateway: "2001:db8:2005:100::",
      source: "2001:db8:2005:100::13",
      sourceScope: "public",
      public: "nic",
      detail:
        "Traffic leaves from 2001:db8:2005:100::13, the NIC's own public address. A provider firewall or 1:1 NAT in front of the NIC is not visible from this host.",
    },
  ],
  flows: {
    state: "ok",
    readAt: iso(0),
    total: 412,
    classified: 57,
    truncated: false,
    accounting: false,
    edges: [
      {
        from: "docker:93e5e9c9442b5f1a",
        to: "internet",
        flows: 31,
        protocols: ["tcp", "udp"],
        translation: "masquerade",
        via: "ens3",
        path: ["web", "br-93e5e9c9442b", "this server", "masquerade on ens3", "the internet"],
      },
      {
        from: "internet",
        to: "docker:93e5e9c9442b5f1a",
        flows: 18,
        protocols: ["tcp"],
        translation: "forward",
        via: "ens3",
        path: ["the internet", "this server", "port forward on ens3", "br-93e5e9c9442b", "web"],
      },
      {
        from: "link:tailscale0",
        to: "host",
        flows: 8,
        protocols: ["tcp"],
        translation: "none",
        path: ["the tailnet", "tailscale0", "this server"],
      },
    ],
  },
  observations: [
    {
      source: "client",
      label: "The route back to your browser",
      state: "ok",
      href: "/network/routing",
    },
    { source: "firewall", label: "The host firewall", state: "ok", href: "/network/firewall" },
    {
      source: "history",
      label: "Recorded interface errors",
      state: "ok",
      href: "/network/traffic",
    },
  ],
  incidents: [
    {
      id: 7,
      findingId: "link.errors.ens3",
      source: "history",
      level: "warning",
      title: "ens3 dropped or failed 43 packets in the last hour",
      detail: "Errors on the uplink are a cable, a driver or the provider.",
      href: "/network/interfaces?device=ens3",
      openedAt: iso(42),
      lastSeenAt: iso(0),
      related: [6],
    },
    {
      id: 6,
      findingId: "link.carrier.ens3",
      source: "links",
      level: "critical",
      title: "ens3 has no carrier",
      detail: "The device is up but nothing is connected to it.",
      href: "/network/interfaces?device=ens3",
      openedAt: iso(43),
      lastSeenAt: iso(40),
      resolvedAt: iso(39),
      related: [7],
    },
  ],
  readAt: iso(0),
}

/** A device's driver, offloads and counters, as `/links/{name}/detail` answers. */
export function linkDetail(name: string) {
  const ok = { state: "ok" }
  const physical = name === "ens3"
  return {
    name,
    checkedAt: iso(0),
    driver: physical
      ? { name: "virtio_net", version: "1.0.0", bus: "0000:00:03.0" }
      : { name: "bridge" },
    driverRead: ok,
    offloads: physical
      ? [
          { name: "rx-checksumming", enabled: true, fixed: true },
          { name: "generic-receive-offload", enabled: true, fixed: false },
          { name: "large-receive-offload", enabled: false, fixed: true },
        ]
      : [],
    offloadsRead: physical
      ? ok
      : { state: "not_applicable", reason: "The driver reports no offload features." },
    errors: {
      rxErrors: physical ? 43 : 0,
      rxDropped: 0,
      rxOverErrors: 0,
      rxLengthErrors: 0,
      rxCrcErrors: physical ? 41 : 0,
      rxFrameErrors: physical ? 2 : 0,
      rxFifoErrors: 0,
      rxMissedErrors: 0,
      txErrors: 0,
      txDropped: 0,
      txCarrierErrors: 0,
      txCollisions: 0,
      txAbortedErrors: 0,
      txFifoErrors: 0,
      txWindowErrors: 0,
      txHeartbeatErrors: 0,
      carrierChanges: physical ? 3 : 0,
    },
    errorsRead: ok,
  }
}

/** The managed bridge read as a switch. */
export function bridgeView(name: string) {
  const ok = { state: "ok" }
  return {
    name,
    managed: name === "jd-lab",
    checkedAt: iso(0),
    stp: false,
    vlanFiltering: name === "jd-lab",
    multicastSnooping: true,
    defaultPvid: 1,
    ageingSeconds: 300,
    vlanProtocol: "802.1Q",
    settingsRead: ok,
    ports: [
      {
        name,
        self: true,
        managed: name === "jd-lab",
        vlans: [{ vid: 1, pvid: true, untagged: true }],
        desired: [{ vid: 1, pvid: true, untagged: true }],
      },
      {
        name: "vlan30",
        self: false,
        managed: true,
        vlans: [{ vid: 1, pvid: true, untagged: true }],
        desired: [{ vid: 1, pvid: true, untagged: true }],
      },
    ],
    vlansRead: ok,
    fdb: [
      { mac: "02:42:0a:00:04:02", port: "vlan30", vlan: 1, state: "", static: false },
      { mac: "02:00:00:aa:bb:08", port: name, vlan: 1, state: "permanent", static: true },
    ],
    fdbTotal: 2,
    fdbRead: ok,
  }
}

/** What `/links/{name}/readiness` answers for the kinds that have checks. */
export function readiness(name: string) {
  return {
    name,
    kind: "unknown",
    checkedAt: iso(0),
    checks: [],
    limits: [`There are no readiness checks for ${name}.`],
  }
}

/** A namespace read as its own network. */
export function namespaceDetail(name: string, kind: string) {
  const ok = { state: "ok" }
  const container = kind === "container"
  return {
    name,
    kind,
    managed: !container,
    image: container ? "postgres:17-alpine" : undefined,
    pid: container ? 4412 : undefined,
    checkedAt: iso(0),
    devices: [
      {
        name: container ? "eth0" : "lab-n",
        state: "up",
        mtu: 1500,
        addresses: [container ? "10.0.0.3/24" : "192.168.50.20/24"],
      },
    ],
    devicesRead: ok,
    routes: [
      {
        family: "inet",
        destination: "default",
        gateway: container ? "10.0.0.1" : "192.168.50.1",
        device: container ? "eth0" : "lab-n",
        protocol: "static",
      },
      {
        family: "inet",
        destination: container ? "10.0.0.0/24" : "192.168.50.0/24",
        device: container ? "eth0" : "lab-n",
        protocol: "kernel",
        source: container ? "10.0.0.3" : "192.168.50.20",
      },
    ],
    routesRead: ok,
    dns: {
      nameservers: [container ? "127.0.0.11" : "192.168.50.1"],
      search: [],
      options: container ? ["ndots:0"] : [],
    },
    dnsRead: ok,
    listeners: container ? [{ protocol: "tcp", address: "0.0.0.0", port: 5432 }] : [],
    listenersRead: ok,
  }
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

export const connections = {
  total: 214,
  listening: 27,
  loopback: 61,
  peers: [
    {
      address: "198.51.100.23",
      count: 18,
      established: 14,
      ports: [443],
      processes: ["caddy"],
      private: false,
      service: "HTTPS",
    },
    {
      address: "203.0.113.77",
      count: 9,
      established: 6,
      ports: [443, 80],
      processes: ["caddy"],
      private: false,
      service: "HTTPS",
    },
    {
      address: "192.0.2.145",
      count: 4,
      established: 1,
      ports: [22],
      processes: ["sshd"],
      private: false,
      service: "SSH",
    },
    {
      address: "198.51.100.200",
      count: 3,
      established: 3,
      ports: [51820],
      processes: [],
      private: false,
      service: "WireGuard",
    },
    {
      address: "100.110.34.9",
      count: 12,
      established: 12,
      ports: [443, 8443],
      processes: ["caddy"],
      private: true,
    },
    {
      address: "100.84.53.82",
      count: 5,
      established: 4,
      ports: [5432],
      processes: ["postgres"],
      private: true,
      service: "PostgreSQL",
    },
    {
      address: "10.0.4.3",
      count: 22,
      established: 20,
      ports: [5432],
      processes: ["postgres"],
      private: true,
      service: "PostgreSQL",
    },
    {
      address: "10.0.1.2",
      count: 7,
      established: 7,
      ports: [6379],
      processes: ["redis-server"],
      private: true,
      service: "Redis",
    },
  ],
}

const handshake = (secondsAgo: number) => Math.floor(Date.now() / 1000) - secondsAgo

const wgPeer = (
  id: number,
  name: string,
  kind: "device" | "site",
  address: string,
  allowed: string[],
  seenAgo: number,
  rx: number,
  tx: number,
) => ({
  id,
  name,
  kind,
  publicKey: `${name.replace(/\W/g, "")}PublicKeyAAAAAAAAAAAAAAAAAAAAAAAAAAA=`.slice(0, 44),
  address,
  allowedIps: allowed,
  endpoint: seenAgo < 180 ? "198.51.100.40:51820" : "",
  latestHandshake: seenAgo < 0 ? 0 : handshake(seenAgo),
  online: seenAgo >= 0 && seenAgo < 180,
  rxBytes: rx,
  txBytes: tx,
  keepalive: kind === "site" ? 25 : 0,
  hasConfig: true,
  createdAt: iso(60 * 24 * 12),
})

export const vpn = {
  wireguard: {
    installed: true,
    tools: { wg: true, wgQuick: true },
    package: "wireguard-tools",
    kernel: true,
    systemd: true,
    interfaces: [
      {
        name: "wg0",
        managed: true,
        configured: true,
        up: true,
        enabled: true,
        active: true,
        publicKey: "ServerPublicKeyAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
        listenPort: 51820,
        addresses: ["10.8.0.1/24"],
        mtu: 1420,
        dns: ["94.140.14.14", "94.140.15.15"],
        endpoint: "203.0.113.20:51820",
        exitNode: true,
        subnet: "10.8.0.0/24",
        peers: [
          wgPeer(
            1,
            "Ana's phone",
            "device",
            "10.8.0.2/32",
            ["10.8.0.2/32"],
            41,
            1_400_000_000,
            18_000_000_000,
          ),
          wgPeer(
            2,
            "Work laptop",
            "device",
            "10.8.0.3/32",
            ["10.8.0.3/32"],
            95,
            900_000_000,
            11_000_000_000,
          ),
          wgPeer(
            3,
            "Tablet",
            "device",
            "10.8.0.4/32",
            ["10.8.0.4/32"],
            60 * 60 * 30,
            20_000_000,
            400_000_000,
          ),
          wgPeer(
            4,
            "Office",
            "site",
            "10.8.0.10/32",
            ["10.8.0.10/32", "192.168.1.0/24"],
            12,
            600_000_000,
            1_600_000_000,
          ),
          wgPeer(
            5,
            "Backup server",
            "site",
            "10.8.0.11/32",
            ["10.8.0.11/32", "172.20.0.0/16"],
            -1,
            0,
            0,
          ),
        ],
      },
    ],
  },
  tailscale: {
    installed: true,
    running: true,
    backendState: "Running",
    version: "1.90.4-t1234",
    self: {
      hostName: "vps-edge-01",
      dnsName: "vps-edge-01.tail1234.ts.net.",
      tailscaleIps: ["100.110.34.31", "fd7a:115c:a1e0::4c01:2220"],
      os: "linux",
      online: true,
      exitNodeOption: true,
      primaryRoutes: ["10.0.4.0/24"],
      relay: "fra",
    },
    magicDnsSuffix: "tail1234.ts.net",
    tailnet: "ana@example.com",
    health: [],
    peers: [
      {
        id: "n1",
        hostName: "ana-macbook",
        dnsName: "ana-macbook.tail1234.ts.net.",
        os: "macOS",
        tailscaleIps: ["100.110.34.9"],
        online: true,
        active: true,
        exitNode: false,
        exitNodeOption: false,
        relay: "fra",
        direct: true,
        curAddr: "198.51.100.40:41641",
        rxBytes: 4_100_000_000,
        txBytes: 820_000_000,
        lastSeen: iso(0),
        userLoginName: "ana@example.com",
      },
      {
        id: "n2",
        hostName: "ana-iphone",
        dnsName: "ana-iphone.tail1234.ts.net.",
        os: "iOS",
        tailscaleIps: ["100.84.53.82"],
        online: true,
        active: false,
        exitNode: false,
        exitNodeOption: false,
        relay: "fra",
        direct: false,
        curAddr: "",
        rxBytes: 120_000_000,
        txBytes: 40_000_000,
        lastSeen: iso(0),
        userLoginName: "ana@example.com",
      },
      {
        id: "n3",
        hostName: "home-nas",
        dnsName: "home-nas.tail1234.ts.net.",
        os: "linux",
        tailscaleIps: ["100.88.254.125"],
        online: true,
        active: true,
        exitNode: false,
        exitNodeOption: true,
        relay: "ams",
        direct: true,
        curAddr: "192.0.2.80:41641",
        rxBytes: 9_000_000_000,
        txBytes: 2_000_000_000,
        lastSeen: iso(0),
        primaryRoutes: ["192.168.1.0/24"],
        userLoginName: "ana@example.com",
      },
      {
        id: "n4",
        hostName: "gaming-pc",
        dnsName: "gaming-pc.tail1234.ts.net.",
        os: "windows",
        tailscaleIps: ["100.106.3.28"],
        online: true,
        active: false,
        exitNode: false,
        exitNodeOption: false,
        relay: "fra",
        direct: true,
        curAddr: "198.51.100.41:41641",
        rxBytes: 50_000_000,
        txBytes: 9_000_000,
        lastSeen: iso(0),
        userLoginName: "ana@example.com",
      },
      {
        id: "n5",
        hostName: "pixel",
        dnsName: "pixel.tail1234.ts.net.",
        os: "android",
        tailscaleIps: ["100.107.233.73"],
        online: false,
        active: false,
        exitNode: false,
        exitNodeOption: false,
        relay: "fra",
        direct: false,
        curAddr: "",
        rxBytes: 0,
        txBytes: 0,
        lastSeen: iso(60 * 5),
        userLoginName: "ana@example.com",
      },
      {
        id: "n6",
        hostName: "old-laptop",
        dnsName: "old-laptop.tail1234.ts.net.",
        os: "linux",
        tailscaleIps: ["100.100.170.106"],
        online: false,
        active: false,
        exitNode: false,
        exitNodeOption: false,
        relay: "",
        direct: false,
        curAddr: "",
        rxBytes: 0,
        txBytes: 0,
        lastSeen: iso(60 * 24 * 9),
        userLoginName: "ana@example.com",
        expired: true,
      },
    ],
    prefs: {
      advertiseRoutes: ["10.0.4.0/24"],
      advertisingExitNode: true,
      usingExitNode: false,
      routeAll: false,
      corpDns: true,
      shieldsUp: false,
    },
    controlServer: "tailscale",
    controlUrl: "https://controlplane.tailscale.com",
    clientOnTailnet: true,
    forwarding: { ipv4: true, ipv6: true },
    warnings: [],
  },
  headscale: { installed: false, nodes: [], users: [] },
}

export const routing = {
  tables: [
    {
      id: 254,
      name: "main",
      routes: [
        {
          id: 0,
          family: "inet",
          destination: "default",
          type: "unicast",
          gateway: "203.0.113.1",
          device: "ens3",
          protocol: "dhcp",
          metric: 100,
          flags: [],
          nexthops: [],
          owner: "dhcp",
          managed: false,
          guard: "The default route: how this server reaches the internet.",
        },
        {
          id: 0,
          family: "inet",
          destination: "10.0.0.0/24",
          type: "unicast",
          device: "docker0",
          protocol: "kernel",
          scope: "link",
          metric: 0,
          source: "10.0.0.1",
          flags: [],
          nexthops: [],
          owner: "docker",
          managed: false,
        },
        {
          id: 0,
          family: "inet",
          destination: "10.0.4.0/24",
          type: "unicast",
          device: "br-93e5e9c9442b",
          protocol: "kernel",
          scope: "link",
          metric: 0,
          source: "10.0.4.1",
          flags: [],
          nexthops: [],
          owner: "docker",
          managed: false,
        },
        {
          id: 0,
          family: "inet",
          destination: "10.8.0.0/24",
          type: "unicast",
          device: "wg0",
          protocol: "kernel",
          scope: "link",
          metric: 0,
          source: "10.8.0.1",
          flags: [],
          nexthops: [],
          owner: "wireguard",
          managed: false,
        },
        {
          id: 3,
          family: "inet",
          destination: "192.168.1.0/24",
          type: "unicast",
          device: "wg0",
          protocol: "static",
          metric: 0,
          flags: [],
          nexthops: [],
          owner: "just-dashboard",
          managed: true,
        },
        {
          id: 0,
          family: "inet",
          destination: "192.168.50.0/24",
          type: "unicast",
          device: "jd-lab",
          protocol: "kernel",
          scope: "link",
          metric: 0,
          source: "192.168.50.1",
          flags: [],
          nexthops: [],
          owner: "kernel",
          managed: false,
        },
        {
          id: 0,
          family: "inet6",
          destination: "default",
          type: "unicast",
          gateway: "fe80::1",
          device: "ens3",
          protocol: "ra",
          metric: 1024,
          flags: [],
          nexthops: [],
          owner: "dhcp",
          managed: false,
        },
      ],
    },
    {
      id: 52,
      name: "tailscale",
      routes: [
        {
          id: 0,
          family: "inet",
          destination: "100.84.53.82",
          type: "unicast",
          device: "tailscale0",
          protocol: "boot",
          metric: 0,
          flags: [],
          nexthops: [],
          owner: "tailscale",
          managed: false,
          guard: "Tailscale keeps this table.",
        },
        {
          id: 0,
          family: "inet",
          destination: "100.110.34.9",
          type: "unicast",
          device: "tailscale0",
          protocol: "boot",
          metric: 0,
          flags: [],
          nexthops: [],
          owner: "tailscale",
          managed: false,
          guard: "Tailscale keeps this table.",
        },
        {
          id: 0,
          family: "inet",
          destination: "100.100.100.100",
          type: "unicast",
          device: "tailscale0",
          protocol: "boot",
          metric: 0,
          flags: [],
          nexthops: [],
          owner: "tailscale",
          managed: false,
          guard: "Tailscale keeps this table.",
        },
      ],
    },
    {
      id: 100,
      name: "office",
      routes: [
        {
          id: 4,
          family: "inet",
          destination: "default",
          type: "unicast",
          gateway: "10.8.0.10",
          device: "wg0",
          protocol: "static",
          metric: 0,
          flags: [],
          nexthops: [],
          owner: "just-dashboard",
          managed: true,
        },
      ],
    },
  ],
  rules: [
    {
      id: 0,
      family: "inet",
      priority: 0,
      action: "lookup",
      table: 255,
      tableName: "local",
      owner: "system",
      managed: false,
      guard: "The kernel's own.",
    },
    {
      id: 0,
      family: "inet",
      priority: 5270,
      action: "lookup",
      table: 52,
      tableName: "tailscale",
      owner: "tailscale",
      managed: false,
      guard: "Tailscale's.",
    },
    {
      id: 5,
      family: "inet",
      priority: 10000,
      from: "192.168.50.0/24",
      action: "lookup",
      table: 100,
      tableName: "office",
      owner: "just-dashboard",
      managed: true,
    },
    {
      id: 0,
      family: "inet",
      priority: 32766,
      action: "lookup",
      table: 254,
      tableName: "main",
      owner: "system",
      managed: false,
      guard: "The system's.",
    },
    {
      id: 0,
      family: "inet",
      priority: 32767,
      action: "lookup",
      table: 253,
      tableName: "default",
      owner: "system",
      managed: false,
      guard: "The system's.",
    },
  ],
  clientPath: { address: "100.110.34.9", device: "tailscale0", source: "100.110.34.31" },
  hiddenLocal: 31,
  rulePriorities: { min: 10000, max: 19999 },
  forwarding: {
    ipv4: {
      available: true,
      enabled: true,
      persisted: false,
      neededBy: ["5 Docker networks", "the WireGuard exit through wg0", "Tailscale's exit node"],
      guard:
        "Turning it off would stop 5 Docker networks, the WireGuard exit through wg0, Tailscale's exit node.",
    },
    ipv6: {
      available: true,
      enabled: true,
      persisted: false,
      neededBy: ["Tailscale's exit node"],
      guard: "Turning it off would stop Tailscale's exit node.",
    },
  },
}

export const bgp = { installed: false, running: false, families: [] }

export const namespaces = [
  {
    name: "lab",
    kind: "named",
    id: 0,
    managed: true,
    devices: [{ name: "lab-n", state: "up", mtu: 1500, addresses: ["192.168.50.20/24"] }],
  },
  {
    name: "postgres",
    kind: "container",
    managed: false,
    image: "postgres:17-alpine",
    pid: 4412,
    devices: [{ name: "eth0", state: "up", mtu: 1500, addresses: ["10.0.0.3/24"] }],
  },
  {
    name: "proxy",
    kind: "container",
    managed: false,
    image: "caddy:2-alpine",
    pid: 5120,
    devices: [{ name: "eth0", state: "up", mtu: 1500, addresses: ["10.0.4.2/24"] }],
  },
]

export function json(route: Route, body: unknown, status = 200) {
  return route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
}

/**
 * Every route the Network pages read. A test narrows or replaces one through
 * `overrides`, keyed by path; every mutation is recorded and answered with
 * success, so a test reads back exactly what a form sent.
 */
/**
 * Resolves once the page has drawn its readings: the page frame is there and
 * nothing in it is still a skeleton. The Network pages poll every couple of
 * seconds, so the network never goes quiet enough for "networkidle".
 */
export async function loaded(page: Page) {
  await page.waitForSelector("[data-slot=page]")
  await page.waitForFunction(() => !document.querySelector("[data-slot=page] [data-slot=skeleton]"))
}

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
    const linkPart = path.match(
      /^\/network\/links\/([^/]+)\/(detail|bridge|readiness|master\/preview)$/,
    )
    if (linkPart) {
      const name = decodeURIComponent(linkPart[1])
      switch (linkPart[2]) {
        case "detail":
          return json(route, linkDetail(name))
        case "bridge":
          return json(route, bridgeView(name))
        case "readiness":
          return json(route, readiness(name))
        default:
          return json(route, {
            device: name,
            bridge: url.searchParams.get("master") || undefined,
            allowed: true,
            persisted: true,
            steps: [`ip link set ${name} master ${url.searchParams.get("master")}`],
            effects: [],
          })
      }
    }
    const namespacePart = path.match(/^\/network\/namespaces\/([^/]+)(\/lookup)?$/)
    if (namespacePart) {
      const name = decodeURIComponent(namespacePart[1])
      if (namespacePart[2])
        return json(route, {
          address: url.searchParams.get("target"),
          device: "lab-n",
          gateway: "192.168.50.1",
          source: "192.168.50.20",
        })
      return json(route, namespaceDetail(name, url.searchParams.get("kind") ?? "named"))
    }
    if (path.startsWith("/network/native/profiles/")) {
      return json(route, {
        checkedAt: new Date().toISOString(),
        device: decodeURIComponent(path.split("/").at(-1) ?? ""),
        kind: "physical",
        owner: "unknown",
        editable: false,
        refusal: "No supported existing native profile was verified in this fixture.",
        contract: { members: [] },
        configured: { status: "unknown" },
        runtime: { status: "unknown" },
        boot: { status: "unknown" },
      })
    }
    switch (path) {
      case "/auth/session":
        return json(route, options.session ?? admin)
      case "/exposure":
        return json(route, exposure)
      case "/security/posture":
        return json(route, posture)
      case "/firewall/":
        return json(route, firewall)
      case "/firewall/apps":
        return json(route, [{ name: "OpenSSH", ports: ["22/tcp"] }])
      case "/security/services":
        return json(route, [])
      case "/network/overview":
        return json(route, overview)
      case "/network/dns/services":
      case "/network/dns/services/provisions":
        return json(route, [])
      case "/network/links":
        return json(route, links)
      case "/connections":
        return json(route, connections)
      case "/network/vpn":
        return json(route, vpn)
      case "/network/ipam/":
        return json(route, {
          pools: [],
          reservations: [],
          utilization: [],
          limitations: [],
          inventory: {
            checkedAt: new Date().toISOString(),
            finishedAt: new Date().toISOString(),
            observations: [],
            coverage: [],
          },
        })
      case "/network/routing":
        return json(route, routing)
      case "/network/bgp":
        return json(route, bgp)
      case "/network/namespaces":
        return json(route, namespaces)
      case "/ports/meta":
        return json(route, { ephemeralRange: [32768, 60999] })
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
