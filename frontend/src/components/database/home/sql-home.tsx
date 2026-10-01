"use client"

import type { DbOverview } from "@/lib/types"
import { usePoll, type PollState } from "@/hooks/use-poll"
import { Notice } from "@/components/state"
import { Activity } from "@/components/database/home/activity"
import { AdvisorAttention, ConcernAttention } from "@/components/database/home/attention-block"
import { placementConcerns } from "@/components/database/home/attention"
import { CouldNotRead } from "@/components/database/home/blocks"
import { BusiestStatements, FileFacts } from "@/components/database/home/busiest"
import { CLICKHOUSE_VIEWS, SQL_VIEWS } from "@/components/database/home/charts"
import { HomeIdentity } from "@/components/database/home/identity"
import { Pair } from "@/components/database/home/layout"
import { LargestTablesBlock, type LargestTables } from "@/components/database/home/largest"
import { read } from "@/components/database/home/read"
import {
  backupReading,
  clickhouseReadings,
  holdingsReading,
  sqlReadings,
  sqliteReadings,
  type Holdings,
  type Reading,
} from "@/components/database/home/readings"
import {
  BackupsBlock,
  ReachableFrom,
  RunsAs,
  ServerDatabases,
  useBackups,
} from "@/components/database/home/reference"
import { sqlSample } from "@/components/database/home/samples"
import { ReadingTiles } from "@/components/database/home/tiles"
import type { DbServerStats, DbTableStats } from "@/components/database/home/types"
import { UsedBy } from "@/components/database/home/used-by"
import { useSamples } from "@/components/database/home/use-samples"
import { useDatabase } from "@/components/database/shell/database-context"

/**
 * The home of a SQL database.
 *
 * Three engines read differently enough to have their own six figures, and
 * the registry says which: a file-based one (SQLite) is its file — size,
 * pages, journal — and has no sessions or rates to chart; ClickHouse is read
 * by its queries, inserts and parts, and by the machine under it; every other
 * SQL server is sessions against the limit, transactions, the cache, its
 * size, what it holds and when it was last backed up. The blocks under the
 * figures are the same for all three.
 */
export function SqlHome() {
  const { id, conn, engine, summary } = useDatabase()
  const file = engine.can("fileBased")
  const analytic = engine.can("clickhouseViews")
  const stats = useSamples<DbServerStats>(id, sqlSample, engine.can("stats"))
  const largest = useLargestTables()
  const backups = useBackups(engine.can("dump"))

  const answer = stats.answer
  const samples = stats.samples
  // Ages are measured against the newest reading the page holds — the
  // server's, then the summary's — not against a clock read while drawing.
  const now = samples[samples.length - 1]?.at ?? Date.parse(summary?.checkedAt ?? conn.createdAt)
  const unread = stats.error && samples.length === 0 ? stats.error : undefined
  const refused = answer && !answer.supported ? answer : undefined

  const backup = backupReading(
    {
      lastBackup: summary?.lastBackup,
      newest: backups.data?.files[0],
      running: Boolean(backups.data?.job),
    },
    now,
  )
  const held = holdingsReading(
    engine.nouns,
    holdingsOf(largest.data),
    largest.error && !largest.data ? largest.error.message : undefined,
  )
  const pending = engine.can("stats") && stats.loading
  const own: Reading[] = file
    ? sqliteReadings(samples, answer)
    : analytic
      ? clickhouseReadings(samples)
      : sqlReadings(samples, answer)
  const readings = (analytic ? own : file ? [...own, backup] : [...own, held, backup]).map(
    (reading) =>
      // The figures of a snapshot that has not landed hold their place; the
      // ones read elsewhere say what they have as soon as they have it.
      pending && own.includes(reading)
        ? { ...reading, value: undefined, hint: undefined, pending: true }
        : unread && own.includes(reading)
          ? { ...reading, value: undefined, hint: "Could not be read", trend: undefined }
          : reading,
  )

  return (
    <>
      <HomeIdentity uptimeSeconds={answer?.uptimeSeconds} />
      {refused && (
        <Notice title="This server's statistics are not available">
          <p className="wrap-anywhere">{refused.reason}</p>
        </Notice>
      )}
      <ReadingTiles readings={engine.can("stats") ? readings : [held, backup]} />
      {unread && (
        <CouldNotRead what="the server's statistics" error={unread} onRetry={stats.refresh} />
      )}
      {engine.can("stats") && !file && (
        <Activity
          id={id}
          views={analytic ? CLICKHOUSE_VIEWS : SQL_VIEWS}
          samples={samples}
          loading={stats.loading}
          error={stats.error}
        />
      )}
      <Pair>
        {engine.can("advisor") ? (
          <AdvisorAttention />
        ) : (
          <ConcernAttention
            pending={false}
            quiet="Nothing about how it is placed needs you"
            concerns={placementConcerns(
              {
                name: conn.name,
                exposure: summary?.exposure ?? "unknown",
                lastBackup: summary?.lastBackup,
                dumps: engine.can("dump"),
                managed: Boolean(summary?.managed),
              },
              now,
            )}
          />
        )}
        {engine.can("statements") ? (
          <BusiestStatements />
        ) : engine.can("sqliteFile") ? (
          <FileFacts />
        ) : null}
      </Pair>
      <Pair>
        <LargestTablesBlock poll={largest} />
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

/** What the largest-tables read says the database holds in all. */
function holdingsOf(data: LargestTables | undefined): Holdings | undefined {
  if (!data) return undefined
  const counted = data.tables.filter((table) => table.rows >= 0)
  return {
    objects: data.tables.length,
    more: data.truncated,
    rows: counted.length > 0 ? counted.reduce((total, table) => total + table.rows, 0) : undefined,
  }
}

/** How many tables are read for the ranking and the count. The server's own default. */
const READ = 200

/**
 * The database's tables with their sizes, largest first. An engine with table
 * statistics answers for every schema at once; one without them is asked for
 * its storage overview instead, which ranks the connection's own schema.
 */
function useLargestTables(): PollState<LargestTables> {
  const { id, engine } = useDatabase()
  const ranked = engine.can("tableStats")
  return usePoll(
    async (signal): Promise<LargestTables> => {
      if (ranked) {
        const stats = await read<DbTableStats>(
          `/databases/${id}/tablestats`,
          (answer) => Array.isArray(answer.tables),
          { limit: READ },
          signal,
        )
        if (stats.supported) {
          return {
            tables: stats.tables.map((table) => ({
              schema: table.schema,
              table: table.table,
              rows: table.rows,
              bytes: table.totalBytes,
            })),
            sizesKnown: stats.tables.length === 0 || stats.tables.some((t) => t.totalBytes > 0),
            truncated: stats.truncated,
          }
        }
      }
      const overview = await read<DbOverview>(
        `/databases/${id}/overview`,
        (answer) => Array.isArray(answer.tables),
        { schema: "" },
        signal,
      )
      const weigh = (table: DbOverview["tables"][number]) =>
        overview.sizesKnown ? table.bytes : table.rows
      return {
        tables: [...overview.tables]
          .sort((a, b) => weigh(b) - weigh(a))
          .map((table) => ({
            schema: table.schema,
            table: table.table,
            rows: table.rows,
            bytes: table.bytes,
          })),
        sizesKnown: overview.sizesKnown,
        truncated: false,
      }
    },
    120_000,
    [id, ranked],
  )
}
