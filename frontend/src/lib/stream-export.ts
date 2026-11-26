import { get } from "@/lib/api"
import { saveBlob } from "@/lib/download"
import { bytes } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { DbExportState } from "@/components/database/data/types"

/**
 * The plumbing the database exports share: the table editor's, the query
 * result's and MongoDB's.
 *
 * Each export is read by its page rather than handed to the browser as a
 * link. A link that is refused navigates the tab to a page of JSON, and one
 * that breaks off half-way leaves a short file that looks like a whole one.
 * The three differ in what they ask the server for and in how they say the
 * ending, so each keeps its own hook; the reading, the server's record of how
 * it went and the save are here, once.
 */

/** How often a long export says it is still moving: once a megabyte. */
const PROGRESS_EVERY = 1 << 20

export type StreamedExport = {
  /** The file's bytes as they came. */
  parts: BlobPart[]
  received: number
  /**
   * The server broke the connection before the end, which is how it ends an
   * export that failed. A reader who pressed Cancel is not this: that throws.
   */
  cut: boolean
}

/**
 * Reads an export's body to the end, telling `progress` how much has come
 * each megabyte. Throws when `signal` was aborted (the reader cancelled), and
 * says `cut` when the connection broke for any other reason.
 */
export async function streamExport(
  response: Response,
  { signal, progress }: { signal?: AbortSignal; progress?: (received: number) => void } = {},
): Promise<StreamedExport> {
  if (!response.body) throw new Error("The export response has no body")
  const parts: BlobPart[] = []
  const reader = response.body.getReader()
  let received = 0
  let said = 0
  try {
    for (;;) {
      const { done, value } = await reader.read()
      if (done) break
      parts.push(value)
      received += value.byteLength
      if (received - said >= PROGRESS_EVERY) {
        said = received
        progress?.(received)
      }
    }
  } catch (err) {
    if (signal?.aborted) throw err
    return { parts, received, cut: true }
  }
  return { parts, received, cut: false }
}

/** The loading toast's line while an export streams: "Exporting orders… 12 MB". */
export function exportProgress(name: string, toastId: string | number) {
  return (received: number) =>
    notify.raw.loading(`Exporting ${name}… ${bytes(received)}`, { id: toastId })
}

/** A wait that ends at once, by throwing, when `signal` is aborted. */
function sleep(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    signal?.throwIfAborted()
    const timer = setTimeout(() => {
      signal?.removeEventListener("abort", stop)
      resolve()
    }, ms)
    function stop() {
      clearTimeout(timer)
      reject(signal?.reason)
    }
    signal?.addEventListener("abort", stop, { once: true })
  })
}

/**
 * The server's record of how an export went, once it has stopped running.
 * `null` when it has none (it restarted, or has forgotten): the file's own
 * end mark then answers.
 *
 * Throws when `signal` is aborted, as `streamExport` does: the loading toast
 * still offers Cancel while this asks, and a reader who pressed it then is
 * not handed the file once the record lands.
 */
export async function settledExport(
  dbId: number,
  exportId: string,
  signal?: AbortSignal,
): Promise<DbExportState | null> {
  for (let attempt = 0; attempt < 10; attempt++) {
    try {
      const state = await get<DbExportState>(
        `/databases/${dbId}/export/status`,
        { exportId },
        signal,
      )
      if (state.status !== "running") return state
    } catch (err) {
      if (signal?.aborted) throw err
      return null
    }
    await sleep(500, signal)
  }
  return null
}

/** The file's bytes, typed as the server said. */
export function exportBlob(parts: BlobPart[], response: Response): Blob {
  return new Blob(parts, {
    type: response.headers.get("Content-Type") ?? "application/octet-stream",
  })
}

/** The name the server gave the file, or `fallback` when it gave none. */
export function exportName(response: Response, fallback: string): string {
  const disposition = response.headers.get("Content-Disposition") ?? ""
  return /filename="?([^";]+)"?/.exec(disposition)?.[1] ?? fallback
}

/**
 * Hands the browser the finished file, under the server's name for it. A
 * `signal` that was aborted saves nothing and throws: Cancel pressed at any
 * point before the save is a cancelled export, never a file.
 */
export function saveExport(
  blob: Blob,
  response: Response,
  fallbackName: string,
  signal?: AbortSignal,
): void {
  signal?.throwIfAborted()
  saveBlob(exportName(response, fallbackName), blob)
}
