"use client"

import { useCallback, useState } from "react"
import Link from "next/link"
import { useParams, useRouter } from "next/navigation"
import { Clock, Copy, Stopwatch } from "@/components/icons"
import { copyText } from "@/lib/clipboard"
import { replaceJobAt } from "@/lib/crontab"
import { countdown, deferredToSystemd, timerFor, type ScheduleLane } from "@/lib/schedule"
import type { CronJob, SystemdTimer } from "@/lib/types"
import { decodeSegment } from "@/lib/route-params"
import { forgetMemoryState } from "@/lib/view-state"
import { useAuth } from "@/hooks/use-auth"
import { useNow } from "@/hooks/use-now"
import { useConfirm } from "@/components/confirm-dialog"
import { DetailPage, useDetailTab, useSiblingSteps, type Step } from "@/components/detail-page"
import { Fact } from "@/components/fact-line"
import { IdentityHeader, IdentityVerbs } from "@/components/identity-header"
import { ProductLogo, programProduct, unitProduct } from "@/components/product-logo"
import { EmptyState, ErrorState } from "@/components/state"
import { LoadingRows } from "@/components/loading"
import { Status } from "@/components/status-dot"
import { ViewTabList, type TabView } from "@/components/tabs"
import { Tabs, TabsContent } from "@/components/ui/tabs"
import { TextShimmer } from "@/components/ui/text-shimmer"
import type { Verb } from "@/components/verbs"
import { CronJobOverview, CronJobRuns } from "@/components/procs/cron-job-parts"
import {
  CronJobEditor,
  cronJobActions,
  cronJobVerbs,
  useCrontabSave,
} from "@/components/procs/cron-jobs"
import { useSchedules } from "@/components/procs/schedule-data"
import {
  SCHEDULE_TABS,
  cronJobHref,
  laneHref,
  parseCronJobId,
  type ScheduleTab,
} from "@/components/procs/schedule-links"
import { ScheduleRail } from "@/components/procs/schedule-rail"
import { TimerOverview, TimerRuns } from "@/components/procs/timer-parts"
import { TimerState, useTimerVerbs } from "@/components/procs/timers"
import { useUnitControl } from "@/components/procs/unit-actions"

const BACK = { href: "/processes/scheduled", label: "Scheduled" }

const VIEWS: TabView<ScheduleTab>[] = [
  { key: "overview", label: "Overview" },
  { key: "runs", label: "Runs" },
]

/**
 * One schedule, as a place of its own: a cron line (an account's or a package
 * file's) or a systemd timer. Each was a read-only sheet over the Scheduled
 * page until 0.9.0, while the section's other subjects (a service, a PM2 app,
 * a process) were pages; a schedule is one now, on the shared detail frame
 * (`DetailPage`), with its views in `?tab=` (the schedule and what it runs,
 * then the runs it left in a log) and from `xl` every schedule beside it,
 * soonest first, so the next one is a press and Alt+↑ and Alt+↓ walk them.
 * Adding and editing a cron line stay a sheet: a small form.
 */
function useScheduleFrame(current: string | null, user?: string) {
  const router = useRouter()
  const data = useSchedules(user)
  const [tab, setTab] = useDetailTab(SCHEDULE_TABS, { remember: "processes.scheduled.tab" })
  const open = useCallback(
    (lane: ScheduleLane) => router.push(laneHref(lane, data.user, tab)),
    [router, data.user, tab],
  )
  const lane = data.lanes.find((one) => one.key === current)
  const steps = useSiblingSteps(data.lanes, lane, open)
  const failed = [data.crontab, data.timers, data.system].find((read) => read.error && !read.data)
  return {
    data,
    tab,
    setTab,
    lane,
    steps,
    rail: (
      <ScheduleRail
        lanes={data.lanes}
        user={data.user}
        current={current}
        loading={!data.settled}
        error={data.lanes.length === 0 ? failed?.error : undefined}
        onRetry={() => {
          data.crontab.refresh()
          data.timers.refresh()
          data.system.refresh()
        }}
      />
    ),
  }
}

function ScheduleFrame({
  frame,
  name,
  title,
  identity,
  children,
}: {
  frame: { steps: { previous: Step; next: Step }; rail: React.ReactNode; refresh: () => void }
  name: string
  title: string
  identity?: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <DetailPage
      name={name}
      what="schedule"
      stateKey="processes.scheduled.page"
      refresh={frame.refresh}
      steps={frame.steps}
      back={BACK}
      title={title}
      rail={frame.rail}
      identity={identity}
    >
      {children}
    </DetailPage>
  )
}

/** The line a schedule's page opens on before its schedule is read: its final shape (D11). */
function ReadingLine({ what }: { what: string }) {
  return (
    <IdentityHeader
      loading
      className="border-b-0 pb-0"
      fallback={Clock}
      title={<TextShimmer>{`Reading ${what}…`}</TextShimmer>}
      state
      command
      menu={false}
    />
  )
}

/** The next run as the line's last fact, counting down. */
function NextFact({ at, quiet }: { at?: number; quiet?: string }) {
  const now = useNow(1000)
  if (quiet) return <Fact>{quiet}</Fact>
  if (at === undefined) return <Fact>nothing scheduled</Fact>
  return <Fact className="numeric text-foreground">next in {countdown(Math.max(0, at - now))}</Fact>
}

/** A cron line's page: `/processes/scheduled/cron/<user>:<line>` or `…/system:<file>:<line>`. */
export function CronJobPage() {
  const params = useParams<{ id: string }>()
  const id = decodeSegment(params.id)
  const parsed = parseCronJobId(id)
  const frame = useScheduleFrame(
    parsed?.key ?? null,
    parsed?.kind === "cron" ? parsed.user : undefined,
  )
  const { data, tab, setTab, lane } = frame
  const { can } = useAuth()
  const admin = can("system.admin")
  const { confirm, dialog } = useConfirm()
  const router = useRouter()
  const [editing, setEditing] = useState<CronJob | null>(null)
  const { save } = useCrontabSave(data.user, data.crontab)
  const refresh = () => {
    data.crontab.refresh()
    data.system.refresh()
  }
  const shell = { ...frame, refresh }

  const system =
    parsed?.kind === "system"
      ? (data.system.data ?? [])
          .flatMap((file) => file.jobs.map((line) => ({ file, line })))
          .find(({ file, line }) => `system:${file.source}:${line.line}` === parsed.key)
      : undefined
  const job =
    parsed?.kind === "system"
      ? system?.line
      : data.crontab.data?.jobs.find((one) => `cron:${one.line}` === parsed?.key)
  const read = parsed?.kind === "system" ? data.system : data.crontab
  const timers = data.timers.data?.timers ?? []
  const skipped = Boolean(system && data.systemd && deferredToSystemd(system.line.command))
  const instead = system ? timerFor(system.line.command, timers) : undefined
  const owner = system ? system.file.source : `${data.user}'s crontab`

  if (!parsed || (read.data && !job)) {
    return (
      <ScheduleFrame frame={shell} name="Cron job" title="Cron job">
        <EmptyState
          icon={Clock}
          title="No cron line by that name on this host"
          description="It may have been removed or moved to another line. Pick another from the list."
          action={
            <Link
              href="/processes/scheduled"
              className="text-body font-medium text-brand hover:underline"
            >
              Back to the schedules
            </Link>
          }
        />
      </ScheduleFrame>
    )
  }
  if (!job) {
    return (
      <ScheduleFrame
        frame={shell}
        name="Cron job"
        title="Cron job"
        identity={read.error ? undefined : <ReadingLine what="the cron line" />}
      >
        {read.error ? <ErrorState error={read.error} onRetry={read.refresh} /> : <LoadingRows />}
      </ScheduleFrame>
    )
  }

  const name = lane?.name ?? job.command
  const verbs: Verb[] = system
    ? [
        {
          key: "copy",
          label: "Copy command",
          icon: Copy,
          inline: true,
          run: () => void copyText(system.line.command, "Command copied"),
        },
      ]
    : cronJobVerbs({
        job,
        admin,
        confirm,
        ...cronJobActions({
          raw: data.crontab.data?.raw ?? "",
          job,
          save,
          onEdit: () => setEditing(job),
          onRemoved: () => router.push("/processes/scheduled"),
        }),
      })
  const quiet = job.disabled ? "switched off" : skipped ? "left to systemd" : undefined

  return (
    <ScheduleFrame
      frame={shell}
      name="Cron job"
      title={name}
      identity={
        <IdentityHeader
          className="animate-rise border-b-0 pb-0"
          logo={
            <ProductLogo
              id={programProduct(lane?.product)}
              fallback={Clock}
              className="size-10 rounded-lg [&_img]:size-6"
            />
          }
          title={<span className="truncate">{name}</span>}
          state={
            <Status
              state={quiet ? "inactive" : "active"}
              label={job.disabled ? "switched off" : skipped ? "left to systemd" : "enabled"}
            />
          }
          facts={
            <>
              <Fact className="text-foreground">{lane?.when}</Fact>
              {job.user && <Fact mono>{job.user}</Fact>}
              <Fact mono={Boolean(system)}>{owner}</Fact>
              <NextFact at={lane?.next} quiet={quiet} />
            </>
          }
          {...IdentityVerbs({ verbs, name })}
        />
      }
    >
      <Tabs value={tab} onValueChange={setTab} className="gap-0">
        <ViewTabList label={`Views of ${name}`} views={VIEWS} value={tab} sticky />
        <TabsContent value="overview" className="pt-6">
          <CronJobOverview
            key={`${job.line}:${job.raw}`}
            job={job}
            owner={owner}
            source={system?.file.source}
            kind={system ? "system" : "cron"}
            zone={data.zone}
            skipped={skipped}
            instead={instead}
          />
        </TabsContent>
        <TabsContent value="runs" className="flex flex-col pt-4">
          {tab === "runs" && <CronJobRuns job={job} cron={data.cron} />}
        </TabsContent>
      </Tabs>
      {!system && (
        <CronJobEditor
          open={editing !== null && Boolean(data.crontab.data?.revision)}
          job={editing}
          user={data.user}
          baseline={{ raw: data.crontab.data?.raw ?? "", revision: data.crontab.data?.revision }}
          timeZone={editing?.timeZone ?? data.crontab.data?.timeZone}
          onClose={() => {
            setEditing(null)
            forgetMemoryState(`processes.cron.job.${data.user}.`)
          }}
          onSave={async (edit, baseline) => {
            if (!editing) return
            const saved = replaceJobAt(baseline.raw, editing, edit)
            await save(saved.raw, "Cron job updated", undefined, baseline.revision)
            // The address names the line; a comment added or removed moves the job.
            if (saved.line !== editing.line) {
              router.replace(cronJobHref(`${data.user}:${saved.line}`, tab))
            }
          }}
        />
      )}
      {dialog}
    </ScheduleFrame>
  )
}

/** A timer's page: `/processes/scheduled/timers/<unit>`. */
export function TimerPage() {
  const params = useParams<{ unit: string }>()
  const unit = decodeSegment(params.unit)
  const frame = useScheduleFrame(`timer:${unit}`)
  const { data, tab, setTab, lane } = frame
  const shell = { ...frame, refresh: data.timers.refresh }
  const timer = data.timers.data?.timers.find((one) => one.unit === unit)
  const { confirm, dialog } = useConfirm()
  const control = useUnitControl(data.timers.refresh)

  if (data.timers.data && !timer) {
    return (
      <ScheduleFrame frame={shell} name="Timer" title="Timer">
        <EmptyState
          icon={Stopwatch}
          title={`No timer named ${unit || "that"} on this host`}
          description="It may have been removed, or the name in the link is misspelt. Pick another from the list."
          action={
            <Link
              href="/processes/scheduled"
              className="text-body font-medium text-brand hover:underline"
            >
              Back to the schedules
            </Link>
          }
        />
      </ScheduleFrame>
    )
  }
  if (!timer) {
    return (
      <ScheduleFrame
        frame={shell}
        name="Timer"
        title={unit}
        identity={data.timers.error ? undefined : <ReadingLine what={unit} />}
      >
        {data.timers.error ? (
          <ErrorState error={data.timers.error} onRetry={data.timers.refresh} />
        ) : (
          <LoadingRows />
        )}
      </ScheduleFrame>
    )
  }
  return (
    <ScheduleFrame
      frame={shell}
      name="Timer"
      title={unit}
      identity={
        <TimerLine
          timer={timer}
          lane={lane}
          busy={control.pending[timer.unit] ?? control.pending[timer.activates]}
          confirm={confirm}
          act={control.act}
          onChanged={data.timers.refresh}
        />
      }
    >
      <Tabs value={tab} onValueChange={setTab} className="gap-0">
        <ViewTabList label={`Views of ${timer.unit}`} views={VIEWS} value={tab} sticky />
        <TabsContent value="overview" className="pt-6">
          <TimerOverview key={timer.unit} timer={timer} zone={data.zone} />
        </TabsContent>
        <TabsContent value="runs" className="flex flex-col pt-4">
          {tab === "runs" &&
            (timer.activates ? (
              <TimerRuns unit={timer.activates} />
            ) : (
              <p className="text-body text-muted-foreground">
                {timer.unit} starts nothing, so it has no runs.
              </p>
            ))}
        </TabsContent>
      </Tabs>
      {dialog}
    </ScheduleFrame>
  )
}

/**
 * A timer's line: the unit as what it runs, armed or stopped, its calendar in
 * words, the service it starts and when it fires next, and its verbs: Run now
 * as the command, arming and boot behind `⋯`.
 */
function TimerLine({
  timer,
  lane,
  busy,
  confirm,
  act,
  onChanged,
}: {
  timer: SystemdTimer
  lane?: ScheduleLane
  busy?: string
  confirm: Parameters<typeof useTimerVerbs>[0]["confirm"]
  act: Parameters<typeof useTimerVerbs>[0]["act"]
  onChanged: () => void
}) {
  const verbs = useTimerVerbs({ timer, confirm, act, onChanged })
  const name = timer.unit.replace(/\.timer$/, "")
  const stopped = timer.activeState !== "active"
  return (
    <IdentityHeader
      className="animate-rise border-b-0 pb-0"
      logo={
        <ProductLogo
          id={unitProduct(timer.unit)}
          fallback={Stopwatch}
          className="size-10 rounded-lg [&_img]:size-6"
        />
      }
      title={<span className="truncate">{name}</span>}
      state={<TimerState timer={timer} busy={busy} />}
      facts={
        <>
          {lane?.when && <Fact className="text-foreground">{lane.when}</Fact>}
          {timer.activates && (
            <Fact>
              runs{" "}
              <Link
                href={`/processes/services/${encodeURIComponent(timer.activates)}`}
                className="font-mono hover:underline"
              >
                {timer.activates}
              </Link>
            </Fact>
          )}
          <NextFact at={lane?.next} quiet={stopped ? "stopped" : undefined} />
        </>
      }
      {...IdentityVerbs({ verbs, name: timer.unit })}
    />
  )
}
