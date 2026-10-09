import { expect, test } from "bun:test"
import { readDNSDHCPView, readDNSHandoffs } from "./network-dns-dhcp"

const at = "2026-10-09T04:00:00Z"
const connection = {
  id: "dns-dhcp-fixture",
  name: "Fixture DNS",
  engine: "pihole",
  endpoint: "http://127.0.0.1:40080",
  customCA: false,
  management: false,
  hasCredential: true,
  generation: 2,
  ownership: "owned",
  createdAt: at,
  updatedAt: at,
}
const reading = (state) => ({ state, basis: "native_table", summary: "Native table." })
const view = (inventory = {}) => ({
  connection,
  state: "available",
  inventory: {
    engine: "pihole",
    nativeVersion: "v6.2.1",
    observedAt: at,
    transport: reading("loopback_http"),
    enabled: false,
    configuration: reading("configured"),
    ranges: [{ family: "ipv4", start: "192.168.0.10", end: "192.168.0.250" }],
    leases: [{ address: "192.168.0.123", hardware: "11:22:33:44:55:66", static: true }],
    leaseEvidence: reading("reported"),
    limitations: ["Read-only."],
    ...inventory,
  },
})

test("a DHCP reading is accepted only for its own connection and generation", () => {
  const read = readDNSDHCPView(view(), connection)
  expect(read.inventory.leases[0].static).toBe(true)
  expect(read.inventory.ranges[0].end).toBe("192.168.0.250")
  expect(() => readDNSDHCPView(view(), { ...connection, generation: 3 })).toThrow(
    "another connection",
  )
  expect(() => readDNSDHCPView(view({ engine: "adguard" }), connection)).toThrow()
  expect(() =>
    readDNSDHCPView(view({ leases: [{ address: "", static: false }] }), connection),
  ).toThrow()
  expect(() =>
    readDNSDHCPView(
      view({ leases: [{ address: "192.168.0.9", static: false, expires: "tomorrow" }] }),
      connection,
    ),
  ).toThrow()
  const unavailable = readDNSDHCPView(
    { connection, state: "unsupported", error: "outside the contract" },
    connection,
  )
  expect(unavailable.inventory).toBeUndefined()
})

test("handoffs keep the detected server, its origin and the connections that reach it", () => {
  const rows = readDNSHandoffs([
    {
      detected: {
        kind: "adguardhome",
        name: "AdGuard Home",
        runsAs: "container",
        answering: true,
        webPort: 3000,
      },
      engine: "adguard",
      endpoint: "http://127.0.0.1:3000",
      connections: [
        {
          id: "a1",
          name: "Home filter",
          management: false,
          ownership: "connected",
          match: "endpoint",
        },
      ],
    },
  ])
  expect(rows[0].connections[0].match).toBe("endpoint")
  expect(() =>
    readDNSHandoffs([
      { detected: { kind: "bind", name: "x" }, engine: "adguard", connections: [] },
    ]),
  ).toThrow()
  expect(() => readDNSHandoffs({})).toThrow()
})
