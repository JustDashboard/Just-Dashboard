import type { AuditEntry } from "@/lib/types"
import { plural } from "@/lib/format"
import { auditSection, isMachine } from "@/components/audit/marks"

const HOUR_MS = 3_600_000

/** How many hours the page's opening reads: the running one and the 23 before it. */
export const DAY_HOURS = 24

/**
 * Where the last day starts: on the hour, 23 hours before the running one. On
 * the hour so that each stretch of the strip is an hour a clock names ("14:00"),
 * and so that the question only changes once an hour: a `since` of "24 hours
 * before this instant" was a new question on every poll.
 */
export function dayStart(now: number) {
  return Math.floor(now / HOUR_MS) * HOUR_MS - (DAY_HOURS - 1) * HOUR_MS
}

export type DayHour = {
  /** When the hour starts, in milliseconds. */
  from: number
  changes: number
  failed: number
  /** The parts of the product changed in it, busiest first. */
  sections: string[]
}

export type DayReading = {
  /** Oldest first, as a strip reads into the present. */
  hours: DayHour[]
  /** The accounts behind the changes, most active first. */
  people: string[]
  /** Changes nobody made: webhooks and the dashboard's own. */
  machines: number
  /** The parts of the product changed, busiest first, with how often. */
  sections: { label: string; changes: number }[]
  signIns: {
    ok: number
    refused: number
    /** The newest sign-in that was let in. */
    last?: Pick<AuditEntry, "username" | "ip" | "ts">
  }
}

/**
 * The last day as the page's opening says it, from the day's entries, newest
 * first as the server hands them over. An entry older than the day, or with a
 * date nothing can read, is counted for who made it and left out of the hours.
 */
export function dayReading(entries: AuditEntry[], start: number): DayReading {
  const hours = Array.from({ length: DAY_HOURS }, (_, index) => ({
    from: start + index * HOUR_MS,
    changes: 0,
    failed: 0,
    sections: new Map<string, number>(),
  }))
  const people = new Map<string, number>()
  const sections = new Map<string, number>()
  let machines = 0
  const signIns: DayReading["signIns"] = { ok: 0, refused: 0 }
  for (const entry of entries) {
    const label = auditSection(entry.action)?.label
    const hour = hours[Math.floor((new Date(entry.ts).getTime() - start) / HOUR_MS)]
    if (hour) {
      hour.changes++
      if (!entry.success) hour.failed++
      if (label) hour.sections.set(label, (hour.sections.get(label) ?? 0) + 1)
    }
    if (isMachine(entry)) machines++
    else if (entry.username && entry.actor !== "anonymous") {
      people.set(entry.username, (people.get(entry.username) ?? 0) + 1)
    }
    if (label) sections.set(label, (sections.get(label) ?? 0) + 1)
    if (entry.action === "auth.login") {
      if (entry.success) {
        signIns.ok++
        signIns.last ??= { username: entry.username, ip: entry.ip, ts: entry.ts }
      } else signIns.refused++
    }
  }
  return {
    hours: hours.map((hour) => ({ ...hour, sections: ranked(hour.sections).map(([l]) => l) })),
    people: ranked(people).map(([name]) => name),
    machines,
    sections: ranked(sections).map(([label, changes]) => ({ label, changes })),
    signIns,
  }
}

function ranked(counts: Map<string, number>) {
  return [...counts.entries()].sort((a, b) => b[1] - a[1])
}

/**
 * How an hour went, in the availability ribbon's four: nothing was changed,
 * every change went through, some were refused, every one was.
 */
export function hourTone(hour: Pick<DayHour, "changes" | "failed">) {
  if (hour.changes === 0) return "quiet"
  if (hour.failed === 0) return "ok"
  return hour.failed < hour.changes ? "dented" : "failed"
}

/** "3 changes, 1 failed · Docker, Sign-in": an hour, as the line under the strip says it. */
export function hourWords(hour: DayHour) {
  if (hour.changes === 0) return "nothing was changed"
  const count = plural(hour.changes, "change")
  const outcome =
    hour.failed === 0
      ? count
      : hour.failed === hour.changes
        ? `${count}, ${hour.changes === 1 ? "and it failed" : "all failed"}`
        : `${count}, ${hour.failed} failed`
  const where = hour.sections.slice(0, 3).join(", ")
  return where ? `${outcome} · ${where}` : outcome
}

/** "1 of 8 failed": the day's failures as a share nobody has to work out. */
export function failedWords(failed: number, total: number) {
  if (total === 0) return "Nothing was changed"
  if (failed === 0) return "Every change went through"
  return `${failed.toLocaleString()} of ${total.toLocaleString()} failed`
}

/** Who made the day's changes: "2 people and 3 automated changes". */
export function peopleWords(people: string[], machines: number) {
  if (people.length === 0) {
    return machines > 0 ? `only automations, ${plural(machines, "change")}` : undefined
  }
  const who = people.length === 1 ? people[0] : plural(people.length, "person", "people")
  return machines > 0 ? `${who} and ${plural(machines, "automated change")}` : who
}
