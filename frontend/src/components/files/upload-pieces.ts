import { API_BASE, ApiError, errorFromResponse, mutationHeaders } from "@/lib/api"

/**
 * An upload in pieces (`/files/uploads`): what the server says about one, and
 * the transport for a piece. The queue in `uploads.tsx` drives it.
 */
export type UploadSession = {
  id: string
  path: string
  dir: string
  name: string
  size: number
  key: string
  overwrite: boolean
  received: number
  next: number
  created: string
  updated: string
}

/** A file larger than this goes in pieces; one this size or smaller is one request. */
export const SINGLE_MAX = 32 * 1024 * 1024
/** One piece. Small enough that a dropped connection loses seconds, not minutes. */
export const PIECE = 8 * 1024 * 1024

/** Which file this is, as far as a page reloaded since can tell: dropping it again carries on. */
export function fileKey(file: File, name: string): string {
  return `${name}:${file.size}:${file.lastModified}`
}

/**
 * Whether asking again can help: no answer at all, a server that is
 * restarting or busy. A full disk (507) and a refusal are not.
 */
export function transient(err: unknown): boolean {
  if (err instanceof ApiError) {
    return (
      (err.status >= 500 && err.status !== 507) ||
      err.status === 408 ||
      err.status === 429 ||
      err.status === 0
    )
  }
  return !(err instanceof DOMException && err.name === "AbortError")
}

/** The wait before the next try after `failures` in a row: 1, 2, 4 … 30 seconds. */
export function retryWait(failures: number): number {
  return Math.min(30_000, 1000 * 2 ** Math.max(0, failures - 1))
}

/**
 * Waits `ms`, or less: until the browser says it is back online, the tab comes
 * back to the front (a laptop waking), or `wake` is called (Resume pressed).
 */
export function pause(ms: number, signal: { wake?: () => void }): Promise<void> {
  return new Promise((resolve) => {
    const done = () => {
      clearTimeout(timer)
      window.removeEventListener("online", done)
      document.removeEventListener("visibilitychange", visible)
      signal.wake = undefined
      resolve()
    }
    const visible = () => {
      if (document.visibilityState === "visible") done()
    }
    const timer = setTimeout(done, ms)
    window.addEventListener("online", done)
    document.addEventListener("visibilitychange", visible)
    signal.wake = done
  })
}

/**
 * Sends one piece with `XMLHttpRequest`, the one browser transport that
 * reports upload progress. Rejects with an `ApiError`, status 0 when there was
 * no answer.
 */
export function sendPiece(
  session: string,
  offset: number,
  piece: Blob,
  onProgress: (loaded: number) => void,
  hold: { xhr?: XMLHttpRequest },
): Promise<UploadSession> {
  return new Promise((resolve, reject) => {
    const path = `/files/uploads/${encodeURIComponent(session)}`
    const xhr = new XMLHttpRequest()
    hold.xhr = xhr
    xhr.open("PUT", `${API_BASE}${path}?offset=${offset}`)
    xhr.withCredentials = true
    for (const [key, value] of Object.entries(mutationHeaders())) xhr.setRequestHeader(key, value)
    xhr.setRequestHeader("Content-Type", "application/octet-stream")
    xhr.upload.onprogress = (event) => onProgress(event.loaded)
    xhr.onload = () => {
      hold.xhr = undefined
      if (xhr.status >= 200 && xhr.status < 300) {
        try {
          resolve(JSON.parse(xhr.responseText) as UploadSession)
        } catch {
          reject(new ApiError(0, "unknown", "The answer to a piece could not be read."))
        }
      } else reject(errorFromResponse(xhr.status, xhr.responseText, path, xhr.statusText))
    }
    xhr.onerror = () => {
      hold.xhr = undefined
      reject(new ApiError(0, "network", "The connection dropped."))
    }
    xhr.onabort = () => {
      hold.xhr = undefined
      reject(new DOMException("Stopped", "AbortError"))
    }
    xhr.send(piece)
  })
}
