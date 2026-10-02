"use client"

import { useMemo, useState } from "react"
import Link from "next/link"
import {
  ChevronDown,
  ChevronRight,
  Layers,
  MoreHorizontal,
  Plus,
  RefreshClockwise,
  SidebarLeftClose,
} from "@/components/icons"
import { bytes } from "@/lib/format"
import { cn } from "@/lib/utils"
import { IconAction, rowReveal } from "@/components/icon-action"
import { SearchInput } from "@/components/page"
import { EmptyNote, EmptyState } from "@/components/state"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { VerbMenu, type Verb } from "@/components/verbs"
import { compactCount, grouped } from "@/components/database/data/view"
import { nameHue } from "@/components/database/home/kinds"
import type { SectionId } from "@/components/database/engine"
import {
  COLLECTION_KINDS,
  CollectionMark,
  collectionKind,
  type CollectionKind,
} from "@/components/database/mongo/kinds"
import type { MongoCollection, MongoDatabase } from "@/components/database/mongo/types"
import type { Catalog, Mongo } from "@/components/database/mongo/use-mongo"
import { ReadError } from "@/components/database/redis/read-error"

/** Past this many databases the picker grows a box to find one in. */
const SEARCH_FROM = 12

/** The groups the rail lists, in order, with the word each run is headed by. */
const GROUPS: { kind: CollectionKind; plural: string }[] = [
  { kind: "collection", plural: "Collections" },
  { kind: "capped", plural: "Capped" },
  { kind: "timeseries", plural: "Time series" },
  { kind: "view", plural: "Views" },
]

/**
 * The rail of the MongoDB workbenches: which database, and what it holds.
 *
 * Collections are listed by kind — ordinary, capped, time series, views —
 * each with its kind's glyph in its kind's hue, and its document count and
 * data size at the right. A row is a link: it opens in a new tab, and
 * choosing one is a step of history, so Back returns to the collection the
 * reader came from. The server's own collections (`system.*`) fold away
 * under the rest.
 *
 * The same rail stands beside Documents, Aggregations and Schema, and a row
 * opens its collection in whichever of the three the reader is on.
 */
export function CollectionRail({
  mongo,
  catalog,
  section,
  verbsFor,
  onNew,
  onHide,
}: {
  mongo: Mongo
  catalog: Catalog
  section: SectionId
  verbsFor: (collection: MongoCollection) => Verb[]
  /** Opens "New collection"; absent where the role, the engine or the connection cannot. */
  onNew?: () => void
  onHide: () => void
}) {
  const { engine, href, database, collection: current } = mongo
  const { collections } = catalog
  const [query, setQuery] = useState("")
  const [systemOpen, setSystemOpen] = useState(false)
  const needle = query.trim().toLowerCase()
  const data = collections.data

  const { groups, system, own } = useMemo(() => {
    const all = data?.collections ?? []
    const match = (entry: MongoCollection) => !needle || entry.name.toLowerCase().includes(needle)
    const byKind = new Map<CollectionKind, MongoCollection[]>()
    for (const entry of all) {
      const kind = collectionKind(entry)
      byKind.set(kind, [...(byKind.get(kind) ?? []), entry])
    }
    return {
      groups: GROUPS.map((group) => ({
        ...group,
        entries: (byKind.get(group.kind) ?? []).filter(match),
      })).filter((group) => group.entries.length > 0),
      system: (byKind.get("system") ?? []).filter(match),
      own: all.filter((entry) => !entry.system),
    }
  }, [data, needle])

  const matched = groups.reduce((sum, group) => sum + group.entries.length, 0) + system.length
  const measured = own.filter((entry) => entry.statsKnown)
  const documents = measured.reduce((sum, entry) => sum + entry.count, 0)
  const size = measured.reduce((sum, entry) => sum + entry.size, 0)
  // A search, or the open collection being one of them, shows the server's own.
  const showSystem = systemOpen || Boolean(needle) || system.some((entry) => entry.name === current)

  return (
    <nav
      data-slot="collection-rail"
      aria-label={`${engine.nouns.objects} of this ${engine.nouns.container}`}
      className="flex min-h-0 min-w-0 flex-1 flex-col"
    >
      <div className="flex h-10 shrink-0 items-center gap-1 border-b border-hairline bg-surface-header pr-1.5 pl-2">
        <DatabasePicker mongo={mongo} catalog={catalog} section={section} />
        {onNew && (
          <IconAction
            label={`New ${engine.nouns.object}`}
            className="size-7 max-sm:size-8"
            onClick={onNew}
          >
            <Plus />
          </IconAction>
        )}
        <IconAction
          label={`Read the ${engine.nouns.objects} again`}
          className="size-7 max-sm:size-8"
          onClick={catalog.refresh}
        >
          <RefreshClockwise />
        </IconAction>
        <IconAction
          label={`Hide the ${engine.nouns.objects}`}
          aria-pressed
          className="size-7 max-sm:size-8"
          onClick={onHide}
        >
          <SidebarLeftClose />
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
        {!database ? (
          <EmptyNote className="px-2 py-6">
            The connection names no {engine.nouns.container}. Choose one above.
          </EmptyNote>
        ) : !data && collections.error ? (
          <ReadError error={collections.error} onRetry={collections.refresh} className="m-1" />
        ) : !data ? (
          <RailSkeleton />
        ) : data.collections.length === 0 ? (
          <EmptyState
            className="mt-2 border-0 px-2 py-8"
            icon={Layers}
            title={`No ${engine.nouns.objects} in ${database}`}
            description={
              onNew
                ? `A ${engine.nouns.container} exists once it holds a ${engine.nouns.object}. Create the first one here.`
                : `Nothing in this ${engine.nouns.container} holds ${engine.nouns.rows} yet.`
            }
            action={
              onNew && (
                <Button size="sm" variant="outline" onClick={onNew}>
                  <Plus />
                  New {engine.nouns.object}
                </Button>
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
          <>
            {groups.map((group) => (
              <section key={group.kind} aria-label={group.plural} className="pt-2">
                <h3 className="eyebrow flex items-center gap-1.5 px-2 pb-1">
                  {group.plural}
                  <span className="numeric">{grouped(group.entries.length)}</span>
                </h3>
                <ul>
                  {group.entries.map((entry) => (
                    <RailRow
                      key={entry.name}
                      entry={entry}
                      current={entry.name === current}
                      href={href(section, { db: database, collection: entry.name })}
                      verbs={verbsFor(entry)}
                    />
                  ))}
                </ul>
              </section>
            ))}
            {system.length > 0 && (
              <section aria-label="The server's own collections" className="pt-2">
                <h3>
                  <button
                    type="button"
                    aria-expanded={showSystem}
                    onClick={() => setSystemOpen(!showSystem)}
                    className="eyebrow flex w-full items-center gap-1 rounded-sm px-1.5 pb-1 focus-ring hover:text-foreground"
                  >
                    {showSystem ? (
                      <ChevronDown className="size-3" />
                    ) : (
                      <ChevronRight className="size-3" />
                    )}
                    The server&rsquo;s own
                    <span className="numeric">{grouped(system.length)}</span>
                  </button>
                </h3>
                {showSystem && (
                  <ul>
                    {system.map((entry) => (
                      <RailRow
                        key={entry.name}
                        entry={entry}
                        current={entry.name === current}
                        href={href(section, { db: database, collection: entry.name })}
                        verbs={verbsFor(entry)}
                      />
                    ))}
                  </ul>
                )}
              </section>
            )}
          </>
        )}
      </div>

      {data && own.length > 0 && (
        <div className="flex h-8 shrink-0 items-center gap-1.5 border-t border-hairline bg-surface-header px-3 text-hint text-muted-foreground">
          <span className="numeric min-w-0 truncate">
            {grouped(documents)} {documents === 1 ? engine.nouns.row : engine.nouns.rows}
            {" · "}
            {bytes(size)}
            {data.statsTruncated ? " · some not measured" : ""}
          </span>
        </div>
      )}
    </nav>
  )
}

function RailRow({
  entry,
  current,
  href,
  verbs,
}: {
  entry: MongoCollection
  current: boolean
  href: string
  verbs: Verb[]
}) {
  const kind = collectionKind(entry)
  return (
    <li
      data-current={current || undefined}
      className={cn(
        "group flex h-7 items-center rounded-md transition-colors max-sm:h-9",
        current ? "bg-accent" : "hover:bg-row-hover",
      )}
    >
      <Link
        href={href}
        aria-current={current ? "page" : undefined}
        title={
          entry.type === "view"
            ? `A view on ${entry.viewOn}`
            : entry.statsKnown
              ? `${grouped(entry.count)} documents · ${bytes(entry.size)} of data`
              : COLLECTION_KINDS[kind].label
        }
        className="flex h-full min-w-0 flex-1 items-center gap-1.5 rounded-md pr-1 pl-2 focus-ring-inset"
      >
        <CollectionMark kind={kind} />
        <span className="min-w-0 flex-1 truncate font-mono text-xs">{entry.name}</span>
        {entry.statsKnown ? (
          <>
            <span className="numeric shrink-0 text-hint text-muted-foreground">
              {compactCount(entry.count)}
            </span>
            <span className="numeric w-14 shrink-0 text-right text-hint text-muted-foreground/70">
              {bytes(entry.size, 0)}
            </span>
          </>
        ) : (
          entry.type === "view" && (
            <span className="shrink-0 text-hint text-muted-foreground/70">view</span>
          )
        )}
      </Link>
      {verbs.length > 0 && (
        <VerbMenu
          verbs={verbs}
          label={`Actions for ${entry.name}`}
          trigger={
            <Button
              size="icon-xs"
              variant="ghost"
              aria-label={`Actions for ${entry.name}`}
              className={cn("mr-0.5 shrink-0 max-sm:size-8", rowReveal())}
            >
              <MoreHorizontal className="size-3.5" />
            </Button>
          }
        />
      )}
    </li>
  )
}

function RailSkeleton() {
  return (
    <div aria-hidden className="space-y-1.5 px-2 pt-3">
      <Skeleton className="h-2.5 w-20" />
      {[72, 56, 64, 48, 60].map((width, index) => (
        <div key={index} className="flex h-7 items-center gap-2">
          <Skeleton className="size-3.5 rounded-sm" />
          <Skeleton className="h-3" style={{ width: `${width}%` }} />
        </div>
      ))}
    </div>
  )
}

/**
 * Which database the rail lists. The server's databases with what each
 * holds; choosing one opens it with no collection named, since the one that
 * was open is not in it.
 */
function DatabasePicker({
  mongo,
  catalog,
  section,
}: {
  mongo: Mongo
  catalog: Catalog
  section: SectionId
}) {
  const { engine, href, database, conn } = mongo
  const [query, setQuery] = useState("")
  const needle = query.trim().toLowerCase()
  const all = catalog.databases.data ?? []
  // The open database is listed even before it holds anything: an empty
  // database does not exist for the server until it has a collection.
  const listed: MongoDatabase[] =
    database && !all.some((entry) => entry.name === database)
      ? [...all, { ...EMPTY_DATABASE, name: database }]
      : all
  const shown = listed.filter((entry) => !needle || entry.name.toLowerCase().includes(needle))
  return (
    <DropdownMenu onOpenChange={(open) => !open && setQuery("")}>
      <DropdownMenuTrigger asChild>
        <Button
          size="sm"
          variant="ghost"
          aria-label={`${engine.nouns.container}: ${database || "none chosen"}`}
          className="h-7 min-w-0 flex-1 justify-start gap-1.5 px-1.5"
        >
          <DatabaseMark name={database} />
          <span className="min-w-0 truncate font-mono text-xs">{database || "Choose one"}</span>
          <ChevronDown className="ml-auto size-3 text-muted-foreground" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-72">
        {listed.length > SEARCH_FROM && (
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
          {catalog.databases.error && !catalog.databases.data && (
            <p className="px-2 py-2 text-hint text-muted-foreground">
              The server&rsquo;s {engine.nouns.containers} could not be listed:{" "}
              {catalog.databases.error.message}
            </p>
          )}
          {shown.map((entry) => (
            <DropdownMenuItem
              key={entry.name}
              asChild
              className={cn(entry.name === database && "bg-accent")}
            >
              <Link href={href(section, { db: entry.name, collection: null })}>
                <DatabaseMark name={entry.name} />
                <span className="min-w-0 flex-1 truncate font-mono text-xs">{entry.name}</span>
                <span className="numeric shrink-0 text-hint text-muted-foreground">
                  {entry.name === conn.database ? "connects here · " : ""}
                  {entry.statsKnown
                    ? `${grouped(entry.collections)} · ${bytes(entry.dataSize, 0)}`
                    : "not measured"}
                </span>
              </Link>
            </DropdownMenuItem>
          ))}
          {shown.length === 0 && !catalog.databases.error && (
            <p className="px-2 py-3 text-center text-hint text-muted-foreground">
              {catalog.databases.data
                ? `No ${engine.nouns.container} is called that`
                : `Reading the ${engine.nouns.containers}…`}
            </p>
          )}
        </div>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

const EMPTY_DATABASE: MongoDatabase = {
  name: "",
  collections: 0,
  views: 0,
  objects: 0,
  avgObjSize: 0,
  dataSize: 0,
  storageSize: 0,
  indexes: 0,
  indexSize: 0,
  sizeOnDisk: 0,
  empty: true,
  statsKnown: true,
}

/** A database's own colour, stable for its name, as a small square before it. */
export function DatabaseMark({ name }: { name: string }) {
  return (
    <span
      aria-hidden
      className="size-2 shrink-0 rounded-sm"
      style={{ background: name ? nameHue(name) : "var(--muted-foreground)" }}
    />
  )
}
