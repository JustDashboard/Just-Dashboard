"use client"

import { Suspense, useEffect, useState } from "react"
import { useRouter, useSearchParams } from "next/navigation"
import { useAuth } from "@/hooks/use-auth"
import { NetworkDevice, Plus } from "@/components/icons"
import { get } from "@/lib/api"
import { plural } from "@/lib/format"
import type { NetworkNamespace } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Page, PageContext, SearchInput, Toolbar } from "@/components/page"
import { ErrorState, Hold, LoadingPanel, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { FilterChip, ChipCount } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { CreateDevice } from "@/components/network/interfaces/create-device"
import { deviceHref, listFacts } from "@/components/network/interfaces/device-facts"
import { DeviceList } from "@/components/network/interfaces/device-list"
import { InterfacesLog } from "@/components/network/interfaces/interfaces-log"
import { Namespaces } from "@/components/network/interfaces/namespaces"
import {
  NamespaceSheet,
  type NamespaceTarget,
} from "@/components/network/interfaces/namespace-sheet"
import { useDeviceFilter, useDevices } from "@/components/network/interfaces/use-devices"

/**
 * Every device on the machine, and the ones this server can make.
 *
 * The page opens on one line saying what is here: how many devices, which
 * carries the default route, how many the dashboard made, with the command
 * that makes another. Then the list in the order a device list is read: the
 * uplink first, then the cards, the tunnels, the bridges and VLANs, each a
 * lit card that opens the device's own page, with its in and out live; and,
 * one press away, Docker's veths and the kernel's own fallback devices, which
 * are most of a busy host's list and none of what anybody comes here for.
 * Under the devices, the other network stacks on the machine: the named
 * namespaces, and each container's; and last what the kernel and the network
 * daemon logged about the links.
 */
export default function NetworkInterfacesPage() {
  return (
    // The device a link points at lives in the query string, which the App
    // Router only hands out inside a Suspense boundary.
    <Suspense fallback={<LoadingPanel />}>
      <Interfaces />
    </Suspense>
  )
}

function Interfaces() {
  const { can } = useAuth()
  const admin = can("system.admin")
  const params = useSearchParams()
  const router = useRouter()
  const { links, live, devices } = useDevices()
  const namespaces = usePoll<NetworkNamespace[]>(
    (signal) => get("/network/namespaces", undefined, signal),
    30_000,
  )
  const { everything, setEverything, query, setQuery } = useDeviceFilter()
  const [inspecting, setInspecting] = useState<NamespaceTarget>()
  const [creating, setCreating] = useState(() => params.get("create") !== null)
  const [created, setCreated] = useState<string>()

  // The address that opened a device's sheet: the Overview's picture, the
  // Drift page and old bookmarks still send it. It lands on the device's page.
  const device = params.get("device")
  useEffect(() => {
    if (device) router.replace(deviceHref(device))
    else if (params.get("create") !== null)
      window.history.replaceState(null, "", window.location.pathname)
  }, [device, params, router])
  // A device just made is opened once the kernel lists it.
  useEffect(() => {
    if (created && devices?.some((l) => l.name === created)) router.push(deviceHref(created))
  }, [created, devices, router])

  if (device) return <LoadingPanel />

  const all = devices ?? []
  const facts = listFacts(all)
  const unjoined = all.filter((l) => l.dockerJoin)
  const unjoinedReasons = [...new Set(unjoined.map((l) => l.dockerJoinReason ?? ""))].filter(
    Boolean,
  )

  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Network" title="Interfaces" />
      <HostIdentity
        label="About the interfaces"
        fallback={NetworkDevice}
        title="Interfaces"
        state={
          !devices ? (
            links.error ? (
              <Status tone="unknown" label="Not read" />
            ) : (
              <TextShimmer className="text-xs font-medium">Reading…</TextShimmer>
            )
          ) : facts.noCarrier > 0 ? (
            <Status tone="warning" label={`${facts.noCarrier} without a carrier`} />
          ) : (
            <Status
              tone="running"
              label={facts.up === facts.devices ? "All up" : `${facts.up} of ${facts.devices} up`}
            />
          )
        }
        facts={
          // A box of the line's own height: the uplink's name is mono, and a
          // mono word in the line made it a pixel taller than while it waited.
          <span className="flex h-4 min-w-0 items-center gap-x-3 whitespace-nowrap">
            {devices ? (
              <>
                <span className="numeric">{plural(facts.devices, "device")}</span>
                {facts.uplinks.length > 0 && (
                  <>
                    <FactDot />
                    <span className="truncate">
                      uplink <span className="font-mono">{facts.uplinks.join(", ")}</span>
                    </span>
                  </>
                )}
                <FactDot />
                <span className="numeric shrink-0">{facts.made} made here</span>
              </>
            ) : (
              <>&nbsp;</>
            )}
          </span>
        }
        command={
          admin ? (
            <Button size="sm" onClick={() => setCreating(true)}>
              <Plus aria-hidden />
              New device
            </Button>
          ) : undefined
        }
      />
      {devices && (
        <NetworkReadWarning
          error={links.error}
          refresh={links.refresh}
          lastSuccess={links.lastSuccess}
        />
      )}
      {live.error && live.now > 0 && (
        <NetworkReadWarning
          error={live.error}
          refresh={live.refresh}
          lastSuccess={live.lastSuccess}
          reading="live throughput"
        />
      )}
      {unjoined.length > 0 && (
        <Notice
          tone="warning"
          title={`${plural(unjoined.length, "Docker device")} not joined to a container or network`}
        >
          <ul className="flex flex-col gap-1">
            {unjoinedReasons.map((reason) => (
              <li key={reason}>{reason}</li>
            ))}
          </ul>
          <p className="mt-1">
            Their rows say so under Containers in Everything; nothing about them is guessed.
          </p>
        </Notice>
      )}

      {/* The chips and the search stand while the list is read, so the list
          lands under them; a list that could not be read has nothing to filter. */}
      {(devices || !links.error) && (
        <Toolbar>
          <FilterChip selected={!everything} onClick={() => setEverything(false)}>
            Real devices
            <ChipCount>{facts.devices}</ChipCount>
          </FilterChip>
          <FilterChip selected={everything} onClick={() => setEverything(true)}>
            Everything
            <ChipCount>{facts.everything}</ChipCount>
          </FilterChip>
          <SearchInput
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Name, address or container"
            aria-label="Search devices"
          />
        </Toolbar>
      )}
      {!devices ? (
        links.error ? (
          <ErrorState error={links.error} onRetry={links.refresh} />
        ) : (
          <LoadingPanel hold />
        )
      ) : (
        <DeviceList links={all} series={live.series} everything={everything} query={query} />
      )}

      {namespaces.data ? (
        <NetworkReadWarning
          error={namespaces.error}
          refresh={namespaces.refresh}
          lastSuccess={namespaces.lastSuccess}
          reading="namespaces"
        />
      ) : (
        namespaces.error && <ErrorState error={namespaces.error} onRetry={namespaces.refresh} />
      )}
      <Namespaces
        namespaces={namespaces.data}
        links={all}
        onChanged={() => {
          namespaces.refresh()
          links.refresh()
        }}
        onOpen={(ns) => setInspecting({ kind: ns.kind, name: ns.name, readError: ns.readError })}
      />
      {/* A row for each namespace: the log under them waits to know how many. */}
      <Hold until={!namespaces.loading} />
      <InterfacesLog />
      <NamespaceSheet
        target={inspecting}
        links={all}
        onOpenChange={(next) => !next && setInspecting(undefined)}
        onOpenDevice={(name) => {
          setInspecting(undefined)
          router.push(deviceHref(name))
        }}
      />
      <CreateDevice
        open={creating && admin}
        onOpenChange={setCreating}
        links={all}
        onCreated={(name) => {
          links.refresh()
          setCreated(name)
        }}
      />
    </Page>
  )
}
