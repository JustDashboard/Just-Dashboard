"use client"

import { Suspense, useEffect, useMemo, useState } from "react"
import { useSearchParams } from "next/navigation"
import { Plus } from "@/components/icons"
import { get } from "@/lib/api"
import { plural } from "@/lib/format"
import type { NetworkLink } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Page, PageContext, SearchInput, Toolbar } from "@/components/page"
import { ErrorState, LoadingPanel } from "@/components/state"
import { FilterChip, ChipCount } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { DeviceList, GROUPS } from "@/components/network/interfaces/device-list"
import { DeviceSheet } from "@/components/network/interfaces/device-sheet"
import { CreateDevice } from "@/components/network/interfaces/create-device"
import { useLiveTraffic } from "@/components/network/use-live-traffic"

/**
 * Every device on the machine, and the ones this server can make.
 *
 * The page answers in the order a device list is read: the uplink first,
 * then the cards, the tunnels, the bridges and VLANs — each a lit card that
 * opens its sheet, with its in and out live — and, one press away, Docker's
 * veths and the kernel's own fallback devices, which are most of a busy
 * host's list and none of what anybody comes here for. The command to make
 * a device sits with the list it adds to.
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
  const params = useSearchParams()
  const links = usePoll<NetworkLink[]>((signal) => get("/network/links", undefined, signal), 10_000)
  const live = useLiveTraffic()
  const [everything, setEverything] = useState(false)
  const [query, setQuery] = useState("")
  const [open, setOpen] = useState<string | undefined>(() => params.get("device") ?? undefined)
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

  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Network" title="Interfaces" />
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
          <Button size="sm" onClick={() => setCreating(true)}>
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

      <DeviceSheet
        link={chosen}
        links={withRates}
        points={chosen ? live.series[chosen.name] : undefined}
        open={!!chosen}
        onOpenChange={(next) => !next && setOpen(undefined)}
        onChanged={links.refresh}
      />
      <CreateDevice
        open={creating}
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
