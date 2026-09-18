"use client"

import { useState } from "react"
import { get, post } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type { DeploymentPreview, DeploymentPreviewApproval } from "@/lib/types"
import { Well } from "@/components/panel"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import { Status, type DotTone } from "@/components/status-dot"
import { SidePanel } from "@/components/side-panel"
import { Button } from "@/components/ui/button"
import { useConfirm } from "@/components/confirm-dialog"
import { SettingCard } from "@/components/deploy/settings/setting-card"
import { VariablesPanel } from "@/components/deploy/settings/variables"
import { humanize } from "@/components/deploy/vocabulary"

function isolationOf(preview: DeploymentPreview): { tone: DotTone; label: string; note?: string } {
  if (preview.isolationStatus === "pending") {
    return {
      tone: "danger",
      label: "Isolation incomplete",
      note:
        "This older preview may share production resources. Deployments are blocked while its " +
        "containers and route are isolated. Cleanup retries automatically; check Docker and proxy " +
        "availability if it remains blocked.",
    }
  }
  if (preview.isolationStatus === "quarantined") {
    return {
      tone: "stopped",
      label: "Stopped for isolation",
      note:
        "The previous runtime is stopped and stored data is retained. Redeliver the pull request " +
        "event, approve its revision, and configure preview settings before deploying again.",
    }
  }
  return { tone: preview.state === "open" ? "running" : "stopped", label: humanize(preview.state) }
}

export function Previews({ projectId }: { projectId: number }) {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const approvals = usePoll(
    (signal) =>
      get<DeploymentPreviewApproval[]>(
        `/deploy/${projectId}/previews/approvals`,
        undefined,
        signal,
      ),
    5000,
    [projectId],
  )
  const previews = usePoll(
    (signal) => get<DeploymentPreview[]>(`/deploy/${projectId}/previews`, undefined, signal),
    5000,
    [projectId],
  )
  const [variablesFor, setVariablesFor] = useState<DeploymentPreview>()
  const [deploying, setDeploying] = useState<number>()

  const approvePreview = (approval: DeploymentPreviewApproval) => {
    let configured: DeploymentPreview | undefined
    confirm({
      title: `Approve PR ${approval.providerRef}`,
      confirmLabel: "Approve and configure",
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
      action: async () => {
        const result = await post<{ preview: DeploymentPreview }>(
          `/deploy/${projectId}/previews/approvals/${approval.id}/approve`,
          { revision: approval.revision, deploy: false },
        )
        configured = result.preview
        approvals.refresh()
        previews.refresh()
      },
      onDone: () => setVariablesFor(configured),
    })
  }

  const deployPreview = async (preview: DeploymentPreview) => {
    setDeploying(preview.id)
    try {
      await post(`/deploy/${projectId}/environments/${preview.environmentId}/runs`, {
        operation: "deploy",
      })
      notify.success("Preview deployment queued")
      previews.refresh()
    } catch (error) {
      notify.error("Could not deploy preview", error)
    } finally {
      setDeploying(undefined)
    }
  }

  const rejectApproval = (approval: DeploymentPreviewApproval) =>
    confirm({
      title: `Reject PR ${approval.providerRef}`,
      confirmLabel: "Reject",
      description: "This revision will not build. A later revision can still be approved.",
      action: async () => {
        await post(`/deploy/${projectId}/previews/approvals/${approval.id}/reject`, {
          revision: approval.revision,
        })
        approvals.refresh()
      },
    })

  const pendingApprovals = approvals.data?.filter(
    (approval) => approval.state === "pending" || !approval.configured,
  )

  return (
    <>
      <SettingCard title="Preview environments">
        <div className="space-y-4">
          {approvals.error && <ErrorState error={approvals.error} onRetry={approvals.refresh} />}
          {pendingApprovals && pendingApprovals.length > 0 && (
            <ul aria-label="Pending approvals" className="divide-y divide-hairline">
              {pendingApprovals.map((approval) => (
                <li
                  key={approval.id}
                  className="flex min-w-0 flex-wrap items-center justify-between gap-3 py-3 first:pt-0 last:pb-0"
                >
                  <div className="min-w-0">
                    <p className="text-body font-medium">
                      PR {approval.providerRef} ·{" "}
                      {approval.state === "rejected"
                        ? "Rejected"
                        : approval.state === "pending"
                          ? "Awaiting approval"
                          : "Setup incomplete"}
                    </p>
                    <p className="truncate text-hint text-muted-foreground">
                      {approval.author || "Unknown author"} ·{" "}
                      {approval.headRepository || approval.repository}
                    </p>
                    <p className="font-mono text-hint text-muted-foreground">
                      {approval.revision.slice(0, 12)}
                    </p>
                  </div>
                  {approval.state === "rejected" ? (
                    <Status tone="stopped" label="Rejected" />
                  ) : (
                    can("system.admin") && (
                      <div className="flex shrink-0 items-center gap-2">
                        <Button size="sm" onClick={() => approvePreview(approval)}>
                          Review revision
                        </Button>
                        <Button
                          size="xs"
                          variant="ghost"
                          className="text-destructive"
                          onClick={() => rejectApproval(approval)}
                        >
                          Reject
                        </Button>
                      </div>
                    )
                  )}
                </li>
              ))}
            </ul>
          )}

          {previews.loading && !previews.data ? (
            <LoadingRows rows={2} />
          ) : previews.error ? (
            <ErrorState error={previews.error} onRetry={previews.refresh} />
          ) : (previews.data?.length ?? 0) === 0 ? (
            <EmptyNote>
              No previews. Pull requests appear above for review — approve a revision, configure its
              variables, then deploy.
            </EmptyNote>
          ) : (
            <ul aria-label="Preview environments" className="divide-y divide-hairline">
              {previews.data?.map((preview) => {
                const isolation = isolationOf(preview)
                const blocked =
                  preview.isolationStatus === "pending" || preview.isolationStatus === "quarantined"
                return (
                  <li key={preview.id} className="min-w-0 space-y-2 py-3 first:pt-0 last:pb-0">
                    <div className="flex min-w-0 flex-wrap items-center justify-between gap-3">
                      <div className="min-w-0">
                        <p className="font-mono text-body">{preview.environmentSlug}</p>
                        <p className="text-hint text-muted-foreground">
                          PR {preview.providerRef} · updated {relativeTime(preview.updatedAt)}
                        </p>
                      </div>
                      <div className="flex shrink-0 items-center gap-2">
                        <Status tone={isolation.tone} label={isolation.label} />
                        {preview.state === "open" && can("system.admin") && (
                          <Button
                            size="sm"
                            variant="outline"
                            onClick={() => setVariablesFor(preview)}
                          >
                            Variables
                          </Button>
                        )}
                        {preview.state === "open" && can("service.control") && (
                          <Button
                            size="sm"
                            disabled={deploying !== undefined || blocked}
                            pending={deploying === preview.id}
                            onClick={() => void deployPreview(preview)}
                          >
                            Deploy preview
                          </Button>
                        )}
                      </div>
                    </div>
                    {isolation.note && (
                      <p className="max-w-prose text-hint text-muted-foreground">
                        {isolation.note}
                      </p>
                    )}
                  </li>
                )
              })}
            </ul>
          )}
        </div>
      </SettingCard>

      <SidePanel
        open={variablesFor !== undefined}
        onOpenChange={(open) => !open && setVariablesFor(undefined)}
        title={`Preview ${variablesFor?.providerRef ?? ""} variables`}
        description="Configure values used only by this preview before deploying."
        width="lg"
      >
        {variablesFor && (
          <VariablesPanel
            projectId={projectId}
            environmentId={variablesFor.environmentId}
            compact
          />
        )}
      </SidePanel>
      {dialog}
    </>
  )
}
