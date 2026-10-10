import { describe, expect, test } from "bun:test"
import {
  containerBucket,
  containerWord,
  exitCodeOf,
  exitedWhen,
  exitWords,
  networkRates,
  oomKilledIds,
  recentEntries,
  sortContainers,
} from "./containers"

const container = (over) => ({
  id: "a".repeat(64),
  name: "web",
  image: "nginx:alpine",
  state: "running",
  status: "Up 2 hours",
  uptimeSeconds: 7200,
  inspected: true,
  ...over,
})

const NOW = Date.UTC(2026, 9, 8, 12, 0, 0)
const at = (secondsAgo) => new Date(NOW - secondsAgo * 1000).toISOString()
const event = (secondsAgo, action, over = {}) => ({
  time: at(secondsAgo),
  type: "container",
  action,
  name: "db",
  id: "d".repeat(64),
  message: "",
  level: "notice",
  source: "docker",
  ...over,
})

describe("exitCodeOf and exitedWhen", () => {
  test("read Docker's status sentence", () => {
    expect(exitCodeOf({ status: "Exited (137) 2 hours ago" })).toBe(137)
    expect(exitCodeOf({ status: "Restarting (1) 8 seconds ago" })).toBe(1)
    expect(exitCodeOf({ status: "Up 2 hours" })).toBeUndefined()
    expect(exitedWhen({ status: "Exited (0) 5 hours ago" })).toBe("5 hours ago")
    expect(exitedWhen({ status: "Exited (0) About a minute ago" })).toBe("a minute ago")
    expect(exitedWhen({ status: "Created" })).toBeUndefined()
  })
})

describe("containerBucket", () => {
  test("a failing health check is as failing as a restart loop", () => {
    expect(containerBucket(container({ health: "unhealthy" }))).toBe("failing")
    expect(containerBucket(container({ state: "restarting" }))).toBe("failing")
    expect(containerBucket(container({ state: "dead" }))).toBe("failing")
  })

  test("a deliberate stop is stopped and a crash is failing", () => {
    const exited = (code) => container({ state: "exited", status: `Exited (${code}) 1 hour ago` })
    expect(containerBucket(exited(0))).toBe("stopped")
    expect(containerBucket(exited(143))).toBe("stopped")
    // `docker stop` past its timeout leaves 137 as well, so only the event
    // log can call it a memory kill.
    expect(containerBucket(exited(137))).toBe("stopped")
    expect(containerBucket(exited(137), true)).toBe("failing")
    expect(containerBucket(exited(1))).toBe("failing")
  })

  test("a health check still starting is starting, and the rest keep their state", () => {
    expect(containerBucket(container({ health: "starting" }))).toBe("starting")
    expect(containerBucket(container({ health: "healthy" }))).toBe("running")
    expect(containerBucket(container({ state: "paused" }))).toBe("paused")
    expect(containerBucket(container({ state: "created", status: "Created" }))).toBe("stopped")
  })
})

describe("containerWord and exitWords", () => {
  test("name what exited means", () => {
    const exited = (code) => container({ state: "exited", status: `Exited (${code}) 1 hour ago` })
    expect(containerWord(exited(0))).toBe("Stopped")
    expect(containerWord(exited(1))).toBe("Crashed")
    expect(containerWord(exited(137), true)).toBe("Out of memory")
    expect(containerWord(container({ health: "unhealthy" }))).toBe("Unhealthy")
    expect(containerWord(container({ state: "created" }))).toBe("Never started")
    expect(exitWords(137)).toBe("killed")
    expect(exitWords(137, true)).toBe("killed for memory")
    expect(exitWords(143)).toBe("stopped")
    expect(exitWords(2)).toBe("exit 2")
    expect(exitWords(undefined)).toBeUndefined()
  })
})

describe("sortContainers", () => {
  const rows = [
    container({ id: "1", name: "web" }),
    container({ id: "2", name: "api", state: "exited", status: "Exited (0) 1 hour ago" }),
    container({ id: "3", name: "worker", state: "restarting", status: "Restarting (1) now" }),
    container({ id: "4", name: "db" }),
  ]
  const stats = {
    1: { cpuPercent: 3, memUsage: 300 },
    4: { cpuPercent: 40, memUsage: 100 },
    // A first frame has no interval to measure CPU over.
    3: { cpuPercent: 0, cpuReady: false, memUsage: 50 },
  }
  const names = (list) => list.map((c) => c.name)
  const bucket = (c) => containerBucket(c)

  test("by state puts failing first, then by name", () => {
    expect(names(sortContainers(rows, { key: "state", dir: "asc" }, stats, bucket))).toEqual([
      "worker",
      "db",
      "web",
      "api",
    ])
  })

  test("by a figure puts a container with no reading last in either direction", () => {
    const desc = sortContainers(rows, { key: "cpu", dir: "desc" }, stats, bucket)
    expect(names(desc)).toEqual(["db", "web", "api", "worker"])
    const asc = sortContainers(rows, { key: "cpu", dir: "asc" }, stats, bucket)
    expect(names(asc)).toEqual(["web", "db", "api", "worker"])
    const memory = sortContainers(rows, { key: "memory", dir: "desc" }, stats, bucket)
    expect(names(memory)).toEqual(["web", "db", "worker", "api"])
  })
})

describe("networkRates", () => {
  const frame = (secondsAgo, rx, tx, over = {}) => ({
    id: "a",
    ts: at(secondsAgo),
    netRx: rx,
    netTx: tx,
    ...over,
  })

  test("is the difference of two frames over the time between them", () => {
    const rates = networkRates({ a: frame(4, 1000, 500) }, [frame(2, 5000, 1500)])
    expect(rates.a).toEqual({ rx: 2000, tx: 500 })
  })

  test("says nothing for a restart, a first frame or the host's network", () => {
    expect(networkRates({ a: frame(4, 9000, 500) }, [frame(2, 10, 20)]).a).toBeUndefined()
    expect(networkRates({}, [frame(2, 10, 20)]).a).toBeUndefined()
    const host = frame(2, 5000, 1500, { networkAvailable: false })
    expect(networkRates({ a: frame(4, 1000, 500) }, [host]).a).toBeUndefined()
  })
})

describe("recentEntries and oomKilledIds", () => {
  test("drops the daemon answering questions and keeps what happened", () => {
    const entries = recentEntries([
      event(30, "exec_start: sh"),
      event(20, "kill"),
      event(19, "die", { exitCode: "143" }),
      event(10, "start"),
    ])
    expect(entries.map((e) => e.event.action)).toEqual(["start", "die"])
  })

  test("keeps a healthy verdict only after an unhealthy one", () => {
    const entries = recentEntries([
      event(40, "health_status: healthy"),
      event(30, "health_status: unhealthy"),
      event(20, "health_status: healthy"),
    ])
    expect(entries.map((e) => e.event.action)).toEqual([
      "health_status: healthy",
      "health_status: unhealthy",
    ])
  })

  test("an exit the OOM killer caused marks the container until it starts again", () => {
    const killed = recentEntries([event(60, "oom"), event(60, "die", { exitCode: "137" })])
    expect(killed).toHaveLength(1)
    expect(oomKilledIds(killed).has("d".repeat(64))).toBe(true)

    const back = recentEntries([
      event(60, "oom"),
      event(60, "die", { exitCode: "137" }),
      event(5, "start"),
    ])
    expect(oomKilledIds(back).size).toBe(0)
  })

  test("a restart loop is one entry", () => {
    const loop = recentEntries(
      [0, 1, 2].flatMap((i) => [
        event(300 - i * 60, "die", { exitCode: "1" }),
        event(299 - i * 60, "start"),
      ]),
    )
    expect(loop).toHaveLength(1)
    expect(loop[0].kind).toBe("loop")
    expect(loop[0].times).toBe(3)
  })
})
