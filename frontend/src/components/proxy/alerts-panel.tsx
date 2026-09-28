"use client"

import { useId, useState } from "react"
import Link from "next/link"
import {
  Clock,
  CrossCircle,
  Globe,
  NetworkDevice,
  PaperAirplane,
  Pause,
  Pencil,
  Play,
  Plus,
  RefreshClockwise,
  Servers,
  ShieldOff,
  Slash,
  Trash,
  type Icon,
} from "@/components/icons"
import { del, get, post, put } from "@/lib/api"
import { notify } from "@/lib/toast"
import { plural, relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type {
  NotificationChannel,
  ProxyAlertEvent,
  ProxyAlertKind,
  ProxyAlertParams,
  ProxyAlertRule,
  ProxyAlerts,
  ProxyAlertSubject,
} from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { ChoiceCard, ChoiceCardHint, ChoiceCardTitle, ChoiceGrid } from "@/components/choice-card"
import { Field, FieldRow, FormNote, FormSection, OptionList, OptionRow } from "@/components/form"
import { ProductLogo } from "@/components/product-logo"
import { ChannelGlyph } from "@/components/deploy/vocabulary"
import { ChannelNames } from "@/components/deploy/settings/traffic-alerts"
import { SidePanel, SidePanelFooter } from "@/components/side-panel"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
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

/**
 * The proxy's alerts: rules over the readings the overview shows, told to the
 * deployments' notification channels once when a subject starts firing and
 * once when it comes back.
 *
 * The sheet leads with what is firing now, because that is what a reader
 * opens it for, then the rules, the subjects kept quiet by a mute, a test per
 * channel, and what was told lately. A rule is edited in the same sheet: the
 * list gives way to the form and comes back when it is saved.
 */

type KindWords = {
  label: string
  hint: string
  icon: Icon
  describe: (params: ProxyAlertParams) => string
}

const EXPIRY_DAYS = [21, 7, 3, 1]

const KINDS: Record<ProxyAlertKind, KindWords> = {
  cert_expiring: {
    label: "Certificate expiring",
    hint: "Told once at each threshold a certificate crosses.",
    icon: Clock,
    describe: (p) => `at ${(p.days ?? EXPIRY_DAYS).join(", ")} days left`,
  },
  cert_expired: {
    label: "Certificate expired",
    hint: "A certificate on this host has run out.",
    icon: CrossCircle,
    describe: () => "once a certificate runs out",
  },
  engine_down: {
    label: "Engine not running",
    hint: "The engine's unit stopped or failed, on two checks in a row.",
    icon: Servers,
    describe: () => "stopped or failed on two checks in a row",
  },
  upstream_down: {
    label: "Upstream down",
    hint: "An address nginx forwards to stops answering.",
    icon: NetworkDevice,
    describe: (p) => `down for ${p.minutes ?? 5} min`,
  },
  watch_unreachable: {
    label: "Watched endpoint unreachable",
    hint: "No TLS handshake on two checks in a row.",
    icon: Globe,
    describe: () => "no TLS handshake on two checks in a row",
  },
  watch_untrusted: {
    label: "Watched endpoint untrusted",
    hint: "It serves a certificate the system does not trust.",
    icon: ShieldOff,
    describe: () => "serves a certificate that is not trusted",
  },
  site_errors: {
    label: "Site failing",
    hint: "A site's share of 5xx answers over a window.",
    icon: Slash,
    describe: (p) =>
      `more than ${p.threshold ?? 5}% answered 5xx over ${p.minutes ?? 15} min, from ${p.minRequests ?? 20} requests`,
  },
}

const KIND_ORDER = Object.keys(KINDS) as ProxyAlertKind[]

const DEFAULT_PARAMS: Record<ProxyAlertKind, ProxyAlertParams> = {
  cert_expiring: { days: EXPIRY_DAYS },
  cert_expired: {},
  engine_down: {},
  upstream_down: { minutes: 5 },
  watch_unreachable: {},
  watch_untrusted: {},
  site_errors: { threshold: 5, minutes: 15, minRequests: 20 },
}

function KindTile({ kind, className }: { kind: ProxyAlertKind; className?: string }) {
  return <ProductLogo size="sm" fallback={KINDS[kind].icon} className={className} />
}

export function AlertsPanel({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const alerts = usePoll(
    (signal) => get<ProxyAlerts>("/proxy/alerts", undefined, signal),
    60_000,
    [],
    { enabled: open },
  )
  const channels = usePoll(
    (signal) => get<NotificationChannel[]>("/deploy/notifications", undefined, signal),
    300_000,
    [],
    { enabled: open },
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
  const active = (data?.subjects ?? []).filter((s) => s.state !== "quiet")
  const muted = (data?.subjects ?? []).filter((s) => s.state === "quiet" && s.muted)

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
    mark: <KindTile kind={rule.kind} />,
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

  return (
    <SidePanel
      open={open}
      onOpenChange={onOpenChange}
      title={editing ? (editing.rule ? "Edit alert" : "Add alert") : "Alerts"}
      description="Rules over the proxy's readings, told to your notification channels once when a subject starts firing and once when it comes back."
      width="lg"
      initialFocus="body"
      actions={
        !editing && (
          <>
            <Button size="xs" variant="outline" onClick={check} pending={checking}>
              <RefreshClockwise />
              Check now
            </Button>
            <Button size="xs" variant="outline" onClick={() => edit({})}>
              <Plus />
              Add rule
            </Button>
            {data?.lastPass && (
              <span className="text-hint text-muted-foreground">
                checked {relativeTime(data.lastPass)}
              </span>
            )}
          </>
        )
      }
    >
      {editing ? (
        <RuleForm
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
      ) : alerts.loading && !data ? (
        <LoadingRows rows={4} />
      ) : alerts.error && !data ? (
        <ErrorState error={alerts.error} onRetry={alerts.refresh} />
      ) : (
        <div className="flex flex-col gap-8">
          <FormSection title="Firing">
            {active.length === 0 ? (
              <EmptyNote className="px-0 py-0 text-left">
                {rules.length === 0
                  ? "No rules yet, so nothing is followed."
                  : "Nothing is firing."}
              </EmptyNote>
            ) : (
              <ul aria-label="Firing" className="divide-y divide-hairline">
                {active.map((subject) => (
                  <SubjectRow
                    key={`${subject.ruleId}:${subject.subject}`}
                    subject={subject}
                    busy={busy === `mute:${subject.ruleId}:${subject.subject}`}
                    onMute={(on) => void setMuted(subject, on)}
                  />
                ))}
              </ul>
            )}
          </FormSection>

          <FormSection title="Rules">
            {rules.length === 0 ? (
              <div className="space-y-3">
                <EmptyNote className="max-w-prose px-0 py-0 text-left">
                  Nobody is told when a certificate runs low, the engine stops or a site starts
                  failing. A rule is checked every {Math.round((data?.intervalSeconds ?? 300) / 60)}{" "}
                  minutes and tells your channels once on the way over and once on the way back.
                </EmptyNote>
                <ChoiceGrid columns={2} className="grid-cols-1 sm:grid-cols-2 lg:grid-cols-2">
                  {KIND_ORDER.map((kind) => (
                    <KindCard key={kind} kind={kind} onClick={() => edit({ kind })} />
                  ))}
                </ChoiceGrid>
              </div>
            ) : (
              <ul aria-label="Alert rules" className="divide-y divide-hairline">
                {rules.map((rule) => (
                  <li
                    key={rule.id}
                    className={cn(
                      "grid min-w-0 grid-cols-[2rem_minmax(0,1fr)_auto] items-start gap-x-3 gap-y-1.5 py-3 first:pt-0 last:pb-0",
                      !rule.enabled && "opacity-80",
                    )}
                  >
                    <KindTile kind={rule.kind} className={cn(!rule.enabled && "opacity-60")} />
                    <p className="min-w-0 pt-1.5 text-body leading-snug text-pretty">
                      <span className="font-medium">{KINDS[rule.kind].label}</span>{" "}
                      <span className="text-muted-foreground">
                        {KINDS[rule.kind].describe(rule.params)}
                      </span>
                    </p>
                    <span className="flex items-start gap-3">
                      {!rule.enabled && (
                        <span className="flex pt-1.5">
                          <Status tone="stopped" label="Paused" />
                        </span>
                      )}
                      <VerbActions
                        dim
                        verbs={verbsFor(rule)}
                        menuLabel={`Actions for ${KINDS[rule.kind].label}, ${KINDS[rule.kind].describe(rule.params)}`}
                      />
                    </span>
                    <ChannelNames
                      ids={rule.channels}
                      channels={channelList}
                      className="col-span-2 col-start-2 text-hint text-muted-foreground"
                    />
                  </li>
                ))}
              </ul>
            )}
          </FormSection>

          {muted.length > 0 && (
            <FormSection title="Muted">
              <ul aria-label="Muted" className="divide-y divide-hairline">
                {muted.map((subject) => (
                  <SubjectRow
                    key={`${subject.ruleId}:${subject.subject}`}
                    subject={subject}
                    busy={busy === `mute:${subject.ruleId}:${subject.subject}`}
                    onMute={(on) => void setMuted(subject, on)}
                  />
                ))}
              </ul>
            </FormSection>
          )}

          <FormSection title="Send test">
            {channels.error && !channels.data ? (
              <ErrorState error={channels.error} onRetry={channels.refresh} />
            ) : channelList.length === 0 ? (
              <FormNote>
                No notification channels yet, so a rule tells nobody.{" "}
                <Link
                  href="/deploy/notifications"
                  className="text-foreground underline-offset-2 hover:underline"
                >
                  Add a channel
                </Link>
              </FormNote>
            ) : (
              <ul aria-label="Notification channels" className="divide-y divide-hairline">
                {channelList.map((channel) => (
                  <li key={channel.id} className="flex min-w-0 items-center gap-3 py-2.5">
                    <ChannelGlyph channel={channel} />
                    <span className="min-w-0 flex-1 truncate text-body">{channel.name}</span>
                    {!channel.enabled && <Status tone="stopped" label="Paused" />}
                    <Button
                      size="xs"
                      variant="outline"
                      disabled={!channel.enabled}
                      pending={busy === `test:${channel.id}`}
                      onClick={() => void test(channel)}
                      aria-label={`Send a test alert to ${channel.name}`}
                    >
                      <PaperAirplane />
                      Send test
                    </Button>
                  </li>
                ))}
              </ul>
            )}
          </FormSection>

          <FormSection title="History">
            {(data?.history ?? []).length === 0 ? (
              <EmptyNote className="px-0 py-0 text-left">Nothing has been told yet.</EmptyNote>
            ) : (
              <ul aria-label="Alert history" className="divide-y divide-hairline">
                {(data?.history ?? []).map((event) => (
                  <HistoryRow key={event.id} event={event} ruleGone={!ruleOf(event.ruleId)} />
                ))}
              </ul>
            )}
          </FormSection>
        </div>
      )}
      {dialog}
    </SidePanel>
  )
}

function KindCard({ kind, onClick }: { kind: ProxyAlertKind; onClick: () => void }) {
  const Glyph = KINDS[kind].icon
  return (
    <ChoiceCard onClick={onClick} className="min-h-0 gap-1 p-2.5">
      <span className="flex min-w-0 items-center gap-2">
        <Glyph aria-hidden className="size-4 shrink-0 text-muted-foreground" />
        <ChoiceCardTitle className="truncate">{KINDS[kind].label}</ChoiceCardTitle>
      </span>
      <ChoiceCardHint className="line-clamp-2">{KINDS[kind].hint}</ChoiceCardHint>
    </ChoiceCard>
  )
}

/** One subject a rule follows: what it is, its reading, and its mute. */
function SubjectRow({
  subject,
  busy,
  onMute,
}: {
  subject: ProxyAlertSubject
  busy: boolean
  onMute: (muted: boolean) => void
}) {
  const status =
    subject.state === "firing" ? (
      <Status
        tone="danger"
        label={`Firing${subject.since ? ` since ${relativeTime(subject.since)}` : ""}`}
      />
    ) : subject.state === "pending" ? (
      <Status tone="warning" label="Holding before it is told" />
    ) : (
      <Status tone="stopped" label="Quiet" />
    )
  return (
    <li className="grid min-w-0 grid-cols-[2rem_minmax(0,1fr)_auto] items-start gap-x-3 gap-y-1 py-3 first:pt-0 last:pb-0">
      <KindTile kind={subject.kind} />
      <div className="min-w-0 space-y-1 pt-1">
        <p className="min-w-0 text-body leading-snug">
          <span className="font-medium break-all">{subject.label || subject.subject}</span>{" "}
          <span className="text-muted-foreground">· {KINDS[subject.kind].label}</span>
        </p>
        {subject.detail && (
          <p className="text-hint text-pretty text-muted-foreground">{subject.detail}</p>
        )}
        <p className="flex flex-wrap items-center gap-x-3 gap-y-1 text-hint">
          {status}
          {subject.muted && <span className="text-muted-foreground">muted, nobody is told</span>}
        </p>
      </div>
      <Button
        size="xs"
        variant="outline"
        pending={busy}
        onClick={() => onMute(!subject.muted)}
        aria-label={`${subject.muted ? "Unmute" : "Mute"} ${subject.label || subject.subject}`}
      >
        {subject.muted ? "Unmute" : "Mute"}
      </Button>
    </li>
  )
}

function HistoryRow({ event, ruleGone }: { event: ProxyAlertEvent; ruleGone: boolean }) {
  const firing = event.event === "proxy.alert.firing"
  const outcome = event.muted
    ? "muted, nobody told"
    : event.delivered === 0 && event.failed === 0
      ? "no channel to tell"
      : `told ${plural(event.delivered, "channel")}${event.failed > 0 ? `, ${event.failed} failed` : ""}`
  return (
    <li className="min-w-0 space-y-0.5 py-2.5 first:pt-0 last:pb-0">
      <p className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-body">
        <Status tone={firing ? "danger" : "running"} label={firing ? "Fired" : "Resolved"} />
        <span className="min-w-0 font-medium break-all">{event.label || event.subject}</span>
        <span className="text-muted-foreground">
          {KINDS[event.kind]?.label ?? event.kind}
          {ruleGone && " (rule removed)"}
        </span>
      </p>
      <p className="text-hint text-pretty text-muted-foreground">
        {relativeTime(event.createdAt)} · {outcome}
        {event.detail && ` · ${event.detail}`}
      </p>
    </li>
  )
}

/**
 * One rule as a form: what to watch, its line where the kind has one, and
 * who to tell. It brings its own footer, so the command stays beside the rule
 * it saves.
 */
function RuleForm({
  rule,
  kind: preset,
  channels,
  onDone,
  onCancel,
}: {
  rule?: ProxyAlertRule
  kind?: ProxyAlertKind
  channels: NotificationChannel[]
  onDone: () => void
  onCancel: () => void
}) {
  const start = rule?.kind ?? preset ?? "cert_expiring"
  const [kind, setKind] = useState<ProxyAlertKind>(start)
  const initial = { ...DEFAULT_PARAMS[start], ...rule?.params }
  const [days, setDays] = useState<string[]>((initial.days ?? EXPIRY_DAYS).map(String))
  const [minutes, setMinutes] = useState(String(initial.minutes ?? ""))
  const [threshold, setThreshold] = useState(String(initial.threshold ?? ""))
  const [minRequests, setMinRequests] = useState(String(initial.minRequests ?? ""))
  const [selected, setSelected] = useState<number[]>(rule?.channels ?? [])
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string>()
  const formId = useId()

  const choose = (next: ProxyAlertKind) => {
    setKind(next)
    const defaults = DEFAULT_PARAMS[next]
    if (defaults.minutes) setMinutes(String(defaults.minutes))
    if (defaults.threshold) setThreshold(String(defaults.threshold))
    if (defaults.minRequests) setMinRequests(String(defaults.minRequests))
  }

  const params = (): ProxyAlertParams => {
    switch (kind) {
      case "cert_expiring":
        return { days: days.map(Number).sort((a, b) => b - a) }
      case "upstream_down":
        return { minutes: Number(minutes) }
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
    <form
      id={formId}
      className="flex flex-col gap-6"
      onSubmit={(event) => {
        event.preventDefault()
        void save()
      }}
    >
      <Field label="Watch for">
        <div role="group" aria-label="Alert kind">
          <ChoiceGrid columns={2} className="grid-cols-1 sm:grid-cols-2 lg:grid-cols-2">
            {KIND_ORDER.map((option) => {
              const Glyph = KINDS[option].icon
              return (
                <ChoiceCard
                  key={option}
                  selected={kind === option}
                  onClick={() => choose(option)}
                  className="min-h-0 gap-1 p-2.5"
                >
                  <span className="flex min-w-0 items-center gap-2">
                    <Glyph
                      aria-hidden
                      className={cn(
                        "size-4 shrink-0",
                        kind === option ? "text-brand" : "text-muted-foreground",
                      )}
                    />
                    <ChoiceCardTitle className="truncate">{KINDS[option].label}</ChoiceCardTitle>
                  </span>
                  <ChoiceCardHint className="line-clamp-2">{KINDS[option].hint}</ChoiceCardHint>
                </ChoiceCard>
              )
            })}
          </ChoiceGrid>
        </div>
      </Field>

      {kind === "cert_expiring" && (
        <Field label="Tell at" hint="Days left; each is told once." error={error}>
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
          hint="From 5 minutes to a day; checked every 5 minutes."
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

      {kind === "site_errors" && (
        <FieldRow>
          <Field
            label="5xx share above"
            htmlFor="proxy-alert-threshold"
            hint="Of the site's requests."
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
          <Field label="Over" htmlFor="proxy-alert-window" hint="At least 5 minutes.">
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
          <Field label="From at least" htmlFor="proxy-alert-requests" hint="Fewer is not judged.">
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

      <Field label="Tell">
        {channels.length === 0 ? (
          <FormNote>
            No notification channels yet — the rule reaches every channel you add later.{" "}
            <Link
              href="/deploy/notifications"
              className="text-foreground underline-offset-2 hover:underline"
            >
              Add a channel
            </Link>
          </FormNote>
        ) : (
          <div className="space-y-1.5">
            <OptionList>
              {channels.map((channel) => (
                <OptionRow
                  key={channel.id}
                  title={
                    <span className="inline-flex items-center gap-2">
                      <ChannelGlyph channel={channel} />
                      {channel.name}
                    </span>
                  }
                  hint={`${channel.target || channel.url}${channel.enabled ? "" : " · paused"}`}
                  checked={selected.includes(channel.id)}
                  onCheckedChange={(on) =>
                    setSelected((prev) =>
                      on ? [...prev, channel.id] : prev.filter((id) => id !== channel.id),
                    )
                  }
                />
              ))}
            </OptionList>
            <FormNote>
              {selected.length === 0
                ? "None chosen: every enabled channel is told."
                : `${selected.length} chosen.`}
            </FormNote>
          </div>
        )}
      </Field>

      <SidePanelFooter>
        <p className="mr-auto min-w-0 basis-full text-hint text-muted-foreground sm:basis-auto">
          {KINDS[kind].label}: {KINDS[kind].describe(params())}.
        </p>
        <Button type="button" variant="outline" onClick={onCancel} disabled={saving}>
          Cancel
        </Button>
        <Button type="submit" form={formId} pending={saving}>
          {rule ? "Save alert" : "Add alert"}
        </Button>
      </SidePanelFooter>
    </form>
  )
}
