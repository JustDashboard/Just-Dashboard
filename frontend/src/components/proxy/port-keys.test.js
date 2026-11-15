import { describe, expect, test } from "bun:test"
import {
  findPort,
  foldDualStack,
  keyHref,
  parsePortKey,
  parseSocketParam,
  portHref,
  portKey,
  reachLane,
  reachPicture,
} from "./ports"

const listener = (overrides) => ({
  protocol: "tcp",
  family: "ipv4",
  address: "0.0.0.0",
  port: 443,
  pid: 812,
  process: "nginx",
  scope: "all",
  reach: "all",
  network: "all",
  exposed: true,
  ...overrides,
})

const sshd = listener({ port: 22, process: "sshd" })
const sshd6 = { ...sshd, family: "ipv6", address: "::" }
const stub = listener({
  protocol: "udp",
  address: "127.0.0.53",
  port: 53,
  pid: 400,
  process: "systemd-resolve",
  reach: "loopback",
  network: "loopback",
  exposed: false,
})
const stub2 = { ...stub, address: "127.0.0.54" }
const caddy = listener({
  address: "100.110.34.31",
  port: 8443,
  pid: 2066,
  process: "caddy",
  reach: "network",
  network: "tailnet",
  interface: "tailscale0",
})
const redis = listener({
  address: "203.0.113.5",
  port: 6379,
  pid: 3200,
  process: "redis-server",
  reach: "public",
  network: "public",
  service: "Redis",
  danger: "Redis should not face the internet.",
  level: "critical",
  clients: { count: 2, peers: [{ address: "198.51.100.7", count: 2 }] },
})
const exporter = listener({
  address: "10.0.0.1",
  port: 9100,
  pid: 5120,
  process: "node_exporter",
  reach: "network",
  network: "docker",
  interface: "docker0",
})

describe("a port's page key", () => {
  const all = foldDualStack([sshd, sshd6, stub, stub2, caddy])

  test("is the protocol and port where one row holds them", () => {
    expect(portKey(all[0], all)).toBe("tcp-22")
    expect(portHref(all[0], all)).toBe("/proxy/ports/tcp-22")
    expect(portHref(all[0], all, "clients")).toBe("/proxy/ports/tcp-22?tab=clients")
    expect(portHref(all[0], all, "reach")).toBe("/proxy/ports/tcp-22")
  })

  test("carries the address where two rows share the protocol and port", () => {
    const [, first, second] = all
    expect(portKey(first, all)).toBe("udp-53-127.0.0.53")
    expect(portKey(second, all)).toBe("udp-53-127.0.0.54")
  })

  test("parses back, and refuses what is not a port", () => {
    expect(parsePortKey("tcp-443")).toEqual({ protocol: "tcp", port: 443, address: undefined })
    expect(parsePortKey("udp-53-127.0.0.53")).toEqual({
      protocol: "udp",
      port: 53,
      address: "127.0.0.53",
    })
    expect(parsePortKey("tcp-22-::")).toEqual({ protocol: "tcp", port: 22, address: "::" })
    expect(parsePortKey("sctp-1")).toBeUndefined()
    expect(parsePortKey("tcp-70000")).toBeUndefined()
    expect(parsePortKey("443")).toBeUndefined()
  })

  test("finds its row by either address of a folded pair, and the port alone where one row holds it", () => {
    expect(findPort(all, parsePortKey("tcp-22"))?.process).toBe("sshd")
    expect(findPort(all, parsePortKey("tcp-22-::"))).toBe(all[0])
    expect(findPort(all, parsePortKey("udp-53-127.0.0.54"))).toBe(all[2])
    // A link written while a second row shared the port still opens it.
    expect(findPort(all, parsePortKey("tcp-8443-100.64.0.9"))?.process).toBe("caddy")
    // Two rows, and an address neither has: no guess.
    expect(findPort(all, parsePortKey("udp-53-127.0.0.99"))).toBeUndefined()
    expect(findPort(all, parsePortKey("udp-53"))).toBe(all[1])
    expect(findPort(all, parsePortKey("tcp-9999"))).toBeUndefined()
  })
})

describe("the side sheet's old address", () => {
  test("reads the page's own spelling and the host diagnostic's", () => {
    expect(parseSocketParam("tcp:0.0.0.0:5432")).toEqual({
      protocol: "tcp",
      address: "0.0.0.0",
      port: 5432,
    })
    expect(parseSocketParam("tcp::::22")).toEqual({ protocol: "tcp", address: "::", port: 22 })
    expect(parseSocketParam("tcp-ipv4-0.0.0.0-22")).toEqual({
      protocol: "tcp",
      address: "0.0.0.0",
      port: 22,
    })
    expect(parseSocketParam("udp-ipv6-fe80::1%eth0-546")).toEqual({
      protocol: "udp",
      address: "fe80::1%eth0",
      port: 546,
    })
    expect(parseSocketParam("nonsense")).toBeUndefined()
    expect(parseSocketParam("icmp:0.0.0.0:1")).toBeUndefined()
  })

  test("opens the page it names", () => {
    expect(keyHref(parseSocketParam("tcp:0.0.0.0:5432"))).toBe("/proxy/ports/tcp-5432-0.0.0.0")
    expect(keyHref(parseSocketParam("tcp::::22"))).toBe("/proxy/ports/tcp-22-%3A%3A")
  })
})

describe("the picture of who reaches what", () => {
  test("puts each socket on the widest network that reaches it", () => {
    expect(reachLane(sshd)).toBe("internet")
    expect(reachLane(redis)).toBe("internet")
    expect(reachLane(caddy)).toBe("tailnet")
    expect(reachLane(exporter)).toBe("docker")
    expect(reachLane(stub)).toBe("local")
    expect(reachLane({ reach: "network", network: "uplink" })).toBe("private")
  })

  test("wires each network to the programs on it, the worst first and red where critical", () => {
    const model = reachPicture(foldDualStack([sshd, sshd6, stub, caddy, redis, exporter]))
    expect(model.lanes.map((l) => l.lane)).toEqual(["internet", "tailnet", "docker", "local"])
    expect(model.lanes[0]).toEqual({ lane: "internet", sockets: 2, tone: "danger" })
    expect(model.owners[0].title).toBe("redis-server")
    expect(model.owners[0].tone).toBe("danger")
    const wire = model.wires.find((w) => w.owner === model.owners[0].key)
    expect(wire).toMatchObject({ lane: "internet", tone: "danger", busy: true })
    // Nobody is connected to sshd here, so its wire is still.
    const quiet = model.wires.find(
      (w) => w.owner === model.owners.find((o) => o.title === "sshd").key,
    )
    expect(quiet.busy).toBe(false)
    expect(model.hidden).toBe(0)
  })

  test("counts the programs past its room rather than drawing them", () => {
    const many = Array.from({ length: 11 }, (_, i) =>
      listener({ port: 9000 + i, pid: 100 + i, process: `app${i}` }),
    )
    const model = reachPicture(many, 8)
    expect(model.owners).toHaveLength(8)
    expect(model.hidden).toBe(3)
    expect(model.wires).toHaveLength(8)
  })
})
