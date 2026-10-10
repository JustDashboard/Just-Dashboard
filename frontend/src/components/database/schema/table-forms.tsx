"use client"

import { useId, useMemo, useState } from "react"
import { ArrowRight, Plus, Trash } from "@/components/icons"
import { useAuth } from "@/hooks/use-auth"
import { Segments } from "@/components/deploy/settings/segments"
import { Field, FieldRow, FormFact, FormNote, OptionList, OptionRow } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Textarea } from "@/components/ui/textarea"
import { useCatalog, useTableDetail } from "@/components/database/data/use-table"
import { useDatabase } from "@/components/database/shell/database-context"
import { ChangeDialog } from "@/components/database/schema/change-dialog"
import {
  BLANK_INDEX,
  addColumnRequest,
  alterColumnRequest,
  columnChanges,
  columnEdit,
  commentRequest,
  constraintRequest,
  foreignKeyRequest,
  indexRequest,
  qualified,
  renameColumnRequest,
  renameTableRequest,
  type ConstraintDraft,
  type ForeignKeyDraft,
  type IndexDraft,
} from "@/components/database/schema/changes"
import { notesFor, type ReferenceAction } from "@/components/database/schema/engine-notes"
import { ColumnChips, DEFAULT_HINT, TypeField } from "@/components/database/schema/fields"
import type { DbColumn, DbTableDetail, DdlAnswer } from "@/components/database/schema/types"
import { useSupport } from "@/components/database/schema/use-ddl"

/**
 * The forms that change a table that exists: its columns, its indexes, its
 * keys and constraints, its name and its comment.
 *
 * Each is one `ChangeDialog` — the table it acts on, the fields, the server's
 * statement for what the fields say, the one command. Which of them a page
 * may open is decided where their buttons are drawn (the engine's
 * `ddlOperations`, the role, a protected connection); a form here only asks
 * the engine about the options no flag describes.
 */

type TableFormProps = {
  detail: DbTableDetail
  onClose: () => void
  /** The change ran: what was read of the table is stale. */
  onDone: (answer: DdlAnswer) => void
}

const tableOf = (detail: DbTableDetail) => qualified(detail.schema, detail.name)

export function AddColumnDialog({ detail, onClose, onDone }: TableFormProps) {
  const ids = useId()
  const [draft, setDraft] = useState({ name: "", type: "", notNull: false, default: "" })
  const request = addColumnRequest(detail.schema, detail.name, draft)
  const taken = detail.columns.some(
    (column) => column.name.toLowerCase() === draft.name.trim().toLowerCase(),
  )
  return (
    <ChangeDialog
      onClose={onClose}
      onDone={onDone}
      title="Add column"
      subject={{
        name: tableOf(detail),
        facts: <FormFact label="Columns">{detail.columns.length}</FormFact>,
      }}
      request={taken ? null : request}
      waiting={
        taken
          ? "This table already has a column of that name."
          : "Name the column and give it a type."
      }
      command="Add column"
      done={`Added ${draft.name.trim()} to ${detail.name}`}
      dirty={draft.name !== "" || draft.type !== "" || draft.default !== ""}
    >
      <FieldRow>
        <Field
          label="Name"
          htmlFor={`${ids}-name`}
          error={taken ? "This table already has a column of that name." : undefined}
        >
          <Input
            id={`${ids}-name`}
            autoFocus
            autoComplete="off"
            spellCheck={false}
            className="font-mono text-xs"
            value={draft.name}
            aria-invalid={taken || undefined}
            onChange={(event) => setDraft({ ...draft, name: event.target.value })}
          />
        </Field>
        <Field label="Type" htmlFor={`${ids}-type`}>
          <TypeField
            id={`${ids}-type`}
            value={draft.type}
            onChange={(type) => setDraft({ ...draft, type })}
          />
        </Field>
      </FieldRow>
      <Field label="Default" htmlFor={`${ids}-default`} hint={DEFAULT_HINT}>
        <Input
          id={`${ids}-default`}
          autoComplete="off"
          spellCheck={false}
          className="font-mono text-xs"
          placeholder="none"
          value={draft.default}
          onChange={(event) => setDraft({ ...draft, default: event.target.value })}
        />
      </Field>
      <OptionList>
        <OptionRow
          title="Refuse NULL in this column"
          hint={
            detail.estimatedRows !== 0
              ? "Rows the table already holds need the default above to take."
              : undefined
          }
          checked={draft.notNull}
          onCheckedChange={(notNull) => setDraft({ ...draft, notNull })}
        />
      </OptionList>
    </ChangeDialog>
  )
}

export function EditColumnDialog({
  detail,
  column,
  onClose,
  onDone,
}: TableFormProps & { column: DbColumn }) {
  const ids = useId()
  const { id } = useDatabase()
  const { can } = useAuth()
  // A type change rewrites the column, and the server asks more of the role for it.
  const mayRetype = can("destructive")
  const [edit, setEdit] = useState(() => columnEdit(column))
  const changes = columnChanges(column, edit)
  const request = alterColumnRequest(detail.schema, detail.name, column, edit)
  const where = `${detail.schema}.${detail.name}.${column.name}`
  // Two of the engine's own limits no flag states: nullability that is part
  // of the type, and a conversion expression only one engine takes.
  const nullability = useSupport(id, `nullable:${where}`, {
    method: "PATCH",
    path: "/ddl/column",
    body: {
      schema: detail.schema,
      table: detail.name,
      name: column.name,
      nullable: column.nullable,
    },
  })
  const conversion = useSupport(
    id,
    "alter-using",
    mayRetype
      ? {
          method: "PATCH",
          path: "/ddl/column",
          body: {
            schema: detail.schema,
            table: detail.name,
            name: column.name,
            type: column.type,
            using: "NULL",
          },
        }
      : null,
  )
  const risky = changes.type || (changes.nullable && !edit.nullable)

  return (
    <ChangeDialog
      onClose={onClose}
      onDone={onDone}
      title="Edit column"
      subject={{
        name: `${tableOf(detail)}.${column.name}`,
        facts: (
          <>
            <FormFact label="Type" mono>
              {column.type}
            </FormFact>
            <FormFact label="NULL">{column.nullable ? "allowed" : "refused"}</FormFact>
            {column.default !== undefined && (
              <FormFact label="Default" mono>
                {column.default}
              </FormFact>
            )}
          </>
        ),
      }}
      request={request}
      waiting="Change the type, whether it takes NULL, or the default."
      command="Change column"
      done={`Changed ${column.name} of ${detail.name}`}
      dirty={request !== null}
      confirm={
        risky
          ? {
              title: changes.type
                ? `This rewrites ${column.name} in every row`
                : `This fails if a row holds NULL in ${column.name}`,
              description: (
                <>
                  {changes.type && (
                    <p>
                      The engine converts every stored value to the new type and refuses the whole
                      change if one cannot be converted. The table is locked while it does.
                    </p>
                  )}
                  {changes.nullable && !edit.nullable && (
                    <p>
                      Refusing NULL checks every row: one that holds NULL in this column today stops
                      the change.
                    </p>
                  )}
                </>
              ),
              question: changes.type
                ? `Rewrite ${column.name} in every row of ${detail.name}?`
                : `Refuse NULL in ${column.name} from now on?`,
            }
          : null
      }
    >
      {mayRetype ? (
        <Field label="Type" htmlFor={`${ids}-type`}>
          <TypeField
            id={`${ids}-type`}
            value={edit.type}
            onChange={(type) => setEdit({ ...edit, type })}
          />
        </Field>
      ) : (
        <FormNote>
          The type stays {column.type}: changing a type rewrites the column, and your role does not
          permit that.
        </FormNote>
      )}
      {changes.type && conversion?.supported && (
        <Field
          label="Convert the stored values with"
          htmlFor={`${ids}-using`}
          hint="An expression over the column, such as qty::integer. Empty: the engine's own cast."
        >
          <Input
            id={`${ids}-using`}
            autoComplete="off"
            spellCheck={false}
            className="font-mono text-xs"
            value={edit.using}
            onChange={(event) => setEdit({ ...edit, using: event.target.value })}
          />
        </Field>
      )}
      <Field label="Default" htmlFor={`${ids}-default`} hint={`${DEFAULT_HINT} Empty: no default.`}>
        <Input
          id={`${ids}-default`}
          autoComplete="off"
          spellCheck={false}
          className="font-mono text-xs"
          placeholder="none"
          value={edit.default}
          onChange={(event) => setEdit({ ...edit, default: event.target.value })}
        />
      </Field>
      {nullability?.supported === false ? (
        <FormNote>{nullability.reason}</FormNote>
      ) : (
        <OptionList>
          <OptionRow
            title="Allow NULL in this column"
            checked={edit.nullable}
            onCheckedChange={(nullable) => setEdit({ ...edit, nullable })}
          />
        </OptionList>
      )}
    </ChangeDialog>
  )
}

/** Renames a column of the table, or the table itself when no column is named. */
export function RenameDialog({
  detail,
  column,
  onClose,
  onDone,
  onRenamed,
}: TableFormProps & {
  column?: string
  /** The name it has now, for a page whose address holds the old one. */
  onRenamed?: (to: string) => void
}) {
  const ids = useId()
  const current = column ?? detail.name
  const [to, setTo] = useState(current)
  const request = column
    ? renameColumnRequest(detail.schema, detail.name, column, to)
    : renameTableRequest(detail.schema, detail.name, to)
  const taken =
    column !== undefined &&
    to.trim() !== column &&
    detail.columns.some((other) => other.name.toLowerCase() === to.trim().toLowerCase())
  const what = column ? "column" : "table"
  return (
    <ChangeDialog
      onClose={onClose}
      onDone={(answer) => {
        onRenamed?.(to.trim())
        onDone(answer)
      }}
      title={`Rename ${what}`}
      subject={{ name: column ? `${tableOf(detail)}.${column}` : tableOf(detail) }}
      request={taken ? null : request}
      waiting={taken ? "This table already has a column of that name." : "Type the new name."}
      command="Rename"
      done={`Renamed ${current} to ${to.trim()}`}
      dirty={to !== current}
    >
      <Field
        label="New name"
        htmlFor={`${ids}-to`}
        error={taken ? "This table already has a column of that name." : undefined}
        hint={
          column
            ? "Views, functions and applications that name the column are not rewritten."
            : "Views, functions and applications that name the table are not rewritten."
        }
      >
        <Input
          id={`${ids}-to`}
          autoFocus
          autoComplete="off"
          spellCheck={false}
          className="font-mono text-xs"
          value={to}
          aria-invalid={taken || undefined}
          onFocus={(event) => event.target.select()}
          onChange={(event) => setTo(event.target.value)}
        />
      </Field>
    </ChangeDialog>
  )
}

/** The comment on a table, or on one of its columns. Emptied, it is removed. */
export function CommentDialog({
  detail,
  column,
  onClose,
  onDone,
}: TableFormProps & { column?: DbColumn }) {
  const ids = useId()
  const before = (column ? column.comment : detail.comment) ?? ""
  const [comment, setComment] = useState(before)
  const request = commentRequest(detail.schema, detail.name, column?.name, comment, before)
  const name = column ? `${tableOf(detail)}.${column.name}` : tableOf(detail)
  return (
    <ChangeDialog
      onClose={onClose}
      onDone={onDone}
      title={before ? "Edit comment" : "Add comment"}
      subject={{ name }}
      request={request}
      waiting={before ? "Change the comment, or empty it to remove it." : "Write the comment."}
      command={comment.trim() === "" && before ? "Remove comment" : "Save comment"}
      done={`Comment on ${column ? column.name : detail.name} saved`}
      dirty={comment !== before}
    >
      <Field
        label="Comment"
        htmlFor={`${ids}-comment`}
        hint="Kept in the database itself, so every tool that reads the schema sees it."
      >
        <Textarea
          id={`${ids}-comment`}
          autoFocus
          className="min-h-20"
          value={comment}
          maxLength={4000}
          onChange={(event) => setComment(event.target.value)}
        />
      </Field>
    </ChangeDialog>
  )
}

export function AddIndexDialog({ detail, onClose, onDone }: TableFormProps) {
  const ids = useId()
  const { engine } = useDatabase()
  const operations = engine.capabilities.ddlOperations
  const [draft, setDraft] = useState<IndexDraft>(BLANK_INDEX)
  const request = indexRequest(detail.schema, detail.name, draft)
  const columns = useMemo(() => detail.columns.map((column) => column.name), [detail.columns])
  const set = (patch: Partial<IndexDraft>) => setDraft({ ...draft, ...patch })
  const notes = notesFor(engine)
  // The method is being typed rather than chosen: an access method an
  // extension installed, where the engine takes any it knows.
  const [other, setOther] = useState(false)
  return (
    <ChangeDialog
      onClose={onClose}
      onDone={onDone}
      title="Add index"
      subject={{
        name: tableOf(detail),
        facts:
          detail.estimatedRows >= 0 ? (
            <FormFact label="Rows">about {detail.estimatedRows.toLocaleString("en-US")}</FormFact>
          ) : undefined,
      }}
      request={request}
      waiting="Choose the columns the index is over, in the order it should sort them."
      command="Create index"
      done={`Index created on ${detail.name}`}
      dirty={draft.fields.length > 0 || draft.name !== "" || draft.where !== ""}
      size="lg"
    >
      <Field
        label="Columns"
        hint={
          draft.fields.length > 1
            ? "The order is the order pressed: the first column is the one lookups must name."
            : undefined
        }
      >
        <ColumnChips
          label="Columns of the index"
          columns={columns}
          chosen={draft.fields}
          onChange={(fields) => set({ fields })}
        />
      </Field>
      <FieldRow>
        <Field
          label="Name"
          htmlFor={`${ids}-name`}
          hint="Empty: the server names it after its columns."
        >
          <Input
            id={`${ids}-name`}
            autoComplete="off"
            spellCheck={false}
            className="font-mono text-xs"
            value={draft.name}
            onChange={(event) => set({ name: event.target.value })}
          />
        </Field>
        {operations.includes("indexMethod") && notes.indexMethods.length > 0 && (
          <Field
            label="Method"
            htmlFor={`${ids}-method`}
            hint={
              notes.otherIndexMethods && other
                ? "The access method's name, as the engine knows it."
                : undefined
            }
          >
            <MethodField
              id={`${ids}-method`}
              methods={notes.indexMethods}
              open={notes.otherIndexMethods}
              value={draft.method}
              other={other}
              onChange={(method, typed) => {
                setOther(typed)
                set({ method })
              }}
            />
          </Field>
        )}
      </FieldRow>
      {operations.includes("indexPartial") && (
        <Field
          label="Only the rows where"
          htmlFor={`${ids}-where`}
          hint="A condition, such as status = 'pending'. Empty: every row."
        >
          <Input
            id={`${ids}-where`}
            autoComplete="off"
            spellCheck={false}
            className="font-mono text-xs"
            value={draft.where}
            onChange={(event) => set({ where: event.target.value })}
          />
        </Field>
      )}
      <OptionList>
        <OptionRow
          title="Unique: refuse two rows with the same values in these columns"
          checked={draft.unique}
          onCheckedChange={(unique) => set({ unique })}
        />
        {operations.includes("indexConcurrently") && (
          <OptionRow
            title="Build it without blocking writes to the table"
            hint="Slower to build."
            checked={draft.concurrently}
            onCheckedChange={(concurrently) => set({ concurrently })}
          />
        )}
        {operations.includes("indexIfNotExists") && (
          <OptionRow
            title="Do nothing if an index of this name is already there"
            checked={draft.ifNotExists}
            onCheckedChange={(ifNotExists) => set({ ifNotExists })}
          />
        )}
      </OptionList>
    </ChangeDialog>
  )
}

const NO_COLUMNS: readonly string[] = []

export function AddForeignKeyDialog({ detail, onClose, onDone }: TableFormProps) {
  const ids = useId()
  const { id, engine } = useDatabase()
  const [draft, setDraft] = useState<ForeignKeyDraft>({
    name: "",
    columns: [""],
    refSchema: detail.schema,
    refTable: "",
    refColumns: [""],
    onDelete: "NO ACTION",
    onUpdate: "NO ACTION",
  })
  const set = (patch: Partial<ForeignKeyDraft>) => setDraft((held) => ({ ...held, ...patch }))
  const catalog = useCatalog(id, draft.refSchema)
  const referenced = useTableDetail(id, draft.refSchema, draft.refTable, draft.refTable !== "")
  const tables = catalog.data?.objects.tables ?? []
  const schemas = (catalog.data?.schemas ?? []).filter((schema) => !schema.system)
  const refColumns = referenced.data?.columns.map((column) => column.name) ?? NO_COLUMNS
  const own = detail.columns.map((column) => column.name)
  const request = foreignKeyRequest(detail.schema, detail.name, draft)
  const notes = notesFor(engine)
  const limits = actionLimits(engine.label, notes.onDelete, notes.onUpdate)

  const pair = (index: number, side: "columns" | "refColumns", value: string) =>
    set({ [side]: draft[side].map((held, at) => (at === index ? value : held)) })
  const chooseTable = (refTable: string) =>
    set({ refTable, refColumns: draft.refColumns.map(() => "") })

  // The usual key points at the other table's primary key: once that table is
  // read and the pair is still open, its key is what is proposed.
  const key = referenced.data?.primaryKey
  if (
    key &&
    key.length === 1 &&
    draft.refColumns.length === 1 &&
    draft.refColumns[0] === "" &&
    referenced.data?.name === draft.refTable
  ) {
    set({ refColumns: [key[0]] })
  }

  return (
    <ChangeDialog
      onClose={onClose}
      onDone={onDone}
      title="Add foreign key"
      subject={{ name: tableOf(detail) }}
      request={request}
      waiting="Choose the table it points at and pair the columns."
      command="Add foreign key"
      done={`Foreign key added to ${detail.name}`}
      dirty={draft.refTable !== "" || draft.columns.some((c) => c !== "") || draft.name !== ""}
      size="lg"
    >
      <FieldRow>
        {engine.can("schemas") && schemas.length > 1 && (
          <Field label={`Referenced ${engine.nouns.container}`} htmlFor={`${ids}-schema`}>
            <Select
              value={draft.refSchema}
              onValueChange={(refSchema) =>
                set({ refSchema, refTable: "", refColumns: draft.refColumns.map(() => "") })
              }
            >
              <SelectTrigger id={`${ids}-schema`} className="w-full font-mono text-xs">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {schemas.map((schema) => (
                  <SelectItem key={schema.name} value={schema.name} className="font-mono text-xs">
                    {schema.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
        )}
        <Field
          label="Referenced table"
          htmlFor={`${ids}-table`}
          error={catalog.error && !catalog.data ? "The tables could not be read." : undefined}
        >
          <Select value={draft.refTable} onValueChange={chooseTable}>
            <SelectTrigger id={`${ids}-table`} className="w-full font-mono text-xs">
              <SelectValue placeholder={catalog.data ? "Choose a table" : "Reading the tables…"} />
            </SelectTrigger>
            <SelectContent>
              {tables.map((table) => (
                <SelectItem key={table.name} value={table.name} className="font-mono text-xs">
                  {table.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
      </FieldRow>

      <Field label="Columns">
        <div className="space-y-2">
          {draft.columns.map((column, index) => (
            <div key={index} className="flex min-w-0 items-center gap-2">
              <Select value={column} onValueChange={(value) => pair(index, "columns", value)}>
                <SelectTrigger
                  aria-label={`Column ${index + 1} of ${detail.name}`}
                  className="min-w-0 flex-1 font-mono text-xs"
                >
                  <SelectValue placeholder="Column" />
                </SelectTrigger>
                <SelectContent>
                  {own.map((name) => (
                    <SelectItem key={name} value={name} className="font-mono text-xs">
                      {name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <ArrowRight aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />
              <Select
                value={draft.refColumns[index]}
                disabled={draft.refTable === ""}
                onValueChange={(value) => pair(index, "refColumns", value)}
              >
                <SelectTrigger
                  aria-label={`Column ${index + 1} of ${draft.refTable || "the referenced table"}`}
                  className="min-w-0 flex-1 font-mono text-xs"
                >
                  <SelectValue
                    placeholder={draft.refTable ? `Column of ${draft.refTable}` : "Its column"}
                  />
                </SelectTrigger>
                <SelectContent>
                  {refColumns.map((name) => (
                    <SelectItem key={name} value={name} className="font-mono text-xs">
                      {name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              {draft.columns.length > 1 && (
                <IconAction
                  label={`Remove pair ${index + 1}`}
                  onClick={() =>
                    set({
                      columns: draft.columns.filter((_, at) => at !== index),
                      refColumns: draft.refColumns.filter((_, at) => at !== index),
                    })
                  }
                >
                  <Trash />
                </IconAction>
              )}
            </div>
          ))}
          <Button
            size="xs"
            variant="ghost"
            className="text-muted-foreground"
            onClick={() =>
              set({ columns: [...draft.columns, ""], refColumns: [...draft.refColumns, ""] })
            }
          >
            <Plus />
            Another pair, for a key of several columns
          </Button>
        </div>
      </Field>

      <FieldRow columns={notes.onDelete.length > 1 && notes.onUpdate.length > 1 ? 3 : 2}>
        {notes.onDelete.length > 1 && (
          <Field label="When the row it points at is deleted" htmlFor={`${ids}-delete`}>
            <ActionSelect
              id={`${ids}-delete`}
              actions={notes.onDelete}
              value={draft.onDelete}
              onChange={(onDelete) => set({ onDelete })}
            />
          </Field>
        )}
        {notes.onUpdate.length > 1 && (
          <Field label="When its key is changed" htmlFor={`${ids}-update`}>
            <ActionSelect
              id={`${ids}-update`}
              actions={notes.onUpdate}
              value={draft.onUpdate}
              onChange={(onUpdate) => set({ onUpdate })}
            />
          </Field>
        )}
        <Field label="Name" htmlFor={`${ids}-name`} hint="Empty: the server names it.">
          <Input
            id={`${ids}-name`}
            autoComplete="off"
            spellCheck={false}
            className="font-mono text-xs"
            value={draft.name}
            onChange={(event) => set({ name: event.target.value })}
          />
        </Field>
      </FieldRow>
      {limits.map((line) => (
        <FormNote key={line}>{line}</FormNote>
      ))}
      <FormNote>
        Adding it checks every row the table holds: a row that points at nothing stops the change.
      </FormNote>
    </ChangeDialog>
  )
}

/** What the engine's foreign keys cannot do, said under the choices that are left. */
function actionLimits(
  engine: string,
  onDelete: readonly ReferenceAction[],
  onUpdate: readonly ReferenceAction[],
): string[] {
  const lines: string[] = []
  if (onUpdate.length <= 1) {
    lines.push(
      `${engine} has no ON UPDATE for a foreign key: a key that is pointed at cannot be made to carry a change to the rows that point at it.`,
    )
  }
  const gone = (["RESTRICT", "SET DEFAULT"] as const).filter(
    (action) => onDelete.length > 1 && !onDelete.includes(action),
  )
  if (gone.includes("RESTRICT")) {
    lines.push(`${engine} has no RESTRICT: no action is its equivalent.`)
  }
  if (gone.includes("SET DEFAULT")) {
    lines.push(`${engine} does not set a default through a foreign key.`)
  }
  return lines
}

function ActionSelect({
  id,
  actions,
  value,
  onChange,
}: {
  id: string
  /** What this engine's foreign keys may do, its default first. */
  actions: readonly ReferenceAction[]
  value: string
  onChange: (value: string) => void
}) {
  return (
    <Select value={value} onValueChange={onChange}>
      <SelectTrigger id={id} className="w-full text-xs">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {actions.map((action) => (
          <SelectItem key={action} value={action} className="text-xs">
            {action.toLowerCase()}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}

const DEFAULT_METHOD = "default"
const OTHER_METHOD = "other"

/**
 * An index's access method, chosen from the engine's own: a segmented control
 * for the two or three an engine has, a list for more. "Default" sends none
 * and leaves the choice to the engine. Where the engine takes any method it
 * has installed, "another" opens a field for its name.
 */
function MethodField({
  id,
  methods,
  open,
  value,
  other,
  onChange,
}: {
  id: string
  methods: readonly string[]
  /** Any other method the engine knows is taken too. */
  open: boolean
  value: string
  /** The method is being typed. */
  other: boolean
  onChange: (method: string, typed: boolean) => void
}) {
  const chosen = other ? OTHER_METHOD : value === "" ? DEFAULT_METHOD : value
  const pick = (next: string) =>
    next === OTHER_METHOD
      ? onChange("", true)
      : onChange(next === DEFAULT_METHOD ? "" : next, false)
  if (methods.length <= 2 && !open) {
    return (
      <Segments
        id={id}
        label="Index method"
        fill
        value={chosen}
        options={[
          { value: DEFAULT_METHOD, label: "Default" },
          ...methods.map((method) => ({ value: method, label: method, mono: true })),
        ]}
        onChange={pick}
      />
    )
  }
  return (
    <div className="space-y-2">
      <Select value={chosen} onValueChange={pick}>
        <SelectTrigger id={id} className="w-full text-xs">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value={DEFAULT_METHOD} className="text-xs">
            The engine&rsquo;s default
          </SelectItem>
          {methods.map((method) => (
            <SelectItem key={method} value={method} className="font-mono text-xs">
              {method}
            </SelectItem>
          ))}
          {open && (
            <SelectItem value={OTHER_METHOD} className="text-xs">
              Another, by name…
            </SelectItem>
          )}
        </SelectContent>
      </Select>
      {other && (
        <Input
          aria-label="Name of the access method"
          autoFocus
          autoComplete="off"
          spellCheck={false}
          placeholder="hnsw"
          className="font-mono text-xs"
          value={value}
          onChange={(event) => onChange(event.target.value, true)}
        />
      )}
    </div>
  )
}

export function AddConstraintDialog({ detail, onClose, onDone }: TableFormProps) {
  const ids = useId()
  const { engine } = useDatabase()
  const operations = engine.capabilities.ddlOperations
  const kinds = [
    ...(operations.includes("uniqueConstraints")
      ? [{ value: "unique" as const, label: "Unique" }]
      : []),
    ...(operations.includes("checkConstraints")
      ? [{ value: "check" as const, label: "Check" }]
      : []),
  ]
  const [draft, setDraft] = useState<ConstraintDraft>({
    type: kinds[0]?.value ?? "check",
    name: "",
    columns: [],
    expression: "",
  })
  const set = (patch: Partial<ConstraintDraft>) => setDraft({ ...draft, ...patch })
  const request = constraintRequest(detail.schema, detail.name, draft)
  const columns = detail.columns.map((column) => column.name)
  return (
    <ChangeDialog
      onClose={onClose}
      onDone={onDone}
      title="Add constraint"
      subject={{ name: tableOf(detail) }}
      request={request}
      waiting={
        draft.type === "unique"
          ? "Choose the columns that must not repeat."
          : "Write the condition every row must meet."
      }
      command="Add constraint"
      done={`Constraint added to ${detail.name}`}
      dirty={draft.columns.length > 0 || draft.expression !== "" || draft.name !== ""}
      size="lg"
    >
      {kinds.length > 1 && (
        <Field label="Kind">
          <Segments
            label="Kind of constraint"
            value={draft.type}
            options={kinds}
            onChange={(type) => set({ type })}
          />
        </Field>
      )}
      {draft.type === "unique" ? (
        <Field label="No two rows may share these columns">
          <ColumnChips
            label="Columns of the constraint"
            columns={columns}
            chosen={draft.columns}
            onChange={(chosen) => set({ columns: chosen })}
          />
        </Field>
      ) : (
        <Field
          label="Every row must meet"
          htmlFor={`${ids}-expression`}
          hint="A condition without the word CHECK, such as price_cents >= 0. Name columns bare."
        >
          <Input
            id={`${ids}-expression`}
            autoFocus
            autoComplete="off"
            spellCheck={false}
            className="font-mono text-xs"
            value={draft.expression}
            onChange={(event) => set({ expression: event.target.value })}
          />
        </Field>
      )}
      <Field label="Name" htmlFor={`${ids}-name`} hint="Empty: the server names it.">
        <Input
          id={`${ids}-name`}
          autoComplete="off"
          spellCheck={false}
          className="font-mono text-xs"
          value={draft.name}
          onChange={(event) => set({ name: event.target.value })}
        />
      </Field>
      <FormNote>
        Adding it tests every row the table holds: one that breaks the rule stops the change.
      </FormNote>
    </ChangeDialog>
  )
}
