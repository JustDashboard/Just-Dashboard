import type { DeploymentDiagnosisFinding } from "@/lib/types"
import { findingNavigation } from "@/lib/finding-destinations"

/** A saved draft is already shown as pending changes; it is not a failed deployment. */
export function needsProjectAttention(finding: Pick<DeploymentDiagnosisFinding, "severity">) {
  return finding.severity === "critical" || finding.severity === "warning"
}

export function projectFindingNavigation(finding: DeploymentDiagnosisFinding, projectId: number) {
  if (finding.code === "runtime_absent")
    return { label: "Inspect runtime", href: `/deploy/${projectId}/runtime` }
  if (finding.code === "domain_unrouted")
    return { label: "Review domain routing", href: `/deploy/${projectId}/settings/domains` }
  if (["certificate_missing", "certificate_expired", "certificate_expiring"].includes(finding.code))
    return {
      href: findingNavigation(finding.deepLink ?? "")?.href ?? "/proxy/certificates",
      label:
        finding.code === "certificate_missing"
          ? "Review certificate issuance"
          : "Review certificate renewal",
    }
  if (finding.code === "backup_missing") return { label: "Set up a backup job", href: "/backups" }
  return finding.deepLink && !finding.external ? findingNavigation(finding.deepLink) : undefined
}
