import { cn } from "@/lib/utils"
import { KindMark, kindLabel, kindOf } from "@/components/database/redis/kinds"

export type TypeShare = {
  /** The type as the server names it. */
  type: string
  /** What the type weighs against the others: bytes, or keys. */
  weight: number
  /** The weight as the reader sees it: "1.2 MB", "2,053". */
  figure: string
  /** What stands after the figure, quieter: "about 400 keys". */
  detail?: string
}

/**
 * What a database is made of, by key type: one track with a width per type,
 * each in its type's hue, and the same types named under it with their
 * figures.
 *
 * The hues are the legend the key browser draws a key's mark in, so the
 * violet of a hash is one colour on a chip, a row, a key's head and here.
 * Where the page can narrow to a type, the names under the bar are the
 * controls that do it.
 */
export function TypeComposition({
  shares,
  picked,
  onPick,
  className,
}: {
  shares: TypeShare[]
  /** The type the page is narrowed to, where it is. */
  picked?: string
  /** Narrow to a type; the one already picked is given again to clear it. */
  onPick?: (type: string) => void
  className?: string
}) {
  const whole = shares.reduce((sum, share) => sum + share.weight, 0)
  const percent = (share: TypeShare) =>
    whole > 0 ? `${((share.weight / whole) * 100).toFixed(1)}%` : "—"
  return (
    <div data-slot="redis-composition" className={cn("space-y-2.5", className)}>
      <div
        role="img"
        aria-label={shares.map((share) => `${kindLabel(share.type)} ${share.figure}`).join(", ")}
        className="flex h-2 w-full gap-px overflow-hidden rounded-full bg-meter-track"
      >
        {shares.map((share) => (
          <span
            key={share.type}
            className="h-full transition-[width]"
            style={{
              width: `${whole > 0 ? (share.weight / whole) * 100 : 0}%`,
              backgroundColor: kindOf(share.type).color,
            }}
          />
        ))}
      </div>
      <ul className="flex flex-wrap gap-x-5 gap-y-1.5">
        {shares.map((share) => {
          const words = (
            <>
              <KindMark type={share.type} className="size-3" />
              <span className="text-muted-foreground">{kindLabel(share.type)}</span>
              <span className="numeric font-medium text-foreground">{share.figure}</span>
              <span className="numeric text-muted-foreground">
                {percent(share)}
                {share.detail ? ` · ${share.detail}` : ""}
              </span>
            </>
          )
          return (
            <li key={share.type} className="flex min-w-0 text-hint">
              {onPick ? (
                <button
                  type="button"
                  aria-pressed={picked === share.type}
                  aria-label={`Show only ${kindLabel(share.type).toLowerCase()} keys: ${share.figure}`}
                  onClick={() => onPick(share.type)}
                  className={cn(
                    "-mx-1 flex min-w-0 items-center gap-1.5 rounded-sm px-1 py-0.5 focus-ring transition-colors hover:bg-row-hover",
                    picked === share.type && "bg-accent hover:bg-accent",
                  )}
                >
                  {words}
                </button>
              ) : (
                <span className="flex min-w-0 items-center gap-1.5">{words}</span>
              )}
            </li>
          )
        })}
      </ul>
    </div>
  )
}
