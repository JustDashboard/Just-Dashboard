import { describe, expect, test } from "bun:test"
import { accessFor, checkEntry, parseAddress, sameEntry } from "./access-lists"

describe("parseAddress", () => {
  test("reads IPv4 and IPv6 as nginx does", () => {
    expect(parseAddress("10.0.0.1")).toEqual({ family: 4, words: [0x0a00, 0x0001] })
    expect(parseAddress("::1")).toEqual({ family: 6, words: [0, 0, 0, 0, 0, 0, 0, 1] })
    expect(parseAddress("2001:DB8::1")?.words).toEqual([0x2001, 0xdb8, 0, 0, 0, 0, 0, 1])
    expect(parseAddress("::ffff:10.0.0.1")?.words).toEqual([0, 0, 0, 0, 0, 0xffff, 0x0a00, 1])
    expect(parseAddress("1:2:3:4:5:6:7::")?.words).toEqual([1, 2, 3, 4, 5, 6, 7, 0])
  })

  test("refuses what is not one address", () => {
    for (const text of [
      "",
      "10.0.0",
      "10.0.0.256",
      "010.0.0.1",
      "1::2::3",
      "1:2:3:4:5:6:7:8:9",
      "fe80::1%eth0",
      "::ffff:10.0.0.300",
      "office.example.com",
    ]) {
      expect(parseAddress(text)).toBeNull()
    }
  })
})

describe("checkEntry", () => {
  test("takes addresses and ranges", () => {
    for (const entry of [
      "10.0.0.1",
      "10.0.0.0/8",
      "0.0.0.0/0",
      "2001:db8::/32",
      "::/0",
      " 192.168.1.7 ",
    ]) {
      expect(checkEntry(entry)).toBeNull()
    }
  })

  test("says why, in the server's words", () => {
    expect(checkEntry("all")).toContain("rather than all")
    expect(checkEntry("10.0.0.300")).toBe(
      '"10.0.0.300" is not an IP address or a range like 10.0.0.0/8',
    )
    expect(checkEntry("10.0.0.0/33")).toContain("is not an IP address")
    expect(checkEntry("10.0.0.5/8")).toBe(
      "10.0.0.5/8 has bits set past its /8 — the range is 10.0.0.0/8",
    )
    expect(checkEntry("2001:db8::1/32")).toBe(
      "2001:db8::1/32 has bits set past its /32 — the range is 2001:db8::/32",
    )
    expect(checkEntry("fe80::1:2:3:4/64")).toBe(
      "fe80::1:2:3:4/64 has bits set past its /64 — the range is fe80::/64",
    )
  })
})

test("sameEntry compares what is named, not how it is written", () => {
  expect(sameEntry("2001:DB8::/32", "2001:db8:0::/32")).toBe(true)
  // A single address is its own /32, as the server counts it.
  expect(sameEntry("10.0.0.1", "10.0.0.1/32")).toBe(true)
  expect(sameEntry("::ffff:10.0.0.1", "10.0.0.1")).toBe(false)
  expect(sameEntry("10.0.0.0/8", "10.0.0.0/16")).toBe(false)
  expect(sameEntry("x", "x")).toBe(false)
})

describe("accessFor", () => {
  const office = { allow: ["10.0.0.0/8"], deny: ["10.0.0.9"], satisfy: "all" }

  test("an allow list lets in only what it names, after the denials", () => {
    expect(accessFor(office, "10.1.2.3")).toBe("allowed")
    expect(accessFor(office, "10.0.0.9")).toBe("refused")
    expect(accessFor(office, "203.0.113.5")).toBe("refused")
  })

  test("a deny-only list lets in everybody else", () => {
    const list = { allow: [], deny: ["203.0.113.0/24"], satisfy: "all" }
    expect(accessFor(list, "203.0.113.5")).toBe("refused")
    expect(accessFor(list, "198.51.100.1")).toBe("allowed")
  })

  test("a password is asked for on top of an address, or instead of one", () => {
    const both = { ...office, authFile: "staging", satisfy: "all" }
    expect(accessFor(both, "10.1.2.3")).toBe("password")
    expect(accessFor(both, "203.0.113.5")).toBe("refused")
    const either = { ...office, authFile: "staging", satisfy: "any" }
    expect(accessFor(either, "10.1.2.3")).toBe("allowed")
    expect(accessFor(either, "203.0.113.5")).toBe("password")
    expect(accessFor(either, "10.0.0.9")).toBe("password")
    expect(accessFor({ allow: [], deny: [], authFile: "staging", satisfy: "all" }, "1.2.3.4")).toBe(
      "password",
    )
  })

  test("each family is checked against its own rules and the fence", () => {
    const v6 = { allow: ["2001:db8::/32"], deny: [], satisfy: "all" }
    expect(accessFor(v6, "2001:db8::5")).toBe("allowed")
    expect(accessFor(v6, "10.0.0.1")).toBe("refused")
    expect(accessFor(office, "2001:db8::5")).toBe("refused")
    // Denials of one family let the other through.
    expect(accessFor({ allow: [], deny: ["10.0.0.0/8"], satisfy: "all" }, "2001:db8::5")).toBe(
      "allowed",
    )
  })

  test("an IPv4 address carried in IPv6 is checked as IPv4", () => {
    expect(accessFor(office, "::ffff:10.1.2.3")).toBe("allowed")
    expect(accessFor(office, "::ffff:10.0.0.9")).toBe("refused")
    // With no IPv4 rule at all nginx reads it as IPv6.
    expect(
      accessFor({ allow: [], deny: ["::ffff:10.0.0.0/104"], satisfy: "all" }, "::ffff:10.1.1.1"),
    ).toBe("refused")
  })

  test("an address or an entry it cannot read says so", () => {
    expect(accessFor(office, "")).toBe("unknown")
    expect(accessFor(office, "unix:")).toBe("unknown")
    expect(accessFor({ allow: ["10.0.0.5/8"], deny: [], satisfy: "all" }, "10.0.0.5")).toBe(
      "unknown",
    )
  })
})
