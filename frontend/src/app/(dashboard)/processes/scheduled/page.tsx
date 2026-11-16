"use client"

import { Suspense, useEffect, useMemo } from "react"
import { useRouter, useSearchParams } from "next/navigation"
import { Plus } from "@/components/icons"
import { plural } from "@/lib/format"
import { isExampleLine, type ScheduleLane } from "@/lib/schedule"
import { useMetrics } from "@/hooks/use-metrics"
import { useAuth } from "@/hooks/use-auth"
import { useSessionState } from "@/lib/view-state"
import { useConfirm } from "@/components/confirm-dialog"
import { EmbeddedLog } from "@/components/logs/embedded-log"
import { Fact } from "@/components/fact-line"
import { HostIdentity, platformName } from "@/components/identity-header"
import { Page, PageContext } from "@/components/page"
import { platformProduct } from "@/components/product-logo"
import { EmptyNote } from "@/components/state"
import { Button } from "@/components/ui/button"
import { CronJobsPanel } from "@/components/procs/cron-jobs"
import { NextUp, ScheduleBand } from "@/components/procs/schedule-band"
import { useSchedules } from "@/components/procs/schedule-data"
import { laneHref, sheetAddress } from "@/components/procs/schedule-links"
import { SystemCronPanel } from "@/components/procs/system-cron"
import { TimersPanel } from "@/components/procs/timers"

// The old sheets' addresses (`?job=`, `?timer=`) are read from the query
// string, which the App Router only hands out inside a Suspense boundary.
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
 * The crontab and the timers are polled here rather than in their panels
 * (`useSchedules`), because the band and the next run are made of both. A
 * job, a timer and a system cron line are each drawn as the product they run
 * (§14), and each opens a page of its own (`schedule-page.tsx`): a job's week
 * and the runs cron logged for it, a timer's calendar in words and the runs
 * of the service it fires, with every schedule beside it. The sheets' old
 * addresses (`?job=`, `?timer=`) are sent on to those pages.
 */
function Scheduled() {
  const { confirm, dialog } = useConfirm()
  const { host } = useMetrics()
  const { can } = useAuth()
  const router = useRouter()
  const search = useSearchParams()
  const [adding, setAdding] = useSessionState("processes.cron.adding", false)
  const schedules = useSchedules()
  const { user, setUser, users, crontab, timers, system, logs, cron, zone, systemd, lanes } =
    schedules
  const cronSources = useMemo(() => (cron ? [cron] : []), [cron])

  // A link to one of the old sheets opens the page that replaced it.
  const old = sheetAddress({ job: search.get("job"), timer: search.get("timer") }, user)
  useEffect(() => {
    if (old) router.replace(old)
  }, [old, router])

  const jobs = useMemo(() => crontab.data?.jobs ?? [], [crontab.data])
  const timerList = useMemo(() => timers.data?.timers ?? [], [timers.data])
  const systemJobs = useMemo(
    () =>
      (system.data ?? []).reduce(
        (n, file) => n + file.jobs.filter((job) => !isExampleLine(job)).length,
        0,
      ),
    [system.data],
  )
  const disabled = jobs.filter((job) => job.disabled).length
  const armed = timerList.filter((timer) => timer.activeState === "active").length
  // Every time on the page is on the host's clock, which is the one its
  // schedules are written in: "every day at 03:00" and a next run of 22:00
  // for the same line, because the browser was five hours behind the server,
  // was the first thing that made the page unreadable. The zone is said in
  // the identity line when it is not the reader's.
  const browserZone = useMemo(() => Intl.DateTimeFormat().resolvedOptions().timeZone, [])
  const settled = schedules.settled

  const open = (lane: ScheduleLane) => router.push(laneHref(lane, user))
  const openKey = (key: string) => {
    const lane = lanes.find((one) => one.key === key)
    if (lane) open(lane)
  }

  return (
    <Page rise>
      <PageContext title="Scheduled" />

      {host && (
        <HostIdentity
          mark={platformProduct(host.platform)}
          title={host.hostname}
          state={settled ? <NextUp lanes={lanes} zone={zone} /> : true}
          loading={!settled}
          facts={
            <>
              <Fact product={platformProduct(host.platform)}>{platformName(host)}</Fact>
              {crontab.data && (
                <Fact className="numeric">
                  {plural(jobs.length - disabled, "cron job")} for {user}
                  {disabled > 0 && `, ${disabled} disabled`}
                </Fact>
              )}
              {timers.data?.available && (
                <Fact className="numeric">
                  {armed} of {plural(timerList.length, "timer")} armed
                </Fact>
              )}
              {system.data && (
                <Fact className="numeric">{plural(systemJobs, "system cron line")}</Fact>
              )}
              {zone && zone !== browserZone && (
                <Fact
                  title={`Times on this page are the server's (${zone}), not this browser's (${browserZone})`}
                >
                  times in {zone}
                </Fact>
              )}
            </>
          }
          command={
            can("system.admin") ? (
              <Button size="sm" disabled={!crontab.data?.revision} onClick={() => setAdding(true)}>
                <Plus className="size-3.5" />
                Add job
              </Button>
            ) : undefined
          }
        />
      )}

      <ScheduleBand lanes={lanes} zone={zone} settled={settled} onOpen={open} />

      <CronJobsPanel
        user={user}
        users={users.data ?? []}
        crontab={crontab}
        onUserChange={setUser}
        confirm={confirm}
        adding={adding}
        onAddingChange={setAdding}
        onOpen={openKey}
        zone={zone}
      />

      <TimersPanel
        timers={timers}
        confirm={confirm}
        onOpen={(unit) => openKey(`timer:${unit}`)}
        zone={zone}
      />

      <SystemCronPanel
        system={system}
        zone={zone}
        systemd={systemd}
        timers={timerList}
        onOpen={openKey}
      />

      <EmbeddedLog
        title="Cron log"
        sources={logs.data ? cronSources : undefined}
        error={logs.error && !logs.data ? logs.error : undefined}
        onRetry={logs.refresh}
        storageKey="processes.cron.log"
        readings="chips"
        instead={
          logs.data &&
          !cron && (
            // Only a host without the journal gets here: on one with it,
            // cron's lines are read out of it by program even with no unit.
            <EmptyNote className="px-0 text-left">
              {logs.data.missing?.journal ?? "There is no journal to read"}, and there is no cron
              log under {(logs.data.roots ?? []).join(", ") || "the log roots"} — so what cron ran
              is not recorded anywhere this page can read.
            </EmptyNote>
          )
        }
      />

      {dialog}
    </Page>
  )
}
