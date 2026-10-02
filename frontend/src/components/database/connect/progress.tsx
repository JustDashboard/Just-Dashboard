"use client"

import { RunPhases, phaseStates } from "@/components/run-phases"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { PHASE_SENTENCE, PROVISION_STEPS, type ProvisionPhase } from "./provision"

/** Database startup uses the app release path's sweeping segments and stage marks. */
export function DatabaseProgress({
  phase,
  elapsed,
  failed = false,
}: {
  phase: ProvisionPhase
  elapsed: number
  failed?: boolean
}) {
  return (
    <div className="min-w-0 animate-rise space-y-4" data-slot="database-progress">
      <RunPhases
        phases={phaseStates(
          PROVISION_STEPS.map((step) => step.label),
          PROVISION_STEPS.findIndex((step) => step.key === phase),
          failed ? "failed" : "running",
        )}
      />
      {!failed && (
        <p className="flex items-baseline justify-between gap-3 text-body" role="status">
          <TextShimmer className="font-medium">{PHASE_SENTENCE[phase]}</TextShimmer>
          <span className="numeric shrink-0 text-hint text-muted-foreground">{elapsed}s</span>
        </p>
      )}
    </div>
  )
}
