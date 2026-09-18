"use client"

import { Layers, Warning } from "@/components/icons"
import { bytes, percent } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { Container, ContainerSparkline, ContainerStats, DockerDiagnosis } from "@/lib/types"
import { Meter, utilisationTone } from "@/components/meter"
import { ROW_BLEED } from "@/components/row-list"
import { Sparkline } from "@/components/metrics/sparkline"
import { Status } from "@/components/status-dot"
import { PortList } from "@/components/docker/exposure"
import { ContainerRowActions, type ContainerVerb } from "@/components/docker/container-actions"
import { statusDetail, statusWord } from "@/components/docker/container-cells"

/**
 * One container on a screen too narrow for a table.
 *
 * The table used to narrow by dropping columns, which is the right answer until
 * it is not. What survived on a phone was `Container | Status | Issues` — three
 * of nine — and that renders as a wide left cell, a wedge of empty space and two
 * stubs: a table somebody is reading the remains of rather than something built
 * for the screen they are holding.
 *
 * So below `xl` the same data is laid out down the row instead of across it, and
 * *nothing is dropped*: the image, the ports, the CPU and memory readings and
 * the issue count are all here, because a phone is where somebody checks whether
 * the thing they just deployed is alive, and every one of those is part of that
 * answer. The boundary is 1280 rather than 1024 because of the sidebar — at
 * 1024 the table appeared already scrolling inside its own panel.
 *
 * It is a row and not a card, in the design system's sense: no frame of its
 * own, a hairline between it and the next one, a wash under the pointer. A list
 * of bordered cards inside a bordered panel is the stacking the 0.6.7 pass
 * removed everywhere else, and a phone is the screen where it costs the most —
 * two nested frames eat sixteen pixels of a 390px viewport to say nothing.
 */

/** Anything inside the row that owns its own press — mirrors `TableRow`. */
const INTERACTIVE = "a, button, input, select, textarea, label, [role='menuitem']"

export function ContainerCard({
  container,
  stat,
  trend,
  diagnosis,
  verbs,
  pending,
  onOpen,
}: {
  container: Container
  stat?: ContainerStats
  trend?: ContainerSparkline
  diagnosis?: DockerDiagnosis
  verbs: ContainerVerb[]
  /** "Stopping", "Restarting" — the action in flight on this container. */
  pending?: string
  onOpen: () => void
}) {
  const findings = (diagnosis?.findings ?? []).filter((f) => f.targetId === container.id)
  const issues = findings.filter((f) => f.severity === "critical" || f.severity === "warning")
  const critical = issues.some((f) => f.severity === "critical")
  const running = container.state === "running"

  return (
    <li>
      {/*
        The row is a convenient click target, not a control.
        It was `role="button" tabIndex={0}`, which is the obvious way to make a
        whole card pressable and is wrong here: an ARIA button takes its
        accessible name from its contents, so a screen reader announced this one
        as "web nginx:alpine Running for 2 hours healthy CPU 12% Memory 100 MB
        443 1 issue More actions, button" — one control named after everything
        inside it, including the label of the menu button nested in it.

        The container's name is a real `<button>` instead. That is what the
        keyboard reaches and what a screen reader announces; the surrounding
        click handler is a convenience for the pointer, and skips any press that
        landed on a control of its own.
      */}
      <div
        aria-busy={pending ? true : undefined}
        onClick={(event) => {
          if ((event.target as HTMLElement).closest(INTERACTIVE)) return
          onOpen()
        }}
        className={cn(
          "group min-w-0 space-y-2 px-4 py-3 transition-colors hover:bg-row-hover",
          ROW_BLEED,
          pending && "opacity-70",
        )}
      >
        {/*
          Identity and verbs share the top line, and everything measured gets
          the row's full width underneath. They were side by side to begin with,
          which cost the readings ninety pixels — enough that `100 MB / 512 MB`
          was truncated to `100.0…` on a 390px screen, which is a memory figure
          rendered as no memory figure at all.
        */}
        <div className="flex min-w-0 items-start gap-2">
          <div className="min-w-0 flex-1">
            <button
              type="button"
              onClick={onOpen}
              className="block max-w-full truncate rounded-sm text-left text-body leading-tight font-medium focus-ring hover:text-primary"
            >
              {container.name}
            </button>
            <p className="mt-0.5 flex min-w-0 items-center gap-1.5">
              <span className="truncate font-mono text-hint text-muted-foreground">
                {container.image}
              </span>
              {container.composeStack && (
                <>
                  <Layers className="size-3 shrink-0 text-muted-foreground" aria-hidden />
                  <span className="shrink-0 truncate text-hint text-muted-foreground">
                    {container.composeStack}
                  </span>
                </>
              )}
            </p>
          </div>
          <ContainerRowActions verbs={verbs} reveal={false} />
        </div>

        {/* The same two lines the wide table's Status column draws, laid out
            along the row instead of stacked — one helper, so a container reads
            identically on a phone and on a desktop. */}
        <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
          <Status
            state={pending ? "restarting" : container.state}
            live={running && !pending}
            label={pending ? `${pending}\u2026` : statusWord(container)}
          />
          {!pending && statusDetail(container) && (
            <span
              className={cn(
                "min-w-0 truncate text-hint",
                container.health === "unhealthy" ? "text-destructive" : "text-muted-foreground",
              )}
            >
              {statusDetail(container)}
            </span>
          )}
        </div>

        {stat && (
          <div className="grid gap-x-4 gap-y-2 sm:grid-cols-2">
            <Reading
              label="CPU"
              value={percent(stat.cpuPercent)}
              meter={cpuShare(stat, container)}
              trend={trend?.cpu}
            />
            <Reading
              label="Memory"
              value={
                <>
                  {bytes(stat.memUsage)}
                  {stat.memLimited && (
                    <span className="text-muted-foreground"> / {bytes(stat.memLimit)}</span>
                  )}
                </>
              }
              meter={stat.memLimited ? stat.memPercent : (stat.memHostPercent ?? 0)}
              hint={stat.memLimited ? undefined : "no limit"}
            />
          </div>
        )}

        <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1.5">
          <PortList ports={container.exposure ?? []} max={2} />
          {issues.length > 0 && (
            <span
              className={cn(
                "inline-flex items-center gap-1 text-hint font-medium",
                critical ? "text-destructive" : "text-warning",
              )}
            >
              <Warning className="size-3" />
              {issues.length} {issues.length === 1 ? "issue" : "issues"}
            </span>
          )}
          {issues.length === 0 && findings.length > 0 && (
            <span className="text-hint text-muted-foreground">
              {findings.length} {findings.length === 1 ? "suggestion" : "suggestions"}
            </span>
          )}
        </div>
      </div>
    </li>
  )
}

/**
 * One live figure with its bar. The label is spelled out because on this
 * layout there is no column header above it to inherit the name from — which
 * is exactly the detail a responsive table loses and nobody notices.
 */
function Reading({
  label,
  value,
  meter,
  hint,
  trend,
}: {
  label: string
  value: React.ReactNode
  meter: number
  hint?: string
  trend?: number[]
}) {
  return (
    <div className="min-w-0">
      <div className="flex min-w-0 items-baseline justify-between gap-2">
        <span className="eyebrow">{label}</span>
        <span className="numeric truncate font-mono text-hint">{value}</span>
      </div>
      <div className="mt-1 flex items-center gap-1.5">
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
      </div>
      {hint && <p className="mt-0.5 text-micro text-muted-foreground">{hint}</p>}
    </div>
  )
}

/** The container's CPU as a share of what it may use, matching `CpuCell`. */
function cpuShare(stat: ContainerStats, container: Container) {
  const limit = container.cpuLimit ?? stat.cpuLimit ?? 0
  return limit > 0 ? (stat.cpuPercent / (limit * 100)) * 100 : stat.cpuPercent
}
