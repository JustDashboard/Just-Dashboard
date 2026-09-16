"use client"

import type { Health } from "@/lib/types"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Skeleton } from "@/components/ui/skeleton"
import { Status } from "@/components/status-dot"
import { FindingList } from "@/components/finding-list"

/**
 * What the numbers mean.
 *
 * Every dashboard in this class shows utilisation and stops there, leaving the
 * reader to know that 3% steal is bad, that 88% memory usually is not, and
 * that a filesystem at 30% can still refuse to create a file. So each finding
 * carries three things — what was measured, what it means, what to do — and
 * `FindingList` renders them as a list, not a wall of coloured alert boxes.
 */
export function HealthPanel({
  health,
  loading,
  plain,
  emptyLabel,
  className,
}: {
  health: Health | undefined
  loading: boolean
  /** Drawn as a titled list on the page rather than in a frame — see `Panel`. */
  plain?: boolean
  /** What "nothing found" covers, when the caller has folded more checks in. */
  emptyLabel?: string
  className?: string
}) {
  if (loading && !health) {
    return (
      <Panel plain={plain} className={className}>
        <PanelHeader title="Health" />
        <PanelBody className="space-y-2">
          <Skeleton className="h-4 w-40" />
          <Skeleton className="h-4 w-64" />
        </PanelBody>
      </Panel>
    )
  }
  if (!health) return null

  return (
    <Panel plain={plain} className={className}>
      <PanelHeader
        title="Health"
        actions={<Status verdict={health.status} label={verdictLabel(health.status)} />}
      />
      <PanelBody>
        <FindingList
          findings={health.findings}
          emptyLabel={
            emptyLabel ??
            (health.recorded
              ? "Capacity, memory, CPU steal, pressure and sockets all within limits"
              : "Every check passed on the current reading")
          }
        />
      </PanelBody>
    </Panel>
  )
}

/** The one-word verdict, small enough for the top bar and a panel header alike. */
export function HealthVerdict({
  status,
  className,
}: {
  status: Health["status"]
  className?: string
}) {
  return <Status verdict={status} label={verdictLabel(status)} className={className} />
}

function verdictLabel(status: Health["status"]) {
  if (status === "critical") return "Critical"
  if (status === "warning") return "Warning"
  if (status === "notice") return "Notice"
  return "Healthy"
}
