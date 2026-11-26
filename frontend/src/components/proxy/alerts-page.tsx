"use client"

import { useState } from "react"
import {
  Bell,
  Clock,
  CrossCircle,
  Globe,
  LockClosed,
  NetworkDevice,
  Pause,
  Pencil,
  Play,
  Plus,
  RefreshClockwise,
  Servers,
  ShieldOff,
  Slash,
  Trash,
} from "@/components/icons"
import { del, get, post, put } from "@/lib/api"
import { notify } from "@/lib/toast"
import { plural, relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type {
  NotificationChannel,
  ProxyAlertKind,
  ProxyAlertParams,
  ProxyAlertRule,
  ProxyAlerts,
  ProxyAlertSubject,
} from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { Page, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ChoiceList, ChoiceRow } from "@/components/choice-row"
import { Field, FieldRow, FormNote } from "@/components/form"
import { ProductLogo } from "@/components/product-logo"
import { ChannelNames } from "@/components/deploy/settings/traffic-alerts"
import { ErrorState, Notice } from "@/components/state"
import { LoadingPanel } from "@/components/loading"
import { IdentityHeader } from "@/components/identity-header"
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
import {
  NOT_HERE,
  judging,
  reaches,
  type AlertArea,
  type AlertKindSpec,
  type AlertKinds,
} from "@/components/alerts/kinds"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
  InputGroupText,
} from "@/components/ui/input-group"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { useConfirm } from "@/components/confirm-dialog"
import { VerbActions, type Verb } from "@/components/verbs"
import { useProxy } from "@/components/proxy/proxy-context"
import { engineProduct } from "@/components/proxy/marks"

/**
 * The proxy's alerts: rules over the readings the overview shows, told to the
 * deployments' notification channels once when a subject starts firing and
 * once when it comes back.
 *
 * It was a sheet over the overview, grey rows under five headings, and the
 * operator asked for a page of its own drawn the way Notifications is. It
 * opens on a line saying what it is — how many rules are on, what is firing
 * and who is told — with Add rule as its command, then on the same kind of
 * picture: what the rules watch — certificates as
 * Let's Encrypt, the engine as itself, upstreams, sites and watched endpoints
 * — wired into this server and out to each channel as its own service, each
 * wire in the colour of what it carries. Under it what is firing now, because
 * that is what a reader comes for, then the rules as cards that open their
 * form; beside them what was told lately and a test per channel. A rule is
 * edited in a sheet, so the page behind it stays as it was.
 */

/** A kind as the kit draws it, and how its parameters read as a sentence. */
type ProxyKind = AlertKindSpec & { describe: (params: ProxyAlertParams) => string }

const EXPIRY_DAYS = [21, 7, 3, 1]

const KINDS: Record<ProxyAlertKind, ProxyKind> = {
  cert_expiring: {
    label: "Certificate expiring",
    hint: "21, 7, 3 and 1 days left",
    detail: "Told once at each threshold a certificate crosses.",
    icon: Clock,
    describe: (p) => `at ${(p.days ?? EXPIRY_DAYS).join(", ")} days left`,
  },
  cert_expired: {
    label: "Certificate expired",
    hint: "one has run out",
    detail: "A certificate on this host has run out.",
    icon: CrossCircle,
    describe: () => "once a certificate runs out",
  },
  renewal_failed: {
    label: "Renewal failed",
    hint: "certbot or its hook failed",
    detail: "Certbot's last renewal or reload hook failed.",
    icon: RefreshClockwise,
    describe: () => "after a failed certbot renewal or reload hook",
  },
  served_drift: {
    label: "Served certificate differs",
    hint: "not the one in its file",
    detail: "An nginx site serves a different certificate from the one in its file.",
    icon: ShieldOff,
    describe: () => "when a site serves a different certificate",
  },
  engine_down: {
    label: "Engine not running",
    hint: "stopped or failed",
    detail: "The engine's unit stopped or failed, on two checks in a row.",
    icon: Servers,
    describe: () => "stopped or failed on two checks in a row",
  },
  upstream_down: {
    label: "Upstream down",
    hint: "stops answering",
    detail: "An address nginx forwards to stops answering.",
    icon: NetworkDevice,
    describe: (p) => `down for ${p.minutes ?? 5} min`,
  },
  watch_unreachable: {
    label: "Watched endpoint unreachable",
    hint: "no TLS handshake",
    detail: "No TLS handshake on two checks in a row.",
    icon: Globe,
    describe: () => "no TLS handshake on two checks in a row",
  },
  watch_untrusted: {
    label: "Watched endpoint untrusted",
    hint: "certificate not trusted",
    detail: "It serves a certificate the system does not trust.",
    icon: ShieldOff,
    describe: () => "serves a certificate that is not trusted",
  },
  watch_grade_below: {
    label: "Watched TLS grade below minimum",
    hint: "a scan under your grade",
    detail: "A full TLS scan is below your chosen grade on two checks in a row.",
    icon: Globe,
    describe: (p) => `below grade ${p.grade ?? "A"} on two checks in a row`,
  },
  site_errors: {
    label: "Site failing",
    hint: "too many 5xx answers",
    detail: "A site's share of 5xx answers over a window.",
    icon: Slash,
    describe: (p) =>
      `more than ${p.threshold ?? 5}% answered 5xx over ${p.minutes ?? 15} min, from ${p.minRequests ?? 20} requests`,
  },
}

const KIND_ORDER = Object.keys(KINDS) as ProxyAlertKind[]

const DEFAULT_PARAMS: Record<ProxyAlertKind, ProxyAlertParams> = {
  cert_expiring: { days: EXPIRY_DAYS },
  cert_expired: {},
  renewal_failed: {},
  served_drift: {},
  engine_down: {},
  upstream_down: { minutes: 5 },
  watch_unreachable: {},
  watch_untrusted: {},
  watch_grade_below: { grade: "A" },
  site_errors: { threshold: 5, minutes: 15, minRequests: 20 },
}

/**
 * What a rule watches, as the picture's left column draws it: the things a
 * reader thinks in — a certificate, the engine, a site — rather than the ten
 * kinds of rule there are.
 */
const AREAS: AlertArea<ProxyAlertKind>[] = [
  {
    key: "certificates",
    title: "Certificates",
    product: () => "lets-encrypt",
    fallback: LockClosed,
    kinds: ["cert_expiring", "cert_expired", "renewal_failed", "served_drift"],
  },
  {
    key: "engine",
    title: "The engine",
    product: (engine) => engine,
    fallback: Servers,
    kinds: ["engine_down"],
  },
  { key: "upstreams", title: "Upstreams", fallback: NetworkDevice, kinds: ["upstream_down"] },
  { key: "sites", title: "Sites", fallback: Globe, kinds: ["site_errors"] },
  {
    key: "watched",
    title: "Watched endpoints",
    fallback: ShieldOff,
    kinds: ["watch_unreachable", "watch_untrusted", "watch_grade_below"],
  },
]

/** The proxy's kinds, as the alerts kit reads them. */
const PROXY_KINDS: AlertKinds<ProxyAlertKind> = { kinds: KINDS, order: KIND_ORDER, areas: AREAS }

export function AlertsPage() {
  const { status } = useProxy()
  const engine = status ? engineProduct(status) : undefined
  const { can } = useAuth()
  const admin = can("system.admin")
  // Every route under /proxy/alerts is an administrator's. An account that
  // only reads is not sent to ask and be refused: it got the server's 403 as
  // a failure to retry.
  const alerts = usePoll(
    (signal) => get<ProxyAlerts>("/proxy/alerts", undefined, signal),
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
  const [editing, setEditing] = useState<{ rule?: ProxyAlertRule; kind?: ProxyAlertKind }>()
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
  const firingOf = (rule: ProxyAlertRule) =>
    subjects.filter((s) => s.ruleId === rule.id && isFiring(s)).length

  const edit = (next: { rule?: ProxyAlertRule; kind?: ProxyAlertKind }) => {
    setSession((n) => n + 1)
    setEditing(next)
  }

  const check = async () => {
    setChecking(true)
    try {
      await post<ProxyAlerts>("/proxy/alerts/evaluate", {})
      alerts.refresh()
    } catch (error) {
      notify.error("Could not check the rules", error)
    } finally {
      setChecking(false)
    }
  }

  const setMuted = async (subject: ProxyAlertSubject, on: boolean) => {
    setBusy(`mute:${subject.ruleId}:${subject.subject}`)
    try {
      await put("/proxy/alerts/mutes", {
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

  const test = async (channel: NotificationChannel) => {
    setBusy(`test:${channel.id}`)
    try {
      await post("/proxy/alerts/test", { channelId: channel.id })
      notify.success(`Test sent to ${channel.name}`)
    } catch (error) {
      notify.error(`Could not send a test to ${channel.name}`, error)
    } finally {
      setBusy(undefined)
    }
  }

  const save = async (rule: ProxyAlertRule, enabled: boolean) => {
    try {
      await put(`/proxy/alerts/rules/${rule.id}`, {
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

  const subjectFor = (rule: ProxyAlertRule) => ({
    mark: <KindTile kinds={PROXY_KINDS} kind={rule.kind} engine={engine} />,
    name: KINDS[rule.kind].label,
    facts: <span>{KINDS[rule.kind].describe(rule.params)}</span>,
  })

  const verbsFor = (rule: ProxyAlertRule): Verb[] => [
    { key: "edit", label: "Edit", icon: Pencil, run: () => edit({ rule }) },
    rule.enabled
      ? {
          key: "pause",
          label: "Pause",
          icon: Pause,
          run: () =>
            confirm({
              title: `Pause ${KINDS[rule.kind].label} alert`,
              confirmLabel: "Pause alert",
              subject: subjectFor(rule),
              description:
                "Nobody is told while it is paused, and what it was following starts over when it is resumed.",
              action: async () => {
                await put(`/proxy/alerts/rules/${rule.id}`, {
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
          title: `Remove ${KINDS[rule.kind].label} alert`,
          confirmLabel: "Remove alert",
          subject: subjectFor(rule),
          description:
            "Nobody is told about it again. Its mutes go with it; what it told stays in the history.",
          action: async () => {
            await del(`/proxy/alerts/rules/${rule.id}`)
          },
          onDone: () => alerts.refresh(),
        }),
    },
  ]

  const closeForm = () => setEditing(undefined)
  const firing = active.filter(isFiring).length
  const holding = active.filter((s) => s.state === "pending").length
  const paused = rules.filter((rule) => !rule.enabled).length
  const every = Math.round((data?.intervalSeconds ?? 300) / 60)

  // A rule whose kind reads nothing here is on and can never fire: it is not
  // counted as watching, and the picture, the cards and the form say why.
  const absent = new Set<string>(data?.absent ?? [])
  const on = judging(rules, absent).length
  const idle = rules.length - paused - on
  const told = channelList.filter((channel) => reaches(rules, channel)).length

  return (
    <Page>
      <PageContext title="Alerts" />
      <IdentityHeader
        fallback={Bell}
        title="Alerts"
        state={
          !admin ? (
            <Status tone="unknown" label="Administrators only" />
          ) : !data ? (
            alerts.error ? (
              <Status tone="danger" label="Not read" />
            ) : (
              <Status tone="unknown" label="Reading" />
            )
          ) : (
            <>
              <Status
                tone={on === 0 ? "stopped" : "running"}
                label={on === 0 ? "No rules on" : `${plural(on, "rule")} on`}
              />
              {firing > 0 && <Status tone="danger" label={`${firing} firing`} />}
              {holding > 0 && <Status tone="warning" label={`${holding} holding`} />}
            </>
          )
        }
        facts={
          !admin ? (
            <span>rules over the proxy&apos;s readings, told to the notification channels</span>
          ) : !data ? (
            alerts.error ? (
              <span>the rules could not be read</span>
            ) : (
              <TextShimmer>Reading the rules…</TextShimmer>
            )
          ) : (
            <>
              {idle > 0 && (
                <span className="numeric">
                  {idle} {NOT_HERE}
                </span>
              )}
              <span className="numeric">
                {told === 0 ? "telling nobody yet" : `telling ${plural(told, "channel")}`}
              </span>
            </>
          )
        }
        command={
          admin && (
            <Button size="sm" onClick={() => edit({})} disabled={!data}>
              <Plus className="size-3.5" />
              Add rule
            </Button>
          )
        }
      />

      {!admin ? (
        <Notice title="Only an administrator reads and sets the alert rules">
          They name who is told, and what a rule says is what the proxy serves.
        </Notice>
      ) : alerts.error && !data ? (
        <ErrorState error={alerts.error} onRetry={alerts.refresh} />
      ) : alerts.loading && !data ? (
        <LoadingPanel plain rows={4} />
      ) : (
        <>
          {/* On the page's own ground, as Notifications' picture is. */}
          <div className="relative animate-rise py-2 lg:py-6">
            <AlertsPicture
              kinds={PROXY_KINDS}
              rules={rules}
              subjects={subjects}
              channels={channelList}
              engine={engine}
              title={`checks every ${every} min`}
              reach="tells each change once"
              absent={absent}
              onAdd={admin ? (kind) => edit({ kind }) : undefined}
            />
          </div>

          <div className="grid min-w-0 items-start gap-8 xl:grid-cols-[minmax(0,3fr)_minmax(0,2fr)] 2xl:gap-x-10 [&>*]:min-w-0">
            <div className="min-w-0 space-y-8">
              <Panel plain aria-label="Firing">
                <PanelHeader
                  title="Firing"
                  className={PAIRED_HEAD}
                  actions={
                    <span className="flex flex-wrap items-center gap-3">
                      {data?.lastPass && (
                        <span className="numeric text-hint text-muted-foreground">
                          checked {relativeTime(data.lastPass)}
                        </span>
                      )}
                      {admin && (
                        <Button size="xs" variant="outline" onClick={check} pending={checking}>
                          <RefreshClockwise className="size-3.5" />
                          Check now
                        </Button>
                      )}
                    </span>
                  }
                />
                <PanelBody>
                  {active.length === 0 ? (
                    <AllClear rules={on} idle={idle} cadence={`every ${every} minutes`} />
                  ) : (
                    <ul aria-label="Firing" className="animate-rise space-y-2">
                      {active.map((subject) => (
                        <SubjectCard
                          key={`${subject.ruleId}:${subject.subject}`}
                          subject={subject}
                          kindLabel={KINDS[subject.kind].label}
                          tile={(tone) => (
                            <KindTile
                              kinds={PROXY_KINDS}
                              kind={subject.kind}
                              engine={engine}
                              tone={tone}
                            />
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
                    rules.length > 0
                      ? `${plural(rules.length, "rule")}${paused > 0 ? ` · ${paused} paused` : ""}`
                      : `${KIND_ORDER.length - absent.size} to choose from`
                  }
                />
                <PanelBody>
                  {rules.length === 0 ? (
                    <KindCards
                      kinds={PROXY_KINDS}
                      engine={engine}
                      absent={absent}
                      onAdd={admin ? (kind) => edit({ kind }) : undefined}
                    />
                  ) : (
                    <ChoiceList aria-label="Alert rules" className="animate-rise">
                      {rules.map((rule) => {
                        const fires = firingOf(rule)
                        return (
                          <ChoiceRow
                            key={rule.id}
                            leading={
                              <KindTile
                                kinds={PROXY_KINDS}
                                kind={rule.kind}
                                engine={engine}
                                tone={fires > 0 ? "danger" : undefined}
                                className={cn(!rule.enabled && "opacity-50 grayscale")}
                              />
                            }
                            title={KINDS[rule.kind].label}
                            verb={`Edit ${KINDS[rule.kind].label}`}
                            onSelect={admin ? () => edit({ rule }) : undefined}
                            disabled={!admin}
                            description={
                              <>
                                {KINDS[rule.kind].describe(rule.params)}
                                {" · "}
                                <ChannelNames ids={rule.channels} channels={channelList} />
                              </>
                            }
                            trailing={
                              !rule.enabled ? (
                                <Status tone="stopped" label="Paused" />
                              ) : fires > 0 ? (
                                <Status tone="danger" label={`${fires} firing`} />
                              ) : absent.has(rule.kind) ? (
                                <Status tone="unknown" label="Not on this server" />
                              ) : (
                                <Status tone="running" label="Watching" />
                              )
                            }
                            actions={
                              admin && (
                                <VerbActions
                                  dim
                                  verbs={verbsFor(rule)}
                                  menuLabel={`Actions for ${KINDS[rule.kind].label}, ${KINDS[rule.kind].describe(rule.params)}`}
                                />
                              )
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
                          kindLabel={KINDS[subject.kind].label}
                          tile={(tone) => (
                            <KindTile
                              kinds={PROXY_KINDS}
                              kind={subject.kind}
                              engine={engine}
                              tone={tone}
                            />
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
                  count={
                    (data?.history ?? []).length > 0 &&
                    `newest ${Math.min((data?.history ?? []).length, 100)}`
                  }
                />
                <PanelBody>
                  {(data?.history ?? []).length === 0 ? (
                    <NothingTold />
                  ) : (
                    <ul aria-label="Alert history" className="animate-rise">
                      {(data?.history ?? []).slice(0, 12).map((event) => (
                        <HistoryRow
                          key={event.id}
                          event={event}
                          firing={event.event === "proxy.alert.firing"}
                          kindLabel={KINDS[event.kind]?.label ?? event.kind}
                          tile={
                            KINDS[event.kind] ? (
                              <KindTile
                                kinds={PROXY_KINDS}
                                kind={event.kind}
                                engine={engine}
                                size="sm"
                              />
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
                  <ChannelTests
                    channels={channelList}
                    error={channels.error && !channels.data ? channels.error : undefined}
                    onRetry={channels.refresh}
                    admin={admin}
                    busy={busy}
                    onTest={(channel) => void test(channel)}
                  />
                </PanelBody>
              </Panel>
            </div>
          </div>
        </>
      )}

      <RuleSheet open={Boolean(editing)} editing={Boolean(editing?.rule)} onClose={closeForm}>
        {editing && (
          <ProxyRuleForm
            key={session}
            rule={editing.rule}
            kind={editing.kind}
            absent={absent}
            engine={engine}
            channels={channelList}
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

/** One proxy rule as a form: the kit's shape, with each kind's own fields. */
function ProxyRuleForm({
  rule,
  kind: preset,
  absent,
  engine,
  channels,
  onDone,
  onCancel,
}: {
  rule?: ProxyAlertRule
  kind?: ProxyAlertKind
  absent: ReadonlySet<string>
  engine?: string
  channels: NotificationChannel[]
  onDone: () => void
  onCancel: () => void
}) {
  // A new rule opens on the first kind this server has something to read for.
  const start =
    rule?.kind ?? preset ?? KIND_ORDER.find((option) => !absent.has(option)) ?? "cert_expiring"
  const [kind, setKind] = useState<ProxyAlertKind>(start)
  const initial = { ...DEFAULT_PARAMS[start], ...rule?.params }
  const [days, setDays] = useState<string[]>((initial.days ?? EXPIRY_DAYS).map(String))
  const [minutes, setMinutes] = useState(String(initial.minutes ?? ""))
  const [threshold, setThreshold] = useState(String(initial.threshold ?? ""))
  const [minRequests, setMinRequests] = useState(String(initial.minRequests ?? ""))
  const [grade, setGrade] = useState(initial.grade ?? "A")
  const [selected, setSelected] = useState<number[]>(rule?.channels ?? [])
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string>()

  const choose = (next: ProxyAlertKind) => {
    setKind(next)
    const defaults = DEFAULT_PARAMS[next]
    if (defaults.minutes) setMinutes(String(defaults.minutes))
    if (defaults.threshold) setThreshold(String(defaults.threshold))
    if (defaults.minRequests) setMinRequests(String(defaults.minRequests))
    if (defaults.grade) setGrade(defaults.grade)
  }

  const params = (): ProxyAlertParams => {
    switch (kind) {
      case "cert_expiring":
        return { days: days.map(Number).sort((a, b) => b - a) }
      case "upstream_down":
        return { minutes: Number(minutes) }
      case "watch_grade_below":
        return { grade }
      case "site_errors":
        return {
          threshold: Number(threshold),
          minutes: Number(minutes),
          minRequests: Number(minRequests),
        }
      default:
        return {}
    }
  }

  const save = async () => {
    setSaving(true)
    setError(undefined)
    const body = { kind, params: params(), channels: selected, enabled: rule?.enabled ?? true }
    try {
      if (rule) await put(`/proxy/alerts/rules/${rule.id}`, body)
      else await post("/proxy/alerts/rules", body)
      notify.success(rule ? "Alert updated" : "Alert added", {
        description: `${KINDS[kind].label}: ${KINDS[kind].describe(body.params)}.`,
      })
      onDone()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setSaving(false)
    }
  }

  return (
    <RuleForm
      editing={Boolean(rule)}
      onCancel={onCancel}
      kinds={PROXY_KINDS}
      kind={kind}
      onKind={choose}
      absent={absent}
      engine={engine}
      channels={channels}
      selected={selected}
      onSelected={setSelected}
      sentence={`${KINDS[kind].label}: ${KINDS[kind].describe(params())}.`}
      saving={saving}
      onSave={() => void save()}
    >
      {kind === "cert_expiring" && (
        <Field label="Tell at" info="Days left; each is told once." error={error}>
          <ToggleGroup
            type="multiple"
            value={days}
            onValueChange={(next) => next.length > 0 && setDays(next)}
            variant="outline"
            size="sm"
            className="w-full"
            aria-label="Expiry thresholds"
          >
            {EXPIRY_DAYS.map((d) => (
              <ToggleGroupItem key={d} value={String(d)} className="numeric flex-1 text-hint">
                {d} {d === 1 ? "day" : "days"}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>
        </Field>
      )}

      {kind === "upstream_down" && (
        <Field
          label="Down for"
          htmlFor="proxy-alert-minutes"
          info="From 5 minutes to a day; checked every 5 minutes."
          error={error}
        >
          <InputGroup>
            <InputGroupInput
              id="proxy-alert-minutes"
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
      )}

      {kind === "watch_grade_below" && (
        <Field label="Minimum grade" info="Full scans run in batches while this rule is on.">
          <ToggleGroup
            type="single"
            value={grade}
            onValueChange={(next) => next && setGrade(next)}
            variant="outline"
            size="sm"
            className="w-full"
            aria-label="Minimum TLS grade"
          >
            {["A+", "A", "B", "C"].map((value) => (
              <ToggleGroupItem key={value} value={value} className="numeric flex-1 text-hint">
                {value}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>
        </Field>
      )}

      {kind === "site_errors" && (
        <FieldRow>
          <Field
            label="5xx share above"
            htmlFor="proxy-alert-threshold"
            info="Of the site's requests."
            error={error}
          >
            <InputGroup>
              <InputGroupInput
                id="proxy-alert-threshold"
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
          <Field label="Over" htmlFor="proxy-alert-window" info="At least 5 minutes.">
            <InputGroup>
              <InputGroupInput
                id="proxy-alert-window"
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
          <Field label="From at least" htmlFor="proxy-alert-requests" info="Fewer is not judged.">
            <InputGroup>
              <InputGroupInput
                id="proxy-alert-requests"
                inputMode="numeric"
                value={minRequests}
                onChange={(event) => setMinRequests(event.target.value)}
                className="numeric"
              />
              <InputGroupAddon align="inline-end">
                <InputGroupText>requests</InputGroupText>
              </InputGroupAddon>
            </InputGroup>
          </Field>
        </FieldRow>
      )}

      {kind !== "cert_expiring" && kind !== "upstream_down" && kind !== "site_errors" && error && (
        <FormNote tone="danger">{error}</FormNote>
      )}
    </RuleForm>
  )
}
