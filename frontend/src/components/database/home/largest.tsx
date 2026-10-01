"use client"

import { useMemo } from "react"
import { bytes } from "@/lib/format"
import type { PollState } from "@/hooks/use-poll"
import { BarList, type BarListItem } from "@/components/bar-list"
import { BarsSkeleton, Block, BlockLink, Read, staleOf } from "@/components/database/home/blocks"
import { compact } from "@/components/database/home/readings"
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

/**
 * The largest tables, each a way into its rows. The schema is said only
 * where the list holds more than one: on a database with one schema it is the
 * same word on every row.
 */
export function LargestTablesBlock({ poll }: { poll: PollState<LargestTables> }) {
  const { engine, href, goto } = useDatabase()
  const nouns = engine.nouns
  const items = useMemo<BarListItem[]>(() => {
    const data = poll.data
    if (!data) return []
    const list = data.tables.slice(0, TOP)
    const several = new Set(list.map((entry) => entry.schema)).size > 1
    const weigh = (entry: LargestTable) => (data.sizesKnown ? entry.bytes : Math.max(entry.rows, 0))
    const longest = Math.max(...list.map(weigh), 0)
    return list.map((entry) => {
      const counted = entry.rows >= 0 ? `${compact(entry.rows)} ${nouns.rows}` : undefined
      return {
        key: `${entry.schema}.${entry.table}`,
        label: several && entry.schema ? `${entry.schema}.${entry.table}` : entry.table,
        title: `Open ${entry.table} in ${engine.section("data")?.title ?? "Data"}`,
        value: data.sizesKnown ? bytes(entry.bytes) : entry.rows >= 0 ? compact(entry.rows) : "—",
        share: longest > 0 ? weigh(entry) / longest : 0,
        hint: data.sizesKnown ? counted : undefined,
        onClick: () => goto("data", { schema: entry.schema, table: entry.table }),
      }
    })
  }, [poll.data, nouns, engine, goto])

  return (
    <Block
      title={`Largest ${nouns.objects}`}
      stale={staleOf(poll)}
      actions={<BlockLink href={href("data")}>All {nouns.objects}</BlockLink>}
    >
      <Read poll={poll} what={`the ${nouns.objects}`} skeleton={<BarsSkeleton rows={6} />}>
        {(data) => (
          <>
            <BarList
              items={items}
              emptyLabel={
                data.reason ??
                `No ${nouns.objects} yet. Create one in ${engine.section("schema")?.title ?? "Query"}, or import a file in ${engine.section("data")?.title ?? "Data"}.`
              }
            />
            {items.length > 0 && !data.sizesKnown && (
              <p className="mt-2 px-2 text-hint text-muted-foreground">
                This engine reports no sizes, so they are ranked by {nouns.rows}.
              </p>
            )}
          </>
        )}
      </Read>
    </Block>
  )
}

/** A document database's collections, by the data they hold. */
export function LargestCollectionsBlock({ poll }: { poll: PollState<MongoCollections> }) {
  const { engine, href, goto } = useDatabase()
  const nouns = engine.nouns
  const items = useMemo<BarListItem[]>(() => {
    const data = poll.data
    if (!data) return []
    const list = data.collections
      .filter((collection) => !collection.system)
      .sort((a, b) => b.size - a.size || b.count - a.count)
      .slice(0, TOP)
    const longest = Math.max(...list.map((collection) => collection.size), 0)
    return list.map((collection) => ({
      key: collection.name,
      label: collection.name,
      title: `Open ${collection.name} in ${engine.section("data")?.title ?? "Documents"}`,
      value: collection.statsKnown ? bytes(collection.size) : "—",
      share: longest > 0 ? collection.size / longest : 0,
      hint: collection.statsKnown
        ? `${compact(collection.count)} ${collection.count === 1 ? nouns.row : nouns.rows}`
        : collection.type === "view"
          ? "a view"
          : undefined,
      onClick: () => goto("data", { db: data.database, collection: collection.name }),
    }))
  }, [poll.data, nouns, engine, goto])

  return (
    <Block
      title={`Largest ${nouns.objects}`}
      stale={staleOf(poll)}
      actions={<BlockLink href={href("data")}>All {nouns.objects}</BlockLink>}
    >
      <Read poll={poll} what={`the ${nouns.objects}`} skeleton={<BarsSkeleton rows={6} />}>
        {(data) => (
          <BarList
            items={items}
            emptyLabel={`No ${nouns.objects} in ${data.database} yet. Insert a ${nouns.row} in ${engine.section("data")?.title ?? "Documents"} and its ${nouns.object} is made with it.`}
          />
        )}
      </Read>
    </Block>
  )
}

/**
 * A key–value store's keys, by the name before the first colon. It is one
 * walk of the keyspace, bounded on the server and taken once when the page
 * opens — never on a timer — and the block says how much of the keyspace the
 * walk covered when it was not all of it.
 */
export function LargestNamespacesBlock({ poll }: { poll: PollState<RedisTree> }) {
  const { href, goto } = useDatabase()
  const items = useMemo<BarListItem[]>(() => {
    const data = poll.data
    if (!data) return []
    const list = data.folders.slice(0, TOP)
    const longest = Math.max(...list.map((folder) => folder.count), data.keyCount, 0)
    const rows: BarListItem[] = list.map((folder) => ({
      key: folder.prefix,
      label: `${folder.prefix}*`,
      title: `Browse the keys under ${folder.prefix}`,
      value: compact(folder.count),
      share: longest > 0 ? folder.count / longest : 0,
      hint: Object.entries(folder.types)
        .sort((a, b) => b[1] - a[1])
        .map(([type]) => type)
        .join(" · "),
      onClick: () => goto("data", { db: String(data.db), pattern: folder.pattern }),
    }))
    if (data.keyCount > 0 && rows.length < TOP) {
      rows.push({
        key: "",
        label: "no namespace",
        mono: false,
        value: compact(data.keyCount),
        share: longest > 0 ? data.keyCount / longest : 0,
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
      <Read poll={poll} what="the keyspace" skeleton={<BarsSkeleton rows={6} />}>
        {(data) => (
          <>
            <BarList items={items} emptyLabel={`Database ${data.db} holds no keys.`} />
            {items.length > 0 && (
              <p className="mt-2 px-2 text-hint text-muted-foreground">
                {data.complete
                  ? `Keys in database ${data.db}, by the name before the first “${data.delimiter}”.`
                  : `From the first ${compact(data.scanned)} of ${compact(data.total)} keys in database ${data.db}; the rest are not counted here.`}
              </p>
            )}
          </>
        )}
      </Read>
    </Block>
  )
}
