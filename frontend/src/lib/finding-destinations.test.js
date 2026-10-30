import { expect, test } from "bun:test"
import { existsSync } from "node:fs"
import { findingNavigation } from "./finding-destinations"

test("legacy owner links open the resource itself and keep its requested view", () => {
  expect(findingNavigation("/docker/containers?container=abc&tab=logs")).toEqual({
    href: "/docker/containers/abc?tab=logs",
    label: "Inspect container",
  })
  expect(findingNavigation("/backups?job=31")).toEqual({
    href: "/backups/31",
    label: "Read backup run",
  })
  expect(findingNavigation("/proxy/streams?stream=postgres")).toEqual({
    href: "/proxy/streams?stream=postgres",
    label: "Open stream",
  })
  expect(findingNavigation("/network/tools?tool=dns&target=example.test&record=CAA").href).toBe(
    "/network/tools?tool=dns&target=example.test&record=CAA",
  )
})

test("unknown or external owners get no pretend in-dashboard fix", () => {
  for (const href of [
    "https://elsewhere.test",
    "//elsewhere.test",
    "javascript:alert(1)",
    "/not-a-page",
    "/deploy/1/settings/missing/deep",
    "/deploy/1/settings/missing",
    "/databases/1/missing",
  ])
    expect(findingNavigation(href)).toBeUndefined()
})

test("every named static destination has a mounted page", () => {
  for (const route of [
    "/proxy/sites",
    "/proxy/certificates",
    "/proxy/tls",
    "/proxy/streams",
    "/proxy/ports",
    "/proxy/config",
    "/proxy/traffic",
    "/proxy/tunnel",
    "/docker/containers",
    "/docker/images",
    "/docker/volumes",
    "/docker/stacks",
    "/deploy/credentials",
    "/network/firewall",
    "/network/connections",
    "/network/tools",
    "/network/dns",
    "/network/gateway",
    "/network/routing",
    "/network/protection",
    "/network/investigate",
    "/network/drift",
    "/network/ipam",
    "/network/external",
    "/network/egress",
    "/network/traffic",
    "/network/interfaces",
    "/network/vpn",
    "/security/ssh",
    "/security/intrusion",
    "/processes/services",
    "/packages",
    "/dashboard/configuration",
    "/backups",
    "/files",
    "/git",
    "/terminal",
    "/logs",
  ])
    expect(existsSync(new URL(`../app/(dashboard)${route}/page.tsx`, import.meta.url)), route).toBe(
      true,
    )
})
