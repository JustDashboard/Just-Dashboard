import { expect, test } from "bun:test"
import {
  byteParts,
  bytes,
  calendarDate,
  clock,
  clockAt,
  clockMinute,
  dateFormatter,
  dayAt,
  dayMinuteAt,
  minuteAt,
  minuteSpan,
  rate,
  timestamp,
  weekdayAt,
} from "./format"

test("a value under one byte stays in bytes instead of reaching below the ladder", () => {
  expect(bytes(0.5)).toBe("1 B")
  expect(bytes(0.2)).toBe("0 B")
  expect(bytes(-0.2)).toBe("0 B")
  expect(bytes(-0.5)).not.toContain("undefined")
  expect(rate(0.5)).toBe("1 B/s")
  expect(rate(0.2)).toBe("0 B/s")
})

test("zero and an unknown value read as themselves", () => {
  expect(bytes(0)).toBe("0 B")
  expect(rate(0)).toBe("0 B/s")
  expect(bytes(undefined)).toBe("—")
  expect(bytes(null)).toBe("—")
  expect(bytes(Number.NaN)).toBe("—")
  expect(rate(undefined)).toBe("0 B/s")
})

test("each size takes its unit, at the precision asked for", () => {
  expect(bytes(1)).toBe("1 B")
  expect(bytes(1023)).toBe("1023 B")
  expect(bytes(1024)).toBe("1.0 KB")
  expect(bytes(1536, 2)).toBe("1.50 KB")
  expect(bytes(5 * 1024 ** 3)).toBe("5.0 GB")
  expect(rate(2048)).toBe("2.0 KB/s")
})

test("a negative value scales by its size and keeps its sign", () => {
  expect(bytes(-1)).toBe("-1 B")
  expect(bytes(-2048)).toBe("-2.0 KB")
  expect(rate(-2048)).toBe("-2.0 KB/s")
})

test("past the top of the ladder a value stays in its last unit", () => {
  expect(bytes(1024 ** 5)).toBe("1.0 PB")
  expect(bytes(2 ** 70)).toBe("1048576.0 PB")
})

test("byteParts and bytes agree about values under one byte", () => {
  expect(byteParts(0.5)).toEqual({ value: 0.5, unit: "B", decimals: 0 })
  expect(byteParts(-3)).toEqual({ value: 0, unit: "B", decimals: 0 })
})

// What each format replaced, so the built-once formats print the same words.
const BEFORE = [
  [clockAt, (d) => d.toLocaleTimeString(undefined, { hour12: false })],
  [
    minuteAt,
    (d) => d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit", hour12: false }),
  ],
  [dayAt, (d) => d.toLocaleDateString(undefined, { month: "short", day: "numeric" })],
  [
    weekdayAt,
    (d) => d.toLocaleDateString(undefined, { weekday: "short", month: "short", day: "numeric" }),
  ],
  [
    dayMinuteAt,
    (d) =>
      d.toLocaleString(undefined, {
        month: "short",
        day: "numeric",
        hour: "2-digit",
        minute: "2-digit",
        hour12: false,
      }),
  ],
  [
    (d) => timestamp(d.toISOString()),
    (d) =>
      d.toLocaleString(undefined, {
        year: "numeric",
        month: "short",
        day: "2-digit",
        hour: "2-digit",
        minute: "2-digit",
        second: "2-digit",
      }),
  ],
  [
    (d) => calendarDate(d.toISOString()),
    (d) => d.toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" }),
  ],
  [(d) => clock(d.toISOString()), (d) => d.toLocaleTimeString(undefined, { hour12: false })],
  [
    (d) => clockMinute(d.toISOString()),
    (d) => d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit", hour12: false }),
  ],
]

// Midnight, noon, a single-digit hour and day, the last second of a year.
const MOMENTS = [
  "2026-11-10T00:00:00Z",
  "2026-11-10T12:00:00Z",
  "2026-03-05T09:05:03Z",
  "2026-12-31T23:59:59Z",
  "2024-02-29T16:30:00Z",
]

// `bun test` runs in UTC without setting TZ. The way back is to set it:
// once TZ has been deleted, bun stops following later changes to it, here
// and in every test file after this one.
function inZone(zone, run) {
  const before = process.env.TZ ?? "UTC"
  process.env.TZ = zone
  try {
    run()
  } finally {
    process.env.TZ = before
  }
}

test("a built-once format prints what the toLocale call it replaced printed", () => {
  for (const zone of ["UTC", "America/New_York", "Asia/Kolkata"]) {
    inZone(zone, () => {
      for (const iso of MOMENTS) {
        const date = new Date(iso)
        for (const [now, before] of BEFORE) expect(now(date)).toBe(before(date))
      }
    })
  }
})

test("a format takes a Date or its milliseconds, and a date that is not one says so", () => {
  const date = new Date("2026-11-10T12:34:56Z")
  expect(clockAt(date.getTime())).toBe(clockAt(date))
  expect(clockAt(new Date("nonsense"))).toBe("Invalid Date")
  expect(clockAt(Number.NaN)).toBe("Invalid Date")
  expect(dayAt(Number.POSITIVE_INFINITY)).toBe("Invalid Date")
  expect(clock("nonsense")).toBe("—")
})

test("a format is built once, however many dates it prints", () => {
  const Real = Intl.DateTimeFormat
  let built = 0
  Intl.DateTimeFormat = function (...args) {
    built++
    return new Real(...args)
  }
  try {
    const format = dateFormatter({ hour: "2-digit", minute: "2-digit" })
    expect(built).toBe(0)
    for (let i = 0; i < 500; i++) format(1_700_000_000_000 + i * 60_000)
    expect(built).toBe(1)
  } finally {
    Intl.DateTimeFormat = Real
  }
})

test("a zone that moves under an open tab is followed, as toLocaleString followed it", () => {
  const date = new Date("2026-11-10T12:00:00Z")
  const format = dateFormatter({ hour: "2-digit", minute: "2-digit", hour12: false })
  inZone("UTC", () => expect(format(date)).toBe("12:00"))
  inZone("Asia/Tokyo", () => expect(format(date)).toBe("21:00"))
  inZone("UTC", () => expect(format(date)).toBe("12:00"))
})

test("a format in a named locale keeps that locale's order", () => {
  const british = dateFormatter({ day: "numeric", month: "short", timeZone: "UTC" }, "en-GB")
  expect(british(new Date("2026-11-10T12:00:00Z"))).toBe("10 Nov")
})

test("a span inside one day is two clocks, and one across days names both days", () => {
  inZone("UTC", () => {
    expect(minuteSpan("2026-11-10T11:46:00Z", "2026-11-10T11:51:00Z")).toBe("11:46–11:51")
    expect(minuteSpan("2026-11-10T11:46:00Z")).toBe("11:46–now")
    const across = minuteSpan("2026-11-09T23:50:00Z", "2026-11-10T00:10:00Z")
    expect(across).toContain("Nov 9")
    expect(across).toContain("Nov 10")
  })
})
