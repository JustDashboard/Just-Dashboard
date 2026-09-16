import { describe, expect, test } from "bun:test"
import { defaultConfiguration, withPackageManagerRunner } from "./deployment-defaults"

const candidate = (overrides) => ({
  id: "fixture",
  name: "Fixture",
  root: "",
  profile: "static",
  buildMethod: "recipe",
  confidence: "high",
  evidence: [],
  needsDecision: [],
  ...overrides,
})

describe("detected deployment serving defaults", () => {
  for (const port of [undefined, 0, 3000, 5173]) {
    test(`packaged Vite output serves on nginx port even with source port ${port}`, () => {
      const plan = defaultConfiguration("static", candidate({ outputDirectory: "dist", port }))
      expect(plan.runtime.internalPort).toBe(80)
      expect(plan.checks).toContainEqual(
        expect.objectContaining({ kind: "http", phase: "readiness", required: true }),
      )
    })
  }
  test("plain HTML and nested static roots have readiness without manual settings", () => {
    const plan = defaultConfiguration(
      "static",
      candidate({ buildMethod: "static", root: "public", outputDirectory: "" }),
    )
    expect(plan.runtime.internalPort).toBe(80)
    expect(plan.build.rootDirectory).toBe("public")
    expect(plan.build.outputDirectory).toBe("")
    expect(plan.checks).toHaveLength(1)
  })
  test("Containerfile path stays relative to the detected build root", () => {
    const plan = defaultConfiguration(
      "web",
      candidate({ buildMethod: "dockerfile", dockerfile: "Containerfile", root: "apps/web" }),
    )
    expect(plan.build.dockerfile).toBe("Containerfile")
    expect(plan.build.rootDirectory).toBe("apps/web")
  })
})

describe("package manager runner", () => {
  test("choosing a manager moves the plain script runner with it", () => {
    expect(withPackageManagerRunner("npm run build", "bun")).toBe("bun run build")
    expect(withPackageManagerRunner("bun run start", "npm")).toBe("npm run start")
  })
  test("custom commands and the lockfile default are left alone", () => {
    expect(withPackageManagerRunner("prisma generate && next build", "bun")).toBe(
      "prisma generate && next build",
    )
    expect(withPackageManagerRunner("npm run build", undefined)).toBe("npm run build")
  })
})
