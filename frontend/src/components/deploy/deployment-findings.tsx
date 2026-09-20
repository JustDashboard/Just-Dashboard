"use client"

import Link from "next/link"
import { cn } from "@/lib/utils"
import type { DeploymentPreflightFinding } from "@/lib/types"
import { humanize } from "@/components/deploy/vocabulary"
import { Status, type Verdict } from "@/components/status-dot"

/** A preflight finding's severity as the app's own four-value verdict scale. */
export function findingVerdict(severity: DeploymentPreflightFinding["severity"]): Verdict {
  if (severity === "pass") return "ok"
  if (severity === "warning" || severity === "decision") return "warning"
  if (severity === "blocked") return "critical"
  return "notice"
}

export function blockingFindings(findings: DeploymentPreflightFinding[]) {
  return findings.filter(
    (finding) => finding.severity === "blocked" || finding.severity === "decision",
  )
}

export function warningFindings(findings: DeploymentPreflightFinding[]) {
  return findings.filter((finding) => finding.severity === "warning")
}

/**
 * The finding's own `action` text, overridden for the handful of codes whose
 * remedy is not where the finding's `fieldId` would suggest: a required
 * variable's field lives inside Configure's Advanced disclosure, not beside
 * the environment editor the fieldId's dotted path implies.
 */
export function findingRemedy(finding: DeploymentPreflightFinding) {
  if (finding.code.startsWith("variable_required_") || finding.code.startsWith("compose_variable_"))
    return "Set a value under Advanced → Variable references & scopes."
  return finding.action
}

/**
 * One finding, with what was measured, what it means and what to do next.
 *
 * Shared rather than duplicated because the two flows that reach preflight —
 * the full wizard and quick deploy — fail for the same reasons, and a plan
 * that is refused has to say the same thing whichever screen the operator got
 * to it from. The single commonest complaint about the old flow was a config
 * error with no field attached to it; `action` and `deepLink` are how a
 * finding names its own remedy, so they are never dropped here.
 *
 * `index` disambiguates a DOM id when a code repeats across rows — a mount
 * path outside its roots, an unavailable dependency and a domain conflict all
 * carry the same code once per instance rather than a code per row — and
 * should be the same value the caller keyed the row's own `key` with.
 */
export function FindingRow({
  finding,
  index,
  onOpenRemedy,
  canOpenRemedy = true,
}: {
  finding: DeploymentPreflightFinding
  index?: number
  /** Jumps to the control this finding is about, where one exists (§3). */
  onOpenRemedy?: (finding: DeploymentPreflightFinding) => void
  /**
   * Whether this screen actually owns a control for this finding's field. The
   * link used to be drawn for every finding and answered for one of them, so
   * a blocked plan offered to open a remedy and then did nothing.
   */
  canOpenRemedy?: boolean
}) {
  const remedy = findingRemedy(finding)
  const remedyOpensControl = Boolean(onOpenRemedy) && canOpenRemedy && !finding.deepLink
  return (
    <div
      id={
        finding.fieldId
          ? `finding-${finding.fieldId.replaceAll(".", "-")}-${index ?? 0}`
          : undefined
      }
      className={cn(
        "rounded-xl border p-3",
        finding.severity === "blocked" || finding.severity === "decision"
          ? "border-rule-danger bg-wash-danger"
          : finding.severity === "warning"
            ? "border-rule-warning bg-wash-warning"
            : "border-hairline",
      )}
    >
      <div className="flex min-w-0 flex-wrap items-center gap-2">
        <Status verdict={findingVerdict(finding.severity)} label={humanize(finding.severity)} />
        <p className="min-w-0 flex-1 text-xs font-medium">{finding.title}</p>
        {finding.owner && <span className="text-micro text-muted-foreground">{finding.owner}</span>}
      </div>
      {finding.measured && (
        <p className="mt-2 font-mono text-hint break-words">{finding.measured}</p>
      )}
      {finding.means && (
        <p className="mt-1 text-hint leading-relaxed text-muted-foreground">{finding.means}</p>
      )}
      {remedy && (
        <p className="mt-1 text-hint leading-relaxed">
          <b className="font-medium">Next:</b> {remedy}
          {finding.deepLink && (
            <>
              {" "}
              ·{" "}
              <Link href={finding.deepLink} className="underline underline-offset-2">
                Open owning page
              </Link>
            </>
          )}
          {remedyOpensControl && (
            <>
              {" "}
              ·{" "}
              <button
                type="button"
                onClick={() => onOpenRemedy?.(finding)}
                className="rounded-sm underline underline-offset-2 focus-ring"
              >
                Open it
              </button>
            </>
          )}
        </p>
      )}
    </div>
  )
}
