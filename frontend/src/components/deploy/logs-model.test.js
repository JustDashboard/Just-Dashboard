import { describe, expect, test } from "bun:test"
import {
  EMPTY_REQUEST_QUERY,
  completeQuery,
  containerAt,
  defaultView,
  foldRestarts,
  liveStack,
  loopSpan,
  orderedServices,
  queryAround,
  queryFromParams,
  queryParams,
  requestParams,
} from "./logs-model"

const NOW = Date.parse("2026-09-27T12:00:00Z")

describe("the view a deployment opens on", () => {
  test("requests for a routed site, what it wrote for anything without a route", () => {
    expect(defaultView("web", true)).toBe("requests")
    // Unknown until the domains are read: the request record is the better guess.
    expect(defaultView("web", undefined)).toBe("requests")
    expect(defaultView("web", false)).toBe("output")
    expect(defaultView("game", true)).toBe("output")
    expect(defaultView("worker", undefined)).toBe("output")
  })
})

describe("the request record's question", () => {
  test("a preset is resolved when it is asked, not when it was chosen", () => {
    const hour = requestParams(EMPTY_REQUEST_QUERY, NOW)
    expect(hour.since).toBe("2026-09-27T11:00:00.000Z")
    // Half an hour later the same query asks about a later hour.
    expect(requestParams(EMPTY_REQUEST_QUERY, NOW + 30 * 60_000).since).toBe(
      "2026-09-27T11:30:00.000Z",
    )
    expect(hour.until).toBeUndefined()
  })

  test("carries every narrowing the API reads, and nothing it does not", () => {
    const params = requestParams(
      completeQuery({
        range: "custom",
        since: "2026-09-27T10:00:00Z",
        until: "2026-09-27T10:30:00Z",
        agent: "Googlebot",
        referer: "www.google.com",
        minMs: 250,
        maxMs: 1000,
        statuses: [404, 500],
      }),
      NOW,
    )
    expect(params).toMatchObject({
      since: "2026-09-27T10:00:00Z",
      until: "2026-09-27T10:30:00Z",
      agent: "Googlebot",
      referer: "www.google.com",
      minMs: 250,
      maxMs: 1000,
      status: "404,500",
    })
    expect(params.path).toBeUndefined()
  })

  test("an older kept query gains the narrowings it predates, empty", () => {
    const old = completeQuery({ range: "6h", path: "/api" })
    expect(old.agent).toBe("")
    expect(old.referer).toBe("")
    expect(old.path).toBe("/api")
  })
})

describe("the query in the address", () => {
  test("at rest the address says nothing", () => {
    expect(queryParams(EMPTY_REQUEST_QUERY)).toEqual([])
    expect(queryFromParams(new URLSearchParams(""))).toBeUndefined()
    // Words that are not the query's leave the remembered one standing.
    expect(queryFromParams(new URLSearchParams("view=events&moment=x"))).toBeUndefined()
  })

  test("a narrowing survives the round trip", () => {
    const query = completeQuery({
      range: "custom",
      since: "2026-09-27T10:00:00Z",
      until: "2026-09-27T10:30:00Z",
      path: "/api/checkout",
      agent: "Chrome",
      statuses: [500],
      classes: ["5xx"],
      methods: ["POST"],
      maxMs: 800,
      pages: true,
    })
    const back = queryFromParams(new URLSearchParams(queryParams(query)))
    expect(back).toEqual(query)
  })

  test("what the server would refuse is dropped, not sent", () => {
    const query = queryFromParams(
      new URLSearchParams(
        "range=forever&status=500,abc,999&classes=5xx,9xx&methods=post,DROP TABLE&minMs=-4&since=nope",
      ),
    )
    expect(query?.range).toBe("1h")
    expect(query?.statuses).toEqual([500])
    expect(query?.classes).toEqual(["5xx"])
    expect(query?.methods).toEqual(["POST"])
    expect(query?.minMs).toBeUndefined()
    // A custom range with no start is no range at all.
    expect(queryFromParams(new URLSearchParams("range=custom"))?.range).toBe("1h")
  })

  test("a moment opens the hour around it, and not the minutes after now", () => {
    const early = queryAround("2026-09-27T09:00:00Z", NOW)
    expect(early).toMatchObject({
      range: "custom",
      since: "2026-09-27T08:30:00.000Z",
      until: "2026-09-27T09:30:00.000Z",
    })
    const fresh = queryAround("2026-09-27T11:55:00Z", NOW)
    expect(fresh?.until).toBe("2026-09-27T12:00:00.000Z")
    expect(queryAround("not a time", NOW)).toBeUndefined()
  })
})

const service = (fields) => ({
  releaseId: 20,
  liveRelease: true,
  state: "running",
  health: "healthy",
  imageId: "sha256:x",
  ...fields,
})

describe("which container answered", () => {
  const web20 = service({ containerId: "w20", name: "shop-r20-web", service: "web", stack: "r20" })
  const db20 = service({ containerId: "d20", name: "shop-r20-db", service: "db", stack: "r20" })
  const web19 = service({
    containerId: "w19",
    name: "shop-r19-web",
    service: "web",
    releaseId: 19,
    liveRelease: false,
    stack: "r19",
  })
  const releases = [
    { id: 19, activatedAt: "2026-09-27T09:00:00Z" },
    { id: 20, activatedAt: "2026-09-27T11:00:00Z" },
  ]

  test("the release that had gone live by then, its primary service first", () => {
    const services = [db20, web20, web19]
    const lead = { primary: "web" }
    expect(containerAt(services, releases, Date.parse("2026-09-27T10:00:00Z"), lead)).toBe(web19)
    expect(containerAt(services, releases, Date.parse("2026-09-27T11:30:00Z"), lead)).toBe(web20)
  })

  test("before every release, or once its containers are gone, the live one", () => {
    expect(
      containerAt([db20, web20], releases, Date.parse("2026-09-27T10:00:00Z"), { primary: "web" }),
    ).toBe(web20)
    expect(containerAt([web20], releases, Date.parse("2026-09-27T08:00:00Z"))).toBe(web20)
    expect(containerAt([], releases, NOW)).toBeUndefined()
  })

  test("the picker lists the live release first and offers its stack only when it has several", () => {
    expect(
      orderedServices([web19, db20, web20], { primary: "web" }).map((s) => s.containerId),
    ).toEqual(["w20", "d20", "w19"])
    // With no primary named, the project's own image leads its database,
    // which comes first by name.
    const own = (s) => s.service === "web"
    expect(orderedServices([db20, web20], { own }).map((s) => s.containerId)).toEqual([
      "w20",
      "d20",
    ])
    expect(orderedServices([web20, db20]).map((s) => s.containerId)).toEqual(["d20", "w20"])
    expect(liveStack([web19, db20, web20])).toBe("r20")
    expect(liveStack([web20, web19])).toBeUndefined()
  })
})

const at = (minute, second = 0) =>
  `2026-09-27T11:${String(minute).padStart(2, "0")}:${String(second).padStart(2, "0")}Z`
const ev = (action, time, fields = {}) => ({
  type: "container",
  action,
  time,
  name: "api-r20",
  id: "c0ffee",
  message: `api-r20 ${action}`,
  level: action === "die" ? "error" : "info",
  source: "daemon",
  ...fields,
})
const keyOf = (event) => `${event.time}|${event.action}`

describe("a crash loop", () => {
  test("pairs of an exit and the start after it fold into one row", () => {
    // Newest first, as the feed holds them: down now, after three restarts.
    const events = [
      ev("die", at(9), { exitCode: "1" }),
      ev("start", at(8)),
      ev("die", at(7, 50), { exitCode: "1" }),
      ev("start", at(6)),
      ev("die", at(5, 55), { exitCode: "1" }),
      ev("start", at(4)),
      ev("die", at(3), { exitCode: "1" }),
      ev("create", at(1)),
    ]
    const items = foldRestarts(events, keyOf)
    expect(items.map((item) => item.kind)).toEqual(["event", "loop", "event"])
    const loop = items[1]
    expect(loop.restarts).toBe(3)
    expect(loop.exitCode).toBe("1")
    expect(loop.exit).toBe(events[2])
    expect(loopSpan(loop.spanMs)).toBe("in 5 min")
  })

  test("one restart stays two rows, and a different exit ends the loop", () => {
    expect(
      foldRestarts([ev("start", at(8)), ev("die", at(7), { exitCode: "137" })], keyOf).map(
        (item) => item.kind,
      ),
    ).toEqual(["event", "event"])
    const mixed = foldRestarts(
      [
        ev("start", at(8)),
        ev("die", at(7, 30), { exitCode: "1" }),
        ev("start", at(7)),
        ev("die", at(6, 59), { exitCode: "1" }),
        ev("start", at(6)),
        ev("die", at(5), { exitCode: "137" }),
      ],
      keyOf,
    )
    expect(mixed.map((item) => item.kind)).toEqual(["loop", "event", "event"])
    expect(loopSpan(mixed[0].spanMs)).toBe("in 1 min")
    expect(loopSpan(20_000)).toBe("in under a minute")
  })

  test("another container's events are not part of this one's loop", () => {
    const items = foldRestarts(
      [
        ev("start", at(8)),
        ev("die", at(7), { exitCode: "1", id: "other", name: "db" }),
        ev("start", at(6), { id: "other", name: "db" }),
        ev("die", at(5), { exitCode: "1" }),
      ],
      keyOf,
    )
    expect(items.every((item) => item.kind === "event")).toBe(true)
  })
})
