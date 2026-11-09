"use client"

import { useMemo } from "react"
import Link from "next/link"
import { nextNativeCronRun as nextCronRun } from "@/lib/cron"
import { plural } from "@/lib/format"
import { deferredToSystemd, isExampleLine, timerFor } from "@/lib/schedule"
import type { CronJob, Crontab, SystemdTimer } from "@/lib/types"
import type { PollState } from "@/hooks/use-poll"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import { Status } from "@/components/status-dot"
import {
  stickyTableHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { JobCell, ScheduleCell } from "@/components/procs/cron-jobs"
import { RunAt } from "@/components/procs/timers"

/**
 * The cron files packages install — `/etc/crontab` and `/etc/cron.d/*` —
 * read-only, since editing them belongs to the package manager.
 *
 * It was a table per file, each with its own header row, every line a
 * schedule over a wall of mono, `/etc/crontab`'s worked example listed as a
 * switched-off job (the ruler of pipes above it as its note), and no word on
 * when anything ran. It is one table now, grouped by file under a row that
 * names it, each line the shape a cron job has above it — what it is called,
 * the command that does the work, the schedule in words, who it runs as,
 * and when it runs next. The worked example is left out.
 *
 * A line that steps aside for systemd says so where its next run would be:
 * Debian ships certbot's, e2scrub's and others' cron lines beside timers for
 * the same work, and on a host running systemd the line's guard stops it.
 */
export function SystemCronPanel({
  system,
  zone,
  systemd,
  timers,
  onOpen,
}: {
  system: PollState<Crontab[]>
  zone?: string
  /** Whether the host runs systemd, which makes the guarded lines no-ops. */
  systemd: boolean
  timers: SystemdTimer[]
  onOpen: (key: string) => void
}) {
  const files = useMemo(
    () =>
      (system.data ?? []).map((file) => ({
        ...file,
        jobs: file.jobs.filter((job) => !isExampleLine(job)),
      })),
    [system.data],
  )
  const lines = files.reduce((n, file) => n + file.jobs.length, 0)

  return (
    <Panel>
      <PanelHeader
        title="System cron files"
        advanced
        actions={
          files.length > 0 && (
            <span className="numeric text-hint text-muted-foreground">
              {plural(lines, "line")} in {plural(files.length, "file")} · read-only
            </span>
          )
        }
      />
      <PanelBody flush>
        {system.loading && !system.data && <LoadingRows rows={4} className="pt-3" />}
        {system.error && !system.data && <ErrorState error={system.error} className="mt-3" />}
        {system.data && files.length === 0 && <EmptyNote>No system cron files.</EmptyNote>}
        {files.length > 0 && (
          <Table className="table-fixed" containerClassName="w-auto group-data-[plain]/panel:-mx-4">
            <TableHeader className={stickyTableHeader}>
              <TableRow>
                <TableHead>Job</TableHead>
                <TableHead className="hidden w-[30%] lg:table-cell">Schedule</TableHead>
                <TableHead className="hidden w-24 xl:table-cell">Runs as</TableHead>
                <TableHead className="w-36">Next run</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {files.map((file) => (
                <FileRows
                  key={file.source}
                  file={file}
                  zone={zone}
                  systemd={systemd}
                  timers={timers}
                  onOpen={onOpen}
                />
              ))}
            </TableBody>
          </Table>
        )}
      </PanelBody>
    </Panel>
  )
}

function FileRows({
  file,
  zone,
  systemd,
  timers,
  onOpen,
}: {
  file: Crontab
  zone?: string
  systemd: boolean
  timers: SystemdTimer[]
  onOpen: (key: string) => void
}) {
  return (
    <>
      <TableRow className="bg-surface-header hover:bg-surface-header">
        <TableCell colSpan={4} className="py-2">
          <div className="flex min-w-0 items-baseline gap-3">
            <Link
              href={`/files?path=${encodeURIComponent(file.source)}`}
              className="truncate font-mono text-xs font-medium hover:underline"
            >
              {file.source}
            </Link>
            <span className="text-hint text-muted-foreground">
              {file.jobs.length === 0 ? "no jobs" : plural(file.jobs.length, "line")}
            </span>
          </div>
        </TableCell>
      </TableRow>
      {file.jobs.map((job) => {
        const key = `system:${file.source}:${job.line}`
        return (
          <SystemLine
            key={key}
            job={job}
            zone={zone}
            skipped={systemd && deferredToSystemd(job.command)}
            instead={timerFor(job.command, timers)}
            onOpen={() => onOpen(key)}
          />
        )
      })}
    </>
  )
}

function SystemLine({
  job,
  zone,
  skipped,
  instead,
  onOpen,
}: {
  job: CronJob
  zone?: string
  skipped: boolean
  instead?: string
  onOpen: () => void
}) {
  const quiet = job.disabled || skipped
  const next = useMemo(
    () => (quiet ? null : nextCronRun(job.schedule, new Date(), job.timeZone)),
    [job, quiet],
  )
  return (
    <TableRow className="group" onActivate={onOpen} data-system-line={job.line}>
      <TableCell className="whitespace-normal">
        <JobCell job={job} onOpen={onOpen} dim={quiet} />
      </TableCell>
      <TableCell className="hidden whitespace-normal lg:table-cell">
        <ScheduleCell schedule={job.schedule} dim={quiet} />
      </TableCell>
      <TableCell className="hidden text-muted-foreground xl:table-cell">
        {job.user || "—"}
      </TableCell>
      <TableCell className="whitespace-normal">
        {skipped ? (
          <div title="The line tests for systemd and steps aside, because a timer does the same work">
            <Status state="inactive" label="left to systemd" />
            {instead && (
              <p className="truncate pl-3.5 text-hint text-muted-foreground">{instead}</p>
            )}
          </div>
        ) : job.disabled ? (
          <Status state="inactive" label="commented out" />
        ) : next ? (
          <RunAt at={next.getTime()} zone={zone} />
        ) : (
          <span className="text-muted-foreground">
            {job.schedule.toLowerCase() === "@reboot" ? "at boot" : "never"}
          </span>
        )}
      </TableCell>
    </TableRow>
  )
}
