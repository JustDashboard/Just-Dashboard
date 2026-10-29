"use client"

import { useState } from "react"
import { get, post } from "@/lib/api"
import { percent, relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type { SleepStatus, WakeOutcome } from "@/lib/types"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { asleepShare, lastWakeLine, sleepShown, spanLength } from "@/components/deploy/sleep-model"

/**
 * An environment asleep by design (`deploy/sleep.go`): its container was
 * stopped because nobody had visited for the plan's idle minutes, and the
 * next request starts it. The panel says since when and why, how long a
 * visitor waits, and offers the one verb the state has: wake it now. Drawn
 * only while it is asleep or waking; an awake environment that sleeps says
 * so in its Runtime settings, not here.
 */
export function SleepPanel({
  projectId,
  environmentId,
  status,
  onChange,
}: {
  projectId: number
  environmentId: number
  status: SleepStatus
  /** Called after Wake now lands, so the page's own reads catch up. */
  onChange?: () => void
}) {
  const { can } = useAuth()
  const [waking, setWaking] = useState(false)
  const wakingNow = waking || status.state === "waking"

  const wake = async () => {
    setWaking(true)
    try {
      const answer = await post<{ outcome: WakeOutcome; sleep: SleepStatus }>(
        `/deploy/${projectId}/environments/${environmentId}/sleep/wake`,
        {},
      )
      notify.success("Awake", { description: answer.outcome.reason })
      onChange?.()
    } catch (error) {
      notify.error("Could not wake it", error)
    } finally {
      setWaking(false)
    }
  }

  const last = lastWakeLine(status)
  return (
    <Panel plain>
      <PanelHeader
        title={wakingNow ? "Waking up" : "Asleep"}
        actions={
          <>
            <Status
              tone="notice"
              label={wakingNow ? "Starting" : "Asleep by design"}
              live={wakingNow}
              className="animate-rise"
            />
            {can("service.control") && (
              <Button
                variant="outline"
                size="sm"
                pending={wakingNow}
                disabled={wakingNow}
                onClick={() => void wake()}
              >
                Wake now
              </Button>
            )}
          </>
        }
      />
      <PanelBody flush className="pt-1">
        <StatGrid columns={4} dense>
          <StatTile
            label="Asleep since"
            value={status.since ? relativeTime(status.since) : "—"}
            hint="stopped, not removed"
          />
          <StatTile
            label="Last request"
            value={status.lastRequestAt ? relativeTime(status.lastRequestAt) : "None"}
            hint={
              status.lastRequestAt
                ? `sleeps after ${status.idleMinutes} idle min`
                : "since it went live"
            }
          />
          <StatTile
            label="A visitor waits"
            value={`≤ ${status.holdSeconds} s`}
            hint={`woken within ${status.wakeTimeoutSeconds} s`}
          />
          <StatTile
            label="Asleep this week"
            value={spanLength(status.asleepSeconds)}
            hint={`${percent(asleepShare(status) * 100, 0)} of the last 7 days`}
          />
        </StatGrid>
        <p
          key={status.reason}
          className="max-w-3xl animate-rise px-5 pt-1 pb-4 text-hint leading-relaxed text-muted-foreground"
        >
          {status.reason} The next request to its domain starts it and is answered by it.
          {last && <> {last}.</>}
        </p>
      </PanelBody>
    </Panel>
  )
}

/**
 * The environment's sleep, read on a ten-second poll while the deployment
 * says it is asleep: the summary already carries that, so an awake
 * environment costs no read.
 */
export function useSleep(projectId: number, environmentId: number, enabled: boolean) {
  const base = `/deploy/${projectId}/environments/${environmentId}/sleep`
  const read = usePoll((signal) => get<SleepStatus>(base, undefined, signal), 10000, [base], {
    enabled,
  })
  const status = read.data ?? undefined
  return { status, shown: sleepShown(status), refresh: read.refresh }
}
