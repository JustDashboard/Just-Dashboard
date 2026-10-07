import { describeCron, nextCronRun } from "@/lib/cron"
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
  /** The schedule in words, or for a timer what it starts. */
  when: string
  /** The command a product mark is read from: a cron line's program, a timer's unit. */
  product: string
  /** Run times inside the window, soonest first, at most `RUN_CAP`. */
  times: number[]
  /** More runs fall in the window than `times` holds: drawn as a band. */
  dense: boolean
  /** The next run, wherever it falls, or undefined when there is none. */
  next?: number
  /** The underlying job's line or the timer's unit, for opening its sheet. */
  job?: CronJob
  source?: string
  timer?: SystemdTimer
}

/** Past this many runs in the window a lane is a band rather than marks. */
export const RUN_CAP = 96

const SEPARATORS = /\s*(?:&&|\|\||;)\s*/

/**
 * The program a cron line is there to run, which is rarely its first word:
 * `cd / && run-parts --report /etc/cron.hourly` runs run-parts,
 * `test -x /usr/sbin/anacron || run-parts …` too, and
 * `SERVICE_MODE=1 /sbin/e2scrub_all -A -r` runs e2scrub_all. The last command
 * of a chain is the work and those before it its guards; within it, the
 * first of a pipeline, past any assignments and redirections.
 */
export function cronProgram(command: string): { name: string; segment: string } {
  const parts = command.split(SEPARATORS).filter(Boolean)
  const last = (parts.at(-1) ?? command).split("|")[0].trim()
  const words = last
    .split(/\s+/)
    .filter((word) => word && !/^[A-Za-z_][A-Za-z0-9_]*=/.test(word) && !/^\d*[<>]/.test(word))
  const segment = words.join(" ")
  const first = words[0] ?? command.trim().split(/\s+/)[0] ?? ""
  const name = first.split("/").pop() || first
  return { name, segment: segment || command }
}

/** A cron expression's runs from `from` up to `until`, capped at `cap`. */
export function cronRunsBetween(expression: string, from: number, until: number, cap = RUN_CAP) {
  const times: number[] = []
  let at = new Date(from)
  while (times.length <= cap) {
    const next = nextCronRun(expression, at)
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
): ScheduleLane {
  const program = cronProgram(job.command)
  const { times, dense } = job.disabled
    ? { times: [], dense: false }
    : cronRunsBetween(job.schedule, from, until)
  const next = job.disabled
    ? undefined
    : (times[0] ?? nextCronRun(job.schedule, new Date(from))?.getTime())
  return {
    key,
    kind,
    name: job.comment || program.name,
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
}: {
  crontab?: Crontab
  timers?: SystemdTimer[]
  system?: Crontab[]
  from: number
  until: number
}): ScheduleLane[] {
  const lanes: ScheduleLane[] = []
  for (const job of crontab?.jobs ?? []) {
    lanes.push(cronLane(`cron:${job.line}`, "cron", job, from, until))
  }
  for (const timer of timers ?? []) {
    const next = timer.next ? new Date(timer.next).getTime() : undefined
    const live = next !== undefined && next >= from
    lanes.push({
      key: `timer:${timer.unit}`,
      kind: "timer",
      name: timer.unit.replace(/\.timer$/, ""),
      runs: timer.activates,
      when: timer.activates ? `starts ${timer.activates}` : "starts nothing",
      product: timer.unit,
      times: live && next <= until ? [next] : [],
      dense: false,
      next: live ? next : undefined,
      timer,
    })
  }
  for (const file of system ?? []) {
    for (const job of file.jobs) {
      lanes.push(
        cronLane(`system:${file.source}:${job.line}`, "system", job, from, until, file.source),
      )
    }
  }
  return lanes.sort((a, b) => (a.next ?? Infinity) - (b.next ?? Infinity))
}

/** The hours an axis from `from` to `until` marks, every `step` hours on the hour. */
export function axisHours(from: number, until: number, step: number): number[] {
  const first = new Date(from)
  first.setMinutes(0, 0, 0)
  first.setHours(first.getHours() + 1)
  while (first.getHours() % step !== 0) first.setHours(first.getHours() + 1)
  const out: number[] = []
  for (let at = first.getTime(); at < until;) {
    out.push(at)
    const next = new Date(at)
    next.setHours(next.getHours() + step)
    at = next.getTime()
  }
  return out
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
  const match = spec
    .trim()
    .match(
      /^(?:([A-Za-z.,-]+) )?\*-\*-(\*|\d{1,2}) (\*|[\d,]+):(\d{2}|\*|\d{1,2}\/\d+)(?::\d{2})?$/,
    )
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
      if (name === "OnCalendar") out.push({ spec, words: describeCalendar(spec) })
      else out.push({ spec: `${name}=${spec}`, words: `${spec} ${TRIGGERS[name] ?? name}` })
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
