"use client"

import { useMemo } from "react"
import { get } from "@/lib/api"
import { plural, relativeTime } from "@/lib/format"
import type { DbTopology } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Page, Section } from "@/components/page"
import { ErrorState, LoadingPanel } from "@/components/state"
import { DatabaseTopology, TopologyLegend } from "@/components/database/topology"
import { edgeRank } from "@/components/database/fleet"

/**
 * The map of what feeds what, in full.
 *
 * A database is only ever half of a system, and the other half — the
 * deployment that reads it, the container that was handed its address, the
 * process on this machine holding a session open — is what breaks when the
 * database does. This page draws that, with light travelling along every link
 * something is using.
 *
 * It opened on four readings — databases, fed, links, sessions — and dropped
 * them (§15 pass 2, the `/git` exit): the two counts are the heads of the
 * map's own lanes, and the links and the sessions they carry are the line in
 * the section's corner, beside when the picture was last checked. A figure
 * that says what the picture under it already says is a row of numbers the
 * reader scrolls past to reach the picture.
 */
export default function TopologyPage() {
  const topology = usePoll(
    (signal) => get<DbTopology>("/databases/topology", undefined, signal),
    20_000,
  )
  const readings = useMemo(() => {
    const edges = topology.data?.edges ?? []
    const live = edges.filter((e) => e.sessions > 0).length
    const broken = edges.filter((e) => edgeRank(e.status) >= 2).length
    return { links: edges.length, live, broken }
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
      <Section
        title="Databases and what reads them"
        actions={
          <span className="numeric text-hint text-muted-foreground">
            {plural(readings.links, "link")} · {readings.live} carrying sessions
            {readings.broken > 0 && (
              <span className="text-warning"> · {readings.broken} broken or stale</span>
            )}
            {" · checked "}
            {relativeTime(topology.data.checkedAt)}
          </span>
        }
      >
        <DatabaseTopology topology={topology.data} />
        <TopologyLegend />
      </Section>
    </Page>
  )
}
