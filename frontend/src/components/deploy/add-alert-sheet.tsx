"use client"

import type { DeploymentRequests, NotificationChannel } from "@/lib/types"
import { latency, perMinute } from "@/lib/requests"
import { FormFact, FormFacts } from "@/components/form"
import { SidePanel } from "@/components/side-panel"
import { AlertForm } from "@/components/deploy/settings/traffic-alerts"

/**
 * Adding a traffic alert from the page that shows the traffic.
 *
 * The Logs page is where a reader finds out that nobody would have been told,
 * so the rule is written here, in a sheet over the readings that prompted it,
 * rather than on Automation settings two pages away. The form is Automation's
 * own (`AlertForm`) — one form for one rule, wherever it opens (§4) — and the
 * sheet opens on what the rule will watch as it stands now, so the line is
 * chosen against the reading rather than guessed.
 */
export function AddAlertSheet({
  open,
  onOpenChange,
  projectId,
  channels,
  requests,
  onAdded,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  projectId: number
  channels: NotificationChannel[]
  /** The page's last-hour reading, for what the rule would see today. */
  requests?: DeploymentRequests
  onAdded: () => void
}) {
  const summary = requests?.status === "available" ? requests.summary : undefined
  return (
    <SidePanel
      open={open}
      onOpenChange={onOpenChange}
      // The same title and width Automation opens this form at: one form is
      // drawn one way, and at the narrow width the three kind cards cut
      // their sentences short.
      title="Add alert"
      description="A rule over this deployment's request record, told to your notification channels once when it crosses its line and once when it comes back."
      width="md"
    >
      {summary && (
        <FormFacts className="mb-4 shrink-0 border-b border-hairline pb-3">
          <FormFact label="Now">
            <span className="numeric">{(summary.errorRate * 100).toFixed(1)}% failing</span>
          </FormFact>
          {summary.latency && (
            <FormFact label="p95">
              <span className="numeric">{latency(summary.latency.p95)}</span>
            </FormFact>
          )}
          <FormFact label="Rate">
            <span className="numeric">{perMinute(summary.perMinute)} per minute</span>
          </FormFact>
        </FormFacts>
      )}
      <AlertForm
        projectId={projectId}
        channels={channels}
        onDone={() => {
          onOpenChange(false)
          onAdded()
        }}
        onCancel={() => onOpenChange(false)}
      />
    </SidePanel>
  )
}
