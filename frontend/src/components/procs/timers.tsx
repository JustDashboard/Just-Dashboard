"use client"

import { useMemo } from "react"
import { Lightning, Play, Slash, StopCircle, Stopwatch } from "@/components/icons"
import { post } from "@/lib/api"
import { relativeTime, timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { SystemdTimer, SystemdUnit } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import type { PollState } from "@/hooks/use-poll"
import type { ConfirmRequest } from "@/components/confirm-dialog"
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

type ConfirmFn = (request: ConfirmRequest) => void

export type TimerList = { available: boolean; timers: SystemdTimer[] }

/**
 * systemd timers, beside cron, because on a modern host they are where half
 * of the scheduled work lives — apt, logrotate, certbot and fstrim all run
 * from one — and a page that showed only crontabs answered "what runs at
 * night" with half an answer. Soonest first; a timer with nothing scheduled
 * sinks to the bottom. The list is polled by the page, whose "next run"
 * reading is the soonest of these and the cron jobs together; each timer is
 * drawn as the product its unit runs (§14).
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
          <Table containerClassName="group-data-[plain]/panel:-mx-4 max-h-[calc(100svh-22rem)] w-auto">
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
}: {
  timer: SystemdTimer
  busy?: string
  confirm: ConfirmFn
  act: ReturnType<typeof useUnitControl>["act"]
  onChanged: () => void
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

  return (
    <TableRow className="group">
      <TableCell>
        <div className="flex max-w-[26rem] min-w-0 items-center gap-3">
          <ProductLogo id={unitProduct(timer.unit)} size="sm" fallback={Stopwatch} />
          <div className="min-w-0">
            <p className="truncate text-body font-medium">{timer.unit}</p>
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
  )
}
