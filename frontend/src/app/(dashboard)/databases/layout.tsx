"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { usePathname, useRouter, useSearchParams } from "next/navigation"
import { Database, Plus } from "@/components/icons"
import { notify } from "@/lib/toast"
import { get, post } from "@/lib/api"
import { plural } from "@/lib/format"
import type { DbConnection, DbCredentialServer, DbDriverInfo, DbSyncResult } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { Page, PageHeader } from "@/components/page"
import { EmptyState, ErrorState, LoadingPanel, Spinner } from "@/components/state"
import { Status } from "@/components/status-dot"
import { TabLink } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { NewDatabaseDialog } from "@/components/database/new-database-dialog"
import { ConnectionDialog } from "@/components/database/connection-dialog"
import { HostConnectDialog } from "@/components/database/host-connect-dialog"
import { DatabaseProvider, type SectionParams } from "@/components/database/db-context"

/** Every tab, and which of them a non-SQL engine (Redis, Mongo) still has. */
const TABS = [
  { title: "Browse", href: "/databases", sqlOnly: false },
  { title: "Structure", href: "/databases/structure", sqlOnly: true },
  { title: "Diagram", href: "/databases/diagram", sqlOnly: true },
  { title: "Query", href: "/databases/query", sqlOnly: true },
  { title: "Find", href: "/databases/find", sqlOnly: true },
  { title: "Monitor", href: "/databases/monitor", sqlOnly: true },
  { title: "Generate", href: "/databases/generate", sqlOnly: true },
  { title: "Connection", href: "/databases/connection", sqlOnly: false },
]

export default function DatabasesLayout({ children }: { children: React.ReactNode }) {
  const { can } = useAuth()
  const router = useRouter()
  const pathname = usePathname()
  const params = useSearchParams()

  const connections = usePoll(
    (signal) => get<DbConnection[]>("/databases/", undefined, signal),
    60_000,
  )
  const drivers = usePoll(
    (signal) => get<DbDriverInfo[]>("/databases/drivers", undefined, signal),
    0,
  )

  const [addOpen, setAddOpen] = useState(false)
  const [newOpen, setNewOpen] = useState(false)
  const [credentialsFor, setCredentialsFor] = useState<DbCredentialServer | null>(null)

  const list = connections.data
  const connId = Number(params.get("conn")) || null
  const conn = useMemo(
    () => list?.find((c) => c.id === connId) ?? list?.[0] ?? null,
    [list, connId],
  )
  const info = drivers.data?.find((d) => d.id === conn?.driver)
  const selection = { schema: params.get("schema") ?? "", table: params.get("table") ?? undefined }

  /** Build a section URL from the current params, with the given overrides. */
  const hrefFor = useCallback(
    (target: string, next?: SectionParams) => {
      const q = new URLSearchParams()
      const id = next?.conn ?? conn?.id
      if (id) q.set("conn", String(id))
      // Switching connection drops the table selection — a table from another
      // engine is simply not this one's.
      const switching = next?.conn !== undefined && next.conn !== conn?.id
      const schema = next?.schema ?? (switching ? "" : (params.get("schema") ?? ""))
      if (schema) q.set("schema", schema)
      const table =
        next?.table === null ? "" : (next?.table ?? (switching ? "" : (params.get("table") ?? "")))
      if (table) q.set("table", table)
      const query = q.toString()
      return query ? `${target}?${query}` : target
    },
    [conn?.id, params],
  )
  const goto = useCallback(
    (target: string, next?: SectionParams) => router.push(hrefFor(target, next)),
    [router, hrefFor],
  )
  const select = useCallback(
    (next: SectionParams) => router.replace(hrefFor(pathname, next)),
    [router, hrefFor, pathname],
  )

  // A freshly created (or freshly credentialled) connection: fetch the list
  // now rather than waiting for the slow poll, then land on the new row.
  const landOn = useCallback(
    async (name: string) => {
      connections.refresh()
      try {
        const fresh = await get<DbConnection[]>("/databases/")
        const created = fresh.find((c) => c.name === name)
        if (created) goto("/databases", { conn: created.id })
      } catch {
        // The refresh above still brings it into the picker.
      }
    },
    [connections, goto],
  )

  // Normalise the URL: once the connection list is in, an address with no
  // ?conn= (or a stale one) is rewritten to name the active connection, so
  // every page below reads one source of truth.
  useEffect(() => {
    if (!list || list.length === 0 || !conn) return
    if (connId === conn.id) return
    const q = new URLSearchParams(Array.from(params.entries()))
    q.set("conn", String(conn.id))
    q.delete("schema")
    q.delete("table")
    router.replace(`${pathname}?${q.toString()}`)
  }, [list, conn, connId, params, pathname, router])

  // Databases running on this server connect themselves. Idempotent, skips by
  // address, silent when it adds nothing. Fires once per layout mount — the
  // layout persists across tab navigation, so this no longer re-runs on every
  // tab switch the way it did when it lived on the page.
  const synced = useRef(false)
  useEffect(() => {
    if (synced.current || !can("system.admin")) return
    synced.current = true
    post<DbSyncResult>("/databases/sync", {})
      .then((res) => {
        if (res.added.length > 0) {
          connections.refresh()
          notify.success(`Connected ${plural(res.added.length, "database")} on this server`, {
            description: res.added.join(", "),
          })
        }
        for (const server of res.unreachable ?? []) {
          notify.warning(`${server.container} is running but cannot be reached`, {
            description: server.reason,
            duration: Infinity,
          })
        }
        for (const server of res.needsCredentials ?? []) {
          notify.info(`${server.driver} is running on this server`, {
            description: `On port ${server.port}. It is not in a container, so its password is the one thing this dashboard cannot read for itself.`,
            duration: Infinity,
            action: { label: "Connect", onClick: () => setCredentialsFor(server) },
          })
        }
      })
      .catch(() => undefined)
  }, [can, connections])

  const tabs = TABS.filter((t) => !t.sqlOnly || (info?.sql ?? true))
  const currentTab = TABS.find((t) =>
    t.href === "/databases" ? pathname === "/databases" : pathname.startsWith(t.href),
  )
  // A SQL-only tab is held until the driver catalogue is in — mounting it for a
  // Redis connection before `info` resolves fires a SQL query against a
  // key-value store and flashes an error where an explanation belongs.
  const sqlOnlyRoute = Boolean(currentTab?.sqlOnly)
  const awaitingDrivers = sqlOnlyRoute && !drivers.data
  const blocked = sqlOnlyRoute && Boolean(drivers.data) && info != null && !info.sql

  const dialogs = can("system.admin") && (
    <>
      <NewDatabaseDialog
        open={newOpen}
        onOpenChange={setNewOpen}
        onCreated={(name) => void landOn(name)}
        onConnectManually={() => setAddOpen(true)}
      />
      <ConnectionDialog open={addOpen} onOpenChange={setAddOpen} onDone={connections.refresh} />
      {credentialsFor && (
        <HostConnectDialog
          key={`${credentialsFor.host}:${credentialsFor.port}`}
          server={credentialsFor}
          onOpenChange={(o) => !o && setCredentialsFor(null)}
          onConnected={(name) => void landOn(name)}
        />
      )}
    </>
  )

  if (connections.loading && !connections.data) {
    return (
      <Page>
        <PageHeader eyebrow="Access" title="Databases" />
        <LoadingPanel />
      </Page>
    )
  }
  if (connections.error) {
    return (
      <Page>
        <PageHeader eyebrow="Access" title="Databases" />
        <ErrorState error={connections.error} />
      </Page>
    )
  }
  if (list && list.length === 0) {
    return (
      <Page>
        <PageHeader
          eyebrow="Access"
          title="Databases"
          actions={
            can("system.admin") && (
              <Button size="sm" onClick={() => setNewOpen(true)}>
                <Plus className="size-4" />
                New database
              </Button>
            )
          }
        />
        <EmptyState
          icon={Database}
          title="No databases yet"
          description="Anything running on this server is connected automatically. Use New database to start one — it takes a free port, generates its own password and connects itself — or, from the same dialog, point the dashboard at a database somewhere else."
        />
        {dialogs}
      </Page>
    )
  }

  return (
    <DatabaseProvider
      value={{
        connections: list ?? [],
        drivers: drivers.data ?? [],
        conn,
        info,
        selection,
        refreshConnections: connections.refresh,
        goto,
        select,
        hrefFor,
      }}
    >
      <div className="flex h-full min-h-0 flex-col">
        <div className="shrink-0 border-b border-hairline bg-background/85 backdrop-blur-md">
          <div className="mx-auto w-full max-w-[1600px] px-4 md:px-6">
            <div className="flex flex-wrap items-center gap-2 py-2.5">
              <Select
                value={conn?.id.toString() ?? ""}
                onValueChange={(v) => goto(pathname, { conn: Number(v) })}
              >
                <SelectTrigger size="sm" className="w-[20rem] max-w-full">
                  <SelectValue placeholder="Select a connection" />
                </SelectTrigger>
                <SelectContent>
                  {(list ?? []).map((c) => (
                    <SelectItem key={c.id} value={c.id.toString()}>
                      {c.name} · {drivers.data?.find((d) => d.id === c.driver)?.label ?? c.driver}
                      {c.host ? ` · ${c.host}` : ""}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              {conn && <ConnectionStatus id={conn.id} />}
              <span className="flex-1" />
              {can("system.admin") && (
                <Button size="sm" variant="outline" onClick={() => setNewOpen(true)}>
                  <Plus className="size-4" />
                  New database
                </Button>
              )}
            </div>
            <nav className="-mb-px flex gap-1 overflow-x-auto">
              {tabs.map((tab) => {
                const active =
                  tab.href === "/databases"
                    ? pathname === "/databases"
                    : pathname.startsWith(tab.href)
                return (
                  <TabLink key={tab.href} href={hrefFor(tab.href)} selected={active}>
                    {tab.title}
                  </TabLink>
                )
              })}
            </nav>
          </div>
        </div>

        {/* Keyed on the connection so a switch remounts the tab — the old
            single-page design got this from `key` on <Tabs> plus Radix
            unmounting the inactive panels; a shared route keeps neither. */}
        <div key={conn?.id} className="min-h-0 flex-1 overflow-y-auto">
          {awaitingDrivers ? (
            <Page>
              <LoadingPanel />
            </Page>
          ) : blocked ? (
            <Page>
              <EmptyState
                icon={Database}
                title={`${currentTab?.title} is for SQL databases`}
                description={`${info?.label ?? conn?.driver} is a ${
                  info?.kind === "keyvalue" ? "key-value store" : "document database"
                }. Use Browse to work with its ${
                  info?.kind === "keyvalue" ? "keys" : "collections"
                }.`}
              />
            </Page>
          ) : (
            children
          )}
        </div>
      </div>
      {dialogs}
    </DatabaseProvider>
  )
}

function ConnectionStatus({ id }: { id: number }) {
  const { data } = usePoll(
    (signal) => get<{ ok: boolean; error?: string }>(`/databases/${id}/ping`, undefined, signal),
    30_000,
    [id],
  )
  if (!data) return <Spinner className="text-muted-foreground" />
  return (
    <span title={data.error}>
      <Status verdict={data.ok ? "ok" : "critical"} label={data.ok ? "connected" : "unreachable"} />
    </span>
  )
}
