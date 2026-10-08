import { describe, expect, test } from "bun:test"
import { usageMarkers } from "./usage-markers"

const T0 = Date.UTC(2026, 9, 8, 12, 0, 0)
const at = (seconds) => new Date(T0 + seconds * 1000).toISOString()
const ev = (seconds, action, over = {}) => ({
  time: at(seconds),
  type: "container",
  action,
  name: "web",
  id: "web-id",
  message: `web ${action}`,
  level: action === "die" ? "error" : "info",
  source: "daemon",
  ...over,
})

describe("usageMarkers", () => {
  // Forty exits of one loop are one incident on a chart, drawn as its span.
  test("folds a restart loop into one red mark across it", () => {
    const loop = []
    for (let i = 0; i < 4; i++) {
      loop.push(ev(i * 60, "die", { exitCode: "1" }))
      loop.push(ev(i * 60 + 2, "start"))
    }
    const marks = usageMarkers(loop)
    expect(marks).toHaveLength(1)
    expect(marks[0]).toMatchObject({
      ts: at(0),
      kind: "action",
      title: "restarted ×4",
      detail: "exit 1",
      severity: "error",
      durationSeconds: 182,
    })
  })

  test("tells a crash from a stop and names an OOM kill", () => {
    const marks = usageMarkers([
      ev(0, "die", { exitCode: "1" }),
      ev(600, "die", { exitCode: "143", level: "info" }),
      ev(1200, "oom"),
      ev(1200.5, "die", { exitCode: "137" }),
    ])
    expect(marks.map((mark) => [mark.title, mark.severity])).toEqual([
      ["exited (1)", "error"],
      ["stopped", "info"],
      ["killed for memory", "error"],
    ])
  })

  test("marks starts and a failing check, and nothing that explains no line", () => {
    const marks = usageMarkers([
      ev(0, "create"),
      ev(1, "start"),
      ev(30, "health_status: healthy"),
      ev(90, "health_status: unhealthy"),
      ev(95, "exec_start: wget -q http://localhost"),
      { ...ev(100, "connect"), type: "network" },
    ])
    expect(marks.map((mark) => mark.title)).toEqual(["started", "failing its health check"])
  })

  test("reads the polled and the followed copies of one event once, oldest first", () => {
    const start = ev(10, "start")
    const marks = usageMarkers([ev(20, "restart"), start, { ...start }])
    expect(marks.map((mark) => mark.ts)).toEqual([at(10), at(20)])
  })
})
