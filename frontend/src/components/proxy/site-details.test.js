import { describe, expect, test } from "bun:test"
import {
  FEATURE_LABEL,
  activeOwner,
  isLeftover,
  matchesSearch,
  siteKind,
  siteOrigin,
  siteTitle,
  upstreamTargets,
} from "./site-details"

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

const owner = { projectId: 3, environmentId: 450, project: "media", environment: "production" }
const route = (over) =>
  vhost({
    name: "just-dashboard-env-450.conf",
    kind: "caddy",
    path: "",
    formEditable: false,
    serverNames: ["jellyfin.example.com"],
    upstreams: ["http://10.0.0.10:8096"],
    ...over,
  })

describe("a site's title", () => {
  test("is the first domain its visitors type, not its file", () => {
    expect(siteTitle(route({}))).toBe("jellyfin.example.com")
    expect(siteTitle(vhost({ serverNames: ["_", "", "app.example.com"] }))).toBe("app.example.com")
  })

  test("a host's Caddyfile is the file: it holds every address, not one", () => {
    const caddyfile = vhost({
      name: "Caddyfile",
      kind: "caddy",
      path: "/etc/caddy/Caddyfile",
      serverNames: [":80", "https://blog.example.com"],
    })
    expect(siteTitle(caddyfile)).toBe("Caddyfile")
  })

  test("falls back to the file where nginx's catch-all is all it answers to", () => {
    expect(siteTitle(vhost({ name: "default", serverNames: ["_"] }))).toBe("default")
    expect(siteTitle(vhost({ name: "gone", serverNames: [] }))).toBe("gone")
    expect(siteTitle(vhost({ name: "blank", serverNames: ["", " "] }))).toBe("blank")
  })
})

describe("a route its deployment left", () => {
  test("is one with deploy's name and nothing deploying it", () => {
    expect(isLeftover(route({}))).toBe(true)
    expect(isLeftover(route({ owner: { ...owner, archived: true } }))).toBe(true)
    expect(isLeftover(route({ owner }))).toBe(false)
    // Another file with no owner is the operator's own site.
    expect(isLeftover(vhost({}))).toBe(false)
    expect(isLeftover(route({ name: "just-dashboard-env-0.conf" }))).toBe(false)
  })

  test("says so under its title, and why", () => {
    expect(siteKind(route({}))).toBe("Leftover · its deployment was deleted")
    expect(siteKind(route({ owner: { ...owner, archived: true } }))).toBe(
      "Leftover · media is archived",
    )
    expect(siteKind(route({ owner }))).toBe("media · production")
  })

  test("the engine is never the subtitle", () => {
    const kinds = [
      route({}),
      route({ owner }),
      route({ name: "docker-caddy:edge:app.example.com" }),
      route({ name: "other" }),
      route({ name: "blog", path: "/etc/caddy/Caddyfile" }),
      vhost({}),
      vhost({ layout: "conf.d" }),
    ].map(siteKind)
    expect(kinds).toEqual([
      "Leftover · its deployment was deleted",
      "media · production",
      "Ingress Caddyfile",
      "Caddy route",
      "Caddyfile",
      "nginx site",
      "nginx site in conf.d",
    ])
    for (const kind of kinds) expect(kind).not.toContain("Docker")
  })
})

describe("why a site is not removed from here", () => {
  test("a live deployment's route leads to its Domains settings", () => {
    expect(siteOrigin(route({ owner }))).toEqual({
      kind: "deployment",
      text: "Written by media on every deploy.",
      link: { label: "Remove the domain in media · Domains", href: "/deploy/3/settings/domains" },
    })
  })

  test("a leftover says whose it was", () => {
    expect(siteOrigin(route({}))?.text).toBe("Left by a deleted deployment")
    expect(siteOrigin(route({ owner: { ...owner, archived: true } }))?.text).toBe(
      "Left by media, which is archived",
    )
  })

  test("Caddy's own sites say where they are written; an nginx site has Delete", () => {
    expect(siteOrigin(route({ name: "blog", path: "/etc/caddy/Caddyfile" }))?.kind).toBe(
      "caddyfile",
    )
    expect(siteOrigin(route({ name: "docker-caddy:edge:app.example.com" }))?.text).toBe(
      "Defined in the ingress container's own Caddyfile",
    )
    expect(siteOrigin(vhost({}))).toBeUndefined()
  })
})

describe("search by who deploys a site", () => {
  test("finds a route by its project and its environment", () => {
    expect(matchesSearch(route({ owner }), "media")).toBe(true)
    expect(matchesSearch(route({ owner }), "production")).toBe(true)
    expect(matchesSearch(route({}), "media")).toBe(false)
  })

  test("still finds it by the file the title no longer says", () => {
    expect(matchesSearch(route({ owner }), "env-450")).toBe(true)
  })
})
