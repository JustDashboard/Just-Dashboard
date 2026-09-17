"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { Cross } from "@/components/icons"
import { API_BASE, mutationHeaders } from "@/lib/api"
import { bytes, plural } from "@/lib/format"
import { cn } from "@/lib/utils"
import { Button } from "@/components/ui/button"
import { IconAction } from "@/components/icon-action"
import { Meter } from "@/components/meter"

export type UploadStatus = "queued" | "uploading" | "done" | "failed" | "cancelled"

export type UploadItem = {
  id: number
  name: string
  dir: string
  size: number
  sent: number
  status: UploadStatus
  error?: string
}

export type UploadJob = { file: File; dir: string; name: string; overwrite: boolean }

/** A file on its way in, with where it sits relative to the drop. */
export type Incoming = { file: File; rel: string }

const PARALLEL = 3

/**
 * The upload queue.
 *
 * One request per file rather than one form for all of them: a single
 * multipart body carrying forty files is one progress bar that says nothing,
 * one failure that takes the other thirty-nine with it, and one request whose
 * size limit is the *total* rather than the per-file cap the server documents.
 * `XMLHttpRequest` rather than `fetch` because it is still the only way a
 * browser reports upload progress.
 *
 * Three transfers run at once. More does not make a home connection faster;
 * fewer leaves a folder of small files waiting on round trips.
 */
export function useUploads(onSettled: (dir: string) => void) {
  const [items, setItems] = useState<UploadItem[]>([])
  const queue = useRef<{ id: number; job: UploadJob }[]>([])
  const running = useRef(new Map<number, XMLHttpRequest>())
  const nextId = useRef(1)
  const settled = useRef(onSettled)
  useEffect(() => {
    settled.current = onSettled
  }, [onSettled])

  const update = useCallback((id: number, patch: Partial<UploadItem>) => {
    setItems((prev) => prev.map((item) => (item.id === id ? { ...item, ...patch } : item)))
  }, [])

  // A finished transfer starts the next one, which means the runner calls
  // itself from a callback registered before it existed; the ref is how it
  // reaches its own current self.
  const pumpRef = useRef<() => void>(() => undefined)

  const pump = useCallback(() => {
    while (running.current.size < PARALLEL && queue.current.length > 0) {
      const { id, job } = queue.current.shift()!
      const xhr = new XMLHttpRequest()
      running.current.set(id, xhr)
      const form = new FormData()
      form.append("file", job.file, job.name)
      xhr.open(
        "POST",
        `${API_BASE}/files/upload?path=${encodeURIComponent(job.dir)}&overwrite=${job.overwrite}`,
      )
      xhr.withCredentials = true
      for (const [key, value] of Object.entries(mutationHeaders())) xhr.setRequestHeader(key, value)
      xhr.upload.onprogress = (event) => update(id, { sent: event.loaded })
      const finish = (patch: Partial<UploadItem>) => {
        running.current.delete(id)
        update(id, patch)
        settled.current(job.dir)
        pumpRef.current()
      }
      xhr.onload = () => {
        if (xhr.status >= 200 && xhr.status < 300) finish({ status: "done", sent: job.file.size })
        else finish({ status: "failed", error: failureOf(xhr) })
      }
      xhr.onerror = () => finish({ status: "failed", error: "The connection dropped." })
      xhr.onabort = () => finish({ status: "cancelled" })
      update(id, { status: "uploading" })
      xhr.send(form)
    }
  }, [update])

  useEffect(() => {
    pumpRef.current = pump
  }, [pump])

  const enqueue = useCallback(
    (jobs: UploadJob[]) => {
      if (jobs.length === 0) return
      const created = jobs.map((job) => ({ id: nextId.current++, job }))
      setItems((prev) => [
        ...prev,
        ...created.map(({ id, job }) => ({
          id,
          name: job.name,
          dir: job.dir,
          size: job.file.size,
          sent: 0,
          status: "queued" as const,
        })),
      ])
      queue.current.push(...created)
      pump()
    },
    [pump],
  )

  const cancel = useCallback(
    (id: number) => {
      const live = running.current.get(id)
      if (live) {
        live.abort()
        return
      }
      queue.current = queue.current.filter((entry) => entry.id !== id)
      update(id, { status: "cancelled" })
    },
    [update],
  )

  const dismiss = useCallback((id: number) => {
    setItems((prev) => prev.filter((item) => item.id !== id))
  }, [])

  const clear = useCallback(() => {
    setItems((prev) =>
      prev.filter((item) => item.status === "queued" || item.status === "uploading"),
    )
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
    const live = running.current
    return () => {
      for (const xhr of live.values()) xhr.abort()
    }
  }, [])

  return { items, enqueue, cancel, dismiss, clear }
}

function failureOf(xhr: XMLHttpRequest): string {
  try {
    const body = JSON.parse(xhr.responseText) as { error?: { message?: string; code?: string } }
    if (body.error?.code === "already_exists") return "A file with this name already exists."
    if (body.error?.message) return body.error.message
  } catch {
    // Not JSON; the status line is what there is.
  }
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
 * Each row is a name, a bar and a figure; the strip's own line says how many
 * are still going. It is drawn in the workbench rather than as toasts because
 * an upload is something you watch, and a toast is something that goes away.
 */
export function UploadStrip({
  items,
  currentDir,
  onCancel,
  onDismiss,
  onClear,
  className,
}: {
  items: UploadItem[]
  currentDir: string
  onCancel: (id: number) => void
  onDismiss: (id: number) => void
  onClear: () => void
  className?: string
}) {
  if (items.length === 0) return null
  const live = items.filter((item) => item.status === "queued" || item.status === "uploading")
  const failed = items.filter((item) => item.status === "failed")
  const total = live.reduce((sum, item) => sum + item.size, 0)
  const sent = live.reduce((sum, item) => sum + item.sent, 0)

  return (
    <div className={cn("shrink-0 border-t border-hairline bg-surface-header", className)}>
      <div className="flex items-center gap-3 px-3 py-1.5 text-hint text-muted-foreground">
        <span className="min-w-0 flex-1 truncate">
          {live.length > 0
            ? `Uploading ${plural(live.length, "file")} · ${bytes(sent)} of ${bytes(total)}`
            : failed.length > 0
              ? `${plural(failed.length, "upload")} failed`
              : "Uploads finished"}
        </span>
        {live.length > 0 && (
          <Meter
            value={total > 0 ? (sent / total) * 100 : 0}
            size="thin"
            label="Upload progress"
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
        {items.map((item) => (
          <li key={item.id} className="flex items-center gap-3 px-3 py-1 text-xs">
            <span className="min-w-0 flex-1 truncate" title={`${item.dir}/${item.name}`}>
              {item.name}
              {item.dir !== currentDir && (
                <span className="ml-1.5 font-mono text-hint text-muted-foreground">
                  → {item.dir}
                </span>
              )}
            </span>
            {item.status === "failed" ? (
              <span className="truncate text-hint text-destructive">{item.error}</span>
            ) : item.status === "cancelled" ? (
              <span className="text-hint text-muted-foreground">Cancelled</span>
            ) : item.status === "done" ? (
              <span className="text-hint text-success">Done</span>
            ) : (
              <>
                <Meter
                  value={item.size > 0 ? (item.sent / item.size) * 100 : 0}
                  size="thin"
                  label={`${item.name} progress`}
                  className="w-24"
                />
                <span className="numeric w-24 shrink-0 text-right text-hint text-muted-foreground">
                  {item.status === "queued"
                    ? "Waiting"
                    : `${bytes(item.sent)} / ${bytes(item.size)}`}
                </span>
              </>
            )}
            <IconAction
              label={item.status === "queued" || item.status === "uploading" ? "Cancel" : "Dismiss"}
              className="size-6"
              onClick={() =>
                item.status === "queued" || item.status === "uploading"
                  ? onCancel(item.id)
                  : onDismiss(item.id)
              }
            >
              <Cross />
            </IconAction>
          </li>
        ))}
      </ul>
    </div>
  )
}
