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
import { ChannelNames } from "@/components/deploy/settings/traffic-alerts"
import { Meter } from "@/components/meter"
import { ProductLogo } from "@/components/product-logo"
import { Status, type DotTone } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import type { Tone } from "@/components/tone"
import { VerbActions, type Verb } from "@/components/verbs"
import {
  ALERT_KINDS,
  draftFor,
  eventOutcome,
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
        label: `Firing · ${reading}${focus.since ? ` since ${relativeTime(focus.since)}` : ""}`,
      }
    case "held":
      return {
        tone: "danger",
        meter: "danger",
        label: `Firing · ${reading}, kept back by the cooldown`,
      }
    case "pending":
      return { tone: "warning", meter: "warning", label: `Holding · ${reading}` }
    case "unknown":
      return { tone: "unknown", meter: "default", label: "Not read" }
  }
  return { tone: "running", meter: "default", label: `Quiet · ${reading}` }
}

/**
 * One rule, read as its sentence, whole — it wraps rather than truncates,
 * because on a phone the sentence is the rule. Beside it, where it stands;
 * under it, the reading against the line on a meter where the kind has one,
 * the reading in words as the server wrote it, the channels it tells and when
 * it was last read. A rule for every database says so at the foot of its
 * second line; a backup rule names the jobs it follows.
 */
export function AlertRow({
  rule,
  subjects,
  away,
  empty,
  channels,
  verbs,
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
  className?: string
}) {
  const wide = useMediaQuery("(min-width: 640px)")
  const words = ALERT_KINDS[rule.kind]
  const focus = focusOf(subjects)
  const stand = standing(rule, subjects, Boolean(away))
  const meter = rule.enabled && !away ? meterOf(rule, focus) : undefined
  const status = (
    <Status key={stand.label} tone={stand.tone} label={stand.label} className="animate-rise" />
  )
  const said = away ?? (focus ? (focus.state === "unknown" ? focus.unknown : focus.detail) : empty)
  const checked = subjects
    .map((s) => s.checkedAt)
    .filter(Boolean)
    .sort()
    .at(-1)
  return (
    <li
      className={cn(
        "grid min-w-0 grid-cols-[2rem_minmax(0,1fr)_auto] items-start gap-x-3 gap-y-2 py-3 first:pt-0 last:pb-0",
        !rule.enabled && "opacity-80",
        className,
      )}
    >
      <KindTile kind={rule.kind} className={cn(!rule.enabled && "opacity-60")} />
      <p className="min-w-0 pt-1.5 text-body leading-snug text-pretty">
        <span className="font-medium">{words.label}</span>{" "}
        <span className="text-foreground/85">{words.describe(rule)}</span>
      </p>
      <span className="flex items-start gap-3">
        {wide && <span className="flex pt-1.5">{status}</span>}
        {verbs && <VerbActions dim verbs={verbs} menuLabel={`Actions for ${sentence(rule)}`} />}
      </span>
      <div className="col-span-2 col-start-2 flex min-w-0 flex-wrap items-center gap-x-5 gap-y-2 text-hint text-muted-foreground">
        {!wide && status}
        {meter && (
          <Meter
            value={meter.value}
            mark={meter.mark}
            tone={stand.meter}
            label={meter.label}
            className="w-28 shrink-0 sm:w-40"
          />
        )}
        {isBackupKind(rule.kind) && subjects.length > 0 && (
          <span className="min-w-0 truncate">
            follows {subjects.map((s) => s.label).join(", ")}
          </span>
        )}
        {rule.connectionId === null && <Tag>Every database</Tag>}
        {channels && <ChannelNames ids={rule.channels} channels={channels} />}
        {rule.enabled && checked && (
          <span className="whitespace-nowrap">checked {relativeTime(checked)}</span>
        )}
      </div>
      {rule.enabled && said && (
        <p className="col-span-2 col-start-2 -mt-1 min-w-0 text-hint text-pretty text-muted-foreground">
          {said}
        </p>
      )}
    </li>
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
