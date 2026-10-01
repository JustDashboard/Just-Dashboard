"use client"

import { createContext, useCallback, useContext, useEffect, useMemo } from "react"
import { useRouter } from "next/navigation"
import { get } from "@/lib/api"
import { useViewState } from "@/lib/view-state"
import type { DbConnection, DbDriverInfo } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { engineFor as registryEngineFor, type Engine } from "@/components/database/engine"
import { KNOWN_DATABASES_KEY, type KnownDatabase } from "@/components/database/shell/nav-groups"
import { newDatabaseHref, type AddMode } from "@/components/database/shell/routes"

/**
 * What every page of the Databases section shares: the saved connections and
 * the driver catalogue.
 *
 * Both are read here once, by the section's layout, and every page below
 * reads them from this context rather than fetching its own. The provider
 * always renders its children: a list that has not landed, or a poll that
 * failed after one did, is a fact the pages are told (`loaded`, `error`) and
 * never a reason to take the page they are looking at away from them.
 */
export type DatabasesContextValue = {
  /** Every saved connection, in name order. Empty until the first read lands. */
  connections: DbConnection[]
  /** Whether the list has been read at least once. */
  loaded: boolean
  /** Why the latest read of the list failed. What it held before is still in `connections`. */
  error: Error | undefined
  /** The driver catalogue, read once. Undefined until it lands, and if it never does. */
  drivers: DbDriverInfo[] | undefined
  /** Whether the catalogue has answered or failed — nothing more is coming either way. */
  driversSettled: boolean
  /** Read the list again now: after one was added, renamed or forgotten. */
  refresh: () => void
  /** Whether this role manages connections (`system.admin`). Affordance only; the server decides. */
  admin: boolean
  /** The engine behind any connection, with what the catalogue says about it. */
  engineFor: (conn: Pick<DbConnection, "driver" | "flavor" | "capabilities">) => Engine
  /** The address of Add a database. */
  newHref: (options?: { mode?: AddMode; key?: string }) => string
  /** Go there. */
  openNew: (options?: { mode?: AddMode; key?: string }) => void
}

const DatabasesContext = createContext<DatabasesContextValue | null>(null)

export function DatabasesProvider({ children }: { children: React.ReactNode }) {
  const { can } = useAuth()
  const router = useRouter()
  const list = usePoll((signal) => get<DbConnection[]>("/databases/", undefined, signal), 60_000)
  const catalogue = usePoll(
    (signal) => get<DbDriverInfo[]>("/databases/drivers", undefined, signal),
    0,
  )

  // The rail draws a database's panel from the address before this list has
  // been read (a pasted link, a reload). What it needs for that — the name
  // and the engine — is remembered from the last list, so the panel that
  // arrives is the one already drawn rather than a different shape of list.
  const [known, setKnown] = useViewState<Record<string, KnownDatabase>>(KNOWN_DATABASES_KEY, {})
  useEffect(() => {
    if (!list.data) return
    const next = Object.fromEntries(
      list.data.map((conn) => [
        String(conn.id),
        { name: conn.name, driver: conn.driver, ...(conn.flavor ? { flavor: conn.flavor } : {}) },
      ]),
    )
    if (JSON.stringify(next) !== JSON.stringify(known)) setKnown(next)
  }, [list.data, known, setKnown])

  const drivers = catalogue.data
  const admin = can("system.admin")
  const refresh = list.refresh
  const engineFor = useCallback<DatabasesContextValue["engineFor"]>(
    (conn) => registryEngineFor(conn, drivers),
    [drivers],
  )
  const openNew = useCallback<DatabasesContextValue["openNew"]>(
    (options) => router.push(newDatabaseHref(options)),
    [router],
  )

  const value = useMemo<DatabasesContextValue>(
    () => ({
      connections: list.data ?? [],
      loaded: list.data !== undefined,
      error: list.error,
      drivers,
      driversSettled: !catalogue.loading,
      refresh,
      admin,
      engineFor,
      newHref: newDatabaseHref,
      openNew,
    }),
    [list.data, list.error, drivers, catalogue.loading, refresh, admin, engineFor, openNew],
  )

  return <DatabasesContext.Provider value={value}>{children}</DatabasesContext.Provider>
}

export function useDatabases() {
  const value = useContext(DatabasesContext)
  if (!value) throw new Error("useDatabases must be used inside the databases layout")
  return value
}
