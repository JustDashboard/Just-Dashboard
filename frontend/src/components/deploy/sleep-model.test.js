import { expect, test } from "bun:test"
import {
  asleepShare,
  holdSeconds,
  idleSpan,
  lastWakeLine,
  sleepDraftOf,
  sleepPlanOf,
  sleepPolicyLine,
  sleepRefusal,
  sleepShown,
  sleepTerms,
  spanLength,
  storedSleep,
  wakeCause,
} from "./sleep-model"

test("an absent term is the server's default", () => {
  expect(sleepTerms(undefined)).toEqual({ idleMinutes: 15, wakeTimeoutSeconds: 60 })
  expect(sleepTerms({ enabled: true, idleMinutes: 30 })).toEqual({
    idleMinutes: 30,
    wakeTimeoutSeconds: 60,
  })
})

test("off with nothing typed is stored as nothing, and terms typed while off are kept", () => {
  expect(storedSleep({ enabled: false })).toBeUndefined()
  expect(storedSleep({ enabled: false, idleMinutes: 20 })).toEqual({
    enabled: false,
    idleMinutes: 20,
  })
  expect(sleepPlanOf(sleepDraftOf(undefined))).toBeUndefined()
  expect(sleepPlanOf({ enabled: true, idleMinutes: "", wakeTimeoutSeconds: "0" })).toEqual({
    enabled: true,
  })
  expect(sleepPlanOf({ enabled: true, idleMinutes: "45", wakeTimeoutSeconds: "90" })).toEqual({
    enabled: true,
    idleMinutes: 45,
    wakeTimeoutSeconds: 90,
  })
  expect(sleepDraftOf({ enabled: true, idleMinutes: 45 })).toEqual({
    enabled: true,
    idleMinutes: "45",
    wakeTimeoutSeconds: "",
  })
})

test("terms outside the server's bounds are refused under their field", () => {
  const draft = (fields) => ({ enabled: true, idleMinutes: "", wakeTimeoutSeconds: "", ...fields })
  expect(sleepRefusal(draft({}))).toBeUndefined()
  expect(sleepRefusal(draft({ idleMinutes: "4" }))?.field).toBe("idleMinutes")
  expect(sleepRefusal(draft({ idleMinutes: "1441" }))?.message).toContain("from 5 to 1440")
  expect(sleepRefusal(draft({ idleMinutes: "7.5" }))?.field).toBe("idleMinutes")
  expect(sleepRefusal(draft({ wakeTimeoutSeconds: "601" }))?.field).toBe("wakeTimeoutSeconds")
  expect(sleepRefusal(draft({ wakeTimeoutSeconds: "soon" }))?.message).toBe(
    "The wake limit is a number.",
  )
})

test("the head says what the next release does", () => {
  expect(sleepPolicyLine(undefined)).toBe("Off · the container runs whether or not anyone visits")
  expect(sleepPolicyLine({ enabled: true })).toBe(
    "On · stopped after 15 min without a request, woken within 60 s",
  )
  expect(sleepPolicyLine({ enabled: true, idleMinutes: 90, wakeTimeoutSeconds: 120 })).toBe(
    "On · stopped after 1 h 30 min without a request, woken within 120 s",
  )
  expect(idleSpan(1440)).toBe("1 day")
  expect(idleSpan(120)).toBe("2 h")
  expect(holdSeconds({ enabled: true, wakeTimeoutSeconds: 10 })).toBe(10)
  expect(holdSeconds({ enabled: true, wakeTimeoutSeconds: 300 })).toBe(30)
})

test("a stretch says what woke it and how long it took", () => {
  const span = {
    id: 1,
    environmentId: 4,
    releaseId: 9,
    idleMinutes: 15,
    reason: "No request reached it for 15 minutes.",
    sleptAt: "2026-10-29T08:00:00Z",
    wokeAt: "2026-10-29T09:12:00Z",
    wakeTrigger: "request",
    wakeDetail: "shop.example.test",
    wakeSeconds: 1.24,
  }
  expect(wakeCause(span)).toBe("a request to shop.example.test")
  expect(wakeCause({ ...span, wakeTrigger: "manual", wakeDetail: "ada" })).toBe("Wake now, by ada")
  expect(wakeCause({ ...span, wakeTrigger: "run", wakeDetail: "run #12" })).toBe("run #12")
  const status = {
    enabled: true,
    idleMinutes: 15,
    wakeTimeoutSeconds: 60,
    holdSeconds: 30,
    state: "asleep",
    recent: [{ ...span, id: 2, wokeAt: undefined }, span],
    asleepSeconds: 3 * 24 * 3600 + 12 * 3600,
  }
  expect(lastWakeLine(status)).toBe("Last woken by a request to shop.example.test in 1.2 s")
  expect(sleepShown(status)).toBe(true)
  expect(sleepShown({ ...status, state: "awake" })).toBe(false)
  expect(asleepShare(status)).toBeCloseTo(0.5, 5)
  expect(spanLength(45)).toBe("45 s")
  expect(spanLength(3 * 3600 + 20 * 60)).toBe("3 h 20 min")
  expect(spanLength(5 * 24 * 3600)).toBe("5 days")
})
