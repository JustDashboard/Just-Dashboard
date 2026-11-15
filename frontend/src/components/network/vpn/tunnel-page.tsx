"use client"

import { useCallback, useMemo, useState } from "react"
import Link from "next/link"
import { useParams, useRouter, useSearchParams } from "next/navigation"
import { Tabs as TabsPrimitive } from "radix-ui"
import { ArrowLeft, ChevronDown, ChevronUp, LockClosed } from "@/components/icons"
import { ApiError, get } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import type { VPNView, WGArchived, WGInterface } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { IconAction } from "@/components/icon-action"
import { FactDot, HostFact, HostIdentity } from "@/components/metrics/host-identity"
import { Page, PageContext } from "@/components/page"
import { EmptyState, ErrorState } from "@/components/state"
import { Status } from "@/components/status-dot"
import { ChipCount, tabClasses } from "@/components/tabs"
import { ProductGlyph } from "@/components/product-logo"
import { WireMark } from "@/components/deploy/wire"
import { Button } from "@/components/ui/button"
import { Tabs, TabsContent } from "@/components/ui/tabs"
import { WireFlow } from "@/components/wire-flow"
import { Workspace, WorkspaceHelp } from "@/components/workspace/workspace"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { archivedLine, useArchive, useRestore } from "./archive"
import { usePeerSheets } from "./peers"
import { useHistory } from "./record"
import type { HistoryWindow } from "./record-logic"
import { TunnelExit } from "./tunnel-exit"
import { IdentitySkeleton, TunnelIdentity, useTunnelControl } from "./tunnel-identity"
import { TunnelPicture } from "./tunnel-picture"
import { TunnelRail } from "./tunnel-rail"
import { railTunnels, tunnelHref, type TunnelTab } from "./tunnel-reading"
import { TunnelEndpoint, TunnelHistory, TunnelTraffic } from "./tunnel-views"

const BACK = (
  <Link
    href="/network/vpn"
    className="inline-flex items-center gap-1 rounded-sm focus-ring hover:underline"
  >
    <ArrowLeft className="size-3" /> VPN
  </Link>
)

type Step = { disabled: boolean; run: () => void }

/**
 * One WireGuard tunnel, as a place of its own.
 *
 * Its record was a sheet over the VPN page until 0.8.7: the endpoint check,
 * a chart and the lifecycle in a drawer's width, opened by a small "History"
 * button, with no address to send anybody. A tunnel is a destination with a
 * way back now, on the shape a process and a unit have (`procs/service-page`):
 * the line it opens on with its verbs, its views as the underlined strip
 * (`?tab=`), and from `xl` the other tunnels beside it.
 *
 * - **Overview**: the picture with every peer, whether its clients' internet
 *   leaves through here, and the endpoint check.
 * - **Traffic**: the tunnel's traffic over six hours, a day or a week, and
 *   what each peer has moved.
 * - **History**: everything done to it, with each outcome.
 *
 * A tunnel that was removed keeps its page: its record outlives it, and the
 * page offers to restore it while its file is in the archive.
 */
export function TunnelPage() {
  const params = useParams<{ tunnel: string }>()
  const search = useSearchParams()
  const router = useRouter()
  const { can } = useAuth()
  const admin = can("system.admin")
  const name = decodeName(params.tunnel)
  const [tab, setChosen] = useState(search.get("tab") ?? "")
  const setTab = useCallback((next: string, first: string) => {
    setChosen(next)
    // The address says which view, so a link to a tunnel's history opens on
    // it; replaced rather than pushed, because a view is not a place Back
    // should step through.
    const url = new URL(window.location.href)
    if (next === first) url.searchParams.delete("tab")
    else url.searchParams.set("tab", next)
    window.history.replaceState(null, "", `${url.pathname}${url.search}`)
  }, [])

  const vpn = usePoll<VPNView>((signal) => get("/network/vpn", undefined, signal), 10_000, [], {
    enabled: admin,
  })
  const archive = useArchive(admin)
  const live = vpn.data?.wireguard.interfaces
  const rail = useMemo(() => railTunnels(live ?? [], archive.data ?? []), [live, archive.data])
  const at = rail.findIndex((t) => t.name === name)
  const open = useCallback(
    (next: string) => router.push(tunnelHref(next, tab || "overview")),
    [router, tab],
  )
  const adjacent = (direction: number) => {
    const next = at >= 0 ? rail[at + direction] : rail[direction > 0 ? 0 : -1]
    if (next) open(next.name)
  }
  const previous: Step = { disabled: at <= 0, run: () => adjacent(-1) }
  const next: Step = {
    disabled: rail.length === 0 || at >= rail.length - 1,
    run: () => adjacent(1),
  }
  const refreshVPN = vpn.refresh
  const refreshArchive = archive.refresh
  const changed = useCallback(() => {
    refreshVPN()
    refreshArchive()
  }, [refreshVPN, refreshArchive])

  if (!admin) {
    return (
      <Page>
        <PageContext eyebrow={BACK} title={name} />
        <EmptyState
          icon={LockClosed}
          title="VPN needs the admin capability"
          description="Tunnel peers and their client configurations are visible to administrators."
        />
      </Page>
    )
  }

  const tunnel = live?.find((t) => t.name === name)
  const steps = (
    <span className="flex items-center">
      <IconAction label="Previous tunnel" disabled={previous.disabled} onClick={previous.run}>
        <ChevronUp />
      </IconAction>
      <IconAction label="Next tunnel" disabled={next.disabled} onClick={next.run}>
        <ChevronDown />
      </IconAction>
      <span className="max-sm:hidden">
        <WorkspaceHelp compact />
      </span>
    </span>
  )

  return (
    <Workspace
      name="Tunnel"
      stateKey="network.vpn.tunnel"
      refresh={changed}
      commands={[
        {
          id: "previous",
          label: "Previous tunnel",
          keys: "Alt+↑",
          chord: "Alt+ArrowUp",
          disabled: previous.disabled,
          run: previous.run,
        },
        {
          id: "next",
          label: "Next tunnel",
          keys: "Alt+↓",
          chord: "Alt+ArrowDown",
          disabled: next.disabled,
          run: next.run,
        },
      ]}
    >
      {/* Held to the window where the rail stands beside it, so the list and
          the tunnel's line stay put while a view scrolls; narrower, the page
          is the one pane and scrolls as a whole. */}
      <Page fill="xl" className="gap-0 md:gap-0">
        <div className="flex min-w-0 flex-1 gap-6 xl:min-h-0">
          <TunnelRail
            className="hidden w-64 shrink-0 xl:flex"
            tunnels={rail}
            current={name}
            loading={!vpn.data && !vpn.error}
            onOpen={open}
          />
          <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-5">
            {!vpn.data ? (
              <>
                <PageContext eyebrow={BACK} title={name} actions={steps} />
                {vpn.error ? (
                  <ErrorState error={vpn.error} onRetry={vpn.refresh} retryable />
                ) : (
                  <>
                    <IdentitySkeleton mark="wireguard" />
                    <WireFlow
                      loading
                      start={{ title: "Devices", nodes: [] }}
                      end={{ title: "Sites", nodes: [], loadingRows: 2 }}
                      hub={{
                        mark: (
                          <WireMark tone="neutral" shape="square">
                            <ProductGlyph id="wireguard" />
                          </WireMark>
                        ),
                      }}
                    />
                  </>
                )}
              </>
            ) : tunnel ? (
              <LiveTunnel
                key={name}
                tunnel={tunnel}
                stale={
                  <NetworkReadWarning
                    error={vpn.error}
                    refresh={vpn.refresh}
                    lastSuccess={vpn.lastSuccess}
                  />
                }
                tab={tab}
                setTab={setTab}
                steps={steps}
                onChanged={changed}
              />
            ) : (
              <RemovedTunnel
                key={name}
                name={name}
                archived={archive.data?.find((a) => a.name === name)}
                reading={!archive.data && !archive.error}
                tab={tab}
                setTab={setTab}
                steps={steps}
                onChanged={changed}
              />
            )}
          </div>
        </div>
      </Page>
    </Workspace>
  )
}

/** The route's segment as a tunnel name. */
function decodeName(segment: string | undefined): string {
  if (!segment) return ""
  try {
    return decodeURIComponent(segment)
  } catch {
    return segment
  }
}

type View = { key: TunnelTab; label: string; count?: number }

/** The underlined strip of a tunnel's views. */
function ViewStrip({ name, views, shown }: { name: string; views: View[]; shown: string }) {
  return (
    <TabsPrimitive.List
      aria-label={`Views of ${name}`}
      className="flex shrink-0 [scrollbar-width:none] gap-1 overflow-x-auto border-b border-hairline [&::-webkit-scrollbar]:hidden"
    >
      {views.map((view) => (
        <TabsPrimitive.Trigger
          key={view.key}
          value={view.key}
          className={tabClasses(shown === view.key, "h-10")}
        >
          {view.label}
          {view.count !== undefined && view.count > 0 && <ChipCount>{view.count}</ChipCount>}
        </TabsPrimitive.Trigger>
      ))}
    </TabsPrimitive.List>
  )
}

const VIEW = "min-h-0 flex-1 pt-6 xl:overflow-y-auto"

function LiveTunnel({
  tunnel,
  stale,
  tab,
  setTab,
  steps,
  onChanged,
}: {
  tunnel: WGInterface
  stale: React.ReactNode
  tab: string
  setTab: (next: string, first: string) => void
  steps: React.ReactNode
  onChanged: () => void
}) {
  const control = useTunnelControl(tunnel, onChanged)
  const tunnels = useMemo(() => [tunnel], [tunnel])
  const { add: addPeer, open: openSheet, sheets } = usePeerSheets(tunnels, onChanged)
  const [span, setSpan] = useState<HistoryWindow>("24h")
  const history = useHistory(tunnel.name, undefined, span)
  const addTo = useCallback(
    (kind: "device" | "site") => addPeer?.(tunnel.name, kind),
    [addPeer, tunnel.name],
  )
  // Undefined for a session that may not add a peer: nothing then offers to.
  const add = addPeer ? addTo : undefined
  const openPeer = useCallback(
    (peer: WGInterface["peers"][number]) => openSheet(tunnel.name, peer),
    [openSheet, tunnel.name],
  )
  const views: View[] = [
    { key: "overview", label: "Overview" },
    { key: "traffic", label: "Traffic" },
    { key: "history", label: "History", count: history.data?.events?.length },
  ]
  const shown = views.some((view) => view.key === tab) ? tab : "overview"

  return (
    <>
      <div className="flex min-w-0 shrink-0 flex-col gap-5">
        <PageContext eyebrow={BACK} title={tunnel.name} actions={steps} />
        {stale}
        <TunnelIdentity
          tunnel={tunnel}
          control={control}
          onAdd={add}
          className="animate-rise border-b-0 pb-0"
        />
      </div>
      <Tabs
        value={shown}
        onValueChange={(next) => setTab(next, "overview")}
        className="min-h-0 flex-1 gap-0"
      >
        <ViewStrip name={tunnel.name} views={views} shown={shown} />
        <TabsContent value="overview" className={VIEW}>
          <div className="flex min-w-0 flex-col gap-8">
            <TunnelPicture tunnel={tunnel} onPeer={openPeer} onAdd={add} />
            <TunnelExit tunnel={tunnel} control={control} />
            <TunnelEndpoint tunnel={tunnel} />
          </div>
        </TabsContent>
        <TabsContent value="traffic" className={VIEW}>
          {shown === "traffic" && (
            <TunnelTraffic
              name={tunnel.name}
              tunnel={tunnel}
              history={history}
              span={span}
              onSpan={setSpan}
              onPeer={openPeer}
            />
          )}
        </TabsContent>
        <TabsContent value="history" className={VIEW}>
          {shown === "history" && <TunnelHistory name={tunnel.name} history={history} />}
        </TabsContent>
      </Tabs>
      {sheets}
      {control.dialog}
    </>
  )
}

/**
 * A tunnel that is not on this server now. With its file in the archive it
 * says what the file holds and offers to put it back; with only a record it
 * shows the record; with neither it is a name nobody here answers to.
 */
function RemovedTunnel({
  name,
  archived,
  reading,
  tab,
  setTab,
  steps,
  onChanged,
}: {
  name: string
  archived: WGArchived | undefined
  /** The archive has not answered yet. */
  reading: boolean
  tab: string
  setTab: (next: string, first: string) => void
  steps: React.ReactNode
  onChanged: () => void
}) {
  const [span, setSpan] = useState<HistoryWindow>("24h")
  const history = useHistory(name, undefined, span)
  const { restore, dialog, allowed } = useRestore(onChanged)
  const views: View[] = [
    { key: "history", label: "History", count: history.data?.events?.length },
    { key: "traffic", label: "Traffic" },
  ]
  const shown = views.some((view) => view.key === tab) ? tab : "history"
  const refused =
    history.error instanceof ApiError && [400, 404].includes(history.error.status) && !history.data
  const unknown =
    !archived && !reading && (refused || (history.data && !history.data.events?.length))

  if (!archived && (reading || (!history.data && !history.error)))
    return (
      <>
        <PageContext eyebrow={BACK} title={name} actions={steps} />
        <IdentitySkeleton mark="wireguard" />
      </>
    )
  if (unknown)
    return (
      <>
        <PageContext eyebrow={BACK} title={name} actions={steps} />
        <EmptyState
          icon={LockClosed}
          title={`No tunnel named ${name || "that"} on this server`}
          description="It was never set up here, or the name in the link is misspelt."
          action={
            <Link href="/network/vpn" className="text-body font-medium text-brand hover:underline">
              Back to the VPN
            </Link>
          }
        />
      </>
    )

  return (
    <>
      <div className="flex min-w-0 shrink-0 flex-col gap-5">
        <PageContext eyebrow={BACK} title={name} actions={steps} />
        <HostIdentity
          label={`About ${name}`}
          slot="tunnel-identity"
          mark="wireguard"
          className="animate-rise border-b-0 pb-0"
          title={<span className="truncate font-mono">{name}</span>}
          state={
            <Status
              tone="unknown"
              label={
                archived
                  ? `Removed ${relativeTime(new Date(archived.archivedAt * 1000).toISOString())}`
                  : "Removed"
              }
            />
          }
          facts={
            archived ? (
              <>
                <HostFact>
                  <span className="block truncate font-mono">{archivedLine(archived)}</span>
                </HostFact>
                {archived.restorable && archived.endpoint && (
                  <>
                    <FactDot />
                    <HostFact>
                      <span className="block truncate font-mono">{archived.endpoint}</span>
                    </HostFact>
                  </>
                )}
              </>
            ) : (
              <HostFact>its file is no longer in the archive</HostFact>
            )
          }
          command={
            archived?.restorable && allowed ? (
              <Button size="sm" onClick={() => restore(archived)} aria-label={`Restore ${name}`}>
                Restore
              </Button>
            ) : undefined
          }
        />
      </div>
      <Tabs
        value={shown}
        onValueChange={(next) => setTab(next, "history")}
        className="min-h-0 flex-1 gap-0"
      >
        <ViewStrip name={name} views={views} shown={shown} />
        <TabsContent value="history" className={VIEW}>
          {shown === "history" && <TunnelHistory name={name} history={history} />}
        </TabsContent>
        <TabsContent value="traffic" className={VIEW}>
          {shown === "traffic" && (
            <TunnelTraffic name={name} history={history} span={span} onSpan={setSpan} />
          )}
        </TabsContent>
      </Tabs>
      {dialog}
    </>
  )
}
