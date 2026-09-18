"use client"

import { Tag } from "@/components/tag"
import { useCallback, useMemo, useState } from "react"
import { useRouter } from "next/navigation"
import Link from "next/link"
import { ArrowRight, External, GitBranch } from "@/components/icons"
import { get } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { cn } from "@/lib/utils"
import type {
  ContainerStats,
  DeploymentDiagnosis,
  DeploymentGitWatch,
  DeploymentPreview,
} from "@/lib/types"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { EmptyNote, Notice } from "@/components/state"
import { FindingList, type Finding } from "@/components/finding-list"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Status, StatusDot, type DotTone } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { useProject } from "@/components/deploy/project-context"
import {
  HealthStatus,
  RunStatus,
  deploymentURL,
  formatDuration,
  hostOf,
  runDurationSeconds,
  runSubject,
  runTitle,
  sourceLine,
} from "@/components/deploy/vocabulary"
import { SitePreview } from "@/components/deploy/site-preview"
import { RollbackDialog } from "@/components/deploy/rollback-dialog"
import { CertificateStatus } from "@/components/deploy/project-runtime"

/**
 * The project's front page: a window onto the live site, the facts a visitor
 * asks first, what needs attention, and the last few deployments and the
 * usage they produced. Findings live here rather than on Runtime — Runtime is
 * evidence, this is the verdict.
 */
export function ProjectOverview() {
  const project = useProject()
  const router = useRouter()
  const { can } = useAuth()
  const { deployment, project: record, runtime } = project.detail
  const [rollbackOpen, setRollbackOpen] = useState(false)

  const url = deploymentURL(deployment.endpoint)
  const source = sourceLine(deployment, record, project.liveRun ?? deployment.lastRun)
  const opsDomains =
    project.operations?.domains.status === "available"
      ? project.operations.domains.domains
      : undefined

  const watch = usePoll(
    (signal) =>
      get<DeploymentGitWatch>(
        `/deploy/${project.projectId}/environments/${project.environmentId}/git-watch`,
        undefined,
        signal,
      ),
    15000,
    [project.projectId, project.environmentId],
    {
      enabled:
        project.normalized &&
        deployment.sourceKind === "git" &&
        project.environmentId > 0 &&
        !project.archived,
    },
  )

  const previews = usePoll(
    (signal) =>
      get<DeploymentPreview[]>(`/deploy/${project.projectId}/previews`, undefined, signal),
    30000,
    [project.projectId],
    { enabled: project.normalized && !project.archived },
  )

  const findings = useFindings(project.operations?.diagnosis, router)
  const recentRuns = project.runs
    .filter((run) => run.environmentId === project.environmentId)
    .slice(0, 5)
  const liveService =
    runtime?.status === "available"
      ? (runtime.services.find((service) => service.liveRelease) ?? runtime.services[0])
      : undefined
  const buildLogsRun = deployment.activeRun ?? project.liveRun
  const rollbackEligible = project.releases.some(
    (release) => release.state === "retained" && release.id !== deployment.liveReleaseId,
  )
  const domainHostnames = useMemo(() => {
    if (opsDomains) return opsDomains.map((domain) => domain.hostname)
    const host = url && hostOf(url)
    return host ? [host] : []
  }, [opsDomains, url])

  return (
    <div className="animate-rise space-y-8">
      {deployment.activeRun && (
        <Notice title="A deployment is in progress">
          <Link
            className="inline-flex items-center gap-2 rounded-sm underline focus-ring"
            href={`/deploy/${project.projectId}/runs/${deployment.activeRun.id}`}
          >
            Follow the build <ArrowRight className="size-3.5" />
          </Link>
        </Notice>
      )}

      {/* The one framed block on the page: a window you look through (spec §1.1). */}
      <Panel plain>
        <PanelHeader
          title="Production"
          actions={
            <>
              {buildLogsRun && (
                <Button variant="outline" size="sm" asChild>
                  <Link href={`/deploy/${project.projectId}/runs/${buildLogsRun.id}`}>
                    Build logs
                  </Link>
                </Button>
              )}
              {rollbackEligible && can("destructive") && (
                <Button variant="outline" size="sm" onClick={() => setRollbackOpen(true)}>
                  Roll back
                </Button>
              )}
            </>
          }
        />
        <PanelBody
          flush
          className="grid items-start gap-8 pt-4 lg:grid-cols-[minmax(0,1.25fr)_minmax(0,1fr)]"
        >
          <SitePreview
            key={`${deployment.endpoint}:${deployment.liveReleaseId}`}
            deployment={deployment}
          />
          <div className="min-w-0 space-y-5">
            <div className="min-w-0">
              <p className="eyebrow mb-1.5">Domains</p>
              {opsDomains ? (
                opsDomains.length === 0 ? (
                  <p className="text-body text-muted-foreground">No public domain</p>
                ) : (
                  <ul className="space-y-1.5">
                    {opsDomains.map((domain) => (
                      <li key={domain.hostname} className="flex min-w-0 items-center gap-2">
                        <a
                          href={`${domain.https ? "https" : "http"}://${domain.hostname}/`}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="min-w-0 truncate rounded-sm text-body font-medium focus-ring hover:underline"
                        >
                          {domain.hostname}
                        </a>
                        <CertificateStatus domain={domain} />
                        {domain.protected && <Tag>Password</Tag>}
                        <External aria-hidden className="size-3 shrink-0 text-muted-foreground" />
                      </li>
                    ))}
                  </ul>
                )
              ) : url ? (
                <a
                  href={url}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="inline-flex min-w-0 items-center gap-1.5 rounded-sm text-body font-medium focus-ring hover:underline"
                >
                  {hostOf(url)}{" "}
                  <External aria-hidden className="size-3 shrink-0 text-muted-foreground" />
                </a>
              ) : (
                <p className="text-body text-muted-foreground">
                  {deployment.liveReleaseId
                    ? "Private service"
                    : "Appears after your first deployment"}
                </p>
              )}
            </div>

            <dl className="min-w-0 space-y-2.5">
              <Fact label="Status">
                <HealthStatus health={deployment.health} />
              </Fact>
              <Fact label="Source">
                <span className="flex min-w-0 items-center gap-1.5">
                  <GitBranch aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />
                  <span className="truncate">{source.primary}</span>
                </span>
                {source.secondary && (
                  <span
                    className={cn(
                      "mt-0.5 block truncate text-hint text-muted-foreground",
                      source.mono && "font-mono",
                    )}
                  >
                    {source.secondary}
                  </span>
                )}
              </Fact>
              <Fact label="Live release">
                {project.liveRelease ? (
                  <span className="truncate">
                    <span className="numeric">#{project.liveRelease.number}</span>
                    {project.liveRun && (
                      <>
                        {" · "}
                        {relativeTime(project.liveRun.endedAt ?? project.liveRun.requestedAt)}
                        {project.liveRun.actor && ` · by ${project.liveRun.actor}`}
                        {" · "}
                        {formatDuration(runDurationSeconds(project.liveRun))}
                      </>
                    )}
                  </span>
                ) : (
                  "Not deployed yet"
                )}
              </Fact>
              {deployment.sourceKind === "git" && (
                <Fact label="Auto-deploy">
                  <span className="flex flex-wrap items-center gap-x-3 gap-y-1">
                    {watch.data ? <AutoDeploy watch={watch.data} /> : "—"}
                    <Link
                      href={`/deploy/${project.projectId}/settings/general`}
                      className="inline-flex items-center gap-1 rounded-sm text-hint text-muted-foreground focus-ring hover:text-foreground"
                    >
                      Manage <ArrowRight className="size-3" />
                    </Link>
                  </span>
                </Fact>
              )}
            </dl>

            {project.liveRun?.operation === "rollback" && project.liveRelease && (
              <Notice
                tone="warning"
                title={`Rolled back to release #${project.liveRelease.number}${project.liveRun.actor ? ` by ${project.liveRun.actor}` : ""} ${relativeTime(project.liveRun.endedAt ?? project.liveRun.requestedAt)}`}
              />
            )}
          </div>
        </PanelBody>
      </Panel>

      {findings.length > 0 && (
        <Panel plain>
          <PanelHeader title="Needs attention" />
          <PanelBody>
            <FindingList findings={findings} />
          </PanelBody>
        </Panel>
      )}

      <div className="grid min-w-0 gap-8 xl:grid-cols-2">
        <Panel plain>
          <PanelHeader
            title="Recent deployments"
            actions={
              <Link
                href={`/deploy/${project.projectId}/deployments`}
                className="inline-flex items-center gap-1 rounded-sm text-hint font-medium text-muted-foreground focus-ring hover:text-foreground"
              >
                View all <ArrowRight className="size-3" />
              </Link>
            }
          />
          <PanelBody flush>
            {recentRuns.length ? (
              <RowList>
                {recentRuns.map((run) => (
                  <Row
                    key={run.id}
                    href={`/deploy/${project.projectId}/runs/${run.id}`}
                    leading={<RunStatus state={run.state} />}
                    title={
                      <span className="flex min-w-0 items-baseline gap-2">
                        <span>{runTitle(run)}</span>
                        {runSubject(run) && (
                          <span className="min-w-0 truncate font-normal text-muted-foreground">
                            {runSubject(run)}
                          </span>
                        )}
                      </span>
                    }
                    trailing={
                      <span className="numeric text-hint text-muted-foreground">
                        {relativeTime(run.requestedAt)}
                      </span>
                    }
                  />
                ))}
              </RowList>
            ) : (
              <EmptyNote className="px-0 py-5 text-left">
                Start your first deployment to see its build and release here.
              </EmptyNote>
            )}
          </PanelBody>
        </Panel>

        <Panel plain>
          <PanelHeader
            title="Resource usage"
            actions={
              <Link
                href={`/deploy/${project.projectId}/runtime`}
                className="inline-flex items-center gap-1 rounded-sm text-hint font-medium text-muted-foreground focus-ring hover:text-foreground"
              >
                Runtime <ArrowRight className="size-3" />
              </Link>
            }
          />
          <PanelBody>
            {liveService && runtime?.status === "available" ? (
              <UsageTiles
                key={liveService.containerId}
                containerId={liveService.containerId}
                name={liveService.name}
              />
            ) : (
              <EmptyNote className="px-0 py-5 text-left">
                {runtime?.reason || "Usage appears when your application starts."}
              </EmptyNote>
            )}
          </PanelBody>
        </Panel>
      </div>

      {previews.data && previews.data.length > 0 && (
        <Panel plain>
          <PanelHeader
            title="Preview environments"
            actions={
              <Link
                href={`/deploy/${project.projectId}/settings/automation`}
                className="inline-flex items-center gap-1 rounded-sm text-hint font-medium text-muted-foreground focus-ring hover:text-foreground"
              >
                Manage <ArrowRight className="size-3" />
              </Link>
            }
          />
          <PanelBody flush>
            <RowList aria-label="Preview environments">
              {previews.data.map((preview) => (
                <Row
                  key={preview.id}
                  title={preview.environmentSlug}
                  subtitle={`PR ${preview.providerRef} · updated ${relativeTime(preview.updatedAt)}`}
                  trailing={
                    preview.isolationStatus === "quarantined" ? (
                      <Status tone="danger" label="Quarantined" />
                    ) : (
                      <Status
                        tone={preview.state === "open" ? "running" : "stopped"}
                        label={preview.state === "open" ? "Open" : "Closed"}
                      />
                    )
                  }
                />
              ))}
            </RowList>
          </PanelBody>
        </Panel>
      )}

      <RollbackDialog
        open={rollbackOpen}
        onOpenChange={setRollbackOpen}
        projectId={project.projectId}
        environmentId={project.environmentId}
        liveRelease={project.liveRelease}
        releases={project.releases}
        runs={project.runs}
        domains={domainHostnames}
      />
    </div>
  )
}

/** One labelled fact in the overview's column: a small name over its value. */
function Fact({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="min-w-0">
      <dt className="text-hint text-muted-foreground">{label}</dt>
      <dd className="min-w-0 text-body">{children}</dd>
    </div>
  )
}

function autoDeployReading(watch: DeploymentGitWatch): { tone: DotTone; label: string } {
  const automatic = watch.policy?.automatic ?? watch.automatic
  if (["unavailable", "stale", "policy_conflict"].includes(watch.status)) {
    return { tone: "warning", label: "Needs attention" }
  }
  if (!automatic) return { tone: "stopped", label: "Manual deployments" }
  if (watch.status === "awaiting_first_deployment") {
    return { tone: "stopped", label: "On after first deployment" }
  }
  return { tone: "running", label: `On · every ${watch.intervalSeconds}s` }
}

function AutoDeploy({ watch }: { watch: DeploymentGitWatch }) {
  const reading = autoDeployReading(watch)
  return <Status tone={reading.tone} label={reading.label} />
}

/** CPU and memory from the live stats socket — the same reading Runtime shows, at a glance. */
function UsageTiles({ containerId, name }: { containerId: string; name: string }) {
  const [stats, setStats] = useState<ContainerStats | null>(null)
  const onMessage = useCallback((message: Envelope) => {
    if (message.type === "stats") setStats(message.data as ContainerStats)
  }, [])
  const socket = useSocket(`/docker/containers/${containerId}/stats/stream`, { onMessage })
  const live = socket.state === "open"
  return (
    <StatGrid columns={2}>
      <StatTile
        label="CPU"
        value={stats ? `${stats.cpuPercent.toFixed(1)}%` : "—"}
        hint={name}
        trailing={live && <StatusDot tone="running" live />}
      />
      <StatTile
        label="Memory"
        value={stats ? `${(stats.memUsage / 1024 / 1024).toFixed(0)} MiB` : "—"}
        hint={name}
      />
    </StatGrid>
  )
}

/**
 * `operations.diagnosis.findings` read through `FindingList`'s vocabulary:
 * severity is already the shared level, `measured` is the detail, `means` and
 * `action` join into the advice, and a `deepLink` becomes "Open {owner}".
 * Owners the diagnosis could not read at all are folded into one notice-level
 * row rather than counted as healthy.
 */
function useFindings(
  diagnosis: DeploymentDiagnosis | undefined,
  router: ReturnType<typeof useRouter>,
): Finding[] {
  return useMemo(() => {
    if (!diagnosis) return []
    const findings: Finding[] = diagnosis.findings.map((finding) => ({
      id: `${finding.code}:${finding.owner}:${finding.title}`,
      level: finding.severity,
      title: finding.title,
      detail: finding.measured,
      advice: [finding.means, finding.action].filter(Boolean).join(" "),
      action: finding.deepLink
        ? {
            label: `Open ${finding.owner}`,
            onClick: () =>
              finding.external
                ? window.open(finding.deepLink, "_blank", "noopener,noreferrer")
                : router.push(finding.deepLink!),
          }
        : undefined,
    }))
    if (diagnosis.silences.length > 0) {
      const n = diagnosis.silences.length
      findings.push({
        id: "silences",
        level: "notice",
        title: `${n} owner${n === 1 ? "" : "s"} could not be read`,
        detail: "Their most recent evidence is shown instead of a fresh reading.",
        extra: (
          <ul className="space-y-1">
            {diagnosis.silences.map((silence, index) => (
              <li key={silence.subject + index}>
                <span className="font-medium capitalize">{silence.subject}</span>: {silence.reason}
              </li>
            ))}
          </ul>
        ),
      })
    }
    return findings
  }, [diagnosis, router])
}
