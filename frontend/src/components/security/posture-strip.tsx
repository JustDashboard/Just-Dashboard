"use client"

import {
  Bug,
  CloudDownload,
  FirewallCheck,
  Globe,
  Router,
  SecureConnection,
  ShieldCheck,
  type Icon,
} from "@/components/icons"
import { plural, relativeTime } from "@/lib/format"
import type { Posture, SecurityFinding } from "@/lib/types"
import { cn } from "@/lib/utils"
import { Skeleton } from "@/components/ui/skeleton"

type Area = SecurityFinding["area"]

/** What one area came to: its worst finding, a pass, or a check that could not run. */
export type AreaVerdict = SecurityFinding["level"] | "ok" | "skipped"

/**
 * The seven checks `netsec.Assess` runs, in its own order, with the names the
 * posture's `skipped` list uses for the ones that can fail to run. Intrusion
 * is two of those — fail2ban and the failed-login record — and counts as not
 * checked only when both were missing.
 */
export const POSTURE_AREAS: { area: Area; name: string; glyph: Icon; skipped?: string[] }[] = [
  { area: "exposure", name: "Exposure", glyph: Globe },
  { area: "firewall", name: "Firewall", glyph: FirewallCheck, skipped: ["firewall"] },
  { area: "ssh", name: "SSH", glyph: SecureConnection, skipped: ["ssh"] },
  { area: "intrusion", name: "Intrusion", glyph: Bug, skipped: ["fail2ban", "failed logins"] },
  { area: "ports", name: "Ports", glyph: Router },
  { area: "tls", name: "Certificates", glyph: ShieldCheck },
  { area: "updates", name: "Updates", glyph: CloudDownload, skipped: ["security updates"] },
]

export function areaVerdict(posture: Posture, area: (typeof POSTURE_AREAS)[number]): AreaVerdict {
  const findings = posture.findings.filter((f) => f.area === area.area)
  if (findings.some((f) => f.level === "critical")) return "critical"
  if (findings.some((f) => f.level === "warning")) return "warning"
  if (findings.length > 0) return "notice"
  if (area.skipped?.every((name) => posture.skipped.includes(name))) return "skipped"
  return "ok"
}

const SEGMENT: Record<AreaVerdict, string> = {
  ok: "bg-success",
  notice: "bg-muted-foreground",
  warning: "bg-warning",
  critical: "bg-destructive",
  skipped: "border border-dashed border-border-strong",
}

const WORD: Record<AreaVerdict, string> = {
  ok: "text-success",
  notice: "text-muted-foreground",
  warning: "text-warning",
  critical: "text-destructive",
  skipped: "text-muted-foreground/70",
}

/**
 * The posture as the seven checks it is made of, one segment each, in the
 * colour of what that check found — so "Needs attention" at the end of the
 * identity line is read as *which* of the seven before a finding is opened.
 * It is the release path's bar (`deploy/run-pipeline.tsx`) given checks
 * instead of stages: the same segments, the same mark and name under each.
 *
 * A segment is the question its findings answer: pressing it narrows the
 * findings below to that area, and pressing it again lets it go. A check that
 * could not run is a dashed segment rather than a green one, because "not
 * checked" and "passed" are the two answers this page must never confuse.
 */
export function PostureStrip({
  posture,
  loading,
  area,
  onArea,
}: {
  posture: Posture | undefined
  loading: boolean
  area?: Area
  onArea: (area: Area | undefined) => void
}) {
  if (!posture) {
    if (!loading) return null
    return (
      <div className="grid grid-cols-7 gap-1 sm:gap-3">
        {POSTURE_AREAS.map((a) => (
          <Skeleton key={a.area} className="h-1.5 rounded-full" />
        ))}
      </div>
    )
  }
  const verdicts = POSTURE_AREAS.map((a) => ({ ...a, verdict: areaVerdict(posture, a) }))
  const passed = verdicts.filter((v) => v.verdict === "ok").length
  const notRun = verdicts.filter((v) => v.verdict === "skipped").length

  return (
    <section aria-label="Checks" className="min-w-0 space-y-3">
      <div className="flex min-w-0 flex-wrap items-baseline justify-between gap-x-6 gap-y-1">
        <h2 className="text-base font-semibold tracking-tight">
          <span className="numeric">{passed}</span> of{" "}
          <span className="numeric">{POSTURE_AREAS.length}</span> checks passed
        </h2>
        <p className="numeric text-hint text-muted-foreground">
          {notRun > 0 && `${plural(notRun, "check")} not run · `}checked{" "}
          {relativeTime(posture.checkedAt)}
        </p>
      </div>
      <ol className="grid min-w-0 grid-cols-7 gap-1 sm:gap-3">
        {verdicts.map((v, index) => {
          const count = posture.findings.filter((f) => f.area === v.area).length
          const pressed = area === v.area
          return (
            <li
              key={v.area}
              className="min-w-0 animate-rise"
              style={{ animationDelay: `${index * 45}ms` }}
            >
              <button
                type="button"
                aria-pressed={pressed}
                aria-label={`${v.name}: ${verdictWords(v.verdict, count)}`}
                disabled={count === 0}
                onClick={() => onArea(pressed ? undefined : v.area)}
                className={cn(
                  "group flex w-full min-w-0 flex-col gap-2 rounded-md text-left focus-ring",
                  count > 0 && "cursor-pointer",
                )}
              >
                <span
                  className={cn(
                    "block h-1.5 w-full rounded-full transition-opacity",
                    SEGMENT[v.verdict],
                    area && !pressed && "opacity-35",
                  )}
                />
                <span className="hidden min-w-0 flex-col gap-0.5 px-0.5 sm:flex">
                  <span
                    className={cn(
                      "flex min-w-0 items-center gap-1.5 text-xs font-medium transition-colors",
                      pressed ? "text-foreground" : "text-foreground/90",
                      count > 0 && "group-hover:text-foreground",
                    )}
                  >
                    <v.glyph aria-hidden className="size-3 shrink-0 text-brand" />
                    <span className="truncate">{v.name}</span>
                  </span>
                  <span className={cn("numeric truncate text-hint", WORD[v.verdict])}>
                    {verdictWords(v.verdict, count)}
                  </span>
                </span>
              </button>
            </li>
          )
        })}
      </ol>
    </section>
  )
}

function verdictWords(verdict: AreaVerdict, count: number) {
  if (verdict === "ok") return "Passed"
  if (verdict === "skipped") return "Not checked"
  return plural(count, verdict === "critical" ? "critical finding" : verdict)
}
