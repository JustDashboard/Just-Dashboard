"use client"

import { useMemo, useState } from "react"
import { notify } from "@/lib/toast"
import { cn, ringSafeScroll } from "@/lib/utils"
import { errorMessage } from "@/lib/api"
import { coerceDbValue } from "@/lib/db-values"
import type { DbColumn } from "@/lib/types"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"

import { Modal } from "@/components/modal"

type FieldState = { value: string; isNull: boolean }

/**
 * The form behind insert-row and edit-row. It is deliberately one dialog for
 * both: the only difference is whether the fields start empty or from an
 * existing row, and whether the submit carries a primary key to scope the
 * update. Exact integers and decimals travel as strings so the browser never
 * rounds them; finite ordinary numbers and booleans follow their column type.
 * NULL is kept distinct from the
 * empty string through an explicit toggle rather than guessed from a blank box.
 */
export function RowEditor({
  open,
  onOpenChange,
  mode,
  columns,
  primaryKey,
  initial,
  onSubmit,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  mode: "insert" | "edit"
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

  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      title={mode === "insert" ? "Insert row" : "Edit row"}
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
      <div className={cn("grid max-h-[60vh] gap-3 overflow-y-auto", ringSafeScroll)}>
        {columns.map((c) => {
          const f = fields[c.name] ?? { value: "", isNull: false }
          const isPk = primaryKey.includes(c.name)
          const long = /text|json|xml|blob|bytea/i.test(c.type)
          return (
            <div key={c.name} className="space-y-1.5">
              <div className="flex items-center justify-between gap-2">
                <Label htmlFor={`f-${c.name}`} className="font-mono text-xs">
                  {c.name}
                  <span className="ml-1.5 font-sans font-normal text-muted-foreground">
                    {c.type.toLowerCase()}
                    {isPk && " · pk"}
                    {!c.nullable && " · required"}
                  </span>
                </Label>
                {c.nullable && (
                  <label className="flex items-center gap-1.5 text-hint text-muted-foreground">
                    <Checkbox
                      checked={f.isNull}
                      onCheckedChange={(v) => set(c.name, { isNull: Boolean(v) })}
                    />
                    null
                  </label>
                )}
              </div>
              {long ? (
                <Textarea
                  id={`f-${c.name}`}
                  value={f.isNull ? "" : f.value}
                  disabled={f.isNull}
                  onChange={(e) => set(c.name, { value: e.target.value })}
                  className="min-h-20 font-mono text-xs"
                  placeholder={f.isNull ? "null" : c.default || ""}
                />
              ) : (
                <Input
                  id={`f-${c.name}`}
                  value={f.isNull ? "" : f.value}
                  disabled={f.isNull}
                  onChange={(e) => set(c.name, { value: e.target.value })}
                  className="font-mono text-xs"
                  placeholder={f.isNull ? "null" : c.default || ""}
                />
              )}
            </div>
          )
        })}
      </div>
    </Modal>
  )
}

function stringifyCell(v: unknown): string {
  if (typeof v === "object") return JSON.stringify(v)
  return String(v)
}
