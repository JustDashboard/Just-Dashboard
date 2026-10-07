"use client"

import { Suspense, useEffect, useMemo } from "react"
import Link from "next/link"
import { Copy } from "@/components/icons"
import { get } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { describeCron } from "@/lib/cron"
import { plural } from "@/lib/format"
import { scheduleLanes, type ScheduleLane } from "@/lib/schedule"
import type { Crontab, LogSourceIndex } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useMetrics } from "@/hooks/use-metrics"
import { usePoll } from "@/hooks/use-poll"
import { useQuerySelection } from "@/hooks/use-query-selection"
import { useSessionState, useViewState } from "@/lib/view-state"
import { useConfirm } from "@/components/confirm-dialog"
import { useNow } from "@/components/deploy/vocabulary"
import { ServiceLogs } from "@/components/logs/service-logs"
import { FactDot, HostFact, HostIdentity, platformName } from "@/components/metrics/host-identity"
import { Page, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { platformProduct } from "@/components/product-logo"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { CronJobSheet } from "@/components/procs/cron-job-sheet"
import { CommandCell, CronJobsPanel } from "@/components/procs/cron-jobs"
import { BAND_WINDOW, NextUp, ScheduleBand } from "@/components/procs/schedule-band"
import { TimerSheet } from "@/components/procs/timer-sheet"
import { TimersPanel, type TimerList } from "@/components/procs/timers"
import { cronLogSource } from "@/components/procs/shared"

/** How soon a log index that failed to answer is asked again. */
const INDEX_RETRY = 30_000

// The open job and timer live in the query string, which the App Router only
// hands out inside a Suspense boundary.
export default function ScheduledPage() {
  return (
    <Suspense>
      <Scheduled />
    </Suspense>
  )
}

/**
 * Everything that runs on a clock: one account's crontab as jobs you can
 * act on, the host's systemd timers, and the package-owned cron files,
 * which are read-only here because editing them belongs to the package
 * manager.
 *
 * It opens on the machine's identity line, with what fires next across all
 * three counting down to the second at its end, and then on the next day of
 * all three as one picture (`ScheduleBand`): a lane per schedule on an axis
 * that starts now, so a night of jobs bunched at three and a health check
 * every fifteen minutes are seen before a row is read. The five tiles that
 * stood here took §15 pass 2's exit, each to where it is said better: Next
 * run is the countdown and the band's breathing dot; Cron jobs (and how many
 * are disabled) are the counted chips in the Cron jobs head and a fact in the
 * identity line; Cron runs is the cron log's chips, which count runs and lost
 * output from the same lens readings and narrow the log to them; Timers armed
 * is the timers' head, armed, running and stopped as chips with how many
 * start on boot beside them; System cron is a fact and its section's count.
 *
 * The crontab and the timers are polled here rather than in their panels,
 * because the band and the countdown are made of both. A job, a timer and a
 * system cron line are each drawn as the product they run (§14), and each
 * opens a sheet: a job's week and the runs cron logged for it, a timer's
 * calendar in words and the runs of the service it fires. Both are in the
 * address (`?job=`, `?timer=`), so a sheet can be linked and Back closes it.
 */
function Scheduled() {
  const { confirm, dialog } = useConfirm()
  const { host } = useMetrics()
  const [user, setUser] = useViewState("processes.cron.user", "root")
  const [adding, setAdding] = useSessionState("processes.cron.adding", false)
  const [job, selectJob] = useQuerySelection("job")
  const [timer, selectTimer] = useQuerySelection("timer")
  const users = usePoll((signal) => get<string[]>("/cron/users", undefined, signal), 0)
  const crontab = usePoll(
    (signal) => get<Crontab>(`/cron/user/${encodeURIComponent(user)}`, undefined, signal),
    30_000,
    [user],
  )
  const timers = usePoll((signal) => get<TimerList>("/systemd/timers", undefined, signal), 30_000)
  const system = usePoll((signal) => get<Crontab[]>("/cron/system", undefined, signal), 0)
  // Where cron's log is on this host is a question the log index answers
  // once: the daemon's unit, a cron file, or the journal by program. A read
  // that failed is asked again, or the panel below would say so until the
  // page was reloaded.
  const logs = usePoll((signal) => get<LogSourceIndex>("/logs/sources", undefined, signal), 0)
  useEffect(() => {
    if (!logs.error) return
    const retry = setTimeout(logs.refresh, INDEX_RETRY)
    return () => clearTimeout(retry)
  }, [logs.error, logs.refresh])
  const cron = useMemo(() => cronLogSource(logs.data), [logs.data])
  const cronSources = useMemo(() => (cron ? [cron] : []), [cron])

  const jobs = useMemo(() => crontab.data?.jobs ?? [], [crontab.data])
  const timerList = useMemo(() => timers.data?.timers ?? [], [timers.data])
  const systemJobs = useMemo(
    () => (system.data ?? []).reduce((n, file) => n + file.jobs.length, 0),
    [system.data],
  )
  const disabled = jobs.filter((job) => job.disabled).length
  const armed = timerList.filter((timer) => timer.activeState === "active").length

  // The band is drawn from this minute: an expression's runs are computed
  // once a minute, and every countdown ticks from them in between.
  const now = useNow(60_000)
  const minute = Math.floor(now / 60_000) * 60_000
  const lanes = useMemo(
    () =>
      scheduleLanes({
        crontab: crontab.data,
        timers: timers.data?.timers,
        system: system.data,
        from: minute,
        until: minute + BAND_WINDOW,
      }),
    [crontab.data, timers.data, system.data, minute],
  )
  const settled = Boolean(crontab.data && timers.data)

  const open = (lane: ScheduleLane) => {
    if (lane.kind === "timer") selectTimer(lane.timer!.unit)
    else selectJob(lane.key)
  }
  const systemOpen = job?.startsWith("system:")
    ? (system.data ?? [])
        .flatMap((file) => file.jobs.map((line) => ({ file, line })))
        .find(({ file, line }) => `system:${file.source}:${line.line}` === job)
    : undefined

  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Processes" title="Scheduled" />

      {host && (
        <HostIdentity
          mark={platformProduct(host.platform)}
          title={host.hostname}
          facts={
            <>
              <HostFact product={platformProduct(host.platform)}>{platformName(host)}</HostFact>
              {crontab.data && (
                <>
                  <FactDot />
                  <span className="numeric">
                    {plural(jobs.length - disabled, "cron job")} for {user}
                    {disabled > 0 && `, ${disabled} disabled`}
                  </span>
                </>
              )}
              {timers.data?.available && (
                <>
                  <FactDot />
                  <span className="numeric">
                    {armed} of {plural(timerList.length, "timer")} armed
                  </span>
                </>
              )}
              {system.data && (
                <>
                  <FactDot />
                  <span className="numeric">{plural(systemJobs, "package cron line")}</span>
                </>
              )}
            </>
          }
          aside={settled && <NextUp lanes={lanes} />}
        />
      )}

      {settled && <ScheduleBand lanes={lanes} onOpen={open} />}

      <CronJobsPanel
        user={user}
        users={users.data ?? []}
        crontab={crontab}
        onUserChange={setUser}
        confirm={confirm}
        adding={adding}
        onAddingChange={setAdding}
        open={job}
        onOpen={selectJob}
        cron={cron}
      />

      <TimersPanel timers={timers} confirm={confirm} onOpen={selectTimer} />

      <Panel plain>
        <PanelHeader title="Cron log" />
        <PanelBody flush className="pt-3">
          {logs.error && !logs.data ? (
            <ErrorState error={logs.error} />
          ) : !logs.data ? (
            <LoadingRows rows={4} />
          ) : cron ? (
            <ServiceLogs
              sources={cronSources}
              storageKey="processes.cron.log"
              readings="chips"
              paneClassName="h-[min(70vh,36rem)] min-h-80"
            />
          ) : (
            // Only a host without the journal gets here: on one with it,
            // cron's lines are read out of it by program even with no unit.
            <EmptyNote className="px-0 text-left">
              {logs.data.missing?.journal ?? "There is no journal to read"}, and there is no cron
              log under {(logs.data.roots ?? []).join(", ") || "the log roots"} — so what cron ran
              is not recorded anywhere this page can read.
            </EmptyNote>
          )}
        </PanelBody>
      </Panel>

      <Panel>
        <PanelHeader
          title="System cron files"
          advanced
          actions={
            system.data &&
            system.data.length > 0 && (
              <span className="numeric text-hint text-muted-foreground">
                {plural(systemJobs, "line")} in {plural(system.data.length, "file")}
              </span>
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
                    {file.jobs.map((line) => {
                      const key = `system:${file.source}:${line.line}`
                      return (
                        <TableRow
                          key={line.line}
                          onActivate={() => selectJob(key)}
                          // A commented-out schedule is real syntax, not a
                          // running job — /etc/crontab ships one as its own
                          // worked example. Dimming it keeps the two apart.
                          className={cn(line.disabled && "opacity-55")}
                        >
                          <TableCell className="whitespace-normal">
                            <div className="min-w-44">
                              <p className="font-mono whitespace-nowrap">
                                {line.disabled && (
                                  <span className="mr-1 text-muted-foreground">#</span>
                                )}
                                {line.schedule}
                              </p>
                              <p className="text-hint text-muted-foreground">
                                {describeCron(line.schedule)}
                              </p>
                            </div>
                          </TableCell>
                          <TableCell className="text-muted-foreground">
                            {line.user || "—"}
                          </TableCell>
                          <TableCell className="whitespace-normal">
                            <CommandCell
                              command={line.command}
                              comment={line.comment}
                              onOpen={() => selectJob(key)}
                            />
                          </TableCell>
                        </TableRow>
                      )
                    })}
                  </TableBody>
                </Table>
              )}
            </section>
          ))}
          {system.data && system.data.length === 0 && <EmptyNote>No system cron files.</EmptyNote>}
        </PanelBody>
      </Panel>

      <TimerSheet
        unit={timer}
        timer={timerList.find((t) => t.unit === timer)}
        confirm={confirm}
        onChanged={timers.refresh}
        onOpenChange={(next) => !next && selectTimer(null)}
      />
      <CronJobSheet
        job={systemOpen?.line}
        kind="system"
        owner={systemOpen?.file.source ?? ""}
        source={systemOpen?.file.source}
        verbs={
          systemOpen
            ? [
                {
                  key: "copy",
                  label: "Copy command",
                  icon: Copy,
                  inline: true,
                  run: () => void copyText(systemOpen.line.command, "Command copied"),
                },
              ]
            : []
        }
        cron={cron}
        onOpenChange={(next) => !next && selectJob(null)}
      />
      {dialog}
    </Page>
  )
}
