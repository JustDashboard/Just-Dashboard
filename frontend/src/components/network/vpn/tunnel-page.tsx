"use client"

import { useCallback, useMemo, useState } from "react"
import Link from "next/link"
import { useParams, useRouter, useSearchParams } from "next/navigation"
import { Fact } from "@/components/fact-line"
import { LockClosed } from "@/components/icons"
import { ApiError, get } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import type { VPNView, WGArchived, WGInterface } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { DetailPage, useSiblingSteps, type Step } from "@/components/detail-page"
import { IdentityHeader } from "@/components/identity-header"
import { Page, PageContext } from "@/components/page"
import { EmptyState, ErrorState, NeedsAdmin } from "@/components/state"
import { Status } from "@/components/status-dot"
import { ViewTabList, type TabView } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Tabs, TabsContent } from "@/components/ui/tabs"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { archivedLine, useArchive, useRestore } from "./archive"
import { usePeerSheets } from "./peers"
import { useHistory } from "./record"
import type { HistoryWindow } from "./record-logic"
import { TunnelExit } from "./tunnel-exit"
import { IdentitySkeleton, TunnelIdentity, useTunnelControl } from "./tunnel-identity"
import { TunnelPicture, TunnelPictureWaiting } from "./tunnel-picture"
import { TunnelRail } from "./tunnel-rail"
import { railTunnels, tunnelHref, type TunnelTab } from "./tunnel-reading"
import { TunnelEndpoint, TunnelHistory, TunnelTraffic } from "./tunnel-views"
import { decodeSegment } from "@/lib/route-params"

const BACK = { href: "/network/vpn", label: "VPN" }

/** What every state of the page hands the frame: the rail, the arrows and the way back. */
type Frame = Omit<React.ComponentProps<typeof DetailPage>, "identity" | "children">

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
  const name = decodeSegment(params.tunnel)
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
  const open = useCallback(
    (next: string) => router.push(tunnelHref(next, tab || "overview")),
    [router, tab],
  )
  const steps: { previous: Step; next: Step } = useSiblingSteps(
    rail.map((t) => t.name),
    name,
    open,
  )
  const refreshVPN = vpn.refresh
  const refreshArchive = archive.refresh
  const changed = useCallback(() => {
    refreshVPN()
    refreshArchive()
  }, [refreshVPN, refreshArchive])

  if (!admin) {
    return (
      <Page>
        <PageContext back={BACK} title={name} />
        <NeedsAdmin
          what="VPN"
          why="Tunnel peers and their client configurations are visible to administrators."
        />
      </Page>
    )
  }

  const tunnel = live?.find((t) => t.name === name)
  const frame: Frame = {
    name: "Tunnel",
    what: "tunnel",
    stateKey: "network.vpn.tunnel",
    refresh: changed,
    steps,
    rail: (
      <TunnelRail
        tunnels={rail}
        current={name}
        loading={!vpn.data && !vpn.error}
        error={!vpn.data ? vpn.error : undefined}
        onRetry={vpn.refresh}
      />
    ),
    back: BACK,
    title: name,
  }

  if (!vpn.data)
    return (
      <DetailPage {...frame} identity={!vpn.error && <IdentitySkeleton mark="wireguard" />}>
        {vpn.error ? (
          <ErrorState error={vpn.error} onRetry={vpn.refresh} retryable />
        ) : (
          <TunnelPictureWaiting whole />
        )}
      </DetailPage>
    )
  return tunnel ? (
    <LiveTunnel
      key={name}
      frame={frame}
      tunnel={tunnel}
      stale={
        <NetworkReadWarning error={vpn.error} refresh={vpn.refresh} lastSuccess={vpn.lastSuccess} />
      }
      tab={tab}
      setTab={setTab}
      onChanged={changed}
    />
  ) : (
    <RemovedTunnel
      key={name}
      frame={frame}
      name={name}
      archived={archive.data?.find((a) => a.name === name)}
      reading={!archive.data && !archive.error}
      tab={tab}
      setTab={setTab}
      onChanged={changed}
    />
  )
}

type View = TabView<TunnelTab>

/** The strip of a tunnel's views, directly under its line. */
function ViewStrip({ name, views, shown }: { name: string; views: View[]; shown: string }) {
  return (
    <ViewTabList
      label={`Views of ${name}`}
      views={views}
      value={shown}
      sticky
      className="max-xl:static"
    />
  )
}

// One inset under the strip on every Network detail page.
const VIEW = "pt-6"

function LiveTunnel({
  frame,
  tunnel,
  stale,
  tab,
  setTab,
  onChanged,
}: {
  frame: Frame
  tunnel: WGInterface
  stale: React.ReactNode
  tab: string
  setTab: (next: string, first: string) => void
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
    <DetailPage
      {...frame}
      title={tunnel.name}
      identity={
        <>
          {stale}
          <TunnelIdentity
            tunnel={tunnel}
            control={control}
            onAdd={add}
            className="animate-rise border-b-0 pb-0"
          />
        </>
      }
    >
      <Tabs value={shown} onValueChange={(next) => setTab(next, "overview")} className="gap-0">
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
    </DetailPage>
  )
}

/**
 * A tunnel that is not on this server now. With its file in the archive it
 * says what the file holds and offers to put it back; with only a record it
 * shows the record; with neither it is a name nobody here answers to.
 */
function RemovedTunnel({
  frame,
  name,
  archived,
  reading,
  tab,
  setTab,
  onChanged,
}: {
  frame: Frame
  name: string
  archived: WGArchived | undefined
  /** The archive has not answered yet. */
  reading: boolean
  tab: string
  setTab: (next: string, first: string) => void
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
      <DetailPage {...frame} identity={<IdentitySkeleton mark="wireguard" />}>
        {null}
      </DetailPage>
    )
  if (unknown)
    return (
      <DetailPage {...frame}>
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
      </DetailPage>
    )

  return (
    <DetailPage
      {...frame}
      identity={
        <IdentityHeader
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
                <Fact>
                  <span className="block truncate font-mono">{archivedLine(archived)}</span>
                </Fact>
                {archived.restorable && archived.endpoint && (
                  <Fact>
                    <span className="block truncate font-mono">{archived.endpoint}</span>
                  </Fact>
                )}
              </>
            ) : (
              <Fact>its file is no longer in the archive</Fact>
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
      }
    >
      <Tabs value={shown} onValueChange={(next) => setTab(next, "history")} className="gap-0">
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
    </DetailPage>
  )
}
