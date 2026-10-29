"use client"

import { useEffect, useState } from "react"
import { get, post } from "@/lib/api"
import { percent, relativeTime } from "@/lib/format"
import { latency } from "@/lib/requests"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type { DeploymentCanary, DeploymentCanaryResponse, RollbackReading } from "@/lib/types"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { useConfirm } from "@/components/confirm-dialog"
import { clock } from "@/components/deploy/rollback-watch-model"
import {
  canaryEndLabel,
  canaryOpen,
  canaryProgress,
  canaryRemaining,
  canaryTitle,
  candidateLatencyTone,
  candidateShareTone,
  share,
} from "@/components/deploy/canary-model"
import type { Tone } from "@/components/tone"
import { cn } from "@/lib/utils"

/**
 * A run's canary (`deploy/canary.go`): the new release on its share of the
 * requests beside the live one, the two read side by side as they come in —
 * requests, the share answered 5xx, p95 — the window's countdown, and the two
 * verbs the trial has: promote now, or abort, which removes the candidate
 * with the live release untouched. A trial that ended stays as its outcome.
 */
export function RunCanary({
  projectId,
  environmentId,
  runId,
  active,
}: {
  projectId: number
  environmentId: number
  runId: number
  /** The run's canary step is running, so the trial is read on a short poll. */
  active: boolean
}) {
  const base = `/deploy/${projectId}/environments/${environmentId}/canary`
  const read = usePoll(
    (signal) => get<DeploymentCanaryResponse>(base, undefined, signal),
    active ? 2000 : 30000,
    [base, active],
  )
  const canary = read.data?.canary
  const now = useNow(active)
  if (!canary || canary.runId !== runId) return null
  return <CanaryPanel canary={canary} base={base} now={now} onChange={read.refresh} />
}

function CanaryPanel({
  canary,
  base,
  now,
  onChange,
}: {
  canary: DeploymentCanary
  base: string
  now: number
  onChange: () => void
}) {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const open = canaryOpen(canary) && !canary.decision
  const remaining = canaryRemaining(canary, now)

  const decide = (verb: "promote" | "abort") =>
    confirm({
      title: verb === "promote" ? `Promote release #${canary.releaseNumber}` : "Abort the canary",
      description:
        verb === "promote" ? (
          <>
            Release #{canary.releaseNumber} takes every request now, and release #
            {canary.liveReleaseNumber} is kept warm as after any cutover.
          </>
        ) : (
          <>
            Release #{canary.releaseNumber} is removed and release #{canary.liveReleaseNumber} keeps
            every request. Nothing about the live release changes.
          </>
        ),
      confirmLabel: verb === "promote" ? "Promote" : "Abort",
      workingLabel: verb === "promote" ? "Promoting…" : "Aborting…",
      action: async () => {
        try {
          await post(`${base}/${verb}`)
        } catch (error) {
          notify.error(verb === "promote" ? "Could not promote" : "Could not abort", error)
          throw error
        }
        onChange()
      },
    })

  return (
    <Panel plain aria-label="Canary trial">
      <PanelHeader
        title={canaryTitle(canary)}
        actions={
          open ? (
            can("service.control") && (
              <>
                <Button variant="outline" size="sm" onClick={() => decide("abort")}>
                  Abort
                </Button>
                <Button size="sm" onClick={() => decide("promote")}>
                  Promote now
                </Button>
              </>
            )
          ) : !canaryOpen(canary) ? (
            <Status
              tone={canary.state === "promoted" ? "running" : "danger"}
              label={canaryEndLabel(canary.endReason)}
              className="animate-rise"
            />
          ) : null
        }
      />
      <PanelBody flush className="pt-1">
        {canaryOpen(canary) && (
          <div className="px-5 pb-3">
            <div className="flex items-baseline justify-between gap-3 text-hint text-muted-foreground">
              <span className="numeric">
                {canary.state === "holding"
                  ? "window passed, waiting for you"
                  : `${clock(remaining)} left`}
              </span>
              <span>
                {canary.autoPromote ? "promoted when it does no worse" : "promoted by hand"}
              </span>
            </div>
            <div
              className="mt-2 h-1 overflow-hidden rounded-full bg-muted"
              role="progressbar"
              aria-label="Canary window"
              aria-valuemin={0}
              aria-valuemax={100}
              aria-valuenow={Math.round(canaryProgress(canary, now) * 100)}
            >
              <div
                className="h-full bg-primary transition-[width] duration-1000"
                style={{ width: `${canaryProgress(canary, now) * 100}%` }}
              />
            </div>
          </div>
        )}
        <table className="w-full text-body" aria-label="The two releases side by side">
          <thead>
            <tr className="text-left text-hint text-muted-foreground">
              <th className="px-5 py-2 font-normal">Release</th>
              <th className="px-3 py-2 text-right font-normal">Requests</th>
              <th className="px-3 py-2 text-right font-normal">5xx</th>
              <th className="px-5 py-2 text-right font-normal">p95</th>
            </tr>
          </thead>
          <tbody>
            <ReadingRow
              name={`#${canary.releaseNumber}`}
              role="canary"
              reading={canary.candidate}
              shareTone={candidateShareTone(canary)}
              latencyTone={candidateLatencyTone(canary)}
            />
            <ReadingRow name={`#${canary.liveReleaseNumber}`} role="live" reading={canary.live} />
          </tbody>
        </table>
        {(canary.endMessage || canary.verdict) && (
          <p
            key={canary.endMessage ?? canary.verdict?.reason}
            className="max-w-3xl animate-rise px-5 pt-2 pb-4 text-hint leading-relaxed text-muted-foreground"
          >
            {canaryOpen(canary) ? canary.verdict?.reason : canary.endMessage}
            {!canaryOpen(canary) && canary.endedAt && <> · {relativeTime(canary.endedAt)}</>}
          </p>
        )}
      </PanelBody>
      {dialog}
    </Panel>
  )
}

const TONE_CLASS: Record<Tone, string> = {
  default: "",
  success: "text-success",
  warning: "text-warning",
  danger: "text-destructive",
}

function ReadingRow({
  name,
  role,
  reading,
  shareTone = "default",
  latencyTone = "default",
}: {
  name: string
  role: "canary" | "live"
  reading?: RollbackReading
  shareTone?: Tone
  latencyTone?: Tone
}) {
  const failing = share(reading)
  return (
    <tr className="border-t border-hairline">
      <td className="px-5 py-2">
        <span className="numeric font-medium">{name}</span>{" "}
        <span className="text-hint text-muted-foreground">{role}</span>
      </td>
      <td className="numeric px-3 py-2 text-right">{(reading?.requests ?? 0).toLocaleString()}</td>
      <td className={cn("numeric px-3 py-2 text-right", TONE_CLASS[shareTone])}>
        {failing === undefined ? "—" : percent(failing, 1)}
      </td>
      <td className={cn("numeric px-5 py-2 text-right", TONE_CLASS[latencyTone])}>
        {latency(reading?.p95)}
      </td>
    </tr>
  )
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
