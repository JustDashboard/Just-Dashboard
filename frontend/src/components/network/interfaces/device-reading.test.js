import { expect, test } from "bun:test"
import {
  addressProvenance,
  describeVlans,
  errorRows,
  lifetimeWords,
  parseRemotes,
  sameVlans,
  vlanFields,
  vlanPolicy,
} from "./device-reading"

test("an address says where it came from and how long it lives", () => {
  expect(addressProvenance({ origin: "dhcp", validSeconds: 83020 })).toBe(
    "from DHCP, valid 23 h 3 min",
  )
  expect(addressProvenance({ origin: "static" })).toBe("static")
  expect(addressProvenance({ origin: "slaac", validSeconds: 86400 * 2 })).toBe(
    "from router advertisements, valid 2 d",
  )
  expect(lifetimeWords(undefined)).toBe("forever")
  expect(lifetimeWords(42)).toBe("42 s")
})

test("an access or trunk policy becomes the memberships the route takes", () => {
  expect(vlanPolicy("10", "")).toEqual({ vlans: [{ vid: 10, pvid: true, untagged: true }] })
  expect(vlanPolicy("", "20, 30-32").vlans.map((v) => v.vid)).toEqual([20, 30, 31, 32])
  expect(vlanPolicy("10", "20 30").vlans).toEqual([
    { vid: 10, pvid: true, untagged: true },
    { vid: 20 },
    { vid: 30 },
  ])
  expect(vlanPolicy("0", "").error).toContain("1 to 4094")
  expect(vlanPolicy("10", "10").error).toContain("twice")
  expect(vlanPolicy("", "40-30").error).toContain("range")
  expect(vlanPolicy("", "1-100").error).toContain("64")
  expect(vlanFields([{ vid: 20 }, { vid: 10, pvid: true, untagged: true }])).toEqual({
    native: "10",
    tagged: "20",
  })
})

test("memberships read back in words and compare regardless of order", () => {
  expect(describeVlans([{ vid: 10, pvid: true, untagged: true }, { vid: 20 }])).toBe(
    "native 10 · tagged 20",
  )
  expect(describeVlans([])).toBe("no VLANs")
  expect(
    sameVlans([{ vid: 20 }, { vid: 10, pvid: true }], [{ vid: 10, pvid: true }, { vid: 20 }]),
  ).toBe(true)
  expect(sameVlans([{ vid: 10, pvid: true }], [{ vid: 10 }])).toBe(false)
})

test("flood ends stay in the remote's family and skip the remote itself", () => {
  expect(parseRemotes("198.51.100.8, 198.51.100.7 198.51.100.8", "198.51.100.7")).toEqual({
    remotes: ["198.51.100.8"],
  })
  expect(parseRemotes("2001:db8::8", "198.51.100.7").error).toContain("IPv4")
  expect(parseRemotes("239.1.1.1", "198.51.100.7").error).toContain("unicast")
  expect(parseRemotes("nope", "198.51.100.7").error).toContain("not one IP address")
})

test("only nonzero error counters get a row", () => {
  expect(errorRows(undefined)).toEqual([])
  const rows = errorRows({ rxCrcErrors: 4, rxDropped: 0, carrierChanges: 2 })
  expect(rows).toEqual([
    { label: "CRC errors (received)", value: 4 },
    { label: "Carrier changes", value: 2 },
  ])
})
