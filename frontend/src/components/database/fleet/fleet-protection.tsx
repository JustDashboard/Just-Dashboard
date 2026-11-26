"use client"

import { sectionHref } from "@/components/database/engine"
import {
  alertCover,
  backupCover,
  namesLine,
  NIGHTLY,
  nightlyJob,
  WATCHED_KINDS,
  type AlertCover,
  type BackupCover,
} from "@/components/database/fleet/protection"
import { ALERT_KINDS } from "@/components/database/ops/alerts-model"
import type { DbAlerts } from "@/components/database/ops/alerts-types"
import { Modal, ModalDescription } from "@/components/modal"
import { StatusDot, type DotTone } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { usePoll } from "@/hooks/use-poll"
import { get, post } from "@/lib/api"
import { describeCron } from "@/lib/cron"
import { bytes, duration, plural, relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { BackupJob, DbAlertKind, DbFleetEntry, NotificationChannel } from "@/lib/types"
import { cn } from "@/lib/utils"
import Link from "next/link"
import { useState } from "react"

/** What a rule the Turn on press adds tells you of, in the words of its default line. */
const WATCHES: Record<DbAlertKind, string> = {
  down: "stops answering for 2 minutes",
  disk: "fills its disk past 90%",
  connections: "uses more than 80% of the connections it allows",
  replication: "falls a minute behind",
  backup_failed: "has a backup that fails",
  backup_missed: "misses a backup",
}

/** A schedule as words inside a sentence: "every day at 03:00". */
function when(schedule: string) {
  const words = describeCron(schedule)
  return words.charAt(0).toLowerCase() + words.slice(1)
}

/** How long until an instant, as the time to wait: "in 9h 12m". */
function until(iso: string | undefined) {
  const at = Date.parse(iso ?? "")
  if (Number.isNaN(at)) return undefined
  const seconds = (at - Date.now()) / 1000
  return seconds < 60 ? "any minute now" : `in ${duration(seconds)}`
}

function sayList(parts: readonly string[]) {
  if (parts.length <= 1) return parts[0] ?? ""
  return `${parts.slice(0, -1).join(", ")} or ${parts.at(-1)}`
}

/**
 * Under the control center's identity line: whether every database is backed
 * up on a schedule, and whether anything would say so when one goes down.
 *
 * It took the place of the band of storage, sessions and backups (0.9.1, at
 * the operator's request). The band split totals the identity line already
 * gave across the databases, which led nowhere, and the one problem it showed
 * — a database never backed up — it showed as an amber span with nothing to
 * press. Each line here is a reading in words, and when it is not the answer
 * a server wants, it ends on the one press that makes it so:
 *
 * - **Schedule** puts every database no job dumps by itself into a nightly
 *   database-only job — the one already there, else a new one on the backup
 *   form's defaults — and starts its first run, so "never backed up" ends now
 *   rather than tonight.
 * - **Turn on** adds the usual alert rules over every database, those added
 *   later too, told to every notification channel that is switched on. It
 *   stays offered while one of them is missing (one that stopped half-way),
 *   and makes only those, by the rules as it reads them just before.
 *
 * Each press asks first, naming what it will make, in a dialog whose answer
 * wears the command's blue: `ConfirmDialog` is red on purpose, for acts that
 * are awkward to undo, and neither of these is one.
 *
 * The two are one framed block of two single lines (24 Nov 2026, at the
 * operator's request that the presses be "incorporated better and more
 * clear"): a dot in the line's tone, the subject, what is true in words,
 * the names it is about, and the press straight after the words, saying
 * what it does — *Schedule backups*, *Turn on alerts* — where it had stood
 * alone at the far end of a reading line as *Schedule* and *Turn on*, under
 * a tinted archive and bell. Once both are answered the lines say so
 * quietly and open the job and the alerts.
 *
 * Every line is one height in every state, so nothing under them moves as
 * the three reads they make land. Only an administrator is offered the
 * presses; anyone else reads the same lines as ways to the pages that have
 * the rest.
 */
export function FleetProtection({
  entries,
  dumps,
  admin,
  onScheduled,
}: {
  entries: DbFleetEntry[]
  dumps: (entry: DbFleetEntry) => boolean
  admin: boolean
  /** A first run has started: the fleet's own reading of each dump is behind. */
  onScheduled: () => void
}) {
  const jobs = usePoll((signal) => get<BackupJob[]>("/backups/", undefined, signal), 60_000)
  const alerts = usePoll((signal) => get<DbAlerts>("/databases/alerts", undefined, signal), 60_000)
  const channels = usePoll(
    (signal) => get<NotificationChannel[]>("/deploy/notifications", undefined, signal),
    120_000,
  )
  const backups = jobs.data ? backupCover(entries, jobs.data, dumps) : undefined
  // A channel list that could not be read says nothing about where alerts go.
  const watch = alerts.data
    ? alertCover(entries, alerts.data.rules, alerts.data.subjects, channels.data)
    : undefined
  const [asking, setAsking] = useState<Question | null>(null)

  const schedule = (cover: BackupCover) => {
    const ids = cover.uncovered.map((entry) => entry.id)
    const names = namesLine(
      cover.uncovered.map((entry) => entry.name),
      6,
    )
    const home = cover.home
    setAsking({
      title: home
        ? `Add ${plural(ids.length, "database")} to ${home.name}`
        : `Back up ${plural(ids.length, "database")} every night`,
      body: home ? (
        <p>
          {names} {ids.length === 1 ? "joins" : "join"} the backup job {home.name}, which runs{" "}
          {when(home.schedule)}. Its next run starts now.
        </p>
      ) : (
        <p>
          A backup job named {NIGHTLY.name} dumps {names} every night at 03:00 into{" "}
          <span className="font-mono">{NIGHTLY.path}</span> on this server and keeps the last{" "}
          {NIGHTLY.retention}. Its first run starts now.
        </p>
      ),
      verb: "Schedule",
      working: "Scheduling…",
      run: async () => {
        let job = home
        if (home) {
          // One database at a time on the narrow route: the job in this read
          // may be behind an edit, and its definition must not ride along.
          for (const id of ids) {
            job = await post<BackupJob>(`/backups/${home.id}/database-dumps`, { connectionId: id })
          }
        } else {
          job = await post<BackupJob>("/backups/", nightlyJob(ids))
        }
        jobs.refresh()
        if (!job) return
        try {
          await post(`/backups/${job.id}/run`, {})
          notify.success(`${job.name} is backing up ${plural(ids.length, "database")}`, {
            description: `Then ${when(job.schedule)}.`,
          })
        } catch (error) {
          notify.success(`${job.name} backs them up ${when(job.schedule)}`)
          notify.error("The first run did not start", error)
        }
        jobs.refresh()
        onScheduled()
      },
    })
  }

  const turnOn = (cover: AlertCover) => {
    const quiet = cover.reached !== undefined && cover.reached.length === 0
    setAsking({
      title: "Turn on alerts for every database",
      body: (
        <>
          <p>
            Tells you when a database {sayList(cover.missing.map((kind) => WATCHES[kind]))}, once
            when it happens and once when it is over. The rules watch every database, and the ones
            added later.
          </p>
          {quiet && (
            <p>
              No notification channel is switched on yet. Until one is, an alert is only kept on the
              database&rsquo;s Alerts page.
            </p>
          )}
        </>
      ),
      verb: "Turn on",
      working: "Turning on…",
      run: async () => {
        try {
          // Read again first: a press after one that stopped half-way, or
          // after another tab's, makes only the kinds still missing, never a
          // second rule beside one that is there.
          const now = await get<DbAlerts>("/databases/alerts")
          const missing = alertCover(entries, now.rules, now.subjects, channels.data).missing
          for (const kind of missing) {
            await post("/databases/alerts/rules", { connectionId: null, kind, channels: [] })
          }
        } finally {
          // What was made before a failure is read, so the line says it.
          alerts.refresh()
        }
        notify.success(`Alerts are on for ${plural(cover.watchable.length, "database")}`)
      },
    })
  }

  return (
    <div
      data-slot="fleet-protection"
      className="min-w-0 divide-y divide-hairline overflow-hidden rounded-xl border"
    >
      <BackupsLine
        cover={backups}
        error={jobs.error}
        action={
          admin && backups && backups.uncovered.length > 0 ? (
            <Press onClick={() => schedule(backups)} verb="Schedule" rest="backups" />
          ) : undefined
        }
      />
      <AlertsLine
        cover={watch}
        rules={alerts.data?.rules ?? []}
        error={alerts.error}
        action={
          admin && watch?.offered ? (
            <Press onClick={() => turnOn(watch)} verb="Turn on" rest="alerts" />
          ) : undefined
        }
      />
      <Ask question={asking} onClose={() => setAsking(null)} />
    </div>
  )
}

/** What a press asks before it makes anything. */
type Question = {
  title: string
  body: React.ReactNode
  /** The answer that goes ahead, one word or two. */
  verb: string
  /** What the footer says while it runs. */
  working: string
  run: () => Promise<void>
}

/**
 * The question a press asks: what it will make, and the answer that goes
 * ahead in the command's own face. It stays open with what failed until it
 * is answered again or put away.
 */
function Ask({ question, onClose }: { question: Question | null; onClose: () => void }) {
  const [busy, setBusy] = useState(false)
  const [startedAt, setStartedAt] = useState(0)
  if (!question) return null
  const go = async () => {
    setBusy(true)
    setStartedAt(Date.now())
    try {
      await question.run()
      onClose()
    } catch (error) {
      notify.error(`${question.title} failed`, error)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal
      open
      onOpenChange={(open) => !open && !busy && onClose()}
      title={question.title}
      variant="alert"
      progress={busy && { label: question.working, startedAt }}
      footer={
        <>
          <Button variant="outline" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={() => void go()} pending={busy}>
            {question.verb}
          </Button>
        </>
      }
    >
      <ModalDescription>{question.body}</ModalDescription>
    </Modal>
  )
}

/** How a line stands: what its dot says. */
type LineTone = "good" | "warning" | "unknown"

const DOT: Record<LineTone, DotTone> = { good: "running", warning: "warning", unknown: "unknown" }

/**
 * One line of the block: a dot in the line's tone, the subject, what is
 * true in words, what it is about in the muted ink, and the press that
 * answers it straight after the words. One height in every state, and one
 * line on every width: on a phone the subject and the names give way to the
 * reading and the press, which are what the line is for.
 *
 * With `href` and no press the line is a way to the page that has the rest,
 * and `label` is where it goes.
 */
function CheckLine({
  name,
  tone,
  children,
  detail,
  action,
  href,
  label,
}: {
  name: string
  tone: LineTone
  children: React.ReactNode
  detail?: React.ReactNode
  action?: React.ReactNode
  href?: string
  label?: string
}) {
  const body = (
    <>
      <StatusDot tone={DOT[tone]} />
      <span className="w-16 shrink-0 text-body font-medium max-sm:hidden">{name}</span>
      <span data-slot="check-words" className="flex min-w-0 shrink items-baseline gap-2.5">
        <span
          className={cn(
            "min-w-0 shrink truncate text-body",
            tone === "warning" && "text-warning",
            tone === "unknown" && "text-muted-foreground",
          )}
        >
          {children}
        </span>
        {detail && (
          <span className="min-w-0 shrink-[2] truncate text-hint text-muted-foreground max-sm:hidden">
            {detail}
          </span>
        )}
      </span>
      {action && <span className="flex shrink-0 items-center">{action}</span>}
    </>
  )
  const shape = "flex h-12 min-w-0 items-center gap-3 px-4"
  if (href && !action)
    return (
      <Link
        href={href}
        aria-label={label}
        data-slot="protection-line"
        data-name={name}
        data-tone={tone}
        className={cn(shape, "focus-ring -outline-offset-2 transition-colors hover:bg-row-hover")}
      >
        {body}
      </Link>
    )
  return (
    <div data-slot="protection-line" data-name={name} data-tone={tone} className={shape}>
      {body}
    </div>
  )
}

/**
 * The press that answers a line, in words that say what it does. The noun
 * gives way on a phone, where the line's words already name it.
 */
function Press({ onClick, verb, rest }: { onClick: () => void; verb: string; rest: string }) {
  return (
    <Button size="xs" variant="outline" className="text-foreground" onClick={onClick}>
      {verb}
      <span className="max-sm:hidden">{rest}</span>
    </Button>
  )
}

function BackupsLine({
  cover,
  error,
  action,
}: {
  cover?: BackupCover
  error?: Error
  action?: React.ReactNode
}) {
  if (!cover) {
    return (
      <CheckLine
        name="Backups"
        tone="unknown"
        href="/backups"
        label="Backup jobs"
        detail={error?.message}
      >
        {error ? "The backup jobs could not be read" : "Reading the backup jobs…"}
      </CheckLine>
    )
  }
  const total = cover.dumpable.length
  if (total === 0) {
    return (
      <CheckLine name="Backups" tone="unknown">
        No database here can be dumped from the dashboard
      </CheckLine>
    )
  }
  const left = cover.uncovered.length
  if (left > 0) {
    return (
      <CheckLine
        name="Backups"
        tone="warning"
        href={action ? undefined : "/backups"}
        label={action ? undefined : "Backup jobs"}
        action={action}
        detail={namesLine(cover.uncovered.map((entry) => entry.name))}
      >
        {left < total
          ? `${left} of ${total} not backed up on a schedule`
          : total === 1
            ? "Not backed up on a schedule"
            : `None of the ${total} is backed up on a schedule`}
      </CheckLine>
    )
  }

  const schedules = new Set(cover.jobs.map((job) => job.schedule.trim()))
  const every = schedules.size === 1 ? when(cover.jobs[0].schedule) : undefined
  const reading = every
    ? total === 1
      ? `Backed up ${every}`
      : `All ${total} backed up ${every}`
    : total === 1
      ? "On a backup schedule"
      : `All ${total} on a backup schedule`
  const only = cover.jobs.length === 1 ? cover.jobs[0] : undefined
  if (cover.failed) {
    const run = cover.failed.lastRun
    return (
      <CheckLine
        name="Backups"
        tone="warning"
        href={`/backups/${cover.failed.id}`}
        label={`Backup job ${cover.failed.name}`}
        detail={`The last run of ${cover.failed.name} failed ${relativeTime(run?.endedAt ?? run?.startedAt)}`}
      >
        {reading}
      </CheckLine>
    )
  }
  const next = until(cover.jobs[0]?.nextRun)
  const newest = cover.jobs
    .map((job) => job.lastRun)
    .filter((run) => run?.status === "success")
    .sort((a, b) => Date.parse(b?.startedAt ?? "") - Date.parse(a?.startedAt ?? ""))[0]
  const detail = cover.running
    ? `${cover.running.name} is running now`
    : [
        newest
          ? `Last run ${relativeTime(newest.endedAt ?? newest.startedAt)}${newest.sizeBytes ? `, ${bytes(newest.sizeBytes)}` : ""}`
          : "Not run yet",
        next && `next ${next}`,
      ]
        .filter(Boolean)
        .join(" · ")
  return (
    <CheckLine
      name="Backups"
      tone="good"
      href={only ? `/backups/${only.id}` : "/backups"}
      label={only ? `Backup job ${only.name}` : "Backup jobs"}
      detail={detail}
    >
      {reading}
    </CheckLine>
  )
}

function AlertsLine({
  cover,
  rules,
  error,
  action,
}: {
  cover?: AlertCover
  rules: DbAlerts["rules"]
  error?: Error
  action?: React.ReactNode
}) {
  if (!cover) {
    return (
      <CheckLine name="Alerts" tone="unknown" detail={error?.message}>
        {error ? "The alert rules could not be read" : "Reading the alert rules…"}
      </CheckLine>
    )
  }
  const total = cover.watchable.length
  const first = cover.watchable[0]
  const page = first ? sectionHref(first.id, "alerts") : undefined
  if (total === 0) {
    return (
      <CheckLine name="Alerts" tone="unknown">
        No database to watch
      </CheckLine>
    )
  }

  const firing = cover.firing[0]
  if (firing) {
    const rule = rules.find((one) => one.id === firing.ruleId)
    const what = rule ? ALERT_KINDS[rule.kind].label.toLowerCase() : "firing"
    return (
      <CheckLine
        name="Alerts"
        tone="warning"
        href={
          firing.connectionId !== undefined
            ? sectionHref(firing.connectionId, "alerts")
            : firing.jobId !== undefined
              ? `/backups/${firing.jobId}`
              : page
        }
        label={`Alerts of ${firing.label}`}
        detail={
          cover.firing.length > 1
            ? `${firing.detail} · ${cover.firing.length - 1} more firing`
            : firing.detail
        }
      >
        {`${firing.label}: ${what}`}
      </CheckLine>
    )
  }

  const left = cover.unwatched.length
  if (left > 0) {
    const paused = cover.missing.length === 0
    return (
      <CheckLine
        name="Alerts"
        tone="warning"
        href={action ? undefined : page}
        label={action ? undefined : "Alerts"}
        action={action}
        detail={
          paused
            ? "Its alert rules are paused"
            : left < total
              ? namesLine(cover.unwatched.map((entry) => entry.name))
              : undefined
        }
      >
        {left < total
          ? `${left} of ${total} without an alert`
          : total === 1
            ? "Nothing tells you when it goes down"
            : "Nothing tells you when one goes down"}
      </CheckLine>
    )
  }

  if (cover.partial) {
    // The usual rules were only partly made: the first alone watches every
    // database, and would read as all of them watched.
    return (
      <CheckLine
        name="Alerts"
        tone="warning"
        href={action ? undefined : page}
        label={action ? undefined : "Alerts"}
        action={action}
        detail={`Not on: ${namesLine(
          cover.missing.map((kind) => ALERT_KINDS[kind].label.toLowerCase()),
          2,
        )}`}
      >
        {`${WATCHED_KINDS.length - cover.missing.length} of the ${WATCHED_KINDS.length} usual alerts on`}
      </CheckLine>
    )
  }

  if (cover.reached && cover.reached.length === 0) {
    return (
      <CheckLine
        name="Alerts"
        tone="warning"
        href="/deploy/notifications"
        label="Notification channels"
        detail="No notification channel is switched on"
      >
        Alerts have nowhere to go
      </CheckLine>
    )
  }

  const on = rules.filter((rule) => rule.enabled).length
  return (
    <CheckLine
      name="Alerts"
      tone="good"
      href={page}
      label="Alerts"
      detail={[
        plural(on, "rule"),
        cover.reached && `told to ${namesLine(cover.reached.map((channel) => channel.name))}`,
      ]
        .filter(Boolean)
        .join(" · ")}
    >
      {total === 1 ? "Watched" : `All ${total} watched`}
    </CheckLine>
  )
}
