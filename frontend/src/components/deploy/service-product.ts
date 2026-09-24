import type { DeploymentSummary } from "@/lib/types"
import { imageProduct } from "@/components/product-logo"

/**
 * Which product a running service is, so it is drawn as itself (§14) rather
 * than as the sha256 id Docker hands back.
 *
 * The image it runs names it when it can — `postgres:16` is Postgres. A bare
 * image of the project's own build names nothing, and then the project does
 * (`project.product`: its template, its image, its framework or the language
 * its recipe builds). A Compose stack's own images and an adopted workload
 * have no project product to fall back on, and stay Docker's whale, which is
 * at least true.
 *
 * A module of its own so the console, which only needs a glyph, does not
 * pull the Runtime page and its charts into its route.
 */
export function serviceProduct(
  image: string | undefined,
  kind: DeploymentSummary["sourceKind"] | undefined,
  project: string | undefined,
): string {
  const named = image ? imageProduct(image) : "docker"
  if (named !== "docker" || kind === "compose" || kind === "import") return named
  return project ?? named
}

/**
 * What a named volume is drawn as: the product of the container that keeps
 * its data in it, by the same rule. A bare Docker whale would say nothing the
 * volume glyph does not, so it is none, and the volume keeps Docker's glyph.
 */
export function volumeProduct(
  image: string | undefined,
  kind: DeploymentSummary["sourceKind"] | undefined,
  project: string | undefined,
): string | undefined {
  const named = serviceProduct(image, kind, project)
  return named === "docker" ? undefined : named
}
