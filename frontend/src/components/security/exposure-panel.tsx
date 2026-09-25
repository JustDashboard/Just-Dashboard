"use client"

import { Connection, Globe, Router, type Icon } from "@/components/icons"
import type { Exposure } from "@/lib/types"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { NetworkFact } from "@/components/client-mark"
import { Status, type Verdict } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Skeleton } from "@/components/ui/skeleton"

export const EXPOSURE_GRADE: Record<Exposure["grade"], { label: string; verdict: Verdict }> = {
  tailscale: { label: "Tailscale only", verdict: "ok" },
  tunnel: { label: "SSH tunnel only", verdict: "ok" },
  private: { label: "Private network", verdict: "ok" },
  public: { label: "Public addresses", verdict: "warning" },
  open: { label: "Open to the internet", verdict: "critical" },
}

/**
 * The mark for how the panel is reached, where it is no product: a tunnel is
 * a connection, a private network a router, and the two internet grades the
 * globe. The tailnet is Tailscale's own mark.
 */
const GRADE_GLYPH: Record<Exposure["grade"], Icon> = {
  tailscale: Connection,
  tunnel: Connection,
  private: Router,
  public: Globe,
  open: Globe,
}

/**
 * How this dashboard is reachable — the security property the whole product
 * rests on, and the one that lives in an env file nobody opens again after
 * install day. On screen it stays true: a machine that quietly became
 * reachable from the internet says so here instead of waiting to be found.
 *
 * It is the section's identity line, the shape the host Overview gives the
 * machine and the account pages give a session: the way in drawn as itself
 * (Tailscale's mark on a tailnet-only panel), the grade as the title in the
 * colour of its verdict, and what it was read from as the facts after it —
 * the allowed ranges, the interfaces, the tailnet address, and the address
 * this browser arrived from, which is the one every lockout guard on these
 * pages compares against. The right end is the page's other verdict, the
 * posture's, so the two answers the page is opened for share one line.
 */
export function ExposureIdentity({
  exposure,
  aside,
}: {
  exposure: Exposure | undefined
  aside?: React.ReactNode
}) {
  if (!exposure) {
    return (
      <div className="flex min-w-0 items-center gap-4 border-b border-hairline pb-6">
        <Skeleton className="size-12 rounded-xl" />
        <div className="space-y-2">
          <Skeleton className="h-5 w-40" />
          <Skeleton className="h-4 w-72" />
        </div>
      </div>
    )
  }
  const grade = EXPOSURE_GRADE[exposure.grade]
  return (
    <HostIdentity
      mark={exposure.grade === "tailscale" ? "tailscale" : undefined}
      fallback={GRADE_GLYPH[exposure.grade]}
      title={
        <span className="inline-flex min-w-0 items-center gap-2.5">
          <Status verdict={grade.verdict} label={grade.label} className="text-title" />
        </span>
      }
      facts={
        <>
          <span className="inline-flex min-w-0 flex-wrap items-center gap-1">
            <span>allowed</span>
            {exposure.allowlist.length === 0 ? (
              <span className="font-medium text-warning">every address</span>
            ) : (
              exposure.allowlist.map((cidr) => (
                <Tag key={cidr} mono>
                  {cidr}
                </Tag>
              ))
            )}
          </span>
          {exposure.interfaces.length > 0 && (
            <>
              <FactDot />
              <span className="inline-flex min-w-0 flex-wrap items-center gap-1">
                <span>via</span>
                {exposure.interfaces.map((name) => (
                  <Tag key={name} mono>
                    {name}
                  </Tag>
                ))}
              </span>
            </>
          )}
          {exposure.tailscaleIp && (
            <>
              <FactDot />
              <span className="inline-flex items-center gap-1">
                <span>this host</span>
                <span className="font-mono text-foreground" title="This host's tailnet address">
                  {exposure.tailscaleIp}
                </span>
              </span>
            </>
          )}
          {exposure.client && (
            <>
              <FactDot />
              <span className="inline-flex min-w-0 items-center gap-1">
                <span>you</span>
                <NetworkFact
                  ip={exposure.client}
                  address={
                    <span
                      className="font-mono text-foreground"
                      title="The address this browser is reaching the dashboard from"
                    >
                      {exposure.client}
                    </span>
                  }
                />
              </span>
            </>
          )}
        </>
      }
      aside={aside}
    />
  )
}
