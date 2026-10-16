"use client"

import { Activity } from "@/components/database/home/activity"
import { CouldNotRead } from "@/components/database/home/blocks"
import { SlowOperations } from "@/components/database/home/busiest"
import { MONGO_VIEWS } from "@/components/database/home/charts"
import { chartEvents } from "@/components/database/home/events"
import { HomeIdentity } from "@/components/database/home/identity"
import { LargestCollectionsBlock } from "@/components/database/home/largest"
import { Columns, Pair } from "@/components/database/home/layout"
import { read } from "@/components/database/home/read"
import {
  BackupsBlock,
  ReachableFrom,
  RunsAs,
  ServerDatabases,
  useBackups,
} from "@/components/database/home/reference"
import { nameHue } from "@/components/database/home/kinds"
import { HomePicture, type Holding } from "@/components/database/home/picture"
import { compact } from "@/components/database/home/readings"
import { gauge, mongoSample } from "@/components/database/home/samples"
import { ServerLog } from "@/components/database/home/server-log"
import { StateRegion } from "@/components/database/home/state-region"
import type { MongoCollections, MongoStats } from "@/components/database/home/types"
import { useSamples } from "@/components/database/home/use-samples"
import { useDatabase } from "@/components/database/shell/database-context"
import { Layers } from "@/components/icons"
import { usePoll } from "@/hooks/use-poll"
import { bytes } from "@/lib/format"
import { useMemo } from "react"

export function DocumentHome() {
  const { id, conn, engine, href } = useDatabase()
  const stats = useSamples<MongoStats>(id, mongoSample, engine.can("stats"))
  const collections = usePoll(
    (signal) =>
      read<MongoCollections>(
        `/databases/${id}/mongo/collections`,
        (answer) => Array.isArray(answer.collections),
        { database: conn.database || undefined },
        signal,
      ),
    60_000,
    [id],
    { enabled: engine.can("collections") },
  )
  const backups = useBackups(engine.can("dump"))

  const samples = stats.samples
  const unread = stats.error && samples.length === 0 ? stats.error : undefined
  const dumps = backups.data?.files
  const events = useMemo(() => chartEvents(samples, "uptimeSeconds", dumps), [samples, dumps])
  const listed = collections.data
  const holdings = useMemo<Holding[] | undefined>(
    () =>
      listed &&
      listed.collections
        .filter((collection) => !collection.system)
        .sort((a, b) => b.size - a.size || b.count - a.count)
        .map((collection) => ({
          key: collection.name,
          name: collection.name,
          kind: collection.type === "view" ? "View" : "Collection",
          detail: collection.statsKnown
            ? `${bytes(collection.size)} · ${compact(collection.count)} ${collection.count === 1 ? "document" : "documents"}`
            : undefined,
          color: nameHue(collection.name),
          glyph: Layers,
          href: href("data", { db: listed.database, collection: collection.name }),
        })),
    [listed, href],
  )
  const stored = listed?.collections.reduce((sum, collection) => sum + collection.size, 0)

  return (
    <>
      <HomeIdentity uptimeSeconds={gauge(samples, "uptimeSeconds")} />
      <StateRegion />
      {unread && (
        <CouldNotRead what="the server's statistics" error={unread} onRetry={stats.refresh} />
      )}
      <HomePicture
        holdings={engine.can("collections") ? holdings : []}
        holdingsWord="Collections"
        sessions={gauge(samples, "connections")}
        size={stored}
        backups={engine.can("dump") ? backups : undefined}
      />
      <Activity
        hours={stats.hours}
        onHours={stats.setHours}
        views={MONGO_VIEWS}
        samples={samples}
        loading={stats.loading}
        error={stats.error}
        events={events}
      />
      {engine.has("logs") && <ServerLog />}
      <Pair>
        {engine.can("profiler") && <SlowOperations />}
        {engine.can("collections") && <LargestCollectionsBlock poll={collections} />}
      </Pair>
      <Columns>
        <RunsAs />
        {engine.can("server") && <ReachableFrom />}
        {engine.can("dump") && <BackupsBlock backups={backups} />}
      </Columns>
      {engine.can("server") && <ServerDatabases />}
    </>
  )
}
