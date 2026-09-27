import { describe, expect, test } from "bun:test"
import { byUrgency, isDisabled, isPlain, waiting } from "./site-order"

const vhost = (overrides) => ({
  name: "app.example.com",
  kind: "nginx",
  path: "/etc/nginx/sites-available/app.example.com",
  enabledPath: "/etc/nginx/sites-enabled/app.example.com",
  enabled: true,
  serverNames: ["app.example.com"],
  listen: ["443 ssl"],
  upstreams: ["http://127.0.0.1:3000"],
  tls: true,
  modified: "2026-09-01T00:00:00Z",
  size: 100,
  ...overrides,
})

const sites = {
  tls: vhost({ name: "tls" }),
  plain: vhost({ name: "plain", tls: false }),
  plainStatic: vhost({ name: "plain-static", tls: false, upstreams: [] }),
  plainOff: vhost({ name: "plain-off", tls: false, enabled: false }),
  off: vhost({ name: "off", enabled: false }),
  confdOff: vhost({ name: "confd-off", enabled: false, enabledPath: undefined }),
  caddyPlain: vhost({ name: "caddy-plain", kind: "caddy", tls: false, enabledPath: undefined }),
  caddyOff: vhost({ name: "caddy-off", kind: "caddy", enabled: false }),
}

describe("site order", () => {
  test("plain means an enabled site proxying an application without TLS, of either engine", () => {
    const plain = Object.entries(sites)
      .filter(([, v]) => isPlain(v))
      .map(([k]) => k)
    expect(plain).toEqual(["plain", "caddyPlain"])
  })

  test("disabled means an nginx file with a link it could have and does not", () => {
    const disabled = Object.entries(sites)
      .filter(([, v]) => isDisabled(v))
      .map(([k]) => k)
    expect(disabled).toEqual(["plainOff", "off"])
  })

  test("waiting is either", () => {
    const needs = Object.entries(sites)
      .filter(([, v]) => waiting(v))
      .map(([k]) => k)
    expect(needs).toEqual(["plain", "plainOff", "off", "caddyPlain"])
  })

  test("worst first, then by name", () => {
    const ordered = Object.values(sites)
      .sort(byUrgency)
      .map((v) => v.name)
    expect(ordered).toEqual([
      "caddy-plain",
      "plain",
      "off",
      "plain-off",
      "caddy-off",
      "confd-off",
      "plain-static",
      "tls",
    ])
  })
})
