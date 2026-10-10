import { driftReading, type DriftMove } from "@/lib/network-drift"
import { plural, relativeTime } from "@/lib/format"
import { StatusDot } from "@/components/status-dot"

/**
 * What moved while the page was open: each row whose status was different at
 * the previous inspection, with the two readings and when the later one was
 * made. Nothing here is invented to fill the space — a host that has not
 * drifted says how many inspections it has stood through.
 */
export function DriftRecent({ moves, inspections }: { moves: DriftMove[]; inspections: number }) {
  return (
    <section aria-label="Recent changes" className="min-w-0 py-1 xl:py-6">
      <h2 className="eyebrow pb-3">Since this page opened</h2>
      {moves.length === 0 ? (
        <p className="text-body text-muted-foreground">
          <span className="numeric">{plural(inspections, "inspection")}</span> and nothing has
          changed.
        </p>
      ) : (
        <ul className="divide-y divide-hairline">
          {moves.map((move, index) => {
            const to = driftReading(move.to)
            return (
              <li key={`${move.id}:${move.at}:${index}`} className="animate-rise py-2">
                <p className="min-w-0 truncate font-mono text-body" title={move.resource}>
                  {move.resource}
                </p>
                <p className="flex items-center gap-1.5 text-hint text-muted-foreground">
                  {driftReading(move.from).label}
                  <span aria-hidden>→</span>
                  <StatusDot tone={to.tone} />
                  <span className="text-foreground">{to.label}</span>
                  <span className="ml-auto">{relativeTime(new Date(move.at).toISOString())}</span>
                </p>
              </li>
            )
          })}
        </ul>
      )}
    </section>
  )
}
