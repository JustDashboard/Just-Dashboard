import { describe, expect, test } from "bun:test"
import { CADDY_ACCESS_LOG, engineLogSources, hostCaddy } from "./engine-log"

const status = (over) => ({
  nginx: false,
  caddy: false,
  nginxDir: "/etc/nginx",
  caddyFile: "/etc/caddy/Caddyfile",
  certbot: false,
  ...over,
})
const none = { ingress: false, caddyAccess: false }

describe("which Caddy is the host's", () => {
  test("the status says Caddy for the ingress too, so a host's own has a version or is alone", () => {
    expect(hostCaddy(status({ caddy: true }))).toBe(true)
    expect(hostCaddy(status({ caddy: true, ingressContainer: "caddy" }))).toBe(false)
    expect(
      hostCaddy(status({ caddy: true, caddyVersion: "2.8.4", ingressContainer: "caddy" })),
    ).toBe(true)
    expect(hostCaddy(status())).toBe(false)
  })
})

describe("the engine's logs", () => {
  test("host nginx reads its two files and its unit, then the ingress beside it", () => {
    const sources = engineLogSources(
      status({ nginx: true, caddy: true, ingressContainer: "edge" }),
      { ...none, ingress: true },
    )
    expect(sources.map((s) => [s.id, s.lens])).toEqual([
      ["file:/var/log/nginx/error.log", "nginx-error"],
      ["file:/var/log/nginx/access.log", "http-access"],
      ["journal:nginx.service", "nginx-error"],
      ["docker:edge", "caddy"],
    ])
  })

  test("a host Caddy keeps its journal beside an ingress, and its access file once it was found", () => {
    const both = status({ caddy: true, caddyVersion: "2.8.4", ingressContainer: "edge" })
    expect(engineLogSources(both, { ingress: true, caddyAccess: false }).map((s) => s.id)).toEqual([
      "journal:caddy.service",
      "docker:edge",
    ])
    const sources = engineLogSources(both, { ingress: true, caddyAccess: true })
    expect(sources.map((s) => [s.id, s.lens, s.product])).toEqual([
      ["journal:caddy.service", "caddy", "caddy"],
      [`file:${CADDY_ACCESS_LOG}`, "http-access", "caddy"],
      ["docker:edge", "caddy", "caddy"],
    ])
  })

  test("the ingress alone is its container, and only once it was found", () => {
    const edge = status({ caddy: true, ingressContainer: "edge" })
    expect(engineLogSources(edge, none)).toEqual([])
    expect(engineLogSources(edge, { ...none, ingress: true }).map((s) => s.id)).toEqual([
      "docker:edge",
    ])
    expect(engineLogSources(status({ caddy: true }), none).map((s) => s.id)).toEqual([
      "journal:caddy.service",
    ])
  })
})
