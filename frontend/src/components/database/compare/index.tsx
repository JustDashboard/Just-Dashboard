"use client"

import { useMemo, useState } from "react"
import { useRouter } from "next/navigation"
import { ArrowLeftRight, Check } from "@/components/icons"
import { get } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"
import { Field } from "@/components/form"
import { Panel, PanelHeader } from "@/components/panel"
import { EmptyState, LoadingRows, Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { sectionHref } from "@/components/database/engine"
import { EngineMark, EnvironmentTag, ProtectedTag, SectionFrame } from "@/components/database/kit"
import { ReadError } from "@/components/database/redis/read-error"
import { useDatabase } from "@/components/database/shell/database-context"
import { useDatabases } from "@/components/database/shell/databases-context"
import { DiffList } from "@/components/database/compare/diff-list"
import { Migration } from "@/components/database/compare/migration"
import {
  COMPARED,
  NOT_COMPARED,
  comparableConnections,
  readComparison,
  readSideId,
  summaryLine,
} from "@/components/database/compare/model"

type Head = { defaultSchema: string; schemas: { name: string; system?: boolean }[] }

/** The schemas of a connection, as the catalogue lists them. */
async function readHead(id: number, signal: AbortSignal): Promise<Head> {
  const answer = await get<Partial<Head>>(`/databases/${id}/catalog`, undefined, signal)
  return {
    defaultSchema: answer.defaultSchema ?? "",
    schemas: Array.isArray(answer.schemas) ? answer.schemas : [],
  }
}

/**
 * Two schemas of one engine, compared: what differs between them, side by
 * side, and the SQL that turns this connection's schema into the other one.
 *
 * The connection the page is on is the "from" side, because it is the one the
 * statements are written for. The other side is any saved connection to the
 * same kind of engine, this one's other schemas included. Nothing is read
 * until the reader presses Compare, since a comparison reads every table of
 * both schemas; from then on the result is what the server answered, and the
 * page words it.
 *
 * It changes nothing in either database. The migration is text to read, copy,
 * save, or open in Query — which is where it is classified and recorded if it
 * is run.
 */
export function Compare() {
  const { id, conn, engine, selection, select, param, goto, readOnly } = useDatabase()
  const { connections, engineFor } = useDatabases()
  const router = useRouter()

  const others = useMemo(
    () => comparableConnections(connections, conn, (c) => engineFor(c).can("schemaDiff")),
    [connections, conn, engineFor],
  )
  const toId = readSideId(param("to"))
  const target = others.find((c) => c.id === toId)
  const scoped = engine.can("schemas")

  const fromHead = usePoll((signal) => readHead(id, signal), 0, [id], { enabled: scoped })
  const toHead = usePoll((signal) => readHead(target?.id ?? 0, signal), 0, [target?.id], {
    enabled: scoped && target !== undefined,
  })
  const named = (head: Head | undefined) => (head?.schemas ?? []).filter((s) => !s.system)
  const fromSchema = selection.schema || fromHead.data?.defaultSchema || ""
  const toSchema = param("toSchema") || toHead.data?.defaultSchema || ""

  const sameSchema = target?.id === id && toSchema === fromSchema
  const problem = !target
    ? "Choose the connection to compare with."
    : sameSchema
      ? "Both sides are the same schema. Choose another schema or another connection."
      : undefined

  const [asked, setAsked] = useState<{ key: string; nonce: number }>()
  const request = target
    ? JSON.stringify({ to: target.id, schema: fromSchema, toSchema: toSchema })
    : ""
  const comparison = usePoll(
    async (signal) => {
      const query = JSON.parse(asked?.key ?? "{}") as Record<string, string | number>
      return readComparison(
        await get<unknown>(
          `/databases/${id}/schema-diff`,
          {
            to: query.to,
            schema: query.schema || undefined,
            toSchema: query.toSchema || undefined,
          },
          signal,
        ),
      )
    },
    0,
    [id, asked?.key, asked?.nonce],
    { enabled: asked !== undefined },
  )

  const compare = () => {
    if (problem) return
    setAsked((held) => ({ key: request, nonce: (held?.nonce ?? 0) + 1 }))
  }
  // Turned around, the other connection's page asks the other way: its schema
  // is the one that changes.
  const swap = () => {
    if (!target) return
    router.push(
      sectionHref(target.id, "compare", {
        schema: toSchema,
        to: String(id),
        toSchema: fromSchema,
      }),
    )
  }

  const result = comparison.data

  return (
    <SectionFrame section="compare">
      <div className="space-y-6">
        <Panel plain>
          <PanelHeader
            title="Schemas to compare"
            actions={
              <>
                <Button
                  size="sm"
                  variant="outline"
                  onClick={swap}
                  disabled={!target}
                  title="Open the comparison from the other side"
                >
                  <ArrowLeftRight />
                  Swap sides
                </Button>
                <Button
                  size="sm"
                  onClick={compare}
                  disabled={Boolean(problem)}
                  pending={comparison.loading}
                >
                  Compare
                </Button>
              </>
            }
          />
          <div className="grid min-w-0 gap-x-8 gap-y-5 pt-4 md:grid-cols-2">
            <div className="min-w-0 space-y-3" role="group" aria-label="From">
              <p className="eyebrow">From</p>
              <div className="flex min-w-0 items-center gap-2.5">
                <EngineMark engine={engine} size="sm" />
                <span className="min-w-0 truncate font-mono text-body font-medium">
                  {conn.name}
                </span>
                <EnvironmentTag environment={conn.environment} />
                {readOnly && <ProtectedTag />}
              </div>
              {scoped && (
                <SchemaSelect
                  label="From schema"
                  value={fromSchema}
                  schemas={named(fromHead.data)}
                  loading={fromHead.loading}
                  onChange={(schema) => select({ schema })}
                />
              )}
            </div>
            <div className="min-w-0 space-y-3" role="group" aria-label="To">
              <p className="eyebrow">To</p>
              <Field label="Compare with" htmlFor="compare-to">
                <Select
                  value={target ? String(target.id) : ""}
                  onValueChange={(value) => select({ to: value, toSchema: null })}
                >
                  <SelectTrigger id="compare-to" className="w-full">
                    <SelectValue placeholder="Choose a connection" />
                  </SelectTrigger>
                  <SelectContent>
                    {others.map((other) => (
                      <SelectItem key={other.id} value={String(other.id)}>
                        <span className="font-mono">{other.name}</span>
                        {other.id === id && (
                          <span className="text-muted-foreground"> · this one</span>
                        )}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
              {scoped && target && (
                <SchemaSelect
                  label="To schema"
                  value={toSchema}
                  schemas={named(toHead.data)}
                  loading={toHead.loading}
                  onChange={(schema) => select({ toSchema: schema })}
                />
              )}
            </div>
          </div>
          <p className="pt-4 text-hint text-muted-foreground">
            {problem ??
              `The statements turn ${conn.name}${fromSchema ? ` · ${fromSchema}` : ""} into ${target?.name}${toSchema ? ` · ${toSchema}` : ""}. Nothing is run.`}
          </p>
        </Panel>

        {asked === undefined ? (
          <EmptyState
            icon={ArrowLeftRight}
            title="Nothing compared yet"
            description="Choose what to compare with and press Compare. It lists what differs and writes the SQL that makes the first schema like the second; nothing is changed in either."
          />
        ) : comparison.error ? (
          <ReadError error={comparison.error} onRetry={comparison.refresh} />
        ) : !result ? (
          <div role="status" aria-label="Comparing the schemas">
            <LoadingRows rows={6} />
          </div>
        ) : (
          <Result
            result={result}
            protectedFrom={readOnly}
            editor={engine.editor}
            onQuery={engine.can("sql") ? (sql) => goto("query", { sql }) : undefined}
          />
        )}
      </div>
    </SectionFrame>
  )
}

function SchemaSelect({
  label,
  value,
  schemas,
  loading,
  onChange,
}: {
  label: string
  value: string
  schemas: { name: string }[]
  loading: boolean
  onChange: (schema: string) => void
}) {
  const id = `compare-${label.toLowerCase().replace(/\W+/g, "-")}`
  return (
    <Field label={label} htmlFor={id}>
      <Select value={value} onValueChange={onChange} disabled={loading && schemas.length === 0}>
        <SelectTrigger id={id} className="w-full font-mono">
          <SelectValue placeholder={loading ? "Reading the schemas" : "Choose a schema"} />
        </SelectTrigger>
        <SelectContent>
          {schemas.map((schema) => (
            <SelectItem key={schema.name} value={schema.name} className="font-mono">
              {schema.name}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </Field>
  )
}

function Result({
  result,
  protectedFrom,
  editor,
  onQuery,
}: {
  result: ReturnType<typeof readComparison>
  protectedFrom: boolean
  editor: string
  onQuery: ((sql: string) => void) | undefined
}) {
  const { from, to, objects, notes } = result
  const side = (s: typeof from) => `${s.name}${s.schema ? ` · ${s.schema}` : ""}`
  return (
    <section aria-label="Result" className="min-w-0 animate-rise space-y-4">
      {notes.length > 0 && (
        <Notice tone="warning" title="Not everything was compared">
          <ul className="list-disc space-y-1 pl-4">
            {notes.map((note) => (
              <li key={note}>{note}</li>
            ))}
          </ul>
        </Notice>
      )}
      {objects.length === 0 ? (
        <EmptyState
          icon={Check}
          title="The schemas match"
          description={`${side(from)} and ${side(to)} are the same in everything compared. ${COMPARED}`}
        />
      ) : (
        <>
          <Panel plain>
            <PanelHeader
              title={
                <span className="min-w-0 truncate">
                  {side(from)} <span className="text-muted-foreground">→</span> {side(to)}
                </span>
              }
              actions={
                <p className="numeric text-hint text-muted-foreground">{summaryLine(objects)}</p>
              }
            />
            <div className="grid min-w-0 items-start gap-x-10 gap-y-8 pt-4 xl:grid-cols-2">
              <DiffList objects={objects} from={from} to={to} />
              <Migration
                comparison={result}
                engineEditor={editor}
                onOpenInQuery={onQuery}
                protectedFrom={protectedFrom}
              />
            </div>
          </Panel>
          <p className="text-hint leading-relaxed text-muted-foreground">
            Compared: {COMPARED} Not compared: {NOT_COMPARED}
          </p>
        </>
      )}
    </section>
  )
}
