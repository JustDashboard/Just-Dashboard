"use client"

import { Warning } from "@/components/icons"
import { cn } from "@/lib/utils"
import type { Container, ContainerSparkline, ContainerStats, DockerDiagnosis } from "@/lib/types"
import { ChoiceRow } from "@/components/flow"
import { Status } from "@/components/status-dot"
import { PortList } from "@/components/docker/exposure"
import { ContainerRowActions, type ContainerVerb } from "@/components/docker/container-actions"
import {
  ContainerId,
  ContainerIdentity,
  ContainerStatus,
  CpuReading,
  IssuesCell,
  MemoryReading,
  statusDetail,
  statusWord,
} from "@/components/docker/container-cells"
import { ProductLogo, imageProduct } from "@/components/product-logo"

/**
 * One container, as a card you open — at every width.
 *
 * It replaced a nine-column table on the desktop and a hairlined list on a
 * phone, and the argument is the one `git/repo-card.tsx` made for checkouts:
 * every row here is a place to go (the container's own page), so it is a
 * choice and carries the lit edge §16 gives to things you take. The readings
 * did not become less of a reading by moving into a card; what the table spent
 * its header on — naming nine columns over two rows — each reading now says
 * for itself, in a fixed measure held to the right, so a column of cards is
 * still scanned down the way the table was.
 *
 * Two shapes of one card. From `xl` the name and what it is made of take the
 * slack and the readings sit beside them — status, CPU with its last hour,
 * memory, ports and issues. Below it the same readings go
 * beneath the name at the card's full width, and *nothing is dropped*: a phone
 * is where somebody checks whether the thing they just deployed is alive, and
 * every one of these is part of that answer.
 *
 * The mark is the image's product — nginx, Postgres, n8n — because a list of
 * thirty containers is scanned for the one running a thing, and a column of
 * logos is found before a column of names is read.
 */
export function ContainerCard({
  container,
  stat,
  trend,
  diagnosis,
  verbs,
  pending,
  wide,
  onOpen,
  onOpenIssues,
}: {
  container: Container
  stat?: ContainerStats
  trend?: ContainerSparkline
  diagnosis?: DockerDiagnosis
  verbs: ContainerVerb[]
  /** "Stopping", "Restarting" — the action in flight on this container. */
  pending?: string
  /**
   * Which of the two shapes to draw. Chosen by the page once rather than by a
   * pair of `hidden`/`xl:block` twins, so nothing is in the document twice —
   * a reading that exists in a hidden copy is two answers to every query.
   */
  wide: boolean
  onOpen: () => void
  onOpenIssues: () => void
}) {
  const findings = (diagnosis?.findings ?? []).filter((f) => f.targetId === container.id)
  const issues = findings.filter((f) => f.severity === "critical" || f.severity === "warning")
  const critical = issues.some((f) => f.severity === "critical")
  const running = container.state === "running"
  const detail = pending ? undefined : statusDetail(container)

  return (
    <ChoiceRow
      // The name is the control's name: the row is the container, and every
      // other page that links here calls it by it.
      verb={container.name}
      onSelect={onOpen}
      className={cn(pending && "opacity-70")}
      leading={<ProductLogo id={imageProduct(container.image)} size="sm" />}
      title={container.name}
      description={<ContainerIdentity container={container} id={wide} />}
      trailing={
        wide ? (
          // Each reading in its own fixed measure, so a column of cards lines
          // up the way the table's columns did.
          <>
            <span className="w-32 min-w-0" aria-busy={pending ? true : undefined}>
              <ContainerStatus container={container} pending={pending} />
            </span>
            <span className="w-24">
              <CpuReading stat={stat} container={container} trend={trend?.cpu} />
            </span>
            <span className="w-36">
              <MemoryReading stat={stat} container={container} />
            </span>
            <span className="w-32 min-w-0">
              <PortList ports={container.exposure ?? []} max={1} />
            </span>
            <span className="w-9">
              <IssuesCell diagnosis={diagnosis} containerId={container.id} onOpen={onOpenIssues} />
            </span>
          </>
        ) : (
          // Narrow, the state stays beside the name: it is the one reading
          // that decides whether the rest are worth looking at.
          <Status
            state={pending ? "restarting" : container.state}
            live={running && !pending}
            label={pending ? `${pending}\u2026` : statusWord(container)}
          />
        )
      }
      actions={<ContainerRowActions verbs={verbs} reveal={false} dim />}
    >
      {!wide && (
        <div className="min-w-0 space-y-2.5">
          <p className="flex min-w-0 items-center gap-2 text-hint text-muted-foreground">
            {detail && (
              <span
                className={cn(
                  "min-w-0 truncate",
                  container.health === "unhealthy" && "text-destructive",
                )}
              >
                {detail}
              </span>
            )}
            <ContainerId container={container} />
          </p>
          {stat && (
            <div className="grid gap-x-4 gap-y-2 sm:grid-cols-2">
              <CpuReading stat={stat} container={container} trend={trend?.cpu} />
              <MemoryReading stat={stat} container={container} />
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
      )}
    </ChoiceRow>
  )
}
