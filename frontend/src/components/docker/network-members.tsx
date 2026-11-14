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
 *
 * A row is one height, whatever the container is on: two networks are the
 * row's two lines, a third and later are counted after the first (each is on
 * the container's own page and on that network's), and the names stay on one
 * line with the rest behind a count. A container on two networks used to be
 * a taller row, and one whose names wrapped a taller one again.
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
          {/* The sized columns leave the container its name at 1280, where
              the table begins: at their full width they left it the logo. */}
          <TableHead className="w-32 2xl:w-40">State</TableHead>
          <TableHead className="w-64 2xl:w-80">Networks</TableHead>
          <TableHead className="w-44 2xl:w-56">Answers to</TableHead>
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

/** Every cell of a row, at the one height a row has: two lines, whatever is in them. */
const CELL = "h-16 py-0"

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
      <TableCell className={CELL}>
        <ContainerTitle container={container} onOpen={() => onOpen(container)} />
      </TableCell>
      <TableCell className={CELL}>
        <ContainerState container={container} />
      </TableCell>
      <TableCell className={CELL}>
        <NetworkChips placed={placed} focused={focused} onNetwork={onNetwork} />
      </TableCell>
      <TableCell className={CELL}>
        <Names container={container} placed={placed} />
      </TableCell>
      <TableCell className={CELL}>
        <ContainerRate container={container} placed={placed} traffic={traffic.get(container.id)} />
      </TableCell>
    </TableRow>
  )
}

/**
 * A container drawn down rather than across, nothing dropped: its name and
 * state, the networks it is on in a box two lines tall, then its names and
 * its traffic on one line, so every container is the same row.
 */
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
    // eslint-disable-next-line jsx-a11y/click-events-have-key-events, jsx-a11y/no-noninteractive-element-interactions -- the pointer's larger target: the row's name is the link or button a keyboard opens it by
    <li
      data-workspace-item={container.id}
      data-workspace-name={container.name}
      className={cn(
        // A height rather than padding: the hairline over every row but the
        // first is inside it, so the first is not a pixel shorter.
        "group flex h-[9.5rem] min-w-0 flex-col justify-center gap-2 transition-colors hover:bg-row-hover",
        ROW_BLEED,
        arrived && "animate-rise",
      )}
      onClick={(event) => {
        if ((event.target as HTMLElement).closest("a, button, [role='menuitem']")) return
        onOpen(container)
      }}
    >
      <div className="flex h-10 min-w-0 items-center justify-between gap-3">
        <ContainerTitle container={container} onOpen={() => onOpen(container)} />
        <ContainerState container={container} />
      </div>
      <div className="flex h-11 items-center pl-11">
        <NetworkChips placed={placed} focused={focused} onNetwork={onNetwork} />
      </div>
      <div className="flex h-7 min-w-0 items-center justify-between gap-x-4 pl-11">
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
  // Two lines is the row: with more than two networks the first keeps its
  // line and the rest are counted on the second.
  const shown = placed.length > 2 ? placed.slice(0, 1) : placed
  const rest = placed.slice(shown.length)
  return (
    <ul className="grid w-full min-w-0 grid-cols-[minmax(0,11rem)_auto] items-center gap-x-4">
      {shown.map(({ network, endpoint }) => (
        <li key={network.id} className="col-span-2 grid h-5.5 grid-cols-subgrid items-center">
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
            className="-mx-1 inline-flex h-5.5 min-w-0 items-center gap-1.5 rounded-sm px-1 text-body focus-ring transition-colors hover:bg-row-hover"
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
      {rest.length > 0 && (
        <li
          className="col-span-2 flex h-5.5 items-center text-hint text-muted-foreground"
          title={rest.map((p) => p.network.name).join(", ")}
        >
          and {rest.length} more: <span className="ml-1 truncate">{rest[0].network.name}</span>
          {rest.length > 1 && <span className="numeric ml-1 shrink-0">+{rest.length - 1}</span>}
        </li>
      )}
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
          "min-w-0 truncate text-hint",
          placed.some((p) => p.network.name === "bridge")
            ? "text-warning"
            : "text-muted-foreground",
        )}
        title={why}
      >
        {why ?? "—"}
      </span>
    )
  }
  // On one line: the service's short name first and whole, the container's
  // after it and cut where the column ends, the rest behind a count.
  return (
    <span
      className="flex min-w-0 flex-1 items-center gap-1 overflow-hidden"
      title={names.join(", ")}
    >
      {names.slice(0, 2).map((name, index) => (
        <Tag key={name} mono className={cn(index > 0 && "min-w-0 shrink")}>
          <span className="truncate">{name}</span>
        </Tag>
      ))}
      {names.length > 2 && (
        <span className="numeric shrink-0 text-micro text-muted-foreground">
          +{names.length - 2}
        </span>
      )}
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
