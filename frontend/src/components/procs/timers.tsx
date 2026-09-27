"use client"

import { useMemo, useState } from "react"
import { ChevronDown, Lightning, Play, Slash, StopCircle, Stopwatch } from "@/components/icons"
import { cn } from "@/lib/utils"
import { post } from "@/lib/api"
import { relativeTime, timestamp } from "@/lib/format"
import { journalSource } from "@/lib/log-sources"
import { notify } from "@/lib/toast"
import type { SystemdTimer, SystemdUnit } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import type { PollState } from "@/hooks/use-poll"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { ServiceLogs, type ServiceLogSource } from "@/components/logs/service-logs"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductLogo, unitProduct } from "@/components/product-logo"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { VerbActions, type Verb } from "@/components/verbs"
import {
  stickyTableHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { useUnitControl } from "@/components/procs/unit-actions"
import { unitRunsView } from "@/components/procs/unit-runs"

type ConfirmFn = (request: ConfirmRequest) => void

export type TimerList = { available: boolean; timers: SystemdTimer[] }

/**
 * systemd timers, beside cron, because on a modern host they are where half
 * of the scheduled work lives — apt, logrotate, certbot and fstrim all run
 * from one — and a page that showed only crontabs answered "what runs at
 * night" with half an answer. Soonest first; a timer with nothing scheduled
 * sinks to the bottom. The list is polled by the page, whose "next run"
 * reading is the soonest of these and the cron jobs together; each timer is
 * drawn as the product its unit runs (§14). A timer opens in place on the
 * runs of the unit it fires — when each started, how long it took, how it
 * ended — since "did last night's run work" is the question a timer is
 * looked up for, and its schedule alone cannot answer it.
 */
export function TimersPanel({
  timers,
  confirm,
}: {
  timers: PollState<TimerList>
  confirm: ConfirmFn
}) {
  const { pending, act } = useUnitControl(timers.refresh)
  const list = useMemo(() => timers.data?.timers ?? [], [timers.data])
  // The timers opened on their runs. While any is, the table gives up its
  // own scroll: a run list inside a scrolling grid is a scroll inside a
  // scroll, and on a phone the timer's own row scrolled away above it.
  const [opened, setOpened] = useState<string[]>([])

  return (
    <Panel>
      <PanelHeader
        title="systemd timers"
        advanced
        actions={
          list.length > 0 && (
            <span className="numeric text-hint text-muted-foreground">{list.length}</span>
          )
        }
      />
      <PanelBody flush>
        {timers.loading && !timers.data && <LoadingRows rows={3} className="pt-3" />}
        {timers.error && !timers.data && <ErrorState error={timers.error} className="mt-3" />}
        {timers.data && !timers.data.available && (
          <EmptyNote>systemd is not available on this host.</EmptyNote>
        )}
        {timers.data?.available && list.length === 0 && <EmptyNote>No timers.</EmptyNote>}
        {list.length > 0 && (
          <Table
            containerClassName={cn(
              "w-auto group-data-[plain]/panel:-mx-4",
              opened.length === 0 && "max-h-[calc(100svh-22rem)]",
            )}
          >
            <TableHeader className={stickyTableHeader}>
              <TableRow>
                <TableHead className="w-full">Timer</TableHead>
                <TableHead className="hidden md:table-cell">Next</TableHead>
                <TableHead className="hidden lg:table-cell">Last</TableHead>
                <TableHead>State</TableHead>
                <TableHead className="hidden xl:table-cell">Startup</TableHead>
                <TableHead className="w-px" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {list.map((timer) => (
                <TimerRow
                  key={timer.unit}
                  timer={timer}
                  busy={pending[timer.unit] ?? pending[timer.activates]}
                  confirm={confirm}
                  act={act}
                  onChanged={timers.refresh}
                  open={opened.includes(timer.unit)}
                  onOpenChange={(open) =>
                    setOpened((was) =>
                      open ? [...was, timer.unit] : was.filter((unit) => unit !== timer.unit),
                    )
                  }
                />
              ))}
            </TableBody>
          </Table>
        )}
      </PanelBody>
    </Panel>
  )
}

function asUnit(timer: SystemdTimer): SystemdUnit {
  return {
    name: timer.unit,
    description: "",
    loadState: "loaded",
    activeState: timer.activeState,
    subState: timer.subState,
    unitFileState: timer.unitFileState,
    enabled: timer.enabled,
  }
}

function TimerRow({
  timer,
  busy,
  confirm,
  act,
  onChanged,
  open,
  onOpenChange,
}: {
  timer: SystemdTimer
  busy?: string
  confirm: ConfirmFn
  act: ReturnType<typeof useUnitControl>["act"]
  onChanged: () => void
  /** Open under its row on the runs of what it fires. */
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { can } = useAuth()
  const verbs = useMemo<Verb[]>(() => {
    const list: Verb[] = []
    const unit = asUnit(timer)
    const active = timer.activeState === "active"
    if (can("service.control") && timer.activates) {
      list.push({
        key: "run",
        label: "Run now",
        icon: Lightning,
        inline: true,
        run: () =>
          confirm({
            title: "Run now",
            confirmLabel: "Run",
            description: (
              <p>
                <b>{timer.activates}</b> starts immediately, as it would when the timer fires. The
                timer&apos;s own schedule is unchanged.
              </p>
            ),
            action: async () => {
              await post(`/systemd/${encodeURIComponent(timer.activates)}/start`)
              notify.success(`${timer.activates} started`)
              onChanged()
            },
          }),
      })
    }
    if (can("service.control") && !active) {
      list.push({
        key: "start",
        progressive: "Starting",
        label: "Start timer",
        icon: Play,
        run: () => void act(unit, "start", "Starting").catch(() => undefined),
      })
    }
    if (can("destructive") && active) {
      list.push({
        key: "stop",
        progressive: "Stopping",
        label: "Stop timer",
        icon: StopCircle,
        run: () =>
          confirm({
            title: "Stop timer",
            confirmLabel: "Stop",
            description: (
              <p>
                <b>{timer.unit}</b> stops firing until it is started again. A running{" "}
                {timer.activates} is left to finish.
              </p>
            ),
            action: (phrase) => act(unit, "stop", "Stopping", phrase),
          }),
      })
    }
    if (
      can("system.admin") &&
      ["enabled", "enabled-runtime", "disabled"].includes(timer.unitFileState)
    ) {
      list.push(
        timer.enabled
          ? {
              key: "disable",
              label: "Disable on boot",
              icon: Slash,
              run: () => void act(unit, "disable", "Disabling").catch(() => undefined),
            }
          : {
              key: "enable",
              label: "Enable on boot",
              icon: Lightning,
              run: () => void act(unit, "enable", "Enabling").catch(() => undefined),
            },
      )
    }
    return list
  }, [timer, can, confirm, act, onChanged])

  const toggle = timer.activates ? () => onOpenChange(!open) : undefined

  return (
    <>
      <TableRow className="group" onActivate={toggle}>
        <TableCell>
          <div className="flex max-w-[26rem] min-w-0 items-center gap-3">
            <ProductLogo id={unitProduct(timer.unit)} size="sm" fallback={Stopwatch} />
            <div className="min-w-0">
              {toggle ? (
                <button
                  type="button"
                  aria-expanded={open}
                  onClick={toggle}
                  className="flex max-w-full min-w-0 items-center gap-1 text-left text-body font-medium hover:underline"
                >
                  <span className="truncate">{timer.unit}</span>
                  <ChevronDown
                    aria-hidden
                    className={cn(
                      "size-3.5 shrink-0 text-muted-foreground transition-transform",
                      open && "rotate-180",
                    )}
                  />
                </button>
              ) : (
                <p className="truncate text-body font-medium">{timer.unit}</p>
              )}
              <p className="truncate text-hint text-muted-foreground">
                {timer.activates ? `runs ${timer.activates}` : "activates nothing"}
              </p>
            </div>
          </div>
        </TableCell>
        <TableCell className="hidden md:table-cell">
          {timer.next ? (
            <>
              <p>{relativeTime(timer.next)}</p>
              <p className="text-hint text-muted-foreground">{timestamp(timer.next)}</p>
            </>
          ) : (
            <span className="text-muted-foreground">—</span>
          )}
        </TableCell>
        <TableCell className="hidden text-muted-foreground lg:table-cell">
          {timer.last ? relativeTime(timer.last) : "never"}
        </TableCell>
        <TableCell>
          <Status
            state={busy ? "activating" : timer.activeState}
            label={busy ? `${busy}…` : timer.activeState || "unknown"}
          />
        </TableCell>
        <TableCell className="hidden xl:table-cell">
          <Tag>{timer.unitFileState || "unknown"}</Tag>
        </TableCell>
        <TableCell>
          <VerbActions dim verbs={verbs} />
        </TableCell>
      </TableRow>
      {open && timer.activates && (
        <TableRow className="hover:bg-transparent has-aria-expanded:bg-transparent">
          <TableCell colSpan={6} className="p-0 whitespace-normal">
            <TimerRuns unit={timer.activates} />
          </TableCell>
        </TableRow>
      )}
    </>
  )
}

/**
 * The runs of what a timer fires, opened under its row: the service's own
 * journal with Runs in front, so a failed run is one press from the lines
 * it wrote, and Live beside it for a run started with "Run now". It has no
 * frame of its own — the table's hairlines above and below are its edges,
 * and a frame inside the panel's would be two.
 */
function TimerRuns({ unit }: { unit: string }) {
  const sources = useMemo<ServiceLogSource[]>(
    () => [{ id: journalSource(unit), label: unit, kind: "journal", product: unitProduct(unit) }],
    [unit],
  )
  const views = useMemo(() => [unitRunsView(unit)], [unit])
  // Runs is as tall as its rows, up to a limit; the lines scroll inside a
  // height of their own, so Live and History take the limit outright. The
  // height is the column's, which the pane fills: set on the pane itself it
  // loses to the pane's flex basis. Its width is the table's and adds
  // nothing to it — a table cell grows to its content's widest line, and an
  // unwrapped log line pushed the timers' own columns off a phone.
  const [reading, setReading] = useState("runs")
  return (
    <ServiceLogs
      sources={sources}
      views={views}
      view="runs"
      onViewChange={setReading}
      modes={["live", "search"]}
      layout="sheet"
      flush
      className={cn("[contain:inline-size]", reading === "runs" ? "max-h-[26rem]" : "h-[26rem]")}
    />
  )
}
