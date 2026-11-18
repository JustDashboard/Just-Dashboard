"use client"

import { useCallback, useState } from "react"
import Link from "next/link"
import { useParams, useRouter } from "next/navigation"
import { useAuth } from "@/hooks/use-auth"
import { NetworkDevice } from "@/components/icons"
import type { NetworkLink } from "@/lib/types"
import { DetailPage, useDetailTab, useSiblingSteps } from "@/components/detail-page"
import { EmptyState, ErrorState } from "@/components/state"
import { LoadingRows } from "@/components/loading"
import { ViewTabList } from "@/components/tabs"
import { Tabs, TabsContent } from "@/components/ui/tabs"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { DeviceAddresses } from "./device-addresses"
import {
  DEVICE_TABS,
  behindEverything,
  deviceHref,
  deviceViews,
  groupDevices,
  type DeviceTab,
} from "./device-facts"
import { DeviceHistory, useDeviceHistory } from "./device-history"
import { DeviceIdentity, DeviceIdentityLoading } from "./device-identity"
import { DevicePorts } from "./device-ports"
import { DeviceRail } from "./device-rail"
import { DeviceSettings } from "./device-settings"
import { DeviceTraffic } from "./device-traffic"
import { NamespaceSheet, type NamespaceTarget } from "./namespace-sheet"
import { useDeviceFilter, useDevices } from "./use-devices"
import { decodeSegment } from "@/lib/route-params"

// One inset under the strip on every Network detail page.
const VIEW_PANE = "pt-6"

const LABEL: Record<DeviceTab, string> = {
  traffic: "Traffic",
  addresses: "Addresses",
  ports: "Ports",
  settings: "Settings",
  history: "History",
}

/**
 * One network device, as a place of its own.
 *
 * It was a `SidePanel` over the Interfaces list until 0.8.7: a chart, eight
 * blocks of facts and five forms in a sheet's width, over a list nobody could
 * read behind it, and with no address to send anybody. So a device is a
 * destination with a way back, on the shape a process has
 * (`procs/process-page`): the line it opens on, its views as the underlined
 * strip under that line with the view in the address, and from `xl` the list
 * folded into a column beside it (`DeviceRail`) with the list's own search,
 * so checking the next device is one press, Alt+↑ and Alt+↓ walk it and the
 * view on screen goes with you. On a phone the page is the one pane.
 */
export function DevicePage() {
  const params = useParams<{ name: string }>()
  const router = useRouter()
  const { can } = useAuth()
  const admin = can("system.admin")
  const name = decodeSegment(params.name)
  const [tab, setTab] = useDetailTab(DEVICE_TABS)
  const open = useCallback((next: string) => router.push(deviceHref(next, tab)), [router, tab])

  const { links, live, devices } = useDevices()
  const filter = useDeviceFilter()
  const all = devices ?? []
  const groups = groupDevices(all, filter)
  const order = groups.flatMap((group) => group.members)
  const link = all.find((l) => l.name === name)
  const at = order.findIndex((l) => l.name === name)
  const steps = useSiblingSteps(
    order.map((l) => l.name),
    name,
    open,
  )
  const [inspecting, setInspecting] = useState<NamespaceTarget>()

  return (
    <DetailPage
      name="Device"
      what="device"
      stateKey="network.device.page"
      refresh={links.refresh}
      steps={steps}
      rail={
        <DeviceRail
          groups={groups}
          current={name}
          pinned={link && at < 0 ? link : undefined}
          loading={!devices && !links.error}
          error={!devices ? links.error : undefined}
          onRetry={links.refresh}
          query={filter.query}
          onQuery={filter.setQuery}
          everything={filter.everything}
          onEverything={filter.setEverything}
          hidden={all.filter(behindEverything).length}
        />
      }
      back={{ href: "/network/interfaces", label: "Interfaces" }}
      title={name || "Device"}
      identity={
        link ? (
          <DeviceIdentity
            link={link}
            onChanged={links.refresh}
            onDeleted={() => {
              links.refresh()
              router.push("/network/interfaces")
            }}
            onOpenNamespace={(kind, target) => setInspecting({ kind, name: target })}
          />
        ) : (
          !devices && !links.error && <DeviceIdentityLoading name={name} />
        )
      }
    >
      {devices && (
        <NetworkReadWarning
          error={links.error}
          refresh={links.refresh}
          lastSuccess={links.lastSuccess}
        />
      )}
      {!devices ? (
        links.error ? (
          <ErrorState error={links.error} onRetry={links.refresh} />
        ) : (
          <LoadingRows />
        )
      ) : !link ? (
        <EmptyState
          icon={NetworkDevice}
          title={`No device named ${name}`}
          description="A device goes when its container stops, its tunnel is taken down or it is deleted. Pick another from the list."
          action={
            <Link
              href="/network/interfaces"
              className="rounded-sm text-body font-medium text-brand focus-ring hover:underline"
            >
              Back to the interfaces
            </Link>
          }
        />
      ) : (
        <DeviceViews
          // A device's drafts and readings are its own.
          key={link.name}
          link={link}
          links={all}
          points={live.series[link.name]}
          admin={admin}
          tab={tab}
          setTab={setTab}
          onChanged={links.refresh}
          onOpenNamespace={(kind, target) => setInspecting({ kind, name: target })}
        />
      )}
      <NamespaceSheet
        target={inspecting}
        links={all}
        onOpenChange={(still) => !still && setInspecting(undefined)}
        onOpenDevice={(device) => {
          setInspecting(undefined)
          if (device !== name) router.push(deviceHref(device))
        }}
      />
    </DetailPage>
  )
}

/** The strip of a device's views and the one on screen. */
function DeviceViews({
  link,
  links,
  points,
  admin,
  tab,
  setTab,
  onChanged,
  onOpenNamespace,
}: {
  link: NetworkLink
  links: NetworkLink[]
  points: React.ComponentProps<typeof DeviceTraffic>["points"]
  admin: boolean
  tab: DeviceTab
  setTab: (tab: string) => void
  onChanged: () => void
  onOpenNamespace: (kind: "named" | "container", name: string) => void
}) {
  const views = deviceViews(link, admin)
  const shown = views.includes(tab) ? tab : "traffic"
  const history = useDeviceHistory(link.name, admin)
  const counts: Partial<Record<DeviceTab, number>> = {
    addresses: link.addresses.length,
    ports: links.filter((l) => l.master === link.name).length,
    history: history.changes?.length,
  }
  const pane = VIEW_PANE
  return (
    <Tabs value={shown} onValueChange={setTab} className="gap-0">
      <ViewTabList
        label={`Views of ${link.name}`}
        views={views.map((view) => ({ key: view, label: LABEL[view], count: counts[view] }))}
        value={shown}
        sticky
        className="max-xl:static"
      />

      <TabsContent value="traffic" className={pane}>
        {shown === "traffic" && (
          <DeviceTraffic
            link={link}
            links={links}
            points={points}
            onOpenNamespace={onOpenNamespace}
            onTab={setTab}
          />
        )}
      </TabsContent>
      <TabsContent value="addresses" className={pane}>
        {shown === "addresses" && <DeviceAddresses link={link} onChanged={onChanged} />}
      </TabsContent>
      <TabsContent value="ports" className={pane}>
        {shown === "ports" && <DevicePorts link={link} links={links} />}
      </TabsContent>
      <TabsContent value="settings" className={pane}>
        {shown === "settings" && <DeviceSettings link={link} links={links} onChanged={onChanged} />}
      </TabsContent>
      <TabsContent value="history" className={pane}>
        {shown === "history" && <DeviceHistory name={link.name} history={history} />}
      </TabsContent>
    </Tabs>
  )
}
