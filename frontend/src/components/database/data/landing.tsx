"use client"

import { useMemo } from "react"
import { bytes } from "@/lib/format"
import { cn } from "@/lib/utils"
import { RankList } from "@/components/rank-list"
import { Button } from "@/components/ui/button"
import { EngineMark } from "@/components/database/kit"
import { useDatabase } from "@/components/database/shell/database-context"
import { KindGlyph, ROW_GROUPS, ROW_OBJECT_KINDS } from "@/components/database/data/kinds"
import { schemaSummary } from "@/components/database/data/summary"
import type { DbCatalog } from "@/components/database/data/types"
import { compactCount } from "@/components/database/data/view"
import { FactsLine } from "@/components/database/workbench/facts-line"
import { schemaFacts } from "@/components/database/workbench/table-facts"

/**
 * What stands where a table would, before one is chosen: what the schema
 * holds, said in one line, and its largest tables one press away.
 *
 * The place used to say "pick a table" in the middle of an empty frame. The
 * catalogue the rail has just read already knows how many tables there are,
 * about how many rows and how much of the disk they take — so the first thing
 * a reader sees on this page is the database, not a blank, and the tables
 * most likely to be the ones they came for are named with their sizes.
 */
export function SchemaLanding({
  catalog,
  railShown,
  onShowRail,
}: {
  catalog: DbCatalog
  /** The rail is on screen beside this. */
  railShown: boolean
  onShowRail: () => void
}) {
  const { engine, goto } = useDatabase()
  const summary = useMemo(() => schemaSummary(catalog), [catalog])
  const kindOf = (group: string) => ROW_GROUPS.find((entry) => entry.group === group)!.kind
  const name = catalog.schema || `This ${engine.nouns.container}`
  const facts = schemaFacts({
    counts: summary.counts.map(({ group, count }) => {
      const kind = ROW_OBJECT_KINDS[kindOf(group)]
      return { key: group, count, one: kind.label.toLowerCase(), many: kind.plural.toLowerCase() }
    }),
    rows: summary.rows,
    size: summary.size,
  })

  return (
    <div className="min-h-0 flex-1 overflow-y-auto">
      <div
        className={cn(
          "mx-auto flex w-full max-w-xl animate-rise flex-col gap-6 px-6 py-10",
          // With no list under them the few figures stand in the middle of
          // the frame, not at the top of an empty one.
          summary.largest.length === 0 && "min-h-full justify-center",
        )}
      >
        <div className="flex min-w-0 items-center gap-3">
          <EngineMark engine={engine} />
          <div className="min-w-0">
            <h2 className="truncate text-title font-medium">Pick a {engine.nouns.object}</h2>
            {/* What the schema holds, where a sentence about the page stood
                over a strip of the same figures as tiles. */}
            <FactsLine facts={facts} />
          </div>
        </div>

        {summary.largest.length > 0 && (
          <section aria-label={`Largest in ${name}`} className="min-w-0">
            <h3 className="border-b border-hairline pb-2 text-title font-medium">
              Largest in {name}
            </h3>
            {/* Figures in a column and each one's share of the schema, not a
                bar under every name: bars of different lengths left the
                figures floating off the end of their rows. */}
            <RankList
              className="pt-1.5"
              items={summary.largest.map(({ object, group, share }) => {
                const rows = object.estimatedRows
                const counted = rows !== undefined && rows >= 0
                return {
                  key: `${group}:${object.name}`,
                  label: object.name,
                  mark: <KindGlyph kind={kindOf(group)} />,
                  hint:
                    summary.rankedBy === "size" && counted
                      ? `~${compactCount(rows)} rows`
                      : undefined,
                  value:
                    summary.rankedBy === "size"
                      ? bytes(object.size ?? 0)
                      : `~${compactCount(rows ?? 0)}`,
                  share,
                  title: `Open ${object.name}`,
                  onClick: () =>
                    goto("data", { schema: object.schema || null, table: object.name }),
                }
              })}
            />
          </section>
        )}

        {!railShown && (
          <div>
            <Button size="sm" variant="outline" onClick={onShowRail}>
              Show the {engine.nouns.objects}
            </Button>
          </div>
        )}
      </div>
    </div>
  )
}
