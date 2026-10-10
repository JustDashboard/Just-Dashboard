"use client"

import { useState } from "react"
import { usePoll } from "@/hooks/use-poll"
import { BarList, type BarListItem } from "@/components/bar-list"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, LoadingRows } from "@/components/state"
import { mongoCollections, previewPipeline } from "@/components/database/mongo/api"
import { CollectionMark, collectionKind } from "@/components/database/mongo/kinds"
import {
  COUNTERS_PIPELINE,
  activityOf,
  countersOf,
  watched,
  type CollectionCounters,
} from "@/components/database/mongo/performance/activity"
import type { Mongo } from "@/components/database/mongo/use-mongo"
import { micros, perSecond } from "@/components/database/redis/performance/samples"

/** How many collections are watched: each is one small read every time. */
const WATCHED = 8
/** How often their counters are read. */
const EVERY_MS = 10_000

type Reading = Readonly<Record<string, CollectionCounters | null>>

const grouped = (n: number) => n.toLocaleString("en-US")

/**
 * Which collections the server is working on: reads and writes a second,
 * collection by collection, busiest first.
 *
 * The server keeps a count of the operations on each collection since it
 * started; two readings of it give a rate, the way the page's other figures
 * are made. Only the largest few collections of the database on screen are
 * watched — one small read each, every ten seconds, while this view is open —
 * and the panel says which. Before the second reading there is no rate yet,
 * and the bars show what each has been asked in all. The panel's own reads
 * are left out of what it reports.
 *
 * A server that keeps no such count (one that only speaks the protocol)
 * answers nothing, and the panel is not drawn.
 */
export function BusiestCollections({ mongo }: { mongo: Mongo }) {
  const { id, database, goto } = mongo
  const listed = usePoll((signal) => mongoCollections(id, database, signal), 120_000, [
    id,
    database,
  ])
  const names = watched(listed.data?.collections ?? [], WATCHED).map((entry) => entry.name)
  const key = names.join("\u0000")
  const read = usePoll(
    async (signal): Promise<Reading> => {
      const answers = await Promise.all(
        names.map((collection) =>
          previewPipeline({ id, database, collection }, COUNTERS_PIPELINE, 0, 1, signal)
            .then((answer) => countersOf(answer.documents[0]?.canonical))
            // One collection that cannot be read does not take the others with it.
            .catch(() => null),
        ),
      )
      return Object.fromEntries(names.map((name, at) => [name, answers[at]]))
    },
    EVERY_MS,
    [id, database, key],
    { enabled: names.length > 0 },
  )
  // The reading before the one on screen: a rate is the difference of the two.
  const [pair, setPair] = useState<{ scope: string; before?: Reading; now?: Reading }>({
    scope: key,
  })
  if (pair.scope !== key) setPair({ scope: key })
  else if (read.data && read.data !== pair.now)
    setPair({ scope: key, before: pair.now, now: read.data })

  if (!database) return null
  const now = pair.now
  // No collection answered with counters: this server does not keep them.
  if (now && names.every((name) => !now[name])) return null

  const rows = names.flatMap((name) => {
    const counters = now?.[name]
    if (!counters) return []
    // Each reading is one read of the collection, by this panel: it is not the collection's work.
    return [{ name, ...activityOf(pair.before?.[name] ?? undefined, counters, 1) }]
  })
  const live = rows.some((row) => row.perSecond !== null)
  const sorted = [...rows].sort((a, b) =>
    live ? (b.perSecond ?? 0) - (a.perSecond ?? 0) || b.total - a.total : b.total - a.total,
  )
  const top = Math.max(...sorted.map((row) => (live ? (row.perSecond ?? 0) : row.total)), 0)
  const info = new Map((listed.data?.collections ?? []).map((entry) => [entry.name, entry]))
  const items: BarListItem[] = sorted.map((row) => {
    const amount = live ? (row.perSecond ?? 0) : row.total
    const entry = info.get(row.name)
    return {
      key: row.name,
      label: row.name,
      mark: entry ? <CollectionMark kind={collectionKind(entry)} /> : undefined,
      value: live ? `${perSecond(amount)}/s` : grouped(row.total),
      share: top > 0 ? amount / top : 0,
      hint: live
        ? amount === 0
          ? "at rest"
          : [
              `${perSecond(row.readsPerSecond ?? 0)} reads, ${perSecond(row.writesPerSecond ?? 0)} writes a second`,
              row.readMicros !== null ? `a read takes ${micros(row.readMicros)}` : "",
            ]
              .filter(Boolean)
              .join(" · ")
        : "reads and writes since the server started",
      title: `Open ${row.name} in Documents`,
      onClick: () => goto("data", { db: database, collection: row.name }),
    }
  })
  const more = (listed.data?.collections ?? []).filter(
    (entry) => entry.type === "collection" && !entry.system,
  ).length

  return (
    <Panel plain data-slot="mongo-busiest">
      <PanelHeader
        title="Busiest collections"
        actions={
          <span className="numeric text-hint text-muted-foreground">
            {more > names.length
              ? `the ${names.length} largest of ${grouped(more)} in ${database}`
              : `in ${database}`}
          </span>
        }
      />
      <PanelBody>
        {listed.error && !listed.data ? (
          <EmptyNote>The collections of {database} could not be listed.</EmptyNote>
        ) : !listed.data || (names.length > 0 && !now) ? (
          <LoadingRows rows={4} />
        ) : items.length === 0 ? (
          <EmptyNote>{database} holds no collection to watch.</EmptyNote>
        ) : (
          <BarList items={items} className="-ml-2" />
        )}
      </PanelBody>
    </Panel>
  )
}
