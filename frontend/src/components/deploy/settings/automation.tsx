"use client"

import { Copy } from "@/components/icons"
import { copyText } from "@/lib/clipboard"
import { plural, relativeTime } from "@/lib/format"
import { describeCron } from "@/lib/cron"
import { useAuth } from "@/hooks/use-auth"
import { Field } from "@/components/form"
import { Well } from "@/components/panel"
import { StatGrid, StatLink, StatTile } from "@/components/stat-tile"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { NumberTicker } from "@/components/ui/number-ticker"
import { Skeleton } from "@/components/ui/skeleton"
import { SettingSection, SettingsPage } from "@/components/deploy/settings/setting-card"
import { useConfiguration } from "@/components/deploy/settings/use-configuration"
import { ALERT_KINDS } from "@/lib/requests"
import {
  RUN_LABELS,
  runFailed,
  runTitle,
  runTriggerLine,
  sourceProduct,
} from "@/components/deploy/vocabulary"
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
import { overdue, soonest, untilLabel } from "@/components/deploy/settings/automation/marks"
import {
  useAutomation,
  type Automation,
} from "@/components/deploy/settings/automation/use-automation"

/**
 * Automation — what deploys the project by itself, on what clock, what a pull
 * request gets before it is merged, and who is told when its traffic goes
 * wrong.
 *
 * It opens on four readings — the last run nothing pressed a button for, the
 * next one a clock will start, the revisions waiting for a look, and the
 * alerts firing — then draws the senders as a picture, then lays the four
 * blocks out as the other settings pages do: heads in a rail, each carrying
 * what its block currently is as data, the webhooks, schedules and previews
 * as cards that open their own sheets, and the alert rules as sentences.
 *
 * Every block reads from one set of polls (`useAutomation`), so the readings,
 * the picture and the lists agree. The Add webhook and Add schedule sheets are
 * held here rather than in their blocks: the picture's rings and the previews'
 * "Turn on previews" open them too, and a configuration re-read under them
 * never empties a half-filled form.
 *
 * A legacy Compose project keeps only its original signed hook and its alerts;
 * the rest belongs to the persistent engine, so it has no readings and no
 * picture.
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
      <SettingsPage
        state={state}
        readings={
          project.normalized ? () => <AutomationReadings automation={automation} /> : undefined
        }
      >
        {() =>
          project.normalized ? (
            <>
              <SettingSection
                id="wiring"
                title="What deploys it"
                state={senders(automation, watching?.automatic ? watching.branch : undefined)}
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
                branch={watching?.automatic ? watching.branch : undefined}
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

/** "on push to main · 2 webhooks · 1 schedule" — what starts it, as a line of data. */
function senders(automation: Automation, branch?: string) {
  const parts = [
    branch && `on push to ${branch}`,
    automation.triggers.data?.length && plural(automation.triggers.data.length, "webhook"),
    automation.schedules.data?.length && plural(automation.schedules.data.length, "schedule"),
  ].filter(Boolean)
  return parts.length > 0 ? parts.join(" · ") : "Nothing yet — it deploys when you press Deploy."
}

/** The run triggers a person asks for; everything else started by itself. */
const BY_HAND = new Set(["manual", "rollback", "migration"])

/**
 * The page's four readings. Each figure swaps its key from the placeholder
 * to the value, so it rises when its own poll settles (§11), and none of them
 * repeats a webhook's or a schedule's name as its own text — the lists below
 * are where those are found by name.
 */
function AutomationReadings({ automation }: { automation: Automation }) {
  const project = useProject()
  const automatic = project.runs.find(
    (run) => !BY_HAND.has(run.trigger) && run.environmentId === project.environmentId,
  )
  const schedules = automation.schedules.data
  const next = schedules ? soonest(schedules) : undefined
  const approvals = automation.approvals.data
  const awaiting = approvals?.filter((approval) => approval.state === "pending").length ?? 0
  const open = automation.previews.data?.filter((preview) => preview.state === "open").length ?? 0
  const rules = automation.alerts.data?.alerts
  const firing = rules?.filter((rule) => rule.enabled && rule.state === "firing") ?? []
  const checked = rules
    ?.map((rule) => rule.checkedAt)
    .filter(Boolean)
    .sort()
    .at(-1)

  const loading = (label: string) => (
    <StatTile
      key={`${label} loading`}
      label={label}
      value={<Skeleton className="inline-block h-6 w-20 align-middle" />}
      hint="reading…"
    />
  )
  // A read that failed before it ever answered: the section below says why,
  // and the figure stops claiming it is still being read.
  const unread = (label: string, what: string) => (
    <StatTile key={`${label} unread`} label={label} value="—" hint={`could not read the ${what}`} />
  )

  return (
    <StatGrid columns={4} dense>
      {project.runsLoading ? (
        loading("Last automatic run")
      ) : automatic ? (
        <StatLink
          key="run"
          href={`/deploy/${project.projectId}/runs/${automatic.id}`}
          label="Open the last automatic run"
        >
          <StatTile
            className="h-full animate-rise transition-colors group-hover:bg-row-hover"
            label="Last automatic run"
            value={relativeTime(automatic.requestedAt)}
            tone={
              automatic.state === "rolled_back"
                ? "warning"
                : runFailed(automatic.state)
                  ? "danger"
                  : "default"
            }
            hint={`${runTitle(automatic)} · ${runTriggerLine(automatic)} · ${RUN_LABELS[automatic.state] ?? automatic.state}`}
          />
        </StatLink>
      ) : (
        <StatTile
          key="run none"
          className="animate-rise"
          label="Last automatic run"
          value="None yet"
          hint={
            project.runs.length === 0
              ? "no runs yet"
              : project.runs.length === 1
                ? "nothing automatic in the last run"
                : `nothing automatic in the last ${plural(project.runs.length, "run")}`
          }
        />
      )}
      {automation.schedules.error && !schedules ? (
        unread("Next scheduled", "schedules")
      ) : !schedules ? (
        loading("Next scheduled")
      ) : (
        <StatTile
          key="next"
          className="animate-rise"
          label="Next scheduled"
          value={next ? untilLabel(next.nextRunAt) : "Nothing"}
          tone={next && overdue(next.nextRunAt) ? "warning" : "default"}
          hint={
            next ? (
              <>
                {describeCron(next.expression)}
                {/* Two tiles to a row on a phone: the sentence keeps its room. */}
                <span className="max-sm:hidden"> · {next.timezone}</span>
              </>
            ) : schedules.length > 0 ? (
              "every schedule is paused"
            ) : (
              "no schedule enabled"
            )
          }
        />
      )}
      {automation.approvals.error && !approvals ? (
        unread("Awaiting review", "approvals")
      ) : !approvals ? (
        loading("Awaiting review")
      ) : (
        <StatTile
          key="review"
          className="animate-rise"
          label="Awaiting review"
          value={<NumberTicker value={awaiting} />}
          tone={awaiting > 0 ? "warning" : "default"}
          hint={`${plural(open, "preview")} open`}
        />
      )}
      {automation.alerts.error && !rules ? (
        unread("Alerts firing", "alert rules")
      ) : !rules ? (
        loading("Alerts firing")
      ) : (
        <StatTile
          key="alerts"
          className="animate-rise"
          label="Alerts firing"
          value={<NumberTicker value={firing.length} />}
          tone={firing.length > 0 ? "danger" : "default"}
          hint={
            firing[0] ? (
              <>
                {/* On a phone the reading keeps the room, as the zone gives way above. */}
                <span className="max-sm:hidden">{ALERT_KINDS[firing[0].kind].label} · </span>
                {ALERT_KINDS[firing[0].kind].read(firing[0].observed)}
                {firing[0].stateSince && ` since ${relativeTime(firing[0].stateSince)}`}
              </>
            ) : rules.length > 0 ? (
              `${plural(rules.length, "rule")}${checked ? ` · checked ${relativeTime(checked)}` : ""}`
            ) : (
              "no rules yet"
            )
          }
        />
      )}
    </StatGrid>
  )
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
