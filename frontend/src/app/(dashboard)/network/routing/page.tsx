"use client"

import { Suspense, useCallback, useEffect, useMemo, useState } from "react"
import { useSearchParams } from "next/navigation"
import { Tabs as TabsPrimitive } from "radix-ui"
import { useAuth } from "@/hooks/use-auth"
import { get } from "@/lib/api"
import type { BGPView, NetworkLink, NetworkRoute, NetworkRouting } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useViewState } from "@/lib/view-state"
import { Page, PageContext } from "@/components/page"
import { ErrorState } from "@/components/state"
import { tabClasses } from "@/components/tabs"
import { Tabs, TabsContent } from "@/components/ui/tabs"
import { Skeleton } from "@/components/ui/skeleton"
import { SaveBarProvider } from "@/components/deploy/settings/save-bar"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { RoutingIdentity } from "@/components/network/routing/identity"
import { DecisionMap, DecisionMapWaiting } from "@/components/network/routing/decision-map"
import { RoutesTable, RulesTable, TableWaiting } from "@/components/network/routing/route-tables"
import { AddRoute, AddRule, EditRoute } from "@/components/network/routing/add-route"
import { RouteHistoryPanel } from "@/components/network/routing/route-history"
import { decisionRules, decisionTables } from "@/components/network/routing/decision-reading"
import { parseTarget, targetMatches } from "@/components/network/routing/route-reading"
import { useRouteLookup, type RouteQuestion } from "@/components/network/routing/route-lookup"
import { ForwardingSettings } from "@/components/network/routing/forwarding"
import { BGPPanel } from "@/components/network/routing/bgp"

const VIEWS = [
  { key: "overview", label: "Overview" },
  { key: "history", label: "History" },
  { key: "bgp", label: "BGP" },
  { key: "forwarding", label: "Forwarding" },
] as const
type View = (typeof VIEWS)[number]["key"]

/** What the page drew last time, so its first paint is the size of its next one. */
type Shape = { rules: number; tables: number; routes: number; allRules: number }
const FIRST_SHAPE: Shape = { rules: 4, tables: 4, routes: 8, allRules: 5 }

/**
 * Where a packet goes, and why.
 *
 * The page opens on what it is about — the default way out, how much there
 * is to read, whether the server routes for others — with Add route as its
 * command. Its first view is the decision the kernel makes for every packet
 * it sends: the policy rules in the order they are asked, each wired to the
 * table it looks in, with the path this browser's replies take lit, because
 * "why does it leave through that device" is the question routing pages are
 * opened with. The picture's head takes an address to ask the same of any
 * packet: the kernel's table lights, the routes under it narrow to those
 * covering the address, and the history narrows to the changes touching it.
 * Then every route in one table and the rules in another, the dashboard's
 * own editable from their rows.
 *
 * What is looked at less is a view each, named in the address (`?tab=`):
 * what the route observer saw change, BGP where FRR runs, and forwarding,
 * which is a setting and is saved from the page's bar.
 *
 * Another page can open the route form with a device already chosen
 * (`?route=new&device=dummy0`): a dummy's readiness hands off "route a
 * destination into it" this way.
 */
export default function NetworkRoutingPage() {
  return (
    // The view and the handoff live in the query string, which the App Router
    // only hands out inside a Suspense boundary. What stands there meanwhile
    // is the page's own first paint, so the page does not change shape when
    // the address arrives.
    <Suspense fallback={<Waiting />}>
      <Routing />
    </Suspense>
  )
}

const STRIP =
  "flex shrink-0 [scrollbar-width:none] gap-1 overflow-x-auto border-b border-hairline [&::-webkit-scrollbar]:hidden"
const OVERVIEW = "flex min-w-0 flex-col gap-6 pt-6 md:gap-8"

/** The first view before the first read: the picture and both tables at the size they had last. */
function OverviewWaiting({ shape }: { shape: Shape }) {
  return (
    <>
      <DecisionMapWaiting rules={shape.rules} tables={shape.tables} />
      <TableWaiting title="Routes" rows={shape.routes} wideFrom="1100px" chips />
      <TableWaiting title="Policy rules" rows={shape.allRules} wideFrom="768px" />
    </>
  )
}

function Waiting() {
  return (
    <Page className="gap-5 md:gap-5">
      <PageContext eyebrow="Network" title="Routing" />
      <RoutingIdentity onRefresh={() => {}} />
      <div>
        <div aria-hidden className={STRIP}>
          {VIEWS.map((v) => (
            <span key={v.key} className={tabClasses(v.key === "overview", "h-10")}>
              {v.label}
            </span>
          ))}
        </div>
        <div className={OVERVIEW}>
          <OverviewWaiting shape={FIRST_SHAPE} />
        </div>
      </div>
    </Page>
  )
}

function Routing() {
  const { can } = useAuth()
  const params = useSearchParams()
  const admin = can("system.admin")
  const requested = params.get("tab")
  const [view, setView] = useState<View>(() =>
    VIEWS.some((v) => v.key === requested) ? (requested as View) : "overview",
  )
  const open = useCallback((next: string) => {
    setView(next as View)
    // Replaced rather than pushed: a view is not a place Back should step through.
    const url = new URL(window.location.href)
    if (next === "overview") url.searchParams.delete("tab")
    else url.searchParams.set("tab", next)
    window.history.replaceState(null, "", `${url.pathname}${url.search}`)
  }, [])

  const routing = usePoll<NetworkRouting>(
    (signal) => get("/network/routing", undefined, signal),
    15_000,
  )
  // The forms' device pickers: nothing an account without the forms needs.
  const links = usePoll<NetworkLink[]>(
    (signal) => get("/network/links", undefined, signal),
    30_000,
    [],
    { enabled: admin },
  )
  const bgp = usePoll<BGPView>((signal) => get("/network/bgp", undefined, signal), 30_000, [], {
    enabled: view === "bgp",
  })

  const [adding, setAdding] = useState<"route" | "rule" | undefined>(() =>
    params.get("route") === "new" ? "route" : undefined,
  )
  const [handoffDevice] = useState(() => params.get("device") ?? undefined)
  useEffect(() => {
    if (params.get("route") === null && params.get("device") === null) return
    const url = new URL(window.location.href)
    url.searchParams.delete("route")
    url.searchParams.delete("device")
    window.history.replaceState(null, "", `${url.pathname}${url.search}`)
  }, [params])
  const [editing, setEditing] = useState<{ route: NetworkRoute; tableId: number }>()

  const [question, setQuestion] = useState<RouteQuestion>({ target: "", source: "", mark: "" })
  const lookup = useRouteLookup(question)
  const asked = question.target.trim()
  const parsedTarget = useMemo(() => parseTarget(asked), [asked])
  const data = routing.data
  const matches = useMemo(
    () => (data && parsedTarget ? targetMatches(data.tables, parsedTarget) : undefined),
    [data, parsedTarget],
  )
  const target = matches && {
    text: asked,
    matches,
    answered: lookup.state === "answer" ? lookup.result.table : undefined,
  }

  const [shape, remember] = useViewState<Shape>("network.routing.shape", FIRST_SHAPE)
  useEffect(() => {
    if (!data) return
    const family = data.clientPath.address.includes(":") ? "inet6" : "inet"
    const rules = decisionRules(data, family)
    const next: Shape = {
      rules: rules.length,
      tables:
        decisionTables(data, rules, family).length +
        (rules.some((r) => r.action !== "lookup" && r.action !== "goto") ? 1 : 0),
      routes: data.tables.reduce((n, table) => n + table.routes.length, 0),
      allRules: data.rules.length,
    }
    if (
      next.rules !== shape.rules ||
      next.tables !== shape.tables ||
      next.routes !== shape.routes ||
      next.allRules !== shape.allRules
    )
      remember(next)
  }, [data, shape, remember])

  const failed = !data && routing.error
  return (
    <SaveBarProvider>
      <Page className="animate-rise gap-5 md:gap-5">
        <PageContext eyebrow="Network" title="Routing" />
        <RoutingIdentity
          routing={data}
          failed={Boolean(failed)}
          onAddRoute={admin ? () => setAdding("route") : undefined}
          onAddRule={admin ? () => setAdding("rule") : undefined}
          onRefresh={routing.refresh}
        />
        {data && (
          <NetworkReadWarning
            error={routing.error}
            refresh={routing.refresh}
            lastSuccess={routing.lastSuccess}
          />
        )}

        <Tabs value={view} onValueChange={open} className="gap-0">
          <TabsPrimitive.List aria-label="Views of routing" className={STRIP}>
            {VIEWS.map((v) => (
              <TabsPrimitive.Trigger
                key={v.key}
                value={v.key}
                className={tabClasses(view === v.key, "h-10")}
              >
                {v.label}
              </TabsPrimitive.Trigger>
            ))}
          </TabsPrimitive.List>

          <TabsContent value="overview" className={OVERVIEW}>
            {failed ? (
              <ErrorState error={routing.error!} onRetry={routing.refresh} retryable />
            ) : !data ? (
              <OverviewWaiting shape={shape} />
            ) : (
              <>
                <DecisionMap
                  routing={data}
                  question={question}
                  onQuestion={setQuestion}
                  lookup={lookup}
                />
                <RoutesTable
                  tables={data.tables}
                  hiddenLocal={data.hiddenLocal}
                  target={target}
                  onChanged={routing.refresh}
                  onEdit={(route, tableId) => setEditing({ route, tableId })}
                />
                <RulesTable
                  rules={data.rules}
                  onChanged={routing.refresh}
                  onAdd={admin ? () => setAdding("rule") : undefined}
                />
              </>
            )}
          </TabsContent>

          <TabsContent value="history" className="min-w-0 pt-6">
            {view === "history" && (
              <RouteHistoryPanel
                target={parsedTarget ? asked : undefined}
                onClearTarget={() => setQuestion((held) => ({ ...held, target: "" }))}
              />
            )}
          </TabsContent>

          <TabsContent value="bgp" className="min-w-0 pt-6">
            {view === "bgp" && <BGPPanel read={bgp} />}
          </TabsContent>

          {/* Kept mounted: a switch changed and not yet saved stays on the
              page's bar while another view is read. */}
          <TabsContent
            value="forwarding"
            forceMount
            className="min-w-0 pt-6 data-[state=inactive]:hidden"
          >
            <div className="mx-auto w-full max-w-3xl min-w-0 pb-20">
              {failed ? (
                view === "forwarding" && (
                  <ErrorState error={routing.error!} onRetry={routing.refresh} retryable />
                )
              ) : !data ? (
                <div aria-busy className="space-y-4">
                  <Skeleton className="h-5 w-28" />
                  <Skeleton className="h-12 w-full" />
                  <Skeleton className="h-12 w-full" />
                </div>
              ) : (
                <ForwardingSettings forwarding={data.forwarding} onChanged={routing.refresh} />
              )}
            </div>
          </TabsContent>
        </Tabs>

        {data && (
          <>
            <AddRoute
              open={admin && adding === "route"}
              onOpenChange={(next) => !next && setAdding(undefined)}
              routing={data}
              links={links.data ?? []}
              onAdded={routing.refresh}
              initialDevice={handoffDevice}
            />
            {editing && (
              <EditRoute
                open={admin}
                onOpenChange={(next) => !next && setEditing(undefined)}
                route={editing.route}
                tableId={editing.tableId}
                routing={data}
                links={links.data ?? []}
                onDone={routing.refresh}
              />
            )}
            <AddRule
              open={admin && adding === "rule"}
              onOpenChange={(next) => !next && setAdding(undefined)}
              routing={data}
              links={links.data ?? []}
              onAdded={routing.refresh}
            />
          </>
        )}
      </Page>
    </SaveBarProvider>
  )
}
