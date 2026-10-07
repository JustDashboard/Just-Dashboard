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
