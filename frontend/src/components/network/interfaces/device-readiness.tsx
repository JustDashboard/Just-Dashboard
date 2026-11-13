"use client"

import Link from "next/link"
import { get } from "@/lib/api"
import type { NetworkLink, NetworkLinkReadiness } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { LoadingRows, Notice } from "@/components/state"
import { Status, type DotTone } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { NetworkReadWarning } from "@/components/network/read-warning"

/** The kinds the backend reads readiness for. */
export const READINESS_KINDS = new Set([
  "vxlan",
  "gre",
  "gretap",
  "ip6gre",
  "ip6gretap",
  "macvlan",
  "dummy",
])

const TONE: Record<NetworkLinkReadiness["checks"][number]["state"], DotTone> = {
  ok: "running",
  warning: "warning",
  failed: "danger",
  unknown: "unknown",
  info: "notice",
}

const WORD: Record<NetworkLinkReadiness["checks"][number]["state"], string> = {
  ok: "Ready",
  warning: "Check",
  failed: "Failing",
  unknown: "Unknown",
  info: "Note",
}

/**
 * What this host alone can establish about whether a tunnel or a virtual
 * device can carry traffic: its underlay, its ends, its MTU and whether
 * anything has come in through it, with what it cannot see said under the
 * checks. Nothing here sends a packet to the other end.
 */
export function DeviceReadiness({ link }: { link: NetworkLink }) {
  const readiness = usePoll<NetworkLinkReadiness>(
    (signal) => get(`/network/links/${encodeURIComponent(link.name)}/readiness`, undefined, signal),
    30_000,
    [link.name],
  )
  const r = readiness.data
  const title =
    link.kind === "macvlan"
      ? "Isolation and reach"
      : link.kind === "dummy"
        ? "What uses it"
        : "Underlay and liveness"
  return (
    <Panel plain>
      <PanelHeader
        title={title}
        actions={
          link.kind === "dummy" && (
            <Button size="xs" variant="outline" asChild>
              <Link href={`/network/routing?route=new&device=${encodeURIComponent(link.name)}`}>
                Route a destination into {link.name}
              </Link>
            </Button>
          )
        }
      />
      <PanelBody>
        {r && (
          <NetworkReadWarning
            error={readiness.error}
            refresh={readiness.refresh}
            lastSuccess={readiness.lastSuccess}
            reading="readiness checks"
          />
        )}
        {!r ? (
          readiness.error ? (
            <Notice tone="warning" title="Readiness could not be read">
              {readiness.error.message}
            </Notice>
          ) : (
            <LoadingRows rows={3} />
          )
        ) : (
          <div className="flex flex-col gap-4">
            <ul className="flex flex-col divide-y divide-hairline" aria-label="Readiness checks">
              {r.checks.map((c) => (
                <li
                  key={c.id}
                  className="grid min-w-0 grid-cols-[6rem_minmax(0,1fr)] gap-x-3 py-2 text-body first:pt-0"
                >
                  <Status tone={TONE[c.state]} label={WORD[c.state]} className="h-5" />
                  <span className="min-w-0">
                    <span className="block font-medium">{c.label}</span>
                    <span className="block text-hint text-muted-foreground">{c.detail}</span>
                  </span>
                </li>
              ))}
            </ul>
            {r.limits.length > 0 && (
              <ul className="flex list-disc flex-col gap-1 pl-5 text-hint text-muted-foreground">
                {r.limits.map((limit) => (
                  <li key={limit}>{limit}</li>
                ))}
              </ul>
            )}
          </div>
        )}
      </PanelBody>
    </Panel>
  )
}
