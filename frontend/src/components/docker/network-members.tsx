"use client"

import { useArrivals } from "@/hooks/use-arrivals"
import { useMediaQuery } from "@/hooks/use-mobile"
import { RowLink } from "@/components/page"
import { ProductLogo, containerProduct } from "@/components/product-logo"
import { ROW_BLEED } from "@/components/row-list"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import {
  stickyTableHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { RatePair } from "@/components/network/rate-pair"
import { duration } from "@/lib/format"
import { hueFor, LANES } from "@/lib/hue"
import type { Container, DockerNetwork, NetworkEndpoint, NetworkLivePoint } from "@/lib/types"
import { cn } from "@/lib/utils"
import { HueKey } from "@/components/docker/network-band"
import { answersTo, bareAddress, networkHue } from "@/components/docker/networks"

export type Placement = { network: DockerNetwork; endpoint?: NetworkEndpoint }

export type ContainerTraffic = {
  rx: number
  tx: number
  points: NetworkLivePoint[]
  /** Carried over a frame Docker could not measure; the next such frame drops it. */
  held?: boolean
}

type RowsProps = {
  /** The network the table is narrowed to, whose chips let it go. */
  focused?: string
  placements: Map<string, Placement[]>
  traffic: Map<string, ContainerTraffic>
  /** Narrows the table to one network. */
  onNetwork: (network: DockerNetwork) => void
  onOpen: (container: Container) => void
}

/**
 * Every container and where it sits: each network it is on with its address
 * there, the names the others on it can reach it by, and what it is sending
 * and receiving now.
 *
 * "Can the API reach the database?" is the question this page exists for, and
 * a list of networks answers it only one network at a time. Read down this
 * table it is one look: the two rows share a colour, or they do not. The
 * names column is the other half of the answer — `db` resolves on a network a
 * person or compose made, and on the default bridge nothing does, which is
 * why a container moved there by hand stops finding its database. A row
 * opens the container; a network's chip narrows the table to that network.
 */
export function ContainerNetworkRows({ rows, ...rest }: RowsProps & { rows: Container[] }) {
  const arrived = useArrivals(rows.map((c) => c.id))
  const wide = useMediaQuery("(min-width: 1280px)")
  if (!wide) {
    return (
      <ul aria-label="Containers" className="divide-y divide-hairline px-4">
        {rows.map((container) => (
          <ContainerNarrowRow
            key={container.id}
            container={container}
            arrived={arrived.has(container.id)}
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
          <TableHead>Container</TableHead>
          <TableHead className="w-40">State</TableHead>
          <TableHead className="w-80">Networks</TableHead>
          <TableHead className="w-56">Answers to</TableHead>
          <TableHead className="w-48 text-right">Traffic</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((container) => (
          <ContainerTableRow
            key={container.id}
            container={container}
            arrived={arrived.has(container.id)}
            {...rest}
          />
        ))}
      </TableBody>
    </Table>
  )
}

function ContainerTableRow({
  container,
  arrived,
  focused,
  placements,
  traffic,
  onNetwork,
  onOpen,
}: RowsProps & { container: Container; arrived: boolean }) {
  const placed = placements.get(container.id) ?? []
  return (
    <TableRow
      data-workspace-item={container.id}
      data-workspace-name={container.name}
      className={cn("group", arrived && "animate-rise")}
      onActivate={() => onOpen(container)}
    >
      <TableCell className="py-2.5">
        <ContainerTitle container={container} onOpen={() => onOpen(container)} />
      </TableCell>
      <TableCell className="py-2.5">
        <ContainerState container={container} />
      </TableCell>
      <TableCell className="py-2.5">
        <NetworkChips placed={placed} focused={focused} onNetwork={onNetwork} />
      </TableCell>
      <TableCell className="py-2.5 whitespace-normal">
        <Names container={container} placed={placed} />
      </TableCell>
      <TableCell className="py-2.5">
        <ContainerRate container={container} placed={placed} traffic={traffic.get(container.id)} />
      </TableCell>
    </TableRow>
  )
}

/** A container drawn down rather than across, nothing dropped. */
function ContainerNarrowRow({
  container,
  arrived,
  focused,
  placements,
  traffic,
  onNetwork,
  onOpen,
}: RowsProps & { container: Container; arrived: boolean }) {
  const placed = placements.get(container.id) ?? []
  return (
    <li
      data-workspace-item={container.id}
      data-workspace-name={container.name}
      className={cn(
        "group flex min-w-0 flex-col gap-2 py-3 transition-colors hover:bg-row-hover",
        ROW_BLEED,
        arrived && "animate-rise",
      )}
      onClick={(event) => {
        if ((event.target as HTMLElement).closest("a, button, [role='menuitem']")) return
        onOpen(container)
      }}
    >
      <div className="flex min-w-0 items-start justify-between gap-3">
        <ContainerTitle container={container} onOpen={() => onOpen(container)} />
        <ContainerState container={container} />
      </div>
      <div className="pl-11">
        <NetworkChips placed={placed} focused={focused} onNetwork={onNetwork} />
      </div>
      <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1.5 pl-11">
        <Names container={container} placed={placed} />
        <ContainerRate container={container} placed={placed} traffic={traffic.get(container.id)} />
      </div>
    </li>
  )
}

/** The container as the product it runs, its name, and the compose project in its lane hue. */
function ContainerTitle({ container, onOpen }: { container: Container; onOpen: () => void }) {
  return (
    <div className="flex min-w-0 items-center gap-3">
      <ProductLogo id={containerProduct(container)} size="sm" />
      <div className="min-w-0">
        <RowLink data-workspace-primary title={container.name} onClick={onOpen}>
          {container.name}
        </RowLink>
        <p className="truncate text-hint text-muted-foreground">
          {container.composeStack ? (
            <>
              <span style={{ color: hueFor(container.composeStack, LANES) }}>
                {container.composeStack}
              </span>
              {container.composeService && ` · ${container.composeService}`}
            </>
          ) : (
            <span className="font-mono" title={container.image}>
              {container.image}
            </span>
          )}
        </p>
      </div>
    </div>
  )
}

/** Running and for how long, or how it stopped. */
function ContainerState({ container }: { container: Container }) {
  const running = container.state === "running"
  return (
    <div className="min-w-0 shrink-0">
      <Status state={container.state} label={running ? "running" : container.state} />
      <p className="numeric truncate text-hint text-muted-foreground" title={container.status}>
        {running && container.uptimeSeconds > 0
          ? `up ${duration(container.uptimeSeconds)}`
          : running
            ? " "
            : container.state === "paused"
              ? "keeps its address"
              : "holds no address"}
      </p>
    </div>
  )
}

/**
 * Each network the container is on, in that network's colour, with its
 * address there. A press narrows the table to the network, which is how
 * "who else is on proxy" is asked from any row.
 */
function NetworkChips({
  placed,
  focused,
  onNetwork,
}: {
  placed: Placement[]
  focused?: string
  onNetwork: (network: DockerNetwork) => void
}) {
  if (placed.length === 0) {
    return <span className="text-hint text-muted-foreground">on no network</span>
  }
  return (
    <ul className="grid min-w-0 grid-cols-[minmax(0,11rem)_auto] items-center gap-x-4 gap-y-0.5">
      {placed.map(({ network, endpoint }) => (
        <li key={network.id} className="col-span-2 grid grid-cols-subgrid items-center">
          <button
            type="button"
            onClick={() => onNetwork(network)}
            aria-label={
              network.id === focused
                ? "Show containers on every network"
                : `Show the containers on ${network.name}`
            }
            title={
              network.id === focused
                ? "Show containers on every network"
                : `Show the containers on ${network.name}`
            }
            className="-mx-1 inline-flex min-w-0 items-center gap-1.5 rounded-sm px-1 text-body focus-ring transition-colors hover:bg-row-hover"
          >
            <HueKey color={networkHue(network.name)} />
            <span className="truncate" style={{ color: networkHue(network.name) }}>
              {network.name}
            </span>
          </button>
          <span className="numeric font-mono text-hint text-muted-foreground">
            {bareAddress(endpoint?.ipv4 ?? endpoint?.ipv6) ??
              (network.driver === "host" ? "host" : network.driver === "null" ? "—" : "no address")}
          </span>
        </li>
      ))}
    </ul>
  )
}

/**
 * The names a container answers to on the networks it shares with others. The
 * same on every network a person or compose made; on the default bridge and
 * the host network there is none, and the reason is the cell.
 */
function Names({ container, placed }: { container: Container; placed: Placement[] }) {
  const answers = placed.map((p) => answersTo(container, p.network))
  const names = [...new Set(answers.flatMap((a) => a.names))]
  if (names.length === 0) {
    const why = answers.find((a) => a.why)?.why
    return (
      <span
        className={cn(
          "text-hint",
          placed.some((p) => p.network.name === "bridge")
            ? "text-warning"
            : "text-muted-foreground",
        )}
      >
        {why ?? "—"}
      </span>
    )
  }
  return (
    <span className="flex min-w-0 flex-wrap items-center gap-1">
      {names.map((name) => (
        <Tag key={name} mono>
          {name}
        </Tag>
      ))}
    </span>
  )
}

/**
 * What the container is sending and receiving now, over its last minute. On
 * the host network a container has no interface of its own to count, so it
 * says whose counters it shares rather than measuring forever.
 */
function ContainerRate({
  container,
  placed,
  traffic,
}: {
  container: Container
  placed: Placement[]
  traffic?: ContainerTraffic
}) {
  const own = placed.some((p) => p.network.driver !== "host" && p.network.driver !== "null")
  if (container.state !== "running" || (!own && !traffic)) {
    return (
      <p
        className="text-right text-hint text-muted-foreground"
        title={
          own ? undefined : "On the host's own interfaces: the host's counters are its counters"
        }
      >
        —
      </p>
    )
  }
  if (!traffic) {
    return (
      <p className="text-right text-hint text-muted-foreground" title="A rate needs two readings">
        measuring
      </p>
    )
  }
  return (
    <RatePair rx={traffic.rx} tx={traffic.tx} points={traffic.points} className="justify-end" />
  )
}
