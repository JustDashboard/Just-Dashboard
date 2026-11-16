import { SplitBar } from "@/components/rank-list"
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
 * What a database is made of, by key type: the shared `SplitBar`, a width
 * per type in its type's hue, and the same types named under it with their
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
    <div data-slot="redis-composition" className={className}>
      <SplitBar
        label={shares.map((share) => `${kindLabel(share.type)} ${share.figure}`).join(", ")}
        legend={onPick ? "pick" : "static"}
        picked={picked}
        onPick={onPick}
        parts={shares.map((share) => ({
          key: share.type,
          count: share.weight,
          color: kindOf(share.type).color,
          mark: <KindMark type={share.type} className="size-3" />,
          label: kindLabel(share.type),
          figure: share.figure,
          note: `${percent(share)}${share.detail ? ` · ${share.detail}` : ""}`,
          pickLabel: `Show only ${kindLabel(share.type).toLowerCase()} keys: ${share.figure}`,
        }))}
      />
    </div>
  )
}
