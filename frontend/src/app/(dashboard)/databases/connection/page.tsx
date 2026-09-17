"use client"

import { useState } from "react"
import { ArrowRight, CloudDownload, Pencil, Trash, WarningFill } from "@/components/icons"
import Link from "next/link"
import { notify } from "@/lib/toast"
import { del, downloadUrl, get, post } from "@/lib/api"
import { bytes, relativeTime, timestamp } from "@/lib/format"
import type { DbConnection, DbOverview, DbTable } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { Page, DetailList, Detail } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { ConnectionDialog } from "@/components/database/connection-dialog"
import { useDatabase } from "@/components/database/db-context"

/**
 * The connection itself: what it is, how big it is, and the three things done
 * to it rarely enough that each deserves a sentence — a dump, forgetting it,
 * and dropping the database it points at.
 *
 * Readings first, as tiles on the page's own edge; the facts as a plain list;
 * the verbs as rows, each with the line of plain English that says what it
 * does, because "Remove" and "Delete" side by side in a header were two words
 * for two very different outcomes.
 */
export default function ConnectionPage() {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const { conn, info, refreshConnections, goto, hrefFor } = useDatabase()
  const [editing, setEditing] = useState(false)

  // Redis has no table catalogue — its browser lists keyspaces itself — so the
  // count is only asked for where the endpoint means something.
  const countable = info ? info.kind !== "keyvalue" : true
  const tables = usePoll<DbTable[]>(
    (signal) =>
      conn && countable
        ? get<DbTable[]>(`/databases/${conn.id}/tables`, { schema: "" }, signal)
        : Promise.resolve([]),
    0,
    [conn?.id, countable],
    { enabled: Boolean(conn) && countable },
  )
  // Sizes come from the same breakdown the Monitor tab draws, and only for a
  // SQL engine, which is the only kind that reports them.
  const sql = Boolean(info?.sql)
  const overview = usePoll<DbOverview>(
    (signal) =>
      conn && sql
        ? get<DbOverview>(`/databases/${conn.id}/overview`, { schema: "" }, signal)
        : Promise.reject(new Error("not a SQL engine")),
    0,
    [conn?.id, sql],
    { enabled: Boolean(conn) && sql },
  )

  if (!conn) return null

  const label = info?.label ?? conn.driver
  const objectWord =
    info?.kind === "keyvalue" ? "keyspace" : info?.kind === "document" ? "collection" : "table"
  const objects = tables.data?.length
  const o = overview.data

  return (
    <Page className="animate-rise">
      <StatGrid columns={4}>
        {countable ? (
          <StatTile
            label={objectWord === "collection" ? "Collections" : "Tables"}
            value={
              tables.loading && !tables.data ? (
                <Skeleton className="h-6 w-12" />
              ) : (
                (objects ?? 0).toLocaleString()
              )
            }
            hint={
              objects && objects > 0 ? (
                <Link href={hrefFor("/databases")} className="hover:text-foreground">
                  Browse them <ArrowRight className="inline size-3" />
                </Link>
              ) : (
                "none yet"
              )
            }
          />
        ) : (
          <StatTile label="Engine" value={label} hint="key–value store" />
        )}
        {sql && (
          <StatTile
            label="Rows"
            value={
              overview.loading && !o ? (
                <Skeleton className="h-6 w-16" />
              ) : (
                (o?.totalRows ?? 0).toLocaleString()
              )
            }
            hint="the engine's estimate"
          />
        )}
        {sql && (
          <StatTile
            label="Size"
            value={
              overview.loading && !o ? (
                <Skeleton className="h-6 w-16" />
              ) : o?.sizesKnown ? (
                bytes(o.totalBytes)
              ) : (
                "—"
              )
            }
            hint={o && !o.sizesKnown ? "not reported by this engine" : "data and indexes"}
          />
        )}
        <StatTile
          label="Added"
          value={relativeTime(conn.createdAt)}
          hint={timestamp(conn.createdAt)}
        />
      </StatGrid>

      <div className="grid items-start gap-8 lg:grid-cols-2 [&>*]:min-w-0">
        <Panel plain>
          <PanelHeader
            title="Details"
            actions={
              can("system.admin") && (
                <Button size="sm" variant="outline" onClick={() => setEditing(true)}>
                  <Pencil className="size-3.5" />
                  Edit
                </Button>
              )
            }
          />
          <PanelBody>
            <DetailList className="gap-y-2.5">
              <Detail label="Name">{conn.name}</Detail>
              <Detail label="Engine">{label}</Detail>
              <Detail label="Host">
                <span className="font-mono">{conn.host || "this server"}</span>
              </Detail>
              <Detail label="Port">
                <span className="font-mono">{conn.port || "—"}</span>
              </Detail>
              <Detail label="User">
                <span className="font-mono">{conn.user || "—"}</span>
              </Detail>
              <Detail label={info?.kind === "keyvalue" ? "Keyspace" : "Database"}>
                <span className="font-mono">{conn.database || "—"}</span>
              </Detail>
              <Detail label="Added">{timestamp(conn.createdAt)}</Detail>
            </DetailList>
          </PanelBody>
        </Panel>

        <Panel plain>
          <PanelHeader title="Maintenance" />
          <RowList>
            {can("service.control") && (
              <Row
                title="Dump and download"
                subtitle="Written on the server, where a restore reads it, and downloaded here as it finishes."
                trailing={<BackupButton conn={conn} />}
              />
            )}
            {can("system.admin") && (
              <>
                <Row
                  title="Remove from the dashboard"
                  subtitle="Forgets the connection and its saved queries. The database itself is not touched."
                  trailing={
                    <Button
                      size="sm"
                      variant="outline"
                      onClick={() =>
                        confirm({
                          title: "Remove connection",
                          confirmLabel: "Remove",
                          description: (
                            <p>
                              Removes <b>{conn.name}</b> from the dashboard. The database itself is
                              not touched.
                            </p>
                          ),
                          action: async (c) => {
                            await del(`/databases/${conn.id}`, { confirm: c })
                            refreshConnections()
                            goto("/databases")
                          },
                        })
                      }
                    >
                      <Trash className="size-3.5" />
                      Remove
                    </Button>
                  }
                />
                <Row
                  title="Delete the database"
                  subtitle={dropExplanation(conn)}
                  trailing={
                    <Button
                      size="sm"
                      variant="destructive"
                      onClick={() =>
                        confirm({
                          title: "Delete database",
                          confirmLabel: "Delete for good",
                          phrase: dropPhrase(conn),
                          description: (
                            <div className="space-y-2">
                              <p>
                                Deletes <b>{dropPhrase(conn)}</b> on {conn.host || "this server"}.{" "}
                                {dropExplanation(conn)}
                              </p>
                              <p>
                                Nothing here can bring it back — take a dump first if you might want
                                it. If this database was started from this page, its container keeps
                                running; remove that from Docker.
                              </p>
                            </div>
                          ),
                          action: async (c) => {
                            const res = await del<{ connectionRemoved: boolean }>(
                              `/databases/${conn.id}/database`,
                              { confirm: c, body: {} },
                            )
                            refreshConnections()
                            if (res.connectionRemoved) goto("/databases")
                          },
                        })
                      }
                    >
                      <WarningFill className="size-3.5" />
                      Delete…
                    </Button>
                  }
                />
              </>
            )}
            {!can("service.control") && (
              <Row
                title="Nothing to do here"
                subtitle="Your role can browse this connection but not dump, remove or delete it."
              />
            )}
          </RowList>
        </Panel>
      </div>

      {editing && (
        <ConnectionDialog
          key={conn.id}
          open
          onOpenChange={(o) => !o && setEditing(false)}
          onDone={refreshConnections}
          existing={conn}
        />
      )}
      {dialog}
    </Page>
  )
}

function BackupButton({ conn }: { conn: DbConnection }) {
  const [busy, setBusy] = useState(false)

  const download = (file: string) => {
    const a = document.createElement("a")
    a.href = downloadUrl(`/databases/${conn.id}/backup/download`, { file })
    a.download = file
    a.click()
  }

  const run = async () => {
    setBusy(true)
    try {
      const res = await post<{
        path: string
        file: string
        size: number
        duration: string
        summary?: string
      }>(`/databases/${conn.id}/backup`, { database: conn.database })
      download(res.file)
      notify.success("Dump complete", {
        description: [res.summary, `${bytes(res.size)} in ${res.duration}`, res.path]
          .filter(Boolean)
          .join(" · "),
        action: { label: "Download", onClick: () => download(res.file) },
      })
    } catch (err) {
      notify.error("Dump failed", err)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Button size="sm" variant="outline" onClick={run} pending={busy}>
      <CloudDownload className="size-3.5" />
      Dump
    </Button>
  )
}

/**
 * What the operator has to type to delete a database, mirroring the server's
 * dropTargetName. The server re-decides; this is what the dialog shows.
 */
function dropPhrase(conn: DbConnection) {
  if (conn.driver === "sqlite") return (conn.database ?? "").split("/").pop() ?? ""
  if (conn.driver === "redis") return `db${(conn.database || "0").replace(/^db/, "")}`
  return conn.database ?? ""
}

/** What deleting actually does on this engine, in one sentence for the dialog. */
function dropExplanation(conn: DbConnection) {
  switch (conn.driver) {
    case "sqlite":
      return "The database file is deleted from disk, along with its write-ahead log."
    case "redis":
      return "Every key in this keyspace is deleted. Redis keyspaces are fixed at startup, so the numbered database itself stays — emptied."
    case "oracle":
      return "The schema and everything it owns are dropped. Oracle refuses this while the account is connected, including from this dashboard."
    default:
      return "Every table, view, index and row in it is dropped. The server itself keeps running."
  }
}
