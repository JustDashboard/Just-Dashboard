"use client"

import { useRouter } from "next/navigation"
import { useAuth } from "@/hooks/use-auth"
import { securityRemedy } from "@/lib/security-remedies"

import {
  Bug,
  CheckCircle,
  CloudDownload,
  Cross,
  Globe,
  Information,
  Router,
  ShieldCheck,
  ShieldOff,
  TerminalWindow,
  Warning,
} from "@/components/icons"
import type { Posture, SecurityFinding } from "@/lib/types"
import { ProductGlyph } from "@/components/product-logo"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Status } from "@/components/status-dot"
import { FindingList, type Finding } from "@/components/finding-list"
import { FilterChip } from "@/components/tabs"
import { Skeleton } from "@/components/ui/skeleton"
import { POSTURE_AREAS } from "@/components/security/posture-strip"

/**
 * Whether this machine is in reasonable shape, as a verdict.
 *
 * Everything else in this section shows what is configured — a rule list, a
 * jail, a table of open ports — and leaves the reading to somebody who already
 * knows how. The people who most need the answer are exactly the ones who do
 * not. Cockpit and Webmin show the facts and stop; the hosting panels sell a
 * score out of a hundred, which is a number to optimise rather than a thing to
 * fix.
 *
 * So each finding carries three separate things — what was measured, what it
 * means, and what to do — and where the dashboard can carry the remedy out
 * itself, a button. Rendered through `FindingList`, the same list the health
 * verdict uses, and plain like it: the findings are the first thing to read
 * after the figures, and a frame around them made the page open with a box.
 *
 * The split by severity is the head's, as dots and words; which checks ran is
 * the strip's above it (`posture-strip.tsx`), whose segments narrow this list
 * to one area — the chip in the head says so and lets it go.
 */
export function PosturePanel({
  posture,
  loading,
  onFix,
  area,
  onClearArea,
  className,
}: {
  posture: Posture | undefined
  loading: boolean
  onFix?: (finding: SecurityFinding) => void
  /** The one area the strip narrowed the list to. */
  area?: SecurityFinding["area"]
  onClearArea?: () => void
  className?: string
}) {
  const { can } = useAuth()
  const router = useRouter()
  const act = (finding: SecurityFinding) => {
    const remedy = securityRemedy(!onFix ? { ...finding, fix: undefined } : finding, can)
    if (remedy.apply && onFix) onFix(finding)
    else if (remedy.href) router.push(remedy.href)
  }
  if (loading && !posture) {
    return (
      <Panel plain className={className}>
        <PanelHeader title="Findings" />
        <PanelBody className="space-y-2">
          <Skeleton className="h-4 w-48" />
          <Skeleton className="h-4 w-72" />
        </PanelBody>
      </Panel>
    )
  }
  if (!posture) return null

  const count = (level: SecurityFinding["level"]) =>
    posture.findings.filter((finding) => finding.level === level).length
  const shown = area ? posture.findings.filter((f) => f.area === area) : posture.findings
  const narrowed = POSTURE_AREAS.find((a) => a.area === area)

  return (
    <Panel plain className={className}>
      <PanelHeader
        title="Findings"
        actions={
          <span className="flex flex-wrap items-center gap-x-4 gap-y-1">
            {narrowed && (
              <FilterChip selected onClick={onClearArea} aria-label="Show every area">
                <narrowed.glyph aria-hidden className="size-3 text-brand" />
                Only {narrowed.name}
                <Cross aria-hidden className="size-3 text-muted-foreground" />
              </FilterChip>
            )}
            <Status
              verdict={count("critical") ? "critical" : "notice"}
              label={`${count("critical")} critical`}
            />
            <Status
              verdict={count("warning") ? "warning" : "notice"}
              label={`${count("warning")} warning`}
            />
            <Status verdict="notice" label={`${count("notice")} notice`} />
          </span>
        }
      />
      <PanelBody>
        {/* Keyed by the area, so narrowing it is an arrival (§11). */}
        <div key={area ?? "all"} className="animate-rise">
          <FindingList
            findings={shown.map((finding) =>
              toFinding(
                finding,
                act,
                securityRemedy(!onFix ? { ...finding, fix: undefined } : finding, can).label,
              ),
            )}
            emptyLabel={
              posture.skipped.length > 0
                ? `No findings in completed checks. Not checked: ${posture.skipped.join(", ")}.`
                : "All security checks passed"
            }
          />
        </div>
      </PanelBody>
    </Panel>
  )
}

/**
 * Findings filtered to one area, for rendering above the block that fixes
 * them. Plain, like the health list on the host Overview: the findings are the
 * first thing to read on the page, and a frame around them put a box above
 * the thing they are about.
 */
export function AreaFindings({
  posture,
  area,
  onFix,
  className,
}: {
  posture: Posture | undefined
  area: SecurityFinding["area"] | SecurityFinding["area"][]
  onFix?: (finding: SecurityFinding) => void
  className?: string
}) {
  const { can } = useAuth()
  const router = useRouter()
  const act = (finding: SecurityFinding) => {
    const remedy = securityRemedy(!onFix ? { ...finding, fix: undefined } : finding, can)
    if (remedy.apply && onFix) onFix(finding)
    else if (remedy.href) router.push(remedy.href)
  }
  const areas = Array.isArray(area) ? area : [area]
  const findings = posture?.findings.filter((f) => areas.includes(f.area)) ?? []
  if (findings.length === 0) return null
  return (
    <Panel plain className={className}>
      <PanelHeader
        title="Needs attention"
        actions={
          <Status
            verdict={worstLevel(findings)}
            label={`${findings.length} finding${findings.length === 1 ? "" : "s"}`}
          />
        }
      />
      <PanelBody>
        <FindingList
          findings={findings.map((f) =>
            toFinding(f, act, securityRemedy(!onFix ? { ...f, fix: undefined } : f, can).label),
          )}
        />
      </PanelBody>
    </Panel>
  )
}

/** The most severe level in a set of findings, for the one dot that summarises them. */
export function worstLevel(findings: SecurityFinding[]): SecurityFinding["level"] | "ok" {
  if (findings.some((f) => f.level === "critical")) return "critical"
  if (findings.some((f) => f.level === "warning")) return "warning"
  if (findings.length > 0) return "notice"
  return "ok"
}

/** A `SecurityFinding` as the shape `FindingList` renders: area on the right, fix as a button. */
function toFinding(
  finding: SecurityFinding,
  onFix?: (f: SecurityFinding) => void,
  label?: string,
): Finding {
  return {
    id: finding.id,
    level: finding.level,
    title: finding.title,
    detail: finding.detail,
    advice: finding.advice,
    meta: (
      <span className="inline-flex items-center gap-1">
        {finding.area === "intrusion" ? (
          <ProductGlyph id="fail2ban" />
        ) : (
          <AreaIcon area={finding.area} className="size-3" />
        )}
        {finding.area}
      </span>
    ),
    action: onFix
      ? { label: label ?? finding.fixLabel ?? "Review controls", onClick: () => onFix(finding) }
      : undefined,
  }
}

/**
 * The area's icon.
 *
 * A branch per area returning real JSX rather than resolving a component into
 * a capitalised local: the latter produces a fresh component type on every
 * render, which React treats as a different element and remounts.
 */
function AreaIcon({ area, className }: { area: SecurityFinding["area"]; className?: string }) {
  if (area === "exposure") return <Globe className={className} />
  if (area === "firewall") return <ShieldOff className={className} />
  if (area === "ssh") return <TerminalWindow className={className} />
  if (area === "intrusion") return <Bug className={className} />
  if (area === "ports") return <Router className={className} />
  if (area === "tls") return <ShieldCheck className={className} />
  if (area === "updates") return <CloudDownload className={className} />
  return <Information className={className} />
}

/** The one-word verdict, small enough for a panel header. */
export function PostureBadge({
  status,
  className,
}: {
  status: Posture["status"]
  className?: string
}) {
  const label =
    status === "critical"
      ? "Action needed"
      : status === "warning"
        ? "Needs attention"
        : status === "notice"
          ? "Minor notes"
          : "Hardened"
  const Icon =
    status === "ok"
      ? CheckCircle
      : status === "critical"
        ? ShieldOff
        : status === "warning"
          ? Warning
          : Information
  return <Status verdict={status} label={label} icon={Icon} className={className} />
}
