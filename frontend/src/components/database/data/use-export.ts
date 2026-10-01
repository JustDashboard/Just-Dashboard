"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { ApiError, downloadUrl, get, type ApiErrorBody } from "@/lib/api"
import { bytes } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { DbExportFormat } from "@/lib/types"
import {
  EXPORT_ROWS,
  EXPORT_ROWS_MAX,
  MARK_TAIL,
  exportEnd,
  exportReport,
} from "@/components/database/data/export"
import type { DbExportState } from "@/components/database/data/types"
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

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms))

/** A refused request as the error every other read throws. */
async function refused(response: Response): Promise<ApiError> {
  const text = await response.text()
  try {
    const body = (JSON.parse(text) as ApiErrorBody).error
    return new ApiError(response.status, body.code, body.message, undefined, {
      reason: body.reason,
      raw: body.raw,
    })
  } catch {
    return new ApiError(response.status, "unknown", text.slice(0, 300) || response.statusText)
  }
}

/** The server's record of how an export went, once it has stopped running. */
async function settled(id: number, exportId: string): Promise<DbExportState | null> {
  for (let attempt = 0; attempt < 10; attempt++) {
    try {
      const state = await get<DbExportState>(`/databases/${id}/export/status`, { exportId })
      if (state.status !== "running") return state
    } catch {
      // No record: the server restarted or has forgotten. The file's own mark answers.
      return null
    }
    await sleep(500)
  }
  return null
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
      let received = 0
      try {
        const response = await fetch(
          downloadUrl(`/databases/${id}/export`, {
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
          }),
          { credentials: "include", signal: controller.signal },
        )
        if (!response.ok || !response.body) throw await refused(response)

        const parts: BlobPart[] = []
        const reader = response.body.getReader()
        let said = 0
        let cut = false
        try {
          for (;;) {
            const { done, value } = await reader.read()
            if (done) break
            parts.push(value)
            received += value.byteLength
            // Once a megabyte, so a long export is seen to be moving.
            if (received - said >= 1 << 20) {
              said = received
              notify.raw.loading(`Exporting ${request.table}… ${bytes(received)}`, { id: toast })
            }
          }
        } catch (err) {
          if (controller.signal.aborted) throw err
          // The server ends a failed export by breaking the connection.
          cut = true
        }

        const type = response.headers.get("Content-Type") ?? "application/octet-stream"
        const blob = new Blob(parts, { type })
        const tail = await blob.slice(Math.max(0, blob.size - MARK_TAIL)).text()
        const state = await settled(id, exportId)
        // A broken connection is a failed export whatever else is known; the
        // server's record, when it has one, says why.
        const read = exportEnd(state, request.format, tail)
        const ended = cut ? { ...read, status: "failed" as const } : read
        const report = exportReport(ended, request.table, limit)

        if (ended.status !== "failed") {
          const disposition = response.headers.get("Content-Disposition") ?? ""
          const filename =
            /filename="?([^";]+)"?/.exec(disposition)?.[1] ?? `${request.table}.${request.format}`
          const url = URL.createObjectURL(blob)
          const link = document.createElement("a")
          link.href = url
          link.download = filename
          link.click()
          URL.revokeObjectURL(url)
        }

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
                  onClick: () => void run({ ...request, limit: EXPORT_ROWS_MAX }),
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

  return { run, running }
}
