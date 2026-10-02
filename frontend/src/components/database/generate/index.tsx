"use client"

import { useEffect, useId, useMemo, useState } from "react"
import { ChevronDown, CloudDownload, Code, Sparkles } from "@/components/icons"
import { get, post } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useSessionState, useViewState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { ChoiceCard } from "@/components/choice-card"
import { Segments } from "@/components/deploy/settings/segments"
import { GroupRule } from "@/components/flow"
import { Field, OptionRow } from "@/components/form"
import { LanguageMark } from "@/components/language-icon"
import { SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyState, LoadingRows, Notice } from "@/components/state"
import { ChipCount, ChipStrip, FilterChip, tabClasses } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { BorderBeam } from "@/components/ui/border-beam"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { Skeleton } from "@/components/ui/skeleton"
import { nameHue } from "@/components/database/home/kinds"
import { CodeView, EngineMark, SectionFrame, downloadText } from "@/components/database/kit"
import { ReadError } from "@/components/database/redis/read-error"
import { useDatabase } from "@/components/database/shell/database-context"
import {
  countsLine,
  editorLanguage,
  firstProblem,
  groupTargets,
  optionProblem,
  readResult,
  readTargets,
  requestBody,
  resolveOptions,
  tableName,
  unsupportedReason,
  type OptionValues,
  type OrmOption,
  type OrmResult,
  type OrmTarget,
} from "@/components/database/generate/options"

type Schema = { name: string; system?: boolean; tables: number }
type Head = { defaultSchema: string; schemas: Schema[] }
type OutlineEntry = { schema: string; name: string; type: string }

const NO_OPTIONS: Record<string, OptionValues> = {}
const NO_NAMES: string[] = []

/** How long the page waits after a switch is pressed before it asks again. */
const SETTLE_MS = 450

/**
 * Code from the live schema: pick what to write, set its switches, read the
 * files.
 *
 * It is the file a developer would get from `prisma db pull` or
 * `drizzle-kit pull`, written by the server from the catalogue, for every
 * generator the server lists — the list, the switches each one takes and the
 * engines each one has a connector for all come from the server, so a
 * generator added there appears here. It reads the catalogue and writes
 * nothing, which is why it is offered to every role and on a protected
 * connection.
 *
 * Nothing is asked of the server until the reader asks: choosing a generator
 * or pressing Generate. From then on a change of switch or of scope writes
 * the files again by itself, a moment after the last press. The last
 * generator and each generator's switches are remembered on this screen.
 *
 * The page takes the reading register's exit from headline tiles: what the
 * files hold is counted in the result's own head.
 */
export function Generate() {
  const { id, engine, selection } = useDatabase()

  const catalogue = usePoll(
    async (signal) => readTargets(await get<unknown>("/databases/orm/targets", undefined, signal)),
    0,
    [],
  )
  const targets = catalogue.data
  const groups = useMemo(() => groupTargets(targets ?? []), [targets])

  const [storedTarget, setStoredTarget] = useViewState("databases.generate.target", "prisma")
  const [chosen, setChosen] = useViewState("databases.generate.options", NO_OPTIONS)
  // The remembered generator where this engine has it; else the first one it does have.
  const target = useMemo(() => {
    if (!targets || targets.length === 0) return undefined
    const kept = targets.find((entry) => entry.id === storedTarget)
    if (kept) return kept
    return targets.find((entry) => entry.engines.includes(engine.driver)) ?? targets[0]
  }, [targets, storedTarget, engine.driver])
  const values = useMemo(
    () => (target ? resolveOptions(target, chosen[target.id]) : {}),
    [target, chosen],
  )
  const reason = target ? unsupportedReason(target, engine.driver) : undefined

  /* ---------------------------------------------------------------- scope */

  const head = usePoll(
    async (signal) => {
      const answer = await get<Partial<Head>>(`/databases/${id}/catalog`, undefined, signal)
      return {
        defaultSchema: answer.defaultSchema ?? "",
        schemas: Array.isArray(answer.schemas) ? answer.schemas : [],
      } satisfies Head
    },
    0,
    [id],
  )
  const named = useMemo(
    () => (head.data?.schemas ?? []).filter((schema) => !schema.system),
    [head.data],
  )
  const scoped = engine.can("schemas") && named.length > 1
  const own = selection.schema || head.data?.defaultSchema || ""
  const [pickedSchemas, setPickedSchemas] = useSessionState<string[]>(
    `databases.${id}.generate.schemas`,
    NO_NAMES,
  )
  // What is read: the schemas the reader ticked that still exist, else the connection's own.
  const schemas = useMemo(() => {
    const kept = pickedSchemas.filter((name) => named.some((schema) => schema.name === name))
    return kept.length > 0 ? kept : own ? [own] : []
  }, [pickedSchemas, named, own])
  const schemaKey = schemas.join("\u0000")

  const outline = usePoll(
    async (signal) => {
      const lists = await Promise.all(
        (schemas.length > 0 ? schemas : [""]).map((schema) =>
          get<{ entries?: OutlineEntry[] }>(
            `/databases/${id}/outline`,
            { schema: schema || undefined },
            signal,
          ),
        ),
      )
      return lists.flatMap((list) => (Array.isArray(list?.entries) ? list.entries : []))
    },
    0,
    [id, schemaKey],
    { enabled: head.data !== undefined || head.error !== undefined },
  )
  const several = schemas.length > 1
  // What can be kept or left out is a table: views follow their own switch.
  const tableNames = useMemo(
    () =>
      (outline.data ?? [])
        .filter((entry) => !/view|dictionary/i.test(entry.type))
        .map((entry) => tableName(entry.schema, entry.name, several)),
    [outline.data, several],
  )
  const [pickedTables, setPickedTables] = useSessionState<string[]>(
    `databases.${id}.generate.tables`,
    NO_NAMES,
  )
  const tables = useMemo(
    () => pickedTables.filter((name) => tableNames.includes(name)),
    [pickedTables, tableNames],
  )

  /* ----------------------------------------------------------- generating */

  const problem = target ? firstProblem(target, values) : undefined
  const body = useMemo(
    () => (target ? requestBody(target, values, { schemas, tables }) : null),
    [target, values, schemas, tables],
  )
  const wanted = body ? JSON.stringify(body) : ""
  // Asked for on purpose first; from then on a change asks again by itself,
  // once the reader has stopped pressing.
  const [armed, setArmed] = useState(false)
  const [asked, setAsked] = useState<{ key: string; nonce: number }>({ key: "", nonce: 0 })
  useEffect(() => {
    if (!armed || !wanted || wanted === asked.key) return
    const timer = setTimeout(() => setAsked((held) => ({ ...held, key: wanted })), SETTLE_MS)
    return () => clearTimeout(timer)
  }, [armed, wanted, asked.key])
  const generation = usePoll(
    async () => readResult(await post<unknown>(`/databases/${id}/orm`, JSON.parse(asked.key))),
    0,
    [id, asked.key, asked.nonce],
    { enabled: asked.key !== "" && !reason && !problem },
  )
  // The files on screen stay while the next ones are written.
  const [kept, setKept] = useState<{ result: OrmResult; key: string }>()
  if (generation.data && generation.data !== kept?.result) {
    setKept({ result: generation.data, key: asked.key })
  }
  // …but never under another generator's name.
  const shown =
    generation.data ??
    (generation.error || kept?.result.target !== target?.id ? undefined : kept?.result)
  const writing = generation.loading || (armed && wanted !== asked.key && !reason && !problem)

  const generate = () => {
    if (!wanted) return
    setArmed(true)
    setAsked((held) => ({ key: wanted, nonce: held.nonce + 1 }))
  }
  const choose = (next: OrmTarget) => {
    setStoredTarget(next.id)
    // Choosing a generator is asking for its files.
    if (!unsupportedReason(next, engine.driver)) setArmed(true)
  }
  const setOption = (option: OrmOption, value: boolean | string) => {
    if (!target) return
    setChosen((all) => ({ ...all, [target.id]: { ...all[target.id], [option.id]: value } }))
  }

  /* --------------------------------------------------------------- render */

  if (!targets) {
    return (
      <SectionFrame section="generate">
        {catalogue.error ? (
          <ReadError error={catalogue.error} onRetry={catalogue.refresh} />
        ) : (
          <GenerateSilhouette />
        )}
      </SectionFrame>
    )
  }
  if (targets.length === 0 || !target) {
    return (
      <SectionFrame section="generate">
        <EmptyState
          icon={Code}
          title="No generators"
          description="The server lists nothing it can write from a schema."
          action={
            <Button size="sm" variant="outline" onClick={catalogue.refresh}>
              Ask again
            </Button>
          }
        />
      </SectionFrame>
    )
  }

  return (
    <SectionFrame section="generate">
      <div className="grid min-w-0 items-start gap-8 xl:grid-cols-[21rem_minmax(0,1fr)] [&>*]:min-w-0">
        <div className="space-y-5">
          {groups.map((group, groupIndex) => (
            <section key={group.group} aria-label={group.group} className="space-y-2">
              <GroupRule label={group.group} count={group.targets.length} />
              <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 xl:grid-cols-2">
                {group.targets.map((entry) => {
                  const cannot = unsupportedReason(entry, engine.driver)
                  return (
                    <ChoiceCard
                      key={entry.id}
                      selected={entry.id === target.id}
                      aria-label={cannot ? `${entry.label}, not for ${engine.label}` : entry.label}
                      onClick={() => choose(entry)}
                      className={cn("min-h-0 gap-1 p-2.5", cannot && "opacity-55")}
                      data-target={entry.id}
                      data-group={groupIndex}
                    >
                      <span className="max-w-full truncate text-body font-medium">
                        {entry.label}
                      </span>
                      <Tag className="max-w-full">
                        <LanguageMark language={entry.language} />
                      </Tag>
                    </ChoiceCard>
                  )
                })}
              </div>
            </section>
          ))}
        </div>

        <div className="space-y-6">
          {reason ? (
            <EmptyState
              mark={<EngineMark engine={engine} />}
              title={`${target.label} is not written for ${engine.label}`}
              description={reason}
            />
          ) : (
            <>
              <Panel plain>
                <PanelHeader
                  title={target.label}
                  actions={
                    <Button
                      size="sm"
                      onClick={generate}
                      pending={generation.loading}
                      disabled={Boolean(problem)}
                    >
                      <Sparkles />
                      Generate
                    </Button>
                  }
                />
                <PanelBody className="space-y-5">
                  <p className="max-w-3xl text-body text-muted-foreground">{target.description}</p>

                  <div className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-2">
                    {scoped && (
                      <ChipStrip role="group" aria-label={`The ${engine.nouns.containers} read`}>
                        {named.slice(0, 10).map((schema) => {
                          const on = schemas.includes(schema.name)
                          return (
                            <FilterChip
                              key={schema.name}
                              selected={on}
                              onClick={() => {
                                const next = on
                                  ? schemas.filter((name) => name !== schema.name)
                                  : [...schemas, schema.name]
                                // One is always read: the last cannot be taken off.
                                if (next.length > 0) setPickedSchemas(next)
                              }}
                            >
                              <span
                                aria-hidden
                                className="size-2 shrink-0 rounded-sm"
                                style={{ background: nameHue(schema.name) }}
                              />
                              <span className="font-mono">{schema.name}</span>
                              {schema.tables >= 0 && <ChipCount>{schema.tables}</ChipCount>}
                            </FilterChip>
                          )
                        })}
                      </ChipStrip>
                    )}
                    <TablePicker
                      names={tableNames}
                      picked={tables}
                      loading={outline.loading}
                      onChange={setPickedTables}
                    />
                  </div>

                  {target.options.length > 0 && (
                    <OptionForm target={target} values={values} onChange={setOption} />
                  )}
                </PanelBody>
              </Panel>

              <Output
                key={target.id}
                result={shown}
                error={generation.error}
                onRetry={generate}
                writing={writing}
                problem={problem}
                target={target}
              />
            </>
          )}
        </div>
      </div>
    </SectionFrame>
  )
}

/** A target's switches: each a sentence with its control, as the server words them. */
function OptionForm({
  target,
  values,
  onChange,
}: {
  target: OrmTarget
  values: OptionValues
  onChange: (option: OrmOption, value: boolean | string) => void
}) {
  const id = useId()
  const switches = target.options.filter((option) => option.type === "boolean")
  const others = target.options.filter((option) => option.type !== "boolean")
  return (
    <div className="space-y-4">
      {others.length > 0 && (
        <div className="grid gap-4 sm:grid-cols-2">
          {others.map((option) => {
            const value = String(values[option.id] ?? option.default)
            return option.type === "select" ? (
              <Field key={option.id} label={option.label} hint={option.description}>
                <Segments
                  label={option.label}
                  value={value}
                  options={(option.choices ?? []).map((choice) => ({
                    value: choice.value,
                    label: choice.label,
                  }))}
                  onChange={(next) => onChange(option, next)}
                />
              </Field>
            ) : (
              <Field
                key={option.id}
                label={option.label}
                htmlFor={`${id}-${option.id}`}
                hint={option.description}
                error={optionProblem(option, value)}
              >
                <Input
                  id={`${id}-${option.id}`}
                  value={value}
                  spellCheck={false}
                  className="font-mono sm:max-w-xs"
                  onChange={(event) => onChange(option, event.target.value)}
                />
              </Field>
            )
          })}
        </div>
      )}
      {switches.length > 0 && (
        // Two to a row where there is room: six sentences in one column put
        // the files they shape a screen below the switches.
        <div className="grid gap-x-10 gap-y-1 sm:grid-cols-2">
          {switches.map((option) => (
            // Each in a cell of its own: a row trims its padding as the first
            // or last of a list, and in a grid every one of them is both.
            <div key={option.id} className="min-w-0">
              <OptionRow
                title={option.label}
                hint={option.description}
                checked={values[option.id] === true}
                onCheckedChange={(checked) => onChange(option, checked)}
              />
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

/** Which tables are written: all of them, or the ones ticked. */
function TablePicker({
  names,
  picked,
  loading,
  onChange,
}: {
  names: string[]
  picked: string[]
  loading: boolean
  onChange: (names: string[]) => void
}) {
  const { engine } = useDatabase()
  const [find, setFind] = useState("")
  const needle = find.trim().toLowerCase()
  const listed = needle ? names.filter((name) => name.toLowerCase().includes(needle)) : names
  const every = picked.length === 0
  const toggle = (name: string) => {
    // "Every table" is an empty list; the first one left out names all the others.
    const base = every ? names : picked
    const next = base.includes(name) ? base.filter((entry) => entry !== name) : [...base, name]
    onChange(next.length === names.length ? [] : next)
  }
  return (
    <Popover onOpenChange={(open) => !open && setFind("")}>
      <PopoverTrigger asChild>
        <Button size="sm" variant="outline" disabled={loading || names.length === 0}>
          {loading
            ? `Reading the ${engine.nouns.objects}…`
            : names.length === 0
              ? `No ${engine.nouns.objects}`
              : every
                ? `Every ${engine.nouns.object} (${names.length})`
                : `${picked.length} of ${names.length} ${engine.nouns.objects}`}
          <ChevronDown />
        </Button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-72 p-0">
        <div className="border-b border-hairline p-1.5">
          <SearchInput
            dense
            value={find}
            placeholder={`Find a ${engine.nouns.object}`}
            aria-label={`Find a ${engine.nouns.object}`}
            containerClassName="sm:w-full"
            onChange={(event) => setFind(event.target.value)}
          />
        </div>
        <ul
          aria-label={`The ${engine.nouns.objects} written`}
          className="max-h-64 overflow-y-auto p-1"
        >
          {listed.map((name) => {
            const on = every || picked.includes(name)
            return (
              <li key={name}>
                <label className="flex h-8 min-w-0 cursor-pointer items-center gap-2 rounded-md px-2 hover:bg-menu-hover">
                  <Checkbox checked={on} onCheckedChange={() => toggle(name)} />
                  <span className="min-w-0 truncate font-mono text-xs">{name}</span>
                </label>
              </li>
            )
          })}
          {listed.length === 0 && (
            <li className="px-2 py-3 text-center text-hint text-muted-foreground">
              Nothing here is called that.
            </li>
          )}
        </ul>
        <div className="flex items-center justify-between gap-2 border-t border-hairline p-1.5">
          <span className="px-1 text-hint text-muted-foreground">
            {every ? "All are written" : `${picked.length} written`}
          </span>
          <Button size="xs" variant="ghost" disabled={every} onClick={() => onChange([])}>
            Every one
          </Button>
        </div>
      </PopoverContent>
    </Popover>
  )
}

/** What was written: the notes the generator left, then the files, one to a tab. */
function Output({
  result,
  error,
  onRetry,
  writing,
  problem,
  target,
}: {
  result: OrmResult | undefined
  error: Error | undefined
  onRetry: () => void
  writing: boolean
  problem: string | undefined
  target: OrmTarget
}) {
  const [file, setFile] = useState(0)
  const files = result?.files ?? []
  const at = Math.min(file, Math.max(0, files.length - 1))
  const current = files[at]

  if (error) return <ReadError error={error} onRetry={onRetry} />
  if (!result || !current) {
    return writing ? (
      <div role="status" aria-label="Writing the files" className="space-y-3">
        <div aria-hidden className="h-0.5 overflow-hidden rounded-full bg-meter-track">
          <div className="h-full w-1/3 animate-sweep bg-primary" />
        </div>
        <LoadingRows rows={8} />
      </div>
    ) : (
      <EmptyState
        icon={Code}
        title="Nothing written yet"
        description={
          problem ??
          `Generate reads the catalogue and writes ${target.filename || "the files"} for you to review. Nothing in the database is changed.`
        }
      />
    )
  }

  return (
    <section aria-label="Generated files" className="min-w-0 animate-rise space-y-3">
      {result.warnings.length > 0 && (
        <Notice
          tone="warning"
          title={
            result.warnings.length === 1
              ? "One thing could not be written as it is in the database"
              : `${result.warnings.length} things could not be written as they are in the database`
          }
        >
          <ul className="list-disc space-y-1 pl-4">
            {result.warnings.map((warning, index) => (
              <li key={index}>{warning}</li>
            ))}
          </ul>
        </Notice>
      )}
      <div className="flex min-w-0 flex-wrap items-end gap-x-3 gap-y-1">
        {files.length > 1 ? (
          <div
            role="tablist"
            aria-label="Generated files"
            className="flex min-w-0 flex-1 [scrollbar-width:none] items-stretch overflow-x-auto border-b border-hairline [&::-webkit-scrollbar]:hidden"
          >
            {files.map((entry, index) => (
              <button
                key={entry.filename}
                type="button"
                role="tab"
                aria-selected={index === at}
                className={cn(tabClasses(index === at, "h-9"), "font-mono text-xs")}
                onClick={() => setFile(index)}
              >
                {entry.filename}
              </button>
            ))}
          </div>
        ) : (
          <span className="min-w-0 flex-1" />
        )}
        <p className="numeric shrink-0 pb-1 text-hint text-muted-foreground">
          {countsLine(result.counts)}
        </p>
      </div>
      <div className="relative">
        <CodeView
          code={current.content}
          language={editorLanguage(result.language)}
          filename={current.filename}
          className="h-[32rem] max-h-[70svh]"
          actions={
            files.length > 1 && (
              <Button
                size="xs"
                variant="ghost"
                onClick={() => {
                  for (const entry of files) downloadText(entry.content, entry.filename)
                }}
              >
                <CloudDownload />
                All {files.length} files
              </Button>
            )
          }
        />
        {writing && (
          <span aria-hidden className="pointer-events-none absolute -inset-px rounded-xl">
            <BorderBeam size={120} duration={4} />
          </span>
        )}
      </div>
      <p className="text-hint text-muted-foreground">
        A reviewed starting point: check types, defaults and relation names before committing it.
      </p>
    </section>
  )
}

/** The page's shape before the catalogue has come: a column of generators and the block beside it. */
function GenerateSilhouette() {
  return (
    <div
      role="status"
      aria-label="Reading the generators"
      className="grid min-w-0 items-start gap-8 xl:grid-cols-[21rem_minmax(0,1fr)]"
    >
      <div aria-hidden className="space-y-5">
        {[6, 2, 3].map((cards, group) => (
          <div key={group} className="space-y-2">
            <Skeleton className="h-2.5 w-24" />
            <div className="grid grid-cols-2 gap-2">
              {Array.from({ length: cards }, (_, card) => (
                <Skeleton key={card} className="h-14 rounded-xl" />
              ))}
            </div>
          </div>
        ))}
      </div>
      <div aria-hidden className="space-y-4">
        <Skeleton className="h-4 w-32" />
        <Skeleton className="h-3 w-80 max-w-full" />
        <Skeleton className="h-64 w-full rounded-xl" />
      </div>
    </div>
  )
}
