import { Archive, CloudUpload, Layers, Servers } from "@/components/icons"
import { cn } from "@/lib/utils"
import type { BackupJob, BackupResource, Container } from "@/lib/types"
import { LogoGlyph } from "@/components/logo"
import {
  ProductGlyph,
  ProductLogo,
  ProductLogos,
  containerProduct,
  containerProducts,
  hasProductLogo,
} from "@/components/product-logo"

/**
 * What a backup protects and where it goes, drawn as the products they are
 * (§14): a database as its engine, a proxy's configuration as nginx or Caddy,
 * a repository as git, a volume as the product of the container that keeps
 * its data there, a stack as its services' products overlapping, the
 * dashboard as its own mark — and a job's destination as the service it
 * writes to.
 *
 * The coverage report names things rather than images, so volumes and stacks
 * are joined to the container list: a volume's detail says which containers
 * mount it, a stack's containers carry its name. A container whose image
 * names no product is none, and neither is the volume it mounts — those keep
 * the kind's glyph on the same tile rather than a guessed logo.
 */

const ENGINE: Record<string, string> = { postgres: "postgresql" }

/** A saved connection's driver, the first word of its coverage detail. */
function engineOf(resource: BackupResource) {
  return engineProduct((resource.detail ?? "").split(" · ")[0])
}

/** A database driver as the engine's product: a dump in a run's manifest names its driver. */
export function engineProduct(driver: string) {
  const key = driver.trim().toLowerCase()
  return ENGINE[key] ?? key
}

export function resourceProducts(
  resource: BackupResource,
  containers: Container[],
  resources: BackupResource[],
): string[] {
  switch (resource.kind) {
    case "proxy":
      return [resource.id === "caddy" ? "caddy" : "nginx-static"]
    case "database":
      return [engineOf(resource)]
    case "repository":
      return ["git"]
    case "stack": {
      const members = containers.filter((c) => c.composeStack === resource.name)
      const named = containerProducts(members).filter((id) => id !== "docker")
      return named.length > 0 ? named : ["docker-compose"]
    }
    case "volume": {
      const users = (resource.detail ?? "")
        .replace(/^Mounted by /, "")
        .split(", ")
        .filter(Boolean)
      const ids = users.flatMap((name) => {
        const container = containers.find((c) => c.name === name)
        const product = container ? containerProduct(container) : undefined
        if (product && product !== "docker") return [product]
        // A database container is often run from a bare image id, and its
        // saved connection is named after it: the engine is the product.
        const database = resources.find((r) => r.kind === "database" && r.name === name)
        return database ? [engineOf(database)] : []
      })
      return [...new Set(ids)]
    }
    default:
      return []
  }
}

/**
 * What a job covers, as products: the resources whose coverage names it, and
 * the saved databases it dumps — a dump covers a connection, not a path.
 */
export function jobProducts(
  job: BackupJob,
  resources: BackupResource[],
  containers: Container[],
): string[] {
  return [
    ...new Set(
      resources
        .filter(
          (r) =>
            r.coveredBy.some((c) => c.jobId === job.id) ||
            (r.connectionId !== undefined && job.databaseDumps?.includes(r.connectionId)),
        )
        .flatMap((r) => resourceProducts(r, containers, resources)),
    ),
  ]
}

const KIND_GLYPH = {
  volume: Archive,
  stack: Layers,
  deployment: CloudUpload,
} as const

/** A thing on the server as its product, or its kind's glyph on the same tile. */
export function ResourceMark({
  ids,
  kind,
  ring,
}: {
  ids: string[]
  kind: BackupResource["kind"]
  /** The ground a stack of marks sits on, as `ProductLogos` takes it. */
  ring?: string
}) {
  if (kind === "dashboard") return <DashboardMark />
  if (ids.length > 1) return <ProductLogos ids={ids} ring={ring} />
  return (
    <ProductLogo
      id={ids[0]}
      size="sm"
      fallback={KIND_GLYPH[kind as keyof typeof KIND_GLYPH] ?? Archive}
    />
  )
}

/** The dashboard itself, on the tile a product's logo takes. */
export function DashboardMark({ className }: { className?: string }) {
  return (
    <span
      aria-hidden
      className={cn(
        "flex size-8 shrink-0 items-center justify-center rounded-lg border border-hairline bg-background",
        className,
      )}
    >
      <LogoGlyph className="h-4 w-auto text-brand" />
    </span>
  )
}

/**
 * Where a job's archives go, where that is a product: Backblaze as itself, and
 * an S3 bucket as the provider its endpoint names. S3 is a protocol a dozen
 * providers speak, so the protocol alone is no one company's — but a job with
 * no endpoint is Amazon's, because that is where the SDK sends it, and an
 * endpoint on `r2.cloudflarestorage.com` is Cloudflare's. A host that names
 * nothing, and a directory on this server, keep glyphs.
 */
export function destinationProduct(job: BackupJob) {
  if (job.targetKind === "b2") return "backblaze"
  if (job.targetKind !== "s3") return undefined
  const host = (job.target.endpoint ?? "").trim().toLowerCase()
  if (!host) return "aws"
  return S3_HOSTS.find(([word]) => host.includes(word))?.[1]
}

const S3_HOSTS: [string, string][] = [
  ["amazonaws.com", "aws"],
  ["cloudflarestorage.com", "cloudflare"],
  ["backblazeb2.com", "backblaze"],
  ["googleapis.com", "google-cloud"],
  ["minio", "minio"],
]

const SERVICE: Record<string, string> = {
  aws: "Amazon S3",
  cloudflare: "Cloudflare R2",
  backblaze: "Backblaze B2",
  "google-cloud": "Google Cloud Storage",
  minio: "MinIO",
}

/** One place archives land: a directory here, or a bucket somewhere else. */
export type Destination = {
  key: string
  /** Whether the copy leaves this server — the question a backup exists to answer. */
  offsite: boolean
  /** Who holds it: the provider, or this server's own disk. */
  service: string
  /** The bucket, or the directory. */
  place: string
  product?: string
  jobs: BackupJob[]
}

/**
 * The places a set of jobs write to, each once: two jobs into one bucket are
 * one bucket. In the order the jobs are given, so the worst job's place leads.
 */
export function destinations(jobs: BackupJob[]): Destination[] {
  const out = new Map<string, Destination>()
  for (const job of jobs) {
    const product = destinationProduct(job)
    const offsite = job.targetKind !== "local"
    const place = offsite ? (job.target.bucket ?? "") : (job.target.path ?? "")
    const endpoint = (job.target.endpoint ?? "").trim()
    const key = `${job.targetKind}:${endpoint}:${place}`
    const known = out.get(key)
    if (known) {
      known.jobs.push(job)
      continue
    }
    out.set(key, {
      key,
      offsite,
      place,
      product,
      service: !offsite
        ? "This server's disk"
        : ((product && SERVICE[product]) ?? `S3 · ${endpoint.replace(/^https?:\/\//, "")}`),
      jobs: [job],
    })
  }
  return [...out.values()]
}

/** A destination's mark: the provider as itself, else a glyph for a disk or a bucket. */
export function DestinationGlyph({ destination }: { destination: Destination }) {
  if (hasProductLogo(destination.product)) return <ProductGlyph id={destination.product} />
  return destination.offsite ? <CloudUpload aria-hidden /> : <Servers aria-hidden />
}
