import { describe, expect, test } from "bun:test"
import {
  connectTarget,
  hubHost,
  keptDismissals,
  linkReading,
  linkedByWords,
  noticeHref,
  noticeTone,
  openable,
  orderFleet,
  owedNotices,
  selfServer,
  serverAddress,
  serverEntries,
  serverReading,
  shellHub,
  terminalCommand,
} from "./hub-link"

// The fleet of hub-link-protocol.md §4.3, with the times as the dashboard serves them (§8.2).
const self = {
  serverId: "7a0d3c1e-0000-4000-8000-000000000002",
  name: "vps-07749119-vps-ovh-net",
  endpoint: "https://vps-07749119-vps-ovh-net.tailed39ba.ts.net:8443",
  state: "online",
  version: "0.8.5",
  self: true,
  lastSeenAt: "2026-11-05T10:00:00Z",
}
const other = {
  serverId: "5b1f9a20-0000-4000-8000-000000000001",
  name: "vmi3690758",
  endpoint: "https://vmi3690758.tailed39ba.ts.net:8443",
  state: "late",
  version: "0.8.5",
  self: false,
  lastSeenAt: "2026-11-05T09:59:00Z",
}
const tunnelled = {
  serverId: "3c2e0f11-0000-4000-8000-000000000003",
  name: "Atlas",
  state: "offline",
  version: "",
  self: false,
}

const session = {
  linked: true,
  name: "hub",
  url: "https://hub.tailed39ba.ts.net",
  reachable: true,
}

const linked = {
  enabled: true,
  state: "linked",
  hubUrl: "https://hub.tailed39ba.ts.net",
  hubName: "hub",
  scope: "maintain",
  reachable: true,
  lastHeartbeatAt: "2026-11-05T10:00:00Z",
  fleet: [other, self],
  notices: [],
}

describe("the address ?connect= fills in", () => {
  test("a bare host becomes its https origin, in the spelling §2.2 step 3 stores", () => {
    expect(connectTarget("hub.tailed39ba.ts.net")).toBe("https://hub.tailed39ba.ts.net")
    expect(connectTarget("  HUB.tailed39ba.ts.net. ")).toBe("https://hub.tailed39ba.ts.net")
    expect(connectTarget("hub.tailed39ba.ts.net:443")).toBe("https://hub.tailed39ba.ts.net")
    expect(connectTarget("hub.tailed39ba.ts.net:8443")).toBe("https://hub.tailed39ba.ts.net:8443")
    expect(connectTarget("https://hub.tailed39ba.ts.net/")).toBe("https://hub.tailed39ba.ts.net")
  })

  test("anything that is not a host fills nothing in", () => {
    for (const value of [
      null,
      undefined,
      "",
      "javascript:alert(1)",
      "http://hub.tailed39ba.ts.net",
      "hub.tailed39ba.ts.net/servers",
      "owner@hub.tailed39ba.ts.net",
      "hub.tailed39ba.ts.net?x=1",
      "hub .tailed39ba.ts.net",
      "-hub.tailed39ba.ts.net",
    ]) {
      expect(connectTarget(value)).toBe("")
    }
  })

  test("the test origin is the one http hub, and only when it is the one set", () => {
    expect(connectTarget("127.0.0.1:43180", "http://127.0.0.1:43180")).toBe(
      "http://127.0.0.1:43180",
    )
    expect(connectTarget("127.0.0.1:43180")).toBe("https://127.0.0.1:43180")
  })
})

describe("what the browser may be sent to", () => {
  test("https origins, and loopback http for the test origin", () => {
    expect(openable("https://vmi3690758.tailed39ba.ts.net:8443")).toBe(
      "https://vmi3690758.tailed39ba.ts.net:8443/",
    )
    expect(openable("http://127.0.0.1:43180")).toBe("http://127.0.0.1:43180/")
    expect(openable("http://[::1]:43180")).toBe("http://[::1]:43180/")
  })

  test("never another scheme, plain http elsewhere, or credentials", () => {
    expect(openable("javascript:alert(1)")).toBeUndefined()
    expect(openable("data:text/html,hi")).toBeUndefined()
    expect(openable("http://hub.tailed39ba.ts.net")).toBeUndefined()
    expect(openable("https://user:pass@hub.tailed39ba.ts.net")).toBeUndefined()
    expect(openable("not a url")).toBeUndefined()
    expect(openable(undefined)).toBeUndefined()
  })

  test("a server opens at its own origin, never a path the hub sent", () => {
    expect(serverAddress(other)).toBe("https://vmi3690758.tailed39ba.ts.net:8443")
    expect(serverAddress({ ...other, endpoint: "https://x.tailed39ba.ts.net/evil?a=1" })).toBe(
      "https://x.tailed39ba.ts.net",
    )
    expect(serverAddress(tunnelled)).toBeUndefined()
  })

  test("a notice links only to the hub's own origin (§6.2)", () => {
    const hub = "https://hub.tailed39ba.ts.net"
    expect(noticeHref("https://hub.tailed39ba.ts.net/updates", hub)).toBe(
      "https://hub.tailed39ba.ts.net/updates",
    )
    expect(noticeHref("https://evil.example.com/updates", hub)).toBeUndefined()
    expect(noticeHref("https://hub.tailed39ba.ts.net:8443/updates", hub)).toBeUndefined()
    expect(noticeHref(undefined, hub)).toBeUndefined()
  })

  test("a hub URL reads as its host", () => {
    expect(hubHost("https://hub.tailed39ba.ts.net")).toBe("hub.tailed39ba.ts.net")
    expect(hubHost("http://127.0.0.1:43180")).toBe("127.0.0.1:43180")
    expect(hubHost(undefined)).toBe("")
  })
})

describe("whether the shell is hub-aware", () => {
  test("no session field, nothing at all", () => {
    expect(shellHub(undefined, undefined)).toBeUndefined()
    expect(shellHub(undefined, linked)).toBeUndefined()
  })

  test("the session decides before the status has been read", () => {
    expect(shellHub(session, undefined)).toEqual({
      name: "hub",
      url: "https://hub.tailed39ba.ts.net",
      reachable: true,
      fleet: [],
      notices: [],
    })
  })

  test("the status brings the fleet and the freshest reachability", () => {
    const hub = shellHub(session, { ...linked, reachable: false })
    expect(hub?.reachable).toBe(false)
    expect(hub?.fleet).toHaveLength(2)
  })

  test("a link that went away since the page loaded takes the hub away", () => {
    expect(shellHub(session, { enabled: true, state: "unlinked" })).toBeUndefined()
    expect(shellHub(session, { enabled: false, state: "linked" })).toBeUndefined()
    expect(shellHub(session, { ...linked, state: "pending" })).toBeUndefined()
  })
})

describe("the fleet", () => {
  test("this server first, then the rest by name", () => {
    const names = orderFleet([other, tunnelled, self]).map((server) => server.name)
    expect(names).toEqual(["vps-07749119-vps-ovh-net", "Atlas", "vmi3690758"])
    expect(selfServer([other, self])?.name).toBe("vps-07749119-vps-ovh-net")
    expect(selfServer([])).toBeUndefined()
  })

  test("each state reads as a dot and a word", () => {
    expect(serverReading(self, true)).toEqual({ tone: "running", label: "Online" })
    expect(serverReading(other, true)).toEqual({ tone: "warning", label: "Late" })
    expect(serverReading(tunnelled, true)).toEqual({ tone: "danger", label: "Offline" })
    expect(serverReading({ ...other, state: "updating" }, true)).toEqual({
      tone: "notice",
      label: "Updating",
    })
  })

  test("with the hub unreachable the others are unknown, and this one says why", () => {
    expect(serverReading(other, false)).toEqual({ tone: "unknown", label: "Unknown" })
    expect(serverReading(self, false)).toEqual({ tone: "warning", label: "Not reaching the hub" })
  })
})

describe("the palette's Servers", () => {
  const entries = serverEntries(shellHub(session, { ...linked, fleet: [other, tunnelled, self] }))

  test("every server, this one first and opening here, then the hub", () => {
    expect(entries.map((entry) => entry.title)).toEqual([
      "vps-07749119-vps-ovh-net",
      "Atlas",
      "vmi3690758",
      "Just Hub",
    ])
    expect(entries.every((entry) => entry.kind === "server")).toBe(true)
    expect(entries[0]).toMatchObject({ href: "/", self: true, detail: "This server" })
    expect(entries[2]).toMatchObject({
      href: "https://vmi3690758.tailed39ba.ts.net:8443",
      detail: "0.8.5",
      reading: { tone: "warning", label: "Late" },
    })
    expect(entries[3]).toMatchObject({
      id: "server:hub",
      href: "https://hub.tailed39ba.ts.net/",
      hub: true,
    })
  })

  test("a server with no address is listed and says it cannot be opened", () => {
    expect(entries[1].href).toBeUndefined()
    expect(entries[1].detail).toBe("no report yet · no address")
    expect(entries[1].facts).toContainEqual({ label: "Opens", value: "No address reported" })
  })

  test("found by its address as well as its name", () => {
    expect(entries[2].keywords).toContain("vmi3690758.tailed39ba.ts.net:8443")
  })
})

describe("notices", () => {
  const notices = [
    { id: "update-0.8.6", level: "info", title: "Update ready", body: "", at: "x" },
    { id: "offline:atlas", level: "critical", title: "Atlas is offline", body: "", at: "x" },
  ]

  test("a level is the shared severity union", () => {
    expect(noticeTone("info")).toBe("default")
    expect(noticeTone("warning")).toBe("warning")
    expect(noticeTone("critical")).toBe("danger")
  })

  test("a dismissed notice is no longer owed, the rest keep the hub's order", () => {
    expect(owedNotices(notices, ["update-0.8.6"]).map((notice) => notice.id)).toEqual([
      "offline:atlas",
    ])
    expect(owedNotices(notices, [])).toHaveLength(2)
  })

  test("a dismissal is forgotten once the hub stops carrying its notice", () => {
    expect(keptDismissals(["update-0.8.6", "gone"], notices)).toEqual(["update-0.8.6"])
    expect(keptDismissals(["update-0.8.6"], [])).toEqual([])
  })
})

describe("the link in words", () => {
  test("each state", () => {
    expect(linkReading({ enabled: false, state: "unlinked" })).toEqual({
      tone: "stopped",
      label: "Turned off",
    })
    expect(linkReading({ enabled: true, state: "unlinked" }).label).toBe("Not connected")
    expect(linkReading({ enabled: true, state: "pending" }).label).toBe("Waiting for approval")
    expect(linkReading(linked)).toEqual({ tone: "running", label: "Connected" })
    expect(linkReading({ ...linked, reachable: false })).toEqual({
      tone: "warning",
      label: "Connected, hub unreachable",
    })
    expect(linkReading({ ...linked, lastHeartbeatAt: undefined }).label).toBe(
      "Connected, first report due",
    )
  })

  test("who connected it, and the terminal's way to", () => {
    expect(linkedByWords("cli")).toBe("the terminal")
    expect(linkedByWords("ion")).toBe("ion")
    expect(terminalCommand("https://hub.tailed39ba.ts.net")).toBe(
      "sudo ./scripts/manage.sh hub connect https://hub.tailed39ba.ts.net",
    )
    expect(terminalCommand("")).toBe("sudo ./scripts/manage.sh hub connect URL")
  })
})
