"use client"

import { useMemo } from "react"
import { get } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"
import { schemaModel } from "@/components/database/query/completion"
import type {
  CatalogHead,
  ColumnInfo,
  HistoryEntry,
  Outline,
  SavedQuery,
} from "@/components/database/query/types"

/**
 * The reads the editor's rail and its completion are made of. None polls:
 * the history is read again when a statement has run, the saved queries when
 * one was written, the schema when the reader asks.
 *
 * Each answer is read as its own shape whatever came back. A server without
 * a route answers the path with an empty list, and that is "nothing here",
 * not a page that cannot be drawn.
 */

const listOf = <T>(answer: unknown): T[] => (Array.isArray(answer) ? (answer as T[]) : [])

/** How many statements of history the rail keeps: the most the server hands over. */
export const HISTORY_ROWS = 200

export function useSavedQueries(id: number) {
  return usePoll(
    async (signal) => listOf<SavedQuery>(await get(`/databases/${id}/queries`, undefined, signal)),
    0,
    [id],
  )
}

export function useHistory(id: number) {
  return usePoll(
    async (signal) =>
      listOf<HistoryEntry>(await get(`/databases/${id}/history`, { limit: HISTORY_ROWS }, signal)),
    0,
    [id],
  )
}

/** Which schemas the connection has, and which one unqualified names resolve to. */
export function useCatalogHead(id: number) {
  return usePoll(
    async (signal) => {
      const answer = await get<Partial<CatalogHead>>(`/databases/${id}/catalog`, undefined, signal)
      return {
        schema: answer.schema ?? "",
        defaultSchema: answer.defaultSchema ?? "",
        schemas: Array.isArray(answer.schemas) ? answer.schemas : [],
      } satisfies CatalogHead
    },
    0,
    [id],
  )
}

/** Past this many schemas, only the connection's own and the one being looked at are read. */
export const EVERY_SCHEMA_UNTIL = 12

async function readOutline(id: number, schema: string, signal: AbortSignal): Promise<Outline> {
  const answer = await get<Partial<Outline>>(
    `/databases/${id}/outline`,
    { schema: schema || undefined },
    signal,
  )
  return {
    schema: answer.schema ?? "",
    tables: answer.tables ?? {},
    entries: Array.isArray(answer.entries) ? answer.entries : [],
    truncated: answer.truncated ?? false,
    total: answer.total ?? 0,
    limit: answer.limit ?? 0,
  }
}

/**
 * Every table and view with its columns — what the completion offers and the
 * schema tree lists.
 *
 * A connection with a handful of schemas is read whole, so a name in another
 * schema completes as readily as one in its own. A server with many — where
 * a "schema" is every database on it — is read one at a time: the
 * connection's own, and the one the reader has picked in the tree.
 */
export function useSchema(id: number, picked: string) {
  const head = useCatalogHead(id)
  const settled = head.data !== undefined || head.error !== undefined
  const own = head.data?.defaultSchema ?? ""
  const named = (head.data?.schemas ?? []).filter((schema) => !schema.system).length
  const whole = named <= EVERY_SCHEMA_UNTIL
  const scope = whole ? "" : own
  const outline = usePoll((signal) => readOutline(id, scope, signal), 0, [id, scope], {
    enabled: settled,
  })
  const aside = !whole && picked !== "" && picked !== own
  const other = usePoll((signal) => readOutline(id, picked, signal), 0, [id, picked], {
    enabled: settled && aside,
  })
  const model = useMemo(() => {
    const base = schemaModel(outline.data, head.data)
    if (!aside || !other.data) return base
    const more = schemaModel(other.data, head.data)
    const known = new Set(base.tables.map((table) => `${table.schema}\u0000${table.name}`))
    return {
      ...base,
      tables: [
        ...base.tables,
        ...more.tables.filter((table) => !known.has(`${table.schema}\u0000${table.name}`)),
      ],
    }
  }, [outline.data, other.data, head.data, aside])
  const refreshHead = head.refresh
  const refreshOutline = outline.refresh
  const refreshOther = other.refresh
  return {
    model,
    head: head.data,
    /** The list was cut at the server's limit: more tables exist than are listed. */
    truncated: Boolean(outline.data?.truncated || (aside && other.data?.truncated)),
    loading: !settled || outline.loading || (aside && other.loading),
    error: outline.error ?? (aside ? other.error : undefined) ?? head.error,
    refresh: useMemo(
      () => () => {
        refreshHead()
        refreshOutline()
        refreshOther()
      },
      [refreshHead, refreshOutline, refreshOther],
    ),
  }
}

/** One table's columns with their types, read when its row in the tree is opened. */
export function readColumns(id: number, schema: string, table: string, signal?: AbortSignal) {
  return get<ColumnInfo[]>(
    `/databases/${id}/columns`,
    { schema: schema || undefined, table },
    signal,
  ).then((answer) => listOf<ColumnInfo>(answer))
}
