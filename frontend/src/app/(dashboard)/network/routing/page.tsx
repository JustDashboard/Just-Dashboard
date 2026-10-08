"use client"

import { NetworkReadWarning } from "@/components/network/read-warning"
import { useAuth } from "@/hooks/use-auth"
import { useState } from "react"
import { Plus } from "@/components/icons"
import { get } from "@/lib/api"
import { plural } from "@/lib/format"
import type { BGPView, NetworkLink, NetworkRouting } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Page, PageContext, Section } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ErrorState, LoadingPanel } from "@/components/state"
import { Button } from "@/components/ui/button"
import { DecisionMap } from "@/components/network/routing/decision-map"
import { RouteTable, RuleTable } from "@/components/network/routing/route-tables"
import { AddRoute, AddRule } from "@/components/network/routing/add-route"
import { ForwardingSwitches } from "@/components/network/routing/forwarding"
import { BGPBlock } from "@/components/network/routing/bgp"

/**
 * Where a packet goes, and why.
 *
 * The page opens on the decision the kernel makes for every packet it sends
 * — the policy rules in the order they are asked, each leading to the table
 * it looks in — with the path this browser's replies take lit, because "why
 * does it leave through that device" is the question routing pages are
 * opened with. Then whether the server routes at all, per family, with what
 * depends on it; then each table as the table it is, the dashboard's own
 * routes removable from their rows; then BGP, where FRR runs.
 *
 * The commands sit with what they add to: a route beside the main table, a
 * rule beside the rules.
 */
export default function NetworkRoutingPage() {
  const { can } = useAuth()
  const admin = can("system.admin")
  const routing = usePoll<NetworkRouting>(
    (signal) => get("/network/routing", undefined, signal),
    15_000,
  )
  const links = usePoll<NetworkLink[]>((signal) => get("/network/links", undefined, signal), 30_000)
  const bgp = usePoll<BGPView>((signal) => get("/network/bgp", undefined, signal), 30_000)
  const [adding, setAdding] = useState<"route" | "rule">()

  if (!routing.data) {
    return (
      <Page className="animate-rise">
        <PageContext eyebrow="Network" title="Routing" />
        {routing.error ? (
          <ErrorState error={routing.error} onRetry={routing.refresh} />
        ) : (
          <LoadingPanel />
        )}
      </Page>
    )
  }
  const data = routing.data
  const routes = data.tables.reduce((n, t) => n + t.routes.length, 0)
  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Network" title="Routing" />
      {routing.data && (
        <NetworkReadWarning
          error={routing.error}
          refresh={routing.refresh}
          lastSuccess={routing.lastSuccess}
        />
      )}

      <Panel plain>
        <PanelHeader
          title="How a packet is routed"
          actions={
            <span className="text-hint text-muted-foreground">
              {plural(data.rules.length, "rule")} · {plural(data.tables.length, "table")} ·{" "}
              {plural(routes, "route")}
            </span>
          }
        />
        <PanelBody>
          <DecisionMap routing={data} />
        </PanelBody>
      </Panel>

      <Section title="Forwarding">
        <ForwardingSwitches forwarding={data.forwarding} onChanged={routing.refresh} />
      </Section>

      <Section title="Tables">
        {data.tables.map((table, index) => (
          <RouteTable
            key={table.id}
            table={table}
            onChanged={routing.refresh}
            actions={
              index === 0 ? (
                <Button
                  size="xs"
                  variant="outline"
                  onClick={() => setAdding("route")}
                  disabled={!admin}
                >
                  <Plus aria-hidden />
                  Add route
                </Button>
              ) : undefined
            }
          />
        ))}
        {data.hiddenLocal > 0 && (
          <p className="text-hint text-muted-foreground">
            The local table&rsquo;s {data.hiddenLocal} entries — the kernel&rsquo;s own record of
            which addresses are this machine&rsquo;s — are not drawn.
          </p>
        )}
      </Section>

      <RuleTable
        rules={data.rules}
        onChanged={routing.refresh}
        actions={
          <Button size="xs" variant="outline" onClick={() => setAdding("rule")} disabled={!admin}>
            <Plus aria-hidden />
            Add rule
          </Button>
        }
      />

      <Panel plain>
        <PanelHeader title="BGP" />
        <PanelBody>
          {bgp.error ? (
            <ErrorState error={bgp.error} onRetry={bgp.refresh} />
          ) : (
            <BGPBlock bgp={bgp.data} />
          )}
        </PanelBody>
      </Panel>

      <AddRoute
        open={admin && adding === "route"}
        onOpenChange={(open) => !open && setAdding(undefined)}
        routing={data}
        links={links.data ?? []}
        onAdded={routing.refresh}
      />
      <AddRule
        open={admin && adding === "rule"}
        onOpenChange={(open) => !open && setAdding(undefined)}
        routing={data}
        links={links.data ?? []}
        onAdded={routing.refresh}
      />
    </Page>
  )
}
