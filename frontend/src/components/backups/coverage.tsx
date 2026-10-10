"use client"

import { useMemo } from "react"
import { relativeTime } from "@/lib/format"
import type {
  BackupResource,
  BackupResourceKind,
  BackupResourceReport,
  Container,
} from "@/lib/types"
import { Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Status } from "@/components/status-dot"
import { ChipStrip, FilterChip } from "@/components/tabs"
import { EmptyNote, LoadingRows } from "@/components/state"
import { FormNote } from "@/components/form"
import {
  RESOURCE_KIND_GROUP,
  RESOURCE_KIND_LABEL,
  RESOURCE_KIND_ORDER,
} from "@/components/backups/shared"
import { ResourceMark, resourceProducts } from "@/components/backups/marks"

export type CoverageFilter = "unprotected" | "all"

/**
 * What this server has and whether a backup covers it: every Docker volume,
 * compose stack, deployment, repository, saved database, the proxy's
 * configuration and the dashboard itself. Unprotected first, because that is
 * the list this block exists to empty.
 *
 * Every one of them is taken rather than read (§16): a thing a job covers
 * opens that job, and a thing nothing covers opens the form already filled in
 * for it — its paths, its SQLite file, its native dump, the containers to
 * pause. So they are lit cards, two to a row where there is the width, rather
 * than rows with an outline "Back up" at their far end that was the only part
 * of a 1,100px row that did anything.
 *
 * The filter and the kind live on the page, because the protection picture
 * above narrows the list to a kind when its column is pressed.
 */
export function CoveragePanel({
  report,
  containers,
  loading,
  canCreate,
  filter,
  onFilter,
  kind,
  onKind,
  onProtect,
}: {
  report: BackupResourceReport | undefined
  /** For the marks: a volume or a stack is drawn as the images its containers run. */
  containers: Container[]
  loading: boolean
  canCreate: boolean
  filter: CoverageFilter
  onFilter: (filter: CoverageFilter) => void
  kind: BackupResourceKind | "all"
  onKind: (kind: BackupResourceKind | "all") => void
  onProtect: (resource: BackupResource) => void
}) {
  const resources = useMemo(() => report?.resources ?? [], [report])
  const unprotected = resources.filter((r) => !r.protected)
  const paused = unprotected.filter((r) => r.coveredBy.some((c) => !c.enabled)).length
  const kinds = RESOURCE_KIND_ORDER.filter((k) => resources.some((r) => r.kind === k))
  const inFilter = (r: BackupResource) => filter === "all" || !r.protected
  const visible = resources.filter((r) => inFilter(r) && (kind === "all" || r.kind === kind))
  const unavailable = Object.entries(report?.unavailable ?? {})

  return (
    <Panel plain id="coverage" className="scroll-mt-6">
      <PanelHeader
        title="Coverage"
        actions={
          resources.length > 0 && (
            <CoverageMeter
              total={resources.length}
              protectedCount={resources.length - unprotected.length}
              paused={paused}
            />
          )
        }
      />
      {resources.length > 0 && (
        <PanelToolbar>
          <ChipStrip>
            <FilterChip selected={filter === "unprotected"} onClick={() => onFilter("unprotected")}>
              Not backed up
              <span className="numeric text-warning">{unprotected.length}</span>
            </FilterChip>
            <FilterChip selected={filter === "all"} onClick={() => onFilter("all")}>
              Everything
              <span className="numeric text-muted-foreground">{resources.length}</span>
            </FilterChip>
            {kinds.length > 1 && (
              <>
                <span className="mx-1 h-4 shrink-0 border-l border-hairline" aria-hidden />
                <FilterChip selected={kind === "all"} onClick={() => onKind("all")}>
                  All kinds
                </FilterChip>
                {kinds.map((k) => (
                  <FilterChip key={k} selected={kind === k} onClick={() => onKind(k)}>
                    {RESOURCE_KIND_GROUP[k]}
                    <span className="numeric text-muted-foreground">
                      {resources.filter((r) => r.kind === k && inFilter(r)).length}
                    </span>
                  </FilterChip>
                ))}
              </>
            )}
          </ChipStrip>
        </PanelToolbar>
      )}
      <PanelBody flush className="pt-3">
        {loading && !report && <LoadingRows rows={4} />}
        {report && visible.length === 0 && (
          <EmptyNote>
            {filter === "unprotected" && unprotected.length === 0
              ? "Everything the dashboard knows about is covered by an enabled job."
              : "Nothing matches these filters."}
          </EmptyNote>
        )}
        {visible.length > 0 && (
          <ChoiceList className="grid gap-2 space-y-0 xl:grid-cols-2">
            {visible.map((res, index) => (
              <CoverageCard
                key={`${res.kind}:${res.id}`}
                resource={res}
                index={index}
                ids={resourceProducts(res, containers, resources)}
                canCreate={canCreate}
                onProtect={() => onProtect(res)}
              />
            ))}
          </ChoiceList>
        )}
        {unavailable.length > 0 && (
          <FormNote className="pt-3">
            {unavailable.map(([owner, reason]) => `${owner}: ${reason}`).join(" · ")}
          </FormNote>
        )}
      </PanelBody>
    </Panel>
  )
}

/**
 * One thing on the server, drawn as its product, with the job that covers it
 * — named, with when it last took it — or the word for nothing covering it.
 */
function CoverageCard({
  resource,
  ids,
  index,
  canCreate,
  onProtect,
}: {
  resource: BackupResource
  ids: string[]
  index: number
  canCreate: boolean
  onProtect: () => void
}) {
  const covering = resource.coveredBy.find((c) => c.enabled)
  const paused = resource.coveredBy.find((c) => !c.enabled)
  const job = covering ?? paused
  const path = resource.paths?.[0]
  return (
    <ChoiceRow
      index={index}
      leading={<ResourceMark kind={resource.kind} ids={ids} ring="ring-choice-surface" />}
      title={resource.name}
      verb={job ? `${resource.name}: open ${job.jobName}` : `Back up ${resource.name}`}
      href={job ? `/backups/${job.jobId}` : undefined}
      onSelect={job ? undefined : onProtect}
      disabled={!job && !canCreate}
      description={
        <>
          {RESOURCE_KIND_LABEL[resource.kind]}
          {(path ?? resource.detail) && (
            <>
              {" · "}
              <span className={path ? "font-mono" : undefined}>{path ?? resource.detail}</span>
            </>
          )}
        </>
      }
      trailing={
        covering ? (
          <Status
            state="success"
            label={`${covering.jobName} · ${resource.lastBackupAt ? relativeTime(resource.lastBackupAt) : "no run yet"}`}
          />
        ) : paused ? (
          <Status tone="warning" label={`${paused.jobName} is paused`} />
        ) : (
          <Status tone="warning" label="not backed up" />
        )
      }
    />
  )
}

/**
 * How much of the server is covered, as a bar split the way the list is: what
 * an enabled job protects in green, what only a paused job covers in amber,
 * and the rest the empty track — the list below exists to fill that track.
 */
function CoverageMeter({
  total,
  protectedCount,
  paused,
}: {
  total: number
  protectedCount: number
  paused: number
}) {
  return (
    <span className="flex items-center gap-3">
      <span
        aria-hidden
        className="flex h-1.5 w-32 overflow-hidden rounded-full bg-meter-track sm:w-48"
      >
        <span
          className="bg-success transition-[width] duration-500"
          style={{ width: `${(protectedCount / total) * 100}%` }}
        />
        <span
          className="bg-warning transition-[width] duration-500"
          style={{ width: `${(paused / total) * 100}%` }}
        />
      </span>
      <span className="numeric text-hint text-muted-foreground">
        <span className="font-medium text-foreground">{protectedCount}</span> of {total} protected
      </span>
    </span>
  )
}
