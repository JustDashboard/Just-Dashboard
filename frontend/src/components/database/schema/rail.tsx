"use client"

import { MenuItemText } from "@/components/ui/menu-item-text"

import { useMemo, useState } from "react"
import Link from "next/link"
import {
  ChevronDown,
  ChevronRight,
  MoreHorizontal,
  Plus,
  RefreshClockwise,
} from "@/components/icons"
import type { DbCatalogGroup } from "@/lib/types"
import { cn } from "@/lib/utils"
import { hueFor, LANES } from "@/lib/hue"
import { useViewState } from "@/lib/view-state"
import type { PollState } from "@/hooks/use-poll"
import { IconAction } from "@/components/icon-action"
import { SearchInput } from "@/components/page"
import { EmptyNote, EmptyState } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { VerbMenu, type Verb } from "@/components/verbs"
import { compactCount, grouped } from "@/components/database/data/view"
import { ReadFailed } from "@/components/database/fleet/read-failed"
import { useDatabase } from "@/components/database/shell/database-context"
import {
  objectKey,
  objectParams,
  schemaParams,
  tableParams,
  type Selected,
} from "@/components/database/schema/address"
import { GroupGlyph, groupSpec } from "@/components/database/schema/kinds"
import type { DbCatalog, DbCatalogSchema, SchemaObject } from "@/components/database/schema/types"

/** How many rows of a branch are drawn before the reader asks for more. */
const GROUP_STEP = 300
/** Past this many schemas the picker grows a box to find one in. */
const SEARCH_FROM = 12

const NO_FOLDED: string[] = []
const NO_SCHEMAS: DbCatalogSchema[] = []

/** The kinds whose word says something the branch's heading does not. */
const SORTS_OF_TYPE = new Set(["enum", "domain", "composite", "range"])

/** What stands after an object's name in the tree: the figure or the word that tells it apart. */
function trailing(object: SchemaObject, rows: boolean): { text: string; title?: string } | null {
  if (rows) {
    const count = object.estimatedRows
    return count !== undefined && count >= 0
      ? { text: compactCount(count), title: `About ${grouped(count)} rows` }
      : null
  }
  if (object.kind === "trigger") return object.table ? { text: object.table } : null
  if (object.kind === "function" || object.kind === "procedure") {
    return object.signature !== undefined
      ? { text: `(${object.signature})`, title: `${object.name}(${object.signature})` }
      : null
  }
  return SORTS_OF_TYPE.has(object.kind) ? { text: object.kind } : null
}

/**
 * The rail of the schema browser: which schema, and everything in it by kind.
 *
 * Each branch is a kind of object — tables, views, functions, triggers,
 * sequences, types — under its own glyph in its own hue, with how many it
 * holds. Which branches there are is the engine's (`catalogGroups`), and one
 * the engine has and this schema does not is listed empty rather than left
 * out, so "no triggers" is something the tree says. A row is a link: it opens
 * in a new tab, and choosing one is a step of history.
 *
 * Functions an extension installed are folded behind their own row. A schema
 * with one extension in it is otherwise forty functions nobody wrote.
 */
export function SchemaRail({
  catalog,
  selected,
  creations,
  verbs,
  onRefresh,
  saidBeside = false,
  className,
}: {
  catalog: PollState<DbCatalog>
  selected: Selected
  /** What can be made here, for the head's "New" menu; empty where the role or the engine cannot. */
  creations: Verb[]
  /** What can be done to the schema itself: it is said here too, where the tree is all a phone shows. */
  verbs: Verb[]
  /** Reads the tree again, and with it whatever is open beside it. */
  onRefresh: () => void
  /** The page says beside the rail what is wrong, with the way on: the rail does not say it twice. */
  saidBeside?: boolean
  className?: string
}) {
  const { id, engine, href, selection } = useDatabase()
  const [query, setQuery] = useState("")
  const [folded, setFolded] = useViewState<string[]>(`databases.${id}.schema.folded`, NO_FOLDED)
  const data = catalog.data
  const needle = query.trim().toLowerCase()
  const schema = data?.schema ?? ""
  const known = !data || data.schemas.length === 0 || data.schemas.some((s) => s.name === schema)

  // The engine's own order of branches; anything the server sends beyond it follows.
  const order = useMemo(() => {
    const listed = engine.capabilities.catalogGroups
    const sent = Object.keys(data?.objects ?? {}) as DbCatalogGroup[]
    return [...listed.filter((g) => sent.includes(g)), ...sent.filter((g) => !listed.includes(g))]
  }, [engine, data])

  const groups = useMemo(
    () =>
      order.map((group) => {
        const objects = (data?.objects[group] ?? []) as SchemaObject[]
        const shown = needle
          ? objects.filter((object) => object.name.toLowerCase().includes(needle))
          : objects
        return { group, objects: shown, total: objects.length }
      }),
    [order, data, needle],
  )
  const total = groups.reduce((sum, entry) => sum + entry.total, 0)
  const matched = groups.reduce((sum, entry) => sum + entry.objects.length, 0)

  const selectedKey = !selected
    ? ""
    : selected.type === "table"
      ? `${selected.schema}\u0000${selected.name}`
      : objectKey({ ...selected, signature: selected.signature, table: selected.table })

  return (
    <nav
      data-slot="schema-rail"
      aria-label={`Objects of this ${engine.nouns.container}`}
      className={cn("flex min-h-0 min-w-0 flex-1 flex-col", className)}
    >
      <div className="flex h-10 shrink-0 items-center gap-1 border-b border-hairline bg-surface-header pr-1.5 pl-2">
        {engine.can("schemas") ? (
          // Drawn from the address before the list is read, so the head does
          // not change shape when it lands.
          <SchemaPicker
            schemas={data?.schemas ?? NO_SCHEMAS}
            current={schema || selection.schema}
          />
        ) : (
          <span className="min-w-0 flex-1 truncate px-1 text-body font-medium">Objects</span>
        )}
        {creations.length > 0 && known && (
          <VerbMenu
            verbs={creations}
            label="New"
            trigger={
              <Button
                size="icon-sm"
                variant="ghost"
                aria-label="New"
                className="size-7 max-sm:size-8"
              >
                <Plus className="size-3.5" />
              </Button>
            }
          />
        )}
        <IconAction
          label="Read the schema again"
          className="size-7 max-sm:size-8"
          onClick={onRefresh}
        >
          <RefreshClockwise />
        </IconAction>
        {verbs.length > 0 && known && (
          <VerbMenu
            verbs={verbs}
            trigger={
              <Button
                size="icon-sm"
                variant="ghost"
                aria-label={`More for ${schema || engine.label}`}
                className="size-7 max-sm:size-8"
              >
                <MoreHorizontal className="size-3.5" />
              </Button>
            }
          />
        )}
      </div>
      {data && catalog.error && (
        // The tree is what was read before: the read that was just asked for failed.
        <div
          role="status"
          className="flex shrink-0 items-center gap-2 border-b border-hairline py-1 pr-1.5 pl-3"
        >
          <Status tone="warning" label="Not read again" className="shrink-0" />
          <span
            className="min-w-0 flex-1 truncate text-hint text-muted-foreground"
            title={catalog.error.message}
          >
            last reading
          </span>
          <Button size="xs" variant="ghost" className="shrink-0" onClick={onRefresh}>
            Try again
          </Button>
        </div>
      )}
      <div className="shrink-0 border-b border-hairline p-1.5">
        <SearchInput
          dense
          value={query}
          placeholder="Find an object"
          aria-label="Find an object"
          containerClassName="sm:w-full"
          onChange={(event) => setQuery(event.target.value)}
        />
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto px-1.5 pb-2">
        {!data && catalog.error ? (
          saidBeside ? (
            <EmptyNote className="px-2 py-6">The objects could not be read.</EmptyNote>
          ) : (
            <ReadFailed error={catalog.error} onRetry={catalog.refresh} className="m-1" />
          )
        ) : !data ? (
          <RailSkeleton />
        ) : !known && saidBeside ? (
          <EmptyNote className="px-2 py-6">Nothing is listed.</EmptyNote>
        ) : !known ? (
          <EmptyState
            className="mt-2 border-0 px-2 py-8"
            title={`No ${engine.nouns.container} called ${schema}`}
            description={`This connection has no such ${engine.nouns.container}. It may have been dropped since the link was made.`}
            action={
              <Button size="sm" variant="outline" asChild>
                <Link href={href("schema", schemaParams(data.defaultSchema))}>
                  Open {data.defaultSchema || `the default ${engine.nouns.container}`}
                </Link>
              </Button>
            }
          />
        ) : total > 0 && matched === 0 ? (
          <div className="px-2 py-6 text-center">
            <p className="text-body text-muted-foreground">Nothing here is called that.</p>
            <Button size="xs" variant="ghost" className="mt-2" onClick={() => setQuery("")}>
              Clear the search
            </Button>
          </div>
        ) : (
          groups.map(({ group, objects, total: all }) =>
            // While searching, only the branches that answer are listed.
            needle && objects.length === 0 ? null : (
              <RailGroup
                key={group}
                group={group}
                objects={objects}
                total={all}
                searching={needle !== ""}
                selectedKey={selectedKey}
                folded={!needle && folded.includes(group)}
                onFold={(fold) =>
                  setFolded((held) =>
                    fold
                      ? [...held.filter((g) => g !== group), group]
                      : held.filter((g) => g !== group),
                  )
                }
                error={data.errors?.[group]}
                truncated={data.truncated.includes(group) ? data.limit : undefined}
              />
            ),
          )
        )}
      </div>
    </nav>
  )
}

function RailGroup({
  group,
  objects,
  total,
  searching,
  selectedKey,
  folded,
  onFold,
  error,
  truncated,
}: {
  group: DbCatalogGroup
  objects: SchemaObject[]
  total: number
  searching: boolean
  selectedKey: string
  folded: boolean
  onFold: (folded: boolean) => void
  error?: string
  truncated?: number
}) {
  const { href } = useDatabase()
  const spec = groupSpec(group)
  const [limit, setLimit] = useState(GROUP_STEP)
  const [extensions, setExtensions] = useState(false)

  // What an extension installed is not what somebody wrote here: folded,
  // unless the reader is searching, when a hit is a hit.
  const own = objects.filter((object) => !object.extension)
  const installed = objects.length - own.length
  const listed = searching || extensions ? objects : own
  const shown = listed.slice(0, limit)
  const names = [...new Set(objects.flatMap((o) => (o.extension ? [o.extension] : [])))]

  return (
    <section aria-label={spec.plural} className="pt-1.5">
      <h3>
        <button
          type="button"
          aria-expanded={!folded}
          className="flex h-7 w-full items-center gap-1.5 rounded-md px-1.5 text-left focus-ring-inset transition-colors hover:bg-row-hover max-sm:h-9"
          onClick={() => onFold(!folded)}
        >
          {folded ? (
            <ChevronRight aria-hidden className="size-3 shrink-0 text-muted-foreground" />
          ) : (
            <ChevronDown aria-hidden className="size-3 shrink-0 text-muted-foreground" />
          )}
          <GroupGlyph group={group} />
          <span className="eyebrow min-w-0 flex-1 truncate">{spec.plural}</span>
          <span className="numeric text-hint text-muted-foreground">
            {searching && objects.length !== total
              ? `${grouped(objects.length)} of ${grouped(total)}`
              : grouped(total)}
          </span>
        </button>
      </h3>
      {!folded && (
        <>
          {error && (
            <EmptyNote className="px-2 py-2 text-left text-hint">Not read: {error}</EmptyNote>
          )}
          <ul>
            {shown.map((object) => {
              const params = spec.rows
                ? tableParams(object.schema, object.name)
                : objectParams(object)
              const key = spec.rows ? `${object.schema}\u0000${object.name}` : objectKey(object)
              const current = key === selectedKey
              const tail = trailing(object, spec.rows)
              return (
                <li key={key}>
                  <Link
                    href={href("schema", params)}
                    aria-current={current ? "page" : undefined}
                    title={object.comment || object.detail || undefined}
                    className={cn(
                      "flex h-7 min-w-0 items-center gap-1.5 rounded-md pr-2 pl-6 focus-ring-inset transition-colors max-sm:h-9",
                      current ? "bg-accent" : "hover:bg-row-hover",
                    )}
                  >
                    <span className="min-w-0 flex-1 truncate font-mono text-xs">{object.name}</span>
                    {tail && (
                      <span
                        className={cn(
                          "max-w-[45%] shrink-0 truncate text-hint text-muted-foreground",
                          spec.rows ? "numeric" : "font-mono",
                        )}
                        title={tail.title}
                      >
                        {tail.text}
                      </span>
                    )}
                  </Link>
                </li>
              )
            })}
          </ul>
          {total === 0 && !error && (
            <p className="py-1 pr-2 pl-6 text-hint text-muted-foreground">None</p>
          )}
          {listed.length > limit && (
            <Button
              size="xs"
              variant="ghost"
              className="mt-1 ml-5 text-muted-foreground"
              onClick={() => setLimit((n) => n + GROUP_STEP)}
            >
              Show {Math.min(GROUP_STEP, listed.length - limit)} more of {grouped(listed.length)}
            </Button>
          )}
          {installed > 0 && !searching && (
            <Button
              size="xs"
              variant="ghost"
              aria-expanded={extensions}
              className="mt-0.5 ml-5 max-w-[calc(100%-1.25rem)] text-muted-foreground"
              onClick={() => setExtensions(!extensions)}
            >
              <span className="min-w-0 truncate">
                {extensions ? "Hide" : "Show"} {grouped(installed)} from{" "}
                {names.slice(0, 2).join(", ")}
                {names.length > 2 ? ` and ${names.length - 2} more` : ""}
              </span>
            </Button>
          )}
          {truncated !== undefined && (
            <EmptyNote className="px-2 py-2 text-left text-hint">
              Only the first {grouped(truncated)} are listed.
            </EmptyNote>
          )}
        </>
      )}
    </section>
  )
}

function RailSkeleton() {
  // The coming silhouette: branches with a count, rows of names under them.
  const rows = ["w-28", "w-20", "w-32", "w-24", "w-16"]
  return (
    <div aria-hidden className="space-y-3 px-2 pt-3">
      {[0, 1, 2].map((branch) => (
        <div key={branch} className="space-y-2.5">
          <div className="flex items-center gap-2">
            <Skeleton className="size-3.5 rounded-sm" />
            <Skeleton className="h-2.5 w-16" />
            <Skeleton className="ml-auto h-2.5 w-4" />
          </div>
          {rows.slice(0, branch === 0 ? 5 : 2).map((width, index) => (
            <Skeleton key={index} className={cn("ml-5 h-3", width)} />
          ))}
        </div>
      ))}
    </div>
  )
}

/**
 * Which schema the tree lists, and the way to another. A schema is a place:
 * choosing one is a step of history, and the address afterwards names the
 * schema alone. The engine's own namespaces are listed last, under their own
 * word, so nobody lands in one by scrolling.
 */
function SchemaPicker({ schemas, current }: { schemas: DbCatalogSchema[]; current: string }) {
  const { engine, href } = useDatabase()
  const [query, setQuery] = useState("")
  const needle = query.trim().toLowerCase()
  const match = (schema: DbCatalogSchema) => !needle || schema.name.toLowerCase().includes(needle)
  const own = schemas.filter((schema) => !schema.system && match(schema))
  const system = schemas.filter((schema) => schema.system && match(schema))
  const item = (schema: DbCatalogSchema) => (
    <DropdownMenuItem
      key={schema.name}
      asChild
      className={cn(schema.name === current && "bg-accent")}
    >
      <Link href={href("schema", schemaParams(schema.name))}>
        <SchemaMark name={schema.name} />
        <MenuItemText
          hint={
            schema.tables >= 0 && (
              <span className="numeric text-hint text-muted-foreground">
                {grouped(schema.tables)}
              </span>
            )
          }
        >
          <span className="min-w-0 flex-1 truncate font-mono text-xs">{schema.name}</span>
        </MenuItemText>
      </Link>
    </DropdownMenuItem>
  )
  return (
    <DropdownMenu onOpenChange={(open) => !open && setQuery("")}>
      <DropdownMenuTrigger asChild>
        <Button
          size="sm"
          variant="ghost"
          aria-label={`${engine.nouns.container}: ${current || "default"}`}
          className="h-7 min-w-0 flex-1 justify-start gap-1.5 px-1.5"
        >
          <SchemaMark name={current} />
          <span className="min-w-0 truncate font-mono text-xs">{current || "default"}</span>
          <ChevronDown className="ml-auto size-3 text-muted-foreground" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-64">
        {schemas.length > SEARCH_FROM && (
          <div className="p-1 pb-2">
            <Input
              value={query}
              placeholder={`Find a ${engine.nouns.container}`}
              aria-label={`Find a ${engine.nouns.container}`}
              className="h-7 sm:h-7"
              onChange={(event) => setQuery(event.target.value)}
              // The menu reads typed letters as type-ahead; in the box they are text.
              onKeyDown={(event) => event.key !== "Escape" && event.stopPropagation()}
            />
          </div>
        )}
        <div className="max-h-72 overflow-y-auto">
          {own.map(item)}
          {system.length > 0 && (
            <>
              {own.length > 0 && <DropdownMenuSeparator />}
              <DropdownMenuLabel>The engine&rsquo;s own</DropdownMenuLabel>
              {system.map(item)}
            </>
          )}
          {own.length + system.length === 0 && (
            <p className="px-2 py-3 text-center text-hint text-muted-foreground">
              {schemas.length === 0
                ? `The ${engine.nouns.containers} have not been read`
                : `No ${engine.nouns.container} is called that`}
            </p>
          )}
        </div>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

/** A schema's own colour, stable for its name, as a small square before it. */
export function SchemaMark({ name, className }: { name: string; className?: string }) {
  return (
    <span
      aria-hidden
      className={cn("size-2 shrink-0 rounded-sm", className)}
      style={{ background: hueFor(name.toLowerCase(), LANES) }}
    />
  )
}
