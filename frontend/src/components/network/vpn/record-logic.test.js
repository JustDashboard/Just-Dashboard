import { expect, test } from "bun:test"
import {
  editWithdraws,
  eventLabel,
  handshakeReading,
  keyExpiry,
  killSwitchOffered,
  mtuProblem,
  networkProblem,
  parseBudget,
  peerEdit,
  quotaPercent,
  resolversProblem,
  sharedOf,
  siteNetworks,
} from "./record-logic"

const site = {
  id: 2,
  name: "Office",
  kind: "site",
  address: "10.8.0.3",
  allowedIps: ["10.8.0.3/32", "192.168.77.0/24", "192.168.78.0/24"],
  endpoint: "198.51.100.50:51820",
  keepalive: 25,
}
const device = {
  id: 3,
  name: "Tablet",
  kind: "device",
  address: "10.8.0.4",
  allowedIps: ["10.8.0.4/32"],
  endpoint: "",
  keepalive: 0,
  clientRoutes: ["10.8.0.0/24", "172.17.0.0/16"],
}
const tunnel = ["10.8.0.0/24"]

test("a handshake reads as the server classified it, stale apart from quiet", () => {
  expect(handshakeReading({ handshakeState: "stale" })).toEqual({
    label: "Handshake stale",
    tone: "warning",
  })
  expect(handshakeReading({ handshakeState: "idle" }).label).toBe("Quiet")
  expect(handshakeReading({ online: false, latestHandshake: 0 }).label).toBe("Never connected")
  expect(handshakeReading({ online: true }).tone).toBe("running")
})

test("budgets are typed in binary units and bounded like the server's", () => {
  expect(parseBudget("50 GiB")).toBe(50 * 2 ** 30)
  expect(parseBudget("50 GB")).toBe(50 * 2 ** 30)
  expect(parseBudget("1.5gib")).toBe(1.5 * 2 ** 30)
  expect(parseBudget("512 KiB")).toBeUndefined()
  expect(parseBudget("2 PB")).toBeUndefined()
  expect(parseBudget("ten GB")).toBeUndefined()
  expect(parseBudget("")).toBeUndefined()
  expect(quotaPercent({ usedBytes: 3, limitBytes: 4 })).toBe(75)
  expect(quotaPercent({ usedBytes: 9, limitBytes: 4 })).toBe(100)
})

test("networks are checked before they are sent", () => {
  expect(networkProblem("192.168.1.0/24, fd00:1::/64")).toBeUndefined()
  expect(networkProblem("192.168.1.0")).toContain("not a network")
  expect(networkProblem("192.168.1.0/33")).toContain("not a network")
  expect(networkProblem("0.0.0.0/0")).toContain("full tunnel")
})

test("an edit sends only what changed and says when it withdraws", () => {
  const unchanged = {
    name: "Office",
    keepalive: "25",
    endpoint: "198.51.100.50:51820",
    remote: "192.168.77.0/24, 192.168.78.0/24",
    fullTunnel: false,
    share: "",
  }
  expect(siteNetworks(site)).toEqual(["192.168.77.0/24", "192.168.78.0/24"])
  expect(peerEdit(site, unchanged, tunnel)).toEqual({})
  const narrowed = peerEdit(site, { ...unchanged, remote: "192.168.78.0/24" }, tunnel)
  expect(narrowed).toEqual({ remoteNetworks: ["192.168.78.0/24"] })
  expect(editWithdraws(site, narrowed)).toBe(true)
  const widened = peerEdit(
    site,
    { ...unchanged, remote: `${unchanged.remote} 10.20.0.0/16` },
    tunnel,
  )
  expect(editWithdraws(site, widened)).toBe(false)
  expect(editWithdraws(site, peerEdit(site, { ...unchanged, endpoint: "" }, tunnel))).toBe(true)
  expect(peerEdit(site, { ...unchanged, name: "Head office" }, tunnel)).toEqual({
    name: "Head office",
  })
})

test("a device's routes edit from what was recorded, and unknown routes are sent whole", () => {
  expect(sharedOf(device, tunnel)).toEqual(["172.17.0.0/16"])
  const draft = {
    name: "Tablet",
    keepalive: "0",
    endpoint: "",
    remote: "",
    fullTunnel: true,
    share: "172.17.0.0/16",
  }
  expect(peerEdit(device, draft, tunnel)).toEqual({ fullTunnel: true })
  expect(
    peerEdit({ ...device, clientRoutes: undefined }, { ...draft, fullTunnel: false }, tunnel),
  ).toEqual({
    fullTunnel: false,
    shareNetworks: ["172.17.0.0/16"],
  })
  expect(editWithdraws(device, { fullTunnel: false })).toBe(false)
})

test("the kill-switch file is offered for full or unknown device routes only", () => {
  expect(killSwitchOffered({ ...device, clientRoutes: ["0.0.0.0/0", "::/0"] })).toBe(true)
  expect(killSwitchOffered({ ...device, clientRoutes: undefined })).toBe(true)
  expect(killSwitchOffered(device)).toBe(false)
  expect(killSwitchOffered(site)).toBe(false)
  expect(killSwitchOffered({ ...device, id: 0 })).toBe(false)
})

test("lifecycle kinds read as words and unknown kinds keep their name", () => {
  expect(eventLabel("restored")).toBe("Restored from the archive")
  expect(eventLabel("something_new")).toBe("something new")
})

test("advanced setup values are bounded like the server's", () => {
  expect(mtuProblem("")).toBeUndefined()
  expect(mtuProblem(" 1420 ")).toBeUndefined()
  for (const bad of ["1279", "9001", "14.2", "abc", "01420"]) expect(mtuProblem(bad)).toBeDefined()
  expect(resolversProblem("10.0.4.53, fd00::53")).toBeUndefined()
  expect(resolversProblem("dns.example")).toContain("not an IPv4 or IPv6 address")
  expect(resolversProblem(Array(9).fill("1.1.1.1").join(","))).toContain("eight")
})

test("a key's expiry reads as past or coming against the clock it is given", () => {
  const now = Date.UTC(2026, 9, 9)
  expect(keyExpiry(now / 1000 - 3600, now).expired).toBe(true)
  expect(keyExpiry(now / 1000 + 86400 * 5, now)).toMatchObject({ expired: false })
  expect(keyExpiry(now / 1000 + 86400 * 5, now).label).toStartWith("key expires")
})
