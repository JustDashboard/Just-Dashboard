"use client"

import { useEffect, useId, useMemo, useRef, useState } from "react"
import { ChevronDown, CloudDownload, Code, Sparkles, Warning } from "@/components/icons"
import { get, post } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useSessionState, useViewState } from "@/lib/view-state"
import { useMediaQuery } from "@/hooks/use-mobile"
import { usePoll } from "@/hooks/use-poll"
import { ChoiceCard } from "@/components/choice-card"
import { Segments } from "@/components/deploy/settings/segments"
import { GroupRule } from "@/components/flow"
import { Disclosure, Field, OptionRow } from "@/components/form"
import { LanguageMark } from "@/components/language-icon"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyState, LoadingRows, Notice } from "@/components/state"
import { ChipCount, ChipStrip, FilterChip, tabClasses } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { BorderBeam } from "@/components/ui/border-beam"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { nameHue } from "@/components/database/home/kinds"
import { CodeView, EngineMark, SectionFrame } from "@/components/database/kit"
import { ReadError } from "@/components/database/redis/read-error"
import { useDatabase } from "@/components/database/shell/database-context"
import {
  countsLine,
  editorLanguage,
  firstProblem,
  groupTargets,
  optionProblem,
  optionsLine,
  readResult,
  readTargets,
  requestBody,
  resolveOptions,
  startingTarget,
  tableName,
  unsupportedReason,
  type OptionValues,
  type OrmOption,
  type OrmResult,
  type OrmTarget,
} from "@/components/database/generate/options"
import { TablePicker } from "@/components/database/generate/table-picker"
import { downloadArchive } from "@/components/database/generate/zip"

type Schema = { name: string; system?: boolean; tables: number }
type Head = { defaultSchema: string; schemas: Schema[] }
type OutlineEntry = { schema: string; name: string; type: string }

const NO_OPTIONS: Record<string, OptionValues> = {}
const NO_NAMES: string[] = []

/** How long the page waits after a switch is pressed before it asks again. */
const SETTLE_MS = 450
/** How many schemas are chips; the rest sit behind one menu. */
const SCHEMA_CHIPS = 4

/**
 * Code from the live schema: pick what to write, read the files.
 *
 * It is the file a developer would get from `prisma db pull` or
 * `drizzle-kit pull`, written by the server from the catalogue, for every
 * generator the server lists — the list, the switches each one takes and the
 * engines each one has a connector for all come from the server, so a
 * generator added there appears here. It reads the catalogue and writes
 * nothing, which is why it is offered to every role and on a protected
 * connection.
 *
 * What the reader came for is the files, so the files are what the page gives
 * its height to: the generator's name and its one command, a line of scope,
 * the switches folded into one row that says what they are set to, and then
 * the code down to the bottom of the window. Where the generators and the
 * files cannot stand side by side the generators fold to the chosen one, so
 * choosing a generator is not answered two screens further down.
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
  // A generator the reader picked on this visit is shown whatever the engine
  // makes of it — with the server's reason, where it has no connector. One
  // only remembered from another connection is not: the page opens on the
  // first generator this engine does have, not on a refusal.
  const [pickedHere, setPickedHere] = useState(false)
  const target = useMemo(
    () => startingTarget(targets ?? [], storedTarget, engine.driver, pickedHere),
    [targets, storedTarget, engine.driver, pickedHere],
  )
  const values = useMemo(
    () => (target ? resolveOptions(target, chosen[target.id]) : {}),
    [target, chosen],
  )
  const reason = target ? unsupportedReason(target, engine.driver) : undefined

  // Beside the files where there is room for both; folded to the chosen one where there is not.
  const beside = useMediaQuery("(min-width: 1280px)")
  const [browsing, setBrowsing] = useState(false)

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
    setPickedHere(true)
    setStoredTarget(next.id)
    setBrowsing(false)
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

  const generators = (
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
                  <span
                    title={cannot ?? entry.description}
                    className="max-w-full truncate text-body font-medium"
                  >
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
  )

  return (
    <SectionFrame section="generate">
      <div className="grid min-w-0 items-start gap-x-8 gap-y-5 xl:grid-cols-[21rem_minmax(0,1fr)] [&>*]:min-w-0">
        {beside && generators}

        <Panel plain>
          <PanelHeader
            title={
              <span className="flex min-w-0 items-center gap-2.5">
                <span className="min-w-0 truncate">{target.label}</span>
                <Tag className="shrink-0">
                  <LanguageMark language={target.language} />
                </Tag>
              </span>
            }
            actions={
              <>
                {!beside && (
                  <Button
                    size="sm"
                    variant="outline"
                    aria-expanded={browsing}
                    onClick={() => setBrowsing(!browsing)}
                  >
                    {browsing ? "Keep this one" : `All ${targets.length} generators`}
                  </Button>
                )}
                {!reason && (
                  <Button
                    size="sm"
                    onClick={generate}
                    pending={generation.loading}
                    disabled={Boolean(problem)}
                  >
                    <Sparkles />
                    Generate
                  </Button>
                )}
              </>
            }
          />
          <PanelBody className="space-y-2">
            {!beside && browsing ? (
              // In the files' place, not under them: the list is what was asked for.
              generators
            ) : reason ? (
              <EmptyState
                mark={<EngineMark engine={engine} />}
                title={`${target.label} is not written for ${engine.label}`}
                description={reason}
              />
            ) : (
              <>
                <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2">
                  {scoped && (
                    <SchemaChips
                      named={named}
                      picked={schemas}
                      // One is always read: the last cannot be taken off.
                      onChange={(next) => next.length > 0 && setPickedSchemas(next)}
                    />
                  )}
                  <TablePicker
                    names={tableNames}
                    picked={tables}
                    loading={outline.loading}
                    onChange={setPickedTables}
                  />
                </div>

                {target.options.length > 0 && (
                  <OptionFold target={target} values={values} onChange={setOption} />
                )}

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
          </PanelBody>
        </Panel>
      </div>
    </SectionFrame>
  )
}

/**
 * The schemas read, as chips: the ones ticked first, then the others while
 * there is room, and every one past that in a menu — so a server that names
 * thirty has thirty to choose from, and what is ticked is always in sight.
 */
function SchemaChips({
  named,
  picked,
  onChange,
}: {
  named: Schema[]
  picked: string[]
  onChange: (names: string[]) => void
}) {
  const { engine } = useDatabase()
  const toggle = (name: string) =>
    onChange(picked.includes(name) ? picked.filter((entry) => entry !== name) : [...picked, name])
  const on = named.filter((schema) => picked.includes(schema.name))
  const off = named.filter((schema) => !picked.includes(schema.name))
  const chips = [...on, ...off.slice(0, Math.max(0, SCHEMA_CHIPS - on.length))]
  const rest = off.slice(Math.max(0, SCHEMA_CHIPS - on.length))
  return (
    <ChipStrip role="group" aria-label={`The ${engine.nouns.containers} read`}>
      {chips.map((schema) => (
        <FilterChip
          key={schema.name}
          selected={picked.includes(schema.name)}
          onClick={() => toggle(schema.name)}
        >
          <SchemaMark name={schema.name} />
          <span className="font-mono">{schema.name}</span>
          {schema.tables >= 0 && <ChipCount>{schema.tables}</ChipCount>}
        </FilterChip>
      ))}
      {rest.length > 0 && (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <FilterChip aria-label={`${rest.length} more ${engine.nouns.containers}`}>
              {rest.length} more
              <ChevronDown className="size-3" />
            </FilterChip>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start" className="max-h-72 w-64 overflow-y-auto">
            {rest.map((schema) => (
              <DropdownMenuCheckboxItem
                key={schema.name}
                checked={false}
                onCheckedChange={() => toggle(schema.name)}
              >
                <SchemaMark name={schema.name} />
                <span className="min-w-0 flex-1 truncate font-mono text-xs">{schema.name}</span>
                {schema.tables >= 0 && (
                  <span className="numeric text-hint text-muted-foreground">{schema.tables}</span>
                )}
              </DropdownMenuCheckboxItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>
      )}
    </ChipStrip>
  )
}

function SchemaMark({ name }: { name: string }) {
  return (
    <span
      aria-hidden
      className="size-2 shrink-0 rounded-sm"
      style={{ background: nameHue(name) }}
    />
  )
}

/**
 * A target's switches, folded into one row that says what they are set to.
 *
 * Open, each is a sentence with its control, as the server words them. Shut —
 * as it is until the reader opens it, and it stays how they left it — the row
 * names what differs from the server's defaults, which is what a reader
 * coming back to a file wants to know about it.
 */
function OptionFold({
  target,
  values,
  onChange,
}: {
  target: OrmTarget
  values: OptionValues
  onChange: (option: OrmOption, value: boolean | string) => void
}) {
  const id = useId()
  const [open, setOpen] = useViewState("databases.generate.options.open", false)
  const switches = target.options.filter((option) => option.type === "boolean")
  const others = target.options.filter((option) => option.type !== "boolean")
  // An option that cannot be sent is not left behind a closed fold.
  const broken = firstProblem(target, values) !== undefined
  return (
    <Disclosure
      quiet
      open={open || broken}
      onOpenChange={setOpen}
      summary={
        <span className="flex min-w-0 items-baseline gap-2">
          Options
          <ChipCount>{target.options.length}</ChipCount>
        </span>
      }
      facts={optionsLine(target, values)}
    >
      <div className="@container space-y-4">
        {others.length > 0 && (
          <div className="grid gap-4 @xl:grid-cols-2 @4xl:grid-cols-3">
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
          // As many to a row as keep a switch beside its sentence: one column
          // across a wide page left each switch a hand's width from its words.
          <div className="grid gap-x-10 gap-y-1 @xl:grid-cols-2 @4xl:grid-cols-3">
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
    </Disclosure>
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
  const ids = useId()
  const [file, setFile] = useState(0)
  const strip = useRef<HTMLDivElement>(null)
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
          `${target.description} Generate reads the catalogue and writes ${target.filename || "the files"} for you to review; nothing in the database is changed.`
        }
      />
    )
  }

  const onTabKey = (event: React.KeyboardEvent) => {
    const to =
      event.key === "ArrowRight"
        ? (at + 1) % files.length
        : event.key === "ArrowLeft"
          ? (at + files.length - 1) % files.length
          : event.key === "Home"
            ? 0
            : event.key === "End"
              ? files.length - 1
              : -1
    if (to < 0) return
    event.preventDefault()
    setFile(to)
    const tab = strip.current?.querySelector<HTMLElement>(`[data-file="${to}"]`)
    tab?.focus()
    tab?.scrollIntoView({ block: "nearest", inline: "nearest" })
  }

  return (
    <section aria-label="Generated files" className="min-w-0 animate-rise space-y-2">
      {problem && (
        // The files below are from before the change that cannot be sent.
        <Notice tone="warning" icon={Warning} title="Not written again">
          {problem} The files shown are the ones from before this change.
        </Notice>
      )}
      {result.warnings.length > 0 && <Warnings warnings={result.warnings} />}
      {files.length > 1 && (
        <div
          ref={strip}
          role="tablist"
          aria-label="Generated files"
          className="scroll-affordance flex min-w-0 [scrollbar-width:none] items-stretch overflow-x-auto border-b border-hairline [--panel-ground:var(--background)] [&::-webkit-scrollbar]:hidden"
          onKeyDown={onTabKey}
        >
          {files.map((entry, index) => (
            <button
              key={entry.filename}
              type="button"
              role="tab"
              id={`${ids}-tab-${index}`}
              data-file={index}
              aria-selected={index === at}
              aria-controls={index === at ? `${ids}-panel` : undefined}
              tabIndex={index === at ? 0 : -1}
              className={cn(tabClasses(index === at, "h-9"), "shrink-0 font-mono text-xs")}
              onClick={() => setFile(index)}
            >
              {entry.filename}
            </button>
          ))}
        </div>
      )}
      <div
        role={files.length > 1 ? "tabpanel" : undefined}
        id={`${ids}-panel`}
        aria-labelledby={files.length > 1 ? `${ids}-tab-${at}` : undefined}
        className={cn("relative transition-opacity", problem && "opacity-60")}
      >
        <CodeView
          code={current.content}
          language={editorLanguage(result.language)}
          filename={current.filename}
          // The file's name, and what the files hold between them.
          label={
            <span className="flex min-w-0 items-baseline gap-3">
              <span className="min-w-0 truncate font-mono">{current.filename}</span>
              <span className="numeric shrink-0 text-hint text-muted-foreground max-sm:hidden">
                {countsLine(result.counts)}
              </span>
            </span>
          }
          // Down to the bottom of the window: the files are what the page is for.
          className="h-[calc(100svh-20rem)] min-h-80"
          actions={
            files.length > 1 && (
              <Button
                size="xs"
                variant="ghost"
                onClick={() => downloadArchive(files, `${result.target || target.id}.zip`)}
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

/**
 * What the generator could not write as it is in the database: one line above
 * the code that says how many there are and the first of them, and opens to
 * the rest. A notice of several sentences stood between the reader and the
 * files every time; this says the same and stays out of the way.
 */
function Warnings({ warnings }: { warnings: string[] }) {
  return (
    <Disclosure
      quiet
      summary={
        <span className="flex min-w-0 items-center gap-2">
          <Tag tone="warning" icon={Warning} className="shrink-0">
            {warnings.length === 1 ? "1 note" : `${warnings.length} notes`}
          </Tag>
          <span className="min-w-0 truncate text-xs font-normal text-muted-foreground">
            {warnings[0]}
          </span>
        </span>
      }
    >
      <ul
        aria-label="What could not be written as it is in the database"
        className="list-disc space-y-1 pl-9 text-xs leading-relaxed text-muted-foreground"
      >
        {warnings.map((warning, index) => (
          <li key={index}>{warning}</li>
        ))}
      </ul>
    </Disclosure>
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
      <div aria-hidden className="space-y-5 max-xl:hidden">
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
        <Skeleton className="h-7 w-80 max-w-full" />
        <Skeleton className="h-[calc(100svh-20rem)] min-h-80 w-full rounded-xl" />
      </div>
    </div>
  )
}
