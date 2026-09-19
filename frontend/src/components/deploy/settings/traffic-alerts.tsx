"use client"

import { useState } from "react"
import { Bell, Pause, Pencil, Play, Trash } from "@/components/icons"
import { del, get, post, put } from "@/lib/api"
import { notify } from "@/lib/toast"
import { relativeTime } from "@/lib/format"
import type { NotificationChannel, TrafficAlert, TrafficAlertKind, TrafficAlertList } from "@/lib/types"
import { ALERT_KINDS, latency } from "@/lib/requests"
import { usePoll } from "@/hooks/use-poll"
import { Field, FieldRow, OptionRow } from "@/components/form"
import { Row, RowList } from "@/components/row-list"
import { EmptyNote } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { VerbActions, type Verb } from "@/components/verbs"
import { SettingCard } from "@/components/deploy/settings/setting-card"

/**
 * Traffic alerts: the request record, watched.
 *
 * A rule is a sentence — "more than 1% of requests fail over 5 min" — and the
 * list reads them as sentences, with the state each is in and when it got
 * there. The channels are the ones deployments already announce themselves
 * on; a rule that names none reaches all of them. "Send test" delivers the
 * rule as if it had just fired, so the message is seen on the operator's own
 * phone before it is trusted to wake them.
 */
export function TrafficAlerts({ projectId }: { projectId: number }) {
  const alerts = usePoll<TrafficAlertList>(
    (signal) => get<TrafficAlertList>(`/deploy/${projectId}/alerts`, undefined, signal),
    15000,
    [projectId],
  )
  const channels = usePoll<NotificationChannel[]>(
    (signal) => get<NotificationChannel[]>("/deploy/notifications", undefined, signal),
    0,
    [],
  )
  const [editing, setEditing] = useState<TrafficAlert | "new" | null>(null)

  const rules = alerts.data?.alerts ?? []
  const named = (ids: number[]) =>
    ids.length === 0
      ? "every enabled channel"
      : ids
          .map((id) => channels.data?.find((c) => c.id === id)?.name ?? `channel ${id}`)
          .join(", ")

  const test = async (rule: TrafficAlert) => {
    try {
      const out = await post<{ delivered: number; failed: number; channels: number }>(
        `/deploy/${projectId}/alerts/${rule.id}/test`,
        {},
      )
      if (out.channels === 0) {
        notify.error("No channel to send to", {
          description: "Add a notification channel first, or name one on the rule.",
        })
      } else if (out.failed > 0) {
        notify.error(`Delivered to ${out.delivered}, failed for ${out.failed}`, {
          description: "Open Notification channels to see each delivery.",
        })
      } else {
        notify.success(`Test sent to ${out.delivered} ${out.delivered === 1 ? "channel" : "channels"}`)
      }
    } catch (error) {
      notify.error("Could not send the test", { description: String(error) })
    }
  }

  const remove = async (rule: TrafficAlert) => {
    try {
      await del(`/deploy/${projectId}/alerts/${rule.id}`)
      notify.success("Alert removed")
      alerts.refresh()
    } catch (error) {
      notify.error("Could not remove the alert", { description: String(error) })
    }
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
      notify.error("Could not change the alert", { description: String(error) })
    }
  }

  return (
    <SettingCard
      id="alerts"
      title="Traffic alerts"
      actions={
        <Button size="sm" variant="secondary" onClick={() => setEditing("new")}>
          Add an alert
        </Button>
      }
    >
      {editing && (
        <AlertForm
          projectId={projectId}
          rule={editing === "new" ? undefined : editing}
          channels={channels.data ?? []}
          onDone={() => {
            setEditing(null)
            alerts.refresh()
          }}
          onCancel={() => setEditing(null)}
        />
      )}
      {rules.length === 0 && !editing ? (
        <EmptyNote className="px-0 py-4 text-left">
          Nobody is told when this deployment fails, slows down or goes quiet. An alert watches the
          request record every minute and tells your notification channels once when it crosses the
          line, and once when it comes back.
        </EmptyNote>
      ) : (
        <RowList aria-label="Traffic alerts">
          {rules.map((rule) => {
            const words = ALERT_KINDS[rule.kind]
            const verbs: Verb[] = [
              { key: "test", label: "Send test", detail: "Deliver this rule as if it had just fired.", icon: Bell, run: () => void test(rule) },
              { key: "edit", label: "Edit", detail: "Change the limit, the window or the channels.", icon: Pencil, run: () => setEditing(rule) },
              {
                key: rule.enabled ? "pause" : "resume",
                label: rule.enabled ? "Pause" : "Resume",
                detail: rule.enabled ? "Keep the rule, stop watching." : "Watch again.",
                icon: rule.enabled ? Pause : Play,
                run: () => void toggle(rule, !rule.enabled),
              },
              { key: "remove", label: "Remove", detail: "Delete the rule. Nothing else changes.", icon: Trash, danger: true, run: () => void remove(rule) },
            ]
            return (
              <Row
                key={rule.id}
                title={
                  <>
                    {words.label}
                    <span className="ml-2 font-normal text-muted-foreground">
                      {words.describe(rule.threshold, rule.windowMinutes)}
                    </span>
                  </>
                }
                subtitle={`To ${named(rule.channels)}${rule.checkedAt ? ` · checked ${relativeTime(rule.checkedAt)}` : ""}`}
                trailing={
                  <>
                    {!rule.enabled ? (
                      <Tag>paused</Tag>
                    ) : rule.state === "firing" ? (
                      <Status
                        tone="danger"
                        label={`Firing · ${words.read(rule.observed)}${rule.stateSince ? ` since ${relativeTime(rule.stateSince)}` : ""}`}
                      />
                    ) : (
                      <Status tone="running" label={rule.checkedAt ? `Quiet · ${words.read(rule.observed)}` : "Waiting for a reading"} />
                    )}
                    <VerbActions verbs={verbs} menuLabel={`Alert ${rule.id}`} />
                  </>
                }
              />
            )
          })}
        </RowList>
      )}
    </SettingCard>
  )
}

/**
 * One rule as a form: what to watch, the line, the window, who to tell. The
 * kind decides the unit and the sentence under the field, so a limit typed
 * for "slow responses" is never read as a percentage.
 */
function AlertForm({
  projectId,
  rule,
  channels,
  onDone,
  onCancel,
}: {
  projectId: number
  rule?: TrafficAlert
  channels: NotificationChannel[]
  onDone: () => void
  onCancel: () => void
}) {
  const [kind, setKind] = useState<TrafficAlertKind>(rule?.kind ?? "error_rate")
  const [threshold, setThreshold] = useState(String(rule?.threshold ?? (rule?.kind === "latency" ? 1000 : 1)))
  const [windowMinutes, setWindowMinutes] = useState(String(rule?.windowMinutes ?? 5))
  const [selected, setSelected] = useState<number[]>(rule?.channels ?? [])
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string>()
  const words = ALERT_KINDS[kind]

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
      setError(String(err instanceof Error ? err.message : err))
    } finally {
      setSaving(false)
    }
  }

  return (
    <form
      className="mb-4 space-y-3 border-b border-hairline pb-4"
      onSubmit={(event) => {
        event.preventDefault()
        void save()
      }}
    >
      <FieldRow columns={3}>
        <Field label="Watch for" hint={words.hint}>
          <Select value={kind} onValueChange={(value) => {
            const next = value as TrafficAlertKind
            setKind(next)
            if (next === "latency" && Number(threshold) <= 100) setThreshold("1000")
            if (next === "error_rate" && Number(threshold) > 100) setThreshold("1")
            if (next === "silence" && Number(windowMinutes) < 5) setWindowMinutes("10")
          }}>
            <SelectTrigger aria-label="Alert kind">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {(Object.keys(ALERT_KINDS) as TrafficAlertKind[]).map((k) => (
                <SelectItem key={k} value={k}>
                  {ALERT_KINDS[k].label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        {kind !== "silence" && (
          <Field
            label={kind === "latency" ? "p95 above" : "Failing share above"}
            hint={kind === "latency" ? `Milliseconds — ${latency(Number(threshold) || 0)}.` : "Per cent of requests answered 5xx."}
            error={error}
          >
            <Input
              inputMode="decimal"
              value={threshold}
              onChange={(event) => setThreshold(event.target.value)}
              aria-label="Alert limit"
              className="numeric"
            />
          </Field>
        )}
        <Field
          label="Over"
          hint={kind === "silence" ? "Minutes with no requests at all; at least 5." : "Minutes the reading is taken over."}
          error={kind === "silence" ? error : undefined}
        >
          <Input
            inputMode="numeric"
            value={windowMinutes}
            onChange={(event) => setWindowMinutes(event.target.value)}
            aria-label="Alert window in minutes"
            className="numeric"
          />
        </Field>
      </FieldRow>

      <div>
        <p className="mb-1 text-body font-medium">Tell</p>
        {channels.length === 0 ? (
          <p className="text-hint text-muted-foreground">
            No notification channels yet — the rule will reach every channel you add later.
          </p>
        ) : (
          <div className="space-y-1">
            {channels.map((channel) => (
              <OptionRow
                key={channel.id}
                title={channel.name}
                hint={`${channel.kind} · ${channel.target || channel.url}`}
                checked={selected.includes(channel.id)}
                onCheckedChange={(on) =>
                  setSelected((prev) => (on ? [...prev, channel.id] : prev.filter((id) => id !== channel.id)))
                }
              />
            ))}
            <p className="pt-1 text-hint text-muted-foreground">
              {selected.length === 0 ? "None chosen: every enabled channel is told." : `${selected.length} chosen.`}
            </p>
          </div>
        )}
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <Button type="submit" size="sm" disabled={saving}>
          {saving ? "Saving…" : rule ? "Save alert" : "Add alert"}
        </Button>
        <Button type="button" size="sm" variant="ghost" onClick={onCancel} disabled={saving}>
          Cancel
        </Button>
        <span className="text-hint text-muted-foreground">
          {words.label}: {words.describe(kind === "silence" ? 0 : Number(threshold) || 0, Number(windowMinutes) || 0)}.
        </span>
      </div>
    </form>
  )
}
