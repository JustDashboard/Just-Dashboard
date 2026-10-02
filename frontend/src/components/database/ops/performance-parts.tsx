"use client"

import Link from "next/link"
import { ArrowDown, ArrowUp } from "@/components/icons"
import { cn } from "@/lib/utils"
import type { PollState } from "@/hooks/use-poll"
import { FormNote } from "@/components/form"
import { EmptyState } from "@/components/state"
import { Status, type DotTone } from "@/components/status-dot"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { TableHead } from "@/components/ui/table"
import { CouldNotRead, NotUpdating } from "@/components/database/home/blocks"
import { nameHue, statementVerb } from "@/components/database/home/kinds"
import { EngineMark } from "@/components/database/kit"
import { oneLine } from "@/components/database/ops/queries"
import { SESSION_STATES } from "@/components/database/ops/performance-activity"
import type { DbSessionStatus } from "@/components/database/ops/performance-types"
import { useDatabase } from "@/components/database/shell/database-context"
import { statusLabel } from "@/components/database/shell/status"

/**
 * The small pieces the Performance views share, so that a name, a statement,
 * a session's state and a read that failed look the same in every one of
 * them.
 */

/**
 * The three answers of one view's read: what it holds once it has answered —
 * and for as long as it has, whatever the polls after it do — the failure
 * when it never has, with the way to ask again, and the silhouette of what
 * is coming until either.
 */
export function ViewRead<T>({
  poll,
  what,
  skeleton,
  children,
}: {
  poll: PollState<T>
  /** What the view would have shown: "the sessions". */
  what: string
  skeleton: React.ReactNode
  children: (data: T) => React.ReactNode
}) {
  if (poll.data !== undefined) return <>{children(poll.data)}</>
  if (poll.error) return <CouldNotRead what={what} error={poll.error} onRetry={poll.refresh} />
  return <>{skeleton}</>
}

/** The mark a view's header carries while its last poll has failed over what is drawn. */
export function Stale<T>({ poll }: { poll: PollState<T> }) {
  return poll.data !== undefined && poll.error ? <NotUpdating error={poll.error} /> : null
}

/**
 * A reading this server, or this account on it, does not give: the engine
 * drawn as itself, what is missing, and the server's own reason — which is
 * where the grant to ask for is named.
 */
export function NotAvailable({
  title,
  reason,
  action,
}: {
  title: string
  reason?: React.ReactNode
  action?: React.ReactNode
}) {
  const { engine } = useDatabase()
  return (
    <EmptyState
      mark={<EngineMark engine={engine} size="md" />}
      title={title}
      description={reason && <span className="wrap-anywhere">{reason}</span>}
      action={action}
    />
  )
}

/** The parts of a read the engine refused or cannot give, in its own sentences. */
export function Notes({ notes, className }: { notes?: string[]; className?: string }) {
  if (!notes?.length) return null
  return (
    <div className={cn("space-y-1", className)}>
      {notes.map((note) => (
        <FormNote key={note} className="wrap-anywhere">
          {note}
        </FormNote>
      ))}
    </div>
  )
}

/** A name — an account, an application, a schema — in its own hue, the same on every page that prints it. */
export function Named({
  name,
  mono,
  className,
}: {
  name: string
  mono?: boolean
  className?: string
}) {
  return (
    <span
      title={name}
      className={cn("min-w-0 truncate", mono && "font-mono", className)}
      style={{ color: nameHue(name) }}
    >
      {name}
    </span>
  )
}

/** A table by its schema and name: the schema in its hue, the name in ink. */
export function ObjectName({
  schema,
  name,
  className,
}: {
  schema?: string
  name: string
  className?: string
}) {
  return (
    <span className={cn("min-w-0 truncate font-mono", className)} title={`${schema ?? ""}.${name}`}>
      {schema && (
        <>
          <span style={{ color: nameHue(schema) }}>{schema}</span>
          <span className="text-muted-foreground">.</span>
        </>
      )}
      {name}
    </span>
  )
}

/**
 * A statement on one line, its first word in the hue of what it does — the
 * home's legend, so a list of statements is told apart before one is read.
 */
export function StatementLine({ text, className }: { text: string; className?: string }) {
  const line = oneLine(text)
  const verb = statementVerb(line)
  return (
    <span className={cn("block min-w-0 truncate font-mono", className)} title={line}>
      {verb ? (
        <>
          <span style={{ color: verb.color }}>{verb.word}</span>
          {verb.rest}
        </>
      ) : (
        line
      )}
    </span>
  )
}

const STATE_TONE: Record<DbSessionStatus, DotTone> = {
  active: "running",
  blocked: "warning",
  idle_in_transaction: "notice",
  idle: "stopped",
  background: "unknown",
}

/**
 * A session's state as a dot and a word. `late` is a state that has gone on
 * past its threshold: a blocked session then reads red, and one idle in a
 * transaction amber.
 */
export function SessionState({ status, late }: { status: DbSessionStatus; late?: boolean }) {
  const label = SESSION_STATES.find((state) => state.id === status)?.label ?? status
  const tone: DotTone =
    late && status === "blocked"
      ? "danger"
      : late && status === "idle_in_transaction"
        ? "warning"
        : STATE_TONE[status]
  return <Status tone={tone} label={label} />
}

/**
 * A column's head that orders the rows under it. The head says which way the
 * column runs (`aria-sort`), and the arrow is drawn only on the one in use.
 */
export function SortHead({
  label,
  active,
  descending,
  onSort,
  align = "left",
  className,
}: {
  label: string
  active: boolean
  descending: boolean
  onSort: () => void
  align?: "left" | "right"
  className?: string
}) {
  const Arrow = descending ? ArrowDown : ArrowUp
  return (
    <TableHead
      aria-sort={active ? (descending ? "descending" : "ascending") : "none"}
      className={cn(align === "right" && "text-right", className)}
    >
      <button
        type="button"
        onClick={onSort}
        className={cn(
          "inline-flex items-center gap-1 rounded-sm focus-ring transition-colors hover:text-foreground",
          active && "text-foreground",
        )}
      >
        {label}
        {active && <Arrow aria-hidden className="size-3" />}
      </button>
    </TableHead>
  )
}

/** A dash where a row has no figure: the engine does not count it. */
export function NoFigure() {
  return <span className="text-muted-foreground/50">—</span>
}

/**
 * Which schema a list is narrowed to, as chips: every schema the list spans,
 * with how many of its rows each holds. Drawn only where there is more than
 * one, and "All" is always among them — so a list is never narrowed by
 * something the page does not show.
 */
export function ScopeChips({
  schemas,
  scope,
  onScope,
  noun,
}: {
  schemas: { name: string; count: number }[]
  /** The schema chosen; empty is every schema. */
  scope: string
  onScope: (schema: string) => void
  /** What the engine calls a schema, in the plural: "schemas", "databases". */
  noun: string
}) {
  if (schemas.length < 2) return null
  const total = schemas.reduce((sum, schema) => sum + schema.count, 0)
  return (
    <ChipStrip role="group" aria-label={`Narrow to one of the ${noun}`}>
      <FilterChip selected={!scope} onClick={() => onScope("")}>
        All {noun}
        <ChipCount>{total.toLocaleString()}</ChipCount>
      </FilterChip>
      {schemas.map((schema) => (
        <FilterChip
          key={schema.name}
          selected={scope === schema.name}
          onClick={() => onScope(scope === schema.name ? "" : schema.name)}
        >
          <span className="font-mono" style={{ color: nameHue(schema.name) }}>
            {schema.name}
          </span>
          <ChipCount>{schema.count.toLocaleString()}</ChipCount>
        </FilterChip>
      ))}
    </ChipStrip>
  )
}

/** Whether the server is known not to be there to ask: stopped, paused, or a connection that cannot be opened. */
export function isDown(state: string): boolean {
  return state === "stopped" || state === "paused" || state === "broken"
}

/**
 * A page that reads the live server, on a server that is not answering. It
 * is asked nothing — a stopped server is not dialled by its own pages — and
 * the page says so, and where the server is started from.
 */
export function ServerDown({ what }: { what: string }) {
  const { conn, engine, status, href } = useDatabase()
  return (
    <EmptyState
      mark={<EngineMark engine={engine} size="md" />}
      title={`${conn.name} is ${statusLabel(status.state)}`}
      description={
        <>
          {what} is read from the running server, so there is nothing to show until it answers
          again.{status.error ? <span className="wrap-anywhere"> {status.error}</span> : null}
        </>
      }
      action={
        <span className="flex flex-wrap justify-center gap-2">
          <Button size="sm" variant="outline" asChild>
            <Link href={href("home")}>Open Home</Link>
          </Button>
          <Button size="sm" variant="ghost" onClick={status.refresh}>
            Check again
          </Button>
        </span>
      }
    />
  )
}
