"use client"

import { useMemo, useState } from "react"
import { post, put } from "@/lib/api"
import { bytes, percent, plural, relativeTime, timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { InfoTip } from "@/components/form"
import { RankList, RankSkeleton, type RankItem } from "@/components/rank-list"
import { Button } from "@/components/ui/button"
import { Block, FactRow, FactRows, FactsSkeleton } from "@/components/database/home/blocks"
import { housekeeping } from "@/components/database/home/housekeeping"
import { read, record } from "@/components/database/home/read"
import { compact } from "@/components/database/home/readings"
import type { MongoProfiler, RedisCommandStats, SqliteFile } from "@/components/database/home/types"
import { useStatementsSetup } from "@/components/database/ops/statements-setup"
import { setupStart } from "@/components/database/ops/statements-setup-model"
import { useDatabase } from "@/components/database/shell/database-context"
import { counted, millis } from "@/components/database/kit/figures"
import { Read, staleOf } from "@/components/database/kit/read"
import { useTopStatements } from "@/components/database/kit/top-statements"
import { NotAvailable } from "@/components/database/ops/performance-parts"
import { EmptyNote } from "@/components/state"

/** How many rows a ranked block shows. */
const TOP = 6

/**
 * The statements the server spent its time on, by their share of all of it.
 *
 * The text is a shape with its literals removed — the server never sends one
 * execution — so a row is something to recognise, and it opens the page that
 * has every figure about it. The rows are the Queries view's
 * (`useTopStatements`), in the same words. Where the statistics are off, the
 * block says what turns them on, and offers it where one statement does — or,
 * where the module is not loaded yet, the guided setup the Statements view
 * opens.
 */
export function BusiestStatements() {
  const { id, engine, readOnly, href } = useDatabase()
  const { can } = useAuth()
  const [enabling, setEnabling] = useState(false)
  const { items, state: statements } = useTopStatements(id, { limit: TOP, interval: 60_000 })
  const openSetup = useStatementsSetup(statements.refresh)

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

  const words = engine.nouns.statements
  const since = statements.data?.since
  return (
    <Block
      title={`Busiest ${words}`}
      stale={staleOf(statements)}
      count={
        statements.data?.supported &&
        items.length > 0 && (
          <span
            data-slot="head-fact"
            title={`Share of the runtime of every ${engine.nouns.statement} counted${since ? `, starting ${relativeTime(since)}` : ""}.`}
          >
            share of runtime{since ? ` since ${relativeTime(since)}` : ""}
          </span>
        )
      }
      link={
        statements.data?.supported
          ? { href: href("performance", { view: "statements" }), label: "Performance" }
          : undefined
      }
    >
      <Read
        rise
        poll={statements}
        what={`the busiest ${words}`}
        skeleton={<RankSkeleton rows={TOP} />}
      >
        {(data) => {
          if (data.supported) {
            return items.length === 0 ? (
              <EmptyNote>
                No {engine.nouns.statement} has been counted yet. The list fills as they run.
              </EmptyNote>
            ) : (
              <RankList items={items} />
            )
          }
          const admin = can("system.admin") && !readOnly
          const action =
            data.enable?.sql && data.enable.extension && admin ? (
              <Button
                size="sm"
                variant="outline"
                pending={enabling}
                onClick={() => void enable(data.enable?.extension ?? "")}
              >
                Turn on statement statistics
              </Button>
            ) : data.enable &&
              setupStart(data.enable) &&
              openSetup &&
              engine.can("statementsSetup") &&
              admin ? (
              <Button
                size="sm"
                variant="outline"
                onClick={() => data.enable && openSetup({ enable: data.enable })}
              >
                Turn on statement statistics
              </Button>
            ) : undefined
          return (
            <NotAvailable
              title={
                data.enable
                  ? `This server is not counting its ${words}`
                  : `The ${words} this server has run could not be read`
              }
              reason={
                <>
                  {sentence(data.enable?.note ?? data.reason)}
                  {data.enable?.blocked && ` ${sentence(data.enable.blocked)}`}
                </>
              }
              action={action}
            />
          )
        }}
      </Read>
    </Block>
  )
}

/** A reason in the server's words, closed as the sentence it is. */
function sentence(text: string | undefined): string {
  const trimmed = (text ?? "").trim()
  return !trimmed || /[.!?]$/.test(trimmed) ? trimmed : `${trimmed}.`
}

/** A Redis server's commands, by the time it has spent in each since it started. */
export function BusiestCommands() {
  const { id, href } = useDatabase()
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
  const ranked = useMemo(() => {
    const data = stats.data
    if (!data) return undefined
    const work = data.commands.filter((entry) => !housekeeping(entry.command))
    const usec = work.reduce((total, entry) => total + entry.usec, 0)
    const count = work.reduce((total, entry) => total + entry.calls, 0)
    return { work, usec, count, apart: data.totalUsec - usec }
  }, [stats.data])
  const items = useMemo<RankItem[]>(() => {
    if (!ranked) return []
    const list = ranked.work.slice(0, TOP)
    return list.map((entry) => {
      const share = percent(ranked.usec > 0 ? (entry.usec / ranked.usec) * 100 : 0, 0)
      return {
        key: entry.command,
        label: entry.command,
        title: `${entry.command} — ${share} of the time. Open Performance`,
        value: share,
        hint: `${counted(entry.calls, "call")} · ${entry.usecPerCall.toFixed(entry.usecPerCall >= 10 ? 0 : 1)} µs each`,
        href: href("performance", { view: "commands" }),
      }
    })
  }, [ranked, href])
  return (
    <Block
      title="Busiest commands"
      stale={staleOf(stats)}
      count={items.length > 0 && ranked && "share of time on keys since it started"}
      link={{ href: href("performance", { view: "commands" }), label: "Performance" }}
      actions={
        items.length > 0 &&
        ranked && (
          <InfoTip label="What the shares are of">
            Share of the time spent in {plural(ranked.count, "command")} on keys since the server
            started.
            {ranked.apart > 0 && stats.data && stats.data.totalUsec > 0
              ? ` INFO, COMMAND and the other commands that ask about the server — this dashboard's own among them — took another ${percent((ranked.apart / stats.data.totalUsec) * 100, 0)} of the server's time and are left out.`
              : ""}
          </InfoTip>
        )
      }
    >
      <Read rise poll={stats} what="the command statistics" skeleton={<RankSkeleton rows={TOP} />}>
        {() =>
          items.length === 0 ? (
            <EmptyNote>
              No command that reads or writes a key has been counted since the server started.
            </EmptyNote>
          ) : (
            <RankList items={items} />
          )
        }
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
  const { id, conn, readOnly, href } = useDatabase()
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

  const items = useMemo<RankItem[]>(() => {
    const list = [...(profiler.data?.entries ?? [])]
      .sort((a, b) => b.millis - a.millis)
      .slice(0, TOP)
    return list.map((entry, index) => ({
      key: `${entry.time}:${index}`,
      label: `${entry.op} ${entry.ns}`,
      title: `${entry.op} ${entry.ns} — ${millis(entry.millis)}. Open Performance`,
      value: millis(entry.millis),
      hint: [entry.planSummary, `${compact(entry.docsExamined)} examined`]
        .filter(Boolean)
        .join(" · "),
      href: href("performance", { view: "slow" }),
    }))
  }, [profiler.data, href])

  return (
    <Block
      title="Slowest operations"
      stale={staleOf(profiler)}
      link={{ href: href("performance", { view: "slow" }), label: "Performance" }}
      count={
        items.length > 0 &&
        profiler.data && (
          <span
            data-slot="head-fact"
            title={`The slowest of the last ${profiler.data.entries.length} operations the profiler of ${profiler.data.database} kept${profiler.data.level === 0 ? "; it is off now" : ` (slower than ${profiler.data.slowMs} ms)`}.`}
          >
            of the last {profiler.data.entries.length} kept
            {profiler.data.level === 0
              ? " · profiler off now"
              : ` · slower than ${profiler.data.slowMs} ms`}
          </span>
        )
      }
    >
      <Read rise poll={profiler} what="the profiler" skeleton={<RankSkeleton rows={TOP} />}>
        {(data) =>
          items.length > 0 ? (
            <RankList items={items} />
          ) : data.level === 0 ? (
            <NotAvailable
              title="Slow operations are not being recorded"
              reason={
                <>
                  The profiler of <span className="font-mono">{data.database}</span> is off, so no
                  operation has been recorded. On, it keeps every operation slower than{" "}
                  {data.slowMs} ms.
                </>
              }
              action={
                can("system.admin") &&
                !readOnly && (
                  <Button
                    size="sm"
                    variant="outline"
                    pending={switching}
                    onClick={() => void record(data.database)}
                  >
                    Record slow operations
                  </Button>
                )
              }
            />
          ) : (
            <EmptyNote>
              Nothing slower than {data.slowMs} ms has run on{" "}
              <span className="font-mono">{data.database}</span> since the profiler was turned on.
            </EmptyNote>
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
      link={{ href: href("settings"), label: "Settings" }}
    >
      <Read rise poll={file} what="the file" skeleton={<FactsSkeleton rows={7} />}>
        {(data) => (
          <FactRows>
            <FactRow label="Path" className="font-mono wrap-anywhere">
              {data.path}
            </FactRow>
            <FactRow label="Size">
              {bytes(data.fileBytes)}
              {data.walBytes > 0 ? ` · ${bytes(data.walBytes)} write-ahead log` : ""}
            </FactRow>
            {data.modified && (
              <FactRow label="Last written">
                <span title={timestamp(data.modified)}>{relativeTime(data.modified)}</span>
              </FactRow>
            )}
            <FactRow label="Holds">
              {(["table", "index", "view", "trigger"] as const)
                .filter((kind) => data.objects[kind])
                .map(
                  (kind) =>
                    `${data.objects[kind]} ${
                      data.objects[kind] === 1 ? kind : kind === "index" ? "indexes" : `${kind}s`
                    }`,
                )
                .join(" · ") || "nothing yet"}
            </FactRow>
            <FactRow label="Journal">
              {data.journalMode} · synchronous {data.synchronous}
            </FactRow>
            <FactRow label="Foreign keys">{data.foreignKeys ? "enforced" : "not enforced"}</FactRow>
            <FactRow label="Auto-vacuum">{data.autoVacuum}</FactRow>
            <FactRow label="Text">{data.encoding}</FactRow>
            <FactRow label="SQLite">{data.version}</FactRow>
          </FactRows>
        )}
      </Read>
    </Block>
  )
}
