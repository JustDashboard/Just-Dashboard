"use client"

import { useMemo } from "react"
import { Lightning, Play, Slash, StopCircle, Stopwatch } from "@/components/icons"
import { post } from "@/lib/api"
import { relativeTime, timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { SystemdTimer, SystemdUnit } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import type { PollState } from "@/hooks/use-poll"
import { useArrivals } from "@/hooks/use-arrivals"
import { useSessionState } from "@/lib/view-state"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductLogo, unitProduct } from "@/components/product-logo"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import { Status, StatusDot } from "@/components/status-dot"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { VerbActions, type Verb } from "@/components/verbs"
import { TextShimmer } from "@/components/ui/text-shimmer"
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
import { Countdown } from "@/components/procs/schedule-band"

type ConfirmFn = (request: ConfirmRequest) => void

export type TimerList = { available: boolean; timers: SystemdTimer[] }

/** Which timers the head's chips narrow the table to. */
type TimerState = "" | "armed" | "running" | "stopped"

function stateOf(timer: SystemdTimer): Exclude<TimerState, ""> {
  if (timer.activeState !== "active") return "stopped"
  return timer.subState === "running" ? "running" : "armed"
}

/**
 * systemd timers, beside cron, because on a modern host they are where half
 * of the scheduled work lives — apt, logrotate, certbot and fstrim all run
 * from one — and a page that showed only crontabs answered "what runs at
 * night" with half an answer. Soonest first; a timer with nothing scheduled
 * sinks to the bottom. The list is polled by the page, whose band draws the
 * next day of these and the cron jobs together; each timer is drawn as the
 * product its unit runs (§14).
 *
 * The head counts what the Timers armed tile used to — armed, running now,
 * stopped — as chips that narrow the table to what they count, and how many
 * start on boot beside them. A row opens the timer's sheet
 * (`timer-sheet.tsx`): its calendar in words, what it runs, how the last run
 * ended and every run before it. A timer whose service is running says so as
 * it happens.
 */
export function TimersPanel({
  timers,
  confirm,
  onOpen,
}: {
  timers: PollState<TimerList>
  confirm: ConfirmFn
  onOpen: (unit: string) => void
}) {
  const { pending, act } = useUnitControl(timers.refresh)
  const list = useMemo(() => timers.data?.timers ?? [], [timers.data])
  const [state, setState] = useSessionState<TimerState>("processes.timers.state", "")
  const counts = useMemo(() => {
    const out = { armed: 0, running: 0, stopped: 0 }
    for (const timer of list) out[stateOf(timer)]++
    return out
  }, [list])
  const onBoot = list.filter((timer) => timer.enabled).length
  const shown = state ? list.filter((timer) => stateOf(timer) === state) : list
  const arrived = useArrivals(list.map((timer) => timer.unit))

  return (
    <Panel>
      <PanelHeader
        title="systemd timers"
        advanced
        actions={
          list.length > 0 && (
            <span className="numeric text-hint text-muted-foreground">{onBoot} start on boot</span>
          )
        }
      >
        {list.length > 0 && (
          <ChipStrip aria-label="Timer state" className="mr-auto">
            <FilterChip selected={state === ""} onClick={() => setState("")}>
              All <ChipCount>{list.length}</ChipCount>
            </FilterChip>
            {(
              [
                ["armed", "Armed", "bg-success"],
                ["running", "Running now", "bg-success"],
                ["stopped", "Stopped", "bg-muted-foreground/50"],
              ] as const
            ).map(([value, label, dot]) =>
              counts[value] === 0 && state !== value ? null : (
                <FilterChip
                  key={value}
                  selected={state === value}
                  onClick={() => setState(state === value ? "" : value)}
                >
                  <span aria-hidden className={cn("size-1.5 rounded-full", dot)} />
                  {label}
                  <ChipCount>{counts[value]}</ChipCount>
                </FilterChip>
              ),
            )}
          </ChipStrip>
        )}
      </PanelHeader>
      <PanelBody flush>
        {timers.loading && !timers.data && <LoadingRows rows={3} className="pt-3" />}
        {timers.error && !timers.data && <ErrorState error={timers.error} className="mt-3" />}
        {timers.data && !timers.data.available && (
          <EmptyNote>systemd is not available on this host.</EmptyNote>
        )}
        {timers.data?.available && list.length === 0 && <EmptyNote>No timers.</EmptyNote>}
        {list.length > 0 && shown.length === 0 && <EmptyNote>No timer is {state}.</EmptyNote>}
        {shown.length > 0 && (
          <Table containerClassName="w-auto max-h-[calc(100svh-22rem)] group-data-[plain]/panel:-mx-4">
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
              {shown.map((timer) => (
                <TimerRow
                  key={timer.unit}
                  timer={timer}
                  arrived={arrived.has(timer.unit)}
                  busy={pending[timer.unit] ?? pending[timer.activates]}
                  confirm={confirm}
                  act={act}
                  onChanged={timers.refresh}
                  onOpen={() => onOpen(timer.unit)}
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

/**
 * Every verb this operator may use on a timer, for its row and its sheet
 * alike: Run now starts the service the way the timer would, Start and Stop
 * arm and disarm the timer itself, and Enable and Disable say whether it is
 * armed again at boot.
 */
export function useTimerVerbs({
  timer,
  confirm,
  act,
  onChanged,
}: {
  timer: SystemdTimer
  confirm: ConfirmFn
  act: ReturnType<typeof useUnitControl>["act"]
  onChanged: () => void
}) {
  const { can } = useAuth()
  return useMemo<Verb[]>(() => {
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
}

/** A timer's state as a word: what a row and the sheet's head both say. */
export function TimerState({ timer, busy }: { timer: SystemdTimer; busy?: string }) {
  if (busy) {
    return (
      <span className="inline-flex items-center gap-1.5 text-xs font-medium">
        <StatusDot tone="notice" />
        <TextShimmer>{`${busy}…`}</TextShimmer>
      </span>
    )
  }
  // A timer's `running` is its service, started by it and not yet done: the
  // work is happening now, so it is said as it happens.
  if (stateOf(timer) === "running") {
    return (
      <span className="inline-flex items-center gap-1.5 text-xs font-medium">
        <StatusDot tone="running" live />
        <TextShimmer>running now</TextShimmer>
      </span>
    )
  }
  return (
    <Status
      state={timer.activeState}
      label={timer.activeState === "active" ? "armed" : "stopped"}
    />
  )
}

function TimerRow({
  timer,
  arrived,
  busy,
  confirm,
  act,
  onChanged,
  onOpen,
}: {
  timer: SystemdTimer
  arrived: boolean
  busy?: string
  confirm: ConfirmFn
  act: ReturnType<typeof useUnitControl>["act"]
  onChanged: () => void
  onOpen: () => void
}) {
  const verbs = useTimerVerbs({ timer, confirm, act, onChanged })

  return (
    <TableRow className={cn("group", arrived && "animate-rise")} onActivate={onOpen}>
      <TableCell>
        <div className="flex max-w-[26rem] min-w-0 items-center gap-3">
          <ProductLogo id={unitProduct(timer.unit)} size="sm" fallback={Stopwatch} />
          <div className="min-w-0">
            <button
              type="button"
              onClick={onOpen}
              className="block max-w-full truncate text-left text-body font-medium hover:underline"
            >
              {timer.unit}
            </button>
            <p className="truncate text-hint text-muted-foreground">
              {timer.activates ? `runs ${timer.activates}` : "activates nothing"}
            </p>
          </div>
        </div>
      </TableCell>
      <TableCell className="hidden md:table-cell">
        {timer.next ? (
          <>
            <p className="numeric">
              <Countdown at={new Date(timer.next).getTime()} />
            </p>
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
        <TimerState timer={timer} busy={busy} />
      </TableCell>
      <TableCell className="hidden xl:table-cell">
        <Tag>{timer.unitFileState || "unknown"}</Tag>
      </TableCell>
      <TableCell>
        <VerbActions dim verbs={verbs} />
      </TableCell>
    </TableRow>
  )
}
