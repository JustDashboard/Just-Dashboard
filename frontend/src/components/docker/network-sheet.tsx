"use client"

import { createRef, useMemo, useRef, useState, type RefObject } from "react"
import { Linked, Slash, Trash } from "@/components/icons"
import { del, get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { Detail, DetailList } from "@/components/page"
import { ErrorState, LoadingRows, Notice } from "@/components/state"
import { IconAction } from "@/components/icon-action"
import { SidePanel } from "@/components/side-panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { TileTrend } from "@/components/metrics/sparkline"
import { LiveBytes } from "@/components/overview/readings"
import { ProductGlyph, containerProduct, hasProductLogo } from "@/components/product-logo"
import { WireHost, WireMark, WireNode } from "@/components/deploy/wire"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Cidr } from "@/components/network/address"
import { LinkGlyph, pulseDuration } from "@/components/network/marks"
import { RX, TX } from "@/components/network/rate-pair"
import { calendarDate, plural, rate } from "@/lib/format"
import type { Container, DockerNetwork, NetworkDetail, NetworkMember } from "@/lib/types"
import { cn } from "@/lib/utils"
import type { ConfirmFn } from "@/components/docker/shared"
import { Hint } from "@/components/docker/explain"
import { AttachDialog } from "@/components/docker/network-dialogs"
import { HueKey, type NetworkRate } from "@/components/docker/network-band"
import type { ContainerTraffic } from "@/components/docker/network-members"
import {
  bareAddress,
  hostCapacity,
  isUnused,
  networkHue,
  networkOwner,
} from "@/components/docker/networks"

/** A wire moving less than this is drawn still, as on every Network picture. */
const MOVING = 1024

/**
 * One network as a live readout: what its bridge carries now and over the
 * last two minutes, who is on it and how much of its subnet they take, a
 * picture of the bridge wired to each member — the wire pulsing while that
 * container moves bytes — and then the members as a table of their addresses
 * and the names the others reach them by, and the network's settings.
 *
 * It was a label/value list, a tree of names and a list of rows. The table is
 * the part read most — "what does the API call the database?" — so it has the
 * columns a table has; the detach control is on its row.
 */
export function NetworkSheet({
  id,
  listed,
  rate: bridge,
  traffic,
  containers,
  confirm,
  onOpenChange,
  onChanged,
}: {
  id: string | null
  /** The network as the list has it, drawn while its inspect is on its way. */
  listed?: DockerNetwork
  rate?: NetworkRate
  traffic: Map<string, ContainerTraffic>
  containers: Map<string, Container>
  confirm: ConfirmFn
  onOpenChange: (open: boolean) => void
  onChanged: () => void
}) {
  const { can } = useAuth()
  const [attaching, setAttaching] = useState(false)
  const detail = usePoll<NetworkDetail>(
    (signal) =>
      get<NetworkDetail>(`/docker/networks/${encodeURIComponent(id ?? "")}`, undefined, signal),
    15_000,
    [id],
    { enabled: id !== null },
  )
  const data = detail.data?.id === id ? detail.data : undefined
  const network = data ?? listed

  const refresh = () => {
    detail.refresh()
    onChanged()
  }

  const disconnect = async (member: NetworkMember) => {
    try {
      await post(`/docker/networks/${id}/disconnect`, { container: member.id })
      notify.success(`${member.name} left ${network?.name}`)
      refresh()
    } catch (err) {
      notify.error("Could not detach it", err)
    }
  }

  const system = data?.system ?? false
  const canAttach = can("service.control") && !system && network?.driver !== "host"
  const removable = network !== undefined && can("destructive") && isUnused(network)

  return (
    <SidePanel
      open={id !== null}
      onOpenChange={onOpenChange}
      width="md"
      title={
        network ? (
          <span className="flex min-w-0 items-center gap-2">
            <HueKey color={networkHue(network.name)} />
            <span className="truncate">{network.name}</span>
          </span>
        ) : (
          "Network"
        )
      }
      description={network?.subnets.join(", ")}
      actions={
        network && (
          <>
            {canAttach &&
              (network.attachable ? (
                <Button size="xs" variant="outline" onClick={() => setAttaching(true)}>
                  <Linked className="size-3" />
                  Attach a container
                </Button>
              ) : (
                <Hint className="max-w-72">
                  Takes members only from its compose file — add the service there, then deploy.
                </Hint>
              ))}
            {removable && (
              <Button
                size="xs"
                variant="outline"
                className="text-destructive"
                onClick={() =>
                  confirm({
                    title: "Remove network",
                    confirmLabel: "Remove",
                    description: (
                      <p>
                        Removes <b>{network.name}</b> and returns its subnet to the pool. Nothing is
                        attached to it, so nothing loses a route.
                      </p>
                    ),
                    action: async (c) => {
                      await del(`/docker/networks/${network.id}`, { confirm: c })
                      onOpenChange(false)
                      onChanged()
                    },
                  })
                }
              >
                <Trash className="size-3" />
                Remove
              </Button>
            )}
          </>
        )
      }
    >
      {detail.error && !data && <ErrorState error={detail.error} />}
      {!network && detail.loading && <LoadingRows />}
      {network && (
        <div className="space-y-7">
          <Facts network={network} />
          <Readings network={network} detail={data} bridge={bridge} containers={containers} />
          {data && data.members.length > 0 && (
            <Picture network={data} traffic={traffic} containers={containers} />
          )}
          {data && (
            <Members
              network={data}
              containers={containers}
              traffic={traffic}
              canDetach={can("service.control") && !system}
              onDetach={disconnect}
            />
          )}
          {!data && detail.loading && <LoadingRows rows={3} />}
          {data && <Settings network={data} />}
        </div>
      )}

      <AttachDialog
        open={attaching}
        networkId={id}
        networkName={network?.name}
        onOpenChange={setAttaching}
        onAttached={refresh}
        attached={new Set((data?.members ?? []).map((m) => m.id))}
      />
    </SidePanel>
  )
}

/** Who made it and what it is, as the line under a name. */
function Facts({ network }: { network: DockerNetwork }) {
  const owner = networkOwner(network)
  return (
    <p className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
      <span>{owner.label}</span>
      <span className="text-muted-foreground/40">·</span>
      <span>{network.driver === "null" ? "no network" : `${network.driver} driver`}</span>
      {network.bridge && (
        <>
          <span className="text-muted-foreground/40">·</span>
          <span className="font-mono">{network.bridge}</span>
        </>
      )}
      {network.internal && <Tag>no internet</Tag>}
      {network.ipv6 && <Tag>IPv6</Tag>}
    </p>
  )
}

/**
 * Four readings: in and out on its bridge, each with its last two minutes,
 * how many of its members are running, and how much of its subnet they take.
 */
function Readings({
  network,
  detail,
  bridge,
  containers,
}: {
  network: DockerNetwork
  detail?: NetworkDetail
  bridge?: NetworkRate
  containers: Map<string, Container>
}) {
  const members = detail?.members.length ?? network.usedBy.length
  const running = detail
    ? detail.members.filter((m) => (containers.get(m.id)?.state ?? m.state) === "running").length
    : undefined
  const v4 = network.subnets.find((s) => !s.includes(":"))
  const capacity = v4 ? hostCapacity(v4) : 0
  const addressed = detail ? detail.members.filter((m) => m.ipv4).length : 0
  return (
    <StatGrid columns={2} dense className="-mx-4 border-y border-hairline">
      <StatTile
        label="Received"
        value={bridge ? <LiveBytes value={bridge.rx} suffix="/s" /> : "—"}
        trend={
          bridge && (
            <TileTrend
              values={bridge.points.map((p) => p.rx)}
              color={RX}
              label="Received over the last two minutes"
            />
          )
        }
        hint={!network.bridge ? "no bridge on this host to count" : undefined}
      />
      <StatTile
        label="Sent"
        value={bridge ? <LiveBytes value={bridge.tx} suffix="/s" /> : "—"}
        trend={
          bridge && (
            <TileTrend
              values={bridge.points.map((p) => p.tx)}
              color={TX}
              label="Sent over the last two minutes"
            />
          )
        }
        hint={!network.bridge ? "host, none and macvlan carry their own" : undefined}
      />
      <StatTile
        label="Members"
        value={members}
        tone={running !== undefined && running < members ? "warning" : "default"}
        hint={
          members === 0
            ? "nothing attached"
            : running === undefined
              ? plural(members, "container")
              : running === members
                ? "all running"
                : `${running} running`
        }
      />
      <StatTile
        label="Addresses"
        value={capacity > 0 ? addressed : "—"}
        trailing={capacity > 0 ? `of ${capacity.toLocaleString()}` : undefined}
        meter={
          capacity > 0 ? Math.max((addressed / capacity) * 100, addressed > 0 ? 2 : 0) : undefined
        }
        hint={v4 ? <Cidr cidr={v4} /> : "no IPv4 subnet"}
      />
    </StatGrid>
  )
}

/**
 * The network drawn as what it is: the bridge on the left with its gateway,
 * each member on the right as the product it runs, and a wire between them
 * that pulses while that container moves more than a kilobyte a second —
 * faster the busier — running toward the container for what it receives. A
 * stopped member's wire is dashed: it is on the network and holds no address.
 * Below `lg` the wires are not drawn and each member says its traffic in
 * words, as the Network topology does.
 */
function Picture({
  network,
  traffic,
  containers,
}: {
  network: NetworkDetail
  traffic: Map<string, ContainerTraffic>
  containers: Map<string, Container>
}) {
  const container = useRef<HTMLDivElement>(null)
  const hub = useRef<HTMLDivElement>(null)
  const ids = network.members.map((m) => m.id).join("\n")
  const refs = useMemo(() => {
    const map = new Map<string, RefObject<HTMLDivElement | null>>()
    for (const id of ids.split("\n")) if (id) map.set(id, createRef<HTMLDivElement>())
    return map
  }, [ids])
  const parent = network.options?.parent
  const port = 22

  return (
    <section className="space-y-3" aria-label="Who is on it">
      <p className="eyebrow">On the wire</p>
      <div className="relative py-2">
        <div aria-hidden className="wire-grid pointer-events-none absolute -inset-x-4 inset-y-0" />
        <div ref={container} className="relative">
          {network.members.map((member, index) => {
            const ref = refs.get(member.id)
            if (!ref) return null
            const t = traffic.get(member.id)
            const state = containers.get(member.id)?.state ?? member.state
            const moving = t !== undefined && t.rx + t.tx >= MOVING
            return (
              <AnimatedBeam
                key={member.id}
                containerRef={container}
                fromRef={hub}
                toRef={ref}
                shape="s"
                startXOffset={port}
                endXOffset={-18}
                reverse={t !== undefined && t.tx > t.rx}
                still={!moving}
                dashed={state !== "running"}
                duration={pulseDuration(t ? t.rx + t.tx : 0)}
                delay={(index % 5) * 0.3}
                className="max-lg:hidden"
              />
            )
          })}
          <div className="relative grid gap-y-5 lg:grid-cols-[minmax(0,0.8fr)_4rem_minmax(0,1.2fr)] lg:items-center">
            <WireNode
              nodeRef={hub}
              mark={
                network.driver === "host" ? (
                  <WireHost />
                ) : (
                  <WireMark tone="logo" shape="square" size="md">
                    {network.driver === "bridge" ? (
                      <ProductGlyph id="docker" />
                    ) : (
                      <LinkGlyph kind={network.driver} />
                    )}
                  </WireMark>
                )
              }
              eyebrow={network.driver === "bridge" ? "Bridge" : network.driver}
              title={
                <span className="font-mono">
                  {network.bridge ??
                    parent ??
                    (network.driver === "host" ? "this server" : network.name)}
                </span>
              }
              hint={
                network.gateway ? (
                  <span className="font-mono">gateway {network.gateway}</span>
                ) : (
                  plural(network.members.length, "member")
                )
              }
            />
            <div aria-hidden className="max-lg:hidden" />
            <ol className="flex flex-col gap-3.5">
              {network.members.map((member) => {
                const c = containers.get(member.id)
                const product = c ? containerProduct(c) : undefined
                const t = traffic.get(member.id)
                const state = c?.state ?? member.state
                return (
                  <li
                    key={member.id}
                    className={cn("min-w-0", state !== "running" && "opacity-60")}
                  >
                    <WireNode
                      nodeRef={refs.get(member.id)}
                      mark={
                        <WireMark tone="logo" shape="square" size="sm">
                          {product && hasProductLogo(product) ? (
                            <ProductGlyph id={product} />
                          ) : (
                            <ProductGlyph id="docker" />
                          )}
                        </WireMark>
                      }
                      title={<span className="block truncate">{member.name}</span>}
                      hint={
                        <span className="flex min-w-0 items-center gap-2">
                          <span className="truncate font-mono">
                            {bareAddress(member.ipv4) ??
                              (state === "running" ? "no address" : "stopped")}
                          </span>
                          {t && state === "running" && (
                            <span className="numeric shrink-0 font-mono text-micro">
                              <span style={{ color: RX }}>↓ {rate(t.rx)}</span>{" "}
                              <span style={{ color: TX }}>↑ {rate(t.tx)}</span>
                            </span>
                          )}
                        </span>
                      }
                    />
                  </li>
                )
              })}
            </ol>
          </div>
        </div>
      </div>
    </section>
  )
}

/**
 * The members as a table: the address each is reached at, the names the
 * others can use — a connection string on this network says `db:5432`, not
 * an address that changes when the container is recreated — and its state.
 */
function Members({
  network,
  containers,
  traffic,
  canDetach,
  onDetach,
}: {
  network: NetworkDetail
  containers: Map<string, Container>
  traffic: Map<string, ContainerTraffic>
  canDetach: boolean
  onDetach: (member: NetworkMember) => void
}) {
  const named = network.name !== "bridge" && network.driver !== "host" && network.driver !== "null"
  return (
    <section className="space-y-2" aria-label="Members">
      <div className="flex items-baseline justify-between gap-3">
        <p className="eyebrow">Members</p>
        <span className="numeric text-hint text-muted-foreground">
          {plural(network.members.length, "container")}
        </span>
      </div>
      {network.name === "bridge" && (
        <Notice tone="warning" title="No names on the default bridge">
          Containers here reach each other only by address, which changes when one is recreated. Put
          containers that talk to each other on a network of their own.
        </Notice>
      )}
      {network.members.length === 0 ? (
        <Hint className="italic">Nothing is attached.</Hint>
      ) : (
        <div className="-mx-4 border-y border-hairline">
          <Table className="table-fixed">
            <TableHeader>
              <TableRow>
                <TableHead>Container</TableHead>
                <TableHead className="w-36">Address</TableHead>
                {named && <TableHead className="w-44">Answers to</TableHead>}
                <TableHead className="w-12">
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {network.members.map((member) => {
                const c = containers.get(member.id)
                const state = c?.state ?? member.state ?? "unknown"
                const t = traffic.get(member.id)
                const aliases = member.aliases.filter((a) => !member.id.startsWith(a))
                return (
                  <TableRow key={member.id} className="group">
                    <TableCell className="py-2">
                      <div className="min-w-0">
                        <p className="truncate text-body font-medium" title={member.name}>
                          {member.name}
                        </p>
                        <p className="flex min-w-0 items-center gap-2 text-hint text-muted-foreground">
                          <Status state={state} label={state} className="text-hint font-normal" />
                          {t && state === "running" && (
                            <span className="numeric truncate font-mono text-micro">
                              <span style={{ color: RX }}>↓ {rate(t.rx)}</span>{" "}
                              <span style={{ color: TX }}>↑ {rate(t.tx)}</span>
                            </span>
                          )}
                        </p>
                      </div>
                    </TableCell>
                    <TableCell className="py-2">
                      <p className="truncate font-mono text-xs">
                        {bareAddress(member.ipv4) ?? (
                          <span className="text-muted-foreground">none</span>
                        )}
                      </p>
                      {member.ipv6 && (
                        <p
                          className="truncate font-mono text-micro text-muted-foreground"
                          title={member.ipv6}
                        >
                          {bareAddress(member.ipv6)}
                        </p>
                      )}
                    </TableCell>
                    {named && (
                      <TableCell className="py-2 whitespace-normal">
                        <span className="flex flex-wrap gap-1">
                          {aliases.length === 0 ? (
                            <span className="text-hint text-muted-foreground">{member.name}</span>
                          ) : (
                            aliases.map((a) => (
                              <Tag key={a} mono>
                                {a}
                              </Tag>
                            ))
                          )}
                        </span>
                      </TableCell>
                    )}
                    <TableCell className="py-2">
                      {canDetach && (
                        <IconAction
                          reveal
                          label={`Detach ${member.name}`}
                          onClick={() => onDetach(member)}
                        >
                          <Slash />
                        </IconAction>
                      )}
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        </div>
      )}
    </section>
  )
}

/** The rest of what Docker knows about it, for the reader who came to check one setting. */
function Settings({ network }: { network: NetworkDetail }) {
  const options = Object.entries(network.options ?? {})
  const labels = Object.entries(network.labels ?? {})
  return (
    <section className="space-y-2" aria-label="Settings">
      <p className="eyebrow">Settings</p>
      <DetailList className="gap-y-2">
        <Detail label="Subnets" className="text-body">
          {network.subnets.length > 0 ? (
            <span className="flex flex-col">
              {network.subnets.map((s) => (
                <Cidr key={s} cidr={s} />
              ))}
            </span>
          ) : (
            "assigned by Docker"
          )}
        </Detail>
        <Detail label="Gateway" className="font-mono text-body">
          {network.gateway || "—"}
        </Detail>
        <Detail label="Reaches the internet" className="text-body">
          {network.internal ? "no — its members reach only each other" : "yes"}
        </Detail>
        {/*
          Attachable decides whether a running container can join this network
          afterwards. Compose creates non-attachable networks by default — those
          only accept members listed in the compose file at creation, which is
          why attaching sometimes fails on a network that looks ordinary.
        */}
        <Detail label="Accepts new members" className="text-body">
          {network.attachable ? "yes, while running" : "only through its compose file"}
        </Detail>
        <Detail label="Scope" className="text-body">
          {network.scope || "local"}
        </Detail>
        <Detail label="Created" className="text-body">
          {network.created ? calendarDate(network.created) : "—"}
        </Detail>
        {options.map(([key, value]) => (
          <Detail
            key={key}
            label={key.replace(/^com\.docker\.network\./, "")}
            className="font-mono text-xs"
          >
            {value}
          </Detail>
        ))}
      </DetailList>
      {labels.length > 0 && (
        <ul className="space-y-1 pt-2 font-mono text-hint leading-relaxed text-muted-foreground">
          {labels.map(([key, value]) => (
            <li key={key} className="truncate" title={`${key}=${value}`}>
              {key}={value}
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
