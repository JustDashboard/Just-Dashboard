import { describe, expect, test } from "bun:test"
import { elapsed, maintenanceVariants } from "./maintenance-variants"

describe("the ways a maintenance action is asked for", () => {
  test("an action with no option is its own one way", () => {
    expect(maintenanceVariants({ id: "analyze", label: "Analyze" })).toEqual([
      { key: "plain", label: "Analyze" },
    ])
    expect(maintenanceVariants({ id: "analyze", label: "Analyze", options: [] })).toHaveLength(1)
  })

  test("each option the server lists is a second way, named for what it changes", () => {
    const reindex = maintenanceVariants({
      id: "reindex",
      label: "Reindex",
      options: ["concurrently"],
    })
    expect(reindex.map((variant) => variant.label)).toEqual(["Reindex", "Reindex concurrently"])
    expect(reindex[0].options).toBeUndefined()
    expect(reindex[1].options).toEqual({ concurrently: true })

    const optimize = maintenanceVariants({ id: "optimize", label: "Optimize", options: ["final"] })
    expect(optimize[1]).toMatchObject({ label: "Optimize, final", options: { final: true } })

    const rebuild = maintenanceVariants({ id: "rebuild", label: "Rebuild", options: ["online"] })
    expect(rebuild[1]).toMatchObject({ label: "Rebuild online", options: { online: true } })
  })

  test("a checkpoint is asked for by its mode, and there is no plain one", () => {
    const checkpoint = maintenanceVariants({
      id: "wal_checkpoint",
      label: "Checkpoint",
      options: ["mode"],
    })
    expect(checkpoint.map((variant) => variant.options)).toEqual([
      { mode: "passive" },
      { mode: "full" },
      { mode: "restart" },
      { mode: "truncate" },
    ])
    expect(checkpoint.every((variant) => variant.note)).toBe(true)
  })

  test("every variant of an action has its own key", () => {
    const variants = maintenanceVariants({
      id: "x",
      label: "X",
      options: ["concurrently", "final", "online"],
    })
    expect(new Set(variants.map((variant) => variant.key)).size).toBe(variants.length)
  })
})

describe("how long a run has been going", () => {
  test("ticks in seconds, then minutes, then hours", () => {
    expect(elapsed(0)).toBe("0:00")
    expect(elapsed(7_400)).toBe("0:07")
    expect(elapsed(760_000)).toBe("12:40")
    expect(elapsed(3_723_000)).toBe("1:02:03")
    expect(elapsed(-5)).toBe("0:00")
  })
})
