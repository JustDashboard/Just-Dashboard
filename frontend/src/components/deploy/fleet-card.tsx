"use client"

import Link from "next/link"
import { useRouter } from "next/navigation"
import { ArrowRight } from "@/components/icons"
import { percent, plural, relativeTime, timestamp } from "@/lib/format"
import { perMinute } from "@/lib/requests"
import { cn } from "@/lib/utils"
import type {
  DeploymentActiveWork,
  DeploymentEngineRun,
  DeploymentSummary,
  TrafficPulse,
} from "@/lib/types"
import type { useConfirm } from "@/components/confirm-dialog"
import { CONTROL, ChoiceRow } from "@/components/flow"
import { BranchChip, CommitLine, ShortSha } from "@/components/git/marks"
import { Sparkline } from "@/components/metrics/sparkline"
import { ProductGlyph, ProductGlyphs, imageProducts } from "@/components/product-logo"
import { Status, StatusDot } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { VerbActions } from "@/components/verbs"
import { BlurFade } from "@/components/ui/blur-fade"
import { BorderBeam } from "@/components/ui/border-beam"
import { SpotlightBorder } from "@/components/ui/spotlight-border"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { ProjectMark } from "@/components/deploy/project-mark"
import { useProjectStart, useProjectVerbs } from "@/components/deploy/project-verbs"
import { MiniReleasePath } from "@/components/deploy/run-pipeline"
import { RunActorMark, RunStrip } from "@/components/deploy/run-marks"
import {
  ProjectStatus,
  RUN_LABELS,
  WORKLOAD_LABELS,
  deploymentURL,
  formatDuration,
  hostOf,
  operationLabel,
  reachableAt,
  runCommit,
  runDurationSeconds,
  runTriggerLine,
  shortRevision,
  sourceProduct,
  stepName,
  useNow,
} from "@/components/deploy/vocabulary"
import { FAILING_NOTICE, failingTone } from "@/components/deploy/fleet"

/**
 * One project in the fleet, in the two shapes the page draws: a card in the
 * grid and a row in the list.
 *
 * Both are destinations — every one opens the project — so both carry the lit
 * edge §16 gives to things you take, where the grid used to be framed panels
 * whose only answer to the pointer was a border step and the list was rows you
 * read. And both draw the project as what it is, in the colours that are the
 * data's own: the product it runs, where its source lives and who last
 * committed to it, how much traffic it is taking and what share of it fails,
 * the last fourteen runs as a strip, and, while one is in flight, the release
 * path sweeping and the stage it is at lit.
 *
 * The verbs are the project page's own (`useProjectVerbs`), so the card and
 * the project header cannot disagree about what can be done to a project.
 * Visit is drawn inline on a row, which does not show the host; a card's host
 * line already is the link to the site, so on a card Visit heads the menu's
 * Project group.
 */

type Confirm = ReturnType<typeof useConfirm>["confirm"]

type ProjectProps = {
  deployment: DeploymentSummary
  /** The last hour at the ingress, from the fleet-wide pulse. */
  pulse?: TrafficPulse
  /** The fleet's own line for the run in flight: its stage and place in the queue. */
  work?: DeploymentActiveWork
  confirm: Confirm
  refresh: () => void
}

/**
 * The project's verbs, and the word its state reads while one of them is on
 * its way to the server — "Starting…" rather than a card that sat still and
 * then jumped (§13).
 *
 * Only what can be done now. The project's own header keeps a disabled verb
 * in its place, where the page around it says why; on a card in flight, four
 * greyed verbs with no reason each made a menu taller than a laptop's window.
 */
function useFleetVerbs(deployment: DeploymentSummary, confirm: Confirm, refresh: () => void) {
  // The check the project page last made, when it made one: a card asks
  // "Ready to deploy?" on what that page found, and otherwise builds at once.
  const { start, starting, gate } = useProjectStart(deployment, refresh)
  const all = useProjectVerbs(deployment, {
    confirm,
    refresh,
    start,
    starting,
    navigation: true,
  })
  const progressive = starting ? all.find((verb) => verb.key === starting)?.progressive : undefined
  return { verbs: all.filter((verb) => !verb.disabled), progressive, gate }
}

function FleetStatus({
  deployment,
  progressive,
  className,
}: {
  deployment: DeploymentSummary
  progressive?: string
  className?: string
}) {
  return progressive ? (
    <Status tone="warning" label={progressive} className={className} />
  ) : (
    <ProjectStatus summary={deployment} className={className} />
  )
}

/**
 * The run in flight, drawn the way its own page draws it: the release path's
 * seven segments with the current one sweeping, the stage it is at lit, and
 * how long it has taken — or its place in the queue before it has begun.
 */
function InFlight({
  deployment,
  work,
  now,
}: {
  deployment: DeploymentSummary
  work?: DeploymentActiveWork
  now: number
}) {
  const run = deployment.activeRun
  if (!run) return null
  return (
    <>
      <MiniReleasePath
        currentStep={work?.currentStep ?? run.currentStep?.key}
        currentStatus={work?.currentStatus ?? run.currentStep?.state}
      />
      <TextShimmer className="truncate text-hint font-medium">{stageOf(run, work)}</TextShimmer>
      <span className="numeric shrink-0">{runClock(run, work, now)}</span>
    </>
  )
}

/** The stage a run is at, in the engine's own label, or the run's state before it has one. */
export function stageOf(run: DeploymentEngineRun, work?: DeploymentActiveWork) {
  if (run.cancelRequested) return "Cancelling…"
  if (run.currentStep?.label) return run.currentStep.label
  if (work?.currentStep) return stepName(work.currentStep)
  return RUN_LABELS[run.state]
}

/** How long a run has taken, or its place in the queue before it has begun. */
export function runClock(
  run: DeploymentEngineRun,
  work: DeploymentActiveWork | undefined,
  now: number,
) {
  return work?.queuePosition
    ? `Queue ${work.queuePosition}`
    : formatDuration(runDurationSeconds(run, now))
}

/**
 * Where the project answers, when it answers anywhere: its host, its port, or
 * that it is private. Mono only for the literal the host gave — "Private" is a
 * word. A project that has never been deployed says so in its state, so it
 * has no second line saying it again.
 */
function Address({ deployment, link }: { deployment: DeploymentSummary; link?: boolean }) {
  const url = deploymentURL(deployment.endpoint)
  if (url && link)
    return (
      <a
        href={url}
        target="_blank"
        rel="noopener noreferrer"
        className="min-w-0 truncate rounded-sm font-mono focus-ring hover:text-foreground"
      >
        {hostOf(url)}
      </a>
    )
  if (!url && !deployment.liveReleaseId) return null
  const literal = deployment.endpoint || deployment.hostPort || deployment.internalPort
  return (
    <span className={cn("min-w-0 truncate", literal && "font-mono")}>
      {url ? hostOf(url) : reachableAt(deployment)}
    </span>
  )
}

/**
 * A site's failing share, in the colour the fleet's Failing requests tile
 * gives the same share: amber from 1%, red from 5%. Drawn only from 1%.
 */
export function FailingShare({ rate, className }: { rate: number; className?: string }) {
  return (
    <span
      className={cn(
        "numeric",
        failingTone(rate) === "danger" ? "text-destructive" : "text-warning",
        className,
      )}
    >
      {percent(rate * 100, 1)} failing
    </span>
  )
}

/**
 * The last thing that happened to the project: who or what did it, as their
 * face or product, and when. A project with no runs says when it was last
 * changed instead.
 */
function LastActivity({ deployment }: { deployment: DeploymentSummary }) {
  const last = deployment.lastRun
  if (!last)
    return <span className="whitespace-nowrap">Updated {relativeTime(deployment.updatedAt)}</span>
  const at = last.endedAt ?? last.requestedAt
  return (
    <span className="inline-flex items-center gap-1.5" title={runTriggerLine(last)}>
      <RunActorMark run={last} remote={deployment.sourceRemote} size="xs" />
      <time dateTime={at} title={timestamp(at)} className="whitespace-nowrap">
        {operationLabel(last.operation)} {relativeTime(at)}
      </time>
    </span>
  )
}

/**
 * Where a project comes from — the part of the source line it opens with. A
 * row has no room for a repository's name beside its branch and commit, so it
 * leaves it to the forge's mark; nor for the products a stack runs, which the
 * row's own mark already draws.
 */
function SourceOrigin({
  deployment: d,
  compact,
}: {
  deployment: DeploymentSummary
  compact?: boolean
}) {
  const services = (d.serviceCount ?? 0) > 1 ? plural(d.serviceCount ?? 0, "service") : undefined
  const runs = imageProducts(d.images ?? []).filter((id) => id !== "docker")
  switch (d.sourceKind) {
    case "git":
    case "local": {
      const host = sourceProduct(d)
      return (
        <>
          {host && <ProductGlyph id={host} />}
          {!compact && d.sourceRepository && (
            <span className="min-w-0 truncate font-mono text-foreground/85">
              {d.sourceRepository}
            </span>
          )}
          <BranchChip branch={d.sourceRef || "main"} className="max-w-[10rem] shrink-0" />
          {/* The commit line carries the revision when the engine recorded
              what it was; without a subject the short sha is all there is. */}
          {!runCommit(d.lastRun)?.subject && <ShortSha sha={shortRevision(d.sourceRevision)} />}
          {services && <span className="shrink-0">{services}</span>}
          {services && !compact && <ProductGlyphs ids={runs} />}
        </>
      )
    }
    case "image": {
      const product = sourceProduct(d)
      return (
        <>
          {product && <ProductGlyph id={product} />}
          <span className="min-w-0 truncate font-mono text-foreground/85">
            {d.sourceRepository || d.sourceRef || shortRevision(d.sourceRevision) || "Docker image"}
          </span>
        </>
      )
    }
    case "compose":
      // The file's name is not worth a glance once several services run
      // from it; how many, and what they are, is.
      return (
        <>
          <span className="min-w-0 truncate">
            Compose stack{services ? ` · ${services}` : d.sourceRef ? ` · ${d.sourceRef}` : ""}
          </span>
          {!compact && <ProductGlyphs ids={runs} />}
        </>
      )
    case "blueprint":
      return (
        <>
          <span className="shrink-0">Template</span>
          {/* The tag is a flex box, which text-overflow does not reach, so
              the reference inside it is what truncates. */}
          {(d.sourceRepository || d.sourceRef) && (
            <Tag mono className="min-w-0 shrink">
              <span className="truncate">{d.sourceRepository || d.sourceRef}</span>
            </Tag>
          )}
        </>
      )
    default:
      return (
        <>
          <span className="min-w-0 truncate">{WORKLOAD_LABELS[d.profile]}</span>
          {!compact && <ProductGlyphs ids={runs} />}
        </>
      )
  }
}

/**
 * Where the project comes from and what last went into it: the forge and the
 * repository, the branch, and the commit with its author in their own hue —
 * the Git page's drawing of the same things. A card spends two lines on it;
 * a row's second line takes the origin and the commit's subject.
 */
export function SourceSummary({
  deployment,
  compact,
}: {
  deployment: DeploymentSummary
  compact?: boolean
}) {
  const commit =
    deployment.sourceKind === "git" || deployment.sourceKind === "local"
      ? runCommit(deployment.lastRun)
      : undefined
  if (compact) {
    return (
      <span className="flex max-w-full min-w-0 items-center gap-1.5">
        <SourceOrigin deployment={deployment} compact />
        {commit?.subject && (
          <span className="min-w-0 truncate text-foreground/80">{commit.subject}</span>
        )}
      </span>
    )
  }
  return (
    <div className="min-w-0 space-y-1.5 text-hint text-muted-foreground">
      <p className="flex min-w-0 items-center gap-1.5">
        <SourceOrigin deployment={deployment} />
      </p>
      {commit?.subject && (
        <CommitLine
          className="min-w-0"
          sha={commit.sha}
          subject={commit.subject}
          author={commit.author}
          at={commit.authoredAt}
        />
      )}
    </div>
  )
}

/**
 * The last hour at the ingress as a line the width of the card, beside how
 * many requests a minute and what share failed. The line keeps its series
 * colour; the only red is the failing share, attached to its figure (§3).
 * Nothing is reserved for a project that takes no traffic — a worker, a game
 * server — and the row rises once when its hour of history lands (§11).
 */
function CardTraffic({ pulse }: { pulse: TrafficPulse }) {
  return (
    <div className="flex min-w-0 animate-rise items-end gap-3">
      <Sparkline
        values={pulse.points}
        width={240}
        height={28}
        className="h-7 min-w-0 flex-1"
        label="Requests per minute, last hour"
      />
      <span className="shrink-0 text-right leading-tight">
        <span className="numeric block text-body font-medium">
          {perMinute(pulse.perMinute)}
          <span className="text-hint font-normal text-muted-foreground">/min</span>
        </span>
        {pulse.errorRate >= FAILING_NOTICE ? (
          <FailingShare rate={pulse.errorRate} className="block text-hint" />
        ) : (
          <span className="numeric block text-hint text-muted-foreground">
            {plural(pulse.pages, "view")}
          </span>
        )}
      </span>
    </div>
  )
}

/**
 * The same hour in a row's measure: a short line, the rate, and the failing
 * share when there is one — named, because no header sits over the column.
 */
function RowTraffic({ pulse }: { pulse: TrafficPulse }) {
  return (
    <span className="inline-flex min-w-0 animate-rise items-center gap-2 text-hint">
      <Sparkline
        values={pulse.points}
        width={48}
        height={16}
        label="Requests per minute, last hour"
      />
      <span className="numeric text-foreground/85">{perMinute(pulse.perMinute)}/min</span>
      {pulse.errorRate >= FAILING_NOTICE && <FailingShare rate={pulse.errorRate} />}
    </span>
  )
}

/**
 * A card in the grid.
 *
 * Built the way `git/repo-row.tsx` builds a destination rather than as a
 * `ChoiceRow`: a card is taller than a row and lays its readings out in
 * lines, not in a row's trailing measures. The press anywhere on it opens the
 * project, except on a control of its own — the name, the address, the verbs
 * (`CONTROL`, the rule `ChoiceRow` keeps) — and the name is the real link a
 * keyboard reaches. While a run is in flight a light runs round the edge.
 */
export function ProjectCard({
  deployment,
  pulse,
  work,
  confirm,
  refresh,
  index,
}: ProjectProps & { index: number }) {
  const router = useRouter()
  const { verbs, progressive, gate } = useFleetVerbs(deployment, confirm, refresh)
  // The address under the name is already the way to the site, so a card
  // keeps Visit in its menu, first of the ways into the project, rather than
  // drawing a second glyph for it beside the arrow.
  const visit = verbs.find((verb) => verb.key === "visit")
  const menu = verbs.filter((verb) => verb !== visit)
  if (visit)
    menu.splice(
      menu.findIndex((verb) => verb.key === "open"),
      0,
      { ...visit, inline: false, group: "Project" },
    )
  const active = Boolean(deployment.activeRun)
  const now = useNow(1000, active)
  const base = `/deploy/${deployment.id}`
  const recent = deployment.recentRuns ?? []

  return (
    <li className="min-w-0">
      {/* Each card lands a beat after the one before it, capped so a fleet
          of forty does not take three seconds. */}
      <BlurFade delay={Math.min(index, 11) * 0.045} className="h-full">
        <SpotlightBorder radius={360} className="h-full">
          {active && (
            <span aria-hidden className="pointer-events-none absolute -inset-px rounded-xl">
              <BorderBeam size={80} duration={4} />
            </span>
          )}
          <div
            onClick={(event) => {
              // React carries a press inside the open menu — a group label,
              // a separator — up through its portal to here, though it
              // landed nowhere on the card.
              const target = event.target as HTMLElement
              if (!event.currentTarget.contains(target) || target.closest(CONTROL)) return
              router.push(base)
            }}
            className="group group/choice flex h-full min-w-0 cursor-pointer flex-col gap-3 rounded-xl p-4"
          >
            {/* The state sits under the name rather than beside it: four
                cards to a row leave a name beside a state and two verbs
                about a hundred pixels, and the name is what is read. The
                verbs take the name's line only, so the state and the host
                run on under them to the card's edge. */}
            <div className="grid min-w-0 grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1">
              <ProjectMark deployment={deployment} size="md" className="row-span-2" />
              <Link
                href={base}
                className="block max-w-full min-w-0 truncate rounded-sm text-title leading-tight font-medium focus-ring"
              >
                {deployment.name}
              </Link>
              <span className="-my-1 -mr-1.5 flex items-center gap-1">
                <ArrowRight
                  aria-hidden
                  className="size-3.5 shrink-0 text-muted-foreground transition-colors group-hover/choice:text-foreground"
                />
                <VerbActions dim verbs={menu} menuLabel={`Actions for ${deployment.name}`} />
              </span>
              <div className="col-span-2 col-start-2 flex min-w-0 items-center gap-2 text-hint text-muted-foreground">
                <FleetStatus
                  deployment={deployment}
                  progressive={progressive}
                  className="shrink-0"
                />
                <Address deployment={deployment} link />
              </div>
            </div>

            <SourceSummary deployment={deployment} />

            {/* The traffic sits on the footer rather than under the source, so
                the lines of a row of cards share one baseline whether or not
                each card has a commit to show. */}
            <div className="mt-auto flex min-w-0 flex-col gap-3">
              {pulse?.status === "available" && <CardTraffic pulse={pulse} />}
              {/* Gaps separate the pieces rather than a middle dot, which was
                  left dangling at the end of a line wherever this wrapped. */}
              <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1.5 border-t border-hairline pt-3 text-hint text-muted-foreground">
                {active ? (
                  <InFlight deployment={deployment} work={work} now={now} />
                ) : (
                  <>
                    {recent.length > 0 && <RunStrip runs={recent.slice(0, 14)} />}
                    <LastActivity deployment={deployment} />
                  </>
                )}
                {deployment.pendingChanges && !active && (
                  <Status tone="warning" label="Changes pending" className="ml-auto" />
                )}
              </div>
            </div>
          </div>
        </SpotlightBorder>
      </BlurFade>
      {gate}
    </li>
  )
}

/**
 * A row in the list, in the shape the page picks for its width.
 *
 * `wide` (from `xl`): the readings sit beside the name in fixed measures — its
 * state, its traffic, its history, when — so a column of rows scans like the
 * table it replaces. Below that they go beneath the name at the row's full
 * width and nothing is dropped (§12); `roomy` (from `sm`) keeps the state
 * beside the name, and a phone puts it first on the line beneath, where the
 * verbs beside the name would otherwise leave the source line a third of the
 * row and the commit's subject nothing.
 */
export function ProjectRow({
  deployment,
  pulse,
  work,
  confirm,
  refresh,
  wide,
  roomy,
}: ProjectProps & { wide: boolean; roomy: boolean }) {
  const { verbs, progressive, gate } = useFleetVerbs(deployment, confirm, refresh)
  const now = useNow(1000, Boolean(deployment.activeRun))
  const recent = deployment.recentRuns ?? []
  const traffic = pulse?.status === "available" ? pulse : undefined
  const run = deployment.activeRun
  const when = (
    <span className="numeric">
      {run
        ? runClock(run, work, now)
        : relativeTime(
            deployment.lastRun?.endedAt ?? deployment.lastRun?.requestedAt ?? deployment.updatedAt,
          )}
    </span>
  )
  const history = run ? (
    <MiniReleasePath
      currentStep={work?.currentStep ?? run.currentStep?.key}
      currentStatus={work?.currentStatus ?? run.currentStep?.state}
    />
  ) : (
    recent.length > 0 && <RunStrip runs={recent.slice(0, 14)} />
  )
  // In flight, the state is the stage the release is at, lit: "Deploying"
  // beside a sweeping release path says the same thing twice. A narrow row
  // keeps the short word as its state and puts the stage with the path.
  const stage = run && (
    <TextShimmer className="truncate text-xs font-medium">{stageOf(run, work)}</TextShimmer>
  )
  const status = <FleetStatus deployment={deployment} progressive={progressive} />
  const pending = deployment.pendingChanges && !run && (
    <Status tone="warning" label="Changes pending" />
  )
  const row = (
    <ChoiceRow
      href={`/deploy/${deployment.id}`}
      verb={`Open ${deployment.name}`}
      leading={<ProjectMark deployment={deployment} size="sm" />}
      title={deployment.name}
      description={<SourceSummary deployment={deployment} compact />}
      trailing={
        wide ? (
          <>
            <span className="flex w-32 min-w-0 flex-col gap-0.5">
              {stage ? (
                <span className="inline-flex min-w-0 items-center gap-1.5">
                  <StatusDot tone="warning" />
                  {stage}
                </span>
              ) : (
                status
              )}
              {pending}
            </span>
            <span className="flex w-48 min-w-0">{traffic && <RowTraffic pulse={traffic} />}</span>
            <span className="flex w-28 items-center">{history}</span>
            <span className="w-16 text-right text-hint text-muted-foreground">{when}</span>
          </>
        ) : (
          roomy && status
        )
      }
      actions={
        // A fixed measure where the readings are columns, so a row without a
        // site to visit keeps them in line with the rows that have one.
        <VerbActions
          dim
          verbs={verbs}
          menuLabel={`Actions for ${deployment.name}`}
          className={cn(wide && "w-16.5 justify-end")}
        />
      }
    >
      {!wide && (
        <div className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1.5 text-hint text-muted-foreground sm:pl-11">
          {!roomy && status}
          <Address deployment={deployment} />
          {pending}
          {traffic && <RowTraffic pulse={traffic} />}
          {history}
          {stage}
          {when}
        </div>
      )}
    </ChoiceRow>
  )
  // Beside the row rather than inside it: a press in the dialog would reach
  // the row's own handler through React's tree and open the project.
  return gate ? (
    <>
      {row}
      {gate}
    </>
  ) : (
    row
  )
}
