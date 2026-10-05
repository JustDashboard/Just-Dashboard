import { FormFact, FormFacts, FormNote, FormSection } from "@/components/form"
import { Status } from "@/components/status-dot"
import { ExistingIngressRoutes } from "@/components/deploy/existing-ingress-routes"
import type { WorkloadAdoption } from "@/lib/workload-import"

/** Read back what automation prepared separately from the preserved live baseline. */
export function RecoveredWorkloadPlan({ adoption }: { adoption: WorkloadAdoption }) {
  const sources = adoption.buildSources ?? []
  const inputs = (adoption.inputs ?? []).filter((input) => input.kind === "environment")
  return (
    <>
      {inputs.length > 0 && (
        <FormSection title="Captured application environment">
          <FormFacts>
            <FormFact label="Application keys">
              {inputs.filter((input) => input.category !== "image_default").length} retained
            </FormFact>
            <FormFact label="Image defaults">
              {inputs.filter((input) => input.category === "image_default").length} retained
            </FormFact>
          </FormFacts>
          <FormNote>
            Original keys stay assigned to their service. Open Variables to replace a value.
          </FormNote>
        </FormSection>
      )}
      {sources.length > 0 && (
        <FormSection title="Prepared for future builds">
          <ul className="space-y-3" aria-label="Recovered build sources">
            {sources.map((source) => (
              <li key={source.service} className="min-w-0 space-y-1">
                <p className="font-mono text-body">{source.service}</p>
                <Status
                  tone={source.status === "snapshot" ? "running" : "notice"}
                  label={
                    source.status === "snapshot"
                      ? `${source.framework || source.language || "Verified source"} · source snapshot attached`
                      : "Exact current image retained"
                  }
                />
                {source.reason && <FormNote>{source.reason}</FormNote>}
              </li>
            ))}
          </ul>
          <FormNote>
            The original images and runtime remain the frozen live release for rollback.
          </FormNote>
        </FormSection>
      )}
      <ExistingIngressRoutes bindings={adoption.ingressBindings ?? []} />
    </>
  )
}
