"use client"

import { useMemo } from "react"
import { get } from "@/lib/api"
import { plural, relativeTime } from "@/lib/format"
import type { DbTopology } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Page, Section } from "@/components/page"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { ErrorState, LoadingPanel } from "@/components/state"
import { DatabaseTopology } from "@/components/database/topology"
import { edgeRank, splitTopology } from "@/components/database/fleet"

/**
 * The map of what feeds what, in full.
 *
 * A database is only ever half of a system, and the other half — the
 * deployment that reads it, the container that was handed its address, the
 * process on this machine holding a session open — is what breaks when the
 * database does. This page draws that: readings first (how many are fed,
 * how many links are live, which are broken), then the picture, with light
 * travelling along every link something is using.
 */
export default function TopologyPage() {
  const topology = usePoll(
    (signal) => get<DbTopology>("/databases/topology", undefined, signal),
    20_000,
  )
  const readings = useMemo(() => {
    const t = topology.data
    const { databases, consumers } = splitTopology(t)
    const edges = t?.edges ?? []
    const live = edges.filter((e) => e.sessions > 0).length
    const broken = edges.filter((e) => edgeRank(e.status) >= 2).length
    const sessions = edges.reduce((sum, e) => sum + e.sessions, 0)
    return { databases: databases.length, consumers: consumers.length, links: edges.length, live, broken, sessions }
  }, [topology.data])

  if (topology.error && !topology.data) {
    return (
      <Page>
        <ErrorState error={topology.error} />
      </Page>
    )
  }
  if (!topology.data) {
    return (
      <Page>
        <LoadingPanel />
      </Page>
    )
  }

  return (
    <Page className="animate-rise">
      <StatGrid columns={4} dense>
        <StatTile label="Databases" value={readings.databases.toLocaleString()} hint="on the map" />
        <StatTile
          label="Fed"
          value={readings.consumers.toLocaleString()}
          hint="deployments, containers and machines"
        />
        <StatTile
          label="Links"
          value={readings.links.toLocaleString()}
          tone={readings.broken > 0 ? "warning" : "default"}
          hint={
            readings.broken > 0
              ? `${plural(readings.broken, "link")} broken or stale`
              : `${readings.live} carrying sessions`
          }
        />
        <StatTile
          label="Sessions"
          value={readings.sessions.toLocaleString()}
          hint={`open now · checked ${relativeTime(topology.data.checkedAt)}`}
        />
      </StatGrid>
      <Section title="Databases and what reads them">
        <DatabaseTopology topology={topology.data} />
      </Section>
    </Page>
  )
}
