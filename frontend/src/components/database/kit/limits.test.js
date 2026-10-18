import { describe, expect, test } from "bun:test"
import {
  MIB,
  bufferFact,
  coresWord,
  defaultLimitsFor,
  draftOfLimits,
  limitChanges,
  limitProblems,
  limitsWords,
  liftedLimits,
} from "./limits"

const HOST = { memoryBytes: 16 * 1024 * MIB, cpus: 8 }

describe("the rules a limit is held to", () => {
  test("zero is no limit, and anything this machine can give is allowed", () => {
    expect(limitProblems({ memoryMb: 0, cpus: 0 }, HOST)).toEqual({})
    expect(limitProblems({ memoryMb: 16 * 1024, cpus: 8 }, HOST)).toEqual({})
    expect(limitProblems({ memoryMb: 6, cpus: 0.01 }, HOST)).toEqual({})
  })

  test("Docker's floor and this machine's ceiling, each on its own field", () => {
    expect(limitProblems({ memoryMb: 5, cpus: 0.001 }, HOST)).toEqual({
      memory: "The smallest limit Docker takes is 6 MiB.",
      cpus: "The smallest processor limit is 0.01 of a core.",
    })
    expect(limitProblems({ memoryMb: 16 * 1024 + 1, cpus: 9 }, HOST)).toEqual({
      memory: "This server has 16 GB; a limit above that limits nothing.",
      cpus: "This server has 8 cores.",
    })
  })

  test("a machine not read yet is held to the floor only", () => {
    expect(limitProblems({ memoryMb: 1_000_000, cpus: 64 })).toEqual({})
  })

  test("a memory limit under what it uses now would stop it at once", () => {
    expect(limitProblems({ memoryMb: 256, cpus: 0 }, HOST, 300 * MIB).memory).toBe(
      "It uses 300 MB now; a limit under that stops it at once.",
    )
    expect(limitProblems({ memoryMb: 512, cpus: 0 }, HOST, 300 * MIB)).toEqual({})
    // Lifting it is never under anything.
    expect(limitProblems({ memoryMb: 0, cpus: 0 }, HOST, 300 * MIB)).toEqual({})
  })
})

describe("what the limits are called", () => {
  test("as the section's head says them", () => {
    expect(limitsWords({ memoryBytes: 0, cpus: 0 })).toBe(
      "No limits: it may use all of this server's memory and processors",
    )
    expect(limitsWords({ memoryBytes: 1024 * MIB, cpus: 1.5 })).toBe(
      "Up to 1 GB of memory and 1.5 cores",
    )
    expect(limitsWords({ memoryBytes: 512 * MIB, cpus: 0 })).toBe(
      "Up to 512 MB of memory, any number of processors",
    )
    expect(limitsWords({ memoryBytes: 0, cpus: 1 })).toBe("Up to 1 core, any amount of memory")
  })

  test("a quota Docker holds as 0.299999999 is the 0.3 that set it", () => {
    expect(coresWord(0.299999999)).toBe("0.3 cores")
    expect(draftOfLimits({ memoryBytes: 1536 * MIB + 7, cpus: 0.299999999 })).toEqual({
      memoryMb: 1536,
      cpus: 0.3,
    })
  })
})

describe("a change of limits", () => {
  const saved = { memoryMb: 1024, cpus: 1 }

  test("sends only what changed", () => {
    expect(limitChanges(saved, saved)).toEqual({})
    expect(limitChanges(saved, { memoryMb: 2048, cpus: 1 })).toEqual({ memoryMb: 2048 })
    expect(limitChanges(saved, { memoryMb: 1024, cpus: 0 })).toEqual({ cpus: 0 })
  })

  test("names the limits it takes away, which replaces the container", () => {
    expect(liftedLimits(saved, { memoryMb: 2048, cpus: 2 })).toEqual([])
    expect(liftedLimits(saved, { memoryMb: 0, cpus: 1 })).toEqual(["memory"])
    expect(liftedLimits(saved, { memoryMb: 0, cpus: 0 })).toEqual(["memory", "cpus"])
    // Nothing to take away where there was none.
    expect(liftedLimits({ memoryMb: 0, cpus: 0 }, { memoryMb: 0, cpus: 0 })).toEqual([])
  })
})

describe("the limits a new server's form offers", () => {
  test("the template's, kept to half the memory and to the processors this machine has", () => {
    expect(defaultLimitsFor({ memoryMb: 2048, cpus: 2 }, HOST)).toEqual({ memoryMb: 2048, cpus: 2 })
    expect(
      defaultLimitsFor({ memoryMb: 2048, cpus: 2 }, { memoryBytes: 2 * 1024 * MIB, cpus: 1 }),
    ).toEqual({
      memoryMb: 1024,
      cpus: 1,
    })
    expect(defaultLimitsFor({ memoryMb: 1024, cpus: 1 })).toEqual({ memoryMb: 1024, cpus: 1 })
  })
})

describe("the engine's buffer beside a memory limit", () => {
  const shared = { setting: "shared_buffers", bytes: 128 * MIB }

  test("is a fact, with no effect while the limit leaves room for it", () => {
    expect(bufferFact(shared, 0)).toEqual({ value: "128 MB" })
    expect(bufferFact(shared, 1024)).toEqual({ value: "128 MB" })
  })

  test("says what a limit under it does", () => {
    expect(bufferFact({ setting: "shared_buffers", bytes: 2048 * MIB }, 1024)).toEqual({
      value: "2 GB",
      effect: "more than this limit: the kernel stops the server once the engine fills it",
    })
  })

  test("a key–value server with no maxmemory grows until the limit stops it", () => {
    expect(bufferFact({ setting: "maxmemory", bytes: 0 }, 0)).toEqual({ value: "not set" })
    expect(bufferFact({ setting: "maxmemory", bytes: 0 }, 512)).toEqual({
      value: "not set",
      effect: "so nothing in the engine keeps it under this limit: the kernel stops it there",
    })
  })
})
