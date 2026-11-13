"use client"

import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react"
import { useSearchParams } from "next/navigation"
import { Tabs as TabsPrimitive } from "radix-ui"
import { get } from "@/lib/api"
import type { DNSTLSReport, DNSView, HostRecords } from "@/lib/types"
import { useViewState } from "@/lib/view-state"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { Globe, Inspect, Plus, RefreshClockwise } from "@/components/icons"
import { FactDot, HostFact, HostIdentity } from "@/components/metrics/host-identity"
import { Page, PageContext } from "@/components/page"
import { ErrorState } from "@/components/state"
import { Status } from "@/components/status-dot"
import { tabClasses } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Tabs } from "@/components/ui/tabs"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { VerbMenu, type Verb } from "@/components/verbs"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { DNSLog } from "@/components/network/dns/dns-log"
import { Bar } from "@/components/network/dns/ghost"
import { Lookups } from "@/components/network/dns/lookups"
import {
  SHAPE_GUESS,
  resolverOf,
  shapeOf,
  upstreamWords,
  type DNSShape,
} from "@/components/network/dns/reading"
import { ResolverView } from "@/components/network/dns/resolver-view"
import type { DNSServiceAsk, DNSServiceRequest } from "@/components/network/dns/services"
import { ServicesView } from "@/components/network/dns/services-view"
import { SettingsView } from "@/components/network/dns/settings-view"
import {
  DNS_VIEWS,
  DNS_VIEW_NAME,
  dnsViewOf,
  type DNSViewKey,
  type LookupTool,
} from "@/components/network/dns/views"

/** How many reads of the lookup count the Lookups line draws: ten minutes of them. */
const TREND_READS = 60

const onHash = (changed: () => void) => {
  window.addEventListener("hashchange", changed)
  return () => window.removeEventListener("hashchange", changed)
}
const hashNow = () => window.location.hash
const noHash = () => ""

/**
 * Who answers this server's lookups, and how.
 *
 * The page opens on the resolver itself: its name, the address programs ask,
 * where a lookup goes from there in words, and the share it answers from its
 * cache, with Resolve a name as the one command. Under that line it is five
 * views, named in the address (`?tab=`), because it does four jobs and keeps
 * a log:
 *
 * - **Resolver**, the resolver as it is now: the chain a name goes down, drawn
 *   on `WireFlow`, with what resolved has counted on its node and under the
 *   picture; what answers on port 53; each link's own scope; the certificates
 *   the upstreams present; who owns the file programs read.
 * - **Lookups**, one small workbench for the three questions asked of a name.
 * - **Settings**, the upstreams, DNS over TLS, DNSSEC, the cache and the
 *   host's own records as one column of sections with one floating Save.
 * - **Services**, the DNS filters found on this server and the engines
 *   connected through their own API.
 * - **Log**, what the things that answer have written.
 *
 * It was one 4,800px column under a row of four tiles, and it had no shape
 * until its first read landed. The identity line and the strip are drawn from
 * the first frame and every block of a view waits at the height it will have
 * (`ghost.tsx`), so the read lands in place.
 *
 * The chain is read every ten seconds. A wire into the stub carries while the
 * resolver's lookup count rose over the last interval; the wire to the server
 * in use carries because resolved says it is the one in use. Nothing is drawn
 * live that is not read live.
 */
export function DNSPage() {
  const search = useSearchParams()
  const hash = useSyncExternalStore(onHash, hashNow, noHash)
  const view = dnsViewOf(search.get("tab"), hash)
  const { can } = useAuth()
  const admin = can("system.admin")

  // A view that holds what the reader typed stays mounted once it has been
  // opened: a lookup's answer and a setting's draft outlive a look at the chain.
  const [opened, setOpened] = useState<DNSViewKey[]>([view])
  if (!opened.includes(view)) setOpened([...opened, view])

  const dns = usePoll<DNSView>((signal) => get("/network/dns/", undefined, signal), 10_000)
  const hosts = usePoll<HostRecords>(
    (signal) => get("/network/dns/hosts", undefined, signal),
    30_000,
    [],
    { enabled: opened.includes("settings") },
  )
  const data = dns.data

  const [shape, setShape] = useViewState<DNSShape>("network.dns.shape", SHAPE_GUESS)
  useEffect(() => {
    if (!data) return
    const next = shapeOf(data)
    setShape((held) =>
      held.upstreams === next.upstreams &&
      held.port53 === next.port53 &&
      held.links === next.links &&
      held.readings === next.readings
        ? held
        : next,
    )
  }, [data, setShape])

  // Whether the lookup count rose since the last read, and by how much at
  // each read since the page opened. Derived while rendering from the count
  // seen last time, so there is no effect and no ref written mid-render.
  const transactions = data?.resolved.statistics?.transactions
  const [seen, setSeen] = useState<{
    at?: number
    count?: number
    asking: boolean
    trend: number[]
  }>({ asking: false, trend: [] })
  if (dns.lastSuccess !== seen.at) {
    const rose =
      seen.count !== undefined && transactions !== undefined
        ? Math.max(0, transactions - seen.count)
        : undefined
    setSeen({
      at: dns.lastSuccess,
      count: transactions,
      asking: (rose ?? 0) > 0,
      trend: rose === undefined ? seen.trend : [...seen.trend, rose].slice(-TREND_READS),
    })
  }

  // A detected DNS server's Connect or Inspect, and the menu's two verbs,
  // open the engines' sheets; the nonce makes the same press open it again.
  const [serviceRequest, setServiceRequest] = useState<DNSServiceRequest>()
  const [tls, setTLS] = useState<DNSTLSReport>()

  // The address says which view, so a link to the settings opens on them;
  // replaced rather than pushed, because a view is not a place Back should
  // step through.
  const show = useCallback(
    (next: DNSViewKey, carry: { tool?: LookupTool; anchor?: string } = {}) => {
      const url = new URL(window.location.href)
      if (next === "resolver") url.searchParams.delete("tab")
      else url.searchParams.set("tab", next)
      if (next !== "lookups") url.searchParams.delete("tool")
      else if (carry.tool) url.searchParams.set("tool", carry.tool)
      url.hash = carry.anchor ? `#${carry.anchor}` : ""
      window.history.replaceState(null, "", `${url.pathname}${url.search}${url.hash}`)
    },
    [],
  )

  // An anchor is scrolled to once its view has drawn it: the section a link
  // names is not in the document until the first read lands.
  const scrolled = useRef("")
  useEffect(() => {
    const id = hash.replace(/^#/, "")
    if (!id || scrolled.current === id) return
    const target = document.getElementById(id)
    if (!target) return
    scrolled.current = id
    target.scrollIntoView({ block: "start" })
  }, [hash, view, data, hosts.data])

  const [caret, setCaret] = useState(0)
  useEffect(() => {
    if (caret > 0) document.getElementById("lookup-name")?.focus()
  }, [caret])
  const resolve = () => {
    show("lookups", { tool: "resolve" })
    setCaret((held) => held + 1)
  }
  const requestService = (request: DNSServiceAsk) => {
    setServiceRequest({ ...request, nonce: (serviceRequest?.nonce ?? 0) + 1 })
    show("services")
  }

  const verbs: Verb[] = [
    {
      key: "refresh",
      label: "Refresh",
      icon: RefreshClockwise,
      run: () => {
        dns.refresh()
        if (opened.includes("settings")) hosts.refresh()
      },
    },
    ...(admin
      ? [
          {
            key: "connect",
            label: "Connect a DNS engine",
            icon: Plus,
            run: () => requestService({ kind: "connect" }),
          },
          {
            key: "provision",
            label: "Review an owned setup",
            icon: Inspect,
            run: () => requestService({ kind: "provision" }),
          },
        ]
      : []),
  ]

  const failed = !data && dns.error
  const unread = failed ? <ErrorState error={dns.error!} onRetry={dns.refresh} /> : null
  const panel = "min-w-0 pt-6 focus-ring data-[state=inactive]:hidden"

  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Network" title="DNS" />
      {/* One block, as on a process's page: the strip's hairline is the line
          under the identity, rather than a second one a band of ground away. */}
      <div className="flex min-w-0 flex-col gap-5">
        <Identity
          view={data}
          failed={Boolean(failed)}
          command={
            <Button size="sm" onClick={resolve} aria-label="Resolve a name">
              <span className="sm:hidden">Resolve</span>
              <span className="max-sm:hidden">Resolve a name</span>
            </Button>
          }
          menu={<VerbMenu verbs={verbs} label="DNS actions" />}
        />
        {data && (
          <NetworkReadWarning
            error={dns.error}
            refresh={dns.refresh}
            lastSuccess={dns.lastSuccess}
          />
        )}

        <Tabs
          value={view}
          onValueChange={(next) => show(next as DNSViewKey)}
          className="min-w-0 gap-0"
        >
          <TabsPrimitive.List
            aria-label="Views of DNS"
            className="flex shrink-0 [scrollbar-width:none] gap-1 overflow-x-auto border-b border-hairline [&::-webkit-scrollbar]:hidden"
          >
            {DNS_VIEWS.map((key) => (
              <TabsPrimitive.Trigger
                key={key}
                value={key}
                className={tabClasses(view === key, "h-10")}
              >
                {DNS_VIEW_NAME[key]}
              </TabsPrimitive.Trigger>
            ))}
          </TabsPrimitive.List>

          <TabsPrimitive.Content value="resolver" className={panel}>
            {unread ?? (
              <ResolverView
                view={data}
                shape={shape}
                asking={seen.asking}
                trend={seen.trend}
                tls={tls}
                onTLS={setTLS}
                onChanged={dns.refresh}
              />
            )}
          </TabsPrimitive.Content>

          {/* Kept in the document once opened, and hidden by hand: Radix shows
            a force-mounted panel whichever tab is chosen. */}
          <TabsPrimitive.Content
            value="lookups"
            forceMount
            hidden={view !== "lookups"}
            className={panel}
          >
            {opened.includes("lookups") && (
              <Lookups tool={search.get("tool")} onTool={(tool) => show("lookups", { tool })} />
            )}
          </TabsPrimitive.Content>

          <TabsPrimitive.Content
            value="settings"
            forceMount
            hidden={view !== "settings"}
            className={panel}
          >
            {opened.includes("settings") &&
              (unread ?? <SettingsView view={data} hosts={hosts} onChanged={dns.refresh} />)}
          </TabsPrimitive.Content>

          <TabsPrimitive.Content
            value="services"
            forceMount
            hidden={view !== "services"}
            className={panel}
          >
            {opened.includes("services") && (
              <>
                {unread}
                <ServicesView
                  view={data}
                  failed={Boolean(failed)}
                  request={serviceRequest}
                  onRequest={requestService}
                  onUpstreams={() => show("settings", { anchor: "dns-upstreams" })}
                />
              </>
            )}
          </TabsPrimitive.Content>

          <TabsPrimitive.Content value="log" className={panel}>
            {unread ?? <DNSLog view={data} />}
          </TabsPrimitive.Content>
        </Tabs>
      </div>
    </Page>
  )
}

/**
 * The line the page opens on, laid out as a project's header: the resolver's
 * mark and name with its state beside it, then the address programs ask, where
 * a lookup goes from there, and the share answered from the cache. It is drawn
 * before the first read with its command already in place, so the read fills
 * the line rather than moving what is under it.
 */
function Identity({
  view,
  failed,
  command,
  menu,
}: {
  view?: DNSView
  failed: boolean
  command: React.ReactNode
  menu: React.ReactNode
}) {
  if (!view) {
    return (
      <HostIdentity
        fallback={Globe}
        className="border-b-0 pb-0"
        label="About the resolver"
        title={failed ? "DNS" : <TextShimmer>Reading the resolver…</TextShimmer>}
        state={
          failed ? (
            <Status tone="danger" label="Not read" />
          ) : (
            <Status tone="unknown" label="Reading" />
          )
        }
        facts={failed ? <span>the resolver could not be read</span> : <Bar className="w-56" />}
        command={command}
        menu={menu}
      />
    )
  }
  const resolver = resolverOf(view)
  const upstream = upstreamWords(view)
  const stats = view.resolved.statistics
  return (
    <HostIdentity
      mark={resolver.product}
      fallback={Globe}
      className="border-b-0 pb-0"
      label="About the resolver"
      title={resolver.path ? <span className="font-mono">{resolver.name}</span> : resolver.name}
      state={<Status tone={resolver.state.tone} label={resolver.state.label} />}
      facts={
        <>
          {resolver.address && (
            <>
              <span className="font-mono text-foreground">{resolver.address}</span>
              <FactDot />
            </>
          )}
          <HostFact product={upstream.product}>{upstream.words}</HostFact>
          {stats && (
            <>
              <FactDot />
              <span className="numeric">{Math.round(stats.hitPercent)}% from cache</span>
            </>
          )}
        </>
      }
      command={command}
      menu={menu}
    />
  )
}
