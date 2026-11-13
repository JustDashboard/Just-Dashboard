"use client"

import { Globe, Location, Plus, Servers } from "@/components/icons"
import { WireHost, WireMark, WirePlaceholder } from "@/components/deploy/wire"
import { WireFlow, type WireFlowNode } from "@/components/wire-flow"
import { relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { ExternalCheck, ExternalVantage } from "@/lib/network-external"
import {
  approvedServices,
  checkOutcome,
  checkTarget,
  checkTime,
  familyWord,
  newestFirst,
  type CheckOutcome,
} from "@/components/network/external/reading"

const DRAWN = 5

/**
 * The enrolled sources on the left, the internet they measure across, and
 * this server's approved services on the right. Each source's line is its
 * last check's outcome and each service's line the last check that asked
 * about it: still and solid where a measurement reached it, red where a
 * measured stage refused, dashed and still where nothing was measured, and a
 * pulse while a check is in flight. A source that never reported is never
 * drawn as a failure.
 */
export function ExternalPicture({
  vantages,
  checks,
  loading,
  onSource,
  onEnroll,
}: {
  vantages: ExternalVantage[]
  checks: ExternalCheck[]
  loading?: boolean
  /** What pressing an enrolled source does: check from it. Undefined for a read account. */
  onSource?: (vantage: ExternalVantage) => void
  onEnroll?: () => void
}) {
  const sorted = newestFirst(checks)
  const live = vantages.filter((v) => !v.revokedAt)
  const sources: WireFlowNode[] = live.map((v) => {
    const last = sorted.find((check) => check.vantageId === v.id)
    const outcome = v.enrolledAt
      ? checkOutcome(last)
      : { ...checkOutcome(undefined), word: "awaiting enrollment" }
    return {
      id: `source:${v.id}`,
      mark: (size) => (
        <WireMark size={size} tone={v.enrolledAt ? "logo" : "neutral"}>
          <Location aria-hidden />
        </WireMark>
      ),
      eyebrow: v.location || "Location undeclared",
      title: v.name,
      name: v.name,
      hint: (
        <OutcomeWords outcome={outcome} at={last && v.enrolledAt ? checkTime(last) : undefined} />
      ),
      line: outcome.line,
      onPress: onSource && v.enrolledAt ? () => onSource(v) : undefined,
      label: onSource && v.enrolledAt ? `Check from ${v.name}` : undefined,
    }
  })
  if (sources.length === 0)
    sources.push({
      id: "add:source",
      placeholder: true,
      mark: (
        <WirePlaceholder size="md" fallback={Location}>
          <Plus aria-hidden />
        </WirePlaceholder>
      ),
      title: <span className="text-muted-foreground">Enroll a source</span>,
      name: "Enroll a source",
      hint: "a machine you control elsewhere",
      onPress: onEnroll,
      line: { state: "idle" },
    })

  const services: WireFlowNode[] = approvedServices(live).map((service) => {
    const last = sorted.find((check) => checkTarget(check, vantages) === service.target)
    const outcome = checkOutcome(last)
    return {
      id: `service:${service.target}`,
      mark: (size) => <WireHost size={size === "lg" ? "md" : size} />,
      eyebrow: "This server",
      title: <span className="font-mono">{service.target}</span>,
      name: service.target,
      hint: (
        <>
          <span className="block font-mono">
            {service.ports.map((port) => `:${port}`).join(" ")} ·{" "}
            {service.families.map(familyWord).join(" ")}
          </span>
          <OutcomeWords outcome={outcome} at={last ? checkTime(last) : undefined} />
        </>
      ),
      line: outcome.line,
    }
  })
  if (services.length === 0)
    services.push({
      id: "sink:service",
      placeholder: true,
      mark: (
        <WirePlaceholder size="md" fallback={Servers}>
          <Plus aria-hidden />
        </WirePlaceholder>
      ),
      title: <span className="text-muted-foreground">An approved service</span>,
      name: "An approved service",
      hint: "a name, its addresses and ports",
      line: { state: "idle" },
    })

  const kept = checks.filter((check) => check.result).length
  return (
    <WireFlow
      loading={loading}
      start={{ title: "Sources", nodes: sources, max: DRAWN, count: live.length, loadingRows: 2 }}
      end={{ title: "Approved services", nodes: services, max: DRAWN, loadingRows: 1 }}
      hub={{
        mark: (
          <WireMark tone="logo" size="lg">
            <Globe aria-hidden />
          </WireMark>
        ),
        eyebrow: "Across",
        title: "The internet",
        hint: kept
          ? `${kept} measurement${kept === 1 ? "" : "s"} kept · 7 days`
          : "nothing measured yet",
      }}
    />
  )
}

/** A line's outcome and when it was said, coloured only where it was measured. */
export function OutcomeWords({ outcome, at }: { outcome: CheckOutcome; at?: string }) {
  return (
    <span
      className={cn(
        "block",
        outcome.kind === "reached" && "text-success",
        outcome.kind === "failed" && "text-destructive",
        outcome.kind === "unclear" && "text-warning",
      )}
    >
      {outcome.word}
      {at && <span className="text-muted-foreground"> · {relativeTime(at)}</span>}
    </span>
  )
}
