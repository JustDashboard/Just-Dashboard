"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { Cross, Pause, Play } from "@/components/icons"
import { API_BASE, ApiError, del, errorFromResponse, get, mutationHeaders, post } from "@/lib/api"
import { bytes, plural } from "@/lib/format"
import { cn } from "@/lib/utils"
import { Button } from "@/components/ui/button"
import { IconAction } from "@/components/icon-action"
import { Meter } from "@/components/meter"
import {
  PIECE,
  SINGLE_MAX,
  fileKey,
  pause as waitAWhile,
  retryWait,
  sendPiece,
  transient,
  type UploadSession,
} from "./upload-pieces"
import { transferWords, type TransferReading } from "./transfer-words"

export type UploadStatus = TransferReading["status"]

export type UploadItem = TransferReading & {
  id: string
  name: string
  dir: string
  /** Sent in pieces: it can be paused, and it carries on after a dropped connection. */
  pieces?: boolean
  /** Known only to the server: an upload a reloaded page has not been given the file for. */
  remote?: boolean
}

export type UploadJob = { file: File; dir: string; name: string; overwrite: boolean }

/** A link the server is fetching into a folder (`/files/fetches`). */
export type ServerFetch = {
  id: string
  url: string
  dir: string
  name?: string
  size: number
  received: number
  status: "running" | "done" | "failed" | "cancelled"
  error?: string
  mine: boolean
}

const FETCH_POLL_MS = 1000

function fetchItem(f: ServerFetch): UploadItem {
  return {
    id: `fetch:${f.id}`,
    kind: "fetch",
    name: f.name || f.url.split("/").filter(Boolean).pop() || f.url,
    dir: f.dir,
    size: f.size,
    sent: f.received,
    status:
      f.status === "running"
        ? "uploading"
        : f.status === "done"
          ? "done"
          : f.status === "cancelled"
            ? "cancelled"
            : "failed",
    error: f.error,
  }
}

/** A file on its way in, with where it sits relative to the drop. */
export type Incoming = { file: File; rel: string }

const PARALLEL = 3

type Running = {
  job: UploadJob
  xhr?: XMLHttpRequest
  session?: string
  paused?: boolean
  cancelled?: boolean
  wake?: () => void
  /** For the time left: the last progress reading and the rate measured from it. */
  mark?: { at: number; sent: number }
  rate?: number
}

/**
 * The upload queue.
 *
 * One request per small file rather than one form for all of them: a single
 * multipart body carrying forty files is one progress bar that says nothing,
 * one failure that takes the other thirty-nine with it. A file over
 * `SINGLE_MAX` goes in pieces (`/files/pieces`), which is what lets it be of
 * any size, be paused, and carry on by itself after a dropped connection or a
 * sleeping laptop: the server says where it stands and the next piece starts
 * there. A page reloaded halfway lists what the server still holds, and the
 * same file dropped again continues it. `XMLHttpRequest` rather than `fetch`
 * because it is still the only way a browser reports upload progress.
 *
 * Three transfers run at once. More does not make a home connection faster;
 * fewer leaves a folder of small files waiting on round trips.
 */
export function useUploads(onSettled: (dir: string) => void) {
  const [items, setItems] = useState<UploadItem[]>([])
  const queue = useRef<string[]>([])
  const jobs = useRef(new Map<string, Running>())
  const active = useRef(new Set<string>())
  const nextId = useRef(1)
  const settled = useRef(onSettled)
  useEffect(() => {
    settled.current = onSettled
  }, [onSettled])

  const update = useCallback((id: string, patch: Partial<UploadItem>) => {
    setItems((prev) => prev.map((item) => (item.id === id ? { ...item, ...patch } : item)))
  }, [])

  /** Progress, with the rate measured over a few seconds for the time left. */
  const progress = useCallback(
    (id: string, sent: number) => {
      const run = jobs.current.get(id)
      if (run) {
        const now = Date.now()
        if (!run.mark || sent < run.mark.sent) run.mark = { at: now, sent }
        else if (now - run.mark.at >= 2000) {
          const measured = ((sent - run.mark.sent) * 1000) / (now - run.mark.at)
          run.rate = run.rate ? run.rate * 0.6 + measured * 0.4 : measured
          run.mark = { at: now, sent }
        }
      }
      update(id, { sent, rate: run?.rate })
    },
    [update],
  )

  // A finished transfer starts the next one, which means the runner calls
  // itself from a callback registered before it existed; the ref is how it
  // reaches its own current self.
  const pumpRef = useRef<() => void>(() => undefined)

  const runSingle = useCallback(
    (id: string, run: Running) =>
      new Promise<void>((resolve) => {
        const { job } = run
        const xhr = new XMLHttpRequest()
        run.xhr = xhr
        const form = new FormData()
        form.append("file", job.file, job.name)
        xhr.open(
          "POST",
          `${API_BASE}/files/upload?path=${encodeURIComponent(job.dir)}&overwrite=${job.overwrite}`,
        )
        xhr.withCredentials = true
        for (const [key, value] of Object.entries(mutationHeaders()))
          xhr.setRequestHeader(key, value)
        xhr.upload.onprogress = (event) => progress(id, event.loaded)
        const finish = (patch: Partial<UploadItem>) => {
          update(id, patch)
          resolve()
        }
        xhr.onload = () => {
          if (xhr.status >= 200 && xhr.status < 300) finish({ status: "done", sent: job.file.size })
          else finish({ status: "failed", error: failureOf(xhr) })
        }
        xhr.onerror = () => finish({ status: "failed", error: "The connection dropped." })
        xhr.onabort = () => finish({ status: "cancelled" })
        update(id, { status: "uploading" })
        xhr.send(form)
      }),
    [progress, update],
  )

  const runPieces = useCallback(
    async (id: string, run: Running) => {
      const { job } = run
      const size = job.file.size
      const stopped = () => run.paused || run.cancelled
      let failures = 0
      /** After a failure: true to try again (having waited), false to give up. */
      const recover = async (err: unknown, at: number): Promise<boolean> => {
        if (stopped()) return false
        if (!transient(err)) {
          update(id, { status: "failed", error: sentenceOf(err) })
          return false
        }
        failures += 1
        const wait = retryWait(failures)
        update(id, { status: "reconnecting", retryAt: Date.now() + wait, sent: at })
        await waitAWhile(wait, run)
        return !stopped()
      }

      let session: UploadSession | undefined
      while (!session) {
        if (stopped()) return
        try {
          session = await post<UploadSession>("/files/pieces", {
            path: job.dir,
            name: job.name,
            size,
            key: fileKey(job.file, job.name),
            overwrite: job.overwrite,
          })
        } catch (err) {
          if (!(await recover(err, 0))) return
        }
      }
      run.session = session.id
      // The server already held this one: the reloaded page's row for it is
      // this row now.
      setItems((prev) => prev.filter((item) => item.id !== `server:${session.id}`))
      let offset = session.next
      update(id, { status: "uploading", sent: offset, retryAt: undefined })

      while (offset < size) {
        if (stopped()) return
        const from = offset
        try {
          const answer = await sendPiece(
            session.id,
            from,
            job.file.slice(from, Math.min(size, from + PIECE)),
            (loaded) => progress(id, from + loaded),
            run,
          )
          offset = answer.next
          failures = 0
          update(id, { status: "uploading", sent: offset, retryAt: undefined })
        } catch (err) {
          if (!(await recover(err, from))) return
          try {
            // Where the server stands is where to carry on: a piece cut off
            // halfway kept what arrived.
            offset = (await get<UploadSession>(`/files/pieces/${session.id}`)).next
          } catch (lost) {
            if (lost instanceof ApiError && lost.code === "upload_not_found") {
              update(id, { status: "failed", error: "The server no longer holds this upload." })
              return
            }
          }
          update(id, { status: "uploading", sent: offset, retryAt: undefined })
        }
      }

      update(id, { status: "finishing", sent: size })
      for (;;) {
        if (stopped()) return
        try {
          await post(`/files/pieces/${session.id}/complete`)
          update(id, { status: "done", retryAt: undefined })
          return
        } catch (err) {
          if (err instanceof ApiError && err.code === "upload_not_found" && failures > 0) {
            // The answer to a completion that went through was lost.
            update(id, { status: "done", retryAt: undefined })
            return
          }
          if (!(await recover(err, size))) return
          update(id, { status: "finishing" })
        }
      }
    },
    [progress, update],
  )

  const pump = useCallback(() => {
    while (active.current.size < PARALLEL && queue.current.length > 0) {
      const id = queue.current.shift()!
      const run = jobs.current.get(id)
      if (!run || run.cancelled || run.paused) continue
      active.current.add(id)
      const pieces = run.job.file.size > SINGLE_MAX
      update(id, { status: "uploading" })
      void (pieces ? runPieces(id, run) : runSingle(id, run)).finally(() => {
        active.current.delete(id)
        if (!run.paused) settled.current(run.job.dir)
        pumpRef.current()
      })
    }
  }, [runPieces, runSingle, update])

  useEffect(() => {
    pumpRef.current = pump
  }, [pump])

  // What a page reloaded halfway left on the server: shown, so it can be
  // cancelled, and continued by dropping the same file again.
  useEffect(() => {
    const controller = new AbortController()
    get<UploadSession[]>("/files/pieces", undefined, controller.signal)
      .then((sessions) => {
        if (!Array.isArray(sessions)) return
        const held = new Set([...jobs.current.values()].map((run) => run.session))
        const stopped: UploadItem[] = sessions
          .filter((s) => !held.has(s.id))
          .map((s) => ({
            id: `server:${s.id}`,
            kind: "upload",
            name: s.name,
            dir: s.dir,
            size: s.size,
            sent: s.received,
            status: "stopped",
            pieces: true,
            remote: true,
          }))
        if (stopped.length > 0) setItems((prev) => [...stopped, ...prev])
      })
      .catch(() => undefined)
    return () => controller.abort()
  }, [])

  const enqueue = useCallback(
    (list: UploadJob[]) => {
      if (list.length === 0) return
      const created = list.map((job) => ({ id: `upload:${nextId.current++}`, job }))
      setItems((prev) => [
        ...prev,
        ...created.map(({ id, job }) => ({
          id,
          kind: "upload" as const,
          name: job.name,
          dir: job.dir,
          size: job.file.size,
          sent: 0,
          status: "queued" as const,
          pieces: job.file.size > SINGLE_MAX,
        })),
      ])
      for (const { id, job } of created) {
        jobs.current.set(id, { job })
        queue.current.push(id)
      }
      pump()
    },
    [pump],
  )

  const pauseUpload = useCallback((id: string) => {
    const run = jobs.current.get(id)
    if (!run || run.job.file.size <= SINGLE_MAX) return
    run.paused = true
    run.xhr?.abort()
    run.wake?.()
    queue.current = queue.current.filter((queued) => queued !== id)
    setItems((prev) =>
      prev.map((item) =>
        item.id === id ? { ...item, status: "paused", retryAt: undefined, rate: undefined } : item,
      ),
    )
  }, [])

  const resume = useCallback(
    (id: string) => {
      const run = jobs.current.get(id)
      if (!run || !run.paused) return
      run.paused = false
      run.mark = undefined
      run.rate = undefined
      update(id, { status: "queued" })
      queue.current.unshift(id)
      pump()
    },
    [pump, update],
  )

  const cancel = useCallback(
    (id: string) => {
      if (id.startsWith("server:")) {
        void del(`/files/pieces/${id.slice("server:".length)}`).catch(() => undefined)
        setItems((prev) => prev.filter((item) => item.id !== id))
        return
      }
      const run = jobs.current.get(id)
      if (!run) return
      run.cancelled = true
      run.xhr?.abort()
      run.wake?.()
      queue.current = queue.current.filter((queued) => queued !== id)
      if (run.session) void del(`/files/pieces/${run.session}`).catch(() => undefined)
      update(id, { status: "cancelled" })
    },
    [update],
  )

  const dismiss = useCallback((id: string) => {
    const run = jobs.current.get(id)
    // A failed upload in pieces still holds its bytes on the server; dismissing
    // it is letting them go.
    if (run?.session && !run.cancelled) {
      run.cancelled = true
      void del(`/files/pieces/${run.session}`).catch(() => undefined)
    }
    jobs.current.delete(id)
    setItems((prev) => prev.filter((item) => item.id !== id))
  }, [])

  const clear = useCallback(() => {
    setItems((prev) => {
      for (const item of prev) {
        if (item.status === "failed" && item.kind === "fetch")
          void del(`/files/fetches/${item.id.slice("fetch:".length)}`).catch(() => undefined)
        if (item.status === "failed") {
          const run = jobs.current.get(item.id)
          if (run?.session && !run.cancelled) {
            run.cancelled = true
            void del(`/files/pieces/${run.session}`).catch(() => undefined)
          }
        }
      }
      return prev.filter((item) => isLive(item) || item.status === "paused" || item.remote)
    })
  }, [])

  // A finished transfer leaves the strip on its own a few seconds later; a
  // failed one stays until it is read, because its message is the point.
  useEffect(() => {
    const done = items.filter((item) => item.status === "done" || item.status === "cancelled")
    if (done.length === 0) return
    const timer = setTimeout(() => {
      setItems((prev) =>
        prev.filter((item) => item.status !== "done" && item.status !== "cancelled"),
      )
    }, 4000)
    return () => clearTimeout(timer)
  }, [items])

  useEffect(() => {
    const live = jobs.current
    return () => {
      // Leaving the page stops what this page was sending; an upload in
      // pieces stays on the server for a day and carries on when its file is
      // given again.
      for (const run of live.values()) {
        run.paused = true
        run.xhr?.abort()
        run.wake?.()
      }
    }
  }, [])

  // Links the server is fetching. They run on the server, so they are read
  // back, not driven: once when the page opens (a fetch started before a
  // navigation is still going) and every second while one is running.
  const [fetchTick, setFetchTick] = useState(0)
  const fetchingSome = items.some((item) => item.kind === "fetch" && isLive(item))
  useEffect(() => {
    let stopped = false
    const controller = new AbortController()
    const read = async () => {
      try {
        const list = await get<ServerFetch[]>("/files/fetches", undefined, controller.signal)
        if (stopped || !Array.isArray(list)) return
        setItems((prev) => {
          const byId = new Map(list.map((f) => [`fetch:${f.id}`, f]))
          const next: UploadItem[] = []
          for (const item of prev) {
            if (item.kind !== "fetch") {
              next.push(item)
              continue
            }
            const f = byId.get(item.id)
            byId.delete(item.id)
            if (!f) {
              // Gone from the server's list: finished long enough ago, or
              // dismissed in another tab.
              if (isLive(item)) next.push({ ...item, status: "done" })
              continue
            }
            const fresh = fetchItem(f)
            if (isLive(item) && fresh.status === "done") settled.current(fresh.dir)
            const rate =
              fresh.status === "uploading" && fresh.sent > item.sent
                ? ((fresh.sent - item.sent) * 1000) / FETCH_POLL_MS
                : item.rate
            next.push({ ...fresh, rate: item.rate ? item.rate * 0.6 + (rate ?? 0) * 0.4 : rate })
          }
          // Fetches this page did not start: those still running, and this
          // account's failures, which nobody has read yet.
          for (const f of byId.values()) {
            if (f.status === "running" || (f.mine && f.status === "failed")) next.push(fetchItem(f))
          }
          return next
        })
      } catch {
        // The next read says it again.
      }
    }
    void read()
    if (!fetchingSome) return () => void (stopped = true)
    const timer = setInterval(read, FETCH_POLL_MS)
    return () => {
      stopped = true
      controller.abort()
      clearInterval(timer)
    }
  }, [fetchingSome, fetchTick])

  const fetchLink = useCallback(async (dir: string, url: string) => {
    const started = await post<ServerFetch>("/files/fetch", { path: dir, url })
    setItems((prev) => [...prev, fetchItem(started)])
    setFetchTick((tick) => tick + 1)
  }, [])

  const cancelOrDismiss = useCallback((id: string) => {
    if (!id.startsWith("fetch:")) return false
    const serverId = id.slice("fetch:".length)
    setItems((prev) =>
      prev.flatMap((item) =>
        item.id !== id ? [item] : isLive(item) ? [{ ...item, status: "cancelled" as const }] : [],
      ),
    )
    void del(`/files/fetches/${serverId}`).catch(() => undefined)
    return true
  }, [])

  return {
    items,
    enqueue,
    cancel: (id: string) => cancelOrDismiss(id) || cancel(id),
    dismiss: (id: string) => cancelOrDismiss(id) || dismiss(id),
    clear,
    pause: pauseUpload,
    resume,
    fetchLink,
  }
}

/** Still moving, or about to. */
function isLive(item: UploadItem) {
  return (
    item.status === "queued" ||
    item.status === "uploading" ||
    item.status === "reconnecting" ||
    item.status === "finishing"
  )
}

function sentenceOf(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.code === "already_exists") return "A file with this name already exists."
    return err.message
  }
  return err instanceof Error ? err.message : String(err)
}

function failureOf(xhr: XMLHttpRequest): string {
  const error = errorFromResponse(xhr.status, xhr.responseText, "/files/upload", xhr.statusText)
  if (error.code === "already_exists") return "A file with this name already exists."
  if (error.code !== "unknown") return error.message
  if (xhr.status === 413) return "Too large for one request."
  return xhr.statusText || `Failed (${xhr.status})`
}

/**
 * Everything a drop carried, folders walked.
 *
 * A folder dropped onto the page arrives as a directory entry rather than a
 * file, and reading it as a file — which `dataTransfer.files` will happily
 * let you do — uploads an empty file named after the folder. The entries API
 * is read synchronously, before the first `await`, because a DataTransfer is
 * only valid for the duration of its event.
 */
export async function collectTransfer(transfer: DataTransfer): Promise<Incoming[]> {
  const items = Array.from(transfer.items ?? [])
  const entries = items.map((item) =>
    typeof item.webkitGetAsEntry === "function" ? item.webkitGetAsEntry() : null,
  )
  const files = items.map((item) => (item.kind === "file" ? item.getAsFile() : null))
  if (entries.every((entry) => entry === null)) {
    return Array.from(transfer.files)
      .filter((file) => !looksLikeDirectory(file))
      .map((file) => ({ file, rel: file.name }))
  }
  const out: Incoming[] = []
  for (const [i, entry] of entries.entries()) {
    if (entry) await walk(entry, "", out)
    else if (files[i] && !looksLikeDirectory(files[i]))
      out.push({ file: files[i], rel: files[i].name })
  }
  return out
}

/** Files chosen through an input, with the relative path a folder picker gives them. */
export function collectInput(list: FileList | null): Incoming[] {
  if (!list) return []
  return Array.from(list)
    .filter((file) => !looksLikeDirectory(file))
    .map((file) => ({ file, rel: file.webkitRelativePath || file.name }))
}

// A browser without the entries API hands a dropped folder over as a File
// with no type and no size; nothing useful arrives that way.
function looksLikeDirectory(file: File): boolean {
  return file.size === 0 && file.type === "" && !file.name.includes(".")
}

async function walk(entry: FileSystemEntry, prefix: string, out: Incoming[]): Promise<void> {
  if (entry.isFile) {
    const file = await new Promise<File | null>((resolve) =>
      (entry as FileSystemFileEntry).file(resolve, () => resolve(null)),
    )
    if (file) out.push({ file, rel: prefix + entry.name })
    return
  }
  if (!entry.isDirectory) return
  const reader = (entry as FileSystemDirectoryEntry).createReader()
  // readEntries returns a batch at a time and an empty batch at the end.
  for (;;) {
    const batch = await new Promise<FileSystemEntry[]>((resolve) =>
      reader.readEntries(resolve, () => resolve([])),
    )
    if (batch.length === 0) break
    for (const child of batch) await walk(child, `${prefix}${entry.name}/`, out)
  }
}

/**
 * The transfers in flight, above the listing's footer.
 *
 * Each row is one line: the name, a bar, how far it is in words, and its
 * verbs — pause and resume for a file sent in pieces, cancel for anything
 * moving, dismiss for anything finished. The strip's own line says how many
 * are still going. It is drawn in the workbench rather than as toasts because
 * a transfer is something you watch, and a toast is something that goes away.
 */
export function UploadStrip({
  items,
  currentDir,
  onCancel,
  onDismiss,
  onClear,
  onPause,
  onResume,
  className,
}: {
  items: UploadItem[]
  currentDir: string
  onCancel: (id: string) => void
  onDismiss: (id: string) => void
  onClear: () => void
  onPause?: (id: string) => void
  onResume?: (id: string) => void
  className?: string
}) {
  const [now, setNow] = useState(() => Date.now())
  const waiting = items.some((item) => item.status === "reconnecting")
  useEffect(() => {
    if (!waiting) return
    const timer = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [waiting])
  if (items.length === 0) return null
  const live = items.filter(isLive)
  const failed = items.filter((item) => item.status === "failed")
  const held = items.filter((item) => item.status === "paused" || item.status === "stopped")
  const known = live.filter((item) => item.size >= 0)
  const total = known.reduce((sum, item) => sum + item.size, 0)
  const sent = known.reduce((sum, item) => sum + item.sent, 0)
  const fetching = live.filter((item) => item.kind === "fetch").length
  const uploading = live.length - fetching

  return (
    <div
      className={cn("shrink-0 border-t border-hairline bg-surface-header", className)}
      data-transfers
    >
      <div className="flex items-center gap-3 px-3 py-1.5 text-hint text-muted-foreground">
        <span className="min-w-0 flex-1 truncate" role="status">
          {live.length > 0
            ? [
                uploading > 0 && `Uploading ${plural(uploading, "file")}`,
                fetching > 0 &&
                  `${uploading > 0 ? "fetching" : "Fetching"} ${plural(fetching, "link")}`,
              ]
                .filter(Boolean)
                .join(", ") + (total > 0 ? ` · ${bytes(sent)} of ${bytes(total)}` : "")
            : failed.length > 0
              ? `${plural(failed.length, "transfer")} failed`
              : held.length > 0
                ? `${plural(held.length, "upload")} paused`
                : "Transfers finished"}
        </span>
        {live.length > 0 && total > 0 && (
          <Meter
            value={(sent / total) * 100}
            size="thin"
            label="Transfer progress"
            className="w-32"
          />
        )}
        {live.length === 0 && (
          <Button size="xs" variant="ghost" onClick={onClear}>
            Clear
          </Button>
        )}
      </div>
      <ul className="max-h-40 divide-y divide-hairline overflow-auto border-t border-hairline">
        {items.map((item) => {
          const moving = isLive(item)
          const words = transferWords(item, now)
          return (
            <li
              key={item.id}
              className="flex h-8 items-center gap-3 px-3 text-xs"
              data-transfer={item.name}
              data-status={item.status}
            >
              <span className="min-w-0 flex-1 truncate" title={`${item.dir}/${item.name}`}>
                {item.name}
                {item.dir !== currentDir && (
                  <span className="ml-1.5 font-mono text-hint text-muted-foreground">
                    → {item.dir}
                  </span>
                )}
              </span>
              {(moving || item.status === "paused") && item.size > 0 && (
                <Meter
                  value={(item.sent / item.size) * 100}
                  size="thin"
                  label={`${item.name} progress`}
                  className="hidden w-24 sm:block"
                />
              )}
              <span
                className={cn(
                  "numeric max-w-[55%] min-w-0 shrink truncate text-right text-hint",
                  item.status === "failed"
                    ? "text-destructive"
                    : item.status === "done"
                      ? "text-success"
                      : "text-muted-foreground",
                )}
                title={words}
              >
                {words}
              </span>
              {item.pieces && !item.remote && (moving || item.status === "paused") && (
                <IconAction
                  label={item.status === "paused" ? "Resume" : "Pause"}
                  className="size-6"
                  disabled={item.status === "finishing"}
                  onClick={() =>
                    item.status === "paused" ? onResume?.(item.id) : onPause?.(item.id)
                  }
                >
                  {item.status === "paused" ? <Play /> : <Pause />}
                </IconAction>
              )}
              <IconAction
                label={
                  moving || item.status === "paused" || item.status === "stopped"
                    ? "Cancel"
                    : "Dismiss"
                }
                className="size-6"
                onClick={() =>
                  moving || item.status === "paused" || item.status === "stopped"
                    ? onCancel(item.id)
                    : onDismiss(item.id)
                }
              >
                <Cross />
              </IconAction>
            </li>
          )
        })}
      </ul>
    </div>
  )
}
