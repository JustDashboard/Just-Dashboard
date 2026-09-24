import { describe, expect, test } from "bun:test"
import {
  browserInlined,
  canGenerateSecret,
  checksForRuntime,
  defaultConfiguration,
  discoveredEnvironmentRows,
  generateSecretValue,
  mergeDiscoveredRows,
  pointsAtLocalhost,
  rowNeedsOperator,
  validateConfiguration,
  withPackageManagerRunner,
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
      "APP_KEYS",
      "SESSION_SECRET",
      "NEXTAUTH_SECRET",
      "BETTER_AUTH_SECRET",
      "PAYLOAD_SECRET",
      "API_TOKEN_SALT",
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
      // A provider issues these, however much they end like a self-issued secret.
      "STRIPE_SECRET_KEY",
      "CLERK_SECRET_KEY",
      "SUPABASE_JWT_SECRET",
      "AUTH_GITHUB_SECRET",
      "GOOGLE_CLIENT_SECRET",
      "STRIPE_WEBHOOK_SECRET",
    ])
      expect(canGenerateSecret(name)).toBe(false)
  })
  test("a generated value takes the shape its framework reads", () => {
    const fixed = (length) => new Uint8Array(length).fill(7)
    expect(generateSecretValue("APP_KEY", fixed)).toBe(
      `base64:${btoa(String.fromCharCode(...fixed(32)))}`,
    )
    expect(generateSecretValue("APP_KEYS", fixed).split(",")).toHaveLength(4)
    expect(generateSecretValue("SECRET_KEY_BASE", fixed)).toBe("07".repeat(64))
    expect(generateSecretValue("SESSION_SECRET", fixed)).toBe("07".repeat(32))
    expect(generateSecretValue("SESSION_SECRET")).toMatch(/^[0-9a-f]{64}$/)
    expect(generateSecretValue("APP_KEY")).toMatch(/^base64:[A-Za-z0-9+/]{43}=$/)
  })
})

describe("variables detection set up", () => {
  const detected = candidate({
    profile: "web",
    framework: "nextjs",
    browserPrefixes: ["NEXT_PUBLIC_"],
    variables: [
      {
        name: "AUTH_SECRET",
        sources: [".env.example"],
        setup: "generate",
        setupReason: "Auth.js signs and encrypts sessions with it",
        generateLength: 32,
        generateFormat: "base64",
      },
      {
        name: "AUTH_URL",
        sources: [".env.example"],
        setup: "domain",
        setupReason: "Auth.js builds its callback URLs from it",
        domainTemplate: "{{scheme}}://{{hostname}}",
      },
      {
        name: "LOG_LEVEL",
        example: "debug",
        sources: [".env.example"],
        setup: "default",
        setupReason: "documented in .env.example",
        defaultValue: "info",
      },
      { name: "DATABASE_URL", sources: ["prisma.config.ts"], required: true, phase: "build" },
      { name: "NEXT_PUBLIC_API_URL", sources: ["src/api.ts"], localhostIn: ".env.production" },
      { name: "RESEND_API_KEY", sources: [".env.example"] },
    ],
  })
  test("the plan declares what detection set up, and a required name", () => {
    const plan = defaultConfiguration("web", detected)
    expect(plan.variables).toEqual([
      {
        name: "AUTH_SECRET",
        sensitivity: "secret",
        scopes: ["runtime", "build"],
        generate: 32,
        generateFormat: "base64",
      },
      {
        name: "AUTH_URL",
        sensitivity: "plain",
        scopes: ["runtime", "build"],
        domainTemplate: "{{scheme}}://{{hostname}}",
        value: "",
      },
      { name: "LOG_LEVEL", sensitivity: "plain", scopes: ["runtime", "build"], value: "info" },
      { name: "DATABASE_URL", sensitivity: "secret", scopes: ["runtime", "build"], required: true },
    ])
    // A static site's values are build input only.
    expect(defaultConfiguration("static", detected).variables[0].scopes).toEqual(["build"])
  })
  test("rows say how they are answered, and only unanswered ones need the operator", () => {
    const rows = discoveredEnvironmentRows(detected)
    expect(rows.map((row) => [row.name, row.setup, row.required, row.browser])).toEqual([
      ["AUTH_SECRET", "generate", undefined, undefined],
      ["AUTH_URL", "domain", undefined, undefined],
      ["LOG_LEVEL", "default", undefined, undefined],
      ["DATABASE_URL", undefined, true, undefined],
      ["NEXT_PUBLIC_API_URL", undefined, undefined, true],
      ["RESEND_API_KEY", undefined, undefined, undefined],
    ])
    expect(rows.every((row) => row.value === "")).toBe(true)
    expect(rows.filter(rowNeedsOperator).map((row) => row.name)).toEqual([
      "DATABASE_URL",
      "NEXT_PUBLIC_API_URL",
      "RESEND_API_KEY",
    ])
    expect(rows[4].localhostIn).toBe(".env.production")
    expect(
      rowNeedsOperator({ name: "RAILS_MASTER_KEY", value: "", detected: true, setup: "paste" }),
    ).toBe(true)
  })
  test("browser prefixes follow detection, and fall back to the unambiguous conventions", () => {
    expect(browserInlined("NEXT_PUBLIC_SITE_URL")).toBe(true)
    expect(browserInlined("VITE_API_URL")).toBe(true)
    expect(browserInlined("PUBLIC_URL")).toBe(false)
    expect(browserInlined("PUBLIC_SITE_NAME", ["PUBLIC_", "VITE_"])).toBe(true)
    expect(browserInlined("NEXT_PUBLIC_")).toBe(false)
  })
  test("a value pointing at loopback is recognised in every connection shape", () => {
    for (const [name, value] of [
      ["DATABASE_URL", "postgresql://postgres:postgres@localhost:5432/app"],
      ["MONGODB_URI", "mongodb://db.internal:27017,127.0.0.1:27018/app"],
      ["SPRING_DATASOURCE_URL", "jdbc:postgresql://localhost:5432/app"],
      ["CONNECTIONSTRINGS__DEFAULT", "Host=localhost;Database=app"],
      ["REDIS_HOST", "127.0.0.1:6379"],
      ["NEXT_PUBLIC_API_URL", "http://[::1]:8000"],
    ])
      expect(pointsAtLocalhost(name, value)).toBe(true)
    for (const [name, value] of [
      ["DATABASE_URL", "postgres://db-4.jd.internal:5432/app"],
      ["DATABASE_URL", "${{database.4}}"],
      ["HOST", "127.0.0.1"],
      ["SITE_NAME", "localhost"],
      ["API_URL", ""],
    ])
      expect(pointsAtLocalhost(name, value)).toBe(false)
  })
})
