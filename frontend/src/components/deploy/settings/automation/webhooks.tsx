"use client"

import { useState } from "react"
import Link from "next/link"
import { Key, Logs, Play, Plus, StopCircle, Trash } from "@/components/icons"
import { API_BASE, del, get, post, put } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type { DeploymentTrigger, DeploymentTriggerDelivery } from "@/lib/types"
import { Field, OptionRow } from "@/components/form"
import { Well } from "@/components/panel"
import { EmptyNote, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Row, RowList } from "@/components/row-list"
import { Status, type DotTone } from "@/components/status-dot"
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
import { humanize } from "@/components/deploy/vocabulary"
import { useGitHubApp } from "@/hooks/use-github"

const PROVIDERS = ["github", "gitlab", "bitbucket", "gitea", "generic_hook"] as const

function providerLabel(provider: string) {
  return provider === "generic_hook" ? "Generic signed hook" : humanize(provider)
}

/** How a delivery's decision reads: what happened, and how alarmed to be about it. */
const DECISIONS: Record<string, { label: string; tone: DotTone }> = {
  accepted: { label: "Accepted", tone: "running" },
  suppressed: { label: "Ignored", tone: "stopped" },
  rejected: { label: "Refused", tone: "danger" },
}

function deliveryDecision(decision: string) {
  return DECISIONS[decision] ?? { label: humanize(decision), tone: "notice" as DotTone }
}

/** Where a delivery lands, for the one-time secret notice after creation. */
function hookUrlFor(projectId: number, trigger: DeploymentTrigger) {
  return trigger.kind === "generic_hook" || trigger.kind === "api"
    ? `${API_BASE}/deploy/${projectId}/hooks/${trigger.id}`
    : `${API_BASE}/hooks/providers/${trigger.provider ?? trigger.kind}/${trigger.hookId}`
}

export function Webhooks({
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
  const triggers = usePoll(
    (signal) => get<DeploymentTrigger[]>(`${base}/triggers`, undefined, signal),
    5000,
    [projectId, environmentId],
  )

  const [open, setOpen] = useState(false)
  const [name, setName] = useState("Deployment webhook")
  const [provider, setProvider] = useState<(typeof PROVIDERS)[number]>("github")
  const [repository, setRepository] = useState("")
  const [branch, setBranch] = useState("main")
  const [watchedPaths, setWatchedPaths] = useState("")
  const [previewsEnabled, setPreviewsEnabled] = useState(false)
  const [previewDomain, setPreviewDomain] = useState("")
  // With the App connected, a GitHub trigger needs nothing configured on
  // GitHub: the App's own webhook already reaches this dashboard.
  const githubApp = useGitHubApp()
  const appAvailable = Boolean(githubApp.data?.configured)
  const [viaApp, setViaApp] = useState(true)
  const [connected, setConnected] = useState<string>()
  const [saving, setSaving] = useState(false)
  const [newSecret, setNewSecret] = useState<{ url: string; secret: string }>()
  const [rotated, setRotated] = useState<{ name: string; secret: string }>()
  const [deliveriesTrigger, setDeliveriesTrigger] = useState<DeploymentTrigger>()

  const deliveries = usePoll(
    (signal) =>
      get<DeploymentTriggerDelivery[]>(
        `${base}/triggers/${deliveriesTrigger?.id}/deliveries`,
        undefined,
        signal,
      ),
    0,
    [deliveriesTrigger?.id],
    { enabled: Boolean(deliveriesTrigger) },
  )

  const resetForm = () => {
    setName("Deployment webhook")
    setProvider("github")
    setRepository("")
    setBranch("main")
    setWatchedPaths("")
    setPreviewsEnabled(false)
    setPreviewDomain("")
    setViaApp(true)
  }

  const createTrigger = async () => {
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
            repository: provider === "generic_hook" ? undefined : repository,
            ref: provider === "generic_hook" ? undefined : branch,
            watchInclude: watchedPaths.trim()
              ? watchedPaths
                  .split("\n")
                  .map((line) => line.trim())
                  .filter(Boolean)
              : undefined,
            preview: previewsEnabled,
            previewDomain: previewsEnabled ? previewDomain.trim() || undefined : undefined,
            delivery: provider === "github" && appAvailable && viaApp ? "app" : undefined,
          },
        },
      )
      if (result.trigger.config.delivery === "app") {
        setConnected(result.trigger.name)
      } else {
        setNewSecret({ url: hookUrlFor(projectId, result.trigger), secret: result.secret })
      }
      setOpen(false)
      resetForm()
      triggers.refresh()
      notify.success("Webhook created")
    } catch (error) {
      notify.error("Could not create webhook", error)
    } finally {
      setSaving(false)
    }
  }

  const toggleTrigger = async (trigger: DeploymentTrigger) => {
    try {
      await put(`${base}/triggers/${trigger.id}`, {
        name: trigger.name,
        kind: trigger.kind,
        provider: trigger.provider,
        config: trigger.config,
        enabled: !trigger.enabled,
      })
      triggers.refresh()
      notify.success(trigger.enabled ? "Webhook disabled" : "Webhook enabled")
    } catch (error) {
      notify.error("Could not update webhook", error)
    }
  }

  const removeTrigger = (trigger: DeploymentTrigger) =>
    confirm({
      title: `Remove ${trigger.name}`,
      confirmLabel: "Remove webhook",
      description: "Deliveries to this webhook stop immediately.",
      action: async () => {
        await del(`${base}/triggers/${trigger.id}`)
      },
      onDone: () => triggers.refresh(),
    })

  const rotateSecret = (trigger: DeploymentTrigger) =>
    confirm({
      title: `Rotate secret for ${trigger.name}`,
      confirmLabel: "Rotate secret",
      description:
        "The current secret stops verifying deliveries immediately. Update anything that signs requests with it.",
      action: async () => {
        const result = await post<{ secret: string }>(
          `${base}/triggers/${trigger.id}/rotate-secret`,
        )
        setRotated({ name: trigger.name, secret: result.secret })
      },
    })

  const invalid =
    !name.trim() || (provider !== "generic_hook" && (!repository.trim() || !branch.trim()))

  return (
    <>
      <SettingCard
        title="Webhooks"
        actions={
          canAdmin && (
            <Button size="sm" variant="outline" onClick={() => setOpen(true)}>
              <Plus className="size-3.5" /> Add webhook
            </Button>
          )
        }
      >
        {connected && (
          <Notice title={`${connected} is connected through the GitHub App`}>
            <p className="text-sm">
              Pushes and pull requests on the repository reach this dashboard through the App&apos;s
              own webhook. There is nothing to configure on GitHub.
            </p>
            <Button size="xs" variant="outline" onClick={() => setConnected(undefined)}>
              Dismiss
            </Button>
          </Notice>
        )}
        {newSecret && (
          <Notice tone="warning" title="Copy this secret now">
            <div className="mt-2 space-y-2">
              {/* The origin is added here, at render, rather than stored on
                  `newSecret.url` — `API_BASE` is relative, and an operator
                  pasting this into GitHub needs the host it points at. */}
              <Well className="break-all select-all">
                {window.location.origin}
                {newSecret.url}
              </Well>
              <Well className="break-all select-all">{newSecret.secret}</Well>
              <Button size="xs" variant="outline" onClick={() => setNewSecret(undefined)}>
                Dismiss
              </Button>
            </div>
          </Notice>
        )}
        {rotated && (
          <Notice tone="warning" title={`Copy the new secret for ${rotated.name} now`}>
            <div className="mt-2 space-y-2">
              <Well className="break-all select-all">{rotated.secret}</Well>
              <Button size="xs" variant="outline" onClick={() => setRotated(undefined)}>
                Dismiss
              </Button>
            </div>
          </Notice>
        )}
        {triggers.loading && !triggers.data ? (
          <LoadingRows rows={3} />
        ) : triggers.error ? (
          <ErrorState error={triggers.error} onRetry={triggers.refresh} />
        ) : (triggers.data?.length ?? 0) === 0 ? (
          <EmptyNote>
            No webhooks yet. Git branch deployments already work automatically — add one for another
            integration or pull request previews.
          </EmptyNote>
        ) : (
          <ul aria-label="Webhooks" className="divide-y divide-hairline">
            {triggers.data?.map((trigger) => {
              const verbs: Verb[] = [
                {
                  key: "toggle",
                  label: trigger.enabled ? "Disable" : "Enable",
                  detail: trigger.enabled
                    ? "Stop accepting deliveries until re-enabled."
                    : "Start accepting deliveries again.",
                  icon: trigger.enabled ? StopCircle : Play,
                  inline: true,
                  run: () => void toggleTrigger(trigger),
                },
                {
                  key: "deliveries",
                  label: "Deliveries",
                  detail: "The events this webhook received and what happened to each.",
                  icon: Logs,
                  run: () => setDeliveriesTrigger(trigger),
                },
                {
                  key: "rotate-secret",
                  label: "Rotate secret",
                  detail: "Issue a new signing secret; the old one stops working immediately.",
                  icon: Key,
                  run: () => rotateSecret(trigger),
                },
                {
                  key: "remove",
                  label: "Remove",
                  detail: "Delete this webhook and its secret.",
                  icon: Trash,
                  danger: true,
                  run: () => removeTrigger(trigger),
                },
              ]
              return (
                <li
                  key={trigger.id}
                  className="flex min-w-0 flex-wrap items-center justify-between gap-3 py-3 first:pt-0 last:pb-0"
                >
                  <div className="min-w-0">
                    <p className="truncate text-body font-medium">{trigger.name}</p>
                    <p className="truncate text-hint text-muted-foreground">
                      {providerLabel(trigger.provider ?? trigger.kind)}
                      {trigger.config.delivery === "app" ? " · via GitHub App" : ""}
                      {trigger.config.repository && <> · {trigger.config.repository}</>}
                      {trigger.config.ref && <> · {trigger.config.ref}</>}
                    </p>
                  </div>
                  <div className="flex shrink-0 items-center gap-3">
                    <Status
                      tone={trigger.enabled ? "running" : "stopped"}
                      label={trigger.enabled ? "Enabled" : "Disabled"}
                    />
                    {trigger.lastStatus && (
                      <Status
                        tone={
                          trigger.lastStatus === "accepted"
                            ? "running"
                            : trigger.lastStatus === "rejected"
                              ? "danger"
                              : "warning"
                        }
                        label={humanize(trigger.lastStatus)}
                      />
                    )}
                    {canAdmin && (
                      <VerbActions verbs={verbs} menuLabel={`${trigger.name} actions`} />
                    )}
                  </div>
                </li>
              )
            })}
          </ul>
        )}
      </SettingCard>

      <SidePanel
        open={open}
        onOpenChange={setOpen}
        title="Add webhook"
        description="Add an integration hook, or turn on pull request previews."
        width="md"
        footer={
          <>
            <Button variant="outline" onClick={() => setOpen(false)} disabled={saving}>
              Cancel
            </Button>
            <Button onClick={createTrigger} disabled={invalid} pending={saving}>
              Create webhook
            </Button>
          </>
        }
      >
        <div className="space-y-4" aria-busy={saving}>
          <Field label="Name" htmlFor="webhook-name">
            <Input id="webhook-name" value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          <Field label="Provider" htmlFor="webhook-provider">
            <Select
              value={provider}
              onValueChange={(value: (typeof PROVIDERS)[number]) => setProvider(value)}
            >
              <SelectTrigger id="webhook-provider" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {PROVIDERS.map((item) => (
                  <SelectItem key={item} value={item}>
                    {providerLabel(item)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
          {provider === "github" && appAvailable && (
            <OptionRow
              title="Deliver through the GitHub App"
              hint="Nothing to paste into GitHub: the App's webhook already reaches this dashboard."
              checked={viaApp}
              onCheckedChange={setViaApp}
            />
          )}
          {provider !== "generic_hook" && (
            <>
              <Field label="Repository" htmlFor="webhook-repository">
                <Input
                  id="webhook-repository"
                  placeholder="owner/repository"
                  value={repository}
                  onChange={(e) => setRepository(e.target.value)}
                />
              </Field>
              <Field label="Branch" htmlFor="webhook-branch">
                <Input
                  id="webhook-branch"
                  value={branch}
                  onChange={(e) => setBranch(e.target.value)}
                />
              </Field>
            </>
          )}
          <Field
            label="Watched paths"
            htmlFor="webhook-paths"
            hint="One glob per line. Leave empty to watch everything."
          >
            <Textarea
              id="webhook-paths"
              placeholder="services/api/**"
              value={watchedPaths}
              onChange={(e) => setWatchedPaths(e.target.value)}
            />
          </Field>
          <OptionRow
            title="Create isolated environments for pull requests"
            hint="Each revision needs administrator approval before it builds."
            checked={previewsEnabled}
            onCheckedChange={setPreviewsEnabled}
          >
            <div className="space-y-3">
              <Notice title="Preview policy">
                Each pull request revision needs administrator approval. Preview variables and
                storage are separate from production. Container previews have a dedicated network;
                Compose and workloads needing host access require a separate plan.
              </Notice>
              <Field
                label="Preview domain pattern"
                htmlFor="webhook-preview-domain"
                hint={"Use {number} for the pull request number, and point its DNS to this server."}
              >
                <Input
                  id="webhook-preview-domain"
                  placeholder="pr-{number}.example.com"
                  value={previewDomain}
                  onChange={(e) => setPreviewDomain(e.target.value)}
                />
              </Field>
            </div>
          </OptionRow>
        </div>
      </SidePanel>

      <SidePanel
        open={Boolean(deliveriesTrigger)}
        onOpenChange={(next) => !next && setDeliveriesTrigger(undefined)}
        title={deliveriesTrigger ? `Deliveries · ${deliveriesTrigger.name}` : "Deliveries"}
        description="Recent events this webhook received and what happened to each."
        width="sm"
      >
        {deliveries.loading && !deliveries.data ? (
          <LoadingRows rows={3} />
        ) : deliveries.error ? (
          <ErrorState error={deliveries.error} onRetry={deliveries.refresh} />
        ) : (deliveries.data?.length ?? 0) === 0 ? (
          <EmptyNote>No deliveries have been attempted yet.</EmptyNote>
        ) : (
          <RowList aria-label="Deliveries">
            {deliveries.data?.map((delivery) => {
              const decision = deliveryDecision(delivery.decision)
              return (
                <Row
                  key={delivery.deliveryId}
                  leading={<Status tone={decision.tone} label={decision.label} />}
                  title={humanize(delivery.event)}
                  subtitle={[delivery.ref, delivery.reason, relativeTime(delivery.receivedAt)]
                    .filter(Boolean)
                    .join(" · ")}
                  trailing={
                    delivery.runId !== undefined && (
                      <Button size="xs" variant="outline" asChild>
                        <Link href={`/deploy/${projectId}/runs/${delivery.runId}`}>View run</Link>
                      </Button>
                    )
                  }
                />
              )
            })}
          </RowList>
        )}
      </SidePanel>
      {dialog}
    </>
  )
}
