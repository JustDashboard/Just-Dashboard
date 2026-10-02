"use client"

import { Activity } from "@/components/database/home/activity"
import { placementConcerns, redisConcerns } from "@/components/database/home/attention"
import { ConcernAttention } from "@/components/database/home/attention-block"
import { CouldNotRead } from "@/components/database/home/blocks"
import { BusiestCommands } from "@/components/database/home/busiest"
import { REDIS_VIEWS } from "@/components/database/home/charts"
import { chartEvents } from "@/components/database/home/events"
import { HomeIdentity } from "@/components/database/home/identity"
import { LargestNamespacesBlock } from "@/components/database/home/largest"
import { Columns, Pair } from "@/components/database/home/layout"
import { read, record } from "@/components/database/home/read"
import {
  BackupsBlock,
  Keyspaces,
  ReachableFrom,
  RunsAs,
  useBackups,
} from "@/components/database/home/reference"
import { gauge, redisSample } from "@/components/database/home/samples"
import { StateRegion } from "@/components/database/home/state-region"
import type { RedisServer, RedisStats, RedisTree } from "@/components/database/home/types"
import { useSamples } from "@/components/database/home/use-samples"
import { UsedBy } from "@/components/database/home/used-by"
import { useDatabase } from "@/components/database/shell/database-context"
import { usePoll } from "@/hooks/use-poll"
import { useMemo } from "react"

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
  const stats = useSamples<RedisStats>(id, redisSample, engine.can("stats"))
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
      {unread && (
        <CouldNotRead what="the server's statistics" error={unread} onRetry={stats.refresh} />
      )}
      <Activity
        id={id}
        hours={stats.hours}
        onHours={stats.setHours}
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
