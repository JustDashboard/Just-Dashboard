"use client"

import { createContext, useContext } from "react"
import type { DbConnection, DbDriverInfo } from "@/lib/types"

export type DbSelection = { schema: string; table?: string }

/**
 * The one connection and one table the whole database section is looking at.
 *
 * `connections` and `drivers` are section-wide and cheap, so the layout polls
 * them once and every page reads them here. The active connection and the
 * chosen table live in the URL (`?conn=&schema=&table=`) so the browser back
 * button, a bookmarked link and a jump from the diagram all mean the same
 * thing — `goto`/`hrefFor` are the only writers, and every page reads the URL.
 */
export type DatabaseContextValue = {
  connections: DbConnection[]
  drivers: DbDriverInfo[]
  conn: DbConnection | null
  info: DbDriverInfo | undefined
  selection: DbSelection
  refreshConnections: () => void
  /** Push a route inside the section, carrying conn and (unless overridden) the selection. */
  goto: (pathname: string, params?: SectionParams) => void
  /** Change the chosen table without a new history entry — refining the current view. */
  select: (params: SectionParams) => void
  /** The same as `goto` builds, as a string — for a `<Link>`. */
  hrefFor: (pathname: string, params?: SectionParams) => string
}

export type SectionParams = {
  conn?: number
  schema?: string
  /** `null` clears the table param. */
  table?: string | null
  /** A statement for the Query tab to open with — from the diagram's "query this table". */
  sql?: string
}

const DatabaseContext = createContext<DatabaseContextValue | null>(null)

export const DatabaseProvider = DatabaseContext.Provider

export function useDatabase() {
  const value = useContext(DatabaseContext)
  if (!value) throw new Error("useDatabase must be used inside the databases layout")
  return value
}
