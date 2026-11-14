import { expect, test } from "bun:test"
import { dnsHref, dnsViewOf, lookupToolOf } from "./views"

test("an address opens the view its tab names, and the resolver when it names none", () => {
  expect(dnsViewOf(null)).toBe("resolver")
  expect(dnsViewOf("settings")).toBe("settings")
  expect(dnsViewOf("log")).toBe("log")
  expect(dnsViewOf("upstreams")).toBe("resolver")
})

test("the anchors older links carry open the view their section moved to", () => {
  expect(dnsViewOf(null, "#dns-links")).toBe("resolver")
  expect(dnsViewOf(null, "#dns-upstreams")).toBe("settings")
  expect(dnsViewOf(null, "#dns-hosts")).toBe("settings")
  expect(dnsViewOf(null, "#elsewhere")).toBe("resolver")
})

test("a tab in the address wins over an anchor", () => {
  expect(dnsViewOf("lookups", "#dns-upstreams")).toBe("lookups")
})

test("a lookup is offered only among those the account may run", () => {
  expect(lookupToolOf("policy", ["resolve", "policy", "dnssec"])).toBe("policy")
  expect(lookupToolOf("policy", ["resolve"])).toBe("resolve")
  expect(lookupToolOf(null, ["resolve", "policy", "dnssec"])).toBe("resolve")
  expect(lookupToolOf("zone-transfer", ["resolve", "policy", "dnssec"])).toBe("resolve")
})

test("the default view's address carries no tab", () => {
  expect(dnsHref("resolver")).toBe("/network/dns")
  expect(dnsHref("settings")).toBe("/network/dns?tab=settings")
  expect(dnsHref("lookups", { tool: "dnssec", unused: undefined })).toBe(
    "/network/dns?tab=lookups&tool=dnssec",
  )
})
