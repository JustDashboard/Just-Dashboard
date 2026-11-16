"use client"

import { PanelHeader } from "@/components/panel"
import { RankList, RankSkeleton, type RankItem } from "@/components/rank-list"
import type { SectionId } from "@/components/database/engine"
import { EngineMark } from "@/components/database/kit"
import { NameMark } from "@/components/database/kit/marks"
import { CollectionMark, collectionKind } from "@/components/database/mongo/kinds"
import type { Catalog, Mongo } from "@/components/database/mongo/use-mongo"
import { ObjectHead } from "@/components/database/workbench/chrome"
import { Plus } from "@/components/icons"
import { EmptyState, ErrorState } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { bytes, grouped } from "@/lib/format"
import Link from "next/link"

/** How many collections the ranked list draws; the rail has all of them. */
const RANKED = 12

/** The databases the server keeps for itself: nothing of the reader's belongs in them. */
const RESERVED = new Set(["admin", "local", "config"])

/**
 * The pane while no collection is open: what the database is made of.
 *
 * Four figures — collections, documents, data and indexes — then the
 * collections by the data they hold and the server's other databases, each a
 * way in. It is drawn from the lists the rail already read, so it costs
 * nothing, and the page is never a list beside an empty half.
 *
 * A database with nothing of the reader's in it says which of three things
 * that is: one the server does not have at all (a link that outlived it),
 * one that holds only the server's own collections, or one that is simply
 * empty — and only the last leads with making a collection.
 */
export function DatabasePane({
  mongo,
  catalog,
  section,
  onNew,
}: {
  mongo: Mongo
  catalog: Catalog
  section: SectionId
  onNew?: () => void
}) {
  const { engine, database, conn, goto, href } = mongo
  const { collections, databases } = catalog
  const data = collections.data
  const place = engine.section(section)?.title ?? "Documents"

  const head = (
    <ObjectHead
      mark={<NameMark name={database} />}
      name={database || "No database"}
      kind={database && database === conn.database ? <Tag>connects here</Tag> : undefined}
      verbs={
        onNew &&
        database &&
        !RESERVED.has(database) && (
          <Button size="xs" variant="outline" onClick={onNew}>
            <Plus />
            New {engine.nouns.object}
          </Button>
        )
      }
    />
  )

  const all = databases.data ?? []
  const others = all.filter((entry) => entry.name !== database)
  // A database's share is of everything the server holds, this one included:
  // of the others alone, it would change with the database on screen.
  const serverData = all.every((entry) => entry.statsKnown)
    ? all.reduce((sum, entry) => sum + entry.dataSize, 0)
    : 0
  const elsewhere: RankItem[] = others.map((entry) => ({
    key: entry.name,
    label: entry.name,
    mark: <NameMark name={entry.name} />,
    value: entry.statsKnown ? bytes(entry.dataSize) : "—",
    share: serverData > 0 ? entry.dataSize / serverData : undefined,
    hint: entry.statsKnown
      ? `${grouped(entry.collections)} ${entry.collections === 1 ? engine.nouns.object : engine.nouns.objects}`
      : "not measured",
    title: `Open ${entry.name}`,
    onClick: () => goto(section, { db: entry.name, collection: null }),
  }))
  const elsewhereBlock = elsewhere.length > 0 && (
    <section aria-label="Other databases on this server" className="space-y-2">
      <PanelHeader
        plain
        level={3}
        title="Other databases on this server"
        count={grouped(elsewhere.length)}
      />
      <RankList items={elsewhere} />
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
              <ErrorState retryable error={databases.error} onRetry={databases.refresh} />
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
    const system = data.collections.length
    // An empty database does not exist for the server until it holds a
    // collection, so the one the connection names is not "missing" for being
    // absent from the list; any other name that is absent is.
    const missing =
      system === 0 &&
      databases.data !== undefined &&
      database !== conn.database &&
      !databases.data.some((entry) => entry.name === database)
    return (
      <div data-slot="mongo-database" className="flex min-h-0 min-w-0 flex-1 flex-col">
        {head}
        <div className="min-h-0 flex-1 overflow-auto">
          <div className="animate-rise space-y-6 px-5 py-5">
            {missing ? (
              <EmptyState
                mark={<EngineMark engine={engine} />}
                title={`The server has no database called ${database}`}
                description="A database exists once it holds a collection. This one may have been dropped since the link was made, or never made."
                action={
                  <div className="flex flex-wrap justify-center gap-2">
                    {conn.database && (
                      <Button size="sm" variant="outline" asChild>
                        <Link href={href(section, { db: conn.database, collection: null })}>
                          Open {conn.database}
                        </Link>
                      </Button>
                    )}
                    {onNew && (
                      <Button size="sm" variant="ghost" onClick={onNew}>
                        <Plus />
                        Make it, with a first {engine.nouns.object}
                      </Button>
                    )}
                  </div>
                }
              />
            ) : system > 0 ? (
              <EmptyState
                mark={<EngineMark engine={engine} />}
                title={`${database} holds only the server's own ${engine.nouns.objects}`}
                description={`${grouped(system)} of them, listed under “The server's own” beside this. Nothing in it is a ${engine.nouns.object} of yours.`}
              />
            ) : (
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
            )}
            <div className="max-w-2xl">{elsewhereBlock}</div>
          </div>
        </div>
      </div>
    )
  }

  const ranked = [...own].sort((a, b) => b.size - a.size || a.name.localeCompare(b.name))
  // Of every collection's data, where each was measured; a view holds none.
  const held = own.every((entry) => entry.statsKnown || entry.type === "view")
    ? own.reduce((sum, entry) => sum + (entry.statsKnown ? entry.size : 0), 0)
    : 0
  const rows: RankItem[] = ranked.slice(0, RANKED).map((entry) => ({
    key: entry.name,
    label: entry.name,
    mark: <CollectionMark kind={collectionKind(entry)} />,
    value: entry.statsKnown ? bytes(entry.size) : "—",
    share: entry.statsKnown && held > 0 ? entry.size / held : undefined,
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
          {!data ? (
            <div className="space-y-2" aria-hidden>
              <Skeleton className="h-3.5 w-36" />
              <RankSkeleton rows={6} />
            </div>
          ) : (
            <div className="grid items-start gap-x-10 gap-y-7 @3xl:grid-cols-2 [&>*]:min-w-0">
              <section aria-label="By the data they hold" className="space-y-2">
                <PanelHeader
                  plain
                  level={3}
                  title="By the data they hold"
                  count={
                    own.length > rows.length &&
                    `${grouped(own.length - rows.length)} smaller ones are in the list`
                  }
                />
                <RankList items={rows} />
              </section>
              {elsewhereBlock}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
