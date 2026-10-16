import { ShareBar } from "@/components/procs/workloads"

export type Standing = { key: string; count: number; color: string; word: string }

/**
 * How many of a block's things stand where — backed up today or never,
 * valid or expired — as spans of one bar in their status hue, with each
 * count named under it. It replaced a track under every row with a dot on
 * it: the track had no scale, so where the dot sat said less than the age
 * written beside it, and a thing never backed up was drawn at a place it
 * did not have.
 */
export function StandingBar({ label, parts }: { label: string; parts: Standing[] }) {
  const shown = parts.filter((part) => part.count > 0)
  const total = shown.reduce((sum, part) => sum + part.count, 0)
  if (total === 0) return null
  return (
    <div className="space-y-2">
      <ShareBar
        label={label}
        capacity={total}
        rest={0}
        parts={shown.map((part) => ({
          key: part.key,
          value: part.count,
          color: part.color,
          label: `${part.count} ${part.word}`,
        }))}
        format={String}
      />
      <p className="flex flex-wrap items-center gap-x-3 gap-y-1 text-hint text-muted-foreground">
        {shown.map((part) => (
          <span key={part.key} className="inline-flex items-center gap-1.5">
            <span
              aria-hidden
              className="size-2 shrink-0 rounded-[2px]"
              style={{ background: part.color }}
            />
            <span className="numeric font-medium text-foreground">{part.count}</span>
            {part.word}
          </span>
        ))}
      </p>
    </div>
  )
}
