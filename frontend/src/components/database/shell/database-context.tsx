"use client"

import { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react"
import Link from "next/link"
import { usePathname, useRouter, useSearchParams } from "next/navigation"
import { Database } from "@/components/icons"
import { get } from "@/lib/api"
import { useSessionState } from "@/lib/view-state"
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
  type SelectionKey,
} from "@/components/database/engine"
import { useDatabases } from "@/components/database/shell/databases-context"
import {
  KNOWN_DATABASES_KEY,
  learnedDatabase,
  type KnownDatabase,
} from "@/components/database/shell/nav-groups"
import {
  namedPlace,
  rememberedPlace,
  restoredQuery,
  saidPlace,
  writtenQuery,
  type Place,
} from "@/components/database/shell/place"
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
 * The provider renders its pages only once the connection is in hand and the
 * server has said what it is, so `conn` is never null, `engine` is the one
 * the page will keep, and no page below needs a branch for "not yet".
 */
export type DatabaseContextValue = {
  id: number
  /** The saved row, with what the server has since said it is (flavour, version, capabilities). */
  conn: DbConnection
  /** `GET /databases/{id}` in full; `undefined` only while a read of it has failed. */
  summary: DbConnectionSummary | undefined
  /** The registry's entry for this connection: its words, its pages, what it can do. */
  engine: Engine
  status: DatabaseStatus
  selection: DatabaseSelection
  /** Protected: the dashboard refuses every change to its data or schema. */
  readOnly: boolean
  /**
   * The address of one of this database's pages. It carries the part of the
   * reader's place the target page can use (the registry's `carries`) — from
   * this page's address, or as the last page that could hold it left it — and
   * `params` overrides it; `null` leaves a key out.
   */
  href: (section: SectionId, params?: SectionParams) => string
  /** Go there, as a new history entry. */
  goto: (section: SectionId, params?: SectionParams) => void
  /**
   * Change the address of the page being looked at, without a history entry:
   * the selection, or a key of the page's own. `null` clears a key and what
   * is not named stays. Calls made together are one change, and `selection`
   * and `param` answer with the new value at once.
   */
  select: (params: SectionParams) => void
  /** A key of the page's own in the address (`view`, `source`), `""` when absent. */
  param: (name: string) => string
}

const DatabaseContext = createContext<DatabaseContextValue | null>(null)

export function useDatabase() {
  const value = useContext(DatabaseContext)
  if (!value) throw new Error("useDatabase must be used inside a database's layout")
  return value
}

/** A write to the address that the router has not shown yet. */
type Pending = {
  /** The page it was made on, and the query string that page had. */
  pathname: string
  from: string
  /** The query string it leads to. */
  to: string
  /** The place as remembered once it has landed. */
  place: Place
}

const NO_PLACE: Place = {}
const NOTHING: readonly SelectionKey[] = []

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

  const summary = usePoll(
    (signal) => get<DbConnectionSummary>(`/databases/${id}`, undefined, signal),
    30_000,
    [id],
    { enabled: Boolean(found) },
  )
  // The saved row says how the server is dialled; only the summary says what
  // answered — MariaDB behind the `mysql` driver — and what that product can
  // do. A page mounted before it would be mounted on the driver's own
  // product: Backups drawn, its requests sent, and then taken away when the
  // server turned out to have no dumps. So the pages wait for the first
  // answer, whichever it is: the summary, or its failure.
  const summarySettled = Boolean(summary.data) || Boolean(summary.error)

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

  // What the server said it is goes into the rail's memory of this database,
  // so the panel drawn from the address on the next arrival is this engine's
  // and not its driver's: the rail draws a database early only once it knows
  // what registered for it. Held by content, as the connection is.
  const [, setKnown] = useSessionState<Record<string, KnownDatabase>>(KNOWN_DATABASES_KEY, {})
  const answered = about
    ? JSON.stringify({ flavor: about.flavor, capabilities: about.capabilities })
    : ""
  const learned = useMemo<Pick<KnownDatabase, "flavor" | "capabilities"> | undefined>(
    () => (answered ? JSON.parse(answered) : undefined),
    [answered],
  )
  useEffect(() => {
    if (learned) setKnown((held) => learnedDatabase(held, id, learned))
  }, [id, learned, setKnown])

  const check = summary.refresh
  const status = useMemo<DatabaseStatus>(() => {
    if (found?.broken) return { state: "broken", error: found.brokenReason, refresh: check }
    if (summary.data) {
      const { state, error: why, latencyMs } = summary.data
      return { state, error: why, latencyMs, refresh: check }
    }
    if (summary.error) return { state: "unknown", error: summary.error.message, refresh: check }
    return { state: "checking", refresh: check }
  }, [found, summary.data, summary.error, check])

  // The address, as the pages read it. Two things can put it ahead of what
  // the router shows. A write (`select`) is read back at once and reaches the
  // router after; it is one piece of state, so calls made together build on
  // each other instead of each starting from the address that was rendered —
  // the second used to undo the first. And a bare address is completed with
  // the place remembered for this database, so a link that could not carry
  // the table (the palette's, a bookmark of the page) opens on it all the
  // same, from the first paint.
  const query = params.toString()
  const section = databasePlace(pathname)?.section
  const carries = (section && engine?.section(section)?.carries) || NOTHING
  const [stored, setStored] = useSessionState<Place>(`databases.${id}.place`, NO_PLACE)
  const [pending, setPending] = useState<Pending | null>(null)
  // A write counts until the address moves: to where it was heading, or
  // anywhere else.
  const ahead = pending && pending.pathname === pathname && pending.from === query ? pending : null
  const held = ahead ? ahead.place : stored
  const address = useMemo(() => {
    const base = ahead ? ahead.to : query
    return restoredQuery(base, carries, held) ?? base
  }, [ahead, query, carries, held])
  const current = useMemo(() => new URLSearchParams(address), [address])
  const place = useMemo(
    () => rememberedPlace(held, saidPlace(current, carries) ?? NO_PLACE),
    [held, current, carries],
  )
  useEffect(() => {
    if (place !== stored) setStored(place)
  }, [place, stored, setStored])
  useEffect(() => {
    if (address !== query) {
      router.replace(address ? `${pathname}?${address}` : pathname, { scroll: false })
    }
  }, [router, pathname, address, query])

  const selection = useMemo<DatabaseSelection>(
    () => ({
      schema: current.get("schema") ?? "",
      table: current.get("table") ?? "",
      db: current.get("db") ?? "",
      collection: current.get("collection") ?? "",
      key: current.get("key") ?? "",
      sql: current.get("sql") ?? "",
    }),
    [current],
  )

  const href = useCallback<DatabaseContextValue["href"]>(
    (target, next) => {
      const carried = Object.fromEntries(
        (engine?.section(target)?.carries ?? NOTHING).map((key) => [key, place[key]]),
      )
      return sectionHref(id, target, { ...carried, ...next })
    },
    [id, engine, place],
  )
  const goto = useCallback<DatabaseContextValue["goto"]>(
    (target, next) => router.push(href(target, next)),
    [router, href],
  )
  const select = useCallback<DatabaseContextValue["select"]>(
    (next) =>
      setPending((before) => {
        const live = before && before.pathname === pathname && before.from === query ? before : null
        return {
          pathname,
          from: query,
          to: writtenQuery(live ? live.to : address, next),
          // Named here rather than read back from the address: clearing the
          // whole selection leaves a bare address, which says nothing.
          place: rememberedPlace(live ? live.place : place, namedPlace(next)),
        }
      }),
    [pathname, query, address, place],
  )
  const param = useCallback<DatabaseContextValue["param"]>(
    (name) => current.get(name) ?? "",
    [current],
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
            param,
          }
        : null,
    [id, conn, engine, summary.data, status, selection, href, goto, select, param],
  )

  if (value && driversSettled && summarySettled) {
    return <DatabaseContext.Provider value={value}>{children}</DatabaseContext.Provider>
  }
  // The pages wait for the catalogue and for what the server is: together
  // they say which pages this engine has, and a page mounted before them
  // would ask a key–value store for its tables.
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
