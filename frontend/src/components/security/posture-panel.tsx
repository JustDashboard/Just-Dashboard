"use client"

import {
  Bug,
  CheckCircle,
  CloudDownload,
  Globe,
  Information,
  Router,
  ShieldCheck,
  ShieldOff,
  TerminalWindow,
  Warning,
} from "@/components/icons"
import { relativeTime } from "@/lib/format"
import type { Posture, SecurityFinding } from "@/lib/types"
import { Metric, MetricStrip } from "@/components/page"
import { Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { Status } from "@/components/status-dot"
import { FindingList, type Finding } from "@/components/finding-list"
import { Skeleton } from "@/components/ui/skeleton"

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
 */
export function PosturePanel({
  posture,
  loading,
  onFix,
  className,
}: {
  posture: Posture | undefined
  loading: boolean
  onFix?: (finding: SecurityFinding) => void
  className?: string
}) {
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
    posture.findings.filter((f) => f.level === level).length

  return (
    <Panel plain className={className}>
      {/* The verdict itself is the page's identity line's; this header says
          how many there are and when they were read. */}
      <PanelHeader
        title="Findings"
        actions={
          <span className="numeric text-hint text-muted-foreground">
            {posture.findings.length === 0
              ? "none"
              : `${posture.findings.length} finding${posture.findings.length === 1 ? "" : "s"}`}{" "}
            · checked {relativeTime(posture.checkedAt)}
          </span>
        }
      />
      {/* The severity split as figures rather than as a sentence: three
          findings and three critical findings are not the same morning, and
          the header used to read identically either way. */}
      {posture.findings.length > 0 && (
        <PanelToolbar>
          <MetricStrip>
            <Metric label="critical" value={count("critical")} />
            <Metric label="warning" value={count("warning")} />
            <Metric label="notice" value={count("notice")} />
            <Metric
              label="not checked"
              value={posture.skipped.length}
              hint={posture.skipped.length > 0 ? posture.skipped.join(", ") : undefined}
            />
          </MetricStrip>
        </PanelToolbar>
      )}
      <PanelBody>
        <FindingList
          findings={posture.findings.map((f) => toFinding(f, onFix))}
          emptyLabel={
            posture.skipped.length > 0
              ? `Everything that could be checked is in order — ${posture.skipped.join(", ")} could not be`
              : "Exposure, firewall, SSH, intrusion prevention, open ports, certificates and pending security updates all check out"
          }
        />
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
        <FindingList findings={findings.map((f) => toFinding(f, onFix))} />
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
function toFinding(finding: SecurityFinding, onFix?: (f: SecurityFinding) => void): Finding {
  return {
    id: finding.id,
    level: finding.level,
    title: finding.title,
    detail: finding.detail,
    advice: finding.advice,
    meta: (
      <span className="inline-flex items-center gap-1">
        <AreaIcon area={finding.area} className="size-3" />
        {finding.area}
      </span>
    ),
    action:
      finding.fix && onFix
        ? { label: finding.fixLabel ?? "Fix", onClick: () => onFix(finding) }
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
