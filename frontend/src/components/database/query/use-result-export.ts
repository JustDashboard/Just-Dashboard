"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { ApiError, API_BASE, get, mutationHeaders, type ApiErrorBody } from "@/lib/api"
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

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms))

async function refused(response: Response): Promise<ApiError> {
  const text = await response.text()
  try {
    const body = (JSON.parse(text) as ApiErrorBody).error
    return new ApiError(response.status, body.code, body.message)
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
      return null
    }
    await sleep(500)
  }
  return null
}

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
        const response = await fetch(`${API_BASE}/databases/${id}/export/query`, {
          method: "POST",
          headers: { ...mutationHeaders(), "Content-Type": "application/json" },
          credentials: "include",
          signal: controller.signal,
          body: JSON.stringify({ sql, format, limit, exportId }),
        })
        if (!response.ok || !response.body) throw await refused(response)
        let cut = false
        let blob = new Blob([])
        try {
          blob = await response.blob()
        } catch (err) {
          if (controller.signal.aborted) throw err
          // The server ends a failed export by breaking the connection.
          cut = true
        }
        const tail = await blob.slice(Math.max(0, blob.size - MARK_TAIL)).text()
        const read = exportEnd(await settled(id, exportId), format, tail)
        const ended = cut ? { ...read, status: "failed" as const } : read
        const report = exportReport(ended, "the result", limit)
        if (ended.status !== "failed") {
          const disposition = response.headers.get("Content-Disposition") ?? ""
          const filename = /filename="?([^";]+)"?/.exec(disposition)?.[1] ?? `query.${format}`
          const url = URL.createObjectURL(blob)
          const link = document.createElement("a")
          link.href = url
          link.download = filename
          link.click()
          URL.revokeObjectURL(url)
        }
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
