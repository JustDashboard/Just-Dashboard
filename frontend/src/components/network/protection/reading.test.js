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
