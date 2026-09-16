"use client"

import { useState } from "react"
import { Bell, Plus, Trash } from "@/components/icons"
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
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { EmptyState, ErrorState, LoadingPanel } from "@/components/state"
import { Status } from "@/components/status-dot"
import { SidePanel } from "@/components/side-panel"
import { Tag } from "@/components/tag"
import { useConfirm } from "@/components/confirm-dialog"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"

/**
 * Where a deployment outcome is announced.
 *
 * Every kind receives the same events; what differs is the credential each
 * needs and how the message is rendered. Credentials are written once and
 * never read back — the list shows a masked target, and editing an existing
 * channel leaves a blank credential field meaning "keep what is stored".
 */

const KINDS: {
  kind: NotificationChannelKind
  title: string
  hint: string
}[] = [
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

export function DeploymentNotifications({ enabled = true }: { enabled?: boolean }) {
  const { can } = useAuth()
  const admin = can("system.admin")
  const channels = usePoll(
    (signal) => get<NotificationChannel[]>("/deploy/notifications", undefined, signal),
    10000,
    [],
    { enabled },
  )
  const [draft, setDraft] = useState<Draft>()
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState("")
  const [secret, setSecret] = useState<string>()
  const [testing, setTesting] = useState<number>()
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
        to: draft.kind === "email" ? to : undefined,
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
    setTesting(channel.id)
    try {
      await post(`/deploy/notifications/${channel.id}/test`, {})
      notify.success(`Test message sent to ${channel.name}`)
    } catch (caught) {
      notify.error("Test delivery failed", caught)
    } finally {
      setTesting(undefined)
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

  const edit = (channel: NotificationChannel) =>
    setDraft({
      id: channel.id,
      kind: channel.kind,
      name: channel.name,
      url: channel.kind === "webhook" ? channel.url : "",
      events: channel.events,
      enabled: channel.enabled,
      config: { smtpSecurity: "starttls", toText: "" },
    })

  return (
    <Panel className="xl:col-span-2">
      <PanelHeader
        title="Notifications"
        actions={
          admin && (
            <Button size="xs" variant="outline" onClick={() => setDraft(emptyDraft("discord"))}>
              <Plus className="size-3" /> Add channel
            </Button>
          )
        }
      />
      <PanelBody className="space-y-3">
        <p className="text-xs leading-relaxed text-muted-foreground">
          Channels announce every deployment outcome across all projects: a failed release is
          reported through the same channel as a successful one, and messages link back to the run.
        </p>
        {secret && (
          <Well className="space-y-2">
            <p className="text-xs font-medium">Signing secret — shown once</p>
            <p className="font-mono text-xs break-all select-all">{secret}</p>
            <p className="text-xs text-muted-foreground">
              Verify <span className="font-mono">X-JD-Signature-256</span> as HMAC-SHA256 of the raw
              body with this secret.
            </p>
            <Button size="xs" variant="ghost" onClick={() => setSecret(undefined)}>
              Dismiss
            </Button>
          </Well>
        )}
        {channels.error ? (
          <ErrorState error={channels.error} />
        ) : channels.loading && !channels.data ? (
          <LoadingPanel rows={2} />
        ) : (channels.data?.length ?? 0) === 0 ? (
          <EmptyState
            icon={Bell}
            title="No notification channels"
            description="Add Discord, Slack, Telegram, e-mail or a signed webhook to hear about deployments as they finish."
            className="border-0 py-5"
          />
        ) : (
          <ul className="divide-y divide-hairline" aria-label="Notification channels">
            {channels.data?.map((channel) => (
              <li
                key={channel.id}
                className="flex min-w-0 flex-wrap items-center justify-between gap-3 py-3"
              >
                <div className="min-w-0 flex-1 space-y-1">
                  <div className="flex min-w-0 flex-wrap items-center gap-2">
                    <p className="text-body font-medium">{channel.name}</p>
                    <Tag>
                      {KINDS.find((item) => item.kind === channel.kind)?.title ?? channel.kind}
                    </Tag>
                  </div>
                  <p className="truncate font-mono text-xs text-muted-foreground">
                    {channel.target || channel.url}
                  </p>
                  <p className="text-xs text-muted-foreground">{eventLabel(channel.events)}</p>
                </div>
                <div className="flex flex-wrap items-center gap-2">
                  <Status
                    state={channel.enabled ? "enabled" : "stopped"}
                    label={channel.enabled ? "Enabled" : "Paused"}
                  />
                  {admin && (
                    <>
                      <Switch
                        size="sm"
                        checked={channel.enabled}
                        onCheckedChange={(next) => toggle(channel, next)}
                        aria-label={`${channel.enabled ? "Pause" : "Resume"} ${channel.name}`}
                      />
                      <Button
                        size="xs"
                        variant="outline"
                        disabled={testing === channel.id}
                        onClick={() => test(channel)}
                      >
                        {testing === channel.id ? "Sending…" : "Send test"}
                      </Button>
                      <Button size="xs" variant="ghost" onClick={() => edit(channel)}>
                        Edit
                      </Button>
                    </>
                  )}
                  <Button size="xs" variant="ghost" onClick={() => setHistory(channel)}>
                    History
                  </Button>
                  {admin && (
                    <Button
                      size="icon-xs"
                      variant="ghost"
                      className="text-destructive"
                      aria-label={`Remove ${channel.name}`}
                      onClick={() => remove(channel)}
                    >
                      <Trash className="size-3.5" />
                    </Button>
                  )}
                </div>
              </li>
            ))}
          </ul>
        )}
      </PanelBody>
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
              <div className="space-y-1.5">
                <Label htmlFor="notification-kind">Deliver to</Label>
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
                <p className="text-xs text-muted-foreground">
                  {KINDS.find((item) => item.kind === draft.kind)?.hint}
                </p>
              </div>
            )}
            <div className="space-y-1.5">
              <Label htmlFor="notification-name">Name</Label>
              <Input
                id="notification-name"
                value={draft.name}
                onChange={(event) => setDraft({ ...draft, name: event.target.value })}
              />
            </div>
            <ChannelFields draft={draft} onChange={setDraft} />
            <fieldset className="space-y-2">
              <legend className="text-sm font-medium">Events</legend>
              {EVENTS.map((item) => (
                <label key={item.event} className="flex min-h-9 items-start gap-2 text-sm">
                  <Checkbox
                    className="mt-0.5"
                    checked={draft.events.includes(item.event) || draft.events.length === 0}
                    onCheckedChange={(checked) => {
                      const current =
                        draft.events.length === 0 ? EVENTS.map((e) => e.event) : draft.events
                      setDraft({
                        ...draft,
                        events:
                          checked === true
                            ? [...new Set([...current, item.event])]
                            : current.filter((event) => event !== item.event),
                      })
                    }}
                  />
                  <span>
                    <span className="block">{item.label}</span>
                    <span className="block text-xs text-muted-foreground">{item.hint}</span>
                  </span>
                </label>
              ))}
            </fieldset>
            <label className="flex min-h-9 items-center gap-2 text-sm">
              <Switch
                checked={draft.enabled}
                onCheckedChange={(enabled) => setDraft({ ...draft, enabled })}
              />
              Enabled
            </label>
            {error && (
              <p role="alert" className="text-sm text-destructive">
                {error}
              </p>
            )}
          </div>
        )}
      </SidePanel>
      <DeliveryHistory channel={history} onClose={() => setHistory(undefined)} />
      {dialog}
    </Panel>
  )
}

function ChannelFields({ draft, onChange }: { draft: Draft; onChange: (draft: Draft) => void }) {
  const config = (patch: Partial<Draft["config"]>) =>
    onChange({ ...draft, config: { ...draft.config, ...patch } })
  const keepHint = draft.id ? "Leave blank to keep the stored value." : undefined
  switch (draft.kind) {
    case "webhook":
      return (
        <div className="space-y-1.5">
          <Label htmlFor="notification-url">HTTPS endpoint</Label>
          <Input
            id="notification-url"
            type="url"
            placeholder="https://hooks.example.com/deploy"
            value={draft.url}
            onChange={(event) => onChange({ ...draft, url: event.target.value })}
          />
          <p className="text-xs text-muted-foreground">
            Receives signed JSON with the project, environment, run number, outcome, reason and a
            link to the run.
          </p>
        </div>
      )
    case "discord":
    case "slack":
      return (
        <div className="space-y-1.5">
          <Label htmlFor="notification-webhook">
            {draft.kind === "discord" ? "Discord webhook URL" : "Slack incoming webhook URL"}
          </Label>
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
          <p className="text-xs text-muted-foreground">
            The URL is stored encrypted and shown masked afterwards. {keepHint}
          </p>
        </div>
      )
    case "telegram":
      return (
        <div className="grid gap-3 sm:grid-cols-2">
          <div className="space-y-1.5 sm:col-span-2">
            <Label htmlFor="notification-bot-token">Bot token</Label>
            <Input
              id="notification-bot-token"
              type="password"
              placeholder="123456789:AA…"
              value={draft.config.botToken ?? ""}
              onChange={(event) => config({ botToken: event.target.value })}
              autoComplete="off"
            />
            <p className="text-xs text-muted-foreground">
              From @BotFather. Add the bot to the chat first. {keepHint}
            </p>
          </div>
          <div className="space-y-1.5 sm:col-span-2">
            <Label htmlFor="notification-chat-id">Chat id</Label>
            <Input
              id="notification-chat-id"
              placeholder="-1001234567890 or @channel"
              value={draft.config.chatId ?? ""}
              onChange={(event) => config({ chatId: event.target.value })}
            />
          </div>
        </div>
      )
    case "email":
      return (
        <div className="grid gap-3 sm:grid-cols-2">
          <div className="space-y-1.5">
            <Label htmlFor="notification-smtp-host">SMTP host</Label>
            <Input
              id="notification-smtp-host"
              placeholder="smtp.example.com"
              value={draft.config.smtpHost ?? ""}
              onChange={(event) => config({ smtpHost: event.target.value })}
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="notification-smtp-port">Port</Label>
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
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="notification-smtp-security">Security</Label>
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
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="notification-smtp-username">Username</Label>
            <Input
              id="notification-smtp-username"
              value={draft.config.smtpUsername ?? ""}
              onChange={(event) => config({ smtpUsername: event.target.value })}
              autoComplete="off"
            />
          </div>
          <div className="space-y-1.5 sm:col-span-2">
            <Label htmlFor="notification-smtp-password">Password</Label>
            <Input
              id="notification-smtp-password"
              type="password"
              value={draft.config.smtpPassword ?? ""}
              onChange={(event) => config({ smtpPassword: event.target.value })}
              autoComplete="new-password"
            />
            {keepHint && <p className="text-xs text-muted-foreground">{keepHint}</p>}
          </div>
          <div className="space-y-1.5 sm:col-span-2">
            <Label htmlFor="notification-from">From</Label>
            <Input
              id="notification-from"
              placeholder="Deployments <deploys@example.com>"
              value={draft.config.from ?? ""}
              onChange={(event) => config({ from: event.target.value })}
            />
          </div>
          <div className="space-y-1.5 sm:col-span-2">
            <Label htmlFor="notification-to">Recipients</Label>
            <Textarea
              id="notification-to"
              placeholder={"ops@example.com\nteam@example.com"}
              value={draft.config.toText}
              onChange={(event) => config({ toText: event.target.value })}
              className="min-h-20"
            />
            <p className="text-xs text-muted-foreground">
              One address per line, up to twenty.
              {draft.id ? " Recipients replace the stored list when saved." : ""}
            </p>
          </div>
        </div>
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
        <p className="text-sm text-muted-foreground">No deliveries have been attempted yet.</p>
      ) : (
        <ul className="divide-y divide-hairline">
          {deliveries.data?.map((delivery) => (
            <li
              key={delivery.id}
              className="flex items-center justify-between gap-3 py-2.5 text-xs"
            >
              <span className="min-w-0">
                <span className="block font-medium">
                  {delivery.event}
                  {delivery.runId ? ` · run ${delivery.runId}` : ""}
                </span>
                <span className="text-muted-foreground">
                  {relativeTime(delivery.createdAt)} · attempt {delivery.attempt} ·{" "}
                  {delivery.responseClass}
                </span>
              </span>
              <Status
                verdict={delivery.status === "delivered" ? "ok" : "critical"}
                label={delivery.status === "delivered" ? "Delivered" : "Failed"}
              />
            </li>
          ))}
        </ul>
      )}
    </SidePanel>
  )
}
