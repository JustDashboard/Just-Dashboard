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
import { Cpu, Layers, Box, type Icon } from "@/components/icons"
import { HUE } from "@/components/live-figure"
import { ProductGlyph, hasProductLogo } from "@/components/product-logo"
import { ReadingLine, ReadingLines, ReadingMark } from "@/components/reading-line"
import { Row, RowList } from "@/components/row-list"
import { Status, StatusDot } from "@/components/status-dot"
import { InfoTip } from "@/components/form"
import { Explain } from "@/components/explain"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import {
  CPU_RULE,
  MEMORY_RULE,
  VERDICT_LABEL,
  applyLines,
  applyToRuntime,
  cores,
  cpuLimit,
  cpuReadings,
  cpuUse,
  fleetSummary,
  gaugeScale,
  historyLine,
  isOff,
  memoryLimit,
  memoryReadings,
  memoryUse,
  mib,
  sizingLead,
  suggestionVerb,
  verdictTone,
  type EnvironmentSizing,
  type FleetSizing,
  type SizingVerdict,
} from "@/components/deploy/sizing-model"
import { WireMark, WireNode } from "@/components/wire/wire"

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
 * head, then a sentence on what the project runs and why, and each limit as a
 * reading line — the shape the CPU and memory readings at the top of the page
 * take — that says what to do with it and ends on a gauge of what was used
 * against where the limit would go. A Compose stack has nothing in its plan to
 * size and a read that fails has nothing to say, so neither draws a card.
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
      <PanelBody className="space-y-4 pt-4">
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
    <>
      <WireNode
        mark={
          <span className="relative flex">
            <WireMark tone="logo" size="md">
              {hasProductLogo(product) ? <ProductGlyph id={product} /> : <Box aria-hidden />}
            </WireMark>
            <StatusDot tone={verdictTone(sizing.verdict)} corner="md" />
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
      <ReadingLines>
        <LimitLine
          name="Memory"
          glyph={Layers}
          hue={HUE.mem}
          rule={<InfoTip label="How the memory suggestion is worked out">{MEMORY_RULE}</InfoTip>}
          verdict={memory.verdict}
          suggestion={mib(memory.suggestedMb)}
          current={memory.limitMb > 0 ? memoryLimit(memory.limitMb) : undefined}
          use={memoryUse(memory)}
          used={memory.p95Mb}
          peak={memory.peakMb}
          suggested={memory.suggestedMb}
          limit={memory.limitMb}
          readings={memoryReadings(memory)}
        />
        <LimitLine
          name="CPU"
          glyph={Cpu}
          hue={HUE.cpu}
          rule={<InfoTip label="How the CPU suggestion is worked out">{CPU_RULE}</InfoTip>}
          verdict={cpu.verdict}
          suggestion={cores(cpu.suggested)}
          current={cpu.limit > 0 ? cpuLimit(cpu.limit) : undefined}
          use={cpuUse(cpu)}
          used={cpu.p95}
          peak={cpu.peak}
          suggested={cpu.suggested}
          limit={cpu.limit}
          readings={cpuReadings(cpu)}
        />
      </ReadingLines>
    </>
  )
}

/**
 * One limit as a reading line: its measure's glyph on its hue (the warning or
 * danger tile when the limit is off), what to do with it in words — "Raise to
 * 640 MiB from 512 MiB", or "320 MiB fits" — what it used under that, and at
 * the line's end its gauge. The solid run is what it used most of the time,
 * the paler run its peak, and the brand's tick where the suggestion would put
 * the limit; a limit already set is the tick in the foreground, so "far too
 * big" and "about to hit it" are both visible at once.
 *
 * It was a block of its own: a tile, an eyebrow, the words, a gauge the width
 * of half the page and the readings under it, beside the reading lines it
 * repeated the shape of without being one. The gauge sits where those lines
 * keep their last hour, and the readings in full are its name and the tip of
 * the detail, which a tap opens where the gauge is not drawn.
 */
function LimitLine({
  name,
  glyph,
  hue,
  rule,
  verdict,
  suggestion,
  current,
  use,
  used,
  peak,
  suggested,
  limit,
  readings,
}: {
  name: string
  glyph: Icon
  hue: string
  rule: React.ReactNode
  verdict: SizingVerdict
  suggestion: string
  /** The limit set now, in words, or nothing while there is none. */
  current?: string
  /** What it used, in the detail's few words. */
  use: string
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
    <ReadingLine
      className="animate-rise"
      mark={<ReadingMark hue={hue} glyph={glyph} tone={limitTone(verdict)} />}
      name={
        <>
          {name} {rule}
        </>
      }
      hint={
        // The line keeps one height, so on a phone its detail is cut before
        // the peak and the throttling, and the gauge named by the readings is
        // not drawn: the usage is a term whose tip, on a tap as on a hover or
        // focus, holds every reading and the suggestion.
        <Explain
          face="term"
          label={`${name}: what it used`}
          tip={
            <>
              <span className="block">{readings}</span>
              <span className="block">Suggested {suggestion}</span>
            </>
          }
          className="block max-w-full truncate text-left underline-offset-2 focus-ring-inset"
        >
          {use}
        </Explain>
      }
      trend={
        <span
          role="img"
          aria-label={`${name}: ${readings}; suggested ${suggestion}`}
          data-slot="limit-gauge"
          className="relative flex h-4 items-center"
        >
          <span className="absolute inset-x-0 h-2 rounded-full bg-meter-track" />
          <span
            className="absolute left-0 h-2 rounded-full"
            style={{
              width: at(peak),
              minWidth: 3,
              background: `color-mix(in oklab, ${hue} 40%, transparent)`,
            }}
          />
          <span
            className="absolute left-0 h-2 rounded-full"
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
        </span>
      }
    >
      {verb ? (
        <>
          {verb} <span className="numeric">{suggestion}</span>
          {current && verdict !== "unlimited" && (
            <span className="font-normal text-muted-foreground"> from {current}</span>
          )}
        </>
      ) : (
        <>
          <span className="numeric">{current ?? suggestion}</span>
          <span className="font-normal text-muted-foreground"> fits</span>
        </>
      )}
    </ReadingLine>
  )
}

/** A limit that is off draws its tile in the verdict's tone, as a reading past its line does. */
function limitTone(verdict: SizingVerdict) {
  if (verdict === "too_tight") return "danger"
  if (verdict === "oversized") return "warning"
  return "default"
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
