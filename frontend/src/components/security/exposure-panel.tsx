"use client"

import type { Exposure } from "@/lib/types"
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
 * How this dashboard is reachable — the security property the whole product
 * rests on, and the one that lives in an env file nobody opens again after
 * install day. On screen it stays true: a machine that quietly became
 * reachable from the internet says so here instead of waiting to be found.
 *
 * A row of facts at the start of the page rather than a framed panel, the way the
 * host Overview states its platform and kernel: the grade is a reading, the
 * allowlist and the interfaces are what it was read from, and the address this
 * browser arrived from is the one every lockout guard on these pages compares
 * against — worth knowing before blocking anything.
 */
export function ExposureFacts({ exposure }: { exposure: Exposure | undefined }) {
  if (!exposure) {
    return (
      <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
        <Skeleton className="h-4 w-36" />
        <Skeleton className="h-4 w-64" />
      </div>
    )
  }
  const grade = EXPOSURE_GRADE[exposure.grade]
  return (
    <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1.5 text-body text-muted-foreground">
      <Status verdict={grade.verdict} label={grade.label} className="text-body" />
      <Dot />
      <span className="min-w-0">{exposure.summary}</span>
      <Dot />
      <span className="inline-flex min-w-0 flex-wrap items-center gap-1">
        <span>allowed</span>
        {exposure.allowlist.length === 0 ? (
          <span className="text-foreground">every address</span>
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
          <Dot />
          <span className="inline-flex min-w-0 flex-wrap items-center gap-1">
            <span>via</span>
            {exposure.interfaces.map((name) => (
              <Tag key={name} mono>
                {name}
              </Tag>
            ))}
            {exposure.tailscaleIp && (
              <Tag mono title="This host's tailnet address">
                {exposure.tailscaleIp}
              </Tag>
            )}
          </span>
        </>
      )}
      {exposure.client && (
        <>
          <Dot />
          <span className="inline-flex items-center gap-1">
            <span>you</span>
            <Tag mono title="The address this browser is reaching the dashboard from">
              {exposure.client}
            </Tag>
          </span>
        </>
      )}
    </div>
  )
}

function Dot() {
  return <span className="text-muted-foreground/40">·</span>
}
