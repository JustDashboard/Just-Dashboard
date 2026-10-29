import type {
  DeploymentBuildMethod,
  DeploymentConfiguration,
  DeploymentRuntimeService,
} from "@/lib/types"

/**
 * Replicas: how many containers of each release run side by side behind the
 * route (`deploy/runtime_replicas.go`). Kept out of the components so the
 * settings form, the overview and the tests read the rule the same way.
 */

/** The most replicas one release runs: `MaxRuntimeReplicas`. */
export const MAX_REPLICAS = 8

type RuntimePlan = DeploymentConfiguration["runtime"]

/** How many containers each release of the plan runs; an unset field is one. */
export function effectiveReplicas(runtime: Pick<RuntimePlan, "replicas">): number {
  const replicas = runtime.replicas ?? 0
  return replicas > 1 ? Math.min(replicas, MAX_REPLICAS) : 1
}

/**
 * Why this plan cannot run more than one container per release, or nothing
 * when it can: `replicaRefusal`, in the words the reader needs to fix it.
 * Replicas share one route on one host, so whatever only one container can
 * hold — a port on the host, the host's whole network, a writable mount, a
 * Compose stack's services — rules them out. A read-only mount is shared
 * without harm.
 */
export function replicaRefusal(
  runtime: RuntimePlan,
  method: DeploymentBuildMethod,
): string | undefined {
  if (method === "compose" || method === "legacy_compose")
    return "a Compose stack runs one copy of each of its services"
  if (runtime.hostNetwork) return "on the host network every replica would claim the same ports"
  if ((runtime.hostPort ?? 0) > 0)
    return `host port ${runtime.hostPort} can be held by one container at a time`
  const published = runtime.ports?.[0]
  if (published)
    return `published port ${published.hostPort} can be held by one container at a time`
  const writable = runtime.mounts?.find((mount) => !mount.readOnly)
  if (writable)
    return `${writable.target} is writable, and replicas would write to it at the same time`
  return undefined
}

/**
 * The plan as saved for a replica count: one is the field left out, as the
 * server writes it back, so choosing one again is no edit to the plan.
 */
export function withReplicas<T extends Pick<RuntimePlan, "replicas">>(
  runtime: T,
  replicas: number,
): T {
  const next = { ...runtime }
  if (replicas > 1) next.replicas = Math.min(replicas, MAX_REPLICAS)
  else delete next.replicas
  return next
}

/** A replica answering: running, and not reported unhealthy by its own healthcheck. */
export function replicaHealthy(service: DeploymentRuntimeService): boolean {
  return service.state === "running" && service.health !== "unhealthy"
}

/**
 * The live release's replicas as the overview says them — "2 of 3 replicas
 * healthy" — or nothing for a release of one container. The count is the
 * release's own (its containers' label), not the plan's next one, and a
 * replica Docker no longer lists still counts against it.
 */
export function replicaReading(
  services: DeploymentRuntimeService[] | undefined,
): { healthy: number; total: number } | undefined {
  const live = services?.filter((service) => service.liveRelease) ?? []
  const total = Math.max(0, ...live.map((service) => service.replicas ?? 0))
  if (total <= 1) return undefined
  return { healthy: live.filter(replicaHealthy).length, total }
}
