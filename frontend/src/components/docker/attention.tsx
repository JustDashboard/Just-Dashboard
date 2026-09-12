"use client"

import { useId, useMemo, useState } from "react"
import {
  CheckCircle,
  ChevronDown,
  CrossCircle,
  Heart,
  Information,
  Lifebuoy,
  Warning,
} from "@/components/icons"
import { cn } from "@/lib/utils"
import type {
  AttentionSummary,
  DockerDiagnosis,
  DockerFinding,
  FindingClass,
  RuntimeHealth,
  Severity,
} from "@/lib/types"
import { Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { ChipCount, FilterChip } from "@/components/tabs"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { EmptyNote } from "@/components/state"
import { Button } from "@/components/ui/button"
import { useViewState } from "@/lib/view-state"

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
 */

const SEVERITY: Record<
  Severity,
  { icon: React.ComponentType<{ className?: string }>; tone: string; label: string; rank: number }
> = {
  critical: { icon: CrossCircle, tone: "text-destructive", label: "Critical", rank: 0 },
  warning: { icon: Warning, tone: "text-warning", label: "Warning", rank: 1 },
  recommendation: { icon: Information, tone: "text-primary", label: "Recommendation", rank: 2 },
  info: { icon: Information, tone: "text-muted-foreground", label: "Info", rank: 3 },
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
 * Four is enough to see what kind of thing is being reported; the rest is one
 * click away and stays there.
 */
const COLLAPSED_ROWS = 4

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
      if (SEVERITY[finding.severity].rank < SEVERITY[existing.severity].rank) {
        existing.severity = finding.severity
      }
      continue
    }
    groups.set(key, { key, findings: [finding], severity: finding.severity })
  }
  return [...groups.values()].sort((a, b) => {
    const rank = SEVERITY[a.severity].rank - SEVERITY[b.severity].rank
    return rank !== 0 ? rank : b.findings.length - a.findings.length
  })
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
 * It is a summary that opens, not a page section: the header carries the count
 * and the worst severity, and the list under it is capped, because a panel
 * whose height is "however many findings there happen to be" pushes the
 * containers it is describing off the screen.
 */
export function AttentionPanel({
  diagnosis,
  onAction,
  includeRuntime = false,
  className,
}: {
  diagnosis: DockerDiagnosis | undefined
  onAction?: FindingAction
  includeRuntime?: boolean
  className?: string
}) {
  const [severity, setSeverity] = useState<Severity | null>(null)
  const [showAll, setShowAll] = useState(false)
  // Furniture, not a question: whether this list is worth its height is decided
  // once, and a page that re-expanded it on every visit would be ignoring that.
  const [open, setOpen] = useViewState("docker.attention.open", true)
  const bodyId = useId()

  const findings = useMemo(() => {
    const all = (diagnosis?.findings ?? []).filter((f) => includeRuntime || f.class !== "runtime")
    return severity ? all.filter((f) => f.severity === severity) : all
  }, [diagnosis, includeRuntime, severity])

  const counts = useMemo(() => {
    const out: Partial<Record<Severity, number>> = {}
    for (const f of diagnosis?.findings ?? []) {
      if (!includeRuntime && f.class === "runtime") continue
      out[f.severity] = (out[f.severity] ?? 0) + 1
    }
    return out
  }, [diagnosis, includeRuntime])

  const groups = useMemo(() => groupFindings(findings), [findings])

  const total = Object.values(counts).reduce<number>((a, b) => a + (b ?? 0), 0)
  const issues = (counts.critical ?? 0) + (counts.warning ?? 0)

  if (!diagnosis) return null

  // Nothing to act on is a one-line answer, and it used to be a paragraph
  // inside a body — a fifth of the overview page spent saying "no".
  if (total === 0) {
    return (
      <Panel className={className}>
        <PanelHeader
          icon={Lifebuoy}
          title="Attention"
          actions={<Status verdict="ok" icon={CheckCircle} label="Nothing to act on" />}
        />
      </Panel>
    )
  }

  const shown = showAll ? groups : groups.slice(0, COLLAPSED_ROWS)
  const rest = groups.length - shown.length

  return (
    <Panel className={className}>
      <PanelHeader
        icon={Lifebuoy}
        title="Attention"
        actions={
          <>
            {issues > 0 && (
              <Status
                verdict={counts.critical ? "critical" : "warning"}
                label={`${issues} ${issues === 1 ? "issue" : "issues"}`}
              />
            )}
            {/* The list is deduplicated: one problem hitting five containers is
                one row, not five. Without this the reader compares the issue
                count above with a much shorter list and assumes something was
                dropped. It was a sentence under the title; it is a number, so
                it belongs beside the other numbers. */}
            {groups.length < total && (
              <span className="numeric text-hint text-muted-foreground">
                {groups.length} distinct
              </span>
            )}
            <Button
              size="xs"
              variant="ghost"
              className="text-muted-foreground"
              onClick={() => setOpen(!open)}
              aria-expanded={open}
              aria-controls={bodyId}
            >
              {open ? "Hide" : `Show ${total}`}
              <ChevronDown className={cn("transition-transform", open && "rotate-180")} />
            </Button>
          </>
        }
      />
      {open && (
        <div id={bodyId} className="min-w-0">
          <PanelToolbar>
            <div className="flex flex-wrap gap-1">
              <FilterChip selected={severity === null} onClick={() => setSeverity(null)}>
                All
                <ChipCount>{total}</ChipCount>
              </FilterChip>
              {(["critical", "warning", "recommendation", "info"] as const).map((level) =>
                counts[level] ? (
                  <FilterChip
                    key={level}
                    selected={severity === level}
                    onClick={() => setSeverity(severity === level ? null : level)}
                  >
                    {SEVERITY[level].label}
                    <ChipCount>{counts[level]}</ChipCount>
                  </FilterChip>
                ) : null,
              )}
            </div>
          </PanelToolbar>
          <PanelBody className="space-y-1.5 p-3">
            {findings.length === 0 ? (
              <EmptyNote>Nothing at that severity.</EmptyNote>
            ) : (
              <>
                {shown.map((group) =>
                  group.findings.length === 1 ? (
                    <FindingRow key={group.key} finding={group.findings[0]} onAction={onAction} />
                  ) : (
                    <FindingGroupRow key={group.key} group={group} onAction={onAction} />
                  ),
                )}
                {(rest > 0 || showAll) && (
                  <Button
                    size="xs"
                    variant="ghost"
                    className="w-full text-muted-foreground"
                    onClick={() => setShowAll(!showAll)}
                  >
                    {rest > 0
                      ? `Show ${rest} more ${rest === 1 ? "kind" : "kinds"} of finding`
                      : "Show less"}
                    <ChevronDown className={cn("transition-transform", showAll && "rotate-180")} />
                  </Button>
                )}
              </>
            )}
          </PanelBody>
        </div>
      )}
    </Panel>
  )
}


/**
 * One habit, and the containers that have it.
 *
 * The shared detail and advice are stated once at the top, because they are
 * the same sentence on every member — repeating them per container is how a
 * panel of four real problems reads as twenty-six. Underneath, each container
 * keeps its own row: the group is the summary, not a replacement for knowing
 * which ones.
 */
function FindingGroupRow({ group, onAction }: { group: FindingGroup; onAction?: FindingAction }) {
  const [open, setOpen] = useState(false)
  const first = group.findings[0]
  const meta = SEVERITY[group.severity] ?? SEVERITY.info
  const Icon = meta.icon
  const phrase = groupPhrase(group.findings)
  const count = group.findings.length
  const title = phrase ? `${count} containers ${phrase}` : `${first.title} (${count} containers)`

  return (
    <div
      className={cn(
        // A row is a row, not a card: no frame at rest, a tint under the
        // pointer, and the hairline only once it is open and has a body to
        // fence off. A list of framed rows inside a framed panel is the
        // stacking this pass exists to remove.
        "min-w-0 rounded-lg border transition-colors",
        open ? "border-hairline" : "border-transparent hover:bg-row-hover",
      )}
    >
      <button
        className="flex w-full min-w-0 items-start gap-2.5 rounded-lg px-2.5 py-1.5 text-left focus-ring-inset"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
      >
        <Icon className={cn("mt-0.5 size-3.5 shrink-0", meta.tone)} />
        <span className="min-w-0 flex-1">
          <span className="flex flex-wrap items-center gap-2">
            <span className="text-body leading-snug font-medium">{title}</span>
            <Tag>{CLASS_LABEL[first.class] ?? first.class}</Tag>
          </span>
          <span className="mt-0.5 line-clamp-1 block text-xs text-muted-foreground">
            {open ? group.findings.map((f) => f.target).join(", ") : first.detail}
          </span>
        </span>
        <ChevronDown
          className={cn(
            "mt-0.5 size-3.5 shrink-0 text-muted-foreground transition-transform",
            open && "rotate-180",
          )}
        />
      </button>
      {open && (
        <div className="space-y-2 border-t border-hairline px-2.5 py-2.5">
          <div className="space-y-2 pl-4.5">
            <p className="text-xs leading-relaxed text-muted-foreground">{first.detail}</p>
            {first.advice && (
              <p className="text-xs leading-relaxed">
                <span className="font-medium">What to do: </span>
                <span className="text-muted-foreground">{first.advice}</span>
              </p>
            )}
          </div>
          <div className="space-y-1.5">
            {group.findings.map((finding) => (
              <FindingRow key={finding.id} finding={finding} onAction={onAction} />
            ))}
          </div>
        </div>
      )}
    </div>
  )
}

export function FindingRow({
  finding,
  onAction,
  defaultOpen = false,
}: {
  finding: DockerFinding
  onAction?: FindingAction
  defaultOpen?: boolean
}) {
  // Collapsed by default: the title is the finding, the body is the argument
  // for it. A list where every entry is three paragraphs is one nobody reads.
  const [open, setOpen] = useState(defaultOpen)
  const meta = SEVERITY[finding.severity] ?? SEVERITY.info
  const Icon = meta.icon

  return (
    <div
      className={cn(
        // A row is a row, not a card: no frame at rest, a tint under the
        // pointer, and the hairline only once it is open and has a body to
        // fence off. A list of framed rows inside a framed panel is the
        // stacking this pass exists to remove.
        "min-w-0 rounded-lg border transition-colors",
        open ? "border-hairline" : "border-transparent hover:bg-row-hover",
      )}
    >
      <button
        className="flex w-full min-w-0 items-start gap-2.5 rounded-lg px-2.5 py-1.5 text-left focus-ring-inset"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
      >
        <Icon className={cn("mt-0.5 size-3.5 shrink-0", meta.tone)} />
        <span className="min-w-0 flex-1">
          <span className="flex flex-wrap items-center gap-2">
            <span className="text-body leading-snug font-medium">{finding.title}</span>
            <Tag>{CLASS_LABEL[finding.class] ?? finding.class}</Tag>
          </span>
          {!open && (
            <span className="mt-0.5 line-clamp-1 block text-xs text-muted-foreground">
              {finding.detail}
            </span>
          )}
        </span>
        <ChevronDown
          className={cn(
            "mt-0.5 size-3.5 shrink-0 text-muted-foreground transition-transform",
            open && "rotate-180",
          )}
        />
      </button>
      {open && (
        <div className="space-y-2 border-t border-hairline px-2.5 py-2.5 pl-7">
          <p className="text-xs leading-relaxed text-muted-foreground">{finding.detail}</p>
          {finding.advice && (
            <p className="text-xs leading-relaxed">
              <span className="font-medium">What to do: </span>
              <span className="text-muted-foreground">{finding.advice}</span>
            </p>
          )}
          {finding.action && onAction && (
            <Button size="xs" variant="outline" onClick={() => onAction(finding)}>
              {finding.actionLabel ?? "Fix this"}
            </Button>
          )}
        </div>
      )}
    </div>
  )
}

/**
 * Runtime health, as a sentence and a set of counts.
 *
 * The counts matter as much as the status: a list of problems cannot say
 * "eight healthy, four with no health check at all", and that last number is
 * what stops "all healthy" meaning "nothing is being watched".
 */
export function RuntimeHealthPanel({
  runtime,
  className,
}: {
  runtime: RuntimeHealth | undefined
  className?: string
}) {
  if (!runtime) return null
  return (
    <Panel className={className}>
      <PanelHeader icon={Heart} title="Runtime health" />
      <PanelBody>
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
          <RuntimeStat label="Running" value={runtime.running} of={runtime.total} />
          <RuntimeStat
            label="Passing a health check"
            value={runtime.healthy}
            tone={runtime.healthy ? "text-success" : undefined}
          />
          <RuntimeStat
            label="Failing one"
            value={runtime.unhealthy}
            tone={runtime.unhealthy ? "text-destructive" : undefined}
          />
          <RuntimeStat
            label="Without one"
            value={runtime.noHealthcheck}
            hint="Docker reports these as up whenever their main process is alive."
          />
        </div>
      </PanelBody>
    </Panel>
  )
}

function RuntimeStat({
  label,
  value,
  of,
  tone,
  hint,
}: {
  label: string
  value: number
  of?: number
  tone?: string
  hint?: string
}) {
  return (
    <div className="min-w-0">
      <p className="eyebrow truncate">{label}</p>
      <p className={cn("numeric mt-0.5 text-lg leading-tight font-medium", tone)}>
        {value}
        {of !== undefined && <span className="text-sm text-muted-foreground"> / {of}</span>}
      </p>
      {hint && <p className="mt-0.5 text-micro leading-snug text-muted-foreground">{hint}</p>}
    </div>
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
  return (
    <div className="space-y-2">
      {mine.map((finding) => (
        <FindingRow key={finding.id} finding={finding} onAction={onAction} />
      ))}
    </div>
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
      return "Everything is up"
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
