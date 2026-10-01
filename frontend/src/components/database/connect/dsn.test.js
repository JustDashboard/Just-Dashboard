import { describe, expect, test } from "bun:test"
import {
  EMPTY_CONNECT_FIELDS,
  composeDsn,
  driverOfUrl,
  maskPasted,
  maskedDsn,
  pastedDsn,
  tlsModes,
} from "./dsn"

const fields = (over = {}) => ({
  ...EMPTY_CONNECT_FIELDS,
  host: "db.example.com",
  user: "app",
  password: "s3cret",
  database: "shop",
  ...over,
})

describe("the string the connect form sends", () => {
  test("PostgreSQL says encryption with sslmode", () => {
    expect(composeDsn("postgres", fields())).toBe(
      "postgres://app:s3cret@db.example.com:5432/shop?sslmode=disable",
    )
    expect(composeDsn("postgres", fields({ tls: "on" }))).toEndWith("?sslmode=require")
    expect(composeDsn("postgres", fields({ tls: "verify", port: "6543" }))).toBe(
      "postgres://app:s3cret@db.example.com:6543/shop?sslmode=verify-full",
    )
  })

  test("MySQL keeps its own form, the password as typed, and tls as a parameter", () => {
    expect(composeDsn("mysql", fields({ password: "p@ss:w/rd" }))).toBe(
      "app:p@ss:w/rd@tcp(db.example.com:3306)/shop",
    )
    expect(composeDsn("mysql", fields({ tls: "on" }))).toEndWith("/shop?tls=skip-verify")
    expect(composeDsn("mysql", fields({ tls: "verify" }))).toEndWith("/shop?tls=true")
  })

  test("MongoDB carries the account's database beside the encryption", () => {
    expect(composeDsn("mongodb", fields({ authSource: "admin" }))).toBe(
      "mongodb://app:s3cret@db.example.com:27017/shop?authSource=admin",
    )
    expect(composeDsn("mongodb", fields({ authSource: "admin", tls: "verify" }))).toEndWith(
      "?authSource=admin&tls=true",
    )
    expect(composeDsn("mongodb", fields({ tls: "on" }))).toEndWith(
      "/shop?tls=true&tlsInsecure=true",
    )
  })

  test("Redis says it with its scheme", () => {
    expect(composeDsn("redis", fields({ user: "", database: "2" }))).toBe(
      "redis://:s3cret@db.example.com:6379/2",
    )
    expect(composeDsn("redis", fields({ user: "", database: "", tls: "verify" }))).toBe(
      "rediss://:s3cret@db.example.com:6379/0",
    )
  })

  test("SQL Server and ClickHouse append their own parameters", () => {
    expect(composeDsn("sqlserver", fields())).toBe(
      "sqlserver://app:s3cret@db.example.com:1433?database=shop&encrypt=disable",
    )
    expect(composeDsn("sqlserver", fields({ tls: "on" }))).toEndWith(
      "&encrypt=true&TrustServerCertificate=true",
    )
    expect(composeDsn("clickhouse", fields({ tls: "verify" }))).toBe(
      "clickhouse://app:s3cret@db.example.com:9000/shop?secure=true",
    )
  })

  test("a mode a driver cannot say falls back to its first, and is not offered", () => {
    expect(tlsModes("redis")).toEqual(["off", "verify"])
    expect(tlsModes("oracle")).toEqual([])
    expect(tlsModes("postgres")).toEqual(["off", "on", "verify"])
    expect(composeDsn("redis", fields({ user: "", tls: "on" }))).toStartWith("redis://")
    expect(composeDsn("oracle", fields({ tls: "verify", database: "FREEPDB1" }))).toBe(
      "oracle://app:s3cret@db.example.com:1521/FREEPDB1",
    )
  })

  test("a file is its path", () => {
    expect(composeDsn("sqlite", fields({ database: " /srv/data/app.db " }))).toBe(
      "/srv/data/app.db",
    )
  })
})

describe("what is shown of a string", () => {
  test("the password is hidden and nothing else", () => {
    expect(maskedDsn("postgres", fields())).toBe(
      "postgres://app:••••••@db.example.com:5432/shop?sslmode=disable",
    )
    expect(maskedDsn("mysql", fields())).toBe("app:••••••@tcp(db.example.com:3306)/shop")
    expect(maskedDsn("postgres", fields({ password: "" }))).not.toContain("•")
  })

  test("a pasted string hides whatever stands where a password would", () => {
    expect(maskPasted("postgres://app:s3cret@h:5432/d")).toBe("postgres://app:••••••@h:5432/d")
    expect(maskPasted("redis://:s3cret@h:6379/0")).toBe("redis://:••••••@h:6379/0")
    expect(maskPasted("app:s3cret@tcp(h:3306)/d")).toBe("app:••••••@tcp(h:3306)/d")
    expect(maskPasted("app:p@ss@tcp(h:3306)/d")).toBe("app:••••••@tcp(h:3306)/d")
    expect(maskPasted("mongodb://h:27017/d")).toBe("mongodb://h:27017/d")
  })
})

describe("a pasted string", () => {
  test("its scheme names the driver", () => {
    expect(driverOfUrl("postgresql://a@h/d")).toBe("postgres")
    expect(driverOfUrl("  rediss://h:6380/0")).toBe("redis")
    expect(driverOfUrl("mongodb+srv://a:b@cluster.example.com/d")).toBe("mongodb")
    expect(driverOfUrl("mariadb://a@h/d")).toBe("mysql")
    expect(driverOfUrl("constructor://x")).toBeUndefined()
    expect(driverOfUrl("app:pw@tcp(h:3306)/d")).toBeUndefined()
  })

  test("goes to the driver as written, except MySQL's URL, which becomes its own form", () => {
    expect(pastedDsn("postgres", " postgres://a:b@h/d ")).toBe("postgres://a:b@h/d")
    expect(pastedDsn("mysql", "mysql://app:p%40ss@db.example.com:3307/shop?tls=true")).toBe(
      "app:p@ss@tcp(db.example.com:3307)/shop?tls=true",
    )
    expect(pastedDsn("mysql", "mariadb://app@db.example.com/shop")).toBe(
      "app@tcp(db.example.com:3306)/shop",
    )
    expect(pastedDsn("mysql", "app:pw@tcp(h:3306)/d")).toBe("app:pw@tcp(h:3306)/d")
    // A stray percent sign is somebody's password, not a broken escape.
    expect(pastedDsn("mysql", "mysql://app:100%@h/d")).toBe("app:100%@tcp(h:3306)/d")
  })
})
