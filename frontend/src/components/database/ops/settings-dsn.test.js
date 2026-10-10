import { describe, expect, test } from "bun:test"
import { maskedDsn, patchDsn, readDsn } from "./settings-dsn"

describe("a saved connection string is read for what a form shows", () => {
  test("a URL gives its account, address, database and every option", () => {
    expect(
      readDsn(
        "postgres://app:s3cr%40t@db.internal:6432/shop?sslmode=verify-full&connect_timeout=5",
      ),
    ).toEqual({
      shape: "url",
      scheme: "postgres",
      user: "app",
      password: "s3cr@t",
      host: "db.internal",
      port: "6432",
      hosts: "",
      database: "shop",
      options: ["sslmode=verify-full", "connect_timeout=5"],
    })
  })

  test("the MySQL driver's own form is read as it is written, not decoded", () => {
    const parts = readDsn("app:p%40ss@word@tcp(127.0.0.1:3306)/blog?tls=true&parseTime=true")
    expect(parts.shape).toBe("native")
    expect(parts.user).toBe("app")
    // The driver takes the password as typed: nothing in it is an escape.
    expect(parts.password).toBe("p%40ss@word")
    expect(parts.host).toBe("127.0.0.1")
    expect(parts.port).toBe("3306")
    expect(parts.database).toBe("blog")
    expect(parts.options).toEqual(["tls=true", "parseTime=true"])
  })

  test("a database named in the options is the database, and not one of them", () => {
    const parts = readDsn(
      "sqlserver://sa:JdTest%232024pw@127.0.0.1:1433?database=shop&encrypt=disable",
    )
    expect(parts.database).toBe("shop")
    expect(parts.password).toBe("JdTest#2024pw")
    expect(parts.options).toEqual(["encrypt=disable"])
  })

  test("several hosts are shown as written and never split", () => {
    const parts = readDsn(
      "mongodb://app:pw@a.example:27017,b.example:27017/app?replicaSet=rs0&authSource=admin",
    )
    expect(parts.hosts).toBe("a.example:27017,b.example:27017")
    expect(parts.host).toBe("")
    expect(parts.options).toEqual(["replicaSet=rs0", "authSource=admin"])
  })

  test("an IPv6 address keeps its brackets out of the host", () => {
    expect(readDsn("redis://:pw@[::1]:6380/3")).toMatchObject({
      host: "::1",
      port: "6380",
      user: "",
      password: "pw",
      database: "3",
    })
  })

  test("a path is a file", () => {
    expect(readDsn("/srv/notes/notes.db")).toMatchObject({
      shape: "path",
      database: "/srv/notes/notes.db",
      options: [],
    })
  })
})

describe("a change replaces what was edited and nothing else", () => {
  test("a new password leaves sslmode and every other option as saved", () => {
    const saved = "postgres://app:old@db.internal:6432/shop?sslmode=verify-full&connect_timeout=5"
    expect(patchDsn(saved, { password: "n3w p@ss" })).toBe(
      "postgres://app:n3w%20p%40ss@db.internal:6432/shop?sslmode=verify-full&connect_timeout=5",
    )
  })

  test("sending every field back unchanged is the same string", () => {
    for (const saved of [
      "postgres://app:s3cr%40t@db.internal:6432/shop?sslmode=require",
      "app:p@ss@tcp(127.0.0.1:3306)/blog?tls=skip-verify",
      "sqlserver://sa:x%23y@127.0.0.1:1433?database=shop&encrypt=disable",
      "mongodb://app:pw@a:27017,b:27017/app?replicaSet=rs0",
      "rediss://:pw@cache.internal:6380/0?skip_verify=true",
      "clickhouse://default@127.0.0.1:9000/default?secure=true#frag",
      "/srv/notes/notes.db",
    ]) {
      const { host, port, user, password, database } = readDsn(saved)
      expect(patchDsn(saved, { host, port, user, password, database })).toBe(saved)
    }
  })

  test("the half of the account that was not edited keeps its own spelling", () => {
    // The saved password is escaped in a way this code would not write.
    const saved = "postgres://app:a%2Bb%21@h:5432/d?sslmode=require"
    expect(patchDsn(saved, { user: "reporting" })).toBe(
      "postgres://reporting:a%2Bb%21@h:5432/d?sslmode=require",
    )
  })

  test("the MySQL form takes the password as typed and keeps its parameters", () => {
    const saved = "app:old@tcp(127.0.0.1:3306)/blog?tls=true&parseTime=true"
    expect(patchDsn(saved, { password: "p@ss:word" })).toBe(
      "app:p@ss:word@tcp(127.0.0.1:3306)/blog?tls=true&parseTime=true",
    )
    expect(patchDsn(saved, { host: "db.internal", port: "3307" })).toBe(
      "app:old@tcp(db.internal:3307)/blog?tls=true&parseTime=true",
    )
    expect(patchDsn(saved, { database: "blog_next" })).toBe(
      "app:old@tcp(127.0.0.1:3306)/blog_next?tls=true&parseTime=true",
    )
  })

  test("a database named in the options is changed there", () => {
    const saved = "sqlserver://sa:pw@127.0.0.1:1433?database=shop&encrypt=disable"
    expect(patchDsn(saved, { database: "shop next" })).toBe(
      "sqlserver://sa:pw@127.0.0.1:1433?database=shop%20next&encrypt=disable",
    )
  })

  test("a host and a port are changed without touching the rest", () => {
    const saved = "mongodb://app:pw@127.0.0.1:27017/app?authSource=admin&tls=true"
    expect(patchDsn(saved, { host: "::1", port: "27018" })).toBe(
      "mongodb://app:pw@[::1]:27018/app?authSource=admin&tls=true",
    )
  })

  test("several hosts are never rewritten from one field", () => {
    const saved = "mongodb://app:pw@a:27017,b:27017/app?replicaSet=rs0"
    expect(patchDsn(saved, { host: "c", port: "1" })).toBe(saved)
    expect(patchDsn(saved, { password: "new" })).toBe(
      "mongodb://app:new@a:27017,b:27017/app?replicaSet=rs0",
    )
  })

  test("an account is added to a string that had none", () => {
    expect(patchDsn("redis://127.0.0.1:6379/0", { password: "pw" })).toBe(
      "redis://:pw@127.0.0.1:6379/0",
    )
    expect(patchDsn("mongodb://127.0.0.1:27017/app", { user: "app", password: "pw" })).toBe(
      "mongodb://app:pw@127.0.0.1:27017/app",
    )
  })

  test("a file's path is the whole string", () => {
    expect(patchDsn("/srv/a.db", { database: " /srv/b.db " })).toBe("/srv/b.db")
  })
})

describe("the string shown before it is saved hides the password", () => {
  test("in a URL and in the MySQL form", () => {
    expect(maskedDsn("postgres://app:s3cret@h:5432/d?sslmode=require")).toBe(
      "postgres://app:••••••@h:5432/d?sslmode=require",
    )
    expect(maskedDsn("app:p@ss@tcp(h:3306)/d?tls=true")).toBe("app:••••••@tcp(h:3306)/d?tls=true")
  })

  test("a string with no password is shown as it is", () => {
    expect(maskedDsn("redis://127.0.0.1:6379/0")).toBe("redis://127.0.0.1:6379/0")
    expect(maskedDsn("/srv/a.db")).toBe("/srv/a.db")
  })
})
