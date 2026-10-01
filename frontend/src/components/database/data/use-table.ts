"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { get } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"
import { columnKind, type GridColumn } from "@/components/database/grid"
import type { BrowsePage, DbCatalog, DbTableDetail } from "@/components/database/data/types"
import { browseQuery, countKey, rowsQuery, type ViewState } from "@/components/database/data/view"

/**
 * The reads the table editor is made of. None of them polls: a table's rows
 * are refetched when the reader asks, changes the view or applies a set, and
 * never underneath a set they are staging.
 */

/**
 * What a schema holds, for the rail. `schema` empty asks for the connection's own.
 *
 * The answer is read as a catalogue whatever came back: a server without the
 * route (and the browser fixture every page of the section is walked with)
 * answers the path with an empty list, and that is a database with nothing
 * listed, not a page that cannot be drawn.
 */
export function useCatalog(id: number, schema: string) {
  return usePoll(
    async (signal) => {
      const answer = await get<Partial<DbCatalog>>(
        `/databases/${id}/catalog`,
        { schema: schema || undefined },
        signal,
      )
      return {
        schema: answer.schema ?? schema,
        defaultSchema: answer.defaultSchema ?? "",
        schemas: answer.schemas ?? [],
        objects: answer.objects ?? {},
        truncated: answer.truncated ?? [],
        limit: answer.limit ?? 0,
        errors: answer.errors,
      } satisfies DbCatalog
    },
    0,
    [id, schema],
  )
}

/**
 * One table's columns, keys and definition. Every list the page reads is a
 * list whatever the server sent: the additive ones are absent on a server
 * that predates them, and a table drawn without its incoming keys is still a
 * table.
 */
export function useTableDetail(id: number, schema: string, table: string, enabled = true) {
  return usePoll(
    async (signal) => {
      const answer = await get<Partial<DbTableDetail>>(
        `/databases/${id}/table`,
        { schema: schema || undefined, table },
        signal,
      )
      return {
        ...answer,
        schema: answer.schema ?? schema,
        name: answer.name ?? table,
        columns: answer.columns ?? [],
        primaryKey: answer.primaryKey ?? [],
        indexes: answer.indexes ?? [],
        foreignKeys: answer.foreignKeys ?? [],
        constraints: answer.constraints ?? [],
        referencedBy: answer.referencedBy ?? [],
        facts: answer.facts ?? [],
        estimatedRows: answer.estimatedRows ?? -1,
      } satisfies DbTableDetail
    },
    0,
    [id, schema, table],
    { enabled: enabled && table !== "" },
  )
}

type BrowseView = Pick<ViewState, "filters" | "match" | "sort" | "page">

/**
 * A page of the table.
 *
 * A change of page, sort or filter is a new resource to the poll, which
 * blanks while it loads; the page already on screen is kept under it, so the
 * grid sweeps over the old rows instead of collapsing to a skeleton and
 * losing the reader's place in it. What is kept is only ever this table's: the
 * component holding this hook is keyed by table.
 */
export function useBrowse(
  id: number,
  schema: string,
  table: string,
  view: BrowseView,
  limit: number,
) {
  const query = useMemo(() => browseQuery(schema, table, view, limit), [schema, table, view, limit])
  const identity = JSON.stringify(query)
  const poll = usePoll(
    async (signal) => {
      const answer = await get<Partial<BrowsePage>>(`/databases/${id}/browse`, query, signal)
      const rows = answer.rows ?? []
      return {
        ...answer,
        columns: answer.columns ?? [],
        types: answer.types ?? [],
        rows,
        rowCount: answer.rowCount ?? rows.length,
        rowsAffected: answer.rowsAffected ?? 0,
        duration: answer.duration ?? "",
        truncated: answer.truncated ?? false,
        statement: answer.statement ?? "",
        primaryKey: answer.primaryKey ?? [],
        estimatedRows: answer.estimatedRows ?? null,
        sort: answer.sort ?? [],
        limit: answer.limit ?? limit,
        offset: answer.offset ?? (view.page - 1) * limit,
      } satisfies BrowsePage
    },
    0,
    [id, identity],
  )
  const [kept, setKept] = useState<BrowsePage>()
  if (poll.data && poll.data !== kept) setKept(poll.data)
  return {
    /** The page to draw: the one asked for, or the last one while it loads. */
    page: poll.data ?? kept,
    /** The page drawn is the one asked for. */
    current: poll.data !== undefined,
    loading: poll.loading,
    error: poll.error,
    refresh: poll.refresh,
  }
}

type Counted = { key: string; count: number }

/**
 * The exact number of rows a view matches, counted when the reader asks.
 *
 * The answer is kept with what it is the answer to — the table and the
 * conditions — and shown only while that is still what is on screen. The old
 * count carried no table: one that came back after the reader had moved on
 * was drawn over the next table as "of 200,000,000", with a live Last button
 * leading to a page that did not exist.
 */
export function useExactCount(
  id: number,
  schema: string,
  table: string,
  view: Pick<ViewState, "filters" | "match">,
) {
  const key = countKey(schema, table, view)
  const [counted, setCounted] = useState<Counted>()
  const [pending, setPending] = useState<string>()
  const [error, setError] = useState<{ key: string; error: Error }>()
  const flight = useRef<AbortController | null>(null)

  useEffect(() => () => flight.current?.abort(), [])

  const count = useCallback(() => {
    flight.current?.abort()
    const controller = new AbortController()
    flight.current = controller
    setPending(key)
    setError(undefined)
    get<{ count: number }>(
      `/databases/${id}/count`,
      rowsQuery(schema, table, view),
      controller.signal,
    )
      .then((answer) => {
        if (controller.signal.aborted) return
        setCounted({ key, count: answer.count })
      })
      .catch((err: unknown) => {
        if (controller.signal.aborted) return
        setError({ key, error: err instanceof Error ? err : new Error(String(err)) })
      })
      .finally(() => {
        if (!controller.signal.aborted) setPending(undefined)
      })
  }, [id, schema, table, view, key])

  /** After rows were written the figure is no longer what the table holds. */
  const forget = useCallback(() => {
    flight.current?.abort()
    setCounted(undefined)
    setPending(undefined)
  }, [])

  return {
    exact: counted?.key === key ? counted.count : null,
    counting: pending === key,
    error: error?.key === key ? error.error : undefined,
    count,
    forget,
  }
}

/**
 * The grid's columns for a page: the page says which columns came back and
 * what each holds, the table's detail says what each *is* — its declared
 * type, whether it may be NULL, its default, its enum labels, what it points
 * at. Held by content, so a refetch that brings the same columns back does
 * not redraw every row.
 */
export function useGridColumns(
  page: BrowsePage | undefined,
  detail: DbTableDetail | undefined,
): GridColumn[] {
  const signature = JSON.stringify([page?.columns, page?.types, page?.kinds])
  return useMemo<GridColumn[]>(() => {
    if (!detail) return []
    const primary = new Set(detail.primaryKey)
    // Until the first page lands the header is drawn from the table's own
    // columns, so what loads under it is rows and not the whole grid.
    const names = page?.columns ?? detail.columns.map((column) => column.name)
    return names.map((name, index) => {
      const meta = detail.columns.find((column) => column.name === name)
      const foreign = detail.foreignKeys.find(
        (key) => key.columns.length === 1 && key.columns[0] === name,
      )
      const typeName = meta?.type ?? page?.types[index] ?? ""
      return {
        key: name,
        name,
        typeName,
        kind: columnKind({
          typeName,
          serverKind: page?.kinds?.[index],
          enumValues: meta?.enumValues,
        }),
        nullable: meta?.nullable,
        primaryKey: primary.has(name),
        foreignKey: foreign && {
          schema: foreign.refSchema,
          table: foreign.refTable,
          column: foreign.refColumns[0],
        },
        enumValues: meta?.enumValues,
        generated: Boolean(meta?.generated),
        defaultExpr: meta?.default ?? (meta?.identity ? meta.identity : undefined),
        comment: meta?.comment,
      }
    })
    // The page is read through its signature: a refetch hands back new arrays
    // that say the same thing.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [signature, detail])
}
