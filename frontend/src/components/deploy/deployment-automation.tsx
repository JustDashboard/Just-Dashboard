"use client"

import { useState } from "react"
import { Bell, Clock, GitPullRequest, GitBranch, Plus } from "@/components/icons"
import { get, post } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type { DeploymentPreview, DeploymentSchedule, DeploymentTrigger } from "@/lib/types"
import { Group, Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { EmptyState, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Textarea } from "@/components/ui/textarea"

type NotificationChannel = {
  id: number
  name: string
  url: string
  events: string[]
  enabled: boolean
}

type Props = {
  projectID: number
  environmentID: number
  legacyHook?: string
  legacyEnabled: boolean
  normalized: boolean
}

export function DeploymentAutomation({
  projectID,
  environmentID,
  legacyHook,
  legacyEnabled,
  normalized,
}: Props) {
  const { can } = useAuth()
  const triggers = usePoll(
    (signal) =>
      get<DeploymentTrigger[]>(
        `/deploy/${projectID}/environments/${environmentID}/triggers`,
        undefined,
        signal,
      ),
    5000,
    [projectID, environmentID],
    { enabled: normalized },
  )
  const schedules = usePoll(
    (signal) =>
      get<DeploymentSchedule[]>(
        `/deploy/${projectID}/environments/${environmentID}/schedules`,
        undefined,
        signal,
      ),
    5000,
    [projectID, environmentID],
    { enabled: normalized },
  )
  const previews = usePoll(
    (signal) => get<DeploymentPreview[]>(`/deploy/${projectID}/previews`, undefined, signal),
    5000,
    [projectID],
    { enabled: normalized },
  )
  const notifications = usePoll(
    (signal) => get<NotificationChannel[]>("/deploy/notifications", undefined, signal),
    5000,
    [],
    { enabled: normalized },
  )
  const [showTrigger, setShowTrigger] = useState(false)
  const [provider, setProvider] = useState("github")
  const [name, setName] = useState("Deploy on push")
  const [repository, setRepository] = useState("")
  const [branch, setBranch] = useState("main")
  const [include, setInclude] = useState("")
  const [previewsEnabled, setPreviewsEnabled] = useState(false)
  const [saving, setSaving] = useState(false)
  const [newSecret, setNewSecret] = useState<{ url: string; secret: string }>()
  const [showSchedule, setShowSchedule] = useState(false)
  const [scheduleName, setScheduleName] = useState("Nightly deploy")
  const [expression, setExpression] = useState("0 3 * * *")
  const [timezone, setTimezone] = useState("UTC")
  const [scheduleAction, setScheduleAction] = useState("deploy")
  const [scheduleConfig, setScheduleConfig] = useState("{}")
  const [showNotification, setShowNotification] = useState(false)
  const [notificationName, setNotificationName] = useState("Deployment events")
  const [notificationURL, setNotificationURL] = useState("")
  const base = `/deploy/${projectID}/environments/${environmentID}`

  const saveTrigger = async () => {
    setSaving(true)
    try {
      const result = await post<{ trigger: DeploymentTrigger; secret: string }>(
        `${base}/triggers`,
        {
          name,
          kind: provider,
          provider,
          enabled: true,
          config: {
            repository,
            ref: branch,
            events: provider === "github" ? ["push", "pull_request"] : [],
            watchInclude: include
              .split("\n")
              .map((v) => v.trim())
              .filter(Boolean),
            preview: previewsEnabled,
            previewQuota: 5,
          },
        },
      )
      setNewSecret({
        url:
          provider === "generic_hook"
            ? `/api/v1/deploy/${projectID}/hooks/${result.trigger.id}`
            : `/api/v1/hooks/providers/${provider}/${result.trigger.hookId}`,
        secret: result.secret,
      })
      setShowTrigger(false)
      triggers.refresh()
      notify.success("Automation created")
    } catch (error) {
      notify.error("Could not create automation", error)
    } finally {
      setSaving(false)
    }
  }

  const saveSchedule = async () => {
    setSaving(true)
    try {
      const config = JSON.parse(scheduleConfig) as Record<string, unknown>
      await post(`${base}/schedules`, {
        name: scheduleName,
        expression,
        timezone,
        enabled: true,
        steps: [{ action: scheduleAction, config, required: true }],
      })
      setShowSchedule(false)
      schedules.refresh()
      notify.success("Schedule created")
    } catch (error) {
      notify.error("Could not create schedule", error)
    } finally {
      setSaving(false)
    }
  }

  const saveNotification = async () => {
    setSaving(true)
    try {
      const result = await post<{ channel: NotificationChannel; secret: string }>(
        "/deploy/notifications",
        { name: notificationName, url: notificationURL, events: ["run.finished"], enabled: true },
      )
      setNewSecret({ url: result.channel.url, secret: result.secret })
      setShowNotification(false)
      notifications.refresh()
      notify.success("Notification channel created")
    } catch (error) {
      notify.error("Could not create notification", error)
    } finally {
      setSaving(false)
    }
  }

  if (!normalized) return <LegacyAutomation hook={legacyHook} enabled={legacyEnabled} />
  return (
    <div className="grid min-w-0 gap-4 xl:grid-cols-2">
      <Panel className="xl:col-span-2">
        <PanelHeader
          icon={GitBranch}
          title="Source automations"
          actions={
            can("system.admin") && (
              <Button size="sm" onClick={() => setShowTrigger((v) => !v)}>
                <Plus className="size-3.5" /> Add automation
              </Button>
            )
          }
        />
        <PanelBody className="space-y-3">
          {newSecret && (
            <Notice title="Copy this secret now" tone="warning">
              <div className="mt-2 space-y-2">
                <Well className="break-all select-all">{newSecret.url}</Well>
                <Well className="break-all select-all">{newSecret.secret}</Well>
              </div>
            </Notice>
          )}
          {showTrigger && (
            <Group className="grid gap-4 sm:grid-cols-2" aria-busy={saving} tinted>
              <div className="space-y-1.5">
                <Label htmlFor="automation-name">Name</Label>
                <Input
                  id="automation-name"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                />
              </div>
              <div className="space-y-1.5">
                <Label>Provider</Label>
                <Select value={provider} onValueChange={setProvider}>
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {["github", "gitlab", "bitbucket", "gitea", "generic_hook"].map((item) => (
                      <SelectItem key={item} value={item}>
                        {item === "generic_hook"
                          ? "Generic signed hook"
                          : item[0].toUpperCase() + item.slice(1)}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="automation-repository">Repository</Label>
                <Input
                  id="automation-repository"
                  placeholder="owner/repository"
                  value={repository}
                  onChange={(e) => setRepository(e.target.value)}
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="automation-branch">Branch</Label>
                <Input
                  id="automation-branch"
                  value={branch}
                  onChange={(e) => setBranch(e.target.value)}
                />
              </div>
              <div className="space-y-1.5 sm:col-span-2">
                <Label htmlFor="automation-paths">
                  Watched paths{" "}
                  <span className="font-normal text-muted-foreground">(one glob per line)</span>
                </Label>
                <Input
                  id="automation-paths"
                  placeholder="services/api/**"
                  value={include}
                  onChange={(e) => setInclude(e.target.value)}
                />
              </div>
              <label className="flex min-h-11 items-center gap-2 text-xs sm:col-span-2">
                <Checkbox
                  checked={previewsEnabled}
                  onCheckedChange={(value) => setPreviewsEnabled(value === true)}
                />{" "}
                Create isolated environments for pull requests
              </label>
              <div className="flex gap-2 sm:col-span-2">
                <Button
                  disabled={
                    saving ||
                    !name.trim() ||
                    (provider !== "generic_hook" && (!repository.trim() || !branch.trim()))
                  }
                  onClick={saveTrigger}
                >
                  {saving ? "Creating…" : "Create automation"}
                </Button>
                <Button variant="ghost" onClick={() => setShowTrigger(false)}>
                  Cancel
                </Button>
              </div>
            </Group>
          )}
          {triggers.loading && !triggers.data ? (
            <LoadingPanel rows={3} />
          ) : triggers.error ? (
            <ErrorState error={triggers.error} />
          ) : (triggers.data?.length ?? 0) === 0 ? (
            <EmptyState
              icon={GitBranch}
              title="No source automations"
              description="Add a provider or generic signed hook. Manual deployments remain available."
              className="border-0 py-6"
            />
          ) : (
            <div className="divide-y divide-hairline">
              {triggers.data?.map((trigger) => (
                <div
                  key={trigger.id}
                  className="grid gap-2 py-3 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center"
                >
                  <div className="min-w-0">
                    <p className="text-body font-medium">{trigger.name}</p>
                    <p className="truncate text-xs text-muted-foreground">
                      {trigger.provider || trigger.kind} ·{" "}
                      {trigger.config.repository || "scoped hook"} ·{" "}
                      {trigger.config.ref || "any ref"}
                    </p>
                  </div>
                  <div className="flex items-center gap-3">
                    <Status
                      state={trigger.enabled ? "enabled" : "inactive"}
                      label={trigger.enabled ? "Enabled" : "Disabled"}
                    />
                    {trigger.lastStatus && (
                      <Status
                        verdict={trigger.lastStatus === "accepted" ? "ok" : "critical"}
                        label={trigger.lastStatus}
                      />
                    )}
                  </div>
                </div>
              ))}
            </div>
          )}
        </PanelBody>
      </Panel>
      <Panel>
        <PanelHeader
          icon={Clock}
          title="Scheduled actions"
          actions={
            can("system.admin") && (
              <Button
                size="xs"
                variant="outline"
                onClick={() => setShowSchedule((value) => !value)}
              >
                <Plus className="size-3" /> Add
              </Button>
            )
          }
        />
        <PanelBody className="space-y-3">
          {showSchedule && (
            <Group className="space-y-3" aria-busy={saving} tinted>
              <div className="space-y-1.5">
                <Label htmlFor="schedule-name">Name</Label>
                <Input
                  id="schedule-name"
                  value={scheduleName}
                  onChange={(event) => setScheduleName(event.target.value)}
                />
              </div>
              <div className="grid gap-3 sm:grid-cols-2">
                <div className="space-y-1.5">
                  <Label htmlFor="schedule-expression">Cron expression</Label>
                  <Input
                    id="schedule-expression"
                    className="font-mono"
                    value={expression}
                    onChange={(event) => setExpression(event.target.value)}
                  />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="schedule-timezone">IANA timezone</Label>
                  <Input
                    id="schedule-timezone"
                    value={timezone}
                    onChange={(event) => setTimezone(event.target.value)}
                  />
                </div>
              </div>
              <div className="space-y-1.5">
                <Label>Action</Label>
                <Select value={scheduleAction} onValueChange={setScheduleAction}>
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {["deploy", "restart", "backup", "container_command", "game_command"].map(
                      (action) => (
                        <SelectItem key={action} value={action}>
                          {action.replaceAll("_", " ")}
                        </SelectItem>
                      ),
                    )}
                  </SelectContent>
                </Select>
              </div>
              {!["deploy", "restart"].includes(scheduleAction) && (
                <div className="space-y-1.5">
                  <Label htmlFor="schedule-config">Action configuration</Label>
                  <Textarea
                    id="schedule-config"
                    className="min-h-20 font-mono text-xs"
                    value={scheduleConfig}
                    onChange={(event) => setScheduleConfig(event.target.value)}
                  />
                  <p className="text-hint text-muted-foreground">
                    Backup: {`{"jobId": 4}`}. Container:{" "}
                    {`{"containerId":"web","argv":["app","task"],"timeoutSeconds":300}`}.
                  </p>
                </div>
              )}
              <div className="flex gap-2">
                <Button
                  size="sm"
                  disabled={
                    saving || !scheduleName.trim() || !expression.trim() || !timezone.trim()
                  }
                  onClick={saveSchedule}
                >
                  {saving ? "Creating…" : "Create schedule"}
                </Button>
                <Button size="sm" variant="ghost" onClick={() => setShowSchedule(false)}>
                  Cancel
                </Button>
              </div>
            </Group>
          )}
          {schedules.loading && !schedules.data ? (
            <LoadingPanel rows={2} />
          ) : schedules.error ? (
            <ErrorState error={schedules.error} />
          ) : (schedules.data?.length ?? 0) === 0 ? (
            <EmptyState
              icon={Clock}
              title="No schedules"
              description="Create a timezone-aware action schedule for this environment."
              className="border-0 py-5"
            />
          ) : (
            <div className="space-y-3">
              {schedules.data?.map((schedule) => (
                <Group key={schedule.id}>
                  <div className="flex justify-between gap-3">
                    <p className="text-body font-medium">{schedule.name}</p>
                    <Status
                      state={schedule.enabled ? "enabled" : "stopped"}
                      label={schedule.enabled ? "Enabled" : "Paused"}
                    />
                  </div>
                  <p className="mt-1 font-mono text-hint text-muted-foreground">
                    {schedule.expression} · {schedule.timezone}
                  </p>
                  <p className="mt-1 text-xs text-muted-foreground">
                    {schedule.nextRunAt
                      ? `Next ${relativeTime(schedule.nextRunAt)}`
                      : "No next run"}{" "}
                    · {schedule.steps.map((step) => step.action.replaceAll("_", " ")).join(" → ")}
                  </p>
                </Group>
              ))}
            </div>
          )}
        </PanelBody>
      </Panel>
      <Panel>
        <PanelHeader
          icon={GitPullRequest}
          title="Preview environments"
        />
        <PanelBody>
          {previews.error ? (
            <ErrorState error={previews.error} />
          ) : (previews.data?.length ?? 0) === 0 ? (
            <EmptyState
              icon={GitPullRequest}
              title="No previews"
              description="An enabled preview automation creates one when a pull request opens."
              className="border-0 py-5"
            />
          ) : (
            <div className="space-y-2">
              {previews.data?.map((preview) => (
                <Group key={preview.id} className="flex items-center justify-between">
                  <div>
                    <p className="font-mono text-xs">{preview.environmentSlug}</p>
                    <p className="text-hint text-muted-foreground">
                      PR {preview.providerRef} · updated {relativeTime(preview.updatedAt)}
                    </p>
                  </div>
                  <Status
                    state={preview.state === "open" ? "open" : "closed"}
                    label={preview.state}
                  />
                </Group>
              ))}
            </div>
          )}
        </PanelBody>
      </Panel>
      <Panel className="xl:col-span-2">
        <PanelHeader
          icon={Bell}
          title="Notifications"
          actions={
            can("system.admin") && (
              <Button
                size="xs"
                variant="outline"
                onClick={() => setShowNotification((value) => !value)}
              >
                <Plus className="size-3" /> Add channel
              </Button>
            )
          }
        />
        <PanelBody className="space-y-3">
          {showNotification && (
            <Group className="grid gap-3 sm:grid-cols-2" aria-busy={saving} tinted>
              <div className="space-y-1.5">
                <Label htmlFor="notification-name">Name</Label>
                <Input
                  id="notification-name"
                  value={notificationName}
                  onChange={(event) => setNotificationName(event.target.value)}
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="notification-url">HTTPS endpoint</Label>
                <Input
                  id="notification-url"
                  type="url"
                  placeholder="https://hooks.example.com/deploy"
                  value={notificationURL}
                  onChange={(event) => setNotificationURL(event.target.value)}
                />
              </div>
              <div className="flex gap-2 sm:col-span-2">
                <Button
                  size="sm"
                  disabled={saving || !notificationName.trim() || !notificationURL.trim()}
                  onClick={saveNotification}
                >
                  {saving ? "Creating…" : "Create channel"}
                </Button>
                <Button size="sm" variant="ghost" onClick={() => setShowNotification(false)}>
                  Cancel
                </Button>
              </div>
            </Group>
          )}
          {notifications.error ? (
            <ErrorState error={notifications.error} />
          ) : (notifications.data?.length ?? 0) === 0 ? (
            <EmptyState
              icon={Bell}
              title="No notification channels"
              description="Add an endpoint to receive signed deployment outcomes."
              className="border-0 py-5"
            />
          ) : (
            <div className="divide-y divide-hairline">
              {notifications.data?.map((channel) => (
                <div
                  key={channel.id}
                  className="flex min-w-0 items-center justify-between gap-3 py-3"
                >
                  <div className="min-w-0">
                    <p className="text-body font-medium">{channel.name}</p>
                    <p className="truncate text-xs text-muted-foreground">{channel.url}</p>
                  </div>
                  <Status
                    state={channel.enabled ? "enabled" : "stopped"}
                    label={channel.enabled ? "Enabled" : "Paused"}
                  />
                </div>
              ))}
            </div>
          )}
        </PanelBody>
      </Panel>
    </div>
  )
}

function LegacyAutomation({ hook, enabled }: { hook?: string; enabled: boolean }) {
  return (
    <Panel>
      <PanelHeader icon={GitBranch} title="Legacy deployment hook" />
      <PanelBody className="space-y-3">
        <Status state={enabled ? "enabled" : "inactive"} label={enabled ? "Enabled" : "Disabled"} />
        {hook ? (
          <Well className="break-all select-all">{hook}</Well>
        ) : (
          <p className="text-xs text-muted-foreground">No hook URL is available.</p>
        )}
        <p className="text-xs leading-relaxed text-muted-foreground">
          Legacy HMAC behavior remains byte-for-byte compatible while new automations stay
          environment scoped.
        </p>
      </PanelBody>
    </Panel>
  )
}
