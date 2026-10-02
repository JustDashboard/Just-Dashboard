import { describe, expect, test } from "bun:test"
import {
  ACCOUNT_NAME,
  CONNECTION_NAME,
  CONTAINER_NAME,
  DATABASE_NAME,
  ENVIRONMENT,
  PROVISION_PASSWORD,
  freeName,
  generatePassword,
  suggestedName,
} from "./rules"

describe("the server's rules for names", () => {
  test("a connection's name may hold spaces; a container's may not", () => {
    expect(CONNECTION_NAME.test("shop on prod")).toBe(true)
    expect(CONNECTION_NAME.test(" shop")).toBe(false)
    expect(CONNECTION_NAME.test("a · b")).toBe(false)
    expect(CONTAINER_NAME.test("jd-postgres_2")).toBe(true)
    expect(CONTAINER_NAME.test("jd postgres")).toBe(false)
  })

  test("a database and an account start with a letter", () => {
    expect(DATABASE_NAME.test("app_1")).toBe(true)
    expect(DATABASE_NAME.test("1app")).toBe(false)
    expect(ACCOUNT_NAME.test("just_dashboard")).toBe(true)
    expect(ACCOUNT_NAME.test("just-dashboard")).toBe(false)
  })

  test("an environment is a short word, a typed password one the bootstrap can carry", () => {
    expect(ENVIRONMENT.test("eu-west qa")).toBe(true)
    expect(ENVIRONMENT.test("x".repeat(33))).toBe(false)
    expect(PROVISION_PASSWORD.test("abcDEF12")).toBe(true)
    expect(PROVISION_PASSWORD.test("short")).toBe(false)
    expect(PROVISION_PASSWORD.test("has a space")).toBe(false)
  })
})

describe("the name a connection is offered", () => {
  test("the database, else the host, else the engine — and one nobody has", () => {
    expect(suggestedName({ host: "db.example.com", database: "shop" }, "postgresql", [])).toBe(
      "shop",
    )
    expect(suggestedName({ host: "db.example.com", database: "" }, "redis", [])).toBe("db")
    expect(suggestedName({ host: "", database: "" }, "redis", [])).toBe("redis")
    expect(suggestedName({ host: "", database: "/srv/data/notes.sqlite3" }, "sqlite", [])).toBe(
      "notes",
    )
    expect(suggestedName({ host: "h", database: "shop" }, "x", ["Shop", "shop-2"])).toBe("shop-3")
  })

  test("a number names nothing: a Redis index, the first octet of an address", () => {
    expect(suggestedName({ host: "127.0.0.1", database: "1" }, "redis", [])).toBe("redis")
    expect(suggestedName({ host: "localhost", database: "0" }, "redis", ["redis"])).toBe("redis-2")
    expect(suggestedName({ host: "10.255.255.1", database: "1" }, "redis", [])).toBe(
      "redis-10.255.255.1",
    )
    expect(suggestedName({ host: "cache.internal", database: "1" }, "redis", [])).toBe("cache")
    expect(suggestedName({ host: "[::1]", database: "" }, "postgres", [])).toBe("postgres")
    expect(suggestedName({ host: "2001:db8::7", database: "" }, "postgres", [])).toBe(
      "postgres-2001.db8..7",
    )
    expect(
      CONNECTION_NAME.test(suggestedName({ host: "2001:db8::7", database: "" }, "postgres", [])),
    ).toBe(true)
  })

  test("a name is made of what the rule allows", () => {
    expect(CONNECTION_NAME.test(freeName("my/db:1", []))).toBe(true)
    expect(freeName("///", [])).toBe("database")
    expect(freeName("x".repeat(80), []).length).toBe(60)
  })
})

describe("a generated password", () => {
  test("is the length asked, from characters every engine takes unquoted, and not the last one", () => {
    const first = generatePassword()
    expect(first).toHaveLength(30)
    expect(PROVISION_PASSWORD.test(first)).toBe(true)
    expect(generatePassword()).not.toBe(first)
    expect(generatePassword(12)).toHaveLength(12)
  })
})
