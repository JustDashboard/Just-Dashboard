"use client"

import { useRef, useState } from "react"
import { useRouter } from "next/navigation"
import { ArrowDown, Globe, Pin } from "@/components/icons"
import { post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { plural, relativeTime } from "@/lib/format"
import { useMediaQuery } from "@/hooks/use-mobile"
import { Modal } from "@/components/modal"
import { Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { ChoiceCard } from "@/components/choice-card"
import { GroupRule } from "@/components/flow"
import { AuthorMark } from "@/components/git/marks"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { Button } from "@/components/ui/button"
import type { DeploymentEngineRun, DeploymentRelease } from "@/lib/types"
import { RunActorMark } from "@/components/deploy/run-marks"
import { WireMark } from "@/components/deploy/wire"
import { ReleaseRevision } from "@/components/deploy/release-comparison-sheet"
import { runCommit, runTitle, runTriggerLine } from "@/components/deploy/vocabulary"

/**
 * Roll back the live release to a retained one.
 *
 * Two steps: choose (skipped when a specific release is already named — the
 * Deployments row's "Roll back to this release" opens straight into review),
 * then a review that says what moves and what does not. Ordinary
 * confirmation is this dialog itself — nothing is typed, because rolling back
 * replaces one immutable, already-built release with another rather than
 * destroying anything (spec §5.7). For the same reason the command wears the
 * brand face rather than the danger red: it is the one thing this dialog is
 * for, and it can be undone by rolling forward.
 *
 * Choosing is picking one of a run of releases, so each is a card with the
 * lit edge (§16), led by the face or product of whoever deployed it. The
 * review draws the move itself: where your domains point now, where they will
 * point, as a picture from `sm` with the line to the release they are about
 * to reach dotted until it carries, then as the two releases one above the
 * other — which name both, so the picture carries no words of its own.
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
  remote,
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
  /** The project's source remote, so a push is drawn as the forge it came from. */
  remote?: string
  /** Preselects a release and opens straight into the review step. */
  initialReleaseId?: number
}) {
  const router = useRouter()
  const wide = useMediaQuery("(min-width: 640px)")
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
  const runOf = (candidate?: DeploymentRelease) =>
    candidate && runs.find((entry) => entry.id === candidate.runId)

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
        // On a phone the buttons share the row, so the command is never a
        // small target at the far edge.
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
            <Button disabled={!release} onClick={() => setStep("review")}>
              Continue
            </Button>
          ) : (
            <Button disabled={!release || busy} pending={busy} onClick={rollback}>
              Roll back
            </Button>
          )}
        </div>
      }
    >
      {step === "choose" ? (
        <div className="space-y-4">
          {liveRelease && (
            <ReleaseLine
              release={liveRelease}
              run={runOf(liveRelease)}
              remote={remote}
              status={<Status tone="running" label="Live" />}
            />
          )}
          <GroupRule label="Retained" count={eligible.length} />
          {eligible.length === 0 ? (
            <p className="py-6 text-center text-body text-muted-foreground">
              No retained release is eligible for rollback.
            </p>
          ) : (
            <div role="group" aria-label="Retained releases" className="grid gap-2">
              {eligible.map((candidate) => {
                const run = runOf(candidate)
                const commit = runCommit(run)
                return (
                  <ChoiceCard
                    key={candidate.id}
                    verb={`Release #${candidate.number}`}
                    selected={candidate.id === selected}
                    onClick={() => setSelected(candidate.id)}
                    className="min-h-0"
                    logo={run && <RunActorMark run={run} remote={remote} />}
                    title={
                      <span className="flex min-w-0 items-baseline gap-2">
                        <span className="numeric shrink-0">Release #{candidate.number}</span>
                        {run && (
                          <span className="truncate font-normal text-muted-foreground">
                            {runTitle(run)}
                          </span>
                        )}
                      </span>
                    }
                    description={
                      <span className="flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-1">
                        <ReleaseRevision revision={candidate.sourceRevision} />
                        <AuthorMark name={commit?.author} />
                        {commit?.subject && (
                          <span className="min-w-0 truncate text-foreground/80">
                            {commit.subject}
                          </span>
                        )}
                        <span className="flex basis-full items-center gap-2">
                          <span>
                            built {relativeTime(candidate.createdAt)}
                            {run && ` · ${runTriggerLine(run)}`}
                          </span>
                          {candidate.pinned && <Tag icon={Pin}>Pinned</Tag>}
                        </span>
                      </span>
                    }
                  />
                )
              })}
            </div>
          )}
        </div>
      ) : (
        release && (
          <div className="space-y-5">
            {wide && liveRelease && (
              <SwapPicture
                live={liveRelease.number}
                target={release.number}
                domains={domains.length}
                busy={busy}
              />
            )}
            <div className="space-y-2">
              {liveRelease && (
                <ReleaseLine
                  release={liveRelease}
                  run={runOf(liveRelease)}
                  remote={remote}
                  status={<Status tone="running" label="Live" />}
                />
              )}
              <ArrowDown aria-hidden className="ml-2.5 size-4 text-muted-foreground" />
              {/* Where you are going: the one row with a mark of place (§3). */}
              <div className="border-l-2 border-brand pl-3">
                <ReleaseLine release={release} run={runOf(release)} remote={remote} />
              </div>
            </div>

            <section aria-label="Domains" className="space-y-1.5">
              <p className="text-body font-medium">
                {domains.length === 0 ? "No public domain moves back" : "Domains that move back"}
              </p>
              {domains.length > 0 && (
                <ul className="space-y-1">
                  {domains.map((hostname) => (
                    <li key={hostname} className="flex min-w-0 items-center gap-2">
                      <Globe aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />
                      <span className="truncate font-mono text-xs text-foreground/85">
                        {hostname}
                      </span>
                    </li>
                  ))}
                </ul>
              )}
            </section>

            <Notice tone="warning" title="Databases and files are not rolled back">
              The application image, its variables and its configuration move back. Anything a
              database or a persistent volume holds stays exactly as it is now.
            </Notice>
          </div>
        )
      )}
    </Modal>
  )
}

/**
 * One release as two lines: its number and run, then its commit — the sha,
 * who wrote it and what it says — with when it was built and who deployed it.
 */
function ReleaseLine({
  release,
  run,
  remote,
  status,
}: {
  release: DeploymentRelease
  run?: DeploymentEngineRun
  remote?: string
  status?: React.ReactNode
}) {
  const commit = runCommit(run)
  return (
    <div className="flex min-w-0 items-center gap-3">
      {run && <RunActorMark run={run} remote={remote} />}
      <div className="min-w-0 flex-1 space-y-0.5">
        <p className="flex min-w-0 items-baseline gap-2 text-body font-medium">
          <span className="numeric shrink-0">Release #{release.number}</span>
          {run && (
            <span className="truncate font-normal text-muted-foreground">{runTitle(run)}</span>
          )}
        </p>
        <p className="flex min-w-0 items-center gap-1.5 text-hint text-muted-foreground">
          <ReleaseRevision revision={release.sourceRevision} />
          <AuthorMark name={commit?.author} />
          <span className="min-w-0 truncate">
            {commit?.subject ?? `built ${relativeTime(release.createdAt)}`}
          </span>
        </p>
      </div>
      {status}
    </div>
  )
}

/**
 * The move as the wiring pictures draw a route (§2): your domains on the
 * left, the live release and the one you are going back to on the right. The
 * line to the live one is solid and still — it carries now, and stays put —
 * and the line to the other is dotted, because it does not yet; once Roll back
 * is pressed it is the one line that carries, so the picture says which way
 * the traffic is moving.
 */
function SwapPicture({
  live,
  target,
  domains,
  busy,
}: {
  live: number
  target: number
  domains: number
  busy: boolean
}) {
  const container = useRef<HTMLDivElement>(null)
  const from = useRef<HTMLSpanElement>(null)
  const now = useRef<HTMLSpanElement>(null)
  const next = useRef<HTMLSpanElement>(null)
  return (
    <div
      ref={container}
      aria-hidden
      className="relative flex min-w-0 items-center justify-between gap-10 rounded-lg border border-hairline px-4 py-4"
    >
      <AnimatedBeam containerRef={container} fromRef={from} toRef={now} still />
      <AnimatedBeam
        containerRef={container}
        fromRef={from}
        toRef={next}
        still={!busy}
        dashed={!busy}
      />
      <div className="flex min-w-0 items-center gap-3">
        <div className="min-w-0 text-right">
          <p className="text-body font-medium">Your domains</p>
          <p className="text-hint text-muted-foreground">
            {domains ? plural(domains, "hostname") : "private service"}
          </p>
        </div>
        <span ref={from} className="relative z-10 flex">
          <WireMark tone="logo" size="md">
            <Globe />
          </WireMark>
        </span>
      </div>
      <div className="flex flex-col gap-4">
        <Node markRef={now} number={live} />
        <Node markRef={next} number={target} lit />
      </div>
    </div>
  )
}

function Node({
  markRef,
  number,
  lit,
}: {
  markRef: React.RefObject<HTMLSpanElement | null>
  number: number
  lit?: boolean
}) {
  return (
    <span ref={markRef} className="relative z-10 flex">
      <WireMark tone={lit ? "brand" : "neutral"} size="sm" shape="square">
        <span className="numeric text-xs font-semibold">#{number}</span>
      </WireMark>
    </span>
  )
}
