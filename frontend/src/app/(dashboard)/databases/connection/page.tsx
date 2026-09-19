"use client"

import { useRef, useState } from "react"
import { forgetMemoryState, useMemoryState } from "@/lib/view-state"
import {
  ArrowRight,
  Check,
  CloudDownload,
  Copy,
  Eye,
  EyeOff,
  Key,
  Pencil,
  Trash,
  WarningFill,
} from "@/components/icons"
import Link from "next/link"
import { notify } from "@/lib/toast"
import { del, downloadUrl, get, post, put } from "@/lib/api"
import { bytes, relativeTime, timestamp } from "@/lib/format"
import { buildDsn, DEFAULT_PORT } from "@/lib/db-dsn"
import type { DbAccess, DbAccessChange, DbConnection, DbOverview, DbTable } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useCopy } from "@/hooks/use-copy"
import { useConfirm } from "@/components/confirm-dialog"
import { Page, DetailList, Detail } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Skeleton } from "@/components/ui/skeleton"
import { IconAction } from "@/components/icon-action"
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
 *
 * Between the readings and the facts sits the one thing an operator comes to
 * this page for after the first day: the connection string, to paste into an
 * application. It is shown masked, revealed or copied on purpose (the server
 * audits the reveal), and — where the server is a container on this host — a
 * second string carries the machine's public address, because a database
 * published to loopback is unreachable from the laptop the operator is
 * sitting at, and the switch that changes that lives under Maintenance.
 *
 * The Remove row is only drawn for a connection the sync would not re-add. A
 * database running on this server connects itself on the next page load, so
 * forgetting it was a button that did nothing for as long as it took to press
 * refresh.
 */
export default function ConnectionPage() {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const { conn, info, refreshConnections, goto, hrefFor } = useDatabase()
  const [editing, setEditing] = useMemoryState("databases.connection.editing", false)
  // Read at the action rather than rendered: the confirm dialog takes its
  // description once, so the checkbox inside it is uncontrolled and the
  // ref is what the delete reads.
  const removeContainer = useRef(false)
  const admin = can("system.admin")

  // Where the server is reachable from, and whether it is one the sync would
  // connect again. Admin only, because reading it lists containers and the
  // firewall, and every verb it feeds is admin's too.
  const access = usePoll<DbAccess>(
    (signal) =>
      conn
        ? get<DbAccess>(`/databases/${conn.id}/access`, undefined, signal)
        : Promise.reject(new Error("no connection")),
    0,
    [conn?.id, admin],
    { enabled: Boolean(conn) && admin },
  )
  // Whether the stored credentials still work, because the answer changes
  // what the delete dialog offers and what the page has to explain.
  const ping = usePoll<{ ok: boolean; error?: string }>(
    (signal) =>
      conn
        ? get(`/databases/${conn.id}/ping`, undefined, signal)
        : Promise.reject(new Error("no connection")),
    0,
    [conn?.id],
    { enabled: Boolean(conn) },
  )

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
  const a = access.data
  const unreachable = ping.data ? !ping.data.ok : false
  const container = a?.container && !a.composeProject ? a.container : undefined

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

      {unreachable && (
        <Notice title="This connection cannot sign in" icon={Key} tone="warning">
          <p className="font-mono text-xs break-words">{ping.data?.error}</p>
          {credentialFailure(ping.data?.error) && (
            <p>
              The stored password no longer matches the server.
              {container &&
                " That is what happens when a container is started over a data volume that was set up under an older password: the container states the new one, the data keeps the old one."}{" "}
              Edit the connection with the right password
              {container ? ", or delete the server and its data under Maintenance." : "."}
            </p>
          )}
        </Notice>
      )}

      {admin && conn.driver !== "sqlite" && (
        <Panel plain>
          <PanelHeader title="Connection string" />
          <RowList>
            <ConnectionStringRow
              conn={conn}
              target="host"
              title={a?.exposure === "remote" ? "As saved" : "On this server"}
              preview={previewDsn(conn, conn.host)}
            />
            {a && a.exposure !== "remote" && <PublicStringRow conn={conn} access={a} />}
          </RowList>
        </Panel>
      )}

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
            {admin && a && a.exposure !== "remote" && (
              <AccessRow
                conn={conn}
                access={a}
                confirm={confirm}
                onChanged={() => {
                  access.refresh()
                  ping.refresh()
                }}
              />
            )}
            {admin && a && !a.detected && (
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
              </>
            )}
            {admin && (
              <Row
                title="Delete the database"
                subtitle={
                  container
                    ? `${dropExplanation(conn)} The container ${container} can go with it.`
                    : dropExplanation(conn)
                }
                trailing={
                  <Button
                    size="sm"
                    variant="destructive"
                    onClick={() => {
                      // A server that refuses the password cannot be asked to
                      // drop anything, so removing the container is the only
                      // delete that will work — it is offered ticked.
                      removeContainer.current = Boolean(container) && unreachable
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
                              it.
                              {!container &&
                                " If this database was started from this page, its container keeps running; remove that from Docker."}
                            </p>
                            {container && (
                              <label className="flex items-start gap-2.5 rounded-lg border border-hairline p-3">
                                <Checkbox
                                  className="mt-0.5"
                                  defaultChecked={removeContainer.current}
                                  onCheckedChange={(v) => (removeContainer.current = v === true)}
                                  aria-label={`Also remove the container ${container} and its data`}
                                />
                                <span className="text-body">
                                  <span className="block font-medium">
                                    Also remove the container {container} and its data
                                  </span>
                                  <span className="block text-hint text-muted-foreground">
                                    Stops and removes the container and the volume it wrote to.
                                    {unreachable &&
                                      " This connection cannot sign in, so it is the only delete that will work."}
                                  </span>
                                </span>
                              </label>
                            )}
                          </div>
                        ),
                        action: async (c) => {
                          const res = await del<{
                            connectionRemoved: boolean
                            detail?: string
                            warnings?: string[]
                          }>(`/databases/${conn.id}/database`, {
                            confirm: c,
                            body: {
                              removeContainer: Boolean(container) && removeContainer.current,
                            },
                          })
                          for (const w of res.warnings ?? [])
                            notify.warning("Kept", { description: w })
                          refreshConnections()
                          if (res.connectionRemoved) goto("/databases")
                        },
                      })
                    }}
                  >
                    <WarningFill className="size-3.5" />
                    Delete…
                  </Button>
                }
              />
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
          onOpenChange={(o) => {
            if (o) return
            setEditing(false)
            forgetMemoryState("databases.connect.")
          }}
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

/**
 * Does the ping's refusal name the password? Each engine says it its own way;
 * the page only needs to know whether "edit the password" is the fix.
 */
function credentialFailure(error?: string) {
  if (!error) return false
  return /password authentication failed|access denied for user|authentication failed|wrongpass|noauth|login failed for user|invalid username\/password/i.test(
    error,
  )
}

/**
 * The string as the engine would spell it, with the password hidden — built
 * from the facts the page already has, so drawing it reveals nothing and
 * audits nothing. The real one is fetched when it is copied or shown.
 */
function previewDsn(conn: DbConnection, host: string) {
  return buildDsn(conn.driver, {
    host,
    port: conn.port || DEFAULT_PORT[conn.driver],
    user: conn.user,
    password: "••••••",
    database: conn.database,
    option: "",
  }).replace(encodeURIComponent("••••••"), "••••••")
}

/** One connection string: masked until shown, copied whole. */
function ConnectionStringRow({
  conn,
  target,
  title,
  preview,
}: {
  conn: DbConnection
  target: "host" | "public"
  title: string
  preview: string
}) {
  const { copy, copied } = useCopy()
  const [shown, setShown] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const fetchUrl = async () => {
    const res = await get<{ url: string }>(`/databases/${conn.id}/url`, { target })
    return res.url
  }
  const reveal = async () => {
    if (shown) {
      setShown(null)
      return
    }
    setBusy(true)
    try {
      setShown(await fetchUrl())
    } catch (err) {
      notify.error("Could not read the connection string", err)
    } finally {
      setBusy(false)
    }
  }
  const copyIt = async () => {
    setBusy(true)
    try {
      await copy(shown ?? (await fetchUrl()), "Connection string copied")
    } catch (err) {
      notify.error("Could not read the connection string", err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Row
      title={title}
      subtitle={shown ?? preview}
      mono
      trailing={
        <>
          <IconAction
            label={shown ? "Hide the connection string" : "Show the connection string"}
            onClick={reveal}
            disabled={busy}
          >
            {shown ? <EyeOff /> : <Eye />}
          </IconAction>
          <Button size="sm" variant="outline" onClick={copyIt} disabled={busy}>
            {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
            Copy
          </Button>
        </>
      }
    />
  )
}

/**
 * The string for a laptop, or the sentence that says why there is not one.
 */
function PublicStringRow({ conn, access }: { conn: DbConnection; access: DbAccess }) {
  const address = access.publicAddresses[0]
  if (access.exposure === "public" && address) {
    return (
      <ConnectionStringRow
        conn={conn}
        target="public"
        title="From anywhere"
        preview={previewDsn(conn, address.includes(":") ? `[${address}]` : address)}
      />
    )
  }
  if (access.exposure === "public") {
    return (
      <Row
        title="From anywhere"
        subtitle={`This machine has no public address of its own — the provider maps one in front of it. Use that address with port ${access.port}.`}
      />
    )
  }
  if (access.exposure === "private") {
    return (
      <Row
        title="From anywhere"
        subtitle="Published on one address only. Whoever can reach that address can connect on the same port; the binding is changed on the Docker page."
      />
    )
  }
  return (
    <Row
      title="From anywhere"
      subtitle={
        access.managed
          ? "Not reachable from outside this server. Open it up under Maintenance to connect from your own machine or share it."
          : "Not reachable from outside this server."
      }
    />
  )
}

/**
 * The switch between "this server only" and "every interface", as a row that
 * says what the binding is now and what changing it does.
 */
function AccessRow({
  conn,
  access,
  confirm,
  onChanged,
}: {
  conn: DbConnection
  access: DbAccess
  confirm: ReturnType<typeof useConfirm>["confirm"]
  onChanged: () => void
}) {
  const fw = access.firewall
  const port = access.port ?? 0
  const change = async (exposure: "local" | "public") => {
    const res = await put<DbAccessChange>(`/databases/${conn.id}/access`, { exposure })
    if (res.firewallError)
      notify.warning("The firewall was not changed", { description: res.firewallError })
    onChanged()
  }

  if (access.composeProject) {
    return (
      <Row
        title="Open to the internet"
        subtitle={`${access.container} belongs to the compose project ${access.composeProject}. Change its port binding in the compose file and redeploy the stack.`}
      />
    )
  }
  if (!access.managed) {
    return (
      <Row
        title="Open to the internet"
        subtitle={
          access.exposure === "private"
            ? "Published on one address. Change the binding on the Docker page."
            : "Nothing on this server publishes the port for this connection, so there is no binding to change here."
        }
      />
    )
  }
  if (access.exposure === "public") {
    const firewall = !fw.backend
      ? " There is no firewall on this host, so the binding alone decides."
      : !fw.active
        ? ` The firewall (${fw.backend}) is off.`
        : fw.open
          ? " The firewall lets it through."
          : ` The firewall (${fw.backend}) is not letting it through; open port ${port} there.`
    return (
      <Row
        title="Open to the internet"
        subtitle={`Port ${port} answers on every interface.${firewall} Anyone with the password can connect.`}
        trailing={
          <Button
            size="sm"
            variant="outline"
            onClick={() =>
              confirm({
                title: "Close to the internet",
                confirmLabel: "Close",
                description: (
                  <div className="space-y-2">
                    <p>
                      Recreates <b>{access.container}</b> with port {port} published to this server
                      only, and removes the firewall rule this dashboard added for it.
                    </p>
                    <p>
                      Anything connecting from outside stops working. The engine restarts, which is
                      a few seconds of refused connections; the data is kept.
                    </p>
                  </div>
                ),
                action: () => change("local"),
              })
            }
          >
            Close
          </Button>
        }
      />
    )
  }
  const firewallStep = fw.active
    ? fw.editable
      ? ` and a firewall rule allowing port ${port} is added`
      : ` — the firewall (${fw.backend}) cannot be edited from here, so open port ${port} on it yourself`
    : ""
  return (
    <Row
      title="Open to the internet"
      subtitle={`Port ${port} is published to this server only. Opening it lets your own machine, or anyone you give the string to, connect.`}
      trailing={
        <Button
          size="sm"
          variant="outline"
          onClick={() =>
            confirm({
              title: "Open to the internet",
              confirmLabel: "Open up",
              description: (
                <div className="space-y-2">
                  <p>
                    Recreates <b>{access.container}</b> with port {port} published on every
                    interface{firewallStep}. The engine restarts, which is a few seconds of refused
                    connections; the data volume is kept.
                  </p>
                  <p>
                    Anyone who has the connection string can then reach this database from anywhere.
                    The generated password is long, but treat the string as a secret and close this
                    again when it is no longer needed.
                  </p>
                </div>
              ),
              action: () => change("public"),
            })
          }
        >
          Open up…
        </Button>
      }
    />
  )
}
