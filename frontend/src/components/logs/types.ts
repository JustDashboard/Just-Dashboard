import type { LogLevel } from "@/lib/log-filter"

/**
 * Field predicates, by key: `{ event: ["auth_failed"], client: ["!10.0.0.2"] }`.
 * Each value is one `f=key:value` on the wire, in the grammar `lib/log-filter.ts`
 * reads — exact, `!`, `~`, `*`, `>`/`<` — so the values of one key keep their
 * own operators rather than the map pretending they are all "equals".
 */
export type LogFields = Record<string, string[]>

/**
 * What the operator has narrowed to. Live and Search are two questions about
 * the same thing — what is happening now, and what happened — so they share one
 * filter: seeing errors scroll past and asking when they started is a mode
 * switch, not a form to fill in again.
 *
 * `fields` is optional because a filter stored before it existed has none, and
 * a session restored from that must not throw on its first read. Nothing reads
 * it directly: `fieldsOf` is the one way in.
 */
export type LogFilterState = {
  q: string
  exclude: string
  regex: boolean
  ignoreCase: boolean
  levels: LogLevel[]
  fields?: LogFields
}

/**
 * Which reading of the source is on screen. "search" is History's id, kept
 * because stored sessions and the links a deployment run hands out already
 * say it; a page's own views (Events, Runs, Queries) are modes too, by their
 * ids.
 */
export type LogMode = "live" | "search" | "insights" | (string & {})

export type LogTimeRange = "15m" | "1h" | "6h" | "24h" | "7d" | "all" | "custom"
