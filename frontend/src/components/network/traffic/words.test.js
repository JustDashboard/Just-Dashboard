import { expect, test } from "bun:test"
import {
  budgetWords,
  carryingWords,
  ebpfWords,
  limitWords,
  packetWords,
  queueLine,
  queueState,
  tcpWords,
  thresholdWords,
  throughputWords,
} from "./words"

const step = (retrans, resets = 0) => ({
  t: 0,
  outSegs: 1000,
  retrans,
  opens: 0,
  failed: 0,
  resets,
  established: 0,
})

test("what a device carries and its packets are said with their units", () => {
  expect(carryingWords({ rx: 2_600_000, tx: 1_000_000 })).toBe("2.5 MB/s in, 976.6 KB/s out")
  expect(carryingWords(undefined)).toBeUndefined()
  expect(packetWords({ rxp: 3000, txp: 900 })).toEqual({
    reading: "3.9k packets a second",
    hint: "3.0k/s in · 900/s out",
  })
  expect(packetWords({})).toEqual({ hint: "no uplink reading yet" })
})

test("TCP's health is a sentence, and its round trip is in words, never a median", () => {
  const latency = {
    at: new Date().toISOString(),
    sockets: 37,
    medianMs: 23.4,
    p90Ms: 118,
    maxMs: 412,
    retransmitting: 2,
    loopback: 61,
    truncated: false,
  }
  const words = tcpWords({ tcp: Array.from({ length: 30 }, () => step(30)), latency })
  expect(words.reading).toBe("3.0% resent · round trips half inside 23 ms")
  expect(words.warning).toBe(true)
  expect(words.hint).toMatch(/^9 in 10 inside 118 ms · 37 sockets · 0 resets · read /)
  expect(`${words.reading} ${words.hint}`).not.toMatch(/median|p90|RTT/)

  const calm = tcpWords({ tcp: [step(1, 0.5)], latency: { ...latency, sockets: 0 } })
  expect(calm.warning).toBe(false)
  expect(calm.reading).toBe("0.1% resent")
  expect(calm.hint).toBe("no non-loopback TCP socket to time · 1 reset")
  expect(tcpWords({ tcp: [], latency: undefined })).toEqual({
    hint: "round trip being measured",
    warning: false,
  })
  expect(tcpWords({ tcp: [step(1)], tcpError: "permission denied" })).toEqual({
    hint: "TCP counters unreadable: permission denied",
    warning: true,
  })
})

test("a window's throughput spread is said in shares, not percentiles", () => {
  const words = throughputWords({
    samples: 240,
    basisSeconds: 15,
    rxP50: 1_048_576,
    rxP95: 2_097_152,
    rxP99: 2_621_440,
    txP50: 524_288,
    txP95: 1_048_576,
    txP99: 1_572_864,
  })
  expect(words).toEqual([
    "Half of the time under ↓ 1.0 MB/s ↑ 512.0 KB/s",
    "19 in 20 under ↓ 2.0 MB/s ↑ 1.0 MB/s",
    "99 in 100 under ↓ 2.5 MB/s ↑ 1.5 MB/s",
    "over 240 15s intervals",
  ])
  expect(words.join(" ")).not.toMatch(/p50|p95|p99/)
  expect(thresholdWords("In", 2_097_152)).toBe("In: 19 in 20 under 2.0 MB/s")
})

test("a budget says what it measured, and one that is not measured says why", () => {
  const budget = {
    iface: "ens3",
    period: "month",
    direction: "both",
    limitBytes: 1024 * 2 ** 30,
    periodStart: 0,
    usedBytes: 790 * 2 ** 30,
    estimatedBytes: 40 * 2 ** 30,
    coverage: 0.962,
    retentionShort: false,
    state: "warning",
    enforced: false,
    createdAt: 0,
  }
  expect(budgetWords(budget)).toEqual({
    reading: "830.0 GB of 1.0 TB in and out this month",
    hint: "96% of the month so far was recorded · 40.0 GB estimated from older rows",
  })
  expect(
    budgetWords({
      ...budget,
      state: "unmeasured",
      period: "day",
      direction: "rx",
      limitBytes: 5 * 2 ** 30,
      coverage: 0,
      retentionShort: true,
    }),
  ).toEqual({
    reading: "Not measured against 5.0 GB in a day",
    hint: "The metrics retention keeps nothing · 0% of the day so far was recorded · the retention keeps less than the period, so its start is gone",
  })
})

test("a queue's card says its limits and the worst thing about it", () => {
  const device = {
    name: "ens3",
    kind: "physical",
    root: { kind: "cake" },
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
  }
  expect(limitWords(18000)).toBe("18 Mbit/s")
  expect(limitWords(0)).toBe("no limit")
  expect(queueLine(device)).toBe("cake · uplink")
  expect(queueLine({ ...device, root: null, uplink: false, clientPath: true })).toBe(
    "no queue · carries your connection",
  )
  expect(queueState(device)).toEqual({ label: "shaped", tone: "running" })
  expect(queueState({ ...device, verification: { status: "drift", checkedAt: "" } })).toEqual({
    label: "differs from saved limits",
    tone: "warning",
  })
  expect(queueState({ ...device, verification: { status: "verified", checkedAt: "" } }).label).toBe(
    "matches saved limits",
  )
  expect(
    queueState({ ...device, managed: false, ownership: { verdict: "refused", reason: "" } }),
  ).toEqual({ label: "changes refused", tone: "warning" })
  expect(queueState({ ...device, managed: false }).label).toBe("not shaped")
  expect(queueState({ ...device, shapeable: false }).label).toBe("cannot be shaped")
})

test("the eBPF inventory's four figures are one line", () => {
  const view = {
    installed: true,
    bpfStatsEnabled: false,
    total: 3,
    programs: [{ memlock: 4096 }, { memlock: 4096 }, { memlock: 8192 }],
    byType: [
      { type: "cgroup_skb", count: 2 },
      { type: "cgroup_sysctl", count: 1 },
    ],
    attachments: [],
    cgroupAttachments: 41,
  }
  expect(ebpfWords(view)).toBe(
    "3 programs of 2 types · 16.0 KB of locked memory · attached to no network device · 41 cgroup attachments",
  )
  expect(
    ebpfWords({
      ...view,
      attachments: [
        { device: "ens3", kind: "xdp", programId: 1 },
        { device: "ens3", kind: "tc", programId: 2 },
      ],
      cgroupAttachments: undefined,
    }),
  ).toBe(
    "3 programs of 2 types · 16.0 KB of locked memory · 2 attachments on ens3 · the cgroup tree could not be read",
  )
})
