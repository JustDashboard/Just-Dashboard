"use client"

import { useMemo } from "react"
import Link from "next/link"
import { ArrowRight, Key } from "@/components/icons"
import { get } from "@/lib/api"
import { bytes, plural, relativeTime, timestamp } from "@/lib/format"
import type { DbAccess, DbFleet, DbOverview, DbTable, DbTopology } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { Page, DetailList, Detail, Section } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { BarList, type BarListItem } from "@/components/bar-list"
import { FindingList, type Finding } from "@/components/finding-list"
import { Notice } from "@/components/state"
import { Skeleton } from "@/components/ui/skeleton"
import { Status } from "@/components/status-dot"
import { useDatabase } from "@/components/database/db-context"
import { ConnectionStrings } from "@/components/database/connection-string"
import { DatabaseTopology } from "@/components/database/topology"
import { engineLabel, fleetVersion, fleetWhere } from "@/components/database/fleet-card"
import { fleetConcern, fleetFinding } from "@/components/database/fleet"

/** How many of the largest tables the page draws before pointing at Browse. */
const TOP_TABLES = 8

/**
 * A database's own front page: what somebody who just opened it wants before
 * they want a table.
 *
 * The section used to land on Browse — a rail of twenty-two table names and
 * a grid saying "pick a table" — which is the page for the fourth thing you
 * do with a database, not the first. A hosted database's dashboard answers
 * the first three before it shows a row: how do I connect to this, what is
 * in it and how big, and is anything wrong with it. That is this page's
 * order: the connection string in the shapes it is pasted in, the facts
 * beside it, then what needs a hand, then the largest tables as bars you
 * open in Browse, and the things reading it in the map's own picture.
 *
 * The facts are a list rather than tiles because tiles came off every page in
 * this section: the four the Connection page opened on — tables, rows, size,
 * added — are here, beside the readings the fleet dials for every card on
 * the control center (reachable, sessions, what it feeds, last backup, where
 * its port answers), as one column the eye reads down. The facts the strip
 * over the page already carries (engine, address, user) are not said again.
 *
 * `/databases/fleet` is read for the one entry it has about this connection:
 * it is the read that already dials, sizes and counts every connection for
 * the control center, and asking it once more is cheaper than four reads
 * that each answer a quarter of it.
 */
export default function DatabaseOverviewPage() {
  const { can } = useAuth()
  const { conn, info, goto, hrefFor } = useDatabase()
  const admin = can("system.admin")
  const sql = Boolean(info?.sql)
  const kind = info?.kind ?? "sql"

  const fleet = usePoll(
    (signal) => get<DbFleet>("/databases/fleet", undefined, signal),
    60_000,
    [conn?.id],
    { enabled: Boolean(conn) },
  )
  const access = usePoll<DbAccess>(
    (signal) =>
      conn
        ? get<DbAccess>(`/databases/${conn.id}/access`, undefined, signal)
        : Promise.reject(new Error("no connection")),
    0,
    [conn?.id, admin],
    { enabled: Boolean(conn) && admin },
  )
  const overview = usePoll<DbOverview>(
    (signal) =>
      conn
        ? get<DbOverview>(`/databases/${conn.id}/overview`, { schema: "" }, signal)
        : Promise.reject(new Error("no connection")),
    60_000,
    [conn?.id, sql],
    { enabled: Boolean(conn) && sql },
  )
  // A document store has no size breakdown; its collections come from the
  // catalogue the rail lists, with whatever size the engine reports.
  const collections = usePoll<DbTable[]>(
    (signal) =>
      conn
        ? get<DbTable[]>(`/databases/${conn.id}/tables`, { schema: "" }, signal)
        : Promise.reject(new Error("no connection")),
    60_000,
    [conn?.id, kind],
    { enabled: Boolean(conn) && kind === "document" },
  )
  const consumers = usePoll<DbTopology>(
    (signal) =>
      conn
        ? get<DbTopology>(`/databases/${conn.id}/consumers`, undefined, signal)
        : Promise.reject(new Error("no connection")),
    45_000,
    [conn?.id],
    { enabled: Boolean(conn) },
  )

  const entry = fleet.data?.connections.find((e) => e.id === conn?.id)
  // Decided once per read rather than per render: the concern reads the
  // clock, and a render is not allowed to.
  const concern = useMemo(() => (entry ? fleetConcern(entry) : null), [entry])
  const findings = useMemo<Finding[]>(() => {
    if (!entry) return []
    const finding = fleetFinding(entry, (path) => goto(path.replace(/\?.*$/, "")))
    return finding ? [{ ...finding, meta: engineLabel(entry.driver) }] : []
  }, [entry, goto])

  const tables = useMemo<BarListItem[]>(() => {
    if (!conn) return []
    if (sql) {
      const list = [...(overview.data?.tables ?? [])].sort((a, b) => b.bytes - a.bytes)
      const max = list[0]?.bytes ?? 0
      return list.slice(0, TOP_TABLES).map((t) => ({
        key: `${t.schema}.${t.table}`,
        label: t.table,
        value: overview.data?.sizesKnown ? bytes(t.bytes) : t.rows.toLocaleString(),
        share: max > 0 ? t.bytes / max : 0,
        hint: overview.data?.sizesKnown ? `${t.rows.toLocaleString()} rows` : undefined,
        title: `Browse ${t.table}`,
        onClick: () => goto("/databases/browse", { schema: t.schema, table: t.table }),
      }))
    }
    const list = [...(collections.data ?? [])].sort(
      (a, b) => (b.size ?? b.estimatedRows) - (a.size ?? a.estimatedRows),
    )
    const max = list[0] ? (list[0].size ?? list[0].estimatedRows) : 0
    return list.slice(0, TOP_TABLES).map((t) => ({
      key: `${t.schema}.${t.name}`,
      label: t.name,
      value: t.size !== undefined ? bytes(t.size) : t.estimatedRows.toLocaleString(),
      share: max > 0 ? (t.size ?? t.estimatedRows) / max : 0,
      hint: t.size !== undefined ? `${t.estimatedRows.toLocaleString()} documents` : undefined,
      title: `Browse ${t.name}`,
      onClick: () => goto("/databases/browse", { schema: t.schema, table: t.name }),
    }))
  }, [conn, sql, overview.data, collections.data, goto])

  if (!conn) return null

  const objectWord = kind === "keyvalue" ? "keyspace" : kind === "document" ? "collection" : "table"
  const o = overview.data
  const unreachable = entry ? !entry.ok : false
  const stale = concern?.reason.startsWith("last backup") ?? false

  return (
    <Page className="animate-rise">
      {unreachable && (
        <Notice title="This connection cannot sign in" icon={Key} tone="warning">
          <p className="font-mono text-xs break-words">{entry?.error}</p>
          <p>
            Test the stored password or set a new one on the{" "}
            <Link href={hrefFor("/databases/connection")} className="underline">
              Connection page
            </Link>
            .
          </p>
        </Notice>
      )}

      <div className="grid items-start gap-8 lg:grid-cols-[minmax(0,3fr)_minmax(0,2fr)] [&>*]:min-w-0">
        {admin && conn.driver !== "sqlite" ? (
          <ConnectionStrings conn={conn} access={access.data} />
        ) : (
          <ConnectionStrings conn={conn} />
        )}

        <Panel plain>
          <PanelHeader title="At a glance" />
          <PanelBody className="pt-3">
            {!entry && fleet.loading ? (
              <div className="space-y-2">
                <Skeleton className="h-4 w-2/3" />
                <Skeleton className="h-4 w-1/2" />
                <Skeleton className="h-4 w-3/5" />
              </div>
            ) : (
              <DetailList className="gap-y-2.5">
                <Detail label="Status">
                  {entry ? (
                    <span className="flex flex-wrap items-center gap-x-2">
                      <Status
                        verdict={entry.ok ? "ok" : "critical"}
                        label={entry.ok ? "connected" : "unreachable"}
                      />
                      {entry.ok && (
                        <span className="numeric text-muted-foreground">
                          answers in {entry.latencyMs} ms
                        </span>
                      )}
                    </span>
                  ) : (
                    "—"
                  )}
                </Detail>
                <Detail label="Engine">
                  {engineLabel(conn.driver)}
                  {entry && fleetVersion(entry) ? ` ${fleetVersion(entry)}` : ""}
                </Detail>
                <Detail label="Runs">{entry ? fleetWhere(entry) : conn.host || "here"}</Detail>
                <Detail label="Stored">
                  {entry?.sizesKnown ? (
                    <span className="numeric">{bytes(entry.bytes)}</span>
                  ) : (
                    <span className="text-muted-foreground">not reported by this engine</span>
                  )}
                </Detail>
                <Detail label={objectWord === "collection" ? "Collections" : "Tables"}>
                  {entry ? (
                    <span className="numeric">
                      {entry.objects.toLocaleString()}
                      {sql && o
                        ? ` · ${o.totalRows.toLocaleString()} rows, the engine's estimate`
                        : ""}
                    </span>
                  ) : (
                    "—"
                  )}
                </Detail>
                <Detail label="Sessions">
                  {entry ? (
                    <span className="numeric">
                      {entry.sessions.toLocaleString()} open
                      {sql && o ? ` · ${o.pool.inUse} of ${o.pool.open} from this dashboard` : ""}
                    </span>
                  ) : (
                    "—"
                  )}
                </Detail>
                <Detail label="Feeds">
                  {entry ? (
                    entry.consumers > 0 ? (
                      <Link href="#feeds" className="hover:underline">
                        {plural(entry.consumers, "deployment")}
                      </Link>
                    ) : (
                      <span className="text-muted-foreground">nothing seen reading it yet</span>
                    )
                  ) : (
                    "—"
                  )}
                </Detail>
                <Detail label="Reachable from">
                  {entry ? (
                    entry.exposure === "public" ? (
                      <span className="text-warning">the internet</span>
                    ) : entry.exposure === "private" ? (
                      "one address"
                    ) : entry.exposure === "remote" ? (
                      "wherever it is saved as"
                    ) : (
                      "this server only"
                    )
                  ) : (
                    "—"
                  )}
                </Detail>
                {entry && entry.source !== "file" && (
                  <Detail label="Last backup">
                    {entry.lastBackup ? (
                      <Link
                        href={hrefFor("/databases/backups")}
                        className={stale ? "text-warning hover:underline" : "hover:underline"}
                        title={timestamp(entry.lastBackup)}
                      >
                        {relativeTime(entry.lastBackup)}
                      </Link>
                    ) : (
                      <Link
                        href={hrefFor("/databases/backups")}
                        className="text-warning hover:underline"
                      >
                        never
                      </Link>
                    )}
                  </Detail>
                )}
                <Detail label="Added">{timestamp(conn.createdAt)}</Detail>
              </DetailList>
            )}
          </PanelBody>
        </Panel>
      </div>

      {findings.length > 0 && (
        <Section title="Needs attention">
          <FindingList findings={findings} />
        </Section>
      )}

      {kind !== "keyvalue" && (
        <Panel plain>
          <PanelHeader
            title={
              entry
                ? `${plural(entry.objects, objectWord)}${entry.sizesKnown ? ` · ${bytes(entry.bytes)}` : ""}`
                : objectWord === "collection"
                  ? "Collections"
                  : "Tables"
            }
            actions={
              <Link
                href={hrefFor("/databases/browse")}
                className="flex items-center gap-1 text-body text-muted-foreground hover:text-foreground"
              >
                Browse them <ArrowRight className="size-3.5" />
              </Link>
            }
          />
          <PanelBody className="pt-1">
            {(
              sql ? overview.loading && !overview.data : collections.loading && !collections.data
            ) ? (
              <div className="space-y-2 py-2">
                <Skeleton className="h-5 w-full" />
                <Skeleton className="h-5 w-5/6" />
                <Skeleton className="h-5 w-2/3" />
              </div>
            ) : (
              <BarList
                items={tables}
                emptyLabel={`No ${objectWord}s yet. Create one under Browse, or import a dump under Backups.`}
              />
            )}
          </PanelBody>
        </Panel>
      )}

      <Section
        title={<span id="feeds">What it feeds</span>}
        actions={
          <Link
            href="/databases/topology"
            className="flex items-center gap-1 text-body text-muted-foreground hover:text-foreground"
          >
            Open the map <ArrowRight className="size-3.5" />
          </Link>
        }
      >
        {consumers.data ? (
          <DatabaseTopology topology={consumers.data} compact />
        ) : (
          <Skeleton className="h-32 rounded-xl" />
        )}
      </Section>
    </Page>
  )
}
