import { expect, test } from "bun:test"
import { NextRequest } from "next/server"
import { config, proxy, withoutTrailingSlash } from "./proxy"

/** Whether the proxy runs for a path at all, by the matcher Next compiles. */
function matched(pathname) {
  return new RegExp(`^${config.matcher[0].source}$`).test(pathname)
}

test("a page address loses its trailing slash, and nothing else changes", () => {
  expect(withoutTrailingSlash("/docker/")).toBe("/docker")
  expect(withoutTrailingSlash("/docker/containers/web-1/")).toBe("/docker/containers/web-1")
  expect(withoutTrailingSlash("/docker///")).toBe("/docker")
  for (const kept of ["/", "/docker", "/docker/containers", ""])
    expect(withoutTrailingSlash(kept)).toBeUndefined()
  // Never a Location that a browser reads as another host.
  expect(withoutTrailingSlash("//evil.example/")).toBe("/evil.example")
  expect(withoutTrailingSlash("///")).toBe("/")
})

test("the redirect is permanent, keeps the query and stays on this origin", () => {
  const request = new NextRequest("http://127.0.0.1:3000/docker/containers/?tab=logs")
  const response = proxy(request)
  expect(response.status).toBe(308)
  const location = new URL(response.headers.get("location"))
  expect(location.pathname + location.search).toBe("/docker/containers?tab=logs")
  expect(location.origin).toBe(new URL(request.url).origin)
  expect(response.headers.get("content-security-policy")).toBeNull()
})

test("an address without a trailing slash is served, with the page's policy", () => {
  const response = proxy(new NextRequest("http://127.0.0.1:3000/docker/containers"))
  expect(response.status).toBe(200)
  expect(response.headers.get("content-security-policy")).toContain("script-src 'self' 'nonce-")
})

test("the API never meets the proxy, so its trailing slashes reach the backend as sent", () => {
  for (const path of ["/api/v1/jobs/", "/api/v1/deploy/", "/api/v1/auth/session"])
    expect(matched(path)).toBe(false)
  for (const path of ["/", "/docker/", "/login", "/excalidraw/9b1c"])
    expect(matched(path)).toBe(true)
})
