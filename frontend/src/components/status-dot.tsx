import { cn } from "@/lib/utils"

/**
 * What a status dot can be. A *state*, not a tone: `running` and `stopped` are
 * facts about a thing, and `warning`/`danger`/`notice` are readings of it. The
 * severe level takes the shared word from `components/tone.ts` so a component
 * handing a level to another component does not have to translate it.
 */
export type DotTone = "running" | "stopped" | "warning" | "danger" | "notice" | "unknown"

/** The dot's fill. */
const DOT_TONE: Record<DotTone, string> = {
  running: "bg-success",
  stopped: "bg-muted-foreground",
  warning: "bg-warning",
  danger: "bg-destructive",
  notice: "bg-muted-foreground",
  unknown: "bg-muted-foreground",
}

/**
 * The label's colour. Only the two that need acting on take a hue — a column
 * of green "running" text is as hard to scan as a column with no signal at
 * all.
 */
const TEXT_TONE: Record<DotTone, string> = {
  running: "text-foreground",
  stopped: "text-muted-foreground",
  warning: "text-warning",
  danger: "text-destructive",
  notice: "text-muted-foreground",
  unknown: "text-muted-foreground",
}

/**
 * The icon's colour, where a caller passes one instead of the dot. It tracks
 * `TEXT_TONE` except for `running`: the label there is deliberately neutral
 * (a column of green "running" is noise), but a lone check or arrow icon
 * carries the whole signal and should read as good.
 */
const ICON_TONE: Record<DotTone, string> = {
  ...TEXT_TONE,
  running: "text-success",
}

/** Maps the many state vocabularies (docker, systemd, pm2) onto one signal. */
export function toneFor(state: string | undefined): DotTone {
  switch (state?.toLowerCase()) {
    case "running":
    case "active":
    case "online":
    case "enabled":
    case "success":
    case "open":
    case "connected":
      return "running"
    case "paused":
    case "restarting":
    case "activating":
    case "deactivating":
    case "launching":
    case "stopping":
    case "connecting":
    case "reconnecting":
      return "warning"
    case "failed":
    case "errored":
    case "dead":
    case "unreachable":
      return "warning"
    case "exited":
    case "stopped":
    case "inactive":
    case "created":
    case "closed":
    case "disconnected":
      return "stopped"
    default:
      return "unknown"
  }
}

/**
 * `Health.status` and `Posture.status` are the same shape — a hardened/healthy
 * "ok", ranked up through notice, warning and critical — because they're the
 * same kind of thing: a verdict, not a running state. `VERDICT_TONE` is what
 * lets one indicator answer both without `toneFor`'s state-string guessing,
 * which has no `"critical"` case to guess right.
 */
export type Verdict = "ok" | "notice" | "warning" | "critical"

const VERDICT_TONE: Record<Verdict, DotTone> = {
  ok: "running",
  notice: "notice",
  warning: "warning",
  critical: "danger",
}

export function StatusDot({
  state,
  tone,
  live,
  className,
}: {
  state?: string
  tone?: DotTone
  /**
   * This reading is arriving, not remembered.
   *
   * A halo that breathes out of the dot and fades, once every couple of
   * seconds. It is deliberately not `animate-pulse` — that dims the dot itself,
   * which on a table of twenty running containers reads as twenty things
   * blinking for attention. Here the dot is constant and only the air around it
   * moves, so a glance at the column still reads "all green" and a longer look
   * reads "and it is live".
   *
   * Reserved for a row fed by an open socket. A polled figure is not live, and
   * saying it is would be the interface lying about how fresh its numbers are.
   */
  live?: boolean
  className?: string
}) {
  const fill = DOT_TONE[tone ?? toneFor(state)]
  if (!live) {
    return <span className={cn("size-1.5 shrink-0 rounded-full", fill, className)} />
  }
  return (
    <span className={cn("relative flex size-1.5 shrink-0", className)}>
      <span className={cn("absolute inset-0 animate-breathe rounded-full", fill)} aria-hidden />
      <span className={cn("relative size-1.5 rounded-full", fill)} />
    </span>
  )
}

/**
 * The one status indicator in the app: a coloured dot (or a caller-supplied
 * icon) and a label, and nothing else — no border, no filled pill.
 *
 * A pill turns every status into a small stamped object competing for
 * attention; a dot and a word reads as a fact about the row it sits in. It is
 * reached either through a raw state string — `toneFor` reads Docker's,
 * systemd's and pm2's vocabularies — or through a `verdict`, for the health
 * and security posture pages, whose four-value severity scale doesn't fit any
 * of those vocabularies.
 */
export function Status({
  state,
  verdict,
  tone: given,
  label,
  live,
  icon: Icon,
  className,
}: {
  state?: string
  verdict?: Verdict
  /**
   * The tone directly, for the callers that already hold one — a table of
   * stacks whose six states map to tones the string vocabularies don't cover.
   */
  tone?: DotTone
  label?: React.ReactNode
  /** Passed to the dot — see `StatusDot`. Ignored when an icon is given. */
  live?: boolean
  icon?: React.ComponentType<{ className?: string }>
  className?: string
}) {
  const tone = given ?? (verdict ? VERDICT_TONE[verdict] : toneFor(state))
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 text-xs font-medium whitespace-nowrap",
        className,
      )}
    >
      {Icon ? (
        <Icon className={cn("size-3.5 shrink-0", ICON_TONE[tone])} />
      ) : (
        <StatusDot tone={tone} live={live} />
      )}
      <span className={TEXT_TONE[tone]}>{label ?? state ?? "unknown"}</span>
    </span>
  )
}
