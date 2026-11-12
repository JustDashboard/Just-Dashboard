import { expect, test } from "bun:test"
import { beamLap } from "./beam-lap"

/** Where a frame puts the light's centre, and which way it faces. */
function read(frame, size) {
  const [, x, y, heading] = frame.transform.match(
    /^translate\((-?[\d.]+)px, (-?[\d.]+)px\) rotate\((-?[\d.]+)deg\)$/,
  )
  return { x: Number(x) + size / 2, y: Number(y) + size / 2, heading: Number(heading) }
}

test("a lap starts after the top-left corner, heading along the top, and closes on itself", () => {
  const lap = beamLap(420, 160, 64)
  const first = read(lap[0], 64)
  const last = read(lap.at(-1), 64)
  expect(lap[0].offset).toBe(0)
  expect(lap.at(-1).offset).toBe(1)
  expect(first).toEqual({ x: 64, y: 0, heading: 0 })
  expect(last.x).toBeCloseTo(64, 1)
  expect(last.y).toBeCloseTo(0, 1)
  expect(last.heading).toBe(360)
})

test("it runs clockwise: along the top, down the right, back along the bottom, up the left", () => {
  const places = beamLap(420, 160, 64).map((frame) => read(frame, 64))
  const facing = (heading) => places.filter((p) => p.heading === heading)
  expect(facing(0).map((p) => [p.x, p.y])).toEqual([
    [64, 0],
    [356, 0],
  ])
  expect(facing(90).map((p) => [p.x, p.y])).toEqual([
    [420, 64],
    [420, 96],
  ])
  expect(facing(180).map((p) => [p.x, p.y])).toEqual([
    [356, 160],
    [64, 160],
  ])
  expect(facing(270).map((p) => [p.x, p.y])).toEqual([
    [0, 96],
    [0, 64],
  ])
})

test("the light keeps one speed: every step takes the share of the lap it covers", () => {
  for (const [width, height] of [
    [420, 160],
    [700, 44],
    [96, 96],
  ]) {
    const lap = beamLap(width, height, 64, 8)
    const places = lap.map((frame) => read(frame, 64))
    let travelled = 0
    const steps = []
    for (let i = 1; i < lap.length; i++) {
      const length = Math.hypot(places[i].x - places[i - 1].x, places[i].y - places[i - 1].y)
      travelled += length
      steps.push([length, lap[i].offset - lap[i - 1].offset])
    }
    // A chord is a little shorter than its arc; at eight to a corner, under half a percent.
    for (const [length, share] of steps) expect(length / travelled / share).toBeCloseTo(1, 1)
  }
})

test("the turn never runs backwards, so nothing spins the long way round between frames", () => {
  const lap = beamLap(700, 44, 64)
  for (let i = 1; i < lap.length; i++) {
    expect(lap[i].offset).toBeGreaterThanOrEqual(lap[i - 1].offset)
    expect(read(lap[i], 64).heading).toBeGreaterThanOrEqual(read(lap[i - 1], 64).heading)
  }
})

test("a frame shorter than the light is two straight runs joined by half circles", () => {
  // The corners are cut at half the height, as `round 64px` is on a 44px row.
  const places = beamLap(700, 44, 64).map((frame) => read(frame, 64))
  expect(places[0]).toEqual({ x: 22, y: 0, heading: 0 })
  expect(places.filter((p) => p.heading === 90)).toHaveLength(1)
  expect(Math.max(...places.map((p) => p.x))).toBeCloseTo(700, 1)
  expect(Math.max(...places.map((p) => p.y))).toBeCloseTo(44, 1)
})

test("a frame with no size yet has no lap", () => {
  expect(beamLap(0, 160, 64)).toEqual([])
  expect(beamLap(420, 0, 64)).toEqual([])
  expect(beamLap(Number.NaN, 160, 64)).toEqual([])
})
