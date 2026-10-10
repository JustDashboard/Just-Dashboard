import { describe, expect, test } from "bun:test"
import {
  addressBytes,
  blockStanding,
  delayLabel,
  foldHistoricalFlows,
  hourBounds,
  localInput,
  localInstant,
  millis,
  observationAge,
  packetRate,
  phaseReading,
  quotaReading,
  rangeProblem,
  recentHours,
  retransmitShare,
  shareLabel,
  sourceCovers,
} from "./network-traffic"

describe("live context", () => {
  test("an observation is stale after three missed steps, and waiting before the first", () => {
    expect(observationAge(undefined, 1000).label).toBe("Waiting for the first reading")
    expect(observationAge(998, 1000)).toEqual({ seconds: 2, stale: false, label: "2s old" })
    expect(observationAge(990, 1000)).toEqual({
      seconds: 10,
      stale: true,
      label: "10s old — stale",
    })
    expect(observationAge(1005, 1000).seconds).toBe(0)
  })
  test("the resent share is over what was sent, and unknown where nothing was", () => {
    const pts = [
      { t: 1, outSegs: 100, retrans: 1 },
      { t: 2, outSegs: 300, retrans: 3 },
      { t: 3, outSegs: 0, retrans: 0 },
    ]
    expect(retransmitShare(pts)).toBe(0.01)
    expect(retransmitShare(pts, 1)).toBeUndefined()
    expect(retransmitShare(undefined)).toBeUndefined()
    expect(shareLabel(0.0123)).toBe("1.2%")
    expect(shareLabel(0.0004)).toBe("under 0.1%")
    expect(shareLabel(0)).toBe("0.0%")
    expect(shareLabel(undefined)).toBe("—")
  })
  test("packet rates, round trips and queue delays read at their scale", () => {
    expect(packetRate(950)).toBe("950/s")
    expect(packetRate(1234)).toBe("1.2k/s")
    expect(packetRate(3_400_000)).toBe("3.4M/s")
    expect(millis(0.4)).toBe("0.40 ms")
    expect(millis(4.25)).toBe("4.3 ms")
    expect(millis(42.6)).toBe("43 ms")
    expect(millis(1234)).toBe("1.2 s")
    expect(millis(undefined)).toBe("—")
    expect(delayLabel(1200)).toBe("1.2 ms")
  })
})

describe("recorded history", () => {
  test("a budget's reading counts the estimate and never claims an unmeasured one", () => {
    expect(
      quotaReading({ state: "warning", usedBytes: 800, estimatedBytes: 50, limitBytes: 1000 }),
    ).toEqual({
      percent: 85,
      label: "over 80%",
      tone: "warning",
    })
    expect(
      quotaReading({ state: "exceeded", usedBytes: 5000, estimatedBytes: 0, limitBytes: 1000 })
        .percent,
    ).toBe(100)
    expect(
      quotaReading({ state: "unmeasured", usedBytes: 0, estimatedBytes: 0, limitBytes: 1000 }),
    ).toEqual({
      percent: 0,
      label: "not measured",
      tone: "unknown",
    })
  })
  test("a typed range is bounded, ordered and in the past", () => {
    const from = localInstant("2026-10-08T10:00")
    const to = localInstant("2026-10-08T12:30")
    expect(to - from).toBe(9000)
    expect(localInstant("yesterday")).toBeUndefined()
    expect(localInput(from)).toBe("2026-10-08T10:00")
    const now = to + 60
    expect(rangeProblem(from, to, now)).toBeUndefined()
    expect(rangeProblem(to, from, now)).toBe("The range ends before it starts.")
    expect(rangeProblem(from - 40 * 86400, to, now)).toBe("A range is at most 31 days.")
    expect(rangeProblem(now + 10, now + 20, now)).toContain("future")
    expect(rangeProblem(undefined, to, now)).toBe("Choose both ends of the range.")
  })
})

describe("cross-layer evidence", () => {
  test("addresses parse in both families and IPv4-mapped addresses unmap", () => {
    expect(addressBytes("198.51.100.23")).toEqual([198, 51, 100, 23])
    expect(addressBytes("::ffff:198.51.100.23")).toEqual([198, 51, 100, 23])
    expect(addressBytes("2001:db8::1")).toEqual([
      0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1,
    ])
    expect(addressBytes("300.1.1.1")).toBeUndefined()
    expect(addressBytes("2001:db8::1::2")).toBeUndefined()
    expect(addressBytes("example.com")).toBeUndefined()
  })
  test("a rule's source covers an address by exact match, network or anywhere", () => {
    expect(sourceCovers("198.51.100.23", "198.51.100.23")).toBe(true)
    expect(sourceCovers("198.51.100.0/24", "198.51.100.23")).toBe(true)
    expect(sourceCovers("198.51.100.0/25", "198.51.100.200")).toBe(false)
    expect(sourceCovers("Anywhere", "198.51.100.23")).toBe(true)
    expect(sourceCovers("", "2001:db8::1")).toBe(true)
    expect(sourceCovers("2001:db8::/32", "2001:db8::1")).toBe(true)
    expect(sourceCovers("2001:db8::/32", "198.51.100.23")).toBe(false)
    expect(sourceCovers("10.0.0.0/33", "10.0.0.1")).toBe(false)
  })
  test("retained rows fold by remote address with exact counters and unknowns kept unknown", () => {
    const row = (address, protocol, port, owner, tx, rx, first, last) => ({
      firstSeen: first,
      lastSeen: last,
      socket: {
        protocol,
        remoteAddress: address,
        localPort: port,
        owner: { status: "verified_process", program: owner, pid: 1 },
      },
      txBytes: tx,
      rxBytes: rx,
    })
    const peers = foldHistoricalFlows(
      [
        row(
          "198.51.100.23",
          "tcp",
          443,
          "caddy",
          "1000",
          "4000",
          "2026-10-08T10:05:00Z",
          "2026-10-08T10:20:00Z",
        ),
        row(
          "198.51.100.23",
          "tcp",
          443,
          "caddy",
          "24",
          null,
          "2026-10-08T10:01:00Z",
          "2026-10-08T10:10:00Z",
        ),
        row(
          "198.51.100.23",
          "udp",
          443,
          "caddy",
          null,
          null,
          "2026-10-08T10:02:00Z",
          "2026-10-08T10:03:00Z",
        ),
        row(
          "192.0.2.53",
          "udp",
          53,
          "systemd-resolve",
          null,
          null,
          "2026-10-08T10:00:00Z",
          "2026-10-08T10:00:30Z",
        ),
        row(
          undefined,
          "udp",
          68,
          "dhclient",
          null,
          null,
          "2026-10-08T10:00:00Z",
          "2026-10-08T10:00:30Z",
        ),
      ],
      (r) => r.socket.owner.program,
    )
    expect(peers.map((p) => p.address)).toEqual(["198.51.100.23", "192.0.2.53"])
    const top = peers[0]
    expect(top.sockets).toBe(3)
    expect(top.protocols).toEqual(["tcp", "udp"])
    expect(top.txBytes).toBe(1024n)
    expect(top.rxBytes).toBe(4000n)
    expect(top.firstSeen).toBe("2026-10-08T10:01:00Z")
    expect(top.lastSeen).toBe("2026-10-08T10:20:00Z")
    expect(peers[1].txBytes).toBeNull()
  })
  test("history bounds widen to whole UTC hours and never past a month", () => {
    const at = Date.parse("2026-10-09T12:34:56Z") / 1000
    expect(hourBounds(at - 3600, at)).toEqual({
      from: "2026-10-09T11:00:00Z",
      to: "2026-10-09T13:00:00Z",
    })
    expect(hourBounds(at - 60 * 86400, at).from).toBe("2026-09-08T13:00:00Z")
    expect(
      hourBounds(
        Date.parse("2026-10-09T12:00:00Z") / 1000,
        Date.parse("2026-10-09T12:00:00Z") / 1000,
      ),
    ).toEqual({
      from: "2026-10-09T12:00:00Z",
      to: "2026-10-09T13:00:00Z",
    })
  })
  test("recent hours are whole UTC hours, newest first", () => {
    const hours = recentHours(new Date("2026-10-09T12:34:56Z"), 3)
    expect(hours[0]).toEqual({
      from: "2026-10-09T12:00:00Z",
      to: "2026-10-09T13:00:00Z",
      label: "2026-10-09 12:00 UTC (this hour)",
    })
    expect(hours[2].from).toBe("2026-10-09T10:00:00Z")
  })
})

describe("blocks and hand-offs", () => {
  test("a block says when it ends or how it ended", () => {
    const now = Date.parse("2026-10-09T12:00:00Z")
    expect(blockStanding({ state: "active", expiresAt: "2026-10-09T15:30:00Z" }, now)).toBe(
      "Ends in 3h",
    )
    expect(blockStanding({ state: "active", expiresAt: "2026-10-12T12:00:00Z" }, now)).toBe(
      "Ends in 3d",
    )
    expect(blockStanding({ state: "active", expiresAt: "2026-10-09T12:00:30Z" }, now)).toBe(
      "Ends in 30s",
    )
    expect(blockStanding({ state: "active", expiresAt: "2026-10-09T11:00:00Z" }, now)).toBe(
      "Due to end now",
    )
    expect(blockStanding({ state: "active" }, now)).toBe("Until lifted")
    expect(blockStanding({ state: "lifted", endedBy: "ana" }, now)).toBe("Lifted by ana")
    expect(blockStanding({ state: "expired" }, now)).toBe("Ended on schedule")
  })
  test("a phase that is not done never reads as done", () => {
    expect(phaseReading({ key: "verified", status: "done", detail: "" })).toEqual({
      label: "Verified",
      tone: "running",
      word: "done",
    })
    expect(phaseReading({ key: "active", status: "pending", detail: "" }).word).toBe("not yet")
    expect(phaseReading({ key: "configured", status: "failed", detail: "" }).tone).toBe("danger")
    expect(phaseReading({ key: "configured", status: "not_applicable", detail: "" }).word).toBe(
      "not applicable",
    )
    expect(phaseReading({ key: "verified", status: "unknown", detail: "" }).tone).toBe("unknown")
  })
})
