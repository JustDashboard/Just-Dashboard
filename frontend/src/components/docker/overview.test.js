import { describe, expect, test } from "bun:test"
import { crashed, exitOf, mergeEvents, recentChanges } from "./overview"

const NOW = Date.UTC(2026, 9, 8, 12, 0, 0)
const ago = (ms) => new Date(NOW - ms).toISOString()
const MIN = 60_000

const container = (over) => ({
  id: "a1",
  name: "web",
  image: "nginx",
  state: "running",
  status: "Up 2 hours",
  ...over,
})

const event = (msAgo, name, action, over) => ({
  time: ago(msAgo),
  type: "container",
  action,
  name,
  id: `${name}-id`,
  message: "",
  level: "notice",
  source: "docker",
  ...over,
})

describe("exitOf", () => {
  test("reads Docker's status sentence", () => {
    expect(exitOf(container({ state: "exited", status: "Exited (137) 12 minutes ago" }))).toEqual({
      code: 137,
      ago: "12 minutes ago",
    })
    expect(exitOf(container({}))).toBeUndefined()
  })
})

test("crashed is an exit that is neither a finish nor an honoured stop", () => {
  const exited = (code) => container({ state: "exited", status: `Exited (${code}) 3 days ago` })
  expect(crashed(exited(1))).toBe(true)
  expect(crashed(exited(0))).toBe(false)
  expect(crashed(exited(143))).toBe(false)
  expect(crashed(container({}))).toBe(false)
})

describe("recentChanges", () => {
  test("reads a stop's kill, die and stop as one stop", () => {
    const changes = recentChanges(
      [
        event(MIN, "mailhog", "stop"),
        event(MIN + 100, "mailhog", "die", { exitCode: "0" }),
        event(MIN + 200, "mailhog", "kill"),
      ],
      NOW,
    )
    expect(changes).toHaveLength(1)
    expect(changes[0]).toMatchObject({ name: "mailhog", verb: "stopped", tone: "stopped" })
  })

  test("an OOM kill is one line, by its exit", () => {
    const changes = recentChanges(
      [
        event(12 * MIN, "worker", "die", { exitCode: "137" }),
        event(12 * MIN + 2000, "worker", "oom"),
      ],
      NOW,
    )
    expect(changes).toHaveLength(1)
    expect(changes[0]).toMatchObject({ verb: "ran out of memory", tone: "danger" })
  })

  // "started" over a restart loop is the loop hiding behind its last good moment.
  test("a start after crashes counts them", () => {
    const changes = recentChanges(
      [
        event(MIN, "worker", "start"),
        event(2 * MIN, "worker", "die", { exitCode: "1" }),
        event(5 * MIN, "worker", "start"),
        event(6 * MIN, "worker", "die", { exitCode: "1" }),
        event(9 * MIN, "worker", "die", { exitCode: "0" }),
      ],
      NOW,
    )
    expect(changes[0]).toMatchObject({ verb: "started", tone: "running", crashes: 2 })
  })

  test("one line per container, newest first, inside the last day", () => {
    const changes = recentChanges(
      [
        event(3 * MIN, "api", "start"),
        event(MIN, "kuma", "health_status: unhealthy"),
        event(2 * 86_400_000, "old", "start"),
        event(30, "web", "exec_start: sh"),
        { ...event(MIN, "nginx:alpine", "pull"), type: "image" },
      ],
      NOW,
    )
    expect(changes.map((c) => [c.name, c.verb])).toEqual([
      ["kuma", "failing its health check"],
      ["api", "started"],
    ])
  })
})

test("mergeEvents keeps one copy of a replayed buffer, newest first", () => {
  const a = event(MIN, "api", "start")
  const b = event(2 * MIN, "api", "die", { exitCode: "0" })
  const merged = mergeEvents([a, b], [b, event(10, "web", "start")])
  expect(merged.map((e) => e.name)).toEqual(["web", "api", "api"])
})
