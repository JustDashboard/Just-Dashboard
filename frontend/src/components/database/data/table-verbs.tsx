"use client"

import { useCallback } from "react"
import { Copy, Download, Layout, Trash, Backspace } from "@/components/icons"
import { api } from "@/lib/api"
import { bytes } from "@/lib/format"
import { copyText } from "@/lib/clipboard"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { FormFact, Statement } from "@/components/form"
import type { Verb } from "@/components/verbs"
import { EngineMark } from "@/components/database/kit"
import { useDatabase } from "@/components/database/shell/database-context"
import { EXPORT_FORMATS } from "@/components/database/data/export"
import { ROW_OBJECT_KINDS, type RowObjectKind } from "@/components/database/data/kinds"
import type { DbCatalogObject, DbDdlResult } from "@/components/database/data/types"
import type { ExportRequest } from "@/components/database/data/use-export"
import { grouped } from "@/components/database/data/view"

/**
 * What a row of the rail can be asked to do, as data for its one menu: read
 * its structure, copy its name, save it as a file, empty it, drop it.
 *
 * The two that destroy are drawn only for a role that may destroy and never
 * on a protected connection, and each is confirmed against the object named
 * with the engine's mark — showing the statement the server says it will run,
 * read from the same route with `?preview=1`. That read is made once, when the
 * dialog opens: a preview of a drop spends the same budget as a drop.
 */
export function useTableVerbs({
  onExport,
  onChanged,
}: {
  onExport: (request: ExportRequest) => void
  /** A table was emptied or dropped: the catalogue and the open table are stale. */
  onChanged: (object: DbCatalogObject, change: "truncate" | "drop") => void
}) {
  const { id, engine, readOnly, goto } = useDatabase()
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()

  const destroy = useCallback(
    async (object: DbCatalogObject, kind: RowObjectKind, change: "truncate" | "drop") => {
      const name = object.schema ? `${object.schema}.${object.name}` : object.name
      const isView = kind !== "table"
      const request =
        change === "truncate"
          ? {
              path: "/ddl/truncate",
              method: "POST",
              body: { schema: object.schema, table: object.name },
            }
          : isView
            ? {
                path: "/ddl/view",
                method: "DELETE",
                body: {
                  schema: object.schema,
                  name: object.name,
                  ...(kind === "materialized view" ? { materialized: true } : {}),
                },
              }
            : {
                path: "/ddl/table",
                method: "DELETE",
                body: { schema: object.schema, table: object.name },
              }
      let planned: DbDdlResult
      try {
        planned = await api<DbDdlResult>(`/databases/${id}${request.path}`, {
          method: request.method,
          body: request.body,
          query: { preview: 1 },
        })
      } catch (err) {
        notify.error(`Could not prepare to ${change} ${name}`, err)
        return
      }
      const word = ROW_OBJECT_KINDS[kind].label.toLowerCase()
      const title = change === "truncate" ? `Empty ${word}` : `Drop ${word}`
      confirm({
        title,
        confirmLabel: title,
        subject: {
          mark: <EngineMark engine={engine} size="sm" />,
          name: <span className="font-mono">{name}</span>,
          facts: (
            <>
              {object.estimatedRows !== undefined && object.estimatedRows >= 0 && (
                <FormFact label="Rows">about {grouped(object.estimatedRows)}</FormFact>
              )}
              {object.size !== undefined && <FormFact label="Size">{bytes(object.size)}</FormFact>}
            </>
          ),
        },
        description: (
          <>
            <p>
              {change === "truncate"
                ? `Every row of this ${word} is removed. The ${word} itself, its columns and its indexes stay.`
                : `The ${word} is removed with everything in it. Nothing here can bring it back.`}
            </p>
            <Statement sql={planned.statement} placeholder="" />
          </>
        ),
        action: async () => {
          await api<DbDdlResult>(`/databases/${id}${request.path}`, {
            method: request.method,
            body: request.body,
          })
          onChanged(object, change)
        },
      })
    },
    [id, engine, confirm, onChanged],
  )

  const verbsFor = useCallback(
    (object: DbCatalogObject, kind: RowObjectKind): Verb[] => {
      const operations = engine.capabilities.ddlOperations
      const mayDestroy = can("destructive") && !readOnly
      const verbs: Verb[] = [
        {
          key: "structure",
          label: "Structure",
          icon: Layout,
          run: () => goto("data", { schema: object.schema, table: object.name, view: "structure" }),
        },
        {
          key: "copy",
          label: "Copy name",
          icon: Copy,
          run: () => void copyText(object.name, "Name copied"),
        },
      ]
      for (const format of engine.capabilities.exportFormats) {
        verbs.push({
          key: `export-${format}`,
          label: EXPORT_FORMATS[format].label,
          icon: Download,
          group: "Export as",
          run: () => onExport({ schema: object.schema, table: object.name, format }),
        })
      }
      if (mayDestroy && kind === "table" && operations.includes("truncate")) {
        verbs.push({
          key: "truncate",
          label: "Empty…",
          icon: Backspace,
          danger: true,
          group: "Destroy",
          run: () => void destroy(object, kind, "truncate"),
        })
      }
      const droppable =
        kind === "table"
          ? operations.includes("dropTable")
          : kind === "view"
            ? operations.includes("views")
            : kind === "materialized view" && operations.includes("materializedViews")
      if (mayDestroy && droppable) {
        verbs.push({
          key: "drop",
          label: "Drop…",
          icon: Trash,
          danger: true,
          group: "Destroy",
          run: () => void destroy(object, kind, "drop"),
        })
      }
      return verbs
    },
    [engine, can, readOnly, goto, onExport, destroy],
  )

  return { verbsFor, dialog }
}
