"use client"

import { useCallback, useMemo } from "react"
import Link from "next/link"
import {
  ArrowRight,
  Cross,
  GridSquare,
  Plus,
  RefreshClockwise,
  Table as TableGlyph,
} from "@/components/icons"
import { relativeTime } from "@/lib/format"
import { useViewState } from "@/lib/view-state"
import type { DbFleetEntry, DbTopoNode } from "@/lib/types"
import { useMediaQuery } from "@/hooks/use-mobile"
import { useConfirm } from "@/components/confirm-dialog"
import { Segments } from "@/components/deploy/settings/segments"
import { GroupRule } from "@/components/flow"
import { IconAction } from "@/components/icon-action"
import { Page, PageContext, SearchInput, Section } from "@/components/page"
import { ProductLogos } from "@/components/product-logo"
import { EmptyState, ErrorState } from "@/components/state"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { engineOf, type Engine } from "@/components/database/engine"
import { FoundList } from "@/components/database/connect/found-list"
import { connectsItself, foundShelves } from "@/components/database/connect/inventory"
import { useFound } from "@/components/database/connect/use-found"
import { Attention } from "@/components/database/fleet/attention"
import {
  FLEET_SHOWS,
  groupFleet,
  matchesQuery,
  matchesShow,
  nextSort,
  orderForShow,
  sortRows,
  type FleetGrouping,
  type FleetShow,
  type FleetSort,
  type FleetSortKey,
} from "@/components/database/fleet/fleet"
import { FleetCard } from "@/components/database/fleet/fleet-card"
import { FleetTable } from "@/components/database/fleet/fleet-table"
import { FleetReadings, ReadingsSkeleton } from "@/components/database/fleet/readings"
import { useAddress } from "@/components/database/fleet/use-address"
import { useFleet, useInventory } from "@/components/database/fleet/use-fleet"
import { useFleetControl } from "@/components/database/fleet/use-fleet-control"
import { Wiring } from "@/components/database/fleet/wiring"
import { EngineGlyph } from "@/components/database/kit"
import { useDatabases } from "@/components/database/shell/databases-context"
import { DATABASES_MAP_HREF } from "@/components/database/shell/routes"

const SHOW_WORDS: Record<Exclude<FleetShow, "all">, string> = {
  down: "Not running",
  stored: "By what they store",
  busy: "With open sessions",
  unprotected: "No backup in the last day",
}

const GROUPINGS: { value: FleetGrouping; label: string }[] = [
  { value: "place", label: "Where it runs" },
  { value: "engine", label: "Engine" },
  { value: "environment", label: "Environment" },
]

/** The engines an empty server is told it could have, as the empty state's mark. */
const OFFERED = ["postgres", "redis", "mongodb"]

/**
 * The control center: every database on this server, and what each needs.
 *
 * A reading page (§15) built from the questions somebody arriving at
 * *Databases* has, in the order they are asked. Are they all up, how much do
 * they hold, is anything using them, are they backed up — five figures, each
 * of which narrows the fleet to the databases it is about. What needs a hand,
 * with the fix as the action. Then the fleet itself: every saved connection
 * as a card you open, drawn as its engine with its own readings on it,
 * shelved by where it runs — or laid across as a table when there are many to
 * compare. Under it, what discovery found on this machine that is not
 * connected yet, and the picture of what the databases feed.
 *
 * Everything on it hangs off one read, `/databases/fleet`, which dials every
 * connection; the dump directories, the inventory and the map decorate it.
 * So the fleet alone decides the page's loading and error states, and once
 * it has answered, a failed poll of anything leaves what is drawn where it
 * is and says so beside the time of the last reading.
 *
 * What narrows the fleet (`show`, `engine`, `q`) is in the address, so a link
 * to "the ones that are not running" is that; how it is arranged (shelves,
 * cards or table, the table's order) is the reader's own and kept in view
 * state.
 */
export function ControlCenter() {
  const { admin, engineFor, newHref } = useDatabases()
  const data = useFleet()
  const inventory = useInventory(admin)
  const { confirm, dialog } = useConfirm()
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

  const asked = address.read("show") as FleetShow
  const show: FleetShow = FLEET_SHOWS.includes(asked) ? asked : "all"
  const engineFilter = address.read("engine")
  const query = address.read("q")
  const { fleet, entries, backups, feeds, concernsOf, topology } = data

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
    const narrowed = entries.filter(
      (entry) =>
        matchesShow(entry, show, { backup: backups.get(entry.id), dumps: data.dumps(entry) }) &&
        (!engineFilter || engineFor(entry).id === engineFilter) &&
        matchesQuery(entry, query, engineFor(entry).label),
    )
    return orderForShow(narrowed, show)
  }, [entries, show, engineFilter, query, backups, data, engineFor])

  // What a file weighs, where the engine reports no size and discovery
  // measured the file: by the connection it was found to belong to.
  const fileSizes = useMemo(() => {
    const sizes = new Map<number, number>()
    for (const instance of inventory.data?.instances ?? []) {
      if (!instance.file) continue
      for (const id of instance.connections) sizes.set(id, instance.file.size)
    }
    return sizes
  }, [inventory.data])

  const shelves = useMemo(() => foundShelves(inventory.data), [inventory.data])
  const ready = shelves.servers.filter(connectsItself).length

  // A database node on the map is its connection: drawn as what answered.
  const byId = useMemo(() => new Map(entries.map((entry) => [entry.id, entry])), [entries])
  const engineOfNode = (node: DbTopoNode) => {
    const entry = node.connId === undefined ? undefined : byId.get(node.connId)
    return entry ? engineFor(entry) : engineOf(node.product ?? "")
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
        <PageContext eyebrow="Apps" title="Databases" />
        {fleet.error ? (
          <ErrorState error={fleet.error} onRetry={fleet.refresh} />
        ) : (
          <div role="status" aria-label="Loading databases" className="flex flex-col gap-8">
            <ReadingsSkeleton />
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
      <FoundList inventory={inventory} found={found} />
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

  const narrowed = show !== "all" || engineFilter !== "" || query !== ""
  const clear = () => address.set({ show: null, engine: null, q: null })
  const groups = groupFleet(shown, grouping, (entry) => engineFor(entry).label)
  const table = roomy && view === "table"
  const onSort = (key: FleetSortKey) => setSort((held) => nextSort(held, key))
  const position = new Map(shown.map((entry, index) => [entry.id, index]))

  return (
    <Page className="animate-rise">
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

      <FleetReadings
        data={data}
        engines={engines.flatMap((one) => (one.engine.logo ? [one.engine.logo] : []))}
        show={show}
        onShow={(next) => address.set({ show: next === "all" ? null : next })}
      />

      <Attention
        data={data}
        control={control}
        found={admin ? found : undefined}
        instances={inventory.data?.instances ?? []}
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
              onChange={(event) => address.set({ q: event.target.value }, "replace")}
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
          {(show !== "all" || engines.length > 1) && (
            <ChipStrip role="group" aria-label="Narrow the databases">
              {show !== "all" && (
                <FilterChip selected onClick={() => address.set({ show: null })}>
                  {SHOW_WORDS[show]}
                  <Cross aria-hidden className="size-3 opacity-70" />
                  <span className="sr-only">, remove</span>
                </FilterChip>
              )}
              {engines.length > 1 && (
                <>
                  <FilterChip
                    selected={engineFilter === ""}
                    onClick={() => address.set({ engine: null })}
                  >
                    All <ChipCount>{entries.length}</ChipCount>
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
            title={
              show === "down" && !engineFilter && !query
                ? "Every database is running"
                : "No database matches"
            }
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
            entries={sortRows(shown, sort, (entry) => backups.get(entry.id)?.newest)}
            engineOf={engineFor}
            concernsOf={concernsOf}
            backups={backups}
            feeds={feeds}
            control={control}
            sort={sort}
            onSort={onSort}
          />
        ) : (
          <div className="flex min-w-0 flex-col gap-5">
            {groups.map((group) => (
              <div key={group.key} className="flex min-w-0 flex-col gap-3">
                <GroupRule
                  label={group.label}
                  count={group.entries.length}
                  leading={
                    grouping === "engine" ? (
                      <EngineGlyph engine={engineFor(group.entries[0])} />
                    ) : undefined
                  }
                />
                <ul
                  aria-label={group.label}
                  className="grid min-w-0 grid-cols-[repeat(auto-fill,minmax(min(19rem,100%),1fr))] gap-3"
                >
                  {group.entries.map((entry: DbFleetEntry) => (
                    <FleetCard
                      key={entry.id}
                      entry={entry}
                      engine={engineFor(entry)}
                      concerns={concernsOf(entry)}
                      backup={backups.get(entry.id)}
                      feeds={feeds.get(entry.id)}
                      fileSize={fileSizes.get(entry.id)}
                      control={control}
                      index={position.get(entry.id) ?? 0}
                    />
                  ))}
                </ul>
              </div>
            ))}
          </div>
        )}
      </Section>

      {foundSection}

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
              <Wiring topology={topology.data} keep={feeding} engineOf={engineOfNode} compact />
              {unread.length > 0 && (
                <p className="text-hint text-muted-foreground">
                  Nothing has been seen reading {unread.map((node) => node.name).join(", ")}.
                </p>
              )}
            </>
          ) : (
            <p className="text-body text-muted-foreground">
              Nothing has been seen reading these databases yet. A deployment that links one, a
              container whose environment names one, or an open session appears here.
            </p>
          )
        ) : topology.error ? (
          <ErrorState error={topology.error} onRetry={topology.refresh} />
        ) : (
          <Skeleton className="h-40 rounded-xl" />
        )}
      </Section>

      {dialog}
      {found.dialog}
    </Page>
  )
}
