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
