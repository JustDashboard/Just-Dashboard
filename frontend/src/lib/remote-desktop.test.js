import { describe, expect, test } from "bun:test"
import {
  candidates,
  combosFor,
  computerHues,
  displaySize,
  overviewVerdict,
  peerOS,
  reachability,
  sessionProblem,
  sessionTotals,
} from "./remote-desktop"

const peer = (over) => ({
  id: "n1",
  name: "OFFICE-PC",
  dnsName: "office-pc.example.ts.net.",
  os: "windows",
  address: "100.101.12.7",
  online: true,
  direct: true,
  relay: "",
  lastSeen: 0,
  ...over,
})

const view = (over) => ({
  id: "a",
  name: "office-pc",
  os: "windows",
  host: "office-pc",
  protocol: "rdp",
  port: 3389,
  username: "",
  domain: "",
  hasPassword: true,
  security: "",
  layout: "",
  peerId: "",
  createdAt: 1,
  updatedAt: 1,
  lastSessionAt: 0,
  live: 0,
  reach: { state: "open", latencyMs: 43, checkedAt: 0 },
  ...over,
})

describe("candidates", () => {
  test("offers the tailnet's desktops not added yet, online first, and never a phone", () => {
    const tailnet = {
      installed: true,
      peers: [
        peer({
          id: "mac",
          name: "Studio",
          dnsName: "studio-mac.example.ts.net.",
          os: "macOS",
          online: false,
          lastSeen: 9,
        }),
        peer({ id: "phone", name: "localhost", dnsName: "iphone.example.ts.net.", os: "iOS" }),
        peer({
          id: "added",
          name: "GAMING-PC",
          dnsName: "gaming-pc.example.ts.net.",
          address: "100.101.12.8",
        }),
        peer({
          id: "pc",
          name: "WORKSHOP-PC",
          dnsName: "workshop-pc.example.ts.net.",
          address: "100.101.12.10",
        }),
      ],
    }
    const offered = candidates(tailnet, [view({ host: "100.101.12.8" })])
    expect(offered.map((p) => p.id)).toEqual(["pc", "mac"])
    expect(peerOS("darwin")).toBe("macos")
    expect(peerOS("android")).toBeUndefined()
  })
})

describe("computerHues", () => {
  test("deals hues in the order computers were added, so a new one moves nobody", () => {
    const first = computerHues([view({ id: "b", createdAt: 2 }), view({ id: "a", createdAt: 1 })])
    const later = computerHues([
      view({ id: "a", createdAt: 1 }),
      view({ id: "b", createdAt: 2 }),
      view({ id: "c", createdAt: 3 }),
    ])
    expect(later.get("a")).toBe(first.get("a"))
    expect(later.get("b")).toBe(first.get("b"))
    expect(new Set(later.values()).size).toBe(3)
    expect([...later.values()]).not.toContain("var(--tag-slate)")
  })
})

describe("reachability", () => {
  test("says a live session, a ready port, a refused one and an offline computer apart", () => {
    expect(reachability(view({ live: 1 })).kind).toBe("live")
    expect(reachability(view()).label).toBe("Ready")
    const refused = reachability(
      view({ reach: { state: "closed", error: "refused", checkedAt: 0 } }),
    )
    expect(refused).toEqual({
      kind: "closed",
      tone: "warning",
      label: "Remote Desktop is not answering",
    })
    const mac = view({
      os: "macos",
      protocol: "vnc",
      reach: { state: "closed", error: "no answer", checkedAt: 0 },
    })
    expect(reachability(mac).label).toBe("Screen Sharing is not answering (no answer)")
    expect(reachability(view({ reach: { state: "offline", checkedAt: 0 } })).tone).toBe("neutral")
  })
})

describe("overviewVerdict", () => {
  const overview = (computers, engine = "running") => ({
    engine: { state: engine },
    computers,
    tailnet: { installed: true, peers: [] },
    sessions: [],
    layouts: [],
  })
  test("puts a stopped engine before everything, then open sessions, then readiness", () => {
    expect(overviewVerdict(overview([view()], "missing")).label).toBe("Engine not running")
    expect(overviewVerdict(overview([view({ live: 2 })])).label).toBe("2 sessions open")
    expect(overviewVerdict(overview([view(), view({ id: "b" })])).label).toBe("All 2 ready")
    const half = overviewVerdict(
      overview([view(), view({ id: "b", reach: { state: "offline", checkedAt: 0 } })]),
    )
    expect(half).toEqual({ tone: "neutral", label: "1 of 2 ready" })
    expect(overviewVerdict(overview([]))).toBeUndefined()
  })
})

describe("sessionTotals", () => {
  test("adds each computer's time, counting an open session to now, busiest first", () => {
    const totals = sessionTotals(
      [
        { id: 1, computerId: "a", actor: "w", startedAt: 0, endedAt: 600, outcome: "closed" },
        { id: 2, computerId: "b", actor: "w", startedAt: 100, endedAt: 0, outcome: "" },
        { id: 3, computerId: "a", actor: "w", startedAt: 1000, endedAt: 1300, outcome: "closed" },
      ],
      2000,
    )
    expect(totals).toEqual([
      { computerId: "b", seconds: 1900, count: 1 },
      { computerId: "a", seconds: 900, count: 2 },
    ])
  })
})

describe("sessionProblem", () => {
  test("turns Guacamole's codes into what to check on the computer", () => {
    const pc = { name: "office-pc", port: 3389, protocol: "rdp", os: "windows" }
    expect(sessionProblem(0x0207, "", pc).detail).toContain("Remote Desktop is turned on")
    expect(sessionProblem(0x0301, "", pc).title).toBe("The computer refused the sign-in")
    expect(sessionProblem(0x0200, "Aborted. See logs.", pc).detail).toBe("Aborted. See logs.")
  })
})

describe("displaySize", () => {
  test("asks for the frame's device pixels, capped at twice its size", () => {
    expect(displaySize(1000, 600, 1)).toEqual({ width: 1000, height: 600, dpi: 96, density: 1 })
    expect(displaySize(400, 800, 3)).toEqual({ width: 800, height: 1600, dpi: 192, density: 2 })
  })
})

describe("combosFor", () => {
  test("offers Ctrl+Alt+Del to Windows and not to a Mac", () => {
    expect(combosFor("windows").map((c) => c.key)).toContain("cad")
    expect(combosFor("macos").map((c) => c.key)).not.toContain("cad")
  })
})
