"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import { useRouter } from "next/navigation"
import Link from "next/link"
import { ArrowRight } from "@/components/icons"
import { get } from "@/lib/api"
import { percent, relativeTime } from "@/lib/format"
import { perMinute } from "@/lib/requests"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type {
  DeploymentDiagnosis,
  DeploymentPreview,
  DeploymentSummary,
  TrafficPulse,
} from "@/lib/types"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { EmptyNote } from "@/components/state"
import { FindingList, type Finding } from "@/components/finding-list"
import { ChoiceList } from "@/components/flow"
import { StatGrid, StatLink, StatTile } from "@/components/stat-tile"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { ProductLogo, buildMethodProduct, imageProduct } from "@/components/product-logo"
import { TileTrend } from "@/components/metrics/sparkline"
import { SourcePull } from "@/components/git/glyphs"
import { Button } from "@/components/ui/button"
import { BlurFade } from "@/components/ui/blur-fade"
import { Confetti, type ConfettiRef } from "@/components/ui/confetti"
import { NumberTicker } from "@/components/ui/number-ticker"
import { useProject } from "@/components/deploy/project-context"
import {
  WORKLOAD_LABELS,
  deploymentURL,
  hostOf,
  isActiveRun,
  projectState,
  shortRevision,
  sourceLine,
  sourceProduct,
} from "@/components/deploy/vocabulary"
import { failingTone } from "@/components/deploy/fleet"
import { FirstSignIn } from "@/components/deploy/first-sign-in"
import { SitePreview } from "@/components/deploy/site-preview"
import { RollbackDialog } from "@/components/deploy/rollback-dialog"
import { ProjectWiring } from "@/components/deploy/project-wiring"
import { Insights } from "@/components/deploy/insights"
import { RunRow } from "@/components/deploy/run-row"
import { UsageTiles } from "@/components/deploy/usage-tiles"
import { BeforeYouDeploy } from "@/components/deploy/deploy-check"
import { attentionFindings } from "@/components/deploy/deploy-check-state"

/**
 * The project's front page, in the order a visitor asks: is something
 * happening now, what is live and how a request reaches it, how it is doing,
 * what needs attention, how often it ships, and what shipped last.
 *
 * A run in flight is the first thing on the page, as the run itself — the
 * runs list's own row, with the stage it is at and a light running round its
 * edge — where it was a notice with a link in it (§14: a state is not a
 * banner), and it is not listed again under recent deployments. Under it,
 * Production: a window onto the live site beside the way a request reaches
 * it, and under both the four live readings — requests, the share failing,
 * processor and memory — each carrying its last hour and each a way to the
 * page that has the rest. They were two panels of two tiles, the second of
 * which sat alone on its own row at every desktop width.
 *
 * Findings live here rather than on Runtime — Runtime is evidence, this is
 * the verdict — and the identity line's "needs attention" links to them. The
 * recent deployments are runs you open, drawn as the runs list draws them,
 * beside the preview environments. Each block arrives on its own beat, and a
 * block whose read settles later rises when it does (§11).
 */
export function ProjectOverview() {
  const project = useProject()
  const router = useRouter()
  const { can } = useAuth()
  const { deployment, project: record, runtime } = project.detail
  const [rollbackOpen, setRollbackOpen] = useState(false)

  const url = deploymentURL(deployment.endpoint)
  const state = projectState(deployment, runtime, project.archived)
  const opsDomains =
    project.operations?.domains.status === "available"
      ? project.operations.domains.domains
      : undefined
  const domain = opsDomains?.find((route) => route.hostname === hostOf(url))

  const previews = usePoll(
    (signal) =>
      get<DeploymentPreview[]>(`/deploy/${project.projectId}/previews`, undefined, signal),
    30000,
    [project.projectId],
    { enabled: project.normalized && !project.archived },
  )

  const findings = useFindings(project.operations?.diagnosis, router)
  const runs = project.runs.filter((run) => run.environmentId === project.environmentId)
  // The run in flight is the page's first block; it is not listed a second
  // time under it.
  const recent = runs.filter((run) => run.id !== deployment.activeRun?.id).slice(0, 5)
  const liveService =
    runtime?.status === "available"
      ? (runtime.services.find((service) => service.liveRelease) ?? runtime.services[0])
      : undefined
  const buildLogsRun = deployment.activeRun ?? project.liveRun
  const product = project.product
  const access = project.blueprint?.access

  const rollbackEligible = project.releases.some(
    (release) => release.state === "retained" && release.id !== deployment.liveReleaseId,
  )
  const domainHostnames = useMemo(() => {
    if (opsDomains) return opsDomains.map((route) => route.hostname)
    const host = url && hostOf(url)
    return host ? [host] : []
  }, [opsDomains, url])

  const celebrate = useCelebration(deployment)

  const blocks: [string, React.ReactNode][] = []
  if (deployment.activeRun)
    blocks.push([
      `run-${deployment.activeRun.id}`,
      <ChoiceList key="run" aria-label="Deployment in progress">
        <RunRow run={deployment.activeRun} deployment={deployment} />
      </ChoiceList>,
    ])

  blocks.push([
    "production",
    <Panel key="production" plain>
      <PanelHeader
        title={deployment.environmentName}
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
      <PanelBody flush className="pt-4">
        <div
          // The preview takes a measure rather than a share from `xl`: past
          // about 32rem a thumbnail stops telling the reader more and only
          // grows a tall block of somebody else's website into the middle of
          // the page. Below it the two share the width, because at a 1024
          // window a fixed preview left the wiring 170 pixels and its
          // addresses ran past the page's edge.
          className="grid grid-cols-[minmax(0,1fr)] items-start gap-8 lg:grid-cols-2 xl:grid-cols-[minmax(0,32rem)_minmax(0,1fr)]"
        >
          <SitePreview
            // A new address, a new release or a start after a stop is a fresh
            // load: the frame is hidden again until it has painted.
            key={`${deployment.endpoint}:${deployment.liveReleaseId}:${state === "stopped"}`}
            deployment={deployment}
            product={product}
            domain={domain}
            stopped={state === "stopped"}
          />
          <ProjectWiring
            projectId={project.projectId}
            deployment={deployment}
            state={state}
            branch={sourceLine(deployment, record).primary}
            title={sourceTitle(deployment, record.repoPath, project)}
            sourceMark={sourceProduct(deployment, project.configuration?.source)}
            sourceDetail={sourceDetail(deployment, project)}
            runtimeProduct={runtimeProduct(deployment, product, project.configuration)}
            product={product}
            liveRelease={project.liveRelease}
            liveRun={project.liveRun}
            runs={runs}
            runtime={runtime}
            domains={opsDomains}
            url={url}
            watch={project.gitWatch}
          />
        </div>
        {/* The four readings a visitor asks after "is it up": two from the
            request record and two from the live container, one row of
            figures under the picture they describe. */}
        <div className="mt-6 grid min-w-0 border-t border-hairline xl:grid-cols-2">
          <TrafficTiles projectId={project.projectId} />
          <div className="min-w-0 border-t border-hairline xl:border-t-0 xl:border-l xl:pl-5">
            {/* A container that has exited has no usage to read live: its
                last socket frame would claim a reading of something that is
                not running (§11). */}
            <UsageTiles
              containerId={liveService?.state === "running" ? liveService.containerId : undefined}
              name={liveService?.name}
              reason={
                liveService && liveService.state !== "running"
                  ? "The containers are not running"
                  : runtime?.reason || "Usage appears when your application starts."
              }
              href={`/deploy/${project.projectId}/runtime`}
            />
          </div>
        </div>
      </PanelBody>
    </Panel>,
  ])

  // What a deployment of the saved plan would stop on, or should be
  // confirmed, from the check the project made when it opened — here, before
  // the header's Deploy is pressed, rather than on the run page after.
  if (
    project.check &&
    !deployment.activeRun &&
    attentionFindings(project.check.findings).length > 0
  )
    blocks.push([
      "before-you-deploy",
      <BeforeYouDeploy
        key="before-you-deploy"
        projectId={project.projectId}
        result={project.check}
        checking={project.checking}
        onRecheck={project.recheck}
      />,
    ])

  if (access)
    blocks.push([
      "sign-in",
      <FirstSignIn
        key="sign-in"
        access={access}
        url={url}
        projectId={project.projectId}
        environmentId={project.environmentId}
        canReveal={can("system.admin")}
        product={product}
        service={liveService?.name}
        port={deployment.internalPort}
      />,
    ])

  if (findings.length > 0)
    blocks.push([
      "attention",
      <Panel key="attention" plain id="attention" className="scroll-mt-6">
        <PanelHeader title="Needs attention" />
        <PanelBody>
          <FindingList findings={findings} />
        </PanelBody>
      </Panel>,
    ])

  // The delivery figures belong on the front page as much as on Deployments:
  // how often this project ships and how often it fails are the two facts a
  // visitor asks after "is it up".
  if (project.normalized)
    blocks.push(["delivery", <Insights key="delivery" projectId={project.projectId} />])

  const hasPreviews = Boolean(previews.data && previews.data.length > 0)
  blocks.push([
    "history",
    <div
      key="history"
      className={
        hasPreviews ? "grid min-w-0 gap-8 2xl:grid-cols-[minmax(0,1.6fr)_minmax(0,1fr)]" : "min-w-0"
      }
    >
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
        <PanelBody>
          {recent.length ? (
            <ChoiceList>
              {recent.map((run, index) => (
                <RunRow
                  key={run.id}
                  run={run}
                  deployment={deployment}
                  // The release names the commit of a run that recorded none.
                  release={project.releases.find((release) => release.id === run.releaseId)}
                  index={index}
                  live={run.id === project.liveRun?.id}
                />
              ))}
            </ChoiceList>
          ) : (
            <EmptyNote className="px-0 py-5 text-left">
              {deployment.activeRun
                ? "The deployment above is the first. It is listed here once it finishes."
                : "Start your first deployment to see its build and release here."}
            </EmptyNote>
          )}
        </PanelBody>
      </Panel>

      {hasPreviews && (
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
              {previews.data?.map((preview) => (
                <Row
                  key={preview.id}
                  leading={<ProductLogo size="sm" fallback={SourcePull} />}
                  title={<span className="font-mono">{preview.environmentSlug}</span>}
                  subtitle={`updated ${relativeTime(preview.updatedAt)}`}
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
    </div>,
  ])

  return (
    <div className="space-y-8">
      {blocks.map(([key, block], index) => (
        <BlurFade key={key} delay={index * 0.04}>
          {block}
        </BlurFade>
      ))}

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
      <Confetti ref={celebrate} className="pointer-events-none fixed inset-0 z-50 size-full" />
    </div>
  )
}

/** What the source is called, as the wiring's first node names it. */
function sourceTitle(
  deployment: DeploymentSummary,
  repoPath: string | undefined,
  project: ReturnType<typeof useProject>,
) {
  switch (deployment.sourceKind) {
    case "git":
      return (
        deployment.sourceRepository ||
        project.configuration?.identity?.repository ||
        project.configuration?.source?.url ||
        repoPath ||
        deployment.sourceRef ||
        "Repository"
      )
    case "local":
      return project.configuration?.source?.localPath || repoPath || "Local checkout"
    case "image":
      return deployment.sourceRepository || deployment.sourceRef || "Docker image"
    case "compose":
      return "Compose stack"
    case "blueprint":
      return project.blueprint?.name ?? deployment.sourceRepository ?? "Template"
    default:
      return WORKLOAD_LABELS[deployment.profile]
  }
}

/**
 * What a source with no commit resolved to: a template's version and the
 * image it pins, an image's digest, the file a stack was read from.
 */
function sourceDetail(deployment: DeploymentSummary, project: ReturnType<typeof useProject>) {
  switch (deployment.sourceKind) {
    case "blueprint": {
      const version = (deployment.sourceRepository || deployment.sourceRef || "").split("@")[1]
      return [version && `v${version}`, project.blueprint?.image.reference]
        .filter(Boolean)
        .join(" · ")
    }
    case "image":
      return shortRevision(deployment.sourceRevision)
    case "compose":
      return deployment.sourceRef
    default:
      return undefined
  }
}

/**
 * What the containers run: a repository's language (or Docker for a
 * Dockerfile, nginx for a static site), the product an image or a template is.
 * The header's tile names the framework; this is the thing actually running.
 */
function runtimeProduct(
  deployment: DeploymentSummary,
  product: string | undefined,
  configuration: ReturnType<typeof useProject>["configuration"],
) {
  if (deployment.sourceKind === "git" || deployment.sourceKind === "local") {
    const build = configuration?.build
    return (
      buildMethodProduct(build?.method ?? deployment.buildMethod, {
        recipe: build?.recipe ?? deployment.recipe,
        packageManager: build?.packageManager,
      }) ?? product
    )
  }
  if (deployment.sourceKind === "image")
    return imageProduct(deployment.sourceRepository || deployment.sourceRef || "")
  return product
}

/**
 * Paper, once, when a run the reader watched from this page goes live in front
 * of them — never on arrival at a page whose last run happened to succeed.
 */
function useCelebration(deployment: DeploymentSummary) {
  const confetti = useRef<ConfettiRef>(null)
  const watched = useRef<number | undefined>(undefined)
  const active = deployment.activeRun?.id
  const last = deployment.lastRun
  useEffect(() => {
    if (active) {
      watched.current = active
      return
    }
    if (watched.current === undefined) return
    if (last?.id !== watched.current) {
      watched.current = undefined
      return
    }
    if (isActiveRun(last.state)) return
    if (last.state === "succeeded") confetti.current?.fire()
    watched.current = undefined
  }, [active, last?.id, last?.state])
  return confetti
}

/**
 * `operations.diagnosis.findings` read through `FindingList`'s vocabulary:
 * severity is already the shared level, `measured` is the detail, `means` and
 * `action` join into the advice, a `deepLink` becomes "Open {owner}", and the
 * owner is the tag at the row's edge (§4). Owners the diagnosis could not read
 * at all are folded into one notice-level row rather than counted as healthy.
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
      meta: finding.owner ? <Tag>{finding.owner}</Tag> : undefined,
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

const TILE_HOVER = "h-full transition-colors group-hover:bg-row-hover"

/**
 * The last hour at the ingress: how much, with its shape, and how much of it
 * failed — read from the record the server holds, content with a reading a
 * minute old. Each is a way to the Logs page, where the rows are. With no
 * record yet the tiles stay, with a dash and the reason, so the row of four
 * does not reflow when the first request lands. The failing share takes the
 * fleet's colour for it, so one share is one colour on every deploy page.
 */
function TrafficTiles({ projectId }: { projectId: number }) {
  const pulse = usePoll<Record<string, TrafficPulse>>(
    (signal) => get<Record<string, TrafficPulse>>("/deploy/traffic", undefined, signal),
    30000,
    [],
  )
  const mine = pulse.data?.[String(projectId)]
  const ready = mine?.status === "available" ? mine : undefined
  const waiting = pulse.data ? "No requests recorded yet" : "Reading the request record…"
  const base = `/deploy/${projectId}/logs`
  return (
    <StatGrid columns={2} dense>
      <StatLink href={base} label="Requests on Logs">
        <StatTile
          // Keyed by whether there is a figure, so it rises when the record
          // lands rather than on every poll.
          key={ready ? "requests" : "requests-waiting"}
          label="Requests"
          value={
            ready ? (
              <NumberTicker
                value={Number(perMinute(ready.perMinute))}
                decimalPlaces={ready.perMinute < 10 ? 2 : ready.perMinute < 100 ? 1 : 0}
              />
            ) : (
              "—"
            )
          }
          trailing={ready && "/min"}
          trend={
            ready && <TileTrend values={ready.points} label="Requests per minute, last hour" />
          }
          hint={ready ? `${ready.pages.toLocaleString()} page views · last hour` : waiting}
          className={cn(TILE_HOVER, ready && "animate-rise")}
        />
      </StatLink>
      <StatLink href={`${base}?view=insights`} label="Failing requests on Logs">
        <StatTile
          key={ready ? "failing" : "failing-waiting"}
          label="Failing"
          value={ready ? percent(ready.errorRate * 100, 1) : "—"}
          tone={ready ? failingTone(ready.errorRate) : "default"}
          hint="answered 5xx · last hour"
          className={cn(TILE_HOVER, ready && "animate-rise")}
        />
      </StatLink>
    </StatGrid>
  )
}
