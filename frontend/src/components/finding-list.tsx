"use client"

import { CheckCircle, Wrench } from "@/components/icons"
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from "@/components/ui/accordion"
import { Button } from "@/components/ui/button"
import { StatusDot, type DotTone } from "@/components/status-dot"

/**
 * A verdict's findings, as a plain list rather than a stack of tinted boxes.
 *
 * `netsec.Posture`, `metrics.Health` and `dockerx.Diagnosis` all speak the same
 * three-field shape — what was measured (`detail`), what it means (`title`),
 * what to do (`advice`) — so all three render through here. The title carries
 * the finding and the severity is one coloured dot; the reasoning is one tap
 * away rather than shouting from a coloured card. A row of red-bordered alert
 * boxes is the thing this deliberately is not.
 *
 * Docker had its own parallel implementation of exactly this until 0.6.7 — two
 * components for one idea, so the same kind of list read as two different
 * products depending on which page you were on. The Docker one carried two
 * extras this now has: a short right-hand label (`meta`) and a remedy the
 * dashboard can carry out (`action`), plus `extra` for the one case that has
 * more than a paragraph to say — a finding that covers several containers and
 * has to name them.
 *
 * A row is one line. `detail` belongs in the body, not under the title: a list
 * where every entry is a title *and* a sentence is twice as tall and half as
 * scannable, which is the whole reason this shape won.
 */
export type Finding = {
  id: string
  level: "critical" | "warning" | "notice"
  title: string
  detail: string
  advice?: string
  /** A short label on the right of the row — e.g. the subsystem the finding is about. */
  meta?: React.ReactNode
  /** A one-click remedy, shown in the expanded body. */
  action?: { label: string; onClick: () => void }
  /** Anything the body needs beyond detail and advice — the targets of a grouped finding. */
  extra?: React.ReactNode
}

const LEVEL_TONE: Record<Finding["level"], DotTone> = {
  critical: "danger",
  warning: "warning",
  notice: "notice",
}

export function FindingList({
  findings,
  emptyLabel = "All checks passed",
}: {
  findings: Finding[]
  emptyLabel?: string
}) {
  if (findings.length === 0) {
    return (
      <div className="flex items-center gap-2.5 text-body text-muted-foreground">
        <CheckCircle className="size-4 shrink-0 text-success" />
        <span className="min-w-0">{emptyLabel}</span>
      </div>
    )
  }

  return (
    <Accordion type="multiple" className="min-w-0">
      {findings.map((finding) => (
        <AccordionItem key={finding.id} value={finding.id} className="border-hairline">
          <AccordionTrigger className="min-w-0 items-center gap-3 py-2.5 text-body hover:no-underline">
            <span className="flex min-w-0 flex-1 items-center gap-2.5">
              <StatusDot tone={LEVEL_TONE[finding.level]} />
              <span className="truncate font-medium">{finding.title}</span>
            </span>
            {/* Dropped on a phone rather than clipped: the row is a title, a
                severity dot and a chevron, and at 390px the meta had nowhere
                to go but past the panel's own edge. */}
            <span className="hidden max-w-[45%] shrink-0 text-hint font-normal text-muted-foreground sm:line-clamp-1">
              {finding.meta ?? finding.detail}
            </span>
          </AccordionTrigger>
          <AccordionContent className="space-y-2 pl-[1.375rem] text-xs leading-relaxed text-muted-foreground">
            <p>{finding.detail}</p>
            {finding.advice && <p className="text-foreground/80">{finding.advice}</p>}
            {finding.extra}
            {finding.action && (
              <Button
                size="xs"
                variant="outline"
                className="mt-0.5"
                onClick={finding.action.onClick}
              >
                <Wrench className="size-3" />
                {finding.action.label}
              </Button>
            )}
          </AccordionContent>
        </AccordionItem>
      ))}
    </Accordion>
  )
}
