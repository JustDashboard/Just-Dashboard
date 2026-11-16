"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { fetchResponse } from "@/lib/api"
import {
  exportBlob,
  exportProgress,
  saveExport,
  settledExport,
  streamExport,
} from "@/lib/stream-export"
import { notify } from "@/lib/toast"
import type { DbExportFormat } from "@/lib/types"
import {
  EXPORT_ROWS,
  EXPORT_ROWS_MAX,
  MARK_TAIL,
  exportEnd,
  exportReport,
} from "@/components/database/data/export"
import { rowsQuery, type ViewState } from "@/components/database/data/view"

export interface ExportRequest {
  schema: string
  table: string
  format: DbExportFormat
  /** The conditions and the order of the rows; absent = the whole table as it stands. */
  view?: Pick<ViewState, "filters" | "match" | "sort">
  /** Only these columns, in this order. */
  columns?: string[]
  limit?: number
}

/**
 * A table, or the rows a view matches, saved as a file.
 *
 * The file is read by this page rather than handed to the browser as a link.
 * A link that is refused navigates the tab to a page of JSON, and one that
 * fails half-way leaves a short file that looks like a whole one; read here, a
 * refusal is an error on the page and a file is only saved once the server
 * has said how the export ended — complete, cut at the row limit, or failed.
 */
export function useExport(id: number) {
  const [running, setRunning] = useState<string | null>(null)
  const flight = useRef<AbortController | null>(null)
  useEffect(() => () => flight.current?.abort(), [])
  // The toast's own "export more" asks for the same export again, larger.
  const again = useRef<(request: ExportRequest) => void>(() => {})

  const run = useCallback(
    async (request: ExportRequest) => {
      if (flight.current) return
      const controller = new AbortController()
      flight.current = controller
      setRunning(request.table)
      const limit = request.limit ?? EXPORT_ROWS
      const exportId = crypto.randomUUID()
      const toast = notify.loading(`Exporting ${request.table}…`, {
        action: { label: "Cancel", onClick: () => controller.abort() },
      })
      try {
        const response = await fetchResponse(
          `/databases/${id}/export`,
          { signal: controller.signal },
          {
            ...rowsQuery(
              request.schema,
              request.table,
              request.view ?? { filters: [], match: "all" },
            ),
            sort: request.view?.sort.length ? JSON.stringify(request.view.sort) : undefined,
            columns: request.columns ? JSON.stringify(request.columns) : undefined,
            format: request.format,
            limit,
            exportId,
          },
        )
        const read = await streamExport(response, {
          signal: controller.signal,
          progress: exportProgress(request.table, toast),
        })
        const blob = exportBlob(read.parts, response)
        const tail = await blob.slice(Math.max(0, blob.size - MARK_TAIL)).text()
        const state = await settledExport(id, exportId)
        // A broken connection is a failed export whatever else is known; the
        // server's record, when it has one, says why.
        const end = exportEnd(state, request.format, tail)
        const ended = read.cut ? { ...end, status: "failed" as const } : end
        const report = exportReport(ended, request.table, limit)

        if (ended.status !== "failed")
          saveExport(blob, response, `${request.table}.${request.format}`)

        notify.dismiss(toast)
        if (report.tone === "success") notify.success(report.title)
        else if (report.tone === "danger")
          notify.error(report.title, undefined, { description: report.description })
        else {
          const more = ended.status === "truncated" && limit < EXPORT_ROWS_MAX
          notify.warning(report.title, {
            description: report.description,
            duration: 20_000,
            action: more
              ? {
                  label: `Export up to ${EXPORT_ROWS_MAX.toLocaleString("en-US")}`,
                  onClick: () => again.current({ ...request, limit: EXPORT_ROWS_MAX }),
                }
              : undefined,
          })
        }
      } catch (err) {
        notify.dismiss(toast)
        if (controller.signal.aborted) notify.info(`The export of ${request.table} was cancelled`)
        else notify.error(`Could not export ${request.table}`, err)
      } finally {
        flight.current = null
        setRunning(null)
      }
    },
    [id],
  )

  useEffect(() => {
    again.current = (request) => void run(request)
  }, [run])

  return { run, running }
}
