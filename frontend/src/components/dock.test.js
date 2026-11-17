import { describe, expect, test } from "bun:test"
import { STACK_EDGE, STACK_GAP, stackOffsets, watchOccupant } from "./dock"

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

describe("watching an occupant", () => {
  // A stand-in for the browser's observers: each records what it watches and
  // can be fired by hand.
  function fakeBrowser() {
    const fired = { resize: [], mutation: [] }
    const listeners = new Map()
    class FakeResizeObserver {
      constructor(callback) {
        this.callback = callback
        fired.resize.push(this)
      }
      observe() {}
      disconnect() {
        this.gone = true
      }
    }
    class FakeMutationObserver {
      constructor(callback) {
        this.callback = callback
        fired.mutation.push(this)
      }
      observe(target, options) {
        this.options = options
      }
      disconnect() {
        this.gone = true
      }
    }
    const saved = {
      ResizeObserver: globalThis.ResizeObserver,
      MutationObserver: globalThis.MutationObserver,
      window: globalThis.window,
    }
    globalThis.ResizeObserver = FakeResizeObserver
    globalThis.MutationObserver = FakeMutationObserver
    globalThis.window = {
      addEventListener: (name, fn) => listeners.set(name, fn),
      removeEventListener: (name) => listeners.delete(name),
    }
    const restore = () => Object.assign(globalThis, saved)
    return { fired, listeners, restore }
  }

  test("a bar is measured again when it is lifted, which changes its reach and not its size", () => {
    const browser = fakeBrowser()
    try {
      let measured = 0
      const stop = watchOccupant({}, "bar", () => measured++)
      const [styled] = browser.fired.mutation
      expect(styled.options).toEqual({ attributes: true, attributeFilter: ["style"] })
      // The save bar's `bottom` is set to its lift: no resize, one mutation.
      styled.callback([])
      expect(measured).toBe(1)
      stop()
      expect(styled.gone).toBe(true)
      expect(browser.listeners.size).toBe(0)
    } finally {
      browser.restore()
    }
  })

  test("a dock is placed by the stack, so its style is not watched", () => {
    const browser = fakeBrowser()
    try {
      const stop = watchOccupant({}, "dock", () => {})
      expect(browser.fired.mutation).toHaveLength(0)
      expect(browser.fired.resize).toHaveLength(1)
      stop()
    } finally {
      browser.restore()
    }
  })

  test("the stack's top follows the upper bar once its lifted reach is measured", () => {
    // Before the lift is measured the save bar reports 72 and the top is 16px
    // over the bottom bar; measured again, the top is the save bar's.
    const before = stack([
      ["pane", { layer: "bar", order: 0, extent: 56 }],
      ["save", { layer: "bar", order: 1, extent: 72 }],
    ])
    const after = stack([
      ["pane", { layer: "bar", order: 0, extent: 56 }],
      ["save", { layer: "bar", order: 1, extent: before.bars.save + 72 }],
    ])
    expect(after.top).toBe(56 + 72)
  })
})
