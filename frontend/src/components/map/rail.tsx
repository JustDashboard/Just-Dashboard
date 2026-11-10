"use client"

import { StatusDot } from "@/components/status-dot"
import { systemCensus, systemTitle, type System } from "@/lib/server-systems"
import { cn } from "@/lib/utils"
import type { MapBoard } from "@/components/map/board"
import { SystemMark } from "@/components/map/inspector"
import { TONE_DOT } from "@/components/map/marks"

/** What a system holds, in a few words: "3 containers · 1 database · 2 domains". */
export function censusLine(system: System, most = 3) {
  const c = systemCensus(system)
  const parts: string[] = []
  const say = (n: number, one: string, many: string) => {
    if (n > 0) parts.push(`${n} ${n === 1 ? one : many}`)
  }
  say(c.containers, "container", "containers")
  say(c.databases, "database", "databases")
  say(c.domains, "domain", "domains")
  say(c.volumes, "volume", "volumes")
  say(c.backups, "backup", "backups")
  if (parts.length === 0)
    return `${system.nodes.length} ${system.nodes.length === 1 ? "thing" : "things"}`
  return parts.slice(0, most).join(" · ")
}

/**
 * Every system on the canvas, a row each, grouped the board's way — the
 * server map's by what anchors a system, the databases map's by engine: the
 * contents of the canvas in the order a reader looks for them. A row flies the
 * canvas to its system; the one being read is marked.
 */
export function SystemRail({
  board,
  systems,
  focus,
  onFocus,
}: {
  board: MapBoard
  systems: System[]
  focus: string | null
  onFocus: (id: string) => void
}) {
  return (
    <nav
      aria-label={board.rail.label}
      className="flex min-h-0 min-w-0 flex-col border-r border-hairline"
    >
      <div className="min-h-0 flex-1 overflow-y-auto px-2 py-2">
        {systems.length === 0 && (
          <p className="px-2 py-3 text-hint text-muted-foreground">{board.rail.empty}</p>
        )}
        {board.rail.group(systems).map((group) => {
          const here = group.systems
          if (here.length === 0) return null
          return (
            <section key={group.label} className="mb-3">
              <h3 className="eyebrow px-2 pt-1 pb-1.5">{group.label}</h3>
              <ul className="space-y-0.5">
                {here.map((s) => (
                  <li key={s.id}>
                    <button
                      type="button"
                      onClick={() => onFocus(s.id)}
                      aria-current={focus === s.id ? "true" : undefined}
                      data-system={s.id}
                      className={cn(
                        "flex w-full min-w-0 items-center gap-2.5 rounded-lg px-2 py-1.5 text-left focus-ring transition-colors hover:bg-row-hover",
                        focus === s.id && "bg-row-hover",
                      )}
                    >
                      <SystemMark system={s} />
                      <span className="min-w-0 flex-1">
                        <span
                          className={cn(
                            "block truncate text-body font-medium",
                            focus === s.id ? "text-foreground" : "text-foreground/90",
                          )}
                        >
                          {systemTitle(s)}
                        </span>
                        <span className="block truncate text-hint text-muted-foreground">
                          {board.census(s, 2)}
                        </span>
                      </span>
                      {s.kind !== "loose" && <StatusDot tone={TONE_DOT[s.anchor.tone]} />}
                    </button>
                  </li>
                ))}
              </ul>
            </section>
          )
        })}
      </div>
    </nav>
  )
}
