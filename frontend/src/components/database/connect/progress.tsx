"use client"

import { RunPhases, phaseStates } from "@/components/run-phases"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { phaseSentence, provisionSteps, type ProvisionPhase } from "./provision"

/**
 * Database startup uses the app release path's sweeping segments and stage
 * marks. `build` is a start that builds its image first; `built` one whose
 * image is made on this server rather than pulled.
 */
export function DatabaseProgress({
  phase,
  elapsed,
  build,
  built,
  failed = false,
}: {
  phase: ProvisionPhase
  elapsed: number
  build: boolean
  built: boolean
  failed?: boolean
}) {
  const steps = provisionSteps(build)
  return (
    <div className="min-w-0 animate-rise space-y-4" data-slot="database-progress">
      <RunPhases
        phases={phaseStates(
          steps.map((step) => step.label),
          steps.findIndex((step) => step.key === phase),
          failed ? "failed" : "running",
        )}
      />
      {!failed && (
        <p className="flex items-baseline justify-between gap-3 text-body" role="status">
          <TextShimmer className="font-medium">{phaseSentence(phase, built)}</TextShimmer>
          <span className="numeric shrink-0 text-hint text-muted-foreground">{elapsed}s</span>
        </p>
      )}
    </div>
  )
}
