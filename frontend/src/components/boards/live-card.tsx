"use client"

import { createContext, useCallback, useContext, useEffect, useState } from "react"
import { ArrowRight } from "@/components/icons"
import { get } from "@/lib/api"
import type { BoardResource } from "@/lib/boards"
import { duration, percent } from "@/lib/format"
import type { DbFleet, DeploymentFleet, HostInfo, Snapshot } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { engineFor } from "@/components/database/engine"
import { lastFleet, readFleet } from "@/components/database/fleet/read"
import { EngineGlyph } from "@/components/database/kit/engine-mark"
import { ProjectMark } from "@/components/deploy/project-mark"
import { hostOf, ProjectStatus } from "@/components/deploy/vocabulary"
import { LogoGlyph } from "@/components/logo"
import { Status } from "@/components/status-dot"

type Kind = BoardResource["kind"]
type Live = {
  host?: HostInfo
  metrics?: Snapshot
  fleet?: DeploymentFleet
  databases?: DbFleet
  /** A card says what it shows, so only the reads some card needs are made. */
  want: (kind: Kind) => () => void
  open: (href: string) => void
}

const LiveContext = createContext<Live | null>(null)

/**
 * The reads behind a drawing's live cards, made once for all of them and only
 * while one is on the canvas: a drawing of twenty projects is one fleet read
 * every fifteen seconds, not twenty.
 */
export function LiveCardProvider({
  open,
  children,
}: {
  open: (href: string) => void
  children: React.ReactNode
}) {
  const [wanted, setWanted] = useState<Record<Kind, number>>({ server: 0, project: 0, database: 0 })
  const want = useCallback((kind: Kind) => {
    setWanted((counts) => ({ ...counts, [kind]: counts[kind] + 1 }))
    return () => setWanted((counts) => ({ ...counts, [kind]: counts[kind] - 1 }))
  }, [])
  const host = usePoll((signal) => get<HostInfo>("/system/host", undefined, signal), 0, [], {
    enabled: wanted.server > 0,
  })
  const metrics = usePoll(
    (signal) => get<Snapshot>("/system/metrics", undefined, signal),
    10_000,
    [],
    { enabled: wanted.server > 0 },
  )
  const fleet = usePoll(
    (signal) => get<DeploymentFleet>("/deploy/", { view: "fleet" }, signal),
    15_000,
    [],
    { enabled: wanted.project > 0 },
  )
  const databases = usePoll(readFleet, 30_000, [], {
    enabled: wanted.database > 0,
    seed: lastFleet(),
  })
  return (
    <LiveContext.Provider
      value={{
        host: host.data,
        metrics: metrics.data,
        fleet: fleet.data,
        databases: databases.data,
        want,
        open,
      }}
    >
      {children}
    </LiveContext.Provider>
  )
}

/**
 * A card for something on this server, drawn by Excalidraw as an embed
 * (`renderEmbeddable`). The rectangle cards placed before these said the
 * name and state the thing had when it was placed; this one reads them now.
 */
export function LiveCard({ resource, href }: { resource: BoardResource; href: string }) {
  const live = useContext(LiveContext)
  const want = live?.want
  useEffect(() => want?.(resource.kind), [want, resource.kind])
  if (!live) return null

  let mark: React.ReactNode = null
  let title = ""
  let line: React.ReactNode = null
  if (resource.kind === "server") {
    mark = <LogoGlyph className="h-5 w-auto shrink-0 text-brand" />
    title = live.host?.hostname ?? "This server"
    line = live.metrics ? (
      <span className="numeric truncate text-muted-foreground">
        CPU {percent(live.metrics.cpu.totalPercent, 0)} · memory{" "}
        {percent(live.metrics.memory.usedPercent, 0)} · up {duration(live.metrics.uptimeSeconds)}
      </span>
    ) : null
  } else if (resource.kind === "project") {
    const project = live.fleet?.deployments.find((d) => d.id === resource.id)
    title = project?.name ?? "Project"
    if (project) {
      mark = <ProjectMark deployment={project} size="sm" />
      line = (
        <span className="flex min-w-0 items-center gap-2">
          <ProjectStatus summary={project} live />
          {project.endpoint && (
            <span className="truncate font-mono text-muted-foreground">
              {hostOf(project.endpoint)}
            </span>
          )}
        </span>
      )
    } else if (live.fleet) line = <Gone />
  } else {
    const database = live.databases?.connections.find((c) => c.id === resource.id)
    title = database?.name ?? "Database"
    if (database) {
      mark = <EngineGlyph engine={engineFor(database)} className="size-5" />
      line = (
        <span className="flex min-w-0 items-center gap-2">
          <Status
            tone={database.ok ? "running" : database.state === "stopped" ? "stopped" : "danger"}
            label={
              database.ok ? "Answering" : database.state === "stopped" ? "Stopped" : "Not answering"
            }
          />
          {database.versionNumber && (
            <span className="truncate text-muted-foreground">{database.versionNumber}</span>
          )}
        </span>
      )
    } else if (live.databases) line = <Gone />
  }

  return (
    <div className="flex size-full min-w-0 flex-col justify-between gap-2 rounded-[var(--embeddable-radius)] border border-hairline bg-card p-3 font-sans text-foreground">
      <div className="flex min-w-0 items-center gap-2">
        {mark}
        <span className="truncate text-body font-semibold">{title}</span>
      </div>
      <div className="flex min-w-0 items-center justify-between gap-2 text-hint">
        <span className="min-w-0 flex-1">{line}</span>
        <button
          type="button"
          onClick={() => live.open(href)}
          className="flex shrink-0 items-center gap-1 rounded-md px-1.5 py-0.5 text-muted-foreground focus-ring transition-colors hover:bg-control-hover hover:text-foreground"
        >
          Open <ArrowRight className="size-3" />
        </button>
      </div>
    </div>
  )
}

function Gone() {
  return <span className="text-muted-foreground">No longer on this server</span>
}
