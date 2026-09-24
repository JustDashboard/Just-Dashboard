"use client"

import Link from "next/link"
import { ArrowRight } from "@/components/icons"
import { plural } from "@/lib/format"
import type { DeploymentDetectionChange, DeploymentDetectionProposal } from "@/lib/types"
import { Button } from "@/components/ui/button"
import { frameworkLabel } from "@/components/deploy/vocabulary"
import { proposedValue } from "@/components/deploy/settings/detection-changes"

/**
 * What detection reads in the source now, beside what the plan says, one
 * field at a time.
 *
 * A source change saves what detection read at the new source and hands back
 * the fields it answers differently; Build settings' Detect again reads the
 * source without changing anything and does the same. Each field applies on
 * its own or all together, and applying writes nothing until the form it
 * lands in is saved — or, on the Source page, through the ordinary save of
 * the build plan. A field the plan changed on purpose, which detection did
 * not change its mind about, is offered only where the caller asks for it.
 */
export function DetectionProposalPanel({
  projectId,
  title,
  proposal,
  changes,
  canEdit,
  applying,
  onApply,
  onDismiss,
}: {
  projectId: number
  title: string
  proposal: DeploymentDetectionProposal
  changes: DeploymentDetectionChange[]
  canEdit: boolean
  applying?: boolean
  onApply: (changes: DeploymentDetectionChange[]) => void
  onDismiss?: () => void
}) {
  const unset = proposal.variables.filter(
    (variable) => variable.required || proposal.newVariables.includes(variable.name),
  )
  const detected = proposal.candidate
  if (changes.length === 0 && unset.length === 0 && proposal.databases.length === 0) {
    return (
      <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 rounded-lg border border-hairline p-3 text-body">
        <span className="min-w-0 flex-1">
          Detection reads the source the way the plan builds it
          {detected?.framework ? ` — ${frameworkLabel(detected.framework)}` : ""}.
        </span>
        {onDismiss && (
          <Button type="button" variant="ghost" size="xs" onClick={onDismiss}>
            Dismiss
          </Button>
        )}
      </div>
    )
  }
  return (
    <section
      aria-label={title}
      className="min-w-0 space-y-3 rounded-lg border border-rule-warning bg-wash-warning p-3"
    >
      <div className="flex min-w-0 flex-wrap items-start gap-x-3 gap-y-1">
        <div className="min-w-0 flex-1 space-y-0.5">
          <p className="text-body font-medium">{title}</p>
          <p className="text-xs leading-relaxed text-muted-foreground">
            {detected
              ? `Read ${detected.framework ? frameworkLabel(detected.framework) : detected.name}${
                  detected.root ? ` in ${detected.root}` : ""
                }${proposal.sourceRevision ? ` at ${proposal.sourceRevision.slice(0, 7)}` : ""}.`
              : "Detection found nothing to compare with the plan."}{" "}
            Applying changes the settings below; a deployment takes them once they are saved.
          </p>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          {onDismiss && (
            <Button type="button" variant="ghost" size="xs" onClick={onDismiss} disabled={applying}>
              Dismiss
            </Button>
          )}
          {changes.length > 1 && canEdit && (
            <Button
              type="button"
              variant="outline"
              size="xs"
              onClick={() => onApply(changes)}
              pending={applying}
            >
              Apply all
            </Button>
          )}
        </div>
      </div>
      {changes.length > 0 && (
        <ul className="divide-y divide-hairline border-y border-hairline">
          {changes.map((change) => (
            <li
              key={change.field}
              className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 py-2 text-body"
            >
              <span className="w-36 shrink-0 font-medium">{change.label}</span>
              <span className="flex min-w-0 flex-1 flex-wrap items-center gap-1.5 font-mono text-xs">
                <span className="break-all text-muted-foreground line-through decoration-muted-foreground/50">
                  {proposedValue(change, change.saved)}
                </span>
                <ArrowRight aria-hidden className="size-3 shrink-0 text-muted-foreground" />
                <span className="break-all">{proposedValue(change, change.detected)}</span>
                {!change.changed && (
                  <span className="font-sans text-hint text-muted-foreground">
                    (set on purpose; detection has not changed)
                  </span>
                )}
              </span>
              {canEdit && (
                <Button
                  type="button"
                  variant="outline"
                  size="xs"
                  onClick={() => onApply([change])}
                  disabled={applying}
                >
                  Apply
                </Button>
              )}
            </li>
          ))}
        </ul>
      )}
      {(unset.length > 0 || proposal.databases.length > 0) && (
        <p className="text-xs leading-relaxed text-muted-foreground">
          {unset.length > 0 && (
            <>
              The code reads {plural(unset.length, "variable")} this project does not set:{" "}
              <span className="font-mono text-foreground">
                {unset.map((variable) => variable.name).join(", ")}
              </span>
              .{" "}
              <Link
                href={`/deploy/${projectId}/settings/variables`}
                className="rounded-sm text-foreground underline underline-offset-2 focus-ring"
              >
                Set them in Variables
              </Link>
              .{" "}
            </>
          )}
          {proposal.databases.length > 0 && (
            <>
              It connects to{" "}
              {proposal.databases
                .map((database) => `${database.engine} through ${database.variable}`)
                .join(", ")}
              .{" "}
              <Link
                href={`/deploy/${projectId}/settings/databases`}
                className="rounded-sm text-foreground underline underline-offset-2 focus-ring"
              >
                Link a database
              </Link>
              .
            </>
          )}
        </p>
      )}
    </section>
  )
}
