import { describe, expect, test } from "bun:test"
import { buildDsn, maskDsn } from "./db-dsn"

const fields = {
  host: "127.0.0.1",
  port: "",
  user: "app",
  password: "p@ss word",
  database: "shop",
  option: "",
}

// The strings the connection form renders have to be the ones the server's
// own BuildDSN renders for a detected container, or a hand-entered server and
// a detected one would reach the same engine two different ways.
describe("connection strings", () => {
  test("mirror the server's format per engine", () => {
    expect(buildDsn("postgres", fields)).toBe(
      "postgres://app:p%40ss%20word@127.0.0.1:5432/shop?sslmode=disable",
    )
    expect(buildDsn("postgres", { ...fields, option: "require" })).toBe(
      "postgres://app:p%40ss%20word@127.0.0.1:5432/shop?sslmode=require",
    )
    // MySQL's driver does not decode a URL, so the password travels as typed.
    expect(buildDsn("mysql", fields)).toBe("app:p@ss word@tcp(127.0.0.1:3306)/shop")
    expect(buildDsn("sqlserver", fields)).toBe(
      "sqlserver://app:p%40ss%20word@127.0.0.1:1433?database=shop",
    )
    expect(buildDsn("clickhouse", { ...fields, database: "" })).toBe(
      "clickhouse://app:p%40ss%20word@127.0.0.1:9000/default",
    )
    expect(buildDsn("oracle", { ...fields, database: "ORCLPDB1" })).toBe(
      "oracle://app:p%40ss%20word@127.0.0.1:1521/ORCLPDB1",
    )
    expect(buildDsn("mongodb", { ...fields, option: "admin" })).toBe(
      "mongodb://app:p%40ss%20word@127.0.0.1:27017/shop?authSource=admin",
    )
    expect(buildDsn("redis", { ...fields, user: "", database: "db2" })).toBe(
      "redis://:p%40ss%20word@127.0.0.1:6379/2",
    )
    expect(buildDsn("sqlite", { ...fields, database: "/var/lib/app/data.db" })).toBe(
      "/var/lib/app/data.db",
    )
  })
  test("omit credentials that were not given and bracket IPv6 hosts", () => {
    expect(buildDsn("redis", { ...fields, user: "", password: "", database: "" })).toBe(
      "redis://127.0.0.1:6379/0",
    )
    expect(buildDsn("postgres", { ...fields, host: "fd00::1", port: "5433", password: "" })).toBe(
      "postgres://app@[fd00::1]:5433/shop?sslmode=disable",
    )
  })
  test("mask the password and nothing else", () => {
    expect(maskDsn("postgres", fields)).toBe(
      "postgres://app:••••••@127.0.0.1:5432/shop?sslmode=disable",
    )
    expect(maskDsn("postgres", { ...fields, password: "" })).toBe(
      "postgres://app@127.0.0.1:5432/shop?sslmode=disable",
    )
  })
})
