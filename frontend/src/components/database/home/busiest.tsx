"use client"

import { useMemo, useState } from "react"
import { post, put } from "@/lib/api"
import { bytes, percent, relativeTime, timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { BarList, type BarListItem } from "@/components/bar-list"
import { Detail, DetailList } from "@/components/page"
import { Button } from "@/components/ui/button"
import {
  BarsSkeleton,
  Block,
  BlockLink,
  FactsSkeleton,
  Quiet,
  Read,
  staleOf,
} from "@/components/database/home/blocks"
import { read, record } from "@/components/database/home/read"
import { compact } from "@/components/database/home/readings"
import type {
  DbStatements,
  MongoProfiler,
  RedisCommandStats,
  SqliteFile,
} from "@/components/database/home/types"
import { useDatabase } from "@/components/database/shell/database-context"

/** How many rows a ranked block shows. */
const TOP = 6

/** A statement on one line, as a list has room for it. The whole of it is the row's title. */
function oneLine(text: string): string {
  return text.replace(/\s+/g, " ").trim()
}

/** Milliseconds at the precision a reader compares them at. */
export function millis(value: number): string {
  if (value >= 1000) return `${(value / 1000).toFixed(value >= 10_000 ? 0 : 1)} s`
  if (value >= 10) return `${Math.round(value)} ms`
  return `${value.toFixed(value >= 1 ? 1 : 2)} ms`
}

/**
 * The statements the server spent its time on, by their share of all of it.
 *
 * The text is a shape with its literals removed — the server never sends one
 * execution — so a row is something to recognise, and it opens the page that
 * has every figure about it. Where the statistics are off, the block says
 * what turns them on, and offers it where one statement does.
 */
export function BusiestStatements() {
  const { id, engine, readOnly, href, goto } = useDatabase()
  const { can } = useAuth()
  const [enabling, setEnabling] = useState(false)
  const statements = usePoll(
    (signal) =>
      read<DbStatements>(
        `/databases/${id}/statements`,
        (answer) => Array.isArray(answer.statements),
        { limit: TOP },
        signal,
      ),
    60_000,
    [id],
  )

  const enable = async (extension: string) => {
    setEnabling(true)
    try {
      await post(`/databases/${id}/server/extensions`, { name: extension })
      notify.success("Statement statistics are on", {
        description: "They count from now; the list fills as statements run.",
      })
      statements.refresh()
    } catch (err) {
      notify.error("Could not turn on statement statistics", err)
    } finally {
      setEnabling(false)
    }
  }

  const items = useMemo<BarListItem[]>(() => {
    const list = statements.data?.statements ?? []
    const longest = Math.max(...list.map((statement) => statement.share), 0)
    return list.map((statement) => ({
      key: statement.id,
      label: oneLine(statement.query),
      title: `Open Performance · ${oneLine(statement.query).slice(0, 400)}`,
      value: percent(statement.share * 100, statement.share >= 0.1 ? 0 : 1),
      share: longest > 0 ? statement.share / longest : 0,
      hint: `${compact(statement.calls)} calls · ${millis(statement.meanMs)} each`,
      onClick: () => goto("performance", { view: "statements" }),
    }))
  }, [statements.data, goto])

  const words = engine.nouns.statements
  return (
    <Block
      title={`Busiest ${words}`}
      stale={staleOf(statements)}
      actions={
        statements.data?.supported && (
          <BlockLink href={href("performance", { view: "statements" })}>Performance</BlockLink>
        )
      }
    >
      <Read poll={statements} what={`the busiest ${words}`} skeleton={<BarsSkeleton rows={TOP} />}>
        {(data) =>
          data.supported ? (
            <>
              <BarList
                items={items}
                emptyLabel={`No ${engine.nouns.statement} has been counted yet.`}
              />
              {items.length > 0 && (
                <p className="mt-2 px-2 text-hint text-muted-foreground">
                  Share of the runtime of every {engine.nouns.statement} counted
                  {data.since ? ` since ${relativeTime(data.since)}` : ""}.
                </p>
              )}
            </>
          ) : (
            <div className="space-y-3">
              <Quiet>
                {data.enable
                  ? `This server is not counting its ${words}.`
                  : `The ${words} this server has run could not be read.`}{" "}
                <span className="wrap-anywhere">{data.enable?.note ?? data.reason}</span>
              </Quiet>
              {data.enable?.sql && data.enable.extension && can("system.admin") && !readOnly && (
                <Button
                  size="sm"
                  variant="outline"
                  pending={enabling}
                  onClick={() => void enable(data.enable?.extension ?? "")}
                >
                  Turn on statement statistics
                </Button>
              )}
            </div>
          )
        }
      </Read>
    </Block>
  )
}

/** A Redis server's commands, by the time it has spent in each since it started. */
export function BusiestCommands() {
  const { id, href, goto } = useDatabase()
  const stats = usePoll(
    (signal) =>
      read<RedisCommandStats>(
        `/databases/${id}/redis/commandstats`,
        (answer) => Array.isArray(answer.commands),
        undefined,
        signal,
      ),
    15_000,
    [id],
  )
  const items = useMemo<BarListItem[]>(() => {
    const data = stats.data
    if (!data) return []
    const list = data.commands.slice(0, TOP)
    const longest = list[0]?.usec ?? 0
    return list.map((entry) => ({
      key: entry.command,
      label: entry.command,
      title: `Open Performance · ${entry.command}`,
      value: percent(data.totalUsec > 0 ? (entry.usec / data.totalUsec) * 100 : 0, 0),
      share: longest > 0 ? entry.usec / longest : 0,
      hint: `${compact(entry.calls)} calls · ${entry.usecPerCall.toFixed(entry.usecPerCall >= 10 ? 0 : 1)} µs each`,
      onClick: () => goto("performance", { view: "commands" }),
    }))
  }, [stats.data, goto])
  return (
    <Block
      title="Busiest commands"
      stale={staleOf(stats)}
      actions={<BlockLink href={href("performance", { view: "commands" })}>Performance</BlockLink>}
    >
      <Read poll={stats} what="the command statistics" skeleton={<BarsSkeleton rows={TOP} />}>
        {(data) => (
          <>
            <BarList items={items} emptyLabel="No command has been counted yet." />
            {items.length > 0 && (
              <p className="mt-2 px-2 text-hint text-muted-foreground">
                Share of the time spent in all {compact(data.totalCalls)} commands since the server
                started.
              </p>
            )}
          </>
        )}
      </Read>
    </Block>
  )
}

/**
 * MongoDB's slow operations, as its profiler recorded them for this database.
 * The profiler is off until somebody turns it on, and then the block says
 * so, with the switch where the role may throw it.
 */
export function SlowOperations() {
  const { id, conn, readOnly, href, goto } = useDatabase()
  const { can } = useAuth()
  const [switching, setSwitching] = useState(false)
  const profiler = usePoll(
    (signal) =>
      read<MongoProfiler>(
        `/databases/${id}/mongo/profiler`,
        (answer) => Array.isArray(answer.entries),
        { database: conn.database || undefined, limit: 50 },
        signal,
      ),
    30_000,
    [id],
  )

  const record = async (database: string) => {
    setSwitching(true)
    try {
      await put(`/databases/${id}/mongo/profiler`, { database, level: 1 })
      notify.success("Slow operations are being recorded", {
        description: `The profiler of ${database} keeps every operation slower than its threshold.`,
      })
      profiler.refresh()
    } catch (err) {
      notify.error("Could not turn the profiler on", err)
    } finally {
      setSwitching(false)
    }
  }

  const items = useMemo<BarListItem[]>(() => {
    const list = [...(profiler.data?.entries ?? [])]
      .sort((a, b) => b.millis - a.millis)
      .slice(0, TOP)
    const longest = list[0]?.millis ?? 0
    return list.map((entry, index) => ({
      key: `${entry.time}:${index}`,
      label: `${entry.op} ${entry.ns}`,
      title: `Open Performance · ${oneLine(entry.command).slice(0, 400)}`,
      value: millis(entry.millis),
      share: longest > 0 ? entry.millis / longest : 0,
      hint: [entry.planSummary, `${compact(entry.docsExamined)} examined`]
        .filter(Boolean)
        .join(" · "),
      onClick: () => goto("performance", { view: "slow" }),
    }))
  }, [profiler.data, goto])

  return (
    <Block
      title="Slowest operations"
      stale={staleOf(profiler)}
      actions={<BlockLink href={href("performance", { view: "slow" })}>Performance</BlockLink>}
    >
      <Read poll={profiler} what="the profiler" skeleton={<BarsSkeleton rows={TOP} />}>
        {(data) =>
          items.length > 0 ? (
            <>
              <BarList items={items} />
              <p className="mt-2 px-2 text-hint text-muted-foreground">
                The slowest of the last {data.entries.length} operations the profiler of{" "}
                <span className="font-mono">{data.database}</span> kept
                {data.level === 0 ? "; it is off now" : ` (slower than ${data.slowMs} ms)`}.
              </p>
            </>
          ) : data.level === 0 ? (
            <div className="space-y-3">
              <Quiet>
                The profiler of <span className="font-mono">{data.database}</span> is off, so no
                operation has been recorded. On, it keeps every operation slower than {data.slowMs}{" "}
                ms.
              </Quiet>
              {can("system.admin") && !readOnly && (
                <Button
                  size="sm"
                  variant="outline"
                  pending={switching}
                  onClick={() => void record(data.database)}
                >
                  Record slow operations
                </Button>
              )}
            </div>
          ) : (
            <Quiet>
              Nothing slower than {data.slowMs} ms has run on{" "}
              <span className="font-mono">{data.database}</span> since the profiler was turned on.
            </Quiet>
          )
        }
      </Read>
    </Block>
  )
}

/**
 * A SQLite database keeps no statement statistics; what it has instead is
 * the file. These are the facts about it the tiles above have no room for.
 */
export function FileFacts() {
  const { id, href } = useDatabase()
  const file = usePoll(
    (signal) =>
      read<SqliteFile>(
        `/databases/${id}/sqlite/file`,
        (answer) => record(answer.objects),
        undefined,
        signal,
      ),
    60_000,
    [id],
  )
  return (
    <Block
      title="The file"
      stale={staleOf(file)}
      actions={<BlockLink href={href("settings")}>Settings</BlockLink>}
    >
      <Read poll={file} what="the file" skeleton={<FactsSkeleton rows={7} />}>
        {(data) => (
          <DetailList>
            <Detail label="Path" className="font-mono wrap-anywhere">
              {data.path}
            </Detail>
            <Detail label="Size">
              {bytes(data.fileBytes)}
              {data.walBytes > 0 ? ` · ${bytes(data.walBytes)} write-ahead log` : ""}
            </Detail>
            {data.modified && (
              <Detail label="Last written">
                <span title={timestamp(data.modified)}>{relativeTime(data.modified)}</span>
              </Detail>
            )}
            <Detail label="Holds">
              {(["table", "index", "view", "trigger"] as const)
                .filter((kind) => data.objects[kind])
                .map(
                  (kind) =>
                    `${data.objects[kind]} ${
                      data.objects[kind] === 1 ? kind : kind === "index" ? "indexes" : `${kind}s`
                    }`,
                )
                .join(" · ") || "nothing yet"}
            </Detail>
            <Detail label="Journal">
              {data.journalMode} · synchronous {data.synchronous}
            </Detail>
            <Detail label="Foreign keys">{data.foreignKeys ? "enforced" : "not enforced"}</Detail>
            <Detail label="Auto-vacuum">{data.autoVacuum}</Detail>
            <Detail label="Text">{data.encoding}</Detail>
            <Detail label="SQLite">{data.version}</Detail>
          </DetailList>
        )}
      </Read>
    </Block>
  )
}
