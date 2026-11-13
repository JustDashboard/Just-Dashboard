import { describeCron, nextCronRun, nextCronRunsIn } from "@/lib/cron"
import type { CronJob, Crontab, SystemdTimer } from "@/lib/types"

/**
 * What runs on the clock, as one list across the three places a host keeps
 * it — an account's crontab, systemd's timers and the packages' cron files —
 * so the Scheduled page can draw the next day of all three on one axis.
 *
 * A cron line's runs are its expression's, computed in this browser's clock
 * as `nextCronRun` computes them. A timer's are only what systemd reports,
 * which is the next elapse: its calendar can be read, but most of the
 * packages' timers add a randomised delay to it, so every later run drawn
 * from the calendar would be a time it does not fire at.
 */

export type ScheduleKind = "cron" | "timer" | "system"

export type ScheduleLane = {
  key: string
  kind: ScheduleKind
  /** What a reader calls it: a cron line's note or its program, a timer's unit. */
  name: string
  /** The command a cron line runs, or the unit a timer starts. */
  runs: string
  /** The schedule in words, as a sentence. */
  when: string
  /** The command a product mark is read from: a cron line's program, a timer's unit. */
  product: string
  /** Run times inside the window, soonest first, at most `LANE_CAP`. */
  times: number[]
  /** More runs fall in the window than `times` holds: drawn as a band. */
  dense: boolean
  /** The next run, wherever it falls, or undefined when there is none. */
  next?: number
  /**
   * Why a line whose schedule matches never does its work here: a package's
   * cron line that steps aside on a host running systemd, because a timer
   * does the same work (`deferredToSystemd`).
   */
  skipped?: "systemd"
  /** The timer that does a skipped line's work, when one is listed. */
  instead?: string
  /** What a timer's unit says it is for. */
  detail?: string
  /** The underlying job's line or the timer's unit, for opening its sheet. */
  job?: CronJob
  source?: string
  timer?: SystemdTimer
}

/** Past this many runs in a day a week strip draws a band rather than marks. */
export const RUN_CAP = 96

/**
 * How many runs a lane keeps for the day: one a minute. The band buckets
 * them by hour, and a lane cut at `RUN_CAP` stopped two thirds of the way
 * across it, so a collector every ten minutes vanished from the evening.
 */
export const LANE_CAP = 24 * 60

const SEPARATORS = /\s*(?:&&|\|\||;)\s*/

/**
 * A command line's commands, with the shell's grouping taken off: Debian's
 * `/etc/crontab` writes `test -x /usr/sbin/anacron || { cd / && run-parts
 * --report /etc/cron.daily; }`, whose last part is a lone brace — which the
 * band once named a lane after.
 */
function commandParts(command: string): string[] {
  return command
    .split(SEPARATORS)
    .map((part) =>
      part
        .replace(/^[{(]\s*/, "")
        .replace(/\s*[})]$/, "")
        .trim(),
    )
    .filter(Boolean)
}

/**
 * The program a cron line is there to run, which is rarely its first word:
 * `cd / && run-parts --report /etc/cron.hourly` runs run-parts,
 * `test -x /usr/sbin/anacron || run-parts …` too, and
 * `SERVICE_MODE=1 /sbin/e2scrub_all -A -r` runs e2scrub_all. The last command
 * of a chain is the work and those before it its guards; within it, the
 * first of a pipeline, past any assignments and redirections.
 */
export function cronProgram(command: string): { name: string; segment: string } {
  const parts = commandParts(command)
  const last = (parts.at(-1) ?? command).split("|")[0].trim()
  // Assignments are words only in front of the command — `--filter
  // until=168h` is an argument — and a redirection takes its target with it
  // when the two are apart: `curl … > /dev/null` is not a request for
  // /dev/null.
  const tokens = last.split(/\s+/).filter(Boolean)
  const words: string[] = []
  for (let i = 0; i < tokens.length; i++) {
    const word = tokens[i]
    if (words.length === 0 && /^[A-Za-z_][A-Za-z0-9_]*=/.test(word)) continue
    if (/^(\d*|&)(>>?|<)/.test(word)) {
      if (/^(\d*|&)(>>?|<)$/.test(word)) i++
      continue
    }
    words.push(word)
  }
  const segment = words.join(" ")
  const first = words[0] ?? command.trim().split(/\s+/)[0] ?? ""
  const name = first.split("/").pop() || first
  return { name, segment: segment || command }
}

/** A cron expression's runs from `from` up to `until`, capped at `cap`. */
export function cronRunsBetween(
  expression: string,
  from: number,
  until: number,
  cap = RUN_CAP,
  timeZone?: string,
) {
  const times: number[] = []
  if (timeZone) {
    const times = nextCronRunsIn(expression, timeZone, cap + 1, new Date(from), new Date(until))
      .map((date) => date.getTime())
      .filter((instant) => instant <= until)
    return { times: times.slice(0, cap), dense: times.length > cap }
  }
  let at = new Date(from)
  while (times.length <= cap) {
    const next = nextCronRun(expression, at, timeZone)
    if (!next || next.getTime() > until) break
    times.push(next.getTime())
    at = next
  }
  return { times: times.slice(0, cap), dense: times.length > cap }
}

function cronLane(
  key: string,
  kind: ScheduleKind,
  job: CronJob,
  from: number,
  until: number,
  source?: string,
  systemd?: boolean,
  timers?: SystemdTimer[],
): ScheduleLane {
  const program = cronProgram(job.command)
  const skipped = Boolean(systemd && deferredToSystemd(job.command))
  const quiet = job.disabled || skipped
  const { times, dense } = quiet
    ? { times: [], dense: false }
    : cronRunsBetween(job.schedule, from, until, LANE_CAP, job.timeZone)
  const next = quiet
    ? undefined
    : (times[0] ?? nextCronRun(job.schedule, new Date(from), job.timeZone)?.getTime())
  return {
    key,
    kind,
    name: cronLineName(job),
    skipped: skipped ? "systemd" : undefined,
    instead: skipped ? timerFor(job.command, timers) : undefined,
    runs: job.command,
    when: describeCron(job.schedule),
    product: program.segment,
    times,
    dense,
    next,
    job,
    source,
  }
}

/**
 * Every schedule on the host as lanes, each with its runs between `from` and
 * `until`, soonest first; a lane with nothing to run sorts last.
 */
export function scheduleLanes({
  crontab,
  timers,
  system,
  from,
  until,
  zone,
  systemd = false,
}: {
  crontab?: Crontab
  timers?: SystemdTimer[]
  system?: Crontab[]
  from: number
  until: number
  /** The host's zone, which a timer's calendar is read in. */
  zone?: string
  /** Whether the host runs systemd, so a package line that steps aside for it is skipped. */
  systemd?: boolean
}): ScheduleLane[] {
  const lanes: ScheduleLane[] = []
  for (const job of crontab?.jobs ?? []) {
    lanes.push(cronLane(`cron:${job.line}`, "cron", job, from, until))
  }
  for (const timer of timers ?? []) {
    const next = timer.next ? new Date(timer.next).getTime() : undefined
    const live = timer.activeState === "active" && next !== undefined && next >= from
    const runs = live ? timerRuns(timer, from, until, zone) : { times: [], dense: false }
    lanes.push({
      key: `timer:${timer.unit}`,
      kind: "timer",
      name: timer.unit.replace(/\.timer$/, ""),
      detail: timer.description,
      runs: timer.activates,
      when: describeTimer(timer) ?? (timer.activates ? `Starts ${timer.activates}` : "No schedule"),
      product: timer.unit,
      times: runs.times,
      dense: runs.dense,
      next: live ? next : undefined,
      timer,
    })
  }
  const listed = timers ?? []
  for (const file of system ?? []) {
    for (const job of file.jobs) {
      if (isExampleLine(job)) continue
      lanes.push(
        cronLane(
          `system:${file.source}:${job.line}`,
          "system",
          job,
          from,
          until,
          file.source,
          systemd,
          listed,
        ),
      )
    }
  }
  return lanes.sort((a, b) => (a.next ?? Infinity) - (b.next ?? Infinity))
}

/**
 * The runs of a timer inside a window. systemd reports only the next, so the
 * rest are read off its calendar where that is one this page can walk — and
 * only for a timer with no random delay, since one that adds up to twelve
 * hours to every elapse fires at none of the calendar's times. An interval
 * timer ("1d after it last started") repeats from its next. Anything else is
 * the one run systemd said.
 */
export function timerRuns(
  timer: SystemdTimer,
  from: number,
  until: number,
  zone?: string,
  cap = LANE_CAP,
): { times: number[]; dense: boolean } {
  const next = timer.next ? new Date(timer.next).getTime() : undefined
  if (next === undefined || next < from) return { times: [], dense: false }
  const only = { times: next <= until ? [next] : [], dense: false }
  if (timer.randomDelay) return only
  const calendar = timer.calendar ?? []
  const monotonic = timer.monotonic ?? []
  if (calendar.length > 0 && monotonic.length === 0) {
    const crons = calendar.map(calendarToCron)
    if (crons.some((cron) => !cron)) return only
    const all = new Set<number>()
    let dense = false
    for (const cron of crons) {
      const runs = cronRunsBetween(cron!, from - 1, until, cap, zone)
      dense ||= runs.dense
      for (const at of runs.times) all.add(at)
    }
    const times = [...all].sort((a, b) => a - b)
    // The walk and systemd must agree on the next run, give or take the
    // minute of accuracy systemd allows itself; if they do not, the
    // calendar is not one this page reads right, and the one run is said.
    if (times.length === 0 || Math.abs(times[0] - next) > 90_000) return only
    return { times: times.slice(0, cap), dense: dense || times.length > cap }
  }
  const every = monotonic
    .map((spec) => spec.match(/^OnUnit(?:Active|Inactive)Sec=(.+)$/)?.[1])
    .map((span) => (span ? spanMs(span) : undefined))
    .find((ms) => ms !== undefined && ms >= 60_000)
  if (calendar.length === 0 && every) {
    const times: number[] = []
    for (let at = next; at <= until && times.length <= cap; at += every) times.push(at)
    return { times: times.slice(0, cap), dense: times.length > cap }
  }
  return only
}

/**
 * "4m 12s", "2h 05m", "3d 4h": a countdown that is read at a glance, to the
 * second while it is under an hour, since that is when it is watched.
 */
export function countdown(ms: number): string {
  const s = Math.max(0, Math.round(ms / 1000))
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ${String(s % 60).padStart(2, "0")}s`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h ${String(m % 60).padStart(2, "0")}m`
  return `${Math.floor(h / 24)}d ${h % 24}h`
}

const DOW: Record<string, string> = {
  mon: "Monday",
  tue: "Tuesday",
  wed: "Wednesday",
  thu: "Thursday",
  fri: "Friday",
  sat: "Saturday",
  sun: "Sunday",
}

function days(spec: string): string | undefined {
  const lower = spec.toLowerCase()
  if (lower === "mon..fri" || lower === "mon-fri") return "on weekdays"
  if (lower === "sat,sun" || lower === "sat..sun") return "on weekends"
  const names = lower.split(",").map((d) => DOW[d.slice(0, 3)])
  if (names.some((n) => !n)) return undefined
  return `on ${names.map((n) => `${n}s`).join(" and ")}`
}

function list(items: string[]) {
  return items.length < 2 ? items.join("") : `${items.slice(0, -1).join(", ")} and ${items.at(-1)}`
}

/**
 * A systemd calendar expression in words, for the forms the packages' timers
 * actually use — a time or two a day, an hour's step, a weekday — and the
 * expression itself for anything else, since a wrong sentence is worse than
 * none.
 */
export function describeCalendar(spec: string): string {
  const match = normalCalendar(spec)
  if (!match) return spec
  const [, dow, dom, hour, minute] = match
  const on = dow ? days(dow) : undefined
  if (dow && !on) return spec
  if (hour === "*") {
    if (dom !== "*" || dow) return spec
    const step = minute.match(/^\d{1,2}\/(\d+)$/)
    if (step) return `every ${step[1]} minutes`
    if (/^\d{2}$/.test(minute)) return `every hour at :${minute}`
    return spec
  }
  if (!/^\d{2}$/.test(minute)) return spec
  const times = hour.split(",").map((h) => `${h.padStart(2, "0")}:${minute}`)
  const at = `at ${list(times)}`
  if (dom !== "*") return dow ? spec : `on day ${Number(dom)} of every month ${at}`
  if (on) return `${on} ${at}`
  return times.length === 2 ? `twice a day, ${at}` : `every day ${at}`
}

/** systemd's shorthands, as `systemctl show` writes them out. */
const SHORTHANDS: Record<string, string> = {
  minutely: "*-*-* *:*:00",
  hourly: "*-*-* *:00:00",
  daily: "*-*-* 00:00:00",
  weekly: "Mon *-*-* 00:00:00",
  monthly: "*-*-01 00:00:00",
}

const CALENDAR =
  /^(?:([A-Za-z.,-]+) )?\*-\*-(\*|\d{1,2}) (\*|[\d,]+):(\d{2}|\*|\d{1,2}\/\d+)(?::\d{2})?$/

function normalCalendar(spec: string) {
  let trimmed = spec.trim()
  trimmed = SHORTHANDS[trimmed.toLowerCase()] ?? trimmed
  // A unit file may leave the date out — `*:00/10`, `Sun 03:10` — which
  // `systemctl show` writes back in full; both are read the same.
  if (/^[\d*,/:]+$/.test(trimmed)) trimmed = `*-*-* ${trimmed}`
  else if (/^[A-Za-z.,-]+ [\d*,/:]+$/.test(trimmed)) trimmed = trimmed.replace(" ", " *-*-* ")
  return trimmed.match(CALENDAR)
}

const DOW_NUMBER: Record<string, number> = {
  sun: 0,
  mon: 1,
  tue: 2,
  wed: 3,
  thu: 4,
  fri: 5,
  sat: 6,
}

/**
 * A systemd calendar as the cron expression that fires at the same times,
 * for the forms `describeCalendar` says in words — so a timer's week can be
 * drawn and its runs walked the way a cron line's are. Undefined for any
 * other form.
 */
export function calendarToCron(spec: string): string | undefined {
  const match = normalCalendar(spec)
  if (!match) return undefined
  const [, dow, dom, hour, minute] = match
  let days = "*"
  if (dow) {
    const out: string[] = []
    for (const part of dow.toLowerCase().split(",")) {
      const [a, b] = part.split(/\.\.|-/)
      const start = DOW_NUMBER[a?.slice(0, 3)]
      if (start === undefined) return undefined
      if (b === undefined) out.push(String(start))
      else {
        const end = DOW_NUMBER[b.slice(0, 3)]
        if (end === undefined) return undefined
        // "Sat..Sun" wraps the week, which a cron range cannot.
        if (end < start) out.push(`${start}-6`, `0-${end}`)
        else out.push(`${start}-${end}`)
      }
    }
    days = out.join(",")
  }
  const step = minute.match(/^(\d{1,2})\/(\d+)$/)
  const minutes = step
    ? `${Number(step[1])}-59/${step[2]}`
    : minute === "*"
      ? "*"
      : String(Number(minute))
  const hours = hour === "*" ? "*" : hour.split(",").map(Number).join(",")
  const date = dom === "*" ? "*" : String(Number(dom))
  return `${minutes} ${hours} ${date} * ${days}`
}

/** "12h", "1h 40min", "1w", "30s" as milliseconds, or undefined for anything else. */
export function spanMs(span: string): number | undefined {
  const units: Record<string, number> = {
    us: 0.001,
    usec: 0.001,
    ms: 1,
    msec: 1,
    s: 1000,
    sec: 1000,
    second: 1000,
    seconds: 1000,
    m: 60_000,
    min: 60_000,
    minute: 60_000,
    minutes: 60_000,
    h: 3_600_000,
    hr: 3_600_000,
    hour: 3_600_000,
    hours: 3_600_000,
    d: 86_400_000,
    day: 86_400_000,
    days: 86_400_000,
    w: 604_800_000,
    week: 604_800_000,
    weeks: 604_800_000,
    M: 2_629_800_000,
    month: 2_629_800_000,
    months: 2_629_800_000,
    y: 31_557_600_000,
    year: 31_557_600_000,
    years: 31_557_600_000,
  }
  const parts = [...span.trim().matchAll(/(\d+(?:\.\d+)?)\s*([A-Za-z]*)/g)]
  if (
    parts.length === 0 ||
    parts
      .map((p) => p[0])
      .join("")
      .replace(/\s/g, "") !== span.replace(/\s/g, "")
  ) {
    return undefined
  }
  let total = 0
  for (const [, value, unit] of parts) {
    const size = unit === "" ? 1000 : (units[unit] ?? units[unit.toLowerCase()])
    if (size === undefined) return undefined
    total += Number(value) * size
  }
  return total
}

const SPAN_WORDS: [number, string][] = [
  [604_800_000, "week"],
  [86_400_000, "day"],
  [3_600_000, "hour"],
  [60_000, "minute"],
  [1000, "second"],
]

/** A systemd span in words: "12h" is "12 hours", "1h 40min" "1 hour 40 minutes", "1d" "day". */
export function spanWords(span: string, bare = false): string {
  let ms = spanMs(span)
  if (ms === undefined) return span
  const words: string[] = []
  for (const [size, word] of SPAN_WORDS) {
    const n = Math.floor(ms / size)
    if (n > 0) {
      words.push(`${n} ${word}${n === 1 ? "" : "s"}`)
      ms -= n * size
    }
  }
  const out = words.slice(0, 2).join(" ")
  // "every day", not "every 1 day".
  return bare ? out.replace(/^1 /, "") : out
}

/**
 * When a timer fires, in one sentence: its calendars and intervals in words,
 * and the random delay its package adds — "Twice a day, at 06:00 and 18:00,
 * up to 12 hours later". Undefined while the list has not said.
 */
export function describeTimer(
  timer: Pick<SystemdTimer, "calendar" | "monotonic" | "randomDelay">,
): string | undefined {
  const calendar = timer.calendar ?? []
  const monotonic = timer.monotonic ?? []
  if (calendar.length === 0 && monotonic.length === 0) return undefined
  const said: string[] = calendar.map(describeCalendar)
  const once: string[] = []
  const repeat: string[] = []
  for (const spec of monotonic) {
    const [name, value = ""] = spec.split("=")
    if (name === "OnUnitActiveSec") repeat.push(`every ${spanWords(value, true)}`)
    else if (name === "OnUnitInactiveSec")
      repeat.push(`${spanWords(value)} after each run finishes`)
    else if (name === "OnBootSec") once.push(`${spanWords(value)} after boot`)
    else if (name === "OnStartupSec") once.push(`${spanWords(value)} after systemd starts`)
    else if (name === "OnActiveSec") once.push(`${spanWords(value)} after the timer starts`)
    else once.push(spec)
  }
  if (once.length > 0 && repeat.length > 0) said.push(`${list(once)}, then ${list(repeat)}`)
  else said.push(...once, ...repeat)
  let sentence = list(said)
  if (timer.randomDelay) sentence += `, up to ${spanWords(timer.randomDelay)} later`
  return sentence.charAt(0).toUpperCase() + sentence.slice(1)
}

const TRIGGERS: Record<string, string> = {
  OnUnitActiveSec: "after it last started",
  OnUnitInactiveSec: "after it last finished",
  OnBootSec: "after boot",
  OnStartupSec: "after systemd started",
  OnActiveSec: "after the timer started",
}

/**
 * What a timer fires on, from `systemctl show`: its `TimersCalendar` and
 * `TimersMonotonic`, each `{ OnCalendar=… ; next_elapse=… }`. A unit with
 * several triggers lists each; `show` keeps the last line of each property,
 * so a timer with two calendars reads as its second, which is what the
 * dashboard's unit read gives it.
 */
export function timerTriggers(props: Record<string, string>): { spec: string; words: string }[] {
  const out: { spec: string; words: string }[] = []
  for (const key of ["TimersCalendar", "TimersMonotonic"]) {
    const value = props[key]
    if (!value) continue
    for (const block of value.matchAll(/\{([^}]*)\}/g)) {
      const first = block[1].split(";")[0].trim()
      const [name, ...rest] = first.split("=")
      const spec = rest.join("=").trim()
      if (!spec) continue
      // systemd 255 spells the intervals `OnBootUSec`; the unit file, and
      // the reader, `OnBootSec`.
      const key = name.replace(/USec$/, "Sec")
      if (key === "OnCalendar") out.push({ spec, words: describeCalendar(spec) })
      else out.push({ spec: `${key}=${spec}`, words: `${spanWords(spec)} ${TRIGGERS[key] ?? key}` })
    }
  }
  return out
}

/** `RandomizedDelayUSec` as a reader says it, or nothing for none. */
export function randomDelay(props: Record<string, string>): string | undefined {
  const value = props.RandomizedDelayUSec?.trim()
  if (!value || value === "0" || value === "infinity") return undefined
  return value
}

/** The last word of an `ExecStart` property's argv, as it was written in the unit. */
export function execCommand(props: Record<string, string>): string | undefined {
  return props.ExecStart?.match(/argv\[\]=([^;]*)/)?.[1].trim() || undefined
}

/**
 * A package's cron line that does nothing on a host running systemd. Debian
 * ships its cron files beside timers for the same work, and guards each line
 * with a test of `/run/systemd/system` — `test -e /run/systemd/system || …`,
 * certbot's `test -x … -a \\! -d /run/systemd/system && …` — so that only one
 * of the two runs. On such a host the line's schedule still matches; its
 * work is the timer's.
 */
export function deferredToSystemd(command: string): boolean {
  const parts = commandParts(command)
  return parts.slice(0, -1).some((part) => part.includes("/run/systemd/system"))
}

/** The timer that does a skipped cron line's work, by the program the line runs. */
export function timerFor(command: string, timers: SystemdTimer[] = []): string | undefined {
  const name = cronProgram(command).name
  for (const candidate of [name, name.replace(/[-_]cron$/, "")]) {
    const unit = `${candidate}.timer`
    if (timers.some((timer) => timer.unit === unit)) return unit
  }
  return undefined
}

const RUN_PARTS: Record<string, string> = {
  hourly: "Hourly scripts",
  daily: "Daily scripts",
  weekly: "Weekly scripts",
  monthly: "Monthly scripts",
}

/**
 * What a reader calls a cron line. `run-parts /etc/cron.daily` is the daily
 * scripts; a note above the line is its name when it reads as one — a
 * package's notes are often the tail of a paragraph ("certbot.timer.") or
 * the column ruler of the file's own worked example, and neither is a name;
 * otherwise the program it runs.
 */
export function cronLineName(job: Pick<CronJob, "command" | "comment">): string {
  const program = cronProgram(job.command)
  const dir = program.segment.match(/\/etc\/cron\.(hourly|daily|weekly|monthly)\b/)?.[1]
  if (program.name === "run-parts" && dir) return RUN_PARTS[dir]
  const note = job.comment?.trim()
  if (note && /\s/.test(note) && /[A-Za-z]{3}/.test(note)) return note
  // The program and the subcommands that say what it is asked to do —
  // `docker system prune`, `pm2 resurrect` — up to the first flag, path or
  // argument.
  const words = [program.name]
  for (const word of program.segment.split(/\s+/).slice(1, 3)) {
    if (!/^[a-z][a-z0-9_-]*$/.test(word)) break
    words.push(word)
  }
  return words.join(" ")
}

/**
 * The worked example `/etc/crontab` ships as a comment — `# * * * * *
 * user-name command to be executed` under a ruler of pipes — which reads as a
 * commented-out job and is none.
 */
export function isExampleLine(job: Pick<CronJob, "disabled" | "user" | "command">): boolean {
  return job.disabled && (job.user === "user-name" || /^command to be executed$/.test(job.command))
}

const CLOCKS = new Map<string, Intl.DateTimeFormat | null>()

function clockFormat(zone: string | undefined): Intl.DateTimeFormat {
  const key = zone ?? ""
  if (!CLOCKS.has(key)) {
    try {
      CLOCKS.set(
        key,
        new Intl.DateTimeFormat("en-GB", {
          timeZone: zone,
          hourCycle: "h23",
          year: "numeric",
          month: "short",
          day: "numeric",
          weekday: "short",
          hour: "2-digit",
          minute: "2-digit",
        }),
      )
    } catch {
      CLOCKS.set(key, null)
    }
  }
  return CLOCKS.get(key) ?? clockFormat(undefined)!
}

/** An instant's wall clock in a zone: its date as one number, its weekday, hour and minute. */
export function wallClock(at: number, zone?: string) {
  const parts: Record<string, string> = {}
  for (const part of clockFormat(zone).formatToParts(at)) parts[part.type] = part.value
  return {
    day: `${parts.year}-${parts.month}-${parts.day.padStart(2, "0")}`,
    weekday: parts.weekday,
    date: `${Number(parts.day)} ${parts.month}`,
    hour: Number(parts.hour),
    minute: Number(parts.minute),
    time: `${parts.hour}:${parts.minute}`,
  }
}

/**
 * When a run falls, as the page writes it: "22:40" today, "Tue 00:59" in the
 * next six days, "14 Nov 03:30" past that — on the host's clock, the one its
 * schedules are written in.
 */
export function clockLabel(at: number, now: number, zone?: string): { day?: string; time: string } {
  const then = wallClock(at, zone)
  if (then.day === wallClock(now, zone).day) return { time: then.time }
  if (at - now < 6 * 86_400_000) return { day: then.weekday, time: then.time }
  return { day: then.date, time: then.time }
}

/** "Sun 15 Nov, 03:30", the whole of a run's time, on the host's clock. */
export function longClock(at: number, zone?: string): string {
  const then = wallClock(at, zone)
  return `${then.weekday} ${then.date}, ${then.time}`
}

/**
 * The 24 hours from the start of this one, on the host's clock: each hour's
 * start, and what its label says — the hour, or the day's name where the
 * date turns.
 */
export function dayHours(now: number, zone?: string): { at: number; hour: number; day: string }[] {
  const clock = wallClock(now, zone)
  const start = Math.floor(now / 60_000) * 60_000 - clock.minute * 60_000
  return Array.from({ length: 24 }, (_, i) => {
    const at = start + i * 3_600_000
    const wall = wallClock(at, zone)
    return { at, hour: wall.hour, day: wall.weekday }
  })
}
