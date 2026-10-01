import { describe, expect, test } from "bun:test"
import { engineFor } from "../engine"
import {
  MASK,
  connectFormats,
  connectSnippet,
  connectTargets,
  holdsSecret,
  maskedParts,
  revealedParts,
} from "./connection-string"

function conn(overrides = {}) {
  return {
    id: 7,
    name: "shop",
    driver: "postgres",
    host: "127.0.0.1",
    port: "55432",
    user: "app",
    database: "shop_main",
    createdAt: "2026-09-01T00:00:00Z",
    ...overrides,
  }
}

function access(overrides = {}) {
  return {
    detected: true,
    container: "shop-db",
    managed: true,
    exposure: "local",
    port: 55432,
    publicAddresses: ["2001:db8::13", "203.0.113.7"],
    firewall: { active: true, open: false, editable: true },
    ...overrides,
  }
}

const targets = (c, a) => connectTargets(c, engineFor(c), a)

describe("where a database is connected from", () => {
  test("without an administrator's reading there is one address: the one saved", () => {
    expect(targets(conn())).toEqual([
      { id: "host", label: "As saved", host: "127.0.0.1", port: "55432", note: expect.any(String) },
    ])
  })

  test("a file has no network, whatever is known", () => {
    const file = conn({ driver: "sqlite", host: "localhost", port: "", database: "/data/app.db" })
    expect(targets(file, access()).map((t) => t.id)).toEqual(["host"])
    expect(targets(file, access())[0].note).toContain("file")
  })

  test("a server on another machine is reached where it was saved", () => {
    const remote = conn({ host: "db.example.com", port: "5432" })
    expect(targets(remote, access({ exposure: "remote", container: undefined }))).toHaveLength(1)
  })

  test("a container on this machine has a name for linked deployments and no public string until published", () => {
    const [host, container, anywhere] = targets(conn(), access())
    expect(host).toMatchObject({
      id: "host",
      label: "This server",
      host: "127.0.0.1",
      port: "55432",
    })
    expect(container).toMatchObject({ id: "container", host: "db-7.jd.internal", port: "5432" })
    expect(anywhere.host).toBeUndefined()
    expect(anywhere.unavailable).toContain("Settings")
  })

  test("a published port is reached on the machine's IPv4 address", () => {
    const anywhere = targets(conn(), access({ exposure: "public" }))[2]
    expect(anywhere).toMatchObject({ id: "public", host: "203.0.113.7", port: "55432" })
    expect(anywhere.unavailable).toBeUndefined()
  })

  test("a server installed on the host has no name on a Docker network", () => {
    const container = targets(conn(), access({ container: undefined, managed: false }))[1]
    expect(container.host).toBeUndefined()
    expect(container.unavailable).toContain("host")
  })

  test("published with no address of its own, or on one address only, says so", () => {
    expect(
      targets(conn(), access({ exposure: "public", publicAddresses: [] }))[2].unavailable,
    ).toContain("port 55432")
    expect(targets(conn(), access({ exposure: "private" }))[2].unavailable).toContain("one address")
    expect(targets(conn(), access({ managed: false }))[2].unavailable).toBe(
      "Not reachable from outside this server.",
    )
  })
})

describe("the string, with the password hidden", () => {
  test("is built from what the page already holds", () => {
    const c = conn()
    const engine = engineFor(c)
    const parts = maskedParts(c, engine, targets(c)[0])
    expect(parts.url).toBe(`postgres://app:${MASK}@127.0.0.1:55432/shop_main?sslmode=disable`)
    expect(holdsSecret(parts.url)).toBe(true)
  })

  test("names the target's address, not the saved one", () => {
    const c = conn()
    const engine = engineFor(c)
    const [, container] = targets(c, access())
    expect(maskedParts(c, engine, container).url).toBe(
      `postgres://app:${MASK}@db-7.jd.internal:5432/shop_main?sslmode=disable`,
    )
    const ipv6 = targets(c, access({ exposure: "public", publicAddresses: ["2001:db8::13"] }))[2]
    expect(maskedParts(c, engine, ipv6).url).toContain("@[2001:db8::13]:55432/")
  })

  test("MySQL's is the URL applications expect", () => {
    const c = conn({ driver: "mysql", port: "3306", database: "blog" })
    expect(maskedParts(c, engineFor(c), targets(c)[0]).url).toBe(
      `mysql://app:${MASK}@127.0.0.1:3306/blog`,
    )
  })
})

describe("the shapes it is offered in", () => {
  const c = conn()
  const engine = engineFor(c)
  const parts = maskedParts(c, engine, targets(c)[0])

  test("the URL, an .env line, the engine's shell and its clients", () => {
    expect(connectFormats(engine).map((f) => f.label)).toEqual([
      "URL",
      ".env",
      "psql",
      "Node.js",
      "Python",
    ])
    expect(connectFormats(engineFor({ driver: "mysql", flavor: "mariadb" }))[2].label).toBe(
      "mariadb",
    )
  })

  test("each shape is written around the same string", () => {
    expect(connectSnippet(engine, parts, "url")).toBe(parts.url)
    expect(connectSnippet(engine, parts, "env")).toBe(`DATABASE_URL=${parts.url}`)
    expect(connectSnippet(engine, parts, "cli")).toBe(`psql "${parts.url}"`)
    expect(connectSnippet(engine, parts, "node")).toContain(`connectionString: "${parts.url}"`)
    expect(connectSnippet(engine, parts, "no-such-shape")).toBe(parts.url)
  })

  test("the revealed string replaces the masked one in every shape", () => {
    const real = revealedParts(
      parts,
      engine,
      "postgres://app:s3cret@127.0.0.1:55432/shop_main?sslmode=disable",
    )
    expect(connectSnippet(engine, real, "env")).toBe(
      "DATABASE_URL=postgres://app:s3cret@127.0.0.1:55432/shop_main?sslmode=disable",
    )
    expect(holdsSecret(connectSnippet(engine, real, "env"))).toBe(false)
  })

  test("a shell that asks for the password itself holds no secret", () => {
    const m = conn({ driver: "mysql", port: "3306", database: "blog" })
    const mysql = engineFor(m)
    const cli = connectSnippet(mysql, maskedParts(m, mysql, targets(m)[0]), "cli")
    expect(cli).toBe("mysql --host=127.0.0.1 --port=3306 --user=app --password blog")
    expect(holdsSecret(cli)).toBe(false)
  })
})
