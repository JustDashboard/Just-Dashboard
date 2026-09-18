"use client"

import { useState } from "react"
import Link from "next/link"
import { Bell, ClockRewind, Lightning, Pause, Pencil, Play, Plus, Trash } from "@/components/icons"
import { del, errorMessage, get, post, put } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type {
  NotificationChannel,
  NotificationChannelConfig,
  NotificationChannelKind,
  NotificationDelivery,
  NotificationEvent,
} from "@/lib/types"
import { Page, PageHeader } from "@/components/page"
import { Panel, Well } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { EmptyNote, EmptyState, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { SidePanel } from "@/components/side-panel"
import { Tag } from "@/components/tag"
import { useConfirm } from "@/components/confirm-dialog"
import { Field, FieldRow, FormNote, OptionList, OptionRow } from "@/components/form"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Textarea } from "@/components/ui/textarea"
import { VerbActions, type Verb } from "@/components/verbs"

/**
 * Where a deployment outcome is announced.
 *
 * Channels are global rather than per-project — one Discord room hears about
 * every release — which is why this is its own destination off the fleet
 * page rather than a tab nested under a single project.
 */

const KINDS: { kind: NotificationChannelKind; title: string; hint: string }[] = [
  {
    kind: "discord",
    title: "Discord",
    hint: "A channel webhook from Server settings → Integrations.",
  },
  { kind: "slack", title: "Slack", hint: "An incoming webhook from a Slack app." },
  {
    kind: "telegram",
    title: "Telegram",
    hint: "A bot token from @BotFather and the chat to post in.",
  },
  { kind: "email", title: "E-mail", hint: "An SMTP server, sender and recipients." },
  { kind: "webhook", title: "Signed webhook", hint: "Your own endpoint receives signed JSON." },
]

const EVENTS: { event: NotificationEvent; label: string; hint: string }[] = [
  { event: "run.started", label: "Started", hint: "A build or release has begun." },
  { event: "run.succeeded", label: "Succeeded", hint: "The release is live." },
  {
    event: "run.failed",
    label: "Failed",
    hint: "Including a verified rollback to the previous release.",
  },
  { event: "run.cancelled", label: "Cancelled", hint: "Stopped by an operator before finishing." },
]

const DEFAULT_EVENTS: NotificationEvent[] = ["run.succeeded", "run.failed"]

// notifications.go's update route has no fallback at all for a webhook kind's
// URL (it is never masked, so the read model returns it verbatim and Edit can
// resend it unchanged), and none for email's host/from/to or Telegram's chat
// id — leaving those blank does not "keep the current value", it fails
// validation. Discord/Slack's webhook URL is the one credential that *is*
// kept when blank, so it is the one worth hiding behind a disclosure.
const ALWAYS_OPEN: NotificationChannelKind[] = ["webhook", "email", "telegram"]
const REENTRY_KINDS: NotificationChannelKind[] = ["email", "telegram"]

type Draft = {
  id?: number
  kind: NotificationChannelKind
  name: string
  url: string
  events: string[]
  enabled: boolean
  config: NotificationChannelConfig & { toText: string }
}

function emptyDraft(kind: NotificationChannelKind): Draft {
  return {
    kind,
    name: KINDS.find((item) => item.kind === kind)?.title ?? "Deployment events",
    url: "",
    events: DEFAULT_EVENTS,
    enabled: true,
    config: { smtpSecurity: "starttls", toText: "" },
  }
}

function eventLabel(events: string[]) {
  if (events.length === 0) return "Every event"
  if (events.length === 1 && events[0] === "run.finished") return "Every outcome"
  return EVENTS.filter((item) => events.includes(item.event))
    .map((item) => item.label)
    .concat(events.includes("run.finished") ? ["Every outcome"] : [])
    .join(", ")
}

export function NotificationsPage() {
  const { can } = useAuth()
  const admin = can("system.admin")
  const channels = usePoll(
    (signal) => get<NotificationChannel[]>("/deploy/notifications", undefined, signal),
    10000,
  )
  const [draft, setDraft] = useState<Draft>()
  // Discord/Slack's webhook field starts hidden behind "Replace delivery
  // settings" on Edit; email/telegram show theirs regardless (ALWAYS_OPEN).
  const [revealDelivery, setRevealDelivery] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState("")
  const [secret, setSecret] = useState<string>()
  const [testingId, setTestingId] = useState<number>()
  const [history, setHistory] = useState<NotificationChannel>()
  const { confirm, dialog } = useConfirm()

  const save = async () => {
    if (!draft) return
    setSaving(true)
    setError("")
    const to = draft.config.toText
      .split(/[\n,]/)
      .map((part) => part.trim())
      .filter(Boolean)
    const body = {
      name: draft.name.trim(),
      kind: draft.kind,
      url: draft.kind === "webhook" ? draft.url.trim() : "",
      events: draft.events,
      enabled: draft.enabled,
      config: {
        webhookUrl: draft.config.webhookUrl?.trim() || undefined,
        botToken: draft.config.botToken?.trim() || undefined,
        chatId: draft.config.chatId?.trim() || undefined,
        smtpHost: draft.config.smtpHost?.trim() || undefined,
        smtpPort: draft.config.smtpPort || undefined,
        smtpUsername: draft.config.smtpUsername?.trim() || undefined,
        smtpPassword: draft.config.smtpPassword || undefined,
        smtpSecurity: draft.kind === "email" ? draft.config.smtpSecurity : undefined,
        from: draft.config.from?.trim() || undefined,
        // Never an empty array: the update route requires 1-20 recipients on
        // every write (no blank-keeps-current fallback the way the password
        // and webhook URL have), so a rename-only edit that never touched
        // this field must omit it rather than send one that reads as "clear
        // the list".
        to: draft.kind === "email" && to.length > 0 ? to : undefined,
      },
    }
    try {
      if (draft.id) {
        await put(`/deploy/notifications/${draft.id}`, body)
        notify.success("Notification channel saved")
      } else {
        const result = await post<{ channel: NotificationChannel; secret: string }>(
          "/deploy/notifications",
          body,
        )
        if (result.secret) setSecret(result.secret)
        notify.success("Notification channel created")
      }
      setDraft(undefined)
      channels.refresh()
    } catch (caught) {
      setError(errorMessage(caught))
    } finally {
      setSaving(false)
    }
  }

  const toggle = async (channel: NotificationChannel, next: boolean) => {
    try {
      await put(`/deploy/notifications/${channel.id}/enabled`, { enabled: next })
      channels.refresh()
    } catch (caught) {
      notify.error("Could not update the channel", caught)
    }
  }

  const test = async (channel: NotificationChannel) => {
    setTestingId(channel.id)
    try {
      await post(`/deploy/notifications/${channel.id}/test`, {})
      notify.success(`Test message sent to ${channel.name}`)
    } catch (caught) {
      notify.error("Test delivery failed", caught)
    } finally {
      setTestingId(undefined)
    }
  }

  const remove = (channel: NotificationChannel) =>
    confirm({
      title: `Remove ${channel.name}`,
      confirmLabel: "Remove channel",
      description: (
        <p>
          Deployment events stop reaching {channel.target || channel.url}. Delivery history for this
          channel is removed with it.
        </p>
      ),
      action: async () => {
        await del(`/deploy/notifications/${channel.id}`)
      },
      onDone: () => channels.refresh(),
    })

  const edit = (channel: NotificationChannel) => {
    setRevealDelivery(false)
    setDraft({
      id: channel.id,
      kind: channel.kind,
      name: channel.name,
      url: channel.kind === "webhook" ? channel.url : "",
      events: channel.events,
      enabled: channel.enabled,
      config: { smtpSecurity: "starttls", toText: "" },
    })
  }

  return (
    <Page>
      <PageHeader
        eyebrow={
          <Link href="/deploy" className="rounded-sm focus-ring hover:underline">
            Deployments
          </Link>
        }
        title="Notifications"
        actions={
          admin && (
            <Button size="sm" onClick={() => setDraft(emptyDraft("discord"))}>
              <Plus className="size-3.5" />
              Add channel
            </Button>
          )
        }
      />

      <Notice title="Channels are shared across every project">
        Channels announce every deployment outcome across all projects and link back to the run.
      </Notice>

      {secret && (
        <Well className="space-y-2">
          <p className="text-body font-medium">Signing secret — shown once</p>
          <p className="font-mono text-xs break-all select-all">{secret}</p>
          <p className="text-hint text-muted-foreground">
            Verify <span className="font-mono">X-JD-Signature-256</span> as HMAC-SHA256 of the raw
            body with this secret.
          </p>
          <Button size="xs" variant="ghost" onClick={() => setSecret(undefined)}>
            Dismiss
          </Button>
        </Well>
      )}

      <Panel plain>
        {channels.error ? (
          <ErrorState error={channels.error} onRetry={channels.refresh} />
        ) : channels.loading && !channels.data ? (
          <LoadingPanel rows={3} />
        ) : (channels.data?.length ?? 0) === 0 ? (
          <EmptyState
            icon={Bell}
            title="No notification channels"
            description="Add Discord, Slack, Telegram, e-mail or a signed webhook to hear about deployments as they finish."
          />
        ) : (
          <RowList aria-label="Notification channels" className="animate-rise">
            {channels.data?.map((channel) => {
              const verbs: Verb[] = []
              if (admin) {
                verbs.push({
                  key: "test",
                  label: "Send test",
                  detail: "Deliver a sample message to this channel right now.",
                  icon: Lightning,
                  inline: true,
                  disabled: testingId === channel.id,
                  run: () => void test(channel),
                })
              }
              if (admin) {
                verbs.push({
                  key: "edit",
                  label: "Edit channel",
                  detail: "Change its name, credentials or events.",
                  icon: Pencil,
                  run: () => edit(channel),
                })
                verbs.push({
                  key: "toggle",
                  label: channel.enabled ? "Pause channel" : "Resume channel",
                  detail: channel.enabled
                    ? "Stop delivering messages without losing its history."
                    : "Start delivering messages again.",
                  icon: channel.enabled ? Pause : Play,
                  run: () => void toggle(channel, !channel.enabled),
                })
              }
              verbs.push({
                key: "history",
                label: "Delivery history",
                detail: "The last fifty attempts to reach this channel.",
                icon: ClockRewind,
                run: () => setHistory(channel),
              })
              if (admin) {
                verbs.push({
                  key: "remove",
                  label: "Remove channel",
                  detail: "Deployment events stop reaching it immediately.",
                  icon: Trash,
                  danger: true,
                  run: () => remove(channel),
                })
              }
              return (
                <Row
                  key={channel.id}
                  title={
                    <span className="inline-flex min-w-0 items-center gap-2">
                      <span className="truncate">{channel.name}</span>
                      <Tag>
                        {KINDS.find((item) => item.kind === channel.kind)?.title ?? channel.kind}
                      </Tag>
                    </span>
                  }
                  subtitle={
                    <span className="min-w-0 truncate">
                      <span className="font-mono">{channel.target || channel.url}</span>
                      {" · "}
                      {eventLabel(channel.events)}
                    </span>
                  }
                  trailing={
                    <>
                      <Status
                        tone={channel.enabled ? "running" : "stopped"}
                        label={channel.enabled ? "Enabled" : "Paused"}
                      />
                      <VerbActions verbs={verbs} menuLabel={`Actions for ${channel.name}`} />
                    </>
                  }
                />
              )
            })}
          </RowList>
        )}
      </Panel>

      <SidePanel
        open={Boolean(draft)}
        onOpenChange={(open) => {
          if (!open) {
            setDraft(undefined)
            setError("")
          }
        }}
        title={draft?.id ? "Edit notification channel" : "Add notification channel"}
        description="Choose where deployment events are delivered."
        width="md"
        footer={
          <Button pending={saving} disabled={!draft || !draft.name.trim()} onClick={save}>
            {draft?.id ? "Save channel" : "Create channel"}
          </Button>
        }
      >
        {draft && (
          <div className="space-y-5" aria-busy={saving}>
            {!draft.id && (
              <Field
                label="Deliver to"
                htmlFor="notification-kind"
                hint={KINDS.find((item) => item.kind === draft.kind)?.hint}
              >
                <Select
                  value={draft.kind}
                  onValueChange={(kind: NotificationChannelKind) =>
                    setDraft({ ...emptyDraft(kind), events: draft.events })
                  }
                >
                  <SelectTrigger id="notification-kind" className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {KINDS.map((item) => (
                      <SelectItem key={item.kind} value={item.kind}>
                        {item.title}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
            )}

            <Field label="Name" htmlFor="notification-name">
              <Input
                id="notification-name"
                value={draft.name}
                onChange={(event) => setDraft({ ...draft, name: event.target.value })}
              />
            </Field>

            {/* Editing never reads the stored credential back — the list
                model has no `config` at all — so a blank field here is not
                "unchanged", it is what gets written. Discord/Slack's
                webhook URL is kept when left blank; the rest are not, so
                only that one hides behind a disclosure. */}
            {!draft.id || ALWAYS_OPEN.includes(draft.kind) || revealDelivery ? (
              <>
                {draft.id && REENTRY_KINDS.includes(draft.kind) && (
                  <FormNote>
                    Enter the delivery settings again — the dashboard does not read them back.
                  </FormNote>
                )}
                <ChannelFields draft={draft} onChange={setDraft} />
              </>
            ) : (
              <button
                type="button"
                onClick={() => setRevealDelivery(true)}
                className="w-fit rounded-sm text-hint text-muted-foreground underline underline-offset-2 focus-ring hover:text-foreground"
              >
                Replace delivery settings
              </button>
            )}

            <fieldset className="space-y-1.5">
              <legend className="eyebrow mb-1">Events</legend>
              <OptionList>
                {EVENTS.map((item) => (
                  <OptionRow
                    key={item.event}
                    title={item.label}
                    hint={item.hint}
                    checked={draft.events.includes(item.event) || draft.events.length === 0}
                    onCheckedChange={(checked) => {
                      const current =
                        draft.events.length === 0 ? EVENTS.map((e) => e.event) : draft.events
                      setDraft({
                        ...draft,
                        events: checked
                          ? [...new Set([...current, item.event])]
                          : current.filter((event) => event !== item.event),
                      })
                    }}
                  />
                ))}
              </OptionList>
            </fieldset>

            <OptionRow
              title="Enabled"
              hint="Turn this off to stop deliveries without losing its saved credentials."
              checked={draft.enabled}
              onCheckedChange={(enabled) => setDraft({ ...draft, enabled })}
            />

            {error && (
              <FormNote tone="danger" role="alert">
                {error}
              </FormNote>
            )}
          </div>
        )}
      </SidePanel>

      <DeliveryHistory channel={history} onClose={() => setHistory(undefined)} />
      {dialog}
    </Page>
  )
}

function ChannelFields({ draft, onChange }: { draft: Draft; onChange: (draft: Draft) => void }) {
  const config = (patch: Partial<Draft["config"]>) =>
    onChange({ ...draft, config: { ...draft.config, ...patch } })
  const keepHint = draft.id ? "Leave blank to keep the stored value." : undefined
  switch (draft.kind) {
    case "webhook":
      return (
        <Field
          label="HTTPS endpoint"
          htmlFor="notification-url"
          hint="Receives signed JSON with the project, environment, run number, outcome, reason and a link to the run."
        >
          <Input
            id="notification-url"
            type="url"
            placeholder="https://hooks.example.com/deploy"
            value={draft.url}
            onChange={(event) => onChange({ ...draft, url: event.target.value })}
          />
        </Field>
      )
    case "discord":
    case "slack":
      return (
        <Field
          label={draft.kind === "discord" ? "Discord webhook URL" : "Slack incoming webhook URL"}
          htmlFor="notification-webhook"
          hint={`The URL is stored encrypted and shown masked afterwards.${keepHint ? ` ${keepHint}` : ""}`}
        >
          <Input
            id="notification-webhook"
            type="url"
            placeholder={
              draft.kind === "discord"
                ? "https://discord.com/api/webhooks/…"
                : "https://hooks.slack.com/services/…"
            }
            value={draft.config.webhookUrl ?? ""}
            onChange={(event) => config({ webhookUrl: event.target.value })}
            autoComplete="off"
          />
        </Field>
      )
    case "telegram":
      return (
        <>
          <Field
            label="Bot token"
            htmlFor="notification-bot-token"
            hint={`From @BotFather. Add the bot to the chat first.${keepHint ? ` ${keepHint}` : ""}`}
          >
            <Input
              id="notification-bot-token"
              type="password"
              placeholder="123456789:AA…"
              value={draft.config.botToken ?? ""}
              onChange={(event) => config({ botToken: event.target.value })}
              autoComplete="off"
            />
          </Field>
          <Field label="Chat id" htmlFor="notification-chat-id">
            <Input
              id="notification-chat-id"
              placeholder="-1001234567890 or @channel"
              value={draft.config.chatId ?? ""}
              onChange={(event) => config({ chatId: event.target.value })}
            />
          </Field>
        </>
      )
    case "email":
      return (
        <>
          <FieldRow>
            <Field label="SMTP host" htmlFor="notification-smtp-host">
              <Input
                id="notification-smtp-host"
                placeholder="smtp.example.com"
                value={draft.config.smtpHost ?? ""}
                onChange={(event) => config({ smtpHost: event.target.value })}
              />
            </Field>
            <Field label="Port" htmlFor="notification-smtp-port">
              <Input
                id="notification-smtp-port"
                type="number"
                min={1}
                max={65535}
                placeholder={
                  draft.config.smtpSecurity === "tls"
                    ? "465"
                    : draft.config.smtpSecurity === "none"
                      ? "25"
                      : "587"
                }
                value={draft.config.smtpPort ?? ""}
                onChange={(event) =>
                  config({ smtpPort: event.target.value ? Number(event.target.value) : undefined })
                }
              />
            </Field>
          </FieldRow>
          <FieldRow>
            <Field label="Security" htmlFor="notification-smtp-security">
              <Select
                value={draft.config.smtpSecurity ?? "starttls"}
                onValueChange={(smtpSecurity: "starttls" | "tls" | "none") =>
                  config({ smtpSecurity })
                }
              >
                <SelectTrigger id="notification-smtp-security" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="starttls">STARTTLS (port 587)</SelectItem>
                  <SelectItem value="tls">TLS (port 465)</SelectItem>
                  <SelectItem value="none">None (unauthenticated relay only)</SelectItem>
                </SelectContent>
              </Select>
            </Field>
            <Field label="Username" htmlFor="notification-smtp-username">
              <Input
                id="notification-smtp-username"
                value={draft.config.smtpUsername ?? ""}
                onChange={(event) => config({ smtpUsername: event.target.value })}
                autoComplete="off"
              />
            </Field>
          </FieldRow>
          <Field label="Password" htmlFor="notification-smtp-password" hint={keepHint}>
            <Input
              id="notification-smtp-password"
              type="password"
              value={draft.config.smtpPassword ?? ""}
              onChange={(event) => config({ smtpPassword: event.target.value })}
              autoComplete="new-password"
            />
          </Field>
          <Field label="From" htmlFor="notification-from">
            <Input
              id="notification-from"
              placeholder="Deployments <deploys@example.com>"
              value={draft.config.from ?? ""}
              onChange={(event) => config({ from: event.target.value })}
            />
          </Field>
          <Field
            label="Recipients"
            htmlFor="notification-to"
            hint={`One address per line, up to twenty.${draft.id ? " Recipients replace the stored list when saved." : ""}`}
          >
            <Textarea
              id="notification-to"
              placeholder={"ops@example.com\nteam@example.com"}
              value={draft.config.toText}
              onChange={(event) => config({ toText: event.target.value })}
              className="min-h-20"
            />
          </Field>
        </>
      )
  }
}

function DeliveryHistory({
  channel,
  onClose,
}: {
  channel?: NotificationChannel
  onClose: () => void
}) {
  const deliveries = usePoll(
    (signal) =>
      get<NotificationDelivery[]>(
        `/deploy/notifications/${channel?.id}/deliveries`,
        undefined,
        signal,
      ),
    0,
    [channel?.id],
    { enabled: Boolean(channel) },
  )
  return (
    <SidePanel
      open={Boolean(channel)}
      onOpenChange={(open) => !open && onClose()}
      title={channel ? `Deliveries · ${channel.name}` : "Deliveries"}
      description="Recent delivery attempts for this channel."
      width="sm"
    >
      {deliveries.error ? (
        <ErrorState error={deliveries.error} />
      ) : deliveries.loading && !deliveries.data ? (
        <LoadingPanel rows={3} />
      ) : (deliveries.data?.length ?? 0) === 0 ? (
        <EmptyNote>No deliveries have been attempted yet.</EmptyNote>
      ) : (
        <RowList aria-label="Delivery attempts">
          {deliveries.data?.map((delivery) => (
            <Row
              key={delivery.id}
              title={`${delivery.event}${delivery.runId ? ` · run ${delivery.runId}` : ""}`}
              subtitle={`${relativeTime(delivery.createdAt)} · attempt ${delivery.attempt} · ${delivery.responseClass}`}
              trailing={
                <Status
                  tone={delivery.status === "delivered" ? "running" : "danger"}
                  label={delivery.status === "delivered" ? "Delivered" : "Failed"}
                />
              }
            />
          ))}
        </RowList>
      )}
    </SidePanel>
  )
}
