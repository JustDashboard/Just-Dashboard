"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import Link from "next/link"
import { ChevronDown, Cross, MagnifyingGlass, StopCircle } from "@/components/icons"
import { get } from "@/lib/api"
import { plural } from "@/lib/format"
import { cn } from "@/lib/utils"
import { useMemoryState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { IconAction } from "@/components/icon-action"
import { SearchInput } from "@/components/page"
import { EmptyState, Notice } from "@/components/state"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Skeleton } from "@/components/ui/skeleton"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { encodeFilters } from "@/components/database/data/filters"
import { KindGlyph } from "@/components/database/data/kinds"
import { TablePicker } from "@/components/database/generate/table-picker"
import { KIND_HUE } from "@/components/database/grid"
import { nameHue } from "@/components/database/home/kinds"
import { EngineMark, SectionFrame } from "@/components/database/kit"
import { ReadError } from "@/components/database/redis/read-error"
import { useDatabase } from "@/components/database/shell/database-context"
import {
  PER_TABLE,
  countText,
  groupHits,
  isKeyed,
  otherCells,
  readResult,
  rowFilters,
  scanFilters,
  tableFilters,
  tableKey,
  tableMatches,
  type BrowseAnswer,
  type Hit,
  type OtherCell,
  type SearchMatch,
  type SearchResult,
  type TableHits,
} from "@/components/database/search/search"

/** What the last search came back with, kept with the question it answers. */
type Held = { q: string; scope: string; tables: string; result: SearchResult; tookMs: number }

type Schema = { name: string; system?: boolean; tables: number }
type Head = { defaultSchema: string; schemas: Schema[] }
type Outline = {
  tables: Record<string, string[]>
  entries: { id: string; schema: string; name: string; type: string }[]
}

/** How many schemas are chips; the rest sit behind one menu. */
const CHIPS = 8
/** Every schema the connection can name, rather than one. */
const EVERYWHERE = "all"
/** How long the page waits after a table is ticked before it reads again. */
const SETTLE_MS = 600
const NO_TABLES: string[] = []

/** The tables an address names: a JSON list, or nothing a reader could have meant. */
function tablesOf(param: string): string[] {
  if (!param) return NO_TABLES
  try {
    const read: unknown = JSON.parse(param)
    const names = Array.isArray(read) ? read.filter((name) => typeof name === "string") : []
    return names.length > 0 ? names : NO_TABLES
  } catch {
    return NO_TABLES
  }
}

/**
 * Find a value anywhere: one field, and every table read for it.
 *
 * "This order id appears somewhere — which table?" is a dozen statements
 * typed by hand, one per table somebody remembers, and the answer is in the
 * one they forgot. The server reads them all in one bounded request; what
 * comes back is drawn by table, each row with the cells that hold the value
 * marked, and each row is a way into the table editor filtered to itself.
 *
 * The scope is said before the scan, not after it: which schema, and — within
 * one schema — which of its tables. The server's search reads a whole schema
 * and takes no list of tables, so a search of ticked tables is made of the
 * table editor's own read instead, one table at a time, each asked for the
 * rows in which any column holds the value. That read also says the table's
 * key and whether it holds more rows than are shown, which the whole-schema
 * search does not.
 *
 * It is asked on purpose, never as the reader types: a scan of every table is
 * the most expensive read this section makes. The question is in the address
 * (`q`, which schema, which tables), so a link reproduces it; the answer is
 * kept in memory for the tab, so coming back from a row costs no second scan
 * — and never reaches Web Storage, because it is rows of somebody's data.
 *
 * The page takes the reading register's exit from headline tiles: its
 * figures are the counts on the table chips and beside each table's name.
 */
export function SqlSearch() {
  const { id, engine, selection, select, goto, param, href } = useDatabase()
  const q = param("q")
  const everywhere = param("scope") === EVERYWHERE

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
  const schemas = useMemo(
    () => (head.data?.schemas ?? []).filter((schema) => !schema.system),
    [head.data],
  )
  const scoped = engine.can("schemas") && schemas.length > 1
  const own = selection.schema || head.data?.defaultSchema || ""
  // The scope as the server is asked: one schema, or "" for every one.
  const scope = everywhere && scoped ? "" : own
  const headSettled = head.data !== undefined || head.error !== undefined
  const oneSchema = !(everywhere && scoped)

  /* --------------------------------------------------------------- tables */

  // What the one schema holds, for the picker and for a search of ticked tables.
  const outline = usePoll(
    async (signal) => {
      const answer = await get<Partial<Outline>>(
        `/databases/${id}/outline`,
        { schema: scope || undefined },
        signal,
      )
      return {
        tables: answer.tables ?? {},
        entries: Array.isArray(answer.entries) ? answer.entries : [],
      } satisfies Outline
    },
    0,
    [id, scope],
    { enabled: headSettled && oneSchema },
  )
  const columnsOf = useMemo(() => {
    const columns = new Map<string, string[]>()
    for (const entry of outline.data?.entries ?? []) {
      // Views are left out, as the search of a whole schema leaves them out.
      if (/view|dictionary/i.test(entry.type)) continue
      if (scope && entry.schema !== scope) continue
      columns.set(entry.name, outline.data?.tables[entry.id] ?? [])
    }
    return columns
  }, [outline.data, scope])
  const tableNames = useMemo(() => [...columnsOf.keys()], [columnsOf])

  const stated = param("tables")
  const asked = useMemo(() => tablesOf(stated), [stated])
  // A table ticked is not read at once: the next one may be ticked a moment later.
  const [ticking, setTicking] = useState<string[] | null>(null)
  useEffect(() => {
    if (ticking === null) return
    const timer = setTimeout(() => {
      select({ tables: ticking.length > 0 ? JSON.stringify(ticking) : null })
      setTicking(null)
    }, SETTLE_MS)
    return () => clearTimeout(timer)
  }, [ticking, select])
  // The ones the address names that this schema still has; none means every table.
  const tables = useMemo(
    () => (oneSchema && outline.data ? asked.filter((name) => columnsOf.has(name)) : NO_TABLES),
    [oneSchema, outline.data, asked, columnsOf],
  )
  const tablesKey = tables.join("\u0000")
  // The address names tables this page has not been able to check yet.
  const awaitingTables = oneSchema && asked.length > 0 && !outline.data && !outline.error

  /* ---------------------------------------------------------------- asking */

  const [draft, setDraft] = useState(q)
  const [draftFor, setDraftFor] = useState(q)
  // The address moved under the page (Back, a pasted link): the field follows it.
  if (draftFor !== q) {
    setDraftFor(q)
    setDraft(q)
  }

  const [kept, setKept] = useMemoryState<Held | null>(`databases.${id}.search.held`, null)
  const [again, setAgain] = useState(0)
  const [stopped, setStopped] = useState("")
  const [progress, setProgress] = useState(0)
  const asking = `${q}\u0000${scope}\u0000${tablesKey}\u0000${again}`
  const sameQuestion = (held: Held | null) =>
    held !== null && held.q === q && held.scope === scope && held.tables === tablesKey
  // What was found for this very question a moment ago is not asked for again:
  // coming back from a row costs no second scan.
  const remembered = again === 0 && sameQuestion(kept)
  // One search at a time, and the one in flight is dropped when another is
  // asked for: two scans that finished out of order used to show the answer
  // to the question before last.
  const search = usePoll(
    async (signal) => {
      const started = performance.now()
      setProgress(0)
      const result =
        tables.length > 0
          ? await scanTables(
              id,
              scope,
              tables.map((name) => ({ name, columns: columnsOf.get(name) ?? [] })),
              q,
              signal,
              setProgress,
            )
          : readResult(
              await get<unknown>(
                `/databases/${id}/search`,
                { q, schema: scope || undefined },
                signal,
              ),
            )
      const found: Held = {
        q,
        scope,
        tables: tablesKey,
        result,
        tookMs: performance.now() - started,
      }
      setKept(found)
      return found
    },
    0,
    [id, q, scope, tablesKey, again],
    {
      enabled:
        q.trim() !== "" && headSettled && !awaitingTables && !remembered && stopped !== asking,
    },
  )
  const busy = search.loading
  const failed = search.error
  const held = search.data ?? (sameQuestion(kept) ? kept : null)
  const halted = stopped === asking && !held

  const submit = () => {
    const typed = draft.trim()
    // While a scan is out, Enter starts no second one: Stop is the way to end it.
    if (!typed || busy) return
    // Tables ticked a moment ago are part of the question being asked now,
    // whether or not the address has caught up with them.
    const pending = ticking
    if (pending !== null) setTicking(null)
    const names = pending ?? tables
    const listed = names.length > 0 ? JSON.stringify(names) : null
    if (typed === q) {
      if (pending !== null) select({ tables: listed })
      else setAgain((n) => n + 1)
    }
    // A new question is a step of history: Back returns to the one before it.
    else goto("search", { q: typed, scope: everywhere ? EVERYWHERE : null, tables: listed })
  }
  const stop = () => setStopped(asking)

  const groups = useMemo(() => (held ? groupHits(held.result, held.q) : []), [held])
  const [only, setOnly] = useState<string | null>(null)
  const shown = only && groups.some((group) => group.key === only) ? only : null
  const visible = shown ? groups.filter((group) => group.key === shown) : groups
  const keys = usePrimaryKeys(id, groups, held?.result.keys)

  const scopeWord = scope || (scoped ? `every ${engine.nouns.container}` : "this database")
  const whereWord =
    tables.length > 0 ? `${plural(tables.length, engine.nouns.object)} of ${scopeWord}` : scopeWord
  const chips = schemas.slice(0, CHIPS)
  const rest = schemas.slice(CHIPS)
  // Another schema has other tables: the ticks do not follow.
  const pick = (name: string) => select({ schema: name, scope: null, tables: null })
  const ticked = ticking ?? tables

  return (
    <SectionFrame section="search">
      <div className="min-w-0 space-y-3">
        <form
          role="search"
          className="flex min-w-0 items-center gap-2"
          onSubmit={(event) => {
            event.preventDefault()
            submit()
          }}
        >
          <SearchInput
            value={draft}
            placeholder="An id, an email, an order number…"
            aria-label="The value to find"
            // On a phone the field gives way to its button, which stays beside it.
            containerClassName="min-w-0 flex-1 sm:w-[28rem] sm:max-w-full sm:flex-none"
            onChange={(event) => setDraft(event.target.value)}
            trailing={
              draft && (
                <IconAction
                  type="button"
                  label="Clear"
                  className="size-6"
                  onClick={() => {
                    setDraft("")
                    select({ q: null })
                  }}
                >
                  <Cross />
                </IconAction>
              )
            }
          />
          {/* Two buttons, not one that changes its type: Stop turned into the
              form's submit under the very press that stopped the scan, and the
              browser then submitted the form — a second scan. */}
          {busy ? (
            <Button
              key="stop"
              type="button"
              size="sm"
              variant="outline"
              className="max-sm:h-10"
              onClick={stop}
            >
              <StopCircle />
              Stop
            </Button>
          ) : (
            <Button
              key="search"
              type="submit"
              size="sm"
              className="max-sm:h-10"
              disabled={!draft.trim()}
            >
              <MagnifyingGlass />
              Search
            </Button>
          )}
        </form>

        {/* Where it looks, said before it looks: the schema, then the tables of it. */}
        <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2">
          {scoped && (
            <ChipStrip role="group" aria-label={`Which ${engine.nouns.container} is read`}>
              {chips.map((schema) => (
                <FilterChip
                  key={schema.name}
                  selected={!everywhere && schema.name === own}
                  onClick={() => pick(schema.name)}
                >
                  <SchemaMark name={schema.name} />
                  <span className="font-mono">{schema.name}</span>
                  {schema.tables >= 0 && <ChipCount>{schema.tables}</ChipCount>}
                </FilterChip>
              ))}
              {rest.length > 0 && (
                <DropdownMenu>
                  <DropdownMenuTrigger asChild>
                    <FilterChip
                      selected={!everywhere && rest.some((schema) => schema.name === own)}
                    >
                      {!everywhere && rest.some((schema) => schema.name === own) ? (
                        <span className="font-mono">{own}</span>
                      ) : (
                        `${rest.length} more`
                      )}
                      <ChevronDown className="size-3" />
                    </FilterChip>
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align="start" className="max-h-72 w-60 overflow-y-auto">
                    {rest.map((schema) => (
                      <DropdownMenuItem key={schema.name} onSelect={() => pick(schema.name)}>
                        <SchemaMark name={schema.name} />
                        <span className="min-w-0 flex-1 truncate font-mono text-xs">
                          {schema.name}
                        </span>
                        {schema.tables >= 0 && (
                          <span className="numeric text-hint text-muted-foreground">
                            {schema.tables}
                          </span>
                        )}
                      </DropdownMenuItem>
                    ))}
                  </DropdownMenuContent>
                </DropdownMenu>
              )}
              <FilterChip
                selected={everywhere}
                onClick={() => select({ scope: EVERYWHERE, tables: null })}
              >
                Every {engine.nouns.container}
              </FilterChip>
            </ChipStrip>
          )}
          <TablePicker
            names={tableNames}
            picked={ticked}
            loading={oneSchema && outline.loading && !outline.data}
            what="read"
            disabledWhy={
              oneSchema
                ? undefined
                : `Pick one ${engine.nouns.container} to choose which of its ${engine.nouns.objects} are read`
            }
            onChange={setTicking}
          />
        </div>
      </div>

      {busy ? (
        <Searching
          word={whereWord}
          progress={tables.length > 0 ? { done: progress, of: tables.length } : undefined}
        />
      ) : failed ? (
        <ReadError error={failed} onRetry={() => setAgain((n) => n + 1)} />
      ) : halted ? (
        <Notice tone="warning" title="Stopped before it finished">
          <p>
            Nothing is shown for a scan that did not end: what it had found so far would read as the
            whole answer.
          </p>
          <Button
            size="xs"
            variant="outline"
            className="mt-2"
            onClick={() => setAgain((n) => n + 1)}
          >
            Search again
          </Button>
        </Notice>
      ) : !q.trim() || !held ? (
        <EmptyState
          mark={<EngineMark engine={engine} />}
          title="Find a value anywhere"
          description={
            tables.length > 0
              ? `Every column of the ${plural(tables.length, engine.nouns.object)} ticked is compared as text, so an id and a date are found as readily as a name. At most ${PER_TABLE} rows are shown from each.`
              : `Every column of every ${engine.nouns.object} in ${scopeWord} is compared as text, so an id and a date are found as readily as a name. Views are left out, and the read is bounded: at most 60 ${engine.nouns.objects}, and ${PER_TABLE} rows from each.`
          }
        />
      ) : (
        <div
          key={`${held.q}\u0000${held.scope}\u0000${held.tables}`}
          className="min-w-0 animate-rise space-y-6"
        >
          <div className="space-y-3">
            <p role="status" className="text-body">
              {groups.length === 0 ? (
                <>
                  No row contains <Needle>{held.q}</Needle>.
                </>
              ) : (
                <>
                  <Needle>{held.q}</Needle> is in{" "}
                  <span className="numeric font-medium">
                    {plural(groups.length, engine.nouns.object)}
                  </span>
                  :{" "}
                  <span className="numeric font-medium">
                    {plural(held.result.matches.length, "row")}
                  </span>{" "}
                  {held.result.matches.length === 1 ? "is" : "are"} shown, at most {PER_TABLE} from
                  each.
                </>
              )}{" "}
              <span className="text-muted-foreground">
                {plural(held.result.tablesScanned, engine.nouns.object)} of {scopeWord} read in{" "}
                {held.tookMs < 1000
                  ? `${Math.round(held.tookMs)} ms`
                  : `${(held.tookMs / 1000).toFixed(1)} s`}
                .
              </span>
            </p>
            {groups.length > 1 && (
              <ChipStrip role="group" aria-label={`Narrow to one ${engine.nouns.object}`}>
                <FilterChip selected={shown === null} onClick={() => setOnly(null)}>
                  All
                  <ChipCount>{held.result.matches.length}</ChipCount>
                </FilterChip>
                {groups.map((group) => (
                  <FilterChip
                    key={group.key}
                    selected={shown === group.key}
                    onClick={() => setOnly(shown === group.key ? null : group.key)}
                  >
                    <KindGlyph kind="table" className="size-3" />
                    <span className="font-mono">{group.table}</span>
                    <ChipCount>{countText(group)}</ChipCount>
                  </FilterChip>
                ))}
              </ChipStrip>
            )}
          </div>

          {/* Only where the search itself stopped short. A table showing its
              first rows is not a warning: its count says so with a plus. */}
          {held.result.truncated && !held.tables && (
            <Notice tone="warning" title="There may be more than this">
              The search stops at 60 {engine.nouns.objects}, {PER_TABLE} rows from each and 200 rows
              in all. Open a {engine.nouns.object} below to see every row of it that matches, tick
              the {engine.nouns.objects} to read, or search a narrower value.
            </Notice>
          )}
          {(held.result.tablesSkipped?.length ?? 0) > 0 && (
            <p className="text-hint text-muted-foreground">
              Not read — usually for want of a permission:{" "}
              <span className="font-mono">{held.result.tablesSkipped!.join(", ")}</span>
            </p>
          )}

          {groups.length === 0 ? (
            <EmptyState
              icon={MagnifyingGlass}
              title="Nothing found"
              description={
                held.result.tablesScanned === 0
                  ? `There was no ${engine.nouns.object} to read in ${scopeWord}.`
                  : held.tables
                    ? `It is in none of the ${engine.nouns.objects} ticked. The others of ${scopeWord} were not read.`
                    : scoped && !everywhere
                      ? `It is in none of the ${engine.nouns.objects} of ${scopeWord}. Another ${engine.nouns.container} may hold it.`
                      : "It is compared as text, any case, anywhere in a cell: a shorter piece of the value may find it."
              }
              action={
                held.tables ? (
                  <Button size="sm" variant="outline" onClick={() => select({ tables: null })}>
                    Search every {engine.nouns.object} of {scopeWord}
                  </Button>
                ) : (
                  scoped &&
                  !everywhere && (
                    <Button
                      size="sm"
                      variant="outline"
                      onClick={() => select({ scope: EVERYWHERE, tables: null })}
                    >
                      Search every {engine.nouns.container}
                    </Button>
                  )
                )
              }
            />
          ) : (
            visible.map((group) => (
              <TableGroup
                key={group.key}
                group={group}
                primaryKey={keys[group.key]}
                everyHref={href("data", {
                  schema: group.schema || null,
                  table: group.table,
                  filters: encodeFilters(tableFilters(group, held.q)),
                  match: group.columns.length > 1 ? "any" : null,
                })}
                hrefFor={(hit, primaryKey) =>
                  href("data", {
                    schema: group.schema || null,
                    table: group.table,
                    filters: encodeFilters(rowFilters(hit.match, primaryKey)),
                  })
                }
                qualified={scope === ""}
              />
            ))
          )}
        </div>
      )}
    </SectionFrame>
  )
}

/**
 * A search of the ticked tables, made of the table editor's own read: each
 * table asked for the rows in which any of its columns holds the value, a few
 * tables at a time. A table that cannot be read is named as not read, and the
 * rest are still searched.
 */
async function scanTables(
  id: number,
  schema: string,
  tables: { name: string; columns: string[] }[],
  needle: string,
  signal: AbortSignal,
  onProgress: (done: number) => void,
): Promise<SearchResult> {
  const found = new Map<string, SearchMatch[]>()
  const more: Record<string, boolean> = {}
  const keys: Record<string, string[]> = {}
  const skipped: string[] = []
  let next = 0
  let done = 0
  const take = async (): Promise<void> => {
    for (;;) {
      const table = tables[next++]
      if (!table) return
      try {
        const answers: BrowseAnswer[] = []
        // A table with more columns than one read may name is read in several.
        for (const filters of scanFilters(table.columns, needle)) {
          const answer = await get<BrowseAnswer>(
            `/databases/${id}/browse`,
            {
              schema: schema || undefined,
              table: table.name,
              limit: PER_TABLE,
              match: "any",
              filters: encodeFilters(filters),
            },
            signal,
          )
          answers.push(answer)
          // Its share is already full: the remaining columns can add nothing that is shown.
          if (answer.truncated && (answer.rows?.length ?? 0) >= PER_TABLE) break
        }
        const read = tableMatches(schema, table.name, answers, needle)
        found.set(table.name, read.matches)
        more[tableKey(schema, table.name)] = read.more
        if (read.key.length > 0) keys[tableKey(schema, table.name)] = read.key
      } catch (err) {
        if (signal.aborted) throw err
        skipped.push(table.name)
      }
      onProgress(++done)
    }
  }
  await Promise.all([take(), take(), take(), take()])
  return {
    // In the order the tables are listed, whichever answered first.
    matches: tables.flatMap((table) => found.get(table.name) ?? []),
    tablesScanned: tables.length - skipped.length,
    tablesSkipped: skipped,
    truncated: Object.values(more).some(Boolean),
    more,
    keys,
  }
}

/** The value being looked for, as it was typed. */
function Needle({ children }: { children: string }) {
  return <span className="rounded-sm bg-mark px-1 font-mono">{children}</span>
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

/** The scan is out: what is being read, and the shape of what will come. */
function Searching({ word, progress }: { word: string; progress?: { done: number; of: number } }) {
  return (
    <div role="status" className="min-w-0 space-y-4">
      <div aria-hidden className="h-0.5 overflow-hidden rounded-full bg-meter-track">
        <div className="h-full w-1/3 animate-sweep bg-primary" />
      </div>
      <p className="flex items-center gap-2 text-body">
        <TextShimmer>{`Reading ${progress ? "the" : "every table of"} ${word}…`}</TextShimmer>
        {progress && (
          <span className="numeric text-muted-foreground">
            {progress.done} of {progress.of}
          </span>
        )}
      </p>
      <div aria-hidden className="space-y-2">
        <Skeleton className="h-3 w-40" />
        {[0, 1, 2].map((row) => (
          <Skeleton key={row} className="h-14 w-full rounded-xl" />
        ))}
      </div>
    </div>
  )
}

/**
 * The key of each table that has a match: it is what makes "this row" a
 * condition the table editor can find. A search of ticked tables says it with
 * the rows; a search of a whole schema does not, and it is read once per
 * table. Until one is known — and for a table with none — a row is found by
 * its own values.
 */
function usePrimaryKeys(
  id: number,
  groups: TableHits[],
  known: Record<string, string[]> | undefined,
) {
  const { engine } = useDatabase()
  const [keys, setKeys] = useState<Record<string, string[]>>({})
  const asked = useRef(new Set<string>())
  // An engine whose key repeats (a sorting key) has no key to find a row by.
  const keyed = engine.capabilities.rowIdentity !== "none"
  useEffect(() => {
    if (!keyed) return
    const controller = new AbortController()
    const wanted = groups.filter(
      (group) => !asked.current.has(group.key) && !(known && group.key in known),
    )
    let next = 0
    const take = async (): Promise<void> => {
      const group = wanted[next++]
      if (!group) return
      asked.current.add(group.key)
      try {
        const detail = await get<{ primaryKey?: string[] }>(
          `/databases/${id}/table`,
          { schema: group.schema || undefined, table: group.table },
          controller.signal,
        )
        const key = Array.isArray(detail?.primaryKey) ? detail.primaryKey : []
        setKeys((held) => ({ ...held, [group.key]: key }))
      } catch {
        // No key known: the row is found by its values instead.
        if (controller.signal.aborted) asked.current.delete(group.key)
      }
      return take()
    }
    // A few at a time: a search that found forty tables is not forty reads at once.
    void Promise.all([take(), take(), take(), take()])
    return () => controller.abort()
  }, [id, groups, keyed, known])
  return useMemo(() => (keyed ? { ...keys, ...known } : {}), [keyed, keys, known])
}

function TableGroup({
  group,
  primaryKey,
  everyHref,
  hrefFor,
  qualified,
}: {
  group: TableHits
  primaryKey: string[] | undefined
  everyHref: string
  hrefFor: (hit: Hit, primaryKey: string[] | undefined) => string
  /** More than one schema was read: the table is named with its own. */
  qualified: boolean
}) {
  return (
    <section
      aria-label={`${group.table}, ${plural(group.hits.length, "row")}${group.more ? " shown of more" : ""}`}
      data-slot="search-table"
      className="min-w-0 space-y-2"
    >
      {/* `GroupRule`'s anatomy — a mark, a name, a count, a rule running right
          — drawn here because its name is a small-caps eyebrow, and a table's
          name is somebody's own word: `Mixed Case Table` in capitals is another
          table. So the name is mono, as typed, and the rule ends on the way to
          every match. */}
      <div className="flex min-w-0 items-center gap-2.5">
        <span className="flex shrink-0 items-center">
          <KindGlyph kind="table" />
        </span>
        <h2 className="flex min-w-0 items-baseline font-mono text-xs font-medium">
          {qualified && group.schema && (
            <span className="shrink-0 text-muted-foreground">{group.schema}.</span>
          )}
          <span className="min-w-0 truncate">{group.table}</span>
        </h2>
        <span
          className="numeric shrink-0 text-micro text-muted-foreground"
          title={group.more ? "These are the first; the table holds more" : undefined}
        >
          {countText(group)}
        </span>
        <span aria-hidden className="h-px min-w-0 flex-1 bg-hairline" />
        <Button size="xs" variant="ghost" asChild>
          <Link href={everyHref} aria-label={`Every row of ${group.table} that matches`}>
            Every match
          </Link>
        </Button>
      </div>
      <ChoiceList>
        {group.hits.map((hit, index) => (
          <ChoiceRow
            key={index}
            index={index}
            href={hrefFor(hit, primaryKey)}
            // With no key to name it by, what opens is every row that reads the same.
            verb={
              isKeyed(hit.match, primaryKey)
                ? `Open this row of ${group.table} in Data`
                : `Open the rows of ${group.table} like this one in Data`
            }
            title={<HitCells hit={hit} />}
            description={<OtherCells cells={otherCells(hit, primaryKey)} />}
          />
        ))}
      </ChoiceList>
    </section>
  )
}

/** An instant as a row is recognised by it: to the second, without the machine's fraction. */
function instantText(text: string): string {
  const read =
    /^(\d{4}-\d{2}-\d{2})[T ](\d{2}:\d{2}(?::\d{2})?)(?:\.\d+)?(Z|[-+]\d{2}(?::?\d{2})?)?$/.exec(
      text,
    )
  if (!read) return text
  const zone = read[3] === "Z" ? " UTC" : read[3] ? ` ${read[3]}` : ""
  return `${read[1]} ${read[2]}${zone}`
}

const CELL_HUE: Record<OtherCell["kind"], string> = {
  text: "text-foreground/85",
  number: cn("text-foreground/85", KIND_HUE.number),
  instant: KIND_HUE.datetime,
  flag: KIND_HUE.boolean,
  token: KIND_HUE.uuid,
}

/** A few of the row's other cells, to recognise it by: each a name and its value in the hue of its kind. */
function OtherCells({ cells }: { cells: OtherCell[] }) {
  if (cells.length === 0) return null
  return (
    <span className="font-mono">
      {cells.map((cell, at) => (
        <span key={cell.column}>
          {at > 0 && <span aria-hidden> · </span>}
          {cell.column}{" "}
          <span className={CELL_HUE[cell.kind]}>
            {cell.kind === "instant" ? instantText(cell.text) : cell.text}
          </span>
        </span>
      ))}
    </span>
  )
}

/** The cells of a row that hold the value: the column's name, then the cell with the value marked. */
function HitCells({ hit }: { hit: Hit }) {
  return (
    <span className="font-mono text-xs font-normal">
      {hit.cells.slice(0, 3).map((cell, at) => (
        <span key={cell.column} className={cn(at > 0 && "ml-3")}>
          <span className="text-muted-foreground">{cell.column} </span>
          {cell.parts.map((part, index) =>
            part.hit ? (
              <mark key={index} className="rounded-sm bg-mark px-px text-foreground">
                {part.text}
              </mark>
            ) : (
              <span key={index}>{part.text}</span>
            ),
          )}
        </span>
      ))}
      {hit.cells.length > 3 && (
        <span className="ml-3 text-muted-foreground">and {hit.cells.length - 3} more</span>
      )}
    </span>
  )
}
