"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { forgetMemoryState, useMemoryState, useSessionState } from "@/lib/view-state"
import { usePathname, useRouter, useSearchParams } from "next/navigation"
import { Database, Plus } from "@/components/icons"
import { notify } from "@/lib/toast"
import { get, post } from "@/lib/api"
import { plural, relativeTime } from "@/lib/format"
import type { DbConnection, DbCredentialServer, DbDriverInfo, DbSyncResult } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { Page, PageContext } from "@/components/page"
import { EmptyState, ErrorState, LoadingPanel, Spinner } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { NewDatabaseDialog } from "@/components/database/new-database-dialog"
import { ConnectionDialog } from "@/components/database/connection-dialog"
import { HostConnectDialog } from "@/components/database/host-connect-dialog"
import { ConnectionSwitcher } from "@/components/database/connection-switcher"
import { DatabaseProvider, type SectionParams } from "@/components/database/db-context"
import { useNavScope } from "@/components/nav-scope"
import { NAV } from "@/components/nav"
import { ProductGlyph } from "@/components/product-logo"

/**
 * The section's pages, and which of them a non-SQL engine (Redis, Mongo) still
 * has. The rail draws this list rather than a strip across the page, so the
 * order and the icons come from the one nav registry and only the SQL rule
 * lives here — it is the one thing about these pages the route cannot say.
 */
const SQL_ONLY = new Set([
  "/databases/structure",
  "/databases/diagram",
  "/databases/query",
  "/databases/find",
  "/databases/monitor",
  "/databases/advisor",
  "/databases/generate",
])

/**
 * The pages that are about every database at once rather than one of them:
 * the control center the section opens on, and the map of what feeds what.
 * They draw no connection strip and register no connection panel — the rail
 * shows the section's own pages, and the connection is chosen on the page.
 */
const SECTION_WIDE = new Set(["/databases", "/databases/topology"])

const PAGES =
  NAV.flatMap((group) => group.items).find((item) => item.href === "/databases")?.children ?? []

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

  const [addOpen, setAddOpen] = useMemoryState("databases.connect.open", false)
  const [newOpen, setNewOpen] = useState(false)
  const [credentialsFor, setCredentialsFor] = useState<DbCredentialServer | null>(null)

  const list = connections.data
  const sectionWide = SECTION_WIDE.has(pathname)
  const connId = Number(params.get("conn")) || null
  // Where the reader was in this section, kept for the tab. The rail's Browse
  // link is a bare `/databases/browse`, and without this every visit opened on
  // the first connection's first page rather than the table being worked on.
  const [place, setPlace] = useSessionState<{ conn: number; schema: string; table: string } | null>(
    "databases.place",
    null,
  )
  const conn = useMemo(
    () => list?.find((c) => c.id === (connId ?? place?.conn)) ?? list?.[0] ?? null,
    [list, connId, place?.conn],
  )
  useEffect(() => {
    if (!connId) return
    const next = {
      conn: connId,
      schema: params.get("schema") ?? "",
      table: params.get("table") ?? "",
    }
    setPlace((current) =>
      current &&
      current.conn === next.conn &&
      current.schema === next.schema &&
      current.table === next.table
        ? current
        : next,
    )
  }, [connId, params, setPlace])
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
      // A statement handed to the Query tab travels once and is not carried
      // on to the next page: it is the question being asked, not the place.
      if (next?.sql) q.set("sql", next.sql)
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
        if (created) goto("/databases/browse", { conn: created.id })
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
    if (sectionWide || !list || list.length === 0 || !conn) return
    if (connId === conn.id) return
    const q = new URLSearchParams(Array.from(params.entries()))
    q.set("conn", String(conn.id))
    q.delete("schema")
    q.delete("table")
    // Arriving bare: the table this tab was on comes back with the connection.
    if (!connId && place?.conn === conn.id) {
      if (place.schema) q.set("schema", place.schema)
      if (place.table) q.set("table", place.table)
    }
    router.replace(`${pathname}?${q.toString()}`)
  }, [sectionWide, list, conn, connId, params, pathname, router, place])

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
        // On the control center the servers found here are cards with their
        // own Connect; a toast saying the same thing over them is noise.
        if (SECTION_WIDE.has(window.location.pathname)) return
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

  const pages = PAGES.filter((page) => !SQL_ONLY.has(page.href) || (info?.sql ?? true))
  const currentPage = PAGES.find((page) =>
    page.href === "/databases" ? pathname === "/databases" : pathname.startsWith(page.href),
  )
  // A SQL-only page is held until the driver catalogue is in — mounting it for
  // a Redis connection before `info` resolves fires a SQL query against a
  // key-value store and flashes an error where an explanation belongs.
  const sqlOnlyRoute = Boolean(currentPage && SQL_ONLY.has(currentPage.href))
  const awaitingDrivers = sqlOnlyRoute && !drivers.data
  const blocked = sqlOnlyRoute && Boolean(drivers.data) && info != null && !info.sql
  const admin = can("system.admin")

  // The rail is the section's navigation now, so it has to know two things
  // only this layout holds: which pages this engine actually has, and that
  // every one of them carries the chosen connection in its query string.
  useNavScope(
    conn && !sectionWide
      ? {
          path: "/databases",
          replaces: true,
          title: conn.name,
          mark: <ProductGlyph id={conn.driver} className="size-4" />,
          groups: [
            {
              items: pages.map((page) => ({
                title: page.title,
                href: hrefFor(page.href),
                icon: page.icon,
              })),
            },
          ],
        }
      : null,
  )

  const dialogs = admin && (
    <>
      <NewDatabaseDialog
        open={newOpen}
        onOpenChange={setNewOpen}
        onCreated={(name) => void landOn(name)}
        onConnectManually={() => setAddOpen(true)}
      />
      {addOpen && (
        <ConnectionDialog
          open
          onOpenChange={(o) => {
            if (o) return
            setAddOpen(false)
            forgetMemoryState("databases.connect.")
          }}
          onDone={connections.refresh}
        />
      )}
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
        <PageContext eyebrow="Apps" title="Databases" />
        <LoadingPanel />
      </Page>
    )
  }
  if (connections.error) {
    return (
      <Page>
        <PageContext eyebrow="Apps" title="Databases" />
        <ErrorState error={connections.error} />
      </Page>
    )
  }
  if (list && list.length === 0 && !sectionWide) {
    return (
      <Page>
        <PageContext eyebrow="Apps" title="Databases" />
        <EmptyState
          icon={Database}
          title="No databases yet"
          description="Anything running on this server is connected automatically. Use New database to start one — it takes a free port, generates its own password and connects itself — or, from the same dialog, point the dashboard at a database somewhere else."
          action={
            admin && (
              <Button size="sm" onClick={() => setNewOpen(true)}>
                <Plus className="size-4" />
                New database
              </Button>
            )
          }
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
        openNew: admin ? () => setNewOpen(true) : undefined,
        openConnect: admin ? () => setAddOpen(true) : undefined,
        connectHost: admin ? (server) => setCredentialsFor(server) : undefined,
      }}
    >
      <div className="flex h-full min-h-0 flex-col">
        <h1 className="sr-only">{conn && !sectionWide ? `Database ${conn.name}` : "Databases"}</h1>
        {/* The switcher and facts share one compact strip, the way the Files
            workbench puts the current folder beside its commands. The control
            center and the map are about every connection, so they draw none. */}
        {!sectionWide && (
          <div className="shrink-0 border-b border-hairline">
            <div className="mx-auto flex w-full max-w-[1440px] min-w-0 flex-wrap items-center gap-x-4 gap-y-2 px-5 py-3 md:px-8">
              {conn && (
                <ConnectionSwitcher
                  connections={list ?? []}
                  current={conn}
                  onSelect={(id) => goto(pathname, { conn: id })}
                  onNew={admin ? () => setNewOpen(true) : undefined}
                  onConnect={admin ? () => setAddOpen(true) : undefined}
                />
              )}
              {conn && (
                <div className="flex min-w-0 flex-1 flex-wrap items-center gap-x-2 gap-y-1 text-body text-muted-foreground max-sm:order-3 max-sm:basis-full">
                  <span>{info?.label ?? conn.driver}</span>
                  <Dot />
                  <span className="font-mono text-xs">
                    {conn.host || "this server"}
                    {conn.port ? `:${conn.port}` : ""}
                  </span>
                  {conn.database && (
                    <>
                      <Dot />
                      <span className="font-mono text-xs">{conn.database}</span>
                    </>
                  )}
                  {conn.user && (
                    <>
                      <Dot />
                      <span>as {conn.user}</span>
                    </>
                  )}
                  <Dot />
                  <span>added {relativeTime(conn.createdAt)}</span>
                </div>
              )}
              <div className="ml-auto flex shrink-0 flex-wrap items-center gap-3 max-sm:order-2">
                {conn && <ConnectionStatus id={conn.id} />}
                {admin && (
                  <Button size="sm" variant="outline" onClick={() => setNewOpen(true)}>
                    <Plus className="size-4" />
                    New database
                  </Button>
                )}
              </div>
            </div>
          </div>
        )}

        {/* Keyed on the connection so a switch remounts the page — the old
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
                title={`${currentPage?.title} is for SQL databases`}
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

function Dot() {
  return <span className="text-muted-foreground/40">·</span>
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
