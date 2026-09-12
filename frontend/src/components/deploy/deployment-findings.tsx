"use client"

import Link from "next/link"
import { cn } from "@/lib/utils"
import type { DeploymentPreflightFinding } from "@/lib/types"
import { humanize } from "@/components/deploy/deployment-ui"
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
 * One finding, with what was measured, what it means and what to do next.
 *
 * Shared rather than duplicated because the two flows that reach preflight —
 * the full wizard and quick deploy — fail for the same reasons, and a plan
 * that is refused has to say the same thing whichever screen the operator got
 * to it from. The single commonest complaint about the old flow was a config
 * error with no field attached to it; `action` and `deepLink` are how a
 * finding names its own remedy, so they are never dropped here.
 */
export function FindingRow({ finding }: { finding: DeploymentPreflightFinding }) {
  return (
    <div
      id={finding.fieldId ? finding.fieldId.replaceAll(".", "-") : undefined}
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
      {finding.action && (
        <p className="mt-1 text-hint leading-relaxed">
          <b className="font-medium">Next:</b> {finding.action}
          {finding.deepLink && (
            <>
              {" "}
              ·{" "}
              <Link href={finding.deepLink} className="underline underline-offset-2">
                Open owning page
              </Link>
            </>
          )}
        </p>
      )}
    </div>
  )
}
