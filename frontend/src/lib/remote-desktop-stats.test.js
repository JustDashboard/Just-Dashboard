import { describe, expect, test } from "bun:test"
import { meterTunnel, meterVideo } from "./remote-desktop-stats"

describe("meterTunnel", () => {
  test("counts guacd's frames, the browser's answers, and the time between them", () => {
    let clock = 1000
    const sent = []
    const inner = []
    const tunnel = {
      oninstruction: (opcode, args) => inner.push([opcode, ...args]),
      sendMessage: (...messages) => sent.push(messages),
    }
    const meter = meterTunnel(tunnel, () => clock)
    // Three frames arrive in a second; the browser draws two of them, 30 ms
    // and 50 ms after each arrived, and a mouse move goes through untouched.
    tunnel.oninstruction("sync", ["100"])
    clock += 30
    tunnel.sendMessage("sync", 100)
    tunnel.oninstruction("img", ["0", "14", "0", "image/png", "0", "0"])
    tunnel.oninstruction("sync", ["200"])
    clock += 50
    tunnel.sendMessage("sync", 200)
    tunnel.sendMessage("mouse", 10, 20, 1)
    tunnel.oninstruction("sync", ["300"])
    clock = 2000
    expect(meter.read()).toEqual({ serverFps: 3, clientFps: 2, lagMs: 40 })
    expect(inner.map((i) => i[0])).toEqual(["sync", "img", "sync", "sync"])
    expect(sent).toEqual([
      ["sync", 100],
      ["sync", 200],
      ["mouse", 10, 20, 1],
    ])
    // The window starts again at each read; the frame still waiting, which
    // arrived at 1080, is answered in the next one.
    clock = 2500
    tunnel.sendMessage("sync", 300)
    clock = 3000
    expect(meter.read()).toEqual({ serverFps: 0, clientFps: 1, lagMs: 1420 })
  })
})

describe("meterVideo", () => {
  test("reports frames a second, the browser's own delay, and drops once a second", () => {
    let clock = 0
    let pending
    const reports = []
    let cancelled = 0
    const video = {
      requestVideoFrameCallback: (callback) => {
        pending = callback
        return 7
      },
      cancelVideoFrameCallback: () => {
        cancelled += 1
      },
      getVideoPlaybackQuality: () => ({ droppedVideoFrames: 4 }),
    }
    const stop = meterVideo(
      video,
      (stats) => reports.push(stats),
      () => clock,
    )
    for (let i = 0; i < 25; i++) {
      clock += 40
      pending(clock, { expectedDisplayTime: clock + 10, receiveTime: clock - 12 })
    }
    expect(reports).toEqual([{ fps: 25, delayMs: 22, dropped: 4 }])
    // Without a receive time, over the WebSocket, the delay is not claimed.
    for (let i = 0; i < 25; i++) {
      clock += 40
      pending(clock, { expectedDisplayTime: clock })
    }
    expect(reports[1]).toEqual({ fps: 25, delayMs: undefined, dropped: 4 })
    stop()
    expect(cancelled).toBe(1)
  })

  test("does nothing where the browser cannot count frames", () => {
    expect(typeof meterVideo({}, () => {})).toBe("function")
  })
})
