"use client"

import { useMemo } from "react"
import Link from "next/link"
import { Copy } from "@/components/icons"
import { copyText } from "@/lib/clipboard"
import { describeCron, nextCronRun, supportsCronTimeZone } from "@/lib/cron"
import { exactValue } from "@/lib/log/filter"
import {
  countdown,
  cronLineName,
  cronProgram,
  cronRunsBetween,
  longClock,
  LANE_CAP,
} from "@/lib/schedule"
import type { CronJob } from "@/lib/types"
import { cn } from "@/lib/utils"
import { ShellWords } from "@/components/deploy/run-evidence"
import { useNow } from "@/hooks/use-now"
import { IconAction } from "@/components/icon-action"
import { ServiceLogs, type ServiceLogSource } from "@/components/logs/service-logs"
import { Detail, DetailList } from "@/components/detail-list"
import { Well } from "@/components/panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { EmptyNote, Notice } from "@/components/state"
import { KIND } from "@/components/procs/schedule-band"
import { timerHref } from "@/components/procs/schedule-links"
import {
  ScheduleSummary,
  SheetSection,
  WeekStrip,
  WhenItRuns,
} from "@/components/procs/schedule-sheet"

const DAY = 24 * 3600_000

/**
 * One cron line's page parts, opened from its row, its lane or "Up next"
 * (`schedule-page.tsx`): the schedule in a sentence, when it runs next and
 * how often in a week, the next runs listed beside the week drawn, the command
 * coloured as a command and where the line lives (Overview), and the runs
 * cron logged for it — cron's log narrowed to this line's command, which is
 * how cron names a job in its log (Runs).
 *
 * It was a sheet until 0.9.0; a schedule is a page with `?tab=` and the
 * schedules beside it now, as a service and a process are. The package files'
 * lines open here too, without verbs, since their package owns them; one that
 * steps aside for systemd (`deferredToSystemd`) says so first and links the
 * timer that does its work.
 */
export function CronJobOverview({
  job,
  owner,
  source,
  kind,
  zone,
  skipped,
  instead,
}: {
  job: CronJob
  owner: string
  source?: string
  kind: "cron" | "system"
  zone?: string
  skipped?: boolean
  instead?: string
}) {
  const now = useNow(1000)
  const minute = Math.floor(now / 60_000) * 60_000
  const reboot = job.schedule.trim().toLowerCase() === "@reboot"
  const quiet = job.disabled || skipped
  // What the schedule says, whether or not it runs: a switched-off line is
  // drawn as it would run, dimmed.
  const week = useMemo(
    () => cronRunsBetween(job.schedule, minute, minute + 7 * DAY, 7 * LANE_CAP, job.timeZone),
    [job, minute],
  )
  const next = useMemo(
    () =>
      quiet
        ? undefined
        : (week.times[0] ?? nextCronRun(job.schedule, new Date(minute), job.timeZone)?.getTime()),
    [job, minute, quiet, week],
  )
  const color = KIND[kind].color
  const unreadableZone = Boolean(job.timeZone && !supportsCronTimeZone(job.timeZone))
  const perDay = week.times.length / 7

  const notice = skipped ? (
    <Notice title="systemd does this work here">
      The line starts with a test of <code className="font-mono">/run/systemd/system</code>, so on a
      server running systemd it stops there and does nothing.{" "}
      {instead ? (
        <>
          <Link
            href={timerHref(instead)}
            className="font-medium text-foreground underline-offset-2 hover:underline"
          >
            {instead}
          </Link>{" "}
          runs the same job on its own schedule.
        </>
      ) : (
        "A systemd timer runs the same job."
      )}
    </Notice>
  ) : job.disabled ? (
    <Notice title="Switched off">
      The line is commented out, so cron never runs it. The times below are when it would run.
    </Notice>
  ) : undefined

  return (
    <div className="flex animate-rise flex-col pb-2">
      <ScheduleSummary
        flush
        sentence={reboot ? "Every time the server starts" : describeCron(job.schedule)}
        expression={job.schedule}
        facts={[job.user && `runs as ${job.user}`, zone && `${zone} time`]}
        notice={notice}
      />

      {!reboot && (
        <StatGrid columns={2} dense className="border-t border-hairline">
          <StatTile
            className="px-0"
            label="Next run"
            value={
              next !== undefined ? (
                <span className="numeric">in {countdown(next - now)}</span>
              ) : (
                <span className="text-muted-foreground">—</span>
              )
            }
            hint={
              next !== undefined
                ? longClock(next, zone)
                : skipped
                  ? "systemd runs this work"
                  : job.disabled
                    ? "switched off"
                    : unreadableZone
                      ? `this browser cannot read ${job.timeZone}`
                      : "the schedule never matches a date"
            }
          />
          <StatTile
            className="px-0"
            label="This week"
            value={
              <span className={cn("numeric", quiet && "text-muted-foreground")}>
                {week.times.length}
                {week.dense ? "+" : ""}
              </span>
            }
            trailing={week.times.length === 1 ? "run" : "runs"}
            hint={
              quiet
                ? "if it were running"
                : perDay >= 1
                  ? `about ${Math.round(perDay).toLocaleString()} a day`
                  : week.times.length === 0
                    ? "none in the next seven days"
                    : undefined
            }
          />
        </StatGrid>
      )}

      {!reboot && (
        <SheetSection
          flush
          title="When it runs"
          actions={
            quiet && <span className="text-hint text-muted-foreground">if it were running</span>
          }
        >
          <WhenItRuns
            times={week.times}
            dense={week.dense}
            zone={zone}
            empty={
              unreadableZone ? "This browser cannot read its time zone." : "Not in the next week."
            }
            week={
              <WeekStrip
                schedule={job.schedule}
                timeZone={job.timeZone}
                color={color}
                className={cn(quiet && "opacity-50")}
              />
            }
          />
        </SheetSection>
      )}

      <SheetSection
        flush
        title="Command"
        actions={
          <IconAction
            label="Copy command"
            onClick={() => void copyText(job.command, "Command copied")}
          >
            <Copy />
          </IconAction>
        }
      >
        <Well className="max-h-48 text-xs leading-relaxed break-all whitespace-pre-wrap">
          <ShellWords command={job.command} />
        </Well>
        {job.comment && cronLineName(job) !== job.comment && (
          <p className="mt-2 text-hint text-muted-foreground"># {job.comment}</p>
        )}
      </SheetSection>

      <SheetSection flush title="Details">
        <DetailList className="gap-y-2.5">
          <Detail label="Defined in">
            {source ? (
              <Link
                href={`/files?path=${encodeURIComponent(source)}`}
                className="font-mono hover:underline"
              >
                {source}
              </Link>
            ) : (
              owner
            )}
            <span className="text-muted-foreground">, line {job.line}</span>
          </Detail>
          {job.user && <Detail label="Runs as">{job.user}</Detail>}
          <Detail label="Clock">
            {job.timeZone ?? "This browser's, since the server's zone is unknown"}
          </Detail>
          <Detail label="Program">
            <span className="font-mono">{cronProgram(job.command).name}</span>
          </Detail>
        </DetailList>
      </SheetSection>
    </div>
  )
}

/** The runs cron logged for one line: cron's log narrowed to its command. */
export function CronJobRuns({ job, cron }: { job: CronJob; cron?: ServiceLogSource }) {
  const sources = useMemo(() => (cron ? [cron] : []), [cron])
  // How cron names a job in its log is the command as written in the line,
  // so the log narrowed to that command is this job's runs.
  const ask = useMemo(
    () => ({ key: `command:${job.command}`, fields: { command: [exactValue(job.command)] } }),
    [job.command],
  )
  if (!cron)
    return (
      <EmptyNote className="px-0 text-left">
        This server keeps no cron log the dashboard can read, so its runs are not recorded.
      </EmptyNote>
    )
  return (
    <ServiceLogs
      sources={sources}
      ask={ask}
      modes={["live", "search"]}
      className="[contain:inline-size]"
      paneClassName="h-[min(70svh,36rem)] min-h-80"
    />
  )
}
