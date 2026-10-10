"use client"

import { NetworkReadWarning } from "@/components/network/read-warning"
import { useAuth } from "@/hooks/use-auth"
import { Suspense, useEffect, useMemo, useState } from "react"
import { useSearchParams } from "next/navigation"
import { Plus } from "@/components/icons"
import { get } from "@/lib/api"
import { plural } from "@/lib/format"
import type { NetworkLink, NetworkNamespace } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Page, PageContext, SearchInput, Toolbar } from "@/components/page"
import { ErrorState, LoadingPanel } from "@/components/state"
import { FilterChip, ChipCount } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { DeviceList, GROUPS } from "@/components/network/interfaces/device-list"
import { DeviceSheet } from "@/components/network/interfaces/device-sheet"
import { CreateDevice } from "@/components/network/interfaces/create-device"
import { Namespaces } from "@/components/network/interfaces/namespaces"
import {
  NamespaceSheet,
  type NamespaceTarget,
} from "@/components/network/interfaces/namespace-sheet"
import { Notice } from "@/components/state"
import { useLiveTraffic } from "@/components/network/use-live-traffic"

/**
 * Every device on the machine, and the ones this server can make.
 *
 * The page answers in the order a device list is read: the uplink first,
 * then the cards, the tunnels, the bridges and VLANs — each a lit card that
 * opens its sheet, with its in and out live — and, one press away, Docker's
 * veths and the kernel's own fallback devices, which are most of a busy
 * host's list and none of what anybody comes here for. The command to make
 * a device sits with the list it adds to. Under the devices, the other
 * network stacks on the machine: the named namespaces, and each container's.
 *
 * Its old figures (devices, up, the default route, public addresses) each
 * went where they are said better: the counts are the filter chips', the
 * default route and the public addresses are the Overview's identity line.
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
  const links = usePoll<NetworkLink[]>((signal) => get("/network/links", undefined, signal), 10_000)
  const live = useLiveTraffic()
  const namespaces = usePoll<NetworkNamespace[]>(
    (signal) => get("/network/namespaces", undefined, signal),
    30_000,
  )
  const [everything, setEverything] = useState(false)
  const [query, setQuery] = useState("")
  const [open, setOpen] = useState<string | undefined>(() => params.get("device") ?? undefined)
  const [inspecting, setInspecting] = useState<NamespaceTarget>()
  const [creating, setCreating] = useState(() => params.get("create") !== null)
  useEffect(() => {
    if (params.get("device") || params.get("create") !== null) {
      window.history.replaceState(null, "", window.location.pathname)
    }
  }, [params])

  const withRates = useMemo(
    () =>
      (links.data ?? []).map((link) => {
        const last = live.series[link.name]?.at(-1)
        return last ? { ...link, rxRate: last.rx, txRate: last.tx } : link
      }),
    [links.data, live.series],
  )
  const hidden = withRates.filter((l) => GROUPS.some((g) => g.everythingOnly && g.match(l))).length
  const chosen = withRates.find((l) => l.name === open)
  const unjoined = withRates.filter((l) => l.dockerJoin)
  const unjoinedReasons = [...new Set(unjoined.map((l) => l.dockerJoinReason ?? ""))].filter(
    Boolean,
  )

  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Network" title="Interfaces" />
      {links.data && (
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
      <Toolbar>
        <FilterChip selected={!everything} onClick={() => setEverything(false)}>
          Real devices
          <ChipCount>{withRates.length - hidden}</ChipCount>
        </FilterChip>
        <FilterChip selected={everything} onClick={() => setEverything(true)}>
          Everything
          <ChipCount>{withRates.length}</ChipCount>
        </FilterChip>
        <SearchInput
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          placeholder="Name, address or container"
          aria-label="Search devices"
        />
        <span className="ml-auto flex items-center gap-3">
          <span className="hidden text-hint text-muted-foreground sm:inline">
            {plural(withRates.filter((l) => l.managed).length, "device")} made here
          </span>
          <Button size="sm" onClick={() => setCreating(true)} disabled={!admin}>
            <Plus aria-hidden />
            New device
          </Button>
        </span>
      </Toolbar>

      {!links.data ? (
        links.error ? (
          <ErrorState error={links.error} onRetry={links.refresh} />
        ) : (
          <LoadingPanel />
        )
      ) : (
        <DeviceList
          links={withRates}
          series={live.series}
          everything={everything}
          query={query}
          onOpen={setOpen}
        />
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
        links={withRates}
        onChanged={() => {
          namespaces.refresh()
          links.refresh()
        }}
        onOpen={(ns) => setInspecting({ kind: ns.kind, name: ns.name })}
      />
      <NamespaceSheet
        target={inspecting}
        links={withRates}
        onOpenChange={(next) => !next && setInspecting(undefined)}
        onOpenDevice={(name) => {
          setInspecting(undefined)
          setOpen(name)
        }}
      />

      <DeviceSheet
        key={chosen?.name ?? "closed"}
        link={chosen}
        links={withRates}
        points={chosen ? live.series[chosen.name] : undefined}
        open={!!chosen}
        onOpenChange={(next) => !next && setOpen(undefined)}
        onChanged={links.refresh}
        onOpenNamespace={(kind, name) => {
          setOpen(undefined)
          setInspecting({ kind, name })
        }}
      />
      <CreateDevice
        open={creating && admin}
        onOpenChange={setCreating}
        links={withRates}
        onCreated={(name) => {
          links.refresh()
          setOpen(name)
        }}
      />
    </Page>
  )
}
