import { describe, expect, test } from "bun:test"
import { clientOf, clientProduct, knownClients } from "./clients"

describe("clientProduct", () => {
  test("reads the engines' own tools as the engine", () => {
    expect(clientProduct("psql")).toBe("postgres")
    expect(clientProduct("pg_dump")).toBe("postgres")
    expect(clientProduct("pg_basebackup")).toBe("postgres")
    expect(clientProduct("mysqldump")).toBe("mysql")
    expect(clientProduct("redis-cli")).toBe("redis")
    expect(clientProduct("mongosh 2.3.1")).toBe("mongodb")
    expect(clientProduct("MongoDB Compass")).toBe("mongodb")
  })

  test("reads the driver before the database a driver names", () => {
    expect(clientProduct("PostgreSQL JDBC Driver")).toBe("java")
    expect(clientProduct("node-postgres")).toBe("nodejs")
    expect(clientProduct("redis-py")).toBe("python")
    expect(clientProduct("sidekiq-worker")).toBe("ruby")
    expect(clientProduct("bullmq")).toBe("nodejs")
  })

  test("reads a product before the word it is made of", () => {
    expect(clientProduct("mongo-express")).toBe("mongo-express")
    expect(clientProduct("postgres_exporter")).toBe("prometheus")
    expect(clientProduct("Metabase v0.50.3 [6bb2c3a2]")).toBe("metabase")
    expect(clientProduct("pgAdmin 4 - DB:shop_main")).toBe("pgadmin")
  })

  test("names nothing it cannot read", () => {
    expect(clientProduct("")).toBeUndefined()
    expect(clientProduct(undefined)).toBeUndefined()
    expect(clientProduct("billing-worker")).toBeUndefined()
    expect(clientProduct("DBeaver 24.1.0 - Main")).toBeUndefined()
  })
})

describe("clientOf", () => {
  const topology = {
    checkedAt: "2026-10-01T09:00:00Z",
    nodes: [
      { id: "db:1", kind: "database", name: "shop", product: "postgres", connId: 1 },
      {
        id: "deploy:7",
        kind: "deployment",
        name: "shop-api",
        product: "nodejs",
        href: "/deploy/7",
      },
      { id: "container:grafana", kind: "container", name: "Grafana", product: "grafana" },
      { id: "host", kind: "host", name: "This server" },
    ],
    edges: [],
  }
  const known = knownClients(topology, 1)

  test("draws a session named after a deployment as that deployment", () => {
    const client = clientOf("shop-api", known)
    expect(client.product).toBe("nodejs")
    expect(client.known?.href).toBe("/deploy/7")
  })

  test("matches a known client whatever its case", () => {
    expect(clientOf("grafana", known).known?.name).toBe("Grafana")
  })

  test("falls back to the name's own product, and leaves out this server and the database", () => {
    expect(clientOf("psql", known)).toEqual({ product: "postgres", known: undefined })
    expect(clientOf("shop", known).known).toBeUndefined()
    expect(clientOf("This server", known).known).toBeUndefined()
    expect(clientOf(undefined, known)).toEqual({})
  })
})
