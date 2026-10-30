"use client"

import { useEffect, useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { ArrowRight } from "@/components/icons"
import { get, post } from "@/lib/api"
import { percent, relativeTime } from "@/lib/format"
import { latency } from "@/lib/requests"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type { DeploymentEngineRun, RollbackWatch, RollbackWatchResponse } from "@/lib/types"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { useConfirm } from "@/components/confirm-dialog"
import {
  clock,
  latencyTone,
  shareOf,
  shareTone,
  trustedBaseline,
  warmLine,
  watchShown,
  watchTitle,
  windowRemaining,
} from "@/components/deploy/rollback-watch-model"

/**
 * The window after a blue/green cutover, while the release it replaced is
 * kept warm (`deploy/rollback_watch.go`): the new release's 5xx share and
 * p95 since it went live beside the old one's, how many requests the reading
 * stands on, and how long the window has left — four readings, read as the
 * project's other figures are. With skew protection the warm release still
 * answers the visitors it answered first; the readings are the new release's
 * own requests, and the countdown says how many the warm one answered. Its two verbs are the window's: switch back to
 * the warm release now, or keep the new one and give the warm one's memory
 * back. A window that rolled back stays for an hour with the reading that
 * decided it; one that closed clean is the expected answer and is not drawn.
 */
export function ReleaseWatch({
  projectId,
  state,
  onChange,
}: {
  projectId: number
  state: ReleaseWatchState & { watch: RollbackWatch }
  /** Called after a verb lands, so the page's own reads catch up. */
  onChange?: () => void
}) {
  const { can } = useAuth()
  const router = useRouter()
  const { confirm, dialog } = useConfirm()
  const [acting, setActing] = useState<"keep" | null>(null)
  const { base, watch, now } = state

  const rollBack = () =>
    confirm({
      title: "Roll back instantly",
      description: (
        <>
          Traffic goes back to release #{watch.warmReleaseNumber}&apos;s warm container, which has
          {watch.warmRunning
            ? " kept running since the cutover for the visitors it answered first,"
            : " been paused since the cutover,"}{" "}
          with release #{watch.warmReleaseNumber}&apos;s own configuration. Release #
          {watch.releaseNumber} is removed, and its visitors are answered by release #
          {watch.warmReleaseNumber}. Nothing is built or started anew.
        </>
      ),
      confirmLabel: "Roll back",
      workingLabel: "Rolling back…",
      action: async () => {
        const run = await post<DeploymentEngineRun>(`${base}/rollback`)
        onChange?.()
        router.push(`/deploy/${projectId}/runs/${run.id}`)
      },
    })

  const keep = async () => {
    setActing("keep")
    try {
      await post(`${base}/keep`)
      notify.success(
        `Kept release #${watch.releaseNumber}; release #${watch.warmReleaseNumber} was removed`,
      )
      state.refresh()
      onChange?.()
    } catch (error) {
      notify.error("Could not keep the release", error)
    } finally {
      setActing(null)
    }
  }

  const actionable = watch.state === "watching" && !watch.rollbackRunId
  return (
    <Panel plain>
      <PanelHeader
        title={watchTitle(watch)}
        actions={
          watch.state === "rolled_back" ? (
            <>
              <Status tone="danger" label={rolledBackBy(watch)} className="animate-rise" />
              {watch.rollbackRunId && (
                <Link
                  href={`/deploy/${projectId}/runs/${watch.rollbackRunId}`}
                  className="inline-flex items-center gap-1 rounded-sm text-hint font-medium text-muted-foreground focus-ring hover:text-foreground"
                >
                  Open the rollback <ArrowRight className="size-3" />
                </Link>
              )}
            </>
          ) : (
            actionable && (
              <>
                {can("service.control") && (
                  <Button
                    variant="outline"
                    size="sm"
                    pending={acting === "keep"}
                    disabled={acting !== null}
                    onClick={() => void keep()}
                  >
                    Keep release #{watch.releaseNumber}
                  </Button>
                )}
                {can("destructive") && (
                  <Button variant="outline" size="sm" disabled={acting !== null} onClick={rollBack}>
                    Roll back instantly
                  </Button>
                )}
              </>
            )
          )
        }
      />
      <PanelBody flush className="pt-1">
        {watch.state === "rolled_back" ? (
          <p className="max-w-3xl px-5 py-4 text-sm leading-relaxed text-muted-foreground">
            {watch.endMessage}
            {watch.endedAt && <> · {relativeTime(watch.endedAt)}</>}
          </p>
        ) : (
          <WatchReadings watch={watch} remaining={windowRemaining(watch, now)} />
        )}
      </PanelBody>
      {dialog}
    </Panel>
  )
}

export type ReleaseWatchState = ReturnType<typeof useReleaseWatch>

/**
 * The environment's window, read on a four-second poll, and whether the
 * overview draws it now. The time ticks each second while a window counts
 * down, and while a rollback is on screen, so it leaves when its hour is up.
 */
export function useReleaseWatch(projectId: number, environmentId: number, enabled: boolean) {
  const base = `/deploy/${projectId}/environments/${environmentId}/rollback-watch`
  const read = usePoll(
    (signal) => get<RollbackWatchResponse>(base, undefined, signal),
    4000,
    [base],
    { enabled },
  )
  const watch = read.data?.watch ?? null
  const now = useNow(Boolean(watch) && watch?.state !== "closed")
  return { base, watch, now, shown: watchShown(watch, now), refresh: read.refresh }
}

function WatchReadings({ watch, remaining }: { watch: RollbackWatch; remaining: number }) {
  const baseline = trustedBaseline(watch)
  const observed = watch.observed
  const before = shareOf(baseline)
  const verdict = watch.verdict
  const noRoute = verdict?.code === "no_route"
  return (
    <>
      <StatGrid columns={4} dense>
        <StatTile
          label="5xx since cutover"
          value={noRoute ? "—" : percent(shareOf(observed), 1)}
          tone={shareTone(watch)}
          hint={
            before !== undefined
              ? `${percent(before, 1)} before`
              : watch.baseline?.requests
                ? "too little before to compare"
                : "no traffic before"
          }
        />
        <StatTile
          label="p95"
          value={noRoute ? "—" : latency(observed?.p95)}
          tone={latencyTone(watch)}
          hint={
            verdict && !verdict.latencyJudged && verdict.judged
              ? "not judged"
              : baseline?.p95 !== undefined
                ? `${latency(baseline.p95)} before`
                : "nothing to compare"
          }
        />
        <StatTile
          label="Requests"
          value={noRoute ? "—" : (observed?.requests ?? 0).toLocaleString()}
          hint={`${watch.policy.minRequests} decide`}
        />
        <StatTile
          label="Window"
          value={watch.state === "watching" ? clock(remaining) : "—"}
          hint={warmLine(watch)}
        />
      </StatGrid>
      {verdict && (
        <p
          key={verdict.reason}
          className="max-w-3xl animate-rise px-5 pt-1 pb-4 text-hint leading-relaxed text-muted-foreground"
        >
          {watch.policy.automatic || noRoute
            ? verdict.reason
            : `${verdict.reason} Nothing is rolled back automatically in this environment.`}
        </p>
      )}
    </>
  )
}

function rolledBackBy(watch: RollbackWatch) {
  return watch.endMessage?.includes("Rolled back instantly by")
    ? "Rolled back by hand"
    : "Rolled back automatically"
}

/** The time, ticking each second while a window counts down. */
function useNow(ticking: boolean) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (!ticking) return
    const timer = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [ticking])
  return now
}
