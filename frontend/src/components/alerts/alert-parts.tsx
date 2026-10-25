"use client"

import { useRef, type ReactNode, type RefObject } from "react"
import Link from "next/link"
import { Bell, CheckCircle, Envelope, PaperAirplane } from "@/components/icons"
import { plural, relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { NotificationChannel, NotificationChannelKind } from "@/lib/types"
import { FormNote } from "@/components/form"
import { ProductLogo, channelProduct } from "@/components/product-logo"
import { ChannelGlyph, channelMark } from "@/components/deploy/vocabulary"
import { WireLink, WireMark, WireNode, WirePlaceholder } from "@/components/deploy/wire"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { ErrorState } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"

/**
 * The parts the alerts pages share. The proxy's Alerts page was drawn first,
 * the way Notifications is; the host's Alerts page under Metrics is the same
 * page over different readings, so what does not know which readings it is
 * showing — the channels a rule tells, the all-clear, a subject's card, a line
 * of what was told — lives here once, and each page keeps only its kinds.
 */

/** The channel kinds the picture offers where no channel is set up yet. */
export const ALERT_CHANNEL_KINDS: { kind: NotificationChannelKind; title: string }[] = [
  { kind: "slack", title: "Slack" },
  { kind: "discord", title: "Discord" },
  { kind: "telegram", title: "Telegram" },
  { kind: "email", title: "E-mail" },
]

export type AlertTone = "danger" | "warning"

/** A subject a rule follows, in the fields every alerts page's subjects share. */
export type AlertSubjectView = {
  subject: string
  label: string
  detail: string
  state: "pending" | "firing" | "quiet"
  since?: string
  muted: boolean
}

/** One line of what a rule told, in the fields every alerts page's history shares. */
export type AlertEventView = {
  subject: string
  label: string
  detail: string
  delivered: number
  failed: number
  muted: boolean
  createdAt: string
  test?: boolean
}

export function isFiring(subject: AlertSubjectView) {
  return subject.state === "firing" && !subject.muted
}

/** What became of a transition: told, kept back by a mute, or told nobody. */
export function alertOutcome(event: AlertEventView) {
  if (event.muted) return "muted, nobody told"
  if (event.delivered === 0 && event.failed === 0) return "no channel to tell"
  return `told ${plural(event.delivered, "channel")}${event.failed > 0 ? `, ${event.failed} failed` : ""}`
}

/**
 * Nothing firing, said as the good news it is rather than a grey line — and,
 * with no rule on, as the plain fact that nothing is followed, which is not.
 */
export function AllClear({ rules, cadence }: { rules: number; cadence: string }) {
  const good = rules > 0
  return (
    <div
      className={cn(
        "flex min-h-14 min-w-0 animate-rise items-center gap-3 rounded-xl border bg-choice-surface px-3 py-2.5",
        good && "border-rule-success",
      )}
    >
      <span
        className={cn(
          "flex size-10 shrink-0 rounded-lg border bg-background",
          good ? "border-rule-success" : "border-hairline",
        )}
      >
        <span
          className={cn(
            "flex size-full items-center justify-center rounded-[inherit]",
            good && "bg-wash-success",
          )}
        >
          {good ? (
            <CheckCircle aria-hidden className="size-5 text-success" />
          ) : (
            <Bell aria-hidden className="size-4 text-muted-foreground" />
          )}
        </span>
      </span>
      <div className="min-w-0">
        <p className="text-body leading-snug font-medium">
          {good ? "Nothing is firing" : "No rules yet, so nothing is followed"}
        </p>
        <p className="text-hint leading-snug text-pretty text-muted-foreground">
          {good
            ? `${plural(rules, "rule")} checked ${cadence}; you are told once when one starts and once when it stops.`
            : "Add a rule below and it is checked from the next pass."}
        </p>
      </div>
    </div>
  )
}

/**
 * One subject a rule follows that is firing, holding or muted: a card in the
 * colour of its state, what it is, its reading, and its mute.
 */
export function SubjectCard({
  subject,
  kindLabel,
  tile,
  admin,
  busy,
  onMute,
}: {
  subject: AlertSubjectView
  kindLabel: string
  tile: (tone: AlertTone | undefined) => ReactNode
  admin: boolean
  busy: boolean
  onMute: (muted: boolean) => void
}) {
  const tone = subject.muted
    ? undefined
    : subject.state === "firing"
      ? "danger"
      : subject.state === "pending"
        ? "warning"
        : undefined
  const status =
    subject.state === "firing" ? (
      <Status
        tone={subject.muted ? "stopped" : "danger"}
        label={`Firing${subject.since ? ` since ${relativeTime(subject.since)}` : ""}`}
      />
    ) : subject.state === "pending" ? (
      <Status tone="warning" label="Holding before it is told" />
    ) : (
      <Status tone="stopped" label="Quiet" />
    )
  const name = subject.label || subject.subject
  return (
    <li
      className={cn(
        "flex min-h-14 min-w-0 items-center gap-3 rounded-xl border bg-choice-surface px-3 py-2.5",
        tone === "danger" && "border-rule-danger",
        tone === "warning" && "border-rule-warning",
      )}
    >
      {tile(tone)}
      <div className="min-w-0 flex-1">
        <p className="min-w-0 text-body leading-snug">
          <span className="font-medium break-all">{name}</span>{" "}
          <span className="text-muted-foreground">· {kindLabel}</span>
        </p>
        {subject.detail && (
          <p className="text-hint text-pretty text-muted-foreground">{subject.detail}</p>
        )}
        <p className="flex flex-wrap items-center gap-x-3 gap-y-1 text-hint">
          {status}
          {subject.muted && <span className="text-muted-foreground">muted, nobody is told</span>}
        </p>
      </div>
      {admin && (
        <Button
          size="sm"
          variant="outline"
          pending={busy}
          onClick={() => onMute(!subject.muted)}
          aria-label={`${subject.muted ? "Unmute" : "Mute"} ${name}`}
        >
          {subject.muted ? "Unmute" : "Mute"}
        </Button>
      )}
    </li>
  )
}

/** One transition a rule told, or kept back, as the history lists it. */
export function HistoryRow({
  event,
  firing,
  kindLabel,
  tile,
  ruleGone,
}: {
  event: AlertEventView
  firing: boolean
  kindLabel: string
  tile: ReactNode
  ruleGone: boolean
}) {
  return (
    <li className="flex min-w-0 items-start gap-3 border-b border-hairline py-3 first:pt-0 last:border-b-0 last:pb-0">
      {tile}
      <div className="min-w-0 flex-1 space-y-0.5">
        <p className="flex min-w-0 flex-wrap items-center gap-x-2.5 gap-y-1 text-body">
          <Status
            tone={event.test ? "notice" : firing ? "danger" : "running"}
            label={event.test ? "Test" : firing ? "Fired" : "Resolved"}
          />
          <span className="min-w-0 font-medium break-all">{event.label || event.subject}</span>
        </p>
        <p className="text-hint text-pretty text-muted-foreground">
          {kindLabel}
          {ruleGone && " (rule removed)"} · {relativeTime(event.createdAt)} ·{" "}
          <span className={cn(event.failed > 0 && "text-destructive")}>{alertOutcome(event)}</span>
          {event.detail && ` · ${event.detail}`}
        </p>
      </div>
    </li>
  )
}

/**
 * The notification channels, each with a test an administrator can send, or
 * the way to add one when there are none.
 */
export function ChannelTests({
  channels,
  error,
  onRetry,
  admin,
  busy,
  onTest,
}: {
  channels: NotificationChannel[]
  error?: Error
  onRetry: () => void
  admin: boolean
  busy?: string
  onTest: (channel: NotificationChannel) => void
}) {
  if (error) return <ErrorState error={error} onRetry={onRetry} />
  if (channels.length === 0)
    return (
      <FormNote>
        No notification channels yet, so a rule tells nobody.{" "}
        <Link
          href="/deploy/notifications"
          className="text-foreground underline-offset-2 hover:underline"
        >
          Add a channel
        </Link>
      </FormNote>
    )
  return (
    <ul aria-label="Notification channels" className="space-y-2">
      {channels.map((channel) => (
        <li
          key={channel.id}
          className="flex min-w-0 items-center gap-3 rounded-xl border bg-choice-surface px-3 py-2.5"
        >
          <ProductLogo
            id={channelMark(channel)}
            size="sm"
            fallback={Envelope}
            className={cn(!channel.enabled && "opacity-50 grayscale")}
          />
          <span className="min-w-0 flex-1">
            <span className="block truncate text-body font-medium">{channel.name}</span>
            <span className="block truncate font-mono text-hint text-muted-foreground">
              {channel.target || channel.url}
            </span>
          </span>
          {!channel.enabled && <Status tone="stopped" label="Paused" />}
          {admin && (
            <Button
              size="sm"
              variant="outline"
              disabled={!channel.enabled}
              pending={busy === `test:${channel.id}`}
              onClick={() => onTest(channel)}
              aria-label={`Send a test alert to ${channel.name}`}
            >
              <PaperAirplane className="size-3.5" />
              Send test
            </Button>
          )}
        </li>
      ))}
    </ul>
  )
}

/**
 * One channel in the picture's right column: its own service's mark, its wire
 * moving while an enabled rule reaches it, or a dashed way to add one.
 */
export function PictureChannel({
  channel,
  kind,
  live,
  containerRef,
  hubRef,
  compact,
  onAdd,
}: {
  channel?: NotificationChannel
  kind: NotificationChannelKind
  live: boolean
  containerRef: RefObject<HTMLDivElement | null>
  hubRef: RefObject<HTMLDivElement | null>
  compact: boolean
  onAdd: () => void
}) {
  const mark = useRef<HTMLDivElement>(null)
  const size = compact ? "sm" : "md"
  const title = ALERT_CHANNEL_KINDS.find((item) => item.kind === kind)?.title ?? kind
  const drawn = channel ? (
    <WireMark tone="logo" size={size}>
      <ChannelGlyph channel={channel} />
    </WireMark>
  ) : (
    <WireLink label={`Add a ${title} channel`} onClick={onAdd}>
      <WirePlaceholder size={size} product={channelProduct(kind)} fallback={Envelope} />
    </WireLink>
  )
  const beam = (
    <AnimatedBeam
      containerRef={containerRef}
      fromRef={hubRef}
      toRef={mark}
      still={!live}
      dashed={!channel}
      duration={2.2}
      delay={channel ? (channel.id % 5) * 0.3 : 0}
    />
  )
  const hint = channel
    ? !channel.enabled
      ? "paused"
      : live
        ? (channel.target || channel.url) && `told · ${channel.target || channel.url}`
        : "no rule tells it"
    : "add on Notifications"
  if (compact)
    return (
      <div className="min-w-0">
        {beam}
        <div ref={mark} className="relative z-10 flex">
          {drawn}
        </div>
        {channel && <span className="sr-only">{`${channel.name}: ${hint}`}</span>}
      </div>
    )
  return (
    <div className="min-w-0">
      {beam}
      <WireNode
        nodeRef={mark}
        mark={drawn}
        title={channel ? channel.name : <span className="text-muted-foreground">{title}</span>}
        hint={<span className="block truncate">{hint}</span>}
      />
    </div>
  )
}

/**
 * The channels the picture draws: every configured one, or, with none yet,
 * the kinds that can be added.
 */
export function pictureTargets(channels: NotificationChannel[]) {
  const missing = ALERT_CHANNEL_KINDS.filter(
    (item) => !channels.some((channel) => channel.kind === item.kind),
  )
  return [
    ...channels.map((channel) => ({ key: `channel-${channel.id}`, channel, kind: channel.kind })),
    ...(channels.length === 0
      ? missing.map((item) => ({
          key: `add-${item.kind}`,
          channel: undefined as NotificationChannel | undefined,
          kind: item.kind,
        }))
      : []),
  ]
}
