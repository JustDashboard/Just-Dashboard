"use client"

import { useEffect, useId, useRef, useState } from "react"
import { CloudUpload } from "@/components/icons"
import { ApiError, downloadUrl, errorMessage, mutationHeaders } from "@/lib/api"
import type { ApiErrorBody } from "@/lib/api"
import { bytes } from "@/lib/format"
import { Field, FormFact, FormNote } from "@/components/form"
import { Meter } from "@/components/meter"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { uploadNameProblem } from "@/components/database/ops/backups-model"
import type { DbBackupFile } from "@/components/database/ops/backups-types"
import { TaskDialog, databaseSubject } from "@/components/database/ops/settings-dialog"
import { useDatabase } from "@/components/database/shell/database-context"

const NOTE_BYTES = 500

/**
 * One file sent as the route's `file` part, with how much of it has gone.
 *
 * `fetch` cannot say how far an upload has got, and a dump can be gigabytes:
 * a dialog that shows nothing for a minute is one the reader closes. The
 * request carries the same mutation header and session every other one does,
 * and a refusal is the same `ApiError`.
 */
function sendDump(
  url: string,
  file: File,
  onProgress: (sent: number) => void,
  signal: AbortSignal,
): Promise<DbBackupFile> {
  return new Promise((resolve, reject) => {
    const request = new XMLHttpRequest()
    request.open("POST", url)
    request.withCredentials = true
    for (const [name, value] of Object.entries(mutationHeaders())) {
      request.setRequestHeader(name, value)
    }
    request.upload.onprogress = (event) => onProgress(event.loaded)
    request.onerror = () => reject(new Error("The upload did not reach the server."))
    request.onabort = () => reject(new DOMException("The upload was stopped.", "AbortError"))
    request.onload = () => {
      let parsed: unknown
      try {
        parsed = JSON.parse(request.responseText)
      } catch {
        parsed = undefined
      }
      if (request.status >= 200 && request.status < 300) {
        resolve(parsed as DbBackupFile)
        return
      }
      const error = (parsed as ApiErrorBody | undefined)?.error
      reject(
        new ApiError(
          request.status,
          error?.code ?? "unknown",
          error?.message ?? (request.statusText || "The server refused the upload."),
        ),
      )
    }
    signal.addEventListener("abort", () => request.abort())
    const body = new FormData()
    body.append("file", file)
    request.send(body)
  })
}

/**
 * Add a dump made somewhere else to the ones kept here, so it can be
 * restored from this page. Nothing is restored by the upload itself.
 */
export function UploadDump({
  taken,
  onUploaded,
  onClose,
}: {
  /** The names already kept: one of them is refused. */
  taken: string[]
  onUploaded: (file: DbBackupFile) => void
  onClose: () => void
}) {
  const { id, conn, engine } = useDatabase()
  const fileField = useId()
  const nameField = useId()
  const noteField = useId()
  const [file, setFile] = useState<File>()
  const [name, setName] = useState("")
  const [note, setNote] = useState("")
  const [sent, setSent] = useState(0)
  const [busy, setBusy] = useState(false)
  const [refusal, setRefusal] = useState<string>()
  const abort = useRef<AbortController | null>(null)
  // Leaving the page mid-upload ends the request rather than orphaning it.
  useEffect(() => () => abort.current?.abort(), [])

  const problem = file ? uploadNameProblem(name, taken) : undefined
  const noteBytes = new TextEncoder().encode(note).length

  const run = async () => {
    if (!file) return
    setBusy(true)
    setRefusal(undefined)
    setSent(0)
    const controller = new AbortController()
    abort.current = controller
    try {
      const kept = await sendDump(
        downloadUrl(`/databases/${id}/backups/upload`, {
          name: name.trim(),
          note: note.trim() || undefined,
        }),
        file,
        setSent,
        controller.signal,
      )
      onUploaded(kept)
    } catch (err) {
      if (!(err instanceof DOMException && err.name === "AbortError")) {
        setRefusal(errorMessage(err))
      }
      setBusy(false)
    }
  }

  return (
    <TaskDialog
      title="Upload a dump"
      description={`Add a dump made elsewhere to the dumps kept of ${conn.name}`}
      subject={databaseSubject(conn, engine, <FormFact label="Engine">{engine.label}</FormFact>)}
      dirty={Boolean(file) || note.trim() !== ""}
      busy={busy}
      refusal={refusal}
      note="It is kept with the other dumps. Nothing is restored."
      command="Upload"
      commandIcon={CloudUpload}
      disabled={!file || Boolean(problem) || noteBytes > NOTE_BYTES}
      secondary={
        busy && (
          <Button variant="outline" onClick={() => abort.current?.abort()}>
            Stop
          </Button>
        )
      }
      onRun={() => void run()}
      onClose={onClose}
    >
      <Field
        label="The dump"
        htmlFor={fileField}
        hint="A dump this dashboard wrote, or one from the engine's own tool: plain or gzipped SQL, an archive, a database file."
      >
        <Input
          id={fileField}
          type="file"
          disabled={busy}
          onChange={(event) => {
            const chosen = event.target.files?.[0]
            setFile(chosen)
            setRefusal(undefined)
            // The name it is kept under starts as the file's own.
            if (chosen) setName(chosen.name.replace(/\s+/g, "-"))
          }}
          className="h-auto py-1.5 file:mr-3 file:text-xs file:font-medium"
        />
      </Field>
      {file && (
        <>
          <Field
            label="Kept as"
            htmlFor={nameField}
            hint={`${bytes(file.size)}. The name it is listed, downloaded and restored under.`}
            error={problem}
          >
            <Input
              id={nameField}
              value={name}
              disabled={busy}
              onChange={(event) => setName(event.target.value)}
              autoComplete="off"
              spellCheck={false}
              className="font-mono"
            />
          </Field>
          <Field
            label="Note"
            htmlFor={noteField}
            hint="Where it came from."
            error={noteBytes > NOTE_BYTES ? `At most ${NOTE_BYTES} bytes.` : undefined}
          >
            <Input
              id={noteField}
              value={note}
              disabled={busy}
              onChange={(event) => setNote(event.target.value)}
              placeholder="From the old server, before the move"
              autoComplete="off"
            />
          </Field>
        </>
      )}
      {busy && file && (
        <div role="status" className="space-y-1.5">
          <Meter
            value={file.size > 0 ? (sent / file.size) * 100 : 0}
            label="Uploaded"
            size="thin"
          />
          <FormNote className="numeric">
            {bytes(Math.min(sent, file.size))} of {bytes(file.size)} sent
          </FormNote>
        </div>
      )}
    </TaskDialog>
  )
}
