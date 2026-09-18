"use client"

import { useState } from "react"
import { useRouter } from "next/navigation"
import { Check } from "@/components/icons"
import { cn } from "@/lib/utils"
import { post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { relativeTime } from "@/lib/format"
import { Modal } from "@/components/modal"
import { Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import type { DeploymentEngineRun, DeploymentRelease } from "@/lib/types"
import { runCommit, runTitle, shortRevision } from "@/components/deploy/vocabulary"

/**
 * Roll back the live release to a retained one.
 *
 * Two steps: choose (skipped when a specific release is already named — the
 * Deployments row's "Roll back to this release" opens straight into review),
 * then a review that says what moves and what does not. Ordinary
 * confirmation is this dialog itself — nothing is typed, because rolling back
 * replaces one immutable, already-built release with another rather than
 * destroying anything (spec §5.7).
 */
export function RollbackDialog({
  open,
  onOpenChange,
  projectId,
  environmentId,
  liveRelease,
  releases,
  runs,
  domains,
  initialReleaseId,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  projectId: number
  environmentId: number
  liveRelease?: DeploymentRelease
  releases: DeploymentRelease[]
  runs: DeploymentEngineRun[]
  /** Hostnames that move to whichever release is live. */
  domains: string[]
  /** Preselects a release and opens straight into the review step. */
  initialReleaseId?: number
}) {
  const router = useRouter()
  const eligible = releases.filter(
    (release) => release.state === "retained" && release.id !== liveRelease?.id,
  )
  const [step, setStep] = useState<"choose" | "review">(initialReleaseId ? "review" : "choose")
  const [selected, setSelected] = useState<number | undefined>(initialReleaseId ?? eligible[0]?.id)
  const [busy, setBusy] = useState(false)

  // Radix keeps this component mounted across a close/open cycle, so its
  // state needs resetting on the way in rather than relying on a remount —
  // the same reason `ConfirmDialog` keys itself on the request it is showing.
  // Adjusted during render (React's documented pattern for this) rather than
  // in an effect, which would set state after an extra, visible frame.
  const [wasOpen, setWasOpen] = useState(open)
  if (open !== wasOpen) {
    setWasOpen(open)
    if (open) {
      setStep(initialReleaseId ? "review" : "choose")
      setSelected(initialReleaseId ?? eligible[0]?.id)
    }
  }

  const release = eligible.find((candidate) => candidate.id === selected)
  const run = release && runs.find((candidate) => candidate.id === release.runId)
  const commit = runCommit(run)

  const rollback = async () => {
    if (!release) return
    setBusy(true)
    try {
      const started = await post<DeploymentEngineRun>(
        `/deploy/${projectId}/environments/${environmentId}/rollback`,
        { releaseId: release.id },
      )
      router.push(`/deploy/${projectId}/runs/${started.id}`)
    } catch (error) {
      notify.error("Could not roll back", error)
      setBusy(false)
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={(next) => !busy && onOpenChange(next)}
      title={step === "review" && release ? `Roll back to release #${release.number}` : "Roll back"}
      description="Choose a retained release and review what moves before rolling back."
      footer={
        <>
          {step === "review" && !initialReleaseId && (
            <Button variant="outline" onClick={() => setStep("choose")} disabled={busy}>
              Back
            </Button>
          )}
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          {step === "choose" ? (
            <Button disabled={!release} onClick={() => setStep("review")}>
              Continue
            </Button>
          ) : (
            <Button
              variant="destructive"
              disabled={!release || busy}
              pending={busy}
              onClick={rollback}
            >
              Roll back
            </Button>
          )}
        </>
      }
    >
      {step === "choose" ? (
        <div className="space-y-3">
          {liveRelease && (
            <div className="flex min-w-0 items-center justify-between gap-3 rounded-md bg-surface-header px-3 py-2.5">
              <span className="min-w-0 truncate text-body font-medium">
                Release #{liveRelease.number}
              </span>
              <Status tone="running" label="Live" />
            </div>
          )}
          {eligible.length === 0 ? (
            <p className="py-6 text-center text-body text-muted-foreground">
              No retained release is eligible for rollback.
            </p>
          ) : (
            <div
              role="radiogroup"
              aria-label="Retained releases"
              className="divide-y divide-hairline"
            >
              {eligible.map((candidate) => {
                const candidateRun = runs.find((r) => r.id === candidate.runId)
                const candidateCommit = runCommit(candidateRun)
                const checked = candidate.id === selected
                return (
                  <label
                    key={candidate.id}
                    className={cn(
                      "flex min-w-0 cursor-pointer items-center gap-3 rounded-md px-2 py-3 transition-colors hover:bg-row-hover",
                      "has-[:focus-visible]:outline has-[:focus-visible]:outline-2 has-[:focus-visible]:outline-offset-2 has-[:focus-visible]:outline-ring",
                    )}
                  >
                    <input
                      type="radio"
                      name="rollback-release"
                      value={candidate.id}
                      checked={checked}
                      onChange={() => setSelected(candidate.id)}
                      className="sr-only"
                    />
                    <span
                      aria-hidden="true"
                      className={cn(
                        "flex size-4 shrink-0 items-center justify-center rounded-full border",
                        checked
                          ? "border-primary bg-primary text-primary-foreground"
                          : "border-hairline",
                      )}
                    >
                      {checked && <Check className="size-2.5" />}
                    </span>
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-body font-medium">
                        Release #{candidate.number}
                        {candidateRun && <> · {runTitle(candidateRun)}</>}
                      </span>
                      <span className="block truncate text-hint text-muted-foreground">
                        {candidateCommit?.subject ?? shortRevision(candidate.sourceRevision) ?? "—"}
                        {" · "}
                        {relativeTime(candidate.createdAt)}
                        {candidateRun?.actor && ` · by ${candidateRun.actor}`}
                      </span>
                    </span>
                  </label>
                )
              })}
            </div>
          )}
        </div>
      ) : (
        release && (
          <div className="space-y-4">
            <div>
              <p className="text-body font-medium">
                Release #{release.number}
                {run && <> · {runTitle(run)}</>}
              </p>
              <p className="text-hint text-muted-foreground">
                {commit?.subject ?? shortRevision(release.sourceRevision) ?? "—"} ·{" "}
                {relativeTime(release.createdAt)}
                {run?.actor && ` · by ${run.actor}`}
              </p>
            </div>
            <div>
              <p className="mb-1 text-hint text-muted-foreground">
                {domains.length === 0 ? "No public domain moves back" : "Domains that move back"}
              </p>
              {domains.length > 0 && (
                <ul className="space-y-0.5">
                  {domains.map((hostname) => (
                    <li key={hostname} className="truncate font-mono text-xs">
                      {hostname}
                    </li>
                  ))}
                </ul>
              )}
            </div>
            <p className="text-hint text-muted-foreground">
              Variables and configuration are the ones that release was built with — every release
              is a frozen snapshot of both.
            </p>
            <Notice tone="warning" title="Databases and files are not rolled back">
              Only the application release moves. Anything a database or a persistent volume holds
              stays exactly as it is now.
            </Notice>
          </div>
        )
      )}
    </Modal>
  )
}
