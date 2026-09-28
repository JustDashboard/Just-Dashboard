"use client"

import { Copy, Information, Layers, Warning } from "@/components/icons"
import { bytes, duration, percent } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { Container, ContainerStats, DockerDiagnosis, DockerFinding } from "@/lib/types"
import { HoverCard, HoverCardContent, HoverCardTrigger } from "@/components/ui/hover-card"
import { Meter, utilisationTone } from "@/components/meter"
import { Sparkline } from "@/components/metrics/sparkline"
import { Status } from "@/components/status-dot"
import { copyText } from "@/lib/clipboard"

/**
 * The cells that were saying something other than what they meant.
 *
 * Three of them, all with the same shape of problem — a true number rendered
 * so that it reads as a different, false one:
 *
 *   Memory showed `97 MB / 62.7 GB` for a container with no limit, because
 *   Docker reports host RAM as the limit when there is none. That denominator
 *   is not a budget and the percentage of it means nothing.
 *
 *   CPU showed a percentage with no denominator stated. `docker stats` counts
 *   one core as 100%, so 340% is normal on an 8-core box and looks like an
 *   emergency.
 *
 *   Status carried the worst *finding* under the runtime state, so a healthy
 *   container read "Running" and "publishes PostgreSQL on every interface" in
 *   one cell — a runtime fact and a security opinion in the same column.
 */

/**
 * What a container is made of, as the second line under its name: the image,
 * the stack and service it belongs to, and the id.
 *
 * The name is the thing; the id is a handle. Never concatenated, and the id is
 * one click to copy — it is what every `docker` command wants and there was
 * nowhere in the product to get it.
 */
export function ContainerIdentity({
  container,
  id = true,
}: {
  container: Container
  /** Off where the line is too short to hold the image and the id both. */
  id?: boolean
}) {
  return (
    <span className="flex min-w-0 items-center gap-1.5">
      <span className="min-w-0 truncate font-mono">{container.image}</span>
      {container.composeStack && (
        <span className="flex min-w-0 shrink items-center gap-1 truncate">
          <span aria-hidden>·</span>
          <Layers className="size-3 shrink-0" />
          <span className="truncate">
            {container.composeStack}/{container.composeService}
          </span>
        </span>
      )}
      {id && (
        <>
          <span aria-hidden>·</span>
          <ContainerId container={container} />
        </>
      )}
    </span>
  )
}

/** The short id, one press from the clipboard as the full one. */
export function ContainerId({ container }: { container: Container }) {
  return (
    <button
      type="button"
      onClick={(event) => {
        event.stopPropagation()
        void copyText(container.id, "Container id copied")
      }}
      title="Copy the full container id"
      className="inline-flex shrink-0 items-center gap-1 rounded-sm font-mono focus-ring hover:text-foreground"
    >
      {container.id.slice(0, 12)}
      <Copy className="size-2.5" />
    </button>
  )
}

/**
 * Docker's own status string is a state and a duration welded together — "Up 2
 * hours", "Exited (137) 3 minutes ago" — and reading it back at somebody means
 * the Status column carries a sentence whose first word is the only part the
 * dot beside it is labelling.
 *
 * The word is separated from the rest so the column can be *scanned*: a list of
 * "Running / Running / Stopped" is read down in one pass, and a list of "Up 2
 * hours / Up 13 days / Exited (0) 4 minutes ago" is not. Nothing Docker said is
 * thrown away — the remainder, exit code included, becomes the second line in
 * `statusDetail` below.
 */
export function statusWord(container: Container) {
  return stateWord(container.state)
}

/**
 * The same word for a bare Docker state, for a page that knows the state
 * from somewhere other than Docker's listing — a deployment's runtime.
 */
export function stateWord(state: string) {
  switch (state) {
    case "running":
      return "Running"
    case "paused":
      return "Paused"
    case "restarting":
      return "Restarting"
    case "exited":
      return "Stopped"
    case "created":
      return "Never started"
    case "dead":
      return "Dead"
    case "removing":
      return "Being removed"
    default:
      return state
  }
}

/**
 * The second line: how long, and whether anything is watching.
 *
 * For a running container that is uptime and the health verdict — and "no
 * health check" is said out loud, because a container Docker calls up that
 * nothing is checking is a different claim from one that passed a test a
 * second ago.
 *
 * For anything stopped it is the rest of Docker's own sentence, which is where
 * the exit code lives: "(137) 3 minutes ago" under the word "Stopped" is the
 * most useful pair of facts on the row when something has fallen over, and 137
 * is nearly always the kernel killing it for memory. The leading state word is
 * dropped because the line above it already says that.
 */
export function statusDetail(container: Container): string {
  if (container.state !== "running") {
    return container.status.replace(/^(Up|Exited|Created|Restarting|Paused|Dead)\s*/i, "").trim()
  }
  const parts: string[] = []
  if (container.uptimeSeconds > 0) parts.push(`for ${duration(container.uptimeSeconds)}`)
  if (container.health) parts.push(container.health)
  else if (container.inspected) parts.push("no health check")
  return parts.join(" · ")
}

/**
 * The Status cell: what Docker is doing, and nothing else.
 *
 * It used to carry the worst *finding* about the container underneath the
 * state, so a healthy container read "Running" and "publishes PostgreSQL on
 * every interface" in one cell. Diagnostics have their own column; what belongs
 * on the second line here is the rest of the runtime answer — how long it has
 * been in this state, and whether anything is actually checking that it works.
 *
 * `pending` is the action the operator just pressed. A stop takes ten seconds
 * to honour and the socket reports the old state throughout, so without this
 * the row answers a press by sitting still and then jumping — which is what a
 * broken button also looks like.
 */
export function ContainerStatus({
  container,
  pending,
}: {
  container: Container
  pending?: string
}) {
  const running = container.state === "running"
  const unhealthy = container.health === "unhealthy"
  // A non-breaking space rather than nothing, so a row whose state has no
  // second line is exactly as tall as the one above it. A table whose rows are
  // two different heights for reasons the reader cannot see looks broken.
  const detail = pending ? "\u00a0" : statusDetail(container) || "\u00a0"

  return (
    <>
      <Status
        state={pending ? "restarting" : container.state}
        live={running && !pending}
        label={pending ? `${pending}\u2026` : statusWord(container)}
      />
      <p
        className={cn("mt-0.5 text-hint", unhealthy ? "text-destructive" : "text-muted-foreground")}
      >
        {detail}
      </p>
    </>
  )
}

/**
 * One live figure as a card draws it: the value on the first line, where the
 * eye lands, and on the second what it is — its name, small, in front of the
 * bar it is measured against, or in front of the sentence that says there is
 * nothing to measure it against.
 *
 * The name is spelled out because a card has no column header to inherit one
 * from, which is exactly the detail a table that became cards loses and nobody
 * notices. It sits on the second line rather than beside the value so that
 * `100 MB / 512 MB` keeps the width it needs.
 */
function Reading({
  label,
  value,
  meter,
  note,
  tone,
  trend,
}: {
  label: string
  value: React.ReactNode
  meter?: number
  note?: string
  tone?: "warning"
  trend?: number[]
}) {
  return (
    <span className="block w-full min-w-0 cursor-help text-left">
      <span
        className={cn(
          "numeric block truncate font-mono text-hint",
          tone === "warning" && "text-warning",
        )}
      >
        {value}
      </span>
      <span className="mt-1 flex min-w-0 items-center gap-1.5">
        <span className="shrink-0 text-micro font-medium tracking-[0.06em] text-muted-foreground uppercase">
          {label}
        </span>
        {meter !== undefined ? (
          <>
            <Meter
              value={meter}
              tone={utilisationTone(meter)}
              size="thin"
              label={label}
              className="min-w-0 flex-1"
            />
            {trend && trend.length > 0 && (
              <Sparkline
                values={trend}
                width={36}
                height={12}
                label={`${label} over the last hour`}
                color="var(--chart-1)"
                className="shrink-0 animate-rise"
              />
            )}
          </>
        ) : (
          <span className="min-w-0 truncate text-micro text-muted-foreground">{note}</span>
        )}
      </span>
    </span>
  )
}

/**
 * CPU, with the denominator said out loud.
 *
 * The bar is scaled against the container's own quota where it has one, and
 * against a single core where it does not — which is the unit the number is
 * in. A container allowed two cores and using one and a half is at 75% of what
 * it may have and 150% of a core, and both readings belong on screen.
 */
export function CpuReading({
  stat,
  container,
  trend,
}: {
  stat?: ContainerStats
  container: Container
  trend?: number[]
}) {
  if (!stat) return <Reading label="CPU" value="—" note={unread(container)} />
  const limit = container.cpuLimit ?? stat.cpuLimit ?? 0
  const cores = stat.hostCpus ?? 0
  const ofLimit = limit > 0 ? (stat.cpuPercent / (limit * 100)) * 100 : stat.cpuPercent

  return (
    <HoverCard openDelay={200}>
      <HoverCardTrigger asChild>
        <button type="button" className="block w-full min-w-0 rounded-sm focus-ring">
          <Reading label="CPU" value={percent(stat.cpuPercent)} meter={ofLimit} trend={trend} />
        </button>
      </HoverCardTrigger>
      <HoverCardContent className="w-72 space-y-1 text-xs leading-relaxed">
        <p className="text-body font-medium">{percent(stat.cpuPercent)} of one core</p>
        <p className="text-muted-foreground">
          Docker counts one core as 100%, so a container using two cores fully reads as 200%.
          {cores > 0 &&
            ` This server has ${cores} core${cores === 1 ? "" : "s"}, so ${cores * 100}% is everything it has.`}
        </p>
        <p className="text-muted-foreground">
          {limit > 0
            ? `Limited to ${limit} core${limit === 1 ? "" : "s"}, so it is at ${percent(ofLimit)} of what it may use.`
            : "No CPU limit is set, so it competes with everything else on this server."}
        </p>
      </HoverCardContent>
    </HoverCard>
  )
}

/**
 * Memory, without inventing a limit.
 *
 * When nothing set one, the second line is "no limit" and the share of the
 * host — a fact that exists — rather than host RAM dressed up as a budget.
 */
export function MemoryReading({
  stat,
  container,
}: {
  stat?: ContainerStats
  container: Container
}) {
  if (!stat) return <Reading label="Memory" value="—" note={unread(container)} />

  // `memLimited` is the server's answer, computed by comparing the reported
  // limit against the machine's own memory. `memoryLimit` from the listing
  // agrees with it and is used only when the stats frame predates the field.
  const limited = stat.memLimited || (container.memoryLimit ?? 0) > 0
  const limit = container.memoryLimit || stat.memLimit
  const nearLimit = limited && stat.memPercent >= 85

  return (
    <HoverCard openDelay={200}>
      <HoverCardTrigger asChild>
        <button type="button" className="block w-full min-w-0 rounded-sm focus-ring">
          <Reading
            label="Memory"
            tone={nearLimit ? "warning" : undefined}
            value={
              <>
                {bytes(stat.memUsage)}
                {limited && <span className="text-muted-foreground"> / {bytes(limit)}</span>}
              </>
            }
            meter={limited ? stat.memPercent : undefined}
            note={
              stat.memHostPercent
                ? `no limit · ${percent(stat.memHostPercent)} of host`
                : "no limit"
            }
          />
        </button>
      </HoverCardTrigger>
      <HoverCardContent className="w-72 space-y-1 text-xs leading-relaxed">
        <p className="text-body font-medium">{bytes(stat.memUsage)} in use</p>
        {limited ? (
          <p className="text-muted-foreground">
            Limited to {bytes(limit)}, so it is at {percent(stat.memPercent)} of its allowance. The
            kernel kills it if it goes over, and Docker restarts it as though nothing happened.
          </p>
        ) : (
          <p className="text-muted-foreground">
            No memory limit is set. Docker reports the whole machine as this container&apos;s limit
            when there is none, which is why the figure beside it is a share of the host rather than
            of a budget. Without a limit, the kernel chooses what to kill when memory runs out — and
            it is often not the container that caused the problem.
          </p>
        )}
      </HoverCardContent>
    </HoverCard>
  )
}

/**
 * The diagnostics column: a count, not a sentence.
 *
 * Status says what Docker is doing. This says what the dashboard thinks about
 * it, which is a different question and belongs in a different column — the
 * full explanation is in the Attention panel and the detail view, where there
 * is room for the reasoning.
 */
export function IssuesCell({
  diagnosis,
  containerId,
  onOpen,
}: {
  diagnosis: DockerDiagnosis | undefined
  containerId: string
  onOpen: () => void
}) {
  const mine = (diagnosis?.findings ?? []).filter((f) => f.targetId === containerId)
  const issues = mine.filter((f) => f.severity === "critical" || f.severity === "warning")
  // Everything that is not an issue is a note (recommendations and info).
  // Counting only `recommendation` left info-only containers rendering "0·".
  const notes = mine.filter((f) => f.severity !== "critical" && f.severity !== "warning")

  if (mine.length === 0) {
    return <span className="block text-hint text-muted-foreground">—</span>
  }

  const worst = issues[0] ?? notes[0]
  const hasIssues = issues.length > 0
  const tone = issues.some((f) => f.severity === "critical")
    ? "text-destructive"
    : hasIssues
      ? "text-warning"
      : "text-muted-foreground"

  // Every state carries an icon now. Recommendation-only rows used to render a
  // bare "3·" with no glyph beside rows that had a warning triangle, which is
  // what made the column look broken.
  const Icon = hasIssues ? Warning : Information
  const count = hasIssues ? issues.length : notes.length

  return (
    <HoverCard openDelay={150}>
      <HoverCardTrigger asChild>
        <button
          type="button"
          onClick={(event) => {
            event.stopPropagation()
            onOpen()
          }}
          className={cn(
            "flex items-center gap-1 rounded-sm px-1 text-hint font-medium focus-ring",
            tone,
          )}
        >
          <Icon className="size-3 shrink-0" />
          {count}
        </button>
      </HoverCardTrigger>
      <HoverCardContent className="w-80 space-y-1.5 text-xs leading-relaxed">
        <p className="text-body font-medium">{worst?.title}</p>
        <p className="text-muted-foreground">{worst?.detail}</p>
        {mine.length > 1 && (
          <p className="text-hint text-muted-foreground">
            {issues.length} issue{issues.length === 1 ? "" : "s"} and {notes.length} recommendation
            {notes.length === 1 ? "" : "s"} — open the container for all of them.
          </p>
        )}
      </HoverCardContent>
    </HoverCard>
  )
}

/** The worst thing said about one container, for a compact header. */
export function worstFinding(
  diagnosis: DockerDiagnosis | undefined,
  id: string,
): DockerFinding | undefined {
  return (diagnosis?.findings ?? []).find(
    (f) => f.targetId === id && (f.severity === "critical" || f.severity === "warning"),
  )
}

/** Why a container has no reading: it is not running, or its first one is on the way. */
function unread(container: Container) {
  return container.state === "running" ? "waiting for a reading" : "not running"
}
