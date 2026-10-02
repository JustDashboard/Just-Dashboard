"use client"

import { useCallback, useRef, useState } from "react"
import { Copy, Download, Layout, Trash, Backspace } from "@/components/icons"
import { api } from "@/lib/api"
import { bytes } from "@/lib/format"
import { copyText } from "@/lib/clipboard"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { ConfirmDialog, type ConfirmRequest } from "@/components/confirm-dialog"
import { FormFact, Statement } from "@/components/form"
import type { Verb } from "@/components/verbs"
import { EngineMark } from "@/components/database/kit"
import { useDatabase } from "@/components/database/shell/database-context"
import { EXPORT_FORMATS } from "@/components/database/data/export"
import { focusAfterDialog } from "@/components/database/data/focus"
import { ROW_OBJECT_KINDS, type RowObjectKind } from "@/components/database/data/kinds"
import type { DbCatalogObject, DbDdlResult } from "@/components/database/data/types"
import type { ExportRequest } from "@/components/database/data/use-export"
import { grouped } from "@/components/database/data/view"

/** The attribute a row's menu button is found by again, and what it holds for an object. */
export const ACTIONS_OF = "data-actions-of"
export function actionsOf(object: Pick<DbCatalogObject, "schema" | "name">) {
  return `${object.schema}.${object.name}`
}

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
  // The confirmation is the one every destructive act goes through; it is held
  // here rather than by `useConfirm` so that closing it can hand the keyboard
  // back to the row whose menu asked. A dialog nobody's button opened returns
  // focus to nothing, and the menu that asked had closed before it opened.
  const [request, setRequest] = useState<ConfirmRequest | null>(null)
  const asked = useRef("")
  const dialog = (
    <ConfirmDialog
      request={request}
      onOpenChange={(open) => {
        if (open) return
        setRequest(null)
        const row = asked.current
        focusAfterDialog(() => {
          const rail = document.querySelector("[data-slot=table-rail]")
          const back =
            rail?.querySelector<HTMLElement>(`[${ACTIONS_OF}="${CSS.escape(row)}"]`) ??
            // The row went with its table: the rail's own first field.
            rail?.querySelector<HTMLElement>("input")
          back?.focus()
        })
      }}
    />
  )

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
      asked.current = actionsOf(object)
      setRequest({
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
    [id, engine, onChanged],
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
