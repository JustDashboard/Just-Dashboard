"use client"

import { useMemo, useState } from "react"
import Link from "next/link"
import { CloudUpload, Plus, RefreshClockwise, Table } from "@/components/icons"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import type { PollState } from "@/hooks/use-poll"
import { IconAction } from "@/components/icon-action"
import { EmptyNote, EmptyState, ErrorState } from "@/components/state"
import { Stale } from "@/components/stale"
import { Button } from "@/components/ui/button"
import type { Verb } from "@/components/verbs"
import { ContainerPicker } from "@/components/database/kit/container-picker"
import { useDatabase } from "@/components/database/shell/database-context"
import {
  RAIL_ACTION,
  RailFind,
  RailGroupHead,
  RailHead,
  RailNoMatch,
  RailRow,
  RailSkeleton,
  RailTitle,
} from "@/components/database/workbench/rail"
import {
  KindGlyph,
  ROW_GROUPS,
  ROW_OBJECT_KINDS,
  type RowObjectKind,
} from "@/components/database/data/kinds"
import { ACTIONS_OF, actionsOf } from "@/components/database/data/table-verbs"
import type { DbCatalog, DbCatalogObject } from "@/components/database/data/types"
import { compactCount, grouped } from "@/lib/format"

/** How many rows of a group are drawn before the reader asks for more. */
const GROUP_STEP = 300

/** How the rail names a table to itself: the schema and the name, never the name alone. */
export function tableKey(schema: string, table: string) {
  return `${schema}\u0000${table}`
}

/**
 * The rail of the table editor: which schema, and what in it holds rows.
 *
 * Tables, views and materialized views are listed by kind, each with its
 * kind's glyph in its kind's hue and the engine's own estimate of its rows at
 * the right. A row is a link — it opens in a new tab, and choosing one is a
 * step of history, so Back returns to the table the reader came from.
 *
 * It lists what the catalogue says, in the schema the address names; an
 * address naming a schema that is not there says so and offers the one that
 * is, rather than drawing an empty list with nothing to press.
 */
export function TableRail({
  catalog,
  current,
  staged,
  verbsFor,
  onImportNew,
  saidBeside = false,
  className,
}: {
  catalog: PollState<DbCatalog>
  /** The table open beside the rail. */
  current: { schema: string; table: string } | null
  /** Tables holding unapplied changes, by `tableKey`, with how many. */
  staged: Readonly<Record<string, number>>
  verbsFor: (object: DbCatalogObject, kind: RowObjectKind) => Verb[]
  /** Opens "import a file as a new table"; absent where the role or the engine cannot. */
  onImportNew?: () => void
  /**
   * The page says beside the rail what is wrong — the list could not be read,
   * the schema is not there — with the way on: the rail does not say it twice.
   */
  saidBeside?: boolean
  className?: string
}) {
  const { engine, href, readOnly } = useDatabase()
  const { can } = useAuth()
  const [query, setQuery] = useState("")
  const data = catalog.data
  const needle = query.trim().toLowerCase()

  const groups = useMemo(
    () =>
      ROW_GROUPS.flatMap(({ group, kind }) => {
        const objects = data?.objects[group]
        if (!objects) return []
        const shown = needle
          ? objects.filter((object) => object.name.toLowerCase().includes(needle))
          : objects
        return [{ group, kind, objects: shown, total: objects.length }]
      }),
    [data, needle],
  )
  const total = groups.reduce((sum, entry) => sum + entry.total, 0)
  const matched = groups.reduce((sum, entry) => sum + entry.objects.length, 0)

  const schema = data?.schema ?? ""
  const known = !data || data.schemas.length === 0 || data.schemas.some((s) => s.name === schema)
  const canCreate =
    can("service.control") && !readOnly && engine.capabilities.ddlOperations.includes("createTable")
  const newTable = href("schema", { schema: schema || null, table: null, new: "table" })

  return (
    <nav
      data-slot="table-rail"
      aria-label={`${engine.nouns.objects} of this ${engine.nouns.container}`}
      className={cn("flex min-h-0 min-w-0 flex-1 flex-col", className)}
    >
      <RailHead>
        {engine.can("schemas") && data ? (
          <ContainerPicker
            items={data.schemas.map((entry) => ({
              name: entry.name,
              system: entry.system,
              figure: entry.tables >= 0 ? grouped(entry.tables) : undefined,
            }))}
            current={schema}
            noun={engine.nouns.container}
            nouns={engine.nouns.containers}
            hrefOf={(name) => href("data", { schema: name, table: null })}
          />
        ) : (
          <RailTitle className="capitalize">{engine.nouns.objects}</RailTitle>
        )}
        {canCreate && known && (
          <IconAction label={`New ${engine.nouns.object}`} className={RAIL_ACTION} asChild>
            <Link href={newTable}>
              <Plus />
            </Link>
          </IconAction>
        )}
        {onImportNew && known && (
          <IconAction
            label={`Import a file as a new ${engine.nouns.object}`}
            className={RAIL_ACTION}
            onClick={onImportNew}
          >
            <CloudUpload />
          </IconAction>
        )}
        <IconAction
          label={`Read the ${engine.nouns.objects} again`}
          className={RAIL_ACTION}
          onClick={catalog.refresh}
        >
          <RefreshClockwise />
        </IconAction>
      </RailHead>
      {/* The list is the last reading: the read just asked for failed. */}
      <Stale
        form="strip"
        error={data ? catalog.error : undefined}
        subject={`the ${engine.nouns.objects}`}
        onRetry={catalog.refresh}
      />
      <RailFind value={query} onChange={setQuery} placeholder={`Find a ${engine.nouns.object}`} />

      <div className="min-h-0 flex-1 overflow-y-auto px-1.5 pb-2">
        {!data && catalog.error ? (
          saidBeside ? (
            <EmptyNote className="px-2 py-6">
              The {engine.nouns.objects} could not be read.
            </EmptyNote>
          ) : (
            <ErrorState error={catalog.error} onRetry={catalog.refresh} className="m-1" />
          )
        ) : !data ? (
          <RailSkeleton groups={2} />
        ) : !known ? (
          saidBeside ? (
            <EmptyNote className="px-2 py-6">Nothing is listed.</EmptyNote>
          ) : (
            <EmptyState
              className="mt-2 border-0 px-2 py-8"
              icon={Table}
              title={`No ${engine.nouns.container} called ${schema}`}
              description={`This connection has no such ${engine.nouns.container}. It may have been dropped since the link was made.`}
              action={
                <Button size="sm" variant="outline" asChild>
                  <Link href={href("data", { schema: data.defaultSchema || null, table: null })}>
                    Open {data.defaultSchema || `the default ${engine.nouns.container}`}
                  </Link>
                </Button>
              }
            />
          )
        ) : total === 0 ? (
          <EmptyState
            className="mt-2 border-0 px-2 py-8"
            icon={Table}
            title={`No ${engine.nouns.objects} in ${schema || `this ${engine.nouns.container}`}`}
            description={
              canCreate
                ? `Create one on the Schema page, or pick another ${engine.nouns.container} above.`
                : `Nothing in this ${engine.nouns.container} holds rows yet.`
            }
            action={
              canCreate && (
                <div className="flex flex-wrap justify-center gap-2">
                  <Button size="sm" variant="outline" asChild>
                    <Link href={newTable}>
                      <Plus />
                      New {engine.nouns.object}
                    </Link>
                  </Button>
                  {onImportNew && (
                    <Button size="sm" variant="outline" onClick={onImportNew}>
                      <CloudUpload />
                      Import a file
                    </Button>
                  )}
                </div>
              )
            }
          />
        ) : matched === 0 ? (
          <RailNoMatch onClear={() => setQuery("")} />
        ) : (
          groups.map(({ group, kind, objects, total: all }) =>
            // A kind the engine has and this schema does not is not listed while searching.
            objects.length === 0 && (needle || all === 0) ? null : (
              <RailGroup
                key={group}
                kind={kind}
                objects={objects}
                current={current}
                staged={staged}
                verbsFor={verbsFor}
                error={data.errors?.[group]}
                truncated={data.truncated.includes(group) ? data.limit : undefined}
              />
            ),
          )
        )}
      </div>
    </nav>
  )
}

function RailGroup({
  kind,
  objects,
  current,
  staged,
  verbsFor,
  error,
  truncated,
}: {
  kind: RowObjectKind
  objects: DbCatalogObject[]
  current: { schema: string; table: string } | null
  staged: Readonly<Record<string, number>>
  verbsFor: (object: DbCatalogObject, kind: RowObjectKind) => Verb[]
  error?: string
  truncated?: number
}) {
  const { href } = useDatabase()
  const [limit, setLimit] = useState(GROUP_STEP)
  const spec = ROW_OBJECT_KINDS[kind]
  const shown = objects.slice(0, limit)
  return (
    <section aria-label={spec.plural} className="pt-1.5">
      {/* The count stands at the row's end, over the rows' own figures, as the
          Schema page's tree draws it. */}
      <RailGroupHead label={spec.plural} count={grouped(objects.length)} />
      {error && <EmptyNote className="px-2 py-2 text-left text-hint">Not read: {error}</EmptyNote>}
      <ul>
        {shown.map((object) => {
          const selected =
            current !== null && current.table === object.name && current.schema === object.schema
          const rows = object.estimatedRows
          const counted = rows !== undefined && rows >= 0
          return (
            <RailRow
              key={`${object.schema}.${object.name}`}
              href={href("data", { schema: object.schema, table: object.name })}
              current={selected}
              title={object.comment || object.detail || undefined}
              mark={<KindGlyph kind={kind} />}
              name={object.name}
              staged={{
                count: staged[tableKey(object.schema, object.name)] ?? 0,
                label: "unapplied changes",
              }}
              figures={
                counted
                  ? [
                      {
                        key: "rows",
                        text: compactCount(rows),
                        title: `About ${grouped(rows)} rows`,
                      },
                    ]
                  : []
              }
              verbs={verbsFor(object, kind)}
              menuLabel={`Actions for ${object.name}`}
              triggerProps={{ [ACTIONS_OF]: actionsOf(object) }}
            />
          )
        })}
      </ul>
      {objects.length > limit && (
        <Button
          size="xs"
          variant="ghost"
          className="mt-1 ml-1 text-muted-foreground"
          onClick={() => setLimit((n) => n + GROUP_STEP)}
        >
          Show {Math.min(GROUP_STEP, objects.length - limit)} more of {grouped(objects.length)}
        </Button>
      )}
      {truncated !== undefined && (
        <EmptyNote className="px-2 py-2 text-left text-hint">
          Only the first {grouped(truncated)} are listed.
        </EmptyNote>
      )}
    </section>
  )
}
