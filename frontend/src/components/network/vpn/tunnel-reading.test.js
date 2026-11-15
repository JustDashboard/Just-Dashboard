import { expect, test } from "bun:test"
import {
  drawnPeers,
  familyReading,
  filterMachines,
  findingWords,
  findingsWord,
  machineReach,
  machineState,
  offerChanges,
  offerRequest,
  peerFindings,
  peerLine,
  peerSeen,
  peerTone,
  railTunnels,
  routeProblem,
  routesReading,
  tailnetState,
  tunnelFindings,
  tunnelHref,
  tunnelMoved,
  tunnelState,
} from "./tunnel-reading"

const peer = (over = {}) => ({
  publicKey: "k1",
  kind: "device",
  online: false,
  latestHandshake: 0,
  rxBytes: 0,
  txBytes: 0,
  ...over,
})

test("a tunnel's page is addressed by its name, with the view only when it is not the first", () => {
  expect(tunnelHref("wg0")).toBe("/network/vpn/wg0")
  expect(tunnelHref("wg0", "overview")).toBe("/network/vpn/wg0")
  expect(tunnelHref("wg0", "history")).toBe("/network/vpn/wg0?tab=history")
  expect(tunnelHref("wg office")).toBe("/network/vpn/wg%20office")
})

test("a tunnel says who is online, that it is down, or that nobody is on it yet", () => {
  expect(tunnelState({ up: false, peers: [peer({ online: true })] })).toEqual({
    tone: "stopped",
    label: "Down",
  })
  expect(tunnelState({ up: true, peers: [] }).label).toBe("Up, no peers yet")
  expect(
    tunnelState({ up: true, peers: [peer({ online: true }), peer(), peer({ online: true })] }),
  ).toEqual({ tone: "running", label: "2 of 3 online", live: true })
  expect(tunnelState({ up: true, peers: [peer()] }).live).toBe(false)
})

test("what a tunnel moved is every peer's both ways", () => {
  expect(tunnelMoved({ peers: [peer({ rxBytes: 10, txBytes: 5 }), peer({ rxBytes: 1 })] })).toBe(16)
})

test("a finding is a few words, and an unknown kind keeps its own name", () => {
  expect(findingWords({ kind: "stale_handshake" })).toBe("handshake stale")
  expect(findingWords({ kind: "transport_captured" })).toBe("transport inside a tunnel")
  expect(findingWords({ kind: "quota_exceeded" })).toBe("past its budget")
  expect(findingWords({ kind: "quota_warning" })).toBe("near its budget")
  expect(findingWords({ kind: "key_rotated" })).toBe("key rotated")
  expect(findingsWord(1)).toBe("1 needs a look")
  expect(findingsWord(3)).toBe("3 need a look")
})

test("findings sit on the peer they name, and the rest stay with the tunnel", () => {
  const tunnel = {
    peers: [peer({ publicKey: "a" }), peer({ publicKey: "b" })],
    alerts: [
      { kind: "stale_handshake", peer: "a", message: "a is stale" },
      { kind: "quota_exceeded", peer: "a", message: "a is over" },
      { kind: "transport_captured", peer: "gone", message: "gone" },
      { kind: "stale_handshake", message: "about the tunnel" },
    ],
  }
  expect(peerFindings(tunnel, { publicKey: "a" }).map((f) => f.kind)).toEqual([
    "stale_handshake",
    "quota_exceeded",
  ])
  expect(peerFindings(tunnel, { publicKey: "b" })).toEqual([])
  expect(tunnelFindings(tunnel).map((f) => f.message)).toEqual(["gone", "about the tunnel"])
  expect(peerFindings({}, { publicKey: "a" })).toEqual([])
})

test("a column draws the peers that need a look first and keeps the file's order otherwise", () => {
  const peers = ["a", "b", "c", "d"].map((publicKey) => ({ publicKey }))
  expect(drawnPeers(peers, [{ peer: "c" }, { peer: "c" }]).map((p) => p.publicKey)).toEqual([
    "c",
    "a",
    "b",
    "d",
  ])
  expect(drawnPeers(peers, undefined)).toEqual(peers)
})

test("a peer's eyebrow, mark and wire follow its handshake", () => {
  expect(peerSeen(peer({ online: true }))).toBe("online")
  expect(peerSeen(peer())).toBe("never connected")
  const quiet = peer({ latestHandshake: Math.floor(Date.now() / 1000) - 3600 })
  expect(peerSeen(quiet)).toMatch(/^seen 1h/)
  expect(peerSeen({ ...quiet, handshakeState: "stale" })).toMatch(/^stale · seen 1h/)

  expect(peerTone(peer({ online: true }), 0)).toBe("success")
  expect(peerTone(peer({ online: true }), 1)).toBe("warning")
  expect(peerTone(peer({ transport: { state: "captured" } }), 0)).toBe("warning")
  expect(peerTone(peer(), 0)).toBe("neutral")

  expect(peerLine({ up: false }, peer({ online: true, latestHandshake: 1 }))).toEqual({
    state: "down",
  })
  expect(peerLine({ up: true }, peer())).toEqual({ state: "idle" })
  expect(
    peerLine({ up: true }, peer({ online: true, latestHandshake: 1, rxBytes: 1, txBytes: 9 })),
  ).toEqual({ state: "up", moving: true, reverse: true, duration: 2.2 })
  expect(
    peerLine({ up: true }, peer({ latestHandshake: 1, handshakeState: "stale", kind: "site" })),
  ).toEqual({ state: "warning", moving: false, reverse: false, duration: 3 })
})

test("a family reads its network, its exit and whether its rules were verified", () => {
  const exit = {
    configured: true,
    interface: "eth0",
    runtime: "verified",
    translated: 1234,
    capability: { writable: true },
  }
  expect(
    familyReading({ configured: true, subnet: "10.8.0.0/24", runtime: "present", exit }),
  ).toEqual({
    network: "10.8.0.0/24",
    runtime: "present",
    exit: "Exit configured through eth0 · 1,234 client connections translated",
    reason: undefined,
    verdict: { tone: "running", label: "Rules verified" },
  })
  const off = familyReading({
    configured: true,
    runtime: "not_configured",
    exit: {
      configured: false,
      runtime: "disabled",
      capability: { writable: false, reason: "No opted-in address allocation exists." },
    },
  })
  expect(off.network).toBe("No tunnel network")
  expect(off.runtime).toBe("not configured")
  expect(off.exit).toBe("Exit off")
  expect(off.reason).toBe("No opted-in address allocation exists.")
  expect(off.verdict).toBeUndefined()
  expect(
    familyReading({
      configured: true,
      runtime: "present",
      exit: { ...exit, runtime: "degraded", translated: 1, reason: "not verified" },
    }),
  ).toMatchObject({
    exit: "Exit configured through eth0 · 1 client connection translated",
    reason: "not verified",
    verdict: { tone: "warning", label: "Degraded" },
  })
})

test("the rail lists live tunnels, then removed ones whose name is free, once each", () => {
  const rail = railTunnels(
    [{ name: "wg0", up: true, peers: [peer({ online: true })] }],
    [
      { name: "wg1", archivedAt: Math.floor(Date.now() / 1000) - 86400 },
      { name: "wg1", archivedAt: Math.floor(Date.now() / 1000) - 86400 * 9 },
      { name: "wg0", archivedAt: 1 },
    ],
  )
  expect(rail.map((t) => [t.name, t.archived])).toEqual([
    ["wg0", false],
    ["wg1", true],
  ])
  expect(rail[0].line).toBe("1 of 1 online")
  expect(rail[1].line).toMatch(/^removed 1d/)
})

test("the tailnet's state is who is online, or the daemon's state in words", () => {
  const peers = [{ online: true }, { online: false }]
  expect(tailnetState({ running: true, backendState: "Running", peers })).toEqual({
    tone: "running",
    label: "1 of 2 online",
    live: true,
  })
  expect(tailnetState({ running: true, backendState: "Running", peers: [] }).label).toBe(
    "Running, no other machine",
  )
  expect(tailnetState({ running: false, backendState: "NeedsLogin", peers: [] })).toEqual({
    tone: "warning",
    label: "Needs sign-in",
  })
  expect(tailnetState({ running: false, backendState: "Stopped", peers: [] }).label).toBe("Stopped")
  expect(tailnetState({ running: false, backendState: "InUseOtherUser", peers: [] }).label).toBe(
    "In use other user",
  )
  expect(tailnetState({ running: false, backendState: "", peers: [] }).label).toBe("Not running")
})

test("a machine says its state and how it is reached", () => {
  expect(machineState({ expired: true, online: true })).toEqual({
    tone: "danger",
    label: "Key expired",
  })
  expect(machineState({ online: true, active: true })).toEqual({
    tone: "running",
    label: "Active",
    live: true,
  })
  expect(machineState({ online: true, active: false }).label).toBe("Online")
  expect(machineState({ online: false }).label).toBe("Offline")
  expect(machineState({ online: false, lastSeen: new Date().toISOString() }).label).toBe(
    "Seen just now",
  )
  expect(machineReach({ direct: true, curAddr: "198.51.100.40:41641", relay: "fra" })).toEqual({
    word: "direct",
    detail: "198.51.100.40:41641",
  })
  expect(machineReach({ direct: false, curAddr: "", relay: "fra" })).toEqual({
    word: "relayed",
    detail: "fra",
  })
  expect(machineReach({ direct: false, curAddr: "", relay: "" })).toEqual({ word: "not reached" })
})

test("machines are narrowed by a chip and by a search over name, address and account", () => {
  const machines = [
    {
      hostName: "ana-macbook",
      dnsName: "ana-macbook.tail.ts.net.",
      tailscaleIps: ["100.110.34.9"],
      userLoginName: "ana@example.com",
      os: "macOS",
      online: true,
    },
    {
      hostName: "pixel",
      dnsName: "pixel.tail.ts.net.",
      tailscaleIps: ["100.107.233.73"],
      os: "android",
      online: false,
    },
  ]
  expect(filterMachines(machines, "all", "")).toHaveLength(2)
  expect(filterMachines(machines, "online", "").map((m) => m.hostName)).toEqual(["ana-macbook"])
  expect(filterMachines(machines, "offline", "").map((m) => m.hostName)).toEqual(["pixel"])
  expect(filterMachines(machines, "all", "100.107").map((m) => m.hostName)).toEqual(["pixel"])
  expect(filterMachines(machines, "all", "ANA@").map((m) => m.hostName)).toEqual(["ana-macbook"])
  expect(filterMachines(machines, "online", "pixel")).toEqual([])
})

test("an offer draft counts its edits and becomes only what still differs", () => {
  const prefs = { advertiseRoutes: ["10.0.4.0/24", "192.168.60.0/24"], advertisingExitNode: true }
  expect(offerChanges(prefs, { withdraw: [], route: "" })).toBe(0)
  expect(offerRequest(prefs, { withdraw: [], route: "" })).toBeNull()

  const draft = { exitNode: false, withdraw: ["10.0.4.0/24"], route: " 10.99.0.0/24 " }
  expect(offerChanges(prefs, draft)).toBe(3)
  expect(offerRequest(prefs, draft)).toEqual({
    advertiseExitNode: false,
    advertiseRoutes: ["192.168.60.0/24", "10.99.0.0/24"],
  })

  // A lost answer accepted the last save: against the fresh read nothing is left to send.
  const accepted = {
    advertiseRoutes: ["192.168.60.0/24", "10.99.0.0/24"],
    advertisingExitNode: false,
  }
  expect(offerRequest(accepted, draft)).toBeNull()
  // The exit node as it already is, and a route already withdrawn, are no edit.
  expect(offerChanges(prefs, { exitNode: true, withdraw: ["172.16.0.0/12"], route: "" })).toBe(0)
  expect(offerRequest(prefs, { withdraw: [], route: "10.0.4.0/24" })).toBeNull()
})

test("a typed route is a network with a prefix, and never every address", () => {
  expect(routeProblem("")).toBeUndefined()
  expect(routeProblem("10.0.4.0/24")).toBeUndefined()
  expect(routeProblem("fd00:4::/64")).toBeUndefined()
  expect(routeProblem("10.0.4.0")).toBe("Use a network such as 10.0.4.0/24.")
  expect(routeProblem("10.0.4.0/33")).toBe("Use a network such as 10.0.4.0/24.")
  expect(routeProblem("office.example/24")).toBe("Use a network such as 10.0.4.0/24.")
  expect(routeProblem("0.0.0.0/0")).toBe("Every address is the exit node, not a subnet route.")
})

test("the routes' line says how many are offered, served and not served", () => {
  expect(routesReading([], undefined)).toEqual({ words: "None offered", waiting: 0 })
  expect(routesReading(["10.0.4.0/24"], undefined)).toEqual({ words: "1 offered", waiting: 0 })
  expect(
    routesReading(["10.0.4.0/24", "192.168.60.0/24"], {
      routes: [
        { route: "10.0.4.0/24", state: "serving" },
        { route: "192.168.60.0/24", state: "not_serving" },
      ],
    }),
  ).toEqual({ words: "2 offered · 1 served · 1 not served", waiting: 1 })
})
