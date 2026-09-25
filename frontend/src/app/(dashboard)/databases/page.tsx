"use client"

import { useMemo, useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { useSessionState } from "@/lib/view-state"
import { ArrowRight, Database, Key, Linked, Plus } from "@/components/icons"
import { get } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import type { DbFleet, DbTopology } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { Page, SearchInput, Section } from "@/components/page"
import { ChipStrip, ChipCount, FilterChip } from "@/components/tabs"
import { ChoiceCard, ChoiceGrid } from "@/components/choice-card"
import { FindingList, type Finding } from "@/components/finding-list"
import { EmptyState, ErrorState, LoadingPanel } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { ProductGlyphs, ProductLogo } from "@/components/product-logo"
import { useDatabase } from "@/components/database/db-context"
import { FleetCard, engineLabel } from "@/components/database/fleet-card"
import { DatabaseTopology } from "@/components/database/topology"
import {
  fleetFinding,
  fleetReadings,
  matchesFilter,
  matchesQuery,
  sortFleet,
  type FleetFilter,
} from "@/components/database/fleet"

/**
 * The control center: every database this dashboard knows, at a glance.
 *
 * The section used to open on the first connection's Browse tab — a table
 * rail and an empty grid saying "pick a table" — which answered none of the
 * questions somebody arriving at *Databases* has: are they all up, which one
 * grew, what is talking to which, and is anything running here that is not
 * connected yet. This is a reading page (§15) built from those questions, in
 * the order they are asked: the databases first, as cards you open — each
 * drawn as its engine with its own readings on it — then what needs a hand,
 * then the servers found on this machine that are not connected yet, and the
 * map of what they feed, in the picture the Topology page draws in full.
 *
 * It opened on five readings — databases, reachable, stored, sessions,
 * feeding — and dropped them (§15 pass 2, the `/git` exit): the count is on
 * the *All* chip and the engines are drawn beside the title, whether each
 * answers is the word on its card, and what each takes and carries is the
 * strip of figures on its card. A total over seven databases is a number
 * nobody came for, and it stood between the reader and the cards.
 *
 * Everything on it comes from two reads: `/databases/fleet`, which dials every
 * connection at once, and `/databases/topology`. The connection strip the
 * other pages carry is not drawn here because there is no *one* connection —
 * pressing a card is what chooses one, and it lands on that database's own
 * overview.
 */
export default function DatabasesOverviewPage() {
  const { can } = useAuth()
  const router = useRouter()
  const { drivers, openNew, openConnect, connectHost } = useDatabase()
  const admin = can("system.admin")
  const fleet = usePoll((signal) => get<DbFleet>("/databases/fleet", undefined, signal), 30_000)
  const topology = usePoll(
    (signal) => get<DbTopology>("/databases/topology", undefined, signal),
    45_000,
  )
  const [filter, setFilter] = useSessionState<FleetFilter>("databases.overview.filter", "all")
  const [query, setQuery] = useState("")

  const readings = useMemo(() => fleetReadings(fleet.data), [fleet.data])
  const ordered = useMemo(() => sortFleet(fleet.data?.connections ?? []), [fleet.data])
  const shown = ordered.filter((e) => matchesFilter(e, filter) && matchesQuery(e, query))
  const counts = useMemo(() => {
    const list = fleet.data?.connections ?? []
    const count = (f: FleetFilter) => list.filter((e) => matchesFilter(e, f)).length
    return {
      all: list.length,
      attention: count("attention"),
      docker: count("docker"),
      host: count("host"),
      remote: count("remote"),
    }
  }, [fleet.data])

  const findings = useMemo<Finding[]>(() => {
    const out: Finding[] = []
    for (const entry of fleet.data?.connections ?? []) {
      const finding = fleetFinding(entry, (path) => router.push(path))
      if (finding) out.push({ ...finding, meta: engineLabel(entry.driver) })
    }
    for (const server of fleet.data?.unreachable ?? []) {
      out.push({
        id: `unreachable-${server.container}`,
        level: "warning",
        title: `${server.container} is running but cannot be reached`,
        detail: server.reason,
        advice:
          "Publish its port, or attach it to a network this dashboard can reach, and it connects itself.",
        meta: engineLabel(server.driver),
      })
    }
    return out
  }, [fleet.data, router])

  if (fleet.error && !fleet.data) {
    return (
      <Page>
        <ErrorState error={fleet.error} />
      </Page>
    )
  }
  if (!fleet.data) {
    return (
      <Page>
        <LoadingPanel />
      </Page>
    )
  }

  const waiting = fleet.data.needsCredentials
  const nothing = fleet.data.connections.length === 0

  return (
    <Page className="animate-rise">
      <Section
        title={
          <span className="flex items-center gap-2">
            Databases
            {readings.engines.length > 0 && <ProductGlyphs ids={readings.engines} />}
          </span>
        }
        actions={
          admin && (
            <div className="flex items-center gap-2">
              {openConnect && (
                <Button size="sm" variant="outline" onClick={openConnect}>
                  <Linked className="size-4" />
                  Connect elsewhere
                </Button>
              )}
              {openNew && (
                <Button size="sm" onClick={openNew}>
                  <Plus className="size-4" />
                  New database
                </Button>
              )}
            </div>
          )
        }
      >
        {nothing ? (
          <EmptyState
            icon={Database}
            title="No databases yet"
            description="Anything running on this server connects itself. New database starts one here — it takes a free port, generates its own password and appears above — or point the dashboard at a database somewhere else."
            action={
              admin &&
              openNew && (
                <Button size="sm" onClick={openNew}>
                  <Plus className="size-4" />
                  New database
                </Button>
              )
            }
          />
        ) : (
          <>
            <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2">
              <SearchInput
                dense
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder="Filter databases…"
                aria-label="Filter databases"
              />
              <ChipStrip>
                {(
                  [
                    ["all", "All"],
                    ["attention", "Needs attention"],
                    ["docker", "Containers"],
                    ["host", "On this server"],
                    ["remote", "Elsewhere"],
                  ] as [FleetFilter, string][]
                ).map(([key, label]) =>
                  key === "all" || counts[key] > 0 ? (
                    <FilterChip key={key} selected={filter === key} onClick={() => setFilter(key)}>
                      {label}
                      <ChipCount>{counts[key]}</ChipCount>
                    </FilterChip>
                  ) : null,
                )}
              </ChipStrip>
              <span className="ml-auto text-hint text-muted-foreground">
                checked {relativeTime(fleet.data.checkedAt)}
              </span>
            </div>
            {shown.length === 0 ? (
              <EmptyState
                icon={Database}
                title="No database matches"
                description="Nothing in this list matches the filter and the words above."
              />
            ) : (
              <ChoiceGrid columns={3}>
                {shown.map((entry, index) => (
                  <FleetCard
                    key={entry.id}
                    entry={entry}
                    index={index}
                    href={`/databases/overview?conn=${entry.id}`}
                  />
                ))}
              </ChoiceGrid>
            )}
          </>
        )}
      </Section>

      {(findings.length > 0 || waiting.length > 0) && (
        <Section title="Needs attention">
          {findings.length > 0 && <FindingList findings={findings} />}
          {waiting.length > 0 && (
            <ChoiceGrid columns="fill">
              {waiting.map((server, index) => (
                <ChoiceCard
                  key={`${server.host}:${server.port}`}
                  index={index}
                  verb={`Connect ${server.name}`}
                  onClick={admin && connectHost ? () => connectHost(server) : undefined}
                  disabled={!admin}
                  logo={<ProductLogo id={server.driver} size="md" fallback={Database} />}
                  title={server.name}
                  description={`Found listening on ${server.host}:${server.port}${server.process ? ` (${server.process})` : ""}. It is installed on the machine, not in a container, so its password has to be given or made.`}
                  trailing={
                    <span className="flex items-center gap-1.5 text-hint text-muted-foreground">
                      <Key className="size-3" />
                      waiting for a password
                    </span>
                  }
                />
              ))}
            </ChoiceGrid>
          )}
        </Section>
      )}

      {!nothing && (
        <Section
          title="What they feed"
          actions={
            <Link
              href="/databases/topology"
              className="flex items-center gap-1 text-body text-muted-foreground hover:text-foreground"
            >
              Open the map <ArrowRight className="size-3.5" />
            </Link>
          }
        >
          {topology.data ? (
            <DatabaseTopology topology={topology.data} compact />
          ) : (
            <Skeleton className="h-40 rounded-xl" />
          )}
        </Section>
      )}
      {/* The driver catalogue is read by the layout for the pages below; here
          it only says which engines this dashboard can speak to at all. */}
      <p className="sr-only">{drivers.map((d) => d.label).join(", ")}</p>
    </Page>
  )
}
