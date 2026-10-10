import { afterEach, expect, test } from "bun:test"
import {
  clearCrosshair,
  getCrosshair,
  pinCrosshair,
  setCrosshair,
  subscribeCrosshair,
  unpinCrosshair,
} from "./metrics-crosshair"

afterEach(unpinCrosshair)

test("a pinned instant survives movement and mouse leave until explicitly released", () => {
  pinCrosshair(2000)
  setCrosshair(3000, "cpu")
  clearCrosshair()
  expect(getCrosshair()).toEqual({ ts: 2000, source: null, pinned: true })
  pinCrosshair(1000)
  expect(getCrosshair().ts).toBe(1000)
  unpinCrosshair()
  setCrosshair(3000, "cpu")
  expect(getCrosshair()).toEqual({ ts: 3000, source: "cpu" })
})

test("pinning cancels a queued pointer update", () => {
  const raf = globalThis.requestAnimationFrame
  const caf = globalThis.cancelAnimationFrame
  let queued
  let cancelled = false
  globalThis.requestAnimationFrame = (run) => {
    queued = run
    return 1
  }
  globalThis.cancelAnimationFrame = () => {
    cancelled = true
  }
  let updates = 0
  const stop = subscribeCrosshair(() => updates++)
  try {
    setCrosshair(1000, "cpu")
    pinCrosshair(2000)
    queued()
    expect(cancelled).toBe(true)
    expect(getCrosshair().ts).toBe(2000)
    expect(updates).toBe(1)
  } finally {
    stop()
    globalThis.requestAnimationFrame = raf
    globalThis.cancelAnimationFrame = caf
  }
})
