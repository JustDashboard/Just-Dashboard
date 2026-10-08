"use client"

import { Warning } from "@/components/icons"
import { useArrivals } from "@/hooks/use-arrivals"
import { useMediaQuery } from "@/hooks/use-mobile"
import { useNow } from "@/components/deploy/vocabulary"
import { HUE } from "@/components/overview/readings"
import { Sparkline } from "@/components/metrics/sparkline"
import { utilisationTone } from "@/components/meter"
import { RowLink } from "@/components/page"
import { ProductLogo, containerProduct } from "@/components/product-logo"
import { ROW_BLEED } from "@/components/row-list"
import { Status } from "@/components/status-dot"
import { TextShimmer } from "@/components/ui/text-shimmer"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { bytes, duration, percent, plural } from "@/lib/format"
import { LANES, hueFor } from "@/lib/hue"
import type { Container, ContainerSparkline, ContainerStats, DockerDiagnosis } from "@/lib/types"
import { cn } from "@/lib/utils"
import { MiniBar } from "@/components/procs/process-table"
import { PortList } from "@/components/docker/exposure"
import {
  ContainerRowActions,
  useContainerVerbs,
  type PendingMap,
} from "@/components/docker/container-actions"
import { stateWord } from "@/components/docker/container-cells"
import { bucketTone, containerBucket, stoppedWords } from "@/components/docker/overview"
import type { ConfirmFn } from "@/components/docker/shared"

/** The colour a compose project is drawn in, wherever its name is: a kind, never a state. */
export function projectHue(project: string) {
  return hueFor(project, LANES)
}

export function ProjectKey({ project }: { project: string }) {
  return (
    <span
      aria-hidden
      className="size-1.5 shrink-0 rounded-full"
      style={{ background: projectHue(project) }}
    />
  )
}

type RowsProps = {
  stats: Record<string, ContainerStats>
  trends: Map<string, ContainerSparkline>
  diagnosis?: DockerDiagnosis
  pending: PendingMap
  confirm: ConfirmFn
  act: (c: Container, action: string, progressive: string, phrase?: string) => Promise<void>
  onOpen: (id: string, tab?: string) => void
  onChanged: () => void
  now: number
}

/**
 * Every container on the host, as rows of readings you open.
 *
 * A table again, where `/docker/containers` draws cards: this is the
 * overview's reading of the whole host, and the question it is read with is a
 * column question — which one is using the processor, which is near its
 * memory limit, which is published on every interface — so the readings line
 * up under a header that names them once, the way Services and PM2 draw their
 * units. Each row still opens the container, as a press anywhere on it.
 *
 * Wide, the columns are fixed and the container takes what is left, so a long
 * image reference never pushes the readings or the row's verbs past the
 * panel's edge. Below `xl` the same row is drawn down instead of across, with
 * nothing dropped. One shape is rendered, chosen once, so every row's verbs
 * exist once.
 */
export function ContainerRows({ rows, ...rest }: Omit<RowsProps, "now"> & { rows: Container[] }) {
  const wide = useMediaQuery("(min-width: 1280px)")
  const now = useNow(30_000)
  // A state change updates the row in place; only a new container arrives.
  const arrived = useArrivals(rows.map((c) => c.id))
  // Memory without a limit is drawn against the heaviest listed, as the
  // process table's is: against the host, a column of 2% containers is a
  // column of empty tracks.
  const heaviest = rows.reduce((top, c) => Math.max(top, rest.stats[c.id]?.memUsage ?? 0), 0)
  const shared = { ...rest, now }
  if (!wide) {
    return (
      <ul className="divide-y divide-hairline px-4">
        {rows.map((container) => (
          <NarrowRow
            key={container.id}
            container={container}
            arrived={arrived.has(container.id)}
            {...shared}
          />
        ))}
      </ul>
    )
  }
  return (
    <Table className="table-fixed">
      <TableHeader>
        <TableRow>
          <TableHead>Container</TableHead>
          <TableHead className="w-48">State</TableHead>
          <TableHead className="w-36 text-right">CPU · last hour</TableHead>
          <TableHead className="w-36 text-right">Memory</TableHead>
          <TableHead className="w-36">Published</TableHead>
          <TableHead className="w-28">
            <span className="sr-only">Actions</span>
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((container) => (
          <WideRow
            key={container.id}
            container={container}
            heaviest={heaviest}
            arrived={arrived.has(container.id)}
            {...shared}
          />
        ))}
      </TableBody>
    </Table>
  )
}

function useVerbs(container: Container, props: RowsProps) {
  return useContainerVerbs({
    container,
    confirm: props.confirm,
    act: props.act,
    onOpenTab: (tab) => props.onOpen(container.id, tab),
    onChanged: props.onChanged,
  })
}

function WideRow({
  container,
  heaviest,
  arrived,
  ...props
}: RowsProps & { container: Container; heaviest: number; arrived: boolean }) {
  const verbs = useVerbs(container, props)
  const pending = props.pending[container.id]
  const stat = props.stats[container.id]
  return (
    <TableRow
      data-workspace-item={container.id}
      data-workspace-name={container.name}
      className={cn("group", arrived && "animate-rise", pending && "opacity-70")}
      onActivate={() => props.onOpen(container.id)}
    >
      <TableCell className="py-2">
        <div className="flex w-full min-w-0 items-center gap-3">
          <ProductLogo id={containerProduct(container)} size="sm" />
          <div className="min-w-0">
            <div className="flex min-w-0 items-center gap-2">
              <RowLink title={container.name} onClick={() => props.onOpen(container.id)}>
                {container.name}
              </RowLink>
              <Issues
                container={container}
                diagnosis={props.diagnosis}
                onOpen={() => props.onOpen(container.id, "overview")}
              />
            </div>
            <Origin container={container} />
          </div>
        </div>
      </TableCell>
      <TableCell className="py-2">
        <StateReading
          container={container}
          pending={pending}
          now={props.now}
          diagnosis={props.diagnosis}
        />
      </TableCell>
      <TableCell className="py-2">
        <CpuReading
          container={container}
          stat={stat}
          trend={props.trends.get(container.name)?.cpu}
        />
      </TableCell>
      <TableCell className="py-2">
        <MemoryReading container={container} stat={stat} heaviest={heaviest} />
      </TableCell>
      <TableCell className="py-2">
        <PortList ports={container.exposure ?? []} max={1} />
      </TableCell>
      <TableCell className="py-2">
        {/* Always drawn, quiet until the row is hovered: these own their
            column, and a reserved column left empty reads as a layout bug. */}
        <ContainerRowActions verbs={verbs} reveal={false} dim className="justify-end" />
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
function NarrowRow({
  container,
  arrived,
  ...props
}: RowsProps & { container: Container; arrived: boolean }) {
  const verbs = useVerbs(container, props)
  const pending = props.pending[container.id]
  const stat = props.stats[container.id]
  return (
    <li
      data-workspace-item={container.id}
      data-workspace-name={container.name}
      className={cn(
        "group flex min-w-0 items-start gap-3 py-3 transition-colors hover:bg-row-hover",
        ROW_BLEED,
        arrived && "animate-rise",
        pending && "opacity-70",
      )}
      onClick={(event) => {
        if ((event.target as HTMLElement).closest("a, button, [role='menuitem']")) return
        props.onOpen(container.id)
      }}
    >
      <ProductLogo id={containerProduct(container)} size="sm" />
      <div className="min-w-0 flex-1 space-y-1.5">
        <div className="min-w-0">
          <div className="flex min-w-0 items-center gap-2">
            <RowLink data-workspace-primary onClick={() => props.onOpen(container.id)}>
              {container.name}
            </RowLink>
            <Issues
              container={container}
              diagnosis={props.diagnosis}
              onOpen={() => props.onOpen(container.id, "overview")}
            />
          </div>
          <Origin container={container} />
        </div>
        <StateReading
          container={container}
          pending={pending}
          now={props.now}
          diagnosis={props.diagnosis}
          inline
        />
        {stat && container.state === "running" && (
          <p className="numeric flex flex-wrap items-center gap-x-3 gap-y-1 font-mono text-hint text-muted-foreground">
            <span>{stat.cpuReady === false ? "measuring" : `${percent(stat.cpuPercent)} CPU`}</span>
            <span>
              {bytes(stat.memUsage)}
              {(stat.memLimited || (container.memoryLimit ?? 0) > 0) &&
                ` of ${bytes(container.memoryLimit || stat.memLimit)}`}
            </span>
          </p>
        )}
        {(container.exposure ?? []).some((p) => p.hostPort) && (
          <PortList ports={container.exposure ?? []} max={2} />
        )}
      </div>
      <ContainerRowActions verbs={verbs} reveal={false} className="shrink-0" />
    </li>
  )
}

/** What the container is made of: its project and service in the project's hue, then the image. */
function Origin({ container }: { container: Container }) {
  const project = container.composeStack
  return (
    <p className="flex min-w-0 items-center gap-1.5 text-hint text-muted-foreground">
      {project && (
        <>
          <ProjectKey project={project} />
          <span className="shrink-0 truncate" style={{ color: projectHue(project) }}>
            {project}
            {container.composeService && (
              <span className="text-muted-foreground">/{container.composeService}</span>
            )}
          </span>
          <span aria-hidden>·</span>
        </>
      )}
      <span className="min-w-0 truncate font-mono" title={container.image}>
        {container.image}
      </span>
    </p>
  )
}

/**
 * What the dashboard thinks is wrong with it, beside its name: the count of
 * issues in their worst tone, and nothing when there are none. The
 * recommendations stay in Attention, where there is room for the reasoning.
 */
function Issues({
  container,
  diagnosis,
  onOpen,
}: {
  container: Container
  diagnosis?: DockerDiagnosis
  onOpen: () => void
}) {
  const issues = (diagnosis?.findings ?? []).filter(
    (f) =>
      f.targetId === container.id &&
      f.class !== "runtime" &&
      (f.severity === "critical" || f.severity === "warning"),
  )
  if (issues.length === 0) return null
  const critical = issues.some((f) => f.severity === "critical")
  return (
    <button
      type="button"
      onClick={(event) => {
        event.stopPropagation()
        onOpen()
      }}
      title={issues.map((f) => f.title).join("\n")}
      aria-label={`${plural(issues.length, "issue")} on ${container.name}`}
      className={cn(
        "inline-flex shrink-0 items-center gap-1 rounded-sm text-hint font-medium focus-ring",
        critical ? "text-destructive" : "text-warning",
      )}
    >
      <Warning className="size-3" />
      {issues.length}
    </button>
  )
}

/**
 * The state as a word, and under it how long the container has been in it —
 * up for how long and whether anything is checking it, or how it stopped and
 * when. A verb in flight or a health check that has not passed yet shimmers,
 * because it is happening.
 */
function StateReading({
  container,
  pending,
  now,
  diagnosis,
  inline,
}: {
  container: Container
  pending?: string
  now: number
  diagnosis?: DockerDiagnosis
  inline?: boolean
}) {
  const bucket = containerBucket(container)
  const tone = pending ? "warning" : bucketTone(bucket)
  const running = container.state === "running"
  const word =
    container.health === "unhealthy" && running
      ? "Unhealthy"
      : bucket === "failing" && container.state === "exited"
        ? "Crashed"
        : stateWord(container.state)

  let detail: React.ReactNode
  if (running) {
    const started = container.startedAt ? Date.parse(container.startedAt) : NaN
    const exact = Number.isFinite(started)
      ? Math.max(0, (now - started) / 1000)
      : container.uptimeSeconds
    // To the minute past the first: the clock moves every thirty seconds, so
    // a seconds figure would only ever be the time since the last tick.
    const uptime = exact < 60 ? exact : Math.floor(exact / 60) * 60
    const check =
      container.health === "unhealthy"
        ? "failing its check"
        : container.health
          ? container.health
          : container.inspected
            ? "no health check"
            : undefined
    detail = (
      <>
        {uptime > 0 && `up ${duration(uptime)}`}
        {uptime > 0 && check && " · "}
        {check && (
          <span className={cn(container.health === "unhealthy" && "text-destructive/80")}>
            {check}
          </span>
        )}
      </>
    )
  } else {
    detail = (
      <span className={cn(bucket === "failing" && "text-destructive/80")}>
        {stoppedWords(container, diagnosis?.findings)}
      </span>
    )
  }

  const status = (
    <span title={container.status}>
      <Status
        tone={tone}
        live={running && !pending && bucket === "running"}
        label={
          pending ? (
            <TextShimmer>{`${pending}…`}</TextShimmer>
          ) : bucket === "starting" ? (
            <TextShimmer>Starting</TextShimmer>
          ) : (
            word
          )
        }
      />
    </span>
  )
  if (inline) {
    return (
      <span className="flex min-w-0 items-center gap-2 text-hint">
        {status}
        <span className="numeric truncate text-muted-foreground">{detail}</span>
      </span>
    )
  }
  return (
    <div className="min-w-0">
      {status}
      <p className="numeric truncate text-hint text-muted-foreground">{detail}</p>
    </div>
  )
}

/**
 * A share of one core, as Docker counts it, beside the container's last hour
 * in the processor's hue — so a container that pinned a core for ten minutes
 * and settled still shows the hump. Toned only when it nears its quota, or a
 * whole core where it has none.
 */
function CpuReading({
  container,
  stat,
  trend,
}: {
  container: Container
  stat?: ContainerStats
  trend?: number[]
}) {
  if (container.state !== "running" || !stat) {
    return <p className="text-right font-mono text-muted-foreground/60">—</p>
  }
  const limit = container.cpuLimit ?? stat.cpuLimit ?? 0
  const share = limit > 0 ? stat.cpuPercent / limit : stat.cpuPercent
  const tone = utilisationTone(share)
  const color =
    tone === "danger" ? "var(--destructive)" : tone === "warning" ? "var(--warning)" : HUE.cpu
  return (
    <div
      className="flex items-center justify-end gap-2"
      title={
        limit > 0
          ? `${percent(stat.cpuPercent)} of one core — ${percent(share)} of its ${plural(limit, "core")}`
          : `${percent(stat.cpuPercent)} of one core, no limit`
      }
    >
      {trend && trend.length > 1 ? (
        <Sparkline
          values={trend}
          width={44}
          height={16}
          color={color}
          label={`${container.name}'s processor over the last hour`}
          className="shrink-0"
        />
      ) : (
        <MiniBar value={Math.min(share, 100)} color={color} />
      )}
      <span
        className={cn(
          "numeric w-14 text-right font-mono",
          tone === "danger"
            ? "font-medium text-destructive"
            : tone === "warning"
              ? "text-warning"
              : "text-foreground",
        )}
      >
        {stat.cpuReady === false ? (
          <span className="text-muted-foreground/60">—</span>
        ) : (
          percent(stat.cpuPercent)
        )}
      </span>
    </div>
  )
}

/**
 * Memory against the container's limit where it has one, and against the
 * heaviest container where it has none — never against the host's RAM, which
 * Docker reports as the limit of a container nobody limited.
 */
function MemoryReading({
  container,
  stat,
  heaviest,
}: {
  container: Container
  stat?: ContainerStats
  heaviest: number
}) {
  if (container.state !== "running" || !stat) {
    return <p className="text-right font-mono text-muted-foreground/60">—</p>
  }
  const limited = stat.memLimited || (container.memoryLimit ?? 0) > 0
  const limit = container.memoryLimit || stat.memLimit
  const tone = limited ? utilisationTone(stat.memPercent) : "default"
  return (
    <div className="min-w-0 text-right">
      <div className="flex items-center justify-end gap-2">
        <MiniBar
          value={limited ? stat.memPercent : heaviest > 0 ? (stat.memUsage / heaviest) * 100 : 0}
          color={
            tone === "danger"
              ? "var(--destructive)"
              : tone === "warning"
                ? "var(--warning)"
                : HUE.mem
          }
        />
        <span
          className={cn(
            "numeric w-18 text-right font-mono",
            tone === "danger" ? "text-destructive" : tone === "warning" && "text-warning",
          )}
        >
          {bytes(stat.memUsage)}
        </span>
      </div>
      <p className="numeric truncate text-hint text-muted-foreground">
        {limited ? `${percent(stat.memPercent)} of ${bytes(limit)}` : "no limit"}
      </p>
    </div>
  )
}
