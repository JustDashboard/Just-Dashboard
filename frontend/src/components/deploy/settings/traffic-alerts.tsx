"use client"

import { useId, useState } from "react"
import Link from "next/link"
import {
  CrossCircle,
  NetworkDevice,
  PaperAirplane,
  Pause,
  Pencil,
  Play,
  Plus,
  Stopwatch,
  Trash,
  type Icon,
} from "@/components/icons"
import { del, post, put } from "@/lib/api"
import { notify } from "@/lib/toast"
import { plural, relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { NotificationChannel, TrafficAlert, TrafficAlertKind } from "@/lib/types"
import { ALERT_KINDS, latency } from "@/lib/requests"
import { useAuth } from "@/hooks/use-auth"
import { useMediaQuery } from "@/hooks/use-mobile"
import { ChoiceCard, ChoiceCardHint, ChoiceCardTitle, ChoiceGrid } from "@/components/choice-card"
import { Field, FieldRow, FormNote, OptionList, OptionRow } from "@/components/form"
import { Meter } from "@/components/meter"
import { ProductLogo } from "@/components/product-logo"
import { ChannelGlyph } from "@/components/deploy/vocabulary"
import { SidePanel, SidePanelFooter } from "@/components/side-panel"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import { Status } from "@/components/status-dot"
import type { Tone } from "@/components/tone"
import { Button } from "@/components/ui/button"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
  InputGroupText,
} from "@/components/ui/input-group"
import { useConfirm } from "@/components/confirm-dialog"
import { VerbActions, type Verb } from "@/components/verbs"
import { SettingSection } from "@/components/deploy/settings/setting-card"
import type { Automation } from "@/components/deploy/settings/automation/use-automation"

/**
 * Traffic alerts: the request record, watched.
 *
 * A rule is a sentence — "more than 1% of requests fail over 5 min" — and the
 * list reads each one as its sentence, never cut short, with the state it is
 * in, how close the last reading came to its line on a meter with the line
 * drawn across it, and the channels it tells drawn as the services they post
 * to. The rules are read-only rows: a rule is edited in a sheet, the one form
 * Logs also opens to add the first rule over its readings. "Send test"
 * delivers a rule as if it had just fired, so the message is seen on the
 * operator's own phone before it is trusted to wake them.
 */

const KIND_GLYPH: Record<TrafficAlertKind, Icon> = {
  error_rate: CrossCircle,
  latency: Stopwatch,
  silence: NetworkDevice,
}

/** The limit a kind starts from when it is picked. */
const DEFAULTS: Record<TrafficAlertKind, { threshold: number; windowMinutes: number }> = {
  error_rate: { threshold: 1, windowMinutes: 5 },
  latency: { threshold: 1000, windowMinutes: 5 },
  silence: { threshold: 0, windowMinutes: 10 },
}

const CHANNEL_KIND: Record<NotificationChannel["kind"], string> = {
  discord: "Discord",
  slack: "Slack",
  telegram: "Telegram",
  email: "E-mail",
  webhook: "Webhook",
}

/** The channels a rule tells: the ones it names, or every enabled one. */
export function toldBy(ids: number[], channels: NotificationChannel[]) {
  return ids.length === 0
    ? channels.filter((channel) => channel.enabled)
    : channels.filter((channel) => ids.includes(channel.id))
}

/**
 * Who a rule tells, as the services they post to and their names: "tells"
 * the channels it names, "tells every channel:" when it names none and so
 * reaches every enabled one — and, in amber, that nobody is told when there
 * is no channel for it to reach.
 */
export function ChannelNames({
  ids,
  channels,
  className,
}: {
  ids: number[]
  channels: NotificationChannel[]
  className?: string
}) {
  const told = toldBy(ids, channels)
  if (told.length === 0)
    return (
      <span className={cn("text-warning", className)}>
        {ids.length === 0
          ? "no channels yet — nobody is told"
          : "tells channels since removed — nobody is told"}
      </span>
    )
  return (
    <span className={cn("inline-flex min-w-0 flex-wrap items-center gap-x-2.5 gap-y-1", className)}>
      <span>{ids.length === 0 ? "tells every channel:" : "tells"}</span>
      {told.map((channel) => (
        <span key={channel.id} className="inline-flex min-w-0 items-center gap-1">
          <ChannelGlyph channel={channel} />
          <span className="truncate text-foreground/85">{channel.name}</span>
        </span>
      ))}
    </span>
  )
}

/** Where a rule stands against its line, as the tone a figure takes. */
function standing(rule: TrafficAlert): Tone {
  if (!rule.enabled || !rule.checkedAt) return "default"
  if (rule.state === "firing") return "danger"
  return rule.kind !== "silence" && rule.observed >= rule.threshold * 0.8 ? "warning" : "default"
}

/** The rule's limit in its own unit: "1%", "1 s". */
function limitWords(rule: Pick<TrafficAlert, "kind" | "threshold">) {
  return rule.kind === "latency" ? latency(rule.threshold) : `${rule.threshold}%`
}

export function TrafficAlerts({
  projectId,
  automation,
}: {
  projectId: number
  automation: Pick<Automation, "alerts" | "channels">
}) {
  const { can } = useAuth()
  const canAdmin = can("system.admin")
  const { confirm, dialog } = useConfirm()
  const { alerts, channels } = automation
  const [editing, setEditing] = useState<{ rule?: TrafficAlert; kind?: TrafficAlertKind }>()

  const channelList = channels.data ?? []
  // Firing first: the rule that is paging somebody is the one a reader came for.
  const rules = [...(alerts.data?.alerts ?? [])].sort(
    (a, b) => Number(b.enabled && b.state === "firing") - Number(a.enabled && a.state === "firing"),
  )
  const firing = rules.filter((rule) => rule.enabled && rule.state === "firing").length
  const reached = [...new Set(rules.flatMap((rule) => toldBy(rule.channels, channelList)))]
  const everyChannel = rules.some((rule) => rule.channels.length === 0)
  // When every rule tells the same channels the head says so once, and a
  // row names its channels only where they differ from the rest.
  const told = (rule: TrafficAlert) =>
    toldBy(rule.channels, channelList)
      .map((channel) => channel.id)
      .sort((a, b) => a - b)
      .join()
  const shared = rules.every((rule) => told(rule) === told(rules[0]))

  const test = async (rule: TrafficAlert) => {
    try {
      const out = await post<{ delivered: number; failed: number; channels: number }>(
        `/deploy/${projectId}/alerts/${rule.id}/test`,
        {},
      )
      if (out.channels === 0) {
        notify.error("No channel to send to", undefined, {
          description: "Add a notification channel first, or name one on the rule.",
        })
      } else if (out.failed > 0) {
        notify.error(`Delivered to ${out.delivered}, failed for ${out.failed}`, undefined, {
          description: "Open Notification channels to see each delivery.",
        })
      } else {
        notify.success(`Test sent to ${plural(out.delivered, "channel")}`)
      }
    } catch (error) {
      notify.error("Could not send the test", error)
    }
  }

  const remove = (rule: TrafficAlert) => {
    const words = ALERT_KINDS[rule.kind]
    confirm({
      title: `Remove ${words.label} alert`,
      confirmLabel: "Remove alert",
      subject: {
        mark: <KindTile kind={rule.kind} />,
        name: words.label,
        facts: <span>{words.describe(rule.threshold, rule.windowMinutes)}</span>,
      },
      description:
        "Nobody is told when it crosses its line again. The request record and the other rules are not touched.",
      action: async () => {
        await del(`/deploy/${projectId}/alerts/${rule.id}`)
      },
      onDone: () => alerts.refresh(),
    })
  }

  const toggle = async (rule: TrafficAlert, enabled: boolean) => {
    try {
      await put(`/deploy/${projectId}/alerts/${rule.id}`, {
        kind: rule.kind,
        threshold: rule.threshold,
        windowMinutes: rule.windowMinutes,
        channels: rule.channels,
        enabled,
      })
      alerts.refresh()
    } catch (error) {
      notify.error("Could not change the alert", error)
    }
  }

  const verbsFor = (rule: TrafficAlert): Verb[] => [
    {
      key: "test",
      label: "Send test",
      icon: PaperAirplane,
      run: () => void test(rule),
    },
    {
      key: "edit",
      label: "Edit",
      icon: Pencil,
      run: () => setEditing({ rule }),
    },
    {
      key: rule.enabled ? "pause" : "resume",
      label: rule.enabled ? "Pause" : "Resume",
      icon: rule.enabled ? Pause : Play,
      run: () => void toggle(rule, !rule.enabled),
    },
    {
      key: "remove",
      label: "Remove",
      icon: Trash,
      danger: true,
      run: () => remove(rule),
    },
  ]

  return (
    <SettingSection
      id="alerts"
      title="Traffic alerts"
      state={
        <span className="block space-y-1">
          <span className="block">
            {rules.length === 0 ? (
              "Nobody is told yet."
            ) : (
              <ChannelNames
                ids={everyChannel ? [] : reached.map((channel) => channel.id)}
                channels={channelList}
              />
            )}
          </span>
          <Button variant="link" size="xs" asChild className="h-auto px-0 text-hint">
            <Link href="/deploy/notifications">Notification channels</Link>
          </Button>
        </span>
      }
      actions={
        <>
          {rules.length > 0 && (
            <span className="mr-1.5 text-hint text-muted-foreground">
              <span className="numeric text-foreground">{plural(rules.length, "rule")}</span>
              {" · "}
              <span className={cn("numeric", firing > 0 && "font-medium text-destructive")}>
                {firing} firing
              </span>
            </span>
          )}
          {canAdmin && (
            <Button size="sm" variant="outline" onClick={() => setEditing({})}>
              <Plus className="size-3.5" /> Add alert
            </Button>
          )}
        </>
      }
    >
      {alerts.loading && !alerts.data ? (
        <LoadingRows rows={2} />
      ) : alerts.error && !alerts.data ? (
        <ErrorState error={alerts.error} onRetry={alerts.refresh} />
      ) : rules.length === 0 ? (
        <div className="space-y-3">
          <EmptyNote className="max-w-prose px-0 py-0 text-left">
            Nobody is told when this deployment fails, slows down or goes quiet. An alert watches
            the request record every minute and tells your notification channels once when it
            crosses the line, and once when it comes back.
          </EmptyNote>
          {canAdmin && (
            <ChoiceGrid columns={3} className="lg:grid-cols-3">
              {(Object.keys(ALERT_KINDS) as TrafficAlertKind[]).map((kind) => (
                <StarterCard key={kind} kind={kind} onClick={() => setEditing({ kind })} />
              ))}
            </ChoiceGrid>
          )}
        </div>
      ) : (
        <ul aria-label="Alert rules" className="min-w-0 animate-rise divide-y divide-hairline">
          {rules.map((rule) => (
            <AlertRow
              key={rule.id}
              rule={rule}
              channels={shared ? undefined : channelList}
              verbs={canAdmin ? verbsFor(rule) : undefined}
            />
          ))}
        </ul>
      )}

      <SidePanel
        open={editing !== undefined}
        onOpenChange={(open) => !open && setEditing(undefined)}
        title={editing?.rule ? "Edit alert" : "Add alert"}
        description="A rule over this deployment's request record, told to your notification channels once when it crosses its line and once when it comes back."
        width="md"
      >
        {editing && (
          <AlertForm
            projectId={projectId}
            rule={editing.rule}
            kind={editing.kind}
            channels={channelList}
            onDone={() => {
              setEditing(undefined)
              alerts.refresh()
            }}
            onCancel={() => setEditing(undefined)}
          />
        )}
      </SidePanel>
      {dialog}
    </SettingSection>
  )
}

function KindTile({ kind, className }: { kind: TrafficAlertKind; className?: string }) {
  return <ProductLogo size="sm" fallback={KIND_GLYPH[kind]} className={className} />
}

/** A rule to start from, for a deployment with none: it opens the form set to it. */
function StarterCard({ kind, onClick }: { kind: TrafficAlertKind; onClick: () => void }) {
  const Glyph = KIND_GLYPH[kind]
  const words = ALERT_KINDS[kind]
  const start = DEFAULTS[kind]
  return (
    <ChoiceCard onClick={onClick} className="min-h-0 gap-1 p-2.5">
      <span className="flex min-w-0 items-center gap-2">
        <Glyph aria-hidden className="size-4 shrink-0 text-muted-foreground" />
        <ChoiceCardTitle className="truncate">{words.label}</ChoiceCardTitle>
      </span>
      <ChoiceCardHint>
        {kind === "silence"
          ? `nothing for ${start.windowMinutes} min`
          : words.describe(start.threshold, start.windowMinutes)}
      </ChoiceCardHint>
    </ChoiceCard>
  )
}

/**
 * One rule, read as its sentence, whole — it wraps rather than truncates,
 * because on a phone the sentence *is* the rule. Under it, the last reading
 * against the rule's line on a meter (a count for a silence rule, which has
 * no line), the channels it tells and when it was last checked. The state
 * sits at the right of the sentence when there is room, and leads the second
 * line on a phone.
 */
function AlertRow({
  rule,
  channels,
  verbs,
}: {
  rule: TrafficAlert
  /** The channels to name this rule's among; absent when the head names them for every rule. */
  channels?: NotificationChannel[]
  verbs?: Verb[]
}) {
  const wide = useMediaQuery("(min-width: 640px)")
  const words = ALERT_KINDS[rule.kind]
  const tone = standing(rule)
  const status = !rule.enabled ? (
    <Status tone="stopped" label="Paused" />
  ) : rule.state === "firing" ? (
    <Status
      tone="danger"
      label={`Firing · ${words.read(rule.observed)}${rule.stateSince ? ` since ${relativeTime(rule.stateSince)}` : ""}`}
    />
  ) : (
    <Status
      tone="running"
      label={rule.checkedAt ? `Quiet · ${words.read(rule.observed)}` : "Waiting for a reading"}
    />
  )
  const scale = Math.max(rule.threshold * 2, rule.observed * 1.1, 1)
  return (
    <li
      className={cn(
        "grid min-w-0 grid-cols-[2rem_minmax(0,1fr)_auto] items-start gap-x-3 gap-y-2 py-3 first:pt-0 last:pb-0",
        !rule.enabled && "opacity-80",
      )}
    >
      <KindTile kind={rule.kind} className={cn(!rule.enabled && "opacity-60")} />
      <p className="min-w-0 pt-1.5 text-body leading-snug text-pretty">
        <span className="font-medium">{words.label}</span>{" "}
        <span className="text-muted-foreground">
          {words.describe(rule.threshold, rule.windowMinutes)}
        </span>
      </p>
      {/* The state stands on the sentence's first line, not centred on the
          menu's taller button a line lower. */}
      <span className="flex items-start gap-3">
        {wide && <span className="flex pt-1.5">{status}</span>}
        {verbs && (
          <VerbActions
            dim
            verbs={verbs}
            // Two rules of one kind are told apart by their sentence.
            menuLabel={`Actions for ${words.label}, ${words.describe(rule.threshold, rule.windowMinutes)}`}
          />
        )}
      </span>
      <div className="col-span-2 col-start-2 flex min-w-0 flex-wrap items-center gap-x-5 gap-y-2 text-hint text-muted-foreground">
        {!wide && status}
        {rule.kind === "silence" ? (
          <span className="numeric whitespace-nowrap">
            {rule.checkedAt
              ? `${plural(rule.observed, "request")} in ${rule.windowMinutes} min`
              : "no reading yet"}
          </span>
        ) : (
          rule.checkedAt && (
            <span className="inline-flex items-center gap-2.5">
              <Meter
                value={(rule.observed / scale) * 100}
                mark={(rule.threshold / scale) * 100}
                tone={tone}
                label={`${words.read(rule.observed)} against a ${limitWords(rule)} line`}
                className="w-28 shrink-0 sm:w-40"
              />
              <span
                className={cn(
                  "numeric whitespace-nowrap",
                  tone === "danger" && "text-destructive",
                  tone === "warning" && "text-warning",
                )}
              >
                {rule.kind === "latency" ? latency(rule.observed) : `${rule.observed.toFixed(1)}%`}{" "}
                <span className="text-muted-foreground">against {limitWords(rule)}</span>
              </span>
            </span>
          )
        )}
        {channels && <ChannelNames ids={rule.channels} channels={channels} />}
        {rule.checkedAt && (
          <span className="whitespace-nowrap">checked {relativeTime(rule.checkedAt)}</span>
        )}
      </div>
    </li>
  )
}

/**
 * One rule as a form: what to watch, the line, the window, who to tell.
 *
 * The kind is picked by its glyph from three cards and decides the unit,
 * which sits inside the limit's own edge, so a limit typed for "slow
 * responses" is never read as a percentage. The rule is spoken back at the
 * foot, where the command that saves it is, before it is saved. The form
 * brings its own footer because it opens in two sheets — here, and over the
 * Logs page's readings — and a rule is written the same way in both (§4).
 */
export function AlertForm({
  projectId,
  rule,
  kind: preset,
  channels,
  onDone,
  onCancel,
}: {
  projectId: number
  rule?: TrafficAlert
  /** The kind a new rule starts as, when the reader chose one to start from. */
  kind?: TrafficAlertKind
  channels: NotificationChannel[]
  onDone: () => void
  onCancel: () => void
}) {
  const start = rule?.kind ?? preset ?? "error_rate"
  const [kind, setKind] = useState<TrafficAlertKind>(start)
  const [threshold, setThreshold] = useState(String(rule?.threshold ?? DEFAULTS[start].threshold))
  const [windowMinutes, setWindowMinutes] = useState(
    String(rule?.windowMinutes ?? DEFAULTS[start].windowMinutes),
  )
  const [selected, setSelected] = useState<number[]>(rule?.channels ?? [])
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string>()
  const words = ALERT_KINDS[kind]
  const limit = Number(threshold) || 0
  const formId = useId()

  const choose = (next: TrafficAlertKind) => {
    setKind(next)
    if (next === "latency" && Number(threshold) <= 100) setThreshold("1000")
    if (next === "error_rate" && Number(threshold) > 100) setThreshold("1")
    if (next === "silence" && Number(windowMinutes) < 5) setWindowMinutes("10")
  }

  const save = async () => {
    setSaving(true)
    setError(undefined)
    const body = {
      kind,
      threshold: kind === "silence" ? 0 : Number(threshold),
      windowMinutes: Number(windowMinutes),
      channels: selected,
      enabled: rule?.enabled ?? true,
    }
    try {
      if (rule) await put(`/deploy/${projectId}/alerts/${rule.id}`, body)
      else await post(`/deploy/${projectId}/alerts`, body)
      notify.success(rule ? "Alert updated" : "Alert added", {
        description: `${words.label}: ${words.describe(body.threshold, body.windowMinutes)}.`,
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
          <ChoiceGrid columns={3} className="grid-cols-1 sm:grid-cols-3 lg:grid-cols-3">
            {(Object.keys(ALERT_KINDS) as TrafficAlertKind[]).map((option) => {
              const Glyph = KIND_GLYPH[option]
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
                    <ChoiceCardTitle className="truncate">
                      {ALERT_KINDS[option].label}
                    </ChoiceCardTitle>
                  </span>
                  <ChoiceCardHint className="line-clamp-2">
                    {ALERT_KINDS[option].hint}
                  </ChoiceCardHint>
                </ChoiceCard>
              )
            })}
          </ChoiceGrid>
        </div>
      </Field>

      <FieldRow>
        {kind !== "silence" && (
          <Field
            label={kind === "latency" ? "p95 above" : "Failing share above"}
            htmlFor="alert-limit"
            hint={kind === "latency" ? `= ${latency(limit)}` : "Of requests answered 5xx."}
            error={error}
          >
            <InputGroup>
              <InputGroupInput
                id="alert-limit"
                inputMode="decimal"
                value={threshold}
                onChange={(event) => setThreshold(event.target.value)}
                aria-label="Alert limit"
                className="numeric"
              />
              <InputGroupAddon align="inline-end">
                <InputGroupText>{kind === "latency" ? "ms" : "%"}</InputGroupText>
              </InputGroupAddon>
            </InputGroup>
          </Field>
        )}
        <Field
          label="Over"
          htmlFor="alert-window"
          hint={
            kind === "silence"
              ? "Minutes with no requests at all; at least 5."
              : "The window each reading is taken over."
          }
          error={kind === "silence" ? error : undefined}
        >
          <InputGroup>
            <InputGroupInput
              id="alert-window"
              inputMode="numeric"
              value={windowMinutes}
              onChange={(event) => setWindowMinutes(event.target.value)}
              aria-label="Alert window in minutes"
              className="numeric"
            />
            <InputGroupAddon align="inline-end">
              <InputGroupText>min</InputGroupText>
            </InputGroupAddon>
          </InputGroup>
        </Field>
      </FieldRow>

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
                  hint={`${CHANNEL_KIND[channel.kind]} · ${channel.target || channel.url}${channel.enabled ? "" : " · paused"}`}
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

      {/* The sheet's footer, brought by the form: it opens inside two sheets
          and its command has to stay beside the rule it saves. */}
      <SidePanelFooter>
        <p className="mr-auto min-w-0 basis-full text-hint text-muted-foreground sm:basis-auto">
          {words.label}:{" "}
          {words.describe(kind === "silence" ? 0 : limit, Number(windowMinutes) || 0)}.
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
