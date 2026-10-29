"use client"

import { useCallback, useState } from "react"
import Link from "next/link"
import { get, put } from "@/lib/api"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type { DeploymentEnvironmentConfiguration } from "@/lib/types"
import type { Tone } from "@/components/tone"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Status } from "@/components/status-dot"
import { InfoTip } from "@/components/form"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import {
  CPU_RULE,
  MEMORY_RULE,
  VERDICT_LABEL,
  applyLines,
  applyToRuntime,
  cores,
  cpuReadings,
  fleetSummary,
  historyLine,
  isOff,
  memoryReadings,
  mib,
  verdictTone,
  type EnvironmentSizing,
  type FleetSizing,
  type SizingVerdict,
} from "@/components/deploy/sizing-model"

/**
 * Applying a suggestion is the configuration write Runtime settings make:
 * read the environment's configuration, put the suggested limits into its
 * runtime and save the whole of it back with the revision it read. The same
 * route, the same audit, the same validation and the same "applies with the
 * next release" — a stale read is refused as any settings save is.
 */
async function applySuggestedLimits(sizing: EnvironmentSizing) {
  if (!sizing.apply) return
  const path = `/deploy/${sizing.projectId}/environments/${sizing.environmentId}/configuration`
  const configuration = await get<DeploymentEnvironmentConfiguration>(path)
  await put<DeploymentEnvironmentConfiguration>(path, {
    revision: configuration.revision,
    build: configuration.build,
    runtime: applyToRuntime(configuration.runtime, sizing.apply),
    dependencies: configuration.dependencies,
    checks: configuration.checks,
    domains: configuration.domains,
  })
}

/** The apply verb, one environment at a time, with its toast. */
function useApplySizing(onApplied: () => void) {
  const [applying, setApplying] = useState<number>()
  const apply = useCallback(
    async (sizing: EnvironmentSizing) => {
      setApplying(sizing.environmentId)
      try {
        await applySuggestedLimits(sizing)
        notify.success(`Saved the suggested limits for ${sizing.projectName}`, {
          description: `${applyLines(sizing).join(", ")}. They apply with the next release.`,
        })
        onApplied()
      } catch (error) {
        notify.error("Could not save the suggested limits", error)
      } finally {
        setApplying(undefined)
      }
    },
    [onApplied],
  )
  return { applying, apply }
}

function tileTone(verdict: SizingVerdict): Tone {
  if (verdict === "too_tight") return "danger"
  if (verdict === "oversized") return "warning"
  return "default"
}

/**
 * The project overview's Right-size card: the verdict and the one verb in the
 * head, each limit's suggestion as a figure with the readings it came from
 * under it, and why. A Compose stack has nothing in its plan to size and a
 * read that fails has nothing to say, so neither draws a card.
 */
export function RightSizeCard({
  projectId,
  environmentId,
  onApplied,
}: {
  projectId: number
  environmentId: number
  onApplied?: () => void
}) {
  const { can } = useAuth()
  const state = usePoll(
    (signal) =>
      get<EnvironmentSizing>(
        `/deploy/${projectId}/environments/${environmentId}/sizing`,
        undefined,
        signal,
      ),
    300_000,
    [projectId, environmentId],
    { enabled: projectId > 0 && environmentId > 0 },
  )
  const { refresh } = state
  const applied = useCallback(() => {
    refresh()
    onApplied?.()
  }, [refresh, onApplied])
  const { applying, apply } = useApplySizing(applied)
  const sizing = state.data
  if (!sizing || (sizing.status !== "available" && sizing.status !== "unavailable")) return null

  const canApply = can("system.admin") && sizing.apply !== null && isOff(sizing)
  return (
    <Panel plain aria-labelledby="right-size-title" className="animate-rise">
      <PanelHeader
        title={<span id="right-size-title">Right-size</span>}
        actions={
          <>
            {sizing.status === "available" && (
              <Status tone={verdictTone(sizing.verdict)} label={VERDICT_LABEL[sizing.verdict]} />
            )}
            {canApply && (
              <Button size="sm" disabled={applying !== undefined} onClick={() => apply(sizing)}>
                {applying !== undefined ? "Saving…" : "Apply suggested limits"}
              </Button>
            )}
          </>
        }
      />
      <PanelBody>
        <SizingBody sizing={sizing} />
      </PanelBody>
    </Panel>
  )
}

function SizingBody({ sizing }: { sizing: EnvironmentSizing }) {
  if (sizing.status === "unavailable")
    return <p className="text-body text-muted-foreground">{sizing.reason}</p>
  const { memory, cpu } = sizing
  if (!memory || !cpu)
    return (
      <p className="text-body text-muted-foreground">
        Not enough history yet: {historyLine(sizing.window)}.{sizing.reason && ` ${sizing.reason}`}
      </p>
    )
  const reasons = [
    ...(memory.verdict === "right" ? [] : memory.reasons.slice(0, 1)),
    ...(cpu.verdict === "right" ? [] : cpu.reasons.slice(0, 1)),
  ]
  return (
    <div className="min-w-0 space-y-3">
      {/* Not dense: on a phone each limit takes the width, and its readings
          line is read whole rather than cut at the limit. */}
      <StatGrid columns={2}>
        <StatTile
          label={
            <span className="inline-flex items-center gap-1.5">
              Memory{" "}
              <InfoTip label="How the memory suggestion is worked out">{MEMORY_RULE}</InfoTip>
            </span>
          }
          value={mib(memory.suggestedMb)}
          trailing={memory.verdict === "right" ? "fits" : "suggested"}
          tone={tileTone(memory.verdict)}
          hint={memoryReadings(memory)}
        />
        <StatTile
          label={
            <span className="inline-flex items-center gap-1.5">
              CPU <InfoTip label="How the CPU suggestion is worked out">{CPU_RULE}</InfoTip>
            </span>
          }
          value={cores(cpu.suggested)}
          trailing={cpu.verdict === "right" ? "fits" : "suggested"}
          tone={tileTone(cpu.verdict)}
          hint={cpuReadings(cpu)}
        />
      </StatGrid>
      {reasons.length > 0 && (
        <ul className="space-y-1 text-body">
          {reasons.map((reason) => (
            <li key={reason}>{reason}</li>
          ))}
        </ul>
      )}
      <p className="text-hint text-muted-foreground">
        From {historyLine(sizing.window)}: the 95th percentile of what it used, plus headroom
        {memory.oomKills > 0 || !sizing.oom.watching
          ? ""
          : ", and no OOM kill of the live release since the event log began listening"}
        .{sizing.pending && " The saved limits apply with the next release."}
      </p>
    </div>
  )
}

/**
 * The fleet's Right-size list: the environments whose limits are off, the
 * furthest off first, each with what an apply would change and the verb.
 * What is fine or still gathering history is a count in the head, not rows;
 * a fleet with nothing off draws nothing.
 */
export function FleetRightSize() {
  const { can } = useAuth()
  const state = usePoll(
    (signal) => get<FleetSizing>("/deploy/sizing", undefined, signal),
    300_000,
    [],
  )
  const { applying, apply } = useApplySizing(state.refresh)
  const environments = state.data?.environments ?? []
  const off = environments.filter(isOff)
  if (state.data?.status !== "available" || off.length === 0) return null
  const admin = can("system.admin")
  const summary = fleetSummary(environments)
  return (
    <Panel plain aria-labelledby="fleet-right-size-title" className="animate-rise">
      <PanelHeader
        title={<span id="fleet-right-size-title">Right-size</span>}
        actions={
          <span className="numeric text-hint text-muted-foreground">
            {off.length} to resize{summary && ` · ${summary}`}
          </span>
        }
      />
      <PanelBody>
        <RowList aria-label="Environments to resize">
          {off.map((sizing) => (
            <Row
              key={sizing.environmentId}
              title={
                <span className="flex min-w-0 items-baseline gap-2">
                  <Link
                    href={`/deploy/${sizing.projectId}${sizing.environmentKind === "staging" ? `?environment=${sizing.environmentId}` : ""}`}
                    className="truncate rounded-sm focus-ring hover:underline"
                  >
                    {sizing.projectName}
                  </Link>
                  {sizing.environmentKind !== "production" && <Tag>{sizing.environmentName}</Tag>}
                </span>
              }
              subtitle={
                <span className="numeric">
                  {applyLines(sizing).join(" · ")}
                  {(sizing.memory?.reasons[0] || sizing.cpu?.reasons[0]) && (
                    <span className="max-lg:hidden">
                      {" — "}
                      {sizing.memory && sizing.memory.verdict !== "right"
                        ? sizing.memory.reasons[0]
                        : sizing.cpu?.reasons[0]}
                    </span>
                  )}
                </span>
              }
              trailing={
                <span className="flex shrink-0 items-center gap-3">
                  <Status
                    tone={verdictTone(sizing.verdict)}
                    label={VERDICT_LABEL[sizing.verdict]}
                  />
                  {admin && (
                    <Button
                      size="xs"
                      variant="outline"
                      className={cn(applying === sizing.environmentId && "opacity-70")}
                      disabled={applying !== undefined}
                      aria-label={`Apply suggested limits to ${sizing.projectName} ${sizing.environmentName}`}
                      onClick={() => apply(sizing)}
                    >
                      {applying === sizing.environmentId ? "Saving…" : "Apply"}
                    </Button>
                  )}
                </span>
              }
            />
          ))}
        </RowList>
      </PanelBody>
    </Panel>
  )
}
