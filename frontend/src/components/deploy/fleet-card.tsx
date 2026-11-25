"use client"

import Link from "next/link"
import { useRouter } from "next/navigation"
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
import { CONTROL } from "@/components/choice-row"
import { BranchChip } from "@/components/git/marks"
import { Sparkline } from "@/components/sparkline"
import { ProductGlyph, ProductGlyphs, imageProducts } from "@/components/product-logo"
import { Status, StatusDot } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { VerbActions } from "@/components/verbs"
import { BlurFade } from "@/components/ui/blur-fade"
import { BorderBeam } from "@/components/ui/border-beam"
import { SpotlightBorder } from "@/components/ui/spotlight-border"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { ProjectMark } from "@/components/project-mark"
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
  projectState,
  reachableAt,
  runDurationSeconds,
  runTriggerLine,
  shortRevision,
  sourceProduct,
  stepName,
} from "@/components/deploy/vocabulary"
import { useNow } from "@/hooks/use-now"
import { FAILING_NOTICE, failingTone } from "@/components/deploy/fleet"

/**
 * One project in the fleet, as a card in the grid. The fleet has no list
 * layout: the operator asked for cards only (25 Nov 2026).
 *
 * A card is a destination — it opens the project — so it carries the lit edge
 * §16 gives to things you take, and draws the project as what it is, in the
 * colours that are the data's own: the product it runs, where its source
 * lives, how much traffic it is taking and what share of it fails, the last
 * fourteen runs as a strip and, while one is in flight, the release path
 * sweeping and the stage it is at lit.
 *
 * Three weights, read in this order: the name, then the traffic figure, both
 * in the title's size; everything else is the muted hint. The card does not
 * draw the last commit (who wrote what, under its subject) or the open pull
 * requests: the commit line was a sentence nobody read on a card, and a count
 * of pull requests is the project's Overview's to say. The Pull requests chip
 * above the grid still narrows the fleet to the projects that have one.
 *
 * The verbs are the project page's own (`useProjectVerbs`), so the card and
 * the project header cannot disagree about what can be done to a project. The
 * card's host line is already the link to the site, so Visit heads the menu's
 * Project group rather than drawing a glyph of its own. There is no arrow:
 * the whole card and its name open the project.
 */

/**
 * A card's height, which every card keeps whatever it says. Its placeholder,
 * on the fleet and on the Overview, takes the same, so a card lands where its
 * ghost stood (§11).
 */
export const PROJECT_CARD_HEIGHT = "h-45.5"

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

/**
 * The project's state under its name. Ready is the expected answer, so it is
 * the dot alone, named for a screen reader and the pointer: "Ready" on every
 * card was a word nobody read, and it took the room the host needs. Any other
 * state, and a verb on its way, says its word.
 */
function CardState({
  deployment,
  progressive,
}: {
  deployment: DeploymentSummary
  progressive?: string
}) {
  if (progressive) return <Status tone="warning" label={progressive} className="shrink-0" />
  if (projectState(deployment) === "ready")
    return (
      <span data-slot="project-state" title="Ready" className="flex shrink-0">
        <StatusDot tone="running" label="Ready" />
      </span>
    )
  return <ProjectStatus summary={deployment} className="shrink-0" />
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
      <TextShimmer className="min-w-0 truncate text-hint font-medium">
        {stageOf(run, work)}
      </TextShimmer>
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
 * Where the project answers, when it answers anywhere: its host as the link
 * to the site, its port, or that it is private. Mono only for the literal the
 * host gave — "Private" is a word. A project that has never been deployed
 * says so in its state, so it has no second line saying it again.
 */
function Address({ deployment }: { deployment: DeploymentSummary }) {
  const url = deploymentURL(deployment.endpoint)
  if (url)
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
  if (!deployment.liveReleaseId) return null
  const literal = deployment.endpoint || deployment.hostPort || deployment.internalPort
  return (
    <span className={cn("min-w-0 truncate", literal && "font-mono")}>
      {reachableAt(deployment)}
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
  if (!last) return <span className="truncate">Updated {relativeTime(deployment.updatedAt)}</span>
  const at = last.endedAt ?? last.requestedAt
  return (
    <span className="inline-flex min-w-0 items-center gap-1.5" title={runTriggerLine(last)}>
      <RunActorMark run={last} remote={deployment.sourceRemote} size="xs" />
      <time dateTime={at} title={timestamp(at)} className="truncate">
        {operationLabel(last.operation)} {relativeTime(at)}
      </time>
    </span>
  )
}

/** The image an image project pulls, as it was configured. */
function imageReference(d: DeploymentSummary) {
  return d.sourceRepository || d.sourceRef || shortRevision(d.sourceRevision)
}

/**
 * Where the project comes from, on one line: the forge, the repository and
 * the branch; the image it pulls; or the Compose stack and how many services
 * it runs. What several services run is drawn as their products. The line is
 * a fixed height on every card, so a row of them keeps one height whatever
 * each was deployed from.
 */
function SourceLine({ deployment: d }: { deployment: DeploymentSummary }) {
  const services = (d.serviceCount ?? 0) > 1 ? plural(d.serviceCount ?? 0, "service") : undefined
  const runs = imageProducts(d.images ?? []).filter((id) => id !== "docker")
  let line: React.ReactNode
  switch (d.sourceKind) {
    case "git":
    case "local": {
      const host = sourceProduct(d)
      line = (
        <>
          {host && <ProductGlyph id={host} />}
          {d.sourceRepository && (
            <span className="min-w-0 truncate font-mono">{d.sourceRepository}</span>
          )}
          <BranchChip branch={d.sourceRef || "main"} className="max-w-[10rem] shrink-0" />
          {services && <span className="shrink-0">{services}</span>}
          {services && <ProductGlyphs ids={runs} max={3} />}
        </>
      )
      break
    }
    case "image": {
      const product = sourceProduct(d)
      line = (
        <>
          <ProductGlyph id={product ?? "docker"} />
          <span className="min-w-0 truncate font-mono">{imageReference(d) || "Docker image"}</span>
        </>
      )
      break
    }
    case "compose":
      // The file's name is not worth a glance once several services run
      // from it; how many, and what they are, is.
      line = (
        <>
          <span className="min-w-0 truncate">
            Compose stack{services ? ` · ${services}` : d.sourceRef ? ` · ${d.sourceRef}` : ""}
          </span>
          <ProductGlyphs ids={runs} max={3} />
        </>
      )
      break
    case "blueprint":
      line = (
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
      break
    default:
      line = <span className="min-w-0 truncate">{WORKLOAD_LABELS[d.profile]}</span>
  }
  return (
    <p
      data-slot="project-source"
      className="flex h-4.5 min-w-0 items-center gap-1.5 overflow-hidden text-hint text-muted-foreground"
    >
      {line}
    </p>
  )
}

/**
 * A rate a minute, as the card's one figure: the number in the name's size
 * and weight, its unit small beside it, so the eye goes from the name to the
 * figure and the rest of the card is read only when wanted.
 */
function Rate({ figure, muted }: { figure: string; muted?: boolean }) {
  return (
    <span
      className={cn(
        "numeric text-title leading-5 font-semibold tracking-tight",
        muted ? "text-muted-foreground" : "text-foreground",
      )}
    >
      {figure}
      <span className="ml-0.5 text-hint font-normal tracking-normal text-muted-foreground">
        /min
      </span>
    </span>
  )
}

/**
 * The last hour at the ingress: how many requests a minute and what share
 * failed, then the hour as a line that runs out to the card's edge and stands
 * on the footer's rule. The line keeps its series colour; the only red is the
 * failing share, attached to its figure (§3), and the line rises once when its
 * hour of history lands (§11).
 *
 * The band is drawn on every card at one height, so a row of cards keeps one
 * height and its footers one baseline. A project with no address on the web —
 * a worker, a game server — says so over a flat rule; one with an address and
 * no hour yet draws the rule beside an empty figure.
 */
function CardTraffic({
  deployment,
  pulse,
}: {
  deployment: DeploymentSummary
  pulse?: TrafficPulse
}) {
  const figure = "flex min-w-18 shrink-0 flex-col justify-end gap-0.5 pb-2"
  if (pulse?.status !== "available")
    return (
      <div data-slot="project-traffic" className="flex h-12 min-w-0 items-end gap-3">
        <span className={cn(figure, "text-hint leading-4 text-muted-foreground")}>
          {deploymentURL(deployment.endpoint) ? <Rate figure="—" muted /> : "No web traffic"}
        </span>
        <span aria-hidden className="-mr-3.5 mb-2.5 flex-1 border-t border-dashed" />
      </div>
    )
  return (
    <div data-slot="project-traffic" className="flex h-12 min-w-0 animate-rise items-end gap-3">
      <span className={figure}>
        <Rate figure={perMinute(pulse.perMinute)} />
        {pulse.errorRate >= FAILING_NOTICE ? (
          <FailingShare rate={pulse.errorRate} className="text-hint leading-4" />
        ) : (
          <span className="numeric text-hint leading-4 text-muted-foreground">
            {plural(pulse.pages, "view")}
          </span>
        )}
      </span>
      <Sparkline
        values={pulse.points}
        width={240}
        height={48}
        className="-mr-3.5 h-12 min-w-0 flex-1"
        label="Requests per minute, last hour"
      />
    </div>
  )
}

/**
 * A card in the grid.
 *
 * Built the way `git/repo-card.tsx` builds a destination rather than as a
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
  // drawing a glyph for it beside the menu.
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
    <li className="min-w-0" data-slot="project-card">
      {/* Each card lands a beat after the one before it, capped so a fleet
          of forty does not take three seconds. */}
      <BlurFade delay={Math.min(index, 11) * 0.045} className="h-full">
        <SpotlightBorder className="h-full">
          {active && (
            <span aria-hidden className="pointer-events-none absolute -inset-px rounded-xl">
              <BorderBeam size={80} duration={4} />
            </span>
          )}
          {/* eslint-disable-next-line jsx-a11y/click-events-have-key-events, jsx-a11y/no-static-element-interactions -- the pointer's larger target: the card's name is the link or button a keyboard opens it by */}
          <div
            onClick={(event) => {
              // React carries a press inside the open menu — a group label,
              // a separator — up through its portal to here, though it
              // landed nowhere on the card.
              const target = event.target as HTMLElement
              if (!event.currentTarget.contains(target) || target.closest(CONTROL)) return
              router.push(base)
            }}
            className="group group/choice flex h-full min-w-0 cursor-pointer flex-col rounded-xl"
          >
            {/* Every part of the card is drawn at a fixed height and every
                card draws every part, so a row of them is one height with
                nothing stretched and no gap opened where one project has
                less to say than its neighbour. */}
            <div className="flex min-w-0 flex-col gap-3 px-3.5 pt-3.5">
              {/* The state sits under the name rather than beside it: four
                  cards to a row leave a name beside a state and two verbs
                  about a hundred pixels, and the name is what is read. The
                  verbs take the name's line only, so the state and the host
                  run on under them to the card's edge. */}
              <div className="grid h-10 min-w-0 grid-cols-start-main-end items-center gap-x-3 gap-y-0.5">
                <ProjectMark deployment={deployment} size="md" className="row-span-2" />
                <Link
                  href={base}
                  className="block max-w-full min-w-0 self-end truncate rounded-sm text-title leading-tight font-semibold tracking-tight focus-ring"
                >
                  {deployment.name}
                </Link>
                {/* No arrow at the head (25 Nov 2026, the operator's word, as on
                    the Databases cards): the whole card and its name open it. */}
                <span className="-my-2 -mr-1.5 flex items-center self-end">
                  <VerbActions dim verbs={menu} menuLabel={`Actions for ${deployment.name}`} />
                </span>
                <div className="col-span-2 col-start-2 flex min-w-0 items-center gap-2 self-start text-hint text-muted-foreground">
                  <CardState deployment={deployment} progressive={progressive} />
                  <Address deployment={deployment} />
                </div>
              </div>
              <SourceLine deployment={deployment} />
              <CardTraffic deployment={deployment} pulse={pulse} />
            </div>
            {/* One line that never wraps: the strip and the clock keep their
                width and the words give way, so a card with pending changes
                is as tall as one without. */}
            <div className="mt-auto flex h-9 min-w-0 items-center gap-3 border-t border-hairline px-3.5 text-hint whitespace-nowrap text-muted-foreground">
              {active ? (
                <InFlight deployment={deployment} work={work} now={now} />
              ) : (
                <>
                  {recent.length > 0 && <RunStrip runs={recent.slice(0, 14)} />}
                  <LastActivity deployment={deployment} />
                </>
              )}
              {deployment.pendingChanges && !active && (
                <Status tone="warning" label="Changes pending" className="ml-auto shrink-0" />
              )}
            </div>
          </div>
        </SpotlightBorder>
      </BlurFade>
      {gate}
    </li>
  )
}
