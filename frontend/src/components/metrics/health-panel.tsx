"use client"

import { useState } from "react"
import { useRouter } from "next/navigation"
import type { Health, HealthFinding } from "@/lib/types"
import { relativeTime } from "@/lib/format"
import { healthInvestigation } from "@/lib/server-advisor"
import { HealthInvestigation } from "@/components/metrics/health-investigation"
import { ErrorState } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Skeleton } from "@/components/ui/skeleton"
import { Status } from "@/components/status-dot"
import { FindingList } from "@/components/finding-list"
import { type LinkedFinding, verdictWith, worstFirst } from "@/components/overview/attention"

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
  onChanged,
  error,
  also = [],
}: {
  health: Health | undefined
  loading: boolean
  /**
   * What the other modules found, read into the same list rather than a
   * second one beside it: two lists answering "what needs me" is one too
   * many. Each opens the page that owns it.
   */
  also?: LinkedFinding[]
  /** Drawn as a titled list on the page rather than in a frame — see `Panel`. */
  plain?: boolean
  /** What "nothing found" covers, when the caller has folded more checks in. */
  emptyLabel?: string
  className?: string
  error?: Error
  onChanged?: () => void
}) {
  const [selected, setSelected] = useState<HealthFinding | null>(null)
  const router = useRouter()
  const refresh = () => {
    onChanged?.()
    window.dispatchEvent(new Event("jd:health-changed"))
  }
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
  if (!health) return error ? <HealthReadError error={error} onRetry={refresh} /> : null
  const status = verdictWith(health.status, also)

  return (
    <Panel plain={plain} className={className}>
      <PanelHeader
        title="Health"
        actions={<HealthVerdict status={status} partial={!!health.silences?.length} />}
      />
      <PanelBody>
        {error && <HealthReadError error={error} onRetry={refresh} />}
        <FindingList
          findings={worstFirst([
            ...health.findings.map((finding) => {
              const target = healthInvestigation(finding.id)
              return {
                ...finding,
                action: target
                  ? {
                      label: target.kind === "link" ? target.label : "Investigate",
                      onClick: () =>
                        target.kind === "link" ? router.push(target.href) : setSelected(finding),
                    }
                  : undefined,
              }
            }),
            ...also.map(({ action, ...finding }) => ({
              ...finding,
              action: action && { label: action.label, onClick: () => router.push(action.href) },
            })),
          ])}
          emptyLabel={
            health.silences?.length
              ? "No findings in the completed checks"
              : (emptyLabel ??
                (health.recorded
                  ? "Capacity, memory, CPU steal, pressure and sockets all within limits"
                  : "Every check passed on the current reading"))
          }
        />
        <p className="mt-3 text-hint text-muted-foreground">
          Checked {relativeTime(health.checkedAt)}
        </p>
        {!!health.silences?.length && (
          <div className="mt-2 text-hint text-muted-foreground">
            <p className="font-medium">Not assessed</p>
            <ul className="list-disc pl-4">
              {health.silences.map((silence) => (
                <li key={silence}>{silence}</li>
              ))}
            </ul>
          </div>
        )}
      </PanelBody>
      <HealthInvestigation
        finding={selected}
        onOpenChange={(open) => !open && setSelected(null)}
        onChanged={refresh}
      />
    </Panel>
  )
}

/** The one-word verdict, small enough for the top bar and a panel header alike. */
export function HealthVerdict({
  status,
  className,
  partial = false,
}: {
  status: Health["status"]
  partial?: boolean
  className?: string
}) {
  return (
    <Status
      verdict={status}
      label={status === "ok" && partial ? "Partial assessment" : verdictLabel(status)}
      className={className}
    />
  )
}

function verdictLabel(status: Health["status"]) {
  if (status === "critical") return "Critical"
  if (status === "warning") return "Warning"
  if (status === "notice") return "Notice"
  return "Healthy"
}

function HealthReadError({ error, onRetry }: { error: Error; onRetry: () => void }) {
  return (
    <div className="space-y-2">
      <ErrorState error={error} />
      <Button size="xs" variant="outline" onClick={onRetry}>
        Try again
      </Button>
    </div>
  )
}
