"use client"

import Link from "next/link"
import { useRouter } from "next/navigation"
import { Linked, Trash } from "@/components/icons"
import { useArrivals } from "@/hooks/use-arrivals"
import { useAuth } from "@/hooks/use-auth"
import { useMediaQuery } from "@/hooks/use-mobile"
import { ProductLogos } from "@/components/product-logo"
import { ROW_BLEED } from "@/components/row-list"
import { Tag } from "@/components/tag"
import { VerbActions, type Verb } from "@/components/verbs"
import {
  stickyTableHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Cidr } from "@/components/network/address"
import { RX, RatePair, TX } from "@/components/network/rate-pair"
import { membersKnown } from "@/lib/docker-networks"
import { plural, rate as formatRate } from "@/lib/format"
import type { Container, DockerNetwork } from "@/lib/types"
import { cn } from "@/lib/utils"
import { memberProducts, type NetworkRate } from "@/components/docker/network-band"
import {
  attachRefusal,
  isUnused,
  networkHref,
  networkHue,
  networkOwner,
  removeRefusal,
} from "@/components/docker/networks"

type RowsProps = {
  rates: Map<string, NetworkRate>
  containers: Map<string, Container>
  onAttach: (network: DockerNetwork) => void
  onRemove: (network: DockerNetwork) => void
}

/**
 * The networks, wide and narrow.
 *
 * Wide, from `xl`, a table with fixed columns, the network taking what is
 * left: where it is in the address space, who is on it drawn as the products
 * they run, and what its bridge is carrying now with its last two minutes.
 * Each network keeps its colour down the row's edge, the one its span of the
 * traffic bar, its blocks of the address pool and its chips in the containers
 * table carry. The name is the link to the network's own page; the row is the
 * pointer's larger target for it.
 *
 * It was a column of cards since 2026-09-23, every one the same grey with the
 * same three facts in one line. Eleven networks are compared down their
 * columns — which is busy, which has nothing on it, which holds 172.20 — far
 * more than one is entered, as the containers table argued when it went back
 * to a table (§16). Below `xl` the same row is drawn down, nothing dropped.
 *
 * A row is one height in both, whatever it has: a network nothing is on and
 * one carrying megabytes take the same lines, so the row under the pointer
 * does not move when a bridge starts or stops carrying.
 */
export function NetworkRows({ rows, ...rest }: RowsProps & { rows: DockerNetwork[] }) {
  const arrived = useArrivals(rows.map((n) => n.id))
  const wide = useMediaQuery("(min-width: 1280px)")
  if (!wide) {
    return (
      <ul aria-label="Networks" className="divide-y divide-hairline px-4">
        {rows.map((network) => (
          <NetworkNarrowRow
            key={network.id}
            network={network}
            arrived={arrived.has(network.id)}
            {...rest}
          />
        ))}
      </ul>
    )
  }
  return (
    <Table className="table-fixed" containerClassName="max-h-[calc(100svh-13rem)]">
      <TableHeader className={stickyTableHeader}>
        <TableRow>
          <TableHead>Network</TableHead>
          <TableHead className="w-52">Subnet</TableHead>
          <TableHead className="w-44">Containers</TableHead>
          <TableHead className="w-48 text-right">Traffic</TableHead>
          <TableHead className="w-24">
            <span className="sr-only">Actions</span>
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((network) => (
          <NetworkTableRow
            key={network.id}
            network={network}
            arrived={arrived.has(network.id)}
            {...rest}
          />
        ))}
      </TableBody>
    </Table>
  )
}

/** Every cell of a row, at the one height a row has: two lines, whatever is in them. */
const CELL = "h-16 py-0"

function NetworkTableRow({
  network,
  arrived,
  rates,
  containers,
  onAttach,
  onRemove,
}: RowsProps & { network: DockerNetwork; arrived: boolean }) {
  const router = useRouter()
  const verbs = useNetworkVerbs({ network, onAttach, onRemove })
  return (
    <TableRow
      data-workspace-item={network.id}
      data-workspace-name={network.name}
      className={cn(
        "group",
        arrived && "animate-rise",
        isUnused(network) && "text-muted-foreground",
      )}
      onActivate={() => router.push(networkHref(network.name))}
    >
      <TableCell className={cn("relative", CELL)}>
        <HueEdge network={network} />
        <NetworkTitle network={network} />
      </TableCell>
      <TableCell className={CELL}>
        <SubnetReading network={network} />
      </TableCell>
      <TableCell className={CELL}>
        <MembersReading network={network} containers={containers} />
      </TableCell>
      <TableCell className={CELL}>
        <TrafficReading network={network} rate={rates.get(network.id)} />
      </TableCell>
      <TableCell className={CELL}>
        <VerbActions dim verbs={verbs} className="justify-end" />
      </TableCell>
    </TableRow>
  )
}

/**
 * A network drawn down rather than across. Still a row, not a card: no frame,
 * a hairline to the next, a wash under the pointer. Three lines on the left —
 * its name, who made it, its subnet and how many are on it — and on the right
 * its verbs over what its bridge carries, in a box of one height, so a
 * network with traffic and one without are the same row. The name is the
 * link; the surrounding press is a convenience for the pointer that skips any
 * press landing on a control of its own.
 */
function NetworkNarrowRow({
  network,
  arrived,
  rates,
  containers,
  onAttach,
  onRemove,
}: RowsProps & { network: DockerNetwork; arrived: boolean }) {
  const router = useRouter()
  const verbs = useNetworkVerbs({ network, onAttach, onRemove })
  const rate = rates.get(network.id)
  const carrying = network.bridge && rate && !(membersKnown(network) && network.usedBy.length === 0)
  return (
    // eslint-disable-next-line jsx-a11y/click-events-have-key-events, jsx-a11y/no-noninteractive-element-interactions -- the pointer's larger target: the row's name is the link a keyboard opens it by
    <li
      data-workspace-item={network.id}
      data-workspace-name={network.name}
      className={cn(
        "group relative flex h-[5.5rem] min-w-0 items-center gap-3 transition-colors hover:bg-row-hover",
        ROW_BLEED,
        arrived && "animate-rise",
      )}
      onClick={(event) => {
        if ((event.target as HTMLElement).closest("a, button, [role='menuitem']")) return
        router.push(networkHref(network.name))
      }}
    >
      <HueEdge network={network} />
      <div className="min-w-0 flex-1 pl-3">
        <NetworkTitle network={network} />
        <p className="mt-1 flex min-w-0 items-center gap-x-3 text-hint">
          <SubnetReading network={network} inline />
          <MembersReading network={network} containers={containers} inline />
        </p>
      </div>
      <div className="flex h-full shrink-0 flex-col items-end justify-center gap-1">
        {verbs.length > 0 && <VerbActions verbs={verbs} />}
        <span className="numeric grid h-7 content-center text-right font-mono text-micro leading-tight">
          {carrying && (
            <>
              <span style={{ color: RX }}>↓ {formatRate(rate.rx)}</span>
              <span style={{ color: TX }}>↑ {formatRate(rate.tx)}</span>
            </>
          )}
        </span>
      </div>
    </li>
  )
}

/** The network's colour down the row's leading edge. */
function HueEdge({ network }: { network: DockerNetwork }) {
  return (
    <span
      aria-hidden
      className={cn(
        "absolute inset-y-2 left-0 w-0.5 rounded-full",
        isUnused(network) && "opacity-40",
      )}
      style={{ background: networkHue(network.name) }}
    />
  )
}

/**
 * The name, what it is that the subnet does not say — cut off from the
 * internet, IPv6 — and who made it: a compose project, this dashboard, Docker
 * itself, or nobody in particular.
 */
function NetworkTitle({ network }: { network: DockerNetwork }) {
  const owner = networkOwner(network)
  const driver =
    network.driver === "bridge"
      ? undefined
      : network.driver === "null"
        ? "no network"
        : network.driver
  return (
    <div className="min-w-0">
      <div className="flex min-w-0 items-center gap-2">
        <Link
          data-workspace-primary
          href={networkHref(network.name)}
          title={network.name}
          // The row's own press already opens it for the pointer; this is the
          // link, and must not open it twice.
          onClick={(event) => event.stopPropagation()}
          className="max-w-full min-w-0 truncate rounded-sm text-body font-medium focus-ring hover:underline"
        >
          {network.name}
        </Link>
        {network.internal && (
          <Tag className="shrink-0" title="Containers on it reach each other and nothing beyond">
            no internet
          </Tag>
        )}
        {network.ipv6 && <Tag className="shrink-0">IPv6</Tag>}
      </div>
      <p className="truncate text-hint text-muted-foreground">
        {owner.label}
        {driver && ` · ${driver}`}
        {network.name === "bridge" && " · the default bridge"}
      </p>
    </div>
  )
}

/** The first IPv4 subnet with its prefix in the port hue, and the gateway under it. */
function SubnetReading({ network, inline }: { network: DockerNetwork; inline?: boolean }) {
  const [first, ...more] = network.subnets
  if (!first) {
    return (
      <span className="text-hint text-muted-foreground">
        {network.driver === "host" ? "the host's own addresses" : "no subnet"}
      </span>
    )
  }
  const subnet = (
    <span
      className="inline-flex min-w-0 items-center gap-1.5 text-body"
      title={network.subnets.join(", ")}
    >
      <Cidr cidr={first} className="truncate" />
      {more.length > 0 && (
        <span className="numeric text-hint text-muted-foreground">+{more.length}</span>
      )}
    </span>
  )
  if (inline) return <span className="min-w-0 shrink truncate">{subnet}</span>
  return (
    <div className="min-w-0">
      {subnet}
      <p className="truncate font-mono text-hint text-muted-foreground">
        {network.gateway ? `via ${network.gateway}` : (network.bridge ?? " ")}
      </p>
    </div>
  )
}

/** Who is on it, as the products they run, with how many are running. */
function MembersReading({
  network,
  containers,
  inline,
}: {
  network: DockerNetwork
  containers: Map<string, Container>
  inline?: boolean
}) {
  const count = network.usedBy.length
  // The container listing failed: who is on it is unread, which is not none.
  if (!membersKnown(network)) {
    return (
      <span
        className="text-hint text-warning"
        title={network.membersError ?? "Docker listed the network but not its containers"}
      >
        members unread
      </span>
    )
  }
  if (count === 0) {
    return (
      <span className="text-hint text-muted-foreground" title="Holds a subnet out of the pool">
        nothing attached
      </span>
    )
  }
  const running = (network.endpoints ?? []).filter(
    (e) => containers.get(e.container)?.state === "running",
  ).length
  const products = memberProducts(network, containers)
  return (
    <span
      className={cn("flex min-w-0 items-center gap-2.5", inline && "shrink-0")}
      title={network.usedBy.join(", ")}
    >
      {products.length > 0 && !inline && <ProductLogos ids={products} />}
      <span className="min-w-0">
        <span className={cn("numeric block", inline ? "text-hint" : "text-body")}>
          {plural(count, "container")}
        </span>
        {!inline && containers.size > 0 && (
          <span
            className={cn(
              "numeric block text-hint",
              running < count ? "text-warning" : "text-muted-foreground",
            )}
          >
            {running === count ? "all running" : `${running} running`}
          </span>
        )}
      </span>
    </span>
  )
}

/**
 * The bridge's in and out now, over its last two minutes. A network with no
 * bridge on this host — host, none, a macvlan riding a card — has no counter
 * of its own to read, and one with nothing attached carries nothing; both say
 * so with a dash rather than drawing a zero.
 */
function TrafficReading({ network, rate }: { network: DockerNetwork; rate?: NetworkRate }) {
  if (!network.bridge || (membersKnown(network) && network.usedBy.length === 0) || !rate) {
    return (
      <p
        className="text-right text-hint text-muted-foreground"
        title={
          !network.bridge
            ? "No bridge on this host carries it"
            : membersKnown(network)
              ? "Nothing attached to carry"
              : "Who is attached could not be read"
        }
      >
        —
      </p>
    )
  }
  return <RatePair rx={rate.rx} tx={rate.tx} points={rate.points} className="justify-end" />
}

/**
 * A network's verbs: attach a container where it accepts one, and remove it
 * where Docker would. Both are drawn whether or not they would work, disabled
 * with the reason where they would not — a control that disappears says
 * nothing about why. Removing opens what removing it disturbs, read first.
 */
function useNetworkVerbs({
  network,
  onAttach,
  onRemove,
}: {
  network: DockerNetwork
  onAttach: (network: DockerNetwork) => void
  onRemove: (network: DockerNetwork) => void
}): Verb[] {
  const { can } = useAuth()
  const verbs: Verb[] = []
  const refusal = attachRefusal(network)
  if (can("service.control") && !refusal?.hidden) {
    verbs.push({
      key: "attach",
      label: refusal?.reason ?? `Attach a container to ${network.name}`,
      icon: Linked,
      inline: true,
      disabled: refusal !== undefined,
      run: () => onAttach(network),
    })
  }
  if (can("destructive")) {
    const blocked = removeRefusal(network)
    verbs.push({
      key: "remove",
      label: blocked ?? `Remove ${network.name}`,
      icon: Trash,
      inline: true,
      danger: !blocked,
      disabled: blocked !== undefined,
      run: () => onRemove(network),
    })
  }
  return verbs
}
