import { describe, expect, test } from "bun:test"
import {
  addressPlan,
  answersTo,
  endpointsByContainer,
  hostCapacity,
  networkChanges,
  networkHue,
  networkOrder,
  networkOwner,
  parseV4,
} from "./networks"

const net = (over) => ({
  id: over.name,
  driver: "bridge",
  scope: "local",
  internal: false,
  attachable: false,
  ipv6: false,
  created: "",
  labels: {},
  subnets: [],
  containers: 0,
  usedBy: [],
  ...over,
})

describe("networkOwner", () => {
  test("names Docker's own three, a compose project, this dashboard and the rest", () => {
    expect(networkOwner(net({ name: "bridge" })).kind).toBe("docker")
    expect(
      networkOwner(net({ name: "shop_default", labels: { "com.docker.compose.project": "shop" } })),
    ).toEqual({ kind: "compose", label: "compose · shop", project: "shop" })
    expect(
      networkOwner(
        net({
          name: "jd-e6-db",
          labels: {
            "io.just-dashboard.managed": "true",
            "io.just-dashboard.database-network": "X",
          },
        }),
      ),
    ).toEqual({ kind: "dashboard", label: "a deployment's database network" })
    expect(networkOwner(net({ name: "proxy" })).kind).toBe("standalone")
  })
})

test("Docker's own take slate and every other network keeps one lane hue", () => {
  expect(networkHue("bridge")).toBe("var(--tag-slate)")
  expect(networkHue("proxy")).toBe(networkHue("proxy"))
  expect(networkHue("proxy")).not.toMatch(/red|amber/)
})

test("networks in use come first, then the unused, then Docker's own", () => {
  const list = [
    net({ name: "none" }),
    net({ name: "zeta", usedBy: ["a"] }),
    net({ name: "old" }),
    net({ name: "alpha", usedBy: ["b"] }),
    net({ name: "bridge", usedBy: ["c"] }),
  ]
  expect(list.sort(networkOrder).map((n) => n.name)).toEqual(["alpha", "zeta", "old", "bridge", "none"])
})

describe("parseV4", () => {
  test("reads a network as the range it spans, aligned to its prefix", () => {
    expect(parseV4("172.18.0.0/16")).toEqual({ start: 2886860800, end: 2886926335, bits: 16 })
    expect(parseV4("10.0.5.9/24")?.start).toBe(parseV4("10.0.5.0/24")?.start)
    expect(parseV4("fd00::/64")).toBeNull()
    expect(parseV4("300.1.1.1/8")).toBeNull()
  })

  test("a subnet's capacity leaves out the network, the broadcast and the gateway", () => {
    expect(hostCapacity("172.18.0.0/16")).toBe(65_533)
    expect(hostCapacity("10.0.5.0/24")).toBe(253)
    expect(hostCapacity("fd00::/64")).toBe(0)
  })
})

describe("addressPlan", () => {
  const taken = ["172.17.0.0/16", "172.18.0.0/16", "172.19.0.0/16", "172.20.0.0/16"]

  test("falls back to Docker's built-in pools: thirty-one networks", () => {
    const plan = addressPlan(
      { DefaultAddressPools: null },
      taken.map((cidr, i) => net({ name: `n${i}`, subnets: [cidr] })),
    )
    expect(plan.builtin).toBe(true)
    expect(plan.total).toBe(31)
    expect(plan.used).toBe(4)
    expect(plan.blocks).toHaveLength(31)
    expect(plan.blocks[0]).toEqual({ cidr: "172.17.0.0/16", network: "n0" })
    expect(plan.blocks[4]).toEqual({ cidr: "172.21.0.0/16", network: undefined })
    expect(plan.blocks[15].cidr).toBe("192.168.0.0/20")
    expect(plan.firstFree).toBe("172.21.0.0/16")
    expect(plan.pressure).toBe("ok")
  })

  test("a subnet picked inside a pool holds its block, and one outside every pool is listed apart", () => {
    const plan = addressPlan(undefined, [
      net({ name: "lan", subnets: ["192.168.1.0/24"] }),
      net({ name: "vpn", subnets: ["10.8.0.0/24", "fd00::/64"] }),
    ])
    expect(plan.blocks[15]).toEqual({ cidr: "192.168.0.0/20", network: "lan" })
    expect(plan.outside).toEqual([{ network: "vpn", cidr: "10.8.0.0/24" }])
  })

  test("reads a configured pool, drawing only its start when it is vast", () => {
    const plan = addressPlan({ DefaultAddressPools: [{ Base: "10.0.0.0/8", Size: 24 }] }, [
      net({ name: "a", subnets: ["10.0.0.0/24"] }),
      net({ name: "b", subnets: ["10.0.1.0/24"] }),
      net({ name: "c", subnets: ["10.0.3.0/24"] }),
    ])
    expect(plan.builtin).toBe(false)
    expect(plan.total).toBe(65_536)
    expect(plan.used).toBe(3)
    expect(plan.blocks).toHaveLength(32)
    expect(plan.firstFree).toBe("10.0.2.0/24")
  })

  test("says when the pool is nearly or wholly spent", () => {
    const pool = { DefaultAddressPools: [{ Base: "10.0.0.0/22", Size: 24 }] }
    const some = (n) =>
      Array.from({ length: n }, (_, i) => net({ name: `n${i}`, subnets: [`10.0.${i}.0/24`] }))
    expect(addressPlan(pool, some(3)).pressure).toBe("ok")
    expect(addressPlan(pool, some(4)).pressure).toBe("full")
    expect(addressPlan(pool, some(4)).firstFree).toBeUndefined()
    expect(
      addressPlan({ DefaultAddressPools: [{ Base: "10.0.0.0/21", Size: 24 }] }, some(7)).pressure,
    ).toBe("warning")
  })
})

describe("answersTo", () => {
  const container = { name: "shop-db-1", composeService: "db" }

  test("a compose service answers to its service and its own name", () => {
    expect(answersTo(container, net({ name: "shop_backend" }))).toEqual({
      names: ["db", "shop-db-1"],
    })
    expect(answersTo({ name: "caddy" }, net({ name: "proxy" }))).toEqual({ names: ["caddy"] })
  })

  test("the default bridge and the host network answer to no name, and say why", () => {
    expect(answersTo(container, net({ name: "bridge" }))).toEqual({
      names: [],
      why: "no names on the default bridge",
    })
    expect(answersTo(container, net({ name: "host", driver: "host" })).names).toEqual([])
  })
})

test("each container's networks come from the listing's endpoints, in the page's order", () => {
  const byContainer = endpointsByContainer([
    net({ name: "zeta", usedBy: ["web"], endpoints: [{ container: "w", name: "web", ipv4: "172.19.0.2/16" }] }),
    net({ name: "alpha", usedBy: ["web"], endpoints: [{ container: "w", name: "web", ipv4: "172.18.0.2/16" }] }),
  ])
  expect(byContainer.get("w")?.map((e) => e.network.name)).toEqual(["alpha", "zeta"])
})

describe("networkChanges", () => {
  const ev = (time, action, name, container) => ({
    time,
    type: "network",
    action,
    name,
    id: `${name}-id`,
    container,
    message: "",
    level: "info",
  })

  test("names who joined and left from the listing, newest first, once each", () => {
    const changes = networkChanges(
      [
        ev("2026-10-08T10:00:00Z", "create", "proxy"),
        ev("2026-10-08T10:05:00Z", "connect", "proxy", "c1"),
        ev("2026-10-08T10:05:00Z", "connect", "proxy", "c1"),
        ev("2026-10-08T10:09:00Z", "disconnect", "proxy", "gone"),
        { ...ev("2026-10-08T10:10:00Z", "start", "web"), type: "container" },
      ],
      [{ id: "c1", name: "caddy" }],
    )
    expect(changes.map((c) => [c.action, c.network, c.container])).toEqual([
      ["left", "proxy", undefined],
      ["joined", "proxy", "caddy"],
      ["created", "proxy", undefined],
    ])
  })
})
