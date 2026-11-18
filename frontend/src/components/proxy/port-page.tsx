"use client"

import { useCallback, useMemo } from "react"
import Link from "next/link"
import { useParams, useRouter } from "next/navigation"
import { ArrowUpRight, Router } from "@/components/icons"
import { get } from "@/lib/api"
import { plural, timestamp } from "@/lib/format"
import type { PortsFirewall, PortsMeta, SeenListener } from "@/lib/types"
import { useSessionState, useViewState } from "@/lib/view-state"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { useConfirm } from "@/components/confirm-dialog"
import { DetailPage, siblingSteps, useDetailTab, type Step } from "@/components/detail-page"
import { Fact } from "@/components/fact-line"
import { IdentityHeader, IdentityVerbs } from "@/components/identity-header"
import { Rail, RailLine, RailName, RailRow } from "@/components/rail"
import { EmptyState, ErrorState } from "@/components/state"
import { LoadingRows } from "@/components/loading"
import { StatusDot } from "@/components/status-dot"
import { ViewTabList } from "@/components/tabs"
import { VERDICT_TONE } from "@/components/tone"
import { Tag } from "@/components/tag"
import { VerbMenu, type Verb } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import { Tabs, TabsContent } from "@/components/ui/tabs"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { processHref } from "@/components/procs/process-facts"
import {
  findPort,
  foldDualStack,
  ownerLabel,
  ownerLinks,
  ownerTitle,
  parsePortKey,
  portHref,
  PORTS_VIEW_KEYS,
  socketClients,
  dangerousService,
  networkWords,
  reachVerdict,
  socketPids,
  type Socket,
} from "@/components/proxy/ports"
import {
  DEFAULT_SORT,
  parseProtoFilter,
  parseQuery,
  parseReachFilter,
  parseSort,
  sortSockets,
  visibleSockets,
} from "@/components/proxy/ports-list"
import { firstSeen, isNew } from "@/components/proxy/ports-history"
import { NewMark, PortChanges } from "@/components/proxy/port-changes"
import { addressLine, ProcessMark, ReachWord } from "@/components/proxy/port-marks"
import { proxyWords } from "@/components/proxy/ports-proxy"
import { PortClients, PortOwner, PortReach } from "@/components/proxy/port-views"
import { PortPath } from "@/components/proxy/port-path"
import { socketMenuLabel, useSocketVerbs } from "@/components/proxy/port-verbs"
import { useProxy } from "@/components/proxy/proxy-context"
import type { Reading } from "@/components/proxy/freshness"
import { decodeSegment } from "@/lib/route-params"

const TABS = ["reach", "owner", "clients", "history"] as const
type Tab = (typeof TABS)[number]

const BACK = { href: "/proxy/ports", label: "Ports" }

/**
 * One listening port, as a place of its own (`/proxy/ports/tcp-443`).
 *
 * It was a side sheet over the list until 0.8.7: the owner, the reach, the
 * firewall, the clients, the proof from outside and the identify form in a
 * sheet's width, over a list nobody could read behind it, and no history at
 * all. So a port is a destination on the detail frame every item page shares
 * (`DetailPage`): the way back with the arrows, the identity line — the
 * owner as its product, the port and who holds it, where it answers as the
 * state, the socket's verbs at its end (`IdentityVerbs`) — and its views as
 * the underlined strip under it: Reach (the path to it drawn, then why),
 * Owner, Clients, History.
 *
 * What the sheet did well, the list beside the thing being read, is the rail
 * from `xl`: the list's own search, reach, protocol and sort, so the next
 * port is one press away, Alt+↑ and Alt+↓ walk it, and the view on screen
 * goes with you. On a phone the page is one pane that scrolls as a whole.
 *
 * A port that stops listening keeps its page: it says so, and its history
 * says when.
 */
export function PortPage() {
  const params = useParams<{ port: string }>()
  const router = useRouter()
  const key = useMemo(() => parsePortKey(decodeSegment(params.port)), [params.port])
  const [tab, setTab] = useDetailTab(TABS, { remember: "proxy.port.tab" })

  // The section's own read of the list, which the list page draws from too.
  const shared = useProxy().reads.ports
  const reading = shared.data as Reading<SeenListener[]> | undefined
  const all = useMemo(() => foldDualStack(reading?.value ?? []), [reading])
  const socket = key ? findPort(all, key) : undefined
  const rail = useRail(all)
  const hrefFor = useCallback((next: Socket) => portHref(next, all, tab), [all, tab])
  const steps = siblingSteps(rail.rows, socket, (next) => router.push(hrefFor(next)))

  return (
    <PortMain
      key={key ? `${key.protocol}-${key.port}-${key.address ?? ""}` : "none"}
      portKey={key}
      socket={socket}
      loading={!reading}
      error={reading ? undefined : shared.error}
      listedAt={reading?.at ?? 0}
      onRetry={shared.refresh}
      tab={tab}
      setTab={setTab}
      steps={steps}
      rail={
        <PortRail
          rows={rail.rows}
          all={all}
          current={socket}
          loading={!reading}
          error={reading ? undefined : shared.error}
          onRetry={shared.refresh}
          query={rail.query}
          onQuery={rail.setQuery}
          filtered={rail.filtered}
          onClearFilters={rail.clearFilters}
          hrefFor={hrefFor}
        />
      }
    />
  )
}

/**
 * The list's rows, read for the rail: the same search, reach, protocol and
 * sort the list was left on (they are its session's), over the same reading.
 */
function useRail(all: Socket[]) {
  const [query, setQuery] = useSessionState(PORTS_VIEW_KEYS.query, "")
  const [reachValue, setReach] = useSessionState<string>(PORTS_VIEW_KEYS.reach, "all")
  const [protoValue, setProto] = useSessionState<string>(PORTS_VIEW_KEYS.proto, "all")
  const [sortValue] = useSessionState(PORTS_VIEW_KEYS.sort, "")
  const [hideEphemeral] = useViewState(PORTS_VIEW_KEYS.hideEphemeral, false)
  const meta = usePoll((signal) => get<PortsMeta>("/ports/meta", undefined, signal), 0, [], {
    enabled: hideEphemeral,
  })
  const reach = parseReachFilter(reachValue) ?? "all"
  const proto = parseProtoFilter(protoValue) ?? "all"
  const sort = parseSort(sortValue) ?? DEFAULT_SORT
  const range = hideEphemeral ? (meta.data?.ephemeralRange ?? null) : null
  const rows = useMemo(
    () =>
      sortSockets(
        visibleSockets(all, { terms: parseQuery(query), reach, proto, hide: range }),
        sort,
      ),
    // `sort` is rebuilt each render from its string.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [all, query, reach, proto, range, sortValue],
  )
  return {
    rows,
    query,
    setQuery,
    filtered: reach !== "all" || proto !== "all",
    clearFilters: () => {
      setReach("all")
      setProto("all")
    },
  }
}

/**
 * The socket's verbs on its identity line, by the one rule (`IdentityVerbs`):
 * the first inline verb (the owner's page, or the process) as the command and
 * the rest behind `⋯`, named for the endpoint as a row's menu is. A socket
 * with no inline verb (a reader's) keeps only the menu.
 */
function lineVerbs(socket: Socket, verbs: Verb[]) {
  const name = socketMenuLabel(socket).replace(/^Actions for /, "")
  if (!verbs.some((verb) => verb.inline))
    return {
      command: undefined,
      menu: verbs.length > 0 && <VerbMenu verbs={verbs} label={`More actions for ${name}`} />,
    }
  return IdentityVerbs({ verbs, name })
}

function PortMain({
  portKey,
  socket,
  loading,
  error,
  listedAt,
  onRetry,
  tab,
  setTab,
  steps,
  rail,
}: {
  portKey: ReturnType<typeof parsePortKey>
  socket: Socket | undefined
  loading: boolean
  error: Error | undefined
  listedAt: number
  onRetry: () => void
  tab: Tab
  setTab: (tab: string) => void
  steps: { previous: Step; next: Step }
  rail: React.ReactNode
}) {
  const { can } = useAuth()
  const admin = can("system.admin")
  const { confirm, dialog } = useConfirm()
  const proxy = useProxy()
  const firewall = usePoll(
    (signal) => get<PortsFirewall>("/ports/firewall", undefined, signal),
    15_000,
    [],
    { enabled: admin },
  )
  const verbsFor = useSocketVerbs({
    firewall: firewall.data,
    confirm,
    onDone: proxy.reads.ports.refresh,
  })
  const name = portKey ? `${portKey.port}/${portKey.protocol}` : "Port"

  // The frame every detail page has (D9): the way back with the arrows, the
  // rail from `xl`, and the identity line held still over the views.
  const frame = (title: string, identity: React.ReactNode, body: React.ReactNode) => (
    <DetailPage
      name="Port"
      what="port"
      stateKey="proxy.port.page"
      refresh={onRetry}
      steps={steps}
      rail={rail}
      back={BACK}
      title={title}
      identity={identity}
    >
      {body}
      {dialog}
    </DetailPage>
  )

  if (!portKey) {
    return frame(
      "Port",
      undefined,
      <EmptyState
        icon={Router}
        title="That is not a port"
        description="A port's page is named by its protocol and number, such as tcp-443."
        action={
          <Button size="sm" variant="outline" asChild>
            <Link href="/proxy/ports">All ports</Link>
          </Button>
        }
      />,
    )
  }

  if (!socket) {
    // While the list is read the line keeps a place for its state and its
    // verbs, as the loaded line has them, so it is as tall as that line.
    return frame(
      name,
      <IdentityHeader
        loading={loading}
        className="border-b-0 pb-0"
        fallback={Router}
        title={<PortTitle port={portKey.port} protocol={portKey.protocol} />}
        state={
          loading ? (
            true
          ) : error ? (
            <span className="text-xs text-destructive">not read</span>
          ) : (
            <span className="text-xs font-medium">not listening</span>
          )
        }
        command={loading ? true : undefined}
        menu={loading ? false : undefined}
        facts={
          loading ? (
            <TextShimmer>Reading the sockets…</TextShimmer>
          ) : error ? (
            <Fact>couldn&apos;t read the sockets</Fact>
          ) : (
            <Fact mono={Boolean(portKey.address)}>{portKey.address ?? "any address"}</Fact>
          )
        }
      />,
      loading ? (
        <LoadingRows />
      ) : error ? (
        <ErrorState error={error} onRetry={onRetry} />
      ) : (
        <>
          <EmptyState
            icon={Router}
            title="Nothing listens there now"
            description={`No socket in the last listing is ${portKey.protocol.toUpperCase()} port ${portKey.port}${portKey.address ? ` on ${portKey.address}` : ""}. Its changes are below.`}
            action={
              <Button size="sm" variant="outline" asChild>
                <Link href="/proxy/ports">All ports</Link>
              </Button>
            }
          />
          <PortChanges port={portKey} />
        </>
      ),
    )
  }

  const clients = socketClients(socket)
  const views: { key: Tab; label: string; count?: number }[] = [
    { key: "reach", label: "Reach" },
    { key: "owner", label: "Owner" },
    { key: "clients", label: "Clients", count: clients?.count },
    { key: "history", label: "History" },
  ]

  return frame(
    `${name} ${ownerTitle(socket)}`,
    <PortIdentity socket={socket} listedAt={listedAt} {...lineVerbs(socket, verbsFor(socket))} />,
    <Tabs value={tab} onValueChange={setTab} className="gap-0">
      <ViewTabList label={`Views of port ${socket.port}`} value={tab} views={views} sticky />

      <TabsContent value="reach" className="space-y-8 pt-6">
        {tab === "reach" && (
          <>
            <PortPath socket={socket} />
            <PortReach socket={socket} admin={admin} />
          </>
        )}
      </TabsContent>
      <TabsContent value="owner" className="pt-6">
        {tab === "owner" && <PortOwner socket={socket} />}
      </TabsContent>
      <TabsContent value="clients" className="pt-6">
        {tab === "clients" && <PortClients socket={socket} />}
      </TabsContent>
      <TabsContent value="history" className="pt-6">
        {tab === "history" && <PortChanges port={socket} />}
      </TabsContent>
    </Tabs>,
  )
}

function PortTitle({ port, protocol }: { port: number; protocol: string }) {
  return (
    <span className="inline-flex min-w-0 items-baseline gap-2">
      <span className="numeric font-mono">{port}</span>
      <span className="text-body font-normal text-muted-foreground uppercase">{protocol}</span>
    </span>
  )
}

/**
 * The line the page opens on: the owner as its product, the port and who
 * holds it with where it answers beside it, coloured as the list colours it,
 * the socket's verbs at the end, then the facts a port is opened to check —
 * where it is bound, the account and process, how the owner runs (a link to
 * its page), what the proxy does with it and when it was first seen.
 */
function PortIdentity({
  socket,
  listedAt,
  command,
  menu,
}: {
  socket: Socket
  listedAt: number
  command?: React.ReactNode
  menu?: React.ReactNode
}) {
  const pids = socketPids(socket)
  const label = ownerLabel(socket)
  const link = ownerLinks(socket)[0]
  const proxy = proxyWords(socket)
  const seen = firstSeen(socket)
  return (
    <IdentityHeader
      className="border-b-0 pb-0"
      logo={<ProcessMark listener={socket} className="size-10 rounded-lg [&_img]:size-6" />}
      title={
        <span className="inline-flex min-w-0 items-baseline gap-2">
          <PortTitle port={socket.port} protocol={socket.protocol} />
          <span className="truncate">{ownerTitle(socket)}</span>
          {socket.self && <Tag>This dashboard</Tag>}
          <NewMark socket={socket} now={listedAt} />
        </span>
      }
      state={<ReachWord socket={socket} className="max-w-full" />}
      command={command}
      menu={menu}
      facts={
        <>
          <Fact mono>{addressLine(socket)}</Fact>
          {socket.source !== "docker-nat" && <Fact mono>{socket.user || "unknown user"}</Fact>}
          {socket.source !== "docker-nat" && pids.length > 0 && (
            <Fact className="numeric">
              {pids.length > 1 ? "PIDs " : "PID "}
              {pids.map((pid, i) => (
                <span key={pid}>
                  {i > 0 && ", "}
                  <Link
                    href={processHref(pid)}
                    className="rounded-sm text-foreground focus-ring hover:underline"
                  >
                    {pid}
                  </Link>
                </span>
              ))}
            </Fact>
          )}
          {label && (
            <Fact>
              {link ? (
                <Link
                  href={link.href}
                  className="inline-flex min-w-0 items-center gap-1 rounded-sm text-foreground focus-ring hover:underline"
                >
                  <span className="truncate">{label}</span>
                  <ArrowUpRight aria-hidden className="size-3 shrink-0" />
                </Link>
              ) : (
                label
              )}
            </Fact>
          )}
          {proxy && <Fact>{proxy}</Fact>}
          {seen && (
            <Fact title={timestamp(seen)}>
              {isNew(socket, listedAt) ? "first seen today" : `listening since ${timestamp(seen)}`}
            </Fact>
          )}
        </>
      }
    />
  )
}

/**
 * The list, folded into a column beside the port it opened (`Rail`): the
 * same search, reach, protocol and sort, so the next port is one press away.
 * Each row is the owner as its product with where it answers as the dot in
 * its corner, the port, protocol and owner, and where it answers in words.
 * The words keep the muted ink on every row: in the danger ink they failed
 * contrast on the filled row (PGP-20), and the dot already carries the tone.
 */
function PortRail({
  rows,
  all,
  current,
  loading,
  error,
  onRetry,
  query,
  onQuery,
  filtered,
  onClearFilters,
  hrefFor,
}: {
  rows: Socket[]
  all: Socket[]
  current: Socket | undefined
  loading: boolean
  error?: Error
  onRetry?: () => void
  query: string
  onQuery: (value: string) => void
  filtered: boolean
  onClearFilters: () => void
  hrefFor: (socket: Socket) => string
}) {
  const caption =
    rows.length === all.length
      ? plural(all.length, "socket")
      : `${rows.length} of ${plural(all.length, "socket")}`
  return (
    <Rail
      slot="port-rail"
      label="Ports"
      search={{
        label: "Search the ports",
        placeholder: "Port, process, user or address",
        query,
        onQuery,
      }}
      caption={loading ? "Reading the sockets…" : error ? undefined : caption}
      rows={rows}
      keyOf={railKey}
      current={(socket) => socket === current}
      pinned={current}
      row={(socket, on) => <PortRailRow socket={socket} current={on} href={hrefFor(socket)} />}
      loading={loading}
      loadingRows={8}
      error={error}
      onRetry={onRetry}
      empty="No socket matches."
      filtered={filtered}
      onClearFilters={onClearFilters}
    />
  )
}

function railKey(socket: Socket) {
  return `${socket.protocol}-${socket.family}-${socket.address}-${socket.port}`
}

function PortRailRow({
  socket,
  current,
  href,
}: {
  socket: Socket
  current: boolean
  href: string
}) {
  const verdict = reachVerdict(socket)
  const service = dangerousService(socket)
  return (
    <RailRow
      item={`${socket.protocol}-${socket.address}-${socket.port}`}
      name={`${socket.port} ${ownerTitle(socket)}`}
      label={`${socket.port}/${socket.protocol}, ${ownerTitle(socket)}`}
      current={current}
      href={href}
      mark={<ProcessMark listener={socket} className="size-7 rounded-md [&_img]:size-4" />}
      dot={verdict ? <StatusDot tone={VERDICT_TONE[verdict]} className="size-1.5" /> : null}
    >
      <RailName current={current}>
        <span className="numeric font-mono">{socket.port}</span>{" "}
        <span className="text-micro font-normal text-muted-foreground uppercase">
          {socket.protocol}
        </span>{" "}
        {ownerTitle(socket)}
      </RailName>
      <RailLine>
        {service ? `${service} · ` : ""}
        {networkWords(socket)}
      </RailLine>
    </RailRow>
  )
}
