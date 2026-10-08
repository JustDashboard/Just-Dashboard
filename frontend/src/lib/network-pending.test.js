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
    "/network/drift/repairs",
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
    "/network/gateway/admission/repair",
    "/network/drift/repairs/foreign",
    "/network/drift",
  ]) {
    expect(supportsPendingNetworkMutation(path, "POST")).toBe(false)
  }
})

test("selected drift repair enrollment is exact and POST only", () => {
  expect(supportsPendingNetworkMutation("/network/drift/repairs/?review=one", "POST")).toBe(true)
  expect(supportsPendingNetworkMutation("/network/drift/repairs", "PUT")).toBe(false)
  expect(supportsPendingNetworkMutation("/network/drift/repairs", "DELETE")).toBe(false)
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
