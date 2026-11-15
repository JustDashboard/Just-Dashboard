"use client"

import { memo } from "react"
import { useSearchParams } from "next/navigation"
import { Tabs as TabsPrimitive } from "radix-ui"
import { get } from "@/lib/api"
import type { ContainerTraffic, NetworkLink } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useNow } from "@/components/deploy/vocabulary"
import { Page, PageContext } from "@/components/page"
import { tabClasses } from "@/components/tabs"
import { Tabs, TabsContent } from "@/components/ui/tabs"
import { useLiveTraffic } from "@/components/network/use-live-traffic"
import { TRAFFIC_TABS, tabFromParams, type TrafficTab } from "./address"
import { Bandwidth } from "./bandwidth"
import { TrafficBudgets } from "./budgets"
import { EBPFPrograms } from "./ebpf"
import { TrafficIdentity } from "./identity"
import { Shaping } from "./shaping"
import { coveringWindow, spanFromParams, writeSpan, type TrafficSpan } from "./span"

// The page holds the two-second ring, so it renders every two seconds; the
// views that read none of it are skipped unless their own props change.
const Budgets = memo(TrafficBudgets)
const Queues = memo(Shaping)
const Programs = memo(EBPFPrograms)

/**
 * What is moving bytes, and how fast it may.
 *
 * The page was one scroll of some four thousand pixels: a section title, four
 * tiles, nine charts, the budgets with a form under them, two bands, a
 * switch, two tables, a table of queues, four more tiles and a table of
 * programs. It opens now on the uplink's identity line — what it carries, how
 * fresh that is, whether history is kept — and its four subjects are four
 * views in the address (`?tab=`):
 *
 * - **Bandwidth**: two readings in words (packets, TCP's health), each
 *   device's chart over the window in the address (`?window=`, `?from=&to=`),
 *   then who is moving the bytes, programs now and containers over the
 *   window;
 * - **Budgets**: the transfer budgets as bands, set from the section's verb;
 * - **Shaping**: each device's queue and limits as a card, the evidence on
 *   its sheet, and how TCP paces itself;
 * - **eBPF programs**: what the kernel has loaded, each opening its own page.
 *
 * Where each of the eight tiles' figures went is said in `identity.tsx`,
 * `live-context.tsx` and `ebpf.tsx`.
 */
export function TrafficPage() {
  const params = useSearchParams()
  const tab = tabFromParams(params)
  const span = spanFromParams(params)
  const now = useNow(60_000)
  const range = coveringWindow(span, Math.floor(now / 1000))
  const live = useLiveTraffic(true, 2000, true)
  const links = usePoll<NetworkLink[]>((signal) => get("/network/links", undefined, signal), 30_000)
  // Read here and not in the view that draws them: the same answer says
  // whether the server keeps any history, which is the identity line's.
  const containers = usePoll<ContainerTraffic>(
    (signal) => get("/network/traffic/containers", { window: range }, signal),
    30_000,
    [range],
  )

  // The address says which view and which window, so a link to the queues
  // opens on the queues; replaced rather than pushed, because neither is a
  // place Back should step through.
  const rewrite = (change: (query: URLSearchParams) => void) => {
    const url = new URL(window.location.href)
    change(url.searchParams)
    window.history.replaceState(null, "", `${url.pathname}${url.search}`)
  }
  const setTab = (next: string) =>
    rewrite((query) => {
      if (next === "bandwidth") query.delete("tab")
      else query.set("tab", next)
      // A program type narrows one view's table and means nothing on another.
      if (next !== "ebpf") query.delete("type")
    })
  const setSpan = (next: TrafficSpan) => rewrite((query) => writeSpan(query, next))

  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Network" title="Traffic" />
      {/* The strip's hairline is the identity line's: one rule under the
          header, as a process's page and a project's have. */}
      <Tabs value={tab} onValueChange={setTab} className="gap-0">
        <TrafficIdentity
          className="border-b-0 pb-3"
          live={live}
          links={links.data}
          linksError={links.error}
          recording={containers.data?.recording}
        />
        <TabsPrimitive.List
          aria-label="Views of the traffic"
          className="flex shrink-0 [scrollbar-width:none] gap-1 overflow-x-auto border-b border-hairline [&::-webkit-scrollbar]:hidden"
        >
          {TRAFFIC_TABS.map((view) => (
            <TabsPrimitive.Trigger
              key={view.key}
              value={view.key}
              className={tabClasses(tab === view.key, "h-10")}
            >
              {view.label}
            </TabsPrimitive.Trigger>
          ))}
        </TabsPrimitive.List>
        {/* One view is mounted at a time: each polls for itself, and a view
            nobody is looking at must not keep the server busy. */}
        <View tab="bandwidth" shown={tab}>
          <Bandwidth
            live={live}
            links={links}
            containers={containers}
            range={range}
            span={span}
            onSpan={setSpan}
          />
        </View>
        <View tab="budgets" shown={tab}>
          <Budgets links={links.data} />
        </View>
        <View tab="shaping" shown={tab}>
          <Queues links={links.data} />
        </View>
        <View tab="ebpf" shown={tab}>
          <Programs />
        </View>
      </Tabs>
    </Page>
  )
}

function View({
  tab,
  shown,
  children,
}: {
  tab: TrafficTab
  shown: TrafficTab
  children: React.ReactNode
}) {
  return (
    <TabsContent value={tab} className="min-w-0 pt-6">
      {shown === tab && children}
    </TabsContent>
  )
}
