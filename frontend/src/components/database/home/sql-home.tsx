"use client"

import { Activity } from "@/components/database/home/activity"
import { CouldNotRead } from "@/components/database/home/blocks"
import { BusiestStatements, FileFacts } from "@/components/database/home/busiest"
import { CLICKHOUSE_VIEWS, SQL_VIEWS } from "@/components/database/home/charts"
import { chartEvents } from "@/components/database/home/events"
import { HomeIdentity } from "@/components/database/home/identity"
import { LargestTablesBlock, type LargestTables } from "@/components/database/home/largest"
import { bySchema, schemaDetail } from "@/components/database/home/holdings"
import { nameHue } from "@/components/database/home/kinds"
import { Columns, Pair } from "@/components/database/home/layout"
import { HomePicture, type Holding } from "@/components/database/home/picture"
import { read } from "@/components/database/home/read"
import {
  BackupsBlock,
  ReachableFrom,
  RunsAs,
  ServerDatabases,
  useBackups,
} from "@/components/database/home/reference"
import { gauge, sqlSample } from "@/components/database/home/samples"
import { ServerLog } from "@/components/database/home/server-log"
import { StateRegion } from "@/components/database/home/state-region"
import type { DbServerStats, DbTableStats } from "@/components/database/home/types"
import { useSamples } from "@/components/database/home/use-samples"
import { useDatabase } from "@/components/database/shell/database-context"
import { Layers } from "@/components/icons"
import { Notice } from "@/components/state"
import { usePoll, type PollState } from "@/hooks/use-poll"
import type { DbOverview } from "@/lib/types"
import { useMemo } from "react"

export function SqlHome() {
  const { id, engine, href } = useDatabase()
  const file = engine.can("fileBased")
  const analytic = engine.can("clickhouseViews")
  const stats = useSamples<DbServerStats>(
    id,
    sqlSample,
    engine.can("stats"),
    engine.can("stats") && !file,
  )
  const largest = useLargestTables()
  const backups = useBackups(engine.can("dump"))

  const answer = stats.answer
  const samples = stats.samples
  const unread = stats.error && samples.length === 0 ? stats.error : undefined
  const refused = answer && !answer.supported ? answer : undefined

  const dumps = backups.data?.files
  const events = useMemo(() => chartEvents(samples, "uptimeSeconds", dumps), [samples, dumps])
  const tables = largest.data
  const holdings = useMemo<Holding[] | undefined>(
    () =>
      tables &&
      bySchema(tables.tables, tables.sizesKnown).map((share) => ({
        key: share.schema,
        name: share.schema,
        kind: capital(engine.nouns.container),
        detail: schemaDetail(share, tables.sizesKnown, engine.nouns.object),
        color: nameHue(share.schema),
        glyph: Layers,
        href: href("data", { schema: share.schema, table: null }),
      })),
    [tables, engine.nouns, href],
  )

  return (
    <>
      <HomeIdentity uptimeSeconds={answer?.uptimeSeconds} />
      <StateRegion />
      {refused && (
        <Notice title="This server's statistics are not available">
          <p className="wrap-anywhere">{refused.reason}</p>
        </Notice>
      )}
      {unread && (
        <CouldNotRead what="the server's statistics" error={unread} onRetry={stats.refresh} />
      )}
      <HomePicture
        holdings={holdings}
        holdingsWord={capital(engine.nouns.containers)}
        sessions={gauge(samples, "sessions")}
        size={answer?.databaseBytes ?? gauge(samples, "databaseBytes")}
        backups={engine.can("dump") ? backups : undefined}
      />
      {engine.can("stats") && !file && (
        <Activity
          hours={stats.hours}
          onHours={stats.setHours}
          retentionHours={stats.retentionHours}
          views={analytic ? CLICKHOUSE_VIEWS : SQL_VIEWS}
          samples={samples}
          loading={stats.loading}
          error={stats.error}
          events={events}
        />
      )}
      {engine.has("logs") && <ServerLog />}
      <Pair>
        {engine.can("statements") ? (
          <BusiestStatements />
        ) : engine.can("sqliteFile") ? (
          <FileFacts />
        ) : null}
        <LargestTablesBlock poll={largest} />
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

const capital = (word: string) => word.charAt(0).toUpperCase() + word.slice(1)
