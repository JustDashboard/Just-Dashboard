"use client"

import { Suspense, useMemo, useState } from "react"
import { forgetSessionState, useSessionState } from "@/lib/view-state"
import { ChartActivity, Plus } from "@/components/icons"
import { get } from "@/lib/api"
import { bytes, duration, percent, relativeTime } from "@/lib/format"
import type { PM2Daemon, PM2Inventory, PM2Process } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { useQuerySelection } from "@/hooks/use-query-selection"
import { useConfirm } from "@/components/confirm-dialog"
import { cn } from "@/lib/utils"
import { Page, PageHeader, RowLink, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { ROW_BLEED } from "@/components/row-list"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { ChipCount, FilterChip } from "@/components/tabs"
import { EmptyState, ErrorState, LoadingPanel } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { VerbActions, VerbMenu } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import {
  stickyTableHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { PM2DetailSheet } from "@/components/procs/pm2-detail"
import { PM2StartDialog } from "@/components/procs/pm2-start-dialog"
import {
  pm2Key,
  useDaemonVerbs,
  usePM2Control,
  usePM2Verbs,
  type ConfirmFn,
  type PendingMap,
} from "@/components/procs/pm2-actions"

type StateFilter = "all" | "online" | "stopped" | "errored"

export default function PM2Page() {
  return (
    <Suspense>
      <PM2Applications />
    </Suspense>
  )
}

/**
 * What PM2 runs, and whether it would come back.
 *
 * The daemon facts under the title are the part the old page never said: a
 * daemon with three online applications and no saved list restores nothing
 * after a reboot, and no boot hook restores nothing even with one. Both are
 * facts about the account, so they are stated as a row, not hidden in a
 * tooltip on the Save button.
 */
function PM2Applications() {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const [filter, setFilter] = useSessionState("processes.pm2.query", "")
  const [state, setState] = useSessionState<StateFilter>("processes.pm2.state", "all")
  const [selectedKey, selectKey] = useQuerySelection("app")
  const [focusTab, setFocusTab] = useState<string>()
  const [starting, setStarting] = useSessionState("processes.pm2.starting", false)
  const inventory = usePoll((signal) => get<PM2Inventory>("/pm2/", undefined, signal), 5000)
  const { pending, act } = usePM2Control(inventory.refresh)

  const processes = useMemo(() => inventory.data?.processes ?? [], [inventory.data])
  const daemons = useMemo(() => inventory.data?.daemons ?? [], [inventory.data])
  const daemonVerbs = useDaemonVerbs({ daemons, confirm, onChanged: inventory.refresh })

  const counts = useMemo(
    () => ({
      all: processes.length,
      online: processes.filter((p) => p.status === "online").length,
      stopped: processes.filter((p) => p.status === "stopped").length,
      errored: processes.filter((p) => p.status !== "online" && p.status !== "stopped").length,
    }),
    [processes],
  )
  const totals = useMemo(
    () => ({
      cpu: processes.reduce((s, p) => s + p.cpu, 0),
      memory: processes.reduce((s, p) => s + p.memory, 0),
      restarts: processes.reduce((s, p) => s + p.restarts, 0),
      unstable: processes.reduce((s, p) => s + p.unstableRestarts, 0),
    }),
    [processes],
  )
  const visible = useMemo(() => {
    const needle = filter.trim().toLowerCase()
    return processes.filter((p) => {
      if (state === "online" && p.status !== "online") return false
      if (state === "stopped" && p.status !== "stopped") return false
      if (state === "errored" && (p.status === "online" || p.status === "stopped")) return false
      if (!needle) return true
      return (
        p.name.toLowerCase().includes(needle) ||
        p.scriptPath.toLowerCase().includes(needle) ||
        p.daemonId.toLowerCase().includes(needle)
      )
    })
  }, [processes, filter, state])

  // A deep link may name the application (`?app=api`, from the live table)
  // or its exact identity (`deploy:0`); the name wins only while it is
  // unambiguous.
  const selected = useMemo(() => {
    if (!selectedKey) return null
    const exact = processes.find((p) => pm2Key(p) === selectedKey)
    if (exact) return exact
    const named = processes.filter((p) => p.name === selectedKey)
    return named.length === 1 ? named[0] : null
  }, [processes, selectedKey])

  const open = (process: PM2Process, tab?: string) => {
    setFocusTab(tab)
    selectKey(pm2Key(process))
  }

  const header = (
    <PageHeader
      eyebrow="Processes"
      title="PM2"
      actions={
        inventory.data?.available && (
          <>
            {can("system.admin") && daemons.length > 0 && (
              <Button size="sm" onClick={() => setStarting(true)}>
                <Plus className="size-3.5" />
                Start application
              </Button>
            )}
            {daemonVerbs.length > 0 && (
              <VerbMenu
                verbs={daemonVerbs}
                trigger={
                  <Button size="sm" variant="outline">
                    Startup and bulk
                  </Button>
                }
              />
            )}
          </>
        )
      }
    />
  )

  if (inventory.loading && !inventory.data) {
    return (
      <Page>
        {header}
        <LoadingPanel />
      </Page>
    )
  }
  if (inventory.error && !inventory.data) {
    return (
      <Page>
        {header}
        <ErrorState error={inventory.error} />
      </Page>
    )
  }
  if (!inventory.data?.available) {
    return (
      <Page>
        {header}
        <EmptyState
          icon={ChartActivity}
          title="PM2 is not installed"
          description="Install PM2 for a host account (npm install -g pm2) to manage Node applications from here. The dashboard finds every account's daemon on its own."
        />
      </Page>
    )
  }

  return (
    <Page className="animate-rise">
      {header}

      {daemons.length > 0 && (
        <div className="flex min-w-0 flex-col gap-1">
          {daemons.map((daemon) => (
            <DaemonFacts key={daemon.account} daemon={daemon} several={daemons.length > 1} />
          ))}
        </div>
      )}

      <StatGrid columns={4}>
        <StatTile
          label="Online"
          value={counts.online}
          tone={counts.online === 0 && counts.all > 0 ? "warning" : "default"}
          hint={`of ${counts.all} ${counts.all === 1 ? "application" : "applications"}`}
        />
        <StatTile
          label="Not running"
          value={counts.stopped + counts.errored}
          tone={counts.errored > 0 ? "danger" : "default"}
          hint={
            counts.errored > 0
              ? `${counts.errored} errored · ${counts.stopped} stopped`
              : counts.stopped > 0
                ? "stopped on purpose"
                : "everything registered is up"
          }
        />
        <StatTile
          label="Restarts"
          value={totals.restarts}
          tone={totals.unstable > 0 ? "warning" : "default"}
          hint={
            totals.unstable > 0
              ? `${totals.unstable} unstable — crashing soon after start`
              : "since the counters were last reset"
          }
        />
        <StatTile
          label="Memory"
          value={bytes(totals.memory)}
          hint={`${percent(totals.cpu, 0)} CPU across every application`}
        />
      </StatGrid>

      <Panel plain>
        <PanelHeader title="Applications" />
        <PanelToolbar>
          <SearchInput
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder="Name, script or account"
            containerClassName="sm:w-64"
          />
          <div className="flex min-w-0 flex-wrap items-center gap-1">
            {(["all", "online", "stopped", "errored"] as const).map((key) => (
              <FilterChip key={key} selected={state === key} onClick={() => setState(key)}>
                {key === "all"
                  ? "All"
                  : key === "online"
                    ? "Online"
                    : key === "stopped"
                      ? "Stopped"
                      : "Errored"}{" "}
                <ChipCount>{counts[key]}</ChipCount>
              </FilterChip>
            ))}
          </div>
        </PanelToolbar>
        <PanelBody flush>
          {processes.length === 0 ? (
            <EmptyState
              icon={ChartActivity}
              title="PM2 is running but manages no applications"
              description={
                can("system.admin")
                  ? "Start one from a script or an ecosystem file, and save the startup list once it runs."
                  : "Nothing has been started under PM2 yet."
              }
              className="mt-4"
            />
          ) : visible.length === 0 ? (
            <EmptyState icon={ChartActivity} title="No applications match" className="mt-4" />
          ) : (
            <>
              <div className="-mx-4 hidden min-w-0 lg:block">
                <Table containerClassName="max-h-[calc(100svh-24rem)]">
                  <TableHeader className={stickyTableHeader}>
                    <TableRow>
                      <TableHead className="w-full">Application</TableHead>
                      <TableHead>Status</TableHead>
                      <TableHead>Mode</TableHead>
                      <TableHead className="text-right">CPU</TableHead>
                      <TableHead className="text-right">Memory</TableHead>
                      <TableHead className="text-right">Restarts</TableHead>
                      <TableHead className="hidden xl:table-cell">Uptime</TableHead>
                      <TableHead className="w-px" />
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {visible.map((process) => (
                      <PM2Row
                        key={pm2Key(process)}
                        process={process}
                        several={daemons.length > 1}
                        pending={pending}
                        confirm={confirm}
                        act={act}
                        onOpen={open}
                        onChanged={inventory.refresh}
                      />
                    ))}
                  </TableBody>
                </Table>
              </div>
              <ul className="divide-y divide-hairline lg:hidden">
                {visible.map((process) => (
                  <PM2NarrowRow
                    key={pm2Key(process)}
                    process={process}
                    several={daemons.length > 1}
                    pending={pending}
                    confirm={confirm}
                    act={act}
                    onOpen={open}
                    onChanged={inventory.refresh}
                  />
                ))}
              </ul>
            </>
          )}
        </PanelBody>
      </Panel>

      <PM2DetailSheet
        process={selected}
        initialTab={focusTab}
        onOpenChange={(o) => !o && selectKey(null)}
        onChanged={inventory.refresh}
      />
      <PM2StartDialog
        open={starting}
        daemons={daemons}
        onOpenChange={(open) => {
          setStarting(open)
          // Closed by hand, whether cancelled or started: the next one starts blank.
          if (!open) forgetSessionState("processes.pm2.start.")
        }}
        onStarted={inventory.refresh}
      />
      {dialog}
    </Page>
  )
}

/**
 * Whether this account's applications survive a reboot, as one line: the
 * boot hook, and when the list was last saved. Amber where either is
 * missing, because that is the day-after-the-reboot surprise this page
 * exists to prevent.
 */
function DaemonFacts({ daemon, several }: { daemon: PM2Daemon; several: boolean }) {
  const hook = Boolean(daemon.startupUnit)
  const saved = Boolean(daemon.dumpSavedAt)
  return (
    <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-body text-muted-foreground">
      {several && (
        <>
          <span className="font-medium text-foreground">{daemon.account}</span>
          <Dot />
        </>
      )}
      {hook && saved ? (
        <Status state="active" label="Resurrects on boot" />
      ) : (
        <Status state="failed" label={hook ? "Startup list never saved" : "No boot hook"} />
      )}
      <Dot />
      <span>
        {hook ? (
          <>
            hook <span className="font-mono">{daemon.startupUnit}</span>
          </>
        ) : (
          <>
            run <span className="font-mono">pm2 startup</span> as {daemon.account} to install one
          </>
        )}
      </span>
      <Dot />
      <span>{saved ? `list saved ${relativeTime(daemon.dumpSavedAt)}` : "list not yet saved"}</span>
    </div>
  )
}

function Dot() {
  return <span className="text-muted-foreground/40">·</span>
}

type RowProps = {
  process: PM2Process
  several: boolean
  pending: PendingMap
  confirm: ConfirmFn
  act: Parameters<typeof usePM2Verbs>[0]["act"]
  onOpen: (process: PM2Process, tab?: string) => void
  onChanged: () => void
}

function modeLabel(process: PM2Process): string {
  const cluster = process.execMode === "cluster_mode" || process.execMode === "cluster"
  return cluster ? `cluster × ${process.instances || 1}` : "fork"
}

function PM2Row({ process, several, pending, confirm, act, onOpen, onChanged }: RowProps) {
  const verbs = usePM2Verbs({
    process,
    confirm,
    act,
    onOpenTab: (tab) => onOpen(process, tab),
    onScale: () => onOpen(process),
    onChanged,
  })
  const busy = pending[pm2Key(process)]
  return (
    <TableRow className="group" onActivate={() => onOpen(process)}>
      <TableCell>
        <div className="max-w-[24rem] min-w-0">
          <RowLink onClick={() => onOpen(process)}>{process.name}</RowLink>
          <p
            className="truncate font-mono text-hint text-muted-foreground"
            title={process.scriptPath}
          >
            {process.scriptPath}
          </p>
        </div>
      </TableCell>
      <TableCell>
        <div className="flex flex-col gap-0.5">
          <Status
            state={busy ? "activating" : process.status}
            label={busy ? `${busy}…` : process.status}
          />
          {several && <span className="text-hint text-muted-foreground">{process.daemonId}</span>}
        </div>
      </TableCell>
      <TableCell>
        <div className="flex items-center gap-1.5">
          <Tag>{modeLabel(process)}</Tag>
          {process.watching && <Tag tone="warning">watch</Tag>}
        </div>
      </TableCell>
      <TableCell className="numeric text-right font-mono text-muted-foreground">
        {percent(process.cpu)}
      </TableCell>
      <TableCell className="numeric text-right font-mono">{bytes(process.memory)}</TableCell>
      <TableCell className="numeric text-right font-mono">
        {process.restarts}
        {process.unstableRestarts > 0 && (
          <span className="ml-1 text-destructive">({process.unstableRestarts} unstable)</span>
        )}
      </TableCell>
      <TableCell className="hidden text-muted-foreground xl:table-cell">
        {process.uptimeMs > 0 ? duration(process.uptimeMs / 1000) : "—"}
      </TableCell>
      <TableCell>
        <VerbActions dim verbs={verbs} />
      </TableCell>
    </TableRow>
  )
}

function PM2NarrowRow({ process, several, pending, confirm, act, onOpen, onChanged }: RowProps) {
  const verbs = usePM2Verbs({
    process,
    confirm,
    act,
    onOpenTab: (tab) => onOpen(process, tab),
    onScale: () => onOpen(process),
    onChanged,
  })
  const busy = pending[pm2Key(process)]
  return (
    <li
      className={cn(
        "group flex min-w-0 items-start gap-3 py-3 transition-colors hover:bg-row-hover",
        ROW_BLEED,
      )}
      onClick={(event) => {
        if ((event.target as HTMLElement).closest("a, button, [role='menuitem']")) return
        onOpen(process)
      }}
    >
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 items-baseline gap-2">
          <RowLink onClick={() => onOpen(process)}>{process.name}</RowLink>
          <Tag>{modeLabel(process)}</Tag>
          {several && <span className="text-hint text-muted-foreground">{process.daemonId}</span>}
        </div>
        <p className="truncate font-mono text-hint text-muted-foreground">{process.scriptPath}</p>
        <div className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-hint">
          <Status
            state={busy ? "activating" : process.status}
            label={busy ? `${busy}…` : process.status}
          />
          <span className="numeric font-mono text-muted-foreground">
            {percent(process.cpu)} CPU
          </span>
          <span className="numeric font-mono text-muted-foreground">{bytes(process.memory)}</span>
          <span className="numeric font-mono text-muted-foreground">
            {process.restarts} restarts
            {process.unstableRestarts > 0 && (
              <span className="ml-1 text-destructive">({process.unstableRestarts} unstable)</span>
            )}
          </span>
        </div>
      </div>
      <VerbActions verbs={verbs} className="shrink-0" />
    </li>
  )
}
