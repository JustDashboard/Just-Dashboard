import { describe, expect, test } from "bun:test"
import { portFindings } from "./findings/ports"
import { dangerousPorts, exposedHint, reachVerdict } from "./ports"

const listener = (overrides) => ({
  protocol: "tcp",
  address: "0.0.0.0",
  port: 443,
  pid: 812,
  process: "nginx",
  scope: "all",
  reach: "all",
  exposed: true,
  ...overrides,
})

// Sockets as this host lists them: sshd on every interface, caddy on the
// tailnet address, the DNS stub on loopback.
const sshd = listener({ port: 22, process: "sshd" })
const caddy = listener({
  address: "100.110.34.31",
  port: 8443,
  process: "caddy",
  scope: "interface",
  reach: "network",
})
const resolved = listener({
  protocol: "udp",
  address: "127.0.0.53",
  port: 53,
  process: "systemd-resolve",
  scope: "loopback",
  reach: "loopback",
  exposed: false,
})

// Redis for the containers on two Docker bridges, and MongoDB on docker0's
// link-local address: two databases on three sockets.
const redisBridge = listener({
  address: "10.0.0.1",
  port: 6379,
  process: "redis-server",
  scope: "interface",
  reach: "host",
})
const redisOtherBridge = { ...redisBridge, address: "10.0.2.1" }
const mongoLinkLocal = listener({
  address: "fe80::b482:4dff:fe92:4281",
  port: 27017,
  process: "mongod",
  scope: "interface",
  reach: "host",
})

describe("where a socket answers", () => {
  test("the Exposed tile says which kind of exposure it counts", () => {
    expect(exposedHint([])).toBe("nothing off the machine")
    expect(exposedHint([resolved])).toBe("nothing off the machine")
    expect(exposedHint([sshd, resolved])).toBe("bound to every interface")
    expect(exposedHint([caddy])).toBe("bound to one address")
    expect(exposedHint([caddy, listener({ scope: "interface", address: "10.0.0.1" })])).toBe(
      "each bound to one address",
    )
    expect(exposedHint([sshd, caddy, resolved])).toBe("1 on all · 1 on one IP each")
    // Half a 390px phone holds about 30 characters of hint before it
    // truncates, and the split is the part that would be cut.
    const many = [
      ...Array.from({ length: 19 }, () => sshd),
      ...Array.from({ length: 18 }, () => caddy),
    ]
    expect(exposedHint(many)).toBe("19 on all · 18 on one IP each")
    expect(exposedHint(many).length).toBeLessThanOrEqual(30)
  })

  test("a socket is coloured by who can connect, as the posture grades it", () => {
    const redisOn = (reach, scope = "interface") =>
      reachVerdict(listener({ port: 6379, scope, reach, address: "10.0.0.1" }))
    expect(redisOn("all", "all")).toBe("critical")
    expect(redisOn("public")).toBe("critical")
    expect(redisOn("network")).toBe("warning")
    expect(redisOn("host")).toBe("warning")
    expect(reachVerdict(sshd)).toBe("warning")
    expect(reachVerdict(caddy)).toBe("warning")
    expect(reachVerdict(resolved)).toBeUndefined()
  })
})

describe("databases off this machine are counted per port", () => {
  test("one database bound to two addresses is one", () => {
    const ports = dangerousPorts([redisBridge, redisOtherBridge, mongoLinkLocal, sshd, resolved])
    expect(ports.map((p) => `${p.service} ${p.sockets.length}`)).toEqual(["Redis 2", "MongoDB 1"])
    expect(ports.some((p) => p.internet)).toBe(false)
  })

  test("a dual-stack bind is one database, and faces the internet", () => {
    const ports = dangerousPorts([
      listener({ port: 5432, process: "postgres" }),
      listener({ port: 5432, process: "postgres", address: "::" }),
      redisBridge,
    ])
    expect(ports).toHaveLength(2)
    expect(ports[0]).toMatchObject({ service: "PostgreSQL", internet: true })
    expect(ports[1]).toMatchObject({ service: "Redis", internet: false })
  })

  test("a TCP and a UDP socket on one number are two ports", () => {
    expect(
      dangerousPorts([listener({ port: 11211 }), listener({ port: 11211, protocol: "udp" })]),
    ).toHaveLength(2)
  })
})

describe("a database off this machine", () => {
  test("on a public address is named at that address, not 'every interface'", () => {
    const [finding] = portFindings({
      ports: [
        listener({
          address: "203.0.113.5",
          port: 6379,
          process: "redis-server",
          scope: "interface",
        }),
      ],
    })
    expect(finding.title).toBe("Redis answers on 203.0.113.5")
    expect(finding.detail).toBe("6379/tcp redis-server on 203.0.113.5")
    expect(finding.advice).toStartWith(
      "Bind these to 127.0.0.1 unless something on the same network",
    )
  })

  test("several, some on one address, answer off this machine", () => {
    const [finding] = portFindings({
      ports: [
        listener({ port: 5432, process: "postgres" }),
        listener({
          address: "100.64.1.2",
          port: 6379,
          process: "redis-server",
          scope: "interface",
        }),
      ],
    })
    expect(finding.title).toBe("2 database or control ports answer off this machine")
    expect(finding.detail).toBe("5432/tcp postgres, 6379/tcp redis-server on 100.64.1.2")
  })

  test("on every interface reads as it always did", () => {
    const [finding] = portFindings({ ports: [listener({ port: 5432, process: "postgres" })] })
    expect(finding.title).toBe("PostgreSQL answers on every interface")
    expect(finding.detail).toBe("5432/tcp postgres")
    expect(finding.advice).toStartWith("Bind these to loopback or a private address")
  })

  test("two databases on three sockets are two, each named once with every address", () => {
    const [finding] = portFindings({ ports: [redisBridge, redisOtherBridge, mongoLinkLocal] })
    expect(finding.title).toBe("2 database or control ports answer off this machine")
    expect(finding.detail).toBe(
      "6379/tcp redis-server on 10.0.0.1, 10.0.2.1, 27017/tcp mongod on fe80::b482:4dff:fe92:4281",
    )
  })

  test("one database on two addresses is named once, at both", () => {
    const [finding] = portFindings({ ports: [redisBridge, redisOtherBridge] })
    expect(finding.title).toBe("Redis answers on 2 addresses")
    expect(finding.detail).toBe("6379/tcp redis-server on 10.0.0.1, 10.0.2.1")
  })

  test("a dual-stack database is one, on every interface", () => {
    const [finding] = portFindings({
      ports: [
        listener({ port: 5432, process: "postgres" }),
        listener({ port: 5432, process: "postgres", address: "::" }),
      ],
    })
    expect(finding.title).toBe("PostgreSQL answers on every interface")
    expect(finding.detail).toBe("5432/tcp postgres")
  })

  test("on loopback is not a finding", () => {
    expect(
      portFindings({
        ports: [listener({ address: "127.0.0.1", port: 5432, scope: "loopback", exposed: false })],
      }),
    ).toEqual([])
  })
})
