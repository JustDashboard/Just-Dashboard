import { expect, test } from "bun:test"
import {
  containerHref,
  containerWindow,
  programHref,
  programId,
  tabFromParams,
  trafficHref,
} from "./address"

const query = (text) => new URLSearchParams(text)

test("the address names the view, and an unknown view is the first", () => {
  expect(tabFromParams(query(""))).toBe("bandwidth")
  expect(tabFromParams(query("tab=shaping"))).toBe("shaping")
  expect(tabFromParams(query("tab=ebpf"))).toBe("ebpf")
  expect(tabFromParams(query("tab=queues"))).toBe("bandwidth")
})

test("a link to the page keeps the window and the program filter", () => {
  expect(trafficHref()).toBe("/network/traffic")
  expect(trafficHref({ tab: "budgets" })).toBe("/network/traffic?tab=budgets")
  expect(trafficHref({ span: "24h" })).toBe("/network/traffic?window=24h")
  expect(trafficHref({ span: { from: 10, to: 20 } })).toBe("/network/traffic?from=10&to=20")
  expect(trafficHref({ tab: "ebpf", type: "cgroup_skb" })).toBe(
    "/network/traffic?tab=ebpf&type=cgroup_skb",
  )
})

test("a container's page reads the hour unless the address names a longer window", () => {
  expect(containerHref("registry")).toBe("/network/traffic/containers/registry")
  expect(containerHref("web 1", "24h")).toBe("/network/traffic/containers/web%201?window=24h")
  expect(containerWindow(query(""))).toBe("1h")
  expect(containerWindow(query("window=7d"))).toBe("7d")
  expect(containerWindow(query("window=live"))).toBe("1h")
})

test("a program's address is its id", () => {
  expect(programHref(9101)).toBe("/network/traffic/ebpf/9101")
  expect(programHref(9101, "cgroup_skb")).toBe("/network/traffic/ebpf/9101?type=cgroup_skb")
  expect(programId("9101")).toBe(9101)
  expect(programId("9101x")).toBeNull()
  expect(programId(undefined)).toBeNull()
})
