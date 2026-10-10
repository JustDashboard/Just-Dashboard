import type {
  Container,
  DeploymentEngineRun,
  DeploymentRelease,
  DeploymentRuntimeService,
  FailureDiagnosis,
  MetricEvent,
  PortExposure,
  PortScope,
} from "@/lib/types"

/**
 * What a published port is reachable from, in the words the Runtime page says
 * it in. `internal` is a port the image declares and nobody published, so it
 * has no word: it is not on the list at all.
 */
export const SCOPE_WORD: Record<Exclude<PortScope, "internal">, string> = {
  all: "Public",
  private: "Private",
  loopback: "Loopback",
}

/** Widest reach first, so the port that matters is the one a reader meets first. */
const SCOPE_RANK: Record<Exclude<PortScope, "internal">, number> = {
  all: 0,
  private: 1,
  loopback: 2,
}

export type PublishedPort = {
  key: string
  hostPort: number
  containerPort: number
  scope: Exclude<PortScope, "internal">
  /** `8080 → 80`, with the protocol only where it is not TCP. */
  label: string
  /** Docker's sentence about the binding, for the hover. */
  summary: string
}

/**
 * Every port a container publishes, once each. Docker lists a binding per
 * address family, so `0.0.0.0:80` and `[::]:80` arrive as two entries that a
 * reader should see as one port; the wider scope of the pair is the one kept.
 */
export function publishedPorts(exposure: PortExposure[] | undefined): PublishedPort[] {
  const byBinding = new Map<string, PublishedPort>()
  for (const port of exposure ?? []) {
    if (!port.hostPort || port.scope === "internal") continue
    const suffix = port.protocol === "tcp" ? "" : `/${port.protocol}`
    const key = `${port.hostPort}:${port.containerPort}${suffix}`
    const seen = byBinding.get(key)
    if (seen && SCOPE_RANK[seen.scope] <= SCOPE_RANK[port.scope]) continue
    byBinding.set(key, {
      key,
      hostPort: port.hostPort,
      containerPort: port.containerPort,
      scope: port.scope,
      label: `${port.hostPort} → ${port.containerPort}${suffix}`,
      summary: port.summary,
    })
  }
  return [...byBinding.values()].sort(
    (a, b) => SCOPE_RANK[a.scope] - SCOPE_RANK[b.scope] || a.hostPort - b.hostPort,
  )
}

/**
 * An image that is only its digest — what Docker reports for a container whose
 * tag has since moved to another image. It names nothing a reader can use.
 */
export function isImageDigest(image: string | undefined) {
  return /^sha256:[0-9a-f]{12,}$/.test(image ?? "")
}

/** An image id as Docker's own CLI prints it: twelve hex digits, no algorithm. */
export function shortDigest(id: string | undefined) {
  if (!id) return undefined
  return id.replace(/^sha256:/, "").slice(0, 12)
}

/**
 * Where well-known programs keep their data, so a volume mounted there is
 * drawn as that program when no container can be asked what it is. Only
 * paths that name one product: `/data` is Redis's, MinIO's and half the
 * images on the registry.
 */
const DATA_DIRECTORIES: [string, string][] = [
  ["/var/lib/postgresql", "postgresql"],
  ["/var/lib/mysql", "mysql"],
  ["/var/lib/mongodb", "mongodb"],
  ["/data/db", "mongodb"],
  ["/var/lib/clickhouse", "clickhouse"],
  ["/usr/share/elasticsearch/data", "elasticsearch"],
  ["/usr/share/opensearch/data", "opensearch"],
  ["/var/lib/influxdb", "influxdb"],
  ["/var/lib/grafana", "grafana"],
  ["/prometheus", "prometheus"],
  ["/var/lib/rabbitmq", "rabbitmq"],
  ["/qdrant/storage", "qdrant"],
  ["/meili_data", "meilisearch"],
]

export function mountTargetProduct(target: string): string | undefined {
  const path = target.replace(/\/+$/, "")
  return DATA_DIRECTORIES.find(([root]) => path === root || path.startsWith(`${root}/`))?.[1]
}

/** The states in which a container is worth asking why. */
const FAILING_STATES = new Set(["restarting", "exited", "dead"])

export function wantsFailureReading(state: string) {
  return FAILING_STATES.has(state)
}

/**
 * Whether a diagnosis has something to say that the reader did not already
 * choose. A container somebody stopped exits with status 0, and a notice on
 * every stopped service would be the page crying wolf; a crash, a kill by the
 * kernel, a loop and a failing check are not.
 */
export function failureTone(diagnosis: FailureDiagnosis): "warning" | "danger" | undefined {
  switch (diagnosis.state) {
    case "looping":
    case "unhealthy":
      return "danger"
    case "flapping":
      return "warning"
    case "stopped": {
      if (diagnosis.evidence.some((item) => item.label === "OOM killed")) return "danger"
      const exit = diagnosis.evidence.find((item) => item.label === "Exit code")
      return exit && !/^0\b/.test(exit.value) ? "warning" : undefined
    }
    default:
      return undefined
  }
}

/**
 * The container the lifecycle verbs act on. The release engine knows the id,
 * the name and the state every five seconds; Docker's listing, read once a
 * minute, fills in the rest when it has arrived, and its state is never
 * preferred over the engine's. The verbs read only what the engine knows, so
 * a card whose listing is late still offers them.
 */
export function runtimeContainer(
  service: DeploymentRuntimeService,
  listed: Container | undefined,
): Container {
  return {
    names: [service.name],
    image: service.image ?? "",
    imageId: service.imageId,
    command: "",
    createdAt: "",
    uptimeSeconds: 0,
    ports: [],
    labels: {},
    networks: [],
    exposure: [],
    hasHealthcheck: false,
    inspected: false,
    ...listed,
    id: service.containerId,
    name: service.name || listed?.name || service.containerId,
    state: service.state,
    status: listed?.state === service.state ? listed.status : service.state,
    composeStack: service.stack ?? listed?.composeStack,
    composeService: service.service ?? listed?.composeService,
  }
}

/**
 * This project's own moments, as markers for its usage charts: each release
 * going live and each deployment that failed. The host's event feed marks
 * every project's runs by name and the start of a run rather than the instant
 * its release took traffic, so a step in this project's memory was drawn
 * beside another project's deploy. Read from the releases and runs the page
 * already holds, so it costs no request.
 */
export function releaseEvents(
  releases: DeploymentRelease[],
  runs: DeploymentEngineRun[],
): MetricEvent[] {
  const events: MetricEvent[] = []
  for (const release of releases) {
    if (release.activatedAt)
      events.push({
        ts: release.activatedAt,
        kind: "deploy",
        title: `Release #${release.number} went live`,
        severity: "info",
      })
  }
  for (const run of runs) {
    if ((run.state === "failed" || run.state === "failed_activation") && run.endedAt)
      events.push({
        ts: run.endedAt,
        kind: "deploy",
        title: `Deployment #${run.runNumber} failed`,
        detail: run.terminalReason,
        severity: "error",
      })
  }
  return events.sort((a, b) => Date.parse(a.ts) - Date.parse(b.ts))
}

/**
 * The live services a domain's requests reach. The proxy forwards to a port
 * the release publishes, so a container publishing one is the answer; failing
 * that, every live service that is not a database the release keeps beside
 * it, and failing that the first live one. Nothing names the proxy's target
 * on a service, so this is read from what each container does.
 */
export function publicServices(
  services: DeploymentRuntimeService[],
  publishes: (service: DeploymentRuntimeService) => boolean,
  isDatabase: (service: DeploymentRuntimeService) => boolean,
): DeploymentRuntimeService[] {
  const live = services.filter((service) => service.liveRelease)
  const published = live.filter(publishes)
  if (published.length > 0) return published
  const applications = live.filter((service) => !isDatabase(service))
  if (applications.length > 0) return applications
  return live.slice(0, 1)
}
