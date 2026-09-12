"use client"

import { useState } from "react"
import { CloudUpload } from "@/components/icons"
import { notify } from "@/lib/toast"
import { plural } from "@/lib/format"
import { post } from "@/lib/api"
import type { DbImportResult, DbTableDetail } from "@/lib/types"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"

import { Well } from "@/components/panel"
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs"
import type { useConfirm } from "@/components/confirm-dialog"
import { Modal } from "@/components/modal"

type ConfirmFn = ReturnType<typeof useConfirm>["confirm"]

/**
 * Load CSV or JSON into a table.
 *
 * The whole load is one transaction on the server, so the two switches here
 * mean what they say: "stop at the first bad row" aborts everything, and
 * leaving it off still commits or rolls back as a unit — a row being skipped
 * never means a file was half applied. Appending is a plain confirmation;
 * replacing the contents empties the table first and so asks for the table's
 * name to be typed, exactly as a TRUNCATE does.
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
  const [format, setFormat] = useState<"csv" | "json">(documentStore ? "json" : "csv")
  const [data, setData] = useState("")
  const [hasHeader, setHasHeader] = useState(true)
  const [truncate, setTruncate] = useState(false)
  const [stopOnError, setStopOnError] = useState(false)
  const [nullAs, setNullAs] = useState("")
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState<DbImportResult | null>(null)

  const readFile = (file: File) => {
    const reader = new FileReader()
    reader.onload = () => {
      setData(String(reader.result ?? ""))
      if (file.name.endsWith(".json")) setFormat("json")
      if (file.name.endsWith(".csv")) setFormat("csv")
    }
    reader.readAsText(file)
  }

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

  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      size="lg"
      title={<>Import into {table}</>}
      footer={
        <>
          <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={busy}>
            Close
          </Button>
          <Button onClick={submit} disabled={!data.trim() || busy} pending={busy}>
            <CloudUpload className="size-4" />
            Import
          </Button>
        </>
      }
    >
      <div className="grid gap-3">
        <Tabs value={format} onValueChange={(v) => setFormat(v as "csv" | "json")}>
          <TabsList>
            <TabsTrigger value="csv">CSV</TabsTrigger>
            <TabsTrigger value="json">JSON</TabsTrigger>
          </TabsList>
        </Tabs>

        <div className="space-y-1.5">
          <div className="flex items-center justify-between gap-2">
            <Label htmlFor="import-data">Data</Label>
            <label className="cursor-pointer text-xs text-primary hover:underline">
              Choose a file
              <input
                type="file"
                accept=".csv,.json,.txt,text/csv,application/json"
                className="hidden"
                onChange={(e) => e.target.files?.[0] && readFile(e.target.files[0])}
              />
            </label>
          </div>
          <Textarea
            id="import-data"
            value={data}
            onChange={(e) => setData(e.target.value)}
            className="h-40 font-mono text-xs"
            placeholder={
              format === "csv"
                ? detail?.columns.map((c) => c.name).join(",") || "id,name"
                : '[{"name": "…"}]'
            }
          />
          <p className="text-xs text-muted-foreground">
            Sent in one request, so this tops out around 4 MB. A larger load belongs in the
            engine&apos;s own bulk loader.
            {documentStore &&
              " JSON may be an array or one document per line, which is what mongoexport writes."}
          </p>
        </div>

        <div className="grid gap-2 sm:grid-cols-2">
          {format === "csv" && (
            <>
              <label className="flex items-center gap-2 text-xs">
                <Checkbox checked={hasHeader} onCheckedChange={(v) => setHasHeader(Boolean(v))} />
                First row is a header
              </label>
              <div className="flex items-center gap-2">
                <Label htmlFor="null-as" className="shrink-0 text-xs">
                  NULL is
                </Label>
                <Input
                  id="null-as"
                  value={nullAs}
                  onChange={(e) => setNullAs(e.target.value)}
                  className="h-7 font-mono text-xs"
                  placeholder="(empty string)"
                />
              </div>
            </>
          )}
          <label className="flex items-center gap-2 text-xs">
            <Checkbox checked={stopOnError} onCheckedChange={(v) => setStopOnError(Boolean(v))} />
            Stop at the first bad row
          </label>
          <label className="flex items-center gap-2 text-xs text-destructive">
            <Checkbox checked={truncate} onCheckedChange={(v) => setTruncate(Boolean(v))} />
            Replace existing contents
          </label>
          {documentStore && (
            <p className="col-span-full text-hint text-muted-foreground">
              A standalone MongoDB server has no transaction to wrap this in, so a failure partway
              leaves what already landed in place. The result below says exactly how much that was.
            </p>
          )}
        </div>

        {result && (
          <div className="space-y-1">
            <p className="text-xs">
              <b>{result.inserted}</b> inserted
              {result.failed > 0 && (
                <>
                  , <b className="text-destructive">{result.failed}</b> failed
                </>
              )}
            </p>
            {result.errors.length > 0 && (
              <Well className="max-h-28 font-mono text-hint">
                {result.errors.join("\n")}
                {result.errorsTruncated && "\n… more not shown"}
              </Well>
            )}
          </div>
        )}
      </div>
    </Modal>
  )
}
