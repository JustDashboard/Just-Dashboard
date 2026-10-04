export type WorkloadKind = "stack" | "container" | "pm2" | "systemd" | "process"

export type WorkloadService = {
  name: string
  resourceId: string
  state: string
  health?: string
  image?: string
  ports?: {
    hostIp?: string
    hostPort: number
    containerPort: number
    protocol?: string
  }[]
  pid?: number
}

export type WorkloadCandidate = {
  key: string
  kind: WorkloadKind
  name: string
  resourceId: string
  state: string
  running: number
  total: number
  services: WorkloadService[]
  sourcePath?: string
  managerUrl: string
  configurationAvailable: boolean
  warnings: string[]
  digest: string
}

export type WorkloadDiscovery = {
  checkedAt: string
  items: WorkloadCandidate[]
  silences: string[]
}

export type WorkloadRegistration = {
  projectId: number
  environmentId: number
  created: boolean
}

export const WORKLOAD_KINDS: { key: WorkloadKind; label: string }[] = [
  { key: "stack", label: "Compose stacks" },
  { key: "container", label: "Containers" },
  { key: "pm2", label: "PM2 apps" },
  { key: "systemd", label: "Systemd services" },
  { key: "process", label: "Processes" },
]

export const WORKLOAD_LABELS: Record<WorkloadKind, string> = {
  stack: "Compose stack",
  container: "Docker container",
  pm2: "PM2 app",
  systemd: "Systemd service",
  process: "Listening process",
}

export function workloadMatches(item: WorkloadCandidate, query: string) {
  const needle = query.trim().toLowerCase()
  if (!needle) return true
  return [
    item.name,
    item.resourceId,
    item.sourcePath,
    WORKLOAD_LABELS[item.kind],
    ...item.services.flatMap((service) => [
      service.name,
      service.image,
      ...(service.ports ?? []).map((port) => String(port.hostPort)),
    ]),
  ]
    .filter(Boolean)
    .join(" ")
    .toLowerCase()
    .includes(needle)
}

export function workloadManagerUrl(item: WorkloadCandidate) {
  return item.managerUrl.startsWith("/") && !item.managerUrl.startsWith("//")
    ? item.managerUrl
    : undefined
}

export function workloadPort(port: NonNullable<WorkloadService["ports"]>[number]) {
  const host = port.hostIp ? `${port.hostIp.includes(":") ? `[${port.hostIp}]` : port.hostIp}:` : ""
  const mapping =
    port.containerPort && port.containerPort !== port.hostPort ? ` → ${port.containerPort}` : ""
  return `${host}${port.hostPort}${mapping}${port.protocol ? `/${port.protocol}` : ""}`
}
