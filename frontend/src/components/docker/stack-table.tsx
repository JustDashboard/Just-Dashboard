"use client"

import Link from "next/link"
import {
  ChevronDown,
  ChevronRight,
  Code,
  FolderOpen,
  Layers,
  MoreHorizontal,
  Play,
  Terminal,
} from "@/components/icons"
import { useAuth } from "@/hooks/use-auth"
import { useArrivals } from "@/hooks/use-arrivals"
import { useNow } from "@/components/deploy/vocabulary"
import { HUE } from "@/components/overview/readings"
import { Sparkline } from "@/components/metrics/sparkline"
import { RowLink } from "@/components/page"
import {
  ProductLogo,
  ProductLogos,
  containerProduct,
  imageProduct,
  imageProducts,
} from "@/components/product-logo"
import { Status, StatusDot, type DotTone } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
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
import { bytes, duration, percent, plural } from "@/lib/format"
import type { ComposeStack, Container, ContainerSparkline } from "@/lib/types"
import { cn } from "@/lib/utils"
import { MiniBar } from "@/components/procs/process-table"
import { PortLink, type ConfirmFn } from "@/components/docker/shared"
import { stateWord } from "@/components/docker/container-cells"
import {
  ContainerRowActions,
  useContainerVerbs,
  type PendingMap,
} from "@/components/docker/container-actions"
import { stackLabel, stackTone } from "@/components/docker/stack-state"
import {
  exitCode,
  stackLane,
  type ServiceLine,
  type StackLine,
} from "@/components/docker/stack-readings"

export type RowsProps = {
  pending: PendingMap
  confirm: ConfirmFn
  act: Parameters<typeof useContainerVerbs>[0]["act"]
  /** Each container's last hour, by name. */
  trends: Map<string, ContainerSparkline>
  /** A stack being deployed from the list, and what it is doing. */
  deploying: Record<string, string>
  collapsed: string[]
  onToggle: (stack: string) => void
  onOpenStack: (stack: string) => void
  onOpenContainer: (id: string, tab?: string) => void
  onDeploy: (stack: string) => void
}

/**
 * The stacks as one table of their containers, each stack a row of its own
 * with its containers under it.
 *
 * A stack was a card with its services as a line of dots under its name, so
 * "which part of the shop is using the memory" had no answer on this page
 * and "is the database up" was a 6px dot beside a word. A container under its
 * stack is a row of readings now — its state and for how long, its processor
 * over the last hour and this second, its memory against its limit, where it
 * answers — the containers page's readings, gathered by the application they
 * belong to. The stack's own row sums them.
 *
 * Wide, the columns are fixed and the service takes what is left, so a long
 * image never pushes the readings or the verbs past the panel's edge; below
 * `xl` the same rows are drawn down rather than across with nothing dropped
 * (§12). Which is drawn is decided once by the page rather than by hidden
 * twins.
 */
export function StackRows({
  lines,
  wide,
  ...rest
}: RowsProps & { lines: StackLine[]; wide: boolean }) {
  const keys = lines.flatMap((l) => [l.stack.name, ...l.lines.map((s) => s.key)])
  const arrived = useArrivals(keys)
  // Against the heaviest listed, as Services' bars are: against the host, a
  // column of 2% containers is a column of empty tracks.
  const heaviest = lines.reduce(
    (top, l) => Math.max(top, ...l.lines.map((s) => s.stat?.memUsage ?? 0)),
    0,
  )
  const now = useNow(30_000)
  const shared = { ...rest, arrived, heaviest, now }

  if (!wide) {
    return (
      <div className="divide-y divide-hairline">
        {lines.map((line) => (
          <NarrowStack key={line.stack.name} line={line} {...shared} />
        ))}
      </div>
    )
  }
  return (
    <Table className="table-fixed" containerClassName="max-h-[calc(100svh-13rem)]">
      <TableHeader className={stickyTableHeader}>
        <TableRow>
          <TableHead>Service</TableHead>
          <TableHead className="w-44 2xl:w-52">State</TableHead>
          <TableHead className="w-36 text-right">CPU</TableHead>
          <TableHead className="w-36 text-right">Memory</TableHead>
          <TableHead className="w-36 2xl:w-48">Ports</TableHead>
          <TableHead className="w-28">
            <span className="sr-only">Actions</span>
          </TableHead>
        </TableRow>
      </TableHeader>
      {lines.map((line) => (
        <WideStack key={line.stack.name} line={line} {...shared} />
      ))}
    </Table>
  )
}

type Shared = RowsProps & { arrived: Set<string>; heaviest: number; now: number }

function stackProducts(line: StackLine) {
  const images = line.stack.services.map((s) => s.image).filter(Boolean)
  return images.length > 0 ? imageProducts(images) : ["docker-compose"]
}

/**
 * One stack in the wide table: its own row, then its containers. A body per
 * stack, so a screen reader hears where one application ends and the next
 * begins, and the lane down the first cell ties the containers to it.
 */
function WideStack({ line, ...shared }: Shared & { line: StackLine }) {
  const { stack } = line
  const open = !shared.collapsed.includes(stack.name)
  const lane = stackLane(stack.name)
  return (
    <TableBody className="[&_tr:last-child]:border-b">
      <TableRow
        data-workspace-item={`stack:${stack.name}`}
        data-workspace-name={stack.name}
        data-stack-row={stack.name}
        className={cn(
          "group bg-surface-sunken/60",
          shared.arrived.has(stack.name) && "animate-rise",
        )}
        onActivate={() => shared.onOpenStack(stack.name)}
      >
        <TableCell className="py-2.5 pl-2" style={{ boxShadow: `inset 3px 0 0 ${lane}` }}>
          <div className="flex w-full min-w-0 items-center gap-2">
            <CollapseToggle stack={stack.name} open={open} onToggle={shared.onToggle} />
            <ProductLogos ids={stackProducts(line)} />
            <div className="min-w-0 pl-1">
              <div className="flex min-w-0 items-center gap-2">
                <RowLink
                  title={stack.name}
                  className="text-title font-semibold"
                  onClick={() => shared.onOpenStack(stack.name)}
                >
                  {stack.name}
                </RowLink>
                <StackTags line={line} />
              </div>
              <p
                className="truncate font-mono text-micro text-muted-foreground"
                title={stack.workingDir}
              >
                {stack.workingDir || "no compose directory"}
              </p>
            </div>
          </div>
        </TableCell>
        <TableCell className="py-2.5">
          <StackState line={line} deploying={shared.deploying[stack.name]} />
        </TableCell>
        <TableCell className="py-2.5 text-right">
          <StackFigure line={line} kind="cpu" />
        </TableCell>
        <TableCell className="py-2.5 text-right">
          <StackFigure line={line} kind="memory" />
        </TableCell>
        <TableCell className="py-2.5">
          <PublishedCount line={line} />
        </TableCell>
        <TableCell className="py-2.5">
          <StackVerbs
            line={line}
            deploying={shared.deploying[stack.name]}
            onDeploy={shared.onDeploy}
            onOpen={shared.onOpenStack}
          />
        </TableCell>
      </TableRow>
      {open &&
        line.lines.map((service) => (
          <ServiceRow key={service.key} line={service} stack={line} lane={lane} {...shared} />
        ))}
    </TableBody>
  )
}

function CollapseToggle({
  stack,
  open,
  onToggle,
}: {
  stack: string
  open: boolean
  onToggle: (stack: string) => void
}) {
  const Chevron = open ? ChevronDown : ChevronRight
  return (
    <button
      type="button"
      aria-expanded={open}
      aria-label={`${open ? "Hide" : "Show"} ${stack}'s containers`}
      onClick={() => onToggle(stack)}
      className="flex size-6 shrink-0 items-center justify-center rounded-md text-muted-foreground focus-ring transition-colors hover:bg-row-hover hover:text-foreground"
    >
      <Chevron className="size-3.5" />
    </button>
  )
}

/** What the stack is beyond its state: a leftover it runs, or no file to act from. */
function StackTags({ line }: { line: StackLine }) {
  const { stack } = line
  return (
    <>
      {stack.orphans.length > 0 && (
        <Tag
          tone="warning"
          title={`${stack.orphans.join(", ")} ${stack.orphans.length === 1 ? "is" : "are"} running under this project name and no longer in the compose file. A deploy removes ${stack.orphans.length === 1 ? "it" : "them"}.`}
        >
          {plural(stack.orphans.length, "orphan")}
        </Tag>
      )}
      {!stack.managed && (
        <Tag title="No compose file reachable from this dashboard, so this stack is read-only here.">
          read-only
        </Tag>
      )}
    </>
  )
}

/**
 * The declared services that are up. `running` counts containers, so a stack
 * still running a service its file dropped read "3 of 2 services up".
 */
function declaredUp(stack: ComposeStack) {
  return stack.services.filter((s) => s.state === "running" && !stack.orphans.includes(s.name))
    .length
}

/** The stack's state, and under it how much of it is up — or the command running on it. */
function StackState({ line, deploying }: { line: StackLine; deploying?: string }) {
  const { stack } = line
  const unhealthy = line.lines.filter((s) => s.health === "unhealthy").length
  const detail = !stack.deployed
    ? `${plural(stack.total, "service")} defined`
    : `${declaredUp(stack)} of ${plural(stack.total, "service")} up`
  return (
    <div className="min-w-0">
      {deploying ? (
        <span className="inline-flex items-center gap-1.5 text-xs font-medium">
          <StatusDot tone="notice" />
          <TextShimmer>{`${deploying}…`}</TextShimmer>
        </span>
      ) : (
        <Status
          tone={stackTone(stack.state)}
          live={stack.state === "running"}
          label={stackLabel(stack.state)}
        />
      )}
      <p className="numeric truncate text-hint text-muted-foreground">
        {detail}
        {unhealthy > 0 && <span className="text-destructive"> · {unhealthy} unhealthy</span>}
      </p>
    </div>
  )
}

/**
 * A stack's sum, as plain as its containers' figures under it: a counting
 * figure waits to be scrolled into view, and a table's rows mostly are not.
 * Nothing for a stack with nothing running.
 */
function StackFigure({ line, kind }: { line: StackLine; kind: "cpu" | "memory" }) {
  if (line.stack.running === 0) return null
  if (kind === "cpu") {
    if (!line.measured) return <span className="font-mono text-muted-foreground/60">—</span>
    return (
      <span
        className="numeric font-mono font-medium"
        title={`${percent(line.cpu)} of one core, summed over its containers`}
      >
        {percent(line.cpu)}
      </span>
    )
  }
  if (line.memory === 0) return <span className="font-mono text-muted-foreground/60">—</span>
  return (
    <span className="numeric font-mono font-medium" title="Summed over its containers">
      {bytes(line.memory)}
    </span>
  )
}

function PublishedCount({ line }: { line: StackLine }) {
  const ports = line.lines.reduce(
    (n, s) => n + (s.state === "running" ? s.service.ports.filter((p) => p.publicPort).length : 0),
    0,
  )
  if (ports === 0) return null
  return (
    <span className="numeric text-hint text-muted-foreground">
      {plural(ports, "port")} published
    </span>
  )
}

/**
 * The one action worth having on the list — an application that is down and
 * should not be — and a menu of the ways into the stack. Everything else
 * needs the stack's page, where the output is and a deploy can be previewed.
 */
function StackVerbs({
  line,
  deploying,
  onDeploy,
  onOpen,
}: {
  line: StackLine
  deploying?: string
  onDeploy: (stack: string) => void
  onOpen: (stack: string) => void
}) {
  const { can } = useAuth()
  const { stack } = line
  const canDeploy =
    can("system.admin") && can("service.control") && stack.managed && stack.state !== "running"
  return (
    <span
      className="flex shrink-0 items-center justify-end gap-1"
      aria-busy={deploying ? true : undefined}
    >
      {canDeploy && (
        <Button
          size="xs"
          variant={stack.deployed ? "outline" : "default"}
          onClick={() => onDeploy(stack.name)}
          pending={!!deploying}
        >
          <Play className="size-3" />
          Deploy
        </Button>
      )}
      <StackMenu line={line} onOpen={onOpen} />
    </span>
  )
}

/** The row's overflow: the things that are navigation rather than an action. */
function StackMenu({ line, onOpen }: { line: StackLine; onOpen: (stack: string) => void }) {
  const { can } = useAuth()
  const { stack } = line
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          size="icon-sm"
          variant="ghost"
          aria-label={`More actions for ${stack.name}`}
          onClick={(event) => event.stopPropagation()}
          className="[&_svg:not([class*='size-'])]:size-3.5"
        >
          <MoreHorizontal />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-44">
        <DropdownMenuItem onSelect={() => onOpen(stack.name)}>
          <Code className="size-3.5" />
          View
        </DropdownMenuItem>
        {stack.workingDir && (
          <DropdownMenuItem asChild>
            <Link href={`/files?path=${encodeURIComponent(stack.workingDir)}`}>
              <FolderOpen className="size-3.5" />
              Files
            </Link>
          </DropdownMenuItem>
        )}
        {stack.workingDir && can("terminal") && (
          <DropdownMenuItem asChild>
            <Link href={`/terminal?cwd=${encodeURIComponent(stack.workingDir)}`}>
              <Terminal className="size-3.5" />
              Open shell
            </Link>
          </DropdownMenuItem>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

/** The service as the product its image is; a service compose never created keeps a glyph. */
function ServiceMark({ line }: { line: ServiceLine }) {
  if (line.service.missing) return <ProductLogo size="sm" fallback={Layers} />
  const product = line.container
    ? containerProduct(line.container)
    : imageProduct(line.service.image)
  return <ProductLogo id={product} size="sm" />
}

function ServiceRow({
  line,
  stack,
  lane,
  ...shared
}: Shared & { line: ServiceLine; stack: StackLine; lane: string }) {
  const { service } = line
  const id = line.container?.id ?? service.container
  return (
    <TableRow
      data-workspace-item={line.key}
      data-workspace-name={service.name}
      className={cn("group", shared.arrived.has(line.key) && "animate-rise")}
      onActivate={service.missing ? undefined : () => shared.onOpenContainer(id)}
    >
      <TableCell
        className="py-2 pl-2"
        style={{ boxShadow: `inset 3px 0 0 color-mix(in oklab, ${lane} 45%, transparent)` }}
      >
        <div className="flex w-full min-w-0 items-center gap-3 pl-8">
          <ServiceMark line={line} />
          <div className="min-w-0">
            <div className="flex min-w-0 items-center gap-2">
              {service.missing ? (
                <span className="truncate text-body font-medium text-muted-foreground">
                  {service.name}
                </span>
              ) : (
                <RowLink title={service.name} onClick={() => shared.onOpenContainer(id)}>
                  {service.name}
                </RowLink>
              )}
              {line.orphan && (
                <Tag
                  tone="warning"
                  title="Running under this project name with no service in the compose file"
                >
                  orphan
                </Tag>
              )}
            </div>
            <p
              className="truncate font-mono text-micro text-muted-foreground"
              title={service.image || undefined}
            >
              {service.missing ? `declared in ${fileName(stack)}` : service.image}
            </p>
          </div>
        </div>
      </TableCell>
      <TableCell className="py-2">
        <ServiceState line={line} busy={shared.pending[id]} now={shared.now} />
      </TableCell>
      <TableCell className="py-2">
        <CpuCell line={line} trend={trendOf(line, shared.trends)} />
      </TableCell>
      <TableCell className="py-2">
        <MemoryCell line={line} heaviest={shared.heaviest} />
      </TableCell>
      <TableCell className="py-2">
        <Ports line={line} />
      </TableCell>
      <TableCell className="py-2">
        <ServiceActions line={line} {...shared} />
      </TableCell>
    </TableRow>
  )
}

function fileName(line: StackLine) {
  return line.stack.configFiles[0]?.split("/").pop() ?? "the compose file"
}

function trendOf(line: ServiceLine, trends: Map<string, ContainerSparkline>) {
  return line.container ? trends.get(line.container.name)?.cpu : undefined
}

/** A container's verbs; a row whose container the socket has not delivered has none yet. */
function ServiceActions({
  line,
  narrow,
  ...shared
}: Shared & { line: ServiceLine; narrow?: boolean }) {
  return line.container ? (
    <ContainerActions container={line.container} narrow={narrow} {...shared} />
  ) : null
}

function ContainerActions({
  container,
  narrow,
  confirm,
  act,
  onOpenContainer,
}: Shared & { container: Container; narrow?: boolean }) {
  const verbs = useContainerVerbs({
    container,
    confirm,
    act,
    onOpenTab: (tab) => onOpenContainer(container.id, tab),
  })
  return narrow ? (
    <ContainerRowActions verbs={verbs} reveal={false} className="shrink-0" />
  ) : (
    <ContainerRowActions verbs={verbs} reveal={false} dim className="justify-end" />
  )
}

/**
 * The container's state as a word, and under it how long it has been in it:
 * up for how long and whether its check passes, how it exited and when, or
 * that compose never created it. A verb in flight shimmers, because it is
 * happening.
 */
function ServiceState({
  line,
  busy,
  now,
  inline,
}: {
  line: ServiceLine
  busy?: string
  now: number
  inline?: boolean
}) {
  const { service, container } = line
  let word: string
  let tone: DotTone
  let detail: React.ReactNode = null
  if (service.missing) {
    word = "Not created"
    tone = "unknown"
    detail = "nothing running"
  } else if (line.state === "running") {
    // Docker says running; a container failing its own check is not serving.
    word = line.health === "unhealthy" ? "Unhealthy" : stateWord("running")
    tone = line.health === "unhealthy" ? "danger" : "running"
    const started = container?.startedAt ? Date.parse(container.startedAt) : undefined
    const up = started ? (now - started) / 1000 : container?.uptimeSeconds
    detail = (
      <>
        {up ? `up ${duration(up)}` : "up"}
        {line.health && (
          <span
            className={cn(
              line.health === "unhealthy" && "text-destructive",
              line.health === "starting" && "text-warning",
            )}
          >
            {" "}
            · {line.health === "unhealthy" ? "failing its check" : line.health}
          </span>
        )}
      </>
    )
  } else {
    const status = container?.status ?? service.status
    const code = exitCode(status)
    word = stateWord(line.state)
    tone =
      code !== undefined && code !== 0
        ? "danger"
        : line.state === "restarting"
          ? "warning"
          : "stopped"
    const when = status.replace(/^(Up|Exited|Created|Restarting|Paused|Dead)\s*(\(\d+\))?\s*/i, "")
    detail =
      code !== undefined ? (
        <span className={cn(code !== 0 && "text-destructive/80")}>
          {code === 137 ? "killed · exit 137" : `exit ${code}`}
          {when && ` · ${when}`}
        </span>
      ) : (
        when || null
      )
  }

  const status = busy ? (
    <span className="inline-flex items-center gap-1.5 text-xs font-medium">
      <StatusDot tone="notice" />
      <TextShimmer>{`${busy}…`}</TextShimmer>
    </span>
  ) : (
    <Status tone={tone} live={line.state === "running" && !service.missing} label={word} />
  )
  if (inline) {
    return (
      <span className="inline-flex min-w-0 items-center gap-2">
        {status}
        {detail && <span className="truncate text-muted-foreground">{detail}</span>}
      </span>
    )
  }
  return (
    <div className="min-w-0">
      {status}
      {detail && <p className="numeric truncate text-hint text-muted-foreground">{detail}</p>}
    </div>
  )
}

/**
 * The processor: the last hour as a line, then this second's share of one
 * core. Against a quota where the container has one, which is when a figure
 * can be too high; Docker's percent of one core otherwise, in the
 * measurement's hue.
 */
function CpuCell({ line, trend }: { line: ServiceLine; trend?: number[] }) {
  const stat = line.stat
  if (line.state !== "running" || !stat || stat.cpuReady === false) {
    return <p className="text-right font-mono text-muted-foreground/60">—</p>
  }
  const limit = line.container?.cpuLimit ?? stat.cpuLimit ?? 0
  const ofLimit = limit > 0 ? stat.cpuPercent / limit : 0
  const hot = limit > 0 && ofLimit >= 85
  return (
    <div
      className="min-w-0 text-right"
      title={
        limit > 0
          ? `${percent(stat.cpuPercent)} of one core — ${percent(ofLimit)} of its ${plural(limit, "core")}`
          : `${percent(stat.cpuPercent)} of one core; no CPU limit`
      }
    >
      <div className="flex items-center justify-end gap-2">
        {trend && trend.length > 1 && (
          <Sparkline
            values={trend}
            width={44}
            height={14}
            color={hot ? "var(--warning)" : HUE.cpu}
            label="CPU over the last hour"
            className="shrink-0"
          />
        )}
        <span
          className={cn(
            "numeric w-13 text-right font-mono",
            hot ? "text-warning" : "text-foreground",
          )}
        >
          {percent(stat.cpuPercent)}
        </span>
      </div>
      {limit > 0 && (
        <p className="numeric text-micro text-muted-foreground">of {plural(limit, "core")}</p>
      )}
    </div>
  )
}

/** Memory against the container's limit where it has one, and against the heaviest row where not. */
function MemoryCell({ line, heaviest }: { line: ServiceLine; heaviest: number }) {
  const stat = line.stat
  if (line.state !== "running" || !stat) {
    return <p className="text-right font-mono text-muted-foreground/60">—</p>
  }
  const limited = stat.memLimited || (line.container?.memoryLimit ?? 0) > 0
  const limit = line.container?.memoryLimit || stat.memLimit
  const share = limited ? stat.memPercent : heaviest > 0 ? (stat.memUsage / heaviest) * 100 : 0
  const near = limited && stat.memPercent >= 85
  return (
    <div
      className="min-w-0 text-right"
      title={
        limited ? `${percent(stat.memPercent)} of its ${bytes(limit)} limit` : "No memory limit"
      }
    >
      <div className="flex items-center justify-end gap-2">
        <MiniBar value={share} color={near ? "var(--warning)" : HUE.mem} />
        <span className={cn("numeric w-16 text-right font-mono", near && "text-warning")}>
          {bytes(stat.memUsage)}
        </span>
      </div>
      {limited && (
        <p className={cn("numeric text-micro", near ? "text-warning" : "text-muted-foreground")}>
          {percent(stat.memPercent, 0)} of {bytes(limit, 0)}
        </p>
      )}
    </div>
  )
}

/** Where the container answers; the first two, and how many more. */
function Ports({ line, max = 2 }: { line: ServiceLine; max?: number }) {
  const ports = line.service.ports.filter((p) => p.publicPort)
  if (ports.length === 0 || line.state !== "running") return null
  return (
    <span className="flex min-w-0 flex-wrap items-center gap-1">
      {ports.slice(0, max).map((p, i) => (
        <PortLink key={i} ip={p.ip} port={p.publicPort ?? 0} target={p.privatePort} />
      ))}
      {ports.length > max && (
        <span className="numeric text-micro text-muted-foreground">+{ports.length - max}</span>
      )}
    </span>
  )
}

/**
 * A stack drawn down rather than across: its own line, then its containers,
 * each a row with its state, its readings and where it answers under its name.
 * Still rows rather than cards — a hairline to the next, a wash under the
 * pointer — and the title is the real button.
 */
function NarrowStack({ line, ...shared }: Shared & { line: StackLine }) {
  const { stack } = line
  const open = !shared.collapsed.includes(stack.name)
  const lane = stackLane(stack.name)
  return (
    <section
      aria-label={stack.name}
      className={cn("py-2", shared.arrived.has(stack.name) && "animate-rise")}
    >
      <div
        data-workspace-item={`stack:${stack.name}`}
        data-workspace-name={stack.name}
        data-stack-row={stack.name}
        className="group flex min-w-0 items-center gap-2 px-3 py-2 transition-colors hover:bg-row-hover"
        style={{ boxShadow: `inset 3px 0 0 ${lane}` }}
        onClick={(event) => {
          if ((event.target as HTMLElement).closest("a, button, [role='menuitem']")) return
          shared.onOpenStack(stack.name)
        }}
      >
        <CollapseToggle stack={stack.name} open={open} onToggle={shared.onToggle} />
        <ProductLogos ids={stackProducts(line)} />
        <div className="min-w-0 flex-1 pl-1">
          <div className="flex min-w-0 items-center gap-2">
            <RowLink
              className="text-title font-semibold"
              onClick={() => shared.onOpenStack(stack.name)}
            >
              {stack.name}
            </RowLink>
            <StackTags line={line} />
          </div>
          <div className="mt-0.5 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-0.5 text-hint">
            {shared.deploying[stack.name] ? (
              <TextShimmer>{`${shared.deploying[stack.name]}…`}</TextShimmer>
            ) : (
              <Status
                tone={stackTone(stack.state)}
                live={stack.state === "running"}
                label={stackLabel(stack.state)}
              />
            )}
            <span className="numeric text-muted-foreground">
              {stack.deployed
                ? `${declaredUp(stack)}/${stack.total} up`
                : `${plural(stack.total, "service")} defined`}
            </span>
            {stack.running > 0 && line.measured && (
              <span className="numeric font-mono text-muted-foreground">
                {percent(line.cpu)} · {bytes(line.memory)}
              </span>
            )}
          </div>
        </div>
        <StackVerbs
          line={line}
          deploying={shared.deploying[stack.name]}
          onDeploy={shared.onDeploy}
          onOpen={shared.onOpenStack}
        />
      </div>
      {open && line.lines.length > 0 && (
        <ul
          className="ml-3 divide-y divide-hairline"
          style={{ boxShadow: `inset 3px 0 0 color-mix(in oklab, ${lane} 45%, transparent)` }}
        >
          {line.lines.map((service) => (
            <NarrowService key={service.key} line={service} stack={line} {...shared} />
          ))}
        </ul>
      )}
    </section>
  )
}

function NarrowService({
  line,
  stack,
  ...shared
}: Shared & { line: ServiceLine; stack: StackLine }) {
  const { service } = line
  const id = line.container?.id ?? service.container
  const stat = line.stat
  const trend = trendOf(line, shared.trends)
  return (
    <li
      data-workspace-item={line.key}
      data-workspace-name={service.name}
      className={cn(
        "group flex min-w-0 items-start gap-3 py-2.5 pr-1 pl-4 transition-colors hover:bg-row-hover",
        shared.arrived.has(line.key) && "animate-rise",
      )}
      onClick={(event) => {
        if (service.missing) return
        if ((event.target as HTMLElement).closest("a, button, [role='menuitem']")) return
        shared.onOpenContainer(id)
      }}
    >
      <ServiceMark line={line} />
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 items-center gap-2">
          {service.missing ? (
            <span className="truncate text-body font-medium text-muted-foreground">
              {service.name}
            </span>
          ) : (
            <RowLink onClick={() => shared.onOpenContainer(id)}>{service.name}</RowLink>
          )}
          {line.orphan && <Tag tone="warning">orphan</Tag>}
        </div>
        <p className="truncate font-mono text-micro text-muted-foreground">
          {service.missing ? `declared in ${fileName(stack)}` : service.image}
        </p>
        <div className="mt-1.5 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-hint">
          <ServiceState line={line} busy={shared.pending[id]} now={shared.now} inline />
          {line.state === "running" && stat && (
            <span className="numeric inline-flex items-center gap-1.5 font-mono whitespace-nowrap text-muted-foreground">
              {trend && trend.length > 1 && (
                <Sparkline
                  values={trend}
                  width={36}
                  height={12}
                  color={HUE.cpu}
                  label="CPU over the last hour"
                />
              )}
              {stat.cpuReady === false ? "measuring" : `${percent(stat.cpuPercent)} CPU`}
              {` · ${bytes(stat.memUsage)}`}
            </span>
          )}
        </div>
        <div className="mt-1.5 empty:hidden">
          <Ports line={line} max={3} />
        </div>
      </div>
      <ServiceActions line={line} narrow {...shared} />
    </li>
  )
}
