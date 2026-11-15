/**
 * Data for the three Network evidence pages — External checks, Address
 * planning and Socket history — in their full state. The pages' own specs
 * narrow it, and the captures in a pull request draw it. Documentation
 * addresses only (RFC 5737, RFC 3849, RFC 4193).
 */
import type { ExternalCheck, ExternalVantage } from "../../src/lib/network-external"
import type { IPAMView } from "../../src/lib/network-ipam"
import type { FlowReport, FlowRow } from "../../src/lib/network-flows"

const minutesAgo = (n: number) => new Date(Date.now() - n * 60_000).toISOString()

export const vantage: ExternalVantage = {
  id: "a".repeat(32),
  name: "Controlled source A",
  location: "Declared region A",
  placement: "controlled_fixture",
  scopes: [
    {
      id: "service",
      target: "service.example",
      addresses: ["192.0.2.8", "2001:db8::8"],
      ports: [443],
      families: ["inet", "inet6"],
    },
  ],
  createdAt: "2026-10-08T10:00:00Z",
  enrolledAt: "2026-10-08T10:01:00Z",
  lastSeen: minutesAgo(3),
}

export const vantages: ExternalVantage[] = [
  vantage,
  {
    ...vantage,
    id: "b".repeat(32),
    name: "Controlled source B",
    location: "Declared region B",
    placement: "external_host",
    lastSeen: minutesAgo(1),
    lastIp: "198.51.100.20",
    scopes: [{ ...vantage.scopes[0], families: ["inet"] }],
  },
  {
    ...vantage,
    id: "c".repeat(32),
    name: "Lab probe",
    location: "",
    enrolledAt: undefined,
    lastSeen: undefined,
    scopes: [
      {
        id: "service",
        target: "mail.example",
        addresses: ["192.0.2.25"],
        ports: [25, 587],
        families: ["inet"],
      },
    ],
  },
]

export const queued = (
  id: string,
  family: "inet" | "inet6" = "inet",
  source = vantage.id,
  ago = 13,
): ExternalCheck => ({
  id,
  vantageId: source,
  request: { vantageId: source, scopeId: "service", family, port: 443, tls: true },
  status: "queued",
  createdAt: minutesAgo(ago + 1),
  expiresAt: minutesAgo(ago - 1),
  startedBy: "operator",
})

export function measured(
  id: string,
  family: "inet" | "inet6",
  source = vantage.id,
  tcp: "connected" | "refused" = "connected",
  ago = 13,
): ExternalCheck {
  const row = queued(id, family, source, ago)
  const address = family === "inet" ? "192.0.2.8" : "2001:db8::8"
  const states = ["resolved", tcp, tcp === "connected" ? "verified" : "skipped"]
  return {
    ...row,
    status: "completed",
    completedAt: minutesAgo(ago),
    result: {
      checkId: id,
      vantageId: source,
      request: row.request,
      target: "service.example",
      addresses: [address],
      address,
      sourceAddress: family === "inet" ? "192.0.2.3" : "2001:db8::3",
      startedAt: minutesAgo(ago + 1),
      endedAt: minutesAgo(ago),
      stages: ["dns", "tcp", "tls"].map((name, i) => ({
        name: name as "dns" | "tcp" | "tls",
        basis: i === 2 && tcp === "refused" ? "unknown" : "measured",
        state: states[i],
        startedAt: "2026-10-08T10:03:01Z",
        endedAt: "2026-10-08T10:03:02Z",
        durationMs: 50,
        detail: "Signed fixture measurement from this source at this time",
      })),
      limitations: [
        "Operator-declared placement remains unverified; this fixture is not off-host acceptance.",
      ],
    },
  }
}

export const checks: ExternalCheck[] = [
  measured("v4", "inet", vantage.id, "connected", 4),
  measured("v6", "inet6", vantage.id, "connected", 9),
  measured("b4", "inet", vantages[1].id, "refused", 21),
  { ...queued("old", "inet", vantages[1].id, 95), status: "expired" },
]

const now = new Date().toISOString()
export const poolId = "a".repeat(32)

export const ipamView: IPAMView = {
  pools: [
    {
      id: poolId,
      name: "Shared IPv6",
      prefix: "fd48:abcd::/48",
      family: "inet6",
      allocationBits: 64,
      createdAt: now,
    },
    {
      id: "d".repeat(32),
      name: "Container networks",
      prefix: "10.240.0.0/16",
      family: "inet",
      allocationBits: 24,
      createdAt: now,
    },
    {
      id: "e".repeat(32),
      name: "Old lab",
      prefix: "10.250.0.0/20",
      family: "inet",
      allocationBits: 24,
      createdAt: now,
      retiredAt: now,
    },
  ],
  reservations: [
    {
      id: "b".repeat(32),
      poolId,
      prefix: "fd48:abcd::/64",
      family: "inet6",
      owner: "docker_network",
      resource: "app-private",
      state: "review_required",
      acknowledgedUnknown: true,
      unknownSources: ["provider:unknown"],
      createdAt: now,
      updatedAt: now,
      startedBy: "operator",
      detail: "Native response unavailable; allocation remains held",
    },
    {
      id: "c".repeat(32),
      poolId: "d".repeat(32),
      prefix: "10.240.7.0/24",
      family: "inet",
      owner: "wireguard_server",
      resource: "wg-office",
      state: "reserved",
      acknowledgedUnknown: false,
      unknownSources: [],
      createdAt: now,
      updatedAt: now,
      startedBy: "operator",
    },
    {
      id: "f".repeat(32),
      poolId: "d".repeat(32),
      prefix: "10.240.9.0/24",
      family: "inet",
      owner: "docker_network",
      resource: "shop-backend",
      state: "observed",
      acknowledgedUnknown: false,
      unknownSources: [],
      createdAt: now,
      updatedAt: now,
      startedBy: "operator",
      nativeId: "9f2c" + "0".repeat(60),
    },
  ],
  inventory: {
    checkedAt: now,
    finishedAt: now,
    observations: [
      {
        prefix: "10.240.1.0/24",
        owner: "docker",
        resource: "web",
        domain: "docker_ipam",
        basis: "observed_native",
      },
      {
        prefix: "10.240.2.0/24",
        owner: "tailscale_approved",
        resource: "this host",
        domain: "tailscale",
        basis: "observed_native",
      },
    ],
    coverage: [
      {
        source: "docker",
        state: "observed",
        detail: "Docker networks were read",
        checkedAt: now,
      },
      {
        source: "provider",
        state: "unknown",
        detail: "Provider allocations are not authoritative from this host",
        checkedAt: now,
      },
    ],
  },
  utilization: [
    {
      poolId,
      totalAddresses: "1208925819614629174706176",
      totalBlocks: "65536",
      reservedBlocks: "1",
      observedBlocks: "0",
      unavailableBlocks: "1",
      candidateBlocks: "65535",
      coverage: "unknown",
    },
    {
      poolId: "d".repeat(32),
      totalAddresses: "65536",
      totalBlocks: "256",
      reservedBlocks: "38",
      observedBlocks: "61",
      unavailableBlocks: "92",
      candidateBlocks: "164",
      coverage: "observed",
    },
  ],
  limitations: ["Reservations are planning evidence; native ownership remains unchanged."],
}

export const ipamEmpty: IPAMView = {
  pools: [],
  reservations: [],
  utilization: [],
  limitations: [],
  inventory: { checkedAt: now, finishedAt: now, observations: [], coverage: [] },
}

/** The day the page opens on (yesterday, UTC) and a reading taken the day after. */
export const day = new Date(Date.now() - 86_400_000).toISOString().slice(0, 10)
export const stamp = `${new Date().toISOString().slice(0, 10)}T00:20:00Z`
const at = (clock: string) => `${day}T${clock}Z`
export const containerId = "a".repeat(64)

export const flowRow: FlowRow = {
  id: "native-cookie-owner",
  hour: at("12:00:00"),
  firstSeen: at("12:00:00"),
  lastSeen: at("12:00:30"),
  sourceId: containerId,
  sourceName: "fixture",
  namespace: "4:123",
  bootId: "fixture-boot",
  socket: {
    protocol: "tcp",
    state: "ESTAB",
    localAddress: "172.18.0.2",
    localEndpoint: "172.18.0.2:1234",
    localPort: 1234,
    remoteAddress: "192.0.2.1",
    remoteEndpoint: "192.0.2.1:443",
    remotePort: 443,
    cookie: "abcd",
    inode: "999",
    owner: {
      status: "verified_container",
      containerId,
      containerName: "worker",
      containerStartedAt: at("01:00:00"),
    },
  },
  samples: 2,
  txBytes: "131072",
  rxBytes: "4096",
  retransmissions: null,
  lostGaugeMax: null,
  measuredIntervals: 1,
  txIntervals: 1,
  rxIntervals: 1,
  retransIntervals: 0,
  skippedIntervals: 1,
}

export const udpRow: FlowRow = {
  ...flowRow,
  id: "udp",
  socket: {
    ...flowRow.socket,
    protocol: "udp",
    state: "UNCONN",
    remoteEndpoint: "192.0.2.53:53",
    remoteAddress: "192.0.2.53",
    remotePort: 53,
    owner: { status: "verified_process", program: "resolved", pid: 412 },
  },
  txBytes: null,
  rxBytes: null,
  measuredIntervals: 0,
  txIntervals: 0,
  rxIntervals: 0,
}

export const flowReport: FlowReport = {
  checkedAt: stamp,
  from: at("00:00:00"),
  to: `${stamp.slice(0, 10)}T00:00:00Z`,
  status: "off",
  settings: { enabled: false, intervalSeconds: 30, retentionDays: 7 },
  recordingSince: null,
  collectorStartedAt: stamp,
  lastCycle: null,
  rows: [],
  coverageHours: [],
  truncated: false,
  coverageTruncated: false,
  retainedRows: 0,
  retainedBytes: 0,
  retainedFrom: null,
  prunedRows: 0,
  coverage: ["Native TCP snapshots; first sight and restart do not contribute lifetime bytes."],
  kernelObserver: {
    status: "unavailable",
    reason: "UDP bytes, short-lived flows and dropped-event counts remain unknown.",
  },
}

const cycle = {
  at: at("12:00:00"),
  finishedAt: at("12:00:01"),
  bootId: "fixture-boot",
  kernelRelease: "6.8.0-fixture",
  tool: "ss",
  toolVersion: "ss utility, iproute2-6.1.0",
  toolVersionCheckedAt: stamp,
  sources: [
    {
      id: "host",
      name: "This server",
      namespace: "4:123",
      status: "observed",
      sockets: 14,
      tcp: 11,
      udp: 3,
      tcpTxCounters: 11,
      tcpRxCounters: 11,
      unverifiedOwners: 1,
      truncated: false,
      malformed: 0,
      identityUnavailable: 0,
      unconnectedUdp: 2,
      observedAt: at("12:00:00"),
    },
  ],
  dockerStatus: "observed",
  omittedSources: 0,
  discardedIntervals: 0,
  elapsedMillis: 41,
  droppedEvents: null,
}

const hours = [0, 1, 2, 3, 9, 10, 11, 12].map((h) => {
  const hour = at(`${String(h).padStart(2, "0")}:00:00`)
  return {
    hour,
    firstSampleAt: hour,
    lastSampleAt: hour.replace(":00:00Z", ":59:30Z"),
    samples: 120,
    failedSources: h === 3 ? 1 : 0,
    truncatedSources: 0,
    omittedSources: 0,
    discardedIntervals: h === 10 ? 2 : 0,
    captureMillis: 4000,
    maxCaptureMillis: 60,
    lastCycle: cycle,
  }
})

export const recordingReport: FlowReport = {
  ...flowReport,
  status: "recording",
  settings: { enabled: true, kernelObserverEnabled: false, intervalSeconds: 30, retentionDays: 7 },
  recordingSince: `${new Date(Date.now() - 3 * 86_400_000).toISOString().slice(0, 10)}T08:00:00Z`,
  lastCycle: cycle,
  rows: [flowRow, udpRow],
  coverageHours: hours,
  retainedRows: 2,
  retainedBytes: 2048,
  retainedFrom: `${new Date(Date.now() - 3 * 86_400_000).toISOString().slice(0, 10)}T08:00:00Z`,
  kernelObserver: {
    status: "off",
    reason: "Exact helper support is checked only on explicit attachment.",
    checkedAt: stamp,
    attachmentsRetained: false,
  },
}
