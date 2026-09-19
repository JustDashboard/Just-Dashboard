"use client"

import { useState } from "react"
import { useSessionState } from "@/lib/view-state"
import { Logs, Pause, Play, Plus, Trash } from "@/components/icons"
import { del, get, post, put } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type { BackupJob, DeploymentEngineRun, DeploymentSchedule } from "@/lib/types"
import { Field } from "@/components/form"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import { Row, RowList } from "@/components/row-list"
import { Status } from "@/components/status-dot"
import { SidePanel } from "@/components/side-panel"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Textarea } from "@/components/ui/textarea"
import { useConfirm } from "@/components/confirm-dialog"
import { VerbActions, type Verb } from "@/components/verbs"
import { SettingCard } from "@/components/deploy/settings/setting-card"
import {
  RunStatus,
  formatDuration,
  humanize,
  runDurationSeconds,
  runTitle,
} from "@/components/deploy/vocabulary"

// "game_command" is not offered: the schedule dispatcher
// (handlers_deploy_automation.go) always answers `game_console_unavailable`
// for it, so every run of a schedule built with it would report failed.
const ACTIONS = ["deploy", "restart", "backup", "container_command"] as const
type ScheduleAction = (typeof ACTIONS)[number]

export function Schedules({
  projectId,
  environmentId,
}: {
  projectId: number
  environmentId: number
}) {
  const { can } = useAuth()
  const canAdmin = can("system.admin")
  const { confirm, dialog } = useConfirm()
  const base = `/deploy/${projectId}/environments/${environmentId}`
  const schedules = usePoll(
    (signal) => get<DeploymentSchedule[]>(`${base}/schedules`, undefined, signal),
    5000,
    [projectId, environmentId],
  )

  // The new-schedule form is kept for the tab until it is created.
  const draft = `deploy.${projectId}.${environmentId}.schedules.new`
  const [open, setOpen] = useSessionState(`${draft}.open`, false)
  const [name, setName] = useSessionState(`${draft}.name`, "Nightly deploy")
  const [expression, setExpression] = useSessionState(`${draft}.expression`, "0 3 * * *")
  const [timezone, setTimezone] = useSessionState(`${draft}.timezone`, "UTC")
  const [action, setAction] = useSessionState<ScheduleAction>(`${draft}.action`, "deploy")
  const [backupJobId, setBackupJobId] = useSessionState(`${draft}.backupJob`, "")
  const [containerId, setContainerId] = useSessionState(`${draft}.container`, "")
  const [argv, setArgv] = useSessionState(`${draft}.argv`, "")
  const [timeoutSeconds, setTimeoutSeconds] = useSessionState(`${draft}.timeout`, "")
  const [saving, setSaving] = useState(false)
  const [runsFor, setRunsFor] = useState<DeploymentSchedule>()

  const backupJobs = usePoll((signal) => get<BackupJob[]>("/backups/", undefined, signal), 0, [], {
    enabled: action === "backup",
  })

  const runsQuery = usePoll(
    (signal) =>
      get<{ runs: DeploymentEngineRun[] }>(
        `${base}/schedules/${runsFor?.id}/runs`,
        undefined,
        signal,
      ),
    0,
    [projectId, environmentId, runsFor?.id],
    { enabled: runsFor !== undefined },
  )

  const resetForm = () => {
    setName("Nightly deploy")
    setExpression("0 3 * * *")
    setTimezone("UTC")
    setAction("deploy")
    setBackupJobId("")
    setContainerId("")
    setArgv("")
    setTimeoutSeconds("")
  }

  const configFor = (): Record<string, unknown> => {
    const timeout = timeoutSeconds ? Number(timeoutSeconds) : undefined
    switch (action) {
      case "backup":
        return { jobId: Number(backupJobId), timeoutSeconds: timeout }
      case "container_command":
        return {
          containerId,
          argv: argv
            .split("\n")
            .map((line) => line.trim())
            .filter(Boolean),
          timeoutSeconds: timeout,
        }
      default:
        return {}
    }
  }

  const createSchedule = async () => {
    setSaving(true)
    try {
      await post(`${base}/schedules`, {
        name,
        expression,
        timezone,
        enabled: true,
        steps: [{ action, config: configFor(), required: true }],
      })
      setOpen(false)
      resetForm()
      schedules.refresh()
      notify.success("Schedule created")
    } catch (error) {
      notify.error("Could not create schedule", error)
    } finally {
      setSaving(false)
    }
  }

  const toggleSchedule = async (schedule: DeploymentSchedule) => {
    try {
      await put(`${base}/schedules/${schedule.id}`, {
        name: schedule.name,
        expression: schedule.expression,
        timezone: schedule.timezone,
        enabled: !schedule.enabled,
        steps: schedule.steps,
      })
      schedules.refresh()
      notify.success(schedule.enabled ? "Schedule paused" : "Schedule enabled")
    } catch (error) {
      notify.error("Could not update schedule", error)
    }
  }

  const removeSchedule = (schedule: DeploymentSchedule) =>
    confirm({
      title: `Remove ${schedule.name}`,
      confirmLabel: "Remove schedule",
      description: "This schedule stops running immediately.",
      action: async () => {
        await del(`${base}/schedules/${schedule.id}`)
      },
      onDone: () => schedules.refresh(),
    })

  const invalid =
    !name.trim() || !expression.trim() || !timezone.trim() || (action === "backup" && !backupJobId)

  return (
    <>
      <SettingCard
        title="Schedules"
        actions={
          canAdmin && (
            <Button size="sm" variant="outline" onClick={() => setOpen(true)}>
              <Plus className="size-3.5" /> Add schedule
            </Button>
          )
        }
      >
        {schedules.loading && !schedules.data ? (
          <LoadingRows rows={2} />
        ) : schedules.error ? (
          <ErrorState error={schedules.error} onRetry={schedules.refresh} />
        ) : (schedules.data?.length ?? 0) === 0 ? (
          <EmptyNote>No schedules. Add a timezone-aware action for this environment.</EmptyNote>
        ) : (
          <ul aria-label="Schedules" className="divide-y divide-hairline">
            {schedules.data?.map((schedule) => {
              const verbs: Verb[] = [
                {
                  key: "runs",
                  label: "Runs",
                  detail: "The runs this schedule has produced.",
                  icon: Logs,
                  inline: true,
                  run: () => setRunsFor(schedule),
                },
                {
                  key: "toggle",
                  label: schedule.enabled ? "Pause" : "Enable",
                  detail: schedule.enabled
                    ? "Stop firing until re-enabled."
                    : "Start firing on its schedule again.",
                  icon: schedule.enabled ? Pause : Play,
                  inline: true,
                  run: () => void toggleSchedule(schedule),
                },
                {
                  key: "remove",
                  label: "Remove",
                  detail: "Delete this schedule.",
                  icon: Trash,
                  danger: true,
                  run: () => removeSchedule(schedule),
                },
              ]
              return (
                <li key={schedule.id} className="min-w-0 py-3 first:pt-0 last:pb-0">
                  <div className="flex min-w-0 flex-wrap items-center justify-between gap-3">
                    <p className="truncate text-body font-medium">{schedule.name}</p>
                    <div className="flex shrink-0 items-center gap-3">
                      <Status
                        tone={schedule.enabled ? "running" : "stopped"}
                        label={schedule.enabled ? "Enabled" : "Paused"}
                      />
                      {canAdmin && (
                        <VerbActions verbs={verbs} menuLabel={`${schedule.name} actions`} />
                      )}
                    </div>
                  </div>
                  <p className="mt-0.5 font-mono text-hint text-muted-foreground">
                    {schedule.expression} · {schedule.timezone}
                  </p>
                  <p className="text-hint text-muted-foreground">
                    {schedule.nextRunAt
                      ? `Next ${relativeTime(schedule.nextRunAt)}`
                      : "No next run"}{" "}
                    · {schedule.steps.map((step) => humanize(step.action)).join(" → ")}
                  </p>
                </li>
              )
            })}
          </ul>
        )}
      </SettingCard>

      <SidePanel
        open={open}
        onOpenChange={setOpen}
        title="Add schedule"
        description="Run an action on a cron schedule, in a named timezone."
        width="md"
        footer={
          <>
            <Button variant="outline" onClick={() => setOpen(false)} disabled={saving}>
              Cancel
            </Button>
            <Button onClick={createSchedule} disabled={invalid} pending={saving}>
              Create schedule
            </Button>
          </>
        }
      >
        <div className="space-y-4" aria-busy={saving}>
          <Field label="Name" htmlFor="schedule-name">
            <Input id="schedule-name" value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="Cron expression" htmlFor="schedule-expression">
              <Input
                id="schedule-expression"
                className="font-mono"
                value={expression}
                onChange={(e) => setExpression(e.target.value)}
              />
            </Field>
            <Field label="Timezone" htmlFor="schedule-timezone">
              <Input
                id="schedule-timezone"
                value={timezone}
                onChange={(e) => setTimezone(e.target.value)}
              />
            </Field>
          </div>
          <Field label="Action" htmlFor="schedule-action">
            <Select value={action} onValueChange={(value: ScheduleAction) => setAction(value)}>
              <SelectTrigger id="schedule-action" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {ACTIONS.map((item) => (
                  <SelectItem key={item} value={item}>
                    {humanize(item)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
          {action === "backup" && (
            <Field
              label="Backup job"
              htmlFor="schedule-backup-job"
              error={!backupJobId ? "Choose a backup job." : undefined}
            >
              <Select
                value={backupJobId}
                onValueChange={setBackupJobId}
                disabled={backupJobs.loading}
              >
                <SelectTrigger id="schedule-backup-job" className="w-full">
                  <SelectValue placeholder={backupJobs.loading ? "Loading…" : "Choose a job"} />
                </SelectTrigger>
                <SelectContent>
                  {backupJobs.data?.map((job) => (
                    <SelectItem key={job.id} value={String(job.id)}>
                      {job.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
          )}
          {action === "container_command" && (
            <>
              <Field label="Container ID" htmlFor="schedule-container">
                <Input
                  id="schedule-container"
                  className="font-mono"
                  value={containerId}
                  onChange={(e) => setContainerId(e.target.value)}
                />
              </Field>
              <Field
                label="Command"
                htmlFor="schedule-argv"
                hint="One argument per line: the executable, then each argument."
              >
                <Textarea
                  id="schedule-argv"
                  className="font-mono"
                  value={argv}
                  onChange={(e) => setArgv(e.target.value)}
                />
              </Field>
              <Field label="Timeout (seconds)" htmlFor="schedule-timeout">
                <Input
                  id="schedule-timeout"
                  type="number"
                  min={1}
                  className="w-32"
                  value={timeoutSeconds}
                  onChange={(e) => setTimeoutSeconds(e.target.value)}
                />
              </Field>
            </>
          )}
        </div>
      </SidePanel>

      <SidePanel
        open={runsFor !== undefined}
        onOpenChange={(next) => !next && setRunsFor(undefined)}
        title={`${runsFor?.name ?? "Schedule"} runs`}
        description="The runs this schedule has produced."
        width="md"
      >
        {runsQuery.loading && !runsQuery.data ? (
          <LoadingRows rows={4} />
        ) : runsQuery.error ? (
          <ErrorState error={runsQuery.error} onRetry={runsQuery.refresh} />
        ) : (runsQuery.data?.runs.length ?? 0) === 0 ? (
          <EmptyNote>No runs yet.</EmptyNote>
        ) : (
          <RowList aria-label="Schedule runs">
            {runsQuery.data?.runs.map((run) => (
              <Row
                key={run.id}
                href={`/deploy/${projectId}/runs/${run.id}`}
                leading={<RunStatus state={run.state} />}
                title={runTitle(run)}
                subtitle={relativeTime(run.requestedAt)}
                trailing={
                  <span className="numeric text-hint text-muted-foreground">
                    {formatDuration(runDurationSeconds(run))}
                  </span>
                }
              />
            ))}
          </RowList>
        )}
      </SidePanel>
      {dialog}
    </>
  )
}
