import { expect, test } from "bun:test"
import { wireGuardIPv6Payload, wireGuardIPv6Problem } from "./ipv6-input"

test("WireGuard IPv6 allocation is opt-in and bounded to a unique-local /64", () => {
  for (const valid of ["", " fd42:8::/64 ", "fc00:1::99/64"])
    expect(wireGuardIPv6Problem(valid)).toBeUndefined()
  for (const invalid of [
    "10.8.0.0/24",
    "2001:db8::/64",
    "fe80::/64",
    "fd42:8::/48",
    "fd42:8::/128",
    "fd42:8::/064",
    "fd42:8::%eno1/64",
    "fd42:8::/64/64",
  ])
    expect(wireGuardIPv6Problem(invalid)).toBeDefined()
  expect(wireGuardIPv6Payload(false, "fd42:8::/64", true)).toBeUndefined()
  expect(wireGuardIPv6Payload(true, "", false)).toEqual({ subnet: undefined, exitNode: false })
  expect(wireGuardIPv6Payload(true, " fd42:8::/64 ", true)).toEqual({
    subnet: "fd42:8::/64",
    exitNode: true,
  })
})
