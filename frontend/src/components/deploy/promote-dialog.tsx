"use client"

import { useState } from "react"
import { useRouter } from "next/navigation"
import { ArrowDown } from "@/components/icons"
import { get, post } from "@/lib/api"
import { bytes, relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { usePoll } from "@/hooks/use-poll"
import type {
  DeploymentEngineRun,
  DeploymentPromotionPreview,
  DeploymentRelease,
  DeploymentRunsPage,
  ReleaseListChange,
} from "@/lib/types"
import { Modal } from "@/components/modal"
import { Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { ChoiceCard } from "@/components/choice-card"
import { GroupRule } from "@/components/flow"
import { Skeleton } from "@/components/ui/skeleton"
import { Button } from "@/components/ui/button"
import { AuthorMark } from "@/components/git/marks"
import { RunActorMark } from "@/components/deploy/run-marks"
import { ReleaseRevision } from "@/components/deploy/release-comparison-sheet"
import { runCommit, runTitle } from "@/components/deploy/vocabulary"

/**
 * Promote a staging release into production.
 *
 * Two steps, as rolling back has: choose a staging release (skipped when one
 * is already named — staging's own Promote opens on its live release), then
 * a review of exactly what will happen. The review leads with the image's
 * id, whole, because the promise is that production runs that very image and
 * nothing is built; then where it goes, and how production's configuration
 * differs from the one staging ran it with — the variables by name only,
 * since production's values are production's. What runs before the cutover
 * (production's release tasks, against production's database) and what goes
 * live with it (production's saved, undeployed settings) are said as notices,
 * because they are facts the decision is made on.
 *
 * The command wears the brand face: it is what the dialog is for, and like a
 * deploy it is undone by rolling back.
 */
export function PromoteDialog({
  open,
  onOpenChange,
  projectId,
  productionId,
  stagingId,
  remote,
  initialReleaseId,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  projectId: number
  productionId: number
  stagingId: number
  remote?: string
  initialReleaseId?: number
}) {
  const router = useRouter()
  const [step, setStep] = useState<"choose" | "review">(initialReleaseId ? "review" : "choose")
  const [selected, setSelected] = useState<number | undefined>(initialReleaseId)
  const [busy, setBusy] = useState(false)

  const releases = usePoll(
    (signal) =>
      get<DeploymentRelease[]>(
        `/deploy/${projectId}/environments/${stagingId}/releases`,
        { limit: 30 },
        signal,
      ),
    0,
    [projectId, stagingId, open],
    { enabled: open && stagingId > 0 },
  )
  const runs = usePoll(
    (signal) =>
      get<DeploymentRunsPage>(
        `/deploy/${projectId}/runs`,
        { view: "engine", environment: stagingId, limit: 30 },
        signal,
      ),
    0,
    [projectId, stagingId, open],
    { enabled: open && stagingId > 0 },
  )
  const eligible = (releases.data ?? []).filter(
    (release) => release.state === "live" || release.state === "retained",
  )
  const chosen = selected ?? eligible.find((release) => release.state === "live")?.id
  const preview = usePoll(
    (signal) =>
      get<DeploymentPromotionPreview>(
        `/deploy/${projectId}/environments/${productionId}/promotion`,
        { releaseId: chosen },
        signal,
      ),
    0,
    [projectId, productionId, chosen, open, step],
    { enabled: open && step === "review" && Boolean(chosen) },
  )

  const [wasOpen, setWasOpen] = useState(open)
  if (open !== wasOpen) {
    setWasOpen(open)
    if (open) {
      setStep(initialReleaseId ? "review" : "choose")
      setSelected(initialReleaseId)
    }
  }

  const runOf = (release?: DeploymentRelease) =>
    release && runs.data?.runs.find((run) => run.id === release.runId)
  const reading = preview.data
  const blocked = (reading?.blockers.length ?? 0) > 0

  const promote = async () => {
    if (!chosen) return
    setBusy(true)
    try {
      const started = await post<DeploymentEngineRun>(
        `/deploy/${projectId}/environments/${productionId}/promote`,
        { releaseId: chosen },
      )
      router.push(`/deploy/${projectId}/runs/${started.id}`)
    } catch (error) {
      notify.error("Could not promote", error)
      setBusy(false)
    }
  }

  const number = reading?.source.release.number ?? eligible.find((r) => r.id === chosen)?.number
  return (
    <Modal
      open={open}
      onOpenChange={(next) => !busy && onOpenChange(next)}
      title={
        step === "review" && number !== undefined
          ? `Promote staging release #${number}`
          : "Promote to production"
      }
      description="Choose a staging release and review what production will run before promoting it."
      size="lg"
      footer={
        <div className="flex w-full flex-wrap items-center justify-end gap-2 max-sm:[&>*]:flex-1">
          {step === "review" && !initialReleaseId && (
            <Button variant="outline" onClick={() => setStep("choose")} disabled={busy}>
              Back
            </Button>
          )}
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          {step === "choose" ? (
            <Button disabled={!chosen} onClick={() => setStep("review")}>
              Continue
            </Button>
          ) : (
            <Button
              disabled={!reading || blocked || busy}
              pending={busy}
              onClick={() => void promote()}
            >
              Promote to production
            </Button>
          )}
        </div>
      }
    >
      {step === "choose" ? (
        <div className="space-y-4">
          <GroupRule label="Staging releases" count={eligible.length} />
          {releases.loading && !releases.data ? (
            <Skeleton className="h-16 w-full" />
          ) : eligible.length === 0 ? (
            <p className="py-6 text-center text-body text-muted-foreground">
              Staging has no release to promote yet. Deploy staging first.
            </p>
          ) : (
            <div role="group" aria-label="Staging releases" className="grid gap-2">
              {eligible.map((release) => {
                const run = runOf(release)
                const commit = runCommit(run)
                return (
                  <ChoiceCard
                    key={release.id}
                    verb={`Staging release #${release.number}`}
                    selected={release.id === chosen}
                    onClick={() => setSelected(release.id)}
                    className="min-h-0"
                    logo={run && <RunActorMark run={run} remote={remote} />}
                    title={
                      <span className="flex min-w-0 items-baseline gap-2">
                        <span className="numeric shrink-0">Release #{release.number}</span>
                        {run && (
                          <span className="truncate font-normal text-muted-foreground">
                            {runTitle(run)}
                          </span>
                        )}
                      </span>
                    }
                    description={
                      <span className="flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-1">
                        <ReleaseRevision revision={release.sourceRevision} />
                        <AuthorMark name={commit?.author} />
                        {commit?.subject && (
                          <span className="min-w-0 truncate text-foreground/80">
                            {commit.subject}
                          </span>
                        )}
                        <span className="flex basis-full items-center gap-2">
                          built {relativeTime(release.createdAt)}
                          {release.state === "live" && (
                            <Status tone="running" label="Live in staging" />
                          )}
                        </span>
                      </span>
                    }
                  />
                )
              })}
            </div>
          )}
        </div>
      ) : !reading && preview.error ? (
        <Notice tone="danger" title="This release cannot be promoted">
          {preview.error.message}
        </Notice>
      ) : !reading ? (
        <div className="space-y-3" aria-busy="true">
          <Skeleton className="h-12 w-full" />
          <Skeleton className="h-24 w-full" />
        </div>
      ) : (
        <PromotionReview reading={reading} run={runOf(reading.source.release)} />
      )}
    </Modal>
  )
}

function PromotionReview({
  reading,
  run,
}: {
  reading: DeploymentPromotionPreview
  run?: DeploymentEngineRun
}) {
  const commit = runCommit(run)
  const live = reading.target.liveRelease
  const fields = reading.detail.fields.filter((field) => field.changed)
  const lists = (["variables", "domains", "checks", "dependencies"] as const)
    .map((key) => ({
      key,
      changes: reading.detail[key].filter((change) => change.change !== "unchanged"),
    }))
    .filter((group) => group.changes.length > 0)
  const same = reading.detail.status === "available" && fields.length === 0 && lists.length === 0
  return (
    <div className="space-y-5">
      {reading.blockers.map((blocker) => (
        <Notice key={blocker.code} tone="danger" title="This release cannot be promoted">
          {blocker.message}
        </Notice>
      ))}

      <section aria-label="Image" className="space-y-1.5">
        <p className="eyebrow">Image</p>
        <p data-slot="promotion-image" className="font-mono text-hint break-all text-foreground">
          {reading.source.imageId || reading.source.imageDigest}
        </p>
        <p className="text-hint text-muted-foreground">
          The image staging ran, as it is on this server
          {reading.source.imageSizeBytes > 0 && ` (${bytes(reading.source.imageSizeBytes)})`}.
          Production starts it as it is; nothing is built.
        </p>
      </section>

      <div className="space-y-2">
        <div className="flex min-w-0 items-center gap-3">
          {run && <RunActorMark run={run} />}
          <div className="min-w-0 flex-1 space-y-0.5">
            <p className="flex min-w-0 items-baseline gap-2 text-body font-medium">
              <span className="numeric shrink-0">
                {reading.source.environmentName} release #{reading.source.release.number}
              </span>
            </p>
            <p className="flex min-w-0 items-center gap-1.5 text-hint text-muted-foreground">
              <ReleaseRevision revision={reading.source.release.sourceRevision} />
              <AuthorMark name={commit?.author} />
              <span className="min-w-0 truncate">
                {commit?.subject ?? `built ${relativeTime(reading.source.release.createdAt)}`}
              </span>
            </p>
          </div>
        </div>
        <ArrowDown aria-hidden className="ml-2.5 size-4 text-muted-foreground" />
        <div className="border-l-2 border-brand pl-3">
          <p className="text-body font-medium">Production</p>
          <p className="text-hint text-muted-foreground">
            {live
              ? `Replaces release #${live.number}, live since ${relativeTime(live.activatedAt ?? live.createdAt)}`
              : "Its first release"}
          </p>
        </div>
      </div>

      <section aria-label="Configuration" className="space-y-2">
        <p className="eyebrow">Production&apos;s configuration</p>
        {reading.detail.status !== "available" ? (
          <p className="text-body text-muted-foreground">{reading.detail.reason}</p>
        ) : same ? (
          <p className="text-body">It matches the configuration staging ran this image with.</p>
        ) : (
          <div className="space-y-3">
            <p className="text-hint text-muted-foreground">
              Production runs the image with its own settings and variables. These differ from
              staging&apos;s.
            </p>
            {fields.length > 0 && (
              <dl className="grid grid-cols-[minmax(0,10rem)_minmax(0,1fr)] gap-x-4 gap-y-1.5 text-body">
                {fields.map((field) => (
                  <div key={field.field} className="contents">
                    <dt className="truncate text-muted-foreground first-letter:uppercase">
                      {field.field}
                    </dt>
                    <dd className="min-w-0 font-mono text-hint break-all">
                      <span className="text-muted-foreground">staging </span>
                      {field.from || "none"}
                      <span className="text-muted-foreground"> · production </span>
                      {field.to || "none"}
                    </dd>
                  </div>
                ))}
              </dl>
            )}
            {lists.map((group) => (
              <div key={group.key} className="space-y-1">
                <p className="text-body font-medium first-letter:uppercase">{group.key}</p>
                <ul className="space-y-1">
                  {group.changes.map((change) => (
                    <li key={change.name} className="flex min-w-0 items-baseline gap-2 text-body">
                      <span className="truncate font-mono text-hint">{change.name}</span>
                      <span className="shrink-0 text-hint text-muted-foreground">
                        {differenceWord(change)}
                      </span>
                    </li>
                  ))}
                </ul>
              </div>
            ))}
          </div>
        )}
      </section>

      {reading.target.releaseTasks.length > 0 && (
        <Notice tone="warning" title="Production's release tasks run first">
          {reading.target.releaseTasks.join(", ")} run in this image against production, with
          production&apos;s variables and database, before traffic moves.
        </Notice>
      )}
      {reading.target.pendingChanges.length > 0 && (
        <Notice title="Production's saved settings go live too">
          Production has settings saved since its live release:{" "}
          {[...new Set(reading.target.pendingChanges.map((change) => change.kind))].join(", ")}. The
          promoted release runs with them.
        </Notice>
      )}
      {reading.target.activeRuns > 0 && (
        <Notice title="A production run is ahead of this one">
          The promotion waits for it, then replaces whatever it releases.
        </Notice>
      )}
    </div>
  )
}

/** A difference from the staging release's side, in production's terms. */
function differenceWord(change: ReleaseListChange) {
  switch (change.change) {
    case "added":
      return "only in production"
    case "removed":
      return "only in staging"
    default:
      return change.secret ? "a different value" : `${change.from} · production ${change.to}`
  }
}
