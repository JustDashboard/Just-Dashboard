"use client"

import { useMemo } from "react"
import { useSessionState } from "@/lib/view-state"
import { relativeTime } from "@/lib/format"
import type {
  BackupResource,
  BackupResourceKind,
  BackupResourceReport,
  Container,
} from "@/lib/types"
import { Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { Status } from "@/components/status-dot"
import { FilterChip } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { EmptyNote, LoadingRows } from "@/components/state"
import { FormNote } from "@/components/form"
import { Button } from "@/components/ui/button"
import { RESOURCE_KIND_LABEL } from "@/components/backups/shared"
import { ResourceMark, resourceProducts } from "@/components/backups/marks"

type Filter = "unprotected" | "all"

/**
 * What this server has and whether a backup covers it: every Docker volume,
 * compose stack, deployment, repository, saved database, the proxy's
 * configuration and the dashboard itself, each with the job that protects
 * it or a button that writes one. Unprotected first, because that is the
 * list this block exists to empty.
 */
export function CoveragePanel({
  report,
  containers,
  loading,
  canCreate,
  onProtect,
  onOpenJob,
}: {
  report: BackupResourceReport | undefined
  /** For the marks: a volume or a stack is drawn as the images its containers run. */
  containers: Container[]
  loading: boolean
  canCreate: boolean
  onProtect: (resource: BackupResource) => void
  onOpenJob: (jobId: number) => void
}) {
  const resources = useMemo(() => report?.resources ?? [], [report])
  const unprotected = resources.filter((r) => !r.protected)
  const paused = unprotected.filter((r) => r.coveredBy.some((c) => !c.enabled)).length
  const [filter, setFilter] = useSessionState<Filter>("backups.coverage.filter", "unprotected")
  const [kind, setKind] = useSessionState<BackupResourceKind | "all">(
    "backups.coverage.kind",
    "all",
  )
  const kinds = useMemo(() => [...new Set(resources.map((r) => r.kind))].sort(), [resources])
  const visible = resources.filter(
    (r) => (filter === "all" || !r.protected) && (kind === "all" || r.kind === kind),
  )
  const unavailable = Object.entries(report?.unavailable ?? {})

  return (
    <Panel plain>
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
          <FilterChip selected={filter === "unprotected"} onClick={() => setFilter("unprotected")}>
            Not backed up
            <span className="numeric text-muted-foreground">{unprotected.length}</span>
          </FilterChip>
          <FilterChip selected={filter === "all"} onClick={() => setFilter("all")}>
            Everything
            <span className="numeric text-muted-foreground">{resources.length}</span>
          </FilterChip>
          {kinds.length > 1 && (
            <>
              <span className="mx-1 h-4 border-l border-hairline" aria-hidden />
              <FilterChip selected={kind === "all"} onClick={() => setKind("all")}>
                All kinds
              </FilterChip>
              {kinds.map((k) => (
                <FilterChip key={k} selected={kind === k} onClick={() => setKind(k)}>
                  {RESOURCE_KIND_LABEL[k]}
                </FilterChip>
              ))}
            </>
          )}
        </PanelToolbar>
      )}
      <PanelBody flush className="py-1">
        {loading && !report && <LoadingRows rows={4} />}
        {report && visible.length === 0 && (
          <EmptyNote>
            {filter === "unprotected" && unprotected.length === 0
              ? "Everything the dashboard knows about is covered by an enabled job."
              : "Nothing matches these filters."}
          </EmptyNote>
        )}
        {visible.length > 0 && (
          <RowList className="animate-rise">
            {visible.map((res) => {
              const covering = res.coveredBy.filter((c) => c.enabled)
              const paused = res.coveredBy.filter((c) => !c.enabled)
              return (
                <Row
                  key={`${res.kind}:${res.id}`}
                  leading={
                    <ResourceMark
                      kind={res.kind}
                      ids={resourceProducts(res, containers, resources)}
                    />
                  }
                  title={
                    <span className="flex min-w-0 items-center gap-2">
                      <span className="truncate">{res.name}</span>
                      <Tag>{RESOURCE_KIND_LABEL[res.kind]}</Tag>
                    </span>
                  }
                  subtitle={res.paths?.[0] ?? res.detail}
                  mono={Boolean(res.paths?.[0])}
                  trailing={
                    <>
                      {res.protected ? (
                        <button
                          type="button"
                          className="flex items-center gap-1.5 text-xs hover:underline"
                          onClick={() => onOpenJob(covering[0].jobId)}
                        >
                          <Status
                            state="success"
                            label={
                              res.lastBackupAt
                                ? `${covering[0].jobName} · ${relativeTime(res.lastBackupAt)}`
                                : `${covering[0].jobName} · no run yet`
                            }
                          />
                        </button>
                      ) : paused.length > 0 ? (
                        <button
                          type="button"
                          className="flex items-center gap-1.5 text-xs hover:underline"
                          onClick={() => onOpenJob(paused[0].jobId)}
                        >
                          <Status tone="warning" label={`${paused[0].jobName} is paused`} />
                        </button>
                      ) : (
                        <Status tone="warning" label="not backed up" />
                      )}
                      {canCreate && !res.protected && paused.length === 0 && (
                        <Button size="xs" variant="outline" onClick={() => onProtect(res)}>
                          Back up
                        </Button>
                      )}
                    </>
                  }
                />
              )
            })}
          </RowList>
        )}
        {unavailable.length > 0 && (
          <FormNote className="pt-2">
            {unavailable.map(([owner, reason]) => `${owner}: ${reason}`).join(" · ")}
          </FormNote>
        )}
      </PanelBody>
    </Panel>
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
        <span className="bg-success" style={{ width: `${(protectedCount / total) * 100}%` }} />
        <span className="bg-warning" style={{ width: `${(paused / total) * 100}%` }} />
      </span>
      <span className="numeric text-hint text-muted-foreground">
        <span className="font-medium text-foreground">{protectedCount}</span> of {total} protected
      </span>
    </span>
  )
}
