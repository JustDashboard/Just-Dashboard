"use client"

import { useId, useRef, useState, type RefObject } from "react"
import { useMemoryState } from "@/lib/view-state"
import Link from "next/link"
import {
  Check,
  ClockRewind,
  CloudUpload,
  Copy,
  Envelope,
  Eye,
  EyeOff,
  PaperAirplane,
  Pause,
  Pencil,
  Play,
  Plus,
  Trash,
} from "@/components/icons"
import { del, errorMessage, get, post, put } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { calendarDate, duration, plural, relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import { notify } from "@/lib/toast"
import { useArrivals } from "@/hooks/use-arrivals"
import { useAuth } from "@/hooks/use-auth"
import { useMediaQuery } from "@/hooks/use-mobile"
import { usePoll } from "@/hooks/use-poll"
import type {
  DeploymentFleet,
  DeploymentSummary,
  NotificationChannel,
  NotificationChannelConfig,
  NotificationChannelKind,
  NotificationDelivery,
  NotificationEvent,
  NotificationOutcome,
} from "@/lib/types"
import { Page, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { EmptyNote, ErrorState, LoadingPanel } from "@/components/state"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Status, StatusDot } from "@/components/status-dot"
import { SidePanel } from "@/components/side-panel"
import { Tag } from "@/components/tag"
import { useConfirm } from "@/components/confirm-dialog"
import { ChoiceGrid, ProductCard } from "@/components/choice-card"
import { ChoiceList, ChoiceRow, GroupRule } from "@/components/flow"
import {
  Field,
  FieldCheck,
  FieldRow,
  FormFact,
  FormFacts,
  FormNote,
  FormSection,
  OptionList,
  OptionRow,
} from "@/components/form"
import { OutcomeStrip, type OutcomeTone } from "@/components/outcome-strip"
import {
  ProductGlyph,
  ProductGlyphs,
  ProductLogo,
  channelProduct,
  webhookProduct,
} from "@/components/product-logo"
import { InitialsMark } from "@/components/account/user-avatar"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
  InputGroupText,
  InputGroupToggle,
} from "@/components/ui/input-group"
import { NumberTicker } from "@/components/ui/number-ticker"
import { Skeleton } from "@/components/ui/skeleton"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { Textarea } from "@/components/ui/textarea"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { VerbActions, VerbBar, type Verb } from "@/components/verbs"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { ProjectMark } from "@/components/deploy/project-mark"
import { WireLink, WireMark, WireNode, WirePlaceholder } from "@/components/deploy/wire"
import { ChannelGlyph, channelMark, mailProduct } from "@/components/deploy/vocabulary"

/**
 * Where a deployment outcome is announced.
 *
 * Channels are global rather than per-project — one Discord room hears about
 * every release — which is why this is its own destination off the fleet
 * page rather than a tab nested under a single project.
 *
 * The page's one question is whether the messages are arriving, so it opens
 * on four readings of exactly that — how many channels and which services, how
 * many messages went out and how many were given up on in the last day, and
 * when the last one left — then the picture of where an outcome goes, whose
 * lines are that answer per channel, then the channels as cards drawn as the
 * service each posts to, each saying how its last message went and carrying
 * its last fourteen as a strip. A card opens the channel's own sheet: what it
 * is, and every attempt to reach it.
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

/**
 * The events a channel may choose. What a channel hears about is its
 * configuration, not a reading, so the words carry no status hue (§3): a red
 * "Failed" on a channel whose every message arrived read as a failure.
 */
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

/** What each delivered event announced, as words rather than its wire key. */
const EVENT_WORD: Record<string, string> = {
  "run.started": "Run started",
  "run.succeeded": "Release live",
  "run.failed": "Run failed",
  "run.cancelled": "Cancelled",
  "run.finished": "Run finished",
  test: "Test message",
  "traffic.firing": "Traffic alert fired",
  "traffic.recovered": "Traffic back to normal",
}

/** How the far end answered, as the dispatcher records it. */
const RESPONSE_WORD: Record<string, string> = {
  "2xx": "accepted",
  "4xx": "refused",
  "5xx": "server error",
  network: "unreachable",
  smtp: "mail server",
  sealed: "could not unseal its settings",
  interrupted: "interrupted",
}

// A delivery still pending has no outcome yet, so it is drawn as a run still
// going is: in the brand, not a status hue.
const STRIP_TONE: Record<string, OutcomeTone> = {
  delivered: "success",
  failed: "danger",
  pending: "running",
}

type Security = NonNullable<NotificationChannelConfig["smtpSecurity"]>

const SECURITY: { value: Security; label: string; port: number; hint: string }[] = [
  {
    value: "starttls",
    label: "STARTTLS",
    port: 587,
    hint: "Port 587: the connection is upgraded to TLS before signing in.",
  },
  { value: "tls", label: "TLS", port: 465, hint: "Port 465: TLS from the first byte." },
  {
    value: "none",
    label: "None",
    port: 25,
    hint: "Port 25: an unauthenticated relay on a network you trust. Nothing is encrypted.",
  },
]

// The server keeps three credentials when they are left blank on edit:
// Discord/Slack's webhook URL, Telegram's bot token and the SMTP password
// (notifications.go). It keeps nothing else: an e-mail channel's host, sender
// and recipients, and a Telegram chat id, are sent on every write, and a
// signed webhook's URL is never masked, so Edit resends it as read. Only the
// Discord/Slack URL is hidden until replaced, because it is the one the list
// shows, masked.
const ALWAYS_OPEN: NotificationChannelKind[] = ["webhook", "email", "telegram"]
const REENTRY_KINDS: NotificationChannelKind[] = ["email", "telegram"]

// The server's own shapes (notifications.go), so a form is refused here, with
// its reason lit beside the field, rather than by a 400 after the press.
const TELEGRAM_TOKEN = /^[0-9]{5,}:[A-Za-z0-9_-]{30,}$/
const TELEGRAM_CHAT = /^(-?[0-9]{1,20}|@[A-Za-z0-9_]{5,32})$/
const SMTP_HOST =
  /^([A-Za-z0-9]([A-Za-z0-9-]{0,62}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,62}[A-Za-z0-9])?)*|[0-9a-fA-F:.]+)$/

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

function kindTitle(kind: NotificationChannelKind) {
  return KINDS.find((item) => item.kind === kind)?.title ?? kind
}

/** The Discord or Slack address a pasted URL is, by the server's own rule. */
function webhookShape(url: string): "discord" | "slack" | undefined {
  let parsed: URL
  try {
    parsed = new URL(url.trim())
  } catch {
    return undefined
  }
  if (parsed.protocol !== "https:") return undefined
  const host = parsed.hostname.toLowerCase()
  const discord = ["discord.com", "discordapp.com"].some(
    (name) => host === name || host.endsWith(`.${name}`),
  )
  if (discord && parsed.pathname.startsWith("/api/webhooks/")) return "discord"
  if (host === "hooks.slack.com" && parsed.pathname.startsWith("/services/")) return "slack"
  return undefined
}

/** What a Telegram chat id is, by its shape. */
function chatKind(chat: string) {
  if (chat.startsWith("@")) return "a public channel"
  if (chat.startsWith("-100")) return "a supergroup or channel"
  if (chat.startsWith("-")) return "a group"
  return "a private chat"
}

/** The address in "Deployments <deploys@example.com>", or the line itself. */
function addressOf(line: string) {
  return (/<([^>]*)>\s*$/.exec(line)?.[1] ?? line).trim()
}

function isAddress(line: string) {
  return /^[^\s@<>]+@[^\s@<>]+$/.test(addressOf(line))
}

function recipientsOf(text: string) {
  return text
    .split(/[\n,]/)
    .map((part) => part.trim())
    .filter(Boolean)
}

/**
 * Whether the server would take the form. Every field it requires is checked
 * on every write; only the three it keeps when blank on edit (ALWAYS_OPEN's
 * note) may be left empty there.
 */
function ready(draft: Draft) {
  if (!draft.name.trim()) return false
  const kept = (value: string | undefined, valid: (value: string) => boolean) =>
    value?.trim() ? valid(value.trim()) : Boolean(draft.id)
  const required = (value: string | undefined, valid: (value: string) => boolean) =>
    Boolean(value?.trim()) && valid(value!.trim())
  const config = draft.config
  switch (draft.kind) {
    case "webhook":
      return /^https?:\/\/\S+$/.test(draft.url.trim())
    case "discord":
    case "slack":
      return kept(config.webhookUrl, (url) => webhookShape(url) === draft.kind)
    case "telegram":
      return (
        kept(config.botToken, (token) => TELEGRAM_TOKEN.test(token)) &&
        required(config.chatId, (chat) => TELEGRAM_CHAT.test(chat))
      )
    case "email": {
      const recipients = recipientsOf(config.toText)
      return (
        required(config.smtpHost, (host) => SMTP_HOST.test(host)) &&
        required(config.from, isAddress) &&
        recipients.length >= 1 &&
        recipients.length <= 20 &&
        recipients.every(isAddress)
      )
    }
  }
}

/** A delivery's time within the last day — the readings' window. */
function withinDay(iso: string) {
  return Date.now() - Date.parse(iso) < 86_400_000
}

function retryLabel(next: string) {
  const seconds = (Date.parse(next) - Date.now()) / 1000
  // To the minute: a retry is minutes away, and "9m 59s" reads as a countdown.
  return seconds > 45 ? `retrying in ${duration(Math.round(seconds / 60) * 60)}` : "retrying now"
}

/**
 * The newest attempt at each message in a log that lists attempts newest
 * first: a retried run event is one message with several attempts, and it
 * went out if its last attempt did. A test is a message of its own each time.
 */
function latestAttempts(log: NotificationDelivery[]) {
  const seen = new Set<string>()
  return log.filter((delivery) => {
    const key = delivery.runId ? `${delivery.runId}:${delivery.event}` : `test:${delivery.id}`
    if (seen.has(key)) return false
    seen.add(key)
    return true
  })
}

/**
 * Every channel's recent log, for the day's readings: one read per channel,
 * as a backup job's card reads its own runs. The list itself carries each
 * channel's newest attempt and last fourteen, which is what the cards and the
 * picture draw; only the counts over a day need the log behind them.
 */
function useDeliveryLogs(channels: NotificationChannel[] | undefined) {
  const ids = (channels ?? []).map((channel) => channel.id)
  return usePoll(
    async (signal) => {
      const logs = await Promise.all(
        ids.map((id) =>
          get<NotificationDelivery[]>(
            `/deploy/notifications/${id}/deliveries`,
            { limit: 200 },
            signal,
          ),
        ),
      )
      return new Map(ids.map((id, index) => [id, logs[index]]))
    },
    30000,
    [ids.join(",")],
    { enabled: ids.length > 0 },
  )
}

export function NotificationsPage() {
  const { can } = useAuth()
  const admin = can("system.admin")
  const channels = usePoll(
    (signal) => get<NotificationChannel[]>("/deploy/notifications", undefined, signal),
    10000,
  )
  const logs = useDeliveryLogs(channels.data)
  // The fleet is drawn as the source of every message: the projects
  // themselves. Read once — three marks and a count do not need a fleet read
  // a minute.
  const fleet = usePoll((signal) => get<DeploymentFleet>("/deploy/", { view: "fleet" }, signal), 0)
  // The open form is kept in memory for the tab — memory, because webhook
  // URLs and bot tokens are typed into it — until it is saved or closed.
  const [draft, setDraft] = useMemoryState<Draft | undefined>(
    "deploy.notifications.draft",
    undefined,
  )
  // Discord/Slack's stored address is shown masked on Edit until it is
  // replaced; e-mail and Telegram show their fields regardless (ALWAYS_OPEN).
  const [revealDelivery, setRevealDelivery] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState("")
  // A signed webhook's secret, shown once in the sheet that made it.
  const [issued, setIssued] = useState<{ name: string; secret: string }>()
  // Every channel with a test out: two tests to two channels are two flights,
  // and the first to land must not end the second one's.
  const [testing, setTesting] = useState<ReadonlySet<number>>(new Set())
  // A pause or resume in flight: the card says so until the list agrees.
  const [switching, setSwitching] = useState<{ id: number; enabled: boolean }>()
  const [historyId, setHistoryId] = useState<number>()
  const { confirm, dialog } = useConfirm()
  // On a phone the last delivery goes under the name, which otherwise had the
  // width the outcome and the verbs left it. Chosen once, so it is drawn once.
  const wide = useMediaQuery("(min-width: 640px)")

  const list = channels.data ?? []
  // The open sheet's log, held here so a test or a pause sent from the sheet
  // re-reads it with the list: the sheet's header and its rows then agree.
  const deliveries = usePoll(
    (signal) =>
      get<NotificationDelivery[]>(
        `/deploy/notifications/${historyId}/deliveries`,
        undefined,
        signal,
      ),
    15000,
    [historyId],
    { enabled: list.some((channel) => channel.id === historyId) },
  )
  // The switch has landed once the list agrees with it (or the channel is
  // gone), whoever changed it — the edit sheet's own switch and another
  // operator's pause both arrive through the list.
  if (
    switching &&
    !list.some((channel) => channel.id === switching.id && channel.enabled !== switching.enabled)
  )
    setSwitching(undefined)
  const refresh = () => {
    channels.refresh()
    logs.refresh()
    deliveries.refresh()
  }

  const closeForm = () => {
    setDraft(undefined)
    setIssued(undefined)
    setError("")
  }

  const save = async () => {
    if (!draft) return
    setSaving(true)
    setError("")
    const to = recipientsOf(draft.config.toText)
    const signsIn = draft.config.smtpSecurity !== "none"
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
        // A relay with no TLS takes no sign-in: the server refuses a password
        // in clear text, so a username typed before switching is not sent.
        smtpUsername: (signsIn && draft.config.smtpUsername?.trim()) || undefined,
        smtpPassword: (signsIn && draft.config.smtpPassword) || undefined,
        smtpSecurity: draft.kind === "email" ? draft.config.smtpSecurity : undefined,
        from: draft.config.from?.trim() || undefined,
        to: draft.kind === "email" ? to : undefined,
      },
    }
    try {
      if (draft.id) {
        await put(`/deploy/notifications/${draft.id}`, body)
        notify.success("Notification channel saved")
        setDraft(undefined)
      } else {
        const result = await post<{ channel: NotificationChannel; secret: string }>(
          "/deploy/notifications",
          body,
        )
        notify.success("Notification channel created")
        // The sheet that made a signed webhook stays open on its secret: a
        // secret shown anywhere else is one read after the task that needed it.
        if (result.secret) setIssued({ name: result.channel.name, secret: result.secret })
        setDraft(undefined)
      }
      refresh()
    } catch (caught) {
      setError(errorMessage(caught))
    } finally {
      setSaving(false)
    }
  }

  const toggle = async (channel: NotificationChannel, enabled: boolean) => {
    setSwitching({ id: channel.id, enabled })
    try {
      await put(`/deploy/notifications/${channel.id}/enabled`, { enabled })
      refresh()
    } catch (caught) {
      setSwitching(undefined)
      notify.error("Could not update the channel", caught)
    }
  }

  const test = async (channel: NotificationChannel) => {
    setTesting((current) => new Set(current).add(channel.id))
    try {
      await post(`/deploy/notifications/${channel.id}/test`, {})
      notify.success(`Test message sent to ${channel.name}`)
    } catch (caught) {
      notify.error("Test delivery failed", caught)
    } finally {
      setTesting((current) => {
        const next = new Set(current)
        next.delete(channel.id)
        return next
      })
      // The attempt is in the log either way: a new square on the strip, and
      // the line to its mark re-toned.
      refresh()
    }
  }

  const remove = (channel: NotificationChannel) =>
    confirm({
      title: `Remove ${channel.name}`,
      confirmLabel: "Remove channel",
      subject: {
        mark: <ChannelTile channel={channel} />,
        name: channel.name,
        facts: (
          <>
            <FormFact label="Delivers to">{kindTitle(channel.kind)}</FormFact>
            {channel.lastDelivery && (
              <FormFact label="Last message">
                {relativeTime(channel.lastDelivery.createdAt)}
              </FormFact>
            )}
          </>
        ),
      },
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
    setError("")
    setHistoryId(undefined)
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

  const add = (kind: NotificationChannelKind) => {
    setError("")
    setDraft(emptyDraft(kind))
  }

  // Declared once, and drawn by the card (Send test inline, the rest in its
  // menu) and by the channel's sheet (named buttons), so the two agree.
  const verbsFor = (channel: NotificationChannel, where: "card" | "sheet"): Verb[] => {
    const verbs: Verb[] = []
    if (admin) {
      verbs.push({
        key: "test",
        label: "Send test",
        // The server refuses every delivery to a paused channel, so the verb
        // is not offered as though it might work.
        icon: PaperAirplane,
        inline: true,
        progressive: "Sending test…",
        disabled: !channel.enabled || testing.has(channel.id),
        run: () => void test(channel),
      })
      verbs.push({
        key: "edit",
        label: "Edit channel",
        icon: Pencil,
        run: () => edit(channel),
      })
      verbs.push({
        key: "toggle",
        label: channel.enabled ? "Pause channel" : "Resume channel",
        icon: channel.enabled ? Pause : Play,
        progressive: channel.enabled ? "Pausing…" : "Resuming…",
        run: () => void toggle(channel, !channel.enabled),
      })
    }
    if (where === "card") {
      verbs.push({
        key: "history",
        label: "Delivery history",
        icon: ClockRewind,
        run: () => setHistoryId(channel.id),
      })
    }
    if (admin) {
      verbs.push({
        key: "remove",
        label: "Remove channel",
        icon: Trash,
        danger: true,
        run: () => remove(channel),
      })
    }
    return verbs
  }

  // What a channel is doing right now, in its verb's own participle (§13: the
  // word is declared once, on the verb).
  const working = (channel: NotificationChannel, verbs: Verb[]) => {
    const participle = (key: string) => verbs.find((verb) => verb.key === key)?.progressive
    return {
      sending: testing.has(channel.id) ? participle("test") : undefined,
      switching: switching?.id === channel.id ? participle("toggle") : undefined,
    }
  }

  // A channel that has given up on its last message first, then one still
  // retrying, then the rest as the server lists them.
  const rank = (channel: NotificationChannel) =>
    channel.enabled && channel.lastDelivery?.status === "failed"
      ? channel.lastDelivery.nextAttemptAt
        ? 1
        : 0
      : 2
  const ordered = [...list].sort((a, b) => rank(a) - rank(b))
  const paused = list.filter((channel) => !channel.enabled).length
  const history = list.find((channel) => channel.id === historyId)
  const historyVerbs = history ? verbsFor(history, "sheet") : []
  const editing = draft?.id ? list.find((channel) => channel.id === draft.id) : undefined

  return (
    <Page>
      <PageContext
        eyebrow={
          <Link href="/deploy" className="rounded-sm focus-ring hover:underline">
            Deployments
          </Link>
        }
        title="Notifications"
      />

      {channels.error ? (
        <ErrorState error={channels.error} onRetry={channels.refresh} />
      ) : channels.loading && !channels.data ? (
        <LoadingPanel plain rows={3} />
      ) : (
        <>
          {list.length > 0 && <ChannelReadings channels={list} logs={logs} />}

          {/* Framed, like the GitHub App's picture on Credentials: a drawing
              of one thing and the places it goes needs an edge to read as one. */}
          <div className="animate-rise overflow-hidden rounded-xl border bg-card px-5 py-6 sm:px-6 sm:py-7 lg:px-8 lg:py-9">
            <Fanout
              channels={list}
              fleet={fleet.data?.deployments ?? []}
              onAdd={admin ? add : undefined}
            />
          </div>

          <Panel plain>
            <PanelHeader
              title="Channels"
              actions={
                <span className="flex flex-wrap items-center gap-3">
                  {list.length > 0 && (
                    <span className="numeric text-hint text-muted-foreground">
                      {plural(list.length, "channel")}
                      {paused > 0 && ` · ${paused} paused`}
                    </span>
                  )}
                  {admin && (
                    <Button size="sm" onClick={() => add("discord")}>
                      <Plus className="size-3.5" />
                      Add channel
                    </Button>
                  )}
                </span>
              }
            />
            <PanelBody>
              {list.length === 0 ? (
                // The picture above already offers the five kinds; the list
                // only has to say it is empty.
                <EmptyNote>No notification channels</EmptyNote>
              ) : (
                <ChoiceList aria-label="Notification channels" className="animate-rise">
                  {ordered.map((channel) => {
                    const verbs = verbsFor(channel, "card")
                    return (
                      <ChannelCard
                        key={channel.id}
                        channel={channel}
                        verbs={verbs}
                        {...working(channel, verbs)}
                        wide={wide}
                        onOpen={() => setHistoryId(channel.id)}
                      />
                    )
                  })}
                </ChoiceList>
              )}
            </PanelBody>
          </Panel>
        </>
      )}

      <SidePanel
        open={Boolean(draft) || Boolean(issued)}
        onOpenChange={(open) => !open && closeForm()}
        title={issued ? `${issued.name} is ready` : draft?.id ? "Edit channel" : "Add channel"}
        description="Choose where deployment events are delivered."
        width="md"
        footer={
          issued ? (
            <Button onClick={closeForm}>Done</Button>
          ) : (
            <>
              <Button variant="outline" onClick={closeForm} disabled={saving}>
                Cancel
              </Button>
              <Button pending={saving} disabled={!draft || !ready(draft)} onClick={save}>
                {draft?.id ? "Save channel" : "Add channel"}
              </Button>
            </>
          )
        }
      >
        {issued ? (
          <SigningSecret secret={issued.secret} />
        ) : (
          draft && (
            <div className="space-y-6" aria-busy={saving}>
              {editing ? (
                <div className="flex min-w-0 items-center gap-3">
                  <ChannelTile channel={editing} />
                  <div className="min-w-0 space-y-0.5">
                    <p className="truncate text-body font-medium">{editing.name}</p>
                    <FormFacts>
                      <FormFact label="Delivers to">{kindTitle(editing.kind)}</FormFact>
                      <FormFact label="Added">{calendarDate(editing.createdAt)}</FormFact>
                    </FormFacts>
                  </div>
                </div>
              ) : draft.id ? (
                // A draft kept from an earlier visit, before the list is back:
                // the kind is the draft's own.
                <FormFacts>
                  <FormFact label="Delivers to">{kindTitle(draft.kind)}</FormFact>
                </FormFacts>
              ) : (
                <KindPicker
                  value={draft.kind}
                  // Switching kind keeps the events and starts the kind's
                  // own fields afresh.
                  onChange={(kind) => setDraft({ ...emptyDraft(kind), events: draft.events })}
                />
              )}

              <Field label="Name" htmlFor="notification-name">
                <Input
                  id="notification-name"
                  value={draft.name}
                  onChange={(event) => setDraft({ ...draft, name: event.target.value })}
                />
              </Field>

              {draft.id && REENTRY_KINDS.includes(draft.kind) && (
                <FormNote tone="warning">
                  Enter the delivery settings again — the dashboard does not read them back.
                </FormNote>
              )}
              <ChannelFields
                draft={draft}
                onChange={setDraft}
                stored={editing?.target}
                // A URL already typed stays in view: the draft outlives the
                // page, and the Replace press that opened it does not.
                open={
                  !draft.id ||
                  ALWAYS_OPEN.includes(draft.kind) ||
                  revealDelivery ||
                  Boolean(draft.config.webhookUrl)
                }
                onReplace={() => setRevealDelivery(true)}
              />

              <FormSection title="Events">
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
                <FormNote>
                  Traffic alerts reach this channel through the alert rules that name it, whatever
                  is chosen here.
                </FormNote>
              </FormSection>

              <FormSection title="Delivery">
                <OptionRow
                  title="Deliver as soon as it is saved"
                  hint="Off keeps the channel, its settings and its history, and sends nothing until it is resumed."
                  checked={draft.enabled}
                  onCheckedChange={(enabled) => setDraft({ ...draft, enabled })}
                />
              </FormSection>

              {error && (
                <FormNote tone="danger" role="alert">
                  {error}
                </FormNote>
              )}
            </div>
          )
        )}
      </SidePanel>

      <ChannelSheet
        channel={history}
        deliveries={deliveries}
        verbs={historyVerbs}
        {...(history ? working(history, historyVerbs) : {})}
        onClose={() => setHistoryId(undefined)}
      />
      {dialog}
    </Page>
  )
}

/**
 * Four readings, each part of "are my messages arriving": how many channels
 * there are and which services they post to, how many messages went out and
 * how many were given up on over the last day, and when the last one left.
 * A message retried until it went out counts once, as delivered.
 */
function ChannelReadings({
  channels,
  logs,
}: {
  channels: NotificationChannel[]
  logs: ReturnType<typeof useDeliveryLogs>
}) {
  const paused = channels.filter((channel) => !channel.enabled).length
  const products = [...new Set(channels.flatMap((channel) => channelMark(channel) ?? []))]
  const messages = logs.data
    ? channels.flatMap((channel) =>
        latestAttempts(logs.data?.get(channel.id) ?? []).map((delivery) => ({ channel, delivery })),
      )
    : undefined
  const newest = <T extends { delivery: { createdAt: string } }>(items: T[]) =>
    [...items].sort((a, b) => b.delivery.createdAt.localeCompare(a.delivery.createdAt))[0]
  const delivered = messages?.filter((m) => m.delivery.status === "delivered")
  const givenUp = messages?.filter(
    (m) => m.delivery.status === "failed" && !m.delivery.nextAttemptAt,
  )
  const retrying = messages?.filter(
    (m) => m.delivery.status === "failed" && m.delivery.nextAttemptAt,
  )
  const deliveredToday = delivered?.filter((m) => withinDay(m.delivery.createdAt))
  const failedToday = givenUp?.filter((m) => withinDay(m.delivery.createdAt))
  const lastDelivered = delivered && newest(delivered)
  const lastFailed = failedToday && newest(failedToday)
  const last = newest(
    channels.flatMap((channel) =>
      channel.lastDelivery ? [{ channel, delivery: channel.lastDelivery }] : [],
    ),
  )

  // A count arrives by counting: the figure rises with its tile.
  const figure = (value: number | undefined) =>
    value !== undefined ? (
      <NumberTicker value={value} />
    ) : logs.error ? (
      "—"
    ) : (
      <Skeleton className="h-6 w-8" />
    )
  const settled = messages ? "animate-rise" : undefined
  const key = (name: string) => (messages ? name : `${name}-loading`)
  const unread = logs.error ? "delivery log unavailable" : undefined

  return (
    <StatGrid columns={4} dense className="animate-rise">
      <StatTile
        label="Channels"
        value={<NumberTicker value={channels.length} />}
        hint={
          <span className="inline-flex max-w-full min-w-0 items-center gap-2">
            <span className="truncate">{paused > 0 ? `${paused} paused` : "none paused"}</span>
            <ProductGlyphs ids={products} />
          </span>
        }
      />
      <StatTile
        key={key("delivered")}
        label="Delivered · 24h"
        value={figure(deliveredToday?.length)}
        hint={
          unread ??
          (messages &&
            (lastDelivered
              ? `last to ${lastDelivered.channel.name} ${relativeTime(lastDelivered.delivery.createdAt)}`
              : "nothing delivered yet"))
        }
        className={settled}
      />
      <StatTile
        key={key("failed")}
        label="Failed · 24h"
        value={figure(failedToday?.length)}
        tone={failedToday && failedToday.length > 0 ? "danger" : "default"}
        hint={
          unread ??
          (messages &&
            [
              lastFailed
                ? `${lastFailed.channel.name} · ${
                    RESPONSE_WORD[lastFailed.delivery.responseClass] ??
                    lastFailed.delivery.responseClass
                  } ${relativeTime(lastFailed.delivery.createdAt)}`
                : "none failed",
              retrying && retrying.length > 0 && `${retrying.length} retrying`,
            ]
              .filter(Boolean)
              .join(" · "))
        }
        className={settled}
      />
      <StatTile
        label="Last message"
        value={last ? relativeTime(last.delivery.createdAt) : "—"}
        hint={
          last
            ? `${EVENT_WORD[last.delivery.event] ?? last.delivery.event} to ${last.channel.name}`
            : "nothing sent yet"
        }
      />
    </StatGrid>
  )
}

/** A channel's mark on a tile: its service, or the envelope, dimmed while paused. */
function ChannelTile({
  channel,
  size = "sm",
}: {
  channel: NotificationChannel
  size?: "sm" | "md"
}) {
  const product = channelMark(channel)
  return (
    <span className="relative flex shrink-0">
      <ProductLogo
        id={product}
        size={size}
        fallback={Envelope}
        className={cn(!channel.enabled && "opacity-50 grayscale")}
      />
      {/* A mail service is where the message is sent from; the envelope in
          its corner says it arrives as mail. */}
      {channel.kind === "email" && product && (
        <span
          aria-hidden
          className="absolute -right-1 -bottom-1 flex size-4 items-center justify-center rounded-sm border border-hairline bg-background"
        >
          <Envelope className="size-2.5 text-muted-foreground" />
        </span>
      )}
    </span>
  )
}

/** What a line in the picture says about its channel (§2: the line is the state). */
function lineOf(channel: NotificationChannel | undefined) {
  if (!channel) return { still: true, dashed: true } as const
  const last = channel.lastDelivery
  if (!channel.enabled || !last) return { still: true } as const
  if (last.status === "delivered") return { still: false } as const
  if (last.status === "failed" && !last.nextAttemptAt)
    return { still: true, tone: "danger" } as const
  return { still: true } as const
}

/** How the channel's last message went, in words, for a line of the picture. */
function outcomeWords(channel: NotificationChannel) {
  const last = channel.lastDelivery
  if (!last) return "never sent"
  if (last.status === "pending") return "sending"
  if (last.status === "delivered") return `delivered ${relativeTime(last.createdAt)}`
  if (last.nextAttemptAt) return "retrying"
  return `failed ${relativeTime(last.createdAt)}`
}

/**
 * Where an outcome goes: every deployment — the projects themselves — on one
 * side, and a mark for each channel it reaches on the other, each the service
 * it posts to. The line to a channel pulses while its last message arrived,
 * stands still and red once one was given up on, still while it is paused,
 * retrying or has never sent anything; the kinds not yet set up are their
 * services faded in dashed rings — pressed to add one, for an administrator —
 * so the empty page is the same picture with nothing wired yet.
 *
 * Wide, the source and the channels balance around the lines in the middle of
 * the frame. On a phone the list below already names every channel, so the
 * picture shrinks to what it adds: the source over a row of marks and the
 * lines fanning down to them.
 */
function Fanout({
  channels,
  fleet,
  onAdd,
}: {
  channels: NotificationChannel[]
  fleet: DeploymentSummary[]
  onAdd?: (kind: NotificationChannelKind) => void
}) {
  const wide = useMediaQuery("(min-width: 1024px)")
  const container = useRef<HTMLDivElement>(null)
  const source = useRef<HTMLDivElement>(null)
  const missing = KINDS.filter((item) => !channels.some((channel) => channel.kind === item.kind))
  const targets: { key: string; kind: NotificationChannelKind; channel?: NotificationChannel }[] = [
    ...channels.map((channel) => ({ key: `channel-${channel.id}`, channel, kind: channel.kind })),
    ...missing.map((item) => ({ key: `add-${item.kind}`, kind: item.kind })),
  ]
  // Every channel: the grey mark and its still line say which are paused, and
  // the tile and the list's header already count them.
  const hint =
    channels.length === 0 ? "reaching nobody yet" : `reaching ${plural(channels.length, "channel")}`
  const shown = fleet.slice(0, 3)
  const projects = shown.length > 0 && (
    <span
      aria-hidden
      className={cn("mt-2 flex items-center", wide ? "justify-end" : "justify-center")}
    >
      {shown.map((deployment, index) => (
        <ProjectMark
          key={deployment.id}
          deployment={deployment}
          size="sm"
          className={cn(index > 0 && "-ml-3", "ring-2 ring-card")}
        />
      ))}
      {fleet.length > shown.length && (
        <span className="numeric ml-1.5 text-micro text-muted-foreground">
          +{fleet.length - shown.length}
        </span>
      )}
    </span>
  )
  const mark = (
    <WireMark tone="brand" shape="square">
      <CloudUpload />
    </WireMark>
  )
  const group = { role: "group", "aria-label": "Where deployment events go" } as const

  return (
    <div ref={container} className="relative mx-auto max-w-5xl">
      {wide ? (
        <div className="grid grid-cols-[minmax(0,1fr)_minmax(6rem,0.5fr)_minmax(0,1fr)] items-center">
          <WireNode
            nodeRef={source}
            align="end"
            mark={mark}
            eyebrow="Every deployment"
            title={
              <>
                All projects
                {projects}
              </>
            }
            hint={hint}
          />
          <div aria-hidden />
          {/* Not a list: the cards below are the channels' list, and these
              are a picture of the same channels. */}
          <div {...group} className="flex flex-col gap-4">
            {targets.map((target) => (
              <FanoutTarget
                key={target.key}
                containerRef={container}
                sourceRef={source}
                channel={target.channel}
                kind={target.kind}
                onAdd={onAdd}
              />
            ))}
          </div>
        </div>
      ) : (
        // The words over the mark, so the lines leave it downwards over
        // nothing but the frame's own ground.
        <div className="flex flex-col items-center text-center">
          <p className="eyebrow">Every deployment</p>
          <p className="text-body leading-snug font-medium">All projects</p>
          <p className="text-hint leading-snug text-muted-foreground">{hint}</p>
          {projects}
          <div ref={source} className="relative z-10 mt-4 flex">
            {mark}
          </div>
          <div {...group} className="mt-10 flex flex-wrap justify-center gap-4">
            {targets.map((target) => (
              <FanoutTarget
                key={target.key}
                compact
                containerRef={container}
                sourceRef={source}
                channel={target.channel}
                kind={target.kind}
                onAdd={onAdd}
              />
            ))}
          </div>
        </div>
      )}
    </div>
  )
}

/**
 * One channel, or the ring where a kind of channel would go, and its line from
 * the source. The kind is the title and the name rides in the hint as one
 * string: the cards below are where a channel is found by name.
 */
function FanoutTarget({
  containerRef,
  sourceRef,
  channel,
  kind,
  onAdd,
  compact,
}: {
  containerRef: RefObject<HTMLDivElement | null>
  sourceRef: RefObject<HTMLDivElement | null>
  channel?: NotificationChannel
  kind: NotificationChannelKind
  onAdd?: (kind: NotificationChannelKind) => void
  compact?: boolean
}) {
  const mark = useRef<HTMLDivElement>(null)
  const meta = KINDS.find((item) => item.kind === kind)
  const title = meta?.title ?? kind
  const line = lineOf(channel)
  const size = compact ? "sm" : "md"
  const hint =
    channel &&
    (channel.enabled
      ? `${channel.name} · ${eventLabel(channel.events).toLowerCase()} · ${outcomeWords(channel)}`
      : `${channel.name} · paused`)
  const drawn = channel ? (
    <WireMark tone="logo" size={size}>
      <ChannelGlyph channel={channel} />
    </WireMark>
  ) : (
    <WireLink label={`Add ${title}`} onClick={onAdd ? () => onAdd(kind) : undefined}>
      <WirePlaceholder size={size} product={channelProduct(kind)} fallback={Envelope} />
    </WireLink>
  )
  const beam = (
    <AnimatedBeam
      containerRef={containerRef}
      fromRef={sourceRef}
      toRef={mark}
      still={line.still}
      dashed={"dashed" in line}
      tone={"tone" in line ? line.tone : "default"}
      duration={2.2}
      delay={channel ? (channel.id % 5) * 0.3 : 0}
    />
  )
  if (compact)
    return (
      <div className="min-w-0">
        {beam}
        <div ref={mark} className="relative z-10 flex">
          {drawn}
        </div>
        {channel && <span className="sr-only">{`${title}: ${hint}`}</span>}
      </div>
    )
  return (
    <div className="min-w-0">
      {beam}
      <WireNode
        nodeRef={mark}
        mark={drawn}
        title={channel ? title : <span className="text-muted-foreground">{title}</span>}
        hint={channel ? hint : meta?.hint}
      />
    </div>
  )
}

/**
 * One channel, as a card that opens its sheet: drawn as the service it posts
 * to, named, with the address it posts at as the literal the server holds.
 * How its last message went is beside the name in the colour of what
 * happened, and under the line are the events it hears about and its last
 * fourteen attempts as a strip — so a channel that has been dropping every
 * other message reads as that before its name is read.
 */
function ChannelCard({
  channel,
  verbs,
  sending,
  switching,
  wide,
  onOpen,
}: {
  channel: NotificationChannel
  verbs: Verb[]
  sending?: string
  switching?: string
  wide: boolean
  onOpen: () => void
}) {
  const last = <LastDelivery channel={channel} sending={sending} switching={switching} />
  return (
    <ChoiceRow
      leading={<ChannelTile channel={channel} />}
      title={channel.name}
      verb={`Open ${channel.name}`}
      onSelect={onOpen}
      busy={Boolean(sending)}
      description={
        <>
          <span className="font-mono">{channel.target || channel.url}</span>
          {channel.via && ` · via ${channel.via}`}
        </>
      }
      trailing={wide ? last : undefined}
      actions={<VerbActions dim verbs={verbs} menuLabel={`Actions for ${channel.name}`} />}
    >
      <div className="flex min-w-0 flex-wrap items-center gap-x-6 gap-y-2 text-hint text-muted-foreground sm:pl-11">
        {/* A line of its own, so the state is never read as one more word of
            the events after it. */}
        {!wide && <div className="basis-full">{last}</div>}
        <span className="min-w-0">{eventLabel(channel.events)}</span>
        <RecentStrip
          recent={channel.recent}
          retrying={
            channel.lastDelivery?.status === "failed" && Boolean(channel.lastDelivery.nextAttemptAt)
          }
        />
      </div>
    </ChoiceRow>
  )
}

/**
 * The state word beside a channel's name, in the colour of what last
 * happened — not "Enabled" on every card, which was a column of green words
 * saying nothing about whether a message had arrived.
 */
function LastDelivery({
  channel,
  sending,
  switching,
}: {
  channel: NotificationChannel
  /** The participles of the verbs in flight, from the verbs themselves. */
  sending?: string
  switching?: string
}) {
  if (sending)
    return (
      <span className="inline-flex items-center gap-1.5 text-xs font-medium whitespace-nowrap">
        <StatusDot tone="warning" />
        <TextShimmer>{sending}</TextShimmer>
      </span>
    )
  if (switching)
    return <TextShimmer className="text-xs font-medium whitespace-nowrap">{switching}</TextShimmer>
  if (!channel.enabled) return <Status tone="stopped" label="Paused" />
  const last = channel.lastDelivery
  if (!last)
    return <span className="text-xs whitespace-nowrap text-muted-foreground">never sent</span>
  if (last.status === "pending") return <Status tone="warning" label="sending" />
  if (last.status === "delivered")
    return <Status tone="running" label={`delivered ${relativeTime(last.createdAt)}`} />
  if (last.nextAttemptAt) return <Status tone="warning" label={retryLabel(last.nextAttemptAt)} />
  return <Status tone="danger" label={`failed ${relativeTime(last.createdAt)}`} />
}

/**
 * A channel's last fourteen attempts, oldest first, as the list reports them.
 * The newest is the channel's last delivery, so when that one failed with a
 * retry still ahead its square is amber, as the words beside the name are.
 */
function RecentStrip({ recent, retrying }: { recent?: NotificationOutcome[]; retrying: boolean }) {
  if (!recent || recent.length === 0) return null
  const failed = recent.filter((outcome) => outcome.status === "failed").length - (retrying ? 1 : 0)
  return (
    <OutcomeStrip
      label={`Last ${plural(recent.length, "delivery", "deliveries")}: ${failed} failed${retrying ? ", 1 retrying" : ""}`}
      items={recent.map((outcome, index) => {
        const retry = retrying && index === recent.length - 1
        return {
          key: `${outcome.createdAt}-${index}`,
          tone: retry ? "warning" : (STRIP_TONE[outcome.status] ?? "muted"),
          title: `${outcome.test ? "test · " : ""}${retry ? "retrying" : outcome.status} · ${relativeTime(outcome.createdAt)}`,
        }
      })}
    />
  )
}

/** A failed attempt with a retry ahead is still being worked on, not given up on. */
function attemptTone(delivery: NotificationDelivery): OutcomeTone {
  return delivery.status === "failed" && delivery.nextAttemptAt
    ? "warning"
    : (STRIP_TONE[delivery.status] ?? "muted")
}

/** One attempt's outcome, in the words and colour of what happened to it. */
function DeliveryStatus({ delivery }: { delivery: NotificationDelivery }) {
  if (delivery.status === "delivered") return <Status tone="running" label="Delivered" />
  if (delivery.status === "pending") return <Status tone="warning" label="Sending" />
  if (delivery.nextAttemptAt)
    return <Status tone="warning" label={retryLabel(delivery.nextAttemptAt)} />
  return <Status tone="danger" label="Failed" />
}

function dayOf(iso: string) {
  const day = (date: Date) =>
    new Date(date.getFullYear(), date.getMonth(), date.getDate()).getTime()
  const days = Math.round((day(new Date()) - day(new Date(iso))) / 86_400_000)
  return days === 0 ? "Today" : days === 1 ? "Yesterday" : calendarDate(iso)
}

/**
 * A channel's own sheet: what it is — its service, where it posts, what it
 * hears about, how its last message went — then its attempts, counted, as a
 * strip, and one by one under the day they were made. An attempt that
 * announced a run names the project and run and opens it.
 */
function ChannelSheet({
  channel,
  deliveries,
  verbs,
  sending,
  switching,
  onClose,
}: {
  channel?: NotificationChannel
  /** The channel's log, read by the page so its refresh reaches it. */
  deliveries: ReturnType<typeof usePoll<NotificationDelivery[]>>
  verbs: Verb[]
  sending?: string
  switching?: string
  onClose: () => void
}) {
  const wide = useMediaQuery("(min-width: 640px)")
  const log = deliveries.data ?? []
  const arrived = useArrivals(log.map((delivery) => String(delivery.id)))
  const days: { label: string; rows: NotificationDelivery[] }[] = []
  for (const delivery of log) {
    const label = dayOf(delivery.createdAt)
    const day = days.at(-1)
    if (day?.label === label) day.rows.push(delivery)
    else days.push({ label, rows: [delivery] })
  }
  const delivered = log.filter((delivery) => delivery.status === "delivered").length
  // An attempt with a retry ahead has not been given up on.
  const retrying = log.filter(
    (delivery) => delivery.status === "failed" && delivery.nextAttemptAt,
  ).length
  const failed = log.filter((delivery) => delivery.status === "failed").length - retrying
  const oldestFirst = [...log].reverse()
  // A phone has room for about forty squares; the rest are in the rows below.
  const strip = wide ? oldestFirst : oldestFirst.slice(-36)

  return (
    <SidePanel
      open={Boolean(channel)}
      onOpenChange={(open) => !open && onClose()}
      title={channel ? `Deliveries · ${channel.name}` : "Deliveries"}
      description="Recent delivery attempts for this channel."
      width="md"
      actions={
        channel &&
        verbs.length > 0 && (
          <VerbBar verbs={verbs} menuLabel={`Actions for ${channel.name} in its sheet`} />
        )
      }
    >
      {channel && (
        <div className="space-y-6">
          <div className="flex min-w-0 flex-wrap items-start gap-x-3 gap-y-2">
            <ChannelTile channel={channel} size="md" />
            <div className="min-w-0 flex-1 space-y-1">
              <p className="text-title font-semibold tracking-tight">{kindTitle(channel.kind)}</p>
              <p className="truncate font-mono text-hint text-muted-foreground">
                {channel.target || channel.url}
                {channel.via && ` · via ${channel.via}`}
              </p>
              <div className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1 text-hint text-muted-foreground">
                <span>{eventLabel(channel.events)}</span>
                <span>added {calendarDate(channel.createdAt)}</span>
              </div>
            </div>
            <span className="max-sm:basis-full max-sm:pl-13">
              <LastDelivery channel={channel} sending={sending} switching={switching} />
            </span>
          </div>

          {deliveries.error ? (
            <ErrorState error={deliveries.error} onRetry={deliveries.refresh} />
          ) : deliveries.loading && !deliveries.data ? (
            <LoadingPanel plain rows={3} />
          ) : log.length === 0 ? (
            <EmptyNote>No deliveries have been attempted yet.</EmptyNote>
          ) : (
            <>
              <div className="space-y-2">
                <p className="text-hint text-muted-foreground">
                  <span className="numeric font-medium text-foreground">{delivered}</span> delivered
                  · <span className="numeric font-medium text-foreground">{failed}</span> failed
                  {retrying > 0 && (
                    <>
                      {" "}
                      · <span className="numeric font-medium text-foreground">{retrying}</span>{" "}
                      retrying
                    </>
                  )}{" "}
                  · of the last {plural(log.length, "attempt")}
                </p>
                <OutcomeStrip
                  label={`Last ${plural(strip.length, "attempt")}`}
                  items={strip.map((delivery) => ({
                    key: String(delivery.id),
                    tone: attemptTone(delivery),
                    title: `${EVENT_WORD[delivery.event] ?? delivery.event} · ${delivery.status === "failed" && delivery.nextAttemptAt ? "retrying" : delivery.status} · ${relativeTime(delivery.createdAt)}`,
                  }))}
                />
              </div>

              {/* A plain panel's ground, so the rows bleed to the sheet's own
                  edge the way they do on a page. */}
              <Panel plain className="space-y-4">
                {days.map((day) => (
                  <section key={day.label} className="space-y-1">
                    <GroupRule label={day.label} count={day.rows.length} />
                    <RowList aria-label={`Delivery attempts, ${day.label}`}>
                      {day.rows.map((delivery) => (
                        <AttemptRow
                          key={delivery.id}
                          delivery={delivery}
                          arrived={arrived.has(String(delivery.id))}
                          wide={wide}
                        />
                      ))}
                    </RowList>
                  </section>
                ))}
              </Panel>
            </>
          )}
        </div>
      )}
    </SidePanel>
  )
}

/**
 * One attempt: what it announced, as words, and which run; when, which try it
 * was and how the far end answered; and its outcome. The wire key stays at the
 * edge as the literal a receiver matches on.
 */
function AttemptRow({
  delivery,
  arrived,
  wide,
}: {
  delivery: NotificationDelivery
  arrived: boolean
  wide: boolean
}) {
  const event = EVENT_WORD[delivery.event] ?? delivery.event
  const run = delivery.projectName
    ? ` · ${delivery.projectName} #${delivery.runNumber}`
    : delivery.runId
      ? ` · run ${delivery.runId}`
      : ""
  return (
    <Row
      href={
        delivery.projectId && delivery.runId
          ? `/deploy/${delivery.projectId}/runs/${delivery.runId}`
          : undefined
      }
      className={cn(arrived && "animate-rise")}
      // The event is not the row's state: the one coloured mark on the row
      // is its outcome at the edge (§4).
      title={`${event}${run}`}
      subtitle={[
        relativeTime(delivery.createdAt),
        delivery.attempt > 1 && `attempt ${delivery.attempt}`,
        RESPONSE_WORD[delivery.responseClass] ?? (delivery.responseClass || "—"),
      ]
        .filter(Boolean)
        .join(" · ")}
      trailing={
        <>
          {wide && <Tag mono>{delivery.event}</Tag>}
          <DeliveryStatus delivery={delivery} />
        </>
      }
    />
  )
}

/**
 * The five kinds as the services they are, picked by their marks (§16: a
 * choice between kinds is a grid, and a card you pick has the lit edge). Two
 * across a phone and three across the sheet, whose width does not follow the
 * viewport's.
 */
function KindPicker({
  value,
  onChange,
}: {
  value: NotificationChannelKind
  onChange: (kind: NotificationChannelKind) => void
}) {
  const label = useId()
  return (
    <div className="space-y-1.5">
      <p id={label} className="text-body font-medium">
        Deliver to
      </p>
      <ChoiceGrid
        columns="compact"
        role="group"
        aria-labelledby={label}
        className="gap-2 xl:grid-cols-3"
      >
        {KINDS.map((item) => (
          <ProductCard
            key={item.kind}
            product={channelProduct(item.kind)}
            fallback={Envelope}
            label={item.title}
            selected={value === item.kind}
            onClick={() => value !== item.kind && onChange(item.kind)}
          />
        ))}
      </ChoiceGrid>
      {/* The logo and the name are the choice; where to find the address is
          worth the whole sentence, once, for the kind that is chosen. */}
      <p className="text-hint leading-relaxed text-muted-foreground">
        {KINDS.find((item) => item.kind === value)?.hint}
      </p>
    </div>
  )
}

/**
 * The fields the chosen kind takes, each reading what is typed against the
 * server's own rule and lighting it once it is met: a Discord address that is
 * a Discord webhook, a bot token and the bot it names, a chat id and the kind
 * of chat its shape says it is, the recipients as people.
 */
function ChannelFields({
  draft,
  onChange,
  stored,
  open,
  onReplace,
}: {
  draft: Draft
  onChange: (draft: Draft) => void
  /** The masked address the list reports, shown until it is replaced. */
  stored?: string
  open: boolean
  onReplace: () => void
}) {
  const [shown, setShown] = useState(false)
  const config = (patch: Partial<Draft["config"]>) =>
    onChange({ ...draft, config: { ...draft.config, ...patch } })
  const keepHint = draft.id ? "Leave blank to keep the stored value." : undefined
  switch (draft.kind) {
    case "webhook": {
      const receiver = webhookProduct(draft.url)
      return (
        <Field
          label="HTTPS endpoint"
          htmlFor="notification-url"
          hint="Receives signed JSON with the project, environment, run number, outcome, reason and a link to the run."
        >
          <InputGroup>
            <InputGroupAddon>
              <ProductGlyph id={receiver ?? "webhook"} />
            </InputGroupAddon>
            <InputGroupInput
              id="notification-url"
              type="url"
              placeholder="https://hooks.example.com/deploy"
              value={draft.url}
              onChange={(event) => onChange({ ...draft, url: event.target.value })}
              autoComplete="off"
              spellCheck={false}
            />
          </InputGroup>
        </Field>
      )
    }
    case "discord":
    case "slack": {
      const label = draft.kind === "discord" ? "Discord webhook URL" : "Slack incoming webhook URL"
      const title = kindTitle(draft.kind)
      if (!open)
        return (
          // The stored address is a reading, not a field: nothing here takes
          // input until the reader chooses to replace it.
          <Field label={label} hint="Stored encrypted; the list shows it masked.">
            <InputGroup>
              <InputGroupAddon>
                <ProductGlyph id={draft.kind} />
              </InputGroupAddon>
              <InputGroupText className="min-w-0 flex-1 px-3 font-mono text-hint text-muted-foreground">
                <span className="truncate">{stored}</span>
              </InputGroupText>
              <InputGroupAddon align="inline-end" className="gap-0 p-0">
                <InputGroupButton aria-label="Replace delivery settings" onClick={onReplace}>
                  Replace
                </InputGroupButton>
              </InputGroupAddon>
            </InputGroup>
          </Field>
        )
      const url = draft.config.webhookUrl ?? ""
      const shape = webhookShape(url)
      const other = shape && shape !== draft.kind ? kindTitle(shape) : undefined
      return (
        <div className="space-y-2">
          <Field
            label={label}
            htmlFor="notification-webhook"
            hint={`The URL is stored encrypted and shown masked afterwards.${keepHint ? ` ${keepHint}` : ""}`}
          >
            <InputGroup>
              <InputGroupAddon>
                <ProductGlyph id={draft.kind} />
              </InputGroupAddon>
              <InputGroupInput
                id="notification-webhook"
                type="url"
                placeholder={
                  draft.kind === "discord"
                    ? "https://discord.com/api/webhooks/…"
                    : "https://hooks.slack.com/services/…"
                }
                value={url}
                onChange={(event) => config({ webhookUrl: event.target.value })}
                autoComplete="off"
                spellCheck={false}
              />
              {shape === draft.kind && (
                <InputGroupAddon align="inline-end">
                  <InputGroupText className="text-success">
                    <Check className="size-3.5" />
                    <span className="max-sm:sr-only">{title} webhook</span>
                  </InputGroupText>
                </InputGroupAddon>
              )}
            </InputGroup>
          </Field>
          {other && (
            <FormNote tone="warning">
              {draft.id
                ? `That is a ${other} address; this channel posts to ${title}.`
                : `That is a ${other} address — choose ${other} above.`}
            </FormNote>
          )}
        </div>
      )
    }
    case "telegram": {
      const token = draft.config.botToken?.trim() ?? ""
      const chat = draft.config.chatId?.trim() ?? ""
      return (
        <>
          <div className="space-y-2">
            <Field
              label="Bot token"
              htmlFor="notification-bot-token"
              hint={`From @BotFather. Add the bot to the chat first.${keepHint ? ` ${keepHint}` : ""}`}
            >
              <InputGroup>
                <InputGroupAddon>
                  <ProductGlyph id="telegram" />
                </InputGroupAddon>
                <InputGroupInput
                  id="notification-bot-token"
                  type={shown ? "text" : "password"}
                  placeholder="123456789:AA…"
                  value={draft.config.botToken ?? ""}
                  onChange={(event) => config({ botToken: event.target.value })}
                  autoComplete="off"
                  spellCheck={false}
                  className={cn(shown && "font-mono")}
                />
                <InputGroupAddon align="inline-end" className="gap-0 p-0">
                  <InputGroupToggle
                    icon={shown ? EyeOff : Eye}
                    label="Show"
                    aria-label="Show bot token"
                    pressed={shown}
                    onPressedChange={setShown}
                  />
                </InputGroupAddon>
              </InputGroup>
            </Field>
            {token && (
              <p aria-live="polite">
                {/* The id before the colon is the bot's, and is not the secret. */}
                <FieldCheck met={TELEGRAM_TOKEN.test(token)}>
                  {TELEGRAM_TOKEN.test(token)
                    ? `bot ${token.split(":")[0]}`
                    : "digits, a colon, then the bot's secret"}
                </FieldCheck>
              </p>
            )}
          </div>
          <div className="space-y-2">
            <Field label="Chat id" htmlFor="notification-chat-id">
              <Input
                id="notification-chat-id"
                placeholder="-1001234567890 or @channel"
                value={draft.config.chatId ?? ""}
                onChange={(event) => config({ chatId: event.target.value })}
                className="font-mono"
                autoComplete="off"
                spellCheck={false}
              />
            </Field>
            {chat && (
              <p aria-live="polite">
                <FieldCheck met={TELEGRAM_CHAT.test(chat)}>
                  {TELEGRAM_CHAT.test(chat) ? chatKind(chat) : "a numeric id or an @name"}
                </FieldCheck>
              </p>
            )}
          </div>
        </>
      )
    }
    case "email":
      return <MailFields draft={draft} onChange={onChange} keepHint={keepHint} />
  }
}

/**
 * An SMTP server and a message. Security is three words side by side rather
 * than a select, and the sign-in fields close while it is None — the server
 * refuses a password in clear text, so the form does not invite one. The
 * recipients are drawn as people as they are typed, and a line that is not
 * an address is named.
 */
function MailFields({
  draft,
  onChange,
  keepHint,
}: {
  draft: Draft
  onChange: (draft: Draft) => void
  keepHint?: string
}) {
  const [shown, setShown] = useState(false)
  const config = (patch: Partial<Draft["config"]>) =>
    onChange({ ...draft, config: { ...draft.config, ...patch } })
  const security = SECURITY.find((item) => item.value === (draft.config.smtpSecurity ?? "starttls"))
  const plain = security?.value === "none"
  const host = draft.config.smtpHost?.trim() ?? ""
  const service = mailProduct(host)
  const from = draft.config.from?.trim() ?? ""
  const recipients = recipientsOf(draft.config.toText)
  // One mark per person: an address typed twice is still one recipient.
  const people = [...new Set(recipients.filter(isAddress).map(addressOf))]
  const wrong = recipients.find((line) => !isAddress(line))
  return (
    <>
      <FormSection title="Mail server">
        <FieldRow className="sm:grid-cols-[minmax(0,1fr)_7rem]">
          <Field label="SMTP host" htmlFor="notification-smtp-host">
            <InputGroup>
              <InputGroupAddon>
                {service ? <ProductGlyph id={service} /> : <Envelope aria-hidden />}
              </InputGroupAddon>
              <InputGroupInput
                id="notification-smtp-host"
                placeholder="smtp.example.com"
                value={draft.config.smtpHost ?? ""}
                onChange={(event) => config({ smtpHost: event.target.value })}
                autoComplete="off"
                spellCheck={false}
              />
            </InputGroup>
          </Field>
          <Field label="Port" htmlFor="notification-smtp-port">
            <Input
              id="notification-smtp-port"
              type="number"
              min={1}
              max={65535}
              placeholder={String(security?.port ?? 587)}
              value={draft.config.smtpPort ?? ""}
              onChange={(event) =>
                config({ smtpPort: event.target.value ? Number(event.target.value) : undefined })
              }
            />
          </Field>
        </FieldRow>
        {host && !SMTP_HOST.test(host) && (
          <FormNote tone="warning">{host} is not a hostname or an address.</FormNote>
        )}
        <Field label="Security" hint={security?.hint}>
          <ToggleGroup
            type="single"
            value={security?.value}
            onValueChange={(next: Security) => next && config({ smtpSecurity: next })}
            variant="outline"
            aria-label="Security"
            className="w-full"
          >
            {SECURITY.map((item) => (
              <ToggleGroupItem
                key={item.value}
                value={item.value}
                className="h-11 flex-1 text-xs sm:h-9"
              >
                {item.label}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>
        </Field>
        <FieldRow>
          <Field
            label="Username"
            htmlFor="notification-smtp-username"
            hint={plain ? "Sign-in needs STARTTLS or TLS." : undefined}
          >
            <Input
              id="notification-smtp-username"
              value={draft.config.smtpUsername ?? ""}
              onChange={(event) => config({ smtpUsername: event.target.value })}
              disabled={plain}
              autoComplete="off"
              spellCheck={false}
            />
          </Field>
          <Field
            label="Password"
            htmlFor="notification-smtp-password"
            hint={plain ? undefined : keepHint}
          >
            {/* Dimmed once, border and all, as the username beside it is: the
                controls inside drop their own disabled dimming. */}
            <InputGroup className={cn(plain && "opacity-50 [&_:disabled]:opacity-100")}>
              <InputGroupInput
                id="notification-smtp-password"
                type={shown ? "text" : "password"}
                value={draft.config.smtpPassword ?? ""}
                onChange={(event) => config({ smtpPassword: event.target.value })}
                disabled={plain}
                autoComplete="new-password"
              />
              <InputGroupAddon align="inline-end" className="gap-0 p-0">
                <InputGroupToggle
                  icon={shown ? EyeOff : Eye}
                  label="Show"
                  aria-label="Show password"
                  pressed={shown}
                  onPressedChange={setShown}
                  disabled={plain}
                />
              </InputGroupAddon>
            </InputGroup>
          </Field>
        </FieldRow>
      </FormSection>

      <FormSection title="Message">
        <Field label="From" htmlFor="notification-from">
          <Input
            id="notification-from"
            placeholder="Deployments <deploys@example.com>"
            value={draft.config.from ?? ""}
            onChange={(event) => config({ from: event.target.value })}
            autoComplete="off"
          />
        </Field>
        {from && !isAddress(from) && <FormNote tone="warning">{from} is not an address.</FormNote>}
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
            spellCheck={false}
          />
        </Field>
        {recipients.length > 0 && (
          <div aria-live="polite" className="space-y-1.5">
            {people.length > 0 && (
              <p className="flex flex-wrap items-center gap-1.5 text-hint text-muted-foreground">
                <span className="numeric mr-1">{plural(people.length, "recipient")}</span>
                {people.slice(0, 12).map((address) => (
                  <span key={address} title={address} className="flex">
                    <InitialsMark name={address} size="xs" />
                  </span>
                ))}
                {people.length > 12 && <span className="numeric">+{people.length - 12}</span>}
              </p>
            )}
            {wrong && <FormNote tone="warning">{wrong} is not an address.</FormNote>}
            {recipients.length > 20 && (
              <FormNote tone="warning">At most twenty — {recipients.length} are listed.</FormNote>
            )}
          </div>
        )}
      </FormSection>
    </>
  )
}

/**
 * A signed webhook's secret, once, in the sheet that made it — with how a
 * receiver checks a request against it, which is the one thing the secret is
 * for (the API key dialog's shape: the key, and the command that uses it).
 */
function SigningSecret({ secret }: { secret: string }) {
  // The command names the secret by a variable rather than carrying it, so
  // pasting it does not put the secret in shell history or `ps` a second time.
  const verify = `openssl dgst -sha256 -hmac "$JD_SIGNING_SECRET" < body.json`
  return (
    <div className="space-y-5">
      <Field
        label="Signing secret"
        hint="Copy it now. It is sealed on this server and cannot be shown again."
        trailing={
          <Button
            size="xs"
            variant="outline"
            aria-label="Copy signing secret"
            onClick={() => void copyText(secret, "Secret copied")}
          >
            <Copy />
            Copy
          </Button>
        }
      >
        <Well className="break-all select-all">{secret}</Well>
      </Field>
      <Field
        label="Verify it"
        hint="Every request carries X-JD-Signature-256: sha256= and this digest of its raw body, with the secret exported as JD_SIGNING_SECRET."
        trailing={
          <Button
            size="xs"
            variant="outline"
            aria-label="Copy verify command"
            onClick={() => void copyText(verify, "Command copied")}
          >
            <Copy />
            Copy
          </Button>
        }
      >
        <Well className="break-all">{verify}</Well>
      </Field>
    </div>
  )
}
