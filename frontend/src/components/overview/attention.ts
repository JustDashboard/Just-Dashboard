import type { Finding } from "@/components/finding-list"
import { fleetAttention, type FleetPulses } from "@/components/deploy/fleet"
import { plural, relativeTime } from "@/lib/format"
import type {
  BackupJob,
  Certificate,
  DbConnection,
  DeploymentSummary,
  Exposure,
  Health,
  UpdateReport,
} from "@/lib/types"

/** A finding another page owns, whose remedy is that page. */
export type LinkedFinding = Omit<Finding, "action"> & { action?: { label: string; href: string } }

const LEVEL_ORDER: Record<Finding["level"], number> = { critical: 0, warning: 1, notice: 2 }
const VERDICT_RANK: Record<Health["status"], number> = { ok: 0, notice: 1, warning: 2, critical: 3 }

/**
 * What each module already knows is wrong, said where the reader lands.
 *
 * The recorder's verdict covers the machine — capacity, pressure, services,
 * containers — and stopped there, so a deploy that failed, a backup that went
 * quiet or a certificate past its renewal was only a red figure on a tile
 * further down, with no word of what it was or where to go. Each source is
 * read exactly as its own page reads it (the fleet's findings are the fleet
 * page's own), and every finding carries the page that fixes it.
 */
export function serverAttention({
  deployments,
  pulses,
  backups,
  certificates,
  packages,
  databases,
  exposure,
}: {
  deployments?: DeploymentSummary[]
  pulses?: FleetPulses
  backups?: BackupJob[]
  certificates?: Certificate[]
  packages?: UpdateReport
  databases?: DbConnection[]
  exposure?: Exposure
}): LinkedFinding[] {
  const findings: LinkedFinding[] = [...fleetAttention(deployments ?? [], pulses)]

  for (const job of backups ?? []) {
    if (!job.enabled) continue
    const run = job.lastRun
    if (run?.status === "failed") {
      findings.push({
        id: `backup-${job.id}`,
        level: "critical",
        title: `${job.name}: backup failed ${relativeTime(run.endedAt ?? run.startedAt)}`,
        detail: job.lastSuccessAt
          ? `The last archive that was written is from ${relativeTime(job.lastSuccessAt)}.`
          : "No run of this job has written an archive yet.",
        advice: "Open the job to read the failed run's log.",
        meta: "backup failed",
        action: { label: "Open job", href: `/backups/${job.id}` },
      })
    } else if (job.overdue) {
      findings.push({
        id: `backup-${job.id}`,
        level: "warning",
        title: `${job.name} has gone quiet`,
        detail: job.lastSuccessAt
          ? `Two scheduled runs have passed since the last archive, ${relativeTime(job.lastSuccessAt)}.`
          : "Two scheduled runs have passed without an archive.",
        meta: "backup overdue",
        action: { label: "Open job", href: `/backups/${job.id}` },
      })
    }
  }

  // One finding per kind, naming every certificate in it: three domains on
  // one failing renewal are one thing to go and look at.
  const expired = (certificates ?? []).filter((cert) => cert.expired)
  const overdue = (certificates ?? []).filter((cert) => !cert.expired && cert.expiring)
  const names = (certs: Certificate[]) => certs.map((cert) => cert.domains[0] ?? cert.name)
  if (expired.length > 0) {
    findings.push({
      id: "certificates-expired",
      level: "critical",
      title:
        expired.length === 1
          ? `${names(expired)[0]}: certificate expired`
          : `${expired.length} certificates expired`,
      detail: `Browsers refuse ${names(expired).join(", ")} until a new certificate is issued.`,
      meta: "certificate",
      action: { label: "Open certificates", href: "/proxy/certificates" },
    })
  }
  if (overdue.length > 0) {
    const soonest = Math.min(...overdue.map((cert) => cert.daysLeft))
    findings.push({
      id: "certificates-renewal",
      level: "warning",
      title:
        overdue.length === 1
          ? `${names(overdue)[0]}: certificate expires in ${soonest}d`
          : `${overdue.length} certificates are past their renewal`,
      detail: `Renewal should already have happened for ${names(overdue).join(", ")}.`,
      advice: "Open the certificate to renew it, or read the renewal log for why it stopped.",
      meta: "certificate",
      action: { label: "Open certificates", href: "/proxy/certificates" },
    })
  }

  if (packages?.available && packages.securityCount > 0) {
    findings.push({
      id: "packages-security",
      level: "warning",
      title: `${plural(packages.securityCount, "security update")} waiting`,
      detail: `${plural(packages.packages.length, "update")} in all${packages.manager ? ` from ${packages.manager}` : ""}.`,
      meta: "packages",
      action: { label: "Open packages", href: "/packages" },
    })
  }
  if (packages?.available && packages.rebootRequired) {
    findings.push({
      id: "packages-reboot",
      level: "warning",
      title: "A reboot is owed",
      detail: packages.rebootPackages?.length
        ? `${packages.rebootPackages.join(", ")} take effect only after a restart.`
        : "Updated packages take effect only after a restart.",
      meta: "packages",
      action: { label: "Open packages", href: "/packages" },
    })
  }

  const broken = (databases ?? []).filter((connection) => connection.broken)
  if (broken.length > 0) {
    findings.push({
      id: "databases-broken",
      level: "warning",
      title: `${plural(broken.length, "saved database")} cannot be opened`,
      detail: broken
        .map((connection) =>
          connection.brokenReason
            ? `${connection.name}: ${connection.brokenReason}`
            : connection.name,
        )
        .join(" · "),
      meta: "databases",
      action: { label: "Open databases", href: "/databases" },
    })
  }

  if (exposure && (exposure.grade === "open" || exposure.grade === "public")) {
    findings.push({
      id: "exposure",
      level: exposure.grade === "open" ? "critical" : "warning",
      title:
        exposure.grade === "open"
          ? "The dashboard is open to the internet"
          : "The dashboard answers on a public address",
      detail: exposure.summary,
      advice: exposure.recommendation,
      meta: "security",
      action: { label: "Open security", href: "/security" },
    })
  }

  return worstFirst(findings)
}

/** Critical first, then warning, then notice; each source keeps its own order within a level. */
export function worstFirst<T extends Pick<Finding, "level">>(findings: T[]): T[] {
  return findings
    .map((finding, index) => ({ finding, index }))
    .sort(
      (a, b) => LEVEL_ORDER[a.finding.level] - LEVEL_ORDER[b.finding.level] || a.index - b.index,
    )
    .map(({ finding }) => finding)
}

/** The recorder's verdict, raised to the worst thing anything else has found. */
export function verdictWith(
  status: Health["status"],
  findings: Pick<Finding, "level">[],
): Health["status"] {
  return findings.reduce<Health["status"]>(
    (worst, finding) => (VERDICT_RANK[finding.level] > VERDICT_RANK[worst] ? finding.level : worst),
    status,
  )
}
