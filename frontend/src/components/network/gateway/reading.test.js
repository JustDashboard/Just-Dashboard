import { expect, test } from "bun:test"
import {
  counters,
  forwardRequest,
  forwardTitle,
  grown,
  hostPort,
  ownerOf,
  sourcesWord,
  splitList,
  targetProduct,
} from "./reading"

const forward = (fields) => ({
  id: 1,
  name: "Website",
  protocol: "tcp",
  interface: "",
  ports: "8080",
  target: "10.0.4.5",
  targetPort: "80",
  sources: [],
  sourceNat: "auto",
  masquerade: false,
  enabled: true,
  packets: 0,
  bytes: 0,
  createdAt: "",
  ...fields,
})

test("a forward is named by its public port and its target", () => {
  expect(forwardTitle(forward({}))).toBe(":8080 → 10.0.4.5:80")
  expect(forwardTitle(forward({ targetPort: "" }))).toBe(":8080 → 10.0.4.5:8080")
  expect(hostPort("2001:db8::5", "443")).toBe("[2001:db8::5]:443")
})

test("the product that answers is read from the target's port, not the public one", () => {
  expect(targetProduct({ ports: "8080", targetPort: "5432" })).toBe("postgresql")
  expect(targetProduct({ ports: "25565", targetPort: "" })).toBe("minecraft-java")
  expect(targetProduct({ ports: "8000-8010", targetPort: "" })).toBeUndefined()
})

test("saving a forward unchanged sends every field, and the switch only when it moved", () => {
  const f = forward({ sources: ["203.0.113.0/24"] })
  expect(forwardRequest(f)).toEqual({
    name: "Website",
    protocol: "tcp",
    interface: "",
    ports: "8080",
    target: "10.0.4.5",
    targetPort: "80",
    sourceNat: "auto",
    sources: ["203.0.113.0/24"],
  })
  expect(forwardRequest(f, false).enabled).toBe(false)
})

test("sources are read from commas and spaces alike", () => {
  expect(splitList("203.0.113.0/24, 198.51.100.7  192.0.2.1,")).toEqual([
    "203.0.113.0/24",
    "198.51.100.7",
    "192.0.2.1",
  ])
  expect(sourcesWord([])).toBe("anyone")
  expect(sourcesWord(["a", "b", "c", "d"])).toBe("a, b +2")
})

test("an owner's word names the product and the device", () => {
  expect(ownerOf("wireguard:wg0")).toEqual({
    product: "wireguard",
    name: "WireGuard",
    device: "wg0",
  })
})

test("only a counter that went up is a wire that moves", () => {
  const before = counters([forward({ id: 1, packets: 10 }), forward({ id: 2, packets: 50 })], [])
  const after = counters(
    [
      forward({ id: 1, packets: 10 }),
      forward({ id: 2, packets: 90 }),
      forward({ id: 3, packets: 5 }),
    ],
    [],
  )
  // 1 is idle, 2 grew, 3 is new and has no past to grow from.
  expect([...grown(before, after)]).toEqual(["forward:2"])
  // A counter that fell was reset by the table being loaded again.
  expect(grown({ "forward:1": 100 }, { "forward:1": 3 }).size).toBe(0)
})

test("an entry's readiness reads installed apart from reachable", async () => {
  const { readinessWord, reachabilityWord } = await import("./reading")
  const base = {
    policy: "installed",
    rules: 1,
    expected: 1,
    forwarding: true,
    admission: "present",
    ready: true,
    reachability: "unverified",
    reason: "",
  }
  expect(readinessWord(base).label).toBe("Installed")
  expect(reachabilityWord(base)).toBe("reachability unmeasured")
  expect(readinessWord({ ...base, reachability: "verified" }).tone).toBe("success")
  expect(readinessWord({ ...base, reachability: "failed" }).tone).toBe("danger")
  expect(readinessWord({ ...base, ready: false, admission: "absent" }).label).toBe(
    "Admission missing",
  )
  expect(readinessWord({ ...base, forwarding: false, ready: false }).label).toBe("Forwarding off")
  expect(readinessWord({ ...base, policy: "partial", ready: false }).label).toBe("Partly installed")
  expect(readinessWord({ ...base, policy: "disabled" }).label).toBe("Switched off")
  expect(reachabilityWord({ ...base, policy: "disabled" })).toBeUndefined()
  expect(readinessWord(undefined).label).toBe("Unknown")
})

test("a NAT entry says how it goes out, and saves its mode unchanged", async () => {
  const { natWord, natRequest, natMode } = await import("./reading")
  const nat = (fields) => ({
    id: 1,
    name: "lab",
    source: "10.0.0.0/24",
    interface: "eth0",
    toAddress: "",
    owner: "",
    enabled: true,
    packets: 0,
    bytes: 0,
    createdAt: "",
    ...fields,
  })
  expect(natWord(nat({}))).toBe("masqueraded")
  expect(natWord(nat({ toAddress: "203.0.113.5" }))).toBe("as 203.0.113.5")
  expect(natWord(nat({ destinations: ["192.0.2.0/24"] }))).toBe(
    "masqueraded · only to 192.0.2.0/24",
  )
  expect(natWord(nat({ destinations: ["192.0.2.0/24", "198.51.100.0/24"] }))).toBe(
    "masqueraded · only to 2 networks",
  )
  expect(natWord(nat({ mode: "one-to-one", translated: "203.0.113.25/32" }))).toBe(
    "one-to-one with 203.0.113.25/32",
  )
  expect(natWord(nat({ mode: "nptv6", translated: "2001:db8:3::/48" }))).toBe(
    "IPv6 prefix ↔ 2001:db8:3::/48",
  )
  expect(natMode(nat({ toAddress: "203.0.113.5" }))).toBe("snat")
  expect(natRequest(nat({ mode: "one-to-one", translated: "203.0.113.25/32" }), false)).toEqual({
    name: "lab",
    source: "10.0.0.0/24",
    interface: "eth0",
    toAddress: "",
    mode: "one-to-one",
    translated: "203.0.113.25/32",
    destinations: [],
    enabled: false,
  })
})

test("totals, checks and evidence read as sentences", async () => {
  const { totalWord, checkWord, externalWord, impactTone, layerTone, flowWord } =
    await import("./reading")
  expect(
    totalWord({ packets: 1200, bytes: 0, since: "2026-10-03", resets: 2 }, () => "3 Oct"),
  ).toBe("1.2K packets since 3 Oct, across 2 reloads")
  expect(totalWord({ packets: 5, bytes: 0, since: null, resets: 0 }, () => "")).toBe("5 packets")
  expect(totalWord(undefined, () => "")).toBeUndefined()
  expect(checkWord({ status: "refused", target: "10.0.0.5:80" })).toBe(
    "10.0.0.5:80 refused the connection",
  )
  expect(
    externalWord({ status: "connected", vantage: "A", address: "2001:db8::5", port: 443 }),
  ).toBe("A connected to [2001:db8::5]:443")
  expect(impactTone({ severity: "refused" })).toBe("danger")
  expect(layerTone("restricted")).toBe("default")
  expect(flowWord({ verdict: "unknown" })).toBe("A checked layer may drop it")
})
