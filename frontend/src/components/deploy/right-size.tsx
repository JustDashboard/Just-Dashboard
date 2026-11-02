"use client"

import { useCallback, useState } from "react"
import Link from "next/link"
import { get, put } from "@/lib/api"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type { DeploymentEnvironmentConfiguration } from "@/lib/types"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Cpu, Layers, Box } from "@/components/icons"
import { HUE } from "@/components/overview/readings"
import { ProductGlyph, hasProductLogo } from "@/components/product-logo"
import { Row, RowList } from "@/components/row-list"
import { Status, StatusDot } from "@/components/status-dot"
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
  gaugeScale,
  historyLine,
  isOff,
  memoryReadings,
  mib,
  sizingLead,
  suggestionVerb,
  verdictTone,
  type EnvironmentSizing,
  type FleetSizing,
  type SizingVerdict,
} from "@/components/deploy/sizing-model"
import { WireMark, WireNode } from "@/components/deploy/wire"

/**
 * Applying a suggestion is the configuration write Runtime settings make:
 * read the environment's configuration, put the suggested limits into its
 * runtime and save the whole of it back with the revision it read. The same
 * route, the same audit, the same validation and the same "applies with the
 * next release" — a stale read is refused as any settings save is.
 */
export async function applySuggestedLimits(sizing: EnvironmentSizing) {
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

/**
 * The project overview's Right-size card: the verdict and the one verb in the
 * head, then a sentence on what the project runs and why, and each limit as what
 * to do with it over a gauge of what was used against where the limit would go.
 * A Compose stack has nothing in its plan to size and a read that fails has
 * nothing to say, so neither draws a card.
 */
export function RightSizeCard({
  projectId,
  environmentId,
  onApplied,
  product,
}: {
  projectId: number
  environmentId: number
  onApplied?: () => void
  /** What the containers run, as a `product-logo` id. */
  product?: string
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
        <SizingBody sizing={sizing} product={product} />
      </PanelBody>
    </Panel>
  )
}

function SizingBody({ sizing, product }: { sizing: EnvironmentSizing; product?: string }) {
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
    <div className="min-w-0 space-y-5 pt-1">
      <WireNode
        mark={
          <span className="relative flex">
            <WireMark tone="logo" size="md">
              {hasProductLogo(product) ? <ProductGlyph id={product} /> : <Box aria-hidden />}
            </WireMark>
            <span className="absolute -right-0.5 -bottom-0.5 flex size-3.5 items-center justify-center rounded-full bg-background">
              <StatusDot tone={verdictTone(sizing.verdict)} />
            </span>
          </span>
        }
        title={sizingLead(sizing.verdict, sizing.projectName)}
        hint={
          <>
            {reasons.map((reason) => (
              <span key={reason} className="block">
                {reason}
              </span>
            ))}
            <span className="block">
              From {historyLine(sizing.window)}
              {memory.oomKills > 0 || !sizing.oom.watching
                ? ""
                : ", and no OOM kill since the event log began listening"}
              .{sizing.pending && " The saved limits apply with the next release."}
            </span>
          </>
        }
      />
      <div className="grid min-w-0 gap-x-10 gap-y-5 lg:grid-cols-2">
        <LimitLine
          name="Memory"
          glyph={<Layers aria-hidden />}
          hue={HUE.mem}
          rule={<InfoTip label="How the memory suggestion is worked out">{MEMORY_RULE}</InfoTip>}
          verdict={memory.verdict}
          suggestion={mib(memory.suggestedMb)}
          used={memory.p95Mb}
          peak={memory.peakMb}
          suggested={memory.suggestedMb}
          limit={memory.limitMb}
          readings={memoryReadings(memory)}
        />
        <LimitLine
          name="CPU"
          glyph={<Cpu aria-hidden />}
          hue={HUE.cpu}
          rule={<InfoTip label="How the CPU suggestion is worked out">{CPU_RULE}</InfoTip>}
          verdict={cpu.verdict}
          suggestion={cores(cpu.suggested)}
          used={cpu.p95}
          peak={cpu.peak}
          suggested={cpu.suggested}
          limit={cpu.limit}
          readings={cpuReadings(cpu)}
        />
      </div>
    </div>
  )
}

/**
 * One limit: what to do with it, in words, over a gauge. The solid run is what
 * it used most of the time, the paler run its peak, and the brand's tick where
 * the suggestion would put the limit; a limit already set is the tick in the
 * foreground, so "far too big" and "about to hit it" are both visible at once.
 */
function LimitLine({
  name,
  glyph,
  hue,
  rule,
  verdict,
  suggestion,
  used,
  peak,
  suggested,
  limit,
  readings,
}: {
  name: string
  glyph: React.ReactNode
  hue: string
  rule: React.ReactNode
  verdict: SizingVerdict
  suggestion: string
  used: number
  peak: number
  suggested: number
  limit: number
  readings: string
}) {
  const scale = gaugeScale([suggested, peak, limit])
  const at = (value: number) => `${(value / scale) * 100}%`
  const verb = suggestionVerb(verdict)
  return (
    <div className="flex min-w-0 items-start gap-3.5" data-slot="limit-line">
      <WireMark size="md" hue={hue}>
        {glyph}
      </WireMark>
      <div className="min-w-0 flex-1 space-y-1.5">
        <div>
          <p className="eyebrow flex items-center gap-1.5">
            {name} {rule}
          </p>
          <p className="text-body leading-snug font-medium">
            {verb && <>{verb} </>}
            <span className="numeric">{suggestion}</span>
            {!verb && <span className="font-normal text-muted-foreground"> fits</span>}
          </p>
        </div>
        <div
          role="img"
          aria-label={`${name}: ${readings}; suggested ${suggestion}`}
          className="relative flex h-4 animate-rise items-center"
        >
          <span className="absolute inset-x-0 h-2 rounded-sm bg-meter-track" />
          <span
            className="absolute left-0 h-2 rounded-l-sm"
            style={{
              width: at(peak),
              minWidth: 3,
              background: `color-mix(in oklab, ${hue} 40%, transparent)`,
            }}
          />
          <span
            className="absolute left-0 h-2 rounded-l-sm"
            style={{ width: at(used), minWidth: 3, background: hue }}
          />
          {limit > 0 && (
            <span
              data-slot="limit-tick"
              className="absolute h-full w-px bg-foreground"
              style={{ left: at(limit) }}
            />
          )}
          <span
            data-slot="suggested-tick"
            className="absolute h-full w-0.5 rounded-full bg-brand"
            style={{ left: at(suggested) }}
          />
        </div>
        <p className="numeric text-hint text-muted-foreground">{readings}</p>
      </div>
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
