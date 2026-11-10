"use client"

import { useCallback, useMemo, useRef, useState } from "react"
import Link from "next/link"
import { useSearchParams } from "next/navigation"
import { ReactFlowProvider } from "@xyflow/react"
import { engineOf } from "@/components/database/engine"
import { lastTopology, readTopology } from "@/components/database/fleet/read"
import { ReadFailed } from "@/components/database/fleet/read-failed"
import { useDatabases } from "@/components/database/shell/databases-context"
import { DATABASE_ID } from "@/components/database/shell/routes"
import { Plus } from "@/components/icons"
import {
  MapSkeleton,
  lastServerMap,
  readServerMap,
  stillFilling,
  unreadKinds,
  useFullscreen,
} from "@/components/map"
import type { MapBoard } from "@/components/map/board"
import { MapCanvas, type Unread } from "@/components/map/canvas"
import { useMapMemory } from "@/components/map/memory"
import { Page, PageContext } from "@/components/page"
import { Pane } from "@/components/panel"
import { ProductLogos } from "@/components/product-logo"
import { Button } from "@/components/ui/button"
import { usePoll } from "@/hooks/use-poll"
import {
  DATABASE_KINDS,
  databaseCensus,
  databaseCensusLine,
  detectDatabaseSystems,
} from "@/lib/database-systems"
import { plural } from "@/lib/format"
import type { System } from "@/lib/server-systems"
import { cn } from "@/lib/utils"

/** How often each reading is asked again; the server map sooner while it is still filling in. */
const TOPOLOGY_EVERY = 30_000
const MAP_EVERY = 20_000
const FILLING = 3_000

/**
 * The databases map's own words on the shared canvas (`components/map`): its
 * rail grouped by engine, a database's diagram summed up by what it feeds,
 * and the inspector's account of them all.
 */
const DATABASES_BOARD: MapBoard = {
  label: "Map of the databases and what they feed",
  rail: {
    label: "Databases",
    group: (systems) => {
      const byEngine = new Map<string, { label: string; systems: System[] }>()
      for (const system of systems) {
        const engine = engineOf(system.anchor.product ?? "")
        const held = byEngine.get(engine.label) ?? { label: engine.label, systems: [] }
        held.systems.push(system)
        byEngine.set(engine.label, held)
      }
      return [...byEngine.values()].sort((a, b) => a.label.localeCompare(b.label))
    },
    empty: "No database is connected yet.",
  },
  census: databaseCensusLine,
  holds: (system) => {
    const c = databaseCensus(system)
    return [
      { label: "Read by", value: c.readers.length > 0 ? String(c.readers.length) : "" },
      { label: "Sessions open", value: c.sessions > 0 ? String(c.sessions) : "" },
      { label: "Runs in", value: c.runs.map((n) => n.item.name).join(", ") },
      { label: "Volumes", value: c.volumes > 0 ? String(c.volumes) : "" },
      { label: "Backups", value: c.backups > 0 ? String(c.backups) : "" },
    ].filter((fact) => fact.value !== "")
  },
  whole: (systems) => {
    const readers = new Set<string>()
    let sessions = 0
    let unread = 0
    let backedUp = 0
    for (const system of systems) {
      const c = databaseCensus(system)
      for (const reader of c.readers) readers.add(reader.itemId)
      sessions += c.sessions
      if (c.readers.length === 0) unread += 1
      if (c.backups > 0) backedUp += 1
    }
    return {
      eyebrow: "Databases",
      title: plural(systems.length, "database"),
      line: "Each one drawn with what reads it and where it runs",
      heading: "Across them",
      facts: [
        { label: "Read by", value: readers.size },
        { label: "Sessions open", value: sessions },
        { label: "Nothing reads", value: unread },
        { label: "Backed up by a job", value: backedUp },
      ]
        .filter((fact) => fact.value > 0)
        .map((fact) => ({ label: fact.label, value: fact.value.toLocaleString("en-US") })),
    }
  },
}

/**
 * The databases map: every database as a diagram of its own on the server
 * map's canvas — what reads it on its left, the database, and on its right the
 * container or service it runs in, the volume that keeps its data and the
 * backups that take a copy (`lib/database-systems.ts`).
 *
 * It replaced a fixed two-lane picture with a list of readers beside it, at
 * the operator's word: the same canvas as `/map`, so a database's diagram is
 * moved, arranged and read the way a system's is there — a trackpad slides
 * and pinches it, a mouse drags open ground, a box or a whole diagram is
 * dragged where the reader wants it and kept against the account under its
 * own name (`useMapMemory("databases")`), and choosing a box lights what it
 * is wired to while the inspector names each link and opens the other end.
 *
 * Its lines say what the old picture's did: one with sessions open on it
 * pulses, one known only as a stack or network shared is dashed, and one that
 * broke or went stale is in that colour. `?database=<id>` flies to one.
 *
 * It reads two things: the server map, for what each thing is and the lines
 * it can prove, and the databases topology, for who is connected now. Either
 * may fail on its own and the other is still drawn, with what could not be
 * read said over the canvas; with both failed, the page says so.
 */
export function DatabaseMap() {
  const { admin, newHref } = useDatabases()
  const wanted = useSearchParams().get("database") ?? ""
  const asked = DATABASE_ID.test(wanted) ? `system:db:${wanted}` : undefined

  const [seeds] = useState(() => ({ topology: lastTopology(), map: lastServerMap() }))
  const topology = usePoll(readTopology, TOPOLOGY_EVERY, [], { seed: seeds.topology })
  const [filling, setFilling] = useState(true)
  const fetchMap = useCallback(async (signal: AbortSignal) => {
    const value = await readServerMap(signal)
    setFilling(stillFilling(value, DATABASE_KINDS))
    return value
  }, [])
  const map = usePoll(fetchMap, filling ? FILLING : MAP_EVERY, [], { seed: seeds.map })
  const memory = useMapMemory("databases")
  const systems = useMemo(
    () => detectDatabaseSystems(map.data, topology.data),
    [map.data, topology.data],
  )

  const root = useRef<HTMLDivElement>(null)
  const screen = useFullscreen(root)

  const unread: Unread[] = []
  if (map.data) unread.push(...unreadKinds(map.data.kinds, DATABASE_KINDS))
  else if (map.error) {
    unread.push({ key: "map", label: "Where they run", reason: map.error.message })
  }
  if (!topology.data && topology.error) {
    unread.push({ key: "topology", label: "Sessions and links", reason: topology.error.message })
  }
  const answered = (map.data || map.error) && (topology.data || topology.error)
  const failed = !map.data && !topology.data && map.error && topology.error

  return (
    <Page fill className="max-w-none gap-2 p-0 lg:px-3 lg:py-3">
      <PageContext eyebrow="Databases" title="Map" />
      <div
        ref={root}
        className={cn(
          "flex min-h-0 min-w-0 flex-1 flex-col bg-background [&:fullscreen]:p-3",
          screen.immersive && "fixed inset-0 z-50 p-3",
        )}
      >
        <Pane className="min-h-0 flex-1 max-lg:rounded-none max-lg:border-0">
          {failed ? (
            <ReadFailed
              className="p-4"
              error={topology.error!}
              onRetry={() => {
                topology.refresh()
                map.refresh()
              }}
              every="30 seconds"
            />
          ) : answered && memory.document ? (
            <ReactFlowProvider>
              <MapCanvas
                board={DATABASES_BOARD}
                systems={systems}
                unread={unread}
                empty={<NothingToMap add={admin ? newHref() : undefined} />}
                doc={memory.document}
                update={memory.update}
                full={screen.full}
                onFullscreen={screen.toggle}
                initialSystem={asked}
              />
            </ReactFlowProvider>
          ) : (
            <MapSkeleton label="Reading the databases" />
          )}
        </Pane>
      </div>
    </Page>
  )
}

/** No database at all: the way to add the first. */
function NothingToMap({ add }: { add?: string }) {
  return (
    <div className="flex h-full items-center justify-center p-8">
      <div className="flex max-w-sm flex-col items-center text-center">
        <ProductLogos ids={["postgres", "mysql", "redis"]} size="md" />
        <p className="mt-4 text-title font-semibold">Nothing to map yet</p>
        <p className="mt-1.5 text-body text-muted-foreground">
          Connect a database and it is drawn here with what reads it, the container it runs in and
          the backups that take it.
        </p>
        {add && (
          <Button size="sm" asChild className="mt-4">
            <Link href={add}>
              <Plus className="size-4" />
              Add a database
            </Link>
          </Button>
        )}
      </div>
    </div>
  )
}
