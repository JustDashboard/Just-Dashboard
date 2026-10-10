"use client"

import { Activity } from "@/components/database/home/activity"
import { mongoConcerns, placementConcerns } from "@/components/database/home/attention"
import { ConcernAttention } from "@/components/database/home/attention-block"
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
import { gauge, mongoSample } from "@/components/database/home/samples"
import { StateRegion } from "@/components/database/home/state-region"
import type { MongoCollections, MongoStats } from "@/components/database/home/types"
import { useSamples } from "@/components/database/home/use-samples"
import { UsedBy } from "@/components/database/home/used-by"
import { useDatabase } from "@/components/database/shell/database-context"
import { usePoll } from "@/hooks/use-poll"
import { useMemo } from "react"

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
  const dumps = backups.data?.files
  const events = useMemo(() => chartEvents(samples, "uptimeSeconds", dumps), [samples, dumps])
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
      <StateRegion />
      {unread && (
        <CouldNotRead what="the server's statistics" error={unread} onRetry={stats.refresh} />
      )}
      <Activity
        id={id}
        hours={stats.hours}
        onHours={stats.setHours}
        views={MONGO_VIEWS}
        samples={samples}
        loading={stats.loading}
        error={stats.error}
        events={events}
      />
      <Pair>
        <ConcernAttention
          concerns={concerns}
          pending={stats.loading}
          quiet="Connections, the cache, locks and where it listens are all within limits"
        />
        <UsedBy />
      </Pair>
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
