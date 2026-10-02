"use client"

import Link from "next/link"
import { duration, percent, plural } from "@/lib/format"
import { LANES, hueFor } from "@/lib/hue"
import { BarList, type BarListItem } from "@/components/bar-list"
import { EmptyState } from "@/components/state"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { EngineMark } from "@/components/database/kit"
import { bytesId, bytesLabel } from "@/components/database/redis/bytes"
import { TypeComposition, type TypeShare } from "@/components/database/redis/composition"
import { kindLabel } from "@/components/database/redis/kinds"
import { scanProgress, type TreeLevel } from "@/components/database/redis/keys/scan"
import { ReadError } from "@/components/database/redis/read-error"
import type { RedisTreeFolder } from "@/components/database/redis/types"
import type { Paged } from "@/components/database/redis/use-paged"
import type { Redis } from "@/components/database/redis/use-redis"

/** How many namespaces are ranked: the rest are in the tree. */
const RANKED = 12

/**
 * What the database on screen is made of, where a key would be while none is
 * open.
 *
 * Nothing here is read for its own sake. The figures are the server's count
 * for this database; the mix of types and the namespaces are the walk the
 * rail's tree already made. So the pane says what the rail is a list of — how
 * many keys, how many of them leave by themselves, which types and which
 * namespaces hold them — and every part of it is a way in: a type narrows the
 * rail to that type, a namespace lists its keys.
 *
 * The four tiles are this pane's and not the page's: a workbench has no row
 * of readings above it, and these stand in the place of the key the reader
 * has not picked yet.
 */
export function Keyspace({
  redis,
  walk,
  leading,
  onNamespace,
}: {
  redis: Redis
  /** The rail's walk of the top level, narrowed as the rail is. */
  walk: Paged<TreeLevel>
  /** The control that brings the rail back, when it is hidden. */
  leading?: React.ReactNode
  /** List one namespace's keys in the rail. */
  onNamespace: (pattern: string) => void
}) {
  const { db, server, engine, href, param, select, setDb } = redis
  const pattern = param("pattern")
  const type = param("type")
  const narrowed = Boolean(pattern && pattern !== "*") || Boolean(type)
  const level = walk.state
  const space = server.data?.keyspace.find((entry) => entry.db === db)
  const total = level?.total ?? space?.keys
  const own = server.data !== undefined && server.data.db === db

  const head = (
    <div className="flex min-h-12 shrink-0 items-center gap-2 border-b border-hairline px-3 py-2">
      {leading}
      <h2 className="font-mono text-sm leading-6 font-medium">
        {db === undefined ? "Database" : `db${db}`}
      </h2>
      {own && <Tag>connects here</Tag>}
      <span className="min-w-0 flex-1" />
      {engine.can("memoryAnalysis") && (
        <Button size="xs" variant="ghost" asChild>
          <Link href={href("performance", { view: "memory" })}>Analyse its memory</Link>
        </Button>
      )}
    </div>
  )

  const shares: TypeShare[] = Object.entries(level?.types ?? {})
    .sort((a, b) => b[1] - a[1])
    .map(([name, count]) => ({ type: name, weight: count, figure: count.toLocaleString() }))
  const folders = level?.folders ?? []
  const largest = Math.max(...folders.map((folder) => folder.count), 1)
  const bars = folders.slice(0, RANKED).map((folder) => {
    const item = namespaceBar(folder, largest)
    const to = typeof folder.pattern === "string" ? folder.pattern : undefined
    return to ? { ...item, onClick: () => onNamespace(to) } : item
  })
  const share = space && space.keys > 0 ? (space.expires / space.keys) * 100 : undefined
  // The server's other numbered databases, where it has any: how its keys
  // are spread over them, and the way to each.
  const spaces = server.data?.features.databases ? server.data.keyspace : []
  const everywhere = spaces.reduce((sum, entry) => sum + entry.keys, 0)
  const fullest = Math.max(...spaces.map((entry) => entry.keys), 1)
  const databases: BarListItem[] = spaces.map((entry) => ({
    key: String(entry.db),
    label: `db${entry.db}`,
    value: entry.keys.toLocaleString(),
    share: entry.keys / fullest,
    hint:
      entry.db === db
        ? "on screen"
        : entry.expires > 0
          ? `${entry.expires.toLocaleString()} set to expire`
          : "none set to expire",
    ...(entry.db === db ? {} : { title: `Open db${entry.db}`, onClick: () => setDb(entry.db) }),
  }))
  const elsewhere = databases.length > 0 && (
    <section aria-labelledby="redis-keyspace-databases" className="space-y-2">
      <Heading id="redis-keyspace-databases" title="Databases on this server">
        {`${everywhere.toLocaleString()} keys in ${databases.length}`}
      </Heading>
      <BarList items={databases} className="-ml-2" />
    </section>
  )

  // Nothing was read and nothing is known: the list beside this says why,
  // with the way to try again. Said once, there.
  if (walk.error && !level && !server.data) {
    return (
      <div data-slot="redis-keyspace" className="flex min-h-0 min-w-0 flex-1 flex-col">
        {head}
        <EmptyState
          mark={<EngineMark engine={engine} />}
          className="min-h-0 flex-1 border-0"
          title="The server did not answer"
          description="What the database is made of is drawn here once its keys can be read."
        />
      </div>
    )
  }

  // An empty database has no composition to draw. Where the server's keys
  // are instead is the one useful thing to say beside "there are none".
  if (total === 0) {
    return (
      <div data-slot="redis-keyspace" className="flex min-h-0 min-w-0 flex-1 flex-col">
        {head}
        {elsewhere ? (
          <div className="min-h-0 flex-1 overflow-auto">
            <div className="animate-rise space-y-6 px-5 py-5">
              <p className="max-w-prose text-body text-muted-foreground">
                db{db ?? ""} holds no keys. The server&apos;s keys are in the databases below; each
                opens here.
              </p>
              <div className="max-w-2xl">{elsewhere}</div>
            </div>
          </div>
        ) : (
          <EmptyState
            mark={<EngineMark engine={engine} />}
            className="min-h-0 flex-1 border-0"
            title={`db${db ?? ""} is empty`}
            description="What it is made of — by type and by namespace — is drawn here once it holds a key."
          />
        )}
      </div>
    )
  }

  return (
    <div data-slot="redis-keyspace" className="flex min-h-0 min-w-0 flex-1 flex-col">
      {head}
      <div className="@container min-h-0 flex-1 overflow-auto">
        <div className="animate-rise space-y-7 px-5 pt-1 pb-6">
          {/* Bled by the tiles' own inset, so a tile's figure starts on the
              line every heading under it starts on. */}
          <StatGrid columns={4} dense className="-mx-5 border-b border-hairline">
            <StatTile
              label="Keys"
              value={total === undefined ? "—" : total.toLocaleString()}
              hint={
                narrowed && level
                  ? `${level.count.toLocaleString()} match the filter`
                  : spaces.length > 1
                    ? `of ${everywhere.toLocaleString()} on the server`
                    : undefined
              }
            />
            <StatTile
              label="Set to expire"
              value={space ? space.expires.toLocaleString() : "—"}
              meter={share}
              hint={
                share === undefined
                  ? "The server has not said"
                  : space?.expires === 0
                    ? "every key is kept for good"
                    : `${percent(share)} of the keys`
              }
            />
            <StatTile
              label="Average time left"
              value={space && space.expires > 0 ? duration(space.avgTtlMs / 1000) : "—"}
              hint={space && space.expires > 0 ? "over the keys that expire" : "no key expires"}
            />
            <StatTile
              label="Namespaces"
              value={level ? (folders.length + level.foldersOmitted).toLocaleString() : "—"}
              hint={
                !level
                  ? undefined
                  : level.keyCount > 0
                    ? `${plural(level.keyCount, "key")} outside one`
                    : narrowed
                      ? "that hold a match"
                      : "every key is in one"
              }
            />
          </StatGrid>

          {walk.error && !level ? (
            <ReadError error={walk.error} onRetry={walk.reload} />
          ) : !level ? (
            <div className="space-y-7" aria-hidden>
              <div className="space-y-3">
                <Skeleton className="h-3.5 w-20" />
                <Skeleton className="h-2 w-full" />
                <Skeleton className="h-3 w-2/3" />
              </div>
              <div className="space-y-3">
                <Skeleton className="h-3.5 w-36" />
                {Array.from({ length: 6 }, (_, i) => (
                  <Skeleton key={i} className="h-5" style={{ width: `${92 - i * 11}%` }} />
                ))}
              </div>
            </div>
          ) : (
            <>
              <section aria-labelledby="redis-keyspace-types" className="space-y-3">
                <Heading id="redis-keyspace-types" title="By type">
                  {scanProgress({
                    found: level.count,
                    scanned: level.scanned,
                    total: level.total,
                    complete: walk.done,
                    filtered: narrowed,
                  })}
                  {!walk.done && (
                    <Button
                      size="xs"
                      variant="outline"
                      pending={walk.loadingMore}
                      onClick={walk.more}
                    >
                      Scan more
                    </Button>
                  )}
                </Heading>
                {shares.length === 0 ? (
                  <p className="text-hint text-muted-foreground">
                    No key matches {pattern || "*"}
                    {type ? ` as ${kindLabel(type).toLowerCase()}` : ""}.
                  </p>
                ) : (
                  <TypeComposition
                    shares={shares}
                    picked={type || undefined}
                    onPick={
                      engine.can("keyTypeFilter")
                        ? (next) => select({ type: next === type ? null : next })
                        : undefined
                    }
                  />
                )}
              </section>

              <div className="grid items-start gap-x-10 gap-y-7 @3xl:grid-cols-2 [&>*]:min-w-0">
                {bars.length > 0 && (
                  <section aria-labelledby="redis-keyspace-namespaces" className="space-y-2">
                    <Heading id="redis-keyspace-namespaces" title="Largest namespaces">
                      {folders.length + level.foldersOmitted > bars.length &&
                        `${(folders.length + level.foldersOmitted - bars.length).toLocaleString()} smaller ones are in the tree`}
                    </Heading>
                    <BarList items={bars} className="-ml-2" />
                  </section>
                )}
                {databases.length > 1 && elsewhere}
              </div>
            </>
          )}
        </div>
      </div>
    </div>
  )
}

/** A block's name on the pane, with what qualifies it at the far end of the line. */
function Heading({
  id,
  title,
  children,
}: {
  id: string
  title: string
  children?: React.ReactNode
}) {
  return (
    <div className="flex min-h-7 min-w-0 flex-wrap items-center gap-x-3 gap-y-1 border-b border-hairline pb-2">
      <h3 id={id} className="text-body font-medium">
        {title}
      </h3>
      <span className="numeric ml-auto flex min-w-0 items-center gap-2 text-hint text-muted-foreground">
        {children}
      </span>
    </div>
  )
}

/** A namespace as a ranked bar: its name in its own hue, its keys, and what types they are. */
function namespaceBar(folder: RedisTreeFolder, largest: number): BarListItem {
  const name = bytesLabel(folder.name)
  const mix = Object.entries(folder.types)
    .sort((a, b) => b[1] - a[1])
    .slice(0, 3)
  return {
    key: bytesId(folder.prefix),
    label: name || "(empty)",
    mark: (
      <span
        className="size-1.5 rounded-full"
        style={{ backgroundColor: hueFor(name.toLowerCase(), LANES) }}
      />
    ),
    value: folder.count.toLocaleString(),
    share: folder.count / largest,
    // Of one type, the type is the whole reading; of several, how many of each.
    hint:
      mix.length > 1
        ? mix
            .map(([type, count]) => `${kindLabel(type).toLowerCase()} ${count.toLocaleString()}`)
            .join(" · ")
        : mix.length === 1
          ? kindLabel(mix[0][0]).toLowerCase()
          : undefined,
    title: `List the keys of ${name}`,
  }
}
