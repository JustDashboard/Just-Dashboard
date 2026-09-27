import { describe, expect, test } from "bun:test"
import { crashLoop, foldRuns, runLength, runWindow, unitRuns } from "./unit-runs"

// Lines as the systemd lens hands them over for `journal:<unit>`: the
// manager's sentences (the research's real ones, `research-system-logs.md`),
// each named and with the journal's invocation and exit fields kept.
const at = (clock, day = "20") => `2026-09-${day}T${clock}Z`
const line = (clock, event, text, attrs = {}, day) => ({
  text,
  timestamp: at(clock, day),
  level: "info",
  event,
  attrs: { unit: "nordvpnd.service", program: "systemd", ...attrs },
})

/** One turn of a crash loop: it starts, exits 1, fails, and is restarted. */
function crash(invocation, clock, counter) {
  const [h, m, s] = clock.split(":").map(Number)
  const t = (plus) =>
    `${String(h).padStart(2, "0")}:${String(m).padStart(2, "0")}:${String(s + plus).padStart(2, "0")}.000`
  return [
    line(t(0), "starting", "Starting nordvpnd.service - NordVPN Daemon...", { invocation }),
    line(t(0), "started", "Started nordvpnd.service - NordVPN Daemon.", { invocation }),
    line(t(1), "exited", "nordvpnd.service: Main process exited, code=exited, status=1/FAILURE", {
      invocation,
      exit_code: "exited",
      exit_status: "1",
    }),
    line(t(1), "failed", "nordvpnd.service: Failed with result 'exit-code'.", {
      invocation,
      result: "exit-code",
    }),
    line(
      t(6),
      "restart_scheduled",
      `nordvpnd.service: Scheduled restart job, restart counter is at ${counter}.`,
      { invocation, restarts: String(counter) },
    ),
    line(t(6), "stopped", "Stopped nordvpnd.service - NordVPN Daemon.", { invocation }),
  ]
}

describe("unitRuns", () => {
  test("a timer's oneshot is one run from its start to its deactivation, with its cost", () => {
    const runs = unitRuns([
      line("00:00:01.100", "starting", "Starting logrotate.service - Rotate log files...", {
        invocation: "a1",
      }),
      line("00:00:07.600", "deactivated", "logrotate.service: Deactivated successfully.", {
        invocation: "a1",
      }),
      // "Finished" is the lens's `started`: a oneshot's job ends after its
      // process does, and it is still the same run.
      line("00:00:07.601", "started", "Finished logrotate.service - Rotate log files.", {
        invocation: "a1",
      }),
      line(
        "00:00:07.602",
        "resources",
        "logrotate.service: Consumed 6.514s CPU time, 466.9M memory peak.",
        {
          invocation: "a1",
          cpu: "6514",
          memory: "489580134",
        },
      ),
    ])
    expect(runs).toHaveLength(1)
    const [run] = runs
    expect(run.outcome).toBe("succeeded")
    expect(run.start).toBe(at("00:00:01.100"))
    expect(run.end).toBe(at("00:00:07.600"))
    expect(runLength(run)).toBe(6500)
    expect(run.cpu).toBe(6514)
    expect(run.memory).toBe(489580134)
  })

  test("a crash is a failure with its exit, even though a restart followed it", () => {
    const runs = unitRuns([...crash("c1", "00:54:30", 48214), ...crash("c2", "00:54:36", 48215)])
    expect(runs.map((r) => r.invocation)).toEqual(["c1", "c2"])
    expect(runs[1]).toMatchObject({
      outcome: "failed",
      exitCode: "exited",
      exitStatus: "1",
      result: "exit-code",
      restarted: true,
      restarts: 48215,
      // The end is the exit, not the restart that came five seconds later.
      end: at("00:54:37.000"),
      last: at("00:54:42.000"),
    })
  })

  test("the newest run with no ending is running; an older one merely ended", () => {
    const runs = unitRuns([
      line("09:00:00.000", "starting", "Starting nginx.service...", { invocation: "old" }),
      line("09:00:00.100", "started", "Started nginx.service.", { invocation: "old" }),
      line("10:00:00.000", "starting", "Starting nginx.service...", { invocation: "new" }),
      line("10:00:00.100", "started", "Started nginx.service.", { invocation: "new" }),
    ])
    expect(runs.map((r) => r.outcome)).toEqual(["ended", "running"])
    expect(runLength(runs[1], Date.parse(at("10:00:10.000")))).toBe(10_000)
  })

  test("a stop that was asked for is not a failure, and a kill is not a clean stop", () => {
    const runs = unitRuns([
      line("09:00:00.000", "starting", "Starting api.service...", { invocation: "s" }),
      line("09:30:00.000", "deactivated", "api.service: Deactivated successfully.", {
        invocation: "s",
      }),
      line("09:30:00.001", "stopped", "Stopped api.service.", { invocation: "s" }),
      line("09:31:00.000", "starting", "Starting api.service...", { invocation: "k" }),
      line(
        "09:40:00.000",
        "killed",
        "api.service: Main process exited, code=killed, status=9/KILL",
        {
          invocation: "k",
          exit_code: "killed",
          exit_status: "9",
          signal: "KILL",
        },
      ),
    ])
    expect(runs.map((r) => r.outcome)).toEqual(["stopped", "killed"])
    expect(runs[1].signal).toBe("KILL")
  })

  test("a journal without invocations is sequenced from one start to the next", () => {
    const plain = (clock, event, text) => ({ ...line(clock, event, text), attrs: {} })
    const runs = unitRuns([
      plain("01:00:00.000", "starting", "Starting backup.service..."),
      plain("01:00:09.000", "deactivated", "backup.service: Deactivated successfully."),
      plain("01:00:09.001", "started", "Finished backup.service."),
      plain("02:00:00.000", "starting", "Starting backup.service..."),
      plain("02:00:03.000", "failed", "backup.service: Failed with result 'exit-code'."),
    ])
    expect(runs.map((r) => [r.invocation, r.outcome, r.start])).toEqual([
      ["", "succeeded", at("01:00:00.000")],
      ["", "failed", at("02:00:00.000")],
    ])
  })
})

describe("foldRuns", () => {
  test("identical runs fold into one row, newest first, and a different one stands alone", () => {
    const lines = [
      ...crash("c1", "00:54:00", 1),
      ...crash("c2", "00:54:10", 2),
      ...crash("c3", "00:54:20", 3),
      line("00:55:00.000", "starting", "Starting nordvpnd.service...", { invocation: "ok" }),
      line("00:55:00.100", "started", "Started nordvpnd.service.", { invocation: "ok" }),
    ]
    const groups = foldRuns(unitRuns(lines))
    expect(groups.map((g) => g.map((r) => r.invocation))).toEqual([["ok"], ["c3", "c2", "c1"]])
  })

  test("a run that is still going never folds into the ones before it", () => {
    const runs = unitRuns([
      line("09:00:00.000", "starting", "Starting x.service...", { invocation: "a" }),
      line("09:00:01.000", "started", "Started x.service.", { invocation: "a" }),
    ])
    expect(foldRuns([...runs, ...runs])).toHaveLength(2)
  })
})

describe("crashLoop", () => {
  const loop = [
    ...crash("c1", "00:54:00", 7),
    ...crash("c2", "00:54:10", 8),
    ...crash("c3", "00:54:20", 9),
  ]

  test("three restarts inside ten minutes is a loop, still going when the last was recent", () => {
    const found = crashLoop(loop, Date.parse(at("00:58:00.000")))
    expect(found).toEqual({
      count: 3,
      from: at("00:54:06.000"),
      to: at("00:54:26.000"),
      ongoing: true,
      counter: 9,
    })
  })

  test("the same burst a day ago is a loop that stopped", () => {
    expect(crashLoop(loop, Date.parse(at("00:58:00.000", "21")))?.ongoing).toBe(false)
  })

  test("restarts spread over hours are not a loop", () => {
    const spread = [
      ...crash("a", "01:00:00", 1),
      ...crash("b", "02:00:00", 2),
      ...crash("c", "03:00:00", 3),
    ]
    expect(crashLoop(spread)).toBeUndefined()
  })
})

describe("runWindow", () => {
  test("a run's lines are its span and its invocation, a second either side", () => {
    const [run] = unitRuns(crash("c1", "00:54:00", 1))
    expect(runWindow(run)).toEqual({
      since: at("00:53:59.000"),
      until: at("00:54:07.000"),
      fields: { invocation: ["c1"] },
    })
  })

  test("an untagged run is its span alone, and a running one reaches now", () => {
    const [run] = unitRuns([
      { text: "Starting x.service...", timestamp: at("09:00:00.000"), event: "starting" },
    ])
    expect(runWindow(run, Date.parse(at("09:05:00.000")))).toEqual({
      since: at("08:59:59.000"),
      until: at("09:05:01.000"),
      fields: {},
    })
  })
})
