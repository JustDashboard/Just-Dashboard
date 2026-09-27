import { expect, test } from "bun:test"
import {
  LAST_LINES,
  dedupeEvents,
  foldRestarts,
  healthOf,
  lastLinesSearch,
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
