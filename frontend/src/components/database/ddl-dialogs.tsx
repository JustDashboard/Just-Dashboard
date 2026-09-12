"use client"

import { useState } from "react"
import { Check, ChevronDown, Key, Plus, Star, Trash } from "@/components/icons"
import { notify } from "@/lib/toast"
import { post } from "@/lib/api"
import { plural } from "@/lib/format"
import { cn, ringSafeScroll } from "@/lib/utils"
import type { DbDriverInfo, DbNewColumn, DbTableDetail } from "@/lib/types"
import { IconAction } from "@/components/icon-action"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"

import { Well } from "@/components/panel"
import { Modal } from "@/components/modal"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command"

/**
 * The schema-editing forms.
 *
 * Every one of them shows the statement it will run before it runs it. A DDL
 * form that hides its SQL asks the operator to trust a black box with their
 * schema; showing it costs a few lines and turns the form into something you
 * can also learn from. The server builds the real statement — these previews
 * are rendered from the same fields and are labelled as approximations only
 * where they cannot be exact.
 */

const EMPTY_COLUMN: DbNewColumn = {
  name: "",
  type: "",
  notNull: false,
  primaryKey: false,
  default: "",
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
  const [table, setTable] = useState("")
  const [columns, setColumns] = useState<DbNewColumn[]>([{ ...EMPTY_COLUMN }])
  const [busy, setBusy] = useState(false)

  const types = info?.columnTypes ?? []
  const setCol = (i: number, patch: Partial<DbNewColumn>) =>
    setColumns((cs) => cs.map((c, j) => (j === i ? { ...c, ...patch } : c)))

  const submit = async () => {
    setBusy(true)
    try {
      const res = await post<{ statement: string }>(`/databases/${connId}/ddl/table`, {
        schema,
        table,
        columns: columns.filter((c) => c.name && c.type),
      })
      notify.success(`Created ${table}`, { description: res.statement })
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

  const valid = table.trim() !== "" && columns.some((c) => c.name && c.type)

  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      size="xl"
      title="Create table"
      description={
        <>
          In <span className="font-mono text-xs">{schema || "the default schema"}</span>. The
          statement this builds is shown below before anything runs.
        </>
      }
      footer={
        <>
          <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={!valid || busy} pending={busy}>
            Create table
          </Button>
        </>
      }
    >
      <div className="grid gap-4">
        <div className="space-y-1.5">
          <Label htmlFor="ddl-table">Table name</Label>
          <Input
            id="ddl-table"
            value={table}
            onChange={(e) => setTable(e.target.value)}
            className="font-mono text-xs"
            autoFocus
          />
        </div>

        <div className="space-y-2">
          <div className="flex items-center justify-between">
            <Label>Columns</Label>
            <span className="text-hint text-muted-foreground">
              {plural(columns.filter((c) => c.name && c.type).length, "column")} defined
            </span>
          </div>

          {/* A header row, because four unlabelled fields in a line is a
                puzzle. The widths are shared with the rows below through the
                same grid template, so they stay aligned as the dialog resizes. */}
          <div className={cn(COLUMN_GRID, "px-1 pb-1")}>
            <span className="text-micro font-medium tracking-wide text-muted-foreground uppercase">
              Name
            </span>
            <span className="text-micro font-medium tracking-wide text-muted-foreground uppercase">
              Type
            </span>
            <span className="text-micro font-medium tracking-wide text-muted-foreground uppercase">
              Default
            </span>
            <span className="text-center text-micro font-medium tracking-wide text-muted-foreground uppercase">
              Key
            </span>
            {/* No header over the remove button: a column of one icon needs
                  no title, and giving it one made "Key" look like it belonged
                  to the wrong pair of controls. */}
            <span className="w-8" />
          </div>

          <div className={cn("max-h-64 space-y-1.5 overflow-y-auto", ringSafeScroll)}>
            {columns.map((c, i) => (
              <div key={i} className={COLUMN_GRID}>
                <Input
                  placeholder="id"
                  value={c.name}
                  onChange={(e) => setCol(i, { name: e.target.value })}
                  className="h-8 font-mono text-xs"
                />
                <TypePicker types={types} value={c.type} onChange={(v) => setCol(i, { type: v })} />
                <Input
                  placeholder="—"
                  value={c.default ?? ""}
                  onChange={(e) => setCol(i, { default: e.target.value })}
                  className="h-8 font-mono text-xs"
                  title="A SQL expression, quoted if it is a string: 'none', 0, now()"
                />
                {/* Toggles rather than checkboxes with two-letter labels:
                      "pk" and "req" are abbreviations of abbreviations, and at
                      this size the box and its word were two targets to hit. */}
                <div className="flex items-center gap-1">
                  <FlagToggle
                    on={Boolean(c.primaryKey)}
                    onClick={() => setCol(i, { primaryKey: !c.primaryKey })}
                    title="Primary key"
                  >
                    <Key className="size-3" />
                    PK
                  </FlagToggle>
                  <FlagToggle
                    on={Boolean(c.notNull)}
                    onClick={() => setCol(i, { notNull: !c.notNull })}
                    title="Not null — every row must have a value"
                  >
                    <Star className="size-3" />
                    Req
                  </FlagToggle>
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
            ))}
          </div>
          <Button
            size="sm"
            variant="outline"
            onClick={() => setColumns((cs) => [...cs, { ...EMPTY_COLUMN }])}
          >
            <Plus className="size-3.5" />
            Add column
          </Button>
        </div>

        <div className="space-y-1.5">
          <Label className="text-hint tracking-wide text-muted-foreground uppercase">
            Statement
          </Label>
          {/* whitespace-pre-wrap, because the statement's shape — one column
                per line — is most of what makes it readable at a glance. */}
          <Well className="max-h-32 overflow-auto font-mono text-hint leading-relaxed whitespace-pre-wrap">
            {valid ? (
              previewCreate(schema, table, columns)
            ) : (
              <span className="text-muted-foreground italic">
                Name the table and give at least one column a name and a type.
              </span>
            )}
          </Well>
        </div>
      </div>
    </Modal>
  )
}

/**
 * The column type: an editable field welded to a searchable list of the
 * engine's own types.
 *
 * Both halves are load-bearing. The engine's list is templates —
 * `varchar(255)`, `numeric(10,2)`, `enum('a','b')` — so picking one is a start,
 * not an answer, and the field has to stay editable to change the number. The
 * list is what makes `timestamptz` or `jsonb` a click rather than a spelling
 * test, and it is searchable because twenty-odd names including "double
 * precision" is past the point a plain menu is faster than typing. Anything
 * typed that is not on the list is still sent — the server validates it — so an
 * unusual but legitimate type is never unreachable.
 */
function TypePicker({
  types,
  value,
  onChange,
}: {
  types: string[]
  value: string
  onChange: (v: string) => void
}) {
  const [open, setOpen] = useState(false)
  return (
    <div className="flex h-8 w-full min-w-0 items-center rounded-md border bg-transparent transition-[color,box-shadow] focus-within:border-ring focus-within:ring-[3px] focus-within:ring-ring/50">
      <input
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder="text"
        aria-label="Column type"
        className="h-full min-w-0 flex-1 bg-transparent px-2 font-mono text-xs focus-ring placeholder:text-muted-foreground"
      />
      {types.length > 0 && (
        <Popover open={open} onOpenChange={setOpen}>
          <PopoverTrigger asChild>
            <button
              type="button"
              aria-label="Browse types"
              className="flex h-full w-7 shrink-0 items-center justify-center rounded-r-[5px] border-l border-hairline text-muted-foreground transition-colors hover:bg-accent hover:text-foreground data-[state=open]:bg-accent data-[state=open]:text-foreground"
            >
              <ChevronDown className="size-3.5" />
            </button>
          </PopoverTrigger>
          <PopoverContent align="end" className="w-52 p-0">
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

function previewCreate(schema: string, table: string, columns: DbNewColumn[]) {
  const rel = schema ? `${schema}.${table}` : table
  const pks = columns.filter((c) => c.primaryKey && c.name)
  const lines = columns
    .filter((c) => c.name && c.type)
    .map((c) => {
      let l = `  ${c.name} ${c.type}`
      if (c.default) l += ` DEFAULT ${c.default}`
      if (c.notNull) l += " NOT NULL"
      if (pks.length === 1 && c.primaryKey) l += " PRIMARY KEY"
      return l
    })
  if (pks.length > 1) lines.push(`  PRIMARY KEY (${pks.map((c) => c.name).join(", ")})`)
  return `CREATE TABLE ${rel} (\n${lines.join(",\n")}\n)`
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
  const [col, setCol] = useState<DbNewColumn>({ ...EMPTY_COLUMN })
  const [busy, setBusy] = useState(false)

  const submit = async () => {
    setBusy(true)
    try {
      const res = await post<{ statement: string }>(`/databases/${connId}/ddl/column`, {
        schema,
        table,
        column: col,
      })
      notify.success(`Added ${col.name}`, { description: res.statement })
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
      size="sm"
      title={<>Add column to {table}</>}
      footer={
        <>
          <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={!col.name || !col.type || busy} pending={busy}>
            Add column
          </Button>
        </>
      }
    >
      <div className="grid gap-3">
        <div className="space-y-1.5">
          <Label>Name</Label>
          <Input
            value={col.name}
            onChange={(e) => setCol({ ...col, name: e.target.value })}
            className="font-mono text-xs"
            autoFocus
          />
        </div>
        <div className="space-y-1.5">
          <Label>Type</Label>
          <TypePicker
            types={info?.columnTypes ?? []}
            value={col.type}
            onChange={(v) => setCol({ ...col, type: v })}
          />
        </div>
        <div className="space-y-1.5">
          <Label>Default</Label>
          <Input
            value={col.default ?? ""}
            onChange={(e) => setCol({ ...col, default: e.target.value })}
            className="font-mono text-xs"
            placeholder="literal, e.g. 0 or 'draft'"
          />
        </div>
        <label className="flex items-center gap-2 text-xs">
          <Checkbox
            checked={col.notNull}
            onCheckedChange={(v) => setCol({ ...col, notNull: Boolean(v) })}
          />
          Required (NOT NULL)
        </label>
        {col.notNull && !col.default && (
          <p className="text-xs text-destructive">
            A required column added to a table with existing rows needs a default, or every existing
            row would violate it.
          </p>
        )}
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
  const [name, setName] = useState("")
  const [fields, setFields] = useState<string[]>([])
  const [unique, setUnique] = useState(false)
  const [busy, setBusy] = useState(false)

  const toggle = (col: string) =>
    setFields((f) => (f.includes(col) ? f.filter((c) => c !== col) : [...f, col]))

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
      setName("")
      setFields([])
      onDone()
    } catch (err) {
      notify.error("Could not create the index", err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      size="sm"
      title={<>Create index on {table}</>}
      footer={
        <>
          <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={!name || fields.length === 0 || busy} pending={busy}>
            Create index
          </Button>
        </>
      }
    >
      <div className="grid gap-3">
        <div className="space-y-1.5">
          <Label>Index name</Label>
          <Input
            value={name}
            onChange={(e) => setName(e.target.value)}
            className="font-mono text-xs"
            placeholder={`idx_${table}_…`}
            autoFocus
          />
        </div>
        <div className="space-y-1.5">
          <Label>Columns — order matters</Label>
          <div className="max-h-48 space-y-0.5 overflow-y-auto rounded-md border border-hairline p-1.5">
            {detail?.columns.map((c) => (
              <label
                key={c.name}
                className="flex cursor-pointer items-center gap-2 rounded-sm px-1.5 py-1 text-xs hover:bg-accent"
              >
                <Checkbox
                  checked={fields.includes(c.name)}
                  onCheckedChange={() => toggle(c.name)}
                />
                <span className="font-mono">{c.name}</span>
                <span className="text-muted-foreground">{c.type.toLowerCase()}</span>
                {fields.includes(c.name) && (
                  <span className="ml-auto text-micro text-primary">
                    #{fields.indexOf(c.name) + 1}
                  </span>
                )}
              </label>
            ))}
          </div>
        </div>
        <label className="flex items-center gap-2 text-xs">
          <Checkbox checked={unique} onCheckedChange={(v) => setUnique(Boolean(v))} />
          Unique
        </label>
        <p className="text-xs text-muted-foreground">
          Building an index can lock or rewrite a large table. On a busy server, do it when you can
          afford the write.
        </p>
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
  const [to, setTo] = useState(current)
  const [busy, setBusy] = useState(false)

  const submit = async () => {
    setBusy(true)
    try {
      await post(`/databases/${connId}/ddl/rename`, {
        schema,
        table,
        kind,
        name: kind === "column" ? current : "",
        to,
      })
      notify.success(`Renamed to ${to}`)
      onOpenChange(false)
      onDone(to)
    } catch (err) {
      notify.error("Could not rename", err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      size="sm"
      title={<>Rename {kind}</>}
      footer={
        <>
          <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={!to || to === current || busy} pending={busy}>
            Rename
          </Button>
        </>
      }
    >
      <div className="space-y-1.5">
        <Label>New name for {current}</Label>
        <Input
          value={to}
          onChange={(e) => setTo(e.target.value)}
          className="font-mono text-xs"
          autoFocus
        />
      </div>
    </Modal>
  )
}

/**
 * The shared grid for the column editor's header and its rows.
 *
 * The last two tracks are fixed rather than `auto`, which is what the header
 * labels were drifting on: `auto` sizes to content, and the header's content
 * ("Key", and nothing over the remove button) is far narrower than the row's
 * two toggles and a button. The two grids then divided a different amount of
 * leftover space between the three `fr` tracks, so each label sat a little
 * further right of its field than the one before — which reads as labels
 * floating in the middle of nothing.
 */
const COLUMN_GRID =
  "grid grid-cols-[minmax(0,1.1fr)_minmax(0,1.1fr)_minmax(0,0.9fr)_6.75rem_2rem] items-center gap-2"

function FlagToggle({
  on,
  onClick,
  title,
  children,
}: {
  on: boolean
  onClick: () => void
  title: string
  children: React.ReactNode
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      title={title}
      aria-pressed={on}
      className={cn(
        "inline-flex h-8 items-center gap-1 rounded-md border px-2 text-micro font-medium tracking-wide uppercase transition-colors",
        on
          ? "border-primary bg-plot-primary text-primary"
          : "border-border bg-control text-muted-foreground hover:bg-control-hover",
      )}
    >
      {children}
    </button>
  )
}
