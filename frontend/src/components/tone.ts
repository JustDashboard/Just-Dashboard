/**
 * The colour vocabularies of the product, and the one place each is mapped to
 * a fill or an ink.
 *
 * There are three, and each answers a different question:
 *
 *   `Tone` is a colour for a word or a figure: plain, good, needs a look, bad.
 *   `DotTone` is the state of a thing: running, stopped, a warning, a danger,
 *   a notice or not known. It is what a status dot is drawn in.
 *   `OutcomeTone` is how one attempt ended, for a strip of attempts.
 *
 * They used to be declared beside the component that drew each — status-dot,
 * outcome-strip, two network mark files and a picture each had its own copy
 * of the fills — and the copies drifted: healthy egress lines were green where
 * every other running thing is plain text, and a failed unit read amber.
 * Here they share one set of fills, so a state looks the same wherever it is
 * drawn.
 */

/**
 * The one severity vocabulary for colour, and the one word for "this is the
 * bad one".
 *
 * `danger` wins over `critical` and `destructive` because it describes the
 * *reading* rather than the cause: `critical` is what a health check reports,
 * `destructive` is what a button does, and `danger` is what the colour says.
 * `Verdict` keeps `critical` because that is the value the backend sends, and
 * `Button`'s variant keeps `destructive` because that is what the control is —
 * but anything choosing a *colour* chooses from here.
 */
export type Tone = "default" | "success" | "warning" | "danger"

/**
 * What a status dot can be. A *state*, not a tone: `running` and `stopped` are
 * facts about a thing, and `warning`/`danger`/`notice` are readings of it.
 */
export type DotTone = "running" | "stopped" | "warning" | "danger" | "notice" | "unknown"

/**
 * The dot's fill. `unknown` is a hollow ring rather than the grey `stopped`
 * and `notice` take: a reading nobody has made yet must not look like one
 * that says the thing is off.
 */
export const DOT_TONE: Record<DotTone, string> = {
  running: "bg-success",
  stopped: "bg-muted-foreground",
  warning: "bg-warning",
  danger: "bg-destructive",
  notice: "bg-muted-foreground",
  unknown: "bg-transparent ring-1 ring-inset ring-muted-foreground",
}

/** The same fills as a colour, for something other than a dot drawn in the tone. */
export const DOT_COLOR: Record<DotTone, string> = {
  running: "var(--success)",
  stopped: "var(--muted-foreground)",
  warning: "var(--warning)",
  danger: "var(--destructive)",
  notice: "var(--muted-foreground)",
  unknown: "var(--muted-foreground)",
}

/**
 * The word's colour. Only the two that need acting on take a hue — a column
 * of green "running" text is as hard to scan as a column with no signal at
 * all.
 */
export const TEXT_TONE: Record<DotTone, string> = {
  running: "text-foreground",
  stopped: "text-muted-foreground",
  warning: "text-warning",
  danger: "text-destructive",
  notice: "text-muted-foreground",
  unknown: "text-muted-foreground",
}

/**
 * The icon's colour, where a caller passes one instead of the dot. It tracks
 * `TEXT_TONE` except for `running`: the word there is deliberately neutral,
 * but a lone check or arrow icon carries the whole signal and should read as
 * good.
 */
export const ICON_TONE: Record<DotTone, string> = {
  ...TEXT_TONE,
  running: "text-success",
}

/** A colour tone as a state, for a reading that holds a `Tone` and draws a dot. */
export const TONE_DOT: Record<Tone, DotTone> = {
  default: "stopped",
  success: "running",
  warning: "warning",
  danger: "danger",
}

/**
 * Maps the many state vocabularies (docker, systemd, pm2) onto one signal.
 *
 * A thing that has failed, errored, died or cannot be reached is `danger`: it
 * is the state a reader opens the page to find, and it must not share amber
 * with one that is merely starting or restarting.
 */
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
      return "danger"
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
 * A verdict, not a running state: `Posture.status` and the other assessments
 * rank a hardened or healthy "ok" up through notice, warning and critical.
 * `VERDICT_TONE` lets one indicator answer them without `toneFor`'s
 * state-string guessing, which has no `"critical"` case to guess right.
 */
export type Verdict = "ok" | "notice" | "warning" | "critical"

export const VERDICT_TONE = {
  ok: "running",
  notice: "notice",
  warning: "warning",
  critical: "danger",
} as const satisfies Record<Verdict, DotTone>

/** How one attempt ended, in the only hues a reading of state may take (§3). */
export type OutcomeTone = "success" | "danger" | "warning" | "running" | "muted"

/**
 * Each outcome's fill. The three answers are the status dot's own fills, so a
 * run that failed is the same red in its strip as in its row's dot.
 */
export const OUTCOME_FILL: Record<OutcomeTone, string> = {
  success: DOT_TONE.running,
  danger: DOT_TONE.danger,
  warning: DOT_TONE.warning,
  // The brand, not a status hue: an attempt still going has no outcome yet,
  // and the blue is what this product draws "happening now" in.
  running: "bg-brand",
  // An attempt that ended without an outcome — cancelled, superseded,
  // stopped. Present, so the count is honest, and quiet, so it never reads as
  // either of the two answers the strip is scanned for.
  muted: "bg-muted-foreground/40",
}
