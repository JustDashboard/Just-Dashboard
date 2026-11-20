import { bytes } from "@/lib/format"

/** Where one transfer in the uploads panel stands. */
export type TransferStatus =
  | "queued"
  | "uploading"
  | "reconnecting"
  | "paused"
  | "stopped"
  | "finishing"
  | "done"
  | "failed"
  | "cancelled"

export type TransferReading = {
  kind: "upload" | "fetch"
  status: TransferStatus
  size: number
  sent: number
  /** Bytes a second, measured over the last few seconds; undefined until known. */
  rate?: number
  /** When a dropped connection is tried again (ms since epoch). */
  retryAt?: number
  error?: string
}

/** A wait or a time left, the way a person says it: "a few seconds", "about 3 minutes". */
export function aboutTime(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return ""
  if (seconds < 10) return "a few seconds"
  if (seconds < 60) return `${Math.round(seconds / 5) * 5} seconds`
  const minutes = Math.round(seconds / 60)
  if (minutes < 60) return minutes === 1 ? "about a minute" : `about ${minutes} minutes`
  const hours = Math.floor(minutes / 60)
  const rest = minutes % 60
  const h = hours === 1 ? "1 hour" : `${hours} hours`
  return rest >= 5 ? `about ${h} ${rest} minutes` : `about ${h}`
}

/** "1.2 GB of 4.0 GB", or "12 MB so far" for a download that did not say its size. */
export function progressWords(sent: number, size: number): string {
  return size >= 0 ? `${bytes(sent)} of ${bytes(size)}` : `${bytes(sent)} so far`
}

/**
 * The one line beside a transfer's name: how far it is, in words, and what it
 * is waiting for when it waits.
 */
export function transferWords(t: TransferReading, now: number): string {
  switch (t.status) {
    case "queued":
      return "Waiting for its turn"
    case "uploading": {
      const far = progressWords(t.sent, t.size)
      if (t.rate && t.rate > 0 && t.size > 0 && t.sent < t.size) {
        const left = aboutTime((t.size - t.sent) / t.rate)
        if (left) return `${far}, ${left} left`
      }
      return t.kind === "fetch" && t.sent === 0 ? "Connecting" : far
    }
    case "reconnecting": {
      const wait = t.retryAt ? Math.max(0, (t.retryAt - now) / 1000) : 0
      return wait >= 1
        ? `Connection lost at ${bytes(t.sent)}; trying again in ${aboutTime(wait)}`
        : `Connection lost at ${bytes(t.sent)}; trying again`
    }
    case "paused":
      return `Paused at ${progressWords(t.sent, t.size)}`
    case "stopped":
      return `Stopped at ${progressWords(t.sent, t.size)}; add the file again to carry on`
    case "finishing":
      return "Putting it in place"
    case "done":
      return t.kind === "fetch" ? `Fetched, ${bytes(t.size >= 0 ? t.size : t.sent)}` : "Done"
    case "cancelled":
      return "Cancelled"
    case "failed":
      return t.error ?? "Failed"
  }
}
