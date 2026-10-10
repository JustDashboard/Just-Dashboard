"use client"

import { NetworkReadWarning } from "@/components/network/read-warning"
import { useAuth } from "@/hooks/use-auth"
import { Suspense, useEffect, useMemo, useState } from "react"
import { useSearchParams } from "next/navigation"
import { Plus } from "@/components/icons"
import { get } from "@/lib/api"
import { plural } from "@/lib/format"
import type { BGPView, NetworkLink, NetworkRoute, NetworkRouting } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Page, PageContext, Section } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ErrorState, LoadingPanel } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Field } from "@/components/form"
import { DecisionMap } from "@/components/network/routing/decision-map"
import { RouteTable, RuleTable } from "@/components/network/routing/route-tables"
import { AddRoute, AddRule, EditRoute } from "@/components/network/routing/add-route"
import { RouteHistoryPanel } from "@/components/network/routing/route-history"
import { parseTarget, targetMatches } from "@/components/network/routing/route-reading"
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
 * rule beside the rules. A target narrows every table to the routes that
 * cover it and marks the one each table selects; the same target narrows
 * the observed route history under the tables. Another page can open the
 * route form with a device already chosen (`?route=new&device=dummy0`): a
 * dummy's sheet hands off "route a destination into it" this way.
 */
export default function NetworkRoutingPage() {
  return (
    // The handoff lives in the query string, which the App Router only hands
    // out inside a Suspense boundary.
    <Suspense fallback={<LoadingPanel />}>
      <Routing />
    </Suspense>
  )
}

function Routing() {
  const { can } = useAuth()
  const params = useSearchParams()
  const admin = can("system.admin")
  const routing = usePoll<NetworkRouting>(
    (signal) => get("/network/routing", undefined, signal),
    15_000,
  )
  const links = usePoll<NetworkLink[]>((signal) => get("/network/links", undefined, signal), 30_000)
  const bgp = usePoll<BGPView>((signal) => get("/network/bgp", undefined, signal), 30_000)
  const [adding, setAdding] = useState<"route" | "rule" | undefined>(() =>
    params.get("route") === "new" ? "route" : undefined,
  )
  const [handoffDevice] = useState(() => params.get("device") ?? undefined)
  useEffect(() => {
    if (params.get("route") !== null || params.get("device") !== null) {
      window.history.replaceState(null, "", window.location.pathname)
    }
  }, [params])
  const [editing, setEditing] = useState<{ route: NetworkRoute; tableId: number }>()
  const [target, setTarget] = useState("")
  const parsedTarget = useMemo(() => parseTarget(target), [target])
  const matches = useMemo(
    () =>
      routing.data && parsedTarget ? targetMatches(routing.data.tables, parsedTarget) : undefined,
    [routing.data, parsedTarget],
  )

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
        <Field
          label="Target"
          htmlFor="route-target"
          hint="An address or network: each table shows the routes covering it and the one it selects"
          error={
            target.trim() && !parsedTarget ? "Use an IPv4 or IPv6 address or network." : undefined
          }
        >
          <Input
            id="route-target"
            value={target}
            onChange={(event) => setTarget(event.target.value)}
            placeholder="10.8.0.7 or 2001:db8::/48"
            className="font-mono sm:max-w-sm"
          />
        </Field>
        {data.tables.map((table, index) => (
          <RouteTable
            key={table.id}
            table={table}
            onChanged={routing.refresh}
            match={
              matches && parsedTarget
                ? { target: target.trim(), ...matches.get(table.id)! }
                : undefined
            }
            onEdit={(route) => setEditing({ route, tableId: table.id })}
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

      <RouteHistoryPanel target={parsedTarget ? target.trim() : undefined} />

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
        initialDevice={handoffDevice}
      />
      {editing && (
        <EditRoute
          open={admin}
          onOpenChange={(open) => !open && setEditing(undefined)}
          route={editing.route}
          tableId={editing.tableId}
          routing={data}
          links={links.data ?? []}
          onDone={routing.refresh}
        />
      )}
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
