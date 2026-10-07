"use client"

import { useMemo } from "react"
import Link from "next/link"
import { Clock, Copy } from "@/components/icons"
import { copyText } from "@/lib/clipboard"
import { describeCron, nextCronRun } from "@/lib/cron"
import { timestamp } from "@/lib/format"
import { exactValue } from "@/lib/log-filter"
import { countdown, cronProgram, cronRunsBetween, RUN_CAP } from "@/lib/schedule"
import type { CronJob } from "@/lib/types"
import { cn } from "@/lib/utils"
import { ShellWords } from "@/components/deploy/run-evidence"
import { useNow } from "@/components/deploy/vocabulary"
import { IconAction } from "@/components/icon-action"
import { ServiceLogs, type ServiceLogSource } from "@/components/logs/service-logs"
import { FactDot } from "@/components/metrics/host-identity"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { ProductLogo, programProduct } from "@/components/product-logo"
import { SidePanel } from "@/components/side-panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Status } from "@/components/status-dot"
import { VerbBar, type Verb } from "@/components/verbs"
import { KIND } from "@/components/procs/schedule-band"

const HOUR = 3600_000
const DAY = 24 * HOUR

const WEEKDAY = new Intl.DateTimeFormat(undefined, { weekday: "short" })

function startOfDay(at: number) {
  const day = new Date(at)
  day.setHours(0, 0, 0, 0)
  return day.getTime()
}

/**
 * A schedule's next seven days as seven lines of a day each, midnight to
 * midnight, a tick at every run: the shape a five-field expression has and
 * its sentence cannot carry — "15 2 * * 1-5" is five ticks and two empty
 * lines, "*\/15 8-18 * * *" a block across each working day. Today's line
 * has a hairline at this minute, the runs before it dimmed.
 *
 * The job sheet draws a job's; the job editor draws the one being written,
 * so the picture changes as the fields do.
 */
export function WeekStrip({
  schedule,
  color = KIND.cron.color,
  className,
}: {
  schedule: string
  color?: string
  className?: string
}) {
  const now = useNow(60_000)
  const today = startOfDay(now)
  const days = useMemo(
    () =>
      Array.from({ length: 7 }, (_, i) => {
        const from = startOfDay(today + i * DAY + 12 * HOUR)
        const until = startOfDay(from + DAY + 12 * HOUR)
        return { from, until, ...cronRunsBetween(schedule, from - 1, until - 1) }
      }),
    [schedule, today],
  )
  const place = (at: number, from: number, until: number) => ((at - from) / (until - from)) * 100

  return (
    <div
      role="img"
      aria-label={`${describeCron(schedule)}, over the next seven days`}
      className={cn("space-y-1", className)}
    >
      <div aria-hidden className="ml-12 flex justify-between text-micro text-muted-foreground">
        <span>00</span>
        <span>06</span>
        <span>12</span>
        <span>18</span>
        <span>24</span>
      </div>
      {days.map((day, i) => (
        <div key={day.from} className="flex items-center gap-3">
          <span
            className={cn(
              "w-9 shrink-0 text-hint",
              i === 0 ? "font-medium text-foreground" : "text-muted-foreground",
            )}
          >
            {i === 0 ? "Today" : WEEKDAY.format(day.from)}
          </span>
          <div className="relative h-4 flex-1 rounded-sm bg-surface-header">
            {[25, 50, 75].map((left) => (
              <span
                key={left}
                aria-hidden
                className="absolute inset-y-0 w-px bg-hairline"
                style={{ left: `${left}%` }}
              />
            ))}
            {day.dense ? (
              <span
                aria-hidden
                className="absolute inset-y-1 rounded-full"
                style={{
                  left: `${place(day.times[0], day.from, day.until)}%`,
                  right: 0,
                  background: `color-mix(in oklab, ${color} 55%, transparent)`,
                }}
              />
            ) : (
              day.times.map((at) => (
                <span
                  key={at}
                  aria-hidden
                  title={timestamp(new Date(at).toISOString())}
                  className="absolute inset-y-0.5 w-0.5 -translate-x-1/2 rounded-full"
                  style={{
                    left: `${place(at, day.from, day.until)}%`,
                    background: color,
                    opacity: at < now ? 0.35 : 1,
                  }}
                />
              ))
            )}
            {i === 0 && (
              <span
                aria-hidden
                className="absolute -inset-y-0.5 w-px bg-foreground"
                style={{ left: `${place(now, day.from, day.until)}%` }}
              />
            )}
          </div>
        </div>
      ))}
    </div>
  )
}

/**
 * One cron line, opened from its row or its lane on the band: when it runs
 * next and how often, the week it makes as a picture, the command coloured
 * as a command, and the runs cron logged for it — the cron log narrowed to
 * this line's command, which is how cron names a job in its log.
 *
 * It replaced nothing: a job was a row and its menu, and "when did this last
 * run" meant reading cron's whole log for it two panels down. The package
 * files' lines open here too, without verbs, since their package owns them.
 */
export function CronJobSheet({
  job,
  owner,
  source,
  kind = "cron",
  verbs,
  cron,
  onOpenChange,
}: {
  job: CronJob | undefined
  /** Whose it is: "root's crontab", "/etc/cron.d/sysstat". */
  owner: string
  /** The file it is read from, for a link to it; only the package files have one. */
  source?: string
  kind?: "cron" | "system"
  verbs: Verb[]
  /** cron's own log, when the host keeps one. */
  cron?: ServiceLogSource
  onOpenChange: (open: boolean) => void
}) {
  const program = job ? cronProgram(job.command) : undefined
  return (
    <SidePanel
      open={job !== undefined}
      onOpenChange={onOpenChange}
      title={
        <>
          <ProductLogo id={programProduct(program?.segment)} size="sm" fallback={Clock} />
          <span className="min-w-0 truncate">{job?.comment || program?.name}</span>
        </>
      }
      description={job ? `${describeCron(job.schedule)} · ${job.command}` : "Cron job"}
      width="lg"
      initialFocus="body"
      actions={
        job && (
          <>
            <Status
              state={job.disabled ? "inactive" : "active"}
              label={job.disabled ? "disabled" : "enabled"}
            />
            <VerbBar verbs={verbs} />
          </>
        )
      }
      bodyClassName="flex min-h-0 flex-1 flex-col overflow-y-auto"
    >
      {job && (
        <CronJobDetail
          key={`${job.line}:${job.raw}`}
          job={job}
          owner={owner}
          source={source}
          kind={kind}
          cron={cron}
        />
      )}
    </SidePanel>
  )
}

function CronJobDetail({
  job,
  owner,
  source,
  kind,
  cron,
}: {
  job: CronJob
  owner: string
  source?: string
  kind: "cron" | "system"
  cron?: ServiceLogSource
}) {
  const now = useNow(1000)
  const minute = Math.floor(now / 60_000) * 60_000
  const reboot = job.schedule.trim().toLowerCase() === "@reboot"
  const next = useMemo(
    () => (job.disabled ? undefined : nextCronRun(job.schedule, new Date(minute))?.getTime()),
    [job, minute],
  )
  const day = useMemo(
    () => (job.disabled ? undefined : cronRunsBetween(job.schedule, minute, minute + DAY)),
    [job, minute],
  )
  const week = useMemo(
    () =>
      job.disabled
        ? undefined
        : cronRunsBetween(job.schedule, minute, minute + 7 * DAY, 7 * RUN_CAP),
    [job, minute],
  )
  const sources = useMemo(() => (cron ? [cron] : []), [cron])
  // How cron names a job in its log is the command as written in the line,
  // so the log narrowed to that command is this job's runs.
  const ask = useMemo(
    () => ({ key: `command:${job.command}`, fields: { command: [exactValue(job.command)] } }),
    [job.command],
  )
  const color = KIND[kind].color

  return (
    <div className="flex animate-rise flex-col gap-6 px-5 pt-4 pb-6">
      <p className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
        {source ? (
          <Link
            href={`/files?path=${encodeURIComponent(source)}`}
            className="font-mono hover:text-foreground hover:underline"
          >
            {source}
          </Link>
        ) : (
          <span>{owner}</span>
        )}
        <FactDot />
        <span className="numeric">line {job.line}</span>
        {job.user && (
          <>
            <FactDot />
            <span>as {job.user}</span>
          </>
        )}
        <FactDot />
        <span className="font-mono">{job.schedule}</span>
      </p>

      <StatGrid columns={3} dense className="-mx-5 border-y border-hairline">
        <StatTile
          className="px-5"
          label="Next run"
          value={
            next !== undefined ? (
              <span className="numeric">{countdown(next - now)}</span>
            ) : (
              <span className="text-muted-foreground">{reboot ? "at boot" : "—"}</span>
            )
          }
          hint={
            next !== undefined
              ? timestamp(new Date(next).toISOString())
              : job.disabled
                ? "the line is commented out"
                : reboot
                  ? "only when the server starts"
                  : "never matches a date"
          }
        />
        <StatTile
          className="px-5"
          label="In the next day"
          value={
            <span className="numeric">
              {day ? `${day.times.length}${day.dense ? "+" : ""}` : "0"}
            </span>
          }
          trailing={day && day.times.length === 1 ? "run" : "runs"}
          hint={describeCron(job.schedule)}
        />
        <StatTile
          className="px-5"
          label="In the next week"
          value={
            <span className="numeric">
              {week ? `${week.times.length}${week.dense ? "+" : ""}` : "0"}
            </span>
          }
          trailing={week && week.times.length === 1 ? "run" : "runs"}
          hint={
            week && week.times.length > 0
              ? `last ${timestamp(new Date(week.times.at(-1)!).toISOString())}`
              : undefined
          }
        />
      </StatGrid>

      {!reboot && (
        <Panel plain>
          <PanelHeader
            title="This week"
            actions={
              job.disabled && (
                <span className="text-hint text-muted-foreground">were it enabled</span>
              )
            }
          />
          <PanelBody className="pt-3">
            <WeekStrip
              schedule={job.schedule}
              color={color}
              className={cn(job.disabled && "opacity-40")}
            />
          </PanelBody>
        </Panel>
      )}

      <Panel plain>
        <PanelHeader
          title="Command"
          actions={
            <IconAction
              label="Copy command"
              onClick={() => void copyText(job.command, "Command copied")}
            >
              <Copy />
            </IconAction>
          }
        />
        <PanelBody className="space-y-2 pt-3">
          <Well className="max-h-40 text-xs leading-relaxed break-all whitespace-pre-wrap">
            <ShellWords command={job.command} />
          </Well>
          {job.comment && <p className="text-hint text-muted-foreground"># {job.comment}</p>}
        </PanelBody>
      </Panel>

      {cron && (
        <Panel plain>
          <PanelHeader title="Runs" />
          <ServiceLogs
            sources={sources}
            ask={ask}
            modes={["live", "search"]}
            layout="sheet"
            className="[contain:inline-size]"
            paneClassName="h-[22rem]"
          />
        </Panel>
      )}
    </div>
  )
}
