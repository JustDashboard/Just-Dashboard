"use client"

import { bytes } from "@/lib/format"
import { BarList, type BarListItem } from "@/components/bar-list"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { EmptyState } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { Tag } from "@/components/tag"
import { Plus } from "@/components/icons"
import { grouped } from "@/components/database/data/view"
import type { SectionId } from "@/components/database/engine"
import { EngineMark } from "@/components/database/kit"
import { CollectionMark, collectionKind } from "@/components/database/mongo/kinds"
import { DatabaseMark } from "@/components/database/mongo/rail"
import type { Catalog, Mongo } from "@/components/database/mongo/use-mongo"
import { ReadError } from "@/components/database/redis/read-error"

/** How many collections the ranked list draws; the rail has all of them. */
const RANKED = 12

/**
 * The pane while no collection is open: what the database is made of.
 *
 * Four figures — collections, documents, data and indexes — then the
 * collections by the data they hold and the server's other databases, each a
 * way in. It is drawn from the lists the rail already read, so it costs
 * nothing, and the page is never a list beside an empty half.
 */
export function DatabasePane({
  mongo,
  catalog,
  section,
  leading,
  onNew,
}: {
  mongo: Mongo
  catalog: Catalog
  section: SectionId
  /** The control that brings the rail back, when it is hidden. */
  leading?: React.ReactNode
  onNew?: () => void
}) {
  const { engine, database, conn, goto } = mongo
  const { collections, databases } = catalog
  const data = collections.data
  const place = engine.section(section)?.title ?? "Documents"

  const head = (
    <div className="flex min-h-10 shrink-0 items-center gap-2 border-b border-hairline bg-surface-header px-3 py-1.5">
      {leading}
      <DatabaseMark name={database} />
      <h2 className="min-w-0 truncate font-mono text-sm leading-6 font-medium">
        {database || "No database"}
      </h2>
      {database && database === conn.database && <Tag>connects here</Tag>}
      <span className="min-w-0 flex-1" />
      {onNew && database && (
        <Button size="xs" variant="outline" onClick={onNew}>
          <Plus />
          New {engine.nouns.object}
        </Button>
      )}
    </div>
  )

  const others = (databases.data ?? []).filter((entry) => entry.name !== database)
  const largestDb = Math.max(...others.map((entry) => entry.dataSize), 1)
  const elsewhere: BarListItem[] = others.map((entry) => ({
    key: entry.name,
    label: entry.name,
    mark: <DatabaseMark name={entry.name} />,
    value: entry.statsKnown ? bytes(entry.dataSize) : "—",
    share: entry.statsKnown ? entry.dataSize / largestDb : 0,
    hint: entry.statsKnown
      ? `${grouped(entry.collections)} ${entry.collections === 1 ? engine.nouns.object : engine.nouns.objects}`
      : "not measured",
    title: `Open ${entry.name}`,
    onClick: () => goto(section, { db: entry.name, collection: null }),
  }))
  const elsewhereBlock = elsewhere.length > 0 && (
    <section aria-labelledby="mongo-pane-databases" className="space-y-2">
      <Heading id="mongo-pane-databases" title="Other databases on this server">
        {grouped(elsewhere.length)}
      </Heading>
      <BarList items={elsewhere} className="-ml-2" />
    </section>
  )

  if (!database) {
    return (
      <div data-slot="mongo-database" className="flex min-h-0 min-w-0 flex-1 flex-col">
        {head}
        <div className="min-h-0 flex-1 overflow-auto">
          <div className="animate-rise space-y-6 px-5 py-5">
            <p className="max-w-prose text-body text-muted-foreground">
              The connection string names no database. Choose one of the server&rsquo;s below.
            </p>
            {databases.error && !databases.data ? (
              <ReadError error={databases.error} onRetry={databases.refresh} />
            ) : (
              <div className="max-w-2xl">{elsewhereBlock}</div>
            )}
          </div>
        </div>
      </div>
    )
  }

  // Nothing could be read: the rail beside this says why, with the way to try again.
  if (collections.error && !data) {
    return (
      <div data-slot="mongo-database" className="flex min-h-0 min-w-0 flex-1 flex-col">
        {head}
        <EmptyState
          mark={<EngineMark engine={engine} />}
          className="min-h-0 flex-1 border-0"
          title="The server did not answer"
          description={`What ${database} holds is drawn here once its ${engine.nouns.objects} can be read.`}
        />
      </div>
    )
  }

  const own = (data?.collections ?? []).filter((entry) => !entry.system)
  if (data && own.length === 0) {
    return (
      <div data-slot="mongo-database" className="flex min-h-0 min-w-0 flex-1 flex-col">
        {head}
        <div className="min-h-0 flex-1 overflow-auto">
          <div className="animate-rise space-y-6 px-5 py-5">
            <EmptyState
              mark={<EngineMark engine={engine} />}
              title={`${database} holds no ${engine.nouns.objects}`}
              description={
                onNew
                  ? `Create one, and ${place.toLowerCase()} for it open here.`
                  : `Nothing in this ${engine.nouns.container} holds ${engine.nouns.rows} yet.`
              }
              action={
                onNew && (
                  <Button size="sm" onClick={onNew}>
                    <Plus />
                    New {engine.nouns.object}
                  </Button>
                )
              }
            />
            <div className="max-w-2xl">{elsewhereBlock}</div>
          </div>
        </div>
      </div>
    )
  }

  const measured = own.filter((entry) => entry.statsKnown)
  const views = own.filter((entry) => entry.type === "view").length
  const documents = measured.reduce((sum, entry) => sum + entry.count, 0)
  const size = measured.reduce((sum, entry) => sum + entry.size, 0)
  const stored = measured.reduce((sum, entry) => sum + entry.storageSize, 0)
  const indexes = measured.reduce((sum, entry) => sum + entry.indexCount, 0)
  const indexSize = measured.reduce((sum, entry) => sum + entry.indexSize, 0)

  const ranked = [...own].sort((a, b) => b.size - a.size || a.name.localeCompare(b.name))
  const largest = Math.max(...ranked.map((entry) => entry.size), 1)
  const bars: BarListItem[] = ranked.slice(0, RANKED).map((entry) => ({
    key: entry.name,
    label: entry.name,
    mark: <CollectionMark kind={collectionKind(entry)} />,
    value: entry.statsKnown ? bytes(entry.size) : "—",
    share: entry.statsKnown ? entry.size / largest : 0,
    hint: entry.statsKnown
      ? `${grouped(entry.count)} ${entry.count === 1 ? engine.nouns.row : engine.nouns.rows}`
      : entry.type === "view"
        ? `a view on ${entry.viewOn}`
        : "not measured",
    title: `Open ${entry.name} in ${place}`,
    onClick: () => goto(section, { db: database, collection: entry.name }),
  }))

  return (
    <div data-slot="mongo-database" className="flex min-h-0 min-w-0 flex-1 flex-col">
      {head}
      <div className="@container min-h-0 flex-1 overflow-auto">
        <div className="animate-rise space-y-7 px-5 pt-1 pb-6">
          {/* Bled by the tiles' own inset, so a tile's figure starts on the
              line every heading under it starts on. */}
          <StatGrid columns={4} dense className="-mx-5 border-b border-hairline">
            <StatTile
              label="Collections"
              value={data ? grouped(own.length - views) : "—"}
              hint={
                !data
                  ? undefined
                  : views > 0
                    ? `and ${grouped(views)} ${views === 1 ? "view" : "views"}`
                    : "no views"
              }
            />
            <StatTile
              label="Documents"
              value={data ? grouped(documents) : "—"}
              hint={data?.statsTruncated ? "of the collections measured" : "in every collection"}
            />
            <StatTile
              label="Data"
              value={data ? bytes(size) : "—"}
              hint={data ? `${bytes(stored)} on disk, compressed` : undefined}
            />
            <StatTile
              label="Indexes"
              value={data ? grouped(indexes) : "—"}
              hint={data ? `${bytes(indexSize)} on disk` : undefined}
            />
          </StatGrid>

          {!data ? (
            <div className="space-y-3" aria-hidden>
              <Skeleton className="h-3.5 w-36" />
              {Array.from({ length: 6 }, (_, i) => (
                <Skeleton key={i} className="h-5" style={{ width: `${92 - i * 11}%` }} />
              ))}
            </div>
          ) : (
            <div className="grid items-start gap-x-10 gap-y-7 @3xl:grid-cols-2 [&>*]:min-w-0">
              <section aria-labelledby="mongo-pane-collections" className="space-y-2">
                <Heading id="mongo-pane-collections" title="By the data they hold">
                  {own.length > bars.length &&
                    `${grouped(own.length - bars.length)} smaller ones are in the list`}
                </Heading>
                <BarList items={bars} className="-ml-2" />
              </section>
              {elsewhereBlock}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

/** A block's name on the pane, with what qualifies it at the far end of the line. */
function Heading({
  id,
  title,
  children,
}: {
  id: string
  title: string
  children?: React.ReactNode
}) {
  return (
    <div className="flex min-h-7 min-w-0 flex-wrap items-center gap-x-3 gap-y-1 border-b border-hairline pb-2">
      <h3 id={id} className="text-body font-medium">
        {title}
      </h3>
      <span className="numeric ml-auto flex min-w-0 items-center gap-2 text-hint text-muted-foreground">
        {children}
      </span>
    </div>
  )
}
