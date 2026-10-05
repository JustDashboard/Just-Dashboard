import type { DeploymentDomainRoute } from "./types"

type RuntimeRouteService = {
  id: string
  service: { service?: string; manager?: string }
  reached: boolean
}

export function deploymentRouteId(route: DeploymentDomainRoute): string {
  return `d:${route.id ?? `${route.hostname}:${route.path ?? "/"}:${route.service ?? ""}`}`
}

export function deploymentRouteTargets<T extends RuntimeRouteService>(
  route: DeploymentDomainRoute,
  services: readonly T[],
): T[] {
  if (!route.service) return services.filter((service) => service.reached)
  const exact = services.filter((service) => service.service.service === route.service)
  if (exact.length > 0) return exact
  const standalone = services.filter(
    (service) =>
      !service.service.service &&
      ["", "docker", "pm2", "systemd"].includes(service.service.manager ?? ""),
  )
  return route.service === "app" && standalone.length === 1 ? standalone : []
}
