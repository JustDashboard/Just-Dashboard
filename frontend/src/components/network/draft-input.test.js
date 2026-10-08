import { expect, test } from "bun:test"
import {
  dnsServersProblem,
  literalAddress,
  noteProblem,
  preferredSourceProblem,
} from "./draft-input"

test("preferred sources are literal addresses in the route's family, including mapped IPv4", () => {
  expect(preferredSourceProblem("", "2001:db8::/64", "2001:db8::2")).toBeUndefined()
  expect(preferredSourceProblem("2001:db8::1", "2001:db8::/64", "2001:db8::2")).toBeUndefined()
  expect(preferredSourceProblem("::ffff:192.0.2.1", "192.0.2.0/24", "192.0.2.2")).toBeUndefined()
  expect(preferredSourceProblem("2001:db8::1", "192.0.2.0/24", "")).toContain("same address family")
  for (const address of ["192.0.2.01", "192.0.2.1/24", "fe80::1%eno1", "server.lan"]) {
    expect(literalAddress(address)).toBeNull()
  }
})

test("notes respect the persisted label grammar and UTF-8 limit", () => {
  expect(noteProblem("  Reply traffic for the lab  ")).toBeUndefined()
  expect(noteProblem("网".repeat(27))).toContain("80 bytes")
  expect(noteProblem("shell; directive")).toContain("printable note")
})

test("DNS draft endpoints preserve IPv6, ports, TLS names and both scope positions", () => {
  expect(dnsServersProblem([])).toBeUndefined()
  expect(
    dnsServersProblem([
      "192.0.2.53:5353#resolver.home.arpa",
      "[2001:db8::53]:853#resolver.home.arpa",
      "[fe80::53%eno1]:5353#resolver.home.arpa",
      "[fe80::53]:5353%eno1#resolver.home.arpa.",
    ]),
  ).toBeUndefined()
  for (const server of ["192.0.2.53:0", "fe80::53", "127.0.0.53", "192.0.2.53#bad_name", "::"]) {
    expect(dnsServersProblem([server])).toBeString()
  }
  expect(
    dnsServersProblem(Array.from({ length: 9 }, (_, index) => `192.0.2.${index + 1}`)),
  ).toContain("eight")
})
