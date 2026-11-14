import { expect, test } from "bun:test"
import {
  blocklistRequest,
  choiceWord,
  countriesWord,
  inRange,
  limitSentence,
  settingGroup,
  stagedChanges,
} from "./reading"

const limit = (fields) => ({
  id: 1,
  name: "SSH",
  protocol: "tcp",
  ports: "22",
  rate: 10,
  per: "minute",
  burst: 0,
  perSource: true,
  maxConnections: 0,
  action: "drop",
  enabled: true,
  packets: 0,
  bytes: 0,
  ...fields,
})

test("a limit is said as the sentence its row carries", () => {
  expect(limitSentence(limit({}))).toBe("22/tcp · 10 new connections a minute per address · drop")
  expect(limitSentence(limit({ rate: 1, per: "second", perSource: false, burst: 20 }))).toBe(
    "22/tcp · 1 new connection a second, bursts of 20 · drop",
  )
  expect(
    limitSentence(limit({ rate: 0, per: "", protocol: "both", ports: "53", maxConnections: 50 })),
  ).toBe("53/tcp+udp · at most 50 open per address · drop")
})

test("a country list is named by its countries and counted past four", () => {
  expect(countriesWord(["cn", "ru"])).toBe("China · Russia")
  expect(countriesWord(["cn", "ru", "kp", "ir", "sy"])).toBe(
    "China · Russia · North Korea · Iran · +1",
  )
})

test("a feed saved unchanged names its preset, and a custom one its address", () => {
  const presets = [
    { id: "spamhaus-drop", name: "Spamhaus DROP", url: "https://s.example/drop", description: "" },
  ]
  const list = {
    id: 2,
    name: "Spamhaus DROP",
    kind: "feed",
    countries: [],
    url: "https://s.example/drop",
    entries: [],
    enabled: true,
  }
  expect(blocklistRequest(list, presets)).toEqual({
    name: "Spamhaus DROP",
    kind: "feed",
    entries: [],
    countries: [],
    url: "",
    preset: "spamhaus-drop",
  })
  expect(
    blocklistRequest({ ...list, url: "https://other.example/x" }, presets, false),
  ).toMatchObject({
    url: "https://other.example/x",
    preset: "",
    enabled: false,
  })
})

const setting = (fields) => ({
  key: "net.ipv4.tcp_syncookies",
  kind: "choice",
  allowed: ["0", "1"],
  min: 0,
  max: 0,
  current: "1",
  available: true,
  ...fields,
})

test("only a setting whose staged value differs from the running one is a change", () => {
  const settings = [
    setting({}),
    setting({ key: "net.ipv4.tcp_max_syn_backlog", kind: "number", current: "1024" }),
    setting({ key: "net.ipv6.conf.all.accept_redirects", current: "1", available: false }),
  ]
  expect(
    stagedChanges(settings, {
      "net.ipv4.tcp_syncookies": "1",
      "net.ipv4.tcp_max_syn_backlog": "4096",
      "net.ipv6.conf.all.accept_redirects": "0",
      "net.unknown": "1",
    }),
  ).toEqual({ "net.ipv4.tcp_max_syn_backlog": "4096" })
})

test("a number must be a whole number inside the setting's range", () => {
  const s = setting({ kind: "number", min: 1, max: 5 })
  expect(inRange(s, "3")).toBe(true)
  for (const bad of ["", "0", "6", "2.5", "-1", "abc"]) expect(inRange(s, bad)).toBe(false)
  expect(inRange(setting({}), "anything")).toBe(true)
})

test("zero and one are off and on, except where one would be strict", () => {
  expect(choiceWord("net.ipv4.tcp_syncookies", "1")).toBe("On")
  expect(choiceWord("net.ipv4.conf.all.rp_filter", "2")).toBe("Loose")
  expect(choiceWord("net.ipv4.conf.all.rp_filter", "0")).toBe("Off")
})

test("every setting the server offers has a group, and a stranger goes last", () => {
  expect(settingGroup("net.netfilter.nf_conntrack_max")).toBe("Connection floods")
  expect(settingGroup("net.ipv4.conf.all.rp_filter")).toBe("Spoofed addresses")
  expect(settingGroup("net.ipv4.something_new")).toBe("Other settings")
})

test("a connection ceiling is always per address, and the one for everyone is said beside it", async () => {
  const { ceilingWord } = await import("./reading")
  expect(ceilingWord(limit({ maxConnections: 50, perSource: false }))).toBe(
    "at most 50 open per address",
  )
  expect(ceilingWord(limit({ maxConnections: 20, globalConnections: 200 }))).toBe(
    "at most 20 open per address, 200 open for everyone",
  )
  expect(ceilingWord(limit({ globalConnections: 300 }))).toBe("at most 300 open for everyone")
  expect(ceilingWord(limit({}))).toBeUndefined()
})

test("a fetched list says its schedule, failures and what its last change did", async () => {
  const { refreshWord, diffWord, coverageWord } = await import("./reading")
  const until = () => "in 3h"
  expect(refreshWord({ kind: "feed", refresh: "6h", nextRefresh: "x" }, until)).toBe(
    "every 6 hours · next fetch in 3h",
  )
  expect(refreshWord({ kind: "feed", refresh: "", failures: 2, stale: true }, until)).toBe(
    "daily · failing (2 tries) · stale",
  )
  expect(refreshWord({ kind: "feed", refresh: "manual", nextRefresh: null }, until)).toBe(
    "only when refreshed by hand",
  )
  expect(refreshWord({ kind: "manual" }, until)).toBeUndefined()
  expect(diffWord({ added: 12, removed: 3, baseline: true })).toBe("+12 −3 at the last change")
  expect(diffWord({ added: 0, removed: 0, baseline: true })).toBe("unchanged at the last fetch")
  expect(diffWord({ added: 40, removed: 0, baseline: false })).toBe(
    "40 networks, nothing to compare with",
  )
  expect(
    coverageWord({
      ipv4Addresses: 2_100_000,
      ipv4Share: 0.0005,
      ipv6Slash48s: 1204,
      ipv4Networks: 9,
      ipv6Networks: 3,
    }),
  ).toBe("2.1M IPv4 addresses (0.05% of IPv4) · 1.2K IPv6 /48s")
})

test("expiries, profiles and recorded series become what the server and charts take", async () => {
  const { expiryFrom, profileChanges, limitFromProfile, seriesRows, trustedNoteWord } =
    await import("./reading")
  const now = new Date("2026-10-09T12:00:00.000Z")
  expect(expiryFrom("24h", now)).toBe("2026-10-10T12:00:00Z")
  expect(expiryFrom("", now)).toBe("")
  const settings = [
    { key: "a", available: true, current: "1" },
    { key: "b", available: true, current: "0" },
    { key: "c", available: false, current: "" },
  ]
  expect(profileChanges(settings, { values: { a: "1", b: "1", c: "1" } })).toEqual({ b: "1" })
  expect(
    limitFromProfile({
      name: "SSH",
      protocol: "tcp",
      ports: "22",
      rate: 6,
      per: "minute",
      burst: 4,
      perSource: true,
      maxConnections: 10,
      globalConnections: 0,
      action: "drop",
    }),
  ).toEqual({
    name: "SSH",
    protocol: "tcp",
    ports: "22",
    rate: "6",
    per: "minute",
    burst: "4",
    perSource: true,
    max: "10",
    global: "",
    action: "drop",
  })
  expect(
    seriesRows(
      { "conntrack:count": [{ t: 60, value: 5 }], "conntrack:max": [{ t: 60, value: 9 }] },
      ["conntrack:count", "conntrack:max"],
    ),
  ).toEqual([{ ts: 60_000, "conntrack:count": 5, "conntrack:max": 9 }])
  expect(
    trustedNoteWord(
      { origin: "kept", reason: "office", addedBy: "ion", expiresAt: "x", lastSeen: null },
      () => "tomorrow",
    ),
  ).toBe("office · kept by ion · until tomorrow · no sign-in seen in 90 days")
})

const blocklist = (fields) => ({
  id: 1,
  name: "Feed",
  kind: "feed",
  countries: [],
  url: "https://lists.example/drop.txt",
  entries: [],
  enabled: true,
  refreshed: "then",
  count: 10,
  error: "",
  containsYou: false,
  packets: 0,
  bytes: 0,
  ...fields,
})

test("a row says the worst thing true of its list, and one thing only", async () => {
  const { listState } = await import("./reading")
  const word = (fields) => listState(blocklist(fields)).label
  expect(word({})).toBe("In force")
  expect(word({ enabled: false })).toBe("Off")
  expect(word({ integrity: "signed" })).toBe("Signed")
  expect(word({ integrity: "signed", enforcement: "verified" })).toBe("Verified set")
  expect(word({ integrity: "signed", stale: true })).toBe("Stale")
  expect(word({ stale: true, enforcement: "degraded" })).toBe("Degraded enforcement")
  expect(word({ enforcement: "unknown" })).toBe("Enforcement unknown")
  expect(word({ enforcement: "degraded", containsYou: true })).toBe("Holds your address")
  expect(word({ containsYou: true, stale: true, error: "the feed answered 503" })).toBe(
    "Fetch failed",
  )
  // A list that is off is not stale or degraded: nothing of it is in force.
  expect(word({ enabled: false, stale: true, enforcement: "degraded" })).toBe("Off")
  // Only the two the switch already says are left out of a narrow row.
  expect(listState(blocklist({})).plain).toBe(true)
  expect(listState(blocklist({ enabled: false })).plain).toBe(true)
  expect(listState(blocklist({ stale: true })).plain).toBeUndefined()
  expect(listState(blocklist({ error: "x" })).tone).toBe("danger")
})

test("what a row has no room for is listed in the list's editor", async () => {
  const { listFacts, coverageShort, fetchWords } = await import("./reading")
  const coverage = {
    ipv4Addresses: 412_000_000,
    ipv4Share: 0.0959,
    ipv6Slash48s: 0,
    ipv4Networks: 8400,
    ipv6Networks: 0,
  }
  const when = () => "5h ago"
  expect(
    listFacts(
      blocklist({
        coverage,
        refresh: "6h",
        nextRefresh: "soon",
        integrity: "signed",
        enforcement: "verified",
        lastDiff: { added: 12, removed: 3, baseline: true },
      }),
      [],
      when,
    ),
  ).toEqual([
    "lists.example",
    "10 networks",
    "412M IPv4 addresses (9.59% of IPv4)",
    "fetched 5h ago",
    "every 6 hours · next fetch 5h ago",
    "+12 −3 at the last change",
    "signed",
    "verified set",
  ])
  expect(listFacts(blocklist({ kind: "manual", entries: ["a"], count: 1 }), [], when)).toEqual([
    "1 address typed",
    "1 network",
  ])
  expect(coverageShort(coverage)).toBe("412M IPv4 addresses (9.59%)")
  expect(coverageShort(undefined)).toBeUndefined()
  expect(fetchWords(blocklist({ refresh: "24h", failures: 3, nextRefresh: "x" }), when)).toEqual({
    fetched: "daily · fetched 5h ago",
    next: "3 failed · next 5h ago",
  })
  // The page's clock says "3h from now"; the column says "in 3h".
  expect(
    fetchWords(blocklist({ refresh: "manual", nextRefresh: "x" }), () => "3h from now"),
  ).toEqual({ fetched: "by hand · fetched 3h from now", next: "next in 3h" })
  // A list that is off is not fetched on a schedule, so it names no next fetch.
  expect(
    fetchWords(blocklist({ refreshed: null, enabled: false, nextRefresh: "x" }), when),
  ).toEqual({ fetched: "daily · not fetched yet", next: "" })
  expect(fetchWords(blocklist({ kind: "manual" }), when)).toBeUndefined()
})

test("a row names a list by what it is read from", async () => {
  const { listSource } = await import("./reading")
  expect(listSource(blocklist({ kind: "country", countries: ["cn", "ru"] }))).toBe("China · Russia")
  expect(listSource(blocklist({}))).toBe("lists.example")
  expect(listSource(blocklist({ url: "not an address" }))).toBe("not an address")
  expect(listSource(blocklist({ kind: "manual", entries: ["a", "b"] }))).toBe("2 addresses typed")
})

test("the page's totals count what is in force and add what every entry counted", async () => {
  const { protectionTotals, protectionCounters } = await import("./reading")
  const view = {
    blocklists: [
      blocklist({ id: 1, count: 100, packets: 7, bytes: 70, total: { packets: 50, since: "b" } }),
      blocklist({ id: 2, count: 5, packets: 3, bytes: 30, error: "x", total: { since: "a" } }),
      blocklist({ id: 3, count: 9000, enabled: false }),
    ],
    limits: [limit({ id: 1, packets: 4, bytes: 40 }), limit({ id: 2, enabled: false })],
    exceptions: [
      { id: 1, expired: false, packets: 2 },
      { id: 2, expired: true, packets: 1 },
    ],
  }
  expect(protectionTotals(view)).toMatchObject({
    lists: 3,
    listsOn: 2,
    // A list that is off blocks nothing, so its networks are not counted.
    networks: 105,
    failing: 1,
    dropped: 10,
    droppedBytes: 100,
    droppedEver: 50,
    droppedSince: "a",
    limits: 2,
    limitsOn: 1,
    refused: 4,
    refusedBytes: 40,
    refusedSince: undefined,
    exceptions: 2,
    exceptionsLive: 1,
    letThrough: 3,
    inForce: true,
  })
  expect(protectionTotals({ blocklists: [], limits: [] }).inForce).toBe(false)
  expect(protectionCounters(view)).toEqual({
    "list:1": 7,
    "list:2": 3,
    "list:3": 0,
    "limit:1": 4,
    "limit:2": 0,
    exceptions: 3,
  })
})

test("a limit, an exception and a trusted address each have their short word", async () => {
  const { limitShort, metersWord, exceptionState, trustedState } = await import("./reading")
  expect(limitShort(limit({}))).toBe("10 a minute per address")
  expect(limitShort(limit({ rate: 0, maxConnections: 200, globalConnections: 5000 }))).toBe(
    "200 open per address",
  )
  expect(limitShort(limit({ rate: 0, globalConnections: 5000 }))).toBe("5,000 open for everyone")
  expect(limitShort(limit({ rate: 0 }))).toBeUndefined()
  expect(metersWord({ meters: { rateSources: 41, connSources: null, capacity: 65535 } })).toBe(
    "tracking 41 sources",
  )
  expect(metersWord({ meters: { rateSources: 1, connSources: 9, capacity: 10 } })).toBe(
    "tracking 9 sources (90% of its meter)",
  )
  expect(metersWord({ meters: { rateSources: null, connSources: null, capacity: 10 } })).toBe(
    undefined,
  )
  expect(metersWord({})).toBeUndefined()
  const when = () => "in 6h"
  expect(exceptionState({ expiresAt: null, expired: false }, when)).toEqual({
    label: "Until removed",
    tone: "running",
  })
  expect(exceptionState({ expiresAt: "x", expired: false }, when).label).toBe("Ends in 6h")
  expect(exceptionState({ expiresAt: "x", expired: true }, when)).toEqual({
    label: "Ended in 6h",
    tone: "warning",
  })
  expect(trustedState({})).toBeUndefined()
  expect(trustedState({ stale: true }).label).toBe("Stale")
  expect(trustedState({ stale: true, expired: true }).label).toBe("Expired")
})

test("saving a trusted address's note keeps the end it already has", async () => {
  const { trustedExpiry, trustedExpiryChoice, KEEP_EXPIRY } = await import("./reading")
  const now = new Date("2026-11-13T12:00:00.000Z")
  const ending = { expiresAt: "2026-11-20T12:00:00Z", expired: false }
  // The form opens on the end the address has, and sends it back untouched.
  expect(trustedExpiryChoice(ending)).toBe(KEEP_EXPIRY)
  expect(trustedExpiry(ending, KEEP_EXPIRY, now)).toBe("2026-11-20T12:00:00Z")
  // Another choice is counted from now; "never" is the empty the server takes.
  expect(trustedExpiry(ending, "24h", now)).toBe("2026-11-14T12:00:00Z")
  expect(trustedExpiry(ending, "never", now)).toBe("")
  // An address with no end, or one whose end has passed, has nothing to keep.
  expect(trustedExpiryChoice({ expiresAt: null })).toBe("never")
  expect(trustedExpiryChoice({ expiresAt: "2026-11-01T00:00:00Z", expired: true })).toBe("never")
  expect(trustedExpiry({ expiresAt: "2026-11-01T00:00:00Z", expired: true }, KEEP_EXPIRY)).toBe("")
})

test("a kernel setting takes a switch only while its value is off or on", async () => {
  const { settingControl } = await import("./reading")
  const onOff = { kind: "choice", allowed: ["0", "1"] }
  expect(settingControl(onOff, "0")).toBe("switch")
  expect(settingControl(onOff, "1")).toBe("switch")
  // Found at a third value, a switch would say off or on and be wrong.
  expect(settingControl(onOff, "2")).toBe("segments")
  expect(settingControl({ kind: "choice", allowed: ["0", "2"] }, "2")).toBe("segments")
  expect(settingControl({ kind: "number", allowed: [] }, "1024")).toBe("number")
})
