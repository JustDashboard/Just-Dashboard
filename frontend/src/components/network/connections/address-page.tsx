"use client"

import { useCallback, useMemo, useState } from "react"
import Link from "next/link"
import { useParams, useRouter, useSearchParams } from "next/navigation"
import { Tabs as TabsPrimitive } from "radix-ui"
import {
  ArrowLeft,
  ChevronDown,
  ChevronUp,
  Crosshair,
  Inspect,
  LockOpen,
  Slash,
} from "@/components/icons"
import { del, get } from "@/lib/api"
import { networkOf } from "@/lib/clients"
import { duration, plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import {
  blockStanding,
  recentHours,
  type AddressBlock,
  type ConnectionDetail,
} from "@/lib/network-traffic"
import type { Connections, Peer, PortsMeta } from "@/lib/types"
import { useSessionState } from "@/lib/view-state"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { useConfirm } from "@/components/confirm-dialog"
import { useNow } from "@/components/deploy/vocabulary"
import { IconAction } from "@/components/icon-action"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Page, PageContext } from "@/components/page"
import { EmptyState } from "@/components/state"
import { Status } from "@/components/status-dot"
import { tabClasses } from "@/components/tabs"
import { Tabs, TabsContent } from "@/components/ui/tabs"
import { VerbMenu, type Verb } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import { Workspace, WorkspaceHelp } from "@/components/workspace/workspace"
import { addressVerbs } from "@/components/security/address-verbs"
import { useSecurity } from "@/components/security/security-context"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { BlockDialog } from "@/components/network/connections/block-dialog"
import { AddressRail } from "@/components/network/connections/address-rail"
import {
  AddressLive,
  AddressPast,
  AddressPicture,
} from "@/components/network/connections/address-views"
import { AddressMark } from "@/components/network/connections/rows"
import {
  ADDRESS_TABS,
  CONNECTIONS_FILTER_DEFAULTS,
  CONNECTIONS_FILTER_KEY,
  addressHref,
  blockContext,
  blockable,
  matchPeers,
  parseAddress,
  stepAddress,
  type AddressTab,
  type ConnectionsFilters,
} from "@/components/network/connections/model"

const BACK = (
  <Link
    href="/network/connections"
    className="inline-flex items-center gap-1 rounded-sm focus-ring hover:underline"
  >
    <ArrowLeft className="size-3" /> Connections
  </Link>
)

/**
 * One remote address, as a place of its own.
 *
 * It was a `SidePanel` over the connections table until 0.8.7: a peer's
 * tuples, what closed, the layers it crosses and the block, in a sheet's
 * width with no address of its own to share or come back to. So an address
 * is a destination with a way back, built the way a process is
 * (`procs/process-page.tsx`): its identity line with its verbs, its two views
 * — what it holds now, and what socket history kept of an hour — as the
 * underlined strip, in the address bar as `?tab=` and `?hour=`.
 *
 * From `xl` the live list stands beside it (`AddressRail`), with the list's own
 * search and chip, so checking the next caller is one press, Alt+↑ and Alt+↓
 * walk it and the view on screen goes with you. On a phone the page is the
 * one pane, with the way back and the same two arrows.
 *
 * Every role reads the summary; the tuples, the past hour and the block are
 * an administrator's, as the routes behind them are.
 */
export function AddressPage() {
  const params = useParams<{ address: string }>()
  const search = useSearchParams()
  const router = useRouter()
  const { can } = useAuth()
  const admin = can("system.admin")
  const address = parseAddress(params.address)
  const requested = search.get("tab")
  const [chosen, setChosen] = useState<string>(requested ?? "live")
  const [hour, setHour] = useState(search.get("hour") ?? "")
  const tab: AddressTab =
    admin && (ADDRESS_TABS as readonly string[]).includes(chosen) ? (chosen as AddressTab) : "live"
  const hours = useMemo(() => recentHours(new Date(), 24), [])

  // The address says which view and which hour, replaced rather than pushed:
  // a view is not a place Back should step through.
  const write = useCallback((next: { tab: string; hour: string }) => {
    const url = new URL(window.location.href)
    if (next.tab === "live") url.searchParams.delete("tab")
    else url.searchParams.set("tab", next.tab)
    if (next.tab === "past" && next.hour) url.searchParams.set("hour", next.hour)
    else url.searchParams.delete("hour")
    window.history.replaceState(null, "", `${url.pathname}${url.search}`)
  }, [])
  const setTab = (next: string) => {
    setChosen(next)
    write({ tab: next, hour })
  }
  const pickHour = (next: string) => {
    setHour(next)
    write({ tab, hour: next })
  }

  const list = usePoll<Connections>((signal) => get("/connections", undefined, signal), 10_000)
  const [filters, setFilters] = useSessionState<ConnectionsFilters>(
    CONNECTIONS_FILTER_KEY,
    CONNECTIONS_FILTER_DEFAULTS,
  )
  const scope = filters.scope === "public" ? "public" : "all"
  const rows = useMemo(
    () => matchPeers(list.data?.peers ?? [], scope, filters.q),
    [list.data?.peers, scope, filters.q],
  )
  const order = rows.map((row) => row.address)
  const open = (next: string) => router.push(addressHref(next, { tab, hour }))
  const step = (direction: 1 | -1) => {
    const next = address ? stepAddress(order, address, direction) : undefined
    if (next) open(next)
  }
  const at = address ? order.indexOf(address) : -1
  const previous = { disabled: order.length === 0 || at === 0, run: () => step(-1) }
  const next = { disabled: order.length === 0 || at === order.length - 1, run: () => step(1) }

  return (
    <Workspace
      name="Remote address"
      stateKey="connections.address"
      refresh={list.refresh}
      commands={[
        {
          id: "previous",
          label: "Previous address",
          keys: "Alt+↑",
          chord: "Alt+ArrowUp",
          disabled: previous.disabled,
          run: previous.run,
        },
        {
          id: "next",
          label: "Next address",
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
          {address && (
            <AddressRail
              className="hidden w-72 shrink-0 xl:flex"
              rows={rows}
              current={address}
              loading={list.loading && !list.data}
              query={filters.q}
              onQuery={(q) => setFilters((previous) => ({ ...previous, q }))}
              scope={scope}
              onScope={(next) => setFilters((previous) => ({ ...previous, scope: next }))}
              fromInternet={list.data?.peers.filter((p) => !p.private).length}
              onOpen={open}
            />
          )}
          {address ? (
            <AddressMain
              key={address}
              address={address}
              admin={admin}
              list={list.data}
              listError={list.error}
              listLastSuccess={list.lastSuccess}
              refreshList={list.refresh}
              tab={tab}
              setTab={setTab}
              hours={hours}
              hour={hour}
              onHour={pickHour}
              previous={previous}
              next={next}
            />
          ) : (
            <div className="flex min-w-0 flex-1 flex-col gap-5">
              <PageContext eyebrow={BACK} title="Remote address" />
              <EmptyState
                title="That is not an address"
                description="An address here is an IPv4 or IPv6 address, as the connections list writes it."
                action={
                  <Link
                    href="/network/connections"
                    className="text-body font-medium text-brand hover:underline"
                  >
                    Back to the connections
                  </Link>
                }
              />
            </div>
          )}
        </div>
      </Page>
    </Workspace>
  )
}

type Step = { disabled: boolean; run: () => void }

function AddressMain({
  address,
  admin,
  list,
  listError,
  listLastSuccess,
  refreshList,
  tab,
  setTab,
  hours,
  hour,
  onHour,
  previous,
  next,
}: {
  address: string
  admin: boolean
  list: Connections | undefined
  listError?: Error
  listLastSuccess?: number
  refreshList: () => void
  tab: AddressTab
  setTab: (tab: string) => void
  hours: { from: string; to: string; label: string }[]
  hour: string
  onHour: (hour: string) => void
  previous: Step
  next: Step
}) {
  const { can } = useAuth()
  const router = useRouter()
  const { firewall } = useSecurity()
  const { confirm, dialog } = useConfirm()
  const [blocking, setBlocking] = useState(false)
  const peer = list?.peers.find((p) => p.address === address)
  const detail = usePoll<ConnectionDetail>(
    (signal) => get(`/connections/${encodeURIComponent(address)}`, undefined, signal),
    10_000,
    [address],
    { enabled: admin },
  )
  const blocks = usePoll<AddressBlock[]>(
    (signal) => get("/firewall/blocks", undefined, signal),
    30_000,
  )
  const range = usePoll((signal) => get<PortsMeta>("/ports/meta", undefined, signal), 0).data
    ?.ephemeralRange
  const block = (blocks.data ?? []).find((b) => b.address === address && b.state === "active")
  const isPrivate = peer?.private ?? detail.data?.private
  const mayBlock = admin && blockable(address, isPrivate) && !block
  // Whether it is blocked already is not known until the blocks are read: the
  // command stands in its place meanwhile, and cannot be pressed.
  const blocksRead = blocks.data !== undefined || blocks.error !== undefined

  const lift = (b: AddressBlock) =>
    confirm({
      title: `Lift the block on ${b.address}`,
      confirmLabel: "Lift",
      description: (
        <p>
          Its deny rule is removed and {b.address} can reach this server again. The record of the
          block stays.
        </p>
      ),
      action: async () => {
        await del(`/firewall/blocks/${encodeURIComponent(b.id)}`)
        notify.success(`${b.address} is no longer blocked`)
        blocks.refresh()
      },
    })

  // The lookups and the pages that go further are behind the menu; the
  // block, or lifting it, is the line's one command, because it is what the
  // page is opened to decide.
  const lookups: Verb[] = [
    ...addressVerbs({ ip: address, navigate: (href) => router.push(href) }),
    {
      key: "investigate",
      label: "Investigate a path",
      icon: Inspect,
      run: () => router.push("/network/investigate"),
    },
    {
      key: "capture",
      label: "Capture packets",
      icon: Crosshair,
      run: () => router.push("/network/captures"),
    },
  ]
  // One word on the button, as a verb is; its full name is what a screen
  // reader and the list's menu call it.
  const command = mayBlock ? (
    <Button
      size="sm"
      variant="destructive"
      disabled={blocking || !blocksRead}
      aria-label="Block at the firewall"
      onClick={() => setBlocking(true)}
    >
      <Slash className="size-3.5" />
      Block
    </Button>
  ) : block && can("destructive") ? (
    <Button size="sm" variant="outline" aria-label="Lift the block" onClick={() => lift(block)}>
      <LockOpen className="size-3.5" />
      Lift
    </Button>
  ) : undefined

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-5">
      <div className="flex min-w-0 shrink-0 flex-col gap-5">
        <PageContext
          eyebrow={BACK}
          title={address}
          actions={
            <>
              <span className="flex items-center">
                <IconAction
                  label="Previous address"
                  disabled={previous.disabled}
                  onClick={previous.run}
                >
                  <ChevronUp />
                </IconAction>
                <IconAction label="Next address" disabled={next.disabled} onClick={next.run}>
                  <ChevronDown />
                </IconAction>
                <span className="max-sm:hidden">
                  <WorkspaceHelp compact />
                </span>
              </span>
            </>
          }
        />
        <AddressIdentity
          address={address}
          peer={peer}
          listed={list !== undefined}
          block={block}
          oldest={oldestSocket(detail.data)}
          command={command}
          menu={<VerbMenu verbs={lookups} label={`Actions for ${address}`} />}
        />
        <NetworkReadWarning error={listError} refresh={refreshList} lastSuccess={listLastSuccess} />
      </div>

      {admin ? (
        <Tabs value={tab} onValueChange={setTab} className="min-h-0 flex-1 gap-0">
          <TabsPrimitive.List
            aria-label={`Views of ${address}`}
            className="flex shrink-0 [scrollbar-width:none] gap-1 overflow-x-auto border-b border-hairline [&::-webkit-scrollbar]:hidden"
          >
            <TabsPrimitive.Trigger value="live" className={tabClasses(tab === "live", "h-10")}>
              Live
            </TabsPrimitive.Trigger>
            <TabsPrimitive.Trigger value="past" className={tabClasses(tab === "past", "h-10")}>
              A past hour
            </TabsPrimitive.Trigger>
          </TabsPrimitive.List>
          <TabsContent value="live" className="min-h-0 flex-1 pt-6 xl:overflow-y-auto">
            {tab === "live" && (
              <AddressLive
                address={address}
                peer={peer}
                listed={list !== undefined}
                detail={detail}
                firewall={firewall}
                block={block}
                range={range}
              />
            )}
          </TabsContent>
          <TabsContent value="past" className="min-h-0 flex-1 pt-6 xl:overflow-y-auto">
            {tab === "past" && (
              <AddressPast address={address} hours={hours} hour={hour} onHour={onHour} />
            )}
          </TabsContent>
        </Tabs>
      ) : (
        // Without the detail's capability the summary is all there is of the
        // address: its picture, and no views to choose between.
        <AddressPicture
          address={address}
          peer={peer}
          listed={list !== undefined}
          block={block}
          range={range}
        />
      )}

      {blocking && (
        <BlockDialog
          address={address}
          context={blockContext(peer)}
          onClose={() => setBlocking(false)}
          onBlocked={() => {
            blocks.refresh()
            refreshList()
          }}
        />
      )}
      {dialog}
    </div>
  )
}

function oldestSocket(detail: ConnectionDetail | undefined) {
  return (detail?.sockets ?? []).reduce<string | undefined>(
    (min, s) => (s.firstSeen && (!min || s.firstSeen < min) ? s.firstSeen : min),
    undefined,
  )
}

/**
 * The line the page opens on: the address on its network (Tailscale's mark
 * for the tailnet), and after it what it holds now — its sockets, the
 * established ones and how long the oldest has been seen — with its standing
 * beside the name: blocked with when the block ends, connected, or gone from
 * the table. What it reached is the picture under the line.
 */
function AddressIdentity({
  address,
  peer,
  listed,
  block,
  oldest,
  command,
  menu,
}: {
  address: string
  peer?: Peer
  listed: boolean
  block?: AddressBlock
  /** When the oldest live socket was first seen, for an administrator. */
  oldest?: string
  command?: React.ReactNode
  menu: React.ReactNode
}) {
  const now = useNow(30_000)
  const network = networkOf(address)
  return (
    <HostIdentity
      className="animate-rise"
      logo={
        <AddressMark
          address={address}
          className="size-10 rounded-lg [&_img]:size-6 [&_svg]:size-5"
        />
      }
      // An IPv6 address is longer than a phone's line: it breaks rather than
      // run under the command beside it.
      title={<span className="min-w-0 font-mono break-all">{address}</span>}
      facts={
        <>
          <span>{network.label}</span>
          {(peer || !listed) && <FactDot />}
          {peer ? (
            <>
              <span className="numeric">
                {plural(peer.count, "socket")}, {peer.established} active
              </span>
              {oldest && (
                <>
                  <FactDot />
                  <span className="numeric" title={new Date(oldest).toLocaleString()}>
                    seen for {duration(Math.max(0, (now - Date.parse(oldest)) / 1000))}
                  </span>
                </>
              )}
            </>
          ) : (
            !listed && <span>reading…</span>
          )}
        </>
      }
      command={command}
      menu={menu}
      state={
        block ? (
          <Status tone="danger" label={`blocked · ${blockStanding(block, now).toLowerCase()}`} />
        ) : peer ? (
          <Status state="running" label="connected" />
        ) : listed ? (
          <Status state="stopped" label="not connected" />
        ) : (
          <Status tone="unknown" label="reading" />
        )
      }
    />
  )
}
