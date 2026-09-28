import type { LogLine } from "@/lib/types"

/**
 * A unit's runs, read out of what systemd said about it.
 *
 * The journal of a unit is two voices: what the program printed, and what
 * the service manager recorded around it — "Starting…", "Main process
 * exited, code=exited, status=1/FAILURE", "Failed with result 'exit-code'",
 * "Scheduled restart job", "Consumed 6.5s CPU time". The systemd lens names
 * the second voice's lines (`backend/internal/logsx/lens_systemd.go`), and
 * each carries the invocation it belongs to, so a week of them groups into
 * one row per time the unit ran: when it started, how long it lasted, how it
 * ended. That is the question a failed unit is opened with, and a scroll of
 * the raw journal answers it only to someone who already knows the words.
 */

/** The manager's lines a run is made of, as the systemd lens names them. */
export const RUN_EVENTS = [
  "starting",
  "started",
  "exited",
  "killed",
  "failed",
  "stopped",
  "deactivated",
  "restart_scheduled",
  "oom",
  "start_limit",
  "resources",
  "core_dumped",
] as const

/**
 * The program whose lines are the manager's. A forced lens reads every line
 * of the unit's journal, the program's own included, and "Started worker
 * pool" or "Closed redis connection" from the program is a sentence the
 * systemd lens would read as a start or a stop. A core dump's report is not
 * among them: systemd-coredump writes it under its own unit and invocation,
 * which would be a run of its own, and the manager's `code=dumped` already
 * says it of the run that crashed.
 */
export const MANAGER = "systemd"

/** How far back the runs are read: far enough for a weekly timer to have fired. */
export const RUNS_DAYS = 7

/**
 * A crash loop, as systemd's own start limit would count one: this many
 * restarts inside this many minutes. `StartLimitBurst` defaults to five in
 * ten seconds, which a unit with `RestartSec=5` never trips, so it loops for
 * days; three in ten minutes is a unit that is not staying up.
 */
export const CRASH_LOOP = { restarts: 3, minutes: 10 }

export type RunOutcome =
  "running" | "succeeded" | "failed" | "killed" | "restarted" | "stopped" | "ended"

export type UnitRun = {
  /** The invocation id; "" for a run on a journal too old to tag one. */
  invocation: string
  /** When systemd started it; unset for a run that started before the lines read. */
  start?: string
  /** The first line of it the read holds, which is its start when it has one. */
  first?: string
  /** When it ended — its exit, its failure, its deactivation — if it has. */
  end?: string
  /** The last line of it, which a restart's "Scheduled restart job" can come well after. */
  last?: string
  outcome: RunOutcome
  /** `exited`, `killed` or `dumped`, and the status or signal that went with it. */
  exitCode?: string
  exitStatus?: string
  signal?: string
  /** systemd's own verdict: `exit-code`, `timeout`, `oom-kill`, `start-limit-hit`… */
  result?: string
  /** CPU time in milliseconds and the memory peak in bytes, from the "Consumed" line. */
  cpu?: number
  memory?: number
  /** A restart was scheduled after it, and systemd's counter when it was. */
  restarted?: boolean
  restarts?: number
}

type Draft = UnitRun & { events: Set<string> }

/** The events that say a run is over, whichever way it went. */
const ENDINGS = new Set([
  "exited",
  "killed",
  "failed",
  "stopped",
  "deactivated",
  "oom",
  "start_limit",
  "core_dumped",
])

const FAILURES = ["failed", "oom", "start_limit", "core_dumped"]

/** "nginx" is how journalctl -u takes "nginx.service", and the journal names it the long way. */
function sameUnit(named: string, unit: string) {
  return named === unit || (!unit.includes(".") && named === `${unit}.service`)
}

/**
 * The lines of a unit's journal that are the manager speaking about that
 * unit. The read asks for the manager's lines already; this holds for a
 * line from anywhere else, and drops what `user@1000.service`'s journal is
 * mostly made of: the user manager's lines about the user's own units, each
 * with the user manager's invocation, which would all read as one run.
 */
export function managerLines(lines: readonly LogLine[], unit: string): LogLine[] {
  return lines.filter((line) => {
    const program = line.attrs?.program
    if (program && program !== MANAGER) return false
    const named = line.attrs?.unit
    return !named || sameUnit(named, unit)
  })
}

function numberOf(value: string | undefined): number | undefined {
  if (value === undefined || value === "") return undefined
  const n = Number(value)
  return Number.isFinite(n) ? n : undefined
}

/**
 * The runs in a unit's lifecycle lines (`managerLines`), oldest first.
 *
 * A line tagged with an invocation belongs to that run wherever it falls. A
 * line without one — systemd before 232 wrote none — is sequenced: a start
 * opens a run and everything until the next start is part of it. A oneshot's
 * "Finished" is the lens's `started` and comes after its deactivation, so
 * only a second `started` opens another run. Only a start says when a run
 * started: a unit up for a month and stopped this week has its stop in the
 * window and its start a month back.
 */
export function unitRuns(lines: readonly LogLine[]): UnitRun[] {
  const drafts: Draft[] = []
  const tagged = new Map<string, Draft>()
  let untagged: Draft | undefined
  const open = (invocation: string) => {
    const draft: Draft = { invocation, outcome: "ended", events: new Set() }
    drafts.push(draft)
    return draft
  }

  for (const line of lines) {
    const event = line.event
    if (!event || line.cont) continue
    const attrs = line.attrs ?? {}
    const invocation = attrs.invocation ?? ""
    let run: Draft
    if (invocation) {
      run = tagged.get(invocation) ?? open(invocation)
      tagged.set(invocation, run)
    } else {
      if (
        !untagged ||
        event === "starting" ||
        (event === "started" && untagged.events.has("started"))
      ) {
        untagged = open("")
      }
      run = untagged
    }

    run.events.add(event)
    const at = line.timestamp
    if (at) {
      run.first ??= at
      if (event === "starting" || event === "started") run.start ??= at
      // The first ending is when the process went: the restart job's
      // "Stopped" lands RestartSec later and is not how long it ran.
      if (ENDINGS.has(event)) run.end ??= at
      run.last = at
    }
    if (attrs.exit_code) run.exitCode = attrs.exit_code
    if (attrs.exit_status) run.exitStatus = attrs.exit_status
    if (attrs.signal) run.signal = attrs.signal
    if (attrs.result && attrs.result !== "success") run.result = attrs.result
    if (event === "resources") {
      run.cpu = numberOf(attrs.cpu) ?? run.cpu
      run.memory = numberOf(attrs.memory) ?? run.memory
    }
    if (event === "restart_scheduled") {
      run.restarted = true
      run.restarts = numberOf(attrs.restarts) ?? run.restarts
    }
  }

  return drafts.map(({ events, ...run }, i) => ({
    ...run,
    outcome: outcomeOf(events, run, i === drafts.length - 1),
  }))
}

/**
 * How a run ended, worst first: a failure is a failure even when a restart
 * followed it, and a clean exit a restart followed is a restart. Only the
 * newest run can still be going — an older one with no ending in the window
 * ended somewhere the journal no longer says.
 */
function outcomeOf(events: Set<string>, run: UnitRun, newest: boolean): RunOutcome {
  if (FAILURES.some((event) => events.has(event))) return "failed"
  if (run.exitCode === "exited" && run.exitStatus && run.exitStatus !== "0") return "failed"
  if (events.has("killed")) return "killed"
  if (events.has("restart_scheduled")) return "restarted"
  if (events.has("stopped")) return "stopped"
  if (events.has("deactivated") || events.has("exited")) return "succeeded"
  return newest && !run.end ? "running" : "ended"
}

/**
 * What makes two runs the same run again: how each ended, and nothing about
 * when or for how long — a crash loop's runs differ only in those.
 */
function sameness(run: UnitRun) {
  if (run.outcome === "running") return undefined
  return [
    run.outcome,
    run.exitCode ?? "",
    run.exitStatus ?? "",
    run.signal ?? "",
    run.result ?? "",
    run.restarted ? "restart" : "",
  ].join("|")
}

/**
 * The runs newest first, with each run of identical ones folded into one
 * row: a unit restarting every five seconds is one line that says how many
 * times, and the one run that ended differently stands out between them.
 * Each group is newest first too, so its head is the latest of it.
 */
export function foldRuns(runs: readonly UnitRun[]): UnitRun[][] {
  const groups: UnitRun[][] = []
  for (const run of runs) {
    const last = groups.at(-1)
    const key = sameness(run)
    if (last && key !== undefined && sameness(last[0]) === key) last.unshift(run)
    else groups.push([run])
  }
  return groups.reverse()
}

export type CrashLoop = {
  /** Restarts in the ten minutes that ended with the latest of them. */
  count: number
  from: string
  to: string
  /**
   * It is looping now: the latest restart is inside the last ten minutes,
   * and the unit has not since stayed up longer than two turns of the loop.
   */
  ongoing: boolean
  /** systemd's restart counter at the latest restart. */
  counter?: number
}

function stamped(lines: readonly LogLine[], event: string) {
  return lines
    .filter((line) => line.event === event && line.timestamp && !line.cont)
    .map((line) => ({ at: Date.parse(line.timestamp!), line }))
    .filter((r) => !Number.isNaN(r.at))
    .sort((a, b) => a.at - b.at)
}

/**
 * The latest burst of restarts dense enough to be a loop, or nothing. Read
 * from the restart lines themselves rather than the folded runs, since a
 * loop whose runs end two different ways is still one loop.
 *
 * A loop is over once the unit has stayed up for twice the longest turn it
 * took — a start after the last restart that has lasted that long — even
 * inside the ten minutes: a unit fixed at 10:05 is not "restarting" at
 * 10:12. Twice, because one turn of a loop can run a little longer than the
 * others and the notice should not flicker on it.
 */
export function crashLoop(lines: readonly LogLine[], now = Date.now()): CrashLoop | undefined {
  const restarts = stamped(lines, "restart_scheduled")
  const span = CRASH_LOOP.minutes * 60_000
  for (let last = restarts.length - 1; last >= CRASH_LOOP.restarts - 1; last--) {
    let first = last
    while (first > 0 && restarts[last].at - restarts[first - 1].at <= span) first--
    const count = last - first + 1
    if (count < CRASH_LOOP.restarts) continue
    let turn = 0
    for (let i = first; i < last; i++) turn = Math.max(turn, restarts[i + 1].at - restarts[i].at)
    const since = stamped(lines, "started").find((s) => s.at > restarts[last].at)
    const settled = since !== undefined && now - since.at > 2 * turn
    const newest = restarts[last].line
    return {
      count,
      from: restarts[first].line.timestamp!,
      to: newest.timestamp!,
      ongoing: now - restarts[last].at <= span && !settled,
      counter: numberOf(newest.attrs?.restarts),
    }
  }
  return undefined
}

/** A run's length, to its end or, while it is still going, to now. */
export function runLength(run: UnitRun, now = Date.now()): number | undefined {
  if (!run.start) return undefined
  const end = run.end ? Date.parse(run.end) : run.outcome === "running" ? now : NaN
  const ms = end - Date.parse(run.start)
  return Number.isNaN(ms) || ms < 0 ? undefined : ms
}

/**
 * The stretch of History that is this run: its lines, the program's and the
 * manager's, narrowed to its invocation where the journal tagged one. A
 * second either side, because the manager's stamp and the program's last
 * line can be the same second read two ways, and on whole seconds, which is
 * what the window's fields show. A run that started before the read reaches
 * back to where the read did (`floor`), since what it printed before it
 * stopped is the reason it is being opened.
 */
export function runWindow(
  run: UnitRun,
  now = Date.now(),
  floor?: string,
): { since: string; until: string; fields: Record<string, string[]> } | undefined {
  const from = run.start ?? floor ?? run.first
  if (!from) return undefined
  const since = Math.floor((Date.parse(from) - 1000) / 1000) * 1000
  const last = run.outcome === "running" ? now : Date.parse(run.last ?? run.end ?? from)
  return {
    since: new Date(since).toISOString(),
    until: new Date(Math.ceil((Math.max(last, since) + 1000) / 1000) * 1000).toISOString(),
    fields: run.invocation ? { invocation: [run.invocation] } : {},
  }
}
