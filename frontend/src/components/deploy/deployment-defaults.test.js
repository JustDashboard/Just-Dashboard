import { describe, expect, test } from "bun:test"
import {
  canGenerateSecret,
  checksForRuntime,
  defaultConfiguration,
  discoveredEnvironmentRows,
  environmentRowsToSend,
  generateSecretValue,
  mergeDiscoveredRows,
  persistentStorage,
  projectVolumeName,
  releaseStrategy,
  validateConfiguration,
  withPackageManagerRunner,
  withPersistentVariables,
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
