"use client"

import { useState } from "react"
import {
  CheckCircle,
  ChevronDown,
  CrossCircle,
  Information,
  Lifebuoy,
  Warning,
} from "@/components/icons"
import { cn } from "@/lib/utils"
import type { DockerDiagnosis, DockerFinding } from "@/lib/types"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"

/**
 * What is wrong, in sentences.
 *
 * Every Docker panel shows the same facts — a state, an exit code, a restart
 * count — and leaves the reading to you. Somebody who has run containers for
 * years reads "Exited (137)" as "the kernel killed it for using too much
 * memory"; everyone else reads a number. This is the same information already
 * on the page, turned into what it means and what to do about it, ranked so
 * the thing that is actually broken is first.
 *
 * The shape follows internal/metrics' host health panel deliberately: the
 * dashboard already takes a position on whether the machine is healthy, and
 * Docker should be answered the same way rather than in a second dialect.
 */

const LEVEL: Record<
  string,
  { icon: React.ComponentType<{ className?: string }>; tone: string; label: string }
> = {
  critical: { icon: CrossCircle, tone: "text-destructive", label: "Broken" },
  warning: { icon: Warning, tone: "text-warning", label: "Worth fixing" },
  notice: { icon: Information, tone: "text-muted-foreground", label: "Worth knowing" },
}

export type FindingAction = (finding: DockerFinding) => void

export function DiagnosisPanel({
  diagnosis,
  onAction,
  className,
}: {
  diagnosis: DockerDiagnosis | undefined
  /** Runs a finding's remedy. Undefined hides every action button. */
  onAction?: FindingAction
  className?: string
}) {
  if (!diagnosis) return null

  const counts = diagnosis.findings.reduce<Record<string, number>>((acc, f) => {
    acc[f.level] = (acc[f.level] ?? 0) + 1
    return acc
  }, {})

  return (
    <Panel className={className}>
      <PanelHeader
        icon={Lifebuoy}
        title="What needs attention"
        actions={
          <div className="flex items-center gap-3">
            {(["critical", "warning", "notice"] as const).map((level) =>
              counts[level] ? (
                <Status
                  key={level}
                  verdict={level}
                  label={`${counts[level]} ${LEVEL[level].label.toLowerCase()}`}
                />
              ) : null,
            )}
          </div>
        }
      />
      <PanelBody className={diagnosis.findings.length ? "space-y-2" : undefined}>
        {diagnosis.findings.length === 0 ? (
          <div className="flex items-center gap-3 text-body">
            <CheckCircle className="size-4 shrink-0 text-success" />
            <span className="min-w-0">
              Nothing is failing, restarting in a loop, published wider than it needs to be, or
              quietly filling the disk.
            </span>
          </div>
        ) : (
          diagnosis.findings.map((finding) => (
            <FindingRow key={finding.id} finding={finding} onAction={onAction} />
          ))
        )}
      </PanelBody>
    </Panel>
  )
}

function FindingRow({ finding, onAction }: { finding: DockerFinding; onAction?: FindingAction }) {
  // Collapsed by default: the title is the finding, the body is the argument
  // for it. A list where every entry is three paragraphs is one nobody reads.
  const [open, setOpen] = useState(false)
  const meta = LEVEL[finding.level] ?? LEVEL.notice
  const Icon = meta.icon

  return (
    <div className="min-w-0 rounded-lg border border-hairline">
      <button
        className="flex w-full min-w-0 items-start gap-2.5 px-3 py-2.5 text-left"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
      >
        <Icon className={cn("mt-0.5 size-3.5 shrink-0", meta.tone)} />
        <span className="min-w-0 flex-1">
          <span className="block text-body leading-snug font-medium">{finding.title}</span>
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
        <div className="space-y-2 border-t border-hairline px-3 py-2.5 pl-8">
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

/** A one-word verdict for a badge in a header. */
export function healthLabel(status: DockerDiagnosis["status"] | undefined): string {
  switch (status) {
    case "critical":
      return "Something is broken"
    case "warning":
      return "Worth a look"
    case "notice":
      return "A few notes"
    default:
      return "All good"
  }
}
