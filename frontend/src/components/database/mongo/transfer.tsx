"use client"

import { useEffect, useRef, useState } from "react"
import { ApiError, downloadUrl, errorMessage, type ApiErrorBody } from "@/lib/api"
import { bytes } from "@/lib/format"
import { notify } from "@/lib/toast"
import { Segments } from "@/components/deploy/settings/segments"
import { Field, FormFact, FormFacts, FormNote, OptionList, OptionRow } from "@/components/form"
import { Modal } from "@/components/modal"
import { Well } from "@/components/panel"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { grouped } from "@/components/database/data/view"
import { EngineMark } from "@/components/database/kit"
import { importFile, type MongoTarget } from "@/components/database/mongo/api"
import { draftLabel, isEmptyDraft, type QueryDraft } from "@/components/database/mongo/query"
import type { MongoImportReport } from "@/components/database/mongo/types"
import type { Mongo } from "@/components/database/mongo/use-mongo"

/* ------------------------------------------------------------------ export */

export type ExportFormat = "ndjson" | "json" | "csv"

const EXPORT_LIMITS = [100_000, 1_000_000] as const

export type ExportRequest = {
  target: MongoTarget
  format: ExportFormat
  relaxed: boolean
  limit: number
  /** The query whose matches are exported; absent = the whole collection. */
  draft?: QueryDraft
}

/** A refused request as the error every other read throws. */
async function refused(response: Response): Promise<ApiError> {
  const text = await response.text()
  try {
    const body = (JSON.parse(text) as ApiErrorBody).error
    return new ApiError(response.status, body.code, body.message)
  } catch {
    return new ApiError(response.status, "unknown", text.slice(0, 300) || response.statusText)
  }
}

/**
 * A collection, or what a query matches in it, saved as a file.
 *
 * The file is read by this page rather than handed to the browser as a link.
 * A link that is refused navigates the tab to a page of JSON, and one that
 * breaks off half-way leaves a short file that looks like a whole one. Read
 * here, a refusal is an error on the page, a download the server broke off is
 * said to have failed and saves nothing, and one the row limit cut is saved
 * with the cut said.
 */
export function useMongoExport() {
  const [running, setRunning] = useState<string | null>(null)
  const flight = useRef<AbortController | null>(null)
  useEffect(() => () => flight.current?.abort(), [])

  const run = async (request: ExportRequest) => {
    if (flight.current) return
    const { target, format, draft } = request
    const controller = new AbortController()
    flight.current = controller
    setRunning(target.collection)
    const name = target.collection
    const toast = notify.loading(`Exporting ${name}…`, {
      action: { label: "Cancel", onClick: () => controller.abort() },
    })
    try {
      const text = (value: string | undefined) => (value?.trim() ? value : undefined)
      const response = await fetch(
        downloadUrl(`/databases/${target.id}/mongo/export`, {
          database: target.database || undefined,
          collection: target.collection,
          filter: text(draft?.filter),
          projection: text(draft?.project),
          sort: text(draft?.sort),
          collation: text(draft?.collation),
          hint: text(draft?.hint),
          skip: text(draft?.skip),
          limit: request.limit,
          format,
          relaxed: request.relaxed && format !== "csv" ? 1 : undefined,
        }),
        { credentials: "include", signal: controller.signal },
      )
      if (!response.ok || !response.body) throw await refused(response)

      const parts: BlobPart[] = []
      const reader = response.body.getReader()
      let received = 0
      let said = 0
      for (;;) {
        // The server ends a failed export by breaking the connection: the
        // read throws, and nothing is saved.
        const { done, value } = await reader.read()
        if (done) break
        parts.push(value)
        received += value.byteLength
        if (received - said >= 1 << 20) {
          said = received
          notify.raw.loading(`Exporting ${name}… ${bytes(received)}`, { id: toast })
        }
      }

      const disposition = response.headers.get("Content-Disposition") ?? ""
      const filename = /filename="?([^";]+)"?/.exec(disposition)?.[1] ?? `${name}.${format}`
      const url = URL.createObjectURL(
        new Blob(parts, {
          type: response.headers.get("Content-Type") ?? "application/octet-stream",
        }),
      )
      const link = document.createElement("a")
      link.href = url
      link.download = filename
      link.click()
      URL.revokeObjectURL(url)

      notify.dismiss(toast)
      const truncated = response.headers.get("X-Export-Truncated")
      const total = Number(response.headers.get("X-Export-Total"))
      if (truncated === "true") {
        const more = request.limit < EXPORT_LIMITS[1]
        notify.warning(`${name} was cut at ${grouped(request.limit)} documents`, {
          description:
            total > 0
              ? `${grouped(total)} match. The file holds the first ${grouped(request.limit)}.`
              : `More match than the limit. The file holds the first ${grouped(request.limit)}.`,
          duration: 20_000,
          action: more
            ? {
                label: `Export up to ${grouped(EXPORT_LIMITS[1])}`,
                onClick: () => void run({ ...request, limit: EXPORT_LIMITS[1] }),
              }
            : undefined,
        })
      } else {
        notify.success(`Exported ${name}`, {
          description:
            truncated === "unknown"
              ? `${bytes(received)}. Whether more matched than the limit could not be told.`
              : bytes(received),
        })
      }
    } catch (err) {
      notify.dismiss(toast)
      if (controller.signal.aborted) notify.info(`The export of ${name} was cancelled`)
      else if (err instanceof ApiError) notify.error(`Could not export ${name}`, err)
      else {
        notify.error(`The export of ${name} broke off`, undefined, {
          description: "The server stopped sending before the end, so no file was saved.",
        })
      }
    } finally {
      flight.current = null
      setRunning(null)
    }
  }

  return { run, running }
}

/** What to export and how, before the file is asked for. */
export function ExportDialog({
  mongo,
  request,
  onOpenChange,
  onExport,
}: {
  mongo: Mongo
  /** The collection to export, with the query on screen when there is one; `null` closes it. */
  request: { target: MongoTarget; draft?: QueryDraft } | null
  onOpenChange: (open: boolean) => void
  onExport: (request: ExportRequest) => void
}) {
  const { engine } = mongo
  const [format, setFormat] = useState<ExportFormat>("ndjson")
  const [types, setTypes] = useState<"canonical" | "relaxed">("canonical")
  const [scope, setScope] = useState<"query" | "all">("query")
  if (!request) return null
  const queried = request.draft !== undefined && !isEmptyDraft(request.draft)
  const draft = queried && scope === "query" ? request.draft : undefined

  return (
    <Modal
      open
      onOpenChange={onOpenChange}
      size="md"
      title={`Export ${request.target.collection}`}
      description={`Save documents of ${request.target.collection} as a file`}
      footer={
        <>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button
            onClick={() => {
              onOpenChange(false)
              onExport({
                target: request.target,
                format,
                relaxed: types === "relaxed",
                limit: EXPORT_LIMITS[0],
                draft,
              })
            }}
          >
            Export
          </Button>
        </>
      }
    >
      <div className="space-y-5">
        <div className="flex min-w-0 items-center gap-3">
          <EngineMark engine={engine} size="sm" />
          <div className="min-w-0 space-y-0.5">
            <p className="truncate font-mono text-body font-medium">{request.target.collection}</p>
            <FormFacts>
              <FormFact label="Database" mono>
                {request.target.database}
              </FormFact>
            </FormFacts>
          </div>
        </div>

        {queried && (
          <Field label="Which documents">
            <Segments
              label="Which documents"
              value={scope}
              options={[
                { value: "query", label: "What the query matches" },
                { value: "all", label: "The whole collection" },
              ]}
              onChange={setScope}
            />
            {scope === "query" && request.draft && (
              <Well className="mt-2 max-h-24 text-hint leading-relaxed break-all whitespace-pre-wrap">
                {draftLabel(request.draft)}
              </Well>
            )}
          </Field>
        )}

        <Field
          label="Format"
          hint={
            format === "ndjson"
              ? "One document a line: what mongoexport writes, and what Import reads back."
              : format === "json"
                ? "One array of documents."
                : "A header of the top-level fields; nested values as Extended JSON. Types are lost."
          }
        >
          <Segments
            label="Format"
            value={format}
            options={[
              { value: "ndjson", label: "NDJSON" },
              { value: "json", label: "JSON" },
              { value: "csv", label: "CSV" },
            ]}
            onChange={setFormat}
          />
        </Field>

        {format !== "csv" && (
          <Field
            label="Types"
            hint={
              types === "canonical"
                ? "Every value keeps its type: a file that imports back as the same documents."
                : "Easier to read. An Int64 and an Int32 become the same bare number."
            }
          >
            <Segments
              label="Types"
              value={types}
              options={[
                { value: "canonical", label: "Canonical" },
                { value: "relaxed", label: "Relaxed" },
              ]}
              onChange={setTypes}
            />
          </Field>
        )}

        <FormNote>
          Up to {grouped(EXPORT_LIMITS[0])} documents. A longer result is said to be cut, with the
          way to ask for more.
        </FormNote>
      </div>
    </Modal>
  )
}

/* ------------------------------------------------------------------ import */

const IMPORT_FORMATS = ["ndjson", "json", "csv", "tsv"] as const
type ImportFormat = (typeof IMPORT_FORMATS)[number]

/** The format a file's name says it is. */
function formatOf(name: string): ImportFormat {
  const lower = name.toLowerCase()
  if (lower.endsWith(".ndjson") || lower.endsWith(".jsonl")) return "ndjson"
  if (lower.endsWith(".json")) return "json"
  if (lower.endsWith(".tsv") || lower.endsWith(".tab")) return "tsv"
  return "csv"
}

/** The start of a file a dry run reads: enough for the first documents, cheap to send. */
const PREVIEW_BYTES = 1 << 20

/**
 * A file into the collection: NDJSON or JSON (Extended JSON, types kept), or
 * CSV / TSV (every field a string).
 *
 * The file is looked at before anything is written — the fields found and the
 * first documents as they would be stored — and the command is then named for
 * what it does. Replacing the collection is a removal and is asked for only
 * of a role that may make one.
 */
export function ImportDialog({
  mongo,
  target,
  open,
  onOpenChange,
  onImported,
}: {
  mongo: Mongo
  target: MongoTarget
  open: boolean
  onOpenChange: (open: boolean) => void
  onImported: () => void
}) {
  const { engine, canDestroy } = mongo
  const [file, setFile] = useState<File | null>(null)
  const [format, setFormat] = useState<ImportFormat>("ndjson")
  const [replace, setReplace] = useState(false)
  const [skipBad, setSkipBad] = useState(false)
  const [preview, setPreview] = useState<MongoImportReport | null>(null)
  const [report, setReport] = useState<MongoImportReport | null>(null)
  const [refused, setRefused] = useState("")
  const [busy, setBusy] = useState<"reading" | "importing" | null>(null)

  const formats = IMPORT_FORMATS.filter((entry) =>
    engine.capabilities.importFormats.includes(entry),
  )

  const reset = () => {
    setFile(null)
    setPreview(null)
    setReport(null)
    setRefused("")
    setReplace(false)
    setSkipBad(false)
  }
  const close = (next: boolean) => {
    if (busy) return
    onOpenChange(next)
    if (!next) reset()
  }

  const look = async (chosen: File, as: ImportFormat) => {
    setPreview(null)
    setReport(null)
    setRefused("")
    setBusy("reading")
    try {
      setPreview(
        await importFile(target, chosen.slice(0, PREVIEW_BYTES), chosen.name, {
          format: as,
          mode: "insert",
          skipBadRows: true,
          dryRun: true,
        }),
      )
    } catch (err) {
      setRefused(errorMessage(err))
    } finally {
      setBusy(null)
    }
  }

  const run = async () => {
    if (!file) return
    setRefused("")
    setBusy("importing")
    try {
      const done = await importFile(target, file, file.name, {
        format,
        mode: replace ? "replace" : "insert",
        skipBadRows: skipBad,
        dryRun: false,
      })
      setReport(done)
      onImported()
    } catch (err) {
      setRefused(errorMessage(err))
      // An insert that failed part-way may have left documents in.
      onImported()
    } finally {
      setBusy(null)
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={close}
      size="lg"
      title={`Import into ${target.collection}`}
      description={`Add the documents of a file to ${target.collection}`}
      footer={
        report ? (
          <Button onClick={() => close(false)}>Done</Button>
        ) : (
          <>
            {busy === "importing" && (
              <p className="mr-auto text-hint text-muted-foreground">
                The request stays open until the last document is in.
              </p>
            )}
            <Button variant="outline" disabled={busy !== null} onClick={() => close(false)}>
              Cancel
            </Button>
            <Button
              variant={replace ? "destructive" : "default"}
              pending={busy === "importing"}
              disabled={!file || !preview || busy !== null}
              onClick={() => void run()}
            >
              {replace ? "Replace the collection" : "Import"}
            </Button>
          </>
        )
      }
    >
      <div className="space-y-5">
        <div className="flex min-w-0 items-center gap-3">
          <EngineMark engine={engine} size="sm" />
          <div className="min-w-0 space-y-0.5">
            <p className="truncate font-mono text-body font-medium">{target.collection}</p>
            <FormFacts>
              <FormFact label="Database" mono>
                {target.database}
              </FormFact>
            </FormFacts>
          </div>
        </div>

        {report ? (
          <ImportOutcome report={report} />
        ) : (
          <>
            <Field
              label="File"
              htmlFor="mongo-import-file"
              hint="NDJSON or JSON keeps every type (Extended JSON). CSV and TSV fields become strings."
            >
              <Input
                id="mongo-import-file"
                type="file"
                accept=".json,.ndjson,.jsonl,.csv,.tsv,.tab,application/json,text/csv"
                disabled={busy !== null}
                onChange={(event) => {
                  const chosen = event.target.files?.[0] ?? null
                  setFile(chosen)
                  if (!chosen) {
                    setPreview(null)
                    return
                  }
                  const guessed = formatOf(chosen.name)
                  const as = formats.includes(guessed) ? guessed : (formats[0] ?? "ndjson")
                  setFormat(as)
                  void look(chosen, as)
                }}
              />
            </Field>

            {file && (
              <Field label="Read it as">
                <Segments
                  label="File format"
                  value={format}
                  disabled={busy !== null}
                  options={formats.map((entry) => ({
                    value: entry,
                    label: entry.toUpperCase(),
                  }))}
                  onChange={(next) => {
                    setFormat(next)
                    void look(file, next)
                  }}
                />
              </Field>
            )}

            {busy === "reading" && (
              <p className="text-hint text-muted-foreground">Reading the start of the file…</p>
            )}
            {refused && (
              <Notice tone="danger" title="The file was not imported">
                {refused}
              </Notice>
            )}

            {preview && (
              <>
                <div className="space-y-1.5">
                  <p className="eyebrow">
                    The first {grouped(preview.preview?.rows.length ?? 0)} of{" "}
                    {file && file.size > PREVIEW_BYTES ? "the file" : grouped(preview.rowsRead)}{" "}
                    {file && file.size > PREVIEW_BYTES ? "" : "documents"}
                  </p>
                  <ImportPreview report={preview} />
                </div>
                {preview.warnings.map((warning) => (
                  <FormNote key={warning} tone="warning">
                    {warning}
                  </FormNote>
                ))}
                <OptionList>
                  <OptionRow
                    title="Leave out a document the collection refuses, and carry on"
                    checked={skipBad}
                    onCheckedChange={setSkipBad}
                  />
                  {canDestroy && engine.can("importReplace") && (
                    <OptionRow
                      tone={replace ? "danger" : "default"}
                      title="Replace everything the collection holds with this file"
                      hint="Its indexes and its validation rule are kept. If a document breaks them, the collection is left as it was."
                      checked={replace}
                      onCheckedChange={setReplace}
                    />
                  )}
                </OptionList>
                {!replace && (
                  <FormNote>
                    Documents are added in batches with no transaction around them: if one is
                    refused, the ones before it are already in.
                  </FormNote>
                )}
              </>
            )}
          </>
        )}
      </div>
    </Modal>
  )
}

function ImportPreview({ report }: { report: MongoImportReport }) {
  const preview = report.preview
  if (!preview || preview.rows.length === 0) {
    return <p className="text-hint text-muted-foreground">The file holds no document.</p>
  }
  return (
    // A frame because the preview scrolls both ways inside the dialog.
    <div className="max-h-56 overflow-auto rounded-lg border border-hairline">
      <table className="w-max min-w-full border-collapse font-mono text-hint">
        <thead>
          <tr className="sticky top-0 bg-surface-header text-left text-muted-foreground">
            {preview.columns.map((column) => (
              <th key={column} scope="col" className="px-2 py-1.5 font-medium whitespace-nowrap">
                {column}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {preview.rows.map((row, index) => (
            <tr key={index} className="border-t border-hairline">
              {row.map((cell, at) => (
                <td key={at} className="max-w-64 truncate px-2 py-1 whitespace-nowrap">
                  {cell === null ? (
                    <span className="text-muted-foreground/60">not set</span>
                  ) : cell === "" ? (
                    <span className="text-muted-foreground/60 italic">&quot;&quot;</span>
                  ) : (
                    cell
                  )}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function ImportOutcome({ report }: { report: MongoImportReport }) {
  const partial = report.skipped > 0 || report.errors.length > 0
  return (
    <div className="space-y-3">
      <Notice
        tone={partial ? "warning" : "success"}
        title={
          report.mode === "replace"
            ? `The collection now holds the file's ${grouped(report.inserted)} documents`
            : `${grouped(report.inserted)} ${report.inserted === 1 ? "document" : "documents"} imported`
        }
      >
        {report.skipped > 0
          ? `${grouped(report.skipped)} were left out.`
          : `${grouped(report.rowsRead)} read from the file.`}
      </Notice>
      {report.errors.length > 0 && (
        <Well className="max-h-40 space-y-1 overflow-auto text-hint leading-relaxed whitespace-pre-wrap">
          {report.errors.map((error) => (
            <p key={`${error.row}:${error.line}`}>
              <span className="text-muted-foreground">Line {grouped(error.line)}: </span>
              {error.message}
            </p>
          ))}
          {report.errorsTruncated && <p className="text-muted-foreground">…and more.</p>}
        </Well>
      )}
      {report.warnings.map((warning) => (
        <FormNote key={warning} tone="warning">
          {warning}
        </FormNote>
      ))}
    </div>
  )
}
