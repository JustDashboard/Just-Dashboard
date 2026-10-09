import { expect, test } from "bun:test"
import {
  dnsClassicEndpoint,
  dnsClientAddress,
  dnsDomainName,
  dnsLiteralAddress,
  dnsRecordZoneEditable,
  dnsUnicastAddress,
  prepareDNSClientGroups,
  prepareDNSDomainFilterChange,
  prepareDNSRecordChange,
} from "./network-dns-service-policy"

test("canonical native address grammar preserves Go IPv6 zero-run and mapped distinctions", () => {
  for (const value of [
    "198.51.100.9",
    "2001:db8::9",
    "2001:0:0:1::1",
    "::1",
    "2001:db8:0:1:1:1:1:1",
  ])
    expect(dnsUnicastAddress(value, true)).toBe(true)
  for (const value of [
    "198.051.100.9",
    "2001:DB8::9",
    "2001:db8:0:0:0:0:0:9",
    "2001:0:0:1::1:1",
    "::ffff:198.51.100.9",
    "::198.51.100.9",
    "0.0.0.0",
    "::",
    "239.0.0.1",
    "ff02::1",
    "fe80::1%d0",
    " 198.51.100.9",
    "198.51.100.9/32",
  ])
    expect(dnsUnicastAddress(value, true)).toBe(false)
  expect(dnsLiteralAddress("2001:0:0:1:0:0:0:1").canonical).toBe("2001:0:0:1::1")
  expect(dnsLiteralAddress("2001:0:0:1:0:0:1:1").canonical).toBe("2001::1:0:0:1:1")
  expect(dnsLiteralAddress("::ffff:198.51.100.9").mapped).toBe(true)
})

test("client identity permits canonical network prefixes and explicit all-client prefixes", () => {
  for (const value of ["198.51.100.9", "198.51.100.0/24", "2001:db8::/64", "0.0.0.0/0", "::/0"])
    expect(dnsClientAddress(value)).toBe(true)
  for (const value of [
    "client.example",
    "198.51.100.9/24",
    "2001:DB8::/64",
    "2001:db8::/064",
    "::ffff:198.51.100.0/120",
    "239.0.0.0/8",
    "ff00::/8",
    "198.51.100.0/33",
    "::/129",
    "::1%lo",
  ])
    expect(dnsClientAddress(value)).toBe(false)
})

test("native endpoint and DNS owner drafts use closed literal names and ports", () => {
  for (const value of ["192.0.2.53:53", "[2001:db8::53]:5353"])
    expect(dnsClassicEndpoint(value)).toBe(true)
  for (const value of [
    "dns.example:53",
    "192.0.2.53",
    "192.0.2.53:0",
    "[::]:53",
    "[ff02::1]:53",
    "[fe80::1%lo]:53",
    "192.0.2.53:65536",
    "192.0.2.53:53#tls.example",
  ])
    expect(dnsClassicEndpoint(value)).toBe(false)
  expect(dnsDomainName("host.owned.example")).toBe(true)
  for (const value of [
    "single",
    "*.owned.example",
    "_service.owned.example",
    "UPPER.example",
    "owned.example.",
    "-bad.example",
    `${"a".repeat(64)}.example`,
  ])
    expect(dnsDomainName(value)).toBe(false)
})

const zone = () => ({
  zone: "owned.example",
  type: "Primary",
  disabled: false,
  internal: null,
  nativeVersion: "15.6",
  dnssec: "Unsigned",
})

test("missing native classification stays closed except for the inspected Primary version", () => {
  expect(dnsRecordZoneEditable(zone())).toBe(true)
  expect(dnsRecordZoneEditable({ ...zone(), nativeVersion: "15.6.0" })).toBe(true)
  for (const delta of [
    { nativeVersion: "15.7" },
    { internal: true },
    { type: "Secondary" },
    { disabled: true },
    { dnssec: "SignedWithNSEC" },
  ])
    expect(dnsRecordZoneEditable({ ...zone(), ...delta })).toBe(false)
  expect(dnsRecordZoneEditable({ ...zone(), internal: false, nativeVersion: "15.7" })).toBe(true)
})

test("record forms keep authority, family and TTL explicit while preserving rejected drafts", () => {
  const draft = {
    name: " v6.owned.example ",
    type: "AAAA",
    value: " 2001:db8::9 ",
    zone: "owned.example",
    ttl: "60",
  }
  const before = structuredClone(draft)
  expect(prepareDNSRecordChange("technitium", "record_add", draft, zone()).request).toEqual({
    action: "record_add",
    zone: "owned.example",
    record: { name: "v6.owned.example", type: "AAAA", value: "2001:db8::9", ttl: 60 },
  })
  for (const delta of [
    { ttl: "0" },
    { ttl: "86401" },
    { ttl: "1e2" },
    { ttl: "" },
    { name: "badowned.example" },
    { value: "::ffff:198.51.100.9" },
    { type: "TXT" },
  ])
    expect(
      prepareDNSRecordChange("technitium", "record_add", { ...draft, ...delta }, zone()).request,
    ).toBeUndefined()
  expect(prepareDNSRecordChange("technitium", "record_add", draft).errors.zone).toBeTruthy()
  expect(prepareDNSRecordChange("pihole", "record_add", draft, zone()).request).toBeUndefined()
  expect(draft).toEqual(before)
  expect(
    prepareDNSRecordChange("adguard", "native_command", {
      name: "local.example",
      type: "A",
      value: "198.51.100.9",
    }).errors.action,
  ).toBeTruthy()
  expect(
    prepareDNSRecordChange("adguard", "override_add", {
      name: "local.example",
      type: "A",
      value: "198.51.100.9",
    }).request,
  ).toEqual({
    action: "override_add",
    record: { name: "local.example", type: "A", value: "198.51.100.9" },
  })
  for (const delta of [{ zone: "owned.example" }, { ttl: "60" }])
    expect(
      prepareDNSRecordChange("adguard", "override_add", {
        name: "local.example",
        type: "A",
        value: "198.51.100.9",
        ...delta,
      }).request,
    ).toBeUndefined()
})

test("existing client group form requires an explicit bounded numeric list including empty", () => {
  expect(prepareDNSClientGroups("pihole", "198.51.100.9", []).request).toEqual({
    action: "client_groups",
    client: { address: "198.51.100.9", groups: [] },
  })
  const selected = [0, 7]
  expect(
    prepareDNSClientGroups("pihole", "198.51.100.0/24", selected).request.client.groups,
  ).toEqual(selected)
  expect(prepareDNSClientGroups("pihole", "198.51.100.9", selected).request.client.groups).not.toBe(
    selected,
  )
  for (const value of [
    undefined,
    null,
    ["0"],
    [0, 0],
    [-1],
    [0.5],
    [2147483648],
    Array.from({ length: 65 }, (_, i) => i),
  ])
    expect(prepareDNSClientGroups("pihole", "198.51.100.9", value).request).toBeUndefined()
  expect(prepareDNSClientGroups("adguard", "198.51.100.9", []).request).toBeUndefined()
})

test("domain filter drafts derive suffix and exact semantics from the native engine", () => {
  const draft = { domain: " selected.example ", disposition: "deny" }
  const original = structuredClone(draft)
  expect(prepareDNSDomainFilterChange("adguard", "filter_add", draft)).toEqual({
    errors: {},
    request: {
      action: "filter_add",
      filter: { domain: "selected.example", disposition: "deny", match: "suffix" },
    },
  })
  expect(prepareDNSDomainFilterChange("pihole", "filter_remove", draft).request).toEqual({
    action: "filter_remove",
    filter: { domain: "selected.example", disposition: "deny", match: "exact" },
  })
  expect(draft).toEqual(original)
  expect(prepareDNSDomainFilterChange("technitium", "filter_add", draft).errors.action).toBeTruthy()
  expect(prepareDNSDomainFilterChange("adguard", "native_dsl", draft).errors.action).toBeTruthy()
})

test("Pi-hole additions preserve explicit empty and zero memberships without sharing drafts", () => {
  for (const groups of [[], [0], [0, 7], [2147483647]]) {
    const draft = { domain: "selected.example", disposition: "allow", groups }
    const result = prepareDNSDomainFilterChange("pihole", "filter_add", draft)
    expect(result.errors).toEqual({})
    expect(result.request.filter).toEqual({
      domain: draft.domain,
      disposition: "allow",
      match: "exact",
      groups,
    })
    expect(result.request.filter.groups).not.toBe(groups)
  }
})

test("domain drafts keep invalid input while rejecting native syntax and malformed memberships", () => {
  const draft = { domain: "selected.example", disposition: "deny", groups: [] }
  for (const domain of [
    "||selected.example^",
    "@@||selected.example^",
    "*.selected.example",
    "SELECTED.example",
    "selected.example.",
    "single",
    `${"a".repeat(64)}.example`,
    "a".repeat(254),
  ]) {
    const rejected = { ...draft, domain }
    expect(
      prepareDNSDomainFilterChange("pihole", "filter_add", rejected).errors.domain,
    ).toBeTruthy()
    expect(rejected.domain).toBe(domain)
  }
  for (const groups of [
    undefined,
    null,
    [null],
    ["0"],
    [false],
    [-1],
    [0.5],
    [NaN],
    [Infinity],
    [2147483648],
    [0, 0],
    Array(1),
    Array.from({ length: 65 }, (_, id) => id),
  ])
    expect(
      prepareDNSDomainFilterChange("pihole", "filter_add", { ...draft, groups }).request,
    ).toBeUndefined()
  expect(
    prepareDNSDomainFilterChange("pihole", "filter_add", { ...draft, disposition: "regex" }).errors
      .disposition,
  ).toBeTruthy()
  for (const [engine, action] of [
    ["adguard", "filter_add"],
    ["adguard", "filter_remove"],
    ["pihole", "filter_remove"],
  ])
    for (const groups of [[], [0], null])
      expect(
        prepareDNSDomainFilterChange(engine, action, { ...draft, groups }).errors.groups,
      ).toBeTruthy()
})
