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

/**
 * A statement's whole result as a file, written by the server.
 *
 * The rows on the page are the first few hundred; an export of *those* is a
 * file that looks complete and is not. So the statement is handed back to
 * the server, which runs it again as a read and streams every row — and the
 * file is only saved once the server has said how the export ended: whole,
 * cut at its row limit, or failed. The table editor's export reads its ending
 * the same way; the words and the arithmetic are its.
 */
export function useResultExport(id: number) {
  const [running, setRunning] = useState(false)
  const flight = useRef<AbortController | null>(null)
  useEffect(() => () => flight.current?.abort(), [])
  // The toast's own "export more" asks for the same export again, larger.
  const again = useRef<(sql: string, format: DbExportFormat, limit: number) => void>(() => {})

  const run = useCallback(
    async (sql: string, format: DbExportFormat, limit = EXPORT_ROWS) => {
      if (flight.current) return
      const controller = new AbortController()
      flight.current = controller
      setRunning(true)
      const exportId = crypto.randomUUID()
      const toast = notify.loading("Exporting the result…", {
        action: { label: "Cancel", onClick: () => controller.abort() },
      })
      try {
        const response = await fetchResponse(`/databases/${id}/export/query`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          credentials: "include",
          signal: controller.signal,
          body: JSON.stringify({ sql, format, limit, exportId }),
        })
        const read = await streamExport(response, {
          signal: controller.signal,
          progress: exportProgress("the result", toast),
        })
        const blob = exportBlob(read.parts, response)
        const tail = await blob.slice(Math.max(0, blob.size - MARK_TAIL)).text()
        // A broken connection is a failed export whatever else is known.
        const end = exportEnd(await settledExport(id, exportId, controller.signal), format, tail)
        const ended = read.cut ? { ...end, status: "failed" as const } : end
        const report = exportReport(ended, "the result", limit)
        if (ended.status !== "failed")
          saveExport(blob, response, `query.${format}`, controller.signal)
        notify.dismiss(toast)
        if (report.tone === "success") notify.success(report.title)
        else if (report.tone === "danger") {
          notify.error(report.title, undefined, { description: report.description })
        } else {
          notify.warning(report.title, {
            description: report.description,
            duration: 20_000,
            action:
              ended.status === "truncated" && limit < EXPORT_ROWS_MAX
                ? {
                    label: `Export up to ${EXPORT_ROWS_MAX.toLocaleString("en-US")}`,
                    onClick: () => again.current(sql, format, EXPORT_ROWS_MAX),
                  }
                : undefined,
          })
        }
      } catch (err) {
        notify.dismiss(toast)
        if (controller.signal.aborted) notify.info("The export was cancelled")
        else notify.error("Could not export the result", err)
      } finally {
        flight.current = null
        setRunning(false)
      }
    },
    [id],
  )

  useEffect(() => {
    again.current = (sql, format, limit) => void run(sql, format, limit)
  }, [run])

  return { run, running }
}
