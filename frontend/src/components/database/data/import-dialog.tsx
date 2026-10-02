"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import { CloudUpload } from "@/components/icons"
import { postForm } from "@/lib/api"
import { bytes } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { DbImportFormat } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { Segments } from "@/components/deploy/settings/segments"
import {
  Field,
  FieldRow,
  FormFact,
  FormFacts,
  FormNote,
  FormSection,
  OptionList,
  OptionRow,
  Statement,
} from "@/components/form"
import { Meter } from "@/components/meter"
import { Modal } from "@/components/modal"
import { ErrorState, Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { EngineMark } from "@/components/database/kit"
import { useDatabase } from "@/components/database/shell/database-context"
import {
  newColumns,
  tableNameFrom,
  uploadProgress,
  upsertKeys,
  type ColumnOverride,
} from "@/components/database/data/import"
import type {
  DbImportMode,
  DbImportOptions,
  DbImportReport,
  DbTableDetail,
} from "@/components/database/data/types"
import { uploadForm } from "@/components/database/data/upload"
import { grouped } from "@/components/database/data/view"

/** How much of a file a dry run is sent: the server reads its first thousand rows and no more. */
const SAMPLE_BYTES = 1 << 20

const FORMAT_LABEL: Record<DbImportFormat, string> = {
  csv: "CSV",
  tsv: "TSV",
  json: "JSON",
  ndjson: "JSON Lines",
}

/** The picker's entry for "leave this column out": a name no column can have. */
const SKIP = "\u0000skip"

/** Where the rows go: a table that is there, or one the import makes. */
export type ImportTarget =
  | { kind: "table"; detail: DbTableDetail }
  | {
      kind: "new"
      schema: string
      /** The names the schema already uses: a new table cannot take one. */
      taken: readonly string[]
    }

type Settings = {
  /** Empty = let the server tell by the file's name. */
  format: DbImportFormat | ""
  header: boolean
  delimiter: string
  encoding: "utf-8" | "utf-16" | "latin-1"
  nullToken: string
  mode: DbImportMode
  /** Which of the table's keys an upsert matches by: its place in `upsertKeys`. */
  key: number
  skipBadRows: boolean
}

const START: Settings = {
  format: "",
  header: true,
  delimiter: "",
  encoding: "utf-8",
  nullToken: "",
  mode: "insert",
  key: 0,
  skipBadRows: false,
}

type Run = {
  report?: DbImportReport
  error?: Error
  pending: boolean
  /** Bytes of the file that have left the browser, of how many. */
  sent: number
  total: number
}

const IDLE: Run = { pending: false, sent: 0, total: 0 }

/**
 * A file's rows written into a table, looked at before they are.
 *
 * Choosing a file reads only its first megabyte and asks the server what it
 * would do with it: which of the file's columns goes to which of the table's,
 * what each looks like, the first rows exactly as they would be written, and
 * the statement a row is written with. Nothing is written by that, and every
 * change to the mapping or the options asks again. Only Import sends the
 * whole file — once, in one transaction where the engine has them — and says
 * how much of it has left the browser while it does.
 *
 * The table can be one the import makes: the server reads what each column of
 * the file holds and proposes the table, the reader names it, renames or
 * retypes a column and says which are its key, and the statement that would
 * create it is shown before anything runs.
 *
 * Replace empties the table first, so it is offered only to a role that may
 * destroy and is confirmed against the table by name.
 */
export function ImportDialog({
  open,
  onOpenChange,
  target,
  onImported,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  target: ImportTarget
  /** Rows were written into the table named: what is on screen is stale. */
  onImported: (table: string) => void
}) {
  const { id, engine } = useDatabase()
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const picker = useRef<HTMLInputElement>(null)
  const [file, setFile] = useState<File | null>(null)
  const [settings, setSettings] = useState<Settings>(START)
  // Source column → table column, as the reader left it. Empty until they touch
  // one: the server's own matching stands until then.
  const [mapping, setMapping] = useState<Record<string, string> | null>(null)
  // A new table: its name, and what the reader said of its columns.
  const [name, setName] = useState("")
  const [overrides, setOverrides] = useState<Record<string, ColumnOverride>>({})
  const [plan, setPlan] = useState<{ report?: DbImportReport; error?: Error; pending: boolean }>({
    pending: false,
  })
  const [run, setRun] = useState<Run>(IDLE)
  const flight = useRef<AbortController | null>(null)

  const detail = target.kind === "table" ? target.detail : null
  const schema = target.kind === "table" ? target.detail.schema : target.schema
  const table = detail ? detail.name : name.trim()
  const taken = target.kind === "new" && target.taken.includes(table)
  const named = table !== "" && !taken

  const formats = engine.capabilities.importFormats
  const keys = useMemo(() => (detail ? upsertKeys(detail) : []), [detail])
  const modes = useMemo(() => {
    const list: { value: DbImportMode; label: string }[] = [{ value: "insert", label: "Add rows" }]
    if (!detail) return list
    if (engine.can("importUpsert") && keys.length > 0) {
      list.push({ value: "upsert", label: "Add or update" })
    }
    if (engine.can("importReplace") && can("destructive")) {
      list.push({ value: "replace", label: "Replace all rows" })
    }
    return list
  }, [engine, detail, keys.length, can])
  const matchedBy = keys[settings.key] ?? keys[0]

  const report = plan.report
  const columns = useMemo(() => report?.columns ?? [], [report])
  const made = useMemo(
    () =>
      newColumns(
        columns.map((column) => ({
          source: column.source,
          target: mapping ? (mapping[column.source] ?? "") : column.target,
        })),
        overrides,
      ),
    [columns, mapping, overrides],
  )

  const options = useMemo<DbImportOptions>(() => {
    const out: DbImportOptions = {
      schema,
      table,
      header: settings.header,
      encoding: settings.encoding,
      mode: detail ? settings.mode : "insert",
      skipBadRows: settings.skipBadRows,
    }
    if (settings.format) out.format = settings.format
    if (settings.delimiter) out.delimiter = settings.delimiter
    if (settings.nullToken) out.nullToken = settings.nullToken
    if (mapping) out.mapping = mapping
    if (detail && settings.mode === "upsert" && matchedBy && !matchedBy.primary) {
      out.conflict = { columns: matchedBy.columns }
    }
    if (!detail) out.createTable = made.length > 0 ? { columns: made } : {}
    return out
  }, [schema, table, detail, settings, mapping, matchedBy, made])

  // The dry run: asked again whenever the file or what is asked of it changes.
  const signature = JSON.stringify(options)
  useEffect(() => {
    if (!file || !open || !named) return
    const controller = new AbortController()
    const timer = setTimeout(() => {
      setPlan((held) => ({ ...held, pending: true }))
      const body = new FormData()
      body.append(
        "options",
        JSON.stringify({ ...(JSON.parse(signature) as DbImportOptions), dryRun: true }),
      )
      body.append("file", file.slice(0, SAMPLE_BYTES), file.name)
      postForm<DbImportReport>(`/databases/${id}/import/upload`, body, {
        signal: controller.signal,
      }).then(
        (answer) => setPlan({ report: answer, pending: false }),
        (err: unknown) => {
          if (controller.signal.aborted) return
          setPlan((held) => ({
            // The mapping the reader was editing stays on screen under the reason.
            report: held.report,
            error: err instanceof Error ? err : new Error(String(err)),
            pending: false,
          }))
        },
      )
    }, 250)
    return () => {
      clearTimeout(timer)
      controller.abort()
    }
  }, [id, file, open, named, signature])

  const take = (next: File) => {
    setFile(next)
    setMapping(null)
    setOverrides({})
    if (target.kind === "new" && name.trim() === "") setName(tableNameFrom(next.name))
    setPlan({ pending: true })
    setRun(IDLE)
  }

  const reset = () => {
    setFile(null)
    setSettings(START)
    setMapping(null)
    setName("")
    setOverrides({})
    setPlan({ pending: false })
    setRun(IDLE)
  }

  const close = (next: boolean) => {
    // An import in flight is one transaction on an open request: closing the
    // dialog would hide it, not stop it. It is stopped by name, with Cancel.
    if (run.pending) return
    onOpenChange(next)
    if (!next) reset()
  }

  const send = async () => {
    if (!file) return
    const controller = new AbortController()
    flight.current = controller
    setRun({ pending: true, sent: 0, total: file.size })
    const body = new FormData()
    body.append("options", JSON.stringify(options))
    body.append("file", file, file.name)
    try {
      const answer = await uploadForm<DbImportReport>(`/databases/${id}/import/upload`, body, {
        signal: controller.signal,
        onProgress: (sent, total) => setRun((held) => ({ ...held, sent, total })),
      })
      setRun({ ...IDLE, report: answer })
      onImported(table)
    } catch (err) {
      setRun({
        ...IDLE,
        error: controller.signal.aborted
          ? new Error("The import was cancelled. Nothing was written.")
          : err instanceof Error
            ? err
            : new Error(String(err)),
      })
    } finally {
      flight.current = null
    }
  }

  const start = () => {
    if (!detail || settings.mode !== "replace") {
      void send()
      return
    }
    confirm({
      title: "Replace every row",
      confirmLabel: "Empty the table and import",
      subject: {
        mark: <EngineMark engine={engine} size="sm" />,
        name: (
          <span className="font-mono">
            {detail.schema ? `${detail.schema}.` : ""}
            {detail.name}
          </span>
        ),
        facts: detail.estimatedRows >= 0 && (
          <FormFact label="Rows now">about {grouped(detail.estimatedRows)}</FormFact>
        ),
      },
      description: (
        <p>
          Every row the table holds is removed and the rows of{" "}
          <span className="font-mono">{file?.name}</span> are written in their place
          {engine.capabilities.importAtomic === true
            ? ". If the import fails the table is left as it was."
            : ". This engine has no transaction around it: if the import fails, the table stays empty."}
        </p>
      ),
      action: async () => {
        void send()
        return "reported"
      },
    })
  }

  const targets = detail ? detail.columns.filter((column) => !column.generated) : []
  const targetOf = (source: string, served: string) => (mapping ? (mapping[source] ?? "") : served)
  const mapped = columns.filter((column) => targetOf(column.source, column.target) !== "").length
  const done = run.report
  const progress = uploadProgress(run.sent, run.total)

  const pick = (source: string, next: string) => {
    const base =
      mapping ?? Object.fromEntries(columns.map((column) => [column.source, column.target]))
    setMapping({ ...base, [source]: next === SKIP ? "" : next })
  }
  const override = (source: string, patch: ColumnOverride) =>
    setOverrides((held) => ({ ...held, [source]: { ...held[source], ...patch } }))

  const tabular = report?.format === "csv" || report?.format === "tsv" || !report
  const qualified = `${schema ? `${schema}.` : ""}${table}`

  return (
    <>
      <Modal
        open={open}
        onOpenChange={close}
        size="xl"
        title={detail ? `Import into ${detail.name}` : "Import a file as a new table"}
        description={
          detail
            ? "A file's rows written into this table, previewed first"
            : "A table made from a file's columns and filled with its rows, previewed first"
        }
        footer={
          done ? (
            <Button onClick={() => close(false)}>Done</Button>
          ) : (
            <>
              {run.error && !run.pending ? (
                // Why the import did not happen, beside the button that was
                // pressed: the body may be scrolled anywhere, and said at its
                // top the refusal was out of sight with nothing changed on screen.
                <p
                  role="alert"
                  className="mr-auto min-w-0 basis-full text-hint break-words text-destructive sm:flex-1 sm:basis-0"
                >
                  {run.error.message}
                </p>
              ) : run.pending ? (
                <div className="mr-auto min-w-0 basis-full space-y-1.5 sm:flex-1 sm:basis-0">
                  <p className="numeric text-hint text-muted-foreground" aria-live="polite">
                    {progress.sending ? (
                      progress.words
                    ) : (
                      <TextShimmer>{progress.words}</TextShimmer>
                    )}
                  </p>
                  <Meter
                    size="thin"
                    value={progress.percent}
                    label="How much of the file has been sent"
                    className="max-w-64"
                  />
                </div>
              ) : (
                <p className="mr-auto min-w-0 text-hint text-muted-foreground">
                  {report
                    ? detail
                      ? `${grouped(report.rowsRead)} rows read from the start of the file · ${mapped} of ${columns.length} columns go in`
                      : `${grouped(report.rowsRead)} rows read from the start of the file · a table of ${mapped} ${mapped === 1 ? "column" : "columns"} is made`
                    : "Nothing is written until Import is pressed."}
                </p>
              )}
              {run.pending ? (
                <Button variant="outline" onClick={() => flight.current?.abort()}>
                  Cancel the import
                </Button>
              ) : (
                <Button variant="outline" onClick={() => close(false)}>
                  Cancel
                </Button>
              )}
              <Button
                disabled={
                  !file || !report || !named || mapped === 0 || plan.pending || Boolean(plan.error)
                }
                pending={run.pending}
                onClick={start}
              >
                {detail ? "Import" : "Create and import"}
              </Button>
            </>
          )
        }
      >
        {/* While the file is on its way the form is what was sent: it is not edited under it. */}
        <div className="grid gap-5" inert={run.pending || undefined}>
          <div className="flex min-w-0 items-center gap-3">
            <EngineMark engine={engine} size="sm" />
            <div className="min-w-0 space-y-0.5">
              <p className="flex min-w-0 items-center gap-2 font-mono text-body font-medium">
                <span className="min-w-0 truncate">
                  {detail || table ? qualified : `A new ${engine.nouns.object}`}
                </span>
                {!detail && <Tag className="shrink-0">not made yet</Tag>}
              </p>
              <FormFacts>
                {detail ? (
                  <>
                    <FormFact label="Columns">{detail.columns.length}</FormFact>
                    {detail.estimatedRows >= 0 && (
                      <FormFact label="Rows">about {grouped(detail.estimatedRows)}</FormFact>
                    )}
                    {detail.primaryKey.length > 0 && (
                      <FormFact label="Key" mono>
                        {detail.primaryKey.join(", ")}
                      </FormFact>
                    )}
                  </>
                ) : (
                  <>
                    {schema && (
                      <FormFact
                        label={engine.nouns.container.replace(/^./, (first) => first.toUpperCase())}
                        mono
                      >
                        {schema}
                      </FormFact>
                    )}
                    <FormFact label="Columns">
                      {report ? `${mapped} from the file` : "read from the file"}
                    </FormFact>
                  </>
                )}
              </FormFacts>
            </div>
          </div>

          {done ? (
            <ImportResult report={done} />
          ) : (
            <>
              <input
                ref={picker}
                type="file"
                hidden
                accept=".csv,.tsv,.tab,.txt,.json,.ndjson,.jsonl,text/csv,application/json"
                onChange={(event) => {
                  const next = event.currentTarget.files?.[0] ?? null
                  event.currentTarget.value = ""
                  if (next) take(next)
                }}
              />
              {!file ? (
                <button
                  type="button"
                  className="flex flex-col items-center gap-2 rounded-lg border border-dashed px-6 py-10 text-center focus-ring transition-colors hover:bg-row-hover"
                  onClick={() => picker.current?.click()}
                  onDragOver={(event) => event.preventDefault()}
                  onDrop={(event) => {
                    event.preventDefault()
                    const dropped = event.dataTransfer.files[0]
                    if (dropped) take(dropped)
                  }}
                >
                  <CloudUpload className="size-5 text-muted-foreground" />
                  <span className="text-body font-medium">Choose a file, or drop one here</span>
                  <span className="text-hint text-muted-foreground">
                    {formats.map((format) => FORMAT_LABEL[format]).join(" · ")}
                  </span>
                </button>
              ) : (
                <div className="flex min-w-0 items-center gap-2">
                  <span className="min-w-0 truncate font-mono text-body">{file.name}</span>
                  <span className="numeric shrink-0 text-hint text-muted-foreground">
                    {bytes(file.size)}
                  </span>
                  {report && <Tag>{FORMAT_LABEL[report.format]}</Tag>}
                  <Button
                    size="xs"
                    variant="ghost"
                    className="ml-auto"
                    disabled={run.pending}
                    onClick={() => picker.current?.click()}
                  >
                    Choose another
                  </Button>
                </div>
              )}

              {file && !detail && (
                <Field
                  label={`Name of the new ${engine.nouns.object}`}
                  htmlFor="import-new-name"
                  error={
                    taken
                      ? `${schema || `This ${engine.nouns.container}`} already has a ${engine.nouns.object} called ${table}. Open it and import into it instead.`
                      : undefined
                  }
                >
                  <Input
                    id="import-new-name"
                    value={name}
                    spellCheck={false}
                    autoComplete="off"
                    placeholder="orders_2026"
                    aria-invalid={taken || undefined}
                    className="max-w-sm font-mono sm:h-8"
                    onChange={(event) => setName(event.target.value)}
                  />
                </Field>
              )}

              {file && (
                <>
                  <FormSection title="How the file is read">
                    <FieldRow columns={3}>
                      <Field label="Format">
                        <Select
                          value={settings.format || "auto"}
                          onValueChange={(next) =>
                            setSettings((held) => ({
                              ...held,
                              format: next === "auto" ? "" : (next as DbImportFormat),
                            }))
                          }
                        >
                          <SelectTrigger size="sm" className="w-full" aria-label="Format">
                            <SelectValue />
                          </SelectTrigger>
                          <SelectContent>
                            <SelectItem value="auto">By the file&rsquo;s name</SelectItem>
                            {formats.map((format) => (
                              <SelectItem key={format} value={format}>
                                {FORMAT_LABEL[format]}
                              </SelectItem>
                            ))}
                          </SelectContent>
                        </Select>
                      </Field>
                      <Field label="Encoding">
                        <Segments
                          label="Encoding"
                          fill
                          value={settings.encoding}
                          options={[
                            { value: "utf-8", label: "UTF-8" },
                            { value: "utf-16", label: "UTF-16" },
                            { value: "latin-1", label: "Latin-1" },
                          ]}
                          onChange={(encoding) => setSettings((held) => ({ ...held, encoding }))}
                        />
                      </Field>
                      <Field label="NULL is written as" hint="Unquoted. Blank = an empty field.">
                        <Input
                          value={settings.nullToken}
                          placeholder="(an empty field)"
                          spellCheck={false}
                          className="font-mono sm:h-8"
                          onChange={(event) =>
                            setSettings((held) => ({ ...held, nullToken: event.target.value }))
                          }
                        />
                      </Field>
                    </FieldRow>
                    {tabular && (
                      <FieldRow columns={3}>
                        <Field label="Separator" hint="One character. Blank = the format's own.">
                          <Input
                            value={settings.delimiter}
                            maxLength={1}
                            placeholder={report?.format === "tsv" ? "tab" : ","}
                            spellCheck={false}
                            className="font-mono sm:h-8"
                            onChange={(event) =>
                              setSettings((held) => ({ ...held, delimiter: event.target.value }))
                            }
                          />
                        </Field>
                      </FieldRow>
                    )}
                    <OptionList>
                      {tabular && (
                        <OptionRow
                          title="The first line names the columns"
                          checked={settings.header}
                          onCheckedChange={(header) => {
                            setMapping(null)
                            setOverrides({})
                            setSettings((held) => ({ ...held, header }))
                          }}
                        />
                      )}
                      <OptionRow
                        title="Leave out a row the table refuses, and go on"
                        checked={settings.skipBadRows}
                        onCheckedChange={(skipBadRows) =>
                          setSettings((held) => ({ ...held, skipBadRows }))
                        }
                      />
                    </OptionList>
                  </FormSection>

                  {modes.length > 1 && detail && (
                    <FormSection title="What happens to the rows already there">
                      <Segments
                        label="Import mode"
                        value={settings.mode}
                        options={modes}
                        onChange={(mode) => setSettings((held) => ({ ...held, mode }))}
                      />
                      {settings.mode === "upsert" && (
                        <>
                          {keys.length > 1 && (
                            <Field label="A row of the file is the same row when it has the same">
                              <Select
                                value={String(Math.min(settings.key, keys.length - 1))}
                                onValueChange={(next) =>
                                  setSettings((held) => ({ ...held, key: Number(next) }))
                                }
                              >
                                <SelectTrigger
                                  size="sm"
                                  aria-label="What rows are matched by"
                                  className="w-full max-w-sm"
                                >
                                  <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                  {keys.map((key, at) => (
                                    <SelectItem key={key.label} value={String(at)}>
                                      <span className="font-mono text-xs">
                                        {key.columns.join(", ")}
                                      </span>
                                      <span className="text-hint text-muted-foreground">
                                        {key.label}
                                      </span>
                                    </SelectItem>
                                  ))}
                                </SelectContent>
                              </Select>
                            </Field>
                          )}
                          <FormNote>
                            A row of the file whose{" "}
                            <span className="font-mono">
                              {(report?.key ?? matchedBy?.columns ?? []).join(", ")}
                            </span>{" "}
                            is already in the table updates that row; the others are added.
                          </FormNote>
                        </>
                      )}
                      {settings.mode === "replace" && (
                        <FormNote tone="danger">
                          The table is emptied first. Every row it holds now is removed.
                        </FormNote>
                      )}
                    </FormSection>
                  )}

                  {plan.error && named && <ErrorState error={plan.error} />}
                  {report?.warnings.map((warning) => (
                    <Notice key={warning} tone="warning" title={warning} />
                  ))}

                  {!named ? (
                    <p className="py-4 text-center text-body text-muted-foreground">
                      {taken
                        ? "Give the table a name that is free."
                        : "Name the table to see what would be made of the file."}
                    </p>
                  ) : report ? (
                    <>
                      <FormSection
                        title={detail ? "Which column goes where" : "The columns it is made of"}
                      >
                        {/* Framed: a table owns its sideways scroll (§2). */}
                        <div
                          className={cn(
                            "overflow-hidden rounded-lg border",
                            plan.pending && "opacity-70",
                          )}
                        >
                          <Table>
                            <TableHeader>
                              <TableRow className="hover:bg-transparent">
                                <TableHead className="h-8">In the file</TableHead>
                                <TableHead className="h-8">Looks like</TableHead>
                                <TableHead className="h-8">
                                  {detail ? "Goes to" : "Column"}
                                </TableHead>
                                {!detail && <TableHead className="h-8">Type</TableHead>}
                                {!detail && <TableHead className="h-8">Key</TableHead>}
                                <TableHead className="h-8">Examples</TableHead>
                              </TableRow>
                            </TableHeader>
                            <TableBody>
                              {columns.map((column) => {
                                const goes = targetOf(column.source, column.target)
                                const mine = overrides[column.source]
                                return (
                                  <TableRow
                                    key={`${column.index}:${column.source}`}
                                    className="hover:bg-transparent"
                                  >
                                    <TableCell className="py-1.5 font-mono font-medium">
                                      {column.source}
                                    </TableCell>
                                    <TableCell className="py-1.5 text-muted-foreground">
                                      {column.inferred}
                                    </TableCell>
                                    <TableCell className="py-1.5">
                                      {detail ? (
                                        <Select
                                          value={goes || SKIP}
                                          onValueChange={(next) => pick(column.source, next)}
                                        >
                                          <SelectTrigger
                                            size="sm"
                                            aria-label={`Where ${column.source} goes`}
                                            className="h-7 w-48 font-mono text-xs sm:data-[size=sm]:h-7"
                                          >
                                            <SelectValue />
                                          </SelectTrigger>
                                          <SelectContent>
                                            <SelectItem value={SKIP} className="text-xs italic">
                                              Not imported
                                            </SelectItem>
                                            {targets.map((entry) => (
                                              <SelectItem
                                                key={entry.name}
                                                value={entry.name}
                                                className="font-mono text-xs"
                                              >
                                                {entry.name}
                                              </SelectItem>
                                            ))}
                                          </SelectContent>
                                        </Select>
                                      ) : (
                                        <Input
                                          value={goes}
                                          aria-label={`The column made of ${column.source}`}
                                          placeholder="Left out"
                                          spellCheck={false}
                                          autoComplete="off"
                                          className="h-8 w-44 font-mono placeholder:italic sm:h-7 sm:text-xs"
                                          onChange={(event) =>
                                            pick(column.source, event.target.value)
                                          }
                                        />
                                      )}
                                      {column.warning && (
                                        <p className="mt-1 max-w-64 text-hint whitespace-normal text-warning">
                                          {column.warning}
                                        </p>
                                      )}
                                    </TableCell>
                                    {!detail && (
                                      <TableCell className="py-1.5">
                                        <Input
                                          value={mine?.type ?? ""}
                                          aria-label={`The type of ${goes || column.source}`}
                                          placeholder={column.targetType ?? ""}
                                          disabled={goes === ""}
                                          spellCheck={false}
                                          autoComplete="off"
                                          className="h-8 w-36 font-mono sm:h-7 sm:text-xs"
                                          onChange={(event) =>
                                            override(column.source, { type: event.target.value })
                                          }
                                        />
                                      </TableCell>
                                    )}
                                    {!detail && (
                                      <TableCell className="py-1.5">
                                        <Checkbox
                                          checked={mine?.key === true}
                                          disabled={goes === ""}
                                          aria-label={`${goes || column.source} is part of the primary key`}
                                          onCheckedChange={(checked) =>
                                            override(column.source, { key: checked === true })
                                          }
                                        />
                                      </TableCell>
                                    )}
                                    <TableCell className="max-w-72 truncate py-1.5 font-mono text-muted-foreground">
                                      {column.examples.join(" · ")}
                                    </TableCell>
                                  </TableRow>
                                )
                              })}
                            </TableBody>
                          </Table>
                        </div>
                      </FormSection>

                      {report.preview && report.preview.rows.length > 0 && (
                        <FormSection title="The first rows, as they would be written">
                          <div className="overflow-hidden rounded-lg border">
                            <Table containerClassName="max-h-64">
                              <TableHeader>
                                <TableRow className="hover:bg-transparent">
                                  {report.preview.columns.map((column) => (
                                    <TableHead key={column} className="h-8 font-mono">
                                      {column}
                                    </TableHead>
                                  ))}
                                </TableRow>
                              </TableHeader>
                              <TableBody>
                                {report.preview.rows.map((row, index) => (
                                  <TableRow key={index} className="hover:bg-transparent">
                                    {row.map((value, at) => (
                                      <TableCell
                                        key={at}
                                        className="max-w-64 truncate py-1.5 font-mono"
                                      >
                                        {value === null ? (
                                          <span className="text-muted-foreground/70 italic">
                                            NULL
                                          </span>
                                        ) : value === "" ? (
                                          <span className="text-muted-foreground/70 italic">
                                            &quot;&quot;
                                          </span>
                                        ) : (
                                          value
                                        )}
                                      </TableCell>
                                    ))}
                                  </TableRow>
                                ))}
                              </TableBody>
                            </Table>
                          </div>
                        </FormSection>
                      )}

                      {report.create && (
                        <Statement
                          label="The table is made with"
                          sql={report.create.statement}
                          placeholder=""
                        />
                      )}
                      <Statement
                        label="One row is written with"
                        sql={report.statement}
                        placeholder=""
                      />
                    </>
                  ) : (
                    !plan.error && (
                      <p className="py-4 text-center text-body text-muted-foreground">
                        <TextShimmer>Reading the start of the file…</TextShimmer>
                      </p>
                    )
                  )}
                </>
              )}
            </>
          )}
        </div>
      </Modal>
      {dialog}
    </>
  )
}

/** What an import did, in figures, and the rows it left out. */
function ImportResult({ report }: { report: DbImportReport }) {
  return (
    <div className="grid gap-4">
      <FormFacts>
        {report.create?.created && <FormFact label="Table">created</FormFact>}
        <FormFact label="Read">{grouped(report.rowsRead)} rows</FormFact>
        <FormFact label="Added">{grouped(report.inserted)}</FormFact>
        {report.mode === "upsert" && <FormFact label="Updated">{grouped(report.updated)}</FormFact>}
        <FormFact label="Left out">{grouped(report.skipped)}</FormFact>
      </FormFacts>
      {!report.atomic && (
        <FormNote>This engine wrote the rows without a transaction around them.</FormNote>
      )}
      {report.warnings.map((warning) => (
        <Notice key={warning} tone="warning" title={warning} />
      ))}
      {report.errors.length > 0 && (
        <FormSection title="Rows left out">
          <ul className="divide-y divide-hairline text-xs">
            {report.errors.map((error) => (
              <li key={`${error.row}:${error.line}`} className="flex gap-3 py-1.5">
                <span className="numeric w-24 shrink-0 text-muted-foreground">
                  row {grouped(error.row)}, line {grouped(error.line)}
                </span>
                <span className="min-w-0 font-mono break-words">{error.message}</span>
              </li>
            ))}
          </ul>
          {report.errorsTruncated && (
            <FormNote>More rows were left out than are listed here.</FormNote>
          )}
        </FormSection>
      )}
    </div>
  )
}
