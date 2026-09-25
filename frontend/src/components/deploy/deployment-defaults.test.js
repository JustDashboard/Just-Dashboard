import { describe, expect, test } from "bun:test"
import {
  browserInlined,
  canGenerateSecret,
  installsAssetsWithNode,
  needsStartCommand,
  candidateBlocker,
  checksForRuntime,
  composeSourceForCandidate,
  connectedRepositoryRemote,
  defaultConfiguration,
  defaultReleaseTaskRunner,
  detectedVariableDeclarations,
  dockerfileStageHint,
  discoveredEnvironmentRows,
  environmentRowsToSend,
  generateSecretValue,
  goMainPackageList,
  javaVersionReading,
  dotnetVersionReading,
  mergeDiscoveredRows,
  persistentStorage,
  pointsAtLocalhost,
  projectVolumeName,
  releaseStrategy,
  rowNeedsOperator,
  validateConfiguration,
  withPackageManagerRunner,
  withPersistentVariables,
  withPreviousConnectionShape,
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

describe("readiness detected from the source", () => {
  test("a declared health route becomes the check's path", () => {
    const plan = defaultConfiguration(
      "web",
      candidate({
        profile: "web",
        buildMethod: "dockerfile",
        port: 3000,
        readiness: {
          kind: "http",
          path: "/up",
          source: "framework",
          evidence: "Rails health route in config/routes.rb",
        },
      }),
    )
    expect(plan.checks).toEqual([
      {
        name: "HTTP readiness",
        kind: "http",
        phase: "readiness",
        required: true,
        config: { path: "/up", attempts: 20, timeoutSeconds: 5, intervalSeconds: 3 },
      },
    ])
  })
  test("an API without a health route accepts any answer, with a slow start's budget", () => {
    const plan = defaultConfiguration(
      "web",
      candidate({
        profile: "web",
        recipe: "java",
        port: 8080,
        readiness: {
          kind: "http",
          path: "/",
          acceptAnyAnswer: true,
          attempts: 40,
          intervalSeconds: 3,
          source: "convention",
          evidence: "no health route found",
        },
      }),
    )
    expect(plan.checks[0].config).toEqual({
      path: "/",
      acceptAnyAnswer: true,
      attempts: 40,
      timeoutSeconds: 5,
      intervalSeconds: 3,
    })
  })
  test("a Dockerfile HEALTHCHECK command waits for the container's health", () => {
    const readiness = {
      kind: "docker_health",
      attempts: 44,
      intervalSeconds: 3,
      source: "healthcheck",
      evidence: "HEALTHCHECK in the Dockerfile runs node",
    }
    const plan = defaultConfiguration(
      "web",
      candidate({ profile: "web", buildMethod: "dockerfile", port: 3000, readiness }),
    )
    expect(plan.checks).toEqual([
      {
        name: "Container health",
        kind: "docker_health",
        phase: "readiness",
        required: true,
        config: { attempts: 44, timeoutSeconds: 5, intervalSeconds: 3 },
      },
    ])
    // The port field adding the check later uses the same evidence.
    expect(checksForRuntime([], "web", 3000, readiness)[0].kind).toBe("docker_health")
  })
  test("a worker gets no readiness gate whatever detection read", () => {
    const plan = defaultConfiguration(
      "worker",
      candidate({
        profile: "worker",
        backgroundWorker: { library: "discord.js", kind: "Discord bot", evidence: "" },
      }),
    )
    expect(plan.checks).toEqual([])
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
  test("a file Bun runs moves to node, a TypeScript file stays with Bun", () => {
    expect(withPackageManagerRunner("bun ./build/index.js", "npm")).toBe("node ./build/index.js")
    expect(withPackageManagerRunner("bun run dist/server.mjs", "pnpm")).toBe("node dist/server.mjs")
    expect(withPackageManagerRunner("bun src/index.ts", "npm")).toBe("bun src/index.ts")
    expect(withPackageManagerRunner("bun run src/index.ts", "npm")).toBe("bun run src/index.ts")
    expect(withPackageManagerRunner("bun ./build/index.js", "bun")).toBe("bun ./build/index.js")
    expect(
      withPackageManagerRunner("npx prisma migrate deploy && bun build/index.js", "yarn"),
    ).toBe("yarn prisma migrate deploy && node build/index.js")
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
  test("a recipe that runs a server is refused without its start command", () => {
    const build = (overrides) => ({ method: "recipe", secrets: [], releaseTasks: [], ...overrides })
    const refused = (overrides) =>
      "startCommand" in
      validateConfiguration(
        {
          build: build(overrides),
          runtime: { strategy: "stop_first" },
          variables: [],
          dependencies: [],
          checks: [],
          domains: [],
        },
        "web",
      )
    expect(refused({ recipe: "python" })).toBe(true)
    expect(refused({ recipe: "python", startCommand: "  " })).toBe(true)
    expect(refused({ recipe: "python", startCommand: "gunicorn app:app" })).toBe(false)
    expect(refused({ recipe: "deno" })).toBe(true)
    expect(refused({ recipe: "php" })).toBe(true)
    expect(refused({ recipe: "node" })).toBe(true)
    expect(refused({ recipe: "node", outputDirectory: "dist" })).toBe(false)
    expect(refused({ recipe: "rust" })).toBe(false)
    expect(refused({ recipe: "go" })).toBe(false)
    expect(refused({ method: "dockerfile" })).toBe(false)
  })
})

describe("Java and .NET releases", () => {
  test("a release outside the recipe's catalogue is refused before the request", () => {
    const java = defaultConfiguration(
      "web",
      candidate({ profile: "web", recipe: "java", port: 8080 }),
    )
    expect(
      validateConfiguration({ ...java, build: { ...java.build, javaVersion: "24" } }, "web"),
    ).toHaveProperty("javaVersion")
    expect(
      validateConfiguration({ ...java, build: { ...java.build, javaVersion: "17" } }, "web"),
    ).not.toHaveProperty("javaVersion")
    // Detection chooses; the plan only records an operator's choice.
    expect(java.build.javaVersion).toBeUndefined()
    const dotnet = defaultConfiguration(
      "web",
      candidate({ profile: "web", recipe: "dotnet", port: 8080 }),
    )
    expect(
      validateConfiguration({ ...dotnet, build: { ...dotnet.build, dotnetVersion: "7.0" } }, "web"),
    ).toHaveProperty("dotnetVersion")
    expect(
      validateConfiguration(
        { ...dotnet, build: { ...dotnet.build, dotnetVersion: "10.0" } },
        "web",
      ),
    ).not.toHaveProperty("dotnetVersion")
  })

  test("the automatic choice reads what decides it", () => {
    expect(
      javaVersionReading(
        candidate({
          javaBuild: {
            tool: "gradle",
            release: 21,
            releaseFrom: "app/build.gradle.kts",
            toolchain: true,
            pinned: 21,
            pinnedFrom: ".sdkmanrc",
            context: ".",
            module: ":app",
          },
        }),
      ),
    ).toBe(
      ".sdkmanrc pins Java 21 · app/build.gradle.kts declares Java 21 as a Gradle toolchain · builds :app from .",
    )
    expect(javaVersionReading(candidate({ javaBuild: { tool: "maven" } }))).toBe(
      "Nothing declares a release; the recipe builds on Java 21",
    )
    expect(
      javaVersionReading(
        candidate({ javaBuild: { tool: "gradle", wrapper: "8.4", wrapperUsable: true } }),
      ),
    ).toBe(
      "Nothing declares a release; the recipe builds on Java 17, the newest Gradle 8.4 runs on",
    )
    expect(
      javaVersionReading(
        candidate({ javaBuild: { tool: "gradle", wrapper: "8.14.3", wrapperUsable: true } }),
      ),
    ).toBe("Nothing declares a release; the recipe builds on Java 21")
    expect(javaVersionReading(candidate({}))).toBeUndefined()
    expect(
      dotnetVersionReading(
        candidate({
          dotnetBuild: {
            project: "src/Api/Api.csproj",
            kind: "web",
            targetText: "net10.0",
            sdkPin: "10.0.100",
            sdkPinFrom: "global.json",
            context: ".",
          },
        }),
      ),
    ).toBe("src/Api/Api.csproj targets net10.0 · global.json pins SDK 10.0.100 · published from .")
    expect(
      dotnetVersionReading(candidate({ dotnetBuild: { project: "Api.csproj", kind: "web" } })),
    ).toBe("Api.csproj declares no target framework")
  })
})

test("a Go module's main packages read as one bounded line", () => {
  expect(goMainPackageList(candidate({ goMainPackages: [".", "cmd/worker"] }))).toBe(
    "., ./cmd/worker",
  )
  const many = Array.from({ length: 12 }, (_, index) => `cmd/tool${index}`)
  expect(goMainPackageList(candidate({ goMainPackages: many }))).toBe(
    "./cmd/tool0, ./cmd/tool1, ./cmd/tool2, ./cmd/tool3, ./cmd/tool4, ./cmd/tool5, ./cmd/tool6, ./cmd/tool7 and 4 more",
  )
  // Detection's own list is bounded too, and says how many it left out.
  expect(
    goMainPackageList(candidate({ goMainPackages: ["cmd/a"], goMainPackagesOmitted: 70 })),
  ).toBe("./cmd/a and 70 more")
  expect(goMainPackageList(undefined)).toBe("")
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
      "APPLICATION_SECRET",
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

describe("persistent state", () => {
  const prisma = {
    kind: "sqlite",
    path: "/app/prisma/dev.db",
    target: "/data",
    variable: "DATABASE_URL",
    value: "file:/data/dev.db",
    source: "prisma/schema.prisma",
    reason: "Prisma's SQLite datasource is read from DATABASE_URL",
  }
  const uploads = {
    kind: "uploads",
    path: "/app/uploads",
    target: "/data/uploads",
    variable: "UPLOAD_DIR",
    value: "/data/uploads",
    source: ".env.example",
    reason: "uploaded files are written to the directory UPLOAD_DIR names",
  }
  const inImage = {
    kind: "sqlite",
    path: "/app/db.sqlite3",
    source: "mysite/settings.py",
    reason: "Django's database is a SQLite file",
  }

  test("a volume name is the project's slug, a hash of its exact name and what it holds", () => {
    expect(projectVolumeName("My Blog", "/data")).toMatch(/^my-blog-[0-9a-f]{8}-data$/)
    expect(projectVolumeName("my-blog", "/data")).not.toBe(projectVolumeName("My Blog", "/data"))
    expect(projectVolumeName("my-blog", "/data")).toBe(projectVolumeName("my-blog", "/data"))
    expect(projectVolumeName("", "/home/app/.aspnet/DataProtection-Keys")).toMatch(
      /^app-[0-9a-f]{8}-dataprotection-keys$/,
    )
    expect(projectVolumeName("Ünïcode!!", "/")).toMatch(/^ncode-[0-9a-f]{8}-data$/)
  })

  test("one repository imported twice under the same name gets two volumes", () => {
    const prod = projectVolumeName("blog", "/data", "draft-one")
    const staging = projectVolumeName("blog", "/data", "draft-two")
    expect(prod).toMatch(/^blog-[0-9a-f]{8}-data$/)
    expect(staging).not.toBe(prod)
    expect(projectVolumeName("blog", "/data", "draft-one")).toBe(prod)
    const plan = (draftId) =>
      defaultConfiguration(
        "web",
        candidate({ profile: "web", port: 3000, persistentPaths: [prisma] }),
        undefined,
        undefined,
        "blog",
        draftId,
      )
    expect(plan("draft-one").runtime.mounts[0].source).toBe(prod)
    expect(plan("draft-one").dependencies[0].resourceId).toBe(prod)
    expect(plan("draft-two").runtime.mounts[0].source).toBe(staging)
    const game = (draftId) =>
      defaultConfiguration("game", undefined, undefined, undefined, "survival", draftId).runtime
        .mounts[0].source
    expect(game("draft-one")).toMatch(/^survival-[0-9a-f]{8}-data$/)
    expect(game("draft-one")).not.toBe(game("draft-two"))
  })

  test("each target gets one managed volume and a storage dependency saying why", () => {
    const laravel = {
      ...prisma,
      path: "/app/database/database.sqlite",
      target: "/app/storage",
      reason: "Laravel's database is SQLite",
    }
    const files = { ...uploads, target: "/app/storage", reason: "uploads on the local disk" }
    const { mounts, dependencies } = persistentStorage(
      candidate({ persistentPaths: [laravel, files, inImage] }),
      "shop",
    )
    expect(mounts).toEqual([
      {
        source: projectVolumeName("shop", "/app/storage"),
        target: "/app/storage",
        ownership: "managed",
      },
    ])
    expect(dependencies).toEqual([
      {
        kind: "storage",
        ownership: "managed",
        resourceKind: "docker_volume",
        resourceId: mounts[0].source,
        config: { purpose: "Laravel's database is SQLite; uploads on the local disk", data: true },
      },
    ])
    const twice = persistentStorage(
      candidate({ persistentPaths: [prisma, { ...prisma, target: "/srv/data" }] }),
      "shop",
    )
    expect(new Set(twice.mounts.map((mount) => mount.source)).size).toBe(2)
  })

  test("a detected state plan keeps its volume and releases stop-first", () => {
    const plan = defaultConfiguration(
      "web",
      candidate({ profile: "web", port: 3000, persistentPaths: [prisma, uploads, inImage] }),
      undefined,
      undefined,
      "notes",
    )
    expect(plan.runtime.mounts.map((mount) => mount.target)).toEqual(["/data", "/data/uploads"])
    expect(plan.runtime.strategy).toBe("stop_first")
    expect(plan.dependencies).toHaveLength(2)
    // The values that move the state travel in the plan, to the runtime and
    // release tasks only: the build has no volume to open a database on.
    expect(plan.variables).toEqual([
      {
        name: "DATABASE_URL",
        sensitivity: "plain",
        scopes: ["runtime", "release_task"],
        value: "file:/data/dev.db",
      },
      {
        name: "UPLOAD_DIR",
        sensitivity: "plain",
        scopes: ["runtime", "release_task"],
        value: "/data/uploads",
      },
    ])
    const stateless = defaultConfiguration("web", candidate({ profile: "web", port: 3000 }))
    expect(stateless.runtime.strategy).toBe("blue_green")
    expect(stateless.runtime.mounts).toEqual([])
    expect(stateless.dependencies).toEqual([])
  })

  test("a relocation replaces a bare declaration but never a reference or a generator", () => {
    const withState = candidate({ persistentPaths: [prisma] })
    expect(
      withPersistentVariables(
        [
          {
            name: "DATABASE_URL",
            sensitivity: "secret",
            scopes: ["runtime", "build"],
            required: true,
          },
        ],
        withState,
      ),
    ).toEqual([
      {
        name: "DATABASE_URL",
        sensitivity: "plain",
        scopes: ["runtime", "release_task"],
        value: "file:/data/dev.db",
      },
    ])
    const linked = [
      {
        name: "DATABASE_URL",
        sensitivity: "secret",
        scopes: ["runtime"],
        reference: "${{database.3.url}}",
      },
    ]
    expect(withPersistentVariables(linked, withState)).toEqual(linked)
    expect(withPersistentVariables([], candidate({ persistentPaths: [inImage] }))).toEqual([])
  })

  test("a row still showing the plan's own value is not sent again", () => {
    const variables = [
      {
        name: "DATABASE_URL",
        sensitivity: "plain",
        scopes: ["runtime", "release_task"],
        value: "file:/data/dev.db",
      },
    ]
    const shown = {
      name: "DATABASE_URL",
      value: "file:/data/dev.db",
      detected: true,
      note: "Keeps it on the volume at /data",
    }
    const typed = { name: "API_KEY", value: "typed" }
    const empty = { name: "SMTP_HOST", value: "", detected: true }
    expect(environmentRowsToSend([shown, typed, empty], variables)).toEqual([typed])
    const changed = { ...shown, value: "file:/srv/mine.db", note: undefined }
    expect(environmentRowsToSend([changed], variables)).toEqual([changed])
    expect(environmentRowsToSend([shown], [])).toEqual([shown])
  })

  test("the strategy follows the profile and any writable mount", () => {
    expect(releaseStrategy("web", [])).toBe("blue_green")
    expect(releaseStrategy("static")).toBe("blue_green")
    expect(releaseStrategy("worker", [])).toBe("stop_first")
    expect(releaseStrategy("web", [{ source: "v", target: "/data", ownership: "managed" }])).toBe(
      "stop_first",
    )
    expect(
      releaseStrategy("web", [
        { source: "v", target: "/etc/app", readOnly: true, ownership: "managed" },
      ]),
    ).toBe("blue_green")
  })

  test("a re-detection fills an empty row with the value that moves state onto its volume", () => {
    const current = [
      { name: "DATABASE_URL", value: "", source: ".env.example", detected: true },
      { name: "API_KEY", value: "typed" },
    ]
    const merged = mergeDiscoveredRows(
      current,
      discoveredEnvironmentRows(candidate({ persistentPaths: [prisma] })),
    )
    expect(merged).toEqual([
      {
        name: "DATABASE_URL",
        value: "file:/data/dev.db",
        source: ".env.example",
        detected: true,
        note: "Keeps it on the volume at /data",
      },
      { name: "API_KEY", value: "typed" },
    ])
    const typed = [{ name: "DATABASE_URL", value: "file:/srv/mine.db" }]
    expect(
      mergeDiscoveredRows(
        typed,
        discoveredEnvironmentRows(candidate({ persistentPaths: [prisma] })),
      ),
    ).toBe(typed)
  })

  test("a moved variable the build reads reaches the build too", () => {
    const variables = withPersistentVariables(
      [],
      candidate({
        variables: [{ name: "DATABASE_URL", sources: ["prisma.config.ts"], phase: "build" }],
        persistentPaths: [prisma, uploads],
      }),
    )
    expect(variables.map((variable) => [variable.name, variable.scopes])).toEqual([
      ["DATABASE_URL", ["runtime", "release_task", "build"]],
      ["UPLOAD_DIR", ["runtime", "release_task"]],
    ])
  })

  test("the variable that moves the state onto its volume arrives filled", () => {
    const rows = discoveredEnvironmentRows(
      candidate({
        variables: [{ name: "DATABASE_URL", example: "file:./dev.db", sources: [".env.example"] }],
        persistentPaths: [prisma, uploads, inImage],
      }),
    )
    expect(rows).toEqual([
      {
        name: "DATABASE_URL",
        value: "file:/data/dev.db",
        example: "file:./dev.db",
        source: ".env.example",
        detected: true,
        note: "Keeps it on the volume at /data",
      },
      {
        name: "UPLOAD_DIR",
        value: "/data/uploads",
        source: ".env.example",
        detected: true,
        note: "Keeps it on the volume at /data/uploads",
      },
    ])
  })
})

test("relinking keeps the connection shape the variable's reference asked for", () => {
  expect(withPreviousConnectionShape("${{database.7}}", "5.jdbc")).toBe("${{database.7.jdbc}}")
  expect(withPreviousConnectionShape("${{database.7}}", "5.jdbc-mariadb")).toBe(
    "${{database.7.jdbc-mariadb}}",
  )
  expect(withPreviousConnectionShape("${{database.7}}", "5.adonet")).toBe("${{database.7.adonet}}")
  // A plain link, another server's database name, and a literal address stay as they are.
  expect(withPreviousConnectionShape("${{database.7}}", "5")).toBe("${{database.7}}")
  expect(withPreviousConnectionShape("${{database.7}}", "5.url.app_cache")).toBe("${{database.7}}")
  expect(withPreviousConnectionShape("${{database.7}}")).toBe("${{database.7}}")
  expect(withPreviousConnectionShape("postgres://db-7.jd.internal/app", "5.jdbc")).toBe(
    "postgres://db-7.jd.internal/app",
  )
})

describe("repository container definitions", () => {
  test("a Dockerfile plan builds the stage detection chose", () => {
    const plan = defaultConfiguration(
      "web",
      candidate({
        buildMethod: "dockerfile",
        dockerfile: "deploy/app.Dockerfile",
        dockerfileTarget: "production",
      }),
    )
    expect(plan.build.dockerfile).toBe("deploy/app.Dockerfile")
    expect(plan.build.target).toBe("production")
    const recipe = defaultConfiguration("web", candidate({ dockerfileTarget: "production" }))
    expect(recipe.build.target).toBeUndefined()
  })

  test("a declared release command is planned in the release image", () => {
    const plan = defaultConfiguration(
      "web",
      candidate({ buildMethod: "recipe", releaseCommand: "python manage.py migrate --noinput" }),
    )
    expect(plan.build.releaseTasks).toEqual([
      {
        name: "release",
        command: "python manage.py migrate --noinput",
        timeoutSeconds: 600,
        env: [],
        runner: "image",
      },
    ])
    expect(validateConfiguration(plan, "web").releaseTasks).toBeUndefined()
    expect(
      defaultConfiguration("web", candidate({ buildMethod: "recipe" })).build.releaseTasks,
    ).toEqual([])
  })

  test("an image task without an image to run in is refused before save", () => {
    const plan = defaultConfiguration("worker", candidate({ buildMethod: "recipe" }))
    plan.build.releaseTasks = [
      { name: "migrate", command: "bin/migrate", timeoutSeconds: 60, env: [], runner: "image" },
    ]
    for (const method of ["none", "legacy_compose"]) {
      plan.build.method = method
      expect(validateConfiguration(plan, "worker").releaseTasks).toContain("release image")
      expect(defaultReleaseTaskRunner(method)).toBeUndefined()
    }
    plan.build.method = "compose"
    expect(validateConfiguration(plan, "worker").releaseTasks).toBeUndefined()
    expect(defaultReleaseTaskRunner("dockerfile")).toBe("image")
  })

  test("a blank release task command is refused before save", () => {
    const plan = defaultConfiguration("worker", candidate({ buildMethod: "recipe" }))
    plan.build.releaseTasks = [{ name: "migrate", command: "  \n", timeoutSeconds: 60, env: [] }]
    expect(validateConfiguration(plan, "worker").releaseTasks).toContain("command")
  })

  test("a Dockerfile stage is a stage name, and the hint names the ones detection read", () => {
    const plan = defaultConfiguration("web", candidate({ buildMethod: "dockerfile" }))
    plan.build.target = "prod --push"
    expect(validateConfiguration(plan, "web").target).toContain("stage name")
    plan.build.target = "runner"
    expect(validateConfiguration(plan, "web").target).toBeUndefined()
    expect(dockerfileStageHint(["deps", "runner"])).toBe(
      "Leave empty to build the last stage. This Dockerfile's stages: deps, runner.",
    )
    expect(dockerfileStageHint()).toBe("Leave empty to build the last stage.")
  })

  test("a browser-public Compose variable is planned plain, so a build argument can carry it", () => {
    const plan = defaultConfiguration(
      "compose",
      undefined,
      { kind: "compose", mode: "compose_git" },
      {
        candidates: [],
        compose: { variables: ["NEXT_PUBLIC_API_URL", "DATABASE_URL"] },
      },
    )
    const sensitivity = Object.fromEntries(
      plan.variables.map((variable) => [variable.name, variable.sensitivity]),
    )
    expect(sensitivity).toEqual({ NEXT_PUBLIC_API_URL: "plain", DATABASE_URL: "secret" })
  })

  test("the candidate list says what stops a candidate building", () => {
    expect(
      candidateBlocker(
        candidate({
          buildMethod: "dockerfile",
          imageBuildIssues: [
            { code: "dockerfile_dev_server", severity: "warning", detail: "starts a watcher" },
            {
              code: "dockerfile_copy_source_missing",
              severity: "blocked",
              detail: "line 3 COPY .env: not in the build context .",
            },
          ],
        }),
      ),
    ).toBe("line 3 COPY .env: not in the build context .")
    expect(candidateBlocker(candidate({ recipe: "node", packageManagers: ["bun", "npm"] }))).toBe(
      "choose the package manager: bun, npm",
    )
    expect(candidateBlocker(candidate({ recipe: "node", packageManager: "bun" }))).toBeUndefined()
  })

  test("a repository's Compose file switches to a Compose source with the same checkout", () => {
    const compose = candidate({
      buildMethod: "compose",
      profile: "compose",
      evidence: [
        { path: "docker-compose.yml", reason: "Compose configuration" },
        { path: "docker-compose.yml", reason: "development services" },
      ],
    })
    expect(
      composeSourceForCandidate(
        {
          kind: "git",
          mode: "git_url",
          url: "https://example.com/o/r.git",
          ref: "main",
          subdirectory: "app",
        },
        compose,
      ),
    ).toEqual({
      kind: "compose",
      mode: "compose_git",
      url: "https://example.com/o/r.git",
      ref: "main",
      credentialId: undefined,
      subdirectory: "app",
      includeSubmodules: undefined,
      includeLfs: undefined,
      composeFiles: [{ path: "docker-compose.yml", content: "", order: 0 }],
    })
    expect(composeSourceForCandidate({ kind: "git", mode: "git_url", url: "x" }, candidate())).toBe(
      undefined,
    )
    // A connected repository keeps its credential and becomes the remote the
    // backend would clone it from.
    expect(
      composeSourceForCandidate(
        {
          kind: "git",
          mode: "connected_repository",
          provider: "github",
          repository: "o/r",
          ref: "main",
          credentialId: 7,
        },
        compose,
      ),
    ).toMatchObject({
      mode: "compose_git",
      url: "https://github.com/o/r.git",
      credentialId: 7,
      ref: "main",
    })
  })

  test("a connected repository's remote is the one the backend clones", () => {
    const connected = { kind: "git", mode: "connected_repository", repository: "team/app" }
    expect(connectedRepositoryRemote({ ...connected, provider: "gitlab" })).toBe(
      "https://gitlab.com/team/app.git",
    )
    expect(
      connectedRepositoryRemote({
        ...connected,
        provider: "gitea",
        providerBaseUrl: "https://git.example.com/",
      }),
    ).toBe("https://git.example.com/team/app.git")
    expect(connectedRepositoryRemote({ ...connected, provider: "gitea" })).toBeUndefined()
    expect(connectedRepositoryRemote({ kind: "git", mode: "git_url", url: "x" })).toBeUndefined()
  })
  // Detection marks what render.yaml's generateValue names as generated
  // (detect_platform_manifests.go); the server mints it at commit, like every
  // other detected secret, and the row says so instead of holding a value.
  test("a secret another platform's file generates arrives generated", () => {
    const detected = candidate({
      variables: [
        {
          name: "JWT_SECRET",
          sources: ["render.yaml"],
          setup: "generate",
          setupReason: "Render generates it",
          generateLength: 64,
          generateFormat: "hex",
        },
        { name: "STRIPE_KEY", sources: ["render.yaml"] },
      ],
      platformManifests: [
        { file: "render.yaml", platform: "render", generatedVariables: ["JWT_SECRET"] },
      ],
    })
    const rows = discoveredEnvironmentRows(detected)
    expect(rows[0]).toMatchObject({ name: "JWT_SECRET", value: "", setup: "generate" })
    expect(rows[1].value).toBe("")
    expect(detectedVariableDeclarations(detected, "web")).toEqual([
      {
        name: "JWT_SECRET",
        sensitivity: "secret",
        scopes: ["runtime", "build"],
        generate: 64,
        generateFormat: "hex",
      },
    ])
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

describe("the language recipes", () => {
  test("Ruby needs a start command; the release-built languages start their own", () => {
    expect(needsStartCommand({ method: "recipe", recipe: "ruby" })).toBe(true)
    expect(
      needsStartCommand({ method: "recipe", recipe: "ruby", startCommand: "bundle exec puma" }),
    ).toBe(false)
    for (const recipe of ["elixir", "scala", "clojure", "dart", "gleam"]) {
      expect(needsStartCommand({ method: "recipe", recipe })).toBe(false)
    }
  })

  test("PHP, Python, Ruby and Elixir install their assets through the Node planner", () => {
    for (const recipe of ["php", "python", "ruby", "elixir"])
      expect(installsAssetsWithNode(recipe)).toBe(true)
    for (const recipe of ["node", "go", "scala", "dart", undefined]) {
      expect(installsAssetsWithNode(recipe)).toBe(false)
    }
  })
})
