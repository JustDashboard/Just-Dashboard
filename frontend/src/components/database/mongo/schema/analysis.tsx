"use client"

import { useMemo, useState } from "react"
import { Key } from "@/components/icons"
import { errorMessage } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useMemoryState, useSessionState } from "@/lib/view-state"
import { Segments } from "@/components/deploy/settings/segments"
import { Meter } from "@/components/meter"
import { SearchInput } from "@/components/page"
import { EmptyState, Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { EngineMark } from "@/components/database/kit"
import { analyseSchema } from "@/components/database/mongo/api"
import { CodeField } from "@/components/database/mongo/code-field"
import {
  StopButton,
  Stopped,
  useElapsed,
  useStoppable,
  worthShowing,
} from "@/components/database/mongo/in-flight"
import { TypeTag, typeInfo } from "@/components/database/mongo/kinds"
import {
  EMPTY_QUERY,
  addressOf,
  collectionKey,
  queryMemoryKey,
  type QueryDraft,
} from "@/components/database/mongo/query"
import {
  allUnique,
  distinctWords,
  matchingFields,
  missingClause,
  rangeWords,
  shareWords,
  typeClause,
  valueClause,
} from "@/components/database/mongo/schema/fields"
import { mergeFilter, shapeProblem } from "@/components/database/mongo/shell"
import type { MongoSchema, MongoSchemaField } from "@/components/database/mongo/types"
import type { Workbench } from "@/components/database/mongo/workbench"

const SAMPLES = ["100", "1000", "10000"] as const
type Sample = (typeof SAMPLES)[number]

/** How many of a type's top values are drawn before the rest are asked for. */
const TOP = 6

/**
 * The columns of a field's row, by the pane's own width: field, types, how
 * often it is there, values. Under that a row is the field and its presence
 * on one line, then its types, then its values.
 */
const GRID =
  "@2xl:grid-cols-[minmax(0,11rem)_minmax(0,10rem)_5.5rem_minmax(0,1fr)] @4xl:grid-cols-[minmax(0,14rem)_minmax(0,13rem)_7rem_minmax(0,1fr)]"

/**
 * What a collection holds, read off a sample of its documents.
 *
 * One row per field: the types it has as a bar in the type hues, the share
 * of documents that have it at all, and what its values are — the commonest
 * ones, or the range they span. Each of those is also a question: pressing a
 * value, a type or "missing" opens Documents with that condition added to the
 * filter the collection was last asked.
 *
 * The sample is taken when the reader asks, never on a timer — it is a read
 * of up to ten thousand documents — and every figure says it is of the
 * sample, not of the collection.
 */
export function AnalysisView({ mongo, collection: info }: Pick<Workbench, "mongo" | "collection">) {
  const { id, target, database, collection, engine, goto } = mongo
  const scope = collectionKey(database, collection)
  // Values of the collection's documents: held for the page's life, never written down.
  const [held, setHeld] = useMemoryState<Record<string, MongoSchema>>(
    `databases.${id}.mongo.schema`,
    {},
  )
  const schema = held[scope] as MongoSchema | undefined
  const [memory] = useSessionState<Record<string, QueryDraft>>(queryMemoryKey(id), {})
  const [sample, setSample] = useState<Sample>("1000")
  const [filter, setFilter] = useState("")
  const [busy, setBusy] = useState(false)
  const [refused, setRefused] = useState("")
  const [query, setQuery] = useState("")
  // A sample of ten thousand documents can take a while: it says how long, and can be stopped.
  const flight = useStoppable()
  const seconds = useElapsed(busy)

  const filterBad = shapeProblem(filter, "document")
  const analyse = async () => {
    setBusy(true)
    setRefused("")
    try {
      const answer = await flight.run((signal) =>
        analyseSchema(
          target,
          { sample: Number(sample), filter: filter.trim() ? filter : undefined },
          signal,
        ),
      )
      setHeld((all) => ({ ...all, [scope]: answer }))
    } catch (err) {
      // Stopped by the reader: what was on screen stays, and nothing is said to have failed.
      if (!(err instanceof Stopped)) setRefused(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  /** Opens Documents with one more condition on the query the collection was last asked. */
  const ask = (clause: string) => {
    const last = (memory[scope] as QueryDraft | undefined) ?? EMPTY_QUERY
    goto("data", {
      db: database,
      collection,
      ...addressOf({ ...last, filter: mergeFilter(last.filter, clause) }),
    })
  }

  const fields = useMemo(() => matchingFields(schema?.fields ?? [], query), [schema, query])

  const controls = (
    <div className="flex min-w-0 flex-wrap items-start gap-x-3 gap-y-2">
      <div className="flex items-center gap-2">
        <span className="eyebrow">Sample</span>
        <Segments
          label="Documents to sample"
          value={sample}
          options={SAMPLES.map((value) => ({
            value,
            label: Number(value).toLocaleString("en-US"),
          }))}
          onChange={setSample}
        />
      </div>
      <div className="min-w-0 flex-1 basis-56">
        <CodeField
          dense
          value={filter}
          invalid={Boolean(filterBad)}
          aria-label="Sample only the documents this filter matches"
          placeholder="Only documents matching { … }"
          onChange={setFilter}
          onSubmit={() => !filterBad && void analyse()}
        />
        {filterBad && (
          <p role="alert" className="pt-1 text-hint text-destructive">
            {filterBad}
          </p>
        )}
      </div>
      {busy && worthShowing(seconds) ? (
        <StopButton seconds={seconds} onStop={flight.stop} className="h-8" />
      ) : (
        <Button
          size="sm"
          className="h-8"
          variant={schema ? "outline" : "default"}
          pending={busy}
          disabled={Boolean(filterBad)}
          onClick={() => void analyse()}
        >
          {schema ? "Analyze again" : "Analyze"}
        </Button>
      )}
    </div>
  )

  if (!schema) {
    return (
      <div className="min-h-0 flex-1 overflow-auto">
        <div className="space-y-4 p-4">
          <EmptyState
            mark={<EngineMark engine={engine} />}
            className="border-0 pb-4"
            title={`What ${collection} holds`}
            description={
              info?.statsKnown
                ? `A random sample of its ${info.count.toLocaleString("en-US")} documents is read, and each field is listed with its types, how often it is there and what its values are.`
                : "A random sample of its documents is read, and each field is listed with its types, how often it is there and what its values are."
            }
          />
          <div className="mx-auto max-w-2xl">{controls}</div>
          {refused && (
            <Notice
              tone="danger"
              title="The sample could not be read"
              className="mx-auto max-w-2xl"
            >
              {refused}
            </Notice>
          )}
        </div>
      </div>
    )
  }

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col">
      <div className="shrink-0 space-y-2 border-b border-hairline px-3 py-2">
        {controls}
        {refused && (
          <p role="alert" className="text-hint text-destructive">
            The sample could not be read: {refused}
          </p>
        )}
        <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1.5">
          <p className="numeric min-w-0 flex-1 text-hint text-muted-foreground">
            Based on {schema.sampled.toLocaleString("en-US")}{" "}
            {schema.sampled === 1 ? "document" : "documents"}
            {schema.total >= 0 ? ` of about ${schema.total.toLocaleString("en-US")}` : ""} ·{" "}
            {schema.fields.length.toLocaleString("en-US")} fields ·{" "}
            {schema.durationMs.toLocaleString("en-US")} ms
            {schema.truncated ? " · more fields than are listed" : ""}
          </p>
          <SearchInput
            dense
            value={query}
            placeholder="Find a field"
            aria-label="Find a field"
            containerClassName="sm:w-56"
            onChange={(event) => setQuery(event.target.value)}
          />
        </div>
      </div>

      <div className="@container min-h-0 flex-1 overflow-auto">
        {schema.sampled === 0 ? (
          <p className="px-3 py-10 text-center text-body text-muted-foreground">
            No document was sampled: the collection is empty, or nothing matches the filter.
          </p>
        ) : fields.length === 0 ? (
          <p className="px-3 py-10 text-center text-body text-muted-foreground">
            No field is called that.
          </p>
        ) : (
          <ul aria-label="Fields" className="animate-rise divide-y divide-hairline">
            <li
              aria-hidden
              className={cn(
                "sticky top-0 z-10 grid gap-x-4 bg-card px-3 py-1.5 text-hint font-medium text-muted-foreground @max-2xl:hidden",
                GRID,
              )}
            >
              <span>Field</span>
              <span>Types</span>
              <span>In documents</span>
              <span>Values</span>
            </li>
            {fields.map((field) => (
              <FieldRow key={field.path} field={field} sampled={schema.sampled} onAsk={ask} />
            ))}
          </ul>
        )}
      </div>
    </div>
  )
}

function FieldRow({
  field,
  sampled,
  onAsk,
}: {
  field: MongoSchemaField
  sampled: number
  onAsk: (clause: string) => void
}) {
  const [all, setAll] = useState(false)
  const missing = sampled - field.documents
  const element = field.name === "[]"
  // The type whose values are read out: the commonest one that has any to show.
  const valued = field.types.find((type) => type.top?.length || rangeWords(type))

  return (
    <li
      data-slot="mongo-schema-field"
      className={cn("grid grid-cols-[minmax(0,1fr)_5.5rem] gap-x-4 gap-y-1.5 px-3 py-2", GRID)}
    >
      <div
        className="flex min-w-0 items-center gap-1.5 self-start"
        style={{ paddingLeft: `${field.depth * 12}px` }}
      >
        <span
          className={cn("min-w-0 truncate font-mono text-xs", element && "text-muted-foreground")}
          title={field.path}
        >
          {element ? "each element" : field.name}
        </span>
        {field.indexed && (
          <span
            className="flex shrink-0 items-center text-(--tag-cyan)"
            title={`In ${field.indexes.length === 1 ? "the index" : "the indexes"} ${field.indexes.join(", ")}`}
          >
            <Key aria-hidden className="size-3" />
            <span className="sr-only">indexed by {field.indexes.join(", ")}</span>
          </span>
        )}
      </div>

      <div className="min-w-0 space-y-1 self-start @max-2xl:order-3 @max-2xl:col-span-2">
        <div
          role="group"
          aria-label={`Types of ${field.path}`}
          className="flex h-2 w-full overflow-hidden rounded-full bg-meter-track"
        >
          {field.types.map((type) => (
            <button
              key={type.type}
              type="button"
              title={`${typeInfo(type.type).label}: ${shareWords(type.share)} of its values. Find them`}
              aria-label={`Find the documents where ${field.path} is ${typeInfo(type.type).label}`}
              onClick={() => onAsk(typeClause(field.path, type.type))}
              className="h-full min-w-0.5 focus-ring-inset first:rounded-l-full last:rounded-r-full hover:opacity-80"
              style={{ width: `${type.share * 100}%`, background: typeInfo(type.type).color }}
            />
          ))}
        </div>
        <p className="flex min-w-0 flex-wrap gap-x-2 text-hint">
          {field.types.map((type) => (
            <span key={type.type} className="flex items-center gap-1 whitespace-nowrap">
              <TypeTag type={type.type} />
              {field.types.length > 1 && (
                <span className="numeric text-muted-foreground">{shareWords(type.share)}</span>
              )}
            </span>
          ))}
        </p>
      </div>

      <div className="min-w-0 space-y-1 self-start">
        <div className="flex h-2 items-center">
          <Meter
            size="thin"
            value={field.presence * 100}
            label={`${field.path} is in ${shareWords(field.presence)} of the sampled documents`}
          />
        </div>
        <p className="numeric flex flex-wrap items-center gap-x-1.5 text-hint text-muted-foreground">
          {shareWords(field.presence)}
          {missing > 0 && !element && (
            <button
              type="button"
              aria-label={`Find the ${missing.toLocaleString("en-US")} documents without ${field.path}`}
              onClick={() => onAsk(missingClause(field.path))}
              className="rounded-sm underline decoration-dotted underline-offset-2 focus-ring hover:text-foreground"
            >
              {missing.toLocaleString("en-US")} without
            </button>
          )}
        </p>
      </div>

      <div className="min-w-0 self-start text-hint text-muted-foreground @max-2xl:order-4 @max-2xl:col-span-2">
        {!valued ? (
          field.types[0]?.type === "object" ? (
            <span>a document: its fields follow</span>
          ) : (
            <span>—</span>
          )
        ) : (
          <div className="space-y-1">
            {valued.top && valued.top.length > 0 && !allUnique(valued) && (
              <div className="flex min-w-0 flex-wrap gap-1">
                {(all ? valued.top : valued.top.slice(0, TOP)).map((entry) => {
                  const clause = valueClause(field.path, valued.type, entry.value)
                  return (
                    <button
                      key={entry.value}
                      type="button"
                      disabled={clause === null}
                      title={
                        clause === null
                          ? "The value was cut short in the sample, so it cannot be searched for exactly"
                          : `Find the documents where ${field.path} is ${entry.value}`
                      }
                      // The press is named for what it does, not for the value alone.
                      aria-label={
                        clause === null
                          ? `${entry.value}, cut short in the sample`
                          : `Find the documents where ${field.path} is ${entry.value}`
                      }
                      onClick={() => clause && onAsk(clause)}
                      className="flex h-6 max-w-full min-w-0 items-center gap-1.5 rounded-md border border-hairline px-1.5 font-mono focus-ring transition-colors hover:bg-row-hover disabled:opacity-60"
                    >
                      <span className="min-w-0 truncate text-foreground">
                        {entry.value === "" ? '""' : entry.value}
                      </span>
                      <span className="numeric shrink-0 font-sans text-micro opacity-70">
                        {shareWords(entry.count / Math.max(valued.count, 1))}
                      </span>
                    </button>
                  )
                })}
                {!all && valued.top.length > TOP && (
                  <button
                    type="button"
                    onClick={() => setAll(true)}
                    className="h-6 rounded-md px-1.5 focus-ring hover:text-foreground"
                  >
                    {valued.top.length - TOP} more
                  </button>
                )}
              </div>
            )}
            <p className="numeric">
              {[
                allUnique(valued) ? "every sampled value differs" : null,
                rangeWords(valued),
                allUnique(valued) ? null : distinctWords(valued),
              ]
                .filter(Boolean)
                .join(" · ")}
            </p>
          </div>
        )}
      </div>
    </li>
  )
}
