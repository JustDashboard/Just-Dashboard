import { describe, expect, test } from "bun:test"
import { STACK_EDGE, STACK_GAP, stackOffsets } from "./dock"

// Where the bottom stack puts each thing. Measuring the elements needs a
// document; the sum it is fed is held here.

const stack = (entries) => stackOffsets(new Map(entries))

describe("the bottom stack", () => {
  test("a dock alone sits on the window's edge", () => {
    const { docks, occupied } = stack([["held", { layer: "dock", order: 0, extent: 60 }]])
    expect(occupied).toBe(0)
    expect(docks.held).toBe(STACK_EDGE)
  })

  test("a dock lifts above an open save bar", () => {
    // The save bar: 56px tall, 16px off the bottom edge.
    const { docks, occupied } = stack([
      ["bar", { layer: "bar", order: 0, extent: 72 }],
      ["agent", { layer: "dock", order: 1, extent: 64 }],
    ])
    expect(occupied).toBe(72)
    expect(docks.agent).toBe(72 + STACK_GAP)
  })

  test("docks stack in their order by the measured height of the ones below", () => {
    const { docks, top } = stack([
      ["agent", { layer: "dock", order: 1, extent: 64 }],
      ["held", { layer: "dock", order: 0, extent: 120 }],
    ])
    expect(docks.held).toBe(STACK_EDGE)
    expect(docks.agent).toBe(STACK_EDGE + 120 + STACK_GAP)
    // The top of the stack, which toasts rise above.
    expect(top).toBe(STACK_EDGE + 120 + STACK_GAP + 64)
  })

  test("a save bar over a pane page's bottom bar stands above it, not on its stops", () => {
    // The bottom bar: 56px, in the page's flow at the foot. The save bar asks
    // for a higher order, is lifted by the bar under it, and is measured
    // with the lift in it, so the stack's top is the save bar's.
    const first = stack([
      ["pane", { layer: "bar", order: 0, extent: 56 }],
      ["save", { layer: "bar", order: 1, extent: 72 }],
    ])
    expect(first.bars).toEqual({ pane: 0, save: 56 })
    const lifted = stack([
      ["pane", { layer: "bar", order: 0, extent: 56 }],
      ["save", { layer: "bar", order: 1, extent: 56 + 72 }],
      ["agent", { layer: "dock", order: 1, extent: 64 }],
    ])
    expect(lifted.bars.save).toBe(56)
    expect(lifted.occupied).toBe(56 + 72)
    expect(lifted.docks.agent).toBe(56 + 72 + STACK_GAP)
  })

  test("a bar alone is not lifted", () => {
    expect(stack([["save", { layer: "bar", order: 1, extent: 72 }]]).bars.save).toBe(0)
  })

  test("two bars at once occupy the taller of them, not their sum", () => {
    const { occupied } = stack([
      ["pane", { layer: "bar", order: 0, extent: 56 }],
      ["save", { layer: "bar", order: 0, extent: 80 }],
    ])
    expect(occupied).toBe(80)
  })
})
