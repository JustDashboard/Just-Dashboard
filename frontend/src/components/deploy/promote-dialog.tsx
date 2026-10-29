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

/** How the review names the two ends, in its sentences and its labels. */
type Ends = { source: string; sources: string; target: string; Target: string }
const STAGING_INTO_PRODUCTION: Ends = {
  source: "staging",
  sources: "staging's",
  target: "production",
  Target: "Production",
}
const PREVIEW_INTO_STAGING: Ends = {
  source: "preview",
  sources: "the preview's",
  target: "staging",
  Target: "Staging",
}

/**
 * Promote a staging release into production — or a pull request's preview
 * into staging, the step before it.
 *
 * Two steps, as rolling back has: choose a staging release (skipped when one
 * is already named — staging's own Promote opens on its live release — and
 * for a preview, whose live release is the one promoted), then a review of
 * exactly what will happen. The review leads with the image's id, whole,
 * because the promise is that the target runs that very image and nothing is
 * built; then where it goes, and how the target's configuration differs from
 * the one the source ran it with — the variables by name only, since the
 * target's values are the target's. What runs before the cutover (the
 * target's release tasks, against its database) and what goes live with it
 * (its saved, undeployed settings) are said as notices, because they are
 * facts the decision is made on. A preview's review also says who approved
 * the commit, and warns when it came from a fork.
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
  preview,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  projectId: number
  productionId: number
  stagingId: number
  remote?: string
  initialReleaseId?: number
  /**
   * A pull request whose preview is promoted into staging instead: the
   * dialog opens on its review, and staging is where the image goes.
   */
  preview?: { number: number; title?: string }
}) {
  const router = useRouter()
  const fromPreview = Boolean(preview)
  const ends = fromPreview ? PREVIEW_INTO_STAGING : STAGING_INTO_PRODUCTION
  const targetId = fromPreview ? stagingId : productionId
  const reviewFirst = Boolean(initialReleaseId) || fromPreview
  const [step, setStep] = useState<"choose" | "review">(reviewFirst ? "review" : "choose")
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
    { enabled: open && stagingId > 0 && !fromPreview },
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
    { enabled: open && stagingId > 0 && !fromPreview },
  )
  const eligible = (releases.data ?? []).filter(
    (release) => release.state === "live" || release.state === "retained",
  )
  const chosen = selected ?? eligible.find((release) => release.state === "live")?.id
  const reading = usePoll(
    (signal) =>
      get<DeploymentPromotionPreview>(
        `/deploy/${projectId}/environments/${targetId}/promotion`,
        preview ? { source: "preview", preview: preview.number } : { releaseId: chosen },
        signal,
      ),
    0,
    [projectId, targetId, chosen, open, step, preview?.number],
    { enabled: open && step === "review" && (fromPreview || Boolean(chosen)) },
  )

  const [wasOpen, setWasOpen] = useState(open)
  if (open !== wasOpen) {
    setWasOpen(open)
    if (open) {
      setStep(reviewFirst ? "review" : "choose")
      setSelected(initialReleaseId)
    }
  }

  const runOf = (release?: DeploymentRelease) =>
    release && runs.data?.runs.find((run) => run.id === release.runId)
  const read = reading.data
  const blocked = (read?.blockers.length ?? 0) > 0

  const promote = async () => {
    if (!preview && !chosen) return
    setBusy(true)
    try {
      const started = await post<DeploymentEngineRun>(
        `/deploy/${projectId}/environments/${targetId}/promote`,
        preview ? { source: "preview", preview: preview.number } : { releaseId: chosen },
      )
      router.push(`/deploy/${projectId}/runs/${started.id}`)
    } catch (error) {
      notify.error("Could not promote", error)
      setBusy(false)
    }
  }

  const number = read?.source.release.number ?? eligible.find((r) => r.id === chosen)?.number
  const title = preview
    ? `Promote PR #${preview.number} to staging`
    : step === "review" && number !== undefined
      ? `Promote staging release #${number}`
      : "Promote to production"
  return (
    <Modal
      open={open}
      onOpenChange={(next) => !busy && onOpenChange(next)}
      title={title}
      description={
        preview
          ? "Review what staging will run before promoting the preview's image into it."
          : "Choose a staging release and review what production will run before promoting it."
      }
      size="lg"
      footer={
        <div className="flex w-full flex-wrap items-center justify-end gap-2 max-sm:[&>*]:flex-1">
          {step === "review" && !reviewFirst && (
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
              disabled={!read || blocked || busy}
              pending={busy}
              onClick={() => void promote()}
            >
              Promote to {ends.target}
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
      ) : !read && reading.error ? (
        <Notice
          tone="danger"
          title={preview ? "This preview cannot be promoted" : "This release cannot be promoted"}
        >
          {reading.error.message}
        </Notice>
      ) : !read ? (
        <div className="space-y-3" aria-busy="true">
          <Skeleton className="h-12 w-full" />
          <Skeleton className="h-24 w-full" />
        </div>
      ) : (
        <PromotionReview
          reading={read}
          run={runOf(read.source.release)}
          ends={ends}
          pullTitle={preview?.title}
        />
      )}
    </Modal>
  )
}

function PromotionReview({
  reading,
  run,
  ends,
  pullTitle,
}: {
  reading: DeploymentPromotionPreview
  run?: DeploymentEngineRun
  ends: Ends
  pullTitle?: string
}) {
  const commit = runCommit(run)
  const live = reading.target.liveRelease
  const pull = reading.source.kind === "preview" ? reading.source.preview : undefined
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
        <Notice
          key={blocker.code}
          tone="danger"
          title={pull ? "This preview cannot be promoted" : "This release cannot be promoted"}
        >
          {blocker.message}
        </Notice>
      ))}

      <section aria-label="Image" className="space-y-1.5">
        <p className="eyebrow">Image</p>
        <p data-slot="promotion-image" className="font-mono text-hint break-all text-foreground">
          {reading.source.imageId || reading.source.imageDigest}
        </p>
        <p className="text-hint text-muted-foreground">
          {pull ? `The image PR #${pull.number}'s preview built` : "The image staging ran"}, as it
          is on this server
          {reading.source.imageSizeBytes > 0 && ` (${bytes(reading.source.imageSizeBytes)})`}.{" "}
          {ends.Target} starts it as it is; nothing is built.
        </p>
      </section>

      <div className="space-y-2">
        <div className="flex min-w-0 items-center gap-3">
          {run && <RunActorMark run={run} />}
          <div className="min-w-0 flex-1 space-y-0.5">
            <p className="flex min-w-0 items-baseline gap-2 text-body font-medium">
              <span className="numeric shrink-0">
                {pull
                  ? `PR #${pull.number} · preview release #${reading.source.release.number}`
                  : `${reading.source.environmentName} release #${reading.source.release.number}`}
              </span>
            </p>
            <p className="flex min-w-0 items-center gap-1.5 text-hint text-muted-foreground">
              <ReleaseRevision revision={reading.source.release.sourceRevision} />
              <AuthorMark name={pull?.author ?? commit?.author} />
              <span className="min-w-0 truncate">
                {pull?.title ??
                  pullTitle ??
                  commit?.subject ??
                  `built ${relativeTime(reading.source.release.createdAt)}`}
              </span>
            </p>
            {pull?.approvedBy && (
              <p data-slot="promotion-approval" className="text-hint text-muted-foreground">
                This commit was approved by{" "}
                <span className="text-foreground">{pull.approvedBy}</span>
              </p>
            )}
          </div>
        </div>
        <ArrowDown aria-hidden className="ml-2.5 size-4 text-muted-foreground" />
        <div className="border-l-2 border-brand pl-3">
          <p className="text-body font-medium">{ends.Target}</p>
          <p className="text-hint text-muted-foreground">
            {live
              ? `Replaces release #${live.number}, live since ${relativeTime(live.activatedAt ?? live.createdAt)}`
              : "Its first release"}
          </p>
        </div>
      </div>

      {pull?.fork && (
        <Notice tone="warning" title="This pull request comes from a fork">
          Its code is from {pull.headRepository || "another repository"}. Staging runs it with
          staging&apos;s variables, secrets included, once it is promoted.
        </Notice>
      )}

      <section aria-label="Configuration" className="space-y-2">
        <p className="eyebrow">{ends.Target}&apos;s configuration</p>
        {pull && (
          <p data-slot="promotion-runs-with" className="text-body">
            The release runs with staging&apos;s plan and variables as they are saved now, not the
            preview&apos;s. The preview&apos;s own variables, and any production values copied into
            it, stay with the preview.
          </p>
        )}
        {reading.detail.status !== "available" ? (
          <p className="text-body text-muted-foreground">{reading.detail.reason}</p>
        ) : same ? (
          <p className="text-body">
            It matches the configuration {pull ? "the preview" : "staging"} ran this image with.
          </p>
        ) : (
          <div className="space-y-3">
            <p className="text-hint text-muted-foreground">
              {ends.Target} runs the image with its own settings and variables. These differ from{" "}
              {ends.sources}.
            </p>
            {fields.length > 0 && (
              <dl className="grid grid-cols-[minmax(0,10rem)_minmax(0,1fr)] gap-x-4 gap-y-1.5 text-body">
                {fields.map((field) => (
                  <div key={field.field} className="contents">
                    <dt className="truncate text-muted-foreground first-letter:uppercase">
                      {field.field}
                    </dt>
                    <dd className="min-w-0 font-mono text-hint break-all">
                      <span className="text-muted-foreground">{ends.source} </span>
                      {field.from || "none"}
                      <span className="text-muted-foreground"> · {ends.target} </span>
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
                        {differenceWord(change, ends)}
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
        <Notice tone="warning" title={`${ends.Target}'s release tasks run first`}>
          {reading.target.releaseTasks.join(", ")} run in this image against {ends.target}, with{" "}
          {ends.target}&apos;s variables and database, before traffic moves.
          {reading.target.rehearsal &&
            ` They are rehearsed first in this image against a copy of ${ends.target}'s database restored from its backup; a task that fails there stops the promotion with ${ends.target} untouched.`}
        </Notice>
      )}
      {reading.target.pendingChanges.length > 0 && (
        <Notice title={`${ends.Target}'s saved settings go live too`}>
          {ends.Target} has settings saved since its live release:{" "}
          {[...new Set(reading.target.pendingChanges.map((change) => change.kind))].join(", ")}. The
          promoted release runs with them.
        </Notice>
      )}
      {reading.target.activeRuns > 0 && (
        <Notice title={`A ${ends.target} run is ahead of this one`}>
          The promotion waits for it, then replaces whatever it releases.
        </Notice>
      )}
    </div>
  )
}

/** A difference from the source release's side, in the target's terms. */
function differenceWord(change: ReleaseListChange, ends: Ends) {
  switch (change.change) {
    case "added":
      return `only in ${ends.target}`
    case "removed":
      return `only in ${ends.source}`
    default:
      return change.secret ? "a different value" : `${change.from} · ${ends.target} ${change.to}`
  }
}
