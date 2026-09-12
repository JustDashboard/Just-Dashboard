"use client"

import { Copy, Layers, Warning } from "@/components/icons"
import { bytes, percent } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { Container, ContainerStats, DockerDiagnosis, DockerFinding } from "@/lib/types"
import { HoverCard, HoverCardContent, HoverCardTrigger } from "@/components/ui/hover-card"
import { Meter } from "@/components/meter"
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
 * The name is the thing; the id is a handle. Never concatenated, and the id is
 * one click to copy — it is what every `docker` command wants and there was
 * nowhere in the product to get it.
 */
export function ContainerName({ container, onOpen }: { container: Container; onOpen: () => void }) {
  const copy = (event: React.MouseEvent) => {
    event.stopPropagation()
    void copyText(container.id, "Container id copied")
  }

  return (
    <div className="max-w-[20rem] min-w-0">
      <button
        type="button"
        onClick={onOpen}
        className="max-w-full truncate rounded-sm text-left text-body font-medium focus-ring hover:text-primary"
      >
        {container.name}
      </button>
      <p className="flex min-w-0 items-center gap-1.5 text-hint text-muted-foreground">
        <button
          type="button"
          onClick={copy}
          title="Copy the full container id"
          className="inline-flex shrink-0 items-center gap-1 rounded-sm font-mono focus-ring hover:text-foreground"
        >
          {container.id.slice(0, 12)}
          <Copy className="size-2.5" />
        </button>
        {container.composeStack && (
          <span className="flex min-w-0 items-center gap-1 truncate">
            <span aria-hidden>·</span>
            <Layers className="size-3 shrink-0" />
            <span className="truncate">
              {container.composeStack}/{container.composeService}
            </span>
          </span>
        )}
      </p>
      {/* The image has its own column from md up, where there is room for it.
          Below that the column is dropped rather than squeezed, so the name
          cell carries it — what a container is running is not an optional
          detail on a phone. */}
      <p className="truncate font-mono text-hint text-muted-foreground md:hidden">
        {container.image}
      </p>
    </div>
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
export function CpuCell({ stat, container }: { stat?: ContainerStats; container: Container }) {
  if (!stat) return <Muted />
  const limit = container.cpuLimit ?? stat.cpuLimit ?? 0
  const cores = stat.hostCpus ?? 0
  const ofLimit = limit > 0 ? (stat.cpuPercent / (limit * 100)) * 100 : stat.cpuPercent

  return (
    <HoverCard openDelay={200}>
      <HoverCardTrigger asChild>
        <button type="button" className="flex w-full cursor-help items-center justify-end gap-2">
          <Meter value={ofLimit} size="thin" className="w-10" label="CPU of limit" />
          <span className="numeric w-12 text-right font-mono text-hint">
            {percent(stat.cpuPercent)}
          </span>
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
export function MemoryCell({ stat, container }: { stat?: ContainerStats; container: Container }) {
  if (!stat) return <Muted />

  // `memLimited` is the server's answer, computed by comparing the reported
  // limit against the machine's own memory. `memoryLimit` from the listing
  // agrees with it and is used only when the stats frame predates the field.
  const limited = stat.memLimited || (container.memoryLimit ?? 0) > 0
  const limit = container.memoryLimit || stat.memLimit
  const nearLimit = limited && stat.memPercent >= 85

  return (
    <HoverCard openDelay={200}>
      <HoverCardTrigger asChild>
        <button type="button" className="w-full cursor-help text-right">
          <span className={cn("numeric block font-mono text-hint", nearLimit && "text-warning")}>
            {bytes(stat.memUsage)}
            {limited && <span className="text-muted-foreground"> / {bytes(limit)}</span>}
          </span>
          <span className="block text-micro text-muted-foreground">
            {limited
              ? percent(stat.memPercent)
              : stat.memHostPercent
                ? `no limit · ${percent(stat.memHostPercent)} of host`
                : "no limit"}
          </span>
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
  const recommendations = mine.filter((f) => f.severity === "recommendation")

  if (mine.length === 0) {
    return <span className="block text-center text-hint text-muted-foreground">—</span>
  }

  const worst = issues[0] ?? recommendations[0]
  const tone = issues.some((f) => f.severity === "critical")
    ? "text-destructive"
    : issues.length
      ? "text-warning"
      : "text-muted-foreground"

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
            "mx-auto flex items-center gap-1 rounded-sm px-1 text-hint font-medium focus-ring",
            tone,
          )}
        >
          {issues.length > 0 && <Warning className="size-3" />}
          {issues.length > 0 ? issues.length : `${recommendations.length}·`}
        </button>
      </HoverCardTrigger>
      <HoverCardContent className="w-80 space-y-1.5 text-xs leading-relaxed">
        <p className="text-body font-medium">{worst?.title}</p>
        <p className="text-muted-foreground">{worst?.detail}</p>
        {mine.length > 1 && (
          <p className="text-hint text-muted-foreground">
            {issues.length} issue{issues.length === 1 ? "" : "s"} and {recommendations.length}{" "}
            recommendation
            {recommendations.length === 1 ? "" : "s"} — open the container for all of them.
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

function Muted() {
  return <span className="text-hint text-muted-foreground">—</span>
}
