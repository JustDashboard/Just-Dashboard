import { expect, test } from "bun:test"
import {
  containerConnection,
  hostCandidates,
  isDatabaseLens,
  railSourceFor,
  requestRecords,
  siteOfFile,
  siteRecord,
} from "./service-views-model"

const conn = (over) => ({
  id: 1,
  name: "shop",
  driver: "postgres",
  host: "127.0.0.1",
  port: "5432",
  user: "app",
  database: "shop",
  createdAt: "",
  ok: true,
  latencyMs: 1,
  bytes: 0,
  sizesKnown: true,
  objects: 0,
  objectWord: "tables",
  sessions: 0,
  source: "host",
  exposure: "local",
  consumers: 0,
  ...over,
})

const vhost = (over) => ({
  name: "shop",
  kind: "nginx",
  path: "/etc/nginx/sites-available/shop",
  enabled: true,
  serverNames: ["shop.example.com", "www.shop.example.com"],
  listen: ["443 ssl"],
  upstreams: ["http://127.0.0.1:3000"],
  tls: true,
  modified: "",
  size: 0,
  ...over,
})

const deployment = (over) => ({
  id: 7,
  name: "api-production",
  profile: "web",
  environmentId: 12,
  sourceKind: "image",
  sourceRepository: "ghcr.io/acme/api",
  buildMethod: "none",
  endpoint: "api.example.com",
  health: "healthy",
  pendingChanges: false,
  ...over,
})

const pulse = (over) => ({
  status: "available",
  perMinute: 21.4,
  errorRate: 0.001,
  pages: 10,
  points: [],
  ...over,
})

test("a database's log is the connection's whose container or engine wrote it", () => {
  expect(isDatabaseLens("postgres")).toBe(true)
  expect(isDatabaseLens("mssql")).toBe(true)
  expect(isDatabaseLens("http-access")).toBe(false)
  expect(isDatabaseLens(undefined)).toBe(false)

  const fleet = [
    conn({ id: 1, source: "docker", container: "shop-db" }),
    conn({ id: 2, source: "host" }),
    conn({ id: 3, source: "host", driver: "redis" }),
    conn({ id: 4, source: "remote" }),
  ]
  expect(containerConnection(fleet, "shop-db")?.id).toBe(1)
  expect(containerConnection(fleet, "other")).toBeUndefined()
  // Only the host's own servers of the log's engine are asked which file is theirs.
  expect(hostCandidates(fleet, "postgres").map((c) => c.id)).toEqual([2])
  expect(hostCandidates(fleet, "redis").map((c) => c.id)).toEqual([3])
  expect(hostCandidates(fleet, "mysql")).toEqual([])
})

test("a file is a site's when exactly one site with a record of its own names it", () => {
  const shop = vhost({
    accessLogPath: "/var/log/nginx/shop.access.log",
    errorLogPath: "/var/log/nginx/shop.error.log",
  })
  const blog = vhost({ name: "blog", accessLogPath: "/var/log/nginx/blog.access.log" })
  // A site that logs nowhere of its own has no record to read beside its errors.
  const quiet = vhost({ name: "quiet", errorLogPath: "/var/log/nginx/quiet.error.log" })
  const sites = [shop, blog, quiet]
  expect(siteOfFile(sites, "/var/log/nginx/shop.access.log")?.name).toBe("shop")
  expect(siteOfFile(sites, "/var/log/nginx/shop.error.log")?.name).toBe("shop")
  expect(siteOfFile(sites, "/var/log/nginx/quiet.error.log")).toBeUndefined()
  expect(siteOfFile(sites, "/var/log/nginx/access.log")).toBeUndefined()
  // Two sites writing one file: neither's record.
  const twin = vhost({ name: "twin", accessLogPath: "/var/log/nginx/shop.access.log" })
  expect(siteOfFile([shop, twin], "/var/log/nginx/shop.access.log")).toBeUndefined()
})

test("the records are the deployments with one, then the sites that keep their own", () => {
  const records = requestRecords(
    [
      deployment({ id: 9, name: "worker", endpoint: undefined }),
      deployment(),
      deployment({ id: 8, name: "admin", endpoint: "admin.example.com" }),
    ],
    {
      7: pulse(),
      8: pulse({ perMinute: 3, errorRate: 0.2 }),
      9: pulse({ status: "unavailable" }),
    },
    [
      vhost({ name: "shop", accessLogPath: "/var/log/nginx/shop.access.log" }),
      vhost({ name: "quiet" }),
      // The renderer's own site is the deployment's record, already listed.
      vhost({
        name: "just-dashboard-env-12.conf",
        accessLogPath: "/var/log/nginx/just-dashboard-env-12.conf.access.log",
      }),
      vhost({ name: "caddyfile", kind: "caddy", accessLogPath: "/var/log/caddy/access.log" }),
      vhost({ name: "blog", accessLogPath: "/var/log/nginx/blog.access.log", serverNames: [] }),
    ],
  )
  expect(records.map((r) => r.id)).toEqual(["deploy:8", "deploy:7", "site:blog", "site:shop"])
  const [admin, api, blog, shop] = records
  expect(api).toMatchObject({
    label: "api-production",
    detail: "api.example.com",
    figure: "21.4/min",
    tone: "default",
    base: "/deploy/7",
    subject: "deployment 7",
  })
  expect(admin.figure).toBe("3.00/min")
  expect(admin.tone).toBe("danger")
  expect(shop).toMatchObject({
    label: "shop",
    detail: "shop.example.com",
    product: "nginx-static",
    base: "/proxy/sites/shop",
  })
  // A site with no name a request can carry is found by its file.
  expect(blog.detail).toBe("/var/log/nginx/blog.access.log")
  expect(siteRecord(vhost({ name: "docker caddy:x" })).base).toBe("/proxy/sites/docker%20caddy%3Ax")
})

test("a source a view asks for is found by id, by name, by id prefix or as a unit", () => {
  const full = "3f2a9c1d0b7e5a6f7081928374655647382910abcdefabcdefabcdef012345"
  const sources = [
    { id: "file:/var/log/syslog", label: "syslog", kind: "system", rotated: true },
    { id: `docker:${full}`, label: "shop-db", kind: "docker", rotated: false },
    // A name that happens to look like the start of an id is not one.
    { id: "docker:db0000000000", label: "db", kind: "docker", rotated: false },
    { id: "journal:", label: "systemd journal", kind: "journal", rotated: false },
  ]
  expect(railSourceFor(sources, "file:/var/log/syslog")?.source.label).toBe("syslog")
  expect(railSourceFor(sources, "docker:shop-db")?.source.id).toBe(`docker:${full}`)
  expect(railSourceFor(sources, `docker:${full.slice(0, 12)}`)?.source.label).toBe("shop-db")
  expect(railSourceFor(sources, "docker:db")?.source.label).toBe("db")
  expect(railSourceFor(sources, "docker:gone")).toBeUndefined()
  expect(railSourceFor(sources, "journal:postgresql@16-main.service")).toEqual({
    source: sources[3],
    unit: "postgresql@16-main.service",
  })
  expect(railSourceFor(sources, "file:/var/log/missing.log")).toBeUndefined()
})
