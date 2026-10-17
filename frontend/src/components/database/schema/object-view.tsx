"use client"

import { useEffect, useRef, useState } from "react"
import Link from "next/link"
import {
  Code,
  Copy,
  Pencil,
  Plus,
  SidebarLeftClose,
  SidebarLeftOpen,
  Trash,
} from "@/components/icons"
import { ApiError, get } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { IconAction } from "@/components/icon-action"
import { FormFact } from "@/components/form"
import { Metric } from "@/components/page"
import { EmptyNote, EmptyState } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { ReadFailed } from "@/components/database/fleet/read-failed"
import { CodeView } from "@/components/database/kit"
import { useDatabase } from "@/components/database/shell/database-context"
import {
  objectParams,
  schemaParams,
  tableParams,
  type Selected,
} from "@/components/database/schema/address"
import { qualified } from "@/components/database/schema/changes"
import { Facts } from "@/components/database/schema/facts"
import { GroupGlyph, groupOfKind, kindWord } from "@/components/database/schema/kinds"
import { EnumValueDialog } from "@/components/database/schema/object-forms"
import { RoutineDialog, TriggerDialog } from "@/components/database/schema/program-forms"
import {
  PROGRAM_OPERATION,
  dropRoutineRequest,
  dropTriggerRequest,
  triggerFunctions,
  triggerTables,
  type DbProgram,
} from "@/components/database/schema/programs"
import type { DbCatalog, DbObjectDefinition } from "@/components/database/schema/types"
import { useDestroy } from "@/components/database/schema/use-destroy"

type ObjectSelection = Extract<NonNullable<Selected>, { type: "object" }>

/**
 * One object that holds no rows — a function, a trigger, a sequence, a type —
 * read as what it is: its facts, and the statement that makes it.
 *
 * The statement is the engine's own text where the engine keeps one, and
 * where it keeps none the dashboard assembles it from the catalogue and says
 * so beside it. "Open in Query" hands that text to the editor.
 *
 * A function, a procedure and a trigger can also be edited and dropped here,
 * where the engine's forms can write them and the role may run code in the
 * database. One whose text holds something the form cannot state is not
 * offered for editing, and the page says what.
 *
 * An enum's labels are listed in their order, and a label can be added where
 * the engine's forms and the role allow.
 */
export function ObjectView({
  selected,
  asked,
  railOpen,
  onToggleRail,
  onChanged,
  catalog,
  onDropped,
}: {
  selected: ObjectSelection
  /** Counts the times the reader asked for the schema to be read again. */
  asked: number
  railOpen: boolean
  onToggleRail: () => void
  /** A label was added: the catalogue's copy of the type is stale. */
  onChanged: () => void
  /** The schema's catalogue, for the tables and trigger functions a trigger's form offers. */
  catalog: DbCatalog | undefined
  /** The object was dropped: the page it was on is gone. */
  onDropped: () => void
}) {
  const { id, engine, href, goto, readOnly } = useDatabase()
  const { can } = useAuth()
  const [adding, setAdding] = useState(false)
  const [editing, setEditing] = useState(false)
  const { destroy, dialog } = useDestroy()
  const read = usePoll(
    (signal) =>
      get<DbObjectDefinition>(
        `/databases/${id}/object`,
        {
          kind: selected.kind,
          schema: selected.schema || undefined,
          name: selected.name,
          signature: selected.signature || undefined,
          table: selected.table || undefined,
        },
        signal,
      ),
    0,
    [id, selected.kind, selected.schema, selected.name, selected.signature, selected.table],
  )
  const reread = read.refresh
  const answered = useRef(asked)
  useEffect(() => {
    if (answered.current === asked) return
    answered.current = asked
    reread()
  }, [asked, reread])
  const data = read.data
  const group = groupOfKind(selected.kind)
  const routine = selected.kind === "function" || selected.kind === "procedure"
  const full = qualified(selected.schema, selected.name)
  const mayAddLabel =
    selected.kind === "enum" &&
    can("service.control") &&
    !readOnly &&
    engine.capabilities.ddlOperations.includes("enumTypes")
  const missing = !data && read.error instanceof ApiError && read.error.status === 404
  const program =
    selected.kind === "function" || selected.kind === "procedure" || selected.kind === "trigger"
      ? selected.kind
      : null
  const mayAuthor =
    program !== null &&
    can("service.control") &&
    can("destructive") &&
    !readOnly &&
    engine.capabilities.ddlOperations.includes(PROGRAM_OPERATION[program])
  const editable = usePoll(
    (signal) =>
      get<DbProgram>(
        `/databases/${id}/program`,
        {
          kind: selected.kind,
          schema: selected.schema || undefined,
          name: selected.name,
          signature: selected.signature || undefined,
          table: selected.table || undefined,
        },
        signal,
      ),
    0,
    [id, selected.kind, selected.schema, selected.name, selected.signature, selected.table],
    { enabled: mayAuthor && !missing },
  )
  const drop = () => {
    if (!program || !data) return
    const trigger = program === "trigger"
    void destroy({
      request: trigger
        ? dropTriggerRequest(data.schema, selected.name, data.table ?? selected.table)
        : dropRoutineRequest(program, data.schema, selected.name, selected.signature),
      title: `Drop ${kindWord(selected.kind)}`,
      subject: {
        name: trigger ? full : `${full}(${selected.signature})`,
        facts: trigger && (
          <FormFact label="On" mono>
            {data.table ?? selected.table}
          </FormFact>
        ),
      },
      sentence: trigger
        ? "The table stops running its code. The rows stay."
        : `Whatever calls it fails until it is made again. The engine refuses while another object depends on it.`,
      done: `Dropped ${selected.name}`,
      onDone: onDropped,
    })
  }

  const facts = data
    ? [...(data.owner ? [{ name: "Owner", value: data.owner }] : []), ...data.details]
    : []

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col">
      <div className="flex min-h-10 shrink-0 items-center gap-1.5 border-b border-hairline bg-surface-header px-1.5">
        <IconAction
          label={railOpen ? "Hide the objects" : "Show the objects"}
          aria-pressed={railOpen}
          className="size-7 shrink-0 max-sm:size-8"
          onClick={onToggleRail}
        >
          {railOpen ? <SidebarLeftClose /> : <SidebarLeftOpen />}
        </IconAction>
        <GroupGlyph group={group} />
        <h2
          aria-label={routine ? `${full}(${selected.signature})` : full}
          title={routine ? `${full}(${selected.signature})` : full}
          className="flex min-w-0 items-baseline font-mono text-body font-medium"
        >
          {selected.schema && (
            <span className="shrink-0 text-muted-foreground max-sm:hidden">{selected.schema}.</span>
          )}
          <span className="min-w-[4ch] truncate">{selected.name}</span>
          {routine && (
            <span className="min-w-0 truncate font-normal text-muted-foreground max-md:hidden">
              ({selected.signature})
            </span>
          )}
        </h2>
        <Tag className="shrink-0">{kindWord(selected.kind)}</Tag>
        {!missing && (
          <div className="ml-auto flex shrink-0 items-center gap-1 pl-2">
            {mayAddLabel && data && (
              <Button size="xs" className="max-sm:h-8" onClick={() => setAdding(true)}>
                <Plus />
                Add label
              </Button>
            )}
            {mayAuthor && data && editable.data && (
              <Button
                size="xs"
                className="max-sm:h-8"
                disabled={!editable.data.editable}
                onClick={() => setEditing(true)}
              >
                <Pencil />
                Edit
              </Button>
            )}
            {mayAuthor && data && (
              <Button size="xs" variant="outline" className="max-sm:h-8" onClick={drop}>
                <Trash />
                <span className="max-sm:sr-only">Drop</span>
              </Button>
            )}
            {engine.has("query") && (
              <Button
                size="xs"
                variant="outline"
                className="max-sm:h-8"
                disabled={!data?.definition}
                onClick={() => data && goto("query", { sql: data.definition })}
              >
                <Code />
                <span className="max-sm:sr-only">Open in Query</span>
              </Button>
            )}
            <IconAction
              label="Copy name"
              className="size-7 max-sm:size-8"
              onClick={() => void copyText(full, "Name copied")}
            >
              <Copy />
            </IconAction>
          </div>
        )}
      </div>

      {missing ? (
        <div className="flex min-h-0 flex-1 items-center justify-center p-6">
          <EmptyState
            className="border-0"
            title={`No ${kindWord(selected.kind)} called ${selected.name}`}
            description={`${selected.schema || `This ${engine.nouns.container}`} has nothing of that name and kind. It may have been dropped or replaced since the link was made.`}
            action={
              <Button size="sm" variant="outline" asChild>
                <Link href={href("schema", schemaParams(selected.schema))}>
                  Back to {selected.schema || `the ${engine.nouns.container}`}
                </Link>
              </Button>
            }
          />
        </div>
      ) : !data && read.error ? (
        <div className="p-4">
          <ReadFailed error={read.error} onRetry={read.refresh} />
        </div>
      ) : !data ? (
        <div aria-hidden className="space-y-5 p-4">
          <div className="flex gap-8">
            {["w-16", "w-12", "w-20"].map((width) => (
              <div key={width} className="space-y-2">
                <Skeleton className="h-2.5 w-10" />
                <Skeleton className={cn("h-3.5", width)} />
              </div>
            ))}
          </div>
          <div className="space-y-2.5">
            {["w-3/5", "w-2/5", "w-1/2", "w-1/3"].map((width) => (
              <Skeleton key={width} className={cn("h-3", width)} />
            ))}
          </div>
        </div>
      ) : (
        <div className="flex min-h-0 flex-1 animate-rise flex-col">
          {mayAuthor && !editable.data && editable.error && (
            <p className="shrink-0 border-b border-hairline px-4 py-2 text-hint text-muted-foreground">
              This {kindWord(selected.kind)} could not be read for editing: {editable.error.message}
            </p>
          )}
          {mayAuthor && editable.data && !editable.data.editable && (
            <p className="shrink-0 border-b border-hairline px-4 py-2 text-hint text-muted-foreground">
              Not editable here: {editable.data.reason}. Change it in Query.
            </p>
          )}
          {(facts.length > 0 || data.comment || data.table) && (
            <div className="shrink-0 space-y-3 border-b border-hairline px-4 py-3">
              {data.comment && (
                <p className="max-w-3xl text-body text-muted-foreground">{data.comment}</p>
              )}
              <Facts>
                {facts.map((fact) => (
                  <Metric key={fact.name} label={fact.name} value={fact.value} />
                ))}
              </Facts>
              {selected.kind === "trigger" && data.table && (
                <p className="text-xs text-muted-foreground">
                  Fires on{" "}
                  <Link
                    href={href("schema", {
                      ...tableParams(data.schema, data.table),
                      view: "triggers",
                    })}
                    className="rounded-sm font-mono text-foreground underline-offset-2 focus-ring hover:underline"
                  >
                    {data.table}
                  </Link>
                </p>
              )}
            </div>
          )}
          {data.values && data.values.length > 0 && (
            <div className="shrink-0 border-b border-hairline px-4 py-3">
              <p className="eyebrow pb-1.5">Labels, in their order</p>
              <ol className="flex flex-wrap gap-x-3 gap-y-1.5">
                {data.values.map((label, index) => (
                  <li key={label} className="flex items-baseline gap-1.5">
                    <span className="numeric text-micro text-muted-foreground">{index + 1}</span>
                    <Tag mono className="text-(--tag-pink)">
                      {label}
                    </Tag>
                  </li>
                ))}
              </ol>
            </div>
          )}
          {data.definition ? (
            <CodeView
              code={data.definition}
              language={engine.editor}
              filename={`${selected.name}.sql`}
              label={
                <span className="flex min-w-0 items-center gap-2">
                  <span className="truncate font-mono">{selected.name}.sql</span>
                  {data.source === "generated" && (
                    <Tag title="Assembled by the dashboard from the catalogue: the engine keeps no CREATE text">
                      generated
                    </Tag>
                  )}
                </span>
              }
              className="h-auto min-h-0 flex-1 rounded-none border-0"
            />
          ) : (
            <div className="flex min-h-0 flex-1 items-center justify-center p-6">
              <EmptyNote>
                {data.note ??
                  `${engine.label} did not say how this ${kindWord(selected.kind)} is defined.`}
              </EmptyNote>
            </div>
          )}
          {data.definition && data.note && (
            <p className="shrink-0 border-t border-hairline px-4 py-2 text-hint text-muted-foreground">
              {data.note}
            </p>
          )}
        </div>
      )}

      {editing && editable.data?.editable && editable.data.routine && (
        <RoutineDialog
          kind={editable.data.routine.kind}
          schemas={catalog?.schemas ?? []}
          schema={selected.schema}
          existing={{ routine: editable.data.routine, signature: selected.signature }}
          onClose={() => setEditing(false)}
          onDone={(answer) => {
            onChanged()
            // New parameter types make a different overload, and the address
            // names the old one: the page moves to the routine that was saved.
            if (answer.signature !== undefined && answer.signature !== selected.signature) {
              goto("schema", objectParams({ ...selected, signature: answer.signature }))
              return
            }
            read.refresh()
            editable.refresh()
          }}
        />
      )}
      {editing && editable.data?.editable && editable.data.trigger && (
        <TriggerDialog
          schemas={catalog?.schemas ?? []}
          schema={selected.schema}
          tables={triggerTables(catalog)}
          functions={triggerFunctions(catalog)}
          existing={editable.data.trigger}
          onClose={() => setEditing(false)}
          onDone={() => {
            read.refresh()
            editable.refresh()
            onChanged()
          }}
        />
      )}
      {dialog}
      {adding && data && (
        <EnumValueDialog
          schema={data.schema}
          name={data.name}
          values={data.values ?? []}
          onClose={() => setAdding(false)}
          onDone={() => {
            read.refresh()
            onChanged()
          }}
        />
      )}
    </div>
  )
}
