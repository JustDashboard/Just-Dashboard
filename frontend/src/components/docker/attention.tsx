"use client"

import { useMemo, useState } from "react"
import { CheckCircle, ChevronDown, Cross, RefreshClockwise } from "@/components/icons"
import { cn } from "@/lib/utils"
import { useViewState } from "@/lib/view-state"
import type {
  AttentionSummary,
  DockerDiagnosis,
  DockerFinding,
  FindingClass,
  RuntimeHealth,
  Severity,
} from "@/lib/types"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Status } from "@/components/status-dot"
import { FindingList, type Finding } from "@/components/finding-list"
import { ExplainIcon } from "@/components/docker/explain"
import { Button } from "@/components/ui/button"
import { HoverCard, HoverCardContent, HoverCardTrigger } from "@/components/ui/hover-card"

/**
 * Two questions that were being answered by one word.
 *
 * The overview used to show "Health: all good" above a containers page listing
 * a privileged container with the Docker socket mounted. Both statements were
 * true of what they described, and the page still contradicted itself, because
 * one label meant "is anything down" and "is anything wrong" at once.
 *
 * They are separated at the source — see dockerx/attention.go — and this
 * renders the second half:
 *
 *   Runtime health is Docker's own report. It clears itself.
 *   Attention is posture, storage, configuration and exposure. It does not.
 *
 * A container can be perfectly healthy and need attention. Saying so is the
 * point.
 *
 * The *list* is no longer this file's business. Docker had grown its own
 * finding row — a two-line title-and-detail block with a severity glyph, a tag
 * and a chevron — while Metrics and Security shared a one-line accordion for
 * exactly the same idea. Two components, two densities, one concept: a page
 * that showed both read as two products. The shared one won on the only test
 * that matters, which is how much of it you can take in at a glance, and this
 * maps Docker's findings onto it. See components/finding-list.tsx.
 */

const SEVERITY_RANK: Record<Severity, number> = {
  critical: 0,
  warning: 1,
  recommendation: 2,
  info: 3,
}

/**
 * Four severities onto the three levels a finding row can draw.
 *
 * A recommendation is not a notice with a different name — it is a notice, and
 * giving it a fourth dot colour would mean four alarm states on a panel whose
 * job is to say which two of them need answering today.
 */
const SEVERITY_LEVEL: Record<Severity, Finding["level"]> = {
  critical: "critical",
  warning: "warning",
  recommendation: "notice",
  info: "notice",
}

const CLASS_LABEL: Record<FindingClass, string> = {
  runtime: "Runtime",
  security: "Security",
  storage: "Storage",
  configuration: "Configuration",
  exposure: "Exposure",
  lifecycle: "Lifecycle",
}

export type FindingAction = (finding: DockerFinding) => void

/**
 * How many rows are shown before the panel asks whether you want the rest.
 *
 * A server with twenty containers produces a list longer than the page it sits
 * on, and everything below it — the containers themselves — stops existing.
 * Five is enough to see what kind of thing is being reported; the rest is one
 * click away and stays there.
 */
const COLLAPSED_ROWS = 5

/**
 * The kind of problem a finding is, independent of which container has it.
 *
 * Finding ids are `container.nohealthcheck.<id>` — scope, kind, target. The
 * first two segments are the kind, and they are what makes "no health check"
 * on five containers one habit rather than five problems.
 */
function findingKind(finding: DockerFinding): string {
  const parts = finding.id.split(".")
  return parts.length > 2 ? parts.slice(0, 2).join(".") : finding.id
}

/**
 * Turns "api has no memory limit" into "have no memory limit", so a group of
 * them reads "5 containers have no memory limit".
 *
 * Derived from the title rather than carried on the wire: the finding already
 * names its target, and a second phrasing field would be one more thing to
 * keep in step with the first. A title that does not start with its target is
 * left alone and shown as-is.
 */
function groupPhrase(findings: DockerFinding[]): string | null {
  const first = findings[0]
  if (!first.target || !first.title.startsWith(first.target + " ")) return null
  const rest = first.title.slice(first.target.length + 1)
  // Third person singular to plural for the two verbs these titles open with.
  // Anything else keeps its own words.
  if (rest.startsWith("has ")) return "have " + rest.slice(4)
  if (rest.startsWith("runs ")) return "run " + rest.slice(5)
  if (rest.startsWith("is ")) return "are " + rest.slice(3)
  return rest
}

type FindingGroup = {
  key: string
  findings: DockerFinding[]
  /** The worst severity in the group, which is the one the row wears. */
  severity: Severity
}

/**
 * Collapses findings of the same kind into one row each.
 *
 * Twenty-six findings across eight containers are typically four habits —
 * no health check, no memory limit, a moving tag, a published port — repeated.
 * Listed flat they read as twenty-six separate problems to solve, which is
 * both wrong and demoralising, and the reader has to hold eight container
 * names in their head to notice the pattern themselves.
 *
 * Order is by worst severity, then by size: the thing that is broken stays
 * above the thing that is merely widespread.
 */
function groupFindings(findings: DockerFinding[]): FindingGroup[] {
  const groups = new Map<string, FindingGroup>()
  for (const finding of findings) {
    const key = findingKind(finding)
    const existing = groups.get(key)
    if (existing) {
      existing.findings.push(finding)
      if (SEVERITY_RANK[finding.severity] < SEVERITY_RANK[existing.severity]) {
        existing.severity = finding.severity
      }
      continue
    }
    groups.set(key, { key, findings: [finding], severity: finding.severity })
  }
  return [...groups.values()].sort((a, b) => {
    const rank = SEVERITY_RANK[a.severity] - SEVERITY_RANK[b.severity]
    return rank !== 0 ? rank : b.findings.length - a.findings.length
  })
}

/**
 * One group as one row.
 *
 * `meta` is the class rather than the target, deliberately: a single finding's
 * title already opens with the container's name, so repeating it on the right
 * would print the same word twice on one line. What the right-hand column adds
 * is the *kind* of problem, which is the thing a reader scans a column of
 * these for.
 */
function toFinding(group: FindingGroup, onAction?: FindingAction): Finding {
  const first = group.findings[0]
  const level = SEVERITY_LEVEL[group.severity] ?? "notice"
  const meta = CLASS_LABEL[first.class] ?? first.class

  if (group.findings.length === 1) {
    return {
      id: first.id,
      level,
      title: first.title,
      detail: first.detail,
      advice: first.advice,
      meta,
      action:
        first.action && onAction
          ? { label: first.actionLabel ?? "Fix this", onClick: () => onAction(first) }
          : undefined,
    }
  }

  const phrase = groupPhrase(group.findings)
  return {
    id: group.key,
    level,
    title: phrase
      ? `${group.findings.length} containers ${phrase}`
      : `${first.title} (${group.findings.length} containers)`,
    // Stated once, because it is the same sentence on every member. Repeating
    // it per container is how a panel of four real problems reads as twenty-six.
    detail: first.detail,
    advice: first.advice,
    meta,
    extra: <Targets group={group} onAction={onAction} />,
  }
}

/**
 * Which containers, as the thing you press to deal with one of them.
 *
 * The group is the summary, never a replacement for knowing which ones — and a
 * name that is only a name makes the reader go and find it themselves. Each
 * chip carries that container's own finding, so pressing it runs that
 * container's remedy where there is one and opens it where there is not.
 */
function Targets({ group, onAction }: { group: FindingGroup; onAction?: FindingAction }) {
  return (
    <div className="flex flex-wrap gap-1">
      {group.findings.map((finding) =>
        onAction ? (
          <button
            key={finding.id}
            type="button"
            onClick={() => onAction(finding)}
            title={finding.actionLabel ?? `Open ${finding.target}`}
            className="rounded-sm bg-surface-sunken px-1.5 py-px font-mono text-micro text-muted-foreground focus-ring transition-colors hover:text-foreground"
          >
            {finding.target}
          </button>
        ) : (
          <span
            key={finding.id}
            className="rounded-sm bg-surface-sunken px-1.5 py-px font-mono text-micro text-muted-foreground"
          >
            {finding.target}
          </span>
        ),
      )}
    </div>
  )
}

/**
 * The attention list: everything that is not a runtime fact.
 *
 * Runtime findings are deliberately excluded — they belong to the runtime
 * health summary, which counts what is fine as well as what is not, and a list
 * of problems can never do that. `includeRuntime` puts them back for the
 * container detail panel, where the reader has asked about one container and
 * wants everything known about it in one place.
 *
 * Three things this panel used to carry and no longer does, all of them chrome
 * that outweighed the four rows it was framing: a severity filter strip (four
 * chips to sort a list capped at five), a "N distinct" counter explaining the
 * grouping, and a Hide button for a panel whose whole point is to be read.
 * What is left is a header that says how bad it is and a list you can scan.
 */
export function AttentionPanel({
  diagnosis,
  onAction,
  includeRuntime = false,
  onRescan,
  className,
}: {
  diagnosis: DockerDiagnosis | undefined
  onAction?: FindingAction
  includeRuntime?: boolean
  /** Re-runs the diagnosis pass (the page's refresh) — also restores dismissed rows. */
  onRescan?: () => void
  className?: string
}) {
  const [showAll, setShowAll] = useState(false)
  // Dismissed finding kinds, kept in the browser. A dismissal hides the row
  // until the next rescan — the finding itself is untouched on the server, so
  // "bring them back" is clearing this list and polling again. No API change:
  // the diagnosis is already recomputed live on every poll.
  const [dismissed, setDismissed] = useViewState<string[]>("docker.attention.dismissed", [])

  const findings = useMemo(
    () => (diagnosis?.findings ?? []).filter((f) => includeRuntime || f.class !== "runtime"),
    [diagnosis, includeRuntime],
  )
  const groups = useMemo(
    () => groupFindings(findings).filter((g) => !dismissed.includes(g.key)),
    [findings, dismissed],
  )
  const rows = useMemo(
    () => (showAll ? groups : groups.slice(0, COLLAPSED_ROWS)).map((g) => toFinding(g, onAction)),
    [groups, showAll, onAction],
  )

  if (!diagnosis) return null

  const issues = findings.filter(
    (f) => f.severity === "critical" || f.severity === "warning",
  ).length
  const recommendations = findings.length - issues
  const critical = findings.some((f) => f.severity === "critical")

  // Nothing to act on is a one-line answer, and it used to be a paragraph
  // inside a body — a fifth of the overview page spent saying "no".
  if (findings.length === 0) {
    return (
      <Panel className={className}>
        <PanelHeader
          title={<PanelTitle />}
          actions={<Status verdict="ok" icon={CheckCircle} label="Nothing to act on" />}
        />
      </Panel>
    )
  }

  // Everything currently visible is dismissed. The findings are still there —
  // rescan brings them straight back.
  if (groups.length === 0) {
    const rescan = () => {
      setDismissed([])
      onRescan?.()
    }
    return (
      <Panel className={className}>
        <PanelHeader
          title={<PanelTitle />}
          actions={
            <Button size="xs" variant="ghost" onClick={rescan} className="text-muted-foreground">
              <RefreshClockwise className="size-3" />
              Rescan
            </Button>
          }
        />
        <PanelBody>
          <p className="text-hint text-muted-foreground">
            {findings.length} {findings.length === 1 ? "finding" : "findings"} dismissed. They come
            back on the next rescan.
          </p>
        </PanelBody>
      </Panel>
    )
  }

  const dismissOne = (key: string) => setDismissed((prev) => (prev.includes(key) ? prev : [...prev, key]))
  const dismissAll = () =>
    setDismissed((prev) => [...new Set([...prev, ...groups.map((g) => g.key)])])
  const rescan = () => {
    setDismissed([])
    onRescan?.()
  }

  const rest = groups.length - rows.length
  const headerLabel =
    issues > 0 && recommendations > 0
      ? `${issues} ${issues === 1 ? "issue" : "issues"} · ${recommendations} ${recommendations === 1 ? "recommendation" : "recommendations"}`
      : issues > 0
        ? `${issues} ${issues === 1 ? "issue" : "issues"}`
        : `${findings.length} ${findings.length === 1 ? "recommendation" : "recommendations"}`

  return (
    <Panel className={className}>
      <PanelHeader
        title={<PanelTitle />}
        actions={
          <span className="flex shrink-0 flex-wrap items-center gap-1.5">
            <Status
              verdict={critical ? "critical" : issues > 0 ? "warning" : "notice"}
              label={headerLabel}
            />
            <Button
              size="xs"
              variant="ghost"
              onClick={rescan}
              aria-label="Rescan for attention items"
              title="Clear dismissals and check again"
              className="text-muted-foreground"
            >
              <RefreshClockwise className="size-3" />
              Rescan
            </Button>
            <Button
              size="xs"
              variant="ghost"
              onClick={dismissAll}
              aria-label="Dismiss all attention items"
              title="Hide every item until the next rescan"
              className="text-muted-foreground"
            >
              <Cross className="size-3" />
              Dismiss all
            </Button>
          </span>
        }
      />
      <PanelBody>
        <FindingList findings={rows} onDismiss={dismissOne} />
        {(rest > 0 || showAll) && (
          <Button
            size="xs"
            variant="ghost"
            className="mt-2 w-full py-1.5 text-muted-foreground"
            onClick={() => setShowAll(!showAll)}
          >
            {rest > 0 ? `Show ${rest} more` : "Show less"}
            <ChevronDown className={cn("transition-transform", showAll && "rotate-180")} />
          </Button>
        )}
      </PanelBody>
    </Panel>
  )
}

function PanelTitle() {
  return (
    <span className="inline-flex items-center gap-1.5">
      Attention
      <ExplainIcon name="attention" />
    </span>
  )
}

/**
 * Runtime health: one bar, and the numbers that make it honest.
 *
 * This was four figures in a 2×4 grid — a panel a hundred and forty pixels tall
 * saying "2 / 2, 1, 0, 1" above the containers it was describing, which on a
 * phone is most of a screen spent before the list the page is *for* begins.
 *
 * The counts matter as much as the status and none of them has been dropped:
 * a list of problems cannot say "eight healthy, four with no health check at
 * all", and that last number is what stops "all healthy" meaning "nothing is
 * being watched". What changed is that they are now read off one bar rather
 * than out of four boxes — which is also the only form in which the
 * relationship between them is visible at all. Four numbers in a row do not
 * show you that half the estate is unwatched; a bar that is half grey does.
 *
 * The track is the containers that are not running. That is not a decorative
 * choice: the bar answers "of everything on this server, how much is up and
 * actually being checked", and an unfilled track is exactly what "not up"
 * looks like.
 */
const RUNTIME_SEGMENTS = [
  {
    key: "healthy",
    label: "passing a health check",
    fill: "bg-success",
    swatch: "bg-success",
    of: (r: RuntimeHealth) => r.healthy,
    explain: "A check the image ships is running inside the container and answering.",
  },
  {
    key: "starting",
    label: "still starting",
    fill: "bg-warning",
    swatch: "bg-warning",
    of: (r: RuntimeHealth) => r.starting,
    explain: "Inside its health check's grace period. Not yet a verdict either way.",
  },
  {
    key: "unhealthy",
    label: "failing one",
    fill: "bg-destructive",
    swatch: "bg-destructive",
    of: (r: RuntimeHealth) => r.unhealthy,
    explain: "The container is up and its own health check says it is not working.",
  },
  {
    key: "noHealthcheck",
    label: "without one",
    fill: "bg-muted-foreground",
    swatch: "bg-muted-foreground",
    of: (r: RuntimeHealth) => r.noHealthcheck,
    explain:
      "Docker reports these as up whenever their main process is alive — a wedged application answering nothing still counts.",
  },
] as const

export function RuntimeHealthPanel({
  runtime,
  className,
}: {
  runtime: RuntimeHealth | undefined
  className?: string
}) {
  if (!runtime) return null
  const total = Math.max(runtime.total, 1)
  const stopped = runtime.total - runtime.running
  const segments = RUNTIME_SEGMENTS.map((s) => ({ ...s, count: s.of(runtime) }))

  return (
    <Panel className={className}>
      <PanelHeader
        title={
          <span className="inline-flex items-center gap-1.5">
            Runtime health
            <ExplainIcon name="runtimeHealth" />
          </span>
        }
        actions={
          <>
            <Status
              verdict={runtime.status === "ok" ? "ok" : runtime.status}
              label={runtimeLabel(runtime)}
            />
            <span className="numeric text-hint text-muted-foreground">
              {runtime.running} of {runtime.total} running
            </span>
          </>
        }
      />
      <PanelBody className="space-y-2.5">
        <div
          className="flex h-2 w-full overflow-hidden rounded-full bg-meter-track"
          role="img"
          aria-label={`${runtime.running} of ${runtime.total} containers running; ${segments
            .filter((s) => s.count > 0)
            .map((s) => `${s.count} ${s.label}`)
            .join(", ")}`}
        >
          {segments.map((segment) =>
            segment.count > 0 ? (
              <span
                key={segment.key}
                className={cn(
                  "h-full transition-[width] first:rounded-l-full last:rounded-r-full",
                  segment.fill,
                )}
                style={{ width: `${(segment.count / total) * 100}%` }}
              />
            ) : null,
          )}
        </div>

        {/*
          Every segment, including the empty ones. A count of zero failing
          health checks is a fact worth printing — a legend that only listed
          what happened to be non-zero would quietly stop mentioning the
          category the moment it went right, which is the moment it becomes
          reassuring.
        */}
        <ul className="flex flex-wrap gap-x-4 gap-y-1.5">
          {segments.map((segment) => (
            <RuntimeLegend
              key={segment.key}
              swatch={segment.swatch}
              count={segment.count}
              label={segment.label}
              explain={segment.explain}
            />
          ))}
          {stopped > 0 && (
            <RuntimeLegend
              swatch="bg-meter-track"
              count={stopped}
              label="not running"
              explain="Stopped, exited or never started. Nothing is wrong with them — they are simply not up."
            />
          )}
        </ul>
      </PanelBody>
    </Panel>
  )
}

/**
 * One reading in the legend, with the sentence behind it one hover away.
 *
 * "Without one" is the label that most needs explaining and the one with least
 * room to do it, which is precisely the shape a hover card exists for.
 */
function RuntimeLegend({
  swatch,
  count,
  label,
  explain,
}: {
  swatch: string
  count: number
  label: string
  explain: string
}) {
  return (
    <li className="min-w-0">
      <HoverCard openDelay={200}>
        <HoverCardTrigger asChild>
          <button
            type="button"
            className="flex cursor-help items-center gap-1.5 rounded-sm focus-ring"
          >
            <span aria-hidden className={cn("size-1.5 shrink-0 rounded-full", swatch)} />
            <span
              className={cn(
                "numeric text-body font-medium",
                count === 0 && "text-muted-foreground",
              )}
            >
              {count}
            </span>
            <span className="truncate text-hint text-muted-foreground">{label}</span>
          </button>
        </HoverCardTrigger>
        <HoverCardContent className="w-72 text-xs leading-relaxed text-muted-foreground">
          {explain}
        </HoverCardContent>
      </HoverCard>
    </li>
  )
}

/**
 * The findings that concern one container, for its own detail panel.
 *
 * The same objects the page-level list renders, filtered rather than fetched
 * again: the diagnosis is one pass over every container, and asking for it per
 * panel would turn a single query into one per row.
 */
export function ContainerFindings({
  diagnosis,
  containerId,
  onAction,
}: {
  diagnosis: DockerDiagnosis | undefined
  containerId: string
  onAction?: FindingAction
}) {
  const mine = (diagnosis?.findings ?? []).filter((f) => f.targetId === containerId)
  if (mine.length === 0) return null

  const rows = mine
    .slice()
    .sort((a, b) => SEVERITY_RANK[a.severity] - SEVERITY_RANK[b.severity])
    .map((finding) =>
      toFinding({ key: finding.id, findings: [finding], severity: finding.severity }, onAction),
    )
  const issues = mine.filter((f) => f.severity === "critical" || f.severity === "warning").length
  const recommendations = mine.length - issues

  return (
    <Panel>
      <PanelHeader
        title={<PanelTitle />}
        actions={
          <Status
            verdict={
              mine.some((f) => f.severity === "critical")
                ? "critical"
                : issues > 0
                  ? "warning"
                  : "notice"
            }
            label={
              issues > 0 && recommendations > 0
                ? `${issues} ${issues === 1 ? "issue" : "issues"} · ${recommendations} ${recommendations === 1 ? "recommendation" : "recommendations"}`
                : issues > 0
                  ? `${issues} ${issues === 1 ? "issue" : "issues"}`
                  : `${mine.length} ${mine.length === 1 ? "recommendation" : "recommendations"}`
            }
          />
        }
      />
      <PanelBody>
        <FindingList findings={rows} />
      </PanelBody>
    </Panel>
  )
}

/**
 * The runtime-health verdict for a tile. Deliberately about runtime only —
 * this is the label that used to read "All good" over a page of security
 * warnings, because it was fed the worst of everything.
 */
export function runtimeLabel(runtime: RuntimeHealth | undefined): string {
  if (!runtime || runtime.total === 0) return "Nothing running"
  switch (runtime.status) {
    case "critical":
      return "Something is failing"
    case "warning":
      return "Something is unsettled"
    case "notice":
      return "Something is stopped"
    default:
      return `${runtime.running} / ${runtime.total} running`
  }
}

/** The attention verdict for a tile, which is never the word "health". */
export function attentionLabel(attention: AttentionSummary | undefined): string {
  if (!attention || attention.total === 0) return "Nothing to act on"
  if (attention.issues > 0) {
    return `${attention.issues} ${attention.issues === 1 ? "issue" : "issues"}`
  }
  return `${attention.recommendations} ${attention.recommendations === 1 ? "recommendation" : "recommendations"}`
}
