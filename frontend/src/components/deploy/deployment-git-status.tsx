"use client"

import { useState } from "react"
import { get, put } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { Status } from "@/components/status-dot"
import { SidePanel } from "@/components/side-panel"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"

type GitPolicy = {
  automatic: boolean
  watchInclude: string[]
  watchExclude: string[]
  commitStatuses?: boolean
  revision: number
  inherited?: boolean
  conflict?: boolean
}

type GitWatchStatus = {
  automatic: boolean
  branch?: string
  status: string
  checkedAt?: string
  intervalSeconds: number
  reason?: string
  policy?: GitPolicy
}

const decisionReasons: Record<string, string> = {
  watch_paths_ignored:
    "The latest changes did not match the watched paths. No deployment was queued.",
  watched_paths_changed:
    "The latest changes matched the watched paths and a deployment was queued.",
  branch_changed: "A new branch revision was queued for deployment.",
  already_attempted: "This commit already has a deployment run. Use Retry if it failed.",
  changes_unavailable:
    "The complete changed paths could not be read. Automatic deployment is paused until comparison succeeds.",
  enqueue_failed: "The change could not be queued. The next branch check will retry.",
  policy_conflict:
    "Existing webhook filters disagree. Save one deployment policy to resume automation.",
}

export function DeploymentGitStatus({
  projectID,
  environmentID,
}: {
  projectID: number
  environmentID: number
}) {
  const { can } = useAuth()
  const [editing, setEditing] = useState<GitPolicy>()
  const [include, setInclude] = useState("")
  const [exclude, setExclude] = useState("")
  const [saving, setSaving] = useState(false)
  const watch = usePoll(
    (signal) =>
      get<GitWatchStatus>(
        `/deploy/${projectID}/environments/${environmentID}/git-watch`,
        undefined,
        signal,
      ),
    5000,
    [projectID, environmentID],
  )
  if (watch.error) {
    return <p className="text-xs text-warning">Automatic deployment status is unavailable.</p>
  }
  const status = watch.data
  if (!status) return null
  const policy = status.policy ?? {
    automatic: true,
    commitStatuses: true,
    watchInclude: [],
    watchExclude: [],
    revision: 0,
  }
  const unavailable = ["unavailable", "stale", "policy_conflict"].includes(status.status)
  const save = async () => {
    if (!editing) return
    setSaving(true)
    const patterns = (text: string) =>
      text
        .split("\n")
        .map((line) => line.trim())
        .filter(Boolean)
    try {
      await put(`/deploy/${projectID}/environments/${environmentID}/git-policy`, {
        automatic: editing.automatic,
        commitStatuses: editing.commitStatuses ?? true,
        revision: editing.revision,
        watchInclude: patterns(include),
        watchExclude: patterns(exclude),
      })
      setEditing(undefined)
      watch.refresh()
      notify.success("Deployment policy saved")
    } catch (error) {
      notify.error("Could not save deployment policy", error)
    } finally {
      setSaving(false)
    }
  }
  return (
    <>
      <div className="space-y-2" aria-label="Automatic deployments">
        <Status
          state={unavailable ? "warning" : policy.automatic ? "enabled" : "disabled"}
          label={
            unavailable
              ? "Automatic deployments need attention"
              : policy.automatic
                ? "Automatic deployments"
                : "Manual deployments only"
          }
        />
        <p className="text-xs text-muted-foreground">
          {!policy.automatic
            ? "Git polling, webhooks and scheduled deployments cannot queue new deployments. Deploy manually when ready."
            : unavailable
              ? "Could not check the production branch. Check repository access and credentials."
              : status.status === "not_applicable"
                ? "Signed hooks and schedules can request deployments. This source has no branch to poll."
                : status.status === "awaiting_first_deployment"
                  ? `After your first deployment, new commits to ${status.branch} deploy automatically.`
                  : policy.watchInclude.length || policy.watchExclude.length
                    ? `Matching changes on ${status.branch} deploy automatically. The same path filters apply to polling and push webhooks.`
                    : `New commits to ${status.branch} deploy automatically. Checked every ${status.intervalSeconds} seconds.`}
        </p>
        {status.reason && decisionReasons[status.reason] && (
          <p className="text-xs text-muted-foreground">{decisionReasons[status.reason]}</p>
        )}
        {status.checkedAt && (
          <p className="text-xs text-muted-foreground">
            Last checked {relativeTime(status.checkedAt)}
          </p>
        )}
        {can("system.admin") && (
          <Button
            size="sm"
            variant="outline"
            onClick={() => {
              setEditing(policy)
              setInclude(policy.watchInclude.join("\n"))
              setExclude(policy.watchExclude.join("\n"))
            }}
          >
            Deployment policy
          </Button>
        )}
      </div>
      <SidePanel
        open={!!editing}
        onOpenChange={(open) => {
          if (!open) setEditing(undefined)
        }}
        title="Deployment policy"
        width="sm"
        footer={
          <Button pending={saving} onClick={save}>
            Save deployment policy
          </Button>
        }
      >
        <div className="space-y-4">
          <p className="text-sm text-muted-foreground">
            Choose when this environment accepts automatic deployments. The branch comes from its
            saved source configuration.
          </p>
          {policy.inherited && (
            <p className="text-xs text-muted-foreground">
              These filters were inherited from existing webhooks. Saving makes them the shared
              policy.
            </p>
          )}
          {policy.conflict && (
            <p className="text-xs text-warning">
              Existing webhook filters disagree. Review both lists before saving one shared policy.
            </p>
          )}
          <label className="flex min-h-11 items-center gap-2 text-sm">
            <Checkbox
              checked={editing?.automatic ?? false}
              onCheckedChange={(checked) =>
                setEditing((current) => current && { ...current, automatic: checked === true })
              }
            />
            Deploy automatically
          </label>
          <p className="text-xs text-muted-foreground">
            When disabled, new deployments require a manual action. Runs already queued continue.
          </p>
          <label className="flex min-h-11 items-center gap-2 text-sm">
            <Checkbox
              checked={editing?.commitStatuses ?? true}
              onCheckedChange={(checked) =>
                setEditing((current) => current && { ...current, commitStatuses: checked === true })
              }
            />
            Report deployment status to GitHub commits
          </label>
          <p className="text-xs text-muted-foreground">
            Each run posts pending, success or failure to its commit through the dashboard&apos;s
            GitHub account, with a link back to the run. Only GitHub.com sources are reported.
          </p>
          <div className="space-y-1.5">
            <Label htmlFor="git-policy-include">Include paths</Label>
            <Textarea
              id="git-policy-include"
              value={include}
              onChange={(event) => setInclude(event.target.value)}
              placeholder="services/api/**"
            />
            <p className="text-xs text-muted-foreground">
              One repository-relative glob per line. Leave empty to include all paths. Use
              directory/** for a directory and its children.
            </p>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="git-policy-exclude">Exclude paths</Label>
            <Textarea
              id="git-policy-exclude"
              value={exclude}
              onChange={(event) => setExclude(event.target.value)}
              placeholder="docs/**"
            />
            <p className="text-xs text-muted-foreground">
              Exclusions take priority. Polling and push hooks compare the complete Git changes
              since the last attempted deployment.
            </p>
          </div>
        </div>
      </SidePanel>
    </>
  )
}
