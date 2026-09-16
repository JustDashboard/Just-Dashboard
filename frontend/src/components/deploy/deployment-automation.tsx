"use client"

import { useState } from "react"
import { Clock, GitPullRequest, GitHubMark, Plus } from "@/components/icons"
import { get, post } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type {
  DeploymentPreview,
  DeploymentPreviewApproval,
  DeploymentSchedule,
  DeploymentTrigger,
} from "@/lib/types"
import { Group, Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { EmptyState, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { SidePanel } from "@/components/side-panel"
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
import { DeploymentGitStatus } from "@/components/deploy/deployment-git-status"
import { DeploymentNotifications } from "@/components/deploy/deployment-notifications"
import { NormalizedVariablesTab } from "@/components/deploy/deployment-configuration"
import { ConfirmDialog, type ConfirmRequest } from "@/components/confirm-dialog"

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
  const approvals = usePoll(
    (signal) =>
      get<DeploymentPreviewApproval[]>(
        `/deploy/${projectID}/previews/approvals`,
        undefined,
        signal,
      ),
    5000,
    [projectID],
    { enabled: normalized },
  )
  const [previewVariables, setPreviewVariables] = useState<DeploymentPreview>()
  const [previewConfirm, setPreviewConfirm] = useState<ConfirmRequest | null>(null)
  const [deployingPreview, setDeployingPreview] = useState<number>()
  const [area, setArea] = useState("source")
  const [showTrigger, setShowTrigger] = useState(false)
  const [provider, setProvider] = useState("github")
  const [name, setName] = useState("Deployment webhook")
  const [repository, setRepository] = useState("")
  const [branch, setBranch] = useState("main")
  const [include, setInclude] = useState("")
  const [previewsEnabled, setPreviewsEnabled] = useState(false)
  const [previewDomain, setPreviewDomain] = useState("")
  const [saving, setSaving] = useState(false)
  const [newSecret, setNewSecret] = useState<{ url: string; secret: string }>()
  const [showSchedule, setShowSchedule] = useState(false)
  const [scheduleName, setScheduleName] = useState("Nightly deploy")
  const [expression, setExpression] = useState("0 3 * * *")
  const [timezone, setTimezone] = useState("UTC")
  const [scheduleAction, setScheduleAction] = useState("deploy")
  const [scheduleConfig, setScheduleConfig] = useState("{}")
  const base = `/deploy/${projectID}/environments/${environmentID}`

  const approvePreview = (approval: DeploymentPreviewApproval) => {
    let configuredPreview: DeploymentPreview | undefined
    setPreviewConfirm({
      title: `Approve PR ${approval.providerRef}`,
      description: (
        <div className="space-y-3">
          <p>
            Allow this revision from {approval.author || "an unknown author"} to build and run on
            your server. Review its code before approving.
          </p>
          <Well className="font-mono text-xs break-all">{approval.revision}</Well>
          <p>
            The preview starts with empty storage and its own variables. Add any preview settings
            before deploying. Future revisions require another approval.
          </p>
        </div>
      ),
      confirmLabel: "Approve and configure",
      action: async () => {
        const result = await post<{ preview: DeploymentPreview }>(
          `/deploy/${projectID}/previews/approvals/${approval.id}/approve`,
          { revision: approval.revision, deploy: false },
        )
        approvals.refresh()
        previews.refresh()
        configuredPreview = result.preview
      },
      onDone: () => setPreviewVariables(configuredPreview),
    })
  }

  const deployPreview = async (preview: DeploymentPreview) => {
    setDeployingPreview(preview.id)
    try {
      await post(`/deploy/${projectID}/environments/${preview.environmentId}/runs`, {
        operation: "deploy",
      })
      notify.success("Approved preview deployment queued")
      previews.refresh()
    } catch (error) {
      notify.error("Could not deploy preview", error)
    } finally {
      setDeployingPreview(undefined)
    }
  }

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
            watchInclude: include.trim()
              ? include
                  .split("\n")
                  .map((v) => v.trim())
                  .filter(Boolean)
              : undefined,
            preview: previewsEnabled,
            previewQuota: 5,
            previewDomain: previewsEnabled ? previewDomain.trim() : undefined,
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

  if (!normalized) return <LegacyAutomation hook={legacyHook} enabled={legacyEnabled} />
  return (
    <div className="min-w-0 space-y-4">
      <DeploymentGitStatus projectID={projectID} environmentID={environmentID} />
      <div role="group" aria-label="Automation sections" className="flex flex-wrap gap-2">
        {[
          ["source", "Git & webhooks"],
          ["schedules", "Schedules"],
          ["previews", "Preview environments"],
          ["notifications", "Notifications"],
        ].map(([key, label]) => (
          <Button
            key={key}
            size="sm"
            variant={area === key ? "secondary" : "ghost"}
            aria-pressed={area === key}
            onClick={() => setArea(key)}
          >
            {label}
          </Button>
        ))}
      </div>
      {newSecret && (
        <Notice title="Copy this secret now" tone="warning">
          <div className="mt-2 space-y-2">
            <Well className="break-all select-all">{newSecret.url}</Well>
            <Well className="break-all select-all">{newSecret.secret}</Well>
          </div>
        </Notice>
      )}
      {area === "source" && (
        <Panel className="xl:col-span-2">
          <PanelHeader
            title="Additional webhooks"
            actions={
              can("system.admin") && (
                <Button size="sm" onClick={() => setShowTrigger((v) => !v)}>
                  <Plus className="size-3.5" /> Add webhook
                </Button>
              )
            }
          />
          <PanelBody className="space-y-3">
            <SidePanel
              open={showTrigger}
              onOpenChange={setShowTrigger}
              title="Add webhook"
              description="Add an integration or pull request preview hook. Production branch deployments are automatic."
              width="md"
            >
              <div className="grid gap-4 sm:grid-cols-2" aria-busy={saving}>
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
                  <Textarea
                    id="automation-paths"
                    placeholder="services/api/**"
                    value={include}
                    onChange={(e) => setInclude(e.target.value)}
                  />
                  <p className="text-xs text-muted-foreground">
                    Updates the shared filters for branch polling and all push webhooks. Leave empty
                    to keep the current policy.
                  </p>
                </div>
                <label className="flex min-h-11 items-center gap-2 text-xs sm:col-span-2">
                  <Checkbox
                    checked={previewsEnabled}
                    onCheckedChange={(value) => setPreviewsEnabled(value === true)}
                  />{" "}
                  Create isolated environments for pull requests
                </label>
                {previewsEnabled && (
                  <Notice title="Preview policy" tone="default" className="sm:col-span-2">
                    Each pull request revision needs administrator approval. Preview variables and
                    storage are separate from production. Container previews have a dedicated
                    network; Compose and workloads needing host access require a separate plan.
                  </Notice>
                )}
                {previewsEnabled && (
                  <div className="space-y-1.5 sm:col-span-2">
                    <Label htmlFor="preview-domain">Preview domain pattern (optional)</Label>
                    <Input
                      id="preview-domain"
                      placeholder="pr-{number}.example.com"
                      value={previewDomain}
                      onChange={(event) => setPreviewDomain(event.target.value)}
                    />
                    <p className="text-hint text-muted-foreground">
                      Use {"{number}"} for the pull request number and point its DNS to this server.
                    </p>
                  </div>
                )}
                <div className="flex gap-2 sm:col-span-2">
                  <Button
                    disabled={
                      saving ||
                      !name.trim() ||
                      (provider !== "generic_hook" && (!repository.trim() || !branch.trim()))
                    }
                    onClick={saveTrigger}
                  >
                    {saving ? "Creating…" : "Create webhook"}
                  </Button>
                  <Button variant="ghost" onClick={() => setShowTrigger(false)}>
                    Cancel
                  </Button>
                </div>
              </div>
            </SidePanel>
            {triggers.loading && !triggers.data ? (
              <LoadingPanel rows={3} />
            ) : triggers.error ? (
              <ErrorState error={triggers.error} />
            ) : (triggers.data?.length ?? 0) === 0 ? (
              <EmptyState
                icon={GitHubMark}
                title="No additional webhooks"
                description="Git branch deployments work automatically. Add a webhook only for another integration or pull request previews."
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
      )}
      {area === "schedules" && (
        <Panel>
          <PanelHeader
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
            <SidePanel
              open={showSchedule}
              onOpenChange={setShowSchedule}
              title="Schedule an action"
              description="Configure this project automation."
              width="md"
            >
              <div className="space-y-3" aria-busy={saving}>
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
              </div>
            </SidePanel>
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
      )}
      {area === "previews" && (
        <Panel>
          <PanelHeader title="Preview environments" />
          <PanelBody className="space-y-4">
            {approvals.error && <ErrorState error={approvals.error} />}
            {approvals.data
              ?.filter((approval) => approval.state === "pending" || !approval.configured)
              .map((approval) => (
                <Group
                  key={approval.id}
                  className="flex flex-wrap items-center justify-between gap-3"
                >
                  <div className="min-w-0">
                    <p className="text-xs font-medium">
                      PR {approval.providerRef} ·{" "}
                      {approval.state === "pending" ? "Awaiting approval" : "Setup incomplete"}
                    </p>
                    <p className="text-hint break-all text-muted-foreground">
                      {approval.author || "Unknown author"} ·{" "}
                      {approval.headRepository || approval.repository}
                    </p>
                    <p className="font-mono text-xs break-all">{approval.revision.slice(0, 12)}</p>
                  </div>
                  {can("system.admin") && (
                    <Button size="sm" onClick={() => approvePreview(approval)}>
                      Review revision
                    </Button>
                  )}
                </Group>
              ))}
            {previews.error ? (
              <ErrorState error={previews.error} />
            ) : (previews.data?.length ?? 0) === 0 ? (
              <EmptyState
                icon={GitPullRequest}
                title="No previews"
                description="Pull requests appear for review above. Approve a revision, configure its variables, then deploy."
                className="border-0 py-5"
              />
            ) : (
              <div className="space-y-2">
                {previews.data?.map((preview) => (
                  <Group
                    key={preview.id}
                    className="flex flex-wrap items-center justify-between gap-3"
                  >
                    <div>
                      <p className="font-mono text-xs">{preview.environmentSlug}</p>
                      <p className="text-hint text-muted-foreground">
                        PR {preview.providerRef} · updated {relativeTime(preview.updatedAt)}
                      </p>
                      {preview.isolationStatus === "pending" && (
                        <p className="mt-2 max-w-prose text-xs text-muted-foreground">
                          This older preview may share production resources. Deployments are blocked
                          while its containers and route are isolated. Cleanup retries
                          automatically; check Docker and proxy availability if it remains blocked.
                        </p>
                      )}
                      {preview.isolationStatus === "quarantined" && (
                        <p className="mt-2 max-w-prose text-xs text-muted-foreground">
                          The previous runtime is stopped and stored data is retained. Redeliver the
                          pull request event, approve its revision, and configure preview settings
                          before deploying again.
                        </p>
                      )}
                    </div>
                    <Status
                      state={
                        preview.isolationStatus === "pending"
                          ? "failed"
                          : preview.isolationStatus === "quarantined"
                            ? "stopped"
                            : preview.state
                      }
                      label={
                        preview.isolationStatus === "pending"
                          ? "Isolation incomplete"
                          : preview.isolationStatus === "quarantined"
                            ? "Stopped for isolation"
                            : preview.state
                      }
                    />
                    {preview.state === "open" && (
                      <div className="flex flex-wrap gap-2">
                        {can("system.admin") && (
                          <Button
                            size="sm"
                            variant="outline"
                            onClick={() => setPreviewVariables(preview)}
                          >
                            Variables
                          </Button>
                        )}
                        {can("service.control") && (
                          <Button
                            size="sm"
                            disabled={
                              deployingPreview !== undefined ||
                              preview.isolationStatus === "pending" ||
                              preview.isolationStatus === "quarantined"
                            }
                            onClick={() => deployPreview(preview)}
                          >
                            {deployingPreview === preview.id ? "Queuing…" : "Deploy preview"}
                          </Button>
                        )}
                      </div>
                    )}
                  </Group>
                ))}
              </div>
            )}
          </PanelBody>
        </Panel>
      )}
      <ConfirmDialog
        request={previewConfirm}
        onOpenChange={(open) => !open && setPreviewConfirm(null)}
      />
      <SidePanel
        open={previewVariables !== undefined}
        onOpenChange={(open) => !open && setPreviewVariables(undefined)}
        title={`Preview ${previewVariables?.providerRef ?? ""} variables`}
        description="Configure values used only by this preview before deploying."
        width="lg"
      >
        {previewVariables && (
          <NormalizedVariablesTab
            projectID={projectID}
            environmentID={previewVariables.environmentId}
          />
        )}
      </SidePanel>
      {area === "notifications" && <DeploymentNotifications enabled={normalized} />}
    </div>
  )
}

function LegacyAutomation({ hook, enabled }: { hook?: string; enabled: boolean }) {
  return (
    <Panel>
      <PanelHeader title="Legacy deployment hook" />
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
