"use client"

import { useMemo } from "react"
import Link from "next/link"
import { get } from "@/lib/api"
import { describeCron, nextCronRun } from "@/lib/cron"
import { relativeTime, timestamp } from "@/lib/format"
import type { Crontab } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useSessionState, useViewState } from "@/lib/view-state"
import { useConfirm } from "@/components/confirm-dialog"
import { Page, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { EmptyNote } from "@/components/state"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { CommandCell, CronJobsPanel } from "@/components/procs/cron-jobs"
import { TimersPanel, type TimerList } from "@/components/procs/timers"
import { cn } from "@/lib/utils"

/**
 * Everything that runs on a clock: one account's crontab as jobs you can
 * act on, the host's systemd timers, and the package-owned cron files,
 * which are read-only here because editing them belongs to the package
 * manager.
 *
 * The page opens on four readings (§15 pass 2) that none of the three lists
 * can say alone — what fires next across cron and the timers together, how
 * many jobs this account has and how many are switched off, how many timers
 * are armed, and how much the packages run on their own — which is why the
 * crontab and the timers are polled here and handed to their panels rather
 * than fetched inside them. A job, a timer and a system cron line are each
 * drawn as the product they run (§14): the program a command starts, the
 * unit a timer activates.
 */
export default function ScheduledPage() {
  const { confirm, dialog } = useConfirm()
  const [user, setUser] = useViewState("processes.cron.user", "root")
  const [adding, setAdding] = useSessionState("processes.cron.adding", false)
  const users = usePoll((signal) => get<string[]>("/cron/users", undefined, signal), 0)
  const crontab = usePoll(
    (signal) => get<Crontab>(`/cron/user/${encodeURIComponent(user)}`, undefined, signal),
    30_000,
    [user],
  )
  const timers = usePoll(
    (signal) => get<TimerList>("/systemd/timers", undefined, signal),
    30_000,
  )
  const system = usePoll((signal) => get<Crontab[]>("/cron/system", undefined, signal), 0)

  const jobs = useMemo(() => crontab.data?.jobs ?? [], [crontab.data])
  const timerList = useMemo(() => timers.data?.timers ?? [], [timers.data])
  const systemJobs = useMemo(
    () => (system.data ?? []).reduce((n, file) => n + file.jobs.length, 0),
    [system.data],
  )
  const disabled = jobs.filter((job) => job.disabled).length
  const armed = timerList.filter((timer) => timer.activeState === "active").length
  const onBoot = timerList.filter((timer) => timer.enabled).length

  // The soonest thing on the clock, wherever it is declared: a cron job's
  // next run is computed here from its expression, a timer's is what systemd
  // reports.
  const next = useMemo(() => {
    const candidates: { at: Date; what: string }[] = []
    for (const job of jobs) {
      if (job.disabled) continue
      const at = nextCronRun(job.schedule)
      if (at) candidates.push({ at, what: job.command })
    }
    for (const timer of timerList) {
      if (timer.next) candidates.push({ at: new Date(timer.next), what: timer.activates })
    }
    candidates.sort((a, b) => a.at.getTime() - b.at.getTime())
    return candidates[0]
  }, [jobs, timerList])

  const settled = Boolean(crontab.data && timers.data)

  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Processes" title="Scheduled" />

      {settled && (
        <StatGrid columns={4} key="figures" className="animate-rise">
          <StatTile
            label="Next run"
            value={next ? relativeTime(next.at.toISOString()) : "Nothing"}
            hint={
              next
                ? `${next.what} · ${timestamp(next.at.toISOString())}`
                : "no job or timer is due"
            }
          />
          <StatTile
            label="Cron jobs"
            value={jobs.length - disabled}
            hint={
              jobs.length === 0
                ? `none for ${user}`
                : disabled > 0
                  ? `${disabled} disabled · ${user}`
                  : `all of ${user}'s jobs enabled`
            }
          />
          <StatTile
            label="Timers armed"
            value={armed}
            tone={timerList.length > 0 && armed === 0 ? "warning" : "default"}
            hint={
              timers.data?.available === false
                ? "systemd is not available"
                : `of ${timerList.length} · ${onBoot} enabled on boot`
            }
          />
          <StatTile
            label="System cron"
            value={systemJobs}
            hint={
              system.data
                ? `${system.data.length} ${system.data.length === 1 ? "file" : "files"} owned by packages`
                : "reading /etc/cron.d"
            }
          />
        </StatGrid>
      )}

      <CronJobsPanel
        user={user}
        users={users.data ?? []}
        crontab={crontab}
        onUserChange={setUser}
        confirm={confirm}
        adding={adding}
        onAddingChange={setAdding}
      />

      <TimersPanel timers={timers} confirm={confirm} />

      <Panel>
        <PanelHeader
          title="System cron files"
          advanced
          actions={
            system.data &&
            system.data.length > 0 && (
              <span className="numeric text-hint text-muted-foreground">{system.data.length}</span>
            )
          }
        />
        <PanelBody className="space-y-6">
          {system.data?.map((file) => (
            <section key={file.source} className="min-w-0 space-y-2">
              <p className="eyebrow">
                <Link
                  href={`/files?path=${encodeURIComponent(file.source)}`}
                  className="font-mono tracking-normal normal-case hover:underline"
                >
                  {file.source}
                </Link>
              </p>
              {file.jobs.length === 0 ? (
                <EmptyNote className="py-2 text-left">No jobs.</EmptyNote>
              ) : (
                <Table containerClassName="group-data-[plain]/panel:-mx-4 w-auto">
                  <TableHeader>
                    <TableRow>
                      <TableHead className="w-52">Schedule</TableHead>
                      <TableHead className="w-24">User</TableHead>
                      <TableHead className="w-full">Command</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {file.jobs.map((job, i) => (
                      <TableRow
                        key={i}
                        // A commented-out schedule is real syntax, not a
                        // running job — /etc/crontab ships one as its own
                        // worked example. Dimming it keeps the two apart.
                        className={cn(job.disabled && "opacity-55")}
                      >
                        <TableCell className="whitespace-normal">
                          <div className="min-w-44">
                            <p className="font-mono whitespace-nowrap">
                              {job.disabled && (
                                <span className="mr-1 text-muted-foreground">#</span>
                              )}
                              {job.schedule}
                            </p>
                            <p className="text-hint text-muted-foreground">
                              {describeCron(job.schedule)}
                            </p>
                          </div>
                        </TableCell>
                        <TableCell className="text-muted-foreground">{job.user || "—"}</TableCell>
                        <TableCell className="whitespace-normal">
                          <CommandCell command={job.command} comment={job.comment} />
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              )}
            </section>
          ))}
          {system.data && system.data.length === 0 && <EmptyNote>No system cron files.</EmptyNote>}
        </PanelBody>
      </Panel>
      {dialog}
    </Page>
  )
}
