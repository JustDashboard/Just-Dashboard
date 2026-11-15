import { describe, expect, test } from "bun:test"
import {
  hourWords,
  legacyStreamHref,
  lengthWords,
  restrictionWords,
  routeLine,
  sessionsWords,
  streamHref,
  streamPicture,
  streamTab,
  streamTraits,
  streamsVerdict,
  upstreamsOf,
} from "./stream-facts"

const entry = (over = {}) => ({
  name: "db",
  listen: 5432,
  protocol: "tcp",
  upstream: "10.0.0.5:5432",
  proxyProtocol: false,
  allowFrom: [],
  path: "/etc/nginx/streams/db.conf",
  managed: true,
  open: true,
  unsupported: [],
  state: "live",
  ...over,
})

const status = (streams, over = {}) => ({
  included: true,
  module: { state: "loaded", usable: true },
  snippet: "",
  dir: "/etc/nginx/streams",
  streams,
  ...over,
})

describe("addresses", () => {
  test("a stream's page leaves the overview out of the address", () => {
    expect(streamHref("db")).toBe("/proxy/streams/db")
    expect(streamHref("db", "overview")).toBe("/proxy/streams/db")
    expect(streamHref("a b", "settings")).toBe("/proxy/streams/a%20b?tab=settings")
  })
  test("an unknown view is the overview", () => {
    expect(streamTab("clients")).toBe("clients")
    expect(streamTab("nope")).toBe("overview")
    expect(streamTab(null)).toBe("overview")
  })
  test("an old ?stream= link lands on Settings only where the form could edit it", () => {
    expect(legacyStreamHref("db", entry(), true)).toBe("/proxy/streams/db?tab=settings")
    expect(legacyStreamHref("db", entry(), false)).toBe("/proxy/streams/db")
    expect(legacyStreamHref("db", entry({ paused: true }), true)).toBe("/proxy/streams/db")
    expect(legacyStreamHref("db", entry({ unsupported: ["reuseport"] }), true)).toBe(
      "/proxy/streams/db",
    )
    expect(legacyStreamHref("db", undefined, true)).toBe("/proxy/streams/db")
  })
})

describe("the row's words", () => {
  test("a pool reads as its first server and how many more", () => {
    const pool = entry({
      servers: [{ address: "10.0.0.5:5432" }, { address: "10.0.0.6:5432" }],
      routes: [{ name: "a.example.com", upstream: "10.0.0.7:443" }],
    })
    expect(upstreamsOf(pool)).toEqual(["10.0.0.5:5432", "10.0.0.6:5432", "10.0.0.7:443"])
    expect(routeLine(pool)).toBe("5432 → 10.0.0.5:5432 +2")
    expect(routeLine(entry({ address: "127.0.0.1" }))).toBe("127.0.0.1:5432 → 10.0.0.5:5432")
  })
  test("a file that could not be read names its path", () => {
    expect(routeLine(entry({ error: "permission denied" }))).toBe("/etc/nginx/streams/db.conf")
    expect(upstreamsOf(entry({ error: "x" }))).toEqual([])
  })
  test("open to anyone is a danger on a database port and a warning elsewhere", () => {
    expect(restrictionWords(entry())).toEqual({ label: "PostgreSQL to anyone", tone: "danger" })
    expect(restrictionWords(entry({ listen: 2222 }))).toEqual({ label: "anyone", tone: "warning" })
    expect(restrictionWords(entry({ open: false, allowFrom: ["10.0.0.0/8"] }))).toEqual({
      label: "10.0.0.0/8",
      mono: true,
    })
    expect(
      restrictionWords(
        entry({ open: false, rules: [{ action: "deny", source: "1.2.3.4" }], defaultAllow: false }),
      ).label,
    ).toBe("deny 1.2.3.4, then deny the rest")
  })
  test("the traits name what the form set", () => {
    expect(
      streamTraits(entry({ protocol: "udp", udpMode: "request", timeout: 600, managed: false })),
    ).toEqual(["UDP forwarding", "one reply per session", "10m idle timeout", "written by hand"])
  })
  test("the hour is a count, with a floor marked", () => {
    expect(hourWords(undefined)).toBeUndefined()
    expect(hourWords({ sessions: 1420, denied: 0, bytes: 1, complete: false })).toBe(
      "1,420+ sessions/h",
    )
  })
})

describe("the verdict", () => {
  test("nothing to say about an empty list", () => {
    expect(streamsVerdict(status([]), [])).toBeNull()
  })
  test("a stream that stops every reload outranks one that is merely not live", () => {
    const held = entry({
      state: "not-listening",
      blocker: { port: 5432, proto: "tcp", kind: "program" },
    })
    const quiet = entry({ name: "q", state: "not-listening" })
    expect(streamsVerdict(status([held, quiet]), [held, quiet])).toEqual({
      tone: "danger",
      label: "1 stops every reload",
      chip: "not-live",
    })
    expect(streamsVerdict(status([quiet, entry()]), [quiet, entry()])).toEqual({
      tone: "warning",
      label: "1 not live",
      chip: "not-live",
    })
  })
  test("a paused stream is not counted against the rest", () => {
    const paused = entry({ name: "p", state: "paused", paused: true })
    expect(streamsVerdict(status([entry()]), [entry(), paused])).toEqual({
      tone: "running",
      label: "live",
    })
  })
  test("a directory nginx does not read is said once", () => {
    expect(streamsVerdict(status([entry()], { included: false }), [entry()])?.label).toBe(
      "not read by nginx",
    )
  })
})

describe("sessions in words", () => {
  test("a floor is said, and length is never p50", () => {
    expect(sessionsWords({ sessions: 142, complete: true })).toBe("142 sessions")
    expect(sessionsWords({ sessions: 1, complete: false })).toBe("1 or more session")
    const words = lengthWords({ p50: 2.4, p90: 31, p99: 410, max: 7300 })
    expect(words.reading).toBe("half inside 2.4 s")
    expect(words.hint).toBe("9 in 10 inside 31.0 s · 99 in 100 inside 6m 50s · longest 2h 1m")
    expect(`${words.reading} ${words.hint}`).not.toMatch(/p\d\d/)
  })
})

describe("the picture", () => {
  test("open is amber, a database port red, a port nginx does not hold dashed", () => {
    const pic = streamPicture(
      [
        entry(),
        entry({ name: "ssh", listen: 2222, upstream: "10.0.0.9:22" }),
        entry({
          name: "held",
          listen: 7000,
          open: false,
          state: "not-listening",
          upstream: "10.0.0.5:5432",
        }),
      ],
      { db: { sessions: 3, denied: 0, bytes: 900, complete: true } },
    )
    const port = (name) => pic.edges.find((e) => e.from === `p:${name}`)
    expect(port("db")).toMatchObject({ tone: "danger", dashed: false, moving: true })
    expect(port("ssh")).toMatchObject({ tone: "warning", dashed: false, moving: false })
    expect(port("held")).toMatchObject({ tone: "danger", dashed: true, moving: false })
    // Two streams reach 10.0.0.5:5432; it is up and carries while db does.
    expect(pic.upstreams.map((u) => u.address)).toEqual(["10.0.0.5:5432", "10.0.0.9:22"])
    expect(pic.edges.find((e) => e.to === "u:10.0.0.5:5432")).toMatchObject({
      tone: "default",
      dashed: false,
      moving: true,
    })
  })
  test("an unreadable file is not drawn, and past the cap the rest are counted", () => {
    const many = Array.from({ length: 10 }, (_, i) => entry({ name: `s${i}`, listen: 7000 + i }))
    const pic = streamPicture([entry({ name: "bad", error: "x" }), ...many], undefined, 8)
    expect(pic.ports).toHaveLength(8)
    expect(pic.hidden).toBe(3)
  })
})
