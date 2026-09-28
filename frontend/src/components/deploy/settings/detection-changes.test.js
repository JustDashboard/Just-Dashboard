import { expect, test } from "bun:test"
import { applyDetectionChanges, buildFieldChange, proposedValue } from "./detection-changes"

const change = (field, saved, detected, changed = true) => ({
  field,
  label: field,
  saved,
  detected,
  changed,
})

test("each change sets the one field it names", () => {
  const build = {
    method: "recipe",
    recipe: "node",
    packageManager: "npm",
    buildCommand: "npm run build",
    startCommand: "npm run start",
    outputDirectory: "build",
    rootDirectory: "apps/web",
  }
  const runtime = { strategy: "blue_green", internalPort: 3000 }
  const applied = applyDetectionChanges(build, runtime, [
    change("build.packageManager", "npm", "bun"),
    change("build.buildCommand", "npm run build", "bun run build"),
    change("build.outputDirectory", "build", ""),
    change("build.spaFallback", "false", "true"),
    change("runtime.internalPort", "3000", "8080"),
  ])
  expect(applied.build).toEqual({
    ...build,
    packageManager: "bun",
    buildCommand: "bun run build",
    outputDirectory: undefined,
    spaFallback: true,
  })
  expect(applied.runtime).toEqual({ strategy: "blue_green", internalPort: 8080 })
  // Nothing applied is nothing changed, and the inputs are left alone.
  expect(applyDetectionChanges(build, runtime, [])).toEqual({ build, runtime })
  expect(build.packageManager).toBe("npm")
})

test("a Dockerfile's stage is applied like any other build field", () => {
  const build = { method: "dockerfile", dockerfile: "Dockerfile" }
  const applied = applyDetectionChanges(build, {}, [change("build.target", "", "production")])
  expect(applied.build).toEqual({ ...build, target: "production" })
  expect(buildFieldChange(change("build.target", "", "production"))).toBe(true)
})

test("the Build form applies build fields only", () => {
  expect(buildFieldChange(change("build.goPackage", "", "cmd/api"))).toBe(true)
  expect(buildFieldChange(change("runtime.internalPort", "3000", "80"))).toBe(false)
})

test("values read the way the form shows them", () => {
  expect(proposedValue(change("build.spaFallback", "false", "true"), "true")).toBe("on")
  expect(proposedValue(change("build.packageManager", "", "bun"), "")).toBe("lockfile decides")
  expect(proposedValue(change("build.outputDirectory", "dist", ""), "")).toBe("none")
})
