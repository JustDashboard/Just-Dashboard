"use client"

import { useCallback, useMemo, useRef, useState } from "react"
import Link from "next/link"
import { useSearchParams } from "next/navigation"
import { ReactFlowProvider } from "@xyflow/react"
import { engineOf } from "@/components/database/engine"
import { lastTopology, readTopology } from "@/components/database/fleet/read"
import { useDatabases } from "@/components/database/shell/databases-context"
import { DATABASE_ID } from "@/components/database/shell/routes"
import { Plus, Route } from "@/components/icons"
import {
  MapSkeleton,
  lastServerMap,
  readServerMap,
  stillFilling,
  unreadKinds,
} from "@/components/map"
import type { MapBoard } from "@/components/map/board"
import { MapCanvas, type Unread } from "@/components/map/canvas"
import { useMapMemory } from "@/components/map/memory"
import { Page, PageContext } from "@/components/page"
import { Pane } from "@/components/panel"
import { ProductLogos } from "@/components/product-logo"
import { ErrorState } from "@/components/state"
import { Button } from "@/components/ui/button"
import { useFullscreen } from "@/hooks/use-fullscreen"
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
    // The page draws no header, so the rail is headed as the server map's
    // is, level with the inspector's head across the canvas: one pattern,
    // drawn one way on both maps.
    head: { icon: Route, title: "Map" },
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
      // The engines, as a fact on the title's line: it was a sentence about
      // the picture, which the picture already is.
      line: engineNames(systems),
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
  home: busiest,
}

/** The engines a set of databases runs on, by name, in the rail's order. */
function engineNames(systems: System[]): string {
  const names = new Set(systems.map((system) => engineOf(system.anchor.product ?? "").label))
  return [...names].sort((a, b) => a.localeCompare(b)).join(", ")
}

/**
 * The database the canvas opens on when every diagram at once is too small to
 * read: the one with the most reading it and the most sessions open, then the
 * largest diagram. It opened on all five at 36%, with names 4px tall.
 */
function busiest(systems: System[]): string | undefined {
  let best: { id: string; score: number[] } | undefined
  for (const system of systems) {
    const census = databaseCensus(system)
    const score = [census.readers.length, census.sessions, system.nodes.length]
    if (!best || compareScores(score, best.score) > 0) best = { id: system.id, score }
  }
  return best?.id
}

/** Which of two scores is larger, by their first figure that differs. */
function compareScores(a: readonly number[], b: readonly number[]): number {
  const at = a.findIndex((value, index) => value !== b[index])
  return at < 0 ? 0 : a[at] - b[at]
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
 * On a phone the canvas is one of three panes: the rail of databases and the
 * inspector are the other two, and a bar at the foot picks which is shown.
 * It was the canvas alone there, with no way to the list or to a link's name.
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
    <Page fill="workspace" className="gap-2">
      <PageContext title="Map" />
      <div
        ref={root}
        className={cn(
          "flex min-h-0 min-w-0 flex-1 flex-col bg-background",
          screen.immersive && "fixed inset-0 z-immersive",
        )}
      >
        <Pane flush className="min-h-0 flex-1">
          {failed ? (
            <ErrorState
              className="p-4"
              error={topology.error!}
              onRetry={() => {
                topology.refresh()
                map.refresh()
              }}
              retryable
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
                panes
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
