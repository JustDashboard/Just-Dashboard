import { expect, test } from "bun:test"
import {
  railpackImages,
  railpackNotices,
  railpackOf,
  railpackPackageLine,
  withoutRailpack,
} from "./railpack-model"
import {
  BUILD_METHOD_SHORT,
  RAILPACK_PROVIDERS,
  buildMethodHint,
  railpackProviderLabel,
} from "./vocabulary"

const evidence = {
  version: "0.43.0",
  image: "ghcr.io/railwayapp/railpack-frontend:v0.43.0@sha256:355e",
  providers: ["node"],
  packages: [{ name: "node", requested: "lts", resolved: "24.22.0", source: "railpack default" }],
  steps: [{ name: "install", commands: ["npm install"] }],
  startCommand: "npm run start",
  images: [
    "ghcr.io/railwayapp/railpack-builder:mise-2026.10.14",
    "ghcr.io/railwayapp/railpack-runtime:mise-2026.10.14",
    "dunglas/frankenphp:php8.4-trixie",
  ],
  logs: [
    { level: "info", message: "Detected Node" },
    { level: "warn", message: "No node package manager detected, using npm" },
    { level: "error", message: "No start command detected." },
  ],
  success: true,
}

test("Railpack's decision is read from a planned step and from a refused one", () => {
  const planned = {
    key: "prepare_context",
    evidence: { buildRoot: "/w", prepared: { method: "railpack", railpack: evidence } },
  }
  expect(railpackOf(planned)?.version).toBe("0.43.0")
  const refused = {
    key: "prepare_context",
    evidence: {
      cause: { code: "railpack_no_provider" },
      railpack: { ...evidence, success: false },
    },
  }
  expect(railpackOf(refused)?.success).toBe(false)
  expect(railpackOf({ key: "build_artifact", evidence: planned.evidence })).toBeUndefined()
  expect(
    railpackOf({ key: "prepare_context", evidence: { prepared: { method: "recipe" } } }),
  ).toBeUndefined()
  const rest = withoutRailpack(planned.evidence)
  expect(rest.prepared).toEqual({ method: "railpack" })
  expect(planned.evidence.prepared.railpack).toBe(evidence)
  expect(withoutRailpack(refused.evidence)).toEqual({ cause: { code: "railpack_no_provider" } })
})

test("packages, images and notices read as Railpack decided them", () => {
  expect(railpackPackageLine(evidence.packages[0])).toEqual({
    name: "node 24.22.0",
    asked: "lts, railpack default",
  })
  expect(
    railpackPackageLine({ name: "python", resolved: "3.13.16", requested: "3.13.16" }),
  ).toEqual({
    name: "python 3.13.16",
    asked: undefined,
  })
  expect(railpackImages(evidence).map((image) => [image.role, image.mise])).toEqual([
    ["builder", "2026.10.14"],
    ["runtime", "2026.10.14"],
    [undefined, undefined],
  ])
  expect(railpackNotices(evidence).map((log) => log.level)).toEqual(["error", "warn"])
})

test("Railpack is offered with its description and its providers by name", () => {
  expect(BUILD_METHOD_SHORT.railpack).toBe("Railpack")
  expect(buildMethodHint("recipe")).toBeUndefined()
  expect(buildMethodHint("railpack", "golang")).toContain("reads this directory as Go")
  expect(railpackProviderLabel("node")).toBe("Node.js")
  expect(railpackProviderLabel("cpp")).toBe("C/C++")
  expect(railpackProviderLabel("staticfile")).toBe("Static files")
  expect(railpackProviderLabel("unknown")).toBe("unknown")
  expect(new Set(RAILPACK_PROVIDERS.map(([provider]) => provider)).size).toBe(15)
})

test("a Railpack configuration file is a .json file inside the root", async () => {
  const { validRailpackConfig } = await import("./deployment-defaults")
  for (const path of ["railpack.json", "deploy/railpack.prod.json", "./railpack.json"])
    expect(validRailpackConfig(path)).toBe(true)
  for (const path of [
    "../railpack.json",
    "/etc/railpack.json",
    "railpack.toml",
    ".just-dashboard/railpack.json",
    "a/../../b.json",
  ])
    expect(validRailpackConfig(path)).toBe(false)
})
