"use client"

import { useMemo } from "react"
import { useSessionState } from "@/lib/view-state"
import { Box, CloudUpload, Database, GitBranch, Globe, Layers, SettingsGear } from "@/components/icons"
import { relativeTime } from "@/lib/format"
import type { BackupResource, BackupResourceKind, BackupResourceReport } from "@/lib/types"
import { Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { Status } from "@/components/status-dot"
import { FilterChip } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { EmptyNote, LoadingRows } from "@/components/state"
import { FormNote } from "@/components/form"
import { Button } from "@/components/ui/button"
import { RESOURCE_KIND_LABEL } from "@/components/backups/shared"

/**
 * The wayfinding glyph for each kind of thing — the same mark its own page
 * carries in the sidebar, so "this is a volume" is read without reading.
 */
const KIND_ICON: Record<BackupResourceKind, React.ComponentType<{ className?: string }>> = {
  dashboard: SettingsGear,
  proxy: Globe,
  volume: Box,
  stack: Layers,
  deployment: CloudUpload,
  repository: GitBranch,
  database: Database,
}

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
  loading,
  canCreate,
  onProtect,
  onOpenJob,
}: {
  report: BackupResourceReport | undefined
  loading: boolean
  canCreate: boolean
  onProtect: (resource: BackupResource) => void
  onOpenJob: (jobId: number) => void
}) {
  const resources = useMemo(() => report?.resources ?? [], [report])
  const unprotected = resources.filter((r) => !r.protected)
  const [filter, setFilter] = useSessionState<Filter>("backups.coverage.filter", "unprotected")
  const [kind, setKind] = useSessionState<BackupResourceKind | "all">(
    "backups.coverage.kind",
    "all",
  )
  const kinds = useMemo(
    () => [...new Set(resources.map((r) => r.kind))].sort(),
    [resources],
  )
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
            <span className="numeric text-hint text-muted-foreground">
              {resources.length - unprotected.length} of {resources.length} protected
            </span>
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
              const Icon = KIND_ICON[res.kind]
              const covering = res.coveredBy.filter((c) => c.enabled)
              const paused = res.coveredBy.filter((c) => !c.enabled)
              return (
                <Row
                  key={`${res.kind}:${res.id}`}
                  leading={<Icon aria-hidden className="size-3.5 text-muted-foreground" />}
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
