"use client"

import { useId, useMemo, useState } from "react"
import { CloudUpload, FileText } from "@/components/icons"
import { notify } from "@/lib/toast"
import { plural } from "@/lib/format"
import { post } from "@/lib/api"
import { cn } from "@/lib/utils"
import type { DbImportResult, DbTableDetail } from "@/lib/types"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { Well } from "@/components/panel"
import { FilterChip } from "@/components/tabs"
import type { useConfirm } from "@/components/confirm-dialog"
import { Modal } from "@/components/modal"
import {
  Field,
  FormFact,
  FormFacts,
  FormNote,
  FormSection,
  OptionList,
  OptionRow,
} from "@/components/form"

type ConfirmFn = ReturnType<typeof useConfirm>["confirm"]
type Format = "csv" | "json"

/**
 * Load CSV or JSON into a table.
 *
 * The whole load is one transaction on the server, so the two switches here
 * mean what they say: "stop at the first bad row" aborts everything, and
 * leaving it off still commits or rolls back as a unit — a row being skipped
 * never means a file was half applied. Appending is a plain confirmation;
 * replacing the contents empties the table first and so asks for the table's
 * name to be typed, exactly as a TRUNCATE does.
 *
 * What is about to be loaded is counted before it is sent. A file dropped in
 * the wrong format, or a CSV whose header does not match the table, used to
 * be found out by the server one round trip later; the count under the data
 * says "2,314 rows, 14 columns" or "not valid JSON" while the button is still
 * unpressed.
 */
export function ImportDialog({
  open,
  onOpenChange,
  connId,
  schema,
  table,
  detail,
  confirm,
  documentStore,
  onDone,
}: {
  open: boolean
  onOpenChange: (o: boolean) => void
  connId: number
  schema: string
  table: string
  detail?: DbTableDetail | null
  confirm: ConfirmFn
  /** A document store cannot promise all-or-nothing, and says so. */
  documentStore?: boolean
  onDone: () => void
}) {
  const id = useId()
  const [format, setFormat] = useState<Format>(documentStore ? "json" : "csv")
  const [data, setData] = useState("")
  const [fileName, setFileName] = useState<string | null>(null)
  const [hasHeader, setHasHeader] = useState(true)
  const [truncate, setTruncate] = useState(false)
  const [stopOnError, setStopOnError] = useState(false)
  const [nullAs, setNullAs] = useState("")
  const [dragging, setDragging] = useState(false)
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState<DbImportResult | null>(null)

  const readFile = (file: File) => {
    const reader = new FileReader()
    reader.onload = () => {
      setData(String(reader.result ?? ""))
      setFileName(file.name)
      setResult(null)
      if (file.name.endsWith(".json") || file.name.endsWith(".jsonl")) setFormat("json")
      if (file.name.endsWith(".csv")) setFormat("csv")
    }
    reader.readAsText(file)
  }

  const summary = useMemo(() => summarise(data, format, hasHeader), [data, format, hasHeader])

  const run = async (confirmText?: string) => {
    setBusy(true)
    setResult(null)
    try {
      const res = await post<DbImportResult>(
        `/databases/${connId}/import`,
        { schema, table, format, data, columns: [], hasHeader, truncate, stopOnError, nullAs },
        { confirm: confirmText },
      )
      setResult(res)
      notify.success(`Imported ${plural(res.inserted, "row")}`, {
        description: res.failed ? `${plural(res.failed, "row")} failed` : undefined,
      })
      onDone()
    } catch (err) {
      notify.error("Import failed", err)
      throw err
    } finally {
      setBusy(false)
    }
  }

  const submit = () => {
    if (truncate) {
      confirm({
        title: "Replace table contents",
        phrase: table,
        confirmLabel: "Replace",
        description: (
          <p>
            Empties <b>{table}</b> before loading. Everything currently in the table is lost, and
            this cannot be undone.
          </p>
        ),
        action: (c) => run(c),
      })
      return
    }
    run().catch(() => undefined)
  }

  const ready = data.trim() !== "" && !summary?.error

  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      size="lg"
      title="Import data"
      description={`Loads rows into ${table} from a CSV or JSON file, in one transaction.`}
      footer={
        <>
          {summary && !summary.error && (
            <span className="numeric mr-auto text-hint text-muted-foreground">
              {plural(summary.rows, documentStore ? "document" : "row")}
              {summary.columns ? ` · ${plural(summary.columns, "column")}` : ""}
            </span>
          )}
          <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={busy}>
            {result ? "Close" : "Cancel"}
          </Button>
          <Button onClick={submit} disabled={!ready || busy} pending={busy}>
            <CloudUpload />
            {truncate ? "Replace and import" : "Import"}
          </Button>
        </>
      }
    >
      <div className="grid gap-5">
        <FormFacts>
          <FormFact label="Into" mono>
            {schema ? `${schema}.${table}` : table}
          </FormFact>
          {detail && <FormFact label="Columns">{detail.columns.length}</FormFact>}
        </FormFacts>

        <FormSection
          title="Data"
          actions={
            <>
              <FilterChip selected={format === "csv"} onClick={() => setFormat("csv")}>
                CSV
              </FilterChip>
              <FilterChip selected={format === "json"} onClick={() => setFormat("json")}>
                JSON
              </FilterChip>
            </>
          }
        >
          <label
            onDragOver={(e) => {
              e.preventDefault()
              setDragging(true)
            }}
            onDragLeave={() => setDragging(false)}
            onDrop={(e) => {
              e.preventDefault()
              setDragging(false)
              const file = e.dataTransfer.files?.[0]
              if (file) readFile(file)
            }}
            className={cn(
              "flex cursor-pointer items-center gap-3 rounded-lg border border-dashed px-4 py-3 transition-colors focus-within:border-border-strong hover:border-border-strong",
              dragging ? "border-border-strong bg-accent" : "border-border",
            )}
          >
            <FileText className="size-4 shrink-0 text-muted-foreground" />
            <span className="min-w-0 flex-1">
              <span className="block truncate text-body font-medium">
                {fileName ?? "Drop a file here, or choose one"}
              </span>
              <span className="block text-hint text-muted-foreground">
                {fileName
                  ? "Choose another to replace it"
                  : ".csv, .json or .jsonl — or paste the contents below"}
              </span>
            </span>
            <input
              type="file"
              accept=".csv,.json,.jsonl,.txt,text/csv,application/json"
              className="sr-only"
              onChange={(e) => e.target.files?.[0] && readFile(e.target.files[0])}
            />
          </label>
          <Field
            label={fileName ? "Contents" : "Paste"}
            htmlFor={`${id}-data`}
            error={summary?.error}
            hint="Sent in one request, so this tops out around 4 MB. A larger load belongs in the engine's own bulk loader."
          >
            <Textarea
              id={`${id}-data`}
              value={data}
              onChange={(e) => {
                setData(e.target.value)
                setResult(null)
              }}
              className="max-h-64 min-h-32 font-mono text-xs"
              placeholder={
                format === "csv"
                  ? detail?.columns.map((c) => c.name).join(",") || "id,name"
                  : '[{"name": "…"}]'
              }
              spellCheck={false}
            />
          </Field>
          {format === "json" && documentStore && (
            <FormNote>
              JSON may be an array or one document per line, which is what mongoexport writes.
            </FormNote>
          )}
        </FormSection>

        <FormSection title="Options">
          <OptionList>
            {format === "csv" && (
              <OptionRow
                title="First row is a header"
                hint="Column names are read from it and matched to the table's columns."
                checked={hasHeader}
                onCheckedChange={setHasHeader}
              />
            )}
            <OptionRow
              title="Stop at the first bad row"
              hint="Aborts the whole load on the first row the engine refuses. Off, the bad rows are skipped and named in the result."
              checked={stopOnError}
              onCheckedChange={setStopOnError}
            />
            <OptionRow
              title="Replace existing contents"
              hint="Empties the table first. You will be asked to type its name."
              checked={truncate}
              onCheckedChange={setTruncate}
              tone="danger"
            />
          </OptionList>
          {format === "csv" && (
            <Field
              label="NULL is written as"
              htmlFor={`${id}-null`}
              hint="Cells equal to this become NULL. Empty means an empty cell is NULL."
            >
              <Input
                id={`${id}-null`}
                value={nullAs}
                onChange={(e) => setNullAs(e.target.value)}
                className="font-mono sm:max-w-xs"
                placeholder="(empty string)"
              />
            </Field>
          )}
          {documentStore && (
            <FormNote>
              A standalone MongoDB server has no transaction to wrap this in, so a failure partway
              leaves what already landed in place. The result says exactly how much that was.
            </FormNote>
          )}
        </FormSection>

        {result && (
          <FormSection title="Result">
            <p className="text-body">
              <span className="numeric font-medium">{result.inserted.toLocaleString()}</span>{" "}
              inserted
              {result.failed > 0 && (
                <>
                  ,{" "}
                  <span className="numeric font-medium text-destructive">
                    {result.failed.toLocaleString()}
                  </span>{" "}
                  failed
                </>
              )}
            </p>
            {result.errors.length > 0 && (
              <Well className="max-h-40 text-hint">
                {result.errors.join("\n")}
                {result.errorsTruncated && "\n… more not shown"}
              </Well>
            )}
          </FormSection>
        )}
      </div>
    </Modal>
  )
}

/**
 * What the pasted text amounts to, counted the cheap way: lines for CSV, a
 * parse for JSON. The CSV column count splits the first line on commas and is
 * labelled as a count rather than a parse — a quoted comma would fool it, and
 * the server does the real parsing.
 */
function summarise(
  data: string,
  format: Format,
  hasHeader: boolean,
): { rows: number; columns?: number; error?: string } | null {
  const text = data.trim()
  if (!text) return null
  if (format === "csv") {
    const lines = text.split(/\r?\n/).filter((l) => l.trim() !== "")
    const columns = lines[0]?.split(",").length ?? 0
    return { rows: Math.max(0, lines.length - (hasHeader ? 1 : 0)), columns }
  }
  try {
    const parsed: unknown = JSON.parse(text)
    if (Array.isArray(parsed)) {
      const first = parsed.find((v) => v && typeof v === "object")
      return { rows: parsed.length, columns: first ? Object.keys(first as object).length : 0 }
    }
    if (parsed && typeof parsed === "object")
      return { rows: 1, columns: Object.keys(parsed).length }
    return { rows: 0, error: "JSON must be an array of objects, or one object per line." }
  } catch {
    // Not one document — perhaps one per line, which is what mongoexport
    // writes and what the server also accepts.
    const lines = text.split(/\r?\n/).filter((l) => l.trim() !== "")
    try {
      for (const l of lines) JSON.parse(l)
      return { rows: lines.length }
    } catch {
      return { rows: 0, error: "Not valid JSON — neither one array nor one object per line." }
    }
  }
}
