"use client"

import { Box, ChevronDown, ChevronUp } from "@/components/icons"
import { useArrivals } from "@/hooks/use-arrivals"
import { useMediaQuery } from "@/hooks/use-mobile"
import { useNow } from "@/components/deploy/vocabulary"
import { HUE } from "@/components/overview/readings"
import { RowLink } from "@/components/page"
import { ProductLogo, containerProduct } from "@/components/product-logo"
import { ROW_BLEED } from "@/components/row-list"
import { Status, StatusDot } from "@/components/status-dot"
import { Sparkline } from "@/components/metrics/sparkline"
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
import { cpuTone } from "@/components/procs/shared"
import { RatePair } from "@/components/network/rate-pair"
import { bytes, duration, percent, rate } from "@/lib/format"
import { hueFor, LANES } from "@/lib/hue"
import type { Container, ContainerSparkline, ContainerStats, DockerDiagnosis } from "@/lib/types"
import { cn } from "@/lib/utils"
import { PortList } from "@/components/docker/exposure"
import { IssuesCell } from "@/components/docker/container-cells"
import {
  ContainerRowActions,
  useContainerVerbs,
  type PendingMap,
} from "@/components/docker/container-actions"
import type { ConfirmFn } from "@/components/docker/shared"
import {
  bucketTone,
  containerBucket,
  containerWord,
  exitCodeOf,
  exitedWhen,
  exitWords,
  type ContainerSort,
  type NetRate,
  type SortKey,
} from "@/components/docker/containers"

export type RowsProps = {
  stats: Record<string, ContainerStats>
  rates: Record<string, NetRate>
  trends: Map<string, ContainerSparkline>
  diagnosis?: DockerDiagnosis
  oomKilled: Set<string>
  pending: PendingMap
  confirm: ConfirmFn
  act: (c: Container, action: string, progressive: string, phrase?: string) => Promise<void>
  onOpen: (id: string, tab?: string) => void
  onChanged: () => void
  /** Narrows the table to one compose project; "" is the containers no project owns. */
  onStack: (stack: string) => void
}

/**
 * The containers, wide and narrow.
 *
 * Wide, from `xl`, a table with fixed columns, the container taking what is
 * left, so a long image reference ellipses rather than pushing the readings
 * or the row's verbs past the panel's edge. Every column a reader sorts by is
 * a heading that sorts; the order holds still under the pointer, because a
 * table ranked by a figure that moves every two seconds would otherwise move
 * the row being reached for. Network and the processor's last hour appear
 * where there is room for them, from `2xl`.
 *
 * Below `xl` the same row is drawn down instead of across, nothing dropped.
 *
 * It was a column of cards from 2026-09-23 — every row opens the container's
 * own page, so §16 drew it as a destination. The operator asked for the
 * table back: thirty containers are read down a column far more than they
 * are entered, and Live, PM2 and Services had all become tables whose rows
 * open something. The row still opens the container; its name is the button.
 */
export function ContainerRows({
  rows,
  sort,
  onSort,
  ...rest
}: RowsProps & {
  rows: Container[]
  sort: ContainerSort
  onSort: (key: SortKey) => void
}) {
  // Each uptime ticks every second between the socket's frames.
  const now = useNow(1000)
  const arrived = useArrivals(rows.map((c) => c.id))
  // Without a limit, memory is drawn against the heaviest listed, as the
  // process table's bars are: against the host, a column of 2% containers is
  // a column of empty tracks.
  const heaviest = rows.reduce((top, c) => Math.max(top, rest.stats[c.id]?.memUsage ?? 0), 0)
  // One shape, chosen here, rather than a hidden twin of the other: a
  // reading that exists in a hidden copy is two answers to every query (§12).
  const wide = useMediaQuery("(min-width: 1280px)")
  if (wide) {
    return (
      <div className="min-w-0">
        <Table className="table-fixed" containerClassName="max-h-[calc(100svh-13rem)]">
          <TableHeader className={stickyTableHeader}>
            <TableRow>
              <SortHead label="Container" column="name" sort={sort} onSort={onSort} />
              <SortHead label="State" column="state" sort={sort} onSort={onSort} className="w-44" />
              <SortHead
                label="CPU"
                column="cpu"
                sort={sort}
                onSort={onSort}
                className="w-32 text-right 2xl:w-44"
              />
              <SortHead
                label="Memory"
                column="memory"
                sort={sort}
                onSort={onSort}
                className="w-36 text-right"
              />
              <TableHead className="hidden w-36 text-right 2xl:table-cell">Network</TableHead>
              <TableHead className="w-32 2xl:w-36">Ports</TableHead>
              <TableHead className="w-28">
                <span className="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((container) => (
              <ContainerTableRow
                key={container.id}
                container={container}
                heaviest={heaviest}
                arrived={arrived.has(container.id)}
                now={now}
                {...rest}
              />
            ))}
          </TableBody>
        </Table>
      </div>
    )
  }
  return (
    <ul className="divide-y divide-hairline px-4">
      {rows.map((container) => (
        <ContainerNarrowRow
          key={container.id}
          container={container}
          arrived={arrived.has(container.id)}
          now={now}
          {...rest}
        />
      ))}
    </ul>
  )
}

/** A column heading that sorts by its column, saying so to a screen reader. */
function SortHead({
  label,
  column,
  sort,
  onSort,
  className,
}: {
  label: string
  column: SortKey
  sort: ContainerSort
  onSort: (key: SortKey) => void
  className?: string
}) {
  const active = sort.key === column
  const Arrow = sort.dir === "asc" ? ChevronUp : ChevronDown
  return (
    <TableHead
      className={className}
      aria-sort={active ? (sort.dir === "asc" ? "ascending" : "descending") : "none"}
    >
      <button
        type="button"
        onClick={() => onSort(column)}
        className={cn(
          "-mx-1 inline-flex items-center gap-1 rounded-sm px-1 focus-ring transition-colors hover:text-foreground",
          active && "text-foreground",
        )}
      >
        {label}
        {active && <Arrow aria-hidden className="size-3" />}
      </button>
    </TableHead>
  )
}

type RowProps = RowsProps & { container: Container; arrived: boolean; now: number }

function useRowVerbs(container: Container, rest: RowsProps) {
  return useContainerVerbs({
    container,
    confirm: rest.confirm,
    act: rest.act,
    onOpenTab: (tab) => rest.onOpen(container.id, tab),
    onChanged: rest.onChanged,
  })
}

function ContainerTableRow({
  container,
  heaviest,
  arrived,
  now,
  ...rest
}: RowProps & { heaviest: number }) {
  const verbs = useRowVerbs(container, rest)
  const stat = rest.stats[container.id]
  return (
    <TableRow
      data-workspace-item={container.id}
      data-workspace-name={container.name}
      className={cn("group", arrived && "animate-rise")}
      onActivate={() => rest.onOpen(container.id)}
    >
      <TableCell className="py-2">
        <Identity container={container} {...rest} />
      </TableCell>
      <TableCell className="py-2">
        <State
          container={container}
          busy={rest.pending[container.id]}
          now={now}
          oomKilled={rest.oomKilled.has(container.id)}
        />
      </TableCell>
      <TableCell className="py-2">
        <CpuCell container={container} stat={stat} trend={rest.trends.get(container.name)?.cpu} />
      </TableCell>
      <TableCell className="py-2">
        <MemoryCell container={container} stat={stat} heaviest={heaviest} />
      </TableCell>
      <TableCell className="hidden py-2 2xl:table-cell">
        <NetworkCell container={container} stat={stat} rate={rest.rates[container.id]} />
      </TableCell>
      <TableCell className="py-2">
        <PortList ports={container.exposure ?? []} max={1} />
      </TableCell>
      <TableCell className="py-2">
        {/* Always drawn, quiet until the row is hovered: these own their
            column, and a reserved column left empty reads as a layout bug. */}
        <ContainerRowActions verbs={verbs} reveal={false} dim />
      </TableCell>
    </TableRow>
  )
}

/**
 * A row drawn down rather than across. Still a row, not a card: no frame, a
 * hairline to the next, a wash under the pointer. The name is the real
 * button; the surrounding press is a convenience for the pointer that skips
 * any press landing on a control of its own.
 */
function ContainerNarrowRow({ container, arrived, now, ...rest }: RowProps) {
  const verbs = useRowVerbs(container, rest)
  const stat = rest.stats[container.id]
  const limited = stat && (stat.memLimited || (container.memoryLimit ?? 0) > 0)
  return (
    <li
      data-workspace-item={container.id}
      data-workspace-name={container.name}
      className={cn(
        "group flex min-w-0 items-start gap-3 py-3 transition-colors hover:bg-row-hover",
        ROW_BLEED,
        arrived && "animate-rise",
      )}
      onClick={(event) => {
        if ((event.target as HTMLElement).closest("a, button, [role='menuitem']")) return
        rest.onOpen(container.id)
      }}
    >
      <Identity container={container} {...rest}>
        <div className="mt-1.5 space-y-1.5 text-hint">
          <State
            container={container}
            busy={rest.pending[container.id]}
            now={now}
            oomKilled={rest.oomKilled.has(container.id)}
            inline
          />
          {stat && (
            <p className="numeric flex flex-wrap items-center gap-x-3 gap-y-1 text-muted-foreground [&>span]:whitespace-nowrap">
              {stat.cpuReady !== false && <span>{percent(stat.cpuPercent)} CPU</span>}
              <span>
                {bytes(stat.memUsage)}
                {limited ? ` of ${bytes(container.memoryLimit || stat.memLimit)}` : ", no limit"}
              </span>
              {rest.rates[container.id] && (
                <span>
                  ↓ {rate(rest.rates[container.id].rx)} ↑ {rate(rest.rates[container.id].tx)}
                </span>
              )}
            </p>
          )}
          <PortList ports={container.exposure ?? []} max={2} />
        </div>
      </Identity>
      <ContainerRowActions verbs={verbs} reveal={false} className="shrink-0" />
    </li>
  )
}

/**
 * The container as its image's product, its name and how many things the
 * dashboard found wrong with it, and under them what it is made of: the image
 * and the compose project and service, the project in its own lane hue and a
 * press away from being the only one in the table.
 */
function Identity({
  container,
  diagnosis,
  onOpen,
  onStack,
  children,
}: Pick<RowsProps, "diagnosis" | "onOpen" | "onStack"> & {
  container: Container
  /** What a narrow row draws under the name: its state and readings. */
  children?: React.ReactNode
}) {
  const findings = (diagnosis?.findings ?? []).some((f) => f.targetId === container.id)
  const stack = container.composeStack
  return (
    <div className={cn("flex min-w-0 flex-1 gap-3", children ? "items-start" : "items-center")}>
      <ProductLogo id={containerProduct(container)} size="sm" fallback={Box} />
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 items-center gap-1.5">
          <RowLink title={container.name} onClick={() => onOpen(container.id)}>
            {container.name}
          </RowLink>
          {findings && (
            <IssuesCell
              diagnosis={diagnosis}
              containerId={container.id}
              onOpen={() => onOpen(container.id, "overview")}
            />
          )}
        </div>
        {/* The project leads, in its hue, because it is what the row is
            scanned for in a list of thirty; the image gives way first. */}
        <p className="flex min-w-0 items-center gap-1.5 text-hint text-muted-foreground">
          {stack && (
            <>
              <button
                type="button"
                aria-label={`Only the ${stack} stack`}
                title={`Only the ${stack} stack`}
                onClick={(event) => {
                  event.stopPropagation()
                  onStack(stack)
                }}
                className="inline-flex max-w-[60%] min-w-0 shrink-0 items-center gap-1 rounded-sm focus-ring hover:text-foreground"
              >
                <span
                  aria-hidden
                  className="size-1.5 shrink-0 rounded-full"
                  style={{ background: hueFor(stack, LANES) }}
                />
                <span className="truncate">
                  {stack}/{container.composeService}
                </span>
              </button>
              <span aria-hidden>·</span>
            </>
          )}
          <span className="min-w-0 truncate font-mono" title={container.image}>
            {container.image}
          </span>
        </p>
        {children}
      </div>
    </div>
  )
}

/**
 * The state as a word, and under it how long the container has been in it
 * and whether anything is checking it: up for how long, ticking, beside its
 * health check's verdict or the absence of one; stopped with the exit said in
 * words and when. A verb in flight, a container on its way up or a restart
 * loop shimmers, because it is happening.
 */
function State({
  container,
  busy,
  now,
  oomKilled,
  inline,
}: {
  container: Container
  busy?: string
  now: number
  oomKilled: boolean
  inline?: boolean
}) {
  const bucket = containerBucket(container, oomKilled)
  const tone = bucketTone(bucket)
  const word = containerWord(container, oomKilled)
  const moving = container.state === "restarting" || bucket === "starting"
  const running = container.state === "running"

  let since: React.ReactNode = null
  if (running || container.state === "paused") {
    const started = container.startedAt ? Date.parse(container.startedAt) : NaN
    const seconds = Number.isNaN(started)
      ? container.uptimeSeconds
      : Math.max(0, (now - started) / 1000)
    const check =
      container.health === "healthy"
        ? "healthy"
        : container.health === "unhealthy"
          ? "failing its check"
          : container.health === "starting"
            ? "checking"
            : container.inspected
              ? "no health check"
              : undefined
    since = (
      <>
        {seconds > 0 && `up ${duration(seconds)}`}
        {seconds > 0 && check && " · "}
        {check && (
          <span className={cn(container.health === "unhealthy" && "text-destructive/80")}>
            {check}
          </span>
        )}
      </>
    )
  } else if (container.state === "exited") {
    const words = exitWords(exitCodeOf(container), oomKilled)
    const when = exitedWhen(container)
    since = (
      <span className={cn(bucket === "failing" && "text-destructive/80")}>
        {[words, when].filter(Boolean).join(" · ")}
      </span>
    )
  } else if (container.state === "restarting") {
    const words = exitWords(exitCodeOf(container), oomKilled)
    since = <span className="text-destructive/80">{words ? `${words} · ` : ""}coming back</span>
  } else if (container.state === "created") {
    since = "created, never run"
  }

  const status = busy ? (
    <span className="inline-flex items-center gap-1.5 text-xs font-medium">
      <StatusDot tone="notice" />
      <TextShimmer>{`${busy}…`}</TextShimmer>
    </span>
  ) : (
    <span title={container.status}>
      <Status
        tone={tone}
        live={running && bucket === "running"}
        label={moving ? <TextShimmer>{word}</TextShimmer> : word}
      />
    </span>
  )
  if (inline) {
    return (
      <span className="flex min-w-0 items-center gap-2">
        {status}
        {since && <span className="numeric truncate text-muted-foreground">{since}</span>}
      </span>
    )
  }
  return (
    <div className="min-w-0" aria-busy={busy ? true : undefined}>
      {status}
      {/* A non-breaking space rather than nothing, so a row whose state has
          no second line is exactly as tall as the one above it. */}
      <p className="numeric truncate text-hint text-muted-foreground">{since || " "}</p>
    </div>
  )
}

/**
 * A share of one core, as a figure beside a short bar that fills at a whole
 * core — or at the container's quota where it has one — and from `2xl` the
 * last hour of it before them. Nothing for a container that is not running;
 * a dash for one not yet measured.
 */
function CpuCell({
  container,
  stat,
  trend,
}: {
  container: Container
  stat?: ContainerStats
  trend?: number[]
}) {
  if (!stat || stat.cpuReady === false) {
    return (
      <p
        className="text-right font-mono text-muted-foreground/60"
        title={container.state === "running" ? "Waiting for a second reading" : undefined}
      >
        —
      </p>
    )
  }
  const limit = container.cpuLimit ?? stat.cpuLimit ?? 0
  const share = limit > 0 ? stat.cpuPercent / limit : stat.cpuPercent
  const tone = cpuTone(share)
  return (
    <div
      className="flex items-center justify-end gap-2"
      title={
        `${percent(stat.cpuPercent)} of one core` +
        (limit > 0 ? `, ${percent(share)} of its ${limit}-core limit` : "")
      }
    >
      {trend && trend.length > 1 && (
        <Sparkline
          values={trend}
          width={44}
          height={14}
          label="CPU over the last hour"
          color={HUE.cpu}
          className="hidden shrink-0 2xl:block"
        />
      )}
      <MiniBar
        value={Math.min(share, 100)}
        color={
          tone === "danger" ? "var(--destructive)" : tone === "warning" ? "var(--warning)" : HUE.cpu
        }
      />
      <span
        className={cn(
          "numeric w-13 text-right font-mono",
          tone === "danger"
            ? "font-medium text-destructive"
            : tone === "warning"
              ? "text-warning"
              : "text-muted-foreground",
        )}
      >
        {percent(stat.cpuPercent)}
      </span>
    </div>
  )
}

/**
 * Memory against the container's own limit where it has one — the ceiling the
 * kernel kills it at, amber past 85% — and against the heaviest listed where
 * it has none, with "no limit" said rather than the host's RAM passed off as
 * a budget.
 */
function MemoryCell({
  container,
  stat,
  heaviest,
}: {
  container: Container
  stat?: ContainerStats
  heaviest: number
}) {
  if (!stat) return <p className="text-right font-mono text-muted-foreground/60">—</p>
  const limited = stat.memLimited || (container.memoryLimit ?? 0) > 0
  const limit = container.memoryLimit || stat.memLimit
  const near = limited && stat.memPercent >= 85
  return (
    <div className="flex flex-col items-end">
      <div
        className="flex items-center justify-end gap-2"
        title={
          limited
            ? `${percent(stat.memPercent)} of its ${bytes(limit)} limit`
            : `No limit${stat.memHostPercent ? ` · ${percent(stat.memHostPercent)} of the host` : ""}`
        }
      >
        <MiniBar
          value={limited ? stat.memPercent : heaviest > 0 ? (stat.memUsage / heaviest) * 100 : 0}
          color={near ? "var(--warning)" : HUE.mem}
        />
        <span className={cn("numeric w-16 text-right font-mono", near && "text-warning")}>
          {bytes(stat.memUsage)}
        </span>
      </div>
      <p className="numeric text-hint text-muted-foreground">
        {limited ? `of ${bytes(limit)}` : "no limit"}
      </p>
    </div>
  )
}

/** Bytes a second in and out, in the network section's colours, from the last two frames. */
function NetworkCell({
  container,
  stat,
  rate: net,
}: {
  container: Container
  stat?: ContainerStats
  rate?: NetRate
}) {
  if (!stat || container.state !== "running") {
    return <p className="text-right font-mono text-muted-foreground/60">—</p>
  }
  if (stat.networkAvailable === false) {
    return <p className="text-right text-hint text-muted-foreground">host network</p>
  }
  if (!net) {
    return <p className="text-right font-mono text-muted-foreground/60">—</p>
  }
  return <RatePair rx={net.rx} tx={net.tx} className="justify-end" />
}
