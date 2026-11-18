import { expect, test } from "bun:test"
import {
  ago,
  asSentence,
  byteParts,
  bytes,
  calendarDate,
  clock,
  clockAt,
  clockMinute,
  compactCount,
  dateFormatter,
  dayAt,
  dayHeading,
  dayMinuteAt,
  grouped,
  localInputValue,
  minuteAt,
  minuteSpan,
  plural,
  rate,
  sharePercent,
  shortDigest,
  shortSha,
  timestamp,
  weekdayAt,
  sentenceCase,
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

test("the API's words start with a capital where they stand alone", () => {
  expect(sentenceCase("not found")).toBe("Not found")
  expect(sentenceCase("too many requests, slow down")).toBe("Too many requests, slow down")
  expect(sentenceCase("  path is outside the permitted roots ")).toBe(
    "Path is outside the permitted roots",
  )
  expect(sentenceCase("Already a sentence.")).toBe("Already a sentence.")
  expect(sentenceCase("")).toBe("")
  // A program's name, a unit, a device or an identifier is not a word to raise.
  for (const kept of [
    'nginx: [emerg] unknown directive "foo"',
    "pg_dump: server version mismatch",
    "eth0 is down",
    "ssh.service is masked",
    "/etc/hosts is not writable",
    "x509: certificate signed by unknown authority",
    // A tool's own line: the command, a colon, what it said.
    "nft list set: permission denied",
    "tar: write error",
    // A command that is the sentence's subject keeps its own spelling.
    "nginx refuses the configuration without app.example.com",
    "gh did not answer",
    "systemd did not answer",
    "ufw already has that exact rule",
    "ip netns failed",
    // A command line, as it was run.
    "wget --spider http://localhost:5678/healthz",
    "tar -xzf backup.tgz failed",
  ])
    expect(sentenceCase(kept)).toBe(kept)
  // A sentence with a colon further in is still a sentence.
  expect(sentenceCase("could not connect to the server: connection refused")).toBe(
    "Could not connect to the server: connection refused",
  )
  // A product with a capital of its own, and a word that is also English, take it.
  expect(sentenceCase("docker is not installed")).toBe("Docker is not installed")
  expect(sentenceCase("ping failed")).toBe("Ping failed")
})

test("a reason on its own line ends on a full stop, unless it ends on something quoted", () => {
  expect(asSentence("not found")).toBe("Not found.")
  expect(asSentence("too many requests, slow down")).toBe("Too many requests, slow down.")
  expect(asSentence("Already a sentence.")).toBe("Already a sentence.")
  expect(asSentence("is it running?")).toBe("Is it running?")
  // After a path, a line number or a quoted value the stop would read as part of it.
  expect(asSentence('unknown directive "foo" in /etc/nginx/sites-available/app:3')).toBe(
    'Unknown directive "foo" in /etc/nginx/sites-available/app:3',
  )
  expect(asSentence('no site called "app"')).toBe('No site called "app"')
  expect(asSentence("cannot open /etc/hosts")).toBe("Cannot open /etc/hosts")
  expect(asSentence("wget --spider http://localhost:5678/healthz")).toBe(
    "wget --spider http://localhost:5678/healthz",
  )
  // A command's name is left as written, and its sentence still ends.
  expect(asSentence("gh did not answer")).toBe("gh did not answer.")
  expect(asSentence("retired-api is already used by another project")).toBe(
    "retired-api is already used by another project.",
  )
  expect(asSentence("line one\nline two")).toBe("Line one\nline two")
  expect(asSentence("")).toBe("")
})

test("a count is grouped the one way, whatever the reader's locale", () => {
  expect(grouped(0)).toBe("0")
  expect(grouped(999)).toBe("999")
  expect(grouped(1204)).toBe("1,204")
  expect(grouped(16_103_894)).toBe("16,103,894")
  expect(grouped(-4200)).toBe("-4,200")
  expect(plural(1, "row")).toBe("1 row")
  expect(plural(1204, "row")).toBe("1,204 rows")
  expect(plural(2, "index", "indexes")).toBe("2 indexes")
})

test("a compact count is a lower-case k, one decimal under a hundred of its unit", () => {
  expect(compactCount(0)).toBe("0")
  expect(compactCount(999)).toBe("999")
  expect(compactCount(1000)).toBe("1k")
  expect(compactCount(1204)).toBe("1.2k")
  expect(compactCount(1950)).toBe("2k")
  expect(compactCount(60_000)).toBe("60k")
  expect(compactCount(123_456)).toBe("123k")
  expect(compactCount(2_500_000)).toBe("2.5M")
  expect(compactCount(16_103_894)).toBe("16.1M")
  expect(compactCount(3_000_000_000)).toBe("3B")
  expect(compactCount(-28_600)).toBe("-28.6k")
  expect(compactCount(Number.NaN)).toBe("—")
  for (const n of [1204, 60_000, 999_999]) expect(compactCount(n)).not.toMatch(/K/)
})

test("a compact count that rounds to a thousand of its unit is the next unit", () => {
  expect(compactCount(999_960)).toBe("1M")
  expect(compactCount(999_400)).toBe("999k")
})

test("below exactBelow every digit is written", () => {
  expect(compactCount(9999, { exactBelow: 10_000 })).toBe("9,999")
  expect(compactCount(10_000, { exactBelow: 10_000 })).toBe("10k")
  expect(compactCount(28_600, { exactBelow: 10_000 })).toBe("28.6k")
  expect(compactCount(12.6, { exactBelow: 10_000 })).toBe("13")
  // A threshold under a thousand would write "0.5k".
  expect(compactCount(500, { exactBelow: 100 })).toBe("500")
})

test("an age is its largest unit, and just now for its first five seconds", () => {
  const at = 1_700_000_000_000
  expect(ago(at, at)).toBe("just now")
  expect(ago(at, at + 4_999)).toBe("just now")
  expect(ago(at, at + 5_000)).toBe("5s ago")
  expect(ago(at, at + 14_000)).toBe("14s ago")
  expect(ago(at, at + 150_000)).toBe("2m ago")
  expect(ago(at, at + 3 * 3_600_000 + 7_000)).toBe("3h ago")
  expect(ago(at, at + 47 * 3_600_000)).toBe("1d ago")
  expect(ago(at, at + 9 * 86_400_000)).toBe("9d ago")
  // Two clocks a little apart: an instant just ahead is now, not "-2s".
  expect(ago(at, at - 2_000)).toBe("just now")
})

test("an age in two units keeps the seconds", () => {
  const at = 1_700_000_000_000
  expect(ago(at, at + 150_000, { units: 2 })).toBe("2m 30s ago")
  expect(ago(at, at + 3_600_000, { units: 2 })).toBe("1h ago")
  expect(ago(at, at + 2_000, { units: 2 })).toBe("just now")
})

test("a day's heading is today, yesterday, then the date", () => {
  // The fixture clock ages with its worker, so the days are built from now.
  const now = Date.now()
  const daysAgo = (n) => {
    const d = new Date(now)
    d.setDate(d.getDate() - n)
    d.setHours(12, 0, 0, 0)
    return d.toISOString()
  }
  expect(dayHeading(new Date(now).toISOString(), now)).toBe("Today")
  expect(dayHeading(daysAgo(1), now)).toBe("Yesterday")
  const older = dayHeading(daysAgo(3), now)
  expect(older).toMatch(/^[A-Z][a-z]{2} \d{1,2}(, \d{4})?$/)
  expect(older).toBe(
    new Date(daysAgo(3)).getFullYear() === new Date(now).getFullYear()
      ? dayAt(new Date(daysAgo(3)))
      : calendarDate(daysAgo(3)),
  )
  expect(dayHeading(daysAgo(400), now)).toMatch(/^[A-Z][a-z]{2} \d{1,2}, \d{4}$/)
  expect(dayHeading("not a date", now)).toBe("—")
})

test("a digest is twelve hex characters with no algorithm and no ellipsis", () => {
  const hex = "4f1c2a9b8e7d6c5b4a39281706f5e4d3c2b1a09f8e7d6c5b4a3928170615e4d3"
  expect(shortDigest(`sha256:${hex}`)).toBe("4f1c2a9b8e7d")
  expect(shortDigest(hex)).toBe("4f1c2a9b8e7d")
  expect(shortDigest("release-2026-11-15-a")).toBe("release-2026")
  expect(shortDigest("image")).toBe("image")
  expect(shortDigest(undefined)).toBe("")
  expect(shortSha(hex)).toBe("4f1c2a9")
})

test("a share in whole percent never calls a sliver nothing or a near-whole all of it", () => {
  expect(sharePercent(0)).toBe("0%")
  expect(sharePercent(0.004)).toBe("<1%")
  expect(sharePercent(0.42)).toBe("42%")
  expect(sharePercent(0.996)).toBe(">99%")
  expect(sharePercent(1)).toBe("100%")
})

test("a date as a datetime-local input holds it is the wall clock with no zone", () => {
  const date = new Date(2026, 10, 15, 14, 3, 7, 250)
  expect(localInputValue(date)).toBe("2026-11-15T14:03:07.250")
})
