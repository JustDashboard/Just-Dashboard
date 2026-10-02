"use client"

import { useEffect, useId, useRef, useState } from "react"
import { ArrowDown, ArrowUp, Key, Plus, Trash } from "@/components/icons"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useSessionState } from "@/lib/view-state"
import { Field, FieldRow, FormNote, FormSection } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { SidePanel } from "@/components/side-panel"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { EngineMark } from "@/components/database/kit"
import { useDatabase } from "@/components/database/shell/database-context"
import { PlannedStatement, refusal } from "@/components/database/schema/change-dialog"
import {
  blankColumn,
  isBlank,
  keyPreset,
  tableProblems,
  tableRequest,
  type ColumnDraft,
  type TableDraft,
} from "@/components/database/schema/changes"
import { TypeField, useColumnTypes } from "@/components/database/schema/fields"
import { useFocusReturn } from "@/components/database/schema/focus"
import { SchemaSelect } from "@/components/database/schema/object-forms"
import type { DbCatalogSchema, DdlAnswer } from "@/components/database/schema/types"
import { runChange, usePreview } from "@/components/database/schema/use-ddl"

const START: TableDraft = { schema: "", name: "", columns: [] }

/** A draft somebody has put work into: a name, or a column that is not blank. */
function worked(draft: TableDraft): boolean {
  return draft.name.trim() !== "" || draft.columns.some((column) => !isBlank(column))
}

/**
 * A new table, drawn up in a panel beside the schema it will land in: its
 * name, its schema, its columns one to a row, and under them the statement
 * the server will run for exactly what the rows say.
 *
 * Three things the form it replaces got wrong are the reason for its shape.
 * A column with a name and no type used to be left out of the statement
 * without a word; here it stops the form and is named. The schema used to be
 * whichever one the reader last stood in; here it is a field. And the
 * statement used to be drawn in the browser; here it is the server's, asked
 * for as the rows change.
 *
 * The draft is kept for the tab. Closing the panel — a slip of Escape, a
 * press outside it, a look at another table — loses nothing: opening it again
 * finds the rows as they were left, with "Start over" to clear them. When
 * it closes, the keyboard goes back to the control that opened it.
 */
export function NewTablePanel({
  open,
  schema,
  schemas,
  onClose,
  onCreated,
}: {
  open: boolean
  /** The schema the reader is in. */
  schema: string
  schemas: readonly DbCatalogSchema[]
  onClose: () => void
  onCreated: (schema: string, table: string, answer: DdlAnswer) => void
}) {
  const ids = useId()
  const { id, engine } = useDatabase()
  useFocusReturn(open)
  const [kept, setKept] = useSessionState<TableDraft>(`databases.${id}.schema.newTable`, START)
  // A draft with no rows yet is the form's first sight: one empty row to type in.
  const draft: TableDraft = kept.columns.length > 0 ? kept : { ...kept, columns: [FIRST_ROW] }
  const target = draft.schema || schema
  const stated: TableDraft = { ...draft, schema: target }
  const set = (patch: Partial<TableDraft>) => setKept({ ...stated, ...patch })
  const setColumn = (key: string, patch: Partial<ColumnDraft>) =>
    set({
      columns: stated.columns.map((column) =>
        column.key === key ? { ...column, ...patch } : column,
      ),
    })

  const types = useColumnTypes()
  const preset = keyPreset(types)
  const keyed =
    preset !== null && stated.columns.some((column) => samePreset(column, preset.column))

  const problems = tableProblems(stated)
  const request = open ? tableRequest(stated) : null
  const [attempt, setAttempt] = useState(0)
  const preview = usePreview(id, request, attempt)
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState<Error>()
  // Problems are said once the reader has tried to go on, not while the first
  // name is still being typed.
  const [pressed, setPressed] = useState(false)

  const rows = useRef<HTMLDivElement>(null)
  const focusRow = useRef<string | null>(null)
  useEffect(() => {
    const key = focusRow.current
    if (!key) return
    focusRow.current = null
    rows.current?.querySelector<HTMLInputElement>(`[data-column="${key}"] input`)?.focus()
  })

  const addColumn = (over: Partial<ColumnDraft> = {}, first = false) => {
    const column = blankColumn(over)
    focusRow.current = over.name ? null : column.key
    const held = stated.columns.filter((row, index) => !(index === 0 && first && isBlank(row)))
    set({ columns: first ? [column, ...held] : [...held, column] })
  }
  const move = (index: number, by: -1 | 1) => {
    const next = [...stated.columns]
    const [column] = next.splice(index, 1)
    next.splice(index + by, 0, column)
    set({ columns: next })
  }

  const create = async () => {
    setPressed(true)
    if (!request || !preview.answer || busy) return
    setBusy(true)
    setFailure(undefined)
    try {
      const answer = await runChange(id, request)
      const name = stated.name.trim()
      notify.success(`Created ${name}`, { description: answer.statement })
      setKept(START)
      setPressed(false)
      onCreated(target, name, answer)
    } catch (err) {
      setFailure(err instanceof Error ? err : new Error(String(err)))
    } finally {
      setBusy(false)
    }
  }

  const container = engine.nouns.container
  return (
    <SidePanel
      open={open}
      // Closing keeps the draft, so nothing has to be asked — except while
      // the statement is running, when the panel stays.
      onOpenChange={(next) => !next && !busy && onClose()}
      width="lg"
      title={`New ${engine.nouns.object}`}
      description={`A new ${engine.nouns.object}: its columns, and the statement that makes it`}
      footer={
        <>
          <p className="mr-auto min-w-0 text-hint text-muted-foreground" aria-live="polite">
            {pressed && problems.summary
              ? problems.summary
              : worked(stated)
                ? "Closing keeps this draft for the tab."
                : ""}
          </p>
          <Button variant="outline" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button
            onClick={() => void create()}
            pending={busy}
            // Pressable while the form is unfinished, so the press can say
            // what is missing; off only while the server is still planning.
            disabled={request !== null && !preview.answer}
          >
            Create {engine.nouns.object}
          </Button>
        </>
      }
    >
      <div className="space-y-6">
        <div className="flex min-w-0 items-center gap-3">
          <EngineMark engine={engine} size="sm" />
          <div className="min-w-0">
            <p className="truncate font-mono text-body font-medium">
              {target ? `${target}.` : ""}
              {stated.name.trim() || <span className="text-muted-foreground">new_table</span>}
            </p>
          </div>
          {worked(stated) && (
            <Button
              size="xs"
              variant="ghost"
              className="ml-auto shrink-0 text-muted-foreground"
              onClick={() => {
                setKept(START)
                setPressed(false)
                setFailure(undefined)
              }}
            >
              Start over
            </Button>
          )}
        </div>

        <FieldRow>
          {engine.can("schemas") && schemas.length > 0 && (
            <Field
              label={container[0].toUpperCase() + container.slice(1)}
              htmlFor={`${ids}-schema`}
            >
              <SchemaSelect
                id={`${ids}-schema`}
                schemas={schemas}
                value={target}
                onChange={(next) => set({ schema: next })}
              />
            </Field>
          )}
          <Field label="Name" htmlFor={`${ids}-name`} error={pressed ? problems.name : undefined}>
            <Input
              id={`${ids}-name`}
              autoFocus
              autoComplete="off"
              spellCheck={false}
              className="font-mono text-xs"
              value={stated.name}
              aria-invalid={(pressed && Boolean(problems.name)) || undefined}
              onChange={(event) => set({ name: event.target.value })}
            />
          </Field>
        </FieldRow>

        <FormSection
          title="Columns"
          actions={
            <>
              {preset && !keyed && (
                <Button size="xs" variant="outline" onClick={() => addColumn(preset.column, true)}>
                  <Key />
                  {preset.label}
                </Button>
              )}
              <Button size="xs" variant="outline" onClick={() => addColumn()}>
                <Plus />
                Add column
              </Button>
            </>
          }
        >
          <div ref={rows} className="space-y-1.5">
            <div
              aria-hidden
              className="hidden grid-cols-[minmax(0,1fr)_minmax(0,1fr)_minmax(0,0.8fr)_3rem_2.5rem_4.75rem] gap-2 px-px text-hint font-medium text-muted-foreground sm:grid"
            >
              <span>Name</span>
              <span>Type</span>
              <span>Default</span>
              <span className="text-center">Not null</span>
              <span className="text-center">Key</span>
              <span />
            </div>
            {stated.columns.map((column, index) => {
              const problem = pressed ? problems.columns[column.key] : undefined
              const label = column.name.trim() || `column ${index + 1}`
              return (
                <div key={column.key} data-column={column.key}>
                  <div
                    role="group"
                    aria-label={`Column ${index + 1}`}
                    className={cn(
                      "grid grid-cols-2 items-center gap-2",
                      "sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_minmax(0,0.8fr)_3rem_2.5rem_4.75rem]",
                    )}
                  >
                    <Input
                      aria-label={`Name of column ${index + 1}`}
                      autoComplete="off"
                      spellCheck={false}
                      placeholder="name"
                      className="h-10 font-mono text-xs sm:h-8"
                      value={column.name}
                      aria-invalid={
                        Boolean(problem) && column.name.trim() === "" ? true : undefined
                      }
                      onChange={(event) => setColumn(column.key, { name: event.target.value })}
                    />
                    <TypeField
                      dense
                      label={`Type of ${label}`}
                      value={column.type}
                      invalid={Boolean(problem) && column.type.trim() === ""}
                      onChange={(type) => setColumn(column.key, { type })}
                    />
                    <Input
                      aria-label={`Default of ${label}`}
                      autoComplete="off"
                      spellCheck={false}
                      placeholder="no default"
                      className="h-10 font-mono text-xs max-sm:col-span-2 sm:h-8"
                      value={column.default}
                      onChange={(event) => setColumn(column.key, { default: event.target.value })}
                    />
                    <label className="flex items-center justify-center gap-2 max-sm:justify-start">
                      <Checkbox
                        aria-label={`${label} refuses NULL`}
                        checked={column.notNull || column.primaryKey}
                        disabled={column.primaryKey}
                        onCheckedChange={(checked) =>
                          setColumn(column.key, { notNull: checked === true })
                        }
                      />
                      <span className="text-hint text-muted-foreground sm:hidden">Not null</span>
                    </label>
                    <label className="flex items-center justify-center gap-2 max-sm:justify-start">
                      <Checkbox
                        aria-label={`${label} is part of the primary key`}
                        checked={column.primaryKey}
                        onCheckedChange={(checked) =>
                          setColumn(column.key, { primaryKey: checked === true })
                        }
                      />
                      <span className="text-hint text-muted-foreground sm:hidden">Key</span>
                    </label>
                    <div className="flex items-center justify-end max-sm:col-span-2">
                      <IconAction
                        label={`Move ${label} up`}
                        className="size-6"
                        disabled={index === 0}
                        onClick={() => move(index, -1)}
                      >
                        <ArrowUp />
                      </IconAction>
                      <IconAction
                        label={`Move ${label} down`}
                        className="size-6"
                        disabled={index === stated.columns.length - 1}
                        onClick={() => move(index, 1)}
                      >
                        <ArrowDown />
                      </IconAction>
                      <IconAction
                        label={`Remove ${label}`}
                        className="size-6"
                        onClick={() =>
                          set({ columns: stated.columns.filter((row) => row.key !== column.key) })
                        }
                      >
                        <Trash />
                      </IconAction>
                    </div>
                  </div>
                  {problem && (
                    <p role="alert" className="pt-1 text-hint text-destructive">
                      {problem}
                    </p>
                  )}
                </div>
              )
            })}
          </div>
          {preset && keyed && <FormNote className="pt-1">{preset.says}</FormNote>}
          {!preset && (
            <FormNote className="pt-1">
              A key that numbers itself is declared differently by every engine, and this form
              writes a column as a type and nothing after it: make such a table in Query.
            </FormNote>
          )}
        </FormSection>

        <PlannedStatement
          request={request}
          preview={preview}
          waiting={
            worked(stated)
              ? (problems.summary ?? "Name the table and its columns.")
              : "Name the table and its columns: the statement is written as you go."
          }
          onRetry={() => setAttempt((n) => n + 1)}
        />
        {failure && (
          <FormNote role="alert" tone="danger" className="break-words">
            Nothing was created. {refusal(failure)}
          </FormNote>
        )}
      </div>
    </SidePanel>
  )
}

const FIRST_ROW: ColumnDraft = {
  key: "first",
  name: "",
  type: "",
  notNull: false,
  primaryKey: false,
  default: "",
}

function samePreset(column: ColumnDraft, preset: Pick<ColumnDraft, "name" | "type">) {
  return column.name.trim() === preset.name && column.type.trim() === preset.type
}
