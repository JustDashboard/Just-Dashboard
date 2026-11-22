import { bytes, duration } from "@/lib/format"
import type {
  DeploymentBuildTiming,
  DeploymentEngineRun,
  DeploymentRecentRun,
  DeploymentStep,
} from "@/lib/types"

/** The Build step's timing, as the engine read it from BuildKit's lines (`build_timing.go`). */
export function buildTimingOf(step: Pick<DeploymentStep, "key" | "evidence">) {
  if (step.key !== "build_artifact") return undefined
  const timing = step.evidence?.timing
  if (typeof timing !== "object" || timing === null || Array.isArray(timing)) return undefined
  const value = timing as Partial<DeploymentBuildTiming>
  return typeof value.steps === "number" ? (value as DeploymentBuildTiming) : undefined
}

/** "6s", "1m 47s", "under 1s": a part of a build, rounded as it is read. */
function took(seconds: number) {
  return seconds < 0.5 ? "under 1s" : duration(Math.round(seconds))
}

/**
 * Where a build's time went, as one sentence: "Build 1m 47s: install 6s,
 * compile 25s, image export 61s for 865 MB; 9 of 14 steps cached." The image
 * export is BuildKit compressing the layers and the image store unpacking
 * them, the two halves of one wait. A part the cache answered says so; a part
 * the build did not have is left out.
 */
export function buildTimingSentence(timing: DeploymentBuildTiming, seconds?: number) {
  const parts: string[] = []
  if (timing.installCached) parts.push("install cached")
  else if (timing.installSeconds) parts.push(`install ${took(timing.installSeconds)}`)
  if (timing.compileCached) parts.push("compile cached")
  else if (timing.compileSeconds) parts.push(`compile ${took(timing.compileSeconds)}`)
  const exported = (timing.exportSeconds ?? 0) + (timing.unpackSeconds ?? 0)
  const image = exported > 0 ? exported : (timing.imageSeconds ?? 0)
  const size = timing.imageBytes ? ` for ${bytes(timing.imageBytes, 0)}` : ""
  if (image > 0 || size) parts.push(`image export ${took(image)}${size}`)
  const head = seconds !== undefined ? `Build ${duration(Math.round(seconds))}` : "Build"
  const cached =
    timing.steps > 0 ? `${timing.cachedSteps} of ${timing.steps} steps cached` : undefined
  const body = [parts.join(", "), cached].filter(Boolean).join("; ")
  return body ? `${head}: ${body}.` : undefined
}

/** Runs measured against one another: a deploy is not a restart. */
function sameKind(operation: string) {
  return operation === "redeploy" || operation === "force_build" ? "deploy" : operation
}

/**
 * How long this kind of run usually takes, from the project's recent runs the
 * page already holds: the median of the ones that succeeded, each from its
 * claim to its end, as the header measures a run. Fewer than three is not
 * "usually", and gives nothing.
 */
export function usualRunSeconds(
  recent: DeploymentRecentRun[] | undefined,
  run: Pick<DeploymentEngineRun, "id" | "operation">,
) {
  const kind = sameKind(run.operation)
  const seconds = (recent ?? []).flatMap((entry) => {
    if (entry.id === run.id || entry.state !== "succeeded" || !entry.endedAt) return []
    if (sameKind(entry.operation) !== kind) return []
    const start = Date.parse(entry.claimedAt ?? entry.requestedAt)
    const end = Date.parse(entry.endedAt)
    return Number.isNaN(start) || Number.isNaN(end) || end < start ? [] : [(end - start) / 1000]
  })
  if (seconds.length < 3) return undefined
  seconds.sort((a, b) => a - b)
  const middle = Math.floor(seconds.length / 2)
  return seconds.length % 2 ? seconds[middle] : (seconds[middle - 1] + seconds[middle]) / 2
}
