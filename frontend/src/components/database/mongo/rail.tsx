"use client"

import { useMemo, useState } from "react"
import { Plus, RefreshClockwise } from "@/components/icons"
import { bytes, compactCount, grouped } from "@/lib/format"
import { IconAction } from "@/components/icon-action"
import { EmptyNote, ErrorState } from "@/components/state"
import { Stale } from "@/components/stale"
import type { Verb } from "@/components/verbs"
import type { SectionId } from "@/components/database/engine"
import { ContainerPicker } from "@/components/database/kit/container-picker"
import { NameMark } from "@/components/database/kit/marks"
import {
  RAIL_ACTION,
  RailFind,
  RailGroupHead,
  RailHead,
  RailNoMatch,
  RailRow,
  RailSkeleton,
} from "@/components/database/workbench/rail"
import {
  COLLECTION_KINDS,
  CollectionMark,
  collectionKind,
  type CollectionKind,
} from "@/components/database/mongo/kinds"
import type { MongoCollection, MongoDatabase } from "@/components/database/mongo/types"
import type { Catalog, Mongo } from "@/components/database/mongo/use-mongo"

/** The groups the rail lists, in order, with the word each run is headed by. */
const GROUPS: { kind: CollectionKind; plural: string }[] = [
  { kind: "collection", plural: "Collections" },
  { kind: "capped", plural: "Capped" },
  { kind: "timeseries", plural: "Time series" },
  { kind: "view", plural: "Views" },
]

/**
 * The rail of the MongoDB workbenches: which database, and what it holds.
 *
 * Collections are listed by kind — ordinary, capped, time series, views —
 * each with its kind's glyph in its kind's hue, and its document count and
 * data size at the right. A row is a link: it opens in a new tab, and
 * choosing one is a step of history, so Back returns to the collection the
 * reader came from. The server's own collections (`system.*`) fold away
 * under the rest. A collection whose documents hold edits that were staged
 * and not sent carries a mark in the hue of a change, so they are not
 * forgotten behind another collection.
 *
 * The same rail stands beside Documents, Aggregations and Schema, and a row
 * opens its collection in whichever of the three the reader is on.
 */
export function CollectionRail({
  mongo,
  catalog,
  section,
  verbsFor,
  unsent,
  onNew,
}: {
  mongo: Mongo
  catalog: Catalog
  section: SectionId
  verbsFor: (collection: MongoCollection) => Verb[]
  /** How many documents of a collection hold field edits that were not sent. */
  unsent: (collection: string) => number
  /** Opens "New collection"; absent where the role, the engine or the connection cannot. */
  onNew?: () => void
}) {
  const { engine, href, database, collection: current } = mongo
  const { collections } = catalog
  const [query, setQuery] = useState("")
  const [systemOpen, setSystemOpen] = useState(false)
  const needle = query.trim().toLowerCase()
  const data = collections.data

  const { groups, system, own } = useMemo(() => {
    const all = data?.collections ?? []
    const match = (entry: MongoCollection) => !needle || entry.name.toLowerCase().includes(needle)
    const byKind = new Map<CollectionKind, MongoCollection[]>()
    for (const entry of all) {
      const kind = collectionKind(entry)
      byKind.set(kind, [...(byKind.get(kind) ?? []), entry])
    }
    return {
      groups: GROUPS.map((group) => ({
        ...group,
        entries: (byKind.get(group.kind) ?? []).filter(match),
      })).filter((group) => group.entries.length > 0),
      system: (byKind.get("system") ?? []).filter(match),
      own: all.filter((entry) => !entry.system),
    }
  }, [data, needle])

  const matched = groups.reduce((sum, group) => sum + group.entries.length, 0) + system.length
  const measured = own.filter((entry) => entry.statsKnown)
  const documents = measured.reduce((sum, entry) => sum + entry.count, 0)
  const size = measured.reduce((sum, entry) => sum + entry.size, 0)
  // A search, or the open collection being one of them, shows the server's own.
  const showSystem =
    systemOpen ||
    Boolean(needle) ||
    own.length === 0 ||
    system.some((entry) => entry.name === current)

  return (
    <nav
      data-slot="collection-rail"
      aria-label={`${engine.nouns.objects} of this ${engine.nouns.container}`}
      className="flex min-h-0 min-w-0 flex-1 flex-col"
    >
      <RailHead>
        <DatabasePicker mongo={mongo} catalog={catalog} section={section} />
        {onNew && (
          <IconAction label={`New ${engine.nouns.object}`} className={RAIL_ACTION} onClick={onNew}>
            <Plus />
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
      <Stale
        form="strip"
        error={data ? collections.error : undefined}
        subject={`the ${engine.nouns.objects}`}
        onRetry={collections.refresh}
      />
      <RailFind value={query} onChange={setQuery} placeholder={`Find a ${engine.nouns.object}`} />

      <div className="min-h-0 flex-1 overflow-y-auto px-1.5 pb-2">
        {!database ? (
          <EmptyNote className="px-2 py-6">
            The connection names no {engine.nouns.container}. Choose one above.
          </EmptyNote>
        ) : !data && collections.error ? (
          <ErrorState
            retryable
            error={collections.error}
            onRetry={collections.refresh}
            className="m-1"
          />
        ) : !data ? (
          <RailSkeleton groups={2} />
        ) : data.collections.length === 0 ? (
          // The pane beside this says it, with the way to make the first one.
          <EmptyNote className="px-2 py-6">
            No {engine.nouns.objects} in {database} yet.
          </EmptyNote>
        ) : matched === 0 ? (
          <RailNoMatch onClear={() => setQuery("")} />
        ) : (
          <>
            {groups.map((group) => (
              <section key={group.kind} aria-label={group.plural} className="pt-1.5">
                <RailGroupHead label={group.plural} count={grouped(group.entries.length)} />
                <ul>
                  {group.entries.map((entry) => (
                    <CollectionRow
                      key={entry.name}
                      entry={entry}
                      current={entry.name === current}
                      href={href(section, { db: database, collection: entry.name })}
                      verbs={verbsFor(entry)}
                      unsent={unsent(entry.name)}
                    />
                  ))}
                </ul>
              </section>
            ))}
            {system.length > 0 && (
              <section aria-label="The server's own collections" className="pt-1.5">
                <RailGroupHead
                  label="The server’s own"
                  count={grouped(system.length)}
                  open={showSystem}
                  onToggle={() => setSystemOpen(!showSystem)}
                />
                {showSystem && (
                  <ul>
                    {system.map((entry) => (
                      <CollectionRow
                        key={entry.name}
                        entry={entry}
                        current={entry.name === current}
                        href={href(section, { db: database, collection: entry.name })}
                        verbs={verbsFor(entry)}
                        unsent={unsent(entry.name)}
                      />
                    ))}
                  </ul>
                )}
              </section>
            )}
          </>
        )}
      </div>

      {data && own.length > 0 && (
        <div className="flex h-8 shrink-0 items-center gap-1.5 border-t border-hairline bg-surface-header px-3 text-hint text-muted-foreground">
          <span className="numeric min-w-0 truncate">
            {grouped(documents)} {documents === 1 ? engine.nouns.row : engine.nouns.rows}
            {" · "}
            {bytes(size)}
            {data.statsTruncated ? " · some not measured" : ""}
          </span>
        </div>
      )}
    </nav>
  )
}

function CollectionRow({
  entry,
  current,
  href,
  verbs,
  unsent,
}: {
  entry: MongoCollection
  current: boolean
  href: string
  verbs: Verb[]
  unsent: number
}) {
  const kind = collectionKind(entry)
  return (
    <RailRow
      href={href}
      current={current}
      title={
        entry.type === "view"
          ? `A view on ${entry.viewOn}`
          : entry.statsKnown
            ? `${grouped(entry.count)} documents · ${bytes(entry.size)} of data`
            : COLLECTION_KINDS[kind].label
      }
      mark={<CollectionMark kind={kind} />}
      name={entry.name}
      // Staged and unsent, in the words the tables' rail uses for the same.
      staged={{
        count: unsent,
        slot: "mongo-unsent-mark",
        label: "documents with unsent edits",
        title: `Unsent edits on ${unsent === 1 ? "one document" : `${unsent} documents`}`,
      }}
      figures={
        entry.statsKnown
          ? [
              { key: "count", text: compactCount(entry.count) },
              { key: "size", text: bytes(entry.size, 0), quiet: true, className: "w-14" },
            ]
          : entry.type === "view"
            ? [{ key: "view", text: "view", quiet: true }]
            : []
      }
      verbs={verbs}
      menuLabel={`Actions for ${entry.name}`}
    />
  )
}

/**
 * Which database the rail lists. The server's databases with what each
 * holds; choosing one opens it with no collection named, since the one that
 * was open is not in it.
 */
function DatabasePicker({
  mongo,
  catalog,
  section,
}: {
  mongo: Mongo
  catalog: Catalog
  section: SectionId
}) {
  const { engine, href, database, conn } = mongo
  const all = catalog.databases.data ?? []
  // The open database is listed even before it holds anything: an empty
  // database does not exist for the server until it has a collection.
  const listed: MongoDatabase[] =
    database && !all.some((entry) => entry.name === database)
      ? [...all, { ...EMPTY_DATABASE, name: database }]
      : all
  return (
    <ContainerPicker
      items={listed.map((entry) => ({
        name: entry.name,
        figure: [
          entry.name === conn.database ? "connects here" : null,
          entry.statsKnown
            ? `${grouped(entry.collections)} · ${bytes(entry.dataSize, 0)}`
            : "not measured",
        ]
          .filter(Boolean)
          .join(" · "),
      }))}
      current={database}
      noun={engine.nouns.container}
      nouns={engine.nouns.containers}
      blank="Choose one"
      label="none chosen"
      hrefOf={(name) => href(section, { db: name, collection: null })}
      state={
        catalog.databases.error && !catalog.databases.data
          ? "failed"
          : !catalog.databases.data
            ? "reading"
            : undefined
      }
      error={catalog.databases.error?.message}
    />
  )
}

const EMPTY_DATABASE: MongoDatabase = {
  name: "",
  collections: 0,
  views: 0,
  objects: 0,
  avgObjSize: 0,
  dataSize: 0,
  storageSize: 0,
  indexes: 0,
  indexSize: 0,
  sizeOnDisk: 0,
  empty: true,
  statsKnown: true,
}

/**
 * A database's own colour, stable for its name, as a small square before it.
 *
 * @deprecated use `NameMark` from `database/kit/marks`.
 */
export const DatabaseMark = NameMark
