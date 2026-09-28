import { describe, expect, test } from "bun:test"
import { newExposureFindings } from "./findings/ports-history"
import { foldDualStack } from "./ports"
import {
  changeAddresses,
  changesByDay,
  changeWhen,
  dayLabel,
  firstSeen,
  foldChanges,
  heldFor,
  isNew,
  NEW_FOR_MS,
  seenWords,
  tallyChanges,
} from "./ports-history"

// Local noon, so "today" and "yesterday" never straddle midnight in any zone.
const NOW = new Date(2026, 8, 28, 12, 0, 0).getTime()
const ago = (minutes) => new Date(NOW - minutes * 60_000).toISOString()

const socket = (overrides) => ({
  protocol: "tcp",
  family: "ipv4",
  address: "0.0.0.0",
  port: 6379,
  pid: 2000,
  process: "redis-server",
  cmdline: "redis-server *:6379",
  user: "redis",
  scope: "all",
  reach: "all",
  network: "all",
  exposed: true,
  service: "Redis",
  danger: true,
  ...overrides,
})

const event = (overrides) =>
  socket({ kind: "opened", at: ago(10), after: ago(11), since: ago(10), ...overrides })

describe("new sockets", () => {
  test("a socket first seen within the day is new, one older or undated is not", () => {
    expect(isNew({ firstSeen: ago(10) }, NOW)).toBe(true)
    expect(isNew({ firstSeen: new Date(NOW - NEW_FOR_MS + 60_000).toISOString() }, NOW)).toBe(true)
    expect(isNew({ firstSeen: new Date(NOW - NEW_FOR_MS).toISOString() }, NOW)).toBe(false)
    expect(isNew({}, NOW)).toBe(false)
    expect(isNew({ firstSeen: "not a time" }, NOW)).toBe(false)
  })

  test("a folded pair is as new as its newer half", () => {
    expect(firstSeen({ firstSeen: ago(3000), twin: { firstSeen: ago(10) } })).toBe(ago(10))
    // A service that was listening in one family when recording began and
    // added the other today changed today.
    expect(isNew({ twin: { firstSeen: ago(10) } }, NOW)).toBe(true)
    expect(firstSeen({ firstSeen: ago(10), twin: {} })).toBe(ago(10))
  })

  test("the minute is said as today's or yesterday's", () => {
    const today = new Date(2026, 8, 28, 9, 5).toISOString()
    const yesterday = new Date(2026, 8, 27, 21, 10).toISOString()
    expect(seenWords(today, NOW)).toBe("at 09:05")
    expect(seenWords(yesterday, NOW)).toBe("yesterday at 21:10")
  })
})

describe("foldChanges", () => {
  test("a service's two families changing together are one line", () => {
    const changes = foldChanges([
      event({ family: "ipv4", address: "0.0.0.0" }),
      event({ family: "ipv6", address: "::" }),
    ])
    expect(changes).toHaveLength(1)
    expect(changes[0].kind).toBe("opened")
    expect(changeAddresses(changes[0])).toEqual(["0.0.0.0", "::"])
  })

  test("Docker's two docker-proxy processes for one published port fold", () => {
    const proxy = (family, address, pid) =>
      event({
        family,
        address,
        pid,
        port: 8080,
        process: "docker-proxy",
        user: "root",
        cmdline: `/usr/bin/docker-proxy -proto tcp -host-ip ${address} -host-port 8080 -container-ip 172.17.0.2 -container-port 80`,
      })
    const changes = foldChanges([proxy("ipv4", "0.0.0.0", 501), proxy("ipv6", "::", 502)])
    expect(changes).toHaveLength(1)
    expect(changes[0].socket.twin.pid).toBe(502)
  })

  test("a closing and an opening of one socket in one sample are a new owner", () => {
    const changes = foldChanges([
      event({
        kind: "closed",
        port: 3000,
        address: "127.0.0.1",
        process: "node",
        pid: 1400,
        reach: "loopback",
        network: "loopback",
        scope: "loopback",
        exposed: false,
      }),
      event({
        kind: "opened",
        port: 3000,
        address: "127.0.0.1",
        process: "python3",
        pid: 1500,
        reach: "loopback",
        network: "loopback",
        scope: "loopback",
        exposed: false,
      }),
    ])
    expect(changes).toHaveLength(1)
    expect(changes[0].kind).toBe("replaced")
    expect(changes[0].socket.process).toBe("python3")
    expect(changes[0].previous.process).toBe("node")
  })

  test("the same socket closing in one sample and opening in the next stays two lines", () => {
    const changes = foldChanges([
      event({ kind: "opened", at: ago(5), after: ago(6), process: "python3" }),
      event({ kind: "closed", at: ago(10), after: ago(11), process: "redis-server" }),
    ])
    expect(changes.map((c) => c.kind)).toEqual(["opened", "closed"])
  })

  test("newest sample first, and by port within one", () => {
    const changes = foldChanges([
      event({ at: ago(1), after: ago(2), port: 8443 }),
      event({ at: ago(1), after: ago(2), port: 22, process: "sshd", kind: "closed" }),
      event({ at: ago(30), after: ago(31), port: 5432, process: "postgres" }),
    ])
    expect(changes.map((c) => c.socket.port)).toEqual([22, 8443, 5432])
  })

  test("the folded socket keeps what the list's fold keeps", () => {
    const folded = foldDualStack([event({}), event({ family: "ipv6", address: "::" })])
    expect(foldChanges([event({}), event({ family: "ipv6", address: "::" })])[0].socket).toEqual(
      folded[0],
    )
  })
})

describe("when a change happened", () => {
  test("a minute apart is the minute; further apart is the span nothing was sampled in", () => {
    const at = new Date(2026, 8, 28, 14, 2).toISOString()
    expect(changeWhen({ at, after: new Date(2026, 8, 28, 14, 1).toISOString() }, 60)).toEqual({
      time: "14:02",
    })
    expect(changeWhen({ at, after: new Date(2026, 8, 28, 9, 10).toISOString() }, 60)).toEqual({
      time: "14:02",
      span: "Sometime between 09:10 and 14:02, when nothing was sampled",
    })
  })

  test("a span across midnight names both days", () => {
    const when = changeWhen(
      {
        at: new Date(2026, 8, 28, 8, 0).toISOString(),
        after: new Date(2026, 8, 27, 23, 30).toISOString(),
      },
      60,
    )
    expect(when.span).toMatch(/^Sometime between .*27.* 23:30 and .*28.* 08:00, when/)
  })

  test("a closing says how long it listened", () => {
    const [change] = foldChanges([event({ kind: "closed", at: ago(0), since: ago(134) })])
    expect(heldFor(change)).toBe("open 2h 14m")
    const [early] = foldChanges([event({ kind: "closed", at: ago(0), since: ago(0) })])
    expect(heldFor(early)).toBe("open under a minute")
    const [baseline] = foldChanges([event({ kind: "closed", baseline: true })])
    expect(heldFor(baseline)).toBe("open since before recording began")
    expect(heldFor(foldChanges([event({})])[0])).toBeUndefined()
  })
})

describe("grouping and counting", () => {
  test("days read Today, Yesterday, then the date", () => {
    expect(dayLabel(ago(60), NOW)).toBe("Today")
    expect(dayLabel(new Date(2026, 8, 27, 23, 59).toISOString(), NOW)).toBe("Yesterday")
    expect(dayLabel(new Date(2026, 8, 25, 10, 0).toISOString(), NOW)).not.toMatch(/Today|Yesterday/)
    const days = changesByDay(
      foldChanges([
        event({ at: ago(5), after: ago(6) }),
        event({ at: new Date(2026, 8, 27, 20, 0).toISOString(), after: ago(2000), port: 22 }),
      ]),
      NOW,
    )
    expect(days.map(([day, lines]) => [day, lines.length])).toEqual([
      ["Today", 1],
      ["Yesterday", 1],
    ])
  })

  test("changes split by who could reach the socket", () => {
    const tally = tallyChanges(
      foldChanges([
        event({ port: 1 }),
        event({ port: 2, reach: "network", network: "tailnet", scope: "interface" }),
        event({
          port: 3,
          reach: "loopback",
          network: "loopback",
          scope: "loopback",
          exposed: false,
        }),
        event({
          port: 4,
          reach: "loopback",
          network: "loopback",
          scope: "loopback",
          exposed: false,
        }),
      ]),
    )
    expect(tally).toEqual({ all: 4, internet: 1, private: 1, local: 2 })
  })
})

describe("newExposureFindings", () => {
  const redis = socket({ level: "critical", firstSeen: new Date(2026, 8, 28, 9, 5).toISOString() })

  test("a database that began answering today is a finding at the posture's level", () => {
    expect(
      newExposureFindings(
        [redis, socket({ family: "ipv6", address: "::", level: "critical" })],
        NOW,
      ),
    ).toEqual([
      {
        id: "ports.new-exposure",
        level: "critical",
        title: "Redis began answering on every interface at 09:05",
        detail: "6379/tcp redis-server on every interface, first seen at 09:05",
        advice:
          "Nothing was listening there the sample before. If nobody meant to open it, stop the program or bind it to 127.0.0.1. The Changes list on the ports page shows when it opened and, with the search cleared, what else changed then.",
        meta: "ports",
        href: "/proxy/ports?q=port:6379",
      },
    ])
  })

  test("several are counted, each with where and when", () => {
    const postgres = socket({
      port: 5432,
      process: "postgres",
      address: "100.110.34.31",
      scope: "interface",
      reach: "network",
      network: "tailnet",
      level: "warning",
      firstSeen: new Date(2026, 8, 27, 21, 10).toISOString(),
    })
    const [finding] = newExposureFindings([postgres, redis], NOW)
    expect(finding.level).toBe("critical")
    expect(finding.title).toBe(
      "2 database or control ports began answering off this machine in the last day",
    )
    expect(finding.detail).toBe(
      "5432/tcp postgres on 100.110.34.31, first seen yesterday at 21:10; 6379/tcp redis-server on every interface, first seen at 09:05",
    )
    expect(finding.href).toBe("/proxy/ports?q=port:5432,6379")
  })

  test("nothing for a database that was there before, or a new port that is no database", () => {
    expect(newExposureFindings([socket({ level: "critical" })], NOW)).toEqual([])
    expect(
      newExposureFindings([socket({ level: "critical", firstSeen: ago(60 * 25) })], NOW),
    ).toEqual([])
    expect(newExposureFindings([socket({ port: 8080, firstSeen: ago(5) })], NOW)).toEqual([])
    // Loopback carries no level: the posture raises nothing for it.
    expect(
      newExposureFindings(
        [
          socket({
            address: "127.0.0.1",
            scope: "loopback",
            reach: "loopback",
            exposed: false,
            firstSeen: ago(5),
          }),
        ],
        NOW,
      ),
    ).toEqual([])
  })
})
