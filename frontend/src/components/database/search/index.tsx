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
import { nameHue } from "@/components/database/home/kinds"
import { EngineMark, SectionFrame } from "@/components/database/kit"
import { ReadError } from "@/components/database/redis/read-error"
import { useDatabase } from "@/components/database/shell/database-context"
import {
  PER_TABLE,
  groupHits,
  otherCells,
  readResult,
  rowFilters,
  tableFilters,
  type Hit,
  type SearchResult,
  type TableHits,
} from "@/components/database/search/search"

/** What the last search came back with, kept with the question it answers. */
type Held = { q: string; scope: string; result: SearchResult; tookMs: number }

type Schema = { name: string; system?: boolean; tables: number }
type Head = { defaultSchema: string; schemas: Schema[] }

/** How many schemas are chips; the rest sit behind one menu. */
const CHIPS = 8
/** Every schema the connection can name, rather than one. */
const EVERYWHERE = "all"

/**
 * Find a value anywhere: one field, and every table read for it.
 *
 * "This order id appears somewhere — which table?" is a dozen statements
 * typed by hand, one per table somebody remembers, and the answer is in the
 * one they forgot. The server reads them all in one bounded request; what
 * comes back is drawn by table, each row with the cells that hold the value
 * marked, and each row is a way into the table editor filtered to itself.
 *
 * It is asked on purpose, never as the reader types: a scan of every table is
 * the most expensive read this section makes. The question is in the address
 * (`q`, and which schema), so a link reproduces it; the answer is kept in
 * memory for the tab, so coming back from a row costs no second scan — and
 * never reaches Web Storage, because it is rows of somebody's data.
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
  const asking = `${q}\u0000${scope}\u0000${again}`
  // What was found for this very question a moment ago is not asked for again:
  // coming back from a row costs no second scan.
  const remembered = again === 0 && kept !== null && kept.q === q && kept.scope === scope
  // One search at a time, and the one in flight is dropped when another is
  // asked for: two scans that finished out of order used to show the answer
  // to the question before last.
  const search = usePoll(
    async (signal) => {
      const started = performance.now()
      const answer = await get<unknown>(
        `/databases/${id}/search`,
        { q, schema: scope || undefined },
        signal,
      )
      const found: Held = {
        q,
        scope,
        result: readResult(answer),
        tookMs: performance.now() - started,
      }
      setKept(found)
      return found
    },
    0,
    [id, q, scope, again],
    { enabled: q.trim() !== "" && headSettled && !remembered && stopped !== asking },
  )
  const busy = search.loading
  const failed = search.error
  const held = search.data ?? (kept !== null && kept.q === q && kept.scope === scope ? kept : null)
  const answered = held !== null

  const submit = () => {
    const typed = draft.trim()
    // While a scan is out, Enter starts no second one: Stop is the way to end it.
    if (!typed || busy) return
    if (typed === q) setAgain((n) => n + 1)
    // A new question is a step of history: Back returns to the one before it.
    else goto("search", { q: typed, scope: everywhere ? EVERYWHERE : null })
  }
  const stop = () => setStopped(asking)

  const groups = useMemo(
    () => (answered && held ? groupHits(held.result, held.q) : []),
    [answered, held],
  )
  const [only, setOnly] = useState<string | null>(null)
  const shown = only && groups.some((group) => group.key === only) ? only : null
  const visible = shown ? groups.filter((group) => group.key === shown) : groups
  const keys = usePrimaryKeys(id, groups)

  const scopeWord = scope || (scoped ? `every ${engine.nouns.container}` : "this database")
  const chips = schemas.slice(0, CHIPS)
  const rest = schemas.slice(CHIPS)
  const pick = (name: string) => select({ schema: name, scope: null })

  return (
    <SectionFrame section="search">
      <form
        role="search"
        className="flex min-w-0 flex-wrap items-center gap-2"
        onSubmit={(event) => {
          event.preventDefault()
          submit()
        }}
      >
        <SearchInput
          value={draft}
          placeholder="An id, an email, an order number…"
          aria-label="The value to find"
          containerClassName="sm:w-[28rem] sm:max-w-full"
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
                <FilterChip selected={!everywhere && rest.some((schema) => schema.name === own)}>
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
                    <span className="min-w-0 flex-1 truncate font-mono text-xs">{schema.name}</span>
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
          <FilterChip selected={everywhere} onClick={() => select({ scope: EVERYWHERE })}>
            Every {engine.nouns.container}
          </FilterChip>
        </ChipStrip>
      )}

      {busy ? (
        <Searching word={scopeWord} />
      ) : failed ? (
        <ReadError error={failed} onRetry={() => setAgain((n) => n + 1)} />
      ) : !q.trim() || !answered || !held ? (
        <EmptyState
          mark={<EngineMark engine={engine} />}
          title="Find a value anywhere"
          description={`Every column of every ${engine.nouns.object} in ${scopeWord} is compared as text, so an id and a date are found as readily as a name. Views are left out, and the read is bounded: at most 60 ${engine.nouns.objects}, and 5 rows from each.`}
        />
      ) : (
        <div key={`${held.q}\u0000${held.scope}`} className="min-w-0 animate-rise space-y-6">
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
                    <ChipCount>
                      {group.hits.length >= PER_TABLE ? `${PER_TABLE}+` : group.hits.length}
                    </ChipCount>
                  </FilterChip>
                ))}
              </ChipStrip>
            )}
          </div>

          {held.result.truncated && (
            <Notice tone="warning" title="There may be more than this">
              The search stops at 60 {engine.nouns.objects}, 5 rows from each and 200 rows in all.
              Open a {engine.nouns.object} below to see every row of it that matches, or search a
              narrower value.
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
                  : scoped && !everywhere
                    ? `It is in none of the ${engine.nouns.objects} of ${scopeWord}. Another ${engine.nouns.container} may hold it.`
                    : "It is compared as text, any case, anywhere in a cell: a shorter piece of the value may find it."
              }
              action={
                scoped &&
                !everywhere && (
                  <Button size="sm" variant="outline" onClick={() => select({ scope: EVERYWHERE })}>
                    Search every {engine.nouns.container}
                  </Button>
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
function Searching({ word }: { word: string }) {
  return (
    <div role="status" className="min-w-0 space-y-4">
      <div aria-hidden className="h-0.5 overflow-hidden rounded-full bg-meter-track">
        <div className="h-full w-1/3 animate-sweep bg-primary" />
      </div>
      <p className="text-body">
        <TextShimmer>{`Reading every table of ${word}…`}</TextShimmer>
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
 * The key of each table that has a match, read once per table: it is what
 * makes "this row" a condition the table editor can find. Until one is known
 * — and for a table with none — a row is found by its own values.
 */
function usePrimaryKeys(id: number, groups: TableHits[]) {
  const { engine } = useDatabase()
  const [keys, setKeys] = useState<Record<string, string[]>>({})
  const asked = useRef(new Set<string>())
  // An engine whose key repeats (a sorting key) has no key to find a row by.
  const keyed = engine.capabilities.rowIdentity !== "none"
  useEffect(() => {
    if (!keyed) return
    const controller = new AbortController()
    const wanted = groups.filter((group) => !asked.current.has(group.key))
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
  }, [id, groups, keyed])
  return keys
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
      aria-label={`${group.table}, ${plural(group.hits.length, "row")}`}
      data-slot="search-table"
      className="min-w-0 space-y-2"
    >
      <div className="flex min-w-0 items-center gap-2.5">
        <KindGlyph kind="table" />
        <h2 className="flex min-w-0 items-baseline font-mono text-body font-medium">
          {qualified && group.schema && (
            <span className="shrink-0 text-muted-foreground">{group.schema}.</span>
          )}
          <span className="min-w-0 truncate">{group.table}</span>
        </h2>
        <span className="numeric shrink-0 text-micro text-muted-foreground">
          {group.hits.length}
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
            verb={`Open this row of ${group.table} in Data`}
            title={<HitCells hit={hit} />}
            description={<OtherCells hit={hit} primaryKey={primaryKey} />}
          />
        ))}
      </ChoiceList>
    </section>
  )
}

/** A few of the row's other cells, its key first, to recognise it by. */
function OtherCells({ hit, primaryKey }: { hit: Hit; primaryKey: string[] | undefined }) {
  const cells = otherCells(hit, primaryKey)
  if (cells.length === 0) return null
  return (
    <span className="font-mono">
      {cells.map((cell, at) => (
        <span key={cell.column}>
          {at > 0 && " · "}
          {cell.column} <span className="text-foreground/80">{cell.text}</span>
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
