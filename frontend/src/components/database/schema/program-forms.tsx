"use client"

import { useId, useState } from "react"
import { ArrowDown, ArrowUp, Plus, Trash } from "@/components/icons"
import { cn } from "@/lib/utils"
import { Segments } from "@/components/deploy/settings/segments"
import { Field, FieldRow, FormNote, FormSection, OptionList, OptionRow } from "@/components/form"
import { CodeEditor } from "@/components/code-editor"
import { IconAction } from "@/components/icon-action"
import { ChipStrip, FilterChip } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { useDatabase } from "@/components/database/shell/database-context"
import { ChangeDialog } from "@/components/database/schema/change-dialog"
import { qualified } from "@/components/database/schema/changes"
import { TypeField } from "@/components/database/schema/fields"
import { SchemaSelect } from "@/components/database/schema/object-forms"
import {
  blankArg,
  blankRoutine,
  blankTrigger,
  routineAbilities,
  routineDraftOf,
  routineProblems,
  routineRequest,
  starterBody,
  triggerAbilities,
  triggerDraftOf,
  triggerProblems,
  triggerRequest,
  type ArgMode,
  type ProgramRoutine,
  type ProgramTrigger,
  type RoutineDraft,
  type RoutineKind,
  type TriggerDraft,
  type TriggerEvent,
  type TriggerTiming,
} from "@/components/database/schema/programs"
import type { DbCatalogSchema, DdlAnswer } from "@/components/database/schema/types"

/**
 * The forms that write a function, a procedure or a trigger, and the ones that
 * change one that exists.
 *
 * They are the schema browser's other forms with a body added: the fields the
 * engine has a word for, the code in the editor, and under it the statement
 * the server will send — one statement, whole. An edit says how the engine
 * replaces the object when it is more than one CREATE OR REPLACE.
 */

type FormProps = {
  onClose: () => void
  onDone: (answer: DdlAnswer) => void
}

const KIND_WORD: Record<RoutineKind, string> = { function: "function", procedure: "procedure" }

const SECURITY_WORD: Record<string, string> = { definer: "Its owner's", invoker: "The caller's" }

/** What the body field says it holds, for the engine's way of writing one. */
function bodyHint(driver: string, kind: RoutineKind, language: string): string {
  switch (driver) {
    case "postgres":
      return `The text between the dollar quotes, written in ${language || "the language chosen above"}.`
    case "mysql":
      return "One statement, or a BEGIN … END block."
    case "sqlserver":
      return kind === "function"
        ? "Everything after AS: a BEGIN … END block that RETURNs, or RETURN (SELECT …) for a table."
        : "Everything after AS: a statement or a BEGIN … END block."
    case "oracle":
      return "Everything after IS: declarations, then the BEGIN … END block, ending in a semicolon."
    default:
      return ""
  }
}

function BodyEditor({
  id,
  label,
  value,
  language,
  invalid,
  onChange,
}: {
  id: string
  label: string
  value: string
  language: string
  invalid?: boolean
  onChange: (value: string) => void
}) {
  return (
    <div
      id={id}
      role="group"
      aria-label={label}
      data-slot="program-body"
      aria-invalid={invalid || undefined}
      className={cn(
        "h-64 overflow-hidden rounded-md border bg-card",
        invalid ? "border-destructive" : "border-input",
      )}
    >
      <CodeEditor
        value={value}
        language={language}
        onChange={onChange}
        className="h-full"
        minimap={false}
        fontSize={12}
        wordWrap
        filePath={`${id}.sql`}
      />
    </div>
  )
}

/** One choice from a closed list, with the engine's default said as "Default". */
function Choice({
  id,
  value,
  options,
  onChange,
}: {
  id: string
  value: string
  options: { value: string; label: string }[]
  onChange: (value: string) => void
}) {
  return (
    <Select
      value={value || "default"}
      onValueChange={(next) => onChange(next === "default" ? "" : next)}
    >
      <SelectTrigger id={id} className="w-full text-xs">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {options.map((option) => (
          <SelectItem
            key={option.value || "default"}
            value={option.value || "default"}
            className="text-xs"
          >
            {option.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}

/**
 * A function or procedure made, or — given `existing` — replaced. A replaced
 * routine keeps its name and its place; its parameters, its options and its
 * body are what change.
 */
export function RoutineDialog({
  kind,
  schemas,
  schema,
  existing,
  onClose,
  onDone,
  onMade,
}: FormProps & {
  kind: RoutineKind
  schemas: readonly DbCatalogSchema[]
  /** The schema the reader is in: where a new routine lands unless they say otherwise. */
  schema: string
  /** The routine being edited, as the server read it, and the signature that names it. */
  existing?: { routine: ProgramRoutine; signature: string }
  onMade?: (made: { schema: string; name: string }) => void
}) {
  const ids = useId()
  const { engine } = useDatabase()
  const driver = engine.driver
  const [initial] = useState<RoutineDraft>(() =>
    existing ? routineDraftOf(existing.routine) : blankRoutine(driver, kind, schema),
  )
  const [draft, setDraft] = useState(initial)
  const set = (patch: Partial<RoutineDraft>) => setDraft({ ...draft, ...patch })
  const abilities = routineAbilities(driver, kind)
  const editing = Boolean(existing)
  const request = routineRequest(
    draft,
    abilities,
    existing ? { signature: existing.signature } : undefined,
  )
  const problems = routineProblems(draft, abilities, editing)
  const [pressed, setPressed] = useState(false)
  const word = engine.nouns.container
  const setArg = (key: string, patch: Partial<RoutineDraft["args"][number]>) =>
    set({ args: draft.args.map((arg) => (arg.key === key ? { ...arg, ...patch } : arg)) })
  const move = (index: number, by: -1 | 1) => {
    const args = [...draft.args]
    ;[args[index], args[index + by]] = [args[index + by], args[index]]
    set({ args })
  }
  const modes: { value: ArgMode; label: string }[] = [
    { value: "in", label: "IN" },
    ...abilities.modes.map((mode) => ({
      value: mode,
      label: mode === "inout" ? "INOUT" : "OUT",
    })),
  ]
  const languages = Array.from(new Set(["plpgsql", "sql", draft.language].filter(Boolean)))
  const dirty = editing
    ? JSON.stringify(draft) !== JSON.stringify(initial)
    : draft.name !== "" ||
      draft.args.length > 0 ||
      draft.returns !== "" ||
      draft.body !== initial.body

  return (
    <ChangeDialog
      onClose={onClose}
      onDone={(answer) => {
        if (!editing) onMade?.({ schema: draft.schema, name: draft.name.trim() })
        onDone(answer)
      }}
      title={editing ? `Edit ${KIND_WORD[kind]}` : `New ${KIND_WORD[kind]}`}
      subject={{
        name: editing ? (
          qualified(draft.schema, draft.name)
        ) : (
          <>
            {draft.schema ? `${draft.schema}.` : ""}
            {draft.name.trim() || (
              <span className="text-muted-foreground">new_{KIND_WORD[kind]}</span>
            )}
          </>
        ),
      }}
      request={request}
      waiting={
        pressed && problems.summary
          ? problems.summary
          : `Fill in the ${KIND_WORD[kind]}: the statement is written as you go.`
      }
      command={editing ? "Save changes" : `Create ${KIND_WORD[kind]}`}
      done={editing ? `Saved ${draft.name}` : `Created ${draft.name.trim()}`}
      dirty={dirty}
      size="lg"
    >
      {!editing && (
        <FieldRow>
          {engine.can("schemas") && schemas.length > 0 && (
            <Field label={word[0].toUpperCase() + word.slice(1)} htmlFor={`${ids}-schema`}>
              <SchemaSelect
                id={`${ids}-schema`}
                schemas={schemas}
                value={draft.schema}
                onChange={(next) => set({ schema: next })}
              />
            </Field>
          )}
          <Field label="Name" htmlFor={`${ids}-name`}>
            <Input
              id={`${ids}-name`}
              autoFocus
              autoComplete="off"
              spellCheck={false}
              className="font-mono text-xs"
              value={draft.name}
              onChange={(event) => set({ name: event.target.value })}
            />
          </Field>
        </FieldRow>
      )}

      <FormSection
        title="Parameters"
        actions={
          <Button
            size="xs"
            variant="outline"
            onClick={() => set({ args: [...draft.args, blankArg()] })}
          >
            <Plus />
            Add parameter
          </Button>
        }
      >
        {draft.args.length === 0 ? (
          <FormNote>No parameters.</FormNote>
        ) : (
          <div className="space-y-1.5">
            {draft.args.map((arg, index) => {
              const label = arg.name.trim() || `parameter ${index + 1}`
              const problem = problems.args[arg.key]
              return (
                <div key={arg.key}>
                  <div
                    role="group"
                    aria-label={`Parameter ${index + 1}`}
                    className={cn(
                      "grid grid-cols-2 items-center gap-2",
                      abilities.modes.length > 0 && abilities.defaults
                        ? "sm:grid-cols-[minmax(0,1fr)_5rem_minmax(0,1fr)_minmax(0,0.8fr)_4.75rem]"
                        : abilities.modes.length > 0
                          ? "sm:grid-cols-[minmax(0,1fr)_5rem_minmax(0,1fr)_4.75rem]"
                          : abilities.defaults
                            ? "sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_minmax(0,0.8fr)_4.75rem]"
                            : "sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_4.75rem]",
                    )}
                  >
                    <Input
                      aria-label={`Name of ${label}`}
                      autoComplete="off"
                      spellCheck={false}
                      placeholder="name"
                      className="h-10 font-mono text-xs sm:h-8"
                      value={arg.name}
                      onChange={(event) => setArg(arg.key, { name: event.target.value })}
                    />
                    {abilities.modes.length > 0 && (
                      <Select
                        value={arg.mode}
                        onValueChange={(mode) => setArg(arg.key, { mode: mode as ArgMode })}
                      >
                        <SelectTrigger
                          aria-label={`Mode of ${label}`}
                          className="h-10 w-full font-mono text-xs sm:h-8"
                        >
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          {modes.map((mode) => (
                            <SelectItem
                              key={mode.value}
                              value={mode.value}
                              className="font-mono text-xs"
                            >
                              {mode.label}
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                    )}
                    <TypeField
                      dense
                      label={`Type of ${label}`}
                      value={arg.type}
                      invalid={Boolean(problem) && arg.type.trim() === ""}
                      onChange={(type) => setArg(arg.key, { type })}
                    />
                    {abilities.defaults && (
                      <Input
                        aria-label={`Default of ${label}`}
                        autoComplete="off"
                        spellCheck={false}
                        placeholder="no default"
                        className="h-10 font-mono text-xs max-sm:col-span-2 sm:h-8"
                        value={arg.default}
                        disabled={arg.mode === "out"}
                        onChange={(event) => setArg(arg.key, { default: event.target.value })}
                      />
                    )}
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
                        disabled={index === draft.args.length - 1}
                        onClick={() => move(index, 1)}
                      >
                        <ArrowDown />
                      </IconAction>
                      <IconAction
                        label={`Remove ${label}`}
                        className="size-6"
                        onClick={() =>
                          set({ args: draft.args.filter((row) => row.key !== arg.key) })
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
        )}
      </FormSection>

      {kind === "function" && abilities.returns && (
        <Field
          label="Returns"
          htmlFor={`${ids}-returns`}
          hint={
            driver === "postgres"
              ? "A type, SETOF a type, or TABLE(name type, …). Leave it empty when OUT parameters stand in for it."
              : driver === "sqlserver"
                ? "A type, or TABLE for an inline function that RETURNs a SELECT."
                : undefined
          }
        >
          <TypeField
            id={`${ids}-returns`}
            value={draft.returns}
            onChange={(returns) => set({ returns })}
          />
        </Field>
      )}

      {(abilities.language ||
        abilities.volatility ||
        abilities.dataAccess ||
        abilities.security) && (
        <FieldRow>
          {abilities.language && (
            <Field label="Language" htmlFor={`${ids}-language`}>
              <Select
                value={draft.language}
                onValueChange={(language) => {
                  // The starter frame is for one language; swapping it in is
                  // only right while nobody has written over it.
                  const untouched = draft.body === starterBody(driver, kind, draft.language)
                  set({
                    language,
                    body: untouched ? starterBody(driver, kind, language) : draft.body,
                  })
                }}
              >
                <SelectTrigger id={`${ids}-language`} className="w-full font-mono text-xs">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {languages.map((language) => (
                    <SelectItem key={language} value={language} className="font-mono text-xs">
                      {language}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
          )}
          {abilities.volatility && (
            <Field label="Volatility" htmlFor={`${ids}-volatility`}>
              <Choice
                id={`${ids}-volatility`}
                value={draft.volatility}
                options={[
                  { value: "", label: "Volatile (the default)" },
                  { value: "stable", label: "Stable" },
                  { value: "immutable", label: "Immutable" },
                ]}
                onChange={(volatility) => set({ volatility })}
              />
            </Field>
          )}
          {abilities.dataAccess && (
            <Field label="Data access" htmlFor={`${ids}-access`}>
              <Choice
                id={`${ids}-access`}
                value={draft.dataAccess}
                options={[
                  { value: "", label: "Contains SQL (the default)" },
                  { value: "no sql", label: "No SQL" },
                  { value: "reads sql data", label: "Reads SQL data" },
                  { value: "modifies sql data", label: "Modifies SQL data" },
                ]}
                onChange={(dataAccess) => set({ dataAccess })}
              />
            </Field>
          )}
          {abilities.security && (
            <Field label="Runs with the rights of" htmlFor={`${ids}-security`}>
              <Choice
                id={`${ids}-security`}
                value={draft.security}
                options={[
                  { value: "", label: "The engine's default" },
                  { value: "definer", label: SECURITY_WORD.definer },
                  { value: "invoker", label: SECURITY_WORD.invoker },
                ]}
                onChange={(security) => set({ security })}
              />
            </Field>
          )}
        </FieldRow>
      )}

      {(abilities.strict || abilities.deterministic) && (
        <OptionList>
          {abilities.strict && (
            <OptionRow
              title="Strict"
              hint="Returns NULL without running when any argument is NULL."
              checked={draft.strict}
              onCheckedChange={(strict) => set({ strict })}
            />
          )}
          {abilities.deterministic && (
            <OptionRow
              title="Deterministic"
              hint="The same arguments always give the same result."
              checked={draft.deterministic}
              onCheckedChange={(deterministic) => set({ deterministic })}
            />
          )}
        </OptionList>
      )}

      <Field
        label="Body"
        hint={bodyHint(driver, kind, draft.language)}
        error={pressed ? problems.body : undefined}
      >
        <BodyEditor
          id={`${ids}-body`}
          label="Body"
          value={draft.body}
          language={engine.editor}
          invalid={pressed && Boolean(problems.body)}
          onChange={(body) => {
            setPressed(true)
            set({ body })
          }}
        />
      </Field>
    </ChangeDialog>
  )
}

const EVENT_WORD: Record<TriggerEvent, string> = {
  insert: "Insert",
  update: "Update",
  delete: "Delete",
  truncate: "Truncate",
}

const TIMING_WORD: Record<TriggerTiming, string> = {
  before: "Before",
  after: "After",
  "instead of": "Instead of",
}

/**
 * A trigger made, or — given `existing` — replaced. A replaced trigger stays
 * on its table; its timing, its events, its condition and what it runs change.
 */
export function TriggerDialog({
  schemas,
  schema,
  tables,
  table,
  functions,
  existing,
  onClose,
  onDone,
  onMade,
}: FormProps & {
  schemas: readonly DbCatalogSchema[]
  schema: string
  /** The tables and views of the schema, to choose the one it fires on. */
  tables: readonly string[]
  /** Where it was asked for from, when that is a table's own page. */
  table?: string
  /** Postgres: the functions that return `trigger`, as `schema.name`. */
  functions: readonly string[]
  existing?: ProgramTrigger
  onMade?: (made: { schema: string; name: string; table: string }) => void
}) {
  const ids = useId()
  const { engine } = useDatabase()
  const driver = engine.driver
  const abilities = triggerAbilities(driver)
  const [initial] = useState<TriggerDraft>(() =>
    existing ? triggerDraftOf(existing) : blankTrigger(driver, schema, table ?? ""),
  )
  const [draft, setDraft] = useState(initial)
  const [updateText, setUpdateText] = useState(initial.updateOf.join(", "))
  const set = (patch: Partial<TriggerDraft>) => setDraft({ ...draft, ...patch })
  const editing = Boolean(existing)
  const request = triggerRequest(draft, abilities, editing)
  const problems = triggerProblems(draft, abilities, editing)
  const [pressed, setPressed] = useState(false)
  const word = engine.nouns.container
  const choosable = Array.from(new Set(draft.function ? [...functions, draft.function] : functions))
  const toggleEvent = (event: TriggerEvent) =>
    set({
      events: draft.events.includes(event)
        ? draft.events.filter((e) => e !== event)
        : (["insert", "update", "delete", "truncate"] as TriggerEvent[]).filter(
            (e) => e === event || draft.events.includes(e),
          ),
    })
  const dirty = editing
    ? JSON.stringify(draft) !== JSON.stringify(initial)
    : draft.name !== "" || draft.body !== initial.body || draft.function !== ""

  return (
    <ChangeDialog
      onClose={onClose}
      onDone={(answer) => {
        if (!editing)
          onMade?.({ schema: draft.schema, name: draft.name.trim(), table: draft.table })
        onDone(answer)
      }}
      title={editing ? "Edit trigger" : "New trigger"}
      subject={{
        name: editing ? (
          qualified(draft.schema, draft.name)
        ) : (
          <>
            {draft.schema ? `${draft.schema}.` : ""}
            {draft.name.trim() || <span className="text-muted-foreground">new_trigger</span>}
          </>
        ),
      }}
      request={request}
      waiting={
        pressed && problems.summary
          ? problems.summary
          : "Fill in the trigger: the statement is written as you go."
      }
      command={editing ? "Save changes" : "Create trigger"}
      done={editing ? `Saved ${draft.name}` : `Created ${draft.name.trim()}`}
      dirty={dirty}
      size="lg"
    >
      <FieldRow>
        {!editing && engine.can("schemas") && schemas.length > 0 && (
          <Field label={word[0].toUpperCase() + word.slice(1)} htmlFor={`${ids}-schema`}>
            <SchemaSelect
              id={`${ids}-schema`}
              schemas={schemas}
              value={draft.schema}
              onChange={(next) => set({ schema: next })}
            />
          </Field>
        )}
        {!editing && (
          <Field label="Name" htmlFor={`${ids}-name`}>
            <Input
              id={`${ids}-name`}
              autoFocus
              autoComplete="off"
              spellCheck={false}
              className="font-mono text-xs"
              value={draft.name}
              onChange={(event) => set({ name: event.target.value })}
            />
          </Field>
        )}
        <Field label="Fires on" htmlFor={editing || table ? undefined : `${ids}-table`}>
          {editing || table ? (
            <p className="py-1.5 font-mono text-xs">{draft.table}</p>
          ) : (
            <Select value={draft.table} onValueChange={(next) => set({ table: next })}>
              <SelectTrigger id={`${ids}-table`} className="w-full font-mono text-xs">
                <SelectValue placeholder="Choose a table" />
              </SelectTrigger>
              <SelectContent>
                {tables.map((name) => (
                  <SelectItem key={name} value={name} className="font-mono text-xs">
                    {name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
        </Field>
      </FieldRow>

      <FieldRow>
        <Field label="When">
          <Segments<TriggerTiming>
            label="When the trigger fires"
            value={draft.timing}
            fill
            options={abilities.timings.map((timing) => ({
              value: timing,
              label: TIMING_WORD[timing],
            }))}
            onChange={(timing) => set({ timing })}
          />
        </Field>
        {abilities.forEach.length > 1 && (
          <Field label="For">
            <Segments<"row" | "statement">
              label="Fires for each row or each statement"
              value={draft.forEach}
              fill
              options={[
                { value: "row", label: "Each row" },
                { value: "statement", label: "Each statement" },
              ]}
              onChange={(forEach) => set({ forEach })}
            />
          </Field>
        )}
      </FieldRow>

      <Field label="On" error={pressed ? problems.events : undefined}>
        {abilities.singleEvent ? (
          <Segments<TriggerEvent>
            label="The event it fires on"
            value={draft.events[0] ?? "insert"}
            fill
            options={abilities.events.map((event) => ({ value: event, label: EVENT_WORD[event] }))}
            onChange={(event) => set({ events: [event] })}
          />
        ) : (
          <ChipStrip
            role="group"
            aria-label="The events it fires on"
            className="max-sm:mx-0 max-sm:px-0"
          >
            {abilities.events.map((event) => (
              <FilterChip
                key={event}
                selected={draft.events.includes(event)}
                onClick={() => {
                  setPressed(true)
                  toggleEvent(event)
                }}
              >
                {EVENT_WORD[event]}
              </FilterChip>
            ))}
          </ChipStrip>
        )}
      </Field>

      {abilities.updateOf && draft.events.includes("update") && (
        <Field
          label="Only when these columns are written"
          htmlFor={`${ids}-of`}
          hint="Column names, separated by commas. Leave it empty for any update."
        >
          <Input
            id={`${ids}-of`}
            autoComplete="off"
            spellCheck={false}
            className="font-mono text-xs"
            value={updateText}
            onChange={(event) => {
              setUpdateText(event.target.value)
              set({
                updateOf: event.target.value
                  .split(",")
                  .map((column) => column.trim())
                  .filter((column) => column !== ""),
              })
            }}
          />
        </Field>
      )}

      {abilities.condition && (
        <Field
          label="Only when"
          htmlFor={`${ids}-condition`}
          hint="A condition on the row, without the WHEN: new.total > 0. Leave it empty to always fire."
        >
          <Input
            id={`${ids}-condition`}
            autoComplete="off"
            spellCheck={false}
            className="font-mono text-xs"
            value={draft.condition}
            onChange={(event) => set({ condition: event.target.value })}
          />
        </Field>
      )}

      {abilities.function ? (
        <>
          <Field
            label="Executes"
            htmlFor={`${ids}-function`}
            hint="A function that returns trigger. Make one with New function."
            error={pressed ? problems.function : undefined}
          >
            {choosable.length === 0 ? (
              <FormNote>There is no function returning trigger yet.</FormNote>
            ) : (
              <Select
                value={draft.function}
                onValueChange={(next) => {
                  setPressed(true)
                  set({ function: next })
                }}
              >
                <SelectTrigger id={`${ids}-function`} className="w-full font-mono text-xs">
                  <SelectValue placeholder="Choose a function" />
                </SelectTrigger>
                <SelectContent>
                  {choosable.map((name) => (
                    <SelectItem key={name} value={name} className="font-mono text-xs">
                      {name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
          </Field>
          <Field
            label="Arguments"
            htmlFor={`${ids}-args`}
            hint="Passed to the function as text, one to a line. Most triggers pass none."
          >
            <textarea
              id={`${ids}-args`}
              spellCheck={false}
              className="min-h-16 w-full rounded-md border border-input bg-transparent px-3 py-2 font-mono text-xs focus-ring"
              value={draft.functionArgs}
              onChange={(event) => set({ functionArgs: event.target.value })}
            />
          </Field>
        </>
      ) : (
        <Field
          label="Body"
          hint={
            driver === "sqlite"
              ? "The statements between BEGIN and END, each ending in a semicolon. new and old are the row."
              : driver === "mysql"
                ? "One statement, or a BEGIN … END block. NEW and OLD are the row."
                : driver === "sqlserver"
                  ? "Everything after AS. The rows are in the inserted and deleted tables."
                  : "The block: declarations if any, then BEGIN … END, ending in a semicolon. :NEW and :OLD are the row."
          }
          error={pressed ? problems.body : undefined}
        >
          <BodyEditor
            id={`${ids}-body`}
            label="Body"
            value={draft.body}
            language={engine.editor}
            invalid={pressed && Boolean(problems.body)}
            onChange={(body) => {
              setPressed(true)
              set({ body })
            }}
          />
        </Field>
      )}
    </ChangeDialog>
  )
}
