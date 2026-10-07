"use client"

import { ChevronDown } from "@/components/icons"
import { rowReveal } from "@/components/icon-action"
import { cn } from "@/lib/utils"

/**
 * The share each rank takes of its measurement's hue, heaviest darkest. The
 * workloads are told apart by their marks and names; the bar's colour says
 * which measurement it is, as every chart of it does (§10), and the steps say
 * which part of the bar a row is.
 */
const STEPS = [100, 74, 54, 40, 30]

export function shade(color: string, rank: number) {
  return `color-mix(in oklab, ${color} ${STEPS[rank] ?? 24}%, transparent)`
}

export type BandPart = { key: string; value: number; color: string; label: string }

/**
 * One bar made of what is moving the bytes: a span per program or container,
 * heaviest first, then everything else muted, the whole as wide as their sum.
 * It is the Processes page's workload bar (`procs/workloads.tsx`) turned to
 * traffic, and like it each span eases to its next width rather than jumping,
 * so a poll that changes who is busiest is seen to happen.
 *
 * The bar is a share, not a fraction of the link: a program moving two
 * megabytes a second on a gigabit uplink would be a hairline, and the question
 * this answers is *who*, which the link's speed cannot rank.
 */
export function ShareBar({
  label,
  rest,
  parts,
  format,
}: {
  label: string
  /** Everything not named, in the same unit as the parts. */
  rest: number
  parts: BandPart[]
  format: (value: number) => string
}) {
  const total = parts.reduce((n, p) => n + p.value, 0) + rest
  const width = (value: number) => (total > 0 ? `${Math.min((value / total) * 100, 100)}%` : "0%")
  return (
    <div
      role="img"
      aria-label={`${label}: ${parts.map((p) => p.label).join(", ")}${rest > 0 ? `, everything else ${format(rest)}` : ""}`}
      className="flex h-2.5 w-full overflow-hidden rounded-sm bg-meter-track"
    >
      {parts.map((part) => (
        <span
          key={part.key}
          title={part.label}
          className="h-full shrink-0 border-r border-background transition-[width] duration-700 ease-out last:border-r-0"
          style={{ width: width(part.value), background: part.color }}
        />
      ))}
      {rest > 0 && (
        <span
          title={`Everything else ${format(rest)}`}
          className="h-full shrink-0 bg-muted-foreground/25 transition-[width] duration-700 ease-out"
          style={{ width: width(rest) }}
        />
      )}
    </div>
  )
}

/**
 * One named span of a bar, a line: the key that finds it on the bar, its
 * mark, its name and what it is doing, its figure at the end. With `onPress`
 * the row opens something under the band — the chevron says so, the way the
 * Processes page's funnel says a press narrows — and `pressed` is the one open.
 */
export function BandRow({
  color,
  mark,
  name,
  detail,
  figure,
  middle,
  pressed,
  onPress,
  label,
}: {
  color: string
  mark: React.ReactNode
  name: string
  detail?: React.ReactNode
  figure: React.ReactNode
  /** Something between the detail and the figure: a container's hour. */
  middle?: React.ReactNode
  pressed?: boolean
  onPress?: () => void
  /** The accessible name of the press. */
  label?: string
}) {
  const body = (
    <>
      <span
        aria-hidden
        className="h-2.5 w-0.5 shrink-0 rounded-full"
        style={{ background: color }}
      />
      <span className="flex size-4 shrink-0 items-center justify-center">{mark}</span>
      <span className="min-w-0 truncate font-medium">{name}</span>
      {detail && (
        <span className="numeric shrink-0 truncate text-hint text-muted-foreground">{detail}</span>
      )}
      <span className="ml-auto flex shrink-0 items-center gap-2.5">
        {middle}
        <span className="numeric min-w-16 text-right font-medium text-foreground">{figure}</span>
      </span>
      {onPress && (
        <ChevronDown
          aria-hidden
          className={cn(
            "size-3.5 shrink-0 text-muted-foreground transition-transform",
            pressed ? "rotate-180 text-foreground" : rowReveal(),
          )}
        />
      )}
    </>
  )
  const face =
    "group flex h-8 w-full min-w-0 items-center gap-2.5 rounded-md px-2 text-left text-body"
  return (
    <li>
      {onPress ? (
        <button
          type="button"
          aria-pressed={pressed}
          aria-label={label}
          onClick={onPress}
          className={cn(
            face,
            "focus-ring-inset transition-colors",
            pressed ? "bg-accent" : "hover:bg-row-hover",
          )}
        >
          {body}
        </button>
      ) : (
        <div className={face}>{body}</div>
      )}
    </li>
  )
}
