"use client"

import { useMemo, useState } from "react"
import { ChevronDown, Cross, RefreshClockwise } from "@/components/icons"
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
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"

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

export type FindingAction = ((finding: DockerFinding) => void) & {
  label?: (finding: DockerFinding) => string
}

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
      id: group.key,
      level,
      title: first.title,
      detail: first.detail,
      advice: first.advice,
      meta,
      action:
        (first.action || first.targetId) && onAction
          ? {
              label: onAction.label?.(first) ?? first.actionLabel ?? "Inspect",
              onClick: () => onAction(first),
            }
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
          // A container's name is a literal string from the host, so it is
          // drawn the way every other one is — a `mono` tag — and the press is
          // the tag itself rather than a control beside it.
          <Tag
            key={finding.id}
            mono
            asChild
            className="focus-ring transition-colors hover:bg-accent hover:text-foreground"
          >
            <button
              type="button"
              onClick={() => onAction(finding)}
              title={onAction.label?.(finding) ?? finding.actionLabel ?? `Open ${finding.target}`}
            >
              {finding.target}
            </button>
          </Tag>
        ) : (
          <Tag key={finding.id} mono>
            {finding.target}
          </Tag>
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

  // Plain, like the Health list on the Overview it is the Docker twin of: a
  // titled list of findings on the page, not a box of them above the
  // containers. The whole block rises once, when the diagnosis lands.
  //
  // Nothing to act on is a one-line answer, drawn the way `FindingList` draws
  // every other clean verdict — a check and a sentence — rather than a header
  // with nothing under it.
  if (findings.length === 0) {
    return (
      <Panel plain className={cn("animate-rise", className)}>
        <PanelHeader title={<PanelTitle />} />
        <PanelBody>
          <DiagnosisSilences diagnosis={diagnosis} />
          <FindingList
            findings={[]}
            emptyLabel={
              diagnosis.silences?.length
                ? "No findings in completed checks"
                : "Nothing to act on — posture, storage, configuration and exposure are all as they should be"
            }
          />
        </PanelBody>
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
      <Panel plain className={cn("animate-rise", className)}>
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
          <p className="text-body text-muted-foreground">
            {findings.length} {findings.length === 1 ? "finding" : "findings"} dismissed. They come
            back on the next rescan.
          </p>
        </PanelBody>
      </Panel>
    )
  }

  const dismissOne = (key: string) =>
    setDismissed((prev) => (prev.includes(key) ? prev : [...prev, key]))
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
    <Panel plain className={cn("animate-rise", className)}>
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
        <DiagnosisSilences diagnosis={diagnosis} />
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

function DiagnosisSilences({ diagnosis }: { diagnosis: DockerDiagnosis }) {
  if (!diagnosis.silences?.length) return null
  return (
    <div className="mb-3 text-hint text-muted-foreground">
      <p className="font-medium">Not assessed</p>
      <ul className="list-disc pl-4">
        {diagnosis.silences.map((silence) => (
          <li key={silence}>{silence}</li>
        ))}
      </ul>
    </div>
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
    // Plain inside the detail panel too: the side panel is already the frame,
    // and a box inside it was a box inside a box.
    <Panel plain>
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
