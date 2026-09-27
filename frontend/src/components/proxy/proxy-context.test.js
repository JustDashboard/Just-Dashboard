import { describe, expect, test } from "bun:test"
import { engineKind, engineUnit } from "./proxy-context"

const host = {
  nginx: false,
  caddy: false,
  nginxDir: "/etc/nginx",
  caddyFile: "/etc/caddy/Caddyfile",
  certbot: false,
}

describe("the engine the overview drives", () => {
  test("nginx wherever it is installed", () => {
    const status = { ...host, nginx: true, caddy: true, ingressContainer: "edge" }
    expect(engineKind(status)).toBe("nginx")
    expect(engineUnit(status)).toBe("nginx.service")
  })

  // Test config and Reload sent "caddy" for the Docker ingress, which ran the
  // host's caddy against the host's Caddyfile — neither of them the ingress.
  test("a running Docker Caddy is tested in its container and has no unit", () => {
    const status = { ...host, caddy: true, ingressContainer: "edge", ingressState: "running" }
    expect(engineKind(status)).toBe("caddy-ingress")
    expect(engineUnit(status)).toBeUndefined()
  })

  test("a host Caddy is its own unit", () => {
    const status = { ...host, caddy: true }
    expect(engineKind(status)).toBe("caddy")
    expect(engineUnit(status)).toBe("caddy.service")
  })

  // An ingress the first deployment would start is neither Caddy nor a
  // container: there is no engine to drive yet.
  test("an ingress that is not running yet is no engine", () => {
    expect(engineUnit({ ...host, ingressState: "provisionable" })).toBeUndefined()
  })
})
