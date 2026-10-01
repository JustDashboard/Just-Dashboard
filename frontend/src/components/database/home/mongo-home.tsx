"use client"

import { usePoll } from "@/hooks/use-poll"
import { Activity } from "@/components/database/home/activity"
import { ConcernAttention } from "@/components/database/home/attention-block"
import { mongoConcerns, placementConcerns } from "@/components/database/home/attention"
import { CouldNotRead } from "@/components/database/home/blocks"
import { SlowOperations } from "@/components/database/home/busiest"
import { MONGO_VIEWS } from "@/components/database/home/charts"
import { HomeIdentity } from "@/components/database/home/identity"
import { Pair } from "@/components/database/home/layout"
import { LargestCollectionsBlock } from "@/components/database/home/largest"
import { read } from "@/components/database/home/read"
import {
  backupReading,
  collectionReadings,
  mongoReadings,
} from "@/components/database/home/readings"
import {
  BackupsBlock,
  ReachableFrom,
  RunsAs,
  ServerDatabases,
  useBackups,
} from "@/components/database/home/reference"
import { gauge, mongoSample } from "@/components/database/home/samples"
import { ReadingTiles } from "@/components/database/home/tiles"
import type { MongoCollections, MongoStats } from "@/components/database/home/types"
import { UsedBy } from "@/components/database/home/used-by"
import { useSamples } from "@/components/database/home/use-samples"
import { useDatabase } from "@/components/database/shell/database-context"

/**
 * The home of a document database.
 *
 * Its figures are operations a second, connections against what is left, the
 * storage engine's cache, and then what this database holds — its data size
 * and its collections, counted in documents — and when it was last dumped.
 * Under them: the slow operations its profiler kept where a SQL server has
 * statements, and its collections where one has tables. Like a key–value
 * store it has no advisor, so what needs attention is read off its own
 * figures.
 */
export function DocumentHome() {
  const { id, conn, engine, summary } = useDatabase()
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
  const now = samples[samples.length - 1]?.at ?? Date.parse(summary?.checkedAt ?? conn.createdAt)
  const unread = stats.error && samples.length === 0 ? stats.error : undefined
  const own = mongoReadings(samples).map((reading) =>
    stats.loading
      ? { ...reading, value: undefined, hint: undefined, pending: true }
      : unread
        ? { ...reading, value: undefined, hint: "Could not be read", trend: undefined }
        : reading,
  )
  const readings = [
    ...own,
    ...collectionReadings(
      engine.nouns,
      collections.data,
      collections.error && !collections.data ? collections.error.message : undefined,
    ),
    backupReading(
      {
        lastBackup: summary?.lastBackup,
        newest: backups.data?.files[0],
        running: Boolean(backups.data?.job),
      },
      now,
    ),
  ]
  const concerns = [
    ...mongoConcerns(samples),
    ...placementConcerns(
      {
        name: conn.name,
        exposure: summary?.exposure ?? "unknown",
        lastBackup: backups.data?.files[0]?.takenAt ?? summary?.lastBackup,
        dumps: engine.can("dump"),
        managed: Boolean(summary?.managed),
      },
      now,
    ),
  ]

  return (
    <>
      <HomeIdentity uptimeSeconds={gauge(samples, "uptimeSeconds")} />
      <ReadingTiles readings={readings} />
      {unread && (
        <CouldNotRead what="the server's statistics" error={unread} onRetry={stats.refresh} />
      )}
      <Activity
        id={id}
        views={MONGO_VIEWS}
        samples={samples}
        loading={stats.loading}
        error={stats.error}
      />
      <Pair>
        <ConcernAttention
          concerns={concerns}
          pending={stats.loading}
          quiet="Connections, the cache, locks and where it listens are all within limits"
        />
        {engine.can("profiler") && <SlowOperations />}
      </Pair>
      <Pair>
        {engine.can("collections") && <LargestCollectionsBlock poll={collections} />}
        <UsedBy />
      </Pair>
      <Pair>
        <RunsAs />
        {engine.can("server") && <ReachableFrom />}
        {engine.can("dump") && <BackupsBlock backups={backups} />}
        {engine.can("server") && <ServerDatabases />}
      </Pair>
    </>
  )
}
