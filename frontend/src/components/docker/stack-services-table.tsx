"use client"

import { ArrowDown, ArrowUp, Play } from "@/components/icons"
import { useArrivals } from "@/hooks/use-arrivals"
import { bytes, percent, rate } from "@/lib/format"
import { cn } from "@/lib/utils"
import { useNow } from "@/components/deploy/vocabulary"
import { HUE } from "@/components/overview/readings"
import { Sparkline } from "@/components/metrics/sparkline"
import { RowLink } from "@/components/page"
import { ProductLogo, containerProduct, imageProduct } from "@/components/product-logo"
import { ROW_BLEED } from "@/components/row-list"
import { Status, StatusDot } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { VerbActions, type Verb } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import { TextShimmer } from "@/components/ui/text-shimmer"
import {
  stickyTableHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { MiniBar } from "@/components/procs/process-table"
import { PortLink } from "@/components/docker/shared"
import {
  bucketTone,
  stateDetail,
  type ServiceReading,
} from "@/components/docker/stack-service-readings"

/** The share of a limit past which a reading is amber: the containers page's line. */
const NEAR_LIMIT = 85

export type ServiceRowsProps = {
  rows: ServiceReading[]
  /** The compose verb in flight, and the service it is for — that row says so. */
  pending?: { service?: string; label: string }
  verbsFor: (reading: ServiceReading) => Verb[]
  /** Compose can create a service the file declares; undefined where the reader may not. */
  onCreate?: (reading: ServiceReading) => void
  onOpen: (reading: ServiceReading) => void
  /** From 1280 the rows are a table; below, each is drawn down instead of across. */
  wide: boolean
  /** From 1536 the table has room for the network's rates. */
  roomy: boolean
}

/**
 * A stack's services, one row each: the service as the product its image is
 * with its lane down the row's edge, its state and how long it has been in
 * it, its processor's last hour beside this second's share, its memory
 * against its limit, its traffic, its published ports and its verbs.
 *
 * A table again rather than the lit cards it replaces, because a stack is
 * read across: which of six is the one using the memory, which is the one
 * restarting, is a column read down, and six cards put each reading in a
 * different place. Each row still opens its container. Wide, the columns are
 * fixed and the service takes what is left, so a long image reference never
 * pushes the readings or the verbs past the panel's edge; narrow, the same
 * row is drawn down instead of across with nothing dropped (§12).
 */
export function ServiceRows({ rows, wide, roomy, ...rest }: ServiceRowsProps) {
  // A service is the row's identity: a container replaced by a deploy is the
  // same row changing, not a new one arriving.
  const arrived = useArrivals(rows.map((r) => r.key))
  // Memory with no limit is drawn against the heaviest in the stack, as the
  // process table's bars are; against the host, five 1% services are five
  // empty tracks.
  const heaviest = rows.reduce((top, r) => Math.max(top, r.stat?.memUsage ?? 0), 0)
  if (!wide) {
    return (
      <ul className="divide-y divide-hairline px-4">
        {rows.map((reading) => (
          <ServiceNarrowRow
            key={reading.key}
            reading={reading}
            arrived={arrived.has(reading.key)}
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
          <TableHead>Service</TableHead>
          <TableHead className="w-56">State</TableHead>
          <TableHead className="w-36 text-right">CPU</TableHead>
          <TableHead className="w-44 text-right">Memory</TableHead>
          {roomy && <TableHead className="w-36 text-right">Network</TableHead>}
          <TableHead className="w-40">Ports</TableHead>
          <TableHead className="w-28">
            <span className="sr-only">Actions</span>
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((reading) => (
          <ServiceTableRow
            key={reading.key}
            reading={reading}
            heaviest={heaviest}
            roomy={roomy}
            arrived={arrived.has(reading.key)}
            {...rest}
          />
        ))}
      </TableBody>
    </Table>
  )
}

type RowProps = Omit<ServiceRowsProps, "rows" | "wide" | "roomy"> & {
  reading: ServiceReading
  arrived: boolean
}

function productOf(reading: ServiceReading) {
  if (reading.container) return containerProduct(reading.container)
  return reading.service.image ? imageProduct(reading.service.image) : undefined
}

/**
 * The service as its product, with its lane down the edge: the hue its name
 * takes in the stack's log, so a row and its lines are found as one.
 */
function ServiceMark({ reading }: { reading: ServiceReading }) {
  return (
    <span className="flex shrink-0 items-center gap-2.5">
      <span
        aria-hidden
        className="h-8 w-0.5 shrink-0 rounded-full"
        style={{ background: reading.lane }}
      />
      <span className={cn("flex", reading.bucket === "missing" && "opacity-50 grayscale")}>
        <ProductLogo id={productOf(reading)} size="sm" />
      </span>
    </span>
  )
}

function ServiceName({ reading, onOpen }: { reading: ServiceReading; onOpen: RowProps["onOpen"] }) {
  const name = reading.service.name
  return (
    <span className="flex min-w-0 items-center gap-2">
      {reading.containerId ? (
        <RowLink title={name} onClick={() => onOpen(reading)}>
          {name}
        </RowLink>
      ) : (
        <span className="min-w-0 truncate text-body font-medium text-muted-foreground">{name}</span>
      )}
      {reading.orphan && (
        <Tag
          tone="warning"
          title="Running under this project, but the compose file no longer declares it"
        >
          not in file
        </Tag>
      )}
    </span>
  )
}

function ServiceTableRow({
  reading,
  heaviest,
  roomy,
  arrived,
  pending,
  verbsFor,
  onCreate,
  onOpen,
}: RowProps & { heaviest: number; roomy: boolean }) {
  const busy = pending && pending.service === reading.key ? pending.label : undefined
  return (
    <TableRow
      data-workspace-item={reading.key}
      data-workspace-name={reading.key}
      className={cn("group", arrived && "animate-rise")}
      onActivate={reading.containerId ? () => onOpen(reading) : undefined}
    >
      <TableCell className="py-2">
        <div className="flex w-full min-w-0 items-center gap-3">
          <ServiceMark reading={reading} />
          <div className="min-w-0">
            <ServiceName reading={reading} onOpen={onOpen} />
            <p
              className="truncate font-mono text-hint text-muted-foreground"
              title={reading.service.image}
            >
              {reading.service.image || "built from source"}
            </p>
          </div>
        </div>
      </TableCell>
      <TableCell className="py-2">
        <ServiceState reading={reading} busy={busy} />
      </TableCell>
      <TableCell className="py-2">
        <CpuCell reading={reading} />
      </TableCell>
      <TableCell className="py-2">
        <MemoryCell reading={reading} heaviest={heaviest} />
      </TableCell>
      {roomy && (
        <TableCell className="py-2">
          <NetworkCell reading={reading} />
        </TableCell>
      )}
      <TableCell className="py-2">
        <Ports reading={reading} />
      </TableCell>
      <TableCell className="py-2">
        <RowVerbs
          reading={reading}
          verbsFor={verbsFor}
          onCreate={onCreate}
          busy={Boolean(pending)}
        />
      </TableCell>
    </TableRow>
  )
}

/**
 * A row drawn down rather than across. Still a row, not a card: no frame, a
 * hairline to the next, a wash under the pointer. The name is the real
 * button; the surrounding press is a convenience that skips any press
 * landing on a control of its own.
 */
function ServiceNarrowRow({ reading, arrived, pending, verbsFor, onCreate, onOpen }: RowProps) {
  const busy = pending && pending.service === reading.key ? pending.label : undefined
  const { stat, rate: traffic } = reading
  const figures = [
    stat && stat.cpuReady !== false ? `${percent(stat.cpuPercent)} CPU` : undefined,
    stat ? bytes(stat.memUsage) : undefined,
    traffic ? `↓${rate(traffic.rx)} ↑${rate(traffic.tx)}` : undefined,
  ].filter(Boolean)
  return (
    <li
      data-workspace-item={reading.key}
      data-workspace-name={reading.key}
      className={cn(
        "group flex min-w-0 items-start gap-3 py-3 transition-colors hover:bg-row-hover",
        ROW_BLEED,
        arrived && "animate-rise",
      )}
      onClick={(event) => {
        if (!reading.containerId) return
        if ((event.target as HTMLElement).closest("a, button, [role='menuitem']")) return
        onOpen(reading)
      }}
    >
      <ServiceMark reading={reading} />
      <div className="min-w-0 flex-1">
        <ServiceName reading={reading} onOpen={onOpen} />
        <p className="truncate font-mono text-hint text-muted-foreground">
          {reading.service.image || "built from source"}
        </p>
        <div className="mt-1.5 min-w-0">
          <ServiceState reading={reading} busy={busy} />
        </div>
        {figures.length > 0 && (
          <p className="numeric mt-0.5 flex flex-wrap gap-x-2.5 text-hint text-muted-foreground">
            {figures.map((figure) => (
              <span key={figure} className="whitespace-nowrap">
                {figure}
              </span>
            ))}
          </p>
        )}
        <Ports reading={reading} className="mt-1.5" />
      </div>
      <RowVerbs reading={reading} verbsFor={verbsFor} onCreate={onCreate} busy={Boolean(pending)} />
    </li>
  )
}

/**
 * The state as a word, and under it how long the service has been in it or
 * how it went down. A running service's dot breathes, because the row is fed
 * by the containers socket; a service on its way somewhere, or one a verb is
 * acting on, shimmers, because it is happening.
 */
function ServiceState({ reading, busy }: { reading: ServiceReading; busy?: string }) {
  const now = useNow(1000, reading.state === "running")
  const detail = stateDetail(reading, now)
  const tone = bucketTone(reading.bucket)
  const changing = reading.bucket === "starting" || reading.state === "restarting"
  const status = busy ? (
    <span className="inline-flex items-center gap-1.5 text-xs font-medium">
      <StatusDot tone="notice" />
      <TextShimmer>{`${busy}…`}</TextShimmer>
    </span>
  ) : (
    <span title={reading.service.status || reading.state}>
      <Status
        tone={tone}
        live={reading.state === "running" && reading.container !== undefined}
        label={changing ? <TextShimmer>{reading.word}</TextShimmer> : reading.word}
      />
    </span>
  )
  const line = detail && (
    <span
      className={cn(
        "numeric truncate",
        detail.tone === "danger"
          ? "text-destructive/85"
          : detail.tone === "warning"
            ? "text-warning"
            : "text-muted-foreground",
      )}
    >
      {detail.text}
    </span>
  )
  return (
    <div className="flex min-w-0 flex-col items-start">
      {status}
      {/* A non-breaking space keeps a row with no second line as tall as its neighbours. */}
      <p className="flex max-w-full min-w-0 text-hint">{line || "\u00a0"}</p>
    </div>
  )
}

/** Nothing to read: a service that is not running, or whose first frame is on its way. */
function Unread() {
  return <p className="text-right font-mono text-muted-foreground/60">—</p>
}

/**
 * A share of one core, the unit Docker counts in, beside the hour it came
 * out of. Against a quota where the service has one, and amber near it.
 */
function CpuCell({ reading }: { reading: ServiceReading }) {
  const { stat } = reading
  if (!stat || stat.cpuReady === false) return <Unread />
  const limit = reading.container?.cpuLimit ?? stat.cpuLimit ?? 0
  const near = limit > 0 && stat.cpuPercent / limit >= NEAR_LIMIT
  return (
    <div
      className="flex items-center justify-end gap-2"
      title={
        limit > 0
          ? `${percent(stat.cpuPercent)} of one core — limited to ${limit} core${limit === 1 ? "" : "s"}`
          : `${percent(stat.cpuPercent)} of one core; Docker counts one core as 100%`
      }
    >
      {reading.trend && reading.trend.length > 1 && (
        <Sparkline
          values={reading.trend}
          width={48}
          height={16}
          color={HUE.cpu}
          label={`${reading.key}'s processor over the last hour`}
          className="shrink-0 animate-rise"
        />
      )}
      <span className={cn("numeric w-14 text-right font-mono", near && "text-warning")}>
        {percent(stat.cpuPercent)}
      </span>
    </div>
  )
}

/**
 * Memory against the service's limit where somebody set one — amber past
 * 85%, because the kernel kills it at the limit — and against the heaviest
 * service in the stack where nobody did, which is a comparison rather than a
 * budget, so it carries no tone.
 */
function MemoryCell({ reading, heaviest }: { reading: ServiceReading; heaviest: number }) {
  const { stat } = reading
  if (!stat) return <Unread />
  const limited = stat.memLimited || (reading.container?.memoryLimit ?? 0) > 0
  const limit = reading.container?.memoryLimit || stat.memLimit
  const share = limited ? stat.memPercent : heaviest > 0 ? (stat.memUsage / heaviest) * 100 : 0
  const near = limited && stat.memPercent >= NEAR_LIMIT
  return (
    <div
      className="flex items-center justify-end gap-2"
      title={
        limited
          ? `${bytes(stat.memUsage)} of its ${bytes(limit)} limit — ${percent(stat.memPercent)}`
          : "No memory limit: the kernel decides what to kill when the server runs out"
      }
    >
      <MiniBar
        value={share}
        color={near ? "var(--warning)" : limited ? HUE.mem : "var(--muted-foreground)"}
      />
      <span
        className={cn("numeric text-right font-mono whitespace-nowrap", near && "text-warning")}
      >
        {bytes(stat.memUsage)}
        {limited && <span className="text-muted-foreground"> / {bytes(limit, 0)}</span>}
      </span>
    </div>
  )
}

/** In and out this second, in the network section's colours for each direction. */
function NetworkCell({ reading }: { reading: ServiceReading }) {
  const traffic = reading.rate
  if (!traffic) return <Unread />
  return (
    <div className="numeric flex flex-col items-end font-mono text-hint leading-tight">
      <span className="inline-flex items-center gap-1" title="Received">
        <ArrowDown aria-hidden className="size-3" style={{ color: "var(--chart-2)" }} />
        {rate(traffic.rx)}
      </span>
      <span className="inline-flex items-center gap-1 text-muted-foreground" title="Sent">
        <ArrowUp aria-hidden className="size-3" style={{ color: HUE.net }} />
        {rate(traffic.tx)}
      </span>
    </div>
  )
}

function Ports({ reading, className }: { reading: ServiceReading; className?: string }) {
  const published = reading.service.ports.filter(
    (p, index, all) =>
      p.publicPort &&
      // Docker lists a port on every interface once for IPv4 and once for IPv6.
      all.findIndex((q) => q.publicPort === p.publicPort && q.type === p.type) === index,
  )
  if (published.length === 0) {
    return className ? null : <span className="text-hint text-muted-foreground/60">—</span>
  }
  return (
    <span className={cn("flex flex-wrap gap-1", className)}>
      {published.map((p) => (
        <PortLink
          key={`${p.ip}:${p.publicPort}/${p.type}`}
          ip={p.ip}
          port={p.publicPort ?? 0}
          target={p.privatePort}
        />
      ))}
    </span>
  )
}

function RowVerbs({
  reading,
  verbsFor,
  onCreate,
  busy,
}: {
  reading: ServiceReading
  verbsFor: ServiceRowsProps["verbsFor"]
  onCreate?: ServiceRowsProps["onCreate"]
  busy: boolean
}) {
  if (reading.bucket === "missing") {
    return onCreate ? (
      <Button size="xs" variant="outline" disabled={busy} onClick={() => onCreate(reading)}>
        <Play className="size-3" />
        Create it
      </Button>
    ) : null
  }
  const verbs = verbsFor(reading)
  if (verbs.length === 0) return null
  // Always drawn, quiet until the row is hovered: these own their column, and
  // a reserved column left empty reads as a layout bug.
  return (
    <VerbActions
      dim
      verbs={verbs}
      menuLabel={`More actions for ${reading.key}`}
      className="justify-end"
    />
  )
}
