import { describe, expect, test } from "bun:test"
import { readFileSync } from "node:fs"
import path from "node:path"
import { engineOf } from "../engine"
import {
  EMPTY_PROVISION,
  provisionImage,
  provisionProblems,
  provisionRequest,
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
}
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

  test("a field the engine does not have is not judged", () => {
    expect(
      provisionProblems(redis, { ...EMPTY_PROVISION, database: "1st", user: "the-user" }),
    ).toEqual({})
  })
})
