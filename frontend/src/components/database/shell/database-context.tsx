"use client"

import { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react"
import Link from "next/link"
import { usePathname, useRouter, useSearchParams } from "next/navigation"
import { Database } from "@/components/icons"
import { ApiError, get } from "@/lib/api"
import type { DbConnection } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Page, PageContext, PageState } from "@/components/page"
import { EmptyState } from "@/components/state"
import { Button } from "@/components/ui/button"
import {
  engineFor,
  sectionHref,
  type Engine,
  type SectionId,
  type SectionParams,
} from "@/components/database/engine"
import { useDatabases } from "@/components/database/shell/databases-context"
import { DATABASES_HREF, databasePlace } from "@/components/database/shell/routes"
import type { DbConnectionSummary, DbState } from "@/components/database/shell/types"

/**
 * Whether the server behind the connection answers, in one reading every
 * page shares: the strip, the home and the switcher say the same word because
 * they read it from here.
 *
 * `checking` is before the first answer; `unknown` is a check that itself
 * failed, which is not the same as a server that refused.
 */
export type DatabaseStatus = {
  state: DbState | "checking" | "unknown"
  /** The engine's or the driver's own words, when it did not answer. */
  error?: string
  /** How long the dial took, where the server reports it. */
  latencyMs?: number
  /** Ask again now. */
  refresh: () => void
}

/** The reader's place inside the database, as the address states it. Empty is "not said". */
export type DatabaseSelection = {
  schema: string
  table: string
  db: string
  collection: string
  key: string
  /** A statement handed to Query to open with. It travels once and is never carried on. */
  sql: string
}

/**
 * The one database every page under `/databases/<id>` is about.
 *
 * The id is in the path, so which database a page belongs to is the address
 * and not something a tab remembers; the rest of the reader's place — the
 * schema, the table, the key — is in the query string and read here once.
 * `href`, `goto` and `select` are the only writers of it.
 *
 * The provider renders its pages only once the connection is in hand, so
 * `conn` is never null and no page below needs a branch for "not yet".
 */
export type DatabaseContextValue = {
  id: number
  /** The saved row, with what the server has since said it is (flavour, version, labels). */
  conn: DbConnection
  /** `GET /databases/{id}` in full, where the backend has the route. */
  summary: DbConnectionSummary | undefined
  /** The registry's entry for this connection: its words, its pages, what it can do. */
  engine: Engine
  status: DatabaseStatus
  selection: DatabaseSelection
  /** Protected: the dashboard refuses every change to its data or schema. */
  readOnly: boolean
  /**
   * The address of one of this database's pages. It keeps the part of the
   * current selection the target page can use (the registry's `carries`), and
   * `params` overrides it — `null` clears a key.
   */
  href: (section: SectionId, params?: SectionParams) => string
  /** Go there, as a new history entry. */
  goto: (section: SectionId, params?: SectionParams) => void
  /** Change the selection on the page being looked at, without a history entry. */
  select: (params: SectionParams) => void
}

const DatabaseContext = createContext<DatabaseContextValue | null>(null)

export function useDatabase() {
  const value = useContext(DatabaseContext)
  if (!value) throw new Error("useDatabase must be used inside a database's layout")
  return value
}

/** A route older than the summary answers 404 or 405 for an id the list has. */
function predatesSummary(error: Error | undefined) {
  return error instanceof ApiError && (error.status === 404 || error.status === 405)
}

export function DatabaseProvider({ id, children }: { id: number; children: React.ReactNode }) {
  const router = useRouter()
  const pathname = usePathname()
  const params = useSearchParams()
  const { connections, loaded, error, drivers, driversSettled, refresh } = useDatabases()

  const listed = connections.find((c) => c.id === id)
  // An id the list does not hold is not yet a database that does not exist:
  // the list is a minute old at worst, and a connection made a second ago is
  // opened by exactly this address. So it is read once more before the page
  // says "not found" — and never resolved to some other database.
  const recheck = usePoll(
    (signal) => get<DbConnection[]>("/databases/", undefined, signal),
    0,
    [id],
    { enabled: (loaded || Boolean(error)) && !listed },
  )
  const found = listed ?? recheck.data?.find((c) => c.id === id)
  useEffect(() => {
    if (found && !listed) refresh()
  }, [found, listed, refresh])

  const [summaryMissing, setSummaryMissing] = useState(false)
  const summary = usePoll(
    (signal) => get<DbConnectionSummary>(`/databases/${id}`, undefined, signal),
    30_000,
    [id],
    { enabled: Boolean(found) && !summaryMissing },
  )
  // A backend from before the summary route has only the ping to say whether
  // the server answers, and is not asked for the summary again.
  const pingOnly = summaryMissing || predatesSummary(summary.error)
  if (pingOnly && !summaryMissing) setSummaryMissing(true)
  const ping = usePoll(
    (signal) => get<{ ok: boolean; error?: string }>(`/databases/${id}/ping`, undefined, signal),
    30_000,
    [id],
    { enabled: Boolean(found) && pingOnly && !found?.broken },
  )

  // The list's row is the newer of the two after an edit; what only the
  // summary knows is laid under it. Every poll hands back new objects that
  // say the same thing, so the connection is held by its content: a page
  // keyed on `conn` or `engine` is not re-run each minute for nothing.
  const about = summary.data
  const connKey = found
    ? JSON.stringify(
        about
          ? {
              flavor: about.flavor,
              version: about.version,
              capabilities: about.capabilities,
              ...found,
            }
          : found,
      )
    : ""
  const conn = useMemo<DbConnection | undefined>(
    () => (connKey ? JSON.parse(connKey) : undefined),
    [connKey],
  )

  const engine = useMemo(() => (conn ? engineFor(conn, drivers) : undefined), [conn, drivers])

  const refreshSummary = summary.refresh
  const refreshPing = ping.refresh
  const status = useMemo<DatabaseStatus>(() => {
    const check = () => (pingOnly ? refreshPing() : refreshSummary())
    if (found?.broken) return { state: "broken", error: found.brokenReason, refresh: check }
    if (summary.data) {
      const { state, error: why, latencyMs } = summary.data
      return { state, error: why, latencyMs, refresh: check }
    }
    if (pingOnly) {
      if (ping.data) {
        return {
          state: ping.data.ok ? "running" : "unreachable",
          error: ping.data.error,
          refresh: check,
        }
      }
      if (ping.error) return { state: "unknown", error: ping.error.message, refresh: check }
    } else if (summary.error) {
      return { state: "unknown", error: summary.error.message, refresh: check }
    }
    return { state: "checking", refresh: check }
  }, [
    found,
    summary.data,
    summary.error,
    pingOnly,
    ping.data,
    ping.error,
    refreshSummary,
    refreshPing,
  ])

  const selection = useMemo<DatabaseSelection>(
    () => ({
      schema: params.get("schema") ?? "",
      table: params.get("table") ?? "",
      db: params.get("db") ?? "",
      collection: params.get("collection") ?? "",
      key: params.get("key") ?? "",
      sql: params.get("sql") ?? "",
    }),
    [params],
  )

  const href = useCallback<DatabaseContextValue["href"]>(
    (section, next) => {
      const carried = Object.fromEntries(
        (engine?.section(section)?.carries ?? []).map((key) => [key, selection[key]]),
      )
      return sectionHref(id, section, { ...carried, ...next })
    },
    [id, engine, selection],
  )
  const goto = useCallback<DatabaseContextValue["goto"]>(
    (section, next) => router.push(href(section, next)),
    [router, href],
  )
  const select = useCallback<DatabaseContextValue["select"]>(
    (next) => {
      // Everything the address already says stays — a page's own keys too —
      // and only what is named changes.
      const query = new URLSearchParams(params.toString())
      for (const [key, value] of Object.entries(next)) {
        if (value) query.set(key, value)
        else query.delete(key)
      }
      const text = query.toString()
      router.replace(text ? `${pathname}?${text}` : pathname, { scroll: false })
    },
    [router, pathname, params],
  )

  const value = useMemo<DatabaseContextValue | null>(
    () =>
      conn && engine
        ? {
            id,
            conn,
            summary: summary.data,
            engine,
            status,
            selection,
            readOnly: Boolean(conn.readOnly),
            href,
            goto,
            select,
          }
        : null,
    [id, conn, engine, summary.data, status, selection, href, goto, select],
  )

  if (value && driversSettled) {
    return <DatabaseContext.Provider value={value}>{children}</DatabaseContext.Provider>
  }
  // The pages wait for the catalogue: it is what says which of them this
  // engine has, and a page mounted before it would ask a key–value store for
  // its tables.
  if (value || !(loaded || error) || recheck.loading) {
    return <PageState eyebrow="Databases" title={titleOf(pathname, found?.name)} />
  }
  // A list that failed is why the database cannot be shown only while
  // nothing has answered since; a second read that came back without it has.
  const failure = recheck.error ?? (recheck.data ? undefined : error)
  if (failure) {
    return (
      <PageState
        eyebrow="Databases"
        title={titleOf(pathname)}
        error={failure}
        onRetry={() => {
          refresh()
          recheck.refresh()
        }}
      />
    )
  }
  return <DatabaseNotFound />
}

/** The page's name while its database is still being read: what the address says. */
function titleOf(pathname: string, name?: string) {
  const section = databasePlace(pathname)?.section
  const page =
    !section || section === "home" ? "Database" : section[0].toUpperCase() + section.slice(1)
  return name ? `${name} · ${page}` : page
}

/**
 * An address that names no saved connection.
 *
 * It says so and offers the way back. What it never does is open another
 * database in its place: the section used to fall back to the first
 * connection in the list, which put the reader of a stale link on some other
 * database's pages — its delete button among them — without a word.
 */
export function DatabaseNotFound() {
  return (
    <Page>
      <PageContext eyebrow="Databases" title="Database not found" />
      <EmptyState
        icon={Database}
        title="Database not found"
        description="No saved connection has this address. It may have been forgotten since the link was made, or the link is from another server."
        action={
          <Button size="sm" variant="outline" asChild>
            <Link href={DATABASES_HREF}>All databases</Link>
          </Button>
        }
      />
    </Page>
  )
}
