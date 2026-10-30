import type { DeploymentStep, RailpackEvidence } from "@/lib/types"

/**
 * What Railpack decided for a run (`deploy/build_railpack.go`), read off the
 * Prepare build context step: on a step that planned, under its prepared
 * build; on one Railpack refused, beside the cause. Kept out of the component
 * so the run page and the tests read it the same way.
 */
export function railpackOf(step: Pick<DeploymentStep, "key" | "evidence">) {
  if (step.key !== "prepare_context") return undefined
  const evidence = step.evidence
  if (!isRecord(evidence)) return undefined
  const prepared = isRecord(evidence.prepared) ? evidence.prepared : undefined
  const candidate = prepared?.railpack ?? evidence.railpack
  if (!isRecord(candidate) || typeof candidate.version !== "string") return undefined
  return candidate as unknown as RailpackEvidence
}

/** The step's evidence without the Railpack record the view draws, for the generic reading. */
export function withoutRailpack(evidence: Record<string, unknown>) {
  const rest: Record<string, unknown> = { ...evidence }
  delete rest.railpack
  if (isRecord(rest.prepared)) {
    const prepared = { ...rest.prepared }
    delete prepared.railpack
    rest.prepared = prepared
  }
  return rest
}

/** A resolved package as one line: "node 24.22.0", with what asked for it after. */
export function railpackPackageLine(pkg: RailpackEvidence["packages"][number]) {
  const version = pkg.resolved || pkg.requested
  const asked =
    pkg.requested && pkg.resolved && pkg.requested !== pkg.resolved
      ? `${pkg.requested}${pkg.source ? `, ${pkg.source}` : ""}`
      : pkg.source
  return { name: version ? `${pkg.name} ${version}` : pkg.name, asked }
}

/**
 * The images a plan builds from, as Railpack names them, and the mise release
 * its own builder and runtime are tagged by — the pin Railpack keeps, which is
 * not a digest this dashboard resolved.
 */
export function railpackImages(evidence: RailpackEvidence) {
  return evidence.images.map((image) => {
    const own = /^ghcr\.io\/railwayapp\/railpack-(builder|runtime):mise-(.+)$/.exec(image)
    return { image, role: own?.[1], mise: own?.[2] }
  })
}

/** Railpack's own lines worth reading: what failed, then what it warned about. */
export function railpackNotices(evidence: RailpackEvidence) {
  return (evidence.logs ?? [])
    .filter((log) => log.level === "error" || log.level === "warn")
    .sort((a, b) => Number(b.level === "error") - Number(a.level === "error"))
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value)
}
