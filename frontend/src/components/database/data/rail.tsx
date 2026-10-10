"use client"

import { MenuItemText } from "@/components/ui/menu-item-text"

import { useMemo, useState } from "react"
import Link from "next/link"
import {
  ChevronDown,
  CloudUpload,
  MoreHorizontal,
  Plus,
  RefreshClockwise,
  Table,
} from "@/components/icons"
import { cn } from "@/lib/utils"
import { hueFor, LANES } from "@/lib/hue"
import { useAuth } from "@/hooks/use-auth"
import type { PollState } from "@/hooks/use-poll"
import { IconAction, rowReveal } from "@/components/icon-action"
import { SearchInput } from "@/components/page"
import { EmptyNote, EmptyState, ErrorState } from "@/components/state"
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
import { useDatabase } from "@/components/database/shell/database-context"
import {
  KindGlyph,
  ROW_GROUPS,
  ROW_OBJECT_KINDS,
  type RowObjectKind,
} from "@/components/database/data/kinds"
import { ACTIONS_OF, actionsOf } from "@/components/database/data/table-verbs"
import type { DbCatalog, DbCatalogObject, DbCatalogSchema } from "@/components/database/data/types"
import { compactCount, grouped } from "@/components/database/data/view"

/** How many rows of a group are drawn before the reader asks for more. */
const GROUP_STEP = 300
/** Past this many schemas the picker grows a box to find one in. */
const SEARCH_FROM = 12

/** How the rail names a table to itself: the schema and the name, never the name alone. */
export function tableKey(schema: string, table: string) {
  return `${schema}\u0000${table}`
}

/**
 * The rail of the table editor: which schema, and what in it holds rows.
 *
 * Tables, views and materialized views are listed by kind, each with its
 * kind's glyph in its kind's hue and the engine's own estimate of its rows at
 * the right. A row is a link — it opens in a new tab, and choosing one is a
 * step of history, so Back returns to the table the reader came from.
 *
 * It lists what the catalogue says, in the schema the address names; an
 * address naming a schema that is not there says so and offers the one that
 * is, rather than drawing an empty list with nothing to press.
 */
export function TableRail({
  catalog,
  current,
  staged,
  verbsFor,
  onImportNew,
  saidBeside = false,
  className,
}: {
  catalog: PollState<DbCatalog>
  /** The table open beside the rail. */
  current: { schema: string; table: string } | null
  /** Tables holding unapplied changes, by `tableKey`, with how many. */
  staged: Readonly<Record<string, number>>
  verbsFor: (object: DbCatalogObject, kind: RowObjectKind) => Verb[]
  /** Opens "import a file as a new table"; absent where the role or the engine cannot. */
  onImportNew?: () => void
  /**
   * The page says beside the rail what is wrong — the list could not be read,
   * the schema is not there — with the way on: the rail does not say it twice.
   */
  saidBeside?: boolean
  className?: string
}) {
  const { engine, href, readOnly } = useDatabase()
  const { can } = useAuth()
  const [query, setQuery] = useState("")
  const data = catalog.data
  const needle = query.trim().toLowerCase()

  const groups = useMemo(
    () =>
      ROW_GROUPS.flatMap(({ group, kind }) => {
        const objects = data?.objects[group]
        if (!objects) return []
        const shown = needle
          ? objects.filter((object) => object.name.toLowerCase().includes(needle))
          : objects
        return [{ group, kind, objects: shown, total: objects.length }]
      }),
    [data, needle],
  )
  const total = groups.reduce((sum, entry) => sum + entry.total, 0)
  const matched = groups.reduce((sum, entry) => sum + entry.objects.length, 0)

  const schema = data?.schema ?? ""
  const known = !data || data.schemas.length === 0 || data.schemas.some((s) => s.name === schema)
  const canCreate =
    can("service.control") && !readOnly && engine.capabilities.ddlOperations.includes("createTable")
  const newTable = href("schema", { schema: schema || null, table: null, new: "table" })

  return (
    <nav
      data-slot="table-rail"
      aria-label={`${engine.nouns.objects} of this ${engine.nouns.container}`}
      className={cn("flex min-h-0 min-w-0 flex-1 flex-col", className)}
    >
      <div className="flex h-10 shrink-0 items-center gap-1 border-b border-hairline bg-surface-header pr-1.5 pl-2">
        {engine.can("schemas") && data ? (
          <SchemaPicker schemas={data.schemas} current={schema} />
        ) : (
          <span className="min-w-0 flex-1 truncate px-1 text-body font-medium capitalize">
            {engine.nouns.objects}
          </span>
        )}
        {canCreate && known && (
          <IconAction label={`New ${engine.nouns.object}`} className="size-7 max-sm:size-8" asChild>
            <Link href={newTable}>
              <Plus />
            </Link>
          </IconAction>
        )}
        {onImportNew && known && (
          <IconAction
            label={`Import a file as a new ${engine.nouns.object}`}
            className="size-7 max-sm:size-8"
            onClick={onImportNew}
          >
            <CloudUpload />
          </IconAction>
        )}
        <IconAction
          label={`Read the ${engine.nouns.objects} again`}
          className="size-7 max-sm:size-8"
          onClick={catalog.refresh}
        >
          <RefreshClockwise />
        </IconAction>
      </div>
      <div className="shrink-0 border-b border-hairline p-1.5">
        <SearchInput
          dense
          value={query}
          placeholder={`Find a ${engine.nouns.object}`}
          aria-label={`Find a ${engine.nouns.object}`}
          containerClassName="sm:w-full"
          onChange={(event) => setQuery(event.target.value)}
        />
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto px-1.5 pb-2">
        {!data && catalog.error ? (
          saidBeside ? (
            <EmptyNote className="px-2 py-6">
              The {engine.nouns.objects} could not be read.
            </EmptyNote>
          ) : (
            <ErrorState error={catalog.error} onRetry={catalog.refresh} className="m-1" />
          )
        ) : !data ? (
          <RailSkeleton />
        ) : !known ? (
          saidBeside ? (
            <EmptyNote className="px-2 py-6">Nothing is listed.</EmptyNote>
          ) : (
            <EmptyState
              className="mt-2 border-0 px-2 py-8"
              icon={Table}
              title={`No ${engine.nouns.container} called ${schema}`}
              description={`This connection has no such ${engine.nouns.container}. It may have been dropped since the link was made.`}
              action={
                <Button size="sm" variant="outline" asChild>
                  <Link href={href("data", { schema: data.defaultSchema || null, table: null })}>
                    Open {data.defaultSchema || `the default ${engine.nouns.container}`}
                  </Link>
                </Button>
              }
            />
          )
        ) : total === 0 ? (
          <EmptyState
            className="mt-2 border-0 px-2 py-8"
            icon={Table}
            title={`No ${engine.nouns.objects} in ${schema || `this ${engine.nouns.container}`}`}
            description={
              canCreate
                ? `Create one on the Schema page, or pick another ${engine.nouns.container} above.`
                : `Nothing in this ${engine.nouns.container} holds rows yet.`
            }
            action={
              canCreate && (
                <div className="flex flex-wrap justify-center gap-2">
                  <Button size="sm" variant="outline" asChild>
                    <Link href={newTable}>
                      <Plus />
                      New {engine.nouns.object}
                    </Link>
                  </Button>
                  {onImportNew && (
                    <Button size="sm" variant="outline" onClick={onImportNew}>
                      <CloudUpload />
                      Import a file
                    </Button>
                  )}
                </div>
              )
            }
          />
        ) : matched === 0 ? (
          <div className="px-2 py-6 text-center">
            <p className="text-body text-muted-foreground">Nothing here is called that.</p>
            <Button size="xs" variant="ghost" className="mt-2" onClick={() => setQuery("")}>
              Clear the search
            </Button>
          </div>
        ) : (
          groups.map(({ group, kind, objects, total: all }) =>
            // A kind the engine has and this schema does not is not listed while searching.
            objects.length === 0 && (needle || all === 0) ? null : (
              <RailGroup
                key={group}
                kind={kind}
                objects={objects}
                current={current}
                staged={staged}
                verbsFor={verbsFor}
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
  kind,
  objects,
  current,
  staged,
  verbsFor,
  error,
  truncated,
}: {
  kind: RowObjectKind
  objects: DbCatalogObject[]
  current: { schema: string; table: string } | null
  staged: Readonly<Record<string, number>>
  verbsFor: (object: DbCatalogObject, kind: RowObjectKind) => Verb[]
  error?: string
  truncated?: number
}) {
  const { href } = useDatabase()
  const [limit, setLimit] = useState(GROUP_STEP)
  const spec = ROW_OBJECT_KINDS[kind]
  const shown = objects.slice(0, limit)
  return (
    <section aria-label={spec.plural} className="pt-2">
      <h3 className="eyebrow flex items-center gap-1.5 px-2 pb-1">
        {spec.plural}
        <span className="numeric">{grouped(objects.length)}</span>
      </h3>
      {error && <EmptyNote className="px-2 py-2 text-left text-hint">Not read: {error}</EmptyNote>}
      <ul>
        {shown.map((object) => {
          const selected =
            current !== null && current.table === object.name && current.schema === object.schema
          const pending = staged[tableKey(object.schema, object.name)] ?? 0
          const rows = object.estimatedRows
          return (
            <li
              key={`${object.schema}.${object.name}`}
              data-current={selected || undefined}
              className={cn(
                "group flex h-7 items-center rounded-md transition-colors max-sm:h-9",
                selected ? "bg-accent" : "hover:bg-row-hover",
              )}
            >
              <Link
                href={href("data", { schema: object.schema, table: object.name })}
                aria-current={selected ? "page" : undefined}
                title={object.comment || object.detail || undefined}
                className="flex h-full min-w-0 flex-1 items-center gap-1.5 rounded-md pr-1 pl-2 focus-ring-inset"
              >
                <KindGlyph kind={kind} />
                <span className="min-w-0 flex-1 truncate font-mono text-xs">{object.name}</span>
                {pending > 0 && (
                  <span className="shrink-0 font-mono text-hint text-(--git-modified)">
                    ~{pending}
                    <span className="sr-only"> unapplied changes</span>
                  </span>
                )}
                {rows !== undefined && rows >= 0 && (
                  <span
                    className="numeric shrink-0 text-hint text-muted-foreground"
                    title={`About ${grouped(rows)} rows`}
                  >
                    {compactCount(rows)}
                  </span>
                )}
              </Link>
              <VerbMenu
                verbs={verbsFor(object, kind)}
                label={`Actions for ${object.name}`}
                trigger={
                  <Button
                    size="icon-xs"
                    variant="ghost"
                    aria-label={`Actions for ${object.name}`}
                    {...{ [ACTIONS_OF]: actionsOf(object) }}
                    className={cn("mr-0.5 shrink-0 max-sm:size-8", rowReveal())}
                  >
                    <MoreHorizontal className="size-3.5" />
                  </Button>
                }
              />
            </li>
          )
        })}
      </ul>
      {objects.length > limit && (
        <Button
          size="xs"
          variant="ghost"
          className="mt-1 ml-1 text-muted-foreground"
          onClick={() => setLimit((n) => n + GROUP_STEP)}
        >
          Show {Math.min(GROUP_STEP, objects.length - limit)} more of {grouped(objects.length)}
        </Button>
      )}
      {truncated !== undefined && (
        <EmptyNote className="px-2 py-2 text-left text-hint">
          Only the first {grouped(truncated)} are listed.
        </EmptyNote>
      )}
    </section>
  )
}

function RailSkeleton() {
  // The coming silhouette: a heading, then rows of a glyph, a name and a figure.
  const widths = ["w-28", "w-20", "w-32", "w-24", "w-16", "w-28", "w-24", "w-20"]
  return (
    <div aria-hidden className="space-y-2.5 px-2 pt-3">
      <Skeleton className="h-2.5 w-12" />
      {widths.map((width, index) => (
        <div key={index} className="flex items-center gap-2">
          <Skeleton className="size-3.5 rounded-sm" />
          <Skeleton className={cn("h-3", width)} />
          <Skeleton className="ml-auto h-2.5 w-6" />
        </div>
      ))}
    </div>
  )
}

/**
 * Which schema the rail lists, and the way to another.
 *
 * A schema is a place, so choosing one is a step of history and the address
 * afterwards names the schema alone: no table is open in it yet. The engine's
 * own namespaces are listed last, under their own word — they are read here
 * like any other, and nobody should land in `pg_catalog` by scrolling.
 */
function SchemaPicker({ schemas, current }: { schemas: DbCatalogSchema[]; current: string }) {
  const { engine, href } = useDatabase()
  const [query, setQuery] = useState("")
  const needle = query.trim().toLowerCase()
  const match = (schema: DbCatalogSchema) => !needle || schema.name.toLowerCase().includes(needle)
  const own = schemas.filter((schema) => !schema.system && match(schema))
  const system = schemas.filter((schema) => schema.system && match(schema))
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
          {own.map((schema) => (
            <SchemaItem key={schema.name} schema={schema} current={current} href={href} />
          ))}
          {system.length > 0 && (
            <>
              {own.length > 0 && <DropdownMenuSeparator />}
              <DropdownMenuLabel>The engine&rsquo;s own</DropdownMenuLabel>
              {system.map((schema) => (
                <SchemaItem key={schema.name} schema={schema} current={current} href={href} />
              ))}
            </>
          )}
          {own.length + system.length === 0 && (
            <p className="px-2 py-3 text-center text-hint text-muted-foreground">
              No {engine.nouns.container} is called that
            </p>
          )}
        </div>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function SchemaItem({
  schema,
  current,
  href,
}: {
  schema: DbCatalogSchema
  current: string
  href: ReturnType<typeof useDatabase>["href"]
}) {
  return (
    <DropdownMenuItem asChild className={cn(schema.name === current && "bg-accent")}>
      <Link href={href("data", { schema: schema.name, table: null })}>
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
}

/** A schema's own colour, stable for its name, as a small square before it. */
function SchemaMark({ name }: { name: string }) {
  return (
    <span
      aria-hidden
      className="size-2 shrink-0 rounded-sm"
      style={{ background: hueFor(name.toLowerCase(), LANES) }}
    />
  )
}
