import { expect, test } from "bun:test"
import {
  DAY_HOURS,
  dayReading,
  dayStart,
  failedWords,
  hourTone,
  hourWords,
  peopleWords,
} from "./day"

const HOUR = 3_600_000
const now = Date.UTC(2026, 10, 13, 14, 37)
const start = dayStart(now)
const at = (hoursAgo) => new Date(now - hoursAgo * HOUR).toISOString()
const entry = (fields) => ({
  username: "operator",
  actor: "session",
  ip: "100.64.1.2",
  action: "docker.container.restart",
  success: true,
  ...fields,
})

test("the day starts on the hour, 23 hours before the running one", () => {
  expect(new Date(start).toISOString()).toBe("2026-11-12T15:00:00.000Z")
  // The same question for the whole of an hour.
  expect(dayStart(now + 20 * 60_000)).toBe(start)
  expect(dayStart(now + 30 * 60_000)).toBe(start + HOUR)
})

test("a day is its hours, oldest first, each with what failed in it", () => {
  const day = dayReading(
    [
      entry({ ts: at(0.1) }),
      entry({ ts: at(0.2), action: "auth.login", success: false, actor: "anonymous" }),
      entry({ ts: at(5), action: "git.clone" }),
      entry({ ts: at(5.1), action: "git.clone", success: false }),
      entry({ ts: at(6), action: "docker.container.stop", success: false }),
    ],
    start,
  )
  expect(day.hours).toHaveLength(DAY_HOURS)
  expect(day.hours.at(-1)).toMatchObject({ changes: 2, failed: 1 })
  expect(hourTone(day.hours.at(-1))).toBe("dented")
  expect(day.hours.at(-6)).toMatchObject({ changes: 2, failed: 1, sections: ["Git"] })
  expect(day.hours.at(-7)).toMatchObject({ changes: 1, failed: 1 })
  expect(hourTone(day.hours.at(-7))).toBe("failed")
  expect(hourTone(day.hours[0])).toBe("quiet")
  expect(new Date(day.hours.at(-1).from).toISOString()).toBe("2026-11-13T14:00:00.000Z")
})

test("people are ranked by what they changed and machines are counted apart", () => {
  const day = dayReading(
    [
      entry({ ts: at(1), username: "mira", actor: "token" }),
      entry({ ts: at(1), username: "operator" }),
      entry({ ts: at(2), username: "operator" }),
      entry({ ts: at(2), username: "", actor: "webhook" }),
      entry({ ts: at(3), username: "", actor: "system" }),
      // A refused sign-in names an account nobody proved they hold.
      entry({ ts: at(3), username: "admin", actor: "anonymous", action: "auth.login" }),
    ],
    start,
  )
  expect(day.people).toEqual(["operator", "mira"])
  expect(day.machines).toBe(2)
  expect(day.sections[0]).toEqual({ label: "Docker", changes: 5 })
})

test("the last sign-in is the newest one that was let in", () => {
  const day = dayReading(
    [
      entry({
        ts: at(1),
        action: "auth.login",
        success: false,
        username: "admin",
        ip: "45.12.3.4",
      }),
      entry({ ts: at(2), action: "auth.login", username: "mira", ip: "192.168.1.20" }),
      entry({ ts: at(3), action: "auth.login", ip: "100.64.1.2" }),
    ],
    start,
  )
  expect(day.signIns).toEqual({
    ok: 2,
    refused: 1,
    last: { username: "mira", ip: "192.168.1.20", ts: at(2) },
  })
})

test("an entry outside the day or with no date is left out of the hours", () => {
  const day = dayReading([entry({ ts: at(30) }), entry({ ts: "never" })], start)
  expect(day.hours.reduce((sum, hour) => sum + hour.changes, 0)).toBe(0)
  expect(day.people).toEqual(["operator"])
})

test("an hour is said in words", () => {
  const hour = (fields) => ({ from: start, changes: 0, failed: 0, sections: [], ...fields })
  expect(hourWords(hour({}))).toBe("nothing was changed")
  expect(hourWords(hour({ changes: 3, sections: ["Docker", "Git"] }))).toBe(
    "3 changes · Docker, Git",
  )
  expect(hourWords(hour({ changes: 3, failed: 1, sections: ["Sign-in"] }))).toBe(
    "3 changes, 1 failed · Sign-in",
  )
  expect(hourWords(hour({ changes: 1, failed: 1 }))).toBe("1 change, and it failed")
  expect(hourWords(hour({ changes: 2, failed: 2 }))).toBe("2 changes, all failed")
})

test("the day's verdict and its people are sentences", () => {
  expect(failedWords(0, 0)).toBe("Nothing was changed")
  expect(failedWords(0, 8)).toBe("Every change went through")
  expect(failedWords(1, 8)).toBe("1 of 8 failed")
  expect(peopleWords([], 0)).toBeUndefined()
  expect(peopleWords([], 2)).toBe("only automations, 2 changes")
  expect(peopleWords(["operator"], 0)).toBe("operator")
  expect(peopleWords(["operator", "mira"], 1)).toBe("2 people and 1 automated change")
})
