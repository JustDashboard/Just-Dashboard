import type {
  DeploymentConfiguration,
  DeploymentDetectionChange,
  NodePackageManager,
} from "@/lib/types"

type Build = DeploymentConfiguration["build"]
type Runtime = DeploymentConfiguration["runtime"]

/**
 * A detection proposal's fields applied to a plan: each change sets the one
 * field it names to what detection proposes now, and nothing else moves.
 * An empty proposal clears the field the way a cleared input does, so the
 * saved plan leaves it out rather than holding an empty string.
 */
export function applyDetectionChanges(
  build: Build,
  runtime: Runtime,
  changes: DeploymentDetectionChange[],
): { build: Build; runtime: Runtime } {
  const next = { build: { ...build }, runtime: { ...runtime } }
  for (const change of changes) {
    const value = change.detected || undefined
    switch (change.field) {
      case "build.packageManager":
        next.build.packageManager = value as NodePackageManager | undefined
        break
      case "build.buildCommand":
        next.build.buildCommand = value
        break
      case "build.startCommand":
        next.build.startCommand = value
        break
      case "build.outputDirectory":
        next.build.outputDirectory = value
        break
      case "build.goPackage":
        next.build.goPackage = value
        break
      case "build.target":
        next.build.target = value
        break
      case "build.spaFallback":
        next.build.spaFallback = change.detected === "true" || undefined
        break
      case "runtime.internalPort":
        next.runtime.internalPort = Number(change.detected) || undefined
        break
    }
  }
  return next
}

/** Whether a change is to a field the Build page's form holds. */
export function buildFieldChange(change: DeploymentDetectionChange) {
  return change.field.startsWith("build.")
}

/** A value as the proposal draws it: empty is "none", a flag reads as on and off. */
export function proposedValue(change: DeploymentDetectionChange, value: string) {
  if (change.field === "build.spaFallback") return value === "true" ? "on" : "off"
  if (change.field === "build.packageManager" && !value) return "lockfile decides"
  return value || "none"
}
