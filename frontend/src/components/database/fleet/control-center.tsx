"use client"

import { FoundList } from "@/components/database/connect/found-list"
import {
  connectsItself,
  foundShelves,
  waitingServers,
} from "@/components/database/connect/inventory"
import { useFound } from "@/components/database/connect/use-found"
import { engineOf, type Engine } from "@/components/database/engine"
import {
  fleetHues,
  fleetMarks,
  fleetVerdict,
  readerCount,
  shares,
} from "@/components/database/fleet/band"
import {
  fleetReadings,
  groupFleet,
  matchesQuery,
  nextSort,
  sortRows,
  type FleetGrouping,
  type FleetSort,
  type FleetSortKey,
} from "@/components/database/fleet/fleet"
import { FleetBand } from "@/components/database/fleet/fleet-band"
import { FleetCard } from "@/components/database/fleet/fleet-card"
import { FleetTable } from "@/components/database/fleet/fleet-table"
import { McpServer } from "@/components/database/fleet/mcp-server-dialog"
import { MetricsExport } from "@/components/database/fleet/metrics-export-dialog"
import { ReadFailed } from "@/components/database/fleet/read-failed"
import { Shelves } from "@/components/database/fleet/shelves"
import { useAddress, useTypedParam } from "@/components/database/fleet/use-address"
import { useFleet, useInventory } from "@/components/database/fleet/use-fleet"
import { useFleetConfirm } from "@/components/database/fleet/use-fleet-confirm"
import { useFleetControl } from "@/components/database/fleet/use-fleet-control"
import { Wiring } from "@/components/database/fleet/wiring"
import { EngineGlyph } from "@/components/database/kit"
import { useDatabases } from "@/components/database/shell/databases-context"
import { DATABASES_MAP_HREF } from "@/components/database/shell/routes"
import { fleetStatus, statusTone } from "@/components/database/shell/status"
import { Segments } from "@/components/deploy/settings/segments"
import { IconAction } from "@/components/icon-action"
import {
  ArrowRight,
  Database,
  GridSquare,
  Plus,
  RefreshClockwise,
  Table as TableGlyph,
} from "@/components/icons"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Page, PageContext, SearchInput, Section } from "@/components/page"
import { ProductLogos } from "@/components/product-logo"
import { Status } from "@/components/status-dot"
import { EmptyState } from "@/components/state"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { useMediaQuery } from "@/hooks/use-mobile"
import { useMetrics } from "@/hooks/use-metrics"
import { bytes, plural, relativeTime } from "@/lib/format"
import type { DbFleetEntry, DbTopoNode } from "@/lib/types"
import { useViewState } from "@/lib/view-state"
import Link from "next/link"
import { useCallback, useMemo } from "react"

const GROUPINGS: { value: FleetGrouping; label: string }[] = [
  { value: "place", label: "Where it runs" },
  { value: "engine", label: "Engine" },
  { value: "environment", label: "Environment" },
]

/** The engines an empty server is told it could have, as the empty state's mark. */
const OFFERED = ["postgres", "redis", "mongodb"]

/**
 * The control center: every database on this server, drawn as what it is.
 *
 * A reading page (§15) that opens on the server's databases as one line —
 * their engines as their own marks, how many there are, what they store,
 * how many sessions are open, what reads them and how many were dumped in the
 * last day — with the verdict at its right end beside the commands. Under it
 * the band (`fleet-band.tsx`) reads the fleet three ways the cards cannot:
 * who holds the bytes, who is connected, and how old every newest dump is,
 * each database in its own hue (`fleetHues`) from the bar to its card's
 * edge. Then the fleet itself: every saved connection as a card you open,
 * drawn as its engine with its own readings on it, shelved by where it runs —
 * or laid across as a table when there are many to compare. Under it the
 * picture of what the databases feed — the ones nothing is seen reading named
 * under it, each a way to its own page — and what discovery found on this
 * machine that is not connected yet.
 *
 * It opened on a "Needs attention" list until the operator asked for it to
 * go. Nothing it said was lost: every concern is still said, in its weight,
 * where the thing is — the verdict and the first card on a shelf for a server
 * that does not answer, the Backups block and the card's foot for a dump that
 * is old or missing, the card's foot for a port open to the internet, and *Found on
 * this server* for a server that waits for a password. The cards are still
 * ordered worst first, so what needed a hand is still what is met first.
 *
 * Everything on it hangs off one read, `/databases/fleet`, which dials every
 * connection; the dump directories, the inventory and the map decorate it.
 * So the fleet alone decides the page's loading and error states, and once
 * it has answered, a failed poll of anything leaves what is drawn where it
 * is and says so beside the time of the last reading.
 *
 * Search and engine filters are in the address; how the fleet is arranged
 * (shelves, cards or table, the table's order) is the reader's own and kept
 * in view state. Legacy metric-card `show` parameters are ignored.
 */
export function ControlCenter() {
  const { admin, engineFor, newHref } = useDatabases()
  const { host } = useMetrics()
  const inventory = useInventory(admin)
  // What a file weighs, where the engine reports no size and discovery
  // measured the file: by the connection it was found to belong to.
  const measured = useMemo(() => {
    const sizes = new Map<number, number>()
    for (const instance of inventory.data?.instances ?? []) {
      if (!instance.file) continue
      for (const id of instance.connections) sizes.set(id, instance.file.size)
    }
    return sizes
  }, [inventory.data])
  const data = useFleet(measured)
  const { confirm, dialog } = useFleetConfirm()
  const control = useFleetControl({ data, confirm, onForgotten: inventory.refresh })
  const refreshFleet = data.fleet.refresh
  const found = useFound({ inventory, onConnected: refreshFleet, onSynced: refreshFleet })
  const address = useAddress()
  const [grouping, setGrouping] = useViewState<FleetGrouping>("databases.fleet.group", "place")
  const [view, setView] = useViewState<"cards" | "table">("databases.fleet.view", "cards")
  const [sort, setSort] = useViewState<FleetSort>("databases.fleet.sort", null)
  // A table of eleven columns needs the width of one; below it the cards are
  // the same rows drawn down instead of across (§12), so the choice is not
  // offered.
  const roomy = useMediaQuery("(min-width: 1024px)")

  const engineFilter = address.read("engine")
  const [query, setQuery] = useTypedParam("q")
  const { fleet, entries, backups, feeds, concernsOf, storedOf, topology, dumps } = data
  // Each database's hue, dealt once for the whole fleet so a filter never
  // repaints one: the band's spans and keys, the cards' size bars.
  const hues = useMemo(() => fleetHues(entries), [entries])
  const hueOf = useCallback(
    (entry: DbFleetEntry) => hues.get(entry.id) ?? "var(--tag-blue)",
    [hues],
  )
  const largest = useMemo(() => shares(entries, storedOf).largest, [entries, storedOf])
  const newestOf = useCallback((entry: DbFleetEntry) => backups.get(entry.id)?.newest, [backups])

  const engines = useMemo(() => {
    const seen = new Map<string, { engine: Engine; count: number }>()
    for (const entry of entries) {
      const engine = engineFor(entry)
      const held = seen.get(engine.id) ?? { engine, count: 0 }
      held.count += 1
      seen.set(engine.id, held)
    }
    return [...seen.values()].sort((a, b) => a.engine.label.localeCompare(b.engine.label))
  }, [entries, engineFor])

  const shown = useMemo(() => {
    return entries.filter(
      (entry) =>
        (!engineFilter || engineFor(entry).id === engineFilter) &&
        matchesQuery(entry, query, engineFor(entry).label),
    )
  }, [entries, engineFilter, query, engineFor])

  const shelves = useMemo(() => foundShelves(inventory.data), [inventory.data])
  const ready = shelves.servers.filter(connectsItself).length
  // The servers found running here that only a password keeps from being
  // connected: the inventory's reading, and — until it has answered, or when
  // it could not — the two lists the fleet itself carries.
  const waiting = useMemo(
    () => (admin ? waitingServers(fleet.data, inventory.data) : []),
    [admin, fleet.data, inventory.data],
  )
  const refusals = useMemo(
    () => new Map((fleet.data?.unreachable ?? []).map((one) => [one.container, one.reason])),
    [fleet.data],
  )

  // A database node on the map is its connection: drawn as what answered,
  // with the card's own dot in its corner.
  const byId = useMemo(() => new Map(entries.map((entry) => [entry.id, entry])), [entries])
  const entryOf = (node: DbTopoNode) =>
    node.connId === undefined ? undefined : byId.get(node.connId)
  const engineOfNode = (node: DbTopoNode) => {
    const entry = entryOf(node)
    return entry ? engineFor(entry) : engineOf(node.product ?? "")
  }
  const stateOfNode = (node: DbTopoNode) => {
    const entry = entryOf(node)
    return entry ? statusTone(fleetStatus(entry).state) : undefined
  }

  // The page's picture is of what is wired: a database nothing reads is on
  // the map in full, and here it is a name in the line under the picture.
  const feeding = useCallback(
    (node: DbTopoNode) => node.connId !== undefined && feeds.has(node.connId),
    [feeds],
  )
  const unread = (topology.data?.nodes ?? []).filter(
    (node) => node.kind === "database" && !feeding(node),
  )

  const add = admin && (
    <Button size="sm" asChild>
      <Link href={newHref()}>
        <Plus className="size-4" />
        Add a database
      </Link>
    </Button>
  )
  const scan = admin && (
    <IconAction
      label="Scan this server again"
      pending={inventory.scanning}
      onClick={() => void inventory.scan()}
    >
      <RefreshClockwise />
    </IconAction>
  )

  if (!fleet.data) {
    return (
      <Page className="animate-rise">
        {/* The page's commands stay through a failed read: adding a database
            does not need the fleet to have answered. */}
        <PageContext
          eyebrow="Apps"
          title="Databases"
          className="justify-end"
          actions={
            admin && (
              <>
                {scan}
                {add}
              </>
            )
          }
        />
        {fleet.error ? (
          <ReadFailed error={fleet.error} onRetry={fleet.refresh} every="30 seconds" />
        ) : (
          <div role="status" aria-label="Loading databases" className="flex flex-col gap-8">
            <div className="grid grid-cols-[repeat(auto-fill,minmax(19rem,1fr))] gap-3">
              {[0, 1, 2, 3, 4, 5].map((card) => (
                <Skeleton key={card} className="h-44 rounded-xl" />
              ))}
            </div>
          </div>
        )}
      </Page>
    )
  }

  const foundSection = admin && (
    <Section
      title="Found on this server"
      className="animate-rise"
      actions={
        <span className="flex items-center gap-3">
          {inventory.scanError && (
            <span className="text-hint text-destructive">{inventory.scanError.message}</span>
          )}
          {ready > 1 && (
            <Button
              size="sm"
              variant="outline"
              pending={found.syncing}
              onClick={() => void found.connectAll()}
            >
              Connect all {ready}
            </Button>
          )}
        </span>
      }
    >
      <FoundList inventory={inventory} found={found} waiting={waiting} refusals={refusals} />
    </Section>
  )

  if (entries.length === 0) {
    return (
      <Page className="animate-rise">
        <PageContext eyebrow="Apps" title="Databases" actions={scan} className="justify-end" />
        <EmptyState
          mark={<ProductLogos ids={OFFERED} size="md" />}
          title="No databases yet"
          description={
            admin
              ? "Start one in a container on this server, connect one that already runs here, or point the dashboard at one somewhere else."
              : "Nothing is connected to this dashboard. An administrator can add a database."
          }
          action={add}
        />
        {foundSection}
        {found.dialog}
      </Page>
    )
  }

  const narrowed = engineFilter !== "" || query !== ""
  const clear = () => address.set({ show: null, engine: null, q: null })
  const groups = groupFleet(shown, grouping, (entry) => engineFor(entry).label)
  const table = roomy && view === "table"
  const onSort = (key: FleetSortKey) => setSort((held) => nextSort(held, key))
  const position = new Map(shown.map((entry, index) => [entry.id, index]))

  // The server's databases as one line: their engines as the mark, then the
  // figures the band breaks down, each said once here and never as a tile.
  const readings = fleetReadings(fleet.data, { backups, dumps, storedOf })
  const verdict = fleetVerdict(readings)
  // Engines as common as each other are drawn largest first.
  const bySize = [...entries].sort((a, b) => (storedOf(b) ?? 0) - (storedOf(a) ?? 0))
  const marks = fleetMarks(bySize.map((entry) => engineFor(entry).logo))
  const readers = readerCount(topology.data)
  const facts = [
    <span key="count" className="numeric">
      {plural(readings.total, "database")}
    </span>,
    readings.sized > 0 && (
      <span
        key="stored"
        className="numeric"
        title={
          readings.sized < readings.total
            ? `${readings.sized} of ${readings.total} report their size`
            : undefined
        }
      >
        {bytes(readings.bytes)} stored
      </span>
    ),
    readings.answering > 0 && (
      <span key="sessions" className="numeric">
        {plural(readings.sessions, "session")} open
      </span>
    ),
    readers > 0 && (
      <Link
        key="readers"
        href={DATABASES_MAP_HREF}
        title="Deployments, containers and machines seen reading these databases"
        className="numeric rounded-sm focus-ring transition-colors hover:text-foreground hover:underline"
      >
        read by {readers}
      </Link>
    ),
    readings.dumpable > 0 && (
      <span key="fresh" className="numeric">
        {readings.fresh} of {readings.dumpable} backed up in the last day
      </span>
    ),
    readings.never > 0 && (
      <span key="never" className="numeric font-medium text-warning">
        {readings.never} never backed up
      </span>
    ),
  ]
    .filter(Boolean)
    .flatMap((fact, index) => (index === 0 ? [fact] : [<FactDot key={`dot-${index}`} />, fact]))

  return (
    <Page className="animate-rise">
      {/* The commands are at the identity line's end, beside the verdict. */}
      <PageContext eyebrow="Apps" title="Databases" />

      <HostIdentity
        className="animate-rise"
        logo={
          marks.length > 0 ? (
            <span className="flex h-12 shrink-0 items-center">
              <ProductLogos ids={marks} size="md" />
            </span>
          ) : undefined
        }
        fallback={Database}
        title={host?.hostname ?? "This server"}
        facts={facts}
        aside={
          <div className="flex flex-wrap items-center gap-x-6 gap-y-3">
            {verdict && <Status tone={verdict.tone} label={verdict.label} />}
            <span className="flex flex-wrap items-center gap-2">
              <MetricsExport admin={admin} />
              <McpServer />
              {admin && (
                <>
                  {scan}
                  {add}
                </>
              )}
            </span>
          </div>
        }
      />

      <FleetBand
        entries={entries}
        hueOf={hueOf}
        engineOf={engineFor}
        storedOf={storedOf}
        newestOf={newestOf}
        dumps={dumps}
        feeds={feeds}
      />

      <Section
        title="All databases"
        actions={
          <span className="flex items-center gap-2 text-hint text-muted-foreground">
            {fleet.error ? (
              <>
                <span className="text-warning">the last check did not finish</span>
                <IconAction label="Check again" onClick={fleet.refresh}>
                  <RefreshClockwise />
                </IconAction>
              </>
            ) : (
              <span>checked {relativeTime(fleet.data.checkedAt)}</span>
            )}
          </span>
        }
      >
        <div className="flex min-w-0 flex-col gap-2.5">
          <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2">
            <SearchInput
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="Filter databases"
              aria-label="Filter databases"
            />
            <span className="ml-auto flex max-w-full shrink-0 flex-wrap items-center gap-2">
              {!table && (
                <Segments
                  label="Group by"
                  value={grouping}
                  options={GROUPINGS}
                  onChange={setGrouping}
                />
              )}
              {roomy && (
                <Segments
                  label="Draw as"
                  value={view}
                  onChange={setView}
                  options={[
                    {
                      value: "cards",
                      label: (
                        <>
                          <GridSquare aria-hidden className="size-3.5" />
                          <span className="sr-only">Cards</span>
                        </>
                      ),
                    },
                    {
                      value: "table",
                      label: (
                        <>
                          <TableGlyph aria-hidden className="size-3.5" />
                          <span className="sr-only">Table</span>
                        </>
                      ),
                    },
                  ]}
                />
              )}
            </span>
          </div>
          {engines.length > 1 && (
            <ChipStrip role="group" aria-label="Narrow the databases">
              {engines.length > 1 && (
                <>
                  <FilterChip
                    selected={engineFilter === ""}
                    onClick={() => address.set({ engine: null })}
                  >
                    All engines <ChipCount>{entries.length}</ChipCount>
                  </FilterChip>
                  {engines.map(({ engine, count }) => (
                    <FilterChip
                      key={engine.id}
                      selected={engineFilter === engine.id}
                      onClick={() =>
                        address.set({ engine: engineFilter === engine.id ? null : engine.id })
                      }
                    >
                      <EngineGlyph engine={engine} />
                      {engine.label} <ChipCount>{count}</ChipCount>
                    </FilterChip>
                  ))}
                </>
              )}
            </ChipStrip>
          )}
        </div>

        {shown.length === 0 ? (
          <EmptyState
            title="No database matches"
            description={
              narrowed ? "Nothing in the fleet matches what the list is narrowed to." : undefined
            }
            action={
              <Button size="sm" variant="outline" onClick={clear}>
                Show every database
              </Button>
            }
          />
        ) : table ? (
          <FleetTable
            entries={sortRows(shown, sort, (entry) => backups.get(entry.id)?.newest, storedOf)}
            engineOf={engineFor}
            concernsOf={concernsOf}
            backups={backups}
            feeds={feeds}
            storedOf={storedOf}
            hueOf={hueOf}
            largest={largest}
            control={control}
            sort={sort}
            onSort={onSort}
          />
        ) : (
          <Shelves
            groups={groups}
            leading={
              grouping === "engine"
                ? (group) => <EngineGlyph engine={engineFor(group.entries[0])} />
                : undefined
            }
            card={(entry: DbFleetEntry) => (
              <FleetCard
                key={entry.id}
                entry={entry}
                engine={engineFor(entry)}
                concerns={concernsOf(entry)}
                backup={backups.get(entry.id)}
                feeds={feeds.get(entry.id)}
                stored={storedOf(entry)}
                hue={hueOf(entry)}
                largest={largest}
                control={control}
                index={position.get(entry.id) ?? 0}
              />
            )}
          />
        )}
      </Section>

      <Section
        title="What they feed"
        className="animate-rise"
        actions={
          <Link
            href={DATABASES_MAP_HREF}
            className="flex items-center gap-1 rounded-sm text-body text-muted-foreground focus-ring transition-colors hover:text-foreground"
          >
            Open the map <ArrowRight aria-hidden className="size-3.5" />
          </Link>
        }
      >
        {topology.data ? (
          topology.data.edges.length > 0 ? (
            <>
              <Wiring
                topology={topology.data}
                keep={feeding}
                engineOf={engineOfNode}
                stateOf={stateOfNode}
              />
              {unread.length > 0 && (
                <div className="flex min-w-0 flex-wrap items-center gap-x-5 gap-y-2 border-t border-hairline pt-4">
                  <span className="text-hint text-muted-foreground">Nothing seen reading</span>
                  <ul className="flex min-w-0 flex-wrap items-center gap-x-5 gap-y-2">
                    {unread.map((node) => (
                      <li key={node.id} className="min-w-0">
                        <Link
                          href={node.href ?? DATABASES_MAP_HREF}
                          className="flex min-w-0 items-center gap-2 rounded-sm text-body text-muted-foreground focus-ring transition-colors hover:text-foreground"
                        >
                          <EngineGlyph engine={engineOfNode(node)} className="size-4" />
                          <span className="truncate">{node.name}</span>
                        </Link>
                      </li>
                    ))}
                  </ul>
                </div>
              )}
            </>
          ) : (
            <p className="text-body text-muted-foreground">
              Nothing has been seen reading these databases yet. A deployment that links one, a
              container whose environment names one, or an open session appears here.
            </p>
          )
        ) : topology.error ? (
          <ReadFailed error={topology.error} onRetry={topology.refresh} every="45 seconds" />
        ) : (
          <Skeleton className="h-40 rounded-xl" />
        )}
      </Section>

      {foundSection}

      {dialog}
      {found.dialog}
    </Page>
  )
}
