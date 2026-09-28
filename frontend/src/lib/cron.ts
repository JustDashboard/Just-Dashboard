/**
 * Cron schedules, read and written.
 *
 * A crontab line says `0 3 * * 1` and expects the reader to know that means
 * three in the morning on Mondays. The Scheduled page shows every job with
 * that sentence beside the expression and the moment it next fires under it,
 * which is the difference between a list of jobs and a list of five-field
 * puzzles. The same parser drives the schedule builder in the job dialog, so
 * what the builder writes is what the reader sees described.
 *
 * Only what Vixie cron accepts: five fields, `*`, lists, ranges, steps, the
 * month and weekday names, and the `@` nicknames. `nextCronRun` computes in
 * this browser's clock and time zone — cron runs on the server's — and the
 * page says so beside the figure; a schedule that names its own zone (a
 * deployment's) is read in that zone by `nextCronRunsIn`.
 */

export type CronFields = {
  minute: Set<number>
  hour: Set<number>
  dayOfMonth: Set<number>
  month: Set<number>
  dayOfWeek: Set<number>
  /** Whether the day-of-month and day-of-week fields were both restricted. */
  anyDayOfMonth: boolean
  anyDayOfWeek: boolean
}

const NICKNAMES: Record<string, string> = {
  "@yearly": "0 0 1 1 *",
  "@annually": "0 0 1 1 *",
  "@monthly": "0 0 1 * *",
  "@weekly": "0 0 * * 0",
  "@daily": "0 0 * * *",
  "@midnight": "0 0 * * *",
  "@hourly": "0 * * * *",
}

const MONTHS = ["jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"]
const DAYS = ["sun", "mon", "tue", "wed", "thu", "fri", "sat"]
const MONTH_NAMES = [
  "January",
  "February",
  "March",
  "April",
  "May",
  "June",
  "July",
  "August",
  "September",
  "October",
  "November",
  "December",
]
const DAY_NAMES = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"]

type FieldSpec = { min: number; max: number; names?: string[] }

const SPECS: FieldSpec[] = [
  { min: 0, max: 59 },
  { min: 0, max: 23 },
  { min: 1, max: 31 },
  { min: 1, max: 12, names: MONTHS },
  { min: 0, max: 7, names: DAYS },
]

function parseValue(token: string, spec: FieldSpec): number | null {
  if (/^\d+$/.test(token)) return Number(token)
  const index = spec.names?.indexOf(token.toLowerCase()) ?? -1
  if (index < 0) return null
  return spec.min + index
}

function parseField(field: string, spec: FieldSpec): Set<number> | null {
  const out = new Set<number>()
  for (const part of field.split(",")) {
    if (part === "") return null
    const [rangePart, stepPart] = part.split("/")
    if (stepPart !== undefined && !/^\d+$/.test(stepPart)) return null
    const step = stepPart === undefined ? 1 : Number(stepPart)
    if (step < 1) return null
    let lo: number
    let hi: number
    if (rangePart === "*") {
      lo = spec.min
      hi = spec.max
    } else if (rangePart.includes("-")) {
      const [a, b] = rangePart.split("-")
      const from = parseValue(a, spec)
      const to = parseValue(b, spec)
      if (from === null || to === null || from > to) return null
      lo = from
      hi = to
    } else {
      const value = parseValue(rangePart, spec)
      if (value === null) return null
      lo = value
      // `*/5` is a range; `5/5` in Vixie cron means "from 5 to the end".
      hi = stepPart === undefined ? value : spec.max
    }
    if (lo < spec.min || hi > spec.max) return null
    for (let v = lo; v <= hi; v += step) out.add(v)
  }
  return out
}

/** Reads one schedule. `@reboot` has no fields and returns null. */
export function parseCron(expression: string): CronFields | null {
  const trimmed = expression.trim()
  const expanded = NICKNAMES[trimmed.toLowerCase()] ?? trimmed
  const fields = expanded.split(/\s+/)
  if (fields.length !== 5) return null
  const parsed = fields.map((f, i) => parseField(f, SPECS[i]))
  if (parsed.some((p) => p === null)) return null
  const [minute, hour, dayOfMonth, month, dayOfWeek] = parsed as Set<number>[]
  // Sunday is both 0 and 7.
  if (dayOfWeek.has(7)) {
    dayOfWeek.delete(7)
    dayOfWeek.add(0)
  }
  return {
    minute,
    hour,
    dayOfMonth,
    month,
    dayOfWeek,
    anyDayOfMonth: fields[2] === "*",
    anyDayOfWeek: fields[4] === "*",
  }
}

export function isValidCron(expression: string): boolean {
  return expression.trim().toLowerCase() === "@reboot" || parseCron(expression) !== null
}

function pad(n: number) {
  return String(n).padStart(2, "0")
}

function listOf(values: number[], name: (v: number) => string): string {
  const words = values.map(name)
  if (words.length === 1) return words[0]
  return `${words.slice(0, -1).join(", ")} and ${words[words.length - 1]}`
}

function ordinal(n: number): string {
  const suffix =
    n % 100 >= 11 && n % 100 <= 13
      ? "th"
      : n % 10 === 1
        ? "st"
        : n % 10 === 2
          ? "nd"
          : n % 10 === 3
            ? "rd"
            : "th"
  return `${n}${suffix}`
}

/** A contiguous every-nth run over the whole field, or null. */
function stepOf(values: Set<number>, spec: FieldSpec): number | null {
  const sorted = [...values].sort((a, b) => a - b)
  if (sorted.length < 2 || sorted[0] !== spec.min) return null
  const step = sorted[1] - sorted[0]
  for (let i = 1; i < sorted.length; i++) if (sorted[i] - sorted[i - 1] !== step) return null
  if (sorted[sorted.length - 1] + step <= spec.max) return null
  return step
}

/**
 * The schedule as a sentence: "Every day at 03:00", "Every 5 minutes",
 * "At 07:30 on Monday and Friday", "At 00:00 on the 1st of every month".
 */
export function describeCron(expression: string): string {
  const trimmed = expression.trim().toLowerCase()
  if (trimmed === "@reboot") return "Once, when the server boots"
  const cron = parseCron(expression)
  if (!cron) return "Not a schedule cron understands"

  const full = (values: Set<number>, spec: FieldSpec) => values.size === spec.max - spec.min + 1
  const minutes = [...cron.minute].sort((a, b) => a - b)
  const hours = [...cron.hour].sort((a, b) => a - b)

  let time: string
  const minuteStep = stepOf(cron.minute, SPECS[0])
  const hourStep = stepOf(cron.hour, SPECS[1])
  if (full(cron.minute, SPECS[0]) && full(cron.hour, SPECS[1])) {
    time = "Every minute"
  } else if (full(cron.minute, SPECS[0])) {
    time = `Every minute of ${hours.length === 1 ? `the hour from ${pad(hours[0])}:00` : `the hours ${listOf(hours, (h) => `${pad(h)}:00`)}`}`
  } else if (minuteStep && full(cron.hour, SPECS[1])) {
    time = `Every ${minuteStep} minutes`
  } else if (minuteStep && hours.length > 0) {
    time = `Every ${minuteStep} minutes between ${pad(hours[0])}:00 and ${pad(hours[hours.length - 1])}:59`
  } else if (minutes.length === 1 && hourStep) {
    time =
      hourStep === 1
        ? `Every hour at :${pad(minutes[0])}`
        : `Every ${hourStep} hours at :${pad(minutes[0])}`
  } else if (minutes.length === 1 && full(cron.hour, SPECS[1])) {
    time = `Every hour at :${pad(minutes[0])}`
  } else {
    const times: string[] = []
    for (const h of hours) for (const m of minutes) times.push(`${pad(h)}:${pad(m)}`)
    time =
      times.length > 6
        ? `At ${times.length} times a day`
        : `At ${listOf(
            times.map((_, i) => i),
            (i) => times[i],
          )}`
  }

  const parts: string[] = [time]
  const dom = [...cron.dayOfMonth].sort((a, b) => a - b)
  const dow = [...cron.dayOfWeek].sort((a, b) => a - b)
  const months = [...cron.month].sort((a, b) => a - b)
  const domRestricted = !cron.anyDayOfMonth
  const dowRestricted = !cron.anyDayOfWeek

  if (domRestricted && dowRestricted) {
    parts.push(
      `on the ${listOf(dom, ordinal)} of the month or on ${listOf(dow, (d) => DAY_NAMES[d])}`,
    )
  } else if (domRestricted) {
    parts.push(`on the ${listOf(dom, ordinal)}`)
  } else if (dowRestricted) {
    if (dow.length === 5 && dow.join() === "1,2,3,4,5") parts.push("on weekdays")
    else if (dow.length === 2 && dow.join() === "0,6") parts.push("at weekends")
    else parts.push(`on ${listOf(dow, (d) => DAY_NAMES[d])}`)
  } else if (time.startsWith("At") && !full(cron.month, SPECS[3])) {
    parts.push("every day")
  } else if (time.startsWith("At")) {
    parts[0] = time.replace(/^At /, "Every day at ")
  }

  if (!full(cron.month, SPECS[3])) {
    parts.push(`in ${listOf(months, (m) => MONTH_NAMES[m - 1])}`)
  } else if (domRestricted && !dowRestricted) {
    parts.push("of every month")
  }
  return parts.join(" ")
}

/**
 * The next moment a schedule fires after `from`, in this browser's clock.
 * Walks day by day, then hour, then minute, so a yearly schedule costs a few
 * hundred steps rather than half a million. Null for `@reboot` and for a
 * schedule that never matches (February 30th).
 */
export function nextCronRun(expression: string, from: Date = new Date()): Date | null {
  const cron = parseCron(expression)
  if (!cron) return null
  const hours = [...cron.hour].sort((a, b) => a - b)
  const minutes = [...cron.minute].sort((a, b) => a - b)
  const dayMatches = (d: Date) => onDay(cron, d.getMonth() + 1, d.getDate(), d.getDay())

  const start = new Date(from.getTime())
  start.setSeconds(0, 0)
  start.setMinutes(start.getMinutes() + 1)
  const day = new Date(start.getFullYear(), start.getMonth(), start.getDate())
  // Five years covers every real schedule; anything further is unmatchable.
  const limit = new Date(day.getFullYear() + 5, day.getMonth(), day.getDate())
  for (; day < limit; day.setDate(day.getDate() + 1)) {
    if (!dayMatches(day)) continue
    const sameDay = day.toDateString() === start.toDateString()
    for (const h of hours) {
      for (const m of minutes) {
        const candidate = new Date(day.getFullYear(), day.getMonth(), day.getDate(), h, m)
        if (!sameDay || candidate >= start) return candidate
      }
    }
  }
  return null
}

/**
 * Whether a schedule fires at all on a date. When both day fields are
 * restricted cron fires on either — the 15th, or any Friday — not only on a
 * Friday the 15th.
 */
function onDay(cron: CronFields, month: number, date: number, weekday: number): boolean {
  if (!cron.month.has(month)) return false
  const dom = cron.dayOfMonth.has(date)
  const dow = cron.dayOfWeek.has(weekday)
  if (cron.anyDayOfMonth && cron.anyDayOfWeek) return true
  if (cron.anyDayOfMonth) return dow
  if (cron.anyDayOfWeek) return dom
  return dom || dow
}

const MINUTE = 60_000
const HOUR = 60 * MINUTE
const DAY = 24 * HOUR

const zoneFormats = new Map<string, Intl.DateTimeFormat>()

/** Reads an instant as a zone's wall clock; undefined for a zone this browser does not know. */
function zoneFormat(timeZone: string): Intl.DateTimeFormat | undefined {
  let format = zoneFormats.get(timeZone)
  if (format) return format
  try {
    format = new Intl.DateTimeFormat("en-US", {
      timeZone,
      hourCycle: "h23",
      year: "numeric",
      month: "numeric",
      day: "numeric",
      hour: "numeric",
      minute: "numeric",
      second: "numeric",
    })
  } catch {
    // A zone typed by hand, or one the server knows and this browser's
    // tables do not: there is nothing honest to preview.
    return undefined
  }
  zoneFormats.set(timeZone, format)
  return format
}

/** The zone's wall clock at an instant, written as the UTC instant with the same digits. */
function wallAt(format: Intl.DateTimeFormat, instant: number): number {
  const at: Record<string, number> = {}
  for (const part of format.formatToParts(instant)) at[part.type] = Number(part.value)
  return Date.UTC(at.year, at.month - 1, at.day, at.hour, at.minute, at.second)
}

function offsetAt(format: Intl.DateTimeFormat, instant: number): number {
  return wallAt(format, instant) - Math.floor(instant / 1000) * 1000
}

/**
 * The next `count` moments a schedule fires after `from`, reading its fields
 * as the wall clock of `timeZone` — the zone a deployment's schedule names,
 * which is rarely the browser's. Empty for `@reboot`, for a schedule that
 * never matches, and for a zone this browser cannot read.
 *
 * Daylight saving is kept the way the server's own walk keeps it: a time the
 * spring change skips never fires that day, and a time the autumn change
 * repeats fires twice, once on each side of it.
 *
 * It walks the zone's calendar a day at a time, as `nextCronRun` does. On a
 * day with no change of offset near it every wall time is one instant, found
 * by arithmetic; only a day with a change is checked time by time.
 */
export function nextCronRunsIn(
  expression: string,
  timeZone: string,
  count: number,
  from: Date = new Date(),
): Date[] {
  const cron = parseCron(expression)
  const format = zoneFormat(timeZone)
  if (!cron || !format) return []
  const hours = [...cron.hour].sort((a, b) => a - b)
  const minutes = [...cron.minute].sort((a, b) => a - b)

  const start = Math.floor(from.getTime() / MINUTE) * MINUTE + MINUTE
  const first = new Date(wallAt(format, start))
  let day = Date.UTC(first.getUTCFullYear(), first.getUTCMonth(), first.getUTCDate())
  // Five years covers every real schedule; anything further is unmatchable.
  const limit = Date.UTC(first.getUTCFullYear() + 5, first.getUTCMonth(), first.getUTCDate())
  const out: number[] = []
  for (; day < limit && out.length < count; day += DAY) {
    const date = new Date(day)
    if (!onDay(cron, date.getUTCMonth() + 1, date.getUTCDate(), date.getUTCDay())) continue
    // Offsets run from -12h to +14h, so every instant this day's wall times
    // name lies between these two readings.
    const before = offsetAt(format, day - 14 * HOUR)
    const after = offsetAt(format, day + DAY + 12 * HOUR)
    const offsets = before === after ? [before] : [before, after]
    const found: number[] = []
    for (const h of hours) {
      for (const m of minutes) {
        const wall = day + h * HOUR + m * MINUTE
        for (const offset of offsets) {
          const instant = wall - offset
          if (instant < start) continue
          // Only on a day with a change can an offset be the wrong one: the
          // spring gap matches neither, the autumn overlap matches both.
          if (offsets.length === 1 || wallAt(format, instant) === wall) found.push(instant)
        }
      }
    }
    // The repeated hour interleaves: 01:45 before the change comes ahead of
    // 01:15 after it.
    found.sort((a, b) => a - b)
    out.push(...found.slice(0, count - out.length))
  }
  return out.map((instant) => new Date(instant))
}

/** The presets the job dialog offers, each a real expression. */
export const CRON_PRESETS = [
  { key: "minute", label: "Every minute", expression: "* * * * *" },
  { key: "5min", label: "Every 5 minutes", expression: "*/5 * * * *" },
  { key: "15min", label: "Every 15 minutes", expression: "*/15 * * * *" },
  { key: "hourly", label: "Every hour", expression: "0 * * * *" },
  { key: "daily", label: "Every day at a time", expression: "0 3 * * *" },
  { key: "weekly", label: "Every week on a day", expression: "0 3 * * 1" },
  { key: "monthly", label: "Every month on a date", expression: "0 3 1 * *" },
  { key: "reboot", label: "When the server boots", expression: "@reboot" },
  { key: "custom", label: "Custom expression", expression: "" },
] as const

export type CronPreset = (typeof CRON_PRESETS)[number]["key"]

/** Which preset an existing expression belongs to, for editing a job. */
export function presetFor(expression: string): CronPreset {
  const trimmed = expression.trim()
  if (trimmed.toLowerCase() === "@reboot") return "reboot"
  const fields = trimmed.split(/\s+/)
  if (fields.length !== 5) return "custom"
  const [minute, hour, dom, month, dow] = fields
  if (month !== "*") return "custom"
  const plainMinute = /^\d{1,2}$/.test(minute)
  const plainHour = /^\d{1,2}$/.test(hour)
  if (minute === "*" && hour === "*" && dom === "*" && dow === "*") return "minute"
  if (minute === "*/5" && hour === "*" && dom === "*" && dow === "*") return "5min"
  if (minute === "*/15" && hour === "*" && dom === "*" && dow === "*") return "15min"
  if (plainMinute && hour === "*" && dom === "*" && dow === "*") return "hourly"
  if (plainMinute && plainHour && dom === "*" && dow === "*") return "daily"
  if (plainMinute && plainHour && dom === "*" && /^[0-7]$/.test(dow)) return "weekly"
  if (plainMinute && plainHour && /^\d{1,2}$/.test(dom) && dow === "*") return "monthly"
  return "custom"
}
