"use client"

import { get } from "@/lib/api"
import type { Exposure } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Detail, DetailList } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Skeleton } from "@/components/ui/skeleton"
import { Status, type Verdict } from "@/components/status-dot"
import { Tag } from "@/components/tag"

const GRADE: Record<Exposure["grade"], { label: string; verdict: Verdict }> = {
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
 * The allowlist and the interfaces are the two facts behind the grade, so they
 * are laid out as a labelled pair rather than as a loose row of chips: the
 * panel sits beside the posture verdict, and a body that was one sentence and
 * a chip row left it mostly empty card next to a full one.
 */
export function ExposurePanel({ className }: { className?: string }) {
  const { data } = usePoll<Exposure>((signal) => get("/exposure", undefined, signal), 60_000)

  if (!data) {
    return (
      <Panel className={className}>
        <PanelHeader title="Reachable from" />
        <PanelBody className="space-y-2">
          <Skeleton className="h-4 w-56" />
          <Skeleton className="h-4 w-40" />
          <Skeleton className="h-4 w-64" />
        </PanelBody>
      </Panel>
    )
  }

  const grade = GRADE[data.grade]

  return (
    <Panel className={className}>
      <PanelHeader
        title="Reachable from"
        actions={<Status verdict={grade.verdict} label={grade.label} />}
      />
      <PanelBody className="space-y-3.5">
        <p className="text-body leading-relaxed text-muted-foreground">{data.summary}</p>

        <DetailList>
          <Detail label="Allowlist">
            <span className="flex flex-wrap gap-1">
              {data.allowlist.map((cidr) => (
                <Tag key={cidr} mono>
                  {cidr}
                </Tag>
              ))}
              {data.allowlist.length === 0 && (
                <span className="text-muted-foreground">every address</span>
              )}
            </span>
          </Detail>
          <Detail label="Interfaces">
            <span className="flex flex-wrap gap-1">
              {data.interfaces.map((name) => (
                <Tag key={name} mono>
                  {name}
                </Tag>
              ))}
              {data.interfaces.length === 0 && <span className="text-muted-foreground">—</span>}
            </span>
          </Detail>
          {data.tailscaleIp && (
            <Detail label="Tailscale" className="font-mono">
              {data.tailscaleIp}
            </Detail>
          )}
        </DetailList>

        {data.recommendation && (
          <p className="border-t border-hairline pt-3 text-body leading-relaxed font-medium">
            {data.recommendation}
          </p>
        )}
      </PanelBody>
    </Panel>
  )
}
