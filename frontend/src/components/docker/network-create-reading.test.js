import { expect, test } from "bun:test"
import { EMPTY_NETWORK, networkCreation } from "./network-create-reading"

test("explicit pools keep both families, allocation limits and driver metadata", () => {
  const spec = networkCreation({
    ...EMPTY_NETWORK,
    name: " dual ",
    driver: "macvlan",
    subnet: "192.0.2.0/24",
    gateway: "192.0.2.1",
    ipRange: "192.0.2.128/25",
    ipv6: true,
    ipv6Subnet: "FD00:1::/64",
    ipv6Gateway: "fd00:1::1",
    ipv6Range: "fd00:1::1000/116",
    labels: "purpose=app=private",
    options: "parent=eth0\ncom.docker.network.driver.mtu=1400",
  })
  expect(spec.name).toBe("dual")
  expect(spec.driver).toBe("macvlan")
  expect(spec.ipam).toEqual([
    { subnet: "192.0.2.0/24", gateway: "192.0.2.1", ipRange: "192.0.2.128/25" },
    { subnet: "fd00:1::/64", gateway: "fd00:1::1", ipRange: "fd00:1::1000/116" },
  ])
  expect(spec.subnet).toBeUndefined()
  expect(spec.labels).toEqual({ purpose: "app=private" })
  expect(spec.options).toEqual({ parent: "eth0", "com.docker.network.driver.mtu": "1400" })
})

test("simple and disabled IPv6 drafts preserve the ordinary bridge request", () => {
  const draft = { ...EMPTY_NETWORK, name: "app", subnet: "192.0.2.0/24", ipv6Subnet: "fd00:1::/64" }
  expect(JSON.parse(JSON.stringify(networkCreation(draft)))).toEqual({
    name: "app",
    internal: false,
    subnet: "192.0.2.0/24",
  })
  expect(draft.ipv6Subnet).toBe("fd00:1::/64")
})

for (const [name, patch, reason] of [
  ["host bits", { subnet: "192.0.2.10/24" }, "network address"],
  ["foreign gateway", { subnet: "192.0.2.0/24", gateway: "198.51.100.1" }, "inside its subnet"],
  ["foreign range", { subnet: "192.0.2.0/24", ipRange: "198.51.100.0/24" }, "fit inside"],
  ["unbounded range", { subnet: "192.0.2.0/24", ipRange: "192.0.2.0/23" }, "fit inside"],
  ["wrong family", { subnet: "fd00:1::/64" }, "IPv4"],
  ["duplicate key", { labels: "purpose=one\npurpose=two" }, "repeated key"],
  ["reserved owner", { labels: "io.just-dashboard.managed=true" }, "reserved"],
  ["reserved compose", { labels: "com.docker.compose.project=app" }, "reserved"],
  ["oversized value", { options: `parent=${"x".repeat(4097)}` }, "4096 bytes"],
]) {
  test(`invalid ${name} cannot become a creation request`, () => {
    expect(() => networkCreation({ ...EMPTY_NETWORK, name: "app", ...patch })).toThrow(reason)
  })
}

test("overlap uses address families and containment in either direction", () => {
  const existing = [{ name: "owned", subnets: ["192.0.2.0/24", "fd00:1::/64"] }]
  expect(() =>
    networkCreation({ ...EMPTY_NETWORK, name: "app", subnet: "192.0.2.128/25" }, existing),
  ).toThrow("overlaps owned")
  expect(() =>
    networkCreation(
      { ...EMPTY_NETWORK, name: "app", ipv6: true, ipv6Subnet: "fd00::/16" },
      existing,
    ),
  ).toThrow("overlaps owned")
  expect(
    networkCreation(
      { ...EMPTY_NETWORK, name: "app", ipv6: true, ipv6Subnet: "fd00:2::/64" },
      existing,
    ).ipam,
  ).toEqual([{ subnet: "fd00:2::/64" }])
})
