"use client"

import { useCallback, useMemo } from "react"
import Link from "next/link"
import { Plus } from "@/components/icons"
import { plural } from "@/lib/format"
import type { DbTopoNode } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Page, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductGlyphs, ProductLogos } from "@/components/product-logo"
import { Row, RowList } from "@/components/row-list"
import { EmptyNote, EmptyState, ErrorState } from "@/components/state"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Status } from "@/components/status-dot"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { NumberTicker } from "@/components/ui/number-ticker"
import { Skeleton } from "@/components/ui/skeleton"
import { engineOf, type Engine } from "@/components/database/engine"
import {
  describeEdge,
  edgeRank,
  splitTopology,
  topologyReadings,
  worstStatus,
} from "@/components/database/fleet/fleet"
import { readFleet, readTopology } from "@/components/database/fleet/read"
import { useAddress } from "@/components/database/fleet/use-address"
import {
  ConsumerMark,
  Wiring,
  WiringLegend,
  consumerKind,
  consumerVerdict,
  consumerWord,
} from "@/components/database/fleet/wiring"
import { EngineGlyph } from "@/components/database/kit"
import { useDatabases } from "@/components/database/shell/databases-context"

/**
 * The map: what feeds what, as one picture.
 *
 * A reading page. It opens on the picture's own figures — how many databases
 * are drawn, how many things read them, how many sessions the wires are
 * carrying, how many links are wrong — then the picture at full size with
 * what its lines mean, and beside it (below it, on a narrower screen) every
 * reader as a row: which databases reach it, how each link is known, whether
 * the link holds. The chips narrow the picture to one engine's databases and
 * the readers they feed; the choice is in the address.
 *
 * The lines are what the server found, not what anybody drew: a deployment's
 * binding, a name in a container's environment, a shared stack or network, an
 * open session.
 */
export function DatabaseMap() {
  const { admin, connections, engineFor, newHref } = useDatabases()
  const address = useAddress()
  const topology = usePoll(readTopology, 30_000)
  // Only the fleet knows what answered behind a driver (MariaDB behind
  // `mysql`), so the marks follow it where it has been read.
  const fleet = usePoll(readFleet, 60_000)
  const engineFilter = address.read("engine")

  const engineOfNode = useCallback(
    (node: DbTopoNode): Engine => {
      const saved =
        fleet.data?.connections.find((entry) => entry.id === node.connId) ??
        connections.find((conn) => conn.id === node.connId)
      return saved ? engineFor(saved) : engineOf(node.product ?? "")
    },
    [fleet.data, connections, engineFor],
  )
  const keep = useCallback(
    (node: DbTopoNode) => !engineFilter || engineOfNode(node).id === engineFilter,
    [engineFilter, engineOfNode],
  )

  const all = useMemo(() => splitTopology(topology.data), [topology.data])
  const split = useMemo(() => splitTopology(topology.data, keep), [topology.data, keep])
  const readings = topologyReadings(split)
  const engines = useMemo(() => {
    const seen = new Map<string, { engine: Engine; count: number }>()
    for (const node of all.databases) {
      const engine = engineOfNode(node)
      const held = seen.get(engine.id) ?? { engine, count: 0 }
      held.count += 1
      seen.set(engine.id, held)
    }
    return [...seen.values()].sort((a, b) => a.engine.label.localeCompare(b.engine.label))
  }, [all.databases, engineOfNode])

  if (!topology.data) {
    return (
      <Page className="animate-rise">
        <PageContext eyebrow="Databases" title="Map" />
        {topology.error ? (
          <ErrorState error={topology.error} onRetry={topology.refresh} />
        ) : (
          <div role="status" aria-label="Loading the map" className="flex flex-col gap-8">
            <StatGrid columns={4} dense aria-hidden>
              {["Databases", "Readers", "Sessions", "Links"].map((label) => (
                <div key={label} className="flex min-w-0 flex-col gap-1.5 px-5 py-4">
                  <p className="eyebrow truncate">{label}</p>
                  <Skeleton className="h-7 w-12" />
                  <Skeleton className="h-3 w-24" />
                </div>
              ))}
            </StatGrid>
            <Skeleton className="h-96 rounded-xl" />
          </div>
        )}
      </Page>
    )
  }

  if (all.databases.length === 0) {
    return (
      <Page className="animate-rise">
        <PageContext eyebrow="Databases" title="Map" />
        <EmptyState
          mark={<ProductLogos ids={["postgres", "mysql", "redis"]} size="md" />}
          title="Nothing to map yet"
          description="Connect a database and the things that read it appear here as they are found."
          action={
            admin && (
              <Button size="sm" asChild>
                <Link href={newHref()}>
                  <Plus className="size-4" />
                  Add a database
                </Link>
              </Button>
            )
          }
        />
      </Page>
    )
  }

  const readers = split.consumers.filter((node) => split.fedBy.has(node.id))

  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Databases" title="Map" />

      <StatGrid columns={4} dense>
        <StatTile
          label="Databases"
          value={<NumberTicker value={readings.databases} />}
          hint={
            <span className="flex min-w-0 items-center gap-1.5">
              <span className="truncate">
                {engineFilter ? `of ${all.databases.length}` : plural(engines.length, "engine")}
              </span>
              <ProductGlyphs
                ids={engines.flatMap((one) => (one.engine.logo ? [one.engine.logo] : []))}
              />
            </span>
          }
        />
        <StatTile
          label="Readers"
          value={<NumberTicker value={readings.consumers} />}
          hint={
            readings.consumers === 0
              ? "nothing seen reading them"
              : `over ${plural(readings.links, "link")}`
          }
        />
        <StatTile
          label="Sessions"
          value={readings.sessions.toLocaleString()}
          trailing="open"
          hint="carried by the lit wires"
        />
        <StatTile
          label="Links wrong"
          value={readings.wrong.toLocaleString()}
          tone={readings.wrong > 0 ? "warning" : "default"}
          hint={
            topology.error
              ? "the last check did not finish"
              : readings.wrong > 0
                ? "broken or stale"
                : "every link holds"
          }
        />
      </StatGrid>

      <div className="flex min-w-0 flex-wrap items-center gap-x-6 gap-y-2">
        {engines.length > 1 && (
          <ChipStrip role="group" aria-label="Engines">
            <FilterChip
              selected={engineFilter === ""}
              onClick={() => address.set({ engine: null })}
            >
              All <ChipCount>{all.databases.length}</ChipCount>
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
          </ChipStrip>
        )}
        <WiringLegend className="sm:ml-auto" />
      </div>

      <div className="grid min-w-0 items-start gap-8 2xl:grid-cols-[minmax(0,1fr)_24rem] [&>*]:min-w-0">
        {split.databases.length > 0 ? (
          <Wiring topology={topology.data} keep={keep} engineOf={engineOfNode} />
        ) : (
          <EmptyState
            title="No database of that engine"
            description="The engine the map is narrowed to has no database on it any more."
            action={
              <Button size="sm" variant="outline" onClick={() => address.set({ engine: null })}>
                Show every engine
              </Button>
            }
          />
        )}

        <Panel plain>
          <PanelHeader
            title="What reads them"
            actions={
              <span className="numeric text-hint text-muted-foreground">
                {plural(readers.length, "reader")}
              </span>
            }
          />
          <PanelBody flush>
            {readers.length === 0 ? (
              <EmptyNote className="px-0 text-left">
                Nothing has been seen reading these yet. A deployment that links a database, a
                container whose environment names one, or an open session appears here.
              </EmptyNote>
            ) : (
              <RowList>
                {readers.map((node) => {
                  const sources = split.fedBy.get(node.id) ?? []
                  const worst = worstStatus(sources.map((source) => source.edge))
                  const sessions = sources.reduce((sum, source) => sum + source.edge.sessions, 0)
                  return (
                    <Row
                      key={node.id}
                      href={node.href}
                      leading={<ConsumerMark node={node} worst={worst} size="sm" />}
                      title={
                        <>
                          {node.name}
                          <span className="ml-2 text-hint font-normal text-muted-foreground">
                            {consumerKind(node)}
                          </span>
                        </>
                      }
                      subtitle={sources
                        .map((source) => `${source.from.name} — ${describeEdge(source.edge)}`)
                        .join("; ")}
                      trailing={
                        edgeRank(worst) >= 2 || (node.status && node.kind !== "host") ? (
                          <Status
                            verdict={consumerVerdict(worst, node.status)}
                            label={consumerWord(worst, node.status)}
                          />
                        ) : (
                          <span className="numeric text-hint text-muted-foreground">
                            {plural(sessions, "session")}
                          </span>
                        )
                      }
                    />
                  )
                })}
              </RowList>
            )}
          </PanelBody>
        </Panel>
      </div>
    </Page>
  )
}
