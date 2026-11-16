"use client"

import { useMemo, useState } from "react"
import Link from "next/link"
import { MoreHorizontal, Plus, RefreshClockwise } from "@/components/icons"
import type { DbCatalogGroup } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useViewState } from "@/lib/view-state"
import type { PollState } from "@/hooks/use-poll"
import { IconAction } from "@/components/icon-action"
import { EmptyNote, EmptyState, ErrorState } from "@/components/state"
import { Stale } from "@/components/stale"
import { Button } from "@/components/ui/button"
import { VerbMenu, type Verb } from "@/components/verbs"
import { ContainerPicker } from "@/components/database/kit/container-picker"
import { NameMark } from "@/components/database/kit/marks"
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
  objectKey,
  objectParams,
  schemaParams,
  tableParams,
  type Selected,
} from "@/components/database/schema/address"
import { GroupGlyph, groupSpec } from "@/components/database/schema/kinds"
import type { DbCatalog, DbCatalogSchema, SchemaObject } from "@/components/database/schema/types"
import { compactCount, grouped } from "@/lib/format"

/** How many rows of a branch are drawn before the reader asks for more. */
const GROUP_STEP = 300

const NO_FOLDED: string[] = []
const NO_SCHEMAS: DbCatalogSchema[] = []

/** The kinds whose word says something the branch's heading does not. */
const SORTS_OF_TYPE = new Set(["enum", "domain", "composite", "range"])

/** What stands after an object's name in the tree: the figure or the word that tells it apart. */
function trailing(object: SchemaObject, rows: boolean): { text: string; title?: string } | null {
  if (rows) {
    const count = object.estimatedRows
    return count !== undefined && count >= 0
      ? { text: compactCount(count), title: `About ${grouped(count)} rows` }
      : null
  }
  if (object.kind === "trigger") return object.table ? { text: object.table } : null
  if (object.kind === "function" || object.kind === "procedure") {
    return object.signature !== undefined
      ? { text: `(${object.signature})`, title: `${object.name}(${object.signature})` }
      : null
  }
  return SORTS_OF_TYPE.has(object.kind) ? { text: object.kind } : null
}

/**
 * The rail of the schema browser: which schema, and everything in it by kind.
 *
 * Each branch is a kind of object — tables, views, functions, triggers,
 * sequences, types — under its own glyph in its own hue, with how many it
 * holds. Which branches there are is the engine's (`catalogGroups`), and one
 * the engine has and this schema does not is listed empty rather than left
 * out, so "no triggers" is something the tree says. A row is a link: it opens
 * in a new tab, and choosing one is a step of history.
 *
 * Functions an extension installed are folded behind their own row. A schema
 * with one extension in it is otherwise forty functions nobody wrote.
 */
export function SchemaRail({
  catalog,
  selected,
  creations,
  verbs,
  onRefresh,
  saidBeside = false,
  className,
}: {
  catalog: PollState<DbCatalog>
  selected: Selected
  /** What can be made here, for the head's "New" menu; empty where the role or the engine cannot. */
  creations: Verb[]
  /** What can be done to the schema itself: it is said here too, where the tree is all a phone shows. */
  verbs: Verb[]
  /** Reads the tree again, and with it whatever is open beside it. */
  onRefresh: () => void
  /** The page says beside the rail what is wrong, with the way on: the rail does not say it twice. */
  saidBeside?: boolean
  className?: string
}) {
  const { id, engine, href, selection } = useDatabase()
  const [query, setQuery] = useState("")
  const [folded, setFolded] = useViewState<string[]>(`databases.${id}.schema.folded`, NO_FOLDED)
  const data = catalog.data
  const needle = query.trim().toLowerCase()
  const schema = data?.schema ?? ""
  const known = !data || data.schemas.length === 0 || data.schemas.some((s) => s.name === schema)

  // The engine's own order of branches; anything the server sends beyond it follows.
  const order = useMemo(() => {
    const listed = engine.capabilities.catalogGroups
    const sent = Object.keys(data?.objects ?? {}) as DbCatalogGroup[]
    return [...listed.filter((g) => sent.includes(g)), ...sent.filter((g) => !listed.includes(g))]
  }, [engine, data])

  const groups = useMemo(
    () =>
      order.map((group) => {
        const objects = (data?.objects[group] ?? []) as SchemaObject[]
        const shown = needle
          ? objects.filter((object) => object.name.toLowerCase().includes(needle))
          : objects
        return { group, objects: shown, total: objects.length }
      }),
    [order, data, needle],
  )
  const total = groups.reduce((sum, entry) => sum + entry.total, 0)
  const matched = groups.reduce((sum, entry) => sum + entry.objects.length, 0)

  const selectedKey = !selected
    ? ""
    : selected.type === "table"
      ? `${selected.schema}\u0000${selected.name}`
      : objectKey({ ...selected, signature: selected.signature, table: selected.table })

  return (
    <nav
      data-slot="schema-rail"
      aria-label={`Objects of this ${engine.nouns.container}`}
      className={cn("flex min-h-0 min-w-0 flex-1 flex-col", className)}
    >
      <RailHead>
        {engine.can("schemas") ? (
          // Drawn from the address before the list is read, so the head does
          // not change shape when it lands.
          <ContainerPicker
            items={(data?.schemas ?? NO_SCHEMAS).map((entry) => ({
              name: entry.name,
              system: entry.system,
              figure: entry.tables >= 0 ? grouped(entry.tables) : undefined,
            }))}
            current={schema || selection.schema}
            noun={engine.nouns.container}
            nouns={engine.nouns.containers}
            hrefOf={(name) => href("schema", schemaParams(name))}
          />
        ) : (
          <RailTitle>Objects</RailTitle>
        )}
        {creations.length > 0 && known && (
          <VerbMenu
            verbs={creations}
            label="New"
            trigger={
              <Button size="icon-sm" variant="ghost" aria-label="New" className={RAIL_ACTION}>
                <Plus className="size-3.5" />
              </Button>
            }
          />
        )}
        <IconAction label="Read the schema again" className={RAIL_ACTION} onClick={onRefresh}>
          <RefreshClockwise />
        </IconAction>
        {verbs.length > 0 && known && (
          <VerbMenu
            verbs={verbs}
            trigger={
              <Button
                size="icon-sm"
                variant="ghost"
                aria-label={`More for ${schema || engine.label}`}
                className={RAIL_ACTION}
              >
                <MoreHorizontal className="size-3.5" />
              </Button>
            }
          />
        )}
      </RailHead>
      {/* The tree is what was read before: the read that was just asked for failed. */}
      <Stale form="strip" error={data ? catalog.error : undefined} onRetry={onRefresh} />
      <RailFind value={query} onChange={setQuery} placeholder="Find an object" />

      <div className="min-h-0 flex-1 overflow-y-auto px-1.5 pb-2">
        {!data && catalog.error ? (
          saidBeside ? (
            <EmptyNote className="px-2 py-6">The objects could not be read.</EmptyNote>
          ) : (
            <ErrorState retryable error={catalog.error} onRetry={catalog.refresh} className="m-1" />
          )
        ) : !data ? (
          <RailSkeleton groups={3} />
        ) : !known && saidBeside ? (
          <EmptyNote className="px-2 py-6">Nothing is listed.</EmptyNote>
        ) : !known ? (
          <EmptyState
            className="mt-2 border-0 px-2 py-8"
            title={`No ${engine.nouns.container} called ${schema}`}
            description={`This connection has no such ${engine.nouns.container}. It may have been dropped since the link was made.`}
            action={
              <Button size="sm" variant="outline" asChild>
                <Link href={href("schema", schemaParams(data.defaultSchema))}>
                  Open {data.defaultSchema || `the default ${engine.nouns.container}`}
                </Link>
              </Button>
            }
          />
        ) : total > 0 && matched === 0 ? (
          <RailNoMatch onClear={() => setQuery("")} />
        ) : (
          groups.map(({ group, objects, total: all }) =>
            // While searching, only the branches that answer are listed.
            needle && objects.length === 0 ? null : (
              <RailGroup
                key={group}
                group={group}
                objects={objects}
                total={all}
                searching={needle !== ""}
                selectedKey={selectedKey}
                folded={!needle && folded.includes(group)}
                onFold={(fold) =>
                  setFolded((held) =>
                    fold
                      ? [...held.filter((g) => g !== group), group]
                      : held.filter((g) => g !== group),
                  )
                }
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
  group,
  objects,
  total,
  searching,
  selectedKey,
  folded,
  onFold,
  error,
  truncated,
}: {
  group: DbCatalogGroup
  objects: SchemaObject[]
  total: number
  searching: boolean
  selectedKey: string
  folded: boolean
  onFold: (folded: boolean) => void
  error?: string
  truncated?: number
}) {
  const { href } = useDatabase()
  const spec = groupSpec(group)
  const [limit, setLimit] = useState(GROUP_STEP)
  const [extensions, setExtensions] = useState(false)

  // What an extension installed is not what somebody wrote here: folded,
  // unless the reader is searching, when a hit is a hit.
  const own = objects.filter((object) => !object.extension)
  const installed = objects.length - own.length
  const listed = searching || extensions ? objects : own
  const shown = listed.slice(0, limit)
  const names = [...new Set(objects.flatMap((o) => (o.extension ? [o.extension] : [])))]

  return (
    <section aria-label={spec.plural} className="pt-1.5">
      <RailGroupHead
        label={spec.plural}
        open={!folded}
        onToggle={() => onFold(!folded)}
        count={
          searching && objects.length !== total
            ? `${grouped(objects.length)} of ${grouped(total)}`
            : grouped(total)
        }
      />
      {!folded && (
        <>
          {error && (
            <EmptyNote className="px-2 py-2 text-left text-hint">Not read: {error}</EmptyNote>
          )}
          <ul>
            {shown.map((object) => {
              const params = spec.rows
                ? tableParams(object.schema, object.name)
                : objectParams(object)
              const key = spec.rows ? `${object.schema}\u0000${object.name}` : objectKey(object)
              const current = key === selectedKey
              const tail = trailing(object, spec.rows)
              return (
                <RailRow
                  key={key}
                  href={href("schema", params)}
                  current={current}
                  title={object.comment || object.detail || undefined}
                  mark={<GroupGlyph group={group} />}
                  name={object.name}
                  figures={
                    tail
                      ? [
                          {
                            key: "tail",
                            text: tail.text,
                            title: tail.title,
                            mono: !spec.rows,
                          },
                        ]
                      : []
                  }
                />
              )
            })}
          </ul>
          {total === 0 && !error && (
            <p className="py-1 pr-2 pl-2 text-hint text-muted-foreground">None</p>
          )}
          {listed.length > limit && (
            <Button
              size="xs"
              variant="ghost"
              className="mt-1 ml-1 text-muted-foreground"
              onClick={() => setLimit((n) => n + GROUP_STEP)}
            >
              Show {Math.min(GROUP_STEP, listed.length - limit)} more of {grouped(listed.length)}
            </Button>
          )}
          {installed > 0 && !searching && (
            <Button
              size="xs"
              variant="ghost"
              aria-expanded={extensions}
              className="mt-0.5 ml-1 max-w-[calc(100%-0.25rem)] text-muted-foreground"
              onClick={() => setExtensions(!extensions)}
            >
              <span className="min-w-0 truncate">
                {extensions ? "Hide" : "Show"} {grouped(installed)} from{" "}
                {names.slice(0, 2).join(", ")}
                {names.length > 2 ? ` and ${names.length - 2} more` : ""}
              </span>
            </Button>
          )}
          {truncated !== undefined && (
            <EmptyNote className="px-2 py-2 text-left text-hint">
              Only the first {grouped(truncated)} are listed.
            </EmptyNote>
          )}
        </>
      )}
    </section>
  )
}

/**
 * A schema's own colour, stable for its name, as a small square before it.
 *
 * @deprecated use `NameMark` from `database/kit/marks`.
 */
export const SchemaMark = NameMark
