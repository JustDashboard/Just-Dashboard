"use client"

import { Well } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { Status } from "@/components/status-dot"
import { SettingCard } from "@/components/deploy/settings/setting-card"
import { PendingChanges } from "@/components/deploy/settings/pending-changes"
import {
  ConfigurationState,
  useConfiguration,
} from "@/components/deploy/settings/use-configuration"
import { Previews } from "@/components/deploy/settings/automation/previews"
import { Schedules } from "@/components/deploy/settings/automation/schedules"
import { Webhooks } from "@/components/deploy/settings/automation/webhooks"
import { TrafficAlerts } from "@/components/deploy/settings/traffic-alerts"
import { useProject } from "@/components/deploy/project-context"

/**
 * Automation — what deploys itself, on what clock, and what a pull request
 * gets before it is merged. A legacy Compose project keeps only its original
 * signed hook; everything else here belongs to the persistent engine.
 */
export function AutomationSettings({
  projectId,
  environmentId,
}: {
  projectId: number
  environmentId: number
}) {
  const state = useConfiguration(projectId, environmentId)
  const project = useProject()
  return (
    <ConfigurationState state={state}>
      {(configuration) => (
        <div key={configuration.revision} className="space-y-6">
          <PendingChanges pending={configuration.pending} />
          {project.normalized ? (
            <>
              <Webhooks projectId={projectId} environmentId={environmentId} />
              <Schedules projectId={projectId} environmentId={environmentId} />
              <Previews projectId={projectId} />
            </>
          ) : (
            <LegacyHook />
          )}
          <TrafficAlerts projectId={projectId} />
          <RowList aria-label="Automation links">
            <Row href="/deploy/notifications" title="Notification channels" />
          </RowList>
        </div>
      )}
    </ConfigurationState>
  )
}

function LegacyHook() {
  const project = useProject()
  const record = project.detail.project
  return (
    <SettingCard title="Legacy deployment hook">
      <div className="space-y-3">
        <Status
          tone={record.enabled ? "running" : "stopped"}
          label={record.enabled ? "Enabled" : "Disabled"}
        />
        {record.hookUrl ? (
          <Well className="break-all select-all">{record.hookUrl}</Well>
        ) : (
          <p className="text-hint text-muted-foreground">No hook URL is available.</p>
        )}
      </div>
    </SettingCard>
  )
}
