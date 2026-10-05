import type { DeploymentPreflightFinding } from "./types"

export type WorkloadKind = "stack" | "container" | "pm2" | "systemd" | "process"
export type WorkloadScope = "all_services" | "existing_services"

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

export type RecoveredInput = {
  storageKey: string
  name: string
  service?: string
  kind: "environment" | "label" | "log_option" | "runtime_setting" | "argument"
  origin: "container" | "compose" | "native"
  category: "application" | "image_default" | "runtime_setting"
  sensitivity: "plain" | "secret"
  retained: boolean
  empty: boolean
}

export type AdoptionIssue = {
  code: string
  message: string
  service?: string
  field?: string
  blocking: boolean
}

export type RecoveredBuildSource = {
  service: string
  name?: string
  framework?: string
  language?: string
  role?: string
  confidence?: string
  status: "snapshot" | "image_only"
  reason?: string
  snapshotDigest?: string
}

export type ExistingIngressBinding = {
  id: string
  hostname: string
  path: string
  service: string
  owner: string
  proxyKind: string
  status: "linked" | "blocked" | "hint" | "unverified"
  continuity: "host_port" | "network_alias" | "retarget" | "unverified"
  plannedChange?: string
  https?: boolean
}

export type NativeStartupHandoff = {
  version: number
  manager: string
  digest: string
  actionCount: number
  status: "prepared"
  description: string
}

/** Server-authored origin; private runtime configuration and values stay on the server. */
export type WorkloadAdoption = {
  key: string
  digest: string
  kind: WorkloadKind
  resourceId: string
  manager: string
  name: string
  warnings: string[]
  blockers: string[]
  serviceCount: number
  runningCount: number
  scope?: WorkloadScope
  excludedServices?: string[]
  inputs?: RecoveredInput[]
  issues?: AdoptionIssue[]
  buildSources?: RecoveredBuildSource[]
  ingressBindings?: ExistingIngressBinding[]
  startupHandoff?: NativeStartupHandoff
  baseline?: unknown
}

/** Browser persistence has no use for baseline artifacts or extra server-only fields. */
export function adoptionReviewMetadata(
  adoption: WorkloadAdoption,
): Omit<WorkloadAdoption, "baseline"> {
  const { key, digest, kind, resourceId, manager, name, serviceCount, runningCount } = adoption
  return {
    key,
    digest,
    kind,
    resourceId,
    manager,
    name,
    serviceCount,
    runningCount,
    scope: adoption.scope,
    excludedServices: adoption.excludedServices,
    // Construct every descriptor: server-private additions must never leak to Web Storage.
    inputs: adoption.inputs?.map(
      ({ storageKey, name, service, kind, origin, category, sensitivity, retained, empty }) => ({
        storageKey,
        name,
        service,
        kind,
        origin,
        category,
        sensitivity,
        retained,
        empty,
      }),
    ),
    issues: adoption.issues?.map(({ code, message, service, field, blocking }) => ({
      code,
      message,
      service,
      field,
      blocking,
    })),
    buildSources: adoption.buildSources?.map(
      ({
        service,
        name,
        framework,
        language,
        role,
        confidence,
        status,
        reason,
        snapshotDigest,
      }) => ({
        service,
        name,
        framework,
        language,
        role,
        confidence,
        status,
        reason,
        snapshotDigest,
      }),
    ),
    ingressBindings: adoption.ingressBindings?.map(
      ({
        id,
        hostname,
        path,
        service,
        owner,
        proxyKind,
        status,
        continuity,
        plannedChange,
        https,
      }) => ({
        id,
        hostname,
        path,
        service,
        owner,
        proxyKind,
        status,
        continuity,
        plannedChange,
        https,
      }),
    ),
    startupHandoff: adoption.startupHandoff && {
      version: adoption.startupHandoff.version,
      manager: adoption.startupHandoff.manager,
      digest: adoption.startupHandoff.digest,
      actionCount: adoption.startupHandoff.actionCount,
      status: adoption.startupHandoff.status,
      description: adoption.startupHandoff.description,
    },
    warnings: adoption.warnings ?? [],
    blockers: adoption.blockers ?? [],
  }
}

/** A retained empty value is still present; only a required nonempty value needs an answer. */
export function retainedVariableSatisfied(
  name: string,
  retainedKeys: readonly string[],
  inputs: readonly RecoveredInput[] = [],
) {
  return (
    retainedKeys.includes(name) && !inputs.some((input) => input.storageKey === name && input.empty)
  )
}

export function recoveredEnvironmentSatisfied(
  name: string,
  retainedKeys: readonly string[],
  inputs: readonly RecoveredInput[] = [],
) {
  const matches = inputs.filter((input) => input.kind === "environment" && input.name === name)
  return (
    matches.length > 0 &&
    matches.every((input) => retainedVariableSatisfied(input.storageKey, retainedKeys, inputs))
  )
}

/** Group by service, never by key alone: two apps may have different values for the same name. */
export function recoveredEnvironmentGroups(inputs: readonly RecoveredInput[]) {
  const groups = new Map<string, RecoveredInput[]>()
  for (const input of inputs) {
    if (input.kind !== "environment") continue
    const service = input.service ?? "Application"
    groups.set(service, [...(groups.get(service) ?? []), input])
  }
  return [...groups].map(([service, entries]) => ({
    service,
    application: entries.filter((entry) => entry.category !== "image_default"),
    imageDefaults: entries.filter((entry) => entry.category === "image_default"),
  }))
}

export function groupImportWarnings(findings: readonly DeploymentPreflightFinding[]) {
  const groups = new Map<string, DeploymentPreflightFinding[]>()
  for (const finding of findings) {
    const key = finding.issueCode ? `adoption:${finding.issueCode}` : finding.code
    groups.set(key, [...(groups.get(key) ?? []), finding])
  }
  return [...groups].map(([key, entries]) => ({
    key,
    finding: entries[0],
    codes: [...new Set(entries.map((entry) => entry.code))],
    details: entries,
    services: [...new Set(entries.map((entry) => entry.service).filter(Boolean))],
  }))
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
  return item.managerUrl.startsWith("/") &&
    !item.managerUrl.startsWith("//") &&
    !/[\\\u0000-\u0020]/.test(item.managerUrl)
    ? item.managerUrl
    : undefined
}

export function workloadPort(port: NonNullable<WorkloadService["ports"]>[number]) {
  const host = port.hostIp ? `${port.hostIp.includes(":") ? `[${port.hostIp}]` : port.hostIp}:` : ""
  const mapping =
    port.containerPort && port.containerPort !== port.hostPort ? ` → ${port.containerPort}` : ""
  return `${host}${port.hostPort}${mapping}${port.protocol ? `/${port.protocol}` : ""}`
}
