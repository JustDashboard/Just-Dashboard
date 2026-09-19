"use client"

import { useCallback, useMemo, useState } from "react"
import { useRouter } from "next/navigation"
import Link from "next/link"
import { ArrowRight } from "@/components/icons"
import { get } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import type {
  ContainerHistory,
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
import { Status, StatusDot } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { useProject } from "@/components/deploy/project-context"
import {
  RunStatus,
  deploymentURL,
  hostOf,
  projectState,
  runSubject,
  runTitle,
  sourceLine,
} from "@/components/deploy/vocabulary"
import { SitePreview } from "@/components/deploy/site-preview"
import { RollbackDialog } from "@/components/deploy/rollback-dialog"
import { ProjectWiring } from "@/components/deploy/project-wiring"
import { Insights } from "@/components/deploy/insights"
import { Sparkline } from "@/components/metrics/sparkline"
import { NumberTicker } from "@/components/ui/number-ticker"

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
          // The preview takes a measure rather than a share: past about 32rem a
          // thumbnail stops telling the reader more and only grows a tall block
          // of somebody else's website into the middle of the page.
          className="grid items-start gap-8 pt-4 lg:grid-cols-[minmax(0,32rem)_minmax(0,1fr)]"
        >
          <SitePreview
            key={`${deployment.endpoint}:${deployment.liveReleaseId}`}
            deployment={deployment}
          />
          <ProjectWiring
            projectId={project.projectId}
            deployment={deployment}
            state={projectState(deployment, runtime, project.archived)}
            source={source}
            liveRelease={project.liveRelease}
            liveRun={project.liveRun}
            runtime={runtime}
            domains={opsDomains}
            url={url}
            watch={watch.data}
          />
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

      {/* The delivery figures belong on the front page as much as on
          Deployments: how often this project ships and how often it
          fails are the two facts a visitor asks after "is it up". */}
      {project.normalized && <Insights projectId={project.projectId} />}

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

/**
 * CPU and memory from the live stats socket — the same reading Runtime
 * shows, at a glance — with the last hour's shape beside each figure, from
 * the recorded history, so "1.2%" also says whether it was 40% a moment ago.
 */
function UsageTiles({ containerId, name }: { containerId: string; name: string }) {
  const [stats, setStats] = useState<ContainerStats | null>(null)
  const onMessage = useCallback((message: Envelope) => {
    if (message.type === "stats") setStats(message.data as ContainerStats)
  }, [])
  const socket = useSocket(`/docker/containers/${containerId}/stats/stream`, { onMessage })
  const live = socket.state === "open"
  const history = usePoll(
    (signal) =>
      get<ContainerHistory>(
        `/docker/containers/${encodeURIComponent(containerId)}/stats/history`,
        { points: 60 },
        signal,
      ),
    60000,
    [containerId],
  )
  const points = history.data?.points ?? []
  const trend = (values: number[], label: string, color: string) =>
    values.length > 1 ? (
      <Sparkline values={values} label={label} color={color} width={72} height={20} />
    ) : null
  return (
    <StatGrid columns={2}>
      {/* The figures spring from one reading to the next rather than jumping,
          so a stats socket at one message a second reads as a gauge. */}
      <StatTile
        label="CPU"
        value={stats ? <NumberTicker value={stats.cpuPercent} decimalPlaces={1} /> : "—"}
        trailing={
          <span className="inline-flex items-center gap-3">
            {stats && "%"}
            {trend(
              points.map((point) => point.cpuPeak),
              "CPU over the last hour",
              "var(--chart-1)",
            )}
          </span>
        }
        hint={name}
      />
      <StatTile
        label="Memory"
        value={stats ? <NumberTicker value={stats.memUsage / 1024 / 1024} /> : "—"}
        trailing={
          <span className="inline-flex items-center gap-3">
            {stats && "MiB"}
            {trend(
              points.map((point) => point.memBytesPeak),
              "Memory over the last hour",
              "var(--chart-2)",
            )}
            {live && <StatusDot tone="running" live />}
          </span>
        }
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
