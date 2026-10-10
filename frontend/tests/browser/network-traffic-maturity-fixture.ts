import type { Page } from "@playwright/test"
import {
  connections,
  firewall,
  json,
  liveSeries,
  mockNetwork,
  type Mutation,
} from "./network-fixture"

// Recorded API shapes for the Traffic and Connections maturity work: live
// context, explicit ranges with percentiles and incidents, transfer budgets,
// program and container attribution, queue ownership and upload profiles,
// the congestion comparison, eBPF detail, connection tuples, blocks with an
// end, and the phases after an install. Addresses are documentation ranges.

export const nowSeconds = () => Math.floor(Date.now() / 1000)
const iso = (secondsAgo: number) => new Date(Date.now() - secondsAgo * 1000).toISOString()

/** The live answer with packets, faults, the TCP ring and latency, newest `ageSeconds` old. */
export function liveWithContext(ageSeconds = 1) {
  const now = nowSeconds()
  const sampledAt = now - ageSeconds
  const series: Record<
    string,
    { t: number; rx: number; tx: number; rxp: number; txp: number; err?: number; drop?: number }[]
  > = {}
  for (const [name, points] of Object.entries(liveSeries(sampledAt))) {
    series[name] = points.map((p, i) => ({
      ...p,
      rxp: Math.round(p.rx / 900),
      txp: Math.round(p.tx / 1100),
      ...(name === "ens3" && i === points.length - 5 ? { drop: 3 } : {}),
    }))
  }
  const tcp = Array.from({ length: 60 }, (_, i) => ({
    t: sampledAt - (59 - i) * 2,
    outSegs: 1000,
    retrans: 30,
    opens: 12,
    failed: 0,
    resets: i === 50 ? 1 : 0,
    established: 214,
  }))
  return {
    now,
    sampledAt,
    stepSeconds: 2,
    series,
    tcp,
    latency: {
      at: iso(3),
      sockets: 37,
      medianMs: 23.4,
      p90Ms: 118,
      maxMs: 412,
      retransmitting: 2,
      loopback: 61,
      truncated: false,
    },
  }
}

export function historyWithPercentiles(from: number, to: number) {
  const points = Array.from({ length: 60 }, (_, i) => {
    const t = from + Math.round(((to - from) / 60) * i)
    return {
      t,
      rx: 900_000 + (i % 7) * 40_000,
      tx: 300_000 + (i % 5) * 20_000,
      rxPeak: 2_400_000,
      txPeak: 900_000,
      errors: 0,
      dropped: 0,
    }
  })
  const pct = {
    samples: 240,
    basisSeconds: 15,
    rxP50: 1_000_000,
    rxP95: 2_100_000,
    rxP99: 2_400_000,
    txP50: 320_000,
    txP95: 800_000,
    txP99: 880_000,
  }
  return {
    from,
    to,
    stepSeconds: 60,
    recording: true,
    retainedFrom: nowSeconds() - 7 * 86400,
    interfaces: { ens3: points, wg0: points, tailscale0: points },
    percentiles: { ens3: pct, wg0: pct, tailscale0: pct },
  }
}

export const annotations = (from: number) => [
  {
    ts: new Date((from + 600) * 1000).toISOString(),
    kind: "action",
    title: "network.shaping.set ens3",
    detail: "by operator",
    severity: "info",
    source: "change",
  },
  {
    ts: new Date((from + 1200) * 1000).toISOString(),
    kind: "action",
    title: "Incident: Uplink loss at night",
    detail: "saved diagnostic, completed",
    severity: "warning",
    source: "incident",
    ref: "0123456789abcdef0123456789abcdef",
  },
]

export const quotas = [
  {
    iface: "ens3",
    period: "month",
    direction: "both",
    limitBytes: 1024 * 2 ** 30,
    periodStart: nowSeconds() - 8 * 86400,
    usedBytes: 790 * 2 ** 30,
    estimatedBytes: 40 * 2 ** 30,
    coverage: 0.962,
    retentionShort: false,
    state: "warning",
    enforced: false,
    createdBy: "operator",
    createdAt: nowSeconds() - 20 * 86400,
  },
  {
    iface: "wg0",
    period: "day",
    direction: "rx",
    limitBytes: 5 * 2 ** 30,
    periodStart: nowSeconds() - 3600,
    usedBytes: 0,
    estimatedBytes: 0,
    coverage: 0,
    retentionShort: false,
    state: "unmeasured",
    enforced: false,
    createdAt: nowSeconds() - 86400,
  },
]

export const processes = {
  at: iso(1),
  intervalSeconds: 3,
  warming: false,
  truncated: false,
  missedOpens: 14,
  closed: 3,
  note: "Rates are TCP's per-socket counters; UDP sockets are counted and named without bytes, which the kernel does not keep per socket",
  history: { recording: false, kernelObserver: false, retentionDays: 7 },
  programs: [
    {
      name: "caddy",
      pids: [4410],
      connections: 18,
      rxRate: 1_200_000,
      txRate: 9_400_000,
      rxTotal: 3_000_000_000,
      txTotal: 41_000_000_000,
      udpConnected: 0,
      udpUnconnected: 2,
      medianRttMs: 31.2,
      retransmitted: 120,
      segmentsOut: 40_000,
      peers: [
        {
          address: "198.51.100.23",
          port: 51022,
          protocol: "tcp",
          bytesKnown: true,
          connections: 14,
          rxBytes: 90_000_000,
          txBytes: 2_100_000_000,
        },
        {
          address: "203.0.113.77",
          port: 40110,
          protocol: "tcp",
          bytesKnown: true,
          connections: 4,
          rxBytes: 1_000_000,
          txBytes: 48_000_000,
        },
      ],
    },
    {
      name: "agent-cli",
      pids: [50969],
      connections: 2,
      rxRate: 40_000,
      txRate: 12_000,
      rxTotal: 9_000_000,
      txTotal: 3_000_000,
      udpConnected: 1,
      udpUnconnected: 0,
      medianRttMs: 18,
      retransmitted: 0,
      segmentsOut: 900,
      peers: [
        {
          address: "198.51.100.20",
          port: 443,
          protocol: "tcp",
          bytesKnown: true,
          connections: 2,
          rxBytes: 9_000_000,
          txBytes: 3_000_000,
        },
        {
          address: "198.51.100.20",
          port: 443,
          protocol: "udp",
          bytesKnown: false,
          connections: 1,
          rxBytes: 0,
          txBytes: 0,
        },
      ],
    },
  ],
}

const sparkline = (scale: number) =>
  Array.from({ length: 30 }, (_, i) => ({
    t: nowSeconds() - (30 - i) * 120,
    rx: scale * (1 + (i % 4)),
    tx: scale * (0.5 + (i % 3)),
  }))

export const containerTraffic = {
  windowSeconds: 3600,
  recording: true,
  containers: ["caddy", "postgres", "grafana", "redis", "api", "worker", "registry"].map(
    (name, i) => ({
      name,
      rxBytes: (7 - i) * 200_000_000,
      txBytes: (7 - i) * 90_000_000,
      rxRate: (7 - i) * 50_000,
      txRate: (7 - i) * 20_000,
      lastSeen: nowSeconds() - 15,
      series: sparkline((7 - i) * 10_000),
    }),
  ),
}

export const registryDetail = {
  name: "registry",
  rxBytes: 200_000_000,
  txBytes: 90_000_000,
  rxRate: 50_000,
  txRate: 20_000,
  lastSeen: nowSeconds() - 15,
  windowSeconds: 3600,
  series: sparkline(10_000),
  id: "a".repeat(64),
  image: "registry:2",
  state: "running",
  project: "infra",
  service: "registry",
  networks: ["infra_default"],
}

const flowRow = (
  address: string,
  port: number,
  owner: string,
  tx: string | null,
  rx: string | null,
  protocol = "tcp",
) => ({
  id: `${address}-${port}-${protocol}`,
  hour: iso(1800),
  firstSeen: iso(3000),
  lastSeen: iso(600),
  sourceId: "host",
  sourceName: "host",
  namespace: "",
  bootId: "b",
  socket: {
    protocol,
    state: "ESTAB",
    localAddress: "203.0.113.10",
    localEndpoint: `203.0.113.10:${port}`,
    localPort: port,
    remoteAddress: address,
    remoteEndpoint: `${address}:443`,
    remotePort: 443,
    owner: { status: "verified_process", program: owner, pid: 4410 },
  },
  samples: 12,
  txBytes: tx,
  rxBytes: rx,
  retransmissions: "0",
  lostGaugeMax: "0",
  measuredIntervals: 10,
  txIntervals: 10,
  rxIntervals: 10,
  retransIntervals: 10,
  skippedIntervals: 0,
})

export function flowReport(rows: ReturnType<typeof flowRow>[], enabled = true) {
  return {
    checkedAt: iso(0),
    from: iso(3600),
    to: iso(0),
    status: enabled ? "recording" : "off",
    settings: { enabled, kernelObserverEnabled: false, intervalSeconds: 30, retentionDays: 7 },
    recordingSince: enabled ? iso(86400 * 3) : null,
    collectorStartedAt: iso(86400),
    lastCycle: null,
    rows,
    coverageHours: [],
    truncated: false,
    coverageTruncated: false,
    retainedRows: rows.length,
    retainedBytes: 4096,
    retainedFrom: iso(86400 * 3),
    prunedRows: 0,
    coverage: [],
    kernelObserver: { status: "off", reason: "" },
  }
}

export const registryFlows = flowReport([
  flowRow("198.51.100.99", 5000, "registry", "50000000", "1200000"),
  flowRow("198.51.100.99", 5000, "registry", "1000", null),
  flowRow("192.0.2.53", 5000, "registry", null, null, "udp"),
])

export const pastHourFlows = flowReport([
  flowRow("198.51.100.23", 443, "caddy", "2100000000", "90000000"),
  flowRow("198.51.100.23", 443, "caddy", "1000", "2000"),
  flowRow("203.0.113.200", 22, "sshd", "40000", "9000"),
])

const tins = [
  {
    name: "Bulk",
    thresholdBytesPerSecond: 156250,
    peakDelayUs: 41000,
    avgDelayUs: 6200,
    baseDelayUs: 300,
    sentPackets: 1200,
    sentBytes: 1_700_000,
    drops: 4,
    ecnMarks: 9,
    sparseFlows: 0,
    bulkFlows: 1,
  },
  {
    name: "Best effort",
    thresholdBytesPerSecond: 2500000,
    peakDelayUs: 9100,
    avgDelayUs: 1400,
    baseDelayUs: 120,
    sentPackets: 90000,
    sentBytes: 120_000_000,
    drops: 31,
    ecnMarks: 12,
    sparseFlows: 2,
    bulkFlows: 3,
  },
  {
    name: "Video",
    thresholdBytesPerSecond: 1250000,
    peakDelayUs: 3000,
    avgDelayUs: 600,
    baseDelayUs: 80,
    sentPackets: 300,
    sentBytes: 400_000,
    drops: 0,
    ecnMarks: 0,
    sparseFlows: 1,
    bulkFlows: 0,
  },
  {
    name: "Voice",
    thresholdBytesPerSecond: 625000,
    peakDelayUs: 900,
    avgDelayUs: 200,
    baseDelayUs: 40,
    sentPackets: 800,
    sentBytes: 160_000,
    drops: 0,
    ecnMarks: 0,
    sparseFlows: 4,
    bulkFlows: 0,
  },
]

const fqCodel = {
  limit: "10240",
  flows: "1024",
  quantum: "1514",
  target: "4999",
  interval: "99999",
  memory_limit: "33554432",
  ecn: "true",
  drop_batch: "64",
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
      name: "ens3",
      kind: "physical",
      root: {
        kind: "cake",
        bytes: 9e9,
        packets: 7e6,
        drops: 35,
        overlimits: 900,
        requeues: 0,
        backlog: 3000,
        tins,
      },
      leaf: null,
      ingress: false,
      managed: true,
      qdisc: "cake",
      egressKbit: 18000,
      ingressKbit: 0,
      uplink: true,
      clientPath: false,
      shapeable: true,
      guard: "",
      verification: {
        status: "drift",
        checkedAt: iso(5),
        reason: "changed outside the dashboard since it was applied: rtt 50000 → 100000",
      },
      upload: {
        diffserv: "diffserv4",
        flowMode: "dual-srchost",
        nat: false,
        wash: false,
        ackFilter: false,
        overhead: 34,
        mpu: 64,
        linkLayer: "ptm",
        rttMillis: 50,
      },
      tree: [{ kind: "cake", handle: "8005:", root: true }],
      ownership: { verdict: "managed", reason: "Made here." },
      effective: {
        bandwidth: "2250000",
        diffserv: "diffserv4",
        flowmode: "dual-srchost",
        rtt: "100000",
        overhead: "34",
      },
      applied: { kind: "cake", handle: "8005:", options: { rtt: "50000" }, appliedAt: iso(86400) },
    },
    {
      name: "tailscale0",
      kind: "tun",
      root: {
        kind: "fq_codel",
        bytes: 1e9,
        packets: 9e5,
        drops: 0,
        overlimits: 0,
        requeues: 0,
        backlog: 0,
      },
      leaf: null,
      ingress: false,
      managed: false,
      qdisc: "",
      egressKbit: 0,
      ingressKbit: 0,
      uplink: false,
      clientPath: true,
      shapeable: true,
      guard: "",
      tree: [{ kind: "fq_codel", handle: "8007:", root: true }],
      ownership: {
        verdict: "preserved",
        reason:
          "The existing fq_codel's parameters are captured before the first change and put back when it is cleared or fails.",
      },
      effective: fqCodel,
    },
    {
      name: "jd-lab",
      kind: "bridge",
      root: {
        kind: "htb",
        bytes: 1e6,
        packets: 900,
        drops: 0,
        overlimits: 0,
        requeues: 0,
        backlog: 0,
      },
      leaf: null,
      ingress: false,
      managed: false,
      qdisc: "",
      egressKbit: 0,
      ingressKbit: 0,
      uplink: false,
      clientPath: false,
      shapeable: true,
      guard: "",
      tree: [
        { kind: "htb", handle: "1:", root: true },
        { kind: "sfq", handle: "10:", parent: "1:10", root: false },
      ],
      ownership: {
        verdict: "refused",
        reason: "jd-lab has an unmanaged queue hierarchy; its owner must remove it before shaping",
      },
      effective: { r2q: "10", default: "0x10" },
    },
    {
      name: "wg0",
      kind: "wireguard",
      root: {
        kind: "noqueue",
        bytes: 0,
        packets: 0,
        drops: 0,
        overlimits: 0,
        requeues: 0,
        backlog: 0,
      },
      leaf: null,
      ingress: false,
      managed: false,
      qdisc: "",
      egressKbit: 0,
      ingressKbit: 0,
      uplink: false,
      clientPath: false,
      shapeable: true,
      guard: "",
      tree: [{ kind: "noqueue", handle: "0:", root: true }],
      ownership: { verdict: "kernel", reason: "The kernel's own default queue." },
    },
  ],
}

export const congestionAfterSwitch = {
  now: {
    at: iso(4),
    default: "bbr",
    groups: [
      {
        algorithm: "bbr",
        sockets: 19,
        medianRttMs: 22,
        p90RttMs: 61,
        retransmitShare: 0.0012,
        medianDeliveryMbit: 41.5,
        segmentsOut: 300_000,
        bytesSent: 900_000_000,
      },
      {
        algorithm: "cubic",
        sockets: 23,
        medianRttMs: 47,
        p90RttMs: 140,
        retransmitShare: 0.0081,
        medianDeliveryMbit: 16.2,
        segmentsOut: 800_000,
        bytesSent: 1_100_000_000,
      },
    ],
    loopback: 61,
    truncated: false,
  },
  snapshots: [
    {
      id: 4,
      at: iso(7200),
      before: "cubic",
      after: "bbr",
      actor: "operator",
      comparison: {
        at: iso(7200),
        default: "cubic",
        groups: [
          {
            algorithm: "cubic",
            sockets: 40,
            medianRttMs: 52,
            p90RttMs: 170,
            retransmitShare: 0.0094,
            medianDeliveryMbit: 15.1,
            segmentsOut: 1_000_000,
            bytesSent: 2_000_000_000,
          },
        ],
        loopback: 58,
        truncated: false,
      },
    },
  ],
  note: "Each group is whatever this host's sockets were doing at the read: different peers, paths and workloads, not a controlled test. A socket keeps the algorithm it opened with.",
}

export const ebpf = {
  installed: true,
  bpfStatsEnabled: false,
  total: 3,
  programs: [
    {
      id: 30,
      type: "cgroup_sysctl",
      name: "sysctl_monitor",
      tag: "78dcac0637f56d1c",
      loadedAt: iso(86400 * 9),
      uid: 998,
      bytesXlated: 1232,
      bytesJited: 781,
      memlock: 4096,
      mapIds: [9, 12],
    },
    {
      id: 4633,
      type: "cgroup_skb",
      name: "sd_fw_egress",
      uid: 0,
      bytesXlated: 64,
      bytesJited: 59,
      memlock: 4096,
      mapIds: [],
    },
    {
      id: 9101,
      type: "cgroup_skb",
      name: "jd_flow_egress",
      uid: 0,
      bytesXlated: 4096,
      bytesJited: 2100,
      memlock: 8192,
      mapIds: [77],
    },
  ],
  byType: [
    { type: "cgroup_skb", count: 2 },
    { type: "cgroup_sysctl", count: 1 },
  ],
  attachments: [],
  platform: {
    kernel: "6.14.0-37-generic",
    jit: "1",
    jitHarden: "0",
    unprivilegedDisabled: "2",
    btf: true,
    bpffs: true,
    statsEnabled: false,
  },
  cgroupAttachments: 41,
  observerProgramIds: [9101],
}

export const ebpfDetail = {
  ...ebpf.programs[2],
  gplCompatible: true,
  jited: true,
  btfId: 120,
  verifiedInsns: 512,
  observer: true,
  maps: [
    {
      id: 77,
      type: "ringbuf",
      name: "jd_events",
      keyBytes: 0,
      valueBytes: 0,
      maxEntries: 262144,
      memlock: 275736,
      frozen: false,
    },
  ],
  devices: [],
  cgroups: [
    {
      cgroup: "/sys/fs/cgroup/system.slice",
      programId: 9101,
      attachType: "cgroup_inet_egress",
      flags: "multi",
    },
  ],
  links: [{ id: 14, type: "cgroup", attachType: "cgroup_inet_egress", cgroupId: 3 }],
  errors: [],
}

/** The summary with what folding keeps and what the read could not see. */
export const connectionsWithQuality = {
  ...connections,
  peers: connections.peers.map((p) =>
    p.address === "198.51.100.23"
      ? {
          ...p,
          protocols: ["tcp", "udp"],
          remotePorts: [51022, 51023, 51024],
          morePorts: 15,
          states: { ESTABLISHED: 14, TIME_WAIT: 3, CONNECTED: 1 },
        }
      : { ...p, protocols: ["tcp"], remotePorts: [40110], states: { ESTABLISHED: p.established } },
  ),
  quality: {
    readAt: iso(0),
    intervalSeconds: 10.2,
    closedSinceLast: 5,
    unconnectedUdp: 7,
    limits: [
      "A snapshot of the kernel's socket table: connections that opened and closed between two reads are not in it.",
      "A close is noticed as a tuple missing from the next read; its time is somewhere between the two reads.",
      "Unconnected UDP sockets have no peer to fold by, so their callers are not listed.",
    ],
  },
}

export const peerDetail = {
  address: "198.51.100.23",
  private: false,
  sockets: [
    {
      protocol: "tcp",
      localAddress: "203.0.113.10",
      localPort: 443,
      remoteAddress: "198.51.100.23",
      remotePort: 51022,
      status: "ESTABLISHED",
      pid: 4410,
      process: "caddy",
      firstSeen: iso(5400),
      rxBytes: 90_000_000,
      txBytes: 2_100_000_000,
      rttMs: 31.5,
      retransmitted: 12,
      congestion: "cubic",
    },
    {
      protocol: "udp",
      localAddress: "203.0.113.10",
      localPort: 443,
      remoteAddress: "198.51.100.23",
      remotePort: 51099,
      status: "NONE",
      pid: 4410,
      process: "caddy",
      firstSeen: iso(40),
    },
  ],
  closed: [
    {
      protocol: "tcp",
      localAddress: "203.0.113.10",
      localPort: 443,
      remoteAddress: "198.51.100.23",
      remotePort: 51001,
      process: "caddy",
      firstSeen: iso(900),
      lastSeen: iso(300),
      goneBy: iso(290),
      observedSeconds: 600,
    },
  ],
  quality: connectionsWithQuality.quality,
  countersTruncated: false,
}

export const firewallWithNet = {
  ...firewall,
  rules: [
    ...firewall.rules,
    {
      number: 9,
      action: "deny",
      direction: "in",
      from: "198.51.100.0/24",
      to: "Anywhere",
      port: "22",
      raw: "22 DENY IN 198.51.100.0/24",
    },
  ],
}

export const blocks = [
  {
    id: "b".repeat(32),
    address: "203.0.113.77",
    reason: "credential stuffing on /login",
    comment: "jd-block bbbbbbbbbbbb",
    incidentRunId: "0123456789abcdef0123456789abcdef",
    createdBy: "operator",
    createdAt: iso(3600),
    expiresAt: new Date(Date.now() + 23 * 3600 * 1000).toISOString(),
    state: "active",
    rulePresent: true,
  },
  {
    id: "c".repeat(32),
    address: "192.0.2.145",
    reason: "ssh brute force",
    comment: "jd-block cccccccccccc",
    createdBy: "operator",
    createdAt: iso(86400 * 2),
    expiresAt: iso(86400),
    state: "expired",
    endedAt: iso(86400),
    endedBy: "expiry",
  },
]

/**
 * The Network mocks with this package's shapes, every mutation recorded. Paths
 * with a parameter in them are routed after mockNetwork, so they win.
 */
export async function openMaturity(
  page: Page,
  mutations: Mutation[],
  overrides: Record<string, unknown> = {},
) {
  await mockNetwork(page, mutations, {
    overrides: {
      "/network/traffic/processes": processes,
      "/network/traffic/containers": containerTraffic,
      "/network/traffic/quotas": quotas,
      "/network/shaping": shaping,
      "/network/shaping/congestion": congestionAfterSwitch,
      "/network/ebpf": ebpf,
      "/connections": connectionsWithQuality,
      "/firewall/": firewallWithNet,
      "/firewall/blocks": blocks,
      "/network/routing/lookup": {
        address: "198.51.100.23",
        device: "ens3",
        gateway: "203.0.113.1",
        source: "203.0.113.10",
      },
      "/network/diagnostics/": [
        { id: "0123456789abcdef0123456789abcdef", name: "Uplink loss at night" },
      ],
      ...overrides,
    },
  })
  await page.route("**/api/v1/network/traffic/live**", (route) => json(route, liveWithContext()))
  await page.route("**/api/v1/network/traffic/history**", (route) => {
    const url = new URL(route.request().url())
    const to = Number(url.searchParams.get("to")) || nowSeconds()
    const from = Number(url.searchParams.get("from")) || to - 6 * 3600
    return json(route, historyWithPercentiles(from, to))
  })
  await page.route("**/api/v1/network/traffic/annotations**", (route) => {
    const url = new URL(route.request().url())
    return json(route, annotations(Number(url.searchParams.get("from")) || nowSeconds() - 3600))
  })
  await page.route("**/api/v1/network/traffic/containers/**", (route) =>
    json(route, registryDetail),
  )
  await page.route("**/api/v1/network/ebpf/*", (route) => json(route, ebpfDetail))
  await page.route("**/api/v1/connections/*", (route) => json(route, peerDetail))
  await page.route("**/api/v1/network/flows/**", (route) => {
    const url = new URL(route.request().url())
    if (url.searchParams.get("containerId")) return json(route, registryFlows)
    const address = url.searchParams.get("address")
    if (address) {
      return json(route, {
        ...pastHourFlows,
        rows: pastHourFlows.rows.filter((r) => r.socket.remoteAddress === address),
      })
    }
    return json(route, pastHourFlows)
  })
}
