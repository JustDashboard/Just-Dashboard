import { cn } from "@/lib/utils"
import type { Tone } from "@/components/tone"

/** Thresholds used consistently wherever a utilisation figure is coloured. */
export function utilisationTone(percent: number): Tone {
  if (percent >= 90) return "danger"
  if (percent >= 75) return "warning"
  return "default"
}

const FILL: Record<Tone, string> = {
  default: "bg-primary",
  success: "bg-success",
  warning: "bg-warning",
  danger: "bg-destructive",
}

/**
 * A horizontal bar for a figure between 0 and 100.
 *
 * Four of these existed: `StatTile` drew `h-1` on `bg-muted`, `ui/progress` drew
 * `h-2` on `bg-primary/20`, the database monitor drew `h-1.5` filled
 * `bg-primary/70`, and the file preview drew `h-1.5` on `bg-surface-sunken`
 * filled `bg-primary/60`. Four heights, four tracks, four fills — and two
 * separate tone-to-colour maps that had to be kept in step by hand.
 *
 * One track, one fill, and the tone comes from the same `utilisationTone` that
 * colours the figure above it, which is what stops a number going amber next to
 * a bar that is still the default ink.
 */
export function Meter({
  value,
  tone = "default",
  color,
  size = "default",
  mark,
  label,
  decorative = false,
  className,
}: {
  /** 0–100. Clamped, because a percentage computed from a stale total can exceed it. */
  value: number
  tone?: Tone
  /**
   * A measure's own hue (`HUE.cpu`) in place of the tone's fill, for a bar that
   * says which reading it is rather than how worried to be. A caller that
   * wants both passes the tone's colour once the reading is past a line.
   */
  color?: string
  /**
   * `thin` for a bar under a figure in a tile; `default` inside a table row;
   * `cell` a 32px bar beside a figure in a table cell (the process table's).
   */
  size?: "thin" | "default" | "cell"
  /**
   * 0–100: a line across the track where the reading would matter — the
   * threshold an alert fires at, drawn against the reading it watches, so
   * "how close" is seen rather than worked out from two numbers.
   */
  mark?: number
  /**
   * What the figure measures. A bar with no accessible name is a decoration to a
   * screen reader; with one it is the same reading everyone else gets.
   */
  label?: string
  /**
   * The figure beside it already says the reading, so the bar is hidden from a
   * screen reader rather than read twice: a table cell's bar.
   */
  decorative?: boolean
  className?: string
}) {
  const pct = Math.max(0, Math.min(value, 100))
  const reading = decorative
    ? { "aria-hidden": true as const }
    : {
        role: "meter",
        "aria-valuenow": Math.round(pct),
        "aria-valuemin": 0,
        "aria-valuemax": 100,
        "aria-label": label,
      }
  return (
    <div
      {...reading}
      data-slot="meter"
      className={cn(
        "relative overflow-hidden rounded-full bg-meter-track",
        size === "cell" ? "h-1 w-8 shrink-0" : "w-full",
        size === "thin" ? "h-1" : size === "default" && "h-1.5",
        className,
      )}
    >
      <div
        className={cn(
          "h-full rounded-full transition-[width] duration-700 ease-out",
          !color && FILL[tone],
        )}
        style={{ width: `${pct}%`, backgroundColor: color }}
      />
      {mark !== undefined && (
        <span
          aria-hidden
          // The muted ink, solid: a line faded to 60% of the foreground was a
          // second grey the palette does not have.
          className="absolute inset-y-0 w-px bg-muted-foreground"
          // Held inside the track at 100, where `left: 100%` would put the
          // line just past the edge that clips it.
          style={{ left: `min(${Math.max(0, mark)}%, calc(100% - 1px))` }}
        />
      )}
    </div>
  )
}
