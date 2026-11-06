import { describe, expect, test } from "bun:test"
import {
  atTestOrigin,
  connectTarget,
  describeMachine,
  exchangeFailure,
  firstReportDue,
  hubHost,
  hubLabel,
  hubSays,
  idleReason,
  isIdle,
  keptDismissals,
  linkReading,
  linkedByWords,
  noticeHref,
  noticeTone,
  openable,
  orderFleet,
  owedNotices,
  reportCadence,
  returnAfterSignIn,
  sentence,
  serverAddress,
  serverEntries,
  serverReading,
  shellHub,
  signInAddress,
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
// Another server's version is always "" (§6.1): only this server learns its own.
const other = {
  serverId: "5b1f9a20-0000-4000-8000-000000000001",
  name: "vmi3690758",
  endpoint: "https://vmi3690758.tailed39ba.ts.net:8443",
  state: "late",
  version: "",
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

  test("the test origin written out is filled in exactly, and no other http origin is", () => {
    // A development hub's Connect button names its own origin (docs/e2e.md 3.3).
    const origin = "http://127.0.0.1:9443"
    expect(connectTarget("http://127.0.0.1:9443", origin)).toBe(origin)
    expect(connectTarget(" HTTP://127.0.0.1:9443/ ", origin)).toBe(origin)
    expect(connectTarget("http://127.0.0.1:9443")).toBe("")
    expect(connectTarget("http://127.0.0.1:9444", origin)).toBe("")
    expect(connectTarget("http://127.0.0.1:9443/servers", origin)).toBe("")
    // Its host under https:// is still its host, so it is the test origin too.
    expect(connectTarget("https://127.0.0.1:9443", origin)).toBe(origin)
  })
})

describe("whose machine the hub is, and who is speaking", () => {
  test("a person's machine, a tagged one, and one with neither", () => {
    expect(
      describeMachine({
        id: "nK7f2QbPaa21CNTRL",
        name: "hub.tailed39ba.ts.net",
        owner: "wayy01@github",
        tags: [],
      }),
    ).toBe("hub.tailed39ba.ts.net, a machine of wayy01@github")
    expect(
      describeMachine({ id: "n1", name: "hub.tailed39ba.ts.net", tags: ["tag:server", "tag:hub"] }),
    ).toBe("hub.tailed39ba.ts.net, tagged tag:server, tag:hub")
    expect(describeMachine({ id: "n1", name: "hub.tailed39ba.ts.net", tags: [] })).toBe(
      "hub.tailed39ba.ts.net",
    )
  })

  test("the hub's words are the hub's, by name when it has one", () => {
    expect(hubSays("hub")).toBe("Just Hub (hub) says")
    expect(hubSays(undefined)).toBe("Just Hub says")
    expect(hubLabel("hub")).toBe("Just Hub (hub)")
    expect(hubLabel(undefined)).toBe("Just Hub")
  })

  test("the test origin is told apart by origin, not by spelling", () => {
    expect(atTestOrigin("http://127.0.0.1:9443", "http://127.0.0.1:9443/")).toBe(true)
    expect(atTestOrigin("http://127.0.0.1:9443", "http://127.0.0.1:9444")).toBe(false)
    expect(atTestOrigin("https://hub.tailed39ba.ts.net", undefined)).toBe(false)
    expect(atTestOrigin(undefined, "http://127.0.0.1:9443")).toBe(false)
    expect(atTestOrigin("not a url", "http://127.0.0.1:9443")).toBe(false)
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
      due: false,
      idle: false,
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

  test("before the first report this one says the report is due, not that it fails", () => {
    expect(serverReading(self, false, true)).toEqual({ tone: "notice", label: "First report due" })
    expect(serverReading(other, false, true)).toEqual({ tone: "unknown", label: "Unknown" })
  })
})

describe("the palette's Servers", () => {
  const entries = serverEntries(shellHub(session, { ...linked, fleet: [other, tunnelled, self] }))

  test("every server, this one first and opening here, then the hub by its name", () => {
    expect(entries.map((entry) => entry.title)).toEqual([
      "vps-07749119-vps-ovh-net",
      "Atlas",
      "vmi3690758",
      "Just Hub (hub)",
    ])
    expect(entries.every((entry) => entry.kind === "server")).toBe(true)
    expect(entries[0]).toMatchObject({ href: "/", self: true, detail: "This server" })
    expect(entries[0].facts).toContainEqual({ label: "Version", value: "0.8.5" })
    // Another server reads as where it opens: the hub sends no version for it (§6.1).
    expect(entries[2]).toMatchObject({
      href: "https://vmi3690758.tailed39ba.ts.net:8443",
      detail: "vmi3690758.tailed39ba.ts.net:8443",
      reading: { tone: "warning", label: "Late" },
    })
    expect(entries[2].facts).toContainEqual({ label: "Opens", value: "In a new tab" })
    expect(entries[2].facts.map((fact) => fact.label)).not.toContain("Version")
    expect(entries[3]).toMatchObject({
      id: "server:hub",
      href: "https://hub.tailed39ba.ts.net/",
      hub: true,
    })
    expect(entries[3].facts).toContainEqual({ label: "Opens", value: "In a new tab" })
  })

  test("every name says it is the hub's (§8.10)", () => {
    for (const entry of entries.slice(0, 3)) {
      expect(entry.facts).toContainEqual({ label: "Listed by", value: "Just Hub (hub)" })
    }
  })

  test("a server with no address is listed and says it cannot be opened", () => {
    expect(entries[1].href).toBeUndefined()
    expect(entries[1].detail).toBe("no address · no report yet")
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

// An idle link as an administrator reads it: its reason as its last error (hublink/status_test.go).
const idle = (reason) => ({
  ...linked,
  reachable: false,
  admin: {
    link: {
      serverId: "7a0d3c1e-0000-4000-8000-000000000002",
      linkedAt: "2026-11-05T09:00:00Z",
      linkedBy: "ion",
      lastError: `this server no longer accepts the hub's address: ${reason}; disconnect, then connect again`,
      hubKeys: [],
    },
  },
})

describe("an idle link", () => {
  test("its reason, as a sentence", () => {
    expect(idleReason(idle("the hub's machine changed"))).toBe("The hub's machine changed")
    // A reason with a colon and a semicolon of its own keeps them.
    expect(
      idleReason(
        idle(
          "the hub's address no longer passes this server's tailnet rule: it is an address; not a name",
        ),
      ),
    ).toBe(
      "The hub's address no longer passes this server's tailnet rule: it is an address; not a name",
    )
  })

  test("any other last error is not idleness", () => {
    const failed = idle("x")
    failed.admin.link.lastError = "the heartbeat failed: the hub answered HTTP 503"
    expect(idleReason(failed)).toBeUndefined()
    expect(linkReading(failed).label).toBe("Connected, hub unreachable")
    expect(idleReason(linked)).toBeUndefined()
    // Only a stored link can be idle.
    expect(idleReason({ ...idle("x"), state: "pending" })).toBeUndefined()
  })
})

describe("an idle link leads nowhere near the hub's address", () => {
  // After "the hub's machine changed" that address answers on a machine this server refused.
  const notice = {
    id: "update-0.8.6",
    level: "info",
    title: "Update ready",
    body: "",
    href: "https://hub.tailed39ba.ts.net/updates",
    at: "x",
  }

  test("every reader is told, the reason only an administrator", () => {
    const reader = { ...linked, reachable: false, idle: true }
    expect(isIdle(reader)).toBe(true)
    expect(idleReason(reader)).toBeUndefined()
    expect(linkReading(reader)).toEqual({ tone: "danger", label: "Idle" })
    expect(isIdle(idle("the hub's machine changed"))).toBe(true)
    expect(isIdle(linked)).toBe(false)
    expect(isIdle({ ...linked, state: "pending", idle: true })).toBe(false)
  })

  test("the shell keeps the hub's name and words, and drops its address and links", () => {
    const hub = shellHub(session, { ...linked, reachable: false, idle: true, notices: [notice] })
    expect(hub).toMatchObject({ name: "hub", url: "", reachable: false, idle: true })
    expect(hub.notices).toEqual([{ ...notice, href: undefined }])
    expect(noticeHref(hub.notices[0].href, hub.url)).toBeUndefined()
    // Before the status is read, the session says so.
    expect(shellHub({ ...session, reachable: false, idle: true }, undefined)).toMatchObject({
      url: "",
      idle: true,
    })
    // An administrator's status, read from its reason, idles the shell the same way.
    expect(shellHub(session, idle("the hub's machine changed"))?.url).toBe("")
  })

  test("the palette's hub opens nothing and says why", () => {
    const hub = shellHub(session, { ...linked, reachable: false, idle: true })
    const entry = serverEntries(hub).at(-1)
    expect(entry).toMatchObject({ id: "server:hub", href: undefined, detail: "link idle" })
    expect(entry.reading).toEqual({ tone: "danger", label: "Idle" })
    expect(entry.facts).toContainEqual({ label: "Opens", value: "Not while the link is idle" })
    expect(entry.facts.map((fact) => fact.label)).not.toContain("Address")
  })
})

describe("the link in words", () => {
  test("how often it reports is the hub's interval, never an assumed one", () => {
    expect(reportCadence(15)).toBe("every 15s")
    expect(reportCadence(60)).toBe("every 1m")
    expect(reportCadence(600)).toBe("every 10m")
    expect(reportCadence(undefined)).toBe("on the hub's schedule")
  })

  // What the backend sends from the approval until the first report has gone (hublink/status.go
  // reachable): not reachable, no report time, and for an administrator no failure either.
  const justLinked = {
    ...linked,
    reachable: false,
    lastHeartbeatAt: undefined,
    fleet: [],
    admin: {
      fingerprint: "21fe:31df:a154:a261",
      link: { serverId: "s", linkedAt: "2026-11-05T10:00:00Z", linkedBy: "ion", hubKeys: [] },
    },
  }

  test("a link just made says its first report is due, not that the hub is unreachable", () => {
    expect(firstReportDue(justLinked)).toBe(true)
    expect(linkReading(justLinked)).toEqual({
      tone: "notice",
      label: "Connected, first report due",
    })
    const hub = shellHub(session, justLinked)
    expect(hub).toMatchObject({ reachable: false, due: true })
    expect(serverEntries(hub).at(-1).reading).toEqual({
      tone: "notice",
      label: "First report due",
    })
    // Once the report went, the link reads as any other.
    const reported = { ...justLinked, reachable: true, lastHeartbeatAt: "2026-11-05T10:00:05Z" }
    expect(firstReportDue(reported)).toBe(false)
    expect(linkReading(reported)).toEqual({ tone: "running", label: "Connected" })
  })

  test("a first report that failed, or that nobody can tell from failing, is unreachable", () => {
    const failed = {
      ...justLinked,
      admin: {
        ...justLinked.admin,
        link: { ...justLinked.admin.link, lastError: 'Post "http://h/x": EOF' },
      },
    }
    expect(firstReportDue(failed)).toBe(false)
    expect(linkReading(failed).label).toBe("Connected, hub unreachable")
    expect(shellHub(session, failed)?.due).toBe(false)
    // A reader's status has no `admin` part, so no way to tell a report due from one failing.
    const forReader = { ...justLinked, admin: undefined }
    expect(firstReportDue(forReader)).toBe(false)
    expect(linkReading(forReader).label).toBe("Connected, hub unreachable")
    // Before the status is read, the session cannot tell either.
    expect(shellHub({ ...session, reachable: false }, undefined)?.due).toBe(false)
    expect(firstReportDue({ ...justLinked, state: "pending" })).toBe(false)
    expect(firstReportDue({ ...justLinked, idle: true })).toBe(false)
    expect(firstReportDue({ ...justLinked, enabled: false })).toBe(false)
  })

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
    expect(linkReading(justLinked).label).toBe("Connected, first report due")
    expect(linkReading(idle("the hub's machine changed"))).toEqual({
      tone: "danger",
      label: "Idle",
    })
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

// The hub opens its Connect door in a new tab, usually signed out (§8.1); the sign-in brings the
// browser back to it, and to nowhere else.
describe("the Connect door survives signing in", () => {
  test("the door is carried to the sign-in page and back, re-encoded", () => {
    expect(signInAddress("/dashboard/hub?connect=hub.tailed39ba.ts.net")).toBe(
      "/login?next=%2Fdashboard%2Fhub%3Fconnect%3Dhub.tailed39ba.ts.net",
    )
    expect(returnAfterSignIn("/dashboard/hub?connect=127.0.0.1%3A9641")).toBe(
      "/dashboard/hub?connect=127.0.0.1%3A9641",
    )
    // Only `connect` is kept; the page still treats it as a suggestion (`connectTarget`).
    expect(returnAfterSignIn("/dashboard/hub?connect=hub.tailed39ba.ts.net&x=1#y")).toBe(
      "/dashboard/hub?connect=hub.tailed39ba.ts.net",
    )
    expect(returnAfterSignIn("/dashboard/hub?connect=javascript:alert(1)")).toBe(
      "/dashboard/hub?connect=javascript%3Aalert(1)",
    )
  })

  test("any other address signs in to the Overview, as before", () => {
    expect(signInAddress("/docker")).toBe("/login")
    expect(signInAddress("/dashboard/hub")).toBe("/login")
    for (const next of [
      null,
      undefined,
      "",
      "/",
      "/dashboard/hub",
      "/dashboard/hub?connect=",
      "/dashboard/hubx?connect=hub.tailed39ba.ts.net",
      "/dashboard/hub/../../evil?connect=hub",
      "//evil.example/dashboard/hub?connect=hub",
      "/\\evil.example/dashboard/hub?connect=hub",
      "https://evil.example/dashboard/hub?connect=hub",
      `/dashboard/hub?connect=${"a".repeat(301)}`,
    ]) {
      expect(returnAfterSignIn(next)).toBeUndefined()
    }
  })
})

describe("the backend's reasons, said as sentences", () => {
  test("a capital first letter and a full stop, and nothing else changed", () => {
    expect(
      sentence(
        "the hub must be a machine on this server's tailnet, named like hub.tailed39ba.ts.net",
      ),
    ).toBe("The hub must be a machine on this server's tailnet, named like hub.tailed39ba.ts.net.")
    expect(sentence("Already one.")).toBe("Already one.")
    expect(sentence("  spaced out  ")).toBe("Spaced out.")
    expect(sentence("")).toBe("")
    // The hub's words quoted inside one stay exactly as the hub wrote them (§8.10).
    expect(sentence('the hub answered 403 nope. Just Hub (hub) says: "enter your password"')).toBe(
      'The hub answered 403 nope. Just Hub (hub) says: "enter your password".',
    )
    expect(sentence('Just Hub (hub) says: "why?"')).toBe('Just Hub (hub) says: "why?"')
  })

  test("a request that got no answer is said as what happened, with Go's words kept", () => {
    const host = "127.0.0.1:9641"
    const at = 'Post "http://127.0.0.1:9641/api/hub/v1/heartbeat": '
    for (const [why, said] of [
      ["dial tcp 127.0.0.1:9641: connect: connection refused", "the connection was refused"],
      [
        "dial tcp: lookup hub.tailed39ba.ts.net on 100.100.100.100:53: no such host",
        "its name did not resolve",
      ],
      [
        "context deadline exceeded (Client.Timeout exceeded while awaiting headers)",
        "it did not answer in time",
      ],
      ["dial tcp 100.64.0.9:443: connect: no route to host", "there is no network route to it"],
      [
        "tls: failed to verify certificate: x509: certificate signed by unknown authority",
        "its certificate was not accepted",
      ],
      ["EOF", "the connection closed before it answered"],
    ]) {
      expect(exchangeFailure(at + why, host)).toEqual({
        message: `Could not reach ${host}: ${said}.`,
        raw: at + why,
      })
    }
    expect(exchangeFailure(`${at}something new`, host)).toEqual({
      message: `Could not reach ${host}.`,
      raw: `${at}something new`,
    })
  })

  test("Connect's hub_unreachable names the hub first, and the host it tried is the one said", () => {
    const raw =
      'the hub at http://127.0.0.1:9641: Post "http://127.0.0.1:9641/api/hub/v1/requests": dial tcp 127.0.0.1:9641: connect: connection refused'
    expect(exchangeFailure(raw, "typed.example")).toEqual({
      message: "Could not reach 127.0.0.1:9641: the connection was refused.",
      raw,
    })
    // A hub that answered with something else is the backend's sentence, said as it is.
    const answered =
      "the hub at https://hub.tailed39ba.ts.net: it does not speak hub link protocol 1; update this dashboard or the hub so they speak the same one"
    expect(exchangeFailure(answered, "hub")).toEqual({ message: sentence(answered) })
    expect(exchangeFailure(answered, "hub").message).toStartWith("The hub at https://")
  })

  test("the backend's own sentences and the hub's quoted answers are said as they are", () => {
    expect(exchangeFailure("the heartbeat failed: the hub answered HTTP 503", "hub")).toEqual({
      message: "The heartbeat failed: the hub answered HTTP 503.",
    })
    const clock =
      "This server's clock and the hub's differ by more than 60 seconds; correct the time on this server."
    expect(exchangeFailure(clock, "hub")).toEqual({ message: clock })
  })
})
