"use client"

import { FoundList } from "@/components/database/connect/found-list"
import {
  connectsItself,
  foundShelves,
  waitingServers,
} from "@/components/database/connect/inventory"
import { useFound } from "@/components/database/connect/use-found"
import { type Engine } from "@/components/database/engine"
import { fleetHues, fleetMarks, fleetVerdict, readerCount } from "@/components/database/fleet/band"
import {
  fleetReadings,
  groupFleet,
  matchesQuery,
  type FleetGrouping,
} from "@/components/database/fleet/fleet"
import { FleetBand } from "@/components/database/fleet/fleet-band"
import { FleetCard } from "@/components/database/fleet/fleet-card"
import { McpServer } from "@/components/database/fleet/mcp-server-dialog"
import { MetricsExport } from "@/components/database/fleet/metrics-export-dialog"
import { ReadFailed } from "@/components/database/fleet/read-failed"
import { Shelves } from "@/components/database/fleet/shelves"
import { useAddress, useTypedParam } from "@/components/database/fleet/use-address"
import { useFleet, useInventory } from "@/components/database/fleet/use-fleet"
import { useFleetConfirm } from "@/components/database/fleet/use-fleet-confirm"
import { useFleetControl } from "@/components/database/fleet/use-fleet-control"
import { EngineGlyph } from "@/components/database/kit"
import { useDatabases } from "@/components/database/shell/databases-context"
import { DATABASES_MAP_HREF } from "@/components/database/shell/routes"
import { Segments } from "@/components/deploy/settings/segments"
import { IconAction } from "@/components/icon-action"
import {
  Database,
  LineChart,
  MoreHorizontal,
  Plus,
  RefreshClockwise,
  Sparkles,
} from "@/components/icons"
import { HostIdentity } from "@/components/metrics/host-identity"
import { Page, PageContext, SearchInput, Section } from "@/components/page"
import { ProductLogos } from "@/components/product-logo"
import { Status } from "@/components/status-dot"
import { EmptyState } from "@/components/state"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { VerbMenu, type Verb } from "@/components/verbs"
import { useMetrics } from "@/hooks/use-metrics"
import { bytes, plural, relativeTime } from "@/lib/format"
import type { DbFleetEntry } from "@/lib/types"
import { useViewState } from "@/lib/view-state"
import Link from "next/link"
import { useCallback, useMemo, useRef, useState } from "react"

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
 * A reading page (§15) that opens on the server's databases as one line, laid
 * out as a project's header is: their engines as their own marks, the server's
 * name with the verdict after it, then how many there are, what they store,
 * how many sessions are open, what reads them and how many were dumped in the
 * last day. At its end stands the one command, Add a database, and a menu
 * with the rest: where the recorded activity is exported, the MCP server's
 * keys, and scanning the server again. They were three grey controls beside
 * the command, and the line is drawn in every state of the page — loading,
 * failed and with no database yet — so it is where it will be from the first
 * paint. Under it
 * the band (`fleet-band.tsx`) reads the fleet three ways the cards cannot:
 * who holds the bytes, who is connected, and how old every newest dump is,
 * each database in its own hue (`fleetHues`) from the bar to its card's
 * edge. Then the fleet itself: every saved connection as a card you open,
 * drawn as its engine with its own readings on it, shelved by where it runs.
 * Under it, what discovery found on this machine that is not connected yet.
 *
 * Two things it drew are gone at the operator's word. The table the cards
 * could be laid out as: the cards are the one way the fleet is drawn. And the
 * picture of what the databases feed, with the line of the ones nothing reads
 * under it: what feeds what is the map's (`map.tsx`), reached from the
 * "read by" figure in the line at the top and from the section's own tabs.
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
 * connection; the dump directories, the inventory and the topology decorate it.
 * So the fleet alone decides the page's loading and error states, and once
 * it has answered, a failed poll of anything leaves what is drawn where it
 * is and says so beside the time of the last reading.
 *
 * Search and engine filters are in the address; how the cards are shelved is
 * the reader's own and kept in view state. Legacy metric-card `show`
 * parameters, and the table a reader once chose, are ignored.
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

  const add = admin && (
    <Button size="sm" asChild>
      <Link href={newHref()}>
        <Plus className="size-4" />
        Add a database
      </Link>
    </Button>
  )
  // The dialogs are opened by state from a menu that closes on a choice, so
  // the keyboard goes back to the menu's own button when one is closed.
  const [opened, setOpened] = useState<"metrics" | "mcp" | null>(null)
  const more = useRef<HTMLButtonElement>(null)
  const verbs: Verb[] = [
    { key: "metrics", label: "Metrics export", icon: LineChart, run: () => setOpened("metrics") },
    { key: "mcp", label: "MCP server", icon: Sparkles, run: () => setOpened("mcp") },
    ...(admin
      ? [
          {
            key: "scan",
            label: "Scan this server again",
            icon: RefreshClockwise,
            disabled: inventory.scanning,
            run: () => void inventory.scan(),
          },
        ]
      : []),
  ]
  const menu = (
    <VerbMenu
      verbs={verbs}
      label="More about the databases"
      trigger={
        <Button
          ref={more}
          size="icon-sm"
          variant="ghost"
          aria-label="More about the databases"
          className="[&_svg:not([class*='size-'])]:size-3.5"
        >
          <MoreHorizontal />
        </Button>
      }
    />
  )
  const dialogs = (
    <>
      {opened === "metrics" && (
        <MetricsExport admin={admin} opener={more} onClose={() => setOpened(null)} />
      )}
      {opened === "mcp" && <McpServer opener={more} onClose={() => setOpened(null)} />}
    </>
  )
  // The line the page opens on, before the fleet has answered and when it
  // holds nothing: the server by its name, in the place and at the height the
  // full line takes, with the command and the menu already where they will be.
  const opening = (facts: React.ReactNode, command: React.ReactNode = add) => (
    <HostIdentity
      label="About the databases on this server"
      fallback={Database}
      title={host?.hostname ?? "This server"}
      state={null}
      facts={facts}
      command={command || undefined}
      menu={menu}
      foot
    />
  )

  if (!fleet.data) {
    return (
      <Page className="animate-rise">
        {/* The page's commands stay through a failed read: adding a database
            does not need the fleet to have answered. */}
        <PageContext eyebrow="Apps" title="Databases" />
        {opening(fleet.error ? "The databases could not be read" : "Reading the databases…")}
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
        {dialogs}
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
        <PageContext eyebrow="Apps" title="Databases" />
        {/* The empty state under it holds the page's one command. */}
        {opening("No database is connected yet", null)}
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
        {dialogs}
      </Page>
    )
  }

  const narrowed = engineFilter !== "" || query !== ""
  const clear = () => address.set({ show: null, engine: null, q: null })
  const groups = groupFleet(shown, grouping, (entry) => engineFor(entry).label)
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
    // The line's own gap separates them: a dot between two was left hanging
    // at the end of a line wherever the facts wrapped.
    .filter(Boolean)

  return (
    <Page className="animate-rise">
      {/* The commands are at the identity line's end, beside the verdict. */}
      <PageContext eyebrow="Apps" title="Databases" />

      <HostIdentity
        label="About the databases on this server"
        logo={
          marks.length > 0 ? (
            <span className="flex h-10 shrink-0 items-center">
              <ProductLogos ids={marks} size="md" />
            </span>
          ) : undefined
        }
        fallback={Database}
        title={host?.hostname ?? "This server"}
        state={verdict ? <Status tone={verdict.tone} label={verdict.label} /> : null}
        facts={facts}
        command={add || undefined}
        menu={menu}
        // On a phone the command and the menu stand under the facts: beside
        // the engines' marks they left the server's name a word to a line.
        foot
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
            <Segments
              label="Group by"
              value={grouping}
              options={GROUPINGS}
              onChange={setGrouping}
              className="ml-auto shrink-0"
            />
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
                dumps={dumps(entry)}
                control={control}
                index={position.get(entry.id) ?? 0}
              />
            )}
          />
        )}
      </Section>

      {foundSection}

      {dialog}
      {found.dialog}
      {dialogs}
    </Page>
  )
}
