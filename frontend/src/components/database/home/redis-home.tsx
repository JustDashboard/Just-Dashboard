"use client"

import { useMemo } from "react"
import { usePoll } from "@/hooks/use-poll"
import { Activity } from "@/components/database/home/activity"
import { ConcernAttention } from "@/components/database/home/attention-block"
import { placementConcerns, redisConcerns } from "@/components/database/home/attention"
import { CouldNotRead } from "@/components/database/home/blocks"
import { BusiestCommands } from "@/components/database/home/busiest"
import { REDIS_VIEWS } from "@/components/database/home/charts"
import { chartEvents } from "@/components/database/home/events"
import { HomeIdentity } from "@/components/database/home/identity"
import { Columns, Pair } from "@/components/database/home/layout"
import { LargestNamespacesBlock } from "@/components/database/home/largest"
import { read, record } from "@/components/database/home/read"
import { redisReadings, staled } from "@/components/database/home/readings"
import {
  BackupsBlock,
  Keyspaces,
  ReachableFrom,
  RunsAs,
  useBackups,
} from "@/components/database/home/reference"
import { gauge, redisSample } from "@/components/database/home/samples"
import { StateRegion } from "@/components/database/home/state-region"
import { ReadingTiles } from "@/components/database/home/tiles"
import type { RedisServer, RedisStats, RedisTree } from "@/components/database/home/types"
import { UsedBy } from "@/components/database/home/used-by"
import { useSamples } from "@/components/database/home/use-samples"
import { useDatabase } from "@/components/database/shell/database-context"

/**
 * The home of a key–value store: Redis and the servers that speak its
 * protocol.
 *
 * Its six figures are its own — commands a second, memory against its limit,
 * what its cache is worth, who is connected, how many keys, when it last
 * saved — and so is what sits under them: the commands it spends its time in
 * where a SQL server has statements, its namespaces where one has tables, and
 * its numbered databases where one has siblings. The server has no advisor,
 * so what needs attention is read off the same answers the figures come from.
 *
 * The page must not measure itself. Ranking the namespaces costs the server
 * a SCAN and a TYPE for every key walked, and on a quiet store that walk was
 * the spike in "commands a second" and the scale of the chart for the five
 * minutes it stayed in the window. So the walk is taken first and the
 * statistics are read only once it is done: it is in the first reading's
 * totals and in no interval between two of them.
 */
export function KeyValueHome() {
  const { id, conn, engine, summary } = useDatabase()
  // One bounded walk of the keyspace when the page opens. Never on a timer:
  // it costs the server a SCAN and a TYPE for every key it covers.
  const walks = engine.can("keyTree")
  const tree = usePoll(
    (signal) =>
      read<RedisTree>(
        `/databases/${id}/keys/tree`,
        (answer) => Array.isArray(answer.folders),
        { limit: WALK },
        signal,
      ),
    0,
    [id],
    { enabled: walks },
  )
  const walked = !walks || !tree.loading
  const stats = useSamples<RedisStats>(id, redisSample, engine.can("stats") && walked)
  const server = usePoll(
    (signal) =>
      read<RedisServer>(
        `/databases/${id}/redis/server`,
        (answer) =>
          Array.isArray(answer.keyspace) &&
          record(answer.memory) &&
          record(answer.replication) &&
          record(answer.persistence) &&
          record(answer.persistence.rdb) &&
          record(answer.persistence.aof),
        undefined,
        signal,
      ),
    15_000,
    [id],
    { enabled: engine.can("serverInfo") },
  )
  const backups = useBackups(engine.can("dump"))

  const samples = stats.samples
  const now = samples[samples.length - 1]?.at ?? Date.parse(summary?.checkedAt ?? conn.createdAt)
  const unread = stats.error && samples.length === 0 ? stats.error : undefined
  const loading = stats.loading || !walked
  const stale = Boolean(stats.error) && samples.length > 0
  const figures = redisReadings(samples, server.data)
  const readings = (stale ? staled(figures, samples[samples.length - 1]?.at) : figures).map(
    (reading) =>
      loading
        ? { ...reading, value: undefined, hint: undefined, pending: true }
        : unread
          ? { ...reading, value: undefined, hint: "Could not be read", trend: undefined }
          : reading,
  )
  const dumps = backups.data?.files
  const events = useMemo(() => chartEvents(samples, "uptime_in_seconds", dumps), [samples, dumps])
  const concerns = [
    ...redisConcerns(server.data, samples),
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
      <HomeIdentity uptimeSeconds={gauge(samples, "uptime_in_seconds")} />
      <StateRegion />
      <ReadingTiles readings={readings} />
      {unread && (
        <CouldNotRead what="the server's statistics" error={unread} onRetry={stats.refresh} />
      )}
      <Activity
        id={id}
        views={REDIS_VIEWS}
        samples={samples}
        loading={loading}
        error={stats.error}
        events={events}
      />
      <Pair>
        <ConcernAttention
          concerns={concerns}
          pending={loading || server.loading}
          quiet="Memory, persistence, clients and where it listens are all within limits"
        />
        <UsedBy />
      </Pair>
      <Pair>
        {engine.can("commandStats") && <BusiestCommands />}
        {walks && <LargestNamespacesBlock poll={tree} />}
      </Pair>
      <Columns>
        <RunsAs />
        {engine.can("server") && <ReachableFrom />}
        {engine.can("dump") && <BackupsBlock backups={backups} />}
      </Columns>
      {engine.can("serverInfo") && <Keyspaces server={server} />}
    </>
  )
}

/**
 * How many keys the walk covers. Enough to rank the namespaces of a store of
 * ordinary size whole, and a bounded cost on one of any size: the block says
 * what share of the keyspace it saw when that is not all of it.
 */
const WALK = 5_000
