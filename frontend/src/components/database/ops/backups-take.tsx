"use client"

import { useId, useMemo, useState } from "react"
import { Archive } from "@/components/icons"
import { ApiError, errorMessage, post } from "@/lib/api"
import { plural } from "@/lib/format"
import type { Job } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Segments } from "@/components/deploy/settings/segments"
import { Field, FormFact, FormNote } from "@/components/form"
import { SearchInput } from "@/components/page"
import { ChipStrip, FilterChip } from "@/components/tabs"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { Textarea } from "@/components/ui/textarea"
import { read } from "@/components/database/home/read"
import { tableChoice } from "@/components/database/ops/backups-model"
import type {
  DbBackupRequest,
  DbDumpObject,
  DbDumpSupport,
} from "@/components/database/ops/backups-types"
import { TaskDialog, databaseSubject } from "@/components/database/ops/settings-dialog"
import { useDatabase } from "@/components/database/shell/database-context"

type Scope = "all" | "structure" | "data"
type TableMode = "all" | "only" | "except"
type Compression = "default" | "gzip" | "none"

/** A note travels with the dump; the server keeps five hundred bytes of it. */
const NOTE_BYTES = 500

/** The objects a dump can be narrowed to: the connection's own tables, or its collections. */
function useDumpObjects(enabled: boolean) {
  const { id, conn, engine } = useDatabase()
  const documents = engine.kind === "document"
  return usePoll<DbDumpObject[]>(
    async (signal) => {
      if (documents) {
        const answer = await read<{ collections: { name: string; system?: boolean }[] }>(
          `/databases/${id}/mongo/collections`,
          (listed) => Array.isArray(listed.collections),
          { database: conn.database },
          signal,
        )
        return answer.collections
          .filter((one) => !one.system)
          .map((one) => ({ schema: "", name: one.name }))
      }
      const answer = await read<{ tables: { schema: string; table: string }[] }>(
        `/databases/${id}/tablestats`,
        (listed) => Array.isArray(listed.tables),
        { limit: 500 },
        signal,
      )
      return answer.tables.map((one) => ({ schema: one.schema, name: one.table }))
    },
    0,
    [id, documents],
    { enabled },
  )
}

/** A key–value server's numbered databases that hold keys. */
function useNumberedDatabases(enabled: boolean) {
  const { id } = useDatabase()
  return usePoll(
    (signal) =>
      read<{ name: string; size?: number }[]>(
        `/databases/${id}/schemas`,
        (listed) => Array.isArray(listed),
        undefined,
        signal,
      ),
    0,
    [id],
    { enabled },
  )
}

/**
 * Take a dump now. What the engine's tool can be asked for is the listing's
 * own `options`, so nothing is offered that the request would refuse: the
 * whole database or only its structure or its rows, every table or some of
 * them, how it is compressed, and — on a server that numbers its databases —
 * which of them.
 *
 * The dump itself is a job on the server. The dialog only begins it: the page
 * underneath shows it running.
 */
export function TakeDump({
  options,
  onStarted,
  onRunning,
  onClose,
}: {
  options: DbDumpSupport
  onStarted: (job: Job) => void
  /** Another dump, restore or copy of this connection is already running: its id. */
  onRunning: (jobId: string) => void
  onClose: () => void
}) {
  const { id, conn, engine, summary } = useDatabase()
  const field = useId()
  const [scope, setScope] = useState<Scope>("all")
  const [mode, setMode] = useState<TableMode>("all")
  const [picked, setPicked] = useState<string[]>([])
  const [typed, setTyped] = useState("")
  const [filter, setFilter] = useState("")
  const [compression, setCompression] = useState<Compression>("default")
  const [numbered, setNumbered] = useState<number[]>([])
  const [note, setNote] = useState("")
  const [busy, setBusy] = useState(false)
  const [refusal, setRefusal] = useState<string>()

  const objects = useDumpObjects(options.tables && mode !== "all")
  const databases = useNumberedDatabases(options.databases)
  const nouns = engine.nouns

  // A table's name alone where every one is in the same schema; qualified
  // where the same name could be in two.
  const names = useMemo(() => {
    const list = objects.data ?? []
    const schemas = new Set(list.map((one) => one.schema))
    return list
      .map((one) => (schemas.size > 1 && one.schema ? `${one.schema}.${one.name}` : one.name))
      .sort((a, b) => a.localeCompare(b))
  }, [objects.data])
  const shown = names.filter((name) => name.toLowerCase().includes(filter.trim().toLowerCase()))
  const chosen = objects.data
    ? picked
    : typed
        .split(/[\n,]+/)
        .map((name) => name.trim())
        .filter(Boolean)
  const holding = (databases.data ?? []).filter((one) => (one.size ?? 0) > 0)

  const noteBytes = new TextEncoder().encode(note).length
  const incomplete = mode !== "all" && chosen.length === 0
  const dirty =
    scope !== "all" ||
    mode !== "all" ||
    compression !== "default" ||
    numbered.length > 0 ||
    note.trim() !== ""

  const start = async () => {
    setBusy(true)
    setRefusal(undefined)
    const body: DbBackupRequest = {
      ...(scope === "structure" ? { schemaOnly: true } : {}),
      ...(scope === "data" ? { dataOnly: true } : {}),
      ...tableChoice(mode, chosen),
      ...(compression !== "default" ? { compression } : {}),
      ...(numbered.length > 0 ? { databases: [...numbered].sort((a, b) => a - b) } : {}),
      ...(note.trim() ? { note: note.trim() } : {}),
    }
    try {
      onStarted(await post<Job>(`/databases/${id}/backup`, body))
    } catch (err) {
      // One transfer at a time per connection: the one in the way is shown
      // instead of a refusal the reader can do nothing with.
      if (err instanceof ApiError && err.code === "transfer_running" && err.resource) {
        onRunning(err.resource)
        return
      }
      setRefusal(errorMessage(err))
      setBusy(false)
    }
  }

  const toggle = (name: string) =>
    setPicked((held) =>
      held.includes(name) ? held.filter((one) => one !== name) : [...held, name],
    )

  return (
    <TaskDialog
      title="Back up now"
      description={`Take a dump of ${conn.name} with the options chosen here`}
      subject={databaseSubject(
        conn,
        engine,
        <>
          <FormFact label="Engine">
            {engine.label}
            {summary?.versionNumber ? ` ${summary.versionNumber}` : ""}
          </FormFact>
          {conn.database && !options.databases && (
            <FormFact label={engine.databaseField} mono>
              {conn.database}
            </FormFact>
          )}
        </>,
      )}
      dirty={dirty}
      busy={busy}
      refusal={refusal}
      note="It runs on the server; this page shows it."
      command="Back up now"
      commandIcon={Archive}
      disabled={incomplete || noteBytes > NOTE_BYTES}
      onRun={() => void start()}
      onClose={onClose}
    >
      {(options.schemaOnly || options.dataOnly) && (
        <Field label="What it holds">
          <Segments
            label="What the dump holds"
            value={scope}
            onChange={setScope}
            options={[
              { value: "all", label: "Everything" },
              ...(options.schemaOnly
                ? [{ value: "structure" as const, label: "Structure only" }]
                : []),
              ...(options.dataOnly ? [{ value: "data" as const, label: "Data only" }] : []),
            ]}
          />
        </Field>
      )}

      {options.tables && (
        <Field
          label={`Which ${nouns.objects}`}
          hint={
            mode === "all"
              ? undefined
              : mode === "only"
                ? `Only the ${nouns.objects} ticked are in the dump.`
                : `Every ${nouns.object} but the ones ticked is in the dump.`
          }
          error={
            incomplete && objects.data !== undefined ? `Tick at least one ${nouns.object}.` : ""
          }
        >
          <Segments
            label={`Which ${nouns.objects} the dump holds`}
            value={mode}
            onChange={setMode}
            options={[
              { value: "all", label: `Every ${nouns.object}` },
              { value: "only", label: "Only some" },
              { value: "except", label: "All but some" },
            ]}
          />
          {mode !== "all" &&
            (objects.data ? (
              <div className="space-y-2 pt-1">
                <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1.5">
                  <SearchInput
                    dense
                    aria-label={`Filter the ${nouns.objects}`}
                    placeholder={`Filter ${names.length} ${nouns.objects}`}
                    value={filter}
                    onChange={(event) => setFilter(event.target.value)}
                    containerClassName="sm:w-56"
                  />
                  <span className="numeric text-hint text-muted-foreground">
                    {picked.length} of {names.length} ticked
                  </span>
                </div>
                <ul
                  aria-label={`The ${nouns.objects} of ${conn.database || conn.name}`}
                  className="max-h-48 overflow-y-auto rounded-lg border border-hairline"
                >
                  {shown.length === 0 ? (
                    <li className="px-3 py-2 text-body text-muted-foreground">
                      {names.length === 0
                        ? `This database has no ${nouns.objects}.`
                        : `No ${nouns.object} matches.`}
                    </li>
                  ) : (
                    shown.map((name) => (
                      <li key={name}>
                        <label className="flex min-h-9 cursor-pointer items-center gap-2.5 px-3 py-1 transition-colors hover:bg-row-hover sm:min-h-7">
                          <Checkbox
                            checked={picked.includes(name)}
                            onCheckedChange={() => toggle(name)}
                          />
                          <span className="min-w-0 truncate font-mono text-xs">{name}</span>
                        </label>
                      </li>
                    ))
                  )}
                </ul>
              </div>
            ) : objects.error ? (
              // The list could not be read: the names are typed instead.
              <div className="space-y-1.5 pt-1">
                <Textarea
                  aria-label={`The ${nouns.objects}, one to a line`}
                  value={typed}
                  onChange={(event) => setTyped(event.target.value)}
                  rows={3}
                  spellCheck={false}
                  placeholder={`One ${nouns.object} to a line`}
                  className="font-mono text-xs"
                />
                <FormNote>
                  The {nouns.objects} could not be listed ({errorMessage(objects.error)}), so name
                  them here.
                </FormNote>
              </div>
            ) : (
              <div className="space-y-1.5 pt-2" aria-hidden>
                {[62, 48, 70].map((width) => (
                  <Skeleton key={width} className="h-3" style={{ width: `${width}%` }} />
                ))}
              </div>
            ))}
        </Field>
      )}

      {options.databases && (
        <Field
          label="Which numbered databases"
          hint={
            numbered.length === 0
              ? "Every numbered database that holds a key is in the dump."
              : `Only ${plural(numbered.length, "database")} of this server.`
          }
        >
          {databases.data ? (
            holding.length === 0 ? (
              <FormNote>None of its numbered databases holds a key yet.</FormNote>
            ) : (
              <ChipStrip role="group" aria-label="Numbered databases that hold keys">
                {holding.map((one) => {
                  const number = Number(one.name)
                  const on = numbered.includes(number)
                  return (
                    <FilterChip
                      key={one.name}
                      selected={on}
                      className="font-mono"
                      onClick={() =>
                        setNumbered((held) =>
                          on ? held.filter((n) => n !== number) : [...held, number],
                        )
                      }
                    >
                      db {one.name}
                      <span className="numeric text-micro opacity-60">
                        {(one.size ?? 0).toLocaleString()}
                      </span>
                    </FilterChip>
                  )
                })}
              </ChipStrip>
            )
          ) : databases.error ? (
            <FormNote>
              The numbered databases could not be listed ({errorMessage(databases.error)}); the dump
              takes every one that holds a key.
            </FormNote>
          ) : (
            <Skeleton className="h-7 w-48" aria-hidden />
          )}
        </Field>
      )}

      {options.compression && (
        <Field label="Compression">
          <Segments
            label="How the dump is compressed"
            value={compression}
            onChange={setCompression}
            options={[
              { value: "default", label: "The tool's own" },
              { value: "gzip", label: "gzip", mono: true },
              { value: "none", label: "None" },
            ]}
          />
        </Field>
      )}

      <Field
        label="Note"
        htmlFor={field}
        hint="Kept with the dump: why it was taken."
        error={noteBytes > NOTE_BYTES ? `At most ${NOTE_BYTES} bytes.` : undefined}
      >
        <Input
          id={field}
          value={note}
          onChange={(event) => setNote(event.target.value)}
          placeholder="Before the migration"
          autoComplete="off"
        />
      </Field>
    </TaskDialog>
  )
}
