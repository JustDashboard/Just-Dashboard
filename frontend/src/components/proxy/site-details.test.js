import { describe, expect, test } from "bun:test"
import { FEATURE_LABEL, activeOwner, matchesSearch, upstreamTargets } from "./site-details"

const vhost = (over) => ({
  name: "app",
  kind: "nginx",
  path: "/etc/nginx/sites-available/app",
  enabled: true,
  formEditable: true,
  serverNames: ["app.example.com"],
  listen: ["80"],
  upstreams: [],
  tls: false,
  modified: "",
  size: 0,
  ...over,
})

const pools = [{ name: "app_pool", servers: ["10.0.0.2:8080", "unix:/run/app.sock"] }]

describe("site details", () => {
  test("an upstream block reads as the servers in it", () => {
    expect(
      upstreamTargets(
        vhost({
          upstreams: ["http://app_pool", "https://app_pool/api/", "http://127.0.0.1:3000"],
          pools,
        }),
      ),
    ).toEqual([
      "http://app_pool (10.0.0.2:8080, unix:/run/app.sock)",
      "https://app_pool/api/ (10.0.0.2:8080, unix:/run/app.sock)",
      "http://127.0.0.1:3000",
    ])
  })

  test("a host with a port is not the upstream block of that name, and an empty block is left as written", () => {
    expect(upstreamTargets(vhost({ upstreams: ["http://app_pool:8080"], pools }))).toEqual([
      "http://app_pool:8080",
    ])
    expect(
      upstreamTargets(
        vhost({ upstreams: ["http://empty"], pools: [{ name: "empty", servers: [] }] }),
      ),
    ).toEqual(["http://empty"])
    expect(upstreamTargets(vhost({ upstreams: ["http://app_pool"] }))).toEqual(["http://app_pool"])
  })

  test("a search finds a site by a server behind its upstream block", () => {
    const site = vhost({ upstreams: ["http://app_pool"], pools })
    expect(matchesSearch(site, "unix:/run")).toBe(true)
    expect(matchesSearch(site, "app.example")).toBe(true)
    expect(matchesSearch(site, "elsewhere")).toBe(false)
    expect(matchesSearch(site, "")).toBe(true)
  })

  test("only a deployment that still deploys owns its route", () => {
    const owner = { projectId: 3, environmentId: 7, project: "shop", environment: "production" }
    expect(activeOwner(vhost({ owner }))).toEqual(owner)
    expect(activeOwner(vhost({ owner: { ...owner, archived: true } }))).toBeUndefined()
    expect(activeOwner(vhost({}))).toBeUndefined()
  })

  test("every feature the listing reports has a word", () => {
    expect(Object.keys(FEATURE_LABEL)).toEqual([
      "auth",
      "sso",
      "allow",
      "ratelimit",
      "cache",
      "ws",
      "h2",
      "h3",
      "maintenance",
    ])
  })
})
