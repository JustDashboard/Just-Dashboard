import { describe, expect, test } from "bun:test"
import { POWER, changePower, powerChange, subscribePower } from "./power"

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
    const answer = await changePower(1, "start", {
      request: async () => {
        // While the request is out, every surface reads the change.
        expect(powerChange(1)).toEqual({ action: "start", since: 0 })
        return { action: "start", via: "docker", target: "shop-db", state: "running" }
      },
      read: async () => states[reads++],
      now,
      sleep,
    })
    stop()
    expect(answer.target).toBe("shop-db")
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
    await changePower(3, "restart", {
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

  test("each action has its verb, its participle and the state it settles in", () => {
    expect(POWER.start.progressive).toBe("Starting…")
    expect(POWER.stop.settles).toBe("stopped")
    expect(POWER.restart.settles).toBe("running")
  })
})
