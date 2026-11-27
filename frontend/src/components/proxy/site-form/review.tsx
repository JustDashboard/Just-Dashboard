"use client"

import type { SitePreflightCheck, PreflightLevel } from "@/lib/types"
import { StatusDot, type DotTone } from "@/components/status-dot"

export const CHECK_TONE: Record<PreflightLevel, DotTone> = {
  ok: "running",
  info: "stopped",
  warning: "warning",
  fail: "danger",
}

/**
 * Where each domain points, from the preflight.
 *
 * The first question of every reverse-proxy setup and the cause of most of the
 * failures: certbot cannot prove control of a name that resolves somewhere
 * else, and the error it gives says "challenge failed" rather than "your DNS
 * is not updated yet". Every domain, not only the first: a www alias that
 * points elsewhere fails the certificate order just the same.
 */
export function DomainChecks({ checks }: { checks: SitePreflightCheck[] }) {
  if (checks.length === 0) return null
  return (
    <ul className="-mt-3 space-y-1">
      {checks.map((check) => (
        <li key={check.id} className="flex items-start gap-2 text-hint leading-relaxed">
          <StatusDot tone={CHECK_TONE[check.level]} className="mt-1.5" />
          <span className="min-w-0 text-muted-foreground">
            <span className="font-mono text-foreground">{check.domain}</span> {check.title}.{" "}
            {check.level !== "ok" && check.detail}
          </span>
        </li>
      ))}
    </ul>
  )
}
