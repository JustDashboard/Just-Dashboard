"use client"

import { useState } from "react"
import { ArrowRight, CloudDownload, Pencil, Trash, WarningFill } from "@/components/icons"
import Link from "next/link"
import { notify } from "@/lib/toast"
import { del, downloadUrl, get, post } from "@/lib/api"
import { bytes, timestamp } from "@/lib/format"
import type { DbConnection, DbTable } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { Page, PageHeader, DetailList, Detail } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/state"
import { ConnectionDialog } from "@/components/database/connection-dialog"
import { useDatabase } from "@/components/database/db-context"

export default function ConnectionPage() {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const { conn, info, refreshConnections, goto, hrefFor } = useDatabase()
  const [editing, setEditing] = useState(false)

  // Redis has no table catalogue — its browser lists keyspaces itself — so the
  // count panel is only asked for where the endpoint means something.
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

  if (!conn) return null

  const label = info?.label ?? conn.driver
  const objectWord =
    info?.kind === "keyvalue" ? "keyspace" : info?.kind === "document" ? "collection" : "table"

  return (
    <Page>
      <PageHeader
        eyebrow="Databases"
        title={conn.name}
        actions={
          <>
            {can("service.control") && <BackupButton conn={conn} />}
            {can("system.admin") && (
              <>
                <Button size="sm" variant="ghost" onClick={() => setEditing(true)}>
                  <Pencil className="size-4" />
                  Edit
                </Button>
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={() =>
                    confirm({
                      title: "Remove connection",
                      confirmLabel: "Remove",
                      description: (
                        <p>
                          Removes <b>{conn.name}</b> from the dashboard. The database itself is not
                          touched.
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
                  <Trash className="size-4" />
                  Remove
                </Button>
                <Button
                  size="sm"
                  variant="ghost"
                  className="text-destructive"
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
                            Nothing here can bring it back — take a dump first if you might want it.
                            If this database was started from this page, its container keeps
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
                  <WarningFill className="size-4" />
                  Delete database
                </Button>
              </>
            )}
          </>
        }
      />

      <div className="grid items-start gap-4 lg:grid-cols-2 [&>*]:min-w-0">
        <Panel>
          <PanelHeader title="Details" />
          <PanelBody>
            <DetailList>
              <Detail label="Engine">{label}</Detail>
              <Detail label="Host">{conn.host || "this server"}</Detail>
              <Detail label="Port">{conn.port || "—"}</Detail>
              <Detail label="User">{conn.user || "—"}</Detail>
              <Detail label={info?.kind === "keyvalue" ? "Keyspace" : "Database"}>
                <span className="font-mono">{conn.database || "—"}</span>
              </Detail>
              <Detail label="Added">{timestamp(conn.createdAt)}</Detail>
            </DetailList>
          </PanelBody>
        </Panel>

        <Panel>
          <PanelHeader
            title={
              objectWord === "collection"
                ? "Collections"
                : objectWord === "keyspace"
                  ? "Keys"
                  : "Tables"
            }
            actions={
              <Link
                href={hrefFor("/databases")}
                className="flex items-center gap-1 text-hint font-medium text-muted-foreground hover:text-foreground"
              >
                Browse <ArrowRight className="size-3" />
              </Link>
            }
          />
          <PanelBody>
            {!countable ? (
              <p className="text-sm text-muted-foreground">
                Open Browse to work through this keyspace.
              </p>
            ) : tables.loading && !tables.data ? (
              <Spinner className="text-muted-foreground" />
            ) : (
              <p className="text-sm">
                <span className="numeric text-lg font-semibold">{tables.data?.length ?? 0}</span>{" "}
                <span className="text-muted-foreground">
                  {(tables.data?.length ?? 0) === 1 ? objectWord : `${objectWord}s`}
                </span>
              </p>
            )}
          </PanelBody>
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
      <CloudDownload className="size-4" />
      Dump &amp; download
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
