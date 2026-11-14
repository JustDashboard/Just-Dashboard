"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import Link from "next/link"
import { useParams, useRouter, useSearchParams } from "next/navigation"
import { Tabs as TabsPrimitive } from "radix-ui"
import {
  ArrowLeft,
  ArrowUpRight,
  ChevronDown,
  ChevronUp,
  Copy,
  Layers,
  Linked,
  NetworkDevice,
  Servers,
  Trash,
} from "@/components/icons"
import { ApiError, get, post } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { membersKnown, type NetworkChangePreview } from "@/lib/docker-networks"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { DockerNetwork, NetworkDetail } from "@/lib/types"
import { useSessionState, useViewState } from "@/lib/view-state"
import { useAuth } from "@/hooks/use-auth"
import { useMetrics } from "@/hooks/use-metrics"
import { usePoll } from "@/hooks/use-poll"
import { IconAction } from "@/components/icon-action"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Page, PageContext } from "@/components/page"
import { ProductLogo } from "@/components/product-logo"
import { EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { Status } from "@/components/status-dot"
import { ChipCount, tabClasses } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { VerbMenu, type Verb } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import { Tabs, TabsContent } from "@/components/ui/tabs"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { Workspace, WorkspaceHelp } from "@/components/workspace/workspace"
import { Cidr } from "@/components/network/address"
import { linkGlyph } from "@/components/network/marks"
import { useLiveTraffic } from "@/components/network/use-live-traffic"
import { HueKey, type NetworkRate } from "@/components/docker/network-band"
import { NetworkChangeDialog } from "@/components/docker/network-conflicts"
import { AttachDialog, RemoveNetworkDialog } from "@/components/docker/network-dialogs"
import { useContainersLive, useMemberCounts } from "@/components/docker/network-live"
import { NetworkPicture } from "@/components/docker/network-picture"
import { NetworkRail } from "@/components/docker/network-rail"
import {
  NetworkConfiguration,
  NetworkMembers,
  NetworkReadings,
} from "@/components/docker/network-views"
import {
  NETWORK_FILTER_KEYS,
  attachRefusal,
  filterNetworks,
  findNetwork,
  isSystem,
  memberReadings,
  networkHref,
  networkHue,
  networkOrder,
  networkOwner,
  networkStanding,
  removeRefusal,
  shelfLabel,
  shelfOf,
  type MemberReading,
} from "@/components/docker/networks"

const TABS = ["overview", "members", "configuration"] as const
type Tab = (typeof TABS)[number]

/** How often the list is read, for the rail and for who is on the network now. */
const POLL = 10_000

const BACK = (
  <Link
    href="/docker/networks"
    className="inline-flex items-center gap-1 rounded-sm focus-ring hover:underline"
  >
    <ArrowLeft className="size-3" /> Networks
  </Link>
)

/**
 * One network, as a place of its own.
 *
 * It was a `SidePanel` over the Networks table until 0.8.7: four tiles, a
 * picture, a table of members and a list of settings in a sheet's width, over
 * a table nobody could read behind it, and a member's "also on" a third line
 * under its row. So a network is a destination with a way back, the way a
 * container is (`docker/container-detail`): the identity line every
 * destination opens on (who made it, its driver and device, its subnet, how
 * many are on it, and where it stands beside its name), its views as the
 * underlined strip — the picture and its readings, the members, what Docker
 * was told — and the view in the address.
 *
 * What the sheet did well — the table beside the thing being read — is the
 * column on the left from `xl` (`NetworkRail`): the table's own search, owner
 * chip and order, so the next network is one press, Alt+↑ and Alt+↓ walk it,
 * and the view on screen goes along. On a phone the page is the one pane.
 *
 * The address names the network, and an id opens it too (the Engine resolves
 * either): a deployment's removal plan and the old sheet's `?network=` name
 * one by id, and the address is put right once the name is known.
 */
export function NetworkPage() {
  const params = useParams<{ name: string }>()
  const key = decode(params.name)
  const search = useSearchParams()
  const router = useRouter()
  const requested = search.get("tab")
  const [remembered, remember] = useViewState<string>("docker.network.tab", "overview")
  const [chosen, setChosen] = useState<string>(requested ?? remembered)
  // A view the address comes to name is the view: a link in the picture, or
  // Back to an address that named another. Adjusted while rendering, which
  // is how a value derived from the previous render is kept.
  const [named, setNamed] = useState(requested)
  if (requested !== named) {
    setNamed(requested)
    if (requested) setChosen(requested)
  }
  const tab: Tab = (TABS as readonly string[]).includes(chosen) ? (chosen as Tab) : "overview"
  // The view the page was left on is the one it opens on, and the address
  // says so: a link copied from it opens where the reader is.
  useEffect(() => {
    if (requested || tab === "overview") return
    const url = new URL(window.location.href)
    url.searchParams.set("tab", tab)
    window.history.replaceState(null, "", `${url.pathname}${url.search}`)
  }, [requested, tab])
  const setTab = useCallback(
    (next: string) => {
      setChosen(next)
      remember(next)
      // The address says which view, so a link to a network's members opens
      // on its members; replaced rather than pushed, because the view is not
      // a place Back should step through.
      const url = new URL(window.location.href)
      if (next === "overview") url.searchParams.delete("tab")
      else url.searchParams.set("tab", next)
      window.history.replaceState(null, "", `${url.pathname}${url.search}`)
    },
    [remember],
  )
  const open = useCallback((name: string) => router.push(networkHref(name, tab)), [router, tab])

  const list = usePoll(
    (signal) => get<DockerNetwork[]>("/docker/networks/", undefined, signal),
    POLL,
  )
  const networks = useMemo(() => [...(list.data ?? [])].sort(networkOrder), [list.data])
  const listed = findNetwork(networks, key)
  const live = useContainersLive()
  const counts = useMemberCounts(list.data)

  const [query, setQuery] = useSessionState(NETWORK_FILTER_KEYS.query, "")
  const [rememberedShelf, setShelf] = useSessionState<string>(NETWORK_FILTER_KEYS.shelf, "")
  const shelf = shelfOf(rememberedShelf)
  const rows = useMemo(() => filterNetworks(networks, query, shelf), [networks, query, shelf])
  const at = rows.findIndex((row) => row.id === listed?.id)
  const adjacent = (direction: number) => {
    const next = at >= 0 ? rows[at + direction] : direction > 0 ? rows[0] : undefined
    if (next) open(next.name)
  }
  const first = at <= 0
  const last = rows.length === 0 || at >= rows.length - 1
  const caption = !list.data
    ? list.error
      ? "The networks could not be read"
      : "Reading the networks…"
    : rows.length === networks.length
      ? plural(networks.length, "network")
      : `${rows.length} of ${networks.length}${shelf ? ` · ${shelfLabel(shelf)}` : ""}`

  return (
    <Workspace
      name="Network"
      stateKey="docker.network.page"
      refresh={list.refresh}
      commands={[
        {
          id: "previous",
          label: "Previous network",
          keys: "Alt+↑",
          chord: "Alt+ArrowUp",
          disabled: first,
          run: () => adjacent(-1),
        },
        {
          id: "next",
          label: "Next network",
          keys: "Alt+↓",
          chord: "Alt+ArrowDown",
          disabled: last,
          run: () => adjacent(1),
        },
      ]}
    >
      {/* Held to the window where the rail stands beside it, so the list and
          the identity line stay put while a view scrolls; narrower, the page
          is the one pane and scrolls as a whole. */}
      <Page fill="xl" className="gap-0 md:gap-0">
        <div className="flex min-w-0 flex-1 gap-6 xl:min-h-0">
          <NetworkRail
            className="hidden w-72 shrink-0 xl:flex"
            networks={rows}
            current={listed?.name ?? key}
            currentNetwork={listed}
            containers={live.byId}
            loading={list.loading}
            query={query}
            onQuery={setQuery}
            filtered={shelf !== ""}
            onClearFilters={() => setShelf("")}
            caption={caption}
            onOpen={open}
          />
          <NetworkMain
            key={key}
            address={key}
            listed={listed}
            listRead={list.data !== undefined}
            listError={list.error}
            expected={Object.hasOwn(counts, key) ? counts[key] : undefined}
            refreshList={list.refresh}
            live={live}
            tab={tab}
            setTab={setTab}
            previous={{ disabled: first, run: () => adjacent(-1) }}
            next={{ disabled: last, run: () => adjacent(1) }}
          />
        </div>
      </Page>
    </Workspace>
  )
}

function decode(value: string) {
  try {
    return decodeURIComponent(value)
  } catch {
    return value
  }
}

type Step = { disabled: boolean; run: () => void }

function NetworkMain({
  address,
  listed,
  listRead,
  listError,
  expected,
  refreshList,
  live,
  tab,
  setTab,
  previous,
  next,
}: {
  /** What the address names the network by: its name, or its id. */
  address: string
  /** The network as the list has it, drawn while its inspect is on its way. */
  listed?: DockerNetwork
  listRead: boolean
  listError?: Error
  /** How many members it had when the list was last read in this tab, for the waiting picture. */
  expected?: number
  refreshList: () => void
  live: ReturnType<typeof useContainersLive>
  tab: Tab
  setTab: (tab: string) => void
  previous: Step
  next: Step
}) {
  const router = useRouter()
  const { can } = useAuth()
  const { host } = useMetrics()
  const [attaching, setAttaching] = useState(false)
  const [detaching, setDetaching] = useState<MemberReading | null>(null)
  const [removing, setRemoving] = useState<DockerNetwork | null>(null)

  const inspect = usePoll<NetworkDetail>(
    (signal) =>
      get<NetworkDetail>(`/docker/networks/${encodeURIComponent(address)}`, undefined, signal),
    // Read once, and again when the list says its members changed: each read
    // inspects every member for its aliases, which on a busy default bridge
    // is forty calls to the daemon.
    0,
    [address],
  )
  const detail = inspect.data
  const refreshInspect = inspect.refresh
  const signature = listed?.usedBy.join("\n")
  const seen = useRef<string | undefined>(undefined)
  useEffect(() => {
    if (signature === undefined) return
    const before = seen.current
    seen.current = signature
    if (before !== undefined && before !== signature) refreshInspect()
  }, [signature, refreshInspect])
  // A network the list has dropped is asked for once more, so one removed
  // while this page is open is found gone rather than drawn as it was.
  const wasListed = useRef(false)
  useEffect(() => {
    if (listRead && !listed && wasListed.current) refreshInspect()
    wasListed.current = Boolean(listed)
  }, [listRead, listed, refreshInspect])

  // The list is read every ten seconds and the inspect once, so who is on it
  // and where it stands are the list's where it has the network.
  const network: DockerNetwork | undefined = listed ?? detail
  const members = useMemo(
    () =>
      network
        ? memberReadings(
            { name: network.name, endpoints: listed?.endpoints, members: detail?.members },
            live.byId,
          )
        : [],
    [network, listed?.endpoints, detail?.members, live.byId],
  )
  const traffic = useLiveTraffic(tab === "overview")
  const bridge = useMemo<NetworkRate | undefined>(() => {
    const points = network?.bridge ? traffic.series[network.bridge] : undefined
    const newest = points?.at(-1)
    if (!network || !points || !newest) return undefined
    return { network, rx: newest.rx, tx: newest.tx, points: points.slice(-60) }
  }, [network, traffic.series])

  const changed = useCallback(() => {
    refreshInspect()
    refreshList()
  }, [refreshInspect, refreshList])

  // An address that names the network by id is put right once its name is
  // known, so the address a reader copies is the one the list links to.
  const name = network?.name
  useEffect(() => {
    if (!name || name === address) return
    const url = new URL(window.location.href)
    window.history.replaceState(null, "", `${networkHref(name)}${url.search}`)
  }, [name, address])

  // Whether this page has ever had its network, so one removed while it is
  // open goes back to the list and a bad address says so.
  const loaded = useRef(false)
  const missing = inspect.error instanceof ApiError && inspect.error.status === 404
  const gone = missing && (listRead || listError !== undefined) && !listed
  useEffect(() => {
    if (network) loaded.current = true
    if (gone && loaded.current) router.replace("/docker/networks")
  }, [network, gone, router])

  const header = (
    <PageContext
      eyebrow={BACK}
      title={network?.name ?? "Network"}
      actions={
        <span className="flex items-center">
          <IconAction label="Previous network" disabled={previous.disabled} onClick={previous.run}>
            <ChevronUp />
          </IconAction>
          <IconAction label="Next network" disabled={next.disabled} onClick={next.run}>
            <ChevronDown />
          </IconAction>
          <span className="max-sm:hidden">
            <WorkspaceHelp compact />
          </span>
        </span>
      }
    />
  )

  if (!network && (gone || (inspect.error && !missing && (listRead || listError)))) {
    return (
      <div className="flex min-w-0 flex-1 flex-col gap-5">
        {header}
        {gone ? (
          <EmptyState
            icon={NetworkDevice}
            title={`No network named ${address.length > 40 ? `${address.slice(0, 12)}…` : address}`}
            description="A compose project takes its networks down with it, and a prune removes the ones nothing is on."
            action={
              <Button size="sm" asChild>
                <Link href="/docker/networks">Back to the networks</Link>
              </Button>
            }
          />
        ) : (
          <ErrorState error={inspect.error!} onRetry={changed} />
        )}
      </div>
    )
  }

  const running = live.listed
    ? members.filter((member) => member.state === "running").length
    : undefined
  const refusal = network ? attachRefusal(network) : undefined
  const canAttach = Boolean(network) && can("service.control") && !refusal
  const canDetach = Boolean(network) && can("service.control") && !isSystem(network!)

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-5">
      <div className="flex min-w-0 shrink-0 flex-col gap-5">
        {header}
        {network ? (
          <NetworkIdentity
            network={network}
            running={running}
            canAttach={canAttach}
            onAttach={() => setAttaching(true)}
            onRemove={() => setRemoving(network)}
          />
        ) : (
          <HostIdentity
            slot="network-identity"
            className="border-b-0 pb-0"
            mark="docker"
            title={
              /^[0-9a-f]{64}$/.test(address) ? (
                <TextShimmer>Reading the network…</TextShimmer>
              ) : (
                // The name is in the address, and its colour follows from it:
                // the line is where it will be before anything is read.
                <span className="inline-flex min-w-0 items-center gap-2">
                  <HueKey color={networkHue(address)} />
                  <span className="truncate">{address}</span>
                </span>
              )
            }
            state={
              <span aria-hidden className="text-xs">
                &nbsp;
              </span>
            }
            facts={<span aria-hidden>&nbsp;</span>}
          />
        )}
      </div>

      <Tabs value={tab} onValueChange={setTab} className="min-h-0 flex-1 gap-0">
        <TabsPrimitive.List
          aria-label={`Views of ${network?.name ?? "the network"}`}
          className="flex shrink-0 [scrollbar-width:none] gap-1 overflow-x-auto border-b border-hairline [&::-webkit-scrollbar]:hidden"
        >
          {TABS.map((view) => (
            <TabsPrimitive.Trigger
              key={view}
              value={view}
              className={tabClasses(tab === view, "h-10")}
            >
              {view === "overview" ? "Overview" : view === "members" ? "Members" : "Configuration"}
              {view === "members" && network && membersKnown(network) && members.length > 0 && (
                <ChipCount>{members.length}</ChipCount>
              )}
            </TabsPrimitive.Trigger>
          ))}
        </TabsPrimitive.List>

        <TabsContent value="overview" className="min-h-0 flex-1 pt-4 xl:overflow-y-auto">
          <div className="flex min-w-0 flex-col gap-6 pb-4">
            <NetworkPicture
              network={network}
              members={members}
              options={detail?.options}
              host={host?.hostname}
              traffic={live.traffic}
              containers={live.byId}
              tab={tab}
              loading={!network}
              expected={expected}
              onMembers={() => setTab("members")}
            />
            <NetworkReadings
              network={network}
              members={members}
              bridge={bridge}
              loading={!network}
            />
          </div>
        </TabsContent>
        <TabsContent value="members" className="min-h-0 flex-1 pt-5 xl:overflow-y-auto">
          {tab === "members" && !network && <LoadingRows rows={4} />}
          {tab === "members" && network && (
            <NetworkMembers
              network={network}
              members={members}
              detailed={detail !== undefined}
              membersError={detail?.membersError}
              traffic={live.traffic}
              containers={live.byId}
              canDetach={canDetach}
              canAttach={canAttach}
              onAttach={() => setAttaching(true)}
              onDetach={setDetaching}
            />
          )}
        </TabsContent>
        <TabsContent value="configuration" className="min-h-0 flex-1 pt-6 xl:overflow-y-auto">
          {tab === "configuration" && !network && <LoadingRows rows={6} />}
          {tab === "configuration" && network && (
            <NetworkConfiguration network={network} detail={detail} />
          )}
        </TabsContent>
      </Tabs>

      {network && (
        <>
          <AttachDialog
            open={attaching}
            networkId={network.id}
            networkName={network.name}
            onOpenChange={setAttaching}
            onAttached={changed}
            attached={new Set(members.map((member) => member.id))}
          />
          <NetworkChangeDialog
            open={detaching !== null}
            onOpenChange={(open) => !open && setDetaching(null)}
            target={`${network.id}:${detaching?.id ?? ""}`}
            title={`Detach ${detaching?.name ?? ""}`}
            description={`What detaching ${detaching?.name ?? "the container"} from ${network.name} disturbs, read before it happens.`}
            intro={
              <p className="text-body">
                <b>{detaching?.name}</b> leaves <b>{network.name}</b> immediately. Reattaching puts
                it back; connections open over this network are cut.
              </p>
            }
            confirmLabel="Detach"
            emptyLabel="Nothing else on this network reaches it, and it keeps its other networks."
            load={(signal) =>
              get<NetworkChangePreview>(
                `/docker/networks/${encodeURIComponent(network.id)}/disconnect`,
                { container: detaching?.id ?? "" },
                signal,
              )
            }
            onConfirm={async () => {
              await post(`/docker/networks/${encodeURIComponent(network.id)}/disconnect`, {
                container: detaching?.id,
              })
              notify.success(`${detaching?.name} left ${network.name}`)
              changed()
            }}
          />
          <RemoveNetworkDialog
            network={removing}
            onOpenChange={(open) => !open && setRemoving(null)}
            onRemoved={() => router.replace("/docker/networks")}
          />
        </>
      )}
    </div>
  )
}

/**
 * The line the page opens on, laid out as a project's header: the network as
 * what carries it — Docker's mark for a bridge, the kind of device otherwise —
 * its name behind its own colour, where it stands beside it, and after them
 * who made it (a compose project's is a way to its stack), its driver, its
 * subnet and how many are on it. Attach a
 * container is its one command; the rest are behind the menu, Remove with the
 * reason where Docker would refuse.
 */
function NetworkIdentity({
  network,
  running,
  canAttach,
  onAttach,
  onRemove,
}: {
  network: DockerNetwork
  /** How many of its members run; undefined until the containers socket has answered. */
  running?: number
  canAttach: boolean
  onAttach: () => void
  onRemove: () => void
}) {
  const router = useRouter()
  const { can } = useAuth()
  const owner = networkOwner(network)
  const standing = networkStanding(network, running)
  const refusal = attachRefusal(network)
  const blocked = removeRefusal(network)
  const [first, ...more] = network.subnets
  const bridged = network.driver === "bridge" || network.driver === "overlay"
  const count = network.usedBy.length

  const verbs: Verb[] = []
  if (can("service.control") && refusal?.reason) {
    verbs.push({
      key: "attach",
      label: refusal.reason,
      icon: Linked,
      disabled: true,
      run: () => undefined,
    })
  }
  if (owner.kind === "compose" && owner.project) {
    const project = owner.project
    verbs.push({
      key: "stack",
      label: "Open the stack",
      icon: Layers,
      run: () => router.push(`/docker/stacks/${encodeURIComponent(project)}`),
    })
  }
  verbs.push(
    {
      key: "copy-name",
      label: "Copy name",
      icon: Copy,
      run: () => void copyText(network.name, "Name copied"),
    },
    {
      key: "copy-id",
      label: "Copy ID",
      icon: Copy,
      run: () => void copyText(network.id, "ID copied"),
    },
  )
  if (can("destructive")) {
    verbs.push({
      key: "remove",
      label: blocked ?? "Remove",
      icon: Trash,
      danger: !blocked,
      disabled: blocked !== undefined,
      run: onRemove,
    })
  }

  return (
    <HostIdentity
      slot="network-identity"
      label={`About ${network.name}`}
      className="animate-rise border-b-0 pb-0"
      logo={
        <ProductLogo
          id={bridged ? "docker" : undefined}
          fallback={network.driver === "host" ? Servers : linkGlyph(network.driver)}
          className="size-10 rounded-lg [&_img]:size-6 [&_svg]:size-5"
        />
      }
      title={
        <span className="inline-flex min-w-0 items-center gap-2">
          <HueKey color={networkHue(network.name)} />
          <span className="truncate">{network.name}</span>
        </span>
      }
      state={
        <>
          <Status tone={standing.tone} label={standing.label} />
          {network.internal && (
            <Tag title="Containers on it reach each other and nothing beyond">no internet</Tag>
          )}
          {network.ipv6 && <Tag>IPv6</Tag>}
        </>
      }
      facts={
        <>
          {owner.kind === "compose" && owner.project ? (
            <Link
              href={`/docker/stacks/${encodeURIComponent(owner.project)}`}
              className="inline-flex min-w-0 items-center gap-1 rounded-sm text-foreground focus-ring hover:underline"
            >
              <span className="truncate">{owner.label}</span>
              <ArrowUpRight aria-hidden className="size-3 shrink-0" />
            </Link>
          ) : (
            <span>{owner.label}</span>
          )}
          <FactDot />
          <span>{network.driver === "null" ? "no network" : network.driver}</span>
          <FactDot />
          {first ? (
            <span className="inline-flex items-center gap-1.5" title={network.subnets.join(", ")}>
              <Cidr cidr={first} className="text-foreground" />
              {more.length > 0 && <span className="numeric">+{more.length}</span>}
            </span>
          ) : (
            <span>{network.driver === "host" ? "the server's own addresses" : "no subnet"}</span>
          )}
          <FactDot />
          <span className="numeric">
            {!membersKnown(network)
              ? "members unread"
              : count === 0
                ? "nothing attached"
                : plural(count, "container")}
          </span>
        </>
      }
      command={
        canAttach && (
          <Button size="sm" aria-label="Attach a container" onClick={onAttach}>
            <Linked className="size-3.5" />
            <span className="sm:hidden">Attach</span>
            <span className="max-sm:hidden">Attach a container</span>
          </Button>
        )
      }
      menu={<VerbMenu verbs={verbs} label={`More actions for ${network.name}`} />}
    />
  )
}
