import { expect, test } from "bun:test"
import { calcSubnet } from "./subnet-math"

test("IPv4 host bits are masked, /31 and /32 preserve point-to-point and host ranges", () => {
  expect(calcSubnet("192.168.1.20/24")).toMatchObject({
    cidr: "192.168.1.0/24",
    first: "192.168.1.1",
    last: "192.168.1.254",
  })
  expect(calcSubnet("10.0.0.3/31")).toMatchObject({ first: "10.0.0.2", last: "10.0.0.3" })
  expect(calcSubnet("10.0.0.3/32")).toMatchObject({ first: "10.0.0.3", last: "10.0.0.3" })
  expect(calcSubnet("10.1.1.1/0").cidr).toBe("0.0.0.0/0")
})

test("IPv6 uses exact arithmetic and correctly compresses and masks addresses", () => {
  expect(calcSubnet("2001:0db8:0:1::1234/64")).toMatchObject({
    cidr: "2001:db8:0:1::/64",
    mask: "ffff:ffff:ffff:ffff::",
    last: "2001:db8:0:1:ffff:ffff:ffff:ffff",
    hosts: "18,446,744,073,709,551,616 addresses",
  })
  expect(calcSubnet("2001:db8::3/127")).toMatchObject({ first: "2001:db8::2", last: "2001:db8::3" })
  expect(calcSubnet("::1/128")).toMatchObject({ cidr: "::1/128", first: "::1", last: "::1" })
  expect(calcSubnet("::/0").last).toBe("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff")
  expect(calcSubnet("::ffff:192.0.2.1/128").cidr).toBe("::ffff:c000:201/128")
})

test("invalid and ambiguous addresses are rejected instead of returning plausible networks", () => {
  for (const input of [
    "192.168.01.1/24",
    "256.1.1.1/24",
    "1.1.1.1/33",
    "1.1.1.1/-1",
    "2001::db8::1/64",
    "1:2:3/64",
    "1:2:3:4:5:6:7:8::/64",
    "fe80::1%eth0/64",
    "::1/129",
    "::ffff:999.0.0.1/128",
  ]) {
    expect(() => calcSubnet(input)).toThrow()
  }
})

test("private and special-use address ranges are not called public", () => {
  expect(calcSubnet("100.100.1.2/24").note).toContain("carrier NAT")
  expect(calcSubnet("192.0.2.3/24").note).toContain("Documentation")
  expect(calcSubnet("198.18.1.1/24").note).toContain("Benchmarking")
  expect(calcSubnet("fc00::1/64").note).toContain("Unique local")
  expect(calcSubnet("fe80::1/64").note).toContain("Link-local")
})
