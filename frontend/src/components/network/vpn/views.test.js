import { expect, test } from "bun:test"
import { runsHeadscale, vpnHref, vpnReading, vpnViewOf, vpnViews } from "./views"

test("Headscale has a view only where this server runs one", () => {
  expect(vpnViews(false)).toEqual(["wireguard", "tailscale", "log"])
  expect(vpnViews(true)).toEqual(["wireguard", "tailscale", "headscale", "log"])
  expect(runsHeadscale({ headscale: { installed: false, container: "" } })).toBe(false)
  expect(runsHeadscale({ headscale: { installed: true, container: "" } })).toBe(true)
  expect(runsHeadscale({ headscale: { installed: false, container: "headscale" } })).toBe(true)
})

test("the address names the view, and a view this server has not got opens WireGuard", () => {
  expect(vpnViewOf("tailscale", false)).toBe("tailscale")
  expect(vpnViewOf("log", false)).toBe("log")
  expect(vpnViewOf("headscale", true)).toBe("headscale")
  expect(vpnViewOf("headscale", false)).toBe("wireguard")
  expect(vpnViewOf("nonsense", true)).toBe("wireguard")
  expect(vpnViewOf(null, true)).toBe("wireguard")
})

test("a planned range from Address planning opens WireGuard whatever the address says", () => {
  expect(vpnViewOf("tailscale", false, true)).toBe("wireguard")
  expect(vpnViewOf(null, false, true)).toBe("wireguard")
})

test("the first view's address carries no tab", () => {
  expect(vpnHref("wireguard")).toBe("/network/vpn")
  expect(vpnHref("log")).toBe("/network/vpn?tab=log")
})

test("the page's state says how many tunnels are up and whether the server is on its tailnet", () => {
  const read = (tunnels, tailscale) =>
    vpnReading({ wireguard: { interfaces: tunnels.map((up) => ({ up })) }, tailscale })
  expect(read([true], { installed: true, running: true })).toEqual({
    tone: "running",
    label: "1 tunnel up, on the tailnet",
  })
  expect(read([true, true], { installed: false, running: false })).toEqual({
    tone: "running",
    label: "2 tunnels up",
  })
  expect(read([true, false], { installed: true, running: true })).toEqual({
    tone: "warning",
    label: "1 of 2 tunnels up, on the tailnet",
  })
  expect(read([], { installed: true, running: false })).toEqual({
    tone: "warning",
    label: "off the tailnet",
  })
  expect(read([], { installed: false, running: false })).toEqual({
    tone: "stopped",
    label: "No VPN yet",
  })
})
