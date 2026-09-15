"use client"

import { useMemo, useState } from "react"
import {
  ChartActivity,
  Clock,
  FloppyDisk,
  ListOrdered,
  Play,
  RotateClockwise,
  StopCircle,
  Trash,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { del, get, post, put } from "@/lib/api"
import { bytes, duration, percent } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { Crontab, PM2Process, SystemdUnit } from "@/lib/types"
import { useViewState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { Page, PageHeader, RowLink, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelFooter, PanelHeader, PanelToolbar, Well } from "@/components/panel"
import { EmptyNote, EmptyState, ErrorState, LoadingPanel } from "@/components/state"
import { Status } from "@/components/status-dot"
import { UnitJournalSheet } from "@/components/procs/unit-journal"
import { PM2LogSheet } from "@/components/procs/pm2-logs"
import { ProcessTableTab } from "@/components/procs/process-table"
import { Button } from "@/components/ui/button"
import { DimActions, IconAction, RowActions } from "@/components/icon-action"
import { Tag } from "@/components/tag"
import { Textarea } from "@/components/ui/textarea"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  stickyTableHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

export default function ProcessesPage() {
  // Live is the one inventory every host has and now identifies which manager
  // owns each row. A remembered explicit manager tab still opens where it was
  // left; a new screen starts with the automatically classified whole host.
  const [tab, setTab] = useViewState("processes.tab", "table")

  return (
    <Page>
      <PageHeader eyebrow="Server" title="Processes" />
      <Tabs value={tab} onValueChange={setTab} className="min-w-0 gap-4">
        <TabsList>
          <TabsTrigger value="table">Live</TabsTrigger>
          <TabsTrigger value="pm2">PM2</TabsTrigger>
          <TabsTrigger value="systemd">systemd</TabsTrigger>
          <TabsTrigger value="cron">Cron</TabsTrigger>
        </TabsList>
        <TabsContent value="table" className="min-w-0">
          <ProcessTableTab />
        </TabsContent>
        <TabsContent value="pm2" className="min-w-0">
          <PM2Tab />
        </TabsContent>
        <TabsContent value="systemd" className="min-w-0">
          <SystemdTab />
        </TabsContent>
        <TabsContent value="cron" className="min-w-0">
          <CronTab />
        </TabsContent>
      </Tabs>
    </Page>
  )
}

function PM2Tab() {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const [logsFor, setLogsFor] = useState<PM2Process | null>(null)
  const [saving, setSaving] = useState(false)
  const { data, error, loading, refresh } = usePoll(
    (signal) => get<{ available: boolean; processes: PM2Process[] }>("/pm2/", undefined, signal),
    5000,
  )

  if (loading) return <LoadingPanel />
  if (error) return <ErrorState error={error} />
  if (!data?.available) {
    return (
      <EmptyState
        icon={ChartActivity}
        title="PM2 is not installed"
        description="Install PM2 on this host to manage node applications from here."
      />
    )
  }
  // A null list is a server shape, not an empty one: an older or failing
  // backend can answer `processes: null`, and reading `.length` off that is
  // the crash this page showed instead of its empty state.
  const processes = data.processes ?? []
  if (processes.length === 0) {
    return <EmptyState icon={ChartActivity} title="PM2 is running but manages no processes" />
  }

  const act = async (proc: PM2Process, action: string, confirmText?: string) => {
    await post(`/pm2/${encodeURIComponent(proc.name)}/${action}`, undefined, {
      confirm: confirmText,
      query: { user: proc.daemonId, id: proc.id },
    })
    const past =
      action === "stop"
        ? "stopped"
        : action === "start"
          ? "started"
          : action === "reload"
            ? "reloaded"
            : "restarted"
    notify.success(`${proc.name} ${past}`)
    refresh()
  }

  const saveStartupList = async () => {
    setSaving(true)
    try {
      await post("/pm2/save")
      notify.success("PM2 startup list saved")
    } finally {
      setSaving(false)
    }
  }

  return (
    <>
      <Panel>
        <PanelHeader
          title="PM2 applications"
          actions={
            can("service.control") && (
              <Button
                size="xs"
                variant="outline"
                disabled={saving}
                onClick={() => saveStartupList().catch((error) => notify.error(String(error)))}
              >
                <FloppyDisk className="size-3" />
                {saving ? "Saving…" : "Save startup list"}
              </Button>
            )
          }
        />
        <PanelBody flush>
          <Table containerClassName="max-h-[calc(100svh-20rem)]">
            <TableHeader className={stickyTableHeader}>
              <TableRow>
                <TableHead className="w-full">Application</TableHead>
                <TableHead>Status</TableHead>
                <TableHead className="text-right">CPU</TableHead>
                <TableHead className="text-right">Memory</TableHead>
                <TableHead className="text-right">Restarts</TableHead>
                <TableHead>Uptime</TableHead>
                <TableHead className="w-px" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {processes.map((proc) => (
                <TableRow
                  // Names and numeric ids are per-daemon, and one host can run
                  // a daemon per account — either alone collides across users.
                  key={`${proc.daemonId}:${proc.id}`}
                  className="group"
                  onActivate={() => setLogsFor(proc)}
                >
                  <TableCell>
                    <div className="max-w-[22rem] min-w-0">
                      <RowLink onClick={() => setLogsFor(proc)}>{proc.name}</RowLink>
                      <p className="truncate font-mono text-hint text-muted-foreground">
                        {proc.scriptPath}
                        {proc.daemonId && ` · ${proc.daemonId} #${proc.id}`}
                      </p>
                    </div>
                  </TableCell>
                  <TableCell>
                    <Status state={proc.status} />
                  </TableCell>
                  <TableCell className="numeric text-right font-mono">
                    {percent(proc.cpu)}
                  </TableCell>
                  <TableCell className="numeric text-right font-mono">
                    {bytes(proc.memory)}
                  </TableCell>
                  <TableCell className="numeric text-right font-mono">
                    {proc.restarts}
                    {proc.unstableRestarts > 0 && (
                      <span className="ml-1 text-destructive">
                        ({proc.unstableRestarts} unstable)
                      </span>
                    )}
                  </TableCell>
                  <TableCell className="text-muted-foreground">
                    {proc.uptimeMs > 0 ? duration(proc.uptimeMs / 1000) : "—"}
                  </TableCell>
                  <TableCell>
                    <RowActions>
                      {proc.status !== "online" && can("service.control") && (
                        <IconAction
                          label="Start"
                          onClick={() => act(proc, "start").catch((e) => notify.error(String(e)))}
                        >
                          <Play />
                        </IconAction>
                      )}
                      {proc.status === "online" && can("service.control") && (
                        <IconAction
                          label="Graceful reload"
                          onClick={() => act(proc, "reload").catch((e) => notify.error(String(e)))}
                        >
                          <RotateClockwise />
                        </IconAction>
                      )}
                      {can("destructive") && (
                        <>
                          <IconAction
                            label="Restart"
                            onClick={() =>
                              confirm({
                                title: "Restart application",
                                confirmLabel: "Restart",
                                description: (
                                  <p>
                                    <b>{proc.name}</b> restarts and briefly stops serving.
                                  </p>
                                ),
                                action: (c) => act(proc, "restart", c),
                              })
                            }
                          >
                            <RotateClockwise />
                          </IconAction>
                          <IconAction
                            label="Stop"
                            onClick={() =>
                              confirm({
                                title: "Stop application",
                                confirmLabel: "Stop",
                                description: (
                                  <p>
                                    <b>{proc.name}</b> stops until it is started again.
                                  </p>
                                ),
                                action: (c) => act(proc, "stop", c),
                              })
                            }
                          >
                            <StopCircle />
                          </IconAction>
                          <IconAction
                            label="Delete"
                            className="text-destructive"
                            onClick={() =>
                              confirm({
                                title: "Delete from PM2",
                                confirmLabel: "Delete",
                                description: (
                                  <p>
                                    Removes <b>{proc.name}</b> from PM2 entirely. Files on disk are
                                    untouched, but the process definition is gone.
                                  </p>
                                ),
                                action: async (c) => {
                                  await del(`/pm2/${encodeURIComponent(proc.name)}`, {
                                    confirm: c,
                                    query: { user: proc.daemonId, id: proc.id },
                                  })
                                  refresh()
                                },
                              })
                            }
                          >
                            <Trash />
                          </IconAction>
                        </>
                      )}
                    </RowActions>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </PanelBody>
      </Panel>
      <PM2LogSheet process={logsFor} onOpenChange={(o) => !o && setLogsFor(null)} />
      {dialog}
    </>
  )
}

function SystemdTab() {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const [filter, setFilter] = useState("")
  const [stateFilter, setStateFilter] = useState("all")
  const [journalFor, setJournalFor] = useState<string | null>(null)
  const { data, error, loading, refresh } = usePoll(
    (signal) => get<{ available: boolean; units: SystemdUnit[] }>("/systemd/", undefined, signal),
    10000,
  )

  const visible = useMemo(() => {
    let units = data?.units ?? []
    if (stateFilter !== "all") units = units.filter((u) => u.activeState === stateFilter)
    const needle = filter.toLowerCase()
    if (needle) {
      units = units.filter(
        (u) =>
          u.name.toLowerCase().includes(needle) || u.description.toLowerCase().includes(needle),
      )
    }
    return units
  }, [data, filter, stateFilter])

  if (loading) return <LoadingPanel />
  if (error) return <ErrorState error={error} />
  if (!data?.available)
    return <EmptyState icon={ListOrdered} title="systemd is not available on this host" />

  const act = async (unit: SystemdUnit, action: string, confirmText?: string) => {
    await post(`/systemd/${encodeURIComponent(unit.name)}/${action}`, undefined, {
      confirm: confirmText,
    })
    notify.success(`${unit.name} ${action}`)
    refresh()
  }

  const failed = (data.units ?? []).filter((u) => u.activeState === "failed").length

  return (
    <>
      <Panel>
        <PanelHeader
          title="systemd units"
          advanced
          actions={failed > 0 && <Status verdict="critical" label={`${failed} failed`} />}
        />
        <PanelToolbar>
          <SearchInput
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder="Filter units"
          />
          <Select value={stateFilter} onValueChange={setStateFilter}>
            <SelectTrigger size="sm" className="w-36">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">All states</SelectItem>
              <SelectItem value="active">Active</SelectItem>
              <SelectItem value="inactive">Inactive</SelectItem>
              <SelectItem value="failed">Failed</SelectItem>
            </SelectContent>
          </Select>
        </PanelToolbar>
        <PanelBody flush>
          <Table containerClassName="max-h-[calc(100svh-23rem)]">
            <TableHeader className={stickyTableHeader}>
              <TableRow>
                <TableHead className="w-full">Unit</TableHead>
                <TableHead>State</TableHead>
                <TableHead>Startup</TableHead>
                <TableHead className="w-px" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {visible.slice(0, 200).map((unit) => (
                <TableRow
                  key={unit.name}
                  className="group"
                  onActivate={() => setJournalFor(unit.name)}
                >
                  <TableCell>
                    <div className="max-w-[26rem] min-w-0">
                      <RowLink onClick={() => setJournalFor(unit.name)}>{unit.name}</RowLink>
                      <p className="truncate text-hint text-muted-foreground">{unit.description}</p>
                    </div>
                  </TableCell>
                  <TableCell>
                    <Status
                      state={unit.activeState}
                      label={`${unit.activeState} (${unit.subState})`}
                    />
                  </TableCell>
                  <TableCell>
                    <Tag>{unit.unitFileState || "unknown"}</Tag>
                  </TableCell>
                  <TableCell>
                    {/*
                      Always drawn, merely quiet until the row is hovered. These
                      actions own their column, so a reveal rule would draw an
                      empty column that sprouts buttons on hover — which reads
                      as a layout bug rather than an affordance.
                    */}
                    <DimActions>
                      {unit.activeState !== "active" && can("service.control") && (
                        <IconAction
                          label="Start"
                          onClick={() => act(unit, "start").catch((e) => notify.error(String(e)))}
                        >
                          <Play />
                        </IconAction>
                      )}
                      {can("destructive") && (
                        <>
                          <IconAction
                            label="Restart"
                            onClick={() =>
                              confirm({
                                title: "Restart unit",
                                confirmLabel: "Restart",
                                description: (
                                  <p>
                                    <b>{unit.name}</b> restarts, interrupting whatever it serves.
                                  </p>
                                ),
                                action: (c) => act(unit, "restart", c),
                              })
                            }
                          >
                            <RotateClockwise />
                          </IconAction>
                          {unit.activeState === "active" && (
                            <IconAction
                              label="Stop"
                              onClick={() =>
                                confirm({
                                  title: "Stop unit",
                                  confirmLabel: "Stop",
                                  description: (
                                    <p>
                                      <b>{unit.name}</b> stops until started again.
                                    </p>
                                  ),
                                  action: (c) => act(unit, "stop", c),
                                })
                              }
                            >
                              <StopCircle />
                            </IconAction>
                          )}
                        </>
                      )}
                      {can("system.admin") &&
                        ["enabled", "enabled-runtime", "disabled"].includes(unit.unitFileState) && (
                          <Button
                            size="xs"
                            variant="ghost"
                            className="text-muted-foreground"
                            onClick={() =>
                              act(unit, unit.enabled ? "disable" : "enable").catch((e) =>
                                notify.error(String(e)),
                              )
                            }
                          >
                            {unit.enabled ? "Disable" : "Enable"}
                          </Button>
                        )}
                    </DimActions>
                  </TableCell>
                </TableRow>
              ))}
              {visible.length === 0 && (
                <TableRow>
                  <TableCell colSpan={4} className="p-0">
                    <EmptyState icon={ListOrdered} title="No units match" />
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </PanelBody>
      </Panel>
      <UnitJournalSheet unit={journalFor} onOpenChange={(o) => !o && setJournalFor(null)} />
      {dialog}
    </>
  )
}

function CronTab() {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const [user, setUser] = useState("root")
  const [draft, setDraft] = useState<string | null>(null)

  const users = usePoll((signal) => get<string[]>("/cron/users", undefined, signal), 0)
  const crontab = usePoll(
    (signal) => get<Crontab>(`/cron/user/${encodeURIComponent(user)}`, undefined, signal),
    0,
    [user],
  )
  const system = usePoll((signal) => get<Crontab[]>("/cron/system", undefined, signal), 0)

  return (
    <>
      <div className="flex min-w-0 flex-col gap-4">
        <Panel>
          <PanelHeader
            title="User crontab"
            advanced
            actions={
              <Select
                value={user}
                onValueChange={(v) => {
                  setUser(v)
                  setDraft(null)
                }}
              >
                <SelectTrigger size="sm" className="w-44">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="root">root</SelectItem>
                  {users.data
                    ?.filter((u) => u !== "root")
                    .map((u) => (
                      <SelectItem key={u} value={u}>
                        {u}
                      </SelectItem>
                    ))}
                </SelectContent>
              </Select>
            }
          />

          {crontab.error && (
            <PanelBody>
              <ErrorState error={crontab.error} />
            </PanelBody>
          )}

          {crontab.data && draft === null && (
            <>
              {crontab.data.jobs.length === 0 ? (
                <PanelBody>
                  <EmptyState icon={Clock} title={`No cron jobs for ${user}`} />
                </PanelBody>
              ) : (
                <PanelBody flush>
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead className="w-44">Schedule</TableHead>
                        <TableHead className="w-full">Command</TableHead>
                        <TableHead className="w-24">State</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {crontab.data.jobs.map((job) => (
                        <TableRow key={job.line}>
                          <TableCell className="font-mono">{job.schedule}</TableCell>
                          <TableCell className="whitespace-normal">
                            <div className="font-mono text-xs break-all">{job.command}</div>
                            {job.comment && (
                              <p className="text-hint text-muted-foreground">{job.comment}</p>
                            )}
                          </TableCell>
                          <TableCell>
                            <Status
                              state={job.disabled ? "inactive" : "active"}
                              label={job.disabled ? "disabled" : "active"}
                            />
                          </TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                </PanelBody>
              )}
              {can("system.admin") && (
                <PanelFooter>
                  <Button variant="outline" size="sm" onClick={() => setDraft(crontab.data!.raw)}>
                    Edit crontab
                  </Button>
                </PanelFooter>
              )}
            </>
          )}

          {draft !== null && (
            <PanelBody className="space-y-3">
              <Textarea
                value={draft}
                onChange={(e) => setDraft(e.target.value)}
                className="min-h-64 font-mono text-xs"
                spellCheck={false}
              />
              <div className="flex gap-2">
                <Button
                  size="sm"
                  onClick={() =>
                    confirm({
                      title: "Replace crontab",
                      confirmLabel: "Save",
                      description: (
                        <p>
                          Replaces the entire crontab for <b>{user}</b>. Scheduled jobs start
                          running on the new schedule immediately.
                        </p>
                      ),
                      action: async (c) => {
                        await put(
                          `/cron/user/${encodeURIComponent(user)}`,
                          { content: draft },
                          { confirm: c },
                        )
                        setDraft(null)
                        crontab.refresh()
                      },
                    })
                  }
                >
                  Save
                </Button>
                <Button size="sm" variant="ghost" onClick={() => setDraft(null)}>
                  Cancel
                </Button>
              </div>
            </PanelBody>
          )}
        </Panel>

        <Panel>
          <PanelHeader title="System cron" />
          <PanelBody className="space-y-4">
            {system.data?.map((file) => (
              <div key={file.source} className="min-w-0 space-y-1.5">
                <p className="font-mono text-hint text-muted-foreground">{file.source}</p>
                {file.jobs.length === 0 ? (
                  <EmptyNote>No jobs.</EmptyNote>
                ) : (
                  <Well className="space-y-1 p-2">
                    {file.jobs.map((job, i) => (
                      <div
                        key={i}
                        className={cn(
                          "flex gap-3 text-hint",
                          // A commented-out schedule is real syntax, not a
                          // running job — /etc/crontab ships one as its own
                          // worked example. Dimming it keeps the two apart.
                          job.disabled && "opacity-55",
                        )}
                      >
                        <span className="w-32 shrink-0 text-muted-foreground">
                          {job.disabled && <span className="mr-1">#</span>}
                          {job.schedule}
                        </span>
                        {/* /etc/crontab and /etc/cron.d put the account between
                            the schedule and the command; a personal crontab
                            does not, so this column only appears when parsed. */}
                        {job.user && (
                          <span className="w-20 shrink-0 text-muted-foreground">{job.user}</span>
                        )}
                        <span className="break-all">{job.command}</span>
                      </div>
                    ))}
                  </Well>
                )}
              </div>
            ))}
            {!system.data?.length && <EmptyState icon={Clock} title="No system cron files" />}
          </PanelBody>
        </Panel>
      </div>
      {dialog}
    </>
  )
}
