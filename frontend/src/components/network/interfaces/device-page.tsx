"use client"

import { useCallback, useState } from "react"
import Link from "next/link"
import { useParams, useRouter, useSearchParams } from "next/navigation"
import { Tabs as TabsPrimitive } from "radix-ui"
import { useAuth } from "@/hooks/use-auth"
import { ArrowLeft, ChevronDown, ChevronUp, NetworkDevice } from "@/components/icons"
import type { NetworkLink } from "@/lib/types"
import { IconAction } from "@/components/icon-action"
import { Page, PageContext } from "@/components/page"
import { EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { ChipCount, tabClasses } from "@/components/tabs"
import { Tabs, TabsContent } from "@/components/ui/tabs"
import { Workspace, WorkspaceHelp } from "@/components/workspace/workspace"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { DeviceAddresses } from "./device-addresses"
import {
  DEVICE_TABS,
  behindEverything,
  deviceHref,
  deviceName,
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

const LABEL: Record<DeviceTab, string> = {
  traffic: "Traffic",
  addresses: "Addresses",
  ports: "Ports",
  settings: "Settings",
  history: "History",
}

const BACK = (
  <Link
    href="/network/interfaces"
    className="inline-flex items-center gap-1 rounded-sm focus-ring hover:underline"
  >
    <ArrowLeft className="size-3" /> Interfaces
  </Link>
)

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
  const search = useSearchParams()
  const router = useRouter()
  const { can } = useAuth()
  const admin = can("system.admin")
  const name = deviceName(params.name)
  const requested = search.get("tab") ?? "traffic"
  const tab: DeviceTab = (DEVICE_TABS as readonly string[]).includes(requested)
    ? (requested as DeviceTab)
    : "traffic"
  const setTab = useCallback((next: string) => {
    // The address says which view, so a link to a device's addresses opens
    // on them; replaced rather than pushed, because a view is not a place
    // Back should step through.
    const url = new URL(window.location.href)
    if (next === "traffic") url.searchParams.delete("tab")
    else url.searchParams.set("tab", next)
    window.history.replaceState(null, "", `${url.pathname}${url.search}`)
  }, [])
  const open = useCallback((next: string) => router.push(deviceHref(next, tab)), [router, tab])

  const { links, live, devices } = useDevices()
  const filter = useDeviceFilter()
  const all = devices ?? []
  const groups = groupDevices(all, filter)
  const order = groups.flatMap((group) => group.members)
  const link = all.find((l) => l.name === name)
  const at = order.findIndex((l) => l.name === name)
  const adjacent = (direction: number) => {
    const next = at >= 0 ? order[at + direction] : order[direction > 0 ? 0 : order.length - 1]
    if (next) open(next.name)
  }
  const previous = { disabled: at <= 0, run: () => adjacent(-1) }
  const next = {
    disabled: order.length === 0 || at >= order.length - 1,
    run: () => adjacent(1),
  }
  const [inspecting, setInspecting] = useState<NamespaceTarget>()

  return (
    <Workspace
      name="Device"
      stateKey="network.device.page"
      refresh={links.refresh}
      commands={[
        {
          id: "previous",
          label: "Previous device",
          keys: "Alt+↑",
          chord: "Alt+ArrowUp",
          disabled: previous.disabled,
          run: previous.run,
        },
        {
          id: "next",
          label: "Next device",
          keys: "Alt+↓",
          chord: "Alt+ArrowDown",
          disabled: next.disabled,
          run: next.run,
        },
      ]}
    >
      {/* Held to the window where the rail stands beside it, so the list and
          the identity line stay put while a view scrolls; narrower, the page
          is the one pane and scrolls as a whole. */}
      <Page fill="xl" className="gap-0 md:gap-0">
        <div className="flex min-w-0 flex-1 gap-6 xl:min-h-0">
          <DeviceRail
            className="hidden w-72 shrink-0 xl:flex"
            groups={groups}
            current={name}
            pinned={link && at < 0 ? link : undefined}
            loading={!devices && !links.error}
            query={filter.query}
            onQuery={filter.setQuery}
            everything={filter.everything}
            onEverything={filter.setEverything}
            hidden={all.filter(behindEverything).length}
            onOpen={open}
          />
          <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-5">
            <div className="flex min-w-0 shrink-0 flex-col gap-5">
              <PageContext
                eyebrow={BACK}
                title={name || "Device"}
                actions={
                  <span className="flex items-center">
                    <IconAction
                      label="Previous device"
                      disabled={previous.disabled}
                      onClick={previous.run}
                    >
                      <ChevronUp />
                    </IconAction>
                    <IconAction label="Next device" disabled={next.disabled} onClick={next.run}>
                      <ChevronDown />
                    </IconAction>
                    <span className="max-sm:hidden">
                      <WorkspaceHelp compact />
                    </span>
                  </span>
                }
              />
              {link ? (
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
              )}
            </div>

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
          </div>
        </div>
      </Page>
      <NamespaceSheet
        target={inspecting}
        links={all}
        onOpenChange={(still) => !still && setInspecting(undefined)}
        onOpenDevice={(device) => {
          setInspecting(undefined)
          if (device !== name) router.push(deviceHref(device))
        }}
      />
    </Workspace>
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
  const pane = "min-h-0 flex-1 pt-6 xl:overflow-y-auto"
  return (
    <Tabs value={shown} onValueChange={setTab} className="min-h-0 flex-1 gap-0">
      <TabsPrimitive.List
        aria-label={`Views of ${link.name}`}
        className="flex shrink-0 [scrollbar-width:none] gap-1 overflow-x-auto border-b border-hairline [&::-webkit-scrollbar]:hidden"
      >
        {views.map((view) => (
          <TabsPrimitive.Trigger
            key={view}
            value={view}
            className={tabClasses(shown === view, "h-10")}
          >
            {LABEL[view]}
            {(counts[view] ?? 0) > 0 && <ChipCount>{counts[view]}</ChipCount>}
          </TabsPrimitive.Trigger>
        ))}
      </TabsPrimitive.List>

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
