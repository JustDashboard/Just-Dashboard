import type { ScheduleLane } from "@/lib/schedule"

/**
 * Where each schedule's own page is: a cron line's and a timer's, with the view
 * in `?tab=`, and the addresses the old sheets used read onto them.
 */

/** The views of a schedule's page. */
export const SCHEDULE_TABS = ["overview", "runs"] as const
export type ScheduleTab = (typeof SCHEDULE_TABS)[number]

function withTab(path: string, tab?: string) {
  return tab && tab !== "overview" ? `${path}?tab=${tab}` : path
}

/**
 * A cron line's page. An account's job is named by the account and its line
 * (`root:3`), a package file's by the file and its line (`system:/etc/crontab:20`,
 * its lane key), in one segment.
 */
export function cronJobHref(id: string, tab?: string) {
  return withTab(`/processes/scheduled/cron/${encodeURIComponent(id)}`, tab)
}

/** A timer's page. */
export function timerHref(unit: string, tab?: string) {
  return withTab(`/processes/scheduled/timers/${encodeURIComponent(unit)}`, tab)
}

/** What a cron line's page is named by, from its lane and whose crontab it is in. */
export function cronJobId(lane: Pick<ScheduleLane, "key" | "kind">, user: string) {
  return lane.kind === "system" ? lane.key : `${user}:${lane.key.replace(/^cron:/, "")}`
}

/** A schedule's page, from its lane. */
export function laneHref(lane: ScheduleLane, user: string, tab?: string) {
  return lane.kind === "timer" && lane.timer
    ? timerHref(lane.timer.unit, tab)
    : cronJobHref(cronJobId(lane, user), tab)
}

/**
 * A cron page's id read back: whose crontab and which lane, or which package
 * file and which lane. An account called "system" is still an account: a
 * package file's id has a path and a line after the word.
 */
export function parseCronJobId(
  id: string,
): { kind: "cron"; user: string; key: string } | { kind: "system"; key: string } | null {
  const system = /^system:(.+):(\d+)$/.exec(id)
  if (system) return { kind: "system", key: id }
  const own = /^(.+):(\d+)$/.exec(id)
  if (own) return { kind: "cron", user: own[1], key: `cron:${own[2]}` }
  return null
}

/** Where an address of the old sheets (`?job=`, `?timer=`) now leads. */
export function sheetAddress(
  params: { job?: string | null; timer?: string | null },
  user: string,
): string | null {
  if (params.timer) return timerHref(params.timer)
  if (params.job?.startsWith("system:")) return cronJobHref(params.job)
  if (params.job?.startsWith("cron:")) return cronJobHref(`${user}:${params.job.slice(5)}`)
  return null
}
