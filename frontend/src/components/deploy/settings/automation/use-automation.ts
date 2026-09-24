"use client"

import { useCallback } from "react"
import { get } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"
import type {
  DeploymentPreview,
  DeploymentPreviewApproval,
  DeploymentSchedule,
  DeploymentTrigger,
  NotificationChannel,
  TrafficAlertList,
} from "@/lib/types"

type Poll<T> = ReturnType<typeof usePoll<T>>

export type Automation = {
  triggers: Poll<DeploymentTrigger[]>
  schedules: Poll<DeploymentSchedule[]>
  previews: Poll<DeploymentPreview[]>
  approvals: Poll<DeploymentPreviewApproval[]>
  alerts: Poll<TrafficAlertList>
  channels: Poll<NotificationChannel[]>
  /** Everything again — after a change that shows up in more than one block. */
  refresh: () => void
}

/**
 * Every read the Automation page draws from, polled once for the whole page.
 *
 * Each block used to poll its own endpoints, so the readings at the top, the
 * picture of what deploys the project and the lists under them would each
 * have kept a copy of the same webhooks and schedules on a different clock —
 * three answers to "is anything firing?" a few seconds apart. One set of
 * polls now, on the cadences the blocks had: the engine's own records every
 * five seconds, the alert rules every fifteen (they are evaluated once a
 * minute), and the notification channels once, since they are edited on
 * another page.
 *
 * A legacy project has none of the engine's records, only its alerts, so the
 * rest are not asked for.
 */
export function useAutomation(
  projectId: number,
  environmentId: number,
  normalized: boolean,
): Automation {
  const base = `/deploy/${projectId}/environments/${environmentId}`
  const engine = { enabled: normalized && environmentId > 0 }
  const triggers = usePoll(
    (signal) => get<DeploymentTrigger[]>(`${base}/triggers`, undefined, signal),
    5000,
    [projectId, environmentId],
    engine,
  )
  const schedules = usePoll(
    (signal) => get<DeploymentSchedule[]>(`${base}/schedules`, undefined, signal),
    5000,
    [projectId, environmentId],
    engine,
  )
  const previews = usePoll(
    (signal) => get<DeploymentPreview[]>(`/deploy/${projectId}/previews`, undefined, signal),
    5000,
    [projectId],
    engine,
  )
  const approvals = usePoll(
    (signal) =>
      get<DeploymentPreviewApproval[]>(
        `/deploy/${projectId}/previews/approvals`,
        undefined,
        signal,
      ),
    5000,
    [projectId],
    engine,
  )
  const alerts = usePoll(
    (signal) => get<TrafficAlertList>(`/deploy/${projectId}/alerts`, undefined, signal),
    15000,
    [projectId],
  )
  const channels = usePoll(
    (signal) => get<NotificationChannel[]>("/deploy/notifications", undefined, signal),
    0,
    [],
  )

  const refreshTriggers = triggers.refresh
  const refreshSchedules = schedules.refresh
  const refreshPreviews = previews.refresh
  const refreshApprovals = approvals.refresh
  const refreshAlerts = alerts.refresh
  const refresh = useCallback(() => {
    refreshTriggers()
    refreshSchedules()
    refreshPreviews()
    refreshApprovals()
    refreshAlerts()
  }, [refreshTriggers, refreshSchedules, refreshPreviews, refreshApprovals, refreshAlerts])

  return { triggers, schedules, previews, approvals, alerts, channels, refresh }
}
