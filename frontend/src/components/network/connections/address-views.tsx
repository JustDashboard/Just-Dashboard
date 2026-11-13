"use client"

import { useMemo } from "react"
import { ClockRewind, FirewallCheck, Route, Servers, type Icon } from "@/components/icons"
import { get } from "@/lib/api"
import { networkOf } from "@/lib/clients"
import { bytes, duration, plural, relativeTime } from "@/lib/format"
import type { FirewallStatus, NetworkPath, Peer, PortRange } from "@/lib/types"
import { cn } from "@/lib/utils"
import { flowBytes, flowOwnerName, type FlowReport } from "@/lib/network-flows"
import {
  foldHistoricalFlows,
  hourBounds,
  millis,
  sourceCovers,
  type AddressBlock,
  type ConnectionDetail,
} from "@/lib/network-traffic"
import { usePoll, type PollState } from "@/hooks/use-poll"
import { NETWORK_GLYPH } from "@/components/client-mark"
import { WireHost, WireMark } from "@/components/deploy/wire"
import { useNow } from "@/components/deploy/vocabulary"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import {
  ProductGlyph,
  ProductLogo,
  hasProductLogo,
  processProduct,
} from "@/components/product-logo"
import { Status } from "@/components/status-dot"
import { EmptyNote, ErrorState, LoadingRows, Notice } from "@/components/state"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { toolHref } from "@/components/security/address-verbs"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { WireFlow, type WireLine } from "@/components/wire-flow"
import {
  beat,
  reached,
  socketState,
  stateWords,
  transportWords,
} from "@/components/network/connections/model"
import { HistoryOff } from "@/components/network/connections/recorded"
import { Carried, PortLinks } from "@/components/network/connections/rows"

const endpoint = (addr: string, port: number) =>
  addr.includes(":") ? `[${addr}]:${port}` : `${addr}:${port}`

/**
 * One address and what it holds, as a picture: the address on its network,
 * this server with the ports it was reached on, and the programs holding its
 * sockets. It is what the summary folded of the address — the transports and
 * the far end's ports on the address, the states of its sockets under the
 * ports — which the list's row once carried as a third and fourth line. The
 * wire moves while a connection is established, is red and still for a
 * blocked address, and dashed for one that holds no socket now.
 *
 * Every role reads this; it is all a role without the detail's capability
 * has of the address.
 */
export function AddressPicture({
  address,
  peer,
  listed,
  block,
  range,
}: {
  address: string
  peer?: Peer
  /** The list has been read, so a missing peer is an address that is not connected. */
  listed: boolean
  block?: AddressBlock
  range?: PortRange | null
}) {
  const network = networkOf(address)
  const Glyph = NETWORK_GLYPH[network.kind]
  const line: WireLine = block
    ? { state: "danger" }
    : peer
      ? { state: "up", moving: peer.established > 0, duration: beat(peer.established) }
      : { state: "idle" }
  const programs = peer ? (peer.processes.length > 0 ? peer.processes : [reached(peer)]) : []
  const states = peer && stateWords(peer)
  const transports = peer && transportWords(peer)
  return (
    <section aria-label="What it reached" className="min-w-0">
      <WireFlow
        // The hub's cell keeps room for the words that hang under its mark,
        // and the same room above it is empty: the picture gives it back.
        className="animate-rise lg:-my-6"
        loading={!listed}
        // A narrow hub column: its words hang centred under the mark and may
        // run past it, while the address beside the rail needs its line whole.
        hubShare={0.3}
        start={{
          title: "The address",
          loadingRows: 1,
          nodes: [
            {
              id: "address",
              mark: (
                <WireMark tone={block ? "danger" : "logo"} shape="square" size="md">
                  {network.product ? <ProductGlyph id={network.product} /> : <Glyph aria-hidden />}
                </WireMark>
              ),
              // The transports where the summary kept them; the network is
              // on the line above the picture, and stands in where it did not.
              eyebrow: transports?.protocols ?? network.label,
              title: <span className="font-mono break-all">{address}</span>,
              name: address,
              hint: transports?.ports && <span className="numeric">{transports.ports}</span>,
              line,
            },
          ],
        }}
        hub={{
          mark: <WireHost />,
          eyebrow: "This server",
          title: !peer ? (
            "Not connected"
          ) : peer.ports.length > 0 ? (
            <span className="font-mono">
              <PortLinks ports={peer.ports} range={range} colon />
            </span>
          ) : (
            plural(peer.count, "socket")
          ),
          hint: peer && (
            <>
              {peer.service && <span className="block">{peer.service}</span>}
              {states && <span className="numeric block">{states}</span>}
            </>
          ),
          // The hub's words hang under its mark, and one address is a single
          // row: the cell keeps the room they need.
          className: "lg:py-14",
        }}
        end={{
          title: "What it reached",
          loadingRows: 1,
          empty: "Nothing reached now",
          nodes: programs.map((name) => {
            const product = processProduct(name)
            return {
              id: `program:${name}`,
              mark: (
                <WireMark tone="logo" shape="square" size="md">
                  {hasProductLogo(product) ? (
                    <ProductGlyph id={product} />
                  ) : (
                    <Servers aria-hidden />
                  )}
                </WireMark>
              ),
              title: name,
              name,
              line,
            }
          }),
        }}
      />
    </section>
  )
}

/**
 * An address's live view, for an administrator: the picture of what it
 * reached, then every live tuple with how long it has been seen, its byte
 * counters and TCP's round trip; the tuples seen closing in the last fifteen
 * minutes; and the layers it crosses — what the firewall says about it, the
 * route this server answers it by, what the socket history kept of it in the
 * last day — each opening the page that owns it. Everything is read, nothing
 * probes the address.
 *
 * It was the peer sheet over the table until 0.8.7.
 */
export function AddressLive({
  address,
  peer,
  listed,
  detail,
  firewall,
  block,
  range,
}: {
  address: string
  peer?: Peer
  listed: boolean
  detail: PollState<ConnectionDetail>
  firewall: FirewallStatus | undefined
  block?: AddressBlock
  range?: PortRange | null
}) {
  const now = useNow(1000)
  const d = detail.data
  const tcp = (d?.sockets ?? []).filter((s) => s.rxBytes !== undefined)
  const rx = tcp.reduce((n, s) => n + (s.rxBytes ?? 0), 0)
  const tx = tcp.reduce((n, s) => n + (s.txBytes ?? 0), 0)

  return (
    <div className="flex min-w-0 flex-col gap-8 pb-6">
      <AddressPicture address={address} peer={peer} listed={listed} block={block} range={range} />

      {!d && detail.error ? (
        <ErrorState error={detail.error} onRetry={detail.refresh} retryable />
      ) : (
        <>
          {d && (
            <NetworkReadWarning
              error={detail.error}
              refresh={detail.refresh}
              lastSuccess={detail.lastSuccess}
              reading="connection detail"
            />
          )}
          {d?.countersError && (
            <Notice title="TCP counters could not be read">{d.countersError}</Notice>
          )}

          <Panel plain aria-label="Live sockets">
            <PanelHeader
              title="Live sockets"
              actions={
                d && (
                  <span className="flex items-center gap-3 text-hint text-muted-foreground">
                    {/* What TCP carried over every socket of the address. */}
                    {tcp.length > 0 && <Carried rx={bytes(rx)} tx={bytes(tx)} />}
                    <span className="numeric">{d.sockets.length}</span>
                  </span>
                )
              }
            />
            <PanelBody flush>
              {!d ? (
                <LoadingRows rows={2} />
              ) : d.sockets.length === 0 ? (
                <Quiet>No live socket at this read.</Quiet>
              ) : (
                <SocketList aria-label={`Connections with ${address}`}>
                  {d.sockets.map((s) => (
                    <SocketLine
                      key={`${s.protocol}-${s.localAddress}-${s.localPort}-${s.remotePort}`}
                      tuple={`${s.protocol.toUpperCase()} ${endpoint(s.localAddress, s.localPort)} ↔ ${s.remotePort}`}
                      words={[
                        s.process || "owner unknown",
                        s.firstSeen && `seen ≥ ${duration((now - Date.parse(s.firstSeen)) / 1000)}`,
                        s.rttMs !== undefined && `round trip ${millis(s.rttMs)}`,
                      ]}
                      state={s.status}
                      carried={
                        s.rxBytes === undefined ? (
                          <span className="text-hint text-muted-foreground">
                            {s.protocol === "udp" ? "no counters" : "—"}
                          </span>
                        ) : (
                          <Carried rx={bytes(s.rxBytes)} tx={bytes(s.txBytes)} />
                        )
                      }
                    />
                  ))}
                </SocketList>
              )}
            </PanelBody>
          </Panel>

          <Panel plain aria-label="Closed in the last 15 minutes">
            <PanelHeader
              title="Closed in the last 15 minutes"
              actions={
                d && (
                  <span className="numeric text-hint text-muted-foreground">{d.closed.length}</span>
                )
              }
            />
            <PanelBody flush>
              {!d ? (
                <LoadingRows rows={1} />
              ) : d.closed.length === 0 ? (
                <Quiet>Nothing was seen closing.</Quiet>
              ) : (
                <SocketList aria-label={`Closed connections with ${address}`}>
                  {d.closed.slice(0, 20).map((c) => (
                    <SocketLine
                      key={`${c.protocol}-${c.localPort}-${c.remotePort}-${c.goneBy}`}
                      tuple={`${c.protocol.toUpperCase()} ${endpoint(c.localAddress, c.localPort)} ↔ ${c.remotePort}`}
                      words={[
                        c.process || "owner unknown",
                        `open at least ${duration(c.observedSeconds)}`,
                        `closed between ${new Date(c.lastSeen).toLocaleTimeString()} and ${new Date(c.goneBy).toLocaleTimeString()}`,
                      ]}
                    />
                  ))}
                </SocketList>
              )}
            </PanelBody>
          </Panel>
        </>
      )}

      <Layers address={address} firewall={firewall} block={block} />

      {d &&
        (d.quality.intervalSeconds > 0 || d.countersTruncated || d.quality.limits.length > 0) && (
          <ul
            className="list-disc space-y-1 pl-5 text-hint text-muted-foreground"
            aria-label="What this read cannot see"
          >
            {d.quality.intervalSeconds > 0 && (
              <li>
                The table was read {Math.round(d.quality.intervalSeconds)}s after the one before; a
                connection shorter than that may never have been in it.
              </li>
            )}
            {d.countersTruncated && <li>The host had more sockets than are read for counters.</li>}
            {d.quality.limits.map((l) => (
              <li key={l}>{l}</li>
            ))}
          </ul>
        )}
    </div>
  )
}

function SocketList({ className, ...props }: React.ComponentProps<"ul">) {
  return <ul className={cn("min-w-0 divide-y divide-hairline", className)} {...props} />
}

/** A section's one sentence when it holds nothing, on the section's own edge. */
function Quiet({ children }: { children: React.ReactNode }) {
  return <p className="py-3 text-body text-muted-foreground">{children}</p>
}

/**
 * One socket on one line, live, closed or recorded: its two ends in mono over
 * who holds it and how long it has been seen, and at the line's end its state
 * and what it carried, each in a column of its own. A table of eight columns
 * stood here, cut off beside the rail and on a phone; down a phone the state
 * and the counters take a line under the words rather than a column the
 * screen has not got.
 */
function SocketLine({
  tuple,
  words,
  state,
  carried,
}: {
  tuple: string
  words: (string | false | undefined)[]
  /** The kernel's state for a live socket. */
  state?: string
  carried?: React.ReactNode
}) {
  const said = words.filter(Boolean).join(" · ")
  return (
    <li className="flex min-w-0 flex-wrap items-center gap-x-5 gap-y-1 py-3 sm:flex-nowrap">
      <span className="min-w-0 basis-full sm:flex-1 sm:basis-0">
        <span className="block truncate font-mono text-body font-medium" title={tuple}>
          {tuple}
        </span>
        <span className="block truncate text-hint text-muted-foreground" title={said}>
          {said}
        </span>
      </span>
      {state !== undefined && (
        <Status
          className="shrink-0 sm:w-28"
          tone={state === "ESTABLISHED" ? "running" : "unknown"}
          label={socketState(state)}
        />
      )}
      {carried && <span className="shrink-0 text-right sm:w-44">{carried}</span>}
    </li>
  )
}

function LayerMark({ icon }: { icon: Icon }) {
  return <ProductLogo id={undefined} fallback={icon} size="sm" />
}

/** A layer's sentence: whole on a phone, where one line cannot hold it. */
function Said({ children }: { children: React.ReactNode }) {
  return <span className="max-sm:whitespace-normal">{children}</span>
}

/**
 * The layers the address crosses, each a card that opens the page that owns
 * it: the firewall's word on it, the route this server answers it by, and
 * what socket history kept of it in the last day.
 */
function Layers({
  address,
  firewall,
  block,
}: {
  address: string
  firewall: FirewallStatus | undefined
  block?: AddressBlock
}) {
  const route = usePoll<NetworkPath>(
    (signal) => get("/network/routing/lookup", { target: address }, signal),
    0,
    [address],
  )
  const history = usePoll<FlowReport>(
    (signal) => {
      const t = Math.floor(Date.now() / 1000)
      const bounds = hourBounds(t - 86400, t)
      return get(
        "/network/flows/",
        { address, from: bounds.from, to: bounds.to, limit: 500 },
        signal,
      )
    },
    60_000,
    [address],
  )
  // Rules for any source cover every address; only the ones that name this
  // address or its network are about it.
  const covering = (firewall?.rules ?? []).filter((r) => sourceCovers(r.from, address))
  const rules = covering
    .filter((r) => !sourceCovers(r.from, "0.0.0.0") && !sourceCovers(r.from, "::"))
    .sort((a, b) => Number(/deny|reject/i.test(b.action)) - Number(/deny|reject/i.test(a.action)))
  const general = covering.length - rules.length
  const retained = useMemo(
    () =>
      foldHistoricalFlows(history.data?.rows ?? [], (row) => flowOwnerName(row.socket.owner)).find(
        (p) => p.address === address,
      ),
    [history.data?.rows, address],
  )
  return (
    <Panel plain aria-label="Across the layers">
      <PanelHeader title="Across the layers" />
      <PanelBody flush className="pt-3">
        <ChoiceList>
          <ChoiceRow
            leading={<LayerMark icon={FirewallCheck} />}
            title="Firewall"
            verb="Open the firewall"
            href="/network/firewall"
            description={
              <Said>
                {block
                  ? `Blocked: ${block.reason}`
                  : rules.length === 0
                    ? firewall
                      ? `No rule names this address; the default policy${general ? ` and ${plural(general, "rule")} for any source` : ""} decide${general ? "" : "s"}.`
                      : "The firewall could not be read."
                    : rules
                        .slice(0, 3)
                        .map(
                          (r) =>
                            `${r.action} ${r.direction ?? "in"} from ${r.from}${r.port ? ` to port ${r.port}` : ""}`,
                        )
                        .join(" · ")}
              </Said>
            }
            trailing={
              <Status
                tone={
                  block
                    ? "danger"
                    : rules.some((r) => /deny|reject/i.test(r.action))
                      ? "warning"
                      : "unknown"
                }
                label={block ? "blocked" : `${plural(rules.length, "rule")}`}
              />
            }
          />
          <ChoiceRow
            leading={<LayerMark icon={Route} />}
            title="Route"
            verb="Trace the route"
            href={toolHref("traceroute", address)}
            description={
              <Said>
                {route.data
                  ? route.data.local
                    ? "A local address of this server."
                    : `Answered through ${route.data.device ?? "?"}${route.data.gateway ? ` via ${route.data.gateway}` : ""}${route.data.source ? ` from ${route.data.source}` : ""}.`
                  : route.error
                    ? `The kernel could not be asked: ${route.error.message}`
                    : "Asking the kernel…"}
              </Said>
            }
          />
          <ChoiceRow
            leading={<LayerMark icon={ClockRewind} />}
            title="Socket history"
            verb="Open socket history"
            href="/network/flows"
            description={
              <Said>
                {history.error ? (
                  "Socket history is an administrator's and could not be read."
                ) : !history.data ? (
                  "Reading…"
                ) : !history.data.settings.enabled && history.data.rows.length === 0 ? (
                  "Socket history is off; nothing before this page is kept."
                ) : retained ? (
                  <>
                    {plural(retained.sockets, "socket row")} in the last day, first{" "}
                    {relativeTime(retained.firstSeen)} · {retained.owners.join(", ")}
                    {/* The column the counters stand in is not there on a phone. */}
                    <span className="sm:hidden">
                      {" "}
                      · TCP ↓ {flowBytes(retained.rxBytes)} ↑ {flowBytes(retained.txBytes)}
                    </span>
                  </>
                ) : (
                  "No retained socket with this address in the last day."
                )}
              </Said>
            }
            trailing={
              retained && (
                <Carried
                  rx={flowBytes(retained.rxBytes)}
                  tx={flowBytes(retained.txBytes)}
                  className="max-sm:hidden"
                />
              )
            }
          />
        </ChoiceList>
      </PanelBody>
    </Panel>
  )
}

/**
 * What socket history kept of the address in one hour: the rows it holds for
 * it, each a socket with its owner, its two ends, when it was first and last
 * seen and what TCP carried — the list's past hour, for one address. With
 * the history off there is no past to read, and it says where to turn it on.
 */
export function AddressPast({
  address,
  hours,
  hour,
  onHour,
}: {
  address: string
  hours: { from: string; to: string; label: string }[]
  hour: string
  onHour: (from: string) => void
}) {
  const chosen = hours.find((h) => h.from === hour) ?? hours[0]
  const report = usePoll<FlowReport>(
    (signal) =>
      get("/network/flows/", { address, from: chosen.from, to: chosen.to, limit: 500 }, signal),
    60_000,
    [address, chosen.from],
  )
  const rows = useMemo(
    () => (report.data?.rows ?? []).filter((row) => row.socket.remoteAddress === address),
    [report.data?.rows, address],
  )
  const folded = useMemo(
    () => foldHistoricalFlows(rows, (row) => flowOwnerName(row.socket.owner))[0],
    [rows],
  )
  const coverage = report.data?.coverageHours.find((c) =>
    c.hour.startsWith(chosen.from.slice(0, 13)),
  )
  const off = report.data && !report.data.settings.enabled && report.data.rows.length === 0
  return (
    <div aria-label="Recorded sockets" role="region" className="flex min-w-0 flex-col gap-3 pb-6">
      <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2">
        <Select value={chosen.from} onValueChange={onHour}>
          <SelectTrigger className="w-full font-mono sm:w-72" size="sm" aria-label="Which hour">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {hours.map((h) => (
              <SelectItem key={h.from} value={h.from} className="font-mono">
                {h.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        {report.data && !off && (
          <span className="flex items-center gap-3 text-hint text-muted-foreground">
            <span className="numeric">{plural(rows.length, "socket")}</span>
            {folded && <Carried rx={flowBytes(folded.rxBytes)} tx={flowBytes(folded.txBytes)} />}
          </span>
        )}
      </div>
      {report.data && (
        <NetworkReadWarning
          error={report.error}
          refresh={report.refresh}
          lastSuccess={report.lastSuccess}
          reading="socket history"
        />
      )}
      {!report.data ? (
        report.error ? (
          <ErrorState error={report.error} onRetry={report.refresh} retryable />
        ) : (
          <LoadingRows rows={3} />
        )
      ) : off ? (
        <HistoryOff />
      ) : rows.length === 0 ? (
        <EmptyNote className="border-t border-hairline">
          Socket history kept nothing of {address} in this hour.
        </EmptyNote>
      ) : (
        <SocketList
          aria-label={`Recorded sockets with ${address}`}
          className="border-t border-hairline"
        >
          {rows.map((row) => {
            const s = row.socket
            return (
              <SocketLine
                key={row.id}
                tuple={`${s.protocol.toUpperCase()} ${endpoint(s.localAddress, s.localPort)} ↔ ${s.remotePort ?? "—"}`}
                words={[
                  flowOwnerName(s.owner),
                  `seen ${relativeTime(row.firstSeen)} to ${relativeTime(row.lastSeen)}`,
                ]}
                carried={<Carried rx={flowBytes(row.rxBytes)} tx={flowBytes(row.txBytes)} />}
              />
            )
          })}
        </SocketList>
      )}
      {report.data && !off && (
        <p className="text-hint text-muted-foreground">
          {coverage
            ? `${plural(coverage.samples, "sample")} in this hour${coverage.failedSources ? `, ${plural(coverage.failedSources, "failed source")}` : ""}${coverage.discardedIntervals ? `, ${plural(coverage.discardedIntervals, "discarded interval")}` : ""}. `
            : "No sample was taken in this hour. "}
          {report.data.truncated && "More rows were kept than are shown. "}
          Sockets that opened and closed between two samples are not in the record; UDP has no byte
          counters.
        </p>
      )}
    </div>
  )
}
