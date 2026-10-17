import type { DbAlertKind } from "@/lib/types"
import type {
  DbAlertEvent,
  DbAlertRule,
  DbAlertState,
  DbAlertSubject,
} from "@/components/database/ops/alerts-types"

/**
 * A database alert in words. A rule is read as a sentence — "Disk filling
 * up: above 90% for 5 min" — so the list, the form's foot and the toast say
 * the same thing, and each kind states the range the server holds its line
 * and its minutes to (`dbAlertSpecs` in `api/database_alerts.go`), so the form
 * refuses what the server would before it is sent.
 */

export type AlertKindSpec = {
  label: string
  /** The kind's card: what it watches, in a line. */
  hint: string
  /** The line's unit; absent for a kind with no line. */
  unit?: "%" | "s"
  /** Its range, where it starts, and in words what it is of and how far it goes. */
  line?: { min: number; max: number; start: number; of: string; range: string }
  /** How long it must hold, or a missed backup's grace after its time. */
  minutes: { min: number; max: number; start: number; label: string }
  /** The rule's predicate, read after its label and a colon. */
  describe: (rule: Pick<DbAlertRule, "threshold" | "minutes">) => string
  /** A subject's reading, short. */
  read: (reading: number) => string
}

const NO_HOLD = { min: 0, max: 0, start: 0, label: "" }

/** A length of minutes as the form and the sentences say it. */
export function durationWords(minutes: number): string {
  if (minutes < 60) return `${minutes} min`
  if (minutes % 1440 === 0) return minutes === 1440 ? "1 day" : `${minutes / 1440} days`
  const hours = Math.floor(minutes / 60)
  const rest = minutes % 60
  return rest ? `${hours} h ${rest} min` : `${hours} h`
}

const held = (minutes: number) => (minutes > 0 ? ` for ${durationWords(minutes)}` : "")

/** How long a subject has been firing, to the minute: a status that ticked seconds read as a clock. */
export function firingFor(since: string, now = Date.now()) {
  return durationWords(Math.max(1, Math.floor((now - Date.parse(since)) / 60_000)))
}

/** "a" or "an" before a figure, as it is said: an 80% line, an 18 s line. */
function article(figure: number) {
  const said = String(figure)
  return said.startsWith("8") || /^(11|18)(\d{3})?$/.test(said) ? "an" : "a"
}

const percent = (value: number) =>
  `${value >= 10 || value === 0 ? Math.round(value) : value.toFixed(1)}%`

export const ALERT_KINDS: Record<DbAlertKind, AlertKindSpec> = {
  down: {
    label: "Server down",
    hint: "The server doesn't answer.",
    minutes: { min: 1, max: 1440, start: 2, label: "For" },
    describe: (rule) => `no answer${held(rule.minutes)}`,
    read: (reading) => (reading > 0 ? "not answering" : "answering"),
  },
  disk: {
    label: "Disk filling up",
    hint: "Its data volume passes a set % full.",
    unit: "%",
    line: { min: 10, max: 99, start: 90, of: "Of the disk's size.", range: "Between 10% and 99%." },
    minutes: { min: 0, max: 1440, start: 5, label: "For" },
    describe: (rule) => `above ${rule.threshold}%${held(rule.minutes)}`,
    read: (reading) => `${percent(reading)} used`,
  },
  connections: {
    label: "Connections near the limit",
    hint: "Open connections near the server's limit.",
    unit: "%",
    line: {
      min: 10,
      max: 100,
      start: 80,
      of: "Of the most it allows.",
      range: "Between 10% and 100%.",
    },
    minutes: { min: 0, max: 1440, start: 5, label: "For" },
    describe: (rule) => `above ${rule.threshold}% of the limit${held(rule.minutes)}`,
    read: (reading) => `${percent(reading)} of the limit`,
  },
  replication: {
    label: "Replication behind",
    hint: "A replica falls behind or stops following.",
    unit: "s",
    line: {
      min: 1,
      max: 86400,
      start: 60,
      of: "Seconds behind, up to 24 h.",
      range: "Between 1 s and 24 h (86400 s).",
    },
    minutes: { min: 0, max: 1440, start: 2, label: "For" },
    describe: (rule) => `more than ${rule.threshold} s behind${held(rule.minutes)}, or stopped`,
    read: (reading) => (reading < 0 ? "stopped" : `${Math.round(reading)} s behind`),
  },
  backup_failed: {
    label: "Backup failed",
    hint: "A backup job for it fails.",
    minutes: NO_HOLD,
    describe: () => "the last run of a backup job fails",
    read: (reading) => (reading > 0 ? "last run failed" : "last run succeeded"),
  },
  backup_missed: {
    label: "Backup missed",
    hint: "No successful backup when one was due.",
    minutes: { min: 5, max: 10080, start: 60, label: "Grace" },
    describe: (rule) => `no successful run ${durationWords(rule.minutes)} after one is due`,
    read: (hours) => `${Math.round(hours)} h since a success`,
  },
}

export const ALERT_KIND_ORDER = Object.keys(ALERT_KINDS) as DbAlertKind[]

/** The kinds about a backup job rather than about the server. */
export function isBackupKind(kind: DbAlertKind) {
  return kind === "backup_failed" || kind === "backup_missed"
}

/** A rule as one sentence, without its full stop. */
export function sentence(rule: Pick<DbAlertRule, "kind" | "threshold" | "minutes">) {
  const words = ALERT_KINDS[rule.kind]
  return `${words.label}: ${words.describe(rule)}`
}

export const DEFAULT_COOLDOWN = 30
export const MAX_COOLDOWN = 7 * 24 * 60

/** How often at most a subject is told, as the form says it. */
export function cooldownWords(minutes: number) {
  return minutes > 0 ? `at most once every ${durationWords(minutes)}` : "every time it crosses"
}

/** The draft a form holds: every figure as typed. */
export type AlertDraft = {
  kind: DbAlertKind
  /** One database, or every one whose engine has the kind. */
  scope: "this" | "every"
  threshold: string
  minutes: string
  cooldown: string
  notifyRecovery: boolean
  channels: number[]
}

export function draftFor(
  rule: DbAlertRule | undefined,
  kind: DbAlertKind,
  scope: AlertDraft["scope"],
): AlertDraft {
  if (rule) {
    return {
      kind: rule.kind,
      scope: rule.connectionId === null ? "every" : "this",
      threshold: String(rule.threshold),
      minutes: String(rule.minutes),
      cooldown: String(rule.cooldownMinutes),
      notifyRecovery: rule.notifyRecovery,
      channels: rule.channels,
    }
  }
  const spec = ALERT_KINDS[kind]
  return {
    kind,
    scope,
    threshold: String(spec.line?.start ?? 0),
    minutes: String(spec.minutes.start),
    cooldown: String(DEFAULT_COOLDOWN),
    notifyRecovery: true,
    channels: [],
  }
}

/**
 * The draft on another kind: its own line and minutes, the rest kept. A line
 * typed for disk use is not a number of seconds of lag.
 */
export function withKind(draft: AlertDraft, kind: DbAlertKind): AlertDraft {
  const start = draftFor(undefined, kind, draft.scope)
  return { ...draft, kind, threshold: start.threshold, minutes: start.minutes }
}

const whole = (text: string) => (/^\d+$/.test(text.trim()) ? Number(text) : NaN)

/** Why the draft cannot be saved, by field; empty when it can. */
export function draftErrors(
  draft: AlertDraft,
): Partial<Record<"threshold" | "minutes" | "cooldown", string>> {
  const spec = ALERT_KINDS[draft.kind]
  const errors: Partial<Record<"threshold" | "minutes" | "cooldown", string>> = {}
  if (spec.line) {
    const value = Number(draft.threshold)
    if (
      draft.threshold.trim() === "" ||
      !Number.isFinite(value) ||
      value < spec.line.min ||
      value > spec.line.max
    ) {
      errors.threshold = spec.line.range
    }
  }
  if (spec.minutes.max > 0) {
    const value = whole(draft.minutes)
    if (!(value >= spec.minutes.min && value <= spec.minutes.max)) {
      errors.minutes = `Between ${durationWords(spec.minutes.min)} and ${durationWords(spec.minutes.max)}.`
    }
  }
  const cooldown = whole(draft.cooldown)
  if (!(cooldown >= 0 && cooldown <= MAX_COOLDOWN)) {
    errors.cooldown = `Between 0 min and ${durationWords(MAX_COOLDOWN)}.`
  }
  return errors
}

/** The body the draft is saved as, for a page about database `id`. */
export function draftBody(draft: AlertDraft, id: number, enabled = true) {
  const spec = ALERT_KINDS[draft.kind]
  return {
    connectionId: draft.scope === "every" ? null : id,
    kind: draft.kind,
    ...(spec.line && { threshold: Number(draft.threshold) }),
    ...(spec.minutes.max > 0 && { minutes: whole(draft.minutes) }),
    cooldownMinutes: whole(draft.cooldown),
    notifyRecovery: draft.notifyRecovery,
    channels: draft.channels,
    enabled,
  }
}

/** The draft read as the rule it would save, for its sentence. */
export function draftRule(draft: AlertDraft) {
  return {
    kind: draft.kind,
    threshold: Number(draft.threshold) || 0,
    minutes: whole(draft.minutes) || 0,
  }
}

const RANK: Record<DbAlertState, number> = { firing: 0, held: 1, pending: 2, unknown: 3, quiet: 4 }

/** The subject a rule's row is about: the worst of them, the one that needs a hand first. */
export function focusOf(subjects: DbAlertSubject[]): DbAlertSubject | undefined {
  return [...subjects].sort((a, b) => RANK[a.state] - RANK[b.state])[0]
}

/** Whether a rule is telling somebody about something now. */
export function isFiring(subjects: DbAlertSubject[]) {
  return subjects.some((s) => s.state === "firing" || s.state === "held")
}

/** What happened to an event's message, in words. */
export function eventOutcome(event: Pick<DbAlertEvent, "delivered" | "failed" | "kept" | "event">) {
  if (event.kept === "cooldown") {
    return event.event === "fired"
      ? "kept back by the cooldown"
      : "not told: its firing was kept back"
  }
  if (event.kept === "recovery") return "not told: the rule tells only when it fires"
  if (event.delivered === 0 && event.failed === 0) return "no channel to tell"
  const told = `told ${event.delivered} ${event.delivered === 1 ? "channel" : "channels"}`
  return event.failed > 0 ? `${told}, ${event.failed} failed` : told
}

/** Where a reading stands against its line, as the meter draws it; undefined for a kind with none. */
export function meterOf(rule: DbAlertRule, subject: DbAlertSubject | undefined) {
  const spec = ALERT_KINDS[rule.kind]
  if (!spec.line || !subject || subject.state === "unknown" || subject.reading < 0) return undefined
  const scale = spec.unit === "%" ? 100 : Math.max(rule.threshold * 2, subject.reading * 1.1, 1)
  return {
    value: (subject.reading / scale) * 100,
    mark: (rule.threshold / scale) * 100,
    label: `${spec.read(subject.reading)} against ${article(rule.threshold)} ${rule.threshold}${spec.unit === "%" ? "%" : " s"} line`,
  }
}
