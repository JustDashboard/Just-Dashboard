"use client"

import { useMemo } from "react"
import { bytes } from "@/lib/format"
import type { PollState } from "@/hooks/use-poll"
import { RankList, RankSkeleton, type RankItem } from "@/components/rank-list"
import { Block, BlockLink, Quiet, Read, staleOf } from "@/components/database/home/blocks"
import { byKeyType, keyType, nameHue } from "@/components/database/home/kinds"
import { compact, type Part } from "@/components/database/home/readings"
import type { MongoCollections, RedisTree } from "@/components/database/home/types"
import { useDatabase } from "@/components/database/shell/database-context"

/** How many rows the block ranks. */
const TOP = 6

/** One table as the block ranks it, whichever read it came from. */
export type LargestTable = {
  schema: string
  table: string
  /** -1 where the engine keeps no estimate. */
  rows: number
  bytes: number
}

export type LargestTables = {
  /** Largest first. */
  tables: LargestTable[]
  /** False where the engine reports no sizes: the list is then ranked by rows. */
  sizesKnown: boolean
  /** More tables exist than were read. */
  truncated: boolean
  /** Why the engine has nothing to rank, in its own words. */
  reason?: string
}

/** A table's key wherever the home names it: the size tile's parts and this list's rows agree on it. */
export function tableKey(entry: Pick<LargestTable, "schema" | "table">): string {
  return `${entry.schema}.${entry.table}`
}

/**
 * The swatch before a row that is also a part of the size tile's bar: the
 * part's own colour, so the list under the tiles is that bar's legend. A row
 * the bar does not name keeps the slot in the track's grey, and the names
 * stay in line.
 */
function partSwatch(parts: readonly Part[] | undefined, key: string) {
  if (!parts) return undefined
  return parts.find((entry) => entry.key === key)?.color ?? "var(--meter-track)"
}

/**
 * The largest tables, each a way into its rows, as figures in a column rather
 * than bars of different lengths. The schema is said only where the list holds
 * more than one — on a database with one schema it is the same word on every
 * row — and then in the schema's own hue, so two schemas' tables are told
 * apart down the list without reading the prefix.
 *
 * No row carries a share: the read is the largest few, not every table, so a
 * percentage of what was read would not be a percentage of the database.
 */
export function LargestTablesBlock({
  poll,
  parts,
}: {
  poll: PollState<LargestTables>
  /** The size tile's parts, where it draws them: these rows are their legend. */
  parts?: Part[]
}) {
  const { engine, readOnly, href, goto } = useDatabase()
  const nouns = engine.nouns
  const place = engine.section("data")?.title ?? "Data"
  const items = useMemo<RankItem[]>(() => {
    const data = poll.data
    if (!data) return []
    const list = data.tables.slice(0, TOP)
    const several = new Set(list.map((entry) => entry.schema)).size > 1
    return list.map((entry) => {
      const counted = entry.rows >= 0 ? `${compact(entry.rows)} ${nouns.rows}` : undefined
      const named = several && entry.schema ? tableKey(entry) : entry.table
      return {
        key: tableKey(entry),
        swatch: partSwatch(parts, tableKey(entry)),
        label:
          several && entry.schema ? (
            <>
              <span style={{ color: nameHue(entry.schema) }}>{entry.schema}.</span>
              {entry.table}
            </>
          ) : (
            entry.table
          ),
        // The name a screen reader hears carries the schema whenever the row
        // shows it: two schemas can hold a table of one name.
        title: `Open ${named} in ${place}`,
        value: data.sizesKnown ? bytes(entry.bytes) : entry.rows >= 0 ? compact(entry.rows) : "—",
        hint: data.sizesKnown ? counted : undefined,
        onClick: () => goto("data", { schema: entry.schema, table: entry.table }),
      }
    })
  }, [poll.data, parts, nouns, place, goto])

  return (
    <Block
      title={`Largest ${nouns.objects}`}
      stale={staleOf(poll)}
      actions={<BlockLink href={href("data")}>All {nouns.objects}</BlockLink>}
    >
      <Read poll={poll} what={`the ${nouns.objects}`} skeleton={<RankSkeleton rows={6} />}>
        {(data) =>
          items.length === 0 ? (
            <Quiet>
              {data.reason ??
                (readOnly
                  ? `No ${nouns.objects} yet. The connection is protected, so none can be made from here.`
                  : `No ${nouns.objects} yet. Create one in ${engine.section("schema")?.title ?? "Query"}, or import a file in ${place}.`)}
            </Quiet>
          ) : (
            <>
              <RankList items={items} />
              {!data.sizesKnown && (
                <p className="mt-2 text-hint text-muted-foreground">
                  This engine reports no sizes, so they are ranked by {nouns.rows}.
                </p>
              )}
            </>
          )
        }
      </Read>
    </Block>
  )
}

/** A document database's collections, by the data they hold. */
export function LargestCollectionsBlock({
  poll,
  parts,
}: {
  poll: PollState<MongoCollections>
  parts?: Part[]
}) {
  const { engine, readOnly, href, goto } = useDatabase()
  const nouns = engine.nouns
  const place = engine.section("data")?.title ?? "Documents"
  const items = useMemo<RankItem[]>(() => {
    const data = poll.data
    if (!data) return []
    const own = data.collections.filter((collection) => !collection.system)
    // The read lists every collection, so a row's share is of all of them —
    // unless some went unmeasured, and then there is no whole to be a share of.
    const whole = data.statsTruncated
      ? 0
      : own.reduce((sum, collection) => sum + (collection.statsKnown ? collection.size : 0), 0)
    const list = own.sort((a, b) => b.size - a.size || b.count - a.count).slice(0, TOP)
    return list.map((collection) => ({
      key: collection.name,
      swatch: partSwatch(parts, collection.name),
      label: collection.name,
      title: `Open ${collection.name} in ${place}`,
      value: collection.statsKnown ? bytes(collection.size) : "—",
      share: collection.statsKnown && whole > 0 ? collection.size / whole : undefined,
      hint: collection.statsKnown
        ? `${compact(collection.count)} ${collection.count === 1 ? nouns.row : nouns.rows}`
        : collection.type === "view"
          ? "a view"
          : undefined,
      onClick: () => goto("data", { db: data.database, collection: collection.name }),
    }))
  }, [poll.data, parts, nouns, place, goto])

  return (
    <Block
      title={`Largest ${nouns.objects}`}
      stale={staleOf(poll)}
      actions={<BlockLink href={href("data")}>All {nouns.objects}</BlockLink>}
    >
      <Read poll={poll} what={`the ${nouns.objects}`} skeleton={<RankSkeleton rows={6} />}>
        {(data) =>
          items.length === 0 ? (
            <Quiet>
              {readOnly
                ? `No ${nouns.objects} in ${data.database} yet. The connection is protected, so none can be made from here.`
                : `No ${nouns.objects} in ${data.database} yet. Insert a ${nouns.row} in ${place} and its ${nouns.object} is made with it.`}
            </Quiet>
          ) : (
            <RankList items={items} />
          )
        }
      </Read>
    </Block>
  )
}

/**
 * A key–value store's keys, by the name before the first colon, under what
 * they are by type: one bar of the walk's keys in the type hues, with its
 * legend, and then the namespaces, each saying which types it holds in the
 * same hues.
 *
 * It is one walk of the keyspace, bounded on the server and taken once when
 * the page opens — never on a timer — and the block says how much of the
 * keyspace the walk covered when it was not all of it.
 */
export function LargestNamespacesBlock({ poll }: { poll: PollState<RedisTree> }) {
  const { href, goto } = useDatabase()
  const items = useMemo<RankItem[]>(() => {
    const data = poll.data
    if (!data) return []
    const list = data.folders.slice(0, TOP)
    // Every key the walk saw is of one type, so the types add up to the walk.
    const walked = Object.values(data.types ?? {}).reduce((sum, count) => sum + count, 0)
    const of = (count: number) => (walked > 0 ? count / walked : undefined)
    const rows: RankItem[] = list.map((folder) => ({
      key: folder.prefix,
      label: `${folder.prefix}*`,
      title: `Browse the keys under ${folder.prefix}`,
      value: compact(folder.count),
      share: of(folder.count),
      hint: <TypeWords types={folder.types} />,
      onClick: () => goto("data", { db: String(data.db), pattern: folder.pattern }),
    }))
    if (data.keyCount > 0 && rows.length < TOP) {
      rows.push({
        key: "",
        label: "no namespace",
        mono: false,
        value: compact(data.keyCount),
        share: of(data.keyCount),
        hint: `keys with no ${data.delimiter} in their name`,
      })
    }
    return rows
  }, [poll.data, goto])

  return (
    <Block
      title="Largest namespaces"
      stale={staleOf(poll)}
      actions={<BlockLink href={href("data")}>All keys</BlockLink>}
    >
      <Read poll={poll} what="the keyspace" skeleton={<RankSkeleton rows={6} />}>
        {(data) =>
          items.length === 0 ? (
            <Quiet>Database {data.db} holds no keys.</Quiet>
          ) : (
            <div className="space-y-3">
              <TypeBar types={data.types ?? {}} />
              <RankList items={items} />
              <p className="text-hint text-muted-foreground">
                {data.complete
                  ? `Keys in database ${data.db}, by the name before the first “${data.delimiter}”.`
                  : `From the first ${compact(data.scanned)} of ${compact(data.total)} keys in database ${data.db}; the rest are not counted here.`}
              </p>
            </div>
          )
        }
      </Read>
    </Block>
  )
}

/** The types a namespace holds, most keys first, each word in its type's hue. */
function TypeWords({ types }: { types: Record<string, number> }) {
  const held = Object.entries(types).sort((a, b) => b[1] - a[1])
  return held.map(([type], index) => {
    const kind = keyType(type)
    return (
      <span key={type}>
        {index > 0 && " · "}
        <span style={{ color: kind.color }}>{kind.label.toLowerCase()}</span>
      </span>
    )
  })
}

/**
 * What the walked keys are, by type: one track, as many widths as there are
 * types, and the count under each swatch (the Docker disk summary's shape).
 * A type is its own hue here and wherever else the section draws a key.
 */
function TypeBar({ types }: { types: Record<string, number> }) {
  const held = Object.entries(types)
    .filter(([, count]) => count > 0)
    .sort((a, b) => byKeyType(a[0], b[0]))
    .map(([type, count]) => ({ type, count, ...keyType(type) }))
  const total = held.reduce((sum, entry) => sum + entry.count, 0)
  if (total === 0) return null
  return (
    <div className="space-y-2 pt-1" data-slot="key-types">
      <div
        role="img"
        aria-label={`Keys by type: ${held.map((entry) => `${entry.label} ${entry.count.toLocaleString()}`).join(", ")}`}
        className="flex h-2 w-full gap-px overflow-hidden rounded-full bg-meter-track"
      >
        {held.map((entry) => (
          <span
            key={entry.type}
            className="h-full first:rounded-l-full last:rounded-r-full"
            style={{
              width: `${Math.max((entry.count / total) * 100, 1)}%`,
              backgroundColor: entry.color,
            }}
          />
        ))}
      </div>
      <ul className="flex flex-wrap gap-x-4 gap-y-1">
        {held.map((entry) => (
          <li key={entry.type} className="flex min-w-0 items-center gap-1.5">
            <span
              aria-hidden
              className="size-1.5 shrink-0 rounded-full"
              style={{ backgroundColor: entry.color }}
            />
            <span className="truncate text-hint" style={{ color: entry.color }}>
              {entry.label}
            </span>
            <span className="numeric text-hint text-muted-foreground">{compact(entry.count)}</span>
          </li>
        ))}
      </ul>
    </div>
  )
}
