"use client"

import { useMemo, useState } from "react"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { errorMessage } from "@/lib/api"
import { coerceDbValue } from "@/lib/db-values"
import type { DbColumn } from "@/lib/types"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Modal } from "@/components/modal"
import { FormFact, FormFacts, FormNote } from "@/components/form"

type FieldState = { value: string; isNull: boolean }

/**
 * The form behind insert-row and edit-row. It is deliberately one dialog for
 * both: the only difference is whether the fields start empty or from an
 * existing row, and whether the submit carries a primary key to scope the
 * update. Exact integers and decimals travel as strings so the browser never
 * rounds them; finite ordinary numbers and booleans follow their column type.
 * NULL is kept distinct from the empty string through an explicit switch
 * rather than guessed from a blank box.
 *
 * Each column is one line: its name and type on the left, the value on the
 * right, the null switch at the edge — so a row of twenty columns reads down
 * as the table it came from rather than as twenty stacked forms.
 */
export function RowEditor({
  open,
  onOpenChange,
  mode,
  table,
  columns,
  primaryKey,
  initial,
  onSubmit,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  mode: "insert" | "edit"
  table?: string
  columns: DbColumn[]
  primaryKey: string[]
  initial?: Record<string, unknown>
  onSubmit: (values: Record<string, unknown>, key?: Record<string, unknown>) => Promise<void>
}) {
  const initialFields = useMemo(() => {
    const f: Record<string, FieldState> = {}
    for (const c of columns) {
      const raw = initial?.[c.name]
      f[c.name] = {
        value: raw === null || raw === undefined ? "" : stringifyCell(raw),
        isNull: mode === "insert" ? false : raw === null || raw === undefined,
      }
    }
    return f
  }, [columns, initial, mode])

  const [fields, setFields] = useState<Record<string, FieldState>>(initialFields)
  const [busy, setBusy] = useState(false)

  const set = (name: string, patch: Partial<FieldState>) =>
    setFields((f) => ({ ...f, [name]: { ...f[name], ...patch } }))

  const submit = async () => {
    setBusy(true)
    try {
      const values: Record<string, unknown> = {}
      for (const c of columns) {
        const f = fields[c.name]
        // On insert, an untouched field is left to the column's default rather
        // than forced to NULL or "" — sending it would override the default.
        if (mode === "insert" && !f.isNull && f.value === "") continue
        values[c.name] = f.isNull ? null : coerceDbValue(f.value, c.type)
      }
      let key: Record<string, unknown> | undefined
      if (mode === "edit") {
        key = {}
        for (const pk of primaryKey) key[pk] = initial?.[pk] ?? null
      }
      await onSubmit(values, key)
      onOpenChange(false)
    } catch (err) {
      notify.error(mode === "insert" ? "Insert failed" : "Update failed", errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  const keyFacts = mode === "edit" ? primaryKey.map((pk) => `${pk} = ${String(initial?.[pk])}`) : []

  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      size="lg"
      title={mode === "insert" ? "Insert row" : "Edit row"}
      description={
        mode === "insert"
          ? `A new row in ${table ?? "the table"}. Empty fields take the column's default.`
          : `Changes are scoped to the row's primary key.`
      }
      footer={
        <>
          <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={submit} pending={busy}>
            {mode === "insert" ? "Insert" : "Save changes"}
          </Button>
        </>
      }
    >
      <div className="grid gap-4">
        <FormFacts>
          {table && (
            <FormFact label="Table" mono>
              {table}
            </FormFact>
          )}
          {keyFacts.length > 0 && (
            <FormFact label="Row" mono>
              {keyFacts.join(", ")}
            </FormFact>
          )}
        </FormFacts>

        <div className="min-w-0 divide-y divide-hairline">
          {columns.map((c) => {
            const f = fields[c.name] ?? { value: "", isNull: false }
            const isPk = primaryKey.includes(c.name)
            const long = /text|json|xml|blob|bytea|clob/i.test(c.type)
            const bool = /^bool/i.test(c.type)
            const fieldId = `f-${c.name}`
            return (
              <div
                key={c.name}
                className="grid gap-x-4 gap-y-1.5 py-2.5 first:pt-0 last:pb-0 sm:grid-cols-[minmax(0,11rem)_minmax(0,1fr)_auto] sm:items-start"
              >
                <label htmlFor={fieldId} className="min-w-0 sm:pt-2">
                  <span className="block truncate font-mono text-xs font-medium">{c.name}</span>
                  <span className="block truncate text-hint text-muted-foreground">
                    {c.type.toLowerCase()}
                    {isPk && " · pk"}
                    {!c.nullable && !isPk && " · required"}
                  </span>
                </label>
                <div className="min-w-0">
                  {bool ? (
                    <Select
                      value={f.isNull ? "null" : f.value === "" ? "default" : f.value}
                      onValueChange={(v) => {
                        if (v === "null") set(c.name, { isNull: true })
                        else set(c.name, { isNull: false, value: v === "default" ? "" : v })
                      }}
                    >
                      <SelectTrigger id={fieldId} className="w-full font-mono" disabled={f.isNull}>
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        {mode === "insert" && <SelectItem value="default">default</SelectItem>}
                        <SelectItem value="true" className="font-mono">
                          true
                        </SelectItem>
                        <SelectItem value="false" className="font-mono">
                          false
                        </SelectItem>
                      </SelectContent>
                    </Select>
                  ) : long ? (
                    <Textarea
                      id={fieldId}
                      value={f.isNull ? "" : f.value}
                      disabled={f.isNull}
                      onChange={(e) => set(c.name, { value: e.target.value })}
                      className="max-h-48 min-h-16 font-mono text-xs"
                      placeholder={
                        f.isNull ? "null" : c.default || (mode === "insert" ? "default" : "")
                      }
                    />
                  ) : (
                    <Input
                      id={fieldId}
                      value={f.isNull ? "" : f.value}
                      disabled={f.isNull}
                      onChange={(e) => set(c.name, { value: e.target.value })}
                      className={cn("font-mono", f.isNull && "italic")}
                      placeholder={
                        f.isNull ? "null" : c.default || (mode === "insert" ? "default" : "")
                      }
                    />
                  )}
                </div>
                <div className="flex h-full items-center sm:justify-end sm:pt-2">
                  {c.nullable && (
                    <label className="flex items-center gap-2 text-hint text-muted-foreground">
                      <Switch
                        size="sm"
                        checked={f.isNull}
                        onCheckedChange={(v) => set(c.name, { isNull: v })}
                        aria-label={`${c.name} is null`}
                      />
                      null
                    </label>
                  )}
                </div>
              </div>
            )
          })}
        </div>
        {mode === "insert" && (
          <FormNote>
            Leave a field empty to let the column&apos;s default decide it. Numbers and booleans are
            sent as their column type; everything else as text.
          </FormNote>
        )}
      </div>
    </Modal>
  )
}

function stringifyCell(v: unknown): string {
  if (typeof v === "object") return JSON.stringify(v)
  return String(v)
}
