import type { DockerFinding } from "./types"

/** Routine recommendations stay in Configure; attention is reserved for current risks and failures. */
export function needsDockerAttention(finding: Pick<DockerFinding, "severity" | "level">) {
  const severity = finding.severity ?? finding.level
  return severity === "critical" || severity === "warning"
}

export function priorityDockerFindings(findings: DockerFinding[]) {
  return findings.filter(needsDockerAttention).map((finding) => ({
    ...finding,
    severity: finding.severity ?? (finding.level === "critical" ? "critical" : "warning"),
  }))
}
