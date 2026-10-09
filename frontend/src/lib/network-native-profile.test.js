import { expect, test } from "bun:test"
import { readNativeProfile } from "./network-native-profile"

const sample = () => {
  const family = {
    method: "auto",
    addresses: [],
    dns: [],
    domains: [],
    ignoreAutoDns: false,
    ignoreAutoRoutes: false,
    routes: [],
  }
  return {
    checkedAt: "2026-10-08T12:00:00Z",
    device: "ens3",
    kind: "physical",
    owner: "NetworkManager",
    renderer: "NetworkManager",
    profile: "fixture-uplink",
    generation: "opaque-native-generation",
    editable: true,
    contract: { members: [] },
    intent: { ipv4: structuredClone(family), ipv6: structuredClone(family) },
    configured: { status: "matching" },
    runtime: { status: "matching" },
    boot: { status: "matching", reason: "Native owner enabled; no reboot measured." },
  }
}

test("native profile reads retain opaque generation and separate boot evidence", () => {
  const view = readNativeProfile(sample(), "ens3")
  expect(view.generation).toBe("opaque-native-generation")
  expect(view.boot.reason).toContain("no reboot measured")
})

test("verified standalone veth ownership remains editable through the native wire contract", () => {
  const value = sample()
  value.kind = "veth"
  expect(readNativeProfile(value, "ens3").editable).toBe(true)
  value.runtime.status = "unknown"
  expect(() => readNativeProfile(value, "ens3")).toThrow("lifecycle evidence")
})

test("a native owner refusal can be read without an editable intent", () => {
  const value = sample()
  value.editable = false
  value.owner = "unknown"
  delete value.renderer
  delete value.profile
  delete value.generation
  delete value.intent
  for (const field of ["configured", "runtime", "boot"]) value[field] = { status: "unknown" }
  expect(readNativeProfile(value, "ens3").editable).toBe(false)
})

test("unsupported native renderer intent remains readable when editing is refused", () => {
  const value = sample()
  value.editable = false
  value.intent.ipv6.method = "slaac"
  value.configured.status = "drift"
  expect(readNativeProfile(value, "ens3").editable).toBe(false)
})

for (const [name, mutate] of [
  ["a null route", (v) => (v.intent.ipv4.routes = [null])],
  ["a string route", (v) => (v.intent.ipv4.routes = ["default"])],
  ["a non-string address", (v) => (v.intent.ipv4.addresses = [42])],
  ["a non-string DNS server", (v) => (v.intent.ipv4.dns = [{}])],
  ["a null domain", (v) => (v.intent.ipv6.domains = [null])],
  ["a missing address method", (v) => delete v.intent.ipv4.method],
  ["an unsupported method", (v) => (v.intent.ipv4.method = "slaac")],
  ["an empty manual family", (v) => (v.intent.ipv4.method = "manual")],
  [
    "a populated disabled family",
    (v) => {
      v.intent.ipv4.method = "disabled"
      v.intent.ipv4.dns = ["192.0.2.53"]
    },
  ],
  ["NetworkManager SLAAC", (v) => (v.intent.ipv6.method = "slaac")],
  ["a non-boolean DNS preference", (v) => (v.intent.ipv4.ignoreAutoDns = "false")],
  ["a missing route preference", (v) => delete v.intent.ipv6.ignoreAutoRoutes],
  ["a missing editable intent", (v) => delete v.intent],
  ["an empty editable generation", (v) => (v.generation = "")],
  ["an unknown editable owner", (v) => (v.owner = "unknown")],
  ["a renderer/owner mismatch", (v) => (v.renderer = "networkd")],
  ["an invalid clock", (v) => (v.checkedAt = "not-a-time")],
  ["a non-string owner", (v) => (v.owner = {})],
  ["a non-string profile", (v) => (v.profile = {})],
  ["a non-string lifecycle reason", (v) => (v.runtime.reason = {})],
  ["a missing lifecycle state", (v) => delete v.boot.status],
  ["an editable unknown boot", (v) => (v.boot.status = "unknown")],
  ["a non-string member", (v) => (v.contract.members = [{}])],
  ["an invalid VRF table", (v) => (v.contract.vrfTable = "1001")],
  ["a VRF without its table", (v) => (v.kind = "vrf")],
  ["too many DNS servers", (v) => (v.intent.ipv4.dns = Array(9).fill("192.0.2.53"))],
  [
    "too many routes",
    (v) =>
      (v.intent.ipv4.routes = Array(33).fill({ destination: "0.0.0.0/0", metric: 0, table: 254 })),
  ],
]) {
  test(`native reads refuse ${name} before the editor can use it`, () => {
    const value = sample()
    mutate(value)
    expect(() => readNativeProfile(value, "ens3")).toThrow()
  })
}

for (const [field, value] of [
  ["destination", null],
  ["gateway", {}],
  ["metric", "0"],
  ["metric", NaN],
  ["metric", 0.5],
  ["metric", 1_000_001],
  ["table", "254"],
  ["table", 0],
  ["table", 52],
  ["table", 253],
  ["table", 255],
  ["table", 2_147_483_648],
]) {
  test(`native reads refuse route ${field}=${String(value)} as a failed read`, () => {
    const profile = sample()
    profile.intent.ipv4.routes = [
      { destination: "0.0.0.0/0", metric: 0, table: 254, [field]: value },
    ]
    expect(() => readNativeProfile(profile, "ens3")).toThrow()
  })
}

test("an observed native VRF and signed native default metric remain supported", () => {
  const value = sample()
  value.kind = "vrf"
  value.contract = { members: ["eth1"], vrfTable: 1001 }
  value.intent.ipv4.routes = [{ destination: "0.0.0.0/0", metric: -1, table: 1001 }]
  expect(readNativeProfile(value, "ens3").intent.ipv4.routes[0].table).toBe(1001)
})
