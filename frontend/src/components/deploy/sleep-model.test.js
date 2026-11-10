import { expect, test } from "bun:test"
import {
  asleepShare,
  holdSeconds,
  idleSpan,
  lastWakeLine,
  sleepDraftOf,
  sleepLead,
  sleepPlanOf,
  sleepPolicyLine,
  sleepRefusal,
  sleepShown,
  sleepTerms,
  sleepWeek,
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

test("the Overview's block says asleep on purpose, for how long, and what a visitor meets", () => {
  const now = Date.parse("2026-11-10T12:00:00Z")
  const status = {
    enabled: true,
    idleMinutes: 15,
    wakeTimeoutSeconds: 60,
    holdSeconds: 30,
    state: "asleep",
    since: "2026-11-10T11:18:00Z",
    reason: "No request reached it for 15 minutes; the last was at 11:02 UTC.",
    recent: [
      { id: 2, sleptAt: "2026-11-10T11:18:00Z" },
      {
        id: 1,
        sleptAt: "2026-11-09T12:00:00Z",
        wokeAt: "2026-11-09T18:00:00Z",
        wakeTrigger: "request",
        wakeDetail: "api.example.test",
        wakeSeconds: 1.2,
      },
      // Began before the week: cut at its edge.
      { id: 0, sleptAt: "2026-11-01T00:00:00Z", wokeAt: "2026-11-04T00:00:00Z" },
    ],
    asleepSeconds: 5 * 3600,
  }
  expect(sleepLead(status, false, now)).toEqual({
    title: "Asleep by design for 42 min",
    hint: "No request reached it for 15 minutes; the last was at 11:02 UTC. The next request to its domain starts it and is answered by it, held for up to 30 s. Last woken by a request to api.example.test in 1.2 s.",
  })
  expect(sleepLead(status, true, now).title).toBe("Waking up: its container is starting")
  expect(sleepLead({ ...status, since: undefined }, false, now).title).toBe("Asleep by design")

  const week = sleepWeek(status, now)
  expect(week.map((stretch) => stretch.key)).toEqual([0, 1, 2])
  // The oldest starts on the strip's edge and the open one ends on now.
  expect(week[0].left).toBe(0)
  expect(week[0].width).toBeCloseTo(0.5 / 7, 5)
  expect(week[1].width).toBeCloseTo(0.25 / 7, 5)
  expect(week[2].left + week[2].width).toBeCloseTo(1, 5)
  expect(sleepWeek({ ...status, recent: [] }, now)).toEqual([])
})
