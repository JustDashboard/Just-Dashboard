"use client"

import { Plus, RefreshClockwise, Route } from "@/components/icons"
import { plural } from "@/lib/format"
import type { NetworkRouting } from "@/lib/types"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Status } from "@/components/status-dot"
import { VerbMenu, type Verb } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { defaultRoute, forwardingState } from "./route-reading"

/**
 * What the page is about, on the line every page opens on: the way out this
 * server takes by default, how much there is to read (tables, routes, rules)
 * and whether it routes for others, with Add route as the one command and
 * the rest behind the ⋯. The counts stood on the picture's title line and
 * the page had no opening line at all.
 *
 * It is drawn before the first read too, on the same lines, so the picture
 * under it does not move when the read lands.
 */
export function RoutingIdentity({
  routing,
  failed,
  onAddRoute,
  onAddRule,
  onRefresh,
}: {
  /** Undefined until the first read. */
  routing?: NetworkRouting
  /** The first read failed: there is nothing to say about the server yet. */
  failed?: boolean
  /** Absent for an account that cannot add one. */
  onAddRoute?: () => void
  onAddRule?: () => void
  onRefresh: () => void
}) {
  const verbs: Verb[] = [
    ...(onAddRule
      ? [{ key: "rule", label: "Add rule", icon: Plus, run: onAddRule, disabled: !routing }]
      : []),
    { key: "refresh", label: "Refresh", icon: RefreshClockwise, run: onRefresh },
  ]
  const forwarding = routing && forwardingState(routing.forwarding)
  const way = routing && defaultRoute(routing)
  const routes = routing?.tables.reduce((n, table) => n + table.routes.length, 0) ?? 0
  return (
    <HostIdentity
      label="About routing"
      // The view strip under it carries the hairline.
      className="border-b-0 pb-0"
      fallback={Route}
      title="Routing"
      state={
        forwarding ? (
          <Status tone={forwarding.on ? "running" : "stopped"} label={forwarding.label} />
        ) : failed ? (
          <Status tone="danger" label="Not read" />
        ) : (
          <span className="text-xs font-medium">
            <TextShimmer>Reading the kernel…</TextShimmer>
          </span>
        )
      }
      facts={
        routing ? (
          <>
            {way ? (
              // Each word its own box: mono set inside a line of the body
              // face makes that line a pixel taller than the one it replaces.
              <span className="flex min-w-0 flex-wrap items-center gap-x-1">
                <span>default</span>{" "}
                {way.gateway && (
                  <>
                    <span>via</span>{" "}
                    <span className="min-w-0 font-mono break-all text-foreground">
                      {way.gateway}
                    </span>{" "}
                  </>
                )}
                {way.device && (
                  <>
                    <span>on</span> <span className="font-mono text-foreground">{way.device}</span>
                  </>
                )}
              </span>
            ) : (
              <span className="text-warning">no default route</span>
            )}
            <FactDot />
            <span className="numeric">{plural(routing.tables.length, "table")}</span>
            <FactDot />
            <span className="numeric">{plural(routes, "route")}</span>
            <FactDot />
            <span className="numeric">{plural(routing.rules.length, "rule")}</span>
          </>
        ) : (
          // The facts' own line, held while there is nothing to put on it.
          <span aria-hidden className="max-sm:min-h-9">
            &nbsp;
          </span>
        )
      }
      command={
        onAddRoute && (
          <Button size="sm" onClick={onAddRoute} disabled={!routing}>
            <Plus aria-hidden className="size-3.5" />
            Add route
          </Button>
        )
      }
      menu={<VerbMenu verbs={verbs} label="Routing actions" />}
    />
  )
}
