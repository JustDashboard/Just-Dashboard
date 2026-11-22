import { describe, expect, test } from "bun:test"
import { applyRunEvent } from "./run-events"

const snapshot = (run = {}, steps = []) => ({
  run: { id: 84, state: "queued", requestedAt: "2026-09-03T12:00:00Z", ...run },
  steps,
})
const step = (fields = {}) => ({
  id: 105,
  runId: 84,
  key: "build_artifact",
  ordinal: 5,
  state: "pending",
  attempt: 1,
  timeoutSeconds: 600,
  evidence: {},
  lastSeq: 1,
  ...fields,
})
const stepEvent = (data, fields = {}) => ({
  seq: 10,
  type: "step.state",
  runId: 84,
  stepId: 105,
  ts: "2026-09-03T12:00:05Z",
  data: { key: "build_artifact", attempt: 1, evidence: {}, ...data },
  ...fields,
})
const runEvent = (data, ts = "2026-09-03T12:00:02Z") => ({
  seq: 9,
  type: "run.state",
  runId: 84,
  ts,
  data,
})

describe("applyRunEvent", () => {
  test("a step that starts after the snapshot takes the engine's start", () => {
    const next = applyRunEvent(
      snapshot({}, [step()]),
      stepEvent({ state: "running", startedAt: "2026-09-03T12:00:04Z" }),
    )
    expect(next.steps[0]).toMatchObject({ state: "running", startedAt: "2026-09-03T12:00:04Z" })
    expect(next.steps[0].endedAt).toBeUndefined()
  })

  test("an engine that sent no times has its event's instant read for them", () => {
    let next = applyRunEvent(snapshot({}, [step()]), stepEvent({ state: "running" }))
    expect(next.steps[0].startedAt).toBe("2026-09-03T12:00:05Z")
    next = applyRunEvent(next, stepEvent({ state: "passed" }, { ts: "2026-09-03T12:01:00Z" }))
    expect(next.steps[0]).toMatchObject({
      startedAt: "2026-09-03T12:00:05Z",
      endedAt: "2026-09-03T12:01:00Z",
    })
  })

  test("a start already known is kept, and the engine's end wins over the event's", () => {
    const next = applyRunEvent(
      snapshot({}, [step({ state: "running", startedAt: "2026-09-03T11:59:00Z" })]),
      stepEvent({ state: "passed", endedAt: "2026-09-03T12:00:04Z" }),
    )
    expect(next.steps[0]).toMatchObject({
      startedAt: "2026-09-03T11:59:00Z",
      endedAt: "2026-09-03T12:00:04Z",
    })
  })

  test("a retry is a new attempt with no times of the one before", () => {
    const failed = step({
      state: "failed",
      startedAt: "2026-09-03T11:59:00Z",
      endedAt: "2026-09-03T11:59:30Z",
      errorCode: "build_failed",
      evidence: { result: {} },
    })
    const next = applyRunEvent(
      snapshot({}, [failed]),
      stepEvent({ state: "pending", attempt: 2 }, { stepId: 205 }),
    )
    expect(next.steps).toHaveLength(2)
    expect(next.steps[1]).toMatchObject({ id: 205, attempt: 2, state: "pending", ordinal: 5 })
    expect(next.steps[1].startedAt).toBeUndefined()
    expect(next.steps[1].endedAt).toBeUndefined()
    expect(next.steps[1].errorCode).toBeUndefined()
    expect(next.steps[1].evidence).toEqual({})
  })

  test("a claimed run is running from its claim, and an ended one stops there", () => {
    let next = applyRunEvent(
      snapshot(),
      runEvent({ state: "preparing", claimedAt: "2026-09-03T12:00:01Z" }),
    )
    expect(next.run).toMatchObject({ state: "preparing", claimedAt: "2026-09-03T12:00:01Z" })
    expect(next.run.endedAt).toBeUndefined()
    next = applyRunEvent(next, runEvent({ state: "running" }, "2026-09-03T12:00:09Z"))
    expect(next.run.claimedAt).toBe("2026-09-03T12:00:01Z")
    next = applyRunEvent(
      next,
      runEvent({ state: "failed", code: "readiness", endedAt: "2026-09-03T12:02:00Z" }),
    )
    expect(next.run).toMatchObject({
      state: "failed",
      terminalCode: "readiness",
      endedAt: "2026-09-03T12:02:00Z",
    })
  })

  test("an older engine's claim and end are read from the events' instants", () => {
    let next = applyRunEvent(snapshot(), runEvent({ state: "preparing" }))
    expect(next.run.claimedAt).toBe("2026-09-03T12:00:02Z")
    next = applyRunEvent(next, runEvent({ state: "cancelled" }, "2026-09-03T12:00:30Z"))
    expect(next.run.endedAt).toBe("2026-09-03T12:00:30Z")
  })

  test("a run cancelled while queued never claims a slot", () => {
    const next = applyRunEvent(snapshot(), runEvent({ state: "cancelling" }))
    expect(next.run.claimedAt).toBeUndefined()
    expect(next.run.endedAt).toBeUndefined()
  })
})
