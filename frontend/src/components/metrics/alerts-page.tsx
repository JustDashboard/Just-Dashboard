"use client"

import { useState } from "react"
import Link from "next/link"
import {
  Archive,
  Bell,
  Box,
  CloudUpload,
  Cpu,
  Database,
  Gauge,
  LockClosed,
  PaperAirplane,
  Pause,
  Pencil,
  Play,
  Plus,
  RefreshClockwise,
  ShieldOff,
  StopCircle,
  Trash,
  Users,
  type Icon,
} from "@/components/icons"
import { del, get, post, put } from "@/lib/api"
import { notify } from "@/lib/toast"
import { plural, relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { NotificationChannel } from "@/lib/types"
import {
  HOST_ALERT_DEFAULTS,
  HOST_ALERT_KINDS,
  HOST_ALERT_WORDS,
  cadenceWords,
  describeHostAlert,
  hostAlertCounts,
  hostAlertDraft,
  hostAlertFact,
  hostAlertFields,
  readsMetrics,
  type HostAlertKind,
  type HostAlertRule,
  type HostAlerts,
  type HostAlertSubject,
} from "@/lib/host-alerts"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { Page, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ChoiceList, ChoiceRow } from "@/components/choice-row"
import { Field, FieldRow, FormNote } from "@/components/form"
import { ProductLogo } from "@/components/product-logo"
import { Bone } from "@/components/bone"
import { FactDot, HostIdentity } from "@/components/identity-header"
import { ChannelNames } from "@/components/deploy/settings/traffic-alerts"
import { EmptyState, ErrorState } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
  InputGroupText,
} from "@/components/ui/input-group"
import { useConfirm } from "@/components/confirm-dialog"
import { VerbActions, VerbMenu, type Verb } from "@/components/verbs"
import { Skeleton } from "@/components/ui/skeleton"
import { TextShimmer } from "@/components/ui/text-shimmer"
import {
  AllClear,
  ChannelTests,
  HistoryRow,
  NothingTold,
  SubjectCard,
} from "@/components/alerts/alert-parts"
import {
  AlertsPicture,
  KindCards,
  KindTile,
  PAIRED_HEAD,
  RuleForm,
  RuleSheet,
  isFiring,
} from "@/components/alerts/kit"
import type { AlertArea, AlertKindSpec, AlertKinds } from "@/components/alerts/kinds"

/**
 * The host's alerts: rules over the machine's own readings — its CPU and
 * memory held high, a filesystem filling, a container gone down, a backup job
 * failed — told to the deployments' notification channels once when a
 * subject starts firing and once when it comes back.
 *
 * It is the proxy's Alerts page over different readings, on purpose: the same
 * picture of what the rules watch wired through this server to each channel,
 * what is firing first because that is what a reader comes for, then the
 * rules as rows that open their form in a sheet, and beside them what was
 * told lately and a test per channel. The parts that do not know which
 * readings they show are the alerts kit (`components/alerts/`).
 */

const KIND_ICON: Record<HostAlertKind, Icon> = {
  cpu_high: Cpu,
  memory_high: Gauge,
  disk_filling: Database,
  container_down: StopCircle,
  backup_failed: Archive,
  image_vulnerable: ShieldOff,
  tunnel_down: CloudUpload,
  account_changed: Users,
}

/** What a rule watches, as the picture's left column draws it. */
const AREAS: AlertArea<HostAlertKind>[] = [
  { key: "cpu", title: "CPU", fallback: Cpu, kinds: ["cpu_high"] },
  { key: "memory", title: "Memory", fallback: Gauge, kinds: ["memory_high"] },
  { key: "filesystems", title: "Filesystems", fallback: Database, kinds: ["disk_filling"] },
  {
    key: "containers",
    title: "Containers",
    product: "docker",
    fallback: Box,
    kinds: ["container_down"],
  },
  { key: "backups", title: "Backups", fallback: Archive, kinds: ["backup_failed"] },
  { key: "deployments", title: "Deployments", fallback: ShieldOff, kinds: ["image_vulnerable"] },
  {
    key: "tunnel",
    title: "Cloudflare Tunnel",
    product: "cloudflare",
    fallback: CloudUpload,
    kinds: ["tunnel_down"],
  },
  { key: "accounts", title: "Accounts", fallback: Users, kinds: ["account_changed"] },
]

/**
 * The host's kinds, as the alerts kit reads them: each one's words from
 * `lib/host-alerts` (the form's sentence as the hint, the line it is added
 * with as the add card's fact), its glyph, and the areas above.
 */
const HOST_KINDS: AlertKinds<HostAlertKind> = {
  kinds: Object.fromEntries(
    HOST_ALERT_KINDS.map((kind) => [
      kind,
      {
        label: HOST_ALERT_WORDS[kind].label,
        hint: HOST_ALERT_WORDS[kind].hint,
        fact: hostAlertFact(kind),
        icon: KIND_ICON[kind],
      },
    ]),
  ) as Record<HostAlertKind, AlertKindSpec>,
  order: HOST_ALERT_KINDS,
  areas: AREAS,
}

export function HostAlertsPage() {
  const { can } = useAuth()
  const admin = can("system.admin")
  // Every route of the page is an administrator's, so nobody else asks: the
  // answer would be the server's refusal.
  const alerts = usePoll(
    (signal) => get<HostAlerts>("/system/alerts", undefined, signal),
    30_000,
    [],
    { enabled: admin },
  )
  const channels = usePoll(
    (signal) => get<NotificationChannel[]>("/deploy/notifications", undefined, signal),
    60_000,
    [],
    { enabled: admin },
  )
  const { confirm, dialog } = useConfirm()
  const [editing, setEditing] = useState<{ rule?: HostAlertRule; kind?: HostAlertKind }>()
  // A counter keyed into the form, so a New form never inherits a stale draft.
  const [session, setSession] = useState(0)
  const [checking, setChecking] = useState(false)
  const [busy, setBusy] = useState<string>()

  const data = alerts.data
  const channelList = channels.data ?? []
  const rules = data?.rules ?? []
  const ruleOf = (id: number) => rules.find((rule) => rule.id === id)
  const subjects = data?.subjects ?? []
  // Firing first, then those holding before they are told.
  const active = subjects
    .filter((s) => s.state !== "quiet")
    .sort((a, b) => Number(b.state === "firing") - Number(a.state === "firing"))
  const muted = subjects.filter((s) => s.state === "quiet" && s.muted)
  const firingOf = (rule: HostAlertRule) =>
    subjects.filter((s) => s.ruleId === rule.id && isFiring(s)).length
  const cadence = cadenceWords(data?.intervalSeconds ?? 60)
  const metrics = data?.metrics ?? true

  const edit = (next: { rule?: HostAlertRule; kind?: HostAlertKind }) => {
    setSession((n) => n + 1)
    setEditing(next)
  }

  const check = async () => {
    setChecking(true)
    try {
      await post<HostAlerts>("/system/alerts/evaluate", {})
      alerts.refresh()
    } catch (error) {
      notify.error("Could not check the rules", error)
    } finally {
      setChecking(false)
    }
  }

  const setMuted = async (subject: HostAlertSubject, on: boolean) => {
    setBusy(`mute:${subject.ruleId}:${subject.subject}`)
    try {
      await put("/system/alerts/mutes", {
        ruleId: subject.ruleId,
        subject: subject.subject,
        muted: on,
      })
      alerts.refresh()
    } catch (error) {
      notify.error(on ? "Could not mute it" : "Could not unmute it", error)
    } finally {
      setBusy(undefined)
    }
  }

  const testChannel = async (channel: NotificationChannel) => {
    setBusy(`test:${channel.id}`)
    try {
      await post("/system/alerts/test", { channelId: channel.id })
      notify.success(`Test sent to ${channel.name}`)
    } catch (error) {
      notify.error(`Could not send a test to ${channel.name}`, error)
    } finally {
      setBusy(undefined)
    }
  }

  const testRule = async (rule: HostAlertRule) => {
    try {
      const told = await post<{ delivered: number; failed: number }>(
        `/system/alerts/rules/${rule.id}/test`,
        {},
      )
      if (told.failed > 0)
        notify.error(`${told.failed} of the rule's channels did not take the test`)
      else notify.success(`Test sent to ${plural(told.delivered, "channel")}`)
      alerts.refresh()
    } catch (error) {
      notify.error("Could not send the test", error)
    }
  }

  const save = async (rule: HostAlertRule, enabled: boolean) => {
    try {
      await put(`/system/alerts/rules/${rule.id}`, {
        kind: rule.kind,
        params: rule.params,
        channels: rule.channels,
        enabled,
      })
      alerts.refresh()
    } catch (error) {
      notify.error("Could not change the rule", error)
    }
  }

  const subjectFor = (rule: HostAlertRule) => ({
    mark: <KindTile kinds={HOST_KINDS} kind={rule.kind} />,
    name: HOST_ALERT_WORDS[rule.kind].label,
    facts: <span>{describeHostAlert(rule.kind, rule.params)}</span>,
  })

  const verbsFor = (rule: HostAlertRule): Verb[] => [
    { key: "edit", label: "Edit", icon: Pencil, run: () => edit({ rule }) },
    {
      key: "test",
      label: "Send test",
      icon: PaperAirplane,
      disabled: !rule.enabled,
      run: () => void testRule(rule),
    },
    rule.enabled
      ? {
          key: "pause",
          label: "Pause",
          icon: Pause,
          run: () =>
            confirm({
              title: `Pause ${HOST_ALERT_WORDS[rule.kind].label} alert`,
              confirmLabel: "Pause alert",
              subject: subjectFor(rule),
              description:
                "Nobody is told while it is paused, and what it was following starts over when it is resumed.",
              action: async () => {
                await put(`/system/alerts/rules/${rule.id}`, {
                  kind: rule.kind,
                  params: rule.params,
                  channels: rule.channels,
                  enabled: false,
                })
              },
              onDone: () => alerts.refresh(),
            }),
        }
      : { key: "resume", label: "Resume", icon: Play, run: () => void save(rule, true) },
    {
      key: "remove",
      label: "Remove",
      icon: Trash,
      danger: true,
      run: () =>
        confirm({
          title: `Remove ${HOST_ALERT_WORDS[rule.kind].label} alert`,
          confirmLabel: "Remove alert",
          subject: subjectFor(rule),
          description:
            "Nobody is told about it again. Its mutes go with it; what it told stays in the history.",
          action: async () => {
            await del(`/system/alerts/rules/${rule.id}`)
          },
          onDone: () => alerts.refresh(),
        }),
    },
  ]

  const closeForm = () => setEditing(undefined)
  const { firing, holding } = hostAlertCounts(active)
  const paused = rules.filter((rule) => !rule.enabled).length
  const on = rules.length - paused
  const waiting = alerts.loading && !data
  // A rule with no channels named tells every enabled one.
  const told = channelList.filter(
    (channel) =>
      channel.enabled &&
      rules.some(
        (rule) =>
          rule.enabled && (rule.channels.length === 0 || rule.channels.includes(channel.id)),
      ),
  ).length
  const history = data?.history ?? []

  // No way back above the line: the rail names Alerts under Monitoring, as
  // it names the proxy's Alerts, and a sub-page the rail names carries none.
  const context = <PageContext title="Alerts" />

  if (!admin)
    return (
      <Page>
        {context}
        <EmptyState
          icon={LockClosed}
          title="Alerts are an administrator's"
          description="The rules decide who is told about this server, so only an administrator reads or changes them."
          action={
            <Button size="sm" variant="outline" asChild>
              <Link href="/metrics">Back to Metrics</Link>
            </Button>
          }
        />
      </Page>
    )

  return (
    <Page>
      {context}

      {/* What the page is about, as a project's header: what the rules are
          doing now as the state, how many are on and who they reach as the
          facts, the one command at the end and the rest behind the menu.
          Add rule and Check now stood on two section lines. */}
      <HostIdentity
        fallback={Bell}
        title="Host alerts"
        state={
          waiting ? (
            <Bone className="w-16 text-xs" />
          ) : !data ? (
            <Status tone="warning" label="Unread" />
          ) : firing > 0 ? (
            <>
              <Status tone="danger" label={`${firing} firing`} />
              {holding > 0 && <Status tone="warning" label={`${holding} holding`} />}
            </>
          ) : holding > 0 ? (
            <Status tone="warning" label={`${holding} holding`} />
          ) : on === 0 ? (
            <Status tone="stopped" label={rules.length === 0 ? "No rules" : "Paused"} />
          ) : (
            <Status tone="running" label="Watching" />
          )
        }
        command={
          <Button size="sm" onClick={() => edit({})}>
            <Plus className="size-3.5" />
            Add rule
          </Button>
        }
        menu={
          <VerbMenu
            label="Alert actions"
            verbs={[
              {
                key: "check",
                label: "Check now",
                icon: RefreshClockwise,
                disabled: checking || !data,
                run: () => void check(),
              },
            ]}
          />
        }
        facts={
          waiting ? (
            <Bone className="w-72" />
          ) : !data ? (
            <span>The rules could not be read</span>
          ) : (
            <>
              <span className="numeric">
                {plural(on, "rule")} on{paused > 0 && `, ${paused} paused`}
              </span>
              <FactDot />
              <span className="numeric">
                {channelList.length === 0
                  ? "no channel to tell yet"
                  : `telling ${plural(told, "channel")} of ${channelList.length}`}
              </span>
              <FactDot />
              {checking ? (
                <TextShimmer>checking now</TextShimmer>
              ) : (
                <span className="numeric">
                  {data.lastPass ? `checked ${relativeTime(data.lastPass)}, ` : "checked "}
                  {cadence}
                </span>
              )}
            </>
          )
        }
      />

      {alerts.error && !data ? (
        <ErrorState error={alerts.error} onRetry={alerts.refresh} />
      ) : (
        <>
          {/* On the page's own ground, as Notifications' picture is. Drawn
              while the rules are read too, with what it watches in place, so
              nothing under it moves when they land. */}
          <div className="relative animate-rise py-2 lg:py-6">
            <AlertsPicture
              kinds={HOST_KINDS}
              rules={rules}
              subjects={subjects}
              channels={channelList}
              title={on === 0 ? "No rules on" : plural(on, "rule")}
              reach={
                channelList.length === 0
                  ? "telling nobody yet"
                  : `telling ${plural(told, "channel")}`
              }
              waiting={waiting}
              onAdd={(kind) => edit({ kind })}
              // Five of its eight sources are named nowhere else on the page
              // until a rule watches them, so a phone keeps their words.
              phone="stack"
            />
          </div>

          <div className="grid min-w-0 items-start gap-8 xl:grid-cols-[minmax(0,3fr)_minmax(0,2fr)] 2xl:gap-x-10 [&>*]:min-w-0">
            <div className="min-w-0 space-y-8">
              <Panel plain aria-label="Firing">
                <PanelHeader
                  title="Firing"
                  className={PAIRED_HEAD}
                  actions={
                    (firing > 0 || holding > 0) && (
                      <span className="numeric text-hint text-muted-foreground">
                        <span className={cn("font-medium", firing > 0 && "text-destructive")}>
                          {firing}
                        </span>{" "}
                        firing
                        {holding > 0 && (
                          <>
                            {" · "}
                            <span className="font-medium text-warning">{holding}</span> holding
                          </>
                        )}
                      </span>
                    )
                  }
                />
                <PanelBody>
                  {waiting ? (
                    // The height of the card that says nothing is firing.
                    <Skeleton className="h-15.5 rounded-xl" />
                  ) : active.length === 0 ? (
                    <AllClear rules={rules.length} cadence={cadence} />
                  ) : (
                    <ul aria-label="Firing" className="animate-rise space-y-2">
                      {active.map((subject) => (
                        <SubjectCard
                          key={`${subject.ruleId}:${subject.subject}`}
                          subject={subject}
                          kindLabel={HOST_ALERT_WORDS[subject.kind]?.label ?? subject.kind}
                          tile={(tone) => (
                            <KindTile kinds={HOST_KINDS} kind={subject.kind} tone={tone} />
                          )}
                          admin={admin}
                          busy={busy === `mute:${subject.ruleId}:${subject.subject}`}
                          onMute={(on) => void setMuted(subject, on)}
                        />
                      ))}
                    </ul>
                  )}
                </PanelBody>
              </Panel>

              <Panel plain aria-label="Rules">
                <PanelHeader
                  title="Rules"
                  className={PAIRED_HEAD}
                  count={
                    rules.length > 0 &&
                    `${plural(rules.length, "rule")}${paused > 0 ? ` · ${paused} paused` : ""}`
                  }
                />
                <PanelBody>
                  {waiting ? (
                    <div aria-hidden className="space-y-2">
                      {[0, 1, 2].map((index) => (
                        <Skeleton key={index} className="h-15.5 rounded-xl" />
                      ))}
                    </div>
                  ) : rules.length === 0 ? (
                    <KindCards kinds={HOST_KINDS} onAdd={(kind) => edit({ kind })} />
                  ) : (
                    <ChoiceList aria-label="Alert rules" className="animate-rise">
                      {rules.map((rule) => {
                        const fires = firingOf(rule)
                        const words = HOST_ALERT_WORDS[rule.kind]
                        const unjudged = rule.enabled ? rule.unjudged : undefined
                        return (
                          <ChoiceRow
                            key={rule.id}
                            leading={
                              <KindTile
                                kinds={HOST_KINDS}
                                kind={rule.kind}
                                tone={fires > 0 ? "danger" : undefined}
                                className={cn(!rule.enabled && "opacity-50 grayscale")}
                              />
                            }
                            title={words.label}
                            verb={`Edit ${words.label}`}
                            onSelect={() => edit({ rule })}
                            description={
                              // Why it cannot be judged takes the line: under
                              // it, it made this row a line taller than the
                              // rest. What the rule watches is in its form.
                              unjudged ? (
                                <span
                                  className="text-warning"
                                  title={`${describeHostAlert(rule.kind, rule.params)}. ${unjudged}`}
                                >
                                  {unjudged}
                                </span>
                              ) : (
                                <>
                                  {describeHostAlert(rule.kind, rule.params)}
                                  {" · "}
                                  <ChannelNames
                                    ids={rule.channels}
                                    channels={channelList}
                                    className="flex-nowrap align-bottom"
                                  />
                                </>
                              )
                            }
                            trailing={
                              !rule.enabled ? (
                                <Status tone="stopped" label="Paused" />
                              ) : fires > 0 ? (
                                <Status tone="danger" label={`${fires} firing`} />
                              ) : rule.unjudged ? (
                                <Status tone="warning" label="Cannot judge" />
                              ) : (
                                <Status tone="running" label="Watching" />
                              )
                            }
                            actions={
                              <VerbActions
                                dim
                                verbs={verbsFor(rule)}
                                menuLabel={`Actions for ${words.label}, ${describeHostAlert(rule.kind, rule.params)}`}
                              />
                            }
                          />
                        )
                      })}
                    </ChoiceList>
                  )}
                </PanelBody>
              </Panel>

              {muted.length > 0 && (
                <Panel plain aria-label="Muted">
                  <PanelHeader
                    title="Muted"
                    className={PAIRED_HEAD}
                    count={`${muted.length} nobody is told about`}
                  />
                  <PanelBody>
                    <ul aria-label="Muted" className="space-y-2">
                      {muted.map((subject) => (
                        <SubjectCard
                          key={`${subject.ruleId}:${subject.subject}`}
                          subject={subject}
                          kindLabel={HOST_ALERT_WORDS[subject.kind]?.label ?? subject.kind}
                          tile={(tone) => (
                            <KindTile kinds={HOST_KINDS} kind={subject.kind} tone={tone} />
                          )}
                          admin={admin}
                          busy={busy === `mute:${subject.ruleId}:${subject.subject}`}
                          onMute={(on) => void setMuted(subject, on)}
                        />
                      ))}
                    </ul>
                  </PanelBody>
                </Panel>
              )}
            </div>

            <div className="min-w-0 space-y-8">
              <Panel plain aria-label="Recently told">
                <PanelHeader
                  title="Recently told"
                  className={PAIRED_HEAD}
                  count={history.length > 0 && `newest ${Math.min(history.length, 100)}`}
                />
                <PanelBody>
                  {waiting ? (
                    <Skeleton className="h-28 rounded-xl" />
                  ) : history.length === 0 ? (
                    <NothingTold />
                  ) : (
                    <ul aria-label="Alert history" className="animate-rise">
                      {history.slice(0, 12).map((event) => (
                        <HistoryRow
                          key={event.id}
                          event={event}
                          firing={event.event === "host.alert.firing"}
                          kindLabel={HOST_ALERT_WORDS[event.kind]?.label ?? event.kind}
                          tile={
                            HOST_ALERT_WORDS[event.kind] ? (
                              <KindTile kinds={HOST_KINDS} kind={event.kind} size="sm" />
                            ) : (
                              <ProductLogo size="sm" fallback={Bell} />
                            )
                          }
                          ruleGone={!ruleOf(event.ruleId)}
                        />
                      ))}
                    </ul>
                  )}
                </PanelBody>
              </Panel>

              <Panel plain aria-label="Channels">
                <PanelHeader
                  title="Channels"
                  className={PAIRED_HEAD}
                  link={{ href: "/deploy/notifications", label: "Notifications" }}
                />
                <PanelBody>
                  {channels.loading && !channels.data ? (
                    <Skeleton className="h-28 rounded-xl" />
                  ) : (
                    <ChannelTests
                      channels={channelList}
                      error={channels.error && !channels.data ? channels.error : undefined}
                      onRetry={channels.refresh}
                      admin={admin}
                      busy={busy}
                      onTest={(channel) => void testChannel(channel)}
                    />
                  )}
                </PanelBody>
              </Panel>
            </div>
          </div>
        </>
      )}

      <RuleSheet open={Boolean(editing)} editing={Boolean(editing?.rule)} onClose={closeForm}>
        {editing && (
          <HostRuleForm
            key={session}
            rule={editing.rule}
            kind={editing.kind}
            channels={channelList}
            metrics={metrics}
            onDone={() => {
              closeForm()
              alerts.refresh()
            }}
            onCancel={closeForm}
          />
        )}
      </RuleSheet>
      {dialog}
    </Page>
  )
}

/** One host rule as a form: the kit's shape, with the kind's line and minutes. */
function HostRuleForm({
  rule,
  kind: preset,
  channels,
  metrics,
  onDone,
  onCancel,
}: {
  rule?: HostAlertRule
  kind?: HostAlertKind
  channels: NotificationChannel[]
  metrics: boolean
  onDone: () => void
  onCancel: () => void
}) {
  const start = rule?.kind ?? preset ?? "cpu_high"
  const [kind, setKind] = useState<HostAlertKind>(start)
  const initial = { ...HOST_ALERT_DEFAULTS[start], ...rule?.params }
  const [threshold, setThreshold] = useState(String(initial.threshold ?? ""))
  const [minutes, setMinutes] = useState(String(initial.minutes ?? ""))
  const [selected, setSelected] = useState<number[]>(rule?.channels ?? [])
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string>()
  const fields = hostAlertFields(kind)
  const draft = hostAlertDraft(kind, { threshold, minutes })

  const choose = (next: HostAlertKind) => {
    setKind(next)
    setError(undefined)
    const defaults = HOST_ALERT_DEFAULTS[next]
    if (defaults.threshold) setThreshold(String(defaults.threshold))
    if (defaults.minutes) setMinutes(String(defaults.minutes))
  }

  const save = async () => {
    if (draft.problem) {
      setError(draft.problem)
      return
    }
    setSaving(true)
    setError(undefined)
    const body = { kind, params: draft.params, channels: selected, enabled: rule?.enabled ?? true }
    try {
      if (rule) await put(`/system/alerts/rules/${rule.id}`, body)
      else await post("/system/alerts/rules", body)
      notify.success(rule ? "Alert updated" : "Alert added", {
        description: `${HOST_ALERT_WORDS[kind].label}: ${describeHostAlert(kind, body.params)}.`,
      })
      onDone()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setSaving(false)
    }
  }

  const minutesField = fields.minutes && (
    <Field
      label={kind === "container_down" || kind === "tunnel_down" ? "Down for" : "Averaged over"}
      htmlFor="host-alert-minutes"
      hint={
        kind === "container_down" || kind === "tunnel_down"
          ? "From a minute to a day before it is told."
          : "The recorded samples it averages, from a minute to a day."
      }
    >
      <InputGroup>
        <InputGroupInput
          id="host-alert-minutes"
          inputMode="numeric"
          value={minutes}
          onChange={(event) => setMinutes(event.target.value)}
          className="numeric"
        />
        <InputGroupAddon align="inline-end">
          <InputGroupText>min</InputGroupText>
        </InputGroupAddon>
      </InputGroup>
    </Field>
  )

  return (
    <RuleForm
      editing={Boolean(rule)}
      onCancel={onCancel}
      kinds={HOST_KINDS}
      kind={kind}
      onKind={choose}
      channels={channels}
      selected={selected}
      onSelected={setSelected}
      sentence={`${HOST_ALERT_WORDS[kind].label}: ${describeHostAlert(kind, draft.params)}.`}
      saving={saving}
      onSave={() => void save()}
    >
      {fields.threshold ? (
        <FieldRow>
          <Field
            label="At or above"
            htmlFor="host-alert-threshold"
            hint={
              kind === "disk_filling"
                ? "Of a filesystem's space or inodes; it recovers five points under."
                : "It recovers once it averages five points under."
            }
          >
            <InputGroup>
              <InputGroupInput
                id="host-alert-threshold"
                inputMode="decimal"
                value={threshold}
                onChange={(event) => setThreshold(event.target.value)}
                className="numeric"
              />
              <InputGroupAddon align="inline-end">
                <InputGroupText>%</InputGroupText>
              </InputGroupAddon>
            </InputGroup>
          </Field>
          {minutesField}
        </FieldRow>
      ) : (
        minutesField
      )}

      {kind === "backup_failed" && (
        <FormNote>
          A job a database alert already follows for failed backups is left to that rule, so one
          failure is told once.
        </FormNote>
      )}
      {kind === "image_vulnerable" && (
        <FormNote>
          Each environment&apos;s scan terms decide what counts: scanning off means nothing is told
          for it, and its severity floor is the line. Set them on the project&apos;s overview.
        </FormNote>
      )}
      {!metrics && readsMetrics(kind) && (
        <FormNote tone="warning">
          Metrics history is off on this host (JD_METRICS_RETENTION=0), so this rule has nothing to
          read until it is turned back on.
        </FormNote>
      )}
      {error && <FormNote tone="danger">{error}</FormNote>}
    </RuleForm>
  )
}
