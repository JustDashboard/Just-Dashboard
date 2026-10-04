"use client"

import { Copy } from "@/components/icons"
import { copyText } from "@/lib/clipboard"
import { useAuth } from "@/hooks/use-auth"
import { Field } from "@/components/form"
import { Well } from "@/components/panel"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { SettingSection, SettingsPage } from "@/components/deploy/settings/setting-card"
import { useConfiguration } from "@/components/deploy/settings/use-configuration"
import { sourceProduct } from "@/components/deploy/vocabulary"
import { useProject } from "@/components/deploy/project-context"
import { TrafficAlerts } from "@/components/deploy/settings/traffic-alerts"
import { Previews } from "@/components/deploy/settings/automation/previews"
import {
  ScheduleSheet,
  Schedules,
  useScheduleSheet,
} from "@/components/deploy/settings/automation/schedules"
import {
  WebhookSheet,
  Webhooks,
  sourceHint,
  useWebhookSheet,
} from "@/components/deploy/settings/automation/webhooks"
import { AutomationWiring } from "@/components/deploy/settings/automation/wiring"
import {
  useAutomation,
  type Automation,
} from "@/components/deploy/settings/automation/use-automation"

/**
 * Automation — what deploys the project by itself, on what clock, what a pull
 * request gets before it is merged, and who is told when its traffic goes
 * wrong.
 *
 * It opens on the senders drawn as a picture, then lays the four blocks out as
 * the other settings pages do: each head over its block, carrying what its
 * block currently is as data, the webhooks, schedules and previews as cards
 * that open their own sheets, and the alert rules as sentences.
 *
 * A head's state is one line — how many there are, how many are off, who is
 * told — and holds nothing its cards or the picture already say; its far end
 * holds the one button that adds to the block, as every settings head does.
 * The counts sat beside that button at 11px, a second line of data in a
 * second place, and the state under the title restated what the first card
 * said: the repository, the timezone, the sentence about what a webhook is.
 *
 * Every block reads from one set of polls (`useAutomation`), so the picture
 * and the lists agree. The Add webhook and Add schedule sheets are
 * held here rather than in their blocks: the picture's rings and the previews'
 * "Turn on previews" open them too, and a configuration re-read under them
 * never empties a half-filled form.
 *
 * A legacy Compose project keeps only its original signed hook and its alerts;
 * the rest belongs to the persistent engine, so it has no picture.
 */
export function AutomationSettings({
  projectId,
  environmentId,
}: {
  projectId: number
  environmentId: number
}) {
  const { can } = useAuth()
  const canAdmin = can("system.admin")
  const project = useProject()
  const state = useConfiguration(projectId, environmentId)
  const automation = useAutomation(projectId, environmentId, project.normalized)
  const summary = project.detail.deployment
  const source = sourceHint(state.configuration?.source ?? project.configuration?.source, summary)
  const webhookSheet = useWebhookSheet(source)
  const scheduleSheet = useScheduleSheet(projectId, environmentId)
  const watching =
    project.gitWatch && project.gitWatch.status !== "not_applicable" ? project.gitWatch : undefined

  return (
    <>
      <SettingsPage state={state}>
        {() =>
          project.normalized ? (
            <>
              <SettingSection
                id="wiring"
                title="What deploys it"
                state={
                  byHandOnly(automation, Boolean(watching?.automatic)) && (
                    // General's picture says it in the same words, for the same state.
                    <span className="block animate-rise">Only when you press Deploy</span>
                  )
                }
              >
                <AutomationWiring
                  deployment={summary}
                  release={project.liveRelease}
                  gitWatch={watching}
                  gitProduct={sourceProduct(summary, state.configuration?.source)}
                  repository={source.repository}
                  triggers={automation.triggers.data ?? []}
                  schedules={automation.schedules.data ?? []}
                  previews={automation.previews.data ?? []}
                  onAddWebhook={canAdmin ? () => webhookSheet.openAdd() : undefined}
                  onAddSchedule={canAdmin ? () => scheduleSheet.openAdd() : undefined}
                  onTurnOnPreviews={
                    canAdmin ? () => webhookSheet.openAdd({ preview: true }) : undefined
                  }
                />
              </SettingSection>
              <Webhooks
                projectId={projectId}
                environmentId={environmentId}
                automation={automation}
                sheet={webhookSheet}
              />
              <Schedules
                projectId={projectId}
                environmentId={environmentId}
                automation={automation}
                sheet={scheduleSheet}
              />
              <Previews
                projectId={projectId}
                automation={automation}
                source={source}
                onTurnOn={() => webhookSheet.openAdd({ preview: true })}
              />
              <TrafficAlerts projectId={projectId} automation={automation} />
            </>
          ) : (
            <>
              <LegacyHook />
              <TrafficAlerts projectId={projectId} automation={automation} />
            </>
          )
        }
      </SettingsPage>
      {project.normalized && (
        <>
          <WebhookSheet
            projectId={projectId}
            environmentId={environmentId}
            sheet={webhookSheet}
            source={source}
            onSaved={automation.refresh}
          />
          <ScheduleSheet
            projectId={projectId}
            environmentId={environmentId}
            sheet={scheduleSheet}
            onSaved={automation.refresh}
          />
        </>
      )}
    </>
  )
}

/**
 * Whether nothing deploys it by itself, once the lists have been read. The
 * head says so only then: every sender there is stands in the picture right
 * under it, and a line counting them was each of them said twice. With none
 * to draw, the picture shows rings where one could go, and nothing in it says
 * that until one is added only Deploy deploys it.
 */
function byHandOnly(automation: Automation, watching: boolean) {
  const { triggers, schedules } = automation
  return !watching && triggers.data?.length === 0 && schedules.data?.length === 0
}

/**
 * The signed hook a legacy Compose project was created with: whether it is
 * on, and its address to copy.
 */
function LegacyHook() {
  const project = useProject()
  const record = project.detail.project
  // Drawn only once the configuration has been read in the browser.
  const origin = window.location.origin
  return (
    <SettingSection
      title="Legacy deployment hook"
      state={
        <Status
          tone={record.enabled ? "running" : "stopped"}
          label={record.enabled ? "Enabled" : "Disabled"}
        />
      }
    >
      {record.hookUrl ? (
        <Field
          label="Hook URL"
          trailing={
            <Button
              type="button"
              size="xs"
              variant="ghost"
              onClick={() => void copyText(`${origin}${record.hookUrl}`, "Hook URL copied")}
            >
              <Copy /> Copy
            </Button>
          }
        >
          {/* Whole, as it is copied and as the webhook sheets show theirs; the
              path is its own element, the part the hook is known by. */}
          <Well className="break-all select-all">
            <span className="text-muted-foreground">{origin}</span>
            <span>{record.hookUrl}</span>
          </Well>
        </Field>
      ) : (
        <p className="text-hint text-muted-foreground">No hook URL is available.</p>
      )}
    </SettingSection>
  )
}
