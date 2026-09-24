"use client"

import { Archive, CloudUpload, RefreshClockwise, Terminal, type Icon } from "@/components/icons"
import { API_BASE } from "@/lib/api"
import { duration, plural } from "@/lib/format"
import type { DeploymentSchedule, DeploymentTrigger } from "@/lib/types"
import { cn } from "@/lib/utils"
import type { OutcomeTone } from "@/components/outcome-strip"
import { ProductLogo, gitProviderProduct } from "@/components/product-logo"
import type { DotTone } from "@/components/status-dot"
import { humanize } from "@/components/deploy/vocabulary"

/**
 * The words and marks the Automation page's blocks share: what a sender is
 * called and drawn as, what a delivery's decision reads as, and what a
 * schedule's step does — so the readings, the picture, the lists and the
 * sheets say each of these one way.
 */

/** The senders a webhook can be created for, as the form offers them. */
export const PROVIDERS = [
  { key: "github", label: "GitHub", host: "github.com" },
  { key: "gitlab", label: "GitLab", host: "gitlab.com" },
  { key: "bitbucket", label: "Bitbucket", host: "bitbucket.org" },
  { key: "gitea", label: "Gitea", host: undefined },
  { key: "generic_hook", label: "Signed hook", host: undefined },
] as const

export type Provider = (typeof PROVIDERS)[number]["key"]

/** What a trigger is called by the thing that sends to it. */
export function providerLabel(trigger: { kind: string; provider?: string }): string {
  const key = trigger.provider || trigger.kind
  if (key === "api") return "API"
  if (key === "legacy_hook") return "Legacy hook"
  return PROVIDERS.find((one) => one.key === key)?.label ?? humanize(key)
}

/**
 * The product a trigger is drawn as: its forge, or the webhook's own mark for
 * a sender no forge names — a signed hook, an API call.
 */
export function triggerProduct(trigger: { kind: string; provider?: string }): string {
  return gitProviderProduct(trigger.provider || trigger.kind) ?? "webhook"
}

/** A trigger's tile, faded while it is switched off (KeyRow's revoked look). */
export function TriggerMark({
  trigger,
  className,
}: {
  trigger: { kind: string; provider?: string; enabled: boolean }
  className?: string
}) {
  return (
    <ProductLogo
      id={triggerProduct(trigger)}
      size="sm"
      className={cn(!trigger.enabled && "opacity-50 grayscale", className)}
    />
  )
}

/** A delivery reached by the GitHub App rather than by its own signed hook. */
export function viaApp(trigger: Pick<DeploymentTrigger, "config">) {
  return trigger.config.delivery === "app"
}

/**
 * Where a trigger's deliveries land. Relative, because `API_BASE` is; the
 * page adds its own origin wherever an operator copies it into a forge.
 */
export function hookUrlFor(projectId: number, trigger: DeploymentTrigger) {
  return trigger.kind === "generic_hook" || trigger.kind === "api"
    ? `${API_BASE}/deploy/${projectId}/hooks/${trigger.id}`
    : `${API_BASE}/hooks/providers/${trigger.provider ?? trigger.kind}/${trigger.hookId}`
}

/** How a delivery's decision reads: what happened, and how alarmed to be about it. */
const DECISIONS: Record<string, { label: string; tone: DotTone; strip: OutcomeTone }> = {
  accepted: { label: "Accepted", tone: "running", strip: "success" },
  suppressed: { label: "Ignored", tone: "stopped", strip: "muted" },
  rejected: { label: "Refused", tone: "danger", strip: "danger" },
  pending: { label: "Waiting", tone: "notice", strip: "running" },
  processing: { label: "Processing", tone: "notice", strip: "running" },
}

export function deliveryDecision(decision: string) {
  return (
    DECISIONS[decision] ?? {
      label: humanize(decision),
      tone: "warning" as DotTone,
      strip: "warning" as OutcomeTone,
    }
  )
}

/** "Last 14 deliveries: 10 accepted, 3 ignored, 1 refused". */
export function deliveriesSentence(decisions: string[]) {
  const count = (decision: string) => decisions.filter((one) => one === decision).length
  const parts = [
    `${count("accepted")} accepted`,
    count("suppressed") > 0 && `${count("suppressed")} ignored`,
    count("rejected") > 0 && `${count("rejected")} refused`,
  ].filter(Boolean)
  return `Last ${plural(decisions.length, "delivery", "deliveries")}: ${parts.join(", ")}`
}

/**
 * What a schedule's step does, as the word and the glyph it is picked by.
 * `game_command` is not offered: the schedule dispatcher
 * (handlers_deploy_automation.go) always answers `game_console_unavailable`
 * for it, so every run of a schedule built with it would report failed.
 */
export const ACTIONS = [
  {
    key: "deploy",
    label: "Deploy",
    glyph: CloudUpload,
    hint: "Build and release the configured branch",
  },
  {
    key: "restart",
    label: "Restart",
    glyph: RefreshClockwise,
    hint: "Stop and start the live release",
  },
  {
    key: "backup",
    label: "Backup",
    glyph: Archive,
    hint: "Run one of this server's backup jobs",
  },
  {
    key: "container_command",
    label: "Run a command",
    glyph: Terminal,
    hint: "Exec a command inside a container",
  },
] as const satisfies readonly { key: string; label: string; glyph: Icon; hint: string }[]

export type ScheduleAction = (typeof ACTIONS)[number]["key"]

export function actionOf(action: string) {
  return ACTIONS.find((one) => one.key === action)
}

export const ACTION_GLYPH: Record<string, Icon> = Object.fromEntries(
  ACTIONS.map((one) => [one.key, one.glyph]),
)

/** The zone and its offset from UTC right now: "Europe/Chisinau · UTC+3". */
export function zoneOffset(timeZone: string, at = new Date()): string {
  try {
    const part = new Intl.DateTimeFormat("en-GB", { timeZone, timeZoneName: "shortOffset" })
      .formatToParts(at)
      .find((one) => one.type === "timeZoneName")?.value
    return part === "GMT" ? "UTC" : (part?.replace("GMT", "UTC") ?? "")
  } catch {
    return ""
  }
}

/** The operator's own zone: the one they think in when they say "at three". */
export function browserZone() {
  return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC"
}

/** A moment in a schedule's own zone: "Tue 24 Sep · 03:00". */
export function zonedMoment(iso: string | Date, timeZone: string) {
  const at = typeof iso === "string" ? new Date(iso) : iso
  try {
    const day = at.toLocaleDateString("en-GB", {
      weekday: "short",
      day: "numeric",
      month: "short",
      timeZone,
    })
    const time = at.toLocaleTimeString("en-GB", {
      hour: "2-digit",
      minute: "2-digit",
      hour12: false,
      timeZone,
    })
    return { day, time }
  } catch {
    return { day: at.toDateString(), time: at.toTimeString().slice(0, 5) }
  }
}

/** The schedules a page would name first: enabled, soonest next run. */
export function soonest(schedules: DeploymentSchedule[]) {
  return schedules
    .filter((one) => one.enabled && one.nextRunAt)
    .sort((a, b) => Date.parse(a.nextRunAt!) - Date.parse(b.nextRunAt!))[0]
}

/**
 * How long past its time a firing may still be on its way: the dispatcher's
 * tick and a poll. Later than this it is not due, it was missed — a stalled
 * dispatcher, which "due now" for three weeks would have hidden.
 */
const LATE_AFTER_SECONDS = 120

/** A next run that has passed by more than the dispatcher's own delay. */
export function overdue(iso: string | undefined, now = Date.now()) {
  return Boolean(iso) && Date.parse(iso!) < now - LATE_AFTER_SECONDS * 1000
}

/**
 * Time until a moment, as the figure a tile or a row reads: "in 4h 12m",
 * "due now" once it has passed and the dispatcher has not caught up yet,
 * "overdue" once it plainly will not.
 */
export function untilLabel(iso: string | undefined) {
  if (!iso) return "—"
  const seconds = (Date.parse(iso) - Date.now()) / 1000
  if (Number.isNaN(seconds)) return "—"
  if (seconds < -LATE_AFTER_SECONDS) return "overdue"
  if (seconds < 45) return "due now"
  // Whole minutes: a countdown in seconds on a figure polled every five is
  // a number that jumps rather than one that counts.
  return `in ${duration(Math.max(60, Math.round(seconds / 60) * 60))}`
}
