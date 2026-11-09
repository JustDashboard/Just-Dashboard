"use client"

import { useMemo } from "react"
import Link from "next/link"
import { Clock, Copy } from "@/components/icons"
import { copyText } from "@/lib/clipboard"
import { describeCron, nextCronRun, supportsCronTimeZone } from "@/lib/cron"
import { exactValue } from "@/lib/log-filter"
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
import { useNow } from "@/components/deploy/vocabulary"
import { IconAction } from "@/components/icon-action"
import { ServiceLogs, type ServiceLogSource } from "@/components/logs/service-logs"
import { Detail, DetailList } from "@/components/page"
import { Well } from "@/components/panel"
import { ProductLogo, programProduct } from "@/components/product-logo"
import { SidePanel } from "@/components/side-panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { VerbBar, type Verb } from "@/components/verbs"
import { KIND } from "@/components/procs/schedule-band"
import {
  ScheduleSummary,
  SheetSection,
  WeekStrip,
  WhenItRuns,
} from "@/components/procs/schedule-sheet"

const DAY = 24 * 3600_000

/**
 * One cron line, opened from its row or from "Up next": the schedule in a
 * sentence, when it runs next and how often in a week, the next runs listed
 * beside the week drawn, the command coloured as a command, where the line
 * lives, and the runs cron logged for it — cron's log narrowed to this
 * line's command, which is how cron names a job in its log.
 *
 * The package files' lines open here too, without verbs, since their package
 * owns them; one that steps aside for systemd (`deferredToSystemd`) says so
 * first and links the timer that does its work.
 */
export function CronJobSheet({
  job,
  owner,
  source,
  kind = "cron",
  verbs,
  cron,
  zone,
  skipped,
  instead,
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
  /** The host's zone, which times are written in. */
  zone?: string
  /** The line steps aside on this host because systemd runs its work. */
  skipped?: boolean
  /** The timer that does a skipped line's work. */
  instead?: string
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
          <span className="min-w-0 truncate">{job ? cronLineName(job) : "Cron job"}</span>
        </>
      }
      description={job ? `${describeCron(job.schedule)} · ${job.command}` : "Cron job"}
      width="lg"
      initialFocus="body"
      actions={
        job && (
          <>
            <Status
              state={job.disabled ? "inactive" : skipped ? "inactive" : "active"}
              label={job.disabled ? "switched off" : skipped ? "left to systemd" : "enabled"}
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
          zone={zone}
          skipped={skipped}
          instead={instead}
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
  zone,
  skipped,
  instead,
}: {
  job: CronJob
  owner: string
  source?: string
  kind: "cron" | "system"
  cron?: ServiceLogSource
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
  const sources = useMemo(() => (cron ? [cron] : []), [cron])
  // How cron names a job in its log is the command as written in the line,
  // so the log narrowed to that command is this job's runs.
  const ask = useMemo(
    () => ({ key: `command:${job.command}`, fields: { command: [exactValue(job.command)] } }),
    [job.command],
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
            href={`/processes/scheduled?timer=${encodeURIComponent(instead)}`}
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
        sentence={reboot ? "Every time the server starts" : describeCron(job.schedule)}
        expression={job.schedule}
        facts={[job.user && `runs as ${job.user}`, zone && `${zone} time`]}
        notice={notice}
      />

      {!reboot && (
        <StatGrid columns={2} dense className="border-t border-hairline">
          <StatTile
            className="px-6"
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
            className="px-6"
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

      <SheetSection title="Details">
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

      <SheetSection title="Runs">
        {cron ? (
          <ServiceLogs
            sources={sources}
            ask={ask}
            modes={["live", "search"]}
            layout="sheet"
            className="[contain:inline-size]"
            paneClassName="h-[22rem]"
          />
        ) : (
          <p className="text-body text-muted-foreground">
            This server keeps no cron log the dashboard can read, so its runs are not recorded.
          </p>
        )}
      </SheetSection>
    </div>
  )
}
