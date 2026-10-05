import { dockerSource } from "@/lib/log-sources"
import type { DeploymentRuntimeService, LogSource } from "@/lib/types"

/** A native baseline is not a Docker container, even when its future source builds one. */
export function isDockerService(service: DeploymentRuntimeService) {
  return Boolean(service.containerId) && (!service.manager || service.manager === "docker")
}

export function runtimeServiceId(service: DeploymentRuntimeService) {
  return service.containerId || service.resourceId || service.logSource || service.name
}

export function runtimeLogSource(service: DeploymentRuntimeService) {
  return (
    service.logSource || (isDockerService(service) ? dockerSource(service.containerId) : undefined)
  )
}

export function runtimeLogKind(service: DeploymentRuntimeService): LogSource["kind"] {
  const source = runtimeLogSource(service)
  if (source?.startsWith("pm2:")) return "pm2"
  if (source?.startsWith("journal:")) return "journal"
  if (source?.startsWith("file:")) return "app"
  return "docker"
}

export function runtimeManagerUrl(service: DeploymentRuntimeService) {
  if (service.manager === "pm2") return "/processes/pm2"
  if (service.manager === "systemd") return "/processes/services"
  if (service.manager === "process") return "/processes"
  return `/docker/containers/${encodeURIComponent(service.containerId)}`
}

export function runtimeManagerLabel(service: DeploymentRuntimeService) {
  if (service.manager === "pm2") return "PM2"
  if (service.manager === "systemd") return "systemd"
  if (service.manager === "process") return "Host process"
  return "Docker"
}
