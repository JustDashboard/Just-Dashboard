import { Archive, CloudUpload, Layers } from "@/components/icons"
import { cn } from "@/lib/utils"
import type { BackupJob, BackupResource, Container } from "@/lib/types"
import { LogoGlyph } from "@/components/logo"
import { ProductLogo, ProductLogos, imageProduct, imageProducts } from "@/components/product-logo"

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
 * mount it, a stack's containers carry its name. A container running from a
 * bare image id is no product, and neither is the volume it mounts — those
 * keep the kind's glyph on the same tile rather than a guessed logo.
 */

const ENGINE: Record<string, string> = { postgres: "postgresql" }

/** A saved connection's driver, the first word of its coverage detail. */
function engineOf(resource: BackupResource) {
  const driver = (resource.detail ?? "").split(" · ")[0].trim().toLowerCase()
  return ENGINE[driver] ?? driver
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
      const images = containers.filter((c) => c.composeStack === resource.name).map((c) => c.image)
      const named = imageProducts(images).filter((id) => id !== "docker")
      return named.length > 0 ? named : ["docker-compose"]
    }
    case "volume": {
      const users = (resource.detail ?? "")
        .replace(/^Mounted by /, "")
        .split(", ")
        .filter(Boolean)
      const ids = users.flatMap((name) => {
        const container = containers.find((c) => c.name === name)
        const product = container ? imageProduct(container.image) : undefined
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

const KIND_GLYPH = {
  volume: Archive,
  stack: Layers,
  deployment: CloudUpload,
} as const

/** A thing on the server as its product, or its kind's glyph on the same tile. */
export function ResourceMark({ ids, kind }: { ids: string[]; kind: BackupResource["kind"] }) {
  if (kind === "dashboard") return <DashboardMark />
  if (ids.length > 1) return <ProductLogos ids={ids} />
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
 * Where a job's archives go, where that is a product: Backblaze as itself.
 * S3 and a directory on this server keep glyphs — S3 is a protocol a dozen
 * providers speak, so no one company's mark is the honest drawing of it.
 */
export function destinationProduct(job: BackupJob) {
  return job.targetKind === "b2" ? "backblaze" : undefined
}
