"use client"

import { useId, useState } from "react"
import { Segments } from "@/components/deploy/settings/segments"
import { Field, FieldRow, FormFact, FormNote, OptionList, OptionRow } from "@/components/form"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Textarea } from "@/components/ui/textarea"
import { useDatabase } from "@/components/database/shell/database-context"
import { ChangeDialog } from "@/components/database/schema/change-dialog"
import {
  addEnumValueRequest,
  createEnumRequest,
  createSchemaRequest,
  enumLabels,
  enumProblem,
  qualified,
  viewRequest,
  type ViewDraft,
} from "@/components/database/schema/changes"
import type { DbCatalogSchema, DdlAnswer } from "@/components/database/schema/types"
import { useSupport } from "@/components/database/schema/use-ddl"

/**
 * The forms that make the things a schema holds besides tables — a view, an
 * enum type, a schema itself — and the two that change one: a view's query
 * replaced, a label added to an enum.
 */

type FormProps = {
  onClose: () => void
  onDone: (answer: DdlAnswer) => void
}

/** Which schema a new object lands in: chosen, never assumed from where the reader happens to be. */
export function SchemaSelect({
  id,
  schemas,
  value,
  onChange,
}: {
  id: string
  schemas: readonly DbCatalogSchema[]
  value: string
  onChange: (schema: string) => void
}) {
  return (
    <Select value={value} onValueChange={onChange}>
      <SelectTrigger id={id} className="w-full font-mono text-xs">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {schemas
          .filter((schema) => !schema.system || schema.name === value)
          .map((schema) => (
            <SelectItem key={schema.name} value={schema.name} className="font-mono text-xs">
              {schema.name}
            </SelectItem>
          ))}
      </SelectContent>
    </Select>
  )
}

/** Whether this engine can replace a view in one statement; the server is asked, once. */
export function useViewReplace(schema: string, enabled: boolean) {
  const { id } = useDatabase()
  return useSupport(
    id,
    "view-replace",
    enabled
      ? {
          method: "POST",
          path: "/ddl/view",
          body: { schema, name: "jd_view_probe", query: "select 1", replace: true },
        }
      : null,
  )
}

/**
 * A view made, or — given `existing` — its query replaced. A replaced view
 * keeps its name and its place; what it selects is what changes.
 */
export function ViewDialog({
  schemas,
  schema,
  existing,
  onClose,
  onDone,
  onMade,
}: FormProps & {
  /** A view was made: where it is, for a page that opens it. */
  onMade?: (made: { schema: string; name: string }) => void
  schemas: readonly DbCatalogSchema[]
  /** The schema the reader is in: where a new view lands unless they say otherwise. */
  schema: string
  /** The view being edited, with the query its definition holds ("" when it could not be read out). */
  existing?: { schema: string; name: string; query: string }
}) {
  const ids = useId()
  const { engine } = useDatabase()
  const operations = engine.capabilities.ddlOperations
  const [draft, setDraft] = useState<ViewDraft>({
    schema: existing?.schema ?? schema,
    name: existing?.name ?? "",
    query: existing?.query ?? "",
    replace: Boolean(existing),
    materialized: false,
  })
  const set = (patch: Partial<ViewDraft>) => setDraft({ ...draft, ...patch })
  const replace = useViewReplace(draft.schema, !existing)
  const request = viewRequest(draft)
  const word = engine.nouns.container
  return (
    <ChangeDialog
      onClose={onClose}
      onDone={(answer) => {
        if (!existing) onMade?.({ schema: draft.schema, name: draft.name.trim() })
        onDone(answer)
      }}
      title={existing ? "Replace view" : "New view"}
      subject={{
        // A new view is named as it will be: where it lands, then what it is
        // called — the schema is said once, here, and chosen in its field.
        name: existing ? (
          qualified(existing.schema, existing.name)
        ) : (
          <>
            {draft.schema ? `${draft.schema}.` : ""}
            {draft.name.trim() || <span className="text-muted-foreground">new_view</span>}
          </>
        ),
      }}
      request={request}
      waiting={existing ? "Change the query." : "Name the view and write the query it shows."}
      command={existing ? "Replace view" : "Create view"}
      done={existing ? `Replaced ${existing.name}` : `Created ${draft.name.trim()}`}
      dirty={existing ? draft.query !== existing.query : draft.name !== "" || draft.query !== ""}
      size="lg"
    >
      {!existing && (
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
      <Field
        label="Query"
        htmlFor={`${ids}-query`}
        hint="One SELECT. What it returns is what the view shows."
      >
        <Textarea
          id={`${ids}-query`}
          autoFocus={Boolean(existing)}
          spellCheck={false}
          className="max-h-72 min-h-36 font-mono text-xs sm:text-xs"
          placeholder="SELECT …"
          value={draft.query}
          onChange={(event) => set({ query: event.target.value })}
        />
      </Field>
      {existing && existing.query === "" && (
        <FormNote>
          The query could not be read out of this view&rsquo;s definition, so the field starts
          empty. The definition itself is on the page behind this dialog.
        </FormNote>
      )}
      {!existing && (
        <OptionList>
          {replace?.supported !== false && (
            <OptionRow
              title="Replace a view of this name if there is one"
              checked={draft.replace}
              onCheckedChange={(checked) =>
                set({ replace: checked, materialized: checked ? false : draft.materialized })
              }
            />
          )}
          {operations.includes("materializedViews") && (
            <OptionRow
              title="Materialized: keep the rows it returns, and refresh them when asked"
              checked={draft.materialized}
              onCheckedChange={(checked) =>
                set({ materialized: checked, replace: checked ? false : draft.replace })
              }
            />
          )}
        </OptionList>
      )}
      {!existing && replace?.supported === false && <FormNote>{replace.reason}</FormNote>}
    </ChangeDialog>
  )
}

export function SchemaDialog({
  onClose,
  onDone,
  onMade,
}: FormProps & { onMade?: (name: string) => void }) {
  const ids = useId()
  const { conn, engine } = useDatabase()
  const [name, setName] = useState("")
  return (
    <ChangeDialog
      onClose={onClose}
      onDone={(answer) => {
        onMade?.(name.trim())
        onDone(answer)
      }}
      title={`New ${engine.nouns.container}`}
      subject={{ name: conn.name, facts: <FormFact label="Engine">{engine.label}</FormFact> }}
      request={createSchemaRequest(name)}
      waiting={`Name the ${engine.nouns.container}.`}
      command={`Create ${engine.nouns.container}`}
      done={`Created ${name.trim()}`}
      dirty={name !== ""}
    >
      <Field label="Name" htmlFor={`${ids}-name`}>
        <Input
          id={`${ids}-name`}
          autoFocus
          autoComplete="off"
          spellCheck={false}
          className="font-mono text-xs"
          value={name}
          onChange={(event) => setName(event.target.value)}
        />
      </Field>
    </ChangeDialog>
  )
}

export function EnumDialog({
  schemas,
  schema,
  onClose,
  onDone,
  onMade,
}: FormProps & {
  schemas: readonly DbCatalogSchema[]
  schema: string
  onMade?: (made: { schema: string; name: string }) => void
}) {
  const ids = useId()
  const [draft, setDraft] = useState({ schema, name: "", labels: "" })
  const labels = enumLabels(draft.labels)
  const problem = enumProblem(labels)
  return (
    <ChangeDialog
      onClose={onClose}
      onDone={(answer) => {
        onMade?.({ schema: draft.schema, name: draft.name.trim() })
        onDone(answer)
      }}
      title="New enum type"
      subject={{
        name: (
          <>
            {draft.schema ? `${draft.schema}.` : ""}
            {draft.name.trim() || <span className="text-muted-foreground">new_type</span>}
          </>
        ),
        facts: labels.length > 0 ? <FormFact label="Labels">{labels.length}</FormFact> : undefined,
      }}
      request={createEnumRequest(draft.schema, draft.name, draft.labels)}
      waiting="Name the type and list its labels."
      command="Create type"
      done={`Created ${draft.name.trim()}`}
      dirty={draft.name !== "" || draft.labels !== ""}
    >
      <FieldRow>
        {schemas.length > 0 && (
          <Field label="Schema" htmlFor={`${ids}-schema`}>
            <SchemaSelect
              id={`${ids}-schema`}
              schemas={schemas}
              value={draft.schema}
              onChange={(next) => setDraft({ ...draft, schema: next })}
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
            onChange={(event) => setDraft({ ...draft, name: event.target.value })}
          />
        </Field>
      </FieldRow>
      <Field
        label="Labels"
        htmlFor={`${ids}-labels`}
        hint="One to a line, in the order they sort."
        error={problem}
      >
        <Textarea
          id={`${ids}-labels`}
          spellCheck={false}
          className="max-h-64 min-h-28 font-mono text-xs sm:text-xs"
          placeholder={"pending\npaid\nshipped"}
          value={draft.labels}
          aria-invalid={problem ? true : undefined}
          onChange={(event) => setDraft({ ...draft, labels: event.target.value })}
        />
      </Field>
      <FormNote>
        A label can be added later. It cannot be removed, renamed away or moved once the type is
        made.
      </FormNote>
    </ChangeDialog>
  )
}

type Place = "end" | "before" | "after"

export function EnumValueDialog({
  schema,
  name,
  values,
  onClose,
  onDone,
}: FormProps & { schema: string; name: string; values: readonly string[] }) {
  const ids = useId()
  const [value, setValue] = useState("")
  const [at, setAt] = useState<Place>("end")
  const [beside, setBeside] = useState(values[values.length - 1] ?? "")
  const taken = values.includes(value.trim())
  const request = addEnumValueRequest(
    schema,
    name,
    value,
    at === "end" || beside === "" ? { at: "end" } : { at, label: beside },
  )
  return (
    <ChangeDialog
      onClose={onClose}
      onDone={onDone}
      title="Add label"
      subject={{
        name: qualified(schema, name),
        facts: <FormFact label="Labels">{values.length}</FormFact>,
      }}
      request={taken ? null : request}
      waiting={taken ? "The type already has that label." : "Type the new label."}
      command="Add label"
      done={`Added ${value.trim()} to ${name}`}
      dirty={value !== ""}
    >
      <Field
        label="Label"
        htmlFor={`${ids}-value`}
        error={taken ? "The type already has that label." : undefined}
      >
        <Input
          id={`${ids}-value`}
          autoFocus
          autoComplete="off"
          spellCheck={false}
          className="font-mono text-xs"
          value={value}
          aria-invalid={taken || undefined}
          onChange={(event) => setValue(event.target.value)}
        />
      </Field>
      {values.length > 0 && (
        <FieldRow>
          <Field label="Where it sorts">
            <Segments<Place>
              label="Where the label sorts"
              value={at}
              fill
              options={[
                { value: "end", label: "Last" },
                { value: "before", label: "Before" },
                { value: "after", label: "After" },
              ]}
              onChange={setAt}
            />
          </Field>
          {at !== "end" && (
            <Field label="The label" htmlFor={`${ids}-beside`}>
              <Select value={beside} onValueChange={setBeside}>
                <SelectTrigger id={`${ids}-beside`} className="w-full font-mono text-xs">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {values.map((label) => (
                    <SelectItem key={label} value={label} className="font-mono text-xs">
                      {label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
          )}
        </FieldRow>
      )}
      <FormNote>A label cannot be removed or moved once it is added.</FormNote>
    </ChangeDialog>
  )
}
