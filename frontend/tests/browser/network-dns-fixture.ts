import type { Page } from "@playwright/test"
import { iso, json, links, type Mutation } from "./network-fixture"

/**
 * The DNS and Traffic pages' API, mocked from the shapes `netx` writes, on the
 * machine `network-fixture.ts` describes: a small VPS whose resolver is
 * systemd-resolved's stub over Cloudflare with DNS over TLS where it is
 * offered, Tailscale's MagicDNS for the tailnet's names, and an AdGuard Home
 * container on the `web` Docker network answering port 53 for the containers
 * beside it. Addresses are documentation ranges; nothing here is a real host's.
 *
 * `overrides` is keyed by API path for `mockNetwork(page, mutations,
 * { overrides })`; `mockNetworkWrites` answers the writes these two pages make
 * the way the server does, and records them in the same list.
 */

const now = Math.floor(Date.now() / 1000)

const preset = (
  id: string,
  name: string,
  product: string,
  tlsName: string,
  blocksAds: boolean,
  blocksMalware: boolean,
  servers: string[],
) => ({
  id,
  name,
  product,
  servers,
  tlsName,
  tlsServers: servers.map((s) => `${s}#${tlsName}`),
  blocksAds,
  blocksMalware,
})

export const presets = [
  preset("cloudflare", "Cloudflare", "cloudflare", "cloudflare-dns.com", false, false, [
    "1.1.1.1",
    "1.0.0.1",
    "2606:4700:4700::1111",
    "2606:4700:4700::1001",
  ]),
  preset(
    "cloudflare-malware",
    "Cloudflare, malware blocking",
    "cloudflare",
    "security.cloudflare-dns.com",
    false,
    true,
    ["1.1.1.2", "1.0.0.2", "2606:4700:4700::1112", "2606:4700:4700::1002"],
  ),
  preset("quad9", "Quad9", "quad9", "dns.quad9.net", false, true, [
    "9.9.9.9",
    "149.112.112.112",
    "2620:fe::fe",
    "2620:fe::9",
  ]),
  preset("google", "Google", "google", "dns.google", false, false, [
    "8.8.8.8",
    "8.8.4.4",
    "2001:4860:4860::8888",
    "2001:4860:4860::8844",
  ]),
  preset("adguard", "AdGuard DNS, ad blocking", "adguard", "dns.adguard-dns.com", true, true, [
    "94.140.14.14",
    "94.140.15.15",
    "2a10:50c0::ad1:ff",
    "2a10:50c0::ad2:ff",
  ]),
  preset(
    "mullvad-adblock",
    "Mullvad, ad blocking",
    "mullvad",
    "adblock.dns.mullvad.net",
    true,
    true,
    ["194.242.2.3", "2a07:e340::3"],
  ),
]

export const dnsView = {
  resolvConf: {
    path: "/etc/resolv.conf",
    mode: "stub",
    target: "../run/systemd/resolve/stub-resolv.conf",
    managedBy: "systemd-resolved",
    nameservers: ["127.0.0.53"],
    search: ["tail4f2c.ts.net"],
    options: ["edns0", "trust-ad"],
  },
  resolved: {
    installed: true,
    active: true,
    global: {
      protocols: ["-LLMNR", "-mDNS", "+DNSOverTLS", "DNSSEC=allow-downgrade/supported"],
      dnsOverTLS: "opportunistic",
      dnssec: "allow-downgrade",
      dnssecSupported: true,
      currentServer: "1.1.1.1#cloudflare-dns.com",
      servers: ["1.1.1.1#cloudflare-dns.com", "1.0.0.1#cloudflare-dns.com"],
      fallbackServers: [],
      domains: [],
      resolvConfMode: "stub",
    },
    links: [
      {
        name: "ens3",
        index: 2,
        scopes: ["DNS"],
        defaultRoute: false,
        protocols: ["-DefaultRoute", "-LLMNR", "-mDNS", "-DNSOverTLS"],
        dnsOverTLS: "no",
        dnssec: "no",
        dnssecSupported: false,
        servers: ["198.51.100.2"],
        fallbackServers: [],
        domains: [],
      },
      {
        name: "tailscale0",
        index: 4,
        scopes: ["DNS"],
        defaultRoute: false,
        protocols: ["-DefaultRoute", "-LLMNR", "-mDNS", "-DNSOverTLS"],
        dnsOverTLS: "no",
        dnssec: "no",
        dnssecSupported: false,
        currentServer: "100.100.100.100",
        servers: ["100.100.100.100"],
        fallbackServers: [],
        domains: ["~ts.net", "~0.100.100.in-addr.arpa"],
      },
    ],
    linksTotal: 5,
    statistics: {
      cacheSize: 214,
      cacheHits: 4214,
      cacheMisses: 1188,
      hitPercent: 78,
      transactions: 9631,
      currentTransactions: 2,
      timeouts: 3,
      failures: 11,
      dnssecSecure: 312,
      dnssecInsecure: 4871,
      dnssecBogus: 0,
      dnssecIndeterminate: 4,
    },
  },
  listeners: [
    {
      address: "127.0.0.53",
      protocol: "udp",
      process: "systemd-resolve",
      pid: 812,
      loopback: true,
      kind: "resolved-stub",
    },
    {
      address: "127.0.0.53",
      protocol: "tcp",
      process: "systemd-resolve",
      pid: 812,
      loopback: true,
      kind: "resolved-stub",
    },
    {
      address: "127.0.0.54",
      protocol: "udp",
      process: "systemd-resolve",
      pid: 812,
      loopback: true,
      kind: "resolved-stub",
    },
    {
      address: "10.0.4.9",
      protocol: "udp",
      process: "AdGuardHome",
      pid: 4410,
      container: "adguardhome",
      loopback: false,
      kind: "adguardhome",
    },
    {
      address: "10.0.4.9",
      protocol: "tcp",
      process: "AdGuardHome",
      pid: 4410,
      container: "adguardhome",
      loopback: false,
      kind: "adguardhome",
    },
  ],
  adblock: [
    {
      kind: "adguardhome",
      name: "AdGuard Home",
      runsAs: "container",
      container: "adguardhome",
      image: "adguard/adguardhome:v0.107.57",
      answering: true,
      webPort: 3000,
    },
  ],
  managed: {
    path: "/etc/systemd/resolved.conf.d/90-just-dashboard.conf",
    exists: false,
    servers: [],
    fallback: [],
    domains: [],
    dnssec: "",
    dnsOverTLS: "",
    cache: "",
  },
  presets,
}

/** The same host with the dashboard's own drop-in written and nothing filtering ads. */
export const dnsViewManaged = {
  ...dnsView,
  adblock: [],
  listeners: dnsView.listeners.slice(0, 3),
  managed: {
    ...dnsView.managed,
    exists: true,
    servers: presets[2].tlsServers.slice(0, 2),
    dnssec: "allow-downgrade",
    dnsOverTLS: "yes",
  },
}

export const hostRecords = {
  path: "/etc/hosts",
  managed: [
    { address: "192.0.2.10", names: ["nas.lan", "nas"] },
    { address: "10.0.4.20", names: ["grafana.lan"] },
  ],
  other: [
    { address: "127.0.0.1", names: ["localhost"], line: 1 },
    { address: "127.0.1.1", names: ["vps-edge-01"], line: 2 },
    { address: "::1", names: ["ip6-localhost", "ip6-loopback"], line: 3 },
    { address: "fe00::0", names: ["ip6-localnet"], line: 4 },
    { address: "ff02::1", names: ["ip6-allnodes"], line: 5 },
    { address: "ff02::2", names: ["ip6-allrouters"], line: 6 },
  ],
}

export const lookup = {
  name: "example.com",
  type: "A",
  results: [
    {
      server: "127.0.0.53",
      label: "This server's resolver",
      answers: ["93.184.215.14"],
      latencyMs: 2.4,
    },
    { server: "1.1.1.1", label: "Cloudflare", answers: ["93.184.215.14"], latencyMs: 11.8 },
    { server: "8.8.8.8", label: "Google", answers: ["93.184.215.14"], latencyMs: 17.2 },
    { server: "9.9.9.9", label: "Quad9", answers: ["93.184.215.14"], latencyMs: 24.6 },
    { server: "94.140.14.14", label: "AdGuard DNS", answers: ["93.184.215.14"], latencyMs: 61.3 },
    {
      server: "198.51.100.2",
      label: "ens3's resolver",
      answers: ["93.184.215.14"],
      latencyMs: 8.1,
    },
    {
      server: "194.242.2.3",
      label: "Mullvad",
      answers: [],
      latencyMs: 2000,
      error: "no answer within 2 s",
    },
  ],
}

/** What a good change answers: the name that resolved through the stub afterwards. */
export const applied = { verified: true, via: "cloudflare.com", millis: 14.6, managed: {} }

const iface = (
  name: string,
  rxRate: number,
  txRate: number,
  peers: {
    address: string
    port: number
    connections: number
    rxBytes: number
    txBytes: number
  }[],
  extra: Record<string, unknown> = {},
) => ({
  name,
  pids: [Math.round(rxRate % 9000) + 100],
  connections: peers.reduce((n, p) => n + p.connections, 0),
  rxRate,
  txRate,
  rxTotal: Math.round(rxRate * 5400),
  txTotal: Math.round(txRate * 5400),
  peers,
  ...extra,
})

export const processTraffic = {
  at: iso(0),
  intervalSeconds: 3,
  warming: false,
  truncated: false,
  note: "TCP only: UDP, which carries QUIC and WireGuard, is not counted here.",
  programs: [
    iface("caddy", 410_000, 2_480_000, [
      {
        address: "203.0.113.77",
        port: 51822,
        connections: 4,
        rxBytes: 1_900_000,
        txBytes: 91_000_000,
      },
      {
        address: "198.51.100.41",
        port: 40118,
        connections: 2,
        rxBytes: 640_000,
        txBytes: 38_200_000,
      },
      { address: "192.0.2.150", port: 60294, connections: 1, rxBytes: 120_000, txBytes: 9_400_000 },
      { address: "10.0.4.3", port: 3000, connections: 5, rxBytes: 22_000_000, txBytes: 4_100_000 },
      { address: "10.0.4.4", port: 5678, connections: 2, rxBytes: 3_300_000, txBytes: 800_000 },
    ]),
    iface("dockerd", 1_320_000, 190_000, [
      {
        address: "198.51.100.200",
        port: 443,
        connections: 3,
        rxBytes: 58_000_000,
        txBytes: 1_400_000,
      },
      { address: "203.0.113.9", port: 443, connections: 1, rxBytes: 9_000_000, txBytes: 300_000 },
    ]),
    iface("sshd", 18_000, 410_000, [
      {
        address: "100.110.34.9",
        port: 49812,
        connections: 1,
        rxBytes: 480_000,
        txBytes: 12_000_000,
      },
    ]),
    iface("postgres", 142_000, 310_000, [
      {
        address: "10.0.0.4",
        port: 41120,
        connections: 6,
        rxBytes: 11_000_000,
        txBytes: 29_000_000,
      },
      { address: "10.0.1.2", port: 38814, connections: 2, rxBytes: 1_100_000, txBytes: 3_200_000 },
    ]),
    iface("node", 96_000, 142_000, [
      {
        address: "198.51.100.88",
        port: 443,
        connections: 4,
        rxBytes: 4_800_000,
        txBytes: 2_100_000,
      },
      { address: "203.0.113.61", port: 443, connections: 2, rxBytes: 900_000, txBytes: 600_000 },
    ]),
    iface("tailscaled", 22_000, 31_000, [
      { address: "192.0.2.77", port: 443, connections: 2, rxBytes: 400_000, txBytes: 380_000 },
    ]),
  ],
}

export const processTrafficWarming = {
  ...processTraffic,
  warming: true,
  programs: [],
}

const container = (
  name: string,
  rxRate: number,
  txRate: number,
  phase: number,
  lastSeenMinutes = 0,
) => {
  const series = Array.from({ length: 60 }, (_, i) => {
    const wave = 0.7 + 0.25 * Math.sin(i / 7 + phase) + 0.12 * Math.sin(i / 2.3 + phase * 2)
    return {
      t: now - (59 - i) * 60,
      rx: Math.round(rxRate * wave * (i > 38 && i < 44 ? 2.2 : 1)),
      tx: Math.round(txRate * wave),
    }
  })
  return {
    name,
    rxBytes: series.reduce((n, p) => n + p.rx * 60, 0),
    txBytes: series.reduce((n, p) => n + p.tx * 60, 0),
    rxRate,
    txRate,
    lastSeen: iso(lastSeenMinutes),
    series,
  }
}

export const containerTraffic = {
  windowSeconds: 3600,
  recording: true,
  containers: [
    container("caddy", 1_100_000, 2_100_000, 0.4),
    container("postgres", 120_000, 340_000, 1.9),
    container("n8n", 96_000, 54_000, 3.1),
    container("adguardhome", 41_000, 36_000, 2.2),
    container("uptime-kuma", 8_400, 22_000, 0.9),
  ],
}

const program = (
  id: number,
  type: string,
  name: string | undefined,
  minutesAgo: number,
  extra: Record<string, unknown> = {},
) => ({
  id,
  type,
  name,
  tag: id.toString(16).padStart(8, "0") + "c1e04a7b",
  loadedAt: iso(minutesAgo),
  uid: 0,
  bytesXlated: 400 + id * 3,
  bytesJited: 260 + id * 2,
  memlock: 4096 * (1 + (id % 3)),
  mapIds: [id + 20],
  ...extra,
})

export const ebpf = {
  installed: true,
  bpfStatsEnabled: true,
  total: 10,
  programs: [
    program(41, "cgroup_skb", "sd_fw_ingress", 60 * 26, {
      runCount: 982_410,
      runTimeNs: 211_000_000,
      pinned: ["/sys/fs/bpf/sd_fw_ingress"],
      owners: ["systemd"],
    }),
    program(42, "cgroup_skb", "sd_fw_egress", 60 * 26, {
      runCount: 774_001,
      runTimeNs: 168_000_000,
      owners: ["systemd"],
    }),
    program(43, "cgroup_skb", "sd_fw_ingress", 60 * 26, { runCount: 4_081, runTimeNs: 902_000 }),
    program(44, "cgroup_skb", "sd_fw_egress", 60 * 26, { runCount: 3_902, runTimeNs: 871_000 }),
    program(45, "cgroup_device", undefined, 60 * 26, { runCount: 218, runTimeNs: 51_000 }),
    program(46, "cgroup_device", undefined, 60 * 26, { runCount: 17, runTimeNs: 4_000 }),
    program(77, "tracing", "dump_bpf_map", 60 * 26, { runCount: 0, runTimeNs: 0 }),
    program(78, "tracing", "dump_bpf_prog", 60 * 26, { runCount: 0, runTimeNs: 0 }),
    program(91, "xdp", "xdp_drop_bad", 180, {
      runCount: 31_104_881,
      runTimeNs: 4_810_000_000,
      pinned: ["/sys/fs/bpf/xdp_drop_bad"],
      owners: ["xdp-loader"],
    }),
    program(104, "sched_cls", "cls_ingress", 95, { runCount: 481_220, runTimeNs: 88_000_000 }),
  ],
  byType: [
    { type: "cgroup_skb", count: 4 },
    { type: "cgroup_device", count: 2 },
    { type: "tracing", count: 2 },
    { type: "xdp", count: 1 },
    { type: "sched_cls", count: 1 },
  ],
  attachments: [
    {
      device: "ens3",
      ifindex: 2,
      kind: "xdp",
      programId: 91,
      name: "xdp_drop_bad",
      mode: "driver",
    },
    {
      device: "docker0",
      ifindex: 5,
      kind: "tc ingress",
      programId: 104,
      name: "cls_ingress",
    },
  ],
}

export const ebpfMissing = {
  installed: false,
  package: "bpftool",
  bpfStatsEnabled: false,
  total: 0,
  programs: [],
  byType: [],
  attachments: [],
}

const stat = (kind: string, bytes: number, drops: number, overlimits: number, backlog = 0) => ({
  kind,
  bytes,
  packets: Math.round(bytes / 900),
  drops,
  overlimits,
  requeues: 0,
  backlog,
})

const none = {
  qdisc: "",
  egressKbit: 0,
  ingressKbit: 0,
  managed: false,
  ingress: false,
  uplink: false,
  clientPath: false,
  shapeable: true,
  guard: "",
}

export const shaping = {
  qdiscs: ["fq_codel", "cake", "fq"],
  bbr: {
    available: true,
    active: false,
    congestion: "cubic",
    algorithms: ["reno", "cubic", "bbr"],
    defaultQdisc: "fq_codel",
    managed: false,
  },
  devices: [
    {
      ...none,
      name: "ens3",
      kind: "physical",
      root: stat("fq_codel", 184_000_000_000, 41, 0),
      leaf: null,
      uplink: true,
    },
    {
      ...none,
      name: "tailscale0",
      kind: "tun",
      root: stat("fq_codel", 9_100_000_000, 2, 0),
      leaf: null,
      clientPath: true,
    },
    {
      ...none,
      name: "wg0",
      kind: "wireguard",
      root: stat("cake", 612_000_000, 118, 5_204, 2_840),
      leaf: null,
      qdisc: "cake",
      egressKbit: 50_000,
      managed: true,
      ingress: false,
    },
    {
      ...none,
      name: "docker0",
      kind: "bridge",
      root: stat("noqueue", 0, 0, 0),
      leaf: null,
    },
    {
      ...none,
      name: "veth6e4f828",
      kind: "veth",
      root: stat("noqueue", 0, 0, 0),
      leaf: null,
      shapeable: false,
      guard:
        "Shape the interface this one leads to; a loopback or one end of a veth pair carries no traffic of its own to limit.",
    },
  ],
}

/** Fifteen minutes to seven days of recorded samples for every device that carries traffic. */
const history = () => {
  const interfaces: Record<
    string,
    {
      t: number
      rx: number
      tx: number
      rxPeak: number
      txPeak: number
      errors: number
      dropped: number
    }[]
  > = {}
  for (const link of links) {
    if (link.kind === "veth" && !link.container) continue
    interfaces[link.name] = Array.from({ length: 240 }, (_, i) => {
      const wave = 0.6 + 0.3 * Math.sin(i / 17 + (link.index as number)) + 0.1 * Math.sin(i / 3)
      const rx = Math.round(link.rxRate * wave)
      const tx = Math.round(link.txRate * wave)
      return {
        t: now - (239 - i) * 15,
        rx,
        tx,
        rxPeak: Math.round(rx * 1.7),
        txPeak: Math.round(tx * 1.7),
        errors: 0,
        dropped: 0,
      }
    })
  }
  return { from: now - 3600, to: now, stepSeconds: 15, interfaces, recording: true }
}

/** GET answers keyed by API path, for `mockNetwork`'s `overrides`. */
export const overrides: Record<string, unknown> = {
  "/network/dns/": dnsView,
  "/network/dns/hosts": hostRecords,
  "/network/traffic/history": history(),
  "/network/traffic/processes": processTraffic,
  "/network/traffic/containers": containerTraffic,
  "/network/ebpf": ebpf,
  "/network/shaping": shaping,
}

/**
 * The writes these pages make, answered as the server answers them and
 * recorded in the list the test reads back. Registered after `mockNetwork`, so
 * it sees a request first and hands every other one on. `refuse` answers a
 * path with an error instead: `{ path, status, code, message }`.
 */
export type Refusal = { path: RegExp; status: number; code: string; message: string }

export async function mockNetworkWrites(
  page: Page,
  mutations: Mutation[],
  options: { refuse?: Refusal; lookup?: unknown } = {},
) {
  await page.route(/\/api\/v1\/network\/(dns|shaping)(\/|$)/, async (route) => {
    const request = route.request()
    if (request.method() === "GET") return route.fallback()
    const path = new URL(request.url()).pathname.replace(/^\/api\/v1/, "")
    const body = request.postData() ? request.postDataJSON() : undefined
    mutations.push({ method: request.method(), path, body })
    const refuse = options.refuse
    if (refuse?.path.test(path)) {
      return json(route, { error: { code: refuse.code, message: refuse.message } }, refuse.status)
    }
    if (path === "/network/dns/lookup") return json(route, options.lookup ?? lookup)
    if (path.startsWith("/network/dns/hosts")) return json(route, hostRecords)
    if (path.startsWith("/network/dns")) return json(route, applied)
    if (path === "/network/shaping/bbr") {
      return json(route, { ...shaping.bbr, active: Boolean((body as { on?: boolean }).on) })
    }
    return json(route, { output: "ok" })
  })
}
