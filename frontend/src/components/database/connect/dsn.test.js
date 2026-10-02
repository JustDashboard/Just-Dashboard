import { describe, expect, test } from "bun:test"
import {
  addressOfPasted,
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

  test("Redis says it with its scheme, Oracle with parameters whose names hold a space", () => {
    expect(tlsModes("redis")).toEqual(["off", "on", "verify"])
    expect(composeDsn("redis", fields({ user: "", tls: "off", database: "1" }))).toStartWith(
      "redis://",
    )
    expect(composeDsn("redis", fields({ user: "", tls: "verify", database: "1" }))).toBe(
      "rediss://:s3cret@db.example.com:6379/1",
    )
    expect(composeDsn("redis", fields({ user: "", tls: "on", database: "1" }))).toBe(
      "rediss://:s3cret@db.example.com:6379/1?skip_verify=true",
    )
    expect(tlsModes("oracle")).toEqual(["off", "on", "verify"])
    expect(composeDsn("oracle", fields({ tls: "off", database: "FREEPDB1" }))).toBe(
      "oracle://app:s3cret@db.example.com:1521/FREEPDB1",
    )
    expect(composeDsn("oracle", fields({ tls: "on", database: "FREEPDB1" }))).toBe(
      "oracle://app:s3cret@db.example.com:1521/FREEPDB1?SSL=true&SSL%20VERIFY=false",
    )
    expect(composeDsn("oracle", fields({ tls: "verify", database: "FREEPDB1" }))).toEndWith(
      "/FREEPDB1?SSL=true",
    )
  })

  test("a file has no transport to encrypt, and is offered no mode", () => {
    expect(tlsModes("sqlite")).toEqual([])
    expect(tlsModes("postgres")).toEqual(["off", "on", "verify"])
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

  test("a password with an @ of its own is hidden whole, and so is one given as a parameter", () => {
    expect(maskPasted("postgres://jdtest:p@ss:w0rd@127.0.0.1:55432/shop")).toBe(
      "postgres://jdtest:••••••@127.0.0.1:55432/shop",
    )
    expect(
      maskPasted(
        "sqlserver://127.0.0.1:51433?database=shop&user id=sa&password=Sup3rSecret&encrypt=disable",
      ),
    ).toBe("sqlserver://127.0.0.1:51433?database=shop&user id=sa&password=••••••&encrypt=disable")
    expect(maskPasted("clickhouse://h:9000/d?username=app&Password=s3cret")).toBe(
      "clickhouse://h:9000/d?username=app&Password=••••••",
    )
    expect(maskPasted("host=h user=app password=s3cret dbname=d")).toBe(
      "host=h user=app password=•••••• dbname=d",
    )
    // The path and the parameters after the address are not a password.
    expect(maskPasted("postgres://app@h/d?options=a@b")).toBe("postgres://app@h/d?options=a@b")
  })
})

describe("what a pasted string points at", () => {
  test("the host and the database, from a URL and from MySQL's own form", () => {
    expect(addressOfPasted("postgres://app:pw@db.example.com:5432/shop?sslmode=disable")).toEqual({
      host: "db.example.com",
      database: "shop",
    })
    expect(addressOfPasted("app:p@ss@tcp(10.0.0.9:3306)/inventory?tls=true")).toEqual({
      host: "10.0.0.9",
      database: "inventory",
    })
    expect(addressOfPasted("redis://:pw@cache.internal:6379/1")).toEqual({
      host: "cache.internal",
      database: "1",
    })
    expect(addressOfPasted("sqlserver://sa:pw@127.0.0.1:1433?database=shop_a1")).toEqual({
      host: "127.0.0.1",
      database: "shop_a1",
    })
    expect(addressOfPasted("postgres://app:p@ss@[::1]:5432/d").host).toBe("[::1]")
    expect(addressOfPasted("not a string")).toEqual({ host: "", database: "" })
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
