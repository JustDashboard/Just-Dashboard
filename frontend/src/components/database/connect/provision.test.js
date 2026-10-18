import { describe, expect, test } from "bun:test"
import { readFileSync } from "node:fs"
import path from "node:path"
import { engineOf } from "../engine"
import {
  EMPTY_PROVISION,
  chosenRelease,
  needsBuild,
  phaseSentence,
  provisionImage,
  provisionProblems,
  provisionRequest,
  provisionSteps,
  releaseShelves,
  shelfOf,
} from "./provision"

/** What `GET /databases/drivers` serves: the backend's own record of the route. */
const CATALOGUE = JSON.parse(
  readFileSync(
    path.join(
      import.meta.dir,
      "../../../../../backend/internal/api/testdata/database-drivers.json",
    ),
    "utf8",
  ),
)

const postgres = {
  engine: "postgres",
  label: "PostgreSQL",
  image: "postgres:16-alpine",
  driver: "postgres",
  flavor: "postgres",
  versions: [
    { version: "17", image: "postgres:17-alpine" },
    { version: "16", image: "postgres:16-alpine" },
  ],
  defaultVersion: "16",
  port: 5432,
  defaultUser: "jd",
  database: true,
  defaultLimits: { memoryMb: 1024, cpus: 1 },
  minMemoryMb: 6,
  statements: true,
  autoExplain: true,
}
const mysql = { ...postgres, engine: "mysql", label: "MySQL", minMemoryMb: 512 }
const redis = {
  engine: "redis",
  label: "Redis",
  image: "redis:7-alpine",
  driver: "redis",
  flavor: "redis",
  versions: [{ version: "7", image: "redis:7-alpine" }],
  defaultVersion: "7",
  port: 6379,
  defaultUser: "",
  database: false,
}

describe("the shelf an engine is picked from", () => {
  const shelf = (id) => shelfOf(engineOf(id, CATALOGUE), true)

  test("by the kind of store, with a column store among the analytics", () => {
    expect(shelf("postgres")).toBe("SQL")
    expect(shelf("timescaledb")).toBe("SQL")
    expect(shelf("mariadb")).toBe("SQL")
    expect(shelf("mongodb")).toBe("Documents")
    expect(shelf("valkey")).toBe("Key–value")
    expect(shelf("clickhouse")).toBe("Analytics")
  })

  test("before the catalogue has answered, every SQL engine is on the SQL shelf", () => {
    expect(shelfOf(engineOf("clickhouse"), false)).toBe("SQL")
    expect(shelfOf(engineOf("redis"), false)).toBe("Key–value")
  })
})

describe("what the start form sends", () => {
  test("empty answers are left out, so the server's defaults stand, and it is local", () => {
    expect(provisionRequest(postgres, EMPTY_PROVISION)).toEqual({
      engine: "postgres",
      name: undefined,
      version: "16",
      database: undefined,
      user: undefined,
      password: undefined,
      exposure: "local",
    })
  })

  test("what was typed is sent trimmed; public is an explicit choice", () => {
    expect(
      provisionRequest(postgres, {
        name: " shop-db ",
        version: "17",
        database: "shop",
        user: "shop",
        password: "abcDEF12",
        shared: true,
      }),
    ).toEqual({
      engine: "postgres",
      name: "shop-db",
      version: "17",
      database: "shop",
      user: "shop",
      password: "abcDEF12",
      exposure: "public",
    })
  })

  test("slow plans are asked for only when chosen, and only of a template that offers them", () => {
    expect(provisionRequest(postgres, EMPTY_PROVISION).autoExplain).toBeUndefined()
    expect(provisionRequest(postgres, { ...EMPTY_PROVISION, autoExplain: true }).autoExplain).toBe(
      true,
    )
    // A draft carried over from PostgreSQL to an engine with no plans to log
    // sends nothing the server would refuse.
    expect(
      provisionRequest(redis, { ...EMPTY_PROVISION, autoExplain: true }).autoExplain,
    ).toBeUndefined()
  })

  test("a field the engine has no use for is never sent, and an unknown version is the default", () => {
    const sent = provisionRequest(redis, {
      ...EMPTY_PROVISION,
      database: "app",
      user: "someone",
      version: "99",
    })
    expect(sent.database).toBeUndefined()
    expect(sent.user).toBeUndefined()
    expect(sent.version).toBe("7")
  })

  test("a limited server sends its limits, and a limit of zero is none and is left out", () => {
    const limited = { ...EMPTY_PROVISION, limited: true, memoryMb: 1024, cpus: 1.5 }
    const sent = provisionRequest(postgres, limited)
    expect(sent.memoryMb).toBe(1024)
    expect(sent.cpus).toBe(1.5)
    expect(provisionRequest(postgres, { ...limited, cpus: 0 }).cpus).toBeUndefined()
    // Switched off, what the fields still hold is not sent.
    const open = provisionRequest(postgres, { ...limited, limited: false })
    expect(open.memoryMb).toBeUndefined()
    expect(open.cpus).toBeUndefined()
  })

  test("the image follows the version", () => {
    expect(provisionImage(postgres, EMPTY_PROVISION)).toBe("postgres:16-alpine")
    expect(provisionImage(postgres, { ...EMPTY_PROVISION, version: "17" })).toBe(
      "postgres:17-alpine",
    )
  })
})

describe("the answers the server would refuse", () => {
  test("an empty form has none", () => {
    expect(provisionProblems(postgres, EMPTY_PROVISION)).toEqual({})
  })

  test("each field is held to the server's own rule", () => {
    const problems = provisionProblems(postgres, {
      ...EMPTY_PROVISION,
      name: "my db",
      database: "1st",
      user: "the-user",
      password: "short",
    })
    expect(Object.keys(problems).sort()).toEqual(["database", "name", "password", "user"])
  })

  test("limits are held to this machine, and only while the server is limited", () => {
    const host = { memoryBytes: 4 * 1024 ** 3, cpus: 2 }
    const draft = { ...EMPTY_PROVISION, limited: true, memoryMb: 8192, cpus: 4 }
    expect(Object.keys(provisionProblems(postgres, draft, host)).sort()).toEqual(["cpus", "memory"])
    expect(provisionProblems(postgres, { ...draft, limited: false }, host)).toEqual({})
    expect(provisionProblems(postgres, { ...draft, memoryMb: 1024, cpus: 1 }, host)).toEqual({})
  })

  test("an engine whose first start needs more memory says so", () => {
    const draft = { ...EMPTY_PROVISION, limited: true, memoryMb: 448, cpus: 1 }
    expect(provisionProblems(mysql, draft).memory).toBe(
      "MySQL needs at least 512 MiB the first time it starts.",
    )
    expect(provisionProblems(postgres, draft)).toEqual({})
    expect(provisionProblems(mysql, { ...draft, memoryMb: 512 })).toEqual({})
  })

  test("a field the engine does not have is not judged", () => {
    expect(
      provisionProblems(redis, { ...EMPTY_PROVISION, database: "1st", user: "the-user" }),
    ).toEqual({})
  })
})

const eol = (date, past) => ({
  product: "MySQL",
  release: "",
  date: `${date}T00:00:00Z`,
  past,
  daysLeft: 0,
})
const mysqlReleases = {
  engine: "mysql",
  label: "MySQL",
  image: "mysql:8.4",
  driver: "mysql",
  flavor: "mysql",
  versions: [
    { version: "9.7", image: "mysql:9.7", endOfLife: eol("2034-04-30", false) },
    { version: "8.4", image: "mysql:8.4", endOfLife: eol("2032-04-30", false) },
    { version: "8.0", image: "mysql:8.0", endOfLife: eol("2026-04-30", true) },
  ],
  defaultVersion: "8.4",
  port: 3306,
  defaultUser: "jd",
  database: true,
}
const full = {
  ...postgres,
  engine: "postgres-full",
  label: "PostgreSQL, full",
  image: "jd-postgres-full:18",
  versions: [
    { version: "18", image: "jd-postgres-full:18", build: { ready: false } },
    { version: "17", image: "jd-postgres-full:17", build: { ready: true } },
  ],
  defaultVersion: "18",
  extensions: ["vector", "postgis"],
}

describe("the releases a template offers", () => {
  test("the ones past their end of life stand apart, newest first on each side", () => {
    const { current, older } = releaseShelves(mysqlReleases)
    expect(current.map((one) => one.version)).toEqual(["9.7", "8.4"])
    expect(older.map((one) => one.version)).toEqual(["8.0"])
  })

  test("a release the table cannot date is offered with the maintained ones", () => {
    expect(releaseShelves(redis)).toEqual({ current: redis.versions, older: [] })
  })

  test("an older release is chosen like any other, and sent as chosen", () => {
    const draft = { ...EMPTY_PROVISION, version: "8.0" }
    expect(chosenRelease(mysqlReleases, draft)?.image).toBe("mysql:8.0")
    expect(provisionRequest(mysqlReleases, draft).version).toBe("8.0")
    expect(chosenRelease(mysqlReleases, EMPTY_PROVISION)?.version).toBe("8.4")
  })
})

describe("an image built on this server", () => {
  test("is built first only while it is not, from this recipe", () => {
    const built = new Set()
    expect(needsBuild(chosenRelease(full, EMPTY_PROVISION), built)).toBe(true)
    expect(needsBuild(chosenRelease(full, { ...EMPTY_PROVISION, version: "17" }), built)).toBe(
      false,
    )
    expect(needsBuild(chosenRelease(postgres, EMPTY_PROVISION), built)).toBe(false)
  })

  test("is not built twice in one visit, whatever the catalogue read earlier says", () => {
    expect(needsBuild(chosenRelease(full, EMPTY_PROVISION), new Set(["jd-postgres-full:18"]))).toBe(
      false,
    )
  })

  test("adds its step ahead of the start, and is never said to be pulled", () => {
    expect(provisionSteps(true).map((step) => step.key)).toEqual([
      "build",
      "start",
      "wait",
      "connect",
    ])
    expect(provisionSteps(false).map((step) => step.key)).toEqual(["start", "wait", "connect"])
    expect(phaseSentence("start", false)).toContain("Pulling the image")
    expect(phaseSentence("start", true)).not.toContain("Pulling")
    expect(phaseSentence("build", true)).toContain("Building the image")
  })
})
