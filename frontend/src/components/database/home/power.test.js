import { describe, expect, test } from "bun:test"
import {
  POWER,
  POWER_LIMIT_MS,
  changePower,
  endPower,
  powerCarried,
  powerChange,
  powerEnded,
  subscribePower,
} from "./power"

/** A clock the test moves, and a sleep that moves it. */
function clock() {
  let now = 0
  return { now: () => now, sleep: async (ms) => void (now += ms) }
}

describe("a change of power", () => {
  test("is held from the request until the server is what was asked of it", async () => {
    const seen = []
    const stop = subscribePower(() => seen.push(powerChange(1)?.action))
    const states = ["unreachable", "unreachable", "running"]
    let reads = 0
    const { now, sleep } = clock()
    const outcome = await changePower(1, "start", {
      request: async () => {
        // While the request is out, every surface reads the change.
        expect(powerChange(1)).toEqual({ action: "start", since: 0 })
        expect(powerCarried(1)).toBe(true)
        return { action: "start", via: "docker", target: "shop-db", state: "running" }
      },
      read: async () => states[reads++],
      now,
      sleep,
    })
    stop()
    expect(outcome.answer.target).toBe("shop-db")
    expect(outcome.settled).toBe(true)
    expect(powerCarried(1)).toBe(false)
    // A started engine reads unreachable until it accepts connections: asked three times.
    expect(reads).toBe(3)
    expect(powerChange(1)).toBeUndefined()
    expect(seen).toEqual(["start", undefined])
  })

  test("a stop that already reads stopped ends at the first read", async () => {
    let reads = 0
    await changePower(2, "stop", {
      request: async () => ({ action: "stop", via: "systemd", target: "postgresql.service" }),
      read: async () => {
        reads++
        return "stopped"
      },
      ...clock(),
    })
    expect(reads).toBe(1)
  })

  test("gives up waiting at the deadline, and is no longer held", async () => {
    const { now, sleep } = clock()
    let reads = 0
    const outcome = await changePower(3, "restart", {
      request: async () => ({ action: "restart", via: "docker", target: "x" }),
      read: async () => {
        reads++
        return "unreachable"
      },
      settleMs: 10_000,
      everyMs: 2_000,
      now,
      sleep,
    })
    expect(reads).toBe(6)
    // The request succeeded and the engine never came back: that is not a restart.
    expect(outcome.settled).toBe(false)
    expect(powerChange(3)).toBeUndefined()
  })

  test("a read that fails is not the state arriving", async () => {
    let reads = 0
    await changePower(4, "start", {
      request: async () => ({ action: "start", via: "docker", target: "x" }),
      read: async () => {
        if (reads++ === 0) throw new Error("network")
        return "running"
      },
      ...clock(),
    })
    expect(reads).toBe(2)
  })

  test("a request that is refused releases the change and says why", async () => {
    await expect(
      changePower(5, "stop", {
        request: async () => {
          throw new Error("the container cache is not running")
        },
        read: async () => "running",
        ...clock(),
      }),
    ).rejects.toThrow("not running")
    expect(powerChange(5)).toBeUndefined()
  })

  test("a second change of the same server is refused while one is in flight", async () => {
    let release
    const first = changePower(6, "stop", {
      request: () => new Promise((resolve) => (release = resolve)),
      read: async () => "stopped",
      ...clock(),
    })
    await expect(
      changePower(6, "start", { request: async () => ({}), read: async () => "running" }),
    ).rejects.toThrow("already being changed")
    // Another connection is its own matter.
    expect(powerChange(7)).toBeUndefined()
    release({ action: "stop", via: "docker", target: "x" })
    await first
    expect(powerChange(6)).toBeUndefined()
  })

  test("a change this tab is carrying is ended by its own request, not from outside", async () => {
    let release
    const run = changePower(8, "stop", {
      request: () => new Promise((resolve) => (release = resolve)),
      read: async () => "stopped",
      ...clock(),
    })
    endPower(8)
    expect(powerChange(8)?.action).toBe("stop")
    release({ action: "stop", via: "docker", target: "x" })
    await run
    expect(powerChange(8)).toBeUndefined()
  })

  test("each action has its verb, its participle and the state it settles in", () => {
    expect(POWER.start.progressive).toBe("Starting…")
    expect(POWER.stop.settles).toBe("stopped")
    expect(POWER.restart.settles).toBe("running")
  })
})

describe("a change nobody here is waiting on", () => {
  const at = (action, since = 0) => ({ action, since })

  test("a start ends when the engine answers, and not while it is coming up", () => {
    expect(powerEnded(at("start"), "stopped", false, 5_000)).toBe(false)
    expect(powerEnded(at("start"), "unreachable", true, 20_000)).toBe(false)
    expect(powerEnded(at("start"), "running", true, 25_000)).toBe(true)
  })

  test("a stop ends when the server is down", () => {
    expect(powerEnded(at("stop"), "running", false, 60_000)).toBe(false)
    expect(powerEnded(at("stop"), "stopped", true, 61_000)).toBe(true)
  })

  test("a restart still up is not over until it has been seen down, or has stood too long", () => {
    // Read `running` a moment after it was asked for: it has not gone down yet.
    expect(powerEnded(at("restart"), "running", false, 3_000)).toBe(false)
    expect(powerEnded(at("restart"), "unreachable", true, 8_000)).toBe(false)
    expect(powerEnded(at("restart"), "running", true, 12_000)).toBe(true)
    // Down and up between two readings: nobody saw it, and time settles it.
    expect(powerEnded(at("restart"), "running", false, 44_000)).toBe(false)
    expect(powerEnded(at("restart"), "running", false, 45_000)).toBe(true)
  })

  test("any of them ends when the time the server allows has passed", () => {
    expect(powerEnded(at("start"), "unreachable", true, POWER_LIMIT_MS - 1)).toBe(false)
    expect(powerEnded(at("start"), "unreachable", true, POWER_LIMIT_MS)).toBe(true)
    expect(powerEnded(at("stop", 1_000), "running", false, POWER_LIMIT_MS + 1_000)).toBe(true)
  })

  test("the summary not having answered yet ends nothing", () => {
    expect(powerEnded(at("start"), "checking", false, 1_000)).toBe(false)
    expect(powerEnded(at("stop"), "unknown", false, 1_000)).toBe(false)
  })
})
