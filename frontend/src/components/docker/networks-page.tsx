"use client"

import { useCallback, useMemo, useRef, useState } from "react"
import { useRouter } from "next/navigation"
import { Box, Cross, NetworkDevice, Plus, Trash } from "@/components/icons"
import { useAuth } from "@/hooks/use-auth"
import { useMetrics } from "@/hooks/use-metrics"
import { usePoll } from "@/hooks/use-poll"
import { useQuerySelection } from "@/hooks/use-query-selection"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { useConfirm } from "@/components/confirm-dialog"
import { useNow } from "@/components/deploy/vocabulary"
import { FactDot, HostFact, HostIdentity } from "@/components/metrics/host-identity"
import { Page, PageContext, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelFooter, PanelHeader, PanelToolbar } from "@/components/panel"
import { platformProduct } from "@/components/product-logo"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { EmptyState, ErrorState, LoadingPanel } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Workspace, WorkspaceHelp } from "@/components/workspace/workspace"
import { useLiveTraffic } from "@/components/network/use-live-traffic"
import { get, post } from "@/lib/api"
import { containerRates } from "@/lib/container-usage"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import type {
  Container,
  ContainerStats,
  DockerEvent,
  DockerNetwork,
  DockerNetworkingInfo,
  NetworkLivePoint,
} from "@/lib/types"
import { cn } from "@/lib/utils"
import { useSessionState } from "@/lib/view-state"
import { HueKey, NetworkBand, type NetworkRate } from "@/components/docker/network-band"
import { NetworkRows } from "@/components/docker/network-table"
import {
  ContainerNetworkRows,
  type ContainerTraffic,
  type Placement,
} from "@/components/docker/network-members"
import { NetworkSheet } from "@/components/docker/network-sheet"
import { AttachDialog, NewNetworkDialog } from "@/components/docker/network-dialogs"
import {
  OWNER_KINDS,
  addressPlan,
  endpointsByContainer,
  isSystem,
  isUnused,
  networkChanges,
  networkHue,
  networkOrder,
  networkOwner,
  type OwnerKind,
} from "@/components/docker/networks"

/** How often the list is read; each read is one network list and one container list. */
const POLL = 10_000

/** A container's last minute of rates, at the socket's two-second frames. */
const KEEP = 30

type Shelf = OwnerKind | "unused" | ""
type Lens = "several" | "default" | "stopped" | ""

const LENSES: { value: Exclude<Lens, "">; label: string; title: string; tone?: "warning" }[] = [
  {
    value: "several",
    label: "On several networks",
    title: "The containers that join one network to another",
  },
  {
    value: "default",
    label: "Default bridge",
    title: "Reached only by address: the default bridge has no names",
    tone: "warning",
  },
  { value: "stopped", label: "Stopped", title: "Attached, and holding no address until started" },
]

/**
 * Docker's networks, who is on each, and what they are carrying.
 *
 * The engine first, as the identity line the Docker overview and Services
 * open on — Docker drawn as itself, its version, the host, how many networks
 * and how many containers on them — with the verdict at its right end: the
 * address pool nearly or wholly spent, which is what makes the next
 * `compose up` fail; the networks nothing is attached to, which hold a block
 * of that pool each and narrow the table to them when pressed; or that every
 * network is in use. Create network and Remove unused are the list's own
 * commands, in its toolbar.
 *
 * Then `NetworkBand`: which networks are carrying the traffic, read off their
 * bridges every two seconds; the address space as blocks in each network's
 * colour; and who joined or left which network, from Docker's own events.
 *
 * Then the networks as a table, each keeping its colour down its edge, and
 * every container as a second table of where it sits — each network it is on
 * with its address, the names the others reach it by and its traffic — so
 * "can these two see each other" is answered by whether their rows share a
 * colour. A network opens its sheet; a container its own page.
 *
 * The page was a column of grey cards, each a name, a subnet and a count,
 * with the members one press away and nothing on it that moved. Its three
 * chips are the owner chips now, which count and narrow by who made each
 * network; the count of containers is each row's products and its running
 * share; the subnet is a column, and the pool it came from is the band's.
 */
export function Networks() {
  const router = useRouter()
  const { can } = useAuth()
  const { host } = useMetrics()
  const { confirm, dialog } = useConfirm()
  const [selected, select] = useQuerySelection("network")
  const [creating, setCreating] = useState(false)
  const [attachTo, setAttachTo] = useState<DockerNetwork | null>(null)
  const [query, setQuery] = useSessionState("docker.networks.query", "")
  const [rememberedShelf, setShelf] = useSessionState<string>("docker.networks.shelf", "")
  const shelf = (
    ["compose", "dashboard", "standalone", "docker", "unused"].includes(rememberedShelf)
      ? rememberedShelf
      : ""
  ) as Shelf
  const [lens, setLens] = useSessionState<Lens>("docker.networks.lens", "")
  const [onNetwork, setOnNetwork] = useSessionState<string>("docker.networks.on", "")
  const containersPanel = useRef<HTMLElement>(null)
  const now = useNow(15_000)

  const list = usePoll(
    (signal) => get<DockerNetwork[]>("/docker/networks/", undefined, signal),
    POLL,
  )
  const info = usePoll(
    (signal) => get<DockerNetworkingInfo>("/docker/info", undefined, signal),
    300_000,
  )
  const traffic = useLiveTraffic()

  // Undefined until the socket's first answer, so an empty host is told
  // apart from one still being read.
  const [listed, setContainers] = useState<Container[]>()
  const containers = useMemo(() => listed ?? [], [listed])
  const [containerTraffic, setContainerTraffic] = useState<Map<string, ContainerTraffic>>(
    () => new Map(),
  )
  const previous = useRef(new Map<string, ContainerStats>())
  const onContainers = useCallback((envelope: Envelope) => {
    if (envelope.type === "containers") {
      setContainers(envelope.data as Container[])
      return
    }
    if (envelope.type !== "stats") return
    const frames = envelope.data as ContainerStats[]
    // Measured here rather than in the updater: React may run that later,
    // after the next frame has replaced the one each rate is taken against.
    const before = previous.current
    previous.current = new Map(frames.map((f) => [f.id, f]))
    const measured = frames.map((frame) => ({
      frame,
      rates: containerRates(frame, before.get(frame.id)),
    }))
    setContainerTraffic((held) => {
      const next = new Map<string, ContainerTraffic>()
      for (const { frame, rates } of measured) {
        const prior = held.get(frame.id)
        if (rates.rx === null || rates.tx === null) {
          // One frame Docker could not measure keeps the last reading; a
          // container that has lost its interfaces stops reading at all.
          if (prior && !prior.held) next.set(frame.id, { ...prior, held: true })
          continue
        }
        const point: NetworkLivePoint = {
          t: Date.parse(frame.ts) / 1000,
          rx: rates.rx,
          tx: rates.tx,
        }
        next.set(frame.id, {
          rx: rates.rx,
          tx: rates.tx,
          points: [...(prior?.points ?? []), point].slice(-KEEP),
        })
      }
      return next
    })
  }, [])
  useSocket("/docker/containers/stream", { onMessage: onContainers })

  const [events, setEvents] = useState<DockerEvent[]>([])
  const onEvents = useCallback((envelope: Envelope) => {
    if (envelope.type !== "events") return
    setEvents((before) => [...(envelope.data as DockerEvent[]), ...before].slice(0, 400))
  }, [])
  useSocket("/docker/events/stream", { onMessage: onEvents, query: { kinds: "network" } })

  const networks = useMemo(() => [...(list.data ?? [])].sort(networkOrder), [list.data])
  const byContainer = useMemo(() => new Map(containers.map((c) => [c.id, c])), [containers])
  const rates = useMemo(() => {
    const out = new Map<string, NetworkRate>()
    for (const network of networks) {
      const points = network.bridge ? traffic.series[network.bridge] : undefined
      const last = points?.at(-1)
      if (!points || !last) continue
      out.set(network.id, { network, rx: last.rx, tx: last.tx, points: points.slice(-60) })
    }
    return out
  }, [networks, traffic.series])
  // Docker's built-in pools are an answer only once `docker info` has said
  // no others are set; before that, or when it fails, the pools are unknown.
  const plan = useMemo(
    () => (info.data ? addressPlan(info.data, networks) : undefined),
    [info.data, networks],
  )
  const changes = useMemo(() => networkChanges(events, containers), [events, containers])

  const placements = useMemo(() => {
    const listed = endpointsByContainer(networks)
    const byName = new Map(networks.map((n) => [n.name, n]))
    const out = new Map<string, Placement[]>()
    for (const container of containers) {
      const known = listed.get(container.id) ?? []
      // A container the list has not caught up with yet still shows its
      // networks by name, without an address, rather than none.
      const names = new Set(known.map((k) => k.network.name))
      const extra = container.networks
        .filter((name) => !names.has(name) && byName.has(name))
        .map((name) => ({ network: byName.get(name)! }))
      out.set(container.id, [...known, ...extra])
    }
    return out
  }, [networks, containers])

  const counts = useMemo(() => {
    const kinds: Record<string, number> = { unused: 0 }
    for (const n of networks) {
      const kind = networkOwner(n).kind
      kinds[kind] = (kinds[kind] ?? 0) + 1
      if (isUnused(n)) kinds.unused++
    }
    return kinds
  }, [networks])
  const unused = networks.filter(isUnused)

  const visibleNetworks = useMemo(() => {
    const needle = query.trim().toLowerCase()
    return networks.filter((n) => {
      if (shelf === "unused" && !isUnused(n)) return false
      if (shelf && shelf !== "unused" && networkOwner(n).kind !== shelf) return false
      if (!needle) return true
      return (
        n.name.toLowerCase().includes(needle) ||
        n.driver.toLowerCase().includes(needle) ||
        n.subnets.some((s) => s.toLowerCase().includes(needle)) ||
        n.usedBy.some((c) => c.toLowerCase().includes(needle))
      )
    })
  }, [networks, query, shelf])

  const lensCounts = useMemo(() => {
    const out = { several: 0, default: 0, stopped: 0 }
    for (const c of containers) {
      const placed = placements.get(c.id) ?? []
      if (placed.filter((p) => p.network.driver !== "host").length > 1) out.several++
      if (placed.some((p) => p.network.name === "bridge")) out.default++
      if (c.state !== "running" && c.state !== "paused") out.stopped++
    }
    return out
  }, [containers, placements])
  const focused = networks.find((n) => n.id === onNetwork)
  const visibleContainers = useMemo(() => {
    const needle = query.trim().toLowerCase()
    return containers.filter((c) => {
      const placed = placements.get(c.id) ?? []
      if (focused && !placed.some((p) => p.network.id === focused.id)) return false
      if (lens === "several" && placed.filter((p) => p.network.driver !== "host").length < 2) {
        return false
      }
      if (lens === "default" && !placed.some((p) => p.network.name === "bridge")) return false
      if (lens === "stopped" && (c.state === "running" || c.state === "paused")) return false
      if (!needle) return true
      return (
        c.name.toLowerCase().includes(needle) ||
        c.image.toLowerCase().includes(needle) ||
        (c.composeStack ?? "").toLowerCase().includes(needle) ||
        placed.some(
          (p) =>
            p.network.name.toLowerCase().includes(needle) ||
            (p.endpoint?.ipv4 ?? "").includes(needle) ||
            (p.endpoint?.ipv6 ?? "").toLowerCase().includes(needle),
        )
      )
    })
  }, [containers, placements, focused, lens, query])

  const showOn = (network: DockerNetwork) => {
    setOnNetwork(network.id === onNetwork ? "" : network.id)
    containersPanel.current?.scrollIntoView({ block: "start", behavior: "smooth" })
  }

  // Docker's prune counts endpoints, and a stopped container holds none, so
  // it also removes a network whose members are all stopped — which then
  // fail to start. The confirmation names those too.
  const stoppedOnly = networks.filter(
    (n) =>
      !isUnused(n) &&
      !isSystem(n) &&
      (n.endpoints ?? []).every((e) => {
        const state = byContainer.get(e.container)?.state
        return state !== undefined && state !== "running" && state !== "paused"
      }),
  )
  const prune = () =>
    confirm({
      title: "Remove unused networks",
      confirmLabel: "Remove",
      description: (
        <div className="space-y-2">
          <p>
            Removes the {plural(unused.length, "network")} nothing is attached to:{" "}
            <b>{unused.map((n) => n.name).join(", ")}</b>, and returns{" "}
            {unused.length === 1 ? "its subnet" : "their subnets"} to the pool. Docker recreates a
            compose network the next time its stack comes up.
          </p>
          {stoppedOnly.length > 0 && (
            <p className="text-warning">
              Docker also removes networks whose containers are all stopped:{" "}
              <b>{stoppedOnly.map((n) => n.name).join(", ")}</b>. Those containers will not start
              until their network is made again.
            </p>
          )}
        </div>
      ),
      action: async () => {
        const rep = await post<{ items: string[] }>("/docker/networks/prune")
        notify.success(
          rep.items.length ? `Removed ${plural(rep.items.length, "network")}` : "Nothing to remove",
        )
        list.refresh()
      },
    })

  const header = <PageContext eyebrow="Docker" title="Networks" />

  if (list.loading && !list.data) {
    return (
      <Page>
        {header}
        <LoadingPanel />
      </Page>
    )
  }
  if (list.error && !list.data) {
    return (
      <Page>
        {header}
        <ErrorState error={list.error} />
      </Page>
    )
  }

  const attached = new Set(networks.flatMap((n) => (n.endpoints ?? []).map((e) => e.container)))
  const version = info.data?.ServerVersion
  const selectedNetwork = networks.find((n) => n.id === selected)

  return (
    <Workspace
      name="Networks"
      refresh={list.refresh}
      escape={() => {
        if (query) {
          setQuery("")
          return true
        }
        if (onNetwork) {
          setOnNetwork("")
          return true
        }
        if (lens) {
          setLens("")
          return true
        }
        if (shelf) {
          setShelf("")
          return true
        }
        return false
      }}
      commands={[
        {
          id: "unused",
          label: shelf === "unused" ? "Show every network" : "Show unused networks",
          run: () => setShelf(shelf === "unused" ? "" : "unused"),
        },
      ]}
    >
      <Page className="animate-rise">
        {header}

        <HostIdentity
          mark="docker"
          fallback={Box}
          title={version ? `Docker ${version}` : "Docker"}
          facts={
            <>
              {(host?.hostname || info.data?.Name) && (
                <>
                  <HostFact product={platformProduct(host?.platform)}>
                    {host?.hostname || info.data?.Name}
                  </HostFact>
                  <FactDot />
                </>
              )}
              <span className="numeric">
                {plural(networks.length, "network")}, {networks.length - (counts.docker ?? 0)} made
                here
              </span>
              <FactDot />
              <span className="numeric">{plural(attached.size, "container")} on them</span>
              {lensCounts.several > 0 && (
                <>
                  <FactDot />
                  <span className="numeric">{lensCounts.several} joining two or more</span>
                </>
              )}
            </>
          }
          aside={
            <div className="flex flex-wrap items-center gap-3">
              <Verdict
                plan={plan}
                unused={unused.length}
                pressed={shelf === "unused"}
                onUnused={() => setShelf(shelf === "unused" ? "" : "unused")}
              />
              <div className="flex items-center gap-1">
                <WorkspaceHelp compact />
              </div>
            </div>
          }
        />

        <NetworkBand
          rates={[...rates.values()]}
          trafficReady={traffic.now > 0}
          trafficFailed={traffic.error !== undefined && traffic.now === 0}
          plan={plan}
          poolsFailed={info.error !== undefined && !info.data}
          changes={changes}
          networkIds={new Set(networks.map((n) => n.id))}
          containersReady={listed !== undefined}
          containers={byContainer}
          now={now}
          onOpen={(network) => select(typeof network === "string" ? network : network.id)}
        />

        {/* Framed, because it is a table: the grid owns a scroll region and
            the edge is what says so (§2). Everything above it stays plain. */}
        <Panel>
          <PanelHeader
            title={
              <>
                Networks
                <span className="numeric ml-2 text-body font-normal text-muted-foreground">
                  {networks.length}
                </span>
              </>
            }
          >
            <ChipStrip aria-label="Made by" className="mr-auto">
              {OWNER_KINDS.map(({ kind, label, title }) => {
                const count = counts[kind] ?? 0
                if (count === 0 && shelf !== kind) return null
                return (
                  <FilterChip
                    key={kind}
                    selected={shelf === kind}
                    title={title}
                    onClick={() => setShelf(shelf === kind ? "" : kind)}
                  >
                    {label}
                    <ChipCount>{count}</ChipCount>
                  </FilterChip>
                )
              })}
              {(counts.unused > 0 || shelf === "unused") && (
                <FilterChip
                  selected={shelf === "unused"}
                  title="Nothing attached: each holds a block of the address pool"
                  onClick={() => setShelf(shelf === "unused" ? "" : "unused")}
                >
                  <span aria-hidden className="size-1.5 rounded-full bg-warning" />
                  Unused
                  <ChipCount className="text-warning opacity-100">{counts.unused}</ChipCount>
                </FilterChip>
              )}
            </ChipStrip>
          </PanelHeader>
          <PanelToolbar>
            <SearchInput
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="Network, subnet, container or address"
              containerClassName="sm:w-72"
            />
            {shelf && (
              <FilterChip selected aria-label="Show every network" onClick={() => setShelf("")}>
                {shelf === "unused" ? "Unused" : OWNER_KINDS.find((k) => k.kind === shelf)?.label}
                <Cross aria-hidden className="size-3" />
              </FilterChip>
            )}
            <div className="ml-auto flex items-center gap-1.5">
              {can("destructive") && unused.length > 0 && (
                <Button
                  size="sm"
                  variant="outline"
                  title="docker network prune: remove every network nothing is attached to"
                  onClick={prune}
                >
                  <Trash className="size-3.5" />
                  Remove unused
                </Button>
              )}
              {can("service.control") && (
                <Button size="sm" onClick={() => setCreating(true)}>
                  <Plus className="size-3.5" />
                  Create network
                </Button>
              )}
            </div>
          </PanelToolbar>
          <PanelBody flush>
            {networks.length === 0 ? (
              <EmptyState
                icon={NetworkDevice}
                title="No networks"
                description="Docker has not reported even its own three."
                className="my-4"
              />
            ) : visibleNetworks.length === 0 ? (
              <EmptyState
                icon={NetworkDevice}
                title="No networks match"
                description="Clear the search, or pick a different kind."
                className="my-4"
                action={
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={() => {
                      setQuery("")
                      setShelf("")
                    }}
                  >
                    Clear filters
                  </Button>
                }
              />
            ) : (
              <NetworkRows
                key={`${shelf}\u0000${query}`}
                rows={visibleNetworks}
                rates={rates}
                containers={byContainer}
                confirm={confirm}
                onOpen={(network) => select(network.id)}
                onAttach={setAttachTo}
                onChanged={list.refresh}
              />
            )}
          </PanelBody>
          <PanelFooter className="text-hint text-muted-foreground">
            <span className="numeric">{plural(visibleNetworks.length, "network")}</span>
            <span className="text-muted-foreground/40">·</span>
            <span>in use first, then unused, then Docker&apos;s own</span>
            {traffic.now > 0 && (
              <>
                <span className="text-muted-foreground/40">·</span>
                <span>traffic is each bridge&apos;s, every two seconds</span>
              </>
            )}
          </PanelFooter>
        </Panel>

        <Panel ref={containersPanel} className="scroll-mt-4">
          <PanelHeader
            title={
              <>
                Containers
                <span className="numeric ml-2 text-body font-normal text-muted-foreground">
                  {containers.length}
                </span>
              </>
            }
          >
            <ChipStrip aria-label="Which containers" className="mr-auto">
              {LENSES.map(({ value, label, title, tone }) => {
                const count = lensCounts[value]
                if (count === 0 && lens !== value) return null
                return (
                  <FilterChip
                    key={value}
                    selected={lens === value}
                    title={title}
                    onClick={() => setLens(lens === value ? "" : value)}
                  >
                    {tone && <span aria-hidden className="size-1.5 rounded-full bg-warning" />}
                    {label}
                    <ChipCount className={cn(tone && "text-warning opacity-100")}>
                      {count}
                    </ChipCount>
                  </FilterChip>
                )
              })}
            </ChipStrip>
            {focused && (
              <FilterChip
                selected
                aria-label={`Show containers on every network, not only ${focused.name}`}
                onClick={() => setOnNetwork("")}
              >
                <HueKey color={networkHue(focused.name)} />
                on {focused.name}
                <Cross aria-hidden className="size-3" />
              </FilterChip>
            )}
          </PanelHeader>
          <PanelBody flush>
            {containers.length === 0 ? (
              <EmptyState
                icon={Box}
                title={listed ? "No containers" : "Reading the containers…"}
                description={
                  listed
                    ? "Nothing on this host is on any network yet."
                    : "They arrive over the same socket as the Containers page."
                }
                className="my-4"
              />
            ) : visibleContainers.length === 0 ? (
              <EmptyState
                icon={Box}
                title="No containers match"
                description="Clear the network, the chip or the search."
                className="my-4"
              />
            ) : (
              <ContainerNetworkRows
                key={`${onNetwork}\u0000${lens}\u0000${query}`}
                rows={visibleContainers}
                focused={onNetwork || undefined}
                placements={placements}
                traffic={containerTraffic}
                onNetwork={showOn}
                onOpen={(container) =>
                  router.push(`/docker/containers/${encodeURIComponent(container.id)}`)
                }
              />
            )}
          </PanelBody>
          <PanelFooter className="text-hint text-muted-foreground">
            <span className="numeric">{plural(visibleContainers.length, "container")}</span>
            <span className="text-muted-foreground/40">·</span>
            <span>a network&apos;s name narrows the table to it</span>
          </PanelFooter>
        </Panel>

        <NetworkSheet
          id={selected}
          listed={selectedNetwork}
          rate={selectedNetwork ? rates.get(selectedNetwork.id) : undefined}
          traffic={containerTraffic}
          containers={byContainer}
          confirm={confirm}
          onOpenChange={(open) => !open && select(null)}
          onChanged={list.refresh}
        />
        <AttachDialog
          open={attachTo !== null}
          networkId={attachTo?.id ?? null}
          networkName={attachTo?.name}
          onOpenChange={(open) => !open && setAttachTo(null)}
          onAttached={list.refresh}
          attached={new Set((attachTo?.endpoints ?? []).map((e) => e.container))}
        />
        <NewNetworkDialog open={creating} onOpenChange={setCreating} onCreated={list.refresh} />
        {dialog}
      </Page>
    </Workspace>
  )
}

/**
 * The line's verdict: the pool spent or nearly, which is what fails the next
 * network; else the networks nothing is attached to, a press of which narrows
 * the table to them; else that every network is in use.
 */
function Verdict({
  plan,
  unused,
  pressed,
  onUnused,
}: {
  plan?: ReturnType<typeof addressPlan>
  unused: number
  pressed: boolean
  onUnused: () => void
}) {
  if (plan?.pressure === "full") {
    return <Status tone="danger" label="Address pool full" />
  }
  if (plan?.pressure === "warning") {
    return <Status tone="warning" label={`Address pool ${plan.used} of ${plan.total} taken`} />
  }
  if (unused > 0) {
    return (
      <button
        type="button"
        aria-pressed={pressed}
        onClick={onUnused}
        title="Each holds a block of the address pool"
        className="rounded-md px-1.5 py-1 focus-ring transition-colors hover:bg-row-hover"
      >
        <Status tone="warning" label={`${plural(unused, "network")} unused`} />
      </button>
    )
  }
  return <Status tone="running" label="Every network in use" />
}
