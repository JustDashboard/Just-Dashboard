import { expect, test } from "bun:test"
import { existsSync } from "node:fs"
import { findingLinkLabel, findingNavigation } from "./finding-destinations"

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

test("a proxy or domain link is named for the thing it lands on", () => {
  const label = (href) => findingNavigation(href)?.label
  expect(label("/proxy/sites")).toBe("Open sites")
  expect(label("/proxy/sites?q=shop.example.test")).toBe("Open sites")
  expect(label("/proxy/sites/shop")).toBe("Open site")
  expect(label("/proxy/certificates")).toBe("Open certificates")
  expect(label("/proxy/certificates?tab=dns")).toBe("Open certificates")
  expect(label("/proxy/certificates/shop.example.test")).toBe("Open certificate")
  expect(label("/proxy/ports/tcp%3A0.0.0.0%3A443")).toBe("Open port")
  expect(label("/proxy/streams/postgres")).toBe("Open stream")
  expect(label("/deploy/7/settings/domains")).toBe("Open domains")
  // The other settings views keep the general words.
  expect(label("/deploy/7/settings/variables")).toBe("Review project settings")
  expect(findingNavigation("/proxy/sites?q=shop.example.test").href).toBe(
    "/proxy/sites?q=shop.example.test",
  )
})

test("a finding's link falls back to the old words only for an unnamed address", () => {
  expect(findingLinkLabel("/proxy/sites/shop")).toBe("Open site")
  expect(findingLinkLabel("/deploy/7/settings/domains")).toBe("Open domains")
  expect(findingLinkLabel("/not-a-page")).toBe("Open owning page")
  expect(findingLinkLabel("https://elsewhere.test")).toBe("Open owning page")
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
