import { describe, expect, test } from "bun:test"
import {
  addressHref,
  blockContext,
  blockable,
  foldPeers,
  matchPeers,
  parseAddress,
  socketState,
  stateWords,
  stepAddress,
  transportWords,
} from "./model"

const peer = (address, over = {}) => ({
  address,
  count: 1,
  established: 1,
  ports: [443],
  processes: ["caddy"],
  private: false,
  ...over,
})

describe("an address's page", () => {
  test("escapes an IPv6 address into one segment and carries the past hour", () => {
    expect(addressHref("198.51.100.23")).toBe("/network/connections/198.51.100.23")
    expect(addressHref("2001:db8::1")).toBe("/network/connections/2001%3Adb8%3A%3A1")
    expect(addressHref("198.51.100.23", { tab: "live", hour: "x" })).toBe(
      "/network/connections/198.51.100.23",
    )
    expect(addressHref("198.51.100.23", { tab: "past", hour: "2026-11-13T09:00:00Z" })).toBe(
      "/network/connections/198.51.100.23?tab=past&hour=2026-11-13T09%3A00%3A00Z",
    )
  })

  test("reads the segment back, escaped or not, and refuses what is no address", () => {
    expect(parseAddress("198.51.100.23")).toBe("198.51.100.23")
    expect(parseAddress("2001%3Adb8%3A%3A1")).toBe("2001:db8::1")
    expect(parseAddress("2001:DB8::1")).toBe("2001:db8::1")
    expect(parseAddress("::1")).toBe("::1")
    expect(parseAddress("%3A%3Affff%3A203.0.113.5")).toBe("203.0.113.5")
    expect(parseAddress("fd7a:115c:a1e0::9")).toBe("fd7a:115c:a1e0::9")
    expect(parseAddress("1:2:3:4:5:6:7:8")).toBe("1:2:3:4:5:6:7:8")
    for (const bad of [
      undefined,
      "",
      "256.1.1.1",
      "1.2.3",
      "example.com",
      "1::2::3",
      "1:2:3",
      "12345::1",
      "..%2F..%2Fetc",
      "%E0%A4%A",
    ]) {
      expect(parseAddress(bad)).toBeNull()
    }
  })
})

describe("the list", () => {
  const peers = [
    peer("198.51.100.23", { service: "HTTPS" }),
    peer("100.110.34.9", { private: true, ports: [8443] }),
    peer("10.0.4.3", { private: true, processes: ["postgres"], ports: [5432] }),
  ]

  test("narrows by scope and by address, port, program or service", () => {
    expect(matchPeers(peers, "all", "").map((p) => p.address)).toHaveLength(3)
    expect(matchPeers(peers, "public", "").map((p) => p.address)).toEqual(["198.51.100.23"])
    expect(matchPeers(peers, "all", "5432").map((p) => p.address)).toEqual(["10.0.4.3"])
    expect(matchPeers(peers, "all", "https").map((p) => p.address)).toEqual(["198.51.100.23"])
    expect(matchPeers(peers, "all", " POSTGRES ").map((p) => p.address)).toEqual(["10.0.4.3"])
  })

  test("says what folding kept of a peer's sockets", () => {
    const folded = peer("198.51.100.23", {
      protocols: ["tcp", "udp"],
      remotePorts: [51022, 51023, 51024],
      morePorts: 15,
      states: { TIME_WAIT: 3, ESTABLISHED: 14, CONNECTED: 1 },
    })
    expect(transportWords(folded)).toEqual({
      protocols: "TCP/UDP",
      ports: "from 51022, 51023, 51024 +15",
    })
    expect(stateWords(folded)).toBe("14 established, 3 time-wait, 1 connected")
    expect(transportWords(peer("198.51.100.23"))).toBeUndefined()
    expect(stateWords(peer("198.51.100.23"))).toBeUndefined()
    expect(transportWords(peer("198.51.100.23", { protocols: ["tcp"] }))).toEqual({
      protocols: "TCP",
      ports: undefined,
    })
    expect(socketState("FIN_WAIT_1")).toBe("fin-wait-1")
    expect(socketState("")).toBe("connected")
  })

  test("walks the rows and starts over from an address the list lost", () => {
    const rows = ["a", "b", "c"]
    expect(stepAddress(rows, "b", 1)).toBe("c")
    expect(stepAddress(rows, "b", -1)).toBe("a")
    expect(stepAddress(rows, "c", 1)).toBeUndefined()
    expect(stepAddress(rows, "gone", 1)).toBe("a")
    expect(stepAddress(rows, "gone", -1)).toBe("c")
  })
})

describe("blocks", () => {
  test("are never offered for a private address, whatever the server said", () => {
    expect(blockable("198.51.100.23")).toBe(true)
    expect(blockable("198.51.100.23", true)).toBe(false)
    expect(blockable("100.110.34.9")).toBe(false)
    expect(blockable("10.0.4.3", false)).toBe(false)
    expect(blockable("127.0.0.1")).toBe(false)
  })

  test("offer the reason the reader was looking at", () => {
    expect(blockContext(peer("192.0.2.145", { service: "SSH", ports: [22] }))).toBe(
      "connected to SSH on 22",
    )
    expect(blockContext(peer("192.0.2.145", { processes: [], ports: [] }))).toBe(
      "connected to this server",
    )
    expect(blockContext(undefined)).toBe("seen in the socket history")
  })
})

describe("the picture", () => {
  test("folds by place and program, busiest first, leaving loopback out", () => {
    const { places, programs } = foldPeers([
      peer("198.51.100.23", { count: 18, established: 14 }),
      peer("203.0.113.77", { count: 9, established: 6, ports: [80, 443] }),
      peer("100.110.34.9", { count: 12, established: 12, ports: [8443] }),
      peer("10.0.4.3", { count: 22, established: 20, processes: ["postgres"], ports: [5432] }),
      peer("127.0.0.1", { count: 50, established: 50, processes: ["redis-server"] }),
    ])
    expect(places.map((p) => [p.kind, p.peers.length, p.sockets, p.active])).toEqual([
      ["internet", 2, 27, 20],
      ["tailscale", 1, 12, 12],
      ["local", 1, 22, 20],
    ])
    expect(places[0].peers.map((p) => p.address)).toEqual(["198.51.100.23", "203.0.113.77"])
    expect(programs.map((p) => [p.name, p.ports, p.sockets])).toEqual([
      ["caddy", [80, 443, 8443], 39],
      ["postgres", [5432], 22],
    ])
  })

  test("keeps every place on a quiet server, so the picture has one shape", () => {
    const quiet = foldPeers([])
    expect(quiet.places.map((p) => [p.kind, p.peers.length])).toEqual([
      ["internet", 0],
      ["tailscale", 0],
      ["local", 0],
    ])
    expect(quiet.programs).toEqual([])
    const tailnet = foldPeers([peer("100.110.34.9"), peer("::1")])
    expect(tailnet.places.map((p) => p.peers.length)).toEqual([0, 1, 0])
    expect(tailnet.programs.map((p) => p.name)).toEqual(["caddy"])
  })
})
