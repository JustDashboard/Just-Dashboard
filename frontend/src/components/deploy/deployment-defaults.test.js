import { describe, expect, test } from "bun:test"
import {
  canGenerateSecret,
  checksForRuntime,
  defaultConfiguration,
  discoveredEnvironmentRows,
  generateSecretValue,
  mergeDiscoveredRows,
  validateConfiguration,
  withPackageManagerRunner,
  commandsForPackageManager,
  packageManagerOptions,
  packageManagerReading,
  automaticPackageManagerHint,
} from "./deployment-defaults"

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
  test("a game server's readiness check is a real check kind, not the closed game_handshake", () => {
    const plan = defaultConfiguration("game", candidate({ profile: "game", buildMethod: "image" }))
    // validateCheckConfiguration (backend/internal/deploy/checks.go) refuses
    // "game_handshake" any execution fields at all; "tcp" is the one kind
    // that both passes validation and actually probes the server's port.
    expect(plan.checks).toEqual([
      {
        name: "Game port reachable",
        kind: "tcp",
        phase: "readiness",
        required: true,
        config: { port: 25565 },
      },
    ])
  })
})

describe("package manager runner", () => {
  test("choosing a manager moves the plain script runner with it", () => {
    expect(withPackageManagerRunner("npm run build", "bun")).toBe("bun run build")
    expect(withPackageManagerRunner("bun run start", "npm")).toBe("npm run start")
  })
  test("the start and test shorthands and leading assignments follow too", () => {
    expect(withPackageManagerRunner("npm start", "bun")).toBe("bun run start")
    expect(withPackageManagerRunner("yarn start", "yarn")).toBe("yarn start")
    expect(withPackageManagerRunner("yarn test", "pnpm")).toBe("pnpm run test")
    expect(withPackageManagerRunner("NODE_ENV=production npm run start", "bun")).toBe(
      "NODE_ENV=production bun run start",
    )
    expect(withPackageManagerRunner("bun test", "npm")).toBe("bun test")
    expect(withPackageManagerRunner("npm run build --if-present", "bun")).toBe(
      "npm run build --if-present",
    )
  })
  test("custom commands and the lockfile default are left alone", () => {
    expect(withPackageManagerRunner("prisma generate && next build", "bun")).toBe(
      "prisma generate && next build",
    )
    expect(withPackageManagerRunner("npm run build", undefined)).toBe("npm run build")
    expect(withPackageManagerRunner("yarn build", "bun")).toBe("yarn build")
  })
  test("a detected schema step follows the manager with the start script", () => {
    expect(withPackageManagerRunner("npx prisma migrate deploy && npm run start", "bun")).toBe(
      "bunx prisma migrate deploy && bun run start",
    )
    expect(withPackageManagerRunner("bunx prisma db push && bun run start", "pnpm")).toBe(
      "pnpm exec prisma db push && pnpm run start",
    )
    expect(withPackageManagerRunner("yarn knex migrate:latest && yarn run start", "npm")).toBe(
      "npx knex migrate:latest && npm run start",
    )
    expect(
      withPackageManagerRunner("pnpm exec drizzle-kit migrate && node dist/index.js", "yarn"),
    ).toBe("yarn drizzle-kit migrate && node dist/index.js")
  })
})

describe("catalogue defaults carried into the plan", () => {
  test("a single-page site keeps its fallback and a multi-page site has none", () => {
    const spa = defaultConfiguration(
      "static",
      candidate({ outputDirectory: "dist", spaFallback: true }),
    )
    expect(spa.build.spaFallback).toBe(true)
    const site = defaultConfiguration("static", candidate({ outputDirectory: "dist" }))
    expect(site.build.spaFallback).toBeUndefined()
    // A server never serves through nginx, so the flag has nothing to apply to.
    const server = defaultConfiguration(
      "web",
      candidate({
        profile: "web",
        startCommand: "node .output/server/index.mjs",
        port: 3000,
        spaFallback: true,
      }),
    )
    expect(server.build.spaFallback).toBeUndefined()
    expect(server.runtime.internalPort).toBe(3000)
  })
  test("a web candidate whose source named no port is left asking rather than given 3000", () => {
    // `Port` is `omitempty`, so a Dockerfile with no EXPOSE — or several —
    // arrives with no port at all. Inventing one here let it reach Review with
    // a readiness check built on a port nothing listens to; zero is what sends
    // the reader to the screen that owns the field.
    const plan = defaultConfiguration(
      "web",
      candidate({ profile: "web", buildMethod: "dockerfile" }),
    )
    expect(plan.runtime.internalPort).toBe(0)
    expect(plan.checks).toEqual([])
  })
  test("typing the port a gated workload was missing earns it the readiness gate", () => {
    const plan = defaultConfiguration(
      "web",
      candidate({ profile: "web", buildMethod: "dockerfile" }),
    )
    const checks = checksForRuntime(plan.checks, "web", 8080)
    expect(checks).toContainEqual(
      expect.objectContaining({ kind: "http", phase: "readiness", required: true }),
    )
    // Only once: a plan that already verifies itself is not given a second gate.
    expect(checksForRuntime(checks, "web", 8080)).toHaveLength(checks.length)
    // And a workload preflight demands no gate from keeps what it had.
    expect(checksForRuntime(plan.checks, "image", 8080)).toEqual(plan.checks)
    expect(checksForRuntime(checks, "worker", 8080)).toEqual([])
  })
  test("a Python candidate keeps its interpreter and framework port", () => {
    const plan = defaultConfiguration(
      "web",
      candidate({
        profile: "web",
        recipe: "python",
        pythonVersion: "3.12",
        startCommand: "uvicorn main:app --host 0.0.0.0 --port 8000",
        port: 8000,
      }),
    )
    expect(plan.build.recipe).toBe("python")
    expect(plan.build.pythonVersion).toBe("3.12")
    expect(plan.runtime.internalPort).toBe(8000)
    expect(plan.checks[0]).toMatchObject({ kind: "http", phase: "readiness" })
  })
  test("a Python version outside the recipe's range is refused before the request", () => {
    const plan = defaultConfiguration(
      "web",
      candidate({ profile: "web", recipe: "python", port: 8000 }),
    )
    expect(
      validateConfiguration({ ...plan, build: { ...plan.build, pythonVersion: "3.9" } }, "web"),
    ).toHaveProperty("pythonVersion")
    expect(
      validateConfiguration({ ...plan, build: { ...plan.build, pythonVersion: "3.12" } }, "web"),
    ).not.toHaveProperty("pythonVersion")
  })
})

describe("discovered environment rows", () => {
  const detected = candidate({
    variables: [
      { name: "DATABASE_URL", sources: [".env.example", "src/db.ts"] },
      { name: "SESSION_SECRET", example: "change me", sources: [".env.example"] },
    ],
  })
  test("open with the detected names, their examples as placeholders and their source", () => {
    expect(discoveredEnvironmentRows(detected)).toEqual([
      {
        name: "DATABASE_URL",
        value: "",
        example: undefined,
        source: ".env.example",
        detected: true,
      },
      {
        name: "SESSION_SECRET",
        value: "",
        example: "change me",
        source: ".env.example",
        detected: true,
      },
    ])
    expect(discoveredEnvironmentRows(candidate({}))).toEqual([{ name: "", value: "" }])
    expect(discoveredEnvironmentRows(undefined)).toEqual([{ name: "", value: "" }])
  })
  test("a re-detection adds new names without touching typed rows or repeating names", () => {
    const typed = [
      { name: "DATABASE_URL", value: "postgres://…" },
      { name: "", value: "" },
    ]
    expect(mergeDiscoveredRows(typed, discoveredEnvironmentRows(detected))).toEqual([
      { name: "DATABASE_URL", value: "postgres://…" },
      {
        name: "SESSION_SECRET",
        value: "",
        example: "change me",
        source: ".env.example",
        detected: true,
      },
    ])
    const same = [
      { name: "SESSION_SECRET", value: "x" },
      { name: "DATABASE_URL", value: "" },
    ]
    expect(mergeDiscoveredRows(same, discoveredEnvironmentRows(detected))).toBe(same)
  })
})

describe("self-issued secrets", () => {
  test("only the secrets an application issues to itself can be generated", () => {
    for (const name of [
      "APP_KEY",
      "SESSION_SECRET",
      "NEXTAUTH_SECRET",
      "DJANGO_SECRET_KEY",
      "SECRET_KEY_BASE",
      "N8N_ENCRYPTION_KEY",
    ])
      expect(canGenerateSecret(name)).toBe(true)
    for (const name of [
      "STRIPE_KEY",
      "OPENAI_API_KEY",
      "DATABASE_URL",
      "RAILS_MASTER_KEY",
      "GITHUB_TOKEN",
      "KEY",
    ])
      expect(canGenerateSecret(name)).toBe(false)
  })
  test("a generated value takes the shape its framework reads", () => {
    const fixed = (length) => new Uint8Array(length).fill(7)
    expect(generateSecretValue("APP_KEY", fixed)).toBe(
      `base64:${btoa(String.fromCharCode(...fixed(32)))}`,
    )
    expect(generateSecretValue("SECRET_KEY_BASE", fixed)).toBe("07".repeat(64))
    expect(generateSecretValue("SESSION_SECRET", fixed)).toBe("07".repeat(32))
    expect(generateSecretValue("SESSION_SECRET")).toMatch(/^[0-9a-f]{64}$/)
    expect(generateSecretValue("APP_KEY")).toMatch(/^base64:[A-Za-z0-9+/]{43}=$/)
  })
  test("a Laravel import arrives with its application key minted", () => {
    const fixed = (length) => new Uint8Array(length).fill(1)
    const rows = discoveredEnvironmentRows(
      candidate({
        framework: "laravel",
        variables: [
          { name: "APP_KEY", sources: [".env.example"] },
          { name: "APP_URL", example: "http://localhost", sources: [".env.example"] },
        ],
      }),
      fixed,
    )
    expect(rows[0]).toEqual({
      name: "APP_KEY",
      value: `base64:${btoa(String.fromCharCode(...fixed(32)))}`,
      example: undefined,
      source: ".env.example",
      detected: true,
      generated: true,
    })
    expect(rows[1].value).toBe("")
    // Another framework's APP_KEY is left for the operator: the form does
    // not know what that application reads into it.
    const other = discoveredEnvironmentRows(
      candidate({
        framework: "nextjs",
        variables: [{ name: "APP_KEY", sources: [".env.example"] }],
      }),
      fixed,
    )
    expect(other[0].value).toBe("")
  })
})

// The incident's shape: bun.lock matches package.json, package-lock.json is
// fifteen dependencies behind, and detection recorded what each choice runs.
const incident = candidate({
  profile: "web",
  recipe: "node",
  packageManager: "bun",
  packageManagers: ["bun", "npm"],
  buildCommand: "bun run build",
  startCommand: "bunx prisma db push && bun run start",
  lockfiles: [
    { path: "bun.lock", manager: "bun", state: "in_sync", note: "bun.lock matches package.json" },
    {
      path: "package-lock.json",
      manager: "npm",
      state: "stale",
      note: "package-lock.json is missing 15 dependencies (prisma, zod and 13 more)",
    },
  ],
  nodeInstalls: [
    {
      manager: "bun",
      lockfile: "bun.lock",
      install: "bun install --frozen-lockfile",
      buildCommand: "bun run build",
      startCommand: "bunx prisma db push && bun run start",
    },
    {
      manager: "npm",
      lockfile: "package-lock.json",
      install: "npm install --no-audit --no-fund",
      buildCommand: "npm run build",
      startCommand: "npx prisma db push && npm run start",
    },
    {
      manager: "pnpm",
      buildCommand: "pnpm run build",
      startCommand: "pnpm exec prisma db push && pnpm run start",
      findings: [{ code: "package_manager_lockfile_missing", severity: "blocked", title: "x" }],
    },
    {
      manager: "yarn",
      buildCommand: "yarn run build",
      startCommand: "yarn prisma db push && yarn run start",
      findings: [{ code: "package_manager_lockfile_missing", severity: "blocked", title: "x" }],
    },
  ],
})

describe("choosing a package manager from what detection read", () => {
  test("each option says whether its lockfile matches, and a choice the build refuses is off", () => {
    expect(packageManagerOptions(incident)).toEqual([
      { value: "bun", label: "Bun", hint: "bun.lock matches", disabled: false },
      { value: "npm", label: "npm", hint: "package-lock.json out of sync", disabled: false },
      { value: "pnpm", label: "pnpm", hint: "no lockfile", disabled: true },
      { value: "yarn", label: "Yarn", hint: "no lockfile", disabled: true },
    ])
    expect(automaticPackageManagerHint(incident)).toBe("Bun")
    expect(automaticPackageManagerHint(candidate({ packageManagers: ["bun", "npm"] }))).toBe(
      "choose one",
    )
  })
  test("the reading under the field names the lockfile and the install", () => {
    expect(packageManagerReading(incident, undefined)).toBe(
      "bun.lock matches package.json · bun install --frozen-lockfile",
    )
    expect(packageManagerReading(incident, "npm")).toBe(
      "package-lock.json is missing 15 dependencies (prisma, zod and 13 more) · npm install --no-audit --no-fund",
    )
    expect(packageManagerReading(incident, "pnpm")).toBeUndefined()
  })
  test("detected commands are swapped whole; the operator's keep their words", () => {
    expect(commandsForPackageManager(incident, incident, "npm")).toEqual({
      buildCommand: "npm run build",
      startCommand: "npx prisma db push && npm run start",
    })
    expect(
      commandsForPackageManager(
        incident,
        { buildCommand: "npm run build -- --debug", startCommand: "node server.js" },
        "bun",
      ),
    ).toEqual({ buildCommand: "npm run build -- --debug", startCommand: "node server.js" })
    const kit = candidate({
      packageManager: "bun",
      nodeInstalls: [
        { manager: "bun", startCommand: "bun ./build/index.js", buildCommand: "bun run build" },
        { manager: "npm", startCommand: "node build", buildCommand: "npm run build" },
      ],
    })
    expect(
      commandsForPackageManager(
        kit,
        { buildCommand: "bun run build", startCommand: "bun ./build/index.js" },
        "npm",
      ),
    ).toEqual({ buildCommand: "npm run build", startCommand: "node build" })
  })
  test("from the lockfile moves a stale runner to the manager detection resolved", () => {
    expect(
      commandsForPackageManager(
        incident,
        { buildCommand: "npm run build", startCommand: "npx prisma db push && npm run start" },
        undefined,
      ),
    ).toEqual({
      buildCommand: "bun run build",
      startCommand: "bunx prisma db push && bun run start",
    })
    expect(
      commandsForPackageManager(undefined, { buildCommand: "npm run build" }, undefined),
    ).toEqual({
      buildCommand: "npm run build",
    })
  })
  test("a registry credential row says the install reads it", () => {
    const rows = discoveredEnvironmentRows(
      candidate({
        variables: [
          { name: "NODE_AUTH_TOKEN", sources: [".npmrc"], step: "install", installRequired: true },
          { name: "DATABASE_URL", sources: [".env.example"] },
        ],
      }),
    )
    expect(rows.map((row) => row.source)).toEqual([".npmrc · read by the install", ".env.example"])
  })
})
