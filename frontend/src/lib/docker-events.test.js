import { expect, test } from "bun:test"
import {
  LAST_LINES,
  axisTicks,
  comebacks,
  containerActivity,
  dedupeEvents,
  foldBursts,
  foldRestarts,
  healthOf,
  lastLinesSearch,
  outcomeOf,
  perSlice,
  settleAskedExits,
  spanWords,
} from "./docker-events"

const base = Date.parse("2026-09-27T10:00:00Z")

/** A container event `s` seconds after ten o'clock, in the shape `/docker/events` sends. */
function ev(s, action, extra = {}) {
  const id = extra.id ?? "db1111111111"
  const name = extra.name ?? "shop-db-1"
  const exitCode = extra.exitCode
  return {
    time: new Date(base + s * 1000).toISOString(),
    type: extra.type ?? "container",
    action,
    name,
    id,
    stack: "shop",
    exitCode,
    message: `${name} ${action}`,
    level: action === "die" && exitCode && exitCode !== "0" ? "error" : "notice",
    source: "daemon",
  }
}

function shape(entries) {
  return entries.map((e) =>
    e.kind === "loop"
      ? `loop ${e.name} ×${e.times} exit ${e.exitCode ?? "-"}${e.oom ? " oom" : ""}`
      : `${e.event.name} ${e.event.action}${e.oom ? " +oom" : ""}`,
  )
}

test("a run of restarts that exit the same way is one entry, and the exit that ended it is its own", () => {
  const events = [
    ev(0, "start"),
    ev(10, "die", { exitCode: "1" }),
    ev(11, "start"),
    ev(20, "die", { exitCode: "1" }),
    ev(22, "start"),
    ev(40, "die", { exitCode: "1" }),
    ev(44, "start"),
    // It gave up: the last exit has no start after it.
    ev(80, "die", { exitCode: "1" }),
  ]
  const entries = foldRestarts(events)
  expect(shape(entries)).toEqual(["shop-db-1 die", "loop shop-db-1 ×3 exit 1", "shop-db-1 start"])
  const loop = entries[1]
  expect(loop.since).toBe(events[1].time)
  expect(loop.until).toBe(events[6].time)
  expect(loop.exit).toBe(events[5])
  expect(loop.events.map((e) => e.action)).toEqual(["start", "die", "start", "die", "start", "die"])
})

test("one restart is not a loop, and a different exit or a long quiet starts another run", () => {
  expect(shape(foldRestarts([ev(0, "die", { exitCode: "1" }), ev(1, "start")]))).toEqual([
    "shop-db-1 start",
    "shop-db-1 die",
  ])

  const events = [
    ev(0, "die", { exitCode: "1" }),
    ev(1, "start"),
    ev(5, "die", { exitCode: "1" }),
    ev(6, "start"),
    // Killed for memory twice: the same container, another story.
    ev(9, "oom"),
    ev(9, "die", { exitCode: "137" }),
    ev(10, "start"),
    ev(15, "oom"),
    ev(15, "die", { exitCode: "137" }),
    ev(16, "start"),
    // An hour later, once: a new incident rather than the same loop.
    ev(3600, "die", { exitCode: "137" }),
    ev(3601, "start"),
  ]
  expect(shape(foldRestarts(events))).toEqual([
    "shop-db-1 start",
    "shop-db-1 die",
    "loop shop-db-1 ×2 exit 137 oom",
    "loop shop-db-1 ×2 exit 1",
  ])
})

test("docker restart's own sequence is a cycle, and a health verdict between restarts stays in the loop", () => {
  const events = [
    ev(0, "kill"),
    ev(0.1, "die", { exitCode: "143" }),
    ev(0.2, "stop"),
    ev(0.5, "start"),
    ev(0.6, "restart"),
    ev(30, "health_status: unhealthy"),
    ev(31, "kill"),
    ev(31.1, "die", { exitCode: "143" }),
    ev(31.2, "stop"),
    ev(31.5, "start"),
    ev(31.6, "restart"),
    // After the loop, a verdict that no restart follows is a row of its own.
    ev(60, "health_status: healthy"),
  ]
  const entries = foldRestarts(events)
  expect(shape(entries)).toEqual(["shop-db-1 health_status: healthy", "loop shop-db-1 ×2 exit 143"])
  expect(entries[1].events).toHaveLength(11)
})

test("in a stack, another container's events neither break a loop nor join it", () => {
  const web = { id: "web111111111", name: "shop-web-1" }
  const events = [
    ev(0, "die", { exitCode: "1" }),
    ev(1, "start"),
    ev(2, "start", web),
    ev(5, "die", { exitCode: "1" }),
    ev(6, "start"),
    { ...ev(7, "pull", { type: "image", name: "postgres:17", id: "postgres:17" }) },
  ]
  expect(shape(foldRestarts(events))).toEqual([
    "postgres:17 pull",
    "loop shop-db-1 ×2 exit 1",
    "shop-web-1 start",
  ])
})

test("a stop that nothing restarted, and a create, stay what they are", () => {
  const events = [
    ev(0, "create"),
    ev(1, "start"),
    ev(50, "kill"),
    ev(50.1, "die", { exitCode: "0" }),
    ev(50.2, "stop"),
  ]
  expect(shape(foldRestarts(events))).toEqual([
    "shop-db-1 stop",
    "shop-db-1 die",
    "shop-db-1 kill",
    "shop-db-1 start",
    "shop-db-1 create",
  ])
})

test("a kill for memory is one row on the exit it caused, and an OOM the container lived through is its own", () => {
  // Nothing brought it back: the note and the exit, Docker's two words for one death.
  const died = foldRestarts([ev(0, "start"), ev(9.4, "oom"), ev(9.41, "die", { exitCode: "137" })])
  expect(shape(died)).toEqual(["shop-db-1 die +oom", "shop-db-1 start"])
  expect(died[0].oom.action).toBe("oom")

  // Brought back once, which is not a loop: still one row for the death.
  expect(
    shape(foldRestarts([ev(9.4, "oom"), ev(9.41, "die", { exitCode: "137" }), ev(10, "start")])),
  ).toEqual(["shop-db-1 start", "shop-db-1 die +oom"])

  // The kernel chose a child and the container lived on; an exit much
  // later is another story.
  expect(
    shape(foldRestarts([ev(0, "oom"), ev(600, "die", { exitCode: "1" }), ev(601, "start")])),
  ).toEqual(["shop-db-1 start", "shop-db-1 die", "shop-db-1 oom"])
})

test("the minute before an exit stops at the exit, to the nanosecond", () => {
  const exit = "2026-09-27T10:00:05.310456789Z"
  const search = lastLinesSearch(exit)
  // The server hands Docker the bound as it is written, so the part of a
  // second before the exit is read and the next attempt's start-up, a tenth
  // of a second on, is not.
  expect(search.until).toBe(exit)
  expect(search.since).toBe("2026-09-27T09:59:05.310Z")
  expect(search.limit).toBe(LAST_LINES)
})

test("the polled copy of an event wins over the socket's, and the feed is newest first", () => {
  const polled = { ...ev(5, "die", { exitCode: "1" }), source: "dashboard" }
  const live = [ev(9, "start"), ev(5, "die", { exitCode: "1" })]
  const merged = dedupeEvents([polled, ...live])
  expect(merged.map((e) => e.action)).toEqual(["start", "die"])
  expect(merged[1].source).toBe("dashboard")
})

test("a loop's length reads in the unit it is in", () => {
  expect(spanWords(40_000)).toBe("40 s")
  expect(spanWords(12 * 60_000 + 10_000)).toBe("12 min")
  expect(spanWords(125 * 60_000)).toBe("2 h 5 min")
  expect(spanWords(120 * 60_000)).toBe("2 h")
})

const inspect = {
  Config: { Healthcheck: { Test: ["CMD-SHELL", "pg_isready -U postgres"] } },
  State: {
    Health: {
      Status: "unhealthy",
      FailingStreak: 3,
      Log: [
        {
          Start: "2026-09-27T10:00:00.123456789Z",
          End: "2026-09-27T10:00:00.456789012Z",
          ExitCode: 0,
          Output: "/var/run/postgresql:5432 - accepting connections\n",
        },
        {
          Start: "2026-09-27T10:00:30.000000001Z",
          End: "2026-09-27T10:00:35.000000001Z",
          ExitCode: 1,
          Output: "/var/run/postgresql:5432 - no response\n",
        },
      ],
    },
  },
}

test("the health check reads as its verdict, its command and its probes, newest first", () => {
  const health = healthOf(inspect)
  expect(health.status).toBe("unhealthy")
  expect(health.failingStreak).toBe(3)
  expect(health.test).toBe("pg_isready -U postgres")
  expect(health.probes.map((p) => p.exitCode)).toEqual([1, 0])
  // Nanoseconds cut to what every date parser reads.
  expect(health.probes[1].start).toBe("2026-09-27T10:00:00.123Z")
  expect(Date.parse(health.probes[1].end) - Date.parse(health.probes[1].start)).toBe(333)
  expect(health.probes[0].output).toBe("/var/run/postgresql:5432 - no response")
})

test("a container without a health check has none, rather than an empty one", () => {
  expect(healthOf({ State: { Status: "running" } })).toBeUndefined()
  expect(healthOf(undefined)).toBeUndefined()
  expect(
    healthOf({
      Config: { Healthcheck: { Test: ["CMD", "curl", "-f", "http://localhost"] } },
      State: { Health: { Status: "starting" } },
    }),
  ).toEqual({ status: "starting", failingStreak: 0, test: "curl -f http://localhost", probes: [] })
})

test("an event's outcome is what it did, not its level alone", () => {
  expect(outcomeOf(ev(0, "die", { exitCode: "1" }))).toBe("failed")
  expect(outcomeOf(ev(0, "die", { exitCode: "0" }))).toBe("stopped")
  expect(outcomeOf(ev(0, "oom"))).toBe("failed")
  expect(outcomeOf(ev(0, "health_status: unhealthy"))).toBe("unhealthy")
  expect(outcomeOf(ev(0, "health_status: healthy"))).toBe("healthy")
  expect(outcomeOf(ev(0, "restart"))).toBe("restarted")
  expect(outcomeOf(ev(0, "start"))).toBe("started")
  expect(outcomeOf(ev(0, "kill"))).toBe("stopped")
  expect(outcomeOf(ev(0, "create"))).toBe("changed")
  expect(outcomeOf(ev(0, "pull", { type: "image", name: "postgres:16" }))).toBe("changed")
  // A network joined is bookkeeping, and a "start" on anything but a
  // container is not a container coming up.
  expect(outcomeOf(ev(0, "connect", { type: "network", name: "shop_default" }))).toBe("other")
  expect(outcomeOf(ev(0, "start", { type: "plugin" }))).toBe("other")
})

test("a container's activity counts its comebacks and its failures once each", () => {
  const now = base + 120_000
  const db = [
    ev(0, "start"),
    ev(10, "die", { exitCode: "1" }),
    ev(11, "start"),
    ev(20, "die", { exitCode: "1" }),
    ev(22, "start"),
    ev(40, "die", { exitCode: "1" }),
    ev(44, "start"),
  ]
  const worker = { id: "wk2222222222", name: "worker" }
  const killed = [
    ev(5, "start", worker),
    // The kernel's note and the exit it caused are one failure.
    ev(30, "oom", worker),
    ev(30.2, "die", { ...worker, exitCode: "137" }),
  ]
  const api = { id: "api333333333", name: "shop-api-1" }
  // `docker restart` from this dashboard: one comeback, no failure.
  const restarted = [
    { ...ev(50, "kill", api), source: "dashboard", trigger: { actor: "wayy" } },
    ev(50.5, "die", { ...api, exitCode: "0" }),
    { ...ev(51, "start", api), source: "dashboard", trigger: { actor: "wayy" } },
    { ...ev(51.1, "restart", api), source: "dashboard", trigger: { actor: "wayy" } },
  ]
  const network = ev(60, "connect", { type: "network", name: "shop_default", id: "net" })

  const rows = containerActivity([...db, ...killed, ...restarted, network], now)
  expect(rows.map((r) => r.name)).toEqual(["shop-db-1", "worker", "shop-api-1"])

  const [dbRow, workerRow, apiRow] = rows
  expect(dbRow.looping).toBe(true)
  expect(dbRow.restarts).toBe(3)
  expect(dbRow.failures).toBe(3)
  expect(dbRow.last.action).toBe("start")

  expect(workerRow.failures).toBe(1)
  expect(workerRow.oom).toBe(true)
  expect(workerRow.lastExit?.exitCode).toBe("137")
  expect(workerRow.looping).toBe(false)

  expect(apiRow.failures).toBe(0)
  expect(apiRow.restarts).toBe(1)
  expect(apiRow.actors).toEqual(["wayy"])
})

test("a loop that stopped going is no longer looping, and a removed container sinks", () => {
  const loop = [
    ev(0, "die", { exitCode: "1" }),
    ev(1, "start"),
    ev(10, "die", { exitCode: "1" }),
    ev(11, "start"),
  ]
  const gone = { id: "mig444444444", name: "shop-migrate-1" }
  const removed = [
    ev(100, "start", gone),
    ev(101, "die", { ...gone, exitCode: "2" }),
    ev(102, "destroy", gone),
  ]
  const later = base + 60 * 60_000
  const rows = containerActivity([...loop, ...removed], later)
  expect(rows.find((r) => r.name === "shop-db-1")?.looping).toBe(false)
  const migrate = rows.find((r) => r.name === "shop-migrate-1")
  expect(migrate?.removed).toBe(true)
  // Failed and newer, but gone: it does not outrank one that failed and is still here.
  expect(rows.map((r) => r.name)).toEqual(["shop-db-1", "shop-migrate-1"])
})

test("events are counted into equal slices of the window, oldest first", () => {
  const events = [
    ev(0, "start"),
    ev(30, "die", { exitCode: "1" }),
    ev(59, "start"),
    ev(120, "start"),
  ]
  const from = base
  const to = base + 60_000
  expect(perSlice(events, from, to, 2)).toEqual([1, 2])
  expect(perSlice(events, from, to, 2, (e) => e.action === "die")).toEqual([0, 1])
  expect(perSlice(events, to, from, 3)).toEqual([0, 0, 0])
})

test("a comeback is a start after an exit, not the first start of a new container", () => {
  const api = { id: "api333333333", name: "shop-api-1" }
  const events = [
    ev(0, "create", api),
    ev(1, "start", api),
    ev(5, "die", { ...api, exitCode: "0" }),
    ev(6, "start", api),
    ev(7, "start"),
  ]
  const back = comebacks(events)
  expect([...back]).toEqual([events[3]])
})

test("an axis takes the finest round step that keeps its labels few", () => {
  const hour = 3_600_000
  const from = new Date(2026, 9, 8, 9, 7).getTime()
  const ticks = axisTicks(from, from + hour, 6)
  // Ten minutes is the finest step that fits an hour in six labels.
  expect(ticks.map((t) => new Date(t).getMinutes())).toEqual([10, 20, 30, 40, 50, 0])
  const day = axisTicks(from, from + 24 * hour, 6)
  expect(day.every((t) => new Date(t).getHours() % 6 === 0 && new Date(t).getMinutes() === 0)).toBe(
    true,
  )
  expect(day.length).toBeLessThanOrEqual(6)
  expect(axisTicks(from, from)).toEqual([])
})

test("an exit moments after a kill was asked for, and is not a failure", () => {
  const api = { id: "api333333333", name: "shop-api-1" }
  const restarted = [
    ev(0, "kill", api),
    ev(0.4, "die", { ...api, exitCode: "143" }),
    ev(1, "start", api),
  ]
  // A crash and an OOM kill have no kill before them.
  const crashed = [
    ev(5, "die", { exitCode: "1" }),
    ev(10, "oom"),
    ev(10.2, "die", { exitCode: "137" }),
  ]
  // A kill long before is not what this exit answered.
  const later = ev(60, "die", { ...api, exitCode: "1" })
  const settled = settleAskedExits([...restarted, ...crashed, later])
  expect(settled.map((e) => `${e.action} ${e.level}`)).toEqual([
    "kill notice",
    "die notice",
    "start notice",
    "die error",
    "oom notice",
    "die error",
    "die error",
  ])
  // Still the status it exited with.
  expect(settled[1].exitCode).toBe("143")
  expect(outcomeOf(settled[1])).toBe("stopped")
  // Nothing to settle is the same array back.
  expect(settleAskedExits(crashed)).toBe(crashed)
})

test("a container's events moments apart are one row, titled by what they amount to", () => {
  const api = { id: "api333333333", name: "shop-api-1" }
  const restart = [
    ev(0, "kill", api),
    ev(0.4, "die", { ...api, exitCode: "0" }),
    ev(0.6, "stop", api),
    ev(1.4, "start", api),
    ev(1.5, "restart", api),
  ]
  // A crash the restart policy answered at once: the exit leads.
  const crash = [ev(30, "die", { exitCode: "1" }), ev(30.3, "start")]
  const health = ev(31, "health_status: unhealthy")
  const pull = ev(32, "pull", { type: "image", name: "postgres:16", id: "postgres:16" })
  const lonely = ev(90, "stop", api)
  const feed = foldBursts(foldRestarts([...restart, ...crash, health, pull, lonely]))
  const shape = feed.map((e) =>
    e.kind === "burst"
      ? `burst ${e.lead.name} ${e.lead.action} ×${e.events.length}`
      : e.kind === "loop"
        ? `loop ${e.name}`
        : `${e.event.name} ${e.event.action}`,
  )
  expect(shape).toEqual([
    "shop-api-1 stop",
    "postgres:16 pull",
    "shop-db-1 health_status: unhealthy",
    "burst shop-db-1 die ×2",
    "burst shop-api-1 restart ×5",
  ])
  const burst = feed[4]
  expect(burst.kind === "burst" && burst.events.map((e) => e.action)).toEqual([
    "restart",
    "start",
    "stop",
    "die",
    "kill",
  ])
})
