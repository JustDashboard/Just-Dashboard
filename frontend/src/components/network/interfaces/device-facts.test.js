import { expect, test } from "bun:test"
import {
  behindEverything,
  changeWords,
  deviceChanges,
  deviceHref,
  deviceName,
  devicePicture,
  deviceState,
  deviceViews,
  groupDevices,
  listFacts,
} from "./device-facts"

const link = (over) => ({
  name: "eth0",
  kind: "physical",
  role: "physical",
  owner: "system",
  state: "up",
  adminUp: true,
  carrier: true,
  addresses: [],
  managed: false,
  ...over,
})

const ens3 = link({
  name: "ens3",
  role: "uplink",
  speedMbps: 1000,
  addresses: [
    { cidr: "203.0.113.20/24", family: "inet", scope: "global", public: true },
    { cidr: "fe80::1/64", family: "inet6", scope: "link" },
  ],
})
const lab = link({
  name: "jd-lab",
  kind: "bridge",
  role: "bridge",
  owner: "just-dashboard",
  managed: true,
  addresses: [{ cidr: "192.168.50.1/24", family: "inet", scope: "global" }],
})
const vlan = link({
  name: "vlan30",
  kind: "vlan",
  role: "vlan",
  owner: "just-dashboard",
  managed: true,
  parent: "ens3",
  master: "jd-lab",
  vlanId: 30,
})
const veth = link({
  name: "veth1",
  kind: "veth",
  role: "container",
  owner: "docker",
  master: "docker0",
  container: "postgres",
})
const gre0 = link({ name: "gre0", kind: "gre", role: "tunnel", owner: "kernel", adminUp: false })
const all = [ens3, lab, vlan, veth, gre0]

test("a device's page is its name, and a view other than traffic is in the address", () => {
  expect(deviceHref("ens3")).toBe("/network/interfaces/ens3")
  expect(deviceHref("ens3", "traffic")).toBe("/network/interfaces/ens3")
  expect(deviceHref("br-9/x", "ports")).toBe("/network/interfaces/br-9%2Fx?tab=ports")
  expect(deviceName("br-9%2Fx")).toBe("br-9/x")
  expect(deviceName("100%")).toBe("100%")
  expect(deviceName(undefined)).toBe("")
})

test("a tunnel with no carrier to lose is up, and a card with nothing plugged in is not", () => {
  expect(deviceState({ adminUp: true, carrier: true, state: "up" }).label).toBe("Up")
  expect(deviceState({ adminUp: true, carrier: false, state: "unknown" }).up).toBe(true)
  expect(deviceState({ adminUp: true, carrier: false, state: "down" })).toEqual({
    up: false,
    tone: "warning",
    label: "No carrier",
  })
  expect(deviceState({ adminUp: false, carrier: false, state: "down" }).label).toBe("Down")
})

test("the list hides veths and the kernel's own until Everything or a search asks", () => {
  const titles = (options) => groupDevices(all, options).map((g) => g.title)
  expect(titles({ everything: false, query: "" })).toEqual([
    "Uplink",
    "Bridges",
    "VLANs and virtual devices",
  ])
  expect(titles({ everything: true, query: "" })).toContain("Containers")
  expect(titles({ everything: true, query: "" })).toContain("The kernel's own")
  expect(groupDevices(all, { everything: false, query: "postgres" })[0].members[0].name).toBe(
    "veth1",
  )
  expect(groupDevices(all, { everything: false, query: "203.0.113" })[0].members[0].name).toBe(
    "ens3",
  )
  expect(behindEverything(veth)).toBe(true)
  expect(behindEverything(ens3)).toBe(false)
})

test("the list's line counts what a person would, names the uplink and what was made here", () => {
  expect(listFacts(all)).toEqual({
    devices: 3,
    everything: 5,
    uplinks: ["ens3"],
    made: 2,
    up: 3,
    noCarrier: 0,
  })
  expect(listFacts([link({ carrier: false, state: "down" })]).noCarrier).toBe(1)
})

test("only a bridge has ports, and only an administrator its history", () => {
  expect(deviceViews(lab, true)).toEqual(["traffic", "addresses", "ports", "settings", "history"])
  expect(deviceViews(ens3, false)).toEqual(["traffic", "addresses", "settings"])
})

test("a picture puts the far side of the wire on one side and this server on the other", () => {
  const uplink = devicePicture(ens3, all)
  expect(uplink.away.map((n) => n.title)).toEqual(["The internet"])
  expect(uplink.away[0].hint).toBe("1000 Mb/s link")
  expect(uplink.here.map((n) => [n.eyebrow, n.title])).toEqual([
    ["VLAN 30", "vlan30"],
    ["Answers at", "203.0.113.20/24"],
  ])
  expect(uplink.here[1]).toMatchObject({ hint: "and 1 more", tab: "addresses" })

  const bridge = devicePicture(lab, all)
  expect(bridge.away).toMatchObject([{ eyebrow: "Port", title: "vlan30", device: "vlan30" }])
  expect(bridge.here.map((n) => n.title)).toEqual(["192.168.50.1/24"])

  const port = devicePicture(vlan, all)
  expect(port.away).toMatchObject([{ eyebrow: "Tag 30 on", title: "ens3", device: "ens3" }])
  expect(port.here).toMatchObject([{ eyebrow: "Port of", title: "jd-lab", device: "jd-lab" }])
})

test("a veth leads into its namespace, a container's into the container's", () => {
  const pair = link({
    name: "lab-h",
    kind: "veth",
    role: "virtual",
    peer: "lab-n",
    peerNamespace: "lab",
  })
  expect(devicePicture(pair, [pair]).away).toMatchObject([
    { title: "lab-n", hint: "inside lab", namespace: { kind: "named", name: "lab" } },
  ])
  expect(devicePicture(veth, all).away).toMatchObject([
    { eyebrow: "Container", title: "postgres", namespace: { kind: "container", name: "postgres" } },
  ])
  // A bridge that is not on the list is named and not linked.
  expect(devicePicture(veth, all).here[0]).toMatchObject({ title: "docker0", device: undefined })
})

test("a tunnel's far side is its remote end and every further flood end", () => {
  const vx = link({
    name: "vx42",
    kind: "vxlan",
    role: "tunnel",
    remote: "198.51.100.7",
    local: "203.0.113.20",
    port: 4789,
    remotes: ["198.51.100.8"],
  })
  expect(devicePicture(vx, [vx]).away.map((n) => [n.eyebrow, n.title, n.hint])).toEqual([
    ["Remote end", "198.51.100.7:4789", "from 203.0.113.20"],
    ["Also floods to", "198.51.100.8", undefined],
  ])
  const group = link({ name: "vx9", kind: "vxlan", role: "tunnel", remote: "239.1.1.1" })
  expect(devicePicture(group, [group]).away[0].eyebrow).toBe("Multicast group")
  const wg = link({ name: "wg0", kind: "wireguard", role: "tunnel", owner: "wireguard" })
  expect(devicePicture(wg, [wg]).away[0]).toMatchObject({ href: "/network/vpn" })
  const dummy = link({ name: "svc0", kind: "dummy", role: "virtual" })
  expect(devicePicture(dummy, [dummy])).toEqual({ away: [], here: [] })
})

test("a change reads as what was done and what it was changed to", () => {
  expect(changeWords({ action: "network.link.mtu", detail: '{"mtu":1400}' })).toEqual({
    title: "MTU changed",
    value: "1400",
  })
  expect(changeWords({ action: "network.link.master", detail: '{"master":""}' }).value).toBe(
    "no bridge",
  )
  expect(changeWords({ action: "network.address.add", detail: '{"cidr":"10.0.0.1/24"}' })).toEqual({
    title: "Address added",
    value: "10.0.0.1/24",
  })
  expect(changeWords({ action: "network.link.down", detail: "" })).toEqual({ title: "Set down" })
  expect(changeWords({ action: "network.link.remotes", detail: '{"remotes":[]}' }).value).toBe(
    "none",
  )
  // A change this page has no word for is still said, and a broken detail is no value.
  expect(changeWords({ action: "network.link.rename", detail: "{" })).toEqual({
    title: "link.rename",
  })
})

test("a device's history is its own entries of the network families, newest first", () => {
  const entries = [
    { id: 1, action: "network.link.create", target: "jd-lab", ts: "2026-11-10T10:00:00Z" },
    { id: 2, action: "network.address.add", target: "jd-lab", ts: "2026-11-12T10:00:00Z" },
    { id: 3, action: "network.link.up", target: "vlan30", ts: "2026-11-12T11:00:00Z" },
    { id: 4, action: "network.namespace.create", target: "jd-lab", ts: "2026-11-12T12:00:00Z" },
  ]
  expect(deviceChanges(entries, "jd-lab").map((e) => e.id)).toEqual([2, 1])
  expect(deviceChanges(undefined, "jd-lab")).toEqual([])
  // A fixture that answers an unknown read with a list is no trail.
  expect(deviceChanges({}, "jd-lab")).toEqual([])
})
