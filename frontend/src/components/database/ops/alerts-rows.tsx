"use client"

import {
  Archive,
  ArrowLeftRight,
  Clock,
  Connection,
  Gauge,
  Users,
  type Icon,
} from "@/components/icons"
import { relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { DbAlertKind, NotificationChannel } from "@/lib/types"
import { useMediaQuery } from "@/hooks/use-mobile"
import { ChoiceCard, ChoiceCardHint, ChoiceCardTitle } from "@/components/choice-card"
import { ChoiceRow } from "@/components/flow"
import { ChannelNames } from "@/components/deploy/settings/traffic-alerts"
import { ChannelGlyph } from "@/components/deploy/vocabulary"
import { Meter } from "@/components/meter"
import { ProductLogo } from "@/components/product-logo"
import { Status, type DotTone } from "@/components/status-dot"
import type { Tone } from "@/components/tone"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { VerbActions, type Verb } from "@/components/verbs"
import {
  ALERT_KINDS,
  draftFor,
  eventOutcome,
  firingFor,
  focusOf,
  isBackupKind,
  meterOf,
  sentence,
} from "@/components/database/ops/alerts-model"
import type {
  DbAlertEvent,
  DbAlertRule,
  DbAlertSubject,
} from "@/components/database/ops/alerts-types"

/** Each kind's glyph, on its card, its tile and its rows. */
export const KIND_GLYPH: Record<DbAlertKind, Icon> = {
  down: Connection,
  disk: Gauge,
  connections: Users,
  replication: ArrowLeftRight,
  backup_failed: Archive,
  backup_missed: Clock,
}

export function KindTile({ kind, className }: { kind: DbAlertKind; className?: string }) {
  return <ProductLogo size="sm" fallback={KIND_GLYPH[kind]} className={className} />
}

/** A rule to start from, for a database with none: it opens the form set to it. */
export function StarterCard({ kind, onClick }: { kind: DbAlertKind; onClick: () => void }) {
  const Glyph = KIND_GLYPH[kind]
  const words = ALERT_KINDS[kind]
  const start = draftFor(undefined, kind, "this")
  return (
    <ChoiceCard onClick={onClick} className="min-h-0 gap-1 p-2.5">
      <span className="flex min-w-0 items-center gap-2">
        <Glyph aria-hidden className="size-4 shrink-0 text-muted-foreground" />
        <ChoiceCardTitle className="truncate">{words.label}</ChoiceCardTitle>
      </span>
      <ChoiceCardHint>
        {words.describe({ threshold: Number(start.threshold), minutes: Number(start.minutes) })}
      </ChoiceCardHint>
    </ChoiceCard>
  )
}

/** Where a rule stands, as its status word and the tone its line takes. */
function standing(
  rule: DbAlertRule,
  subjects: DbAlertSubject[],
  away: boolean,
): { tone: DotTone; meter: Tone; label: string } {
  const words = ALERT_KINDS[rule.kind]
  const focus = focusOf(subjects)
  if (!rule.enabled) return { tone: "stopped", meter: "default", label: "Paused" }
  if (away) return { tone: "unknown", meter: "default", label: "Not watched here" }
  if (!focus) {
    return {
      tone: "unknown",
      meter: "default",
      label: isBackupKind(rule.kind) ? "No backup job dumps it" : "Waiting for a reading",
    }
  }
  const reading = words.read(focus.reading)
  switch (focus.state) {
    case "firing":
      return {
        tone: "danger",
        meter: "danger",
        label: `Firing${focus.since ? ` for ${firingFor(focus.since)}` : ""} · ${reading}`,
      }
    case "held":
      return {
        tone: "danger",
        meter: "danger",
        label: `Firing${focus.since ? ` for ${firingFor(focus.since)}` : ""} · ${reading}, held by the cooldown`,
      }
    case "pending":
      return { tone: "warning", meter: "warning", label: `Holding · ${reading}` }
    case "unknown":
      return { tone: "unknown", meter: "default", label: "Not read" }
  }
  return { tone: "running", meter: "default", label: `Quiet · ${reading}` }
}

/**
 * One rule, as a card of one height in every state: its kind's tile, its name
 * over its condition, and where it stands in a column of its own, with the
 * reading against the line as a small mark before the words where the kind
 * has a line. Pressing the card edits the rule, for those who may.
 *
 * The row was a sentence over a meter over the server's own sentence, so a
 * disk rule stood three lines tall beside a two-line server rule and a bar
 * hung between rows. What the server wrote about the reading, when it last
 * read, the jobs a backup rule follows and the channels it tells now open
 * from the status, which is a button for exactly that; a status with nothing
 * behind it stays a word.
 */
export function AlertRow({
  rule,
  subjects,
  away,
  empty,
  channels,
  verbs,
  onEdit,
  className,
}: {
  rule: DbAlertRule
  /** What the rule watches here: this database, or the backup jobs that dump it. */
  subjects: DbAlertSubject[]
  /** Why a rule for every database does not watch this one: its engine lacks the kind. */
  away?: string
  /** What to say while the rule has nothing here to read yet. */
  empty?: React.ReactNode
  /** The channels to name this rule's among; absent when the head names them for every rule. */
  channels?: NotificationChannel[]
  verbs?: Verb[]
  /** Opens the rule's form; absent for a reader who may not change it. */
  onEdit?: () => void
  className?: string
}) {
  // The status and its mark stand beside the name only where the page is wide
  // enough to keep the name whole; under that they are the card's second line,
  // on every card, so the cards stay one height there too.
  const wide = useMediaQuery("(min-width: 1024px)")
  const words = ALERT_KINDS[rule.kind]
  const focus = focusOf(subjects)
  const stand = standing(rule, subjects, Boolean(away))
  const meter = rule.enabled && !away ? meterOf(rule, focus) : undefined
  const said = rule.enabled
    ? (away ?? (focus ? (focus.state === "unknown" ? focus.unknown : focus.detail) : empty))
    : undefined
  const checked = rule.enabled
    ? subjects
        .map((s) => s.checkedAt)
        .filter(Boolean)
        .sort()
        .at(-1)
    : undefined
  const follows = isBackupKind(rule.kind) && subjects.length > 0
  const word = (
    <Status
      key={stand.label}
      tone={stand.tone}
      label={stand.label}
      className="min-w-0 animate-rise [&>span:last-child]:truncate"
    />
  )
  const every = rule.connectionId === null
  const behind = Boolean(said || checked || follows || channels || every)
  const status = behind ? (
    <Popover>
      <PopoverTrigger
        title="What it read"
        className="flex max-w-full min-w-0 rounded-sm text-left focus-ring"
      >
        {word}
      </PopoverTrigger>
      <PopoverContent
        align={wide ? "end" : "start"}
        aria-label={`What ${words.label} read`}
        className="w-80 space-y-2 p-3 text-hint text-muted-foreground"
      >
        {said && <p className="text-pretty text-foreground/90">{said}</p>}
        {follows && (
          <p className="min-w-0 break-words">follows {subjects.map((s) => s.label).join(", ")}</p>
        )}
        {/* Said on the card too, where a phone may cut it off the end of a long condition. */}
        {every && <p>watches every database</p>}
        {channels && <ChannelNames ids={rule.channels} channels={channels} />}
        {checked && <p className="whitespace-nowrap">checked {relativeTime(checked)}</p>}
      </PopoverContent>
    </Popover>
  ) : (
    <span className="flex max-w-full min-w-0">{word}</span>
  )
  const mark = (
    <span className="flex w-16 shrink-0 items-center">
      {meter && (
        <Meter value={meter.value} mark={meter.mark} tone={stand.meter} label={meter.label} />
      )}
    </span>
  )
  return (
    <ChoiceRow
      leading={<KindTile kind={rule.kind} className={cn(!rule.enabled && "opacity-60")} />}
      title={words.label}
      verb={`Edit ${sentence(rule)}`}
      description={
        <>
          <span className="font-mono">{words.describe(rule)}</span>
          {every && " · every database"}
        </>
      }
      onSelect={onEdit}
      disabled={!onEdit}
      trailing={
        wide && (
          <span className="flex items-center gap-3">
            {mark}
            <span className="flex w-64 min-w-0">{status}</span>
          </span>
        )
      }
      actions={
        verbs && <VerbActions dim verbs={verbs} menuLabel={`Actions for ${sentence(rule)}`} />
      }
      className={cn(!rule.enabled && "opacity-80", className)}
    >
      {!wide && (
        <div className="flex min-w-0 items-center gap-3 pl-11">
          <span className="flex min-w-0 flex-1">{status}</span>
          {mark}
        </div>
      )}
    </ChoiceRow>
  )
}

/**
 * Where the rules' messages go, as the services they post to and their
 * names. It is its own phrase beside the firing count, never run on from it.
 */
export function GoesTo({
  channels,
  className,
}: {
  channels: NotificationChannel[]
  className?: string
}) {
  if (channels.length === 0) {
    return <span className={cn("text-warning", className)}>No channel yet, so nobody is told</span>
  }
  return (
    <span className={cn("inline-flex min-w-0 flex-wrap items-center gap-x-2.5 gap-y-1", className)}>
      <span>Goes to</span>
      {channels.map((channel) => (
        <span key={channel.id} className="inline-flex min-w-0 items-center gap-1">
          <ChannelGlyph channel={channel} />
          <span className="truncate text-foreground/85">{channel.name}</span>
        </span>
      ))}
    </span>
  )
}

/** One thing a rule told, or kept back, and what became of the message. */
export function EventRow({ event, ruleGone }: { event: DbAlertEvent; ruleGone: boolean }) {
  const fired = event.event === "fired"
  return (
    <li className="min-w-0 space-y-0.5 py-2.5 first:pt-0 last:pb-0">
      <p className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-body">
        <Status tone={fired ? "danger" : "running"} label={fired ? "Fired" : "Resolved"} />
        <span className="min-w-0 font-medium break-words">
          {ALERT_KINDS[event.kind]?.label ?? event.kind}
        </span>
        <span className="min-w-0 break-words text-muted-foreground">
          {event.label}
          {ruleGone && " · rule removed"}
        </span>
      </p>
      <p className="text-hint text-pretty text-muted-foreground">
        {relativeTime(event.createdAt)} · {eventOutcome(event)}
        {event.detail && ` · ${event.detail}`}
      </p>
    </li>
  )
}
