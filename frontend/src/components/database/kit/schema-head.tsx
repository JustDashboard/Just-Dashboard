"use client"

import { cn } from "@/lib/utils"
import { RankSkeleton } from "@/components/rank-list"
import { Skeleton } from "@/components/ui/skeleton"
import type { Engine } from "@/components/database/engine"
import { EngineMark } from "@/components/database/kit/engine-mark"
import { NameMark } from "@/components/database/kit/marks"
import { FactsLine } from "@/components/database/workbench/facts-line"
import type { Fact } from "@/components/database/workbench/table-facts"

export {
  schemaFigures as largestTables,
  type LargeObject,
  type SchemaFigures,
} from "@/components/database/schema/landing-figures"

/**
 * The head of a schema where an object would stand before one is chosen —
 * Data's landing and Schema's: the engine's mark, the schema's name with its
 * own colour, one line of what it holds, and the verbs that act on it.
 *
 * The two landings had built it two ways one click apart: Data's titled with
 * an instruction ("Pick a table") over a `FactsLine`, Schema's with the name
 * over facts joined by hand into a plain line. Both now say what the place
 * is, the way every other page's head does; the rail beside it is how a
 * table is picked. What each page shows under the head stays its own.
 */
export function SchemaHead({
  engine,
  schema,
  name,
  facts,
  line,
  verbs,
  className,
}: {
  engine: Engine
  /** The schema's own name, for its mark; empty for a connection with none. */
  schema: string
  /** What the head says: the schema's name, or the engine's word for where it is. */
  name: string
  facts?: readonly Fact[]
  /** A sentence in place of the facts, where there are none to say. */
  line?: React.ReactNode
  verbs?: React.ReactNode
  className?: string
}) {
  return (
    <div
      data-slot="schema-head"
      className={cn("flex min-w-0 flex-wrap items-center gap-3", className)}
    >
      <EngineMark engine={engine} />
      <div className="min-w-0 flex-1">
        <h2 className="flex min-w-0 items-center gap-2 font-mono text-title font-medium">
          {schema && <NameMark name={schema} />}
          <span className="truncate">{name}</span>
        </h2>
        {facts && facts.length > 0 ? (
          <FactsLine facts={facts} />
        ) : (
          line && <p className="truncate text-body text-muted-foreground">{line}</p>
        )}
      </div>
      {verbs && <div className="flex shrink-0 items-center gap-1.5">{verbs}</div>}
    </div>
  )
}

/**
 * The landing's shape while the catalogue is read: the head, then the
 * largest tables as `RankSkeleton` — names and figures in a column, never a
 * bar under each name, which is the list the landing no longer draws.
 */
export function SchemaHeadSkeleton({
  label,
  className,
}: {
  /** What is being read, for a screen reader: "Reading the tables". */
  label: string
  className?: string
}) {
  return (
    <div role="status" aria-label={label} className={cn("w-full space-y-6", className)}>
      <div className="flex items-center gap-3">
        <Skeleton className="size-10 rounded-lg" />
        <div className="space-y-2">
          <Skeleton className="h-3.5 w-28" />
          <Skeleton className="h-3 w-64 max-w-full" />
        </div>
      </div>
      <div className="space-y-2">
        <Skeleton className="h-3.5 w-36" />
        <RankSkeleton rows={4} />
      </div>
    </div>
  )
}
