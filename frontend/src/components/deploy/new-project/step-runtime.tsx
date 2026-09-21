"use client"

import { Disclosure, Field, FormSection } from "@/components/form"
import { Input } from "@/components/ui/input"
import type { WizardErrors } from "@/components/deploy/deployment-defaults"
import {
  ContainerAccess,
  HealthChecks,
  RuntimeLimits,
  StorageMounts,
} from "@/components/deploy/new-project/configure-advanced"
import { PublicAddress } from "@/components/deploy/new-project/public-address"
import type { ConfigureFlow, FlowUpdate } from "@/components/deploy/new-project/draft"
import { SECTION_IDS } from "@/components/deploy/new-project/plan-sections"

/**
 * Step two: **how it runs, and where it answers.**
 *
 * The port and the public address are the whole of it in the open, because
 * they are the two the reader can be wrong about in a way nothing else
 * catches — a container on the wrong port is a release that never goes ready,
 * and a project with no hostname is one nobody can reach. The port was
 * previously the last field of a Git repository's build fold and a separate
 * field on the Project section for an image, which is two places for one
 * `runtime.internalPort`; it is one field here for every source.
 *
 * Everything under them is a fold, and each fold says what is in it while it
 * is shut. They were one `Advanced` disclosure holding seven sections — so
 * the answer to "where do I set a memory limit" was a press, a wall of
 * twenty-five fields, and a hunt. Four folds named for what they hold is the
 * same information at the cost of reading four words.
 */
export function StepRuntime({
  flow,
  onFlowChange,
  errors,
  foldsOpen,
}: {
  flow: ConfigureFlow
  onFlowChange: (next: FlowUpdate) => void
  errors: WizardErrors
  /** `?mode=advanced` arrived asking for exactly these. */
  foldsOpen: boolean
}) {
  const configuration = flow.configuration
  const setConfiguration = (next: typeof configuration) =>
    onFlowChange({ ...flow, configuration: next })
  const runtime = configuration.runtime
  const worker = flow.profile === "worker"

  const readiness = configuration.checks.find((check) => check.phase === "readiness")
  const smoke = configuration.checks.find((check) => check.phase === "smoke")
  const checkFacts =
    [
      readiness ? `Readiness · ${readiness.kind}` : "No readiness check",
      smoke && "smoke test after activation",
    ]
      .filter(Boolean)
      .join(" · ") || "No checks"

  const limitFacts =
    [
      runtime.memoryMb ? `${runtime.memoryMb} MB` : undefined,
      runtime.cpus ? `${runtime.cpus} CPU` : undefined,
      runtime.strategy === "blue_green" ? "candidate first" : "stop first",
    ]
      .filter(Boolean)
      .join(" · ") || "No limits"

  const mounts = runtime.mounts ?? []
  const storageFacts =
    mounts.length === 0
      ? "Nothing survives a rebuild"
      : `${mounts.length} ${mounts.length === 1 ? "mount" : "mounts"}`

  const accessFacts =
    [
      runtime.privileged && "privileged",
      runtime.hostNetwork && "host network",
      (runtime.capabilities?.length ?? 0) > 0 && `${runtime.capabilities!.length} capabilities`,
      (runtime.devices?.length ?? 0) > 0 && `${runtime.devices!.length} devices`,
    ]
      .filter(Boolean)
      .join(" · ") || "Unprivileged, own network"

  return (
    <>
      <FormSection id={SECTION_IDS.runtime} title="Container">
        {worker ? (
          /* Data, not a caption (§5): a worker has no port and no address, so
             what this section says is what that means for the release. */
          <p className="text-body text-muted-foreground">
            A worker answers no requests. It runs in the background, is not published, and is
            released by stopping the old container before the new one starts.
          </p>
        ) : (
          <Field
            label="Port the app listens on"
            htmlFor="internal-port"
            className="sm:max-w-xs"
            error={errors.internalPort}
            hint={
              flow.candidate?.port
                ? "Read from the source's own configuration."
                : "What the container serves on, not the host port."
            }
          >
            <Input
              id="internal-port"
              type="number"
              min={0}
              max={65535}
              value={runtime.internalPort ?? 0}
              onChange={(event) =>
                setConfiguration({
                  ...configuration,
                  runtime: { ...runtime, internalPort: Number(event.target.value) || 0 },
                })
              }
              className="font-mono"
            />
          </Field>
        )}
      </FormSection>

      {!worker && (
        <PublicAddress
          id={SECTION_IDS.address}
          domains={configuration.domains}
          suggestion={flow.hostname}
          onChange={(domains) => setConfiguration({ ...configuration, domains })}
        />
      )}

      <Disclosure id={SECTION_IDS.checks} summary="Health check" facts={checkFacts}>
        <HealthChecks configuration={configuration} onChange={setConfiguration} />
      </Disclosure>

      <Disclosure
        id={SECTION_IDS.limits}
        summary="Resources & limits"
        facts={limitFacts}
        open={foldsOpen}
      >
        <RuntimeLimits configuration={configuration} onChange={setConfiguration} errors={errors} />
      </Disclosure>

      <Disclosure id={SECTION_IDS.storage} summary="Storage" facts={storageFacts} open={foldsOpen}>
        <StorageMounts configuration={configuration} onChange={setConfiguration} />
      </Disclosure>

      <Disclosure summary="Container access" facts={accessFacts} open={foldsOpen}>
        <ContainerAccess configuration={configuration} onChange={setConfiguration} />
      </Disclosure>
    </>
  )
}
