import { describe, expect, test } from "bun:test"
import { CHIP_LABEL, matchesChip, sortSites } from "./site-filters"

const vhost = (over) => ({
  name: "app.example.com",
  kind: "nginx",
  path: "/etc/nginx/sites-available/app.example.com",
  enabledPath: "/etc/nginx/sites-enabled/app.example.com",
  enabled: true,
  formEditable: true,
  serverNames: ["app.example.com"],
  listen: ["443 ssl"],
  upstreams: ["http://127.0.0.1:3000"],
  tls: true,
  modified: "2026-09-01T00:00:00Z",
  size: 100,
  ...over,
})
const route = (id, over) =>
  vhost({
    name: `just-dashboard-env-${id}.conf`,
    kind: "caddy",
    path: "",
    enabledPath: undefined,
    formEditable: false,
    ...over,
  })
const owner = { projectId: 3, environmentId: 7, project: "shop", environment: "production" }
const readings = { notLive: () => false }

describe("the Leftover chip", () => {
  test("has a word", () => {
    expect(CHIP_LABEL.leftover).toBe("Leftover")
  })

  test("holds the routes nothing deploys, and no others", () => {
    const hosts = [
      vhost({}),
      route(7, { owner }),
      route(8, {}),
      route(9, { owner: { ...owner, archived: true } }),
    ]
    expect(hosts.filter((v) => matchesChip(v, "leftover", readings)).map((v) => v.name)).toEqual([
      "just-dashboard-env-8.conf",
      "just-dashboard-env-9.conf",
    ])
  })
})

describe("the Name order", () => {
  test("is by the title on the row", () => {
    const hosts = [
      route(1, { serverNames: ["zebra.example.com"], owner }),
      route(2, { serverNames: ["apple.example.com"], owner }),
      vhost({ name: "mango.example.com", serverNames: ["mango.example.com"] }),
    ]
    expect(sortSites(hosts, "name").map((v) => v.serverNames[0])).toEqual([
      "apple.example.com",
      "mango.example.com",
      "zebra.example.com",
    ])
  })
})
