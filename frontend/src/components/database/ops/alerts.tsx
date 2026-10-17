"use client"

import { useState } from "react"
import Link from "next/link"
import { PaperAirplane, Pause, Pencil, Play, Plus, Trash } from "@/components/icons"
import { del, get, post, put } from "@/lib/api"
import { notify } from "@/lib/toast"
import { plural } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { DbAlertKind, NotificationChannel } from "@/lib/types"
import { useArrivals } from "@/hooks/use-arrivals"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { ChoiceGrid } from "@/components/choice-card"
import { useConfirm } from "@/components/confirm-dialog"
import { ChannelNames, toldBy } from "@/components/deploy/settings/traffic-alerts"
import { Panel, PanelHeader } from "@/components/panel"
import { SidePanel } from "@/components/side-panel"
import { EmptyNote, EmptyState, ErrorState, LoadingPanel } from "@/components/state"
import { Button } from "@/components/ui/button"
import type { Verb } from "@/components/verbs"
import { EngineMark, SectionFrame } from "@/components/database/kit"
import { AlertForm } from "@/components/database/ops/alerts-form"
import {
  ALERT_KINDS,
  draftBody,
  draftFor,
  isBackupKind,
  isFiring,
  sentence,
} from "@/components/database/ops/alerts-model"
import { AlertsPicture } from "@/components/database/ops/alerts-picture"
import { AlertRow, EventRow, KindTile, StarterCard } from "@/components/database/ops/alerts-rows"
import type { DbAlertRule, DbAlerts, DbAlertTest } from "@/components/database/ops/alerts-types"
import { useDatabase } from "@/components/database/shell/database-context"

/**
 * Who is told when something goes wrong with this database, and what was.
 *
 * A rule watches this database — or every database — for its server not
 * answering, its disk filling, its connections running out, its replication
 * falling behind, or a backup of it failing or missing its time, and tells
 * the deployments' notification channels once when it crosses its line and
 * once when it is back. The page opens on that path drawn — the database,
 * this server that reads it every thirty seconds, the channels — over the
 * rules as sentences with where each stands, and what was told lately.
 *
 * Reading it is anybody's; a rule, its test and its removal are an
 * administrator's, which is when the verbs and the starter cards are drawn.
 */
export function Alerts() {
  const { id, conn, engine } = useDatabase()
  const { can } = useAuth()
  const admin = can("system.admin")
  const mayDestroy = admin && can("destructive")
  const { confirm, dialog } = useConfirm()
  const [editing, setEditing] = useState<{ rule?: DbAlertRule; kind?: DbAlertKind }>()
  // A counter keyed into the form, so a new rule never inherits a stale draft.
  const [session, setSession] = useState(0)

  const alerts = usePoll(
    (signal) => get<DbAlerts>("/databases/alerts", { connection: id }, signal),
    30_000,
    [id],
  )
  const channels = usePoll(
    (signal) => get<NotificationChannel[]>("/deploy/notifications", undefined, signal),
    300_000,
    [],
  )
  const data = alerts.data
  const own = engine.capabilities.alertKinds
  const channelList = channels.data ?? []
  const subjects = data?.subjects ?? []
  const subjectsOf = (rule: DbAlertRule) => subjects.filter((s) => s.ruleId === rule.id)
  // Firing first: the rule that is telling somebody is the one a reader came for.
  const rules = [...(data?.rules ?? [])].sort(
    (a, b) =>
      Number(b.enabled && isFiring(subjectsOf(b))) - Number(a.enabled && isFiring(subjectsOf(a))),
  )
  const firing = rules.filter((rule) => rule.enabled && isFiring(subjectsOf(rule))).length
  const arrived = useArrivals(rules.map((rule) => String(rule.id)))
  const ruleIds = new Set(rules.map((rule) => rule.id))
  // When every rule tells the same channels the head says so once, and a row
  // names its channels only where they differ from the rest.
  const told = (rule: DbAlertRule) =>
    toldBy(rule.channels, channelList)
      .map((channel) => channel.id)
      .sort((a, b) => a - b)
      .join()
  const shared = rules.every((rule) => told(rule) === told(rules[0]))
  const everyChannel = rules.some((rule) => rule.channels.length === 0)
  const reached = [...new Set(rules.flatMap((rule) => toldBy(rule.channels, channelList)))]

  const edit = (next: { rule?: DbAlertRule; kind?: DbAlertKind }) => {
    setSession((n) => n + 1)
    setEditing(next)
  }

  const test = async (rule: DbAlertRule) => {
    try {
      const out = await post<DbAlertTest>(`/databases/alerts/rules/${rule.id}/test`, {})
      if (out.channels === 0) {
        notify.error("No channel to send to", undefined, {
          description: "Add a notification channel first, or name one on the rule.",
        })
      } else if (out.failed > 0) {
        notify.error(`Delivered to ${out.delivered}, failed for ${out.failed}`, undefined, {
          description: "Open Notification channels to see each delivery.",
        })
      } else if (out.delivered === 0) {
        notify.error("Every channel it tells is paused", undefined, {
          description: "Resume one on Notification channels to receive the test.",
        })
      } else {
        notify.success(`Test sent to ${plural(out.delivered, "channel")}`)
      }
    } catch (error) {
      notify.error("Could not send the test", error)
    }
  }

  const subjectFor = (rule: DbAlertRule) => ({
    mark: <KindTile kind={rule.kind} />,
    name: sentence(rule),
    facts: <span>{rule.connectionId === null ? "Every database" : conn.name}</span>,
  })

  const write = (rule: DbAlertRule, enabled: boolean) =>
    put(
      `/databases/alerts/rules/${rule.id}`,
      draftBody(draftFor(rule, rule.kind, "this"), rule.connectionId ?? id, enabled),
    )

  const verbsFor = (rule: DbAlertRule): Verb[] => [
    { key: "test", label: "Send test", icon: PaperAirplane, run: () => void test(rule) },
    ...(mayDestroy
      ? [
          { key: "edit", label: "Edit", icon: Pencil, run: () => edit({ rule }) },
          rule.enabled
            ? {
                key: "pause",
                label: "Pause",
                icon: Pause,
                run: () =>
                  confirm({
                    title: "Pause alert",
                    confirmLabel: "Pause alert",
                    subject: subjectFor(rule),
                    description:
                      "Nobody is told while it is paused, and what it was following starts over when it is resumed.",
                    action: async () => {
                      await write(rule, false)
                    },
                    onDone: () => alerts.refresh(),
                  }),
              }
            : {
                key: "resume",
                label: "Resume",
                icon: Play,
                run: async () => {
                  try {
                    await write(rule, true)
                    alerts.refresh()
                  } catch (error) {
                    notify.error("Could not resume the alert", error)
                  }
                },
              },
          {
            key: "remove",
            label: "Remove",
            icon: Trash,
            danger: true,
            run: () =>
              confirm({
                title: "Remove alert",
                confirmLabel: "Remove alert",
                subject: subjectFor(rule),
                description:
                  rule.connectionId === null
                    ? "Nobody is told about any database it watched. What it told stays in each database's history."
                    : "Nobody is told when it crosses its line again. What it told stays in the history.",
                action: async () => {
                  await del(`/databases/alerts/rules/${rule.id}`)
                },
                onDone: () => alerts.refresh(),
              }),
          },
        ]
      : []),
  ]

  const closeForm = () => setEditing(undefined)

  return (
    <SectionFrame section="alerts">
      <AlertsPicture data={data} channels={channelList} />

      <Panel plain aria-label="Alert rules">
        <PanelHeader
          title={
            <span className="flex items-baseline gap-2">
              Alerts
              {data && (
                <span className="numeric text-hint font-normal text-muted-foreground">
                  {rules.length}
                </span>
              )}
            </span>
          }
          actions={
            <>
              {firing > 0 && (
                <span
                  key={firing}
                  className="numeric animate-rise text-hint font-medium text-destructive"
                >
                  {firing} firing
                </span>
              )}
              {rules.length > 0 && (
                <ChannelNames
                  ids={everyChannel ? [] : reached.map((channel) => channel.id)}
                  channels={channelList}
                  className="hidden text-hint text-muted-foreground md:inline-flex"
                />
              )}
              {admin && (
                <Button size="sm" variant="outline" onClick={() => edit({})}>
                  <Plus />
                  Add alert
                </Button>
              )}
            </>
          }
        />
        {!data ? (
          alerts.error ? (
            <div className="pt-4">
              <ErrorState error={alerts.error} onRetry={alerts.refresh} />
            </div>
          ) : (
            <LoadingPanel plain rows={3} />
          )
        ) : rules.length === 0 ? (
          admin ? (
            // The consequence, once; how an alert works is the sheet's to say,
            // and the cards are what this engine can be watched for.
            <div className="animate-rise space-y-3 pt-3">
              <EmptyNote className="max-w-prose px-0 py-0 text-left">
                Nobody is told when {conn.name} stops answering, fills its disk or misses a backup.
              </EmptyNote>
              <ChoiceGrid columns={3} className="lg:grid-cols-3">
                {own.map((kind) => (
                  <StarterCard key={kind} kind={kind} onClick={() => edit({ kind })} />
                ))}
              </ChoiceGrid>
            </div>
          ) : (
            <EmptyState
              mark={<EngineMark engine={engine} size="md" />}
              title={`No alert watches ${conn.name}`}
              description="Nobody is told when it stops answering, fills its disk or misses a backup. An administrator can add an alert here."
            />
          )
        ) : (
          <ul
            aria-label="Alert rules"
            className="min-w-0 animate-rise divide-y divide-hairline pt-3"
          >
            {rules.map((rule) => (
              <AlertRow
                key={rule.id}
                rule={rule}
                subjects={subjectsOf(rule)}
                away={
                  own.includes(rule.kind)
                    ? undefined
                    : `${engine.label} has no ${ALERT_KINDS[rule.kind].label.toLowerCase()} to watch; this rule watches the databases that have one.`
                }
                empty={
                  isBackupKind(rule.kind) ? (
                    <>
                      No backup job dumps {conn.name} yet.{" "}
                      <Link
                        href={`/backups?database=${id}`}
                        className="rounded-sm text-foreground underline underline-offset-2 focus-ring"
                      >
                        Schedule one
                      </Link>
                    </>
                  ) : (
                    `${conn.name} is read on the next pass, within ${data.everySeconds} seconds.`
                  )
                }
                channels={shared ? undefined : channelList}
                verbs={admin ? verbsFor(rule) : undefined}
                className={cn(arrived.has(String(rule.id)) && "animate-rise")}
              />
            ))}
          </ul>
        )}
      </Panel>

      {data && (
        <Panel plain aria-label="Recent alerts">
          <PanelHeader title="Recent alerts" />
          {data.events.length === 0 ? (
            <EmptyNote className="px-0 pt-3 pb-0 text-left">
              Nothing has been told about {conn.name} yet.
            </EmptyNote>
          ) : (
            <ul aria-label="Recent alerts" className="min-w-0 divide-y divide-hairline pt-3">
              {data.events.map((event) => (
                <EventRow key={event.id} event={event} ruleGone={!ruleIds.has(event.ruleId)} />
              ))}
            </ul>
          )}
          {channels.error && !channels.data && (
            <p className="pt-3 text-hint text-muted-foreground">
              The notification channels could not be read.{" "}
              <Link href="/deploy/notifications" className="underline underline-offset-2">
                Open Notification channels
              </Link>
            </p>
          )}
        </Panel>
      )}

      <SidePanel
        open={editing !== undefined}
        onOpenChange={(open) => !open && closeForm()}
        title={editing?.rule ? "Edit alert" : "Add alert"}
        description="A rule over this database's readings and the backups of it, told to your notification channels once when it crosses its line and once when it comes back."
        width="md"
      >
        {editing && (
          <AlertForm
            key={session}
            rule={editing.rule}
            kind={editing.kind}
            channels={channelList}
            onDone={() => {
              closeForm()
              alerts.refresh()
            }}
            onCancel={closeForm}
          />
        )}
      </SidePanel>
      {dialog}
    </SectionFrame>
  )
}
