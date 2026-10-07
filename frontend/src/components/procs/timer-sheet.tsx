"use client"

import { useMemo, useState } from "react"
import Link from "next/link"
import { Copy, Stopwatch } from "@/components/icons"
import { get } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { duration, relativeTime, timestamp } from "@/lib/format"
import { journalSource } from "@/lib/log-sources"
import { countdown, execCommand, randomDelay, timerTriggers } from "@/lib/schedule"
import type { SystemdTimer, SystemdUnit } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { ShellWords } from "@/components/deploy/run-evidence"
import { useNow } from "@/components/deploy/vocabulary"
import { IconAction } from "@/components/icon-action"
import { ServiceLogs, type ServiceLogSource } from "@/components/logs/service-logs"
import { FactDot } from "@/components/metrics/host-identity"
import { Detail, DetailList } from "@/components/page"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { ProductLogo, unitProduct } from "@/components/product-logo"
import { SidePanel } from "@/components/side-panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { LoadingRows } from "@/components/state"
import { VerbBar } from "@/components/verbs"
import { TimerState, useTimerVerbs } from "@/components/procs/timers"
import { useUnitControl } from "@/components/procs/unit-actions"
import { unitRunsView } from "@/components/procs/unit-runs"

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
}: {
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
          <span className="min-w-0 truncate">{unit}</span>
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
        <TimerDetail key={timer.unit} timer={timer} />
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

function TimerDetail({ timer }: { timer: SystemdTimer }) {
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
  const triggers = props ? timerTriggers(props) : []
  const delay = props ? randomDelay(props) : undefined
  const command = run ? execCommand(run) : undefined

  const next = timer.next ? new Date(timer.next).getTime() : undefined
  const started = micro(run, "ExecMainStartTimestampMonotonic")
  const exited = micro(run, "ExecMainExitTimestampMonotonic")
  const took = started > 0 && exited >= started ? (exited - started) / 1e6 : undefined
  const result = run?.Result
  const failed = Boolean(result && result !== "success")
  const status = run?.ExecMainStatus

  return (
    <div className="flex animate-rise flex-col gap-6 px-5 pt-4 pb-6">
      <p className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
        <span>runs {timer.activates || "nothing"}</span>
        <FactDot />
        <span>{timer.enabled ? "armed at boot" : "not armed at boot"}</span>
        {props?.Persistent === "yes" && (
          <>
            <FactDot />
            <span title="A run missed while the host was off is made up when it comes back">
              catches up after downtime
            </span>
          </>
        )}
        {props?.FragmentPath && (
          <>
            <FactDot />
            <Link
              href={`/files?path=${encodeURIComponent(props.FragmentPath)}`}
              className="truncate font-mono hover:text-foreground hover:underline"
            >
              {props.FragmentPath}
            </Link>
          </>
        )}
      </p>

      <StatGrid columns={3} dense className="-mx-5 border-y border-hairline">
        <StatTile
          className="px-5"
          label="Next run"
          value={
            next !== undefined && next > now ? (
              <span className="numeric">{countdown(next - now)}</span>
            ) : (
              <span className="text-muted-foreground">—</span>
            )
          }
          hint={
            next !== undefined
              ? timestamp(timer.next)
              : timer.activeState === "active"
                ? "nothing scheduled"
                : "the timer is stopped"
          }
        />
        <StatTile
          className="px-5"
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
                ? timestamp(timer.last)
                : "since the timer was installed"
              : started === 0
                ? "not since boot"
                : `${RESULT[result ?? ""] ?? result ?? "ended"}${
                    failed && status && status !== "0" ? ` · exit ${status}` : ""
                  }`
          }
        />
        <StatTile
          className="px-5"
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
              : run?.Type
                ? `${run.Type} service`
                : undefined
          }
        />
      </StatGrid>

      <Panel plain>
        <PanelHeader title="Fires" />
        <PanelBody className="space-y-3 pt-3">
          {!props ? (
            <LoadingRows rows={2} />
          ) : triggers.length === 0 ? (
            <p className="text-body text-muted-foreground">
              No calendar or interval — only started by hand.
            </p>
          ) : (
            <ul className="space-y-2">
              {triggers.map((trigger) => (
                <li key={trigger.spec} className="min-w-0">
                  <p className="text-body font-medium first-letter:uppercase">{trigger.words}</p>
                  {trigger.words !== trigger.spec && (
                    <p className="font-mono text-hint text-muted-foreground">{trigger.spec}</p>
                  )}
                </li>
              ))}
            </ul>
          )}
          {delay && (
            <DetailList>
              <Detail label="Random delay">
                up to {delay} later, so every host with this package does not fire at once
              </Detail>
              {props?.AccuracyUSec && <Detail label="Accuracy">{props.AccuracyUSec}</Detail>}
            </DetailList>
          )}
        </PanelBody>
      </Panel>

      {timer.activates && (
        <Panel plain>
          <PanelHeader
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
          />
          <PanelBody className="space-y-3 pt-3">
            {run?.Description && <p className="text-body">{run.Description}</p>}
            {command ? (
              <Well className="max-h-40 text-xs leading-relaxed break-all whitespace-pre-wrap">
                <ShellWords command={command} />
              </Well>
            ) : (
              service.data && (
                <p className="text-body text-muted-foreground">
                  {timer.activates} has no command of its own.
                </p>
              )
            )}
          </PanelBody>
        </Panel>
      )}

      {timer.activates && <TimerRuns unit={timer.activates} />}
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
