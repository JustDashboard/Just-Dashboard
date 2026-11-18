"use client"

import { useMemo } from "react"
import { bytes, compactCount } from "@/lib/format"
import { cn } from "@/lib/utils"
import { PanelHeader } from "@/components/panel"
import { RankList } from "@/components/rank-list"
import { Button } from "@/components/ui/button"
import { useDatabase } from "@/components/database/shell/database-context"
import { KindGlyph, ROW_GROUPS, ROW_OBJECT_KINDS } from "@/components/database/data/kinds"
import type { DbCatalog } from "@/components/database/data/types"
import { SchemaHead, largestTables } from "@/components/database/kit/schema-head"
import { schemaFacts } from "@/components/database/workbench/table-facts"

/**
 * What stands where a table would, before one is chosen: the schema, what it
 * holds said in one line, and its largest tables one press away.
 *
 * The place used to say "pick a table" in the middle of an empty frame, and
 * then said it as its title. The catalogue the rail has just read already
 * knows how many tables there are, about how many rows and how much of the
 * disk they take — so the head names the schema, as Schema's landing does
 * (`SchemaHead`), and the tables most likely to be the ones the reader came
 * for are named with their sizes, ranked by the same model.
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
  // The catalogue Data reads is the one Schema's tree reads; only the
  // statistics Schema adds are absent here.
  const figures = useMemo(() => largestTables(catalog), [catalog])
  const kindOf = (group: string) => ROW_GROUPS.find((entry) => entry.group === group)!.kind
  const name = catalog.schema || `This ${engine.nouns.container}`
  const counts = [
    ...(figures.tables > 0 ? [{ group: "tables" as const, count: figures.tables }] : []),
    ...figures.others,
  ].filter(({ group }) => ROW_GROUPS.some((entry) => entry.group === group))
  const facts = schemaFacts({
    counts: counts.map(({ group, count }) => {
      const kind = ROW_OBJECT_KINDS[kindOf(group)]
      return { key: group, count, one: kind.label.toLowerCase(), many: kind.plural.toLowerCase() }
    }),
    rows: figures.rows,
    size: figures.bytes,
  })

  return (
    <div className="min-h-0 flex-1 overflow-y-auto">
      <div
        className={cn(
          "mx-auto flex w-full max-w-xl animate-rise flex-col gap-6 px-6 py-10",
          // With no list under them the few figures stand in the middle of
          // the frame, not at the top of an empty one.
          figures.largest.length === 0 && "min-h-full justify-center",
        )}
      >
        <SchemaHead engine={engine} schema={catalog.schema} name={name} facts={facts} />

        {figures.largest.length > 0 && (
          <section aria-label={`Largest in ${name}`} className="min-w-0">
            <PanelHeader plain level={3} title={`Largest in ${name}`} />
            {/* Figures in a column and each one's share of the schema, not a
                bar under every name: bars of different lengths left the
                figures floating off the end of their rows. */}
            <RankList
              className="pt-1.5"
              items={figures.largest.map(({ object, group, share, rows, bytes: size }) => ({
                key: `${group}:${object.name}`,
                label: object.name,
                mark: <KindGlyph kind={kindOf(group)} />,
                hint:
                  figures.rankedBy === "size" && rows !== null
                    ? `~${compactCount(rows)} rows`
                    : undefined,
                value:
                  figures.rankedBy === "size" ? bytes(size ?? 0) : `~${compactCount(rows ?? 0)}`,
                share,
                title: `Open ${object.name}`,
                onClick: () => goto("data", { schema: object.schema || null, table: object.name }),
              }))}
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
