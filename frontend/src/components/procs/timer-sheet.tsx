"use client"

import { useMemo, useState } from "react"
import Link from "next/link"
import { Copy, Stopwatch } from "@/components/icons"
import { get } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { duration, relativeTime } from "@/lib/format"
import { journalSource } from "@/lib/log-sources"
import {
  calendarToCron,
  countdown,
  describeTimer,
  execCommand,
  longClock,
  randomDelay,
  spanMs,
  spanWords,
  timerRuns,
  timerTriggers,
} from "@/lib/schedule"
import { cn } from "@/lib/utils"
import type { SystemdTimer, SystemdUnit } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { ShellWords } from "@/components/deploy/run-evidence"
import { useNow } from "@/components/deploy/vocabulary"
import { IconAction } from "@/components/icon-action"
import { ServiceLogs, type ServiceLogSource } from "@/components/logs/service-logs"
import { Detail, DetailList } from "@/components/page"
import { Well } from "@/components/panel"
import { ProductLogo, unitProduct } from "@/components/product-logo"
import { SidePanel } from "@/components/side-panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { LoadingRows, Notice } from "@/components/state"
import { VerbBar } from "@/components/verbs"
import { TimerState, useTimerVerbs } from "@/components/procs/timers"
import { useUnitControl } from "@/components/procs/unit-actions"
import { unitRunsView } from "@/components/procs/unit-runs"
import { KIND } from "@/components/procs/schedule-band"
import {
  ScheduleSummary,
  SheetSection,
  WeekStrip,
  WhenItRuns,
} from "@/components/procs/schedule-sheet"

const DAY = 24 * 3600_000

type ConfirmFn = (request: ConfirmRequest) => void

type UnitShow = { unit: SystemdUnit; properties: Record<string, string> }

/** How a oneshot's last run ended, in systemd's `Result` words. */
const RESULT: Record<string, string> = {
  success: "succeeded",
  "exit-code": "failed",
  signal: "killed",
  "core-dump": "core dumped",
  timeout: "timed out",
  "oom-kill": "killed for memory",
  watchdog: "watchdog timeout",
  "start-limit-hit": "start limit hit",
  resources: "resources unavailable",
}

/** A monotonic microsecond property, or zero for one systemd has not set. */
function micro(props: Record<string, string> | undefined, key: string) {
  const value = Number(props?.[key] ?? 0)
  return Number.isFinite(value) ? value : 0
}

/**
 * One timer, opened from its row or its lane on the band: when it fires next
 * and how the last run ended, as readings; what it fires on, in words, beside
 * the calendar it was written as; what it runs, coloured as a command; and
 * every run of that service, read from its journal.
 *
 * It replaced a disclosure under the timer's row that held only the runs.
 * "Why does certbot run at 06:40 when its calendar says midnight and noon"
 * is the question a timer is opened with more often than any other, and its
 * answer — a random delay of up to twelve hours — was in no place on the page.
 *
 * The timer comes from the page's poll, so the sheet's head and verbs follow
 * the list; the two units' own properties are read here, the service's more
 * often, because "running now" is said from it.
 */
export function TimerSheet({
  timer,
  unit,
  confirm,
  onOpenChange,
  onChanged,
  zone,
}: {
  zone?: string
  timer: SystemdTimer | undefined
  /** The unit asked for, which may not be in the list yet — or any more. */
  unit: string | null
  confirm: ConfirmFn
  onOpenChange: (open: boolean) => void
  onChanged: () => void
}) {
  const { pending, act } = useUnitControl(onChanged)
  const busy = timer ? (pending[timer.unit] ?? pending[timer.activates]) : undefined
  return (
    <SidePanel
      open={unit !== null}
      onOpenChange={onOpenChange}
      title={
        <>
          <ProductLogo id={unitProduct(unit ?? "")} size="sm" fallback={Stopwatch} />
          <span className="min-w-0 truncate">{unit?.replace(/\.timer$/, "")}</span>
        </>
      }
      description={timer ? `Runs ${timer.activates}` : "systemd timer"}
      width="lg"
      initialFocus="body"
      actions={
        timer && (
          <TimerSheetActions
            timer={timer}
            busy={busy}
            confirm={confirm}
            act={act}
            onChanged={onChanged}
          />
        )
      }
      bodyClassName="flex min-h-0 flex-1 flex-col overflow-y-auto"
    >
      {timer ? (
        <TimerDetail key={timer.unit} timer={timer} zone={zone} />
      ) : (
        <LoadingRows rows={4} className="m-5" />
      )}
    </SidePanel>
  )
}

function TimerSheetActions({
  timer,
  busy,
  confirm,
  act,
  onChanged,
}: {
  timer: SystemdTimer
  busy?: string
  confirm: ConfirmFn
  act: ReturnType<typeof useUnitControl>["act"]
  onChanged: () => void
}) {
  const verbs = useTimerVerbs({ timer, confirm, act, onChanged })
  return (
    <>
      <TimerState timer={timer} busy={busy} />
      <VerbBar verbs={verbs} />
    </>
  )
}

function TimerDetail({ timer, zone }: { timer: SystemdTimer; zone?: string }) {
  const now = useNow(1000)
  const own = usePoll(
    (signal) => get<UnitShow>(`/systemd/${encodeURIComponent(timer.unit)}`, undefined, signal),
    30_000,
    [timer.unit],
  )
  const service = usePoll(
    (signal) => get<UnitShow>(`/systemd/${encodeURIComponent(timer.activates)}`, undefined, signal),
    10_000,
    [timer.activates],
    { enabled: Boolean(timer.activates) },
  )
  const props = own.data?.properties
  const run = service.data?.properties
  // The list already carries the triggers on a host whose systemctl answers
  // `show`; the unit's own read is the fallback, and the source of the rest.
  const triggers = props ? timerTriggers(props) : []
  const delay = timer.randomDelay ?? (props ? randomDelay(props) : undefined)
  const sentence =
    describeTimer({
      calendar: timer.calendar ?? triggers.filter((t) => !t.spec.includes("=")).map((t) => t.spec),
      monotonic: timer.monotonic ?? triggers.filter((t) => t.spec.includes("=")).map((t) => t.spec),
      randomDelay: delay,
    }) ?? (props ? "Only when started by hand" : undefined)
  const command = run ? execCommand(run) : undefined
  const calendars =
    timer.calendar ?? triggers.filter((t) => !t.spec.includes("=")).map((t) => t.spec)
  const weekly = calendars.length === 1 ? calendarToCron(calendars[0]) : undefined
  const spread = delay ? spanMs(delay) : undefined

  const minute = Math.floor(now / 60_000) * 60_000
  const runs = useMemo(
    () => timerRuns(timer, minute, minute + 7 * DAY, zone),
    [timer, minute, zone],
  )
  const next = timer.next ? new Date(timer.next).getTime() : undefined
  const started = micro(run, "ExecMainStartTimestampMonotonic")
  const exited = micro(run, "ExecMainExitTimestampMonotonic")
  const took = started > 0 && exited >= started ? (exited - started) / 1e6 : undefined
  const result = run?.Result
  const failed = Boolean(result && result !== "success")
  const status = run?.ExecMainStatus
  const stopped = timer.activeState !== "active"

  return (
    <div className="flex animate-rise flex-col pb-2">
      <ScheduleSummary
        sentence={sentence ?? <span className="text-muted-foreground">Reading the timer…</span>}
        expression={triggers.length > 0 ? triggers.map((t) => t.spec).join(" · ") : undefined}
        facts={[zone && `${zone} time`]}
        notice={
          stopped ? (
            <Notice title="Stopped">
              The timer is not armed, so {timer.activates || "its service"} does not run on this
              schedule until the timer is started.
            </Notice>
          ) : undefined
        }
      />

      <StatGrid columns={3} dense className="border-t border-hairline">
        <StatTile
          className="px-6"
          label="Next run"
          value={
            next !== undefined && next > now ? (
              <span className="numeric">in {countdown(next - now)}</span>
            ) : (
              <span className="text-muted-foreground">—</span>
            )
          }
          hint={
            next !== undefined
              ? longClock(next, zone)
              : stopped
                ? "the timer is stopped"
                : "nothing scheduled"
          }
        />
        <StatTile
          className="px-6"
          label="Last run"
          tone={failed ? "danger" : "default"}
          value={
            timer.last ? (
              <span className="numeric">{relativeTime(timer.last)}</span>
            ) : (
              <span className="text-muted-foreground">never</span>
            )
          }
          hint={
            !run
              ? timer.last
                ? longClock(new Date(timer.last).getTime(), zone)
                : "since the timer was installed"
              : started === 0
                ? "not since the server started"
                : `${RESULT[result ?? ""] ?? result ?? "ended"}${
                    failed && status && status !== "0" ? ` · exit ${status}` : ""
                  }`
          }
        />
        <StatTile
          className="px-6"
          label="Took"
          value={
            took !== undefined ? (
              <span className="numeric">
                {took < 1 ? `${Math.round(took * 1000)}ms` : duration(took)}
              </span>
            ) : (
              <span className="text-muted-foreground">—</span>
            )
          }
          hint={
            run?.ActiveState === "activating" || run?.ActiveState === "active"
              ? "running now"
              : "the last run"
          }
        />
      </StatGrid>

      <SheetSection title="When it runs">
        <WhenItRuns
          times={runs.times}
          dense={runs.dense}
          zone={zone}
          empty={stopped ? "Not while the timer is stopped." : "systemd has nothing scheduled."}
          week={
            weekly ? (
              <WeekStrip
                schedule={weekly}
                timeZone={zone}
                color={KIND.timer.color}
                spread={spread}
                className={cn(stopped && "opacity-50")}
              />
            ) : undefined
          }
        />
        {delay && (
          <p className="mt-4 text-hint leading-relaxed text-muted-foreground">
            systemd adds a random delay of up to {spanWords(delay)} to every run, so servers with
            the same package do not all fire at once. The shaded part of each day is when it may
            really start; only the next run is known for sure.
          </p>
        )}
      </SheetSection>

      {timer.activates && (
        <SheetSection
          title="Command"
          actions={
            command && (
              <IconAction
                label="Copy command"
                onClick={() => void copyText(command, "Command copied")}
              >
                <Copy />
              </IconAction>
            )
          }
        >
          {command ? (
            <Well className="max-h-48 text-xs leading-relaxed break-all whitespace-pre-wrap">
              <ShellWords command={command} />
            </Well>
          ) : service.data ? (
            <p className="text-body text-muted-foreground">
              {timer.activates} has no command of its own.
            </p>
          ) : (
            <LoadingRows rows={1} />
          )}
        </SheetSection>
      )}

      <SheetSection title="Details">
        <DetailList className="gap-y-2.5">
          {(timer.description || run?.Description) && (
            <Detail label="What it is">{timer.description || run?.Description}</Detail>
          )}
          <Detail label="Starts">
            {timer.activates ? (
              <Link
                href={`/processes/services?unit=${encodeURIComponent(timer.activates)}`}
                className="font-mono hover:underline"
              >
                {timer.activates}
              </Link>
            ) : (
              "nothing"
            )}
          </Detail>
          <Detail label="After a reboot">
            {timer.enabled ? "armed again" : "stays off until started"}
          </Detail>
          {props?.Persistent === "yes" && (
            <Detail label="Missed runs">made up when the server comes back</Detail>
          )}
          {props?.FragmentPath && (
            <Detail label="Unit file">
              <Link
                href={`/files?path=${encodeURIComponent(props.FragmentPath)}`}
                className="font-mono break-all hover:underline"
              >
                {props.FragmentPath}
              </Link>
            </Detail>
          )}
        </DetailList>
      </SheetSection>

      {timer.activates && (
        <SheetSection title="Runs">
          <TimerRuns unit={timer.activates} />
        </SheetSection>
      )}
    </div>
  )
}

/**
 * The runs of what a timer fires: the service's own journal with Runs in
 * front, so a failed run is one press from the lines it wrote, and Live
 * beside it for a run started with "Run now".
 */
function TimerRuns({ unit }: { unit: string }) {
  const sources = useMemo<ServiceLogSource[]>(
    () => [{ id: journalSource(unit), label: unit, kind: "journal", product: unitProduct(unit) }],
    [unit],
  )
  const views = useMemo(() => [unitRunsView(unit)], [unit])
  // Runs is as tall as its rows, up to a limit; the lines scroll inside a
  // height of their own, so Live and History take the limit outright.
  const [reading, setReading] = useState("runs")
  return (
    <ServiceLogs
      sources={sources}
      views={views}
      view="runs"
      onViewChange={setReading}
      modes={["live", "search"]}
      layout="sheet"
      className="[contain:inline-size]"
      paneClassName={reading === "runs" ? "max-h-[26rem]" : "h-[26rem]"}
    />
  )
}
