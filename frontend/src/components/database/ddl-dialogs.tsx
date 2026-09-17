"use client"

import { useEffect, useId, useMemo, useRef, useState } from "react"
import { ArrowDown, ArrowUp, Check, ChevronDown, Key, Plus, Trash } from "@/components/icons"
import { notify } from "@/lib/toast"
import { post } from "@/lib/api"
import { plural } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { DbDriverInfo, DbNewColumn, DbTableDetail } from "@/lib/types"
import { IconAction } from "@/components/icon-action"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Toggle } from "@/components/ui/toggle"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command"
import { Modal } from "@/components/modal"
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

/**
 * The schema-editing forms.
 *
 * Every one of them shows the statement it will run before it runs it. A DDL
 * form that hides its SQL asks the operator to trust a black box with their
 * schema; showing it costs a few lines and turns the form into something you
 * can also learn from. The server builds the real statement in the engine's
 * own dialect — these previews are rendered from the same fields, so the shape
 * is right even where a keyword differs.
 *
 * They share one vocabulary (`components/form.tsx`) and one rhythm: what the
 * form operates on as facts under the title, the fields, the options as
 * switches with a sentence each, and the statement last — the thing the
 * reader checks before pressing the button sits nearest to it.
 */

const EMPTY_COLUMN: DbNewColumn = {
  name: "",
  type: "",
  notNull: false,
  primaryKey: false,
  default: "",
}

function qualify(schema: string, table: string) {
  return schema ? `${schema}.${table}` : table
}

export function CreateTableDialog({
  open,
  onOpenChange,
  connId,
  schema,
  info,
  onDone,
}: {
  open: boolean
  onOpenChange: (o: boolean) => void
  connId: number
  schema: string
  info?: DbDriverInfo
  onDone: () => void
}) {
  const id = useId()
  const [table, setTable] = useState("")
  const [columns, setColumns] = useState<DbNewColumn[]>([{ ...EMPTY_COLUMN }])
  const [busy, setBusy] = useState(false)
  const list = useRef<HTMLDivElement>(null)
  const focusLast = useRef(false)

  const types = info?.columnTypes ?? []
  const setCol = (i: number, patch: Partial<DbNewColumn>) =>
    setColumns((cs) => cs.map((c, j) => (j === i ? { ...c, ...patch } : c)))
  const addColumn = (preset?: Partial<DbNewColumn>) => {
    focusLast.current = true
    setColumns((cs) => [...cs, { ...EMPTY_COLUMN, ...preset }])
  }
  // The key goes first, where every schema puts it — and takes the place of
  // the empty row a fresh form opens on rather than sitting under it.
  const addIdColumn = (preset: Partial<DbNewColumn>) =>
    setColumns((cs) => {
      const blank = cs.length === 1 && !cs[0].name && !cs[0].type
      return [{ ...EMPTY_COLUMN, ...preset }, ...(blank ? [] : cs)]
    })

  // The new row takes the caret, so a table is typed column after column
  // without reaching for the mouse between them.
  useEffect(() => {
    if (!focusLast.current) return
    focusLast.current = false
    // Three inputs per row — name, type, default — so the last row's name is
    // three from the end.
    const rows = list.current?.querySelectorAll<HTMLInputElement>("[data-column-row] input")
    rows?.[rows.length - 3]?.focus()
  }, [columns.length])

  const complete = columns.filter((c) => c.name.trim() && c.type.trim())
  const duplicates = useMemo(() => {
    const seen = new Map<string, number>()
    for (const c of columns) {
      const key = c.name.trim().toLowerCase()
      if (key) seen.set(key, (seen.get(key) ?? 0) + 1)
    }
    return [...seen.entries()].filter(([, n]) => n > 1).map(([name]) => name)
  }, [columns])
  const valid = table.trim() !== "" && complete.length > 0 && duplicates.length === 0

  const submit = async () => {
    setBusy(true)
    try {
      const res = await post<{ statement: string }>(`/databases/${connId}/ddl/table`, {
        schema,
        table: table.trim(),
        columns: complete.map((c) => ({ ...c, name: c.name.trim(), type: c.type.trim() })),
      })
      notify.success(`Created ${table.trim()}`, { description: res.statement })
      onOpenChange(false)
      setTable("")
      setColumns([{ ...EMPTY_COLUMN }])
      onDone()
    } catch (err) {
      notify.error("Could not create the table", err)
    } finally {
      setBusy(false)
    }
  }

  // The commonest first column, offered as one press: an integer key the
  // engine numbers itself, where the engine's type list has such a thing.
  const idType =
    ["bigserial", "serial", "INTEGER", "integer", "bigint", "BIGINT", "int"].find((t) =>
      types.includes(t),
    ) ?? "integer"
  const hasId = columns.some((c) => c.name.trim().toLowerCase() === "id")

  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      size="xl"
      title="Create table"
      description="Name the table and its columns. The statement is shown before anything runs."
      footer={
        <>
          <span className="mr-auto text-hint text-muted-foreground">
            {plural(complete.length, "column")} defined
          </span>
          <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={!valid || busy} pending={busy}>
            Create table
          </Button>
        </>
      }
    >
      <div className="grid gap-5">
        <FormFacts>
          <FormFact label="Schema" mono>
            {schema || "default"}
          </FormFact>
          {info && <FormFact label="Engine">{info.label}</FormFact>}
        </FormFacts>

        <Field label="Table name" htmlFor={`${id}-table`}>
          <Input
            id={`${id}-table`}
            value={table}
            onChange={(e) => setTable(e.target.value)}
            className="font-mono sm:max-w-sm"
            placeholder="orders"
            autoFocus
          />
        </Field>

        <FormSection
          title="Columns"
          hint="Enter on the last row adds another."
          actions={
            <>
              {!hasId && (
                <Button
                  size="xs"
                  variant="ghost"
                  onClick={() =>
                    addIdColumn({ name: "id", type: idType, primaryKey: true, notNull: true })
                  }
                >
                  <Key />
                  Add id key
                </Button>
              )}
              <Button size="xs" variant="outline" onClick={() => addColumn()}>
                <Plus />
                Add column
              </Button>
            </>
          }
        >
          <div ref={list} className="min-w-0 space-y-1.5">
            <div className={cn(COLUMN_GRID, "px-0.5 text-hint font-medium text-muted-foreground")}>
              <span>Name</span>
              <span>Type</span>
              <span>Default</span>
              <span>Constraints</span>
              <span />
            </div>
            {columns.map((c, i) => {
              const last = i === columns.length - 1
              const duplicate = duplicates.includes(c.name.trim().toLowerCase())
              const half = Boolean(c.name.trim()) !== Boolean(c.type.trim())
              const onEnter = (e: React.KeyboardEvent) => {
                if (e.key === "Enter" && last) {
                  e.preventDefault()
                  addColumn()
                }
              }
              return (
                <div key={i} data-column-row className={COLUMN_GRID}>
                  <Input
                    aria-label={`Column ${i + 1} name`}
                    placeholder="name"
                    value={c.name}
                    aria-invalid={duplicate || (half && !c.name.trim()) || undefined}
                    onChange={(e) => setCol(i, { name: e.target.value })}
                    onKeyDown={onEnter}
                    className="font-mono"
                  />
                  <TypeField
                    types={types}
                    value={c.type}
                    invalid={half && !c.type.trim()}
                    onChange={(v) => setCol(i, { type: v })}
                    onKeyDown={onEnter}
                  />
                  <Input
                    aria-label={`Column ${i + 1} default`}
                    placeholder="none"
                    value={c.default ?? ""}
                    onChange={(e) => setCol(i, { default: e.target.value })}
                    onKeyDown={onEnter}
                    className="font-mono"
                    title="A SQL expression, quoted if it is a string: 'none', 0, now()"
                  />
                  <div className="flex items-center gap-1">
                    <Toggle
                      size="sm"
                      variant="outline"
                      pressed={Boolean(c.primaryKey)}
                      onPressedChange={(on) =>
                        setCol(i, { primaryKey: on, notNull: on ? true : c.notNull })
                      }
                      aria-label="Primary key"
                      title="Primary key"
                      className="gap-1 px-2 text-hint"
                    >
                      <Key className="size-3" />
                      PK
                    </Toggle>
                    <Toggle
                      size="sm"
                      variant="outline"
                      pressed={Boolean(c.notNull)}
                      onPressedChange={(on) => setCol(i, { notNull: on })}
                      aria-label="Not null"
                      title="Not null — every row must have a value"
                      className="px-2 text-hint"
                    >
                      Not null
                    </Toggle>
                  </div>
                  <IconAction
                    label={columns.length === 1 ? "A table needs a column" : "Remove this column"}
                    className="text-muted-foreground hover:text-destructive"
                    disabled={columns.length === 1}
                    onClick={() => setColumns((cs) => cs.filter((_, j) => j !== i))}
                  >
                    <Trash />
                  </IconAction>
                </div>
              )
            })}
          </div>
          {duplicates.length > 0 && (
            <FormNote tone="danger">
              {duplicates.length === 1
                ? `Two columns are called ${duplicates[0]}.`
                : `Repeated column names: ${duplicates.join(", ")}.`}
            </FormNote>
          )}
        </FormSection>

        <Statement
          sql={valid ? previewCreate(schema, table.trim(), complete) : ""}
          placeholder="Name the table and give at least one column a name and a type."
        />
      </div>
    </Modal>
  )
}

/**
 * The shared grid for the column editor's header and its rows. The last two
 * tracks are fixed so the header labels sit over the fields below them however
 * the dialog resizes; `auto` sized each to its own content and the labels
 * drifted rightwards row by row.
 */
const COLUMN_GRID =
  "grid grid-cols-[minmax(0,1.1fr)_minmax(0,1.1fr)_minmax(0,0.9fr)_9.5rem_2rem] items-center gap-2"

/**
 * The column type: a field welded to a searchable list of the engine's own
 * types.
 *
 * Both halves are load-bearing. The engine's list is templates —
 * `varchar(255)`, `numeric(10,2)`, `enum('a','b')` — so picking one is a start,
 * not an answer, and the field stays editable to change the number. The list
 * is what makes `timestamptz` or `jsonb` a click rather than a spelling test,
 * and it is searchable because twenty-odd names is past the point a plain menu
 * is faster than typing. Anything typed that is not on the list is still sent
 * — the server validates it — so an unusual but legitimate type is never
 * unreachable.
 */
function TypeField({
  id,
  types,
  value,
  invalid,
  onChange,
  onKeyDown,
}: {
  id?: string
  types: string[]
  value: string
  invalid?: boolean
  onChange: (v: string) => void
  onKeyDown?: (e: React.KeyboardEvent) => void
}) {
  const [open, setOpen] = useState(false)
  return (
    <div className="relative flex min-w-0 items-center">
      <Input
        id={id}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={onKeyDown}
        placeholder="type"
        aria-label={id ? undefined : "Column type"}
        aria-invalid={invalid || undefined}
        className={cn("font-mono", types.length > 0 && "pr-9")}
      />
      {types.length > 0 && (
        <Popover open={open} onOpenChange={setOpen}>
          <PopoverTrigger asChild>
            <button
              type="button"
              aria-label="Browse types"
              className="absolute right-1 flex size-7 items-center justify-center rounded-sm text-muted-foreground focus-ring transition-colors hover:bg-accent hover:text-foreground data-[state=open]:bg-accent data-[state=open]:text-foreground"
            >
              <ChevronDown className="size-3.5" />
            </button>
          </PopoverTrigger>
          <PopoverContent align="end" className="w-56 p-0">
            <Command
              filter={(v, search) => (v.toLowerCase().includes(search.toLowerCase()) ? 1 : 0)}
            >
              <CommandInput placeholder="Filter types…" className="text-xs" />
              <CommandList className="max-h-60">
                <CommandEmpty className="px-3 py-4 text-center text-xs text-muted-foreground">
                  Not on the list — type it into the field.
                </CommandEmpty>
                <CommandGroup>
                  {types.map((t) => (
                    <CommandItem
                      key={t}
                      value={t}
                      onSelect={() => {
                        onChange(t)
                        setOpen(false)
                      }}
                      className="font-mono text-xs"
                    >
                      {t}
                      {value === t && <Check className="ml-auto size-3.5 text-primary" />}
                    </CommandItem>
                  ))}
                </CommandGroup>
              </CommandList>
            </Command>
          </PopoverContent>
        </Popover>
      )}
    </div>
  )
}

function columnLine(c: DbNewColumn, inlinePk: boolean) {
  let l = `${c.name} ${c.type}`
  if (c.default) l += ` DEFAULT ${c.default}`
  if (c.notNull) l += " NOT NULL"
  if (inlinePk && c.primaryKey) l += " PRIMARY KEY"
  return l
}

function previewCreate(schema: string, table: string, columns: DbNewColumn[]) {
  const pks = columns.filter((c) => c.primaryKey)
  const lines = columns.map((c) => "  " + columnLine(c, pks.length === 1))
  if (pks.length > 1) lines.push(`  PRIMARY KEY (${pks.map((c) => c.name).join(", ")})`)
  return `CREATE TABLE ${qualify(schema, table)} (\n${lines.join(",\n")}\n)`
}

export function AddColumnDialog({
  open,
  onOpenChange,
  connId,
  schema,
  table,
  info,
  onDone,
}: {
  open: boolean
  onOpenChange: (o: boolean) => void
  connId: number
  schema: string
  table: string
  info?: DbDriverInfo
  onDone: () => void
}) {
  const id = useId()
  const [col, setCol] = useState<DbNewColumn>({ ...EMPTY_COLUMN })
  const [busy, setBusy] = useState(false)
  const valid = Boolean(col.name.trim() && col.type.trim())

  const submit = async () => {
    setBusy(true)
    try {
      const column = { ...col, name: col.name.trim(), type: col.type.trim() }
      const res = await post<{ statement: string }>(`/databases/${connId}/ddl/column`, {
        schema,
        table,
        column,
      })
      notify.success(`Added ${column.name}`, { description: res.statement })
      onOpenChange(false)
      setCol({ ...EMPTY_COLUMN })
      onDone()
    } catch (err) {
      notify.error("Could not add the column", err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      title="Add column"
      description={`Adds a column to ${table}. The statement is shown before it runs.`}
      footer={
        <>
          <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={!valid || busy} pending={busy}>
            Add column
          </Button>
        </>
      }
    >
      <div className="grid gap-5">
        <FormFacts>
          <FormFact label="Table" mono>
            {qualify(schema, table)}
          </FormFact>
        </FormFacts>
        <FieldRow>
          <Field label="Name" htmlFor={`${id}-name`}>
            <Input
              id={`${id}-name`}
              value={col.name}
              onChange={(e) => setCol({ ...col, name: e.target.value })}
              className="font-mono"
              placeholder="created_at"
              autoFocus
            />
          </Field>
          <Field label="Type" htmlFor={`${id}-type`}>
            <TypeField
              id={`${id}-type`}
              types={info?.columnTypes ?? []}
              value={col.type}
              onChange={(v) => setCol({ ...col, type: v })}
            />
          </Field>
        </FieldRow>
        <Field
          label="Default"
          htmlFor={`${id}-default`}
          hint="A SQL expression, quoted if it is a string: 'draft', 0, now(). Leave it empty for none."
        >
          <Input
            id={`${id}-default`}
            value={col.default ?? ""}
            onChange={(e) => setCol({ ...col, default: e.target.value })}
            className="font-mono"
          />
        </Field>
        <OptionList>
          <OptionRow
            title="Required"
            hint="NOT NULL — every row must carry a value. Existing rows take the default."
            checked={Boolean(col.notNull)}
            onCheckedChange={(v) => setCol({ ...col, notNull: v })}
          />
        </OptionList>
        {col.notNull && !col.default && (
          <FormNote tone="warning">
            A required column added to a table that already has rows needs a default, or every
            existing row would violate it.
          </FormNote>
        )}
        <Statement
          sql={
            valid
              ? `ALTER TABLE ${qualify(schema, table)}\n  ADD COLUMN ${columnLine(
                  { ...col, name: col.name.trim(), type: col.type.trim() },
                  false,
                )}`
              : ""
          }
          placeholder="Give the column a name and a type."
        />
      </div>
    </Modal>
  )
}

export function CreateIndexDialog({
  open,
  onOpenChange,
  connId,
  schema,
  table,
  detail,
  onDone,
}: {
  open: boolean
  onOpenChange: (o: boolean) => void
  connId: number
  schema: string
  table: string
  detail?: DbTableDetail | null
  onDone: () => void
}) {
  const id = useId()
  const [typed, setTyped] = useState("")
  const [fields, setFields] = useState<string[]>([])
  const [unique, setUnique] = useState(false)
  const [busy, setBusy] = useState(false)

  // The name is suggested from the columns until the operator types one, so
  // the ordinary index is two clicks and the unusual one is still nameable.
  const suggested = fields.length ? `idx_${table}_${fields.join("_")}` : ""
  const name = typed.trim() || suggested

  const toggle = (col: string) =>
    setFields((f) => (f.includes(col) ? f.filter((c) => c !== col) : [...f, col]))
  const move = (col: string, by: -1 | 1) =>
    setFields((f) => {
      const i = f.indexOf(col)
      const j = i + by
      if (i < 0 || j < 0 || j >= f.length) return f
      const next = [...f]
      ;[next[i], next[j]] = [next[j], next[i]]
      return next
    })

  const submit = async () => {
    setBusy(true)
    try {
      const res = await post<{ statement: string }>(`/databases/${connId}/ddl/index`, {
        schema,
        table,
        name,
        fields,
        unique,
      })
      notify.success(`Created ${name}`, { description: res.statement })
      onOpenChange(false)
      setTyped("")
      setFields([])
      onDone()
    } catch (err) {
      notify.error("Could not create the index", err)
    } finally {
      setBusy(false)
    }
  }

  const valid = Boolean(name) && fields.length > 0

  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      title="Create index"
      description={`Indexes ${table} on the columns you pick, in that order.`}
      footer={
        <>
          <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={!valid || busy} pending={busy}>
            Create index
          </Button>
        </>
      }
    >
      <div className="grid gap-5">
        <FormFacts>
          <FormFact label="Table" mono>
            {qualify(schema, table)}
          </FormFact>
        </FormFacts>

        <FormSection
          title="Columns"
          hint="Order matters: the index answers queries that filter on its first column first."
        >
          <div className="min-w-0 divide-y divide-hairline">
            {detail?.columns.map((c) => {
              const at = fields.indexOf(c.name)
              const picked = at >= 0
              return (
                <div key={c.name} className="flex items-center gap-3 py-1.5">
                  <label className="flex min-w-0 flex-1 cursor-pointer items-center gap-3">
                    <Checkbox checked={picked} onCheckedChange={() => toggle(c.name)} />
                    <span className="min-w-0 truncate font-mono text-xs">{c.name}</span>
                    <span className="truncate text-hint text-muted-foreground">
                      {c.type.toLowerCase()}
                    </span>
                  </label>
                  {picked && (
                    <span className="flex shrink-0 items-center gap-0.5">
                      <span className="numeric w-5 text-right text-hint text-muted-foreground">
                        {at + 1}
                      </span>
                      <IconAction
                        label="Move up"
                        disabled={at === 0}
                        onClick={() => move(c.name, -1)}
                      >
                        <ArrowUp />
                      </IconAction>
                      <IconAction
                        label="Move down"
                        disabled={at === fields.length - 1}
                        onClick={() => move(c.name, 1)}
                      >
                        <ArrowDown />
                      </IconAction>
                    </span>
                  )}
                </div>
              )
            })}
            {(!detail || detail.columns.length === 0) && (
              <FormNote className="py-2">No columns are readable on this table.</FormNote>
            )}
          </div>
        </FormSection>

        <Field
          label="Index name"
          htmlFor={`${id}-name`}
          hint={
            typed.trim() ? undefined : "Suggested from the columns; type your own to override it."
          }
        >
          <Input
            id={`${id}-name`}
            value={typed}
            onChange={(e) => setTyped(e.target.value)}
            className="font-mono"
            placeholder={suggested || `idx_${table}_…`}
          />
        </Field>

        <OptionList>
          <OptionRow
            title="Unique"
            hint="Refuses two rows with the same values in these columns."
            checked={unique}
            onCheckedChange={setUnique}
          />
        </OptionList>

        <Statement
          sql={
            valid
              ? `CREATE ${unique ? "UNIQUE " : ""}INDEX ${name}\n  ON ${qualify(schema, table)} (${fields.join(", ")})`
              : ""
          }
          placeholder="Pick at least one column."
        />
        <FormNote>
          Building an index can lock or rewrite a large table. On a busy server, do it when you can
          afford the write.
        </FormNote>
      </div>
    </Modal>
  )
}

export function RenameDialog({
  open,
  onOpenChange,
  connId,
  schema,
  table,
  kind,
  current,
  onDone,
}: {
  open: boolean
  onOpenChange: (o: boolean) => void
  connId: number
  schema: string
  table: string
  kind: "table" | "column"
  current: string
  // Handed the new name: a caller holding a selection has to follow the
  // rename, or it goes on asking the server for a table that no longer exists.
  onDone: (to: string) => void
}) {
  const id = useId()
  const [to, setTo] = useState(current)
  const [busy, setBusy] = useState(false)
  const next = to.trim()
  const valid = Boolean(next) && next !== current

  const submit = async () => {
    setBusy(true)
    try {
      await post(`/databases/${connId}/ddl/rename`, {
        schema,
        table,
        kind,
        name: kind === "column" ? current : "",
        to: next,
      })
      notify.success(`Renamed to ${next}`)
      onOpenChange(false)
      onDone(next)
    } catch (err) {
      notify.error("Could not rename", err)
    } finally {
      setBusy(false)
    }
  }

  const statement = valid
    ? kind === "table"
      ? `ALTER TABLE ${qualify(schema, table)}\n  RENAME TO ${next}`
      : `ALTER TABLE ${qualify(schema, table)}\n  RENAME COLUMN ${current} TO ${next}`
    : ""

  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      size="sm"
      title={kind === "table" ? "Rename table" : "Rename column"}
      description={`Gives ${current} a new name.`}
      footer={
        <>
          <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={!valid || busy} pending={busy}>
            Rename
          </Button>
        </>
      }
    >
      <div className="grid gap-5">
        <FormFacts>
          <FormFact label={kind === "table" ? "Table" : "Column"} mono>
            {kind === "table" ? qualify(schema, current) : `${qualify(schema, table)}.${current}`}
          </FormFact>
        </FormFacts>
        <Field
          label="New name"
          htmlFor={`${id}-to`}
          hint={
            kind === "table"
              ? "Views, foreign keys and code that name the old table will need updating."
              : "Queries and code that name the old column will need updating."
          }
        >
          <Input
            id={`${id}-to`}
            value={to}
            onChange={(e) => setTo(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && valid && !busy && void submit()}
            className="font-mono"
            autoFocus
          />
        </Field>
        <Statement sql={statement} placeholder="Type a different name." />
      </div>
    </Modal>
  )
}
