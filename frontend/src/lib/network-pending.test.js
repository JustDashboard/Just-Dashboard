import { afterEach, expect, test } from "bun:test"
import {
  networkPendingHeaders,
  setNetworkPendingApply,
  supportsPendingNetworkMutation,
} from "./network-pending"

afterEach(() => setNetworkPendingApply(false))

test("pending applies enroll only managed netx mutations", () => {
  for (const path of [
    "/network/links/eth0/mtu",
    "/network/routing/routes",
    "/network/routing/rules/3",
    "/network/forwarding/6/off",
    "/network/shaping/eth0",
    "/network/gateway/nat",
    "/network/protection/settings",
  ]) {
    expect(supportsPendingNetworkMutation(path, "POST")).toBe(true)
    expect(supportsPendingNetworkMutation(path, "GET")).toBe(false)
  }
  for (const path of [
    "/network/vpn/tailscale",
    "/network/dns",
    "/network/firewall/rules",
    "/network/namespaces",
    "/network/changes/one/confirm",
    "/network/gateway-foreign",
  ]) {
    expect(supportsPendingNetworkMutation(path, "POST")).toBe(false)
  }
})

test("the browser must opt in after recovery availability is known", () => {
  expect(networkPendingHeaders("/network/shaping/eth0", "POST")).toEqual({})
  setNetworkPendingApply(true)
  expect(networkPendingHeaders("/network/shaping/eth0", "POST")).toEqual({
    "X-JD-Network-Apply": "pending",
  })
  expect(networkPendingHeaders("/network/shaping/eth0", "GET")).toEqual({})
  expect(networkPendingHeaders("/network/vpn/tailscale", "POST")).toEqual({})
})
