import { perMinute } from "@/lib/requests"
import type {
  DbDriver,
  DbFleetEntry,
  DeploymentSummary,
  LogSource,
  TrafficPulse,
  VHost,
} from "@/lib/types"
import type { Tone } from "@/components/tone"
import { failingTone } from "@/components/deploy/fleet"
import { projectProduct } from "@/components/deploy/vocabulary"
import { siteProduct } from "@/components/proxy/marks"
import { siteHosts } from "@/components/proxy/site-log-plan"

/**
 * What `/logs` knows about a source beyond its lines, decided apart from the
 * drawing so each match is tested on its own: which saved database a
 * container or a file is the server log of, which site a file is the access
 * or error log of — the page views a source is offered — and the request
 * records the rail lists beside the logs.
 */

/** The lens a saved connection's server log is read through, by the connection's engine. */
const DRIVER_LENS: Partial<Record<DbDriver, string>> = {
  postgres: "postgres",
  mysql: "mysql",
  redis: "redis",
  mongodb: "mongodb",
  clickhouse: "clickhouse",
  sqlserver: "mssql",
}

/** Whether a log is read as a database server's, whose statements a saved connection may hold. */
export function isDatabaseLens(lens: string | undefined) {
  return Object.values(DRIVER_LENS).includes(lens ?? "")
}

/** The connection a container is the server of: the fleet names each one's container. */
export function containerConnection(connections: DbFleetEntry[], container: string) {
  return connections.find((c) => c.source === "docker" && c.container === container)
}

/**
 * The connections a host log may belong to: servers on this machine whose
 * engine writes in the log's lens. Which of them wrote it is each
 * connection's own answer (`GET /databases/{id}/logs/sources`), and only
 * these are asked.
 */
export function hostCandidates(connections: DbFleetEntry[], lens: string | undefined) {
  return connections.filter((c) => c.source === "host" && DRIVER_LENS[c.driver] === lens)
}

/**
 * The nginx site a file is the access or error log of — one that keeps its
 * requests in a file of its own, and only when exactly one site names the
 * file: a log two sites write to is neither one's record.
 */
export function siteOfFile(vhosts: VHost[], path: string): VHost | undefined {
  const named = vhosts.filter(
    (v) =>
      v.kind === "nginx" &&
      Boolean(v.accessLogPath) &&
      (v.accessLogPath === path || v.errorLogPath === path),
  )
  return named.length === 1 ? named[0] : undefined
}

/**
 * A request record the rail lists beside the logs: a deployment's, kept by
 * the ingress for its route, or a site's own access log read as one.
 */
export type RequestRecord = {
  /** `deploy:<id>` or `site:<name>`, as `?requests=` carries it. */
  id: string
  label: string
  /** The address it answers at. */
  detail?: string
  product?: string
  /** The record's last hour, as a figure beside its name, and the tone its failures take. */
  figure?: string
  tone?: Tone
  /** Where the record is on the API — `/deploy/12`, `/proxy/sites/shop` — as `RequestsWorkspace` takes it. */
  base: string
  /** What a block from its rows is said to come from, in the audit log. */
  subject: string
  emptyTitle: string
  deployment?: DeploymentSummary
  site?: VHost
}

/**
 * The sites the deployment renderer writes. Their requests are the
 * deployment's own record, which the rail already lists under the
 * deployment's name.
 */
const DEPLOYMENT_SITE = "just-dashboard-"

/**
 * The records, deployments first: each deployment whose route has a record
 * (the fleet's pulse says so — an unrouted one has none to read), then each
 * nginx site that writes its requests to a file of its own.
 */
export function requestRecords(
  deployments: DeploymentSummary[],
  pulses: Record<string, TrafficPulse> | undefined,
  vhosts: VHost[],
): RequestRecord[] {
  const byName = (a: RequestRecord, b: RequestRecord) => a.label.localeCompare(b.label)
  const deployed = deployments.flatMap((deployment): RequestRecord[] => {
    const pulse = pulses?.[String(deployment.id)]
    if (pulse?.status !== "available") return []
    return [
      {
        id: `deploy:${deployment.id}`,
        label: deployment.name,
        detail: deployment.endpoint,
        product: projectProduct(deployment),
        figure: `${perMinute(pulse.perMinute)}/min`,
        tone: failingTone(pulse.errorRate),
        base: `/deploy/${deployment.id}`,
        subject: `deployment ${deployment.id}`,
        emptyTitle: "No request record for this deployment",
        deployment,
      },
    ]
  })
  const sites = vhosts
    .filter((v) => v.kind === "nginx" && v.accessLogPath && !v.name.startsWith(DEPLOYMENT_SITE))
    .map(siteRecord)
  return [...deployed.sort(byName), ...sites.sort(byName)]
}

/** One site's record: its own access log, read through the site's routes. */
export function siteRecord(vhost: VHost): RequestRecord {
  return {
    id: `site:${vhost.name}`,
    label: vhost.name,
    detail: siteHosts(vhost)[0] ?? vhost.accessLogPath,
    product: siteProduct(vhost),
    base: `/proxy/sites/${encodeURIComponent(vhost.name)}`,
    subject: `site ${vhost.name}`,
    emptyTitle: "No request record for this site",
    site: vhost,
  }
}

const HEX_ID = /^[0-9a-f]{12,64}$/

/**
 * The rail's source for an id a view asks for, and the journal unit it
 * names. A container is asked for by whatever the asker holds — a
 * deployment by its full id, a database by its name — and listed by the id
 * Docker's listing gave, so it is found by name, or by either id being the
 * other's prefix. A unit is the journal source narrowed to it.
 */
export function railSourceFor(
  sources: LogSource[],
  id: string,
): { source: LogSource; unit: string } | undefined {
  const exact = sources.find((s) => s.id === id)
  if (exact) return { source: exact, unit: "" }
  if (id.startsWith("journal:")) {
    const journal = sources.find((s) => s.kind === "journal")
    return journal && { source: journal, unit: id.slice("journal:".length) }
  }
  if (!id.startsWith("docker:")) return undefined
  const want = id.slice("docker:".length)
  const container =
    sources.find((s) => s.kind === "docker" && s.label === want) ??
    (HEX_ID.test(want)
      ? sources.find((s) => {
          const have = s.id.slice("docker:".length)
          return (
            s.kind === "docker" &&
            HEX_ID.test(have) &&
            (have.startsWith(want) || want.startsWith(have))
          )
        })
      : undefined)
  return container && { source: container, unit: "" }
}
